package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/statefile"
	"github.com/versality/spore/internal/task"
)

// DefaultInboxWatchdogEscalateAfter is how many consecutive reconcile
// ticks must find the same coordinator sitting on an un-acknowledged
// delivery before the watchdog escalates to a pane nudge. At the ~60s
// tick of the reconcile timer that is roughly three minutes of silence,
// which is long enough that a slow turn has finished and short enough
// that a lost wakeup does not cost the operator an afternoon.
const DefaultInboxWatchdogEscalateAfter = 3

// inboxWatchdogGrace is the minimum age of an un-acknowledged record
// before the watchdog will consider acting.
//
// It has to clear the longest an agent can go without writing anything
// to its transcript while working normally. Claude Code writes one
// line per message, so a tool call writes nothing for its whole
// duration: measured across 3724 real assistant-to-tool-result
// intervals on this host, the median was 0.13s and the p99 26.2s, but
// the longest was 120.2s and this harness permits Bash timeouts of
// 600s. A grace below that ceiling eventually reads a working agent as
// a dead one, and the watchdog types into its pane.
//
// The cost of the wide window is that a genuinely lost delivery waits
// longer. Against the 17.7 hours the 2026-10-06 incident cost, fifteen
// minutes plus the escalation streak is cheap.
const inboxWatchdogGrace = 15 * time.Minute

const inboxWatchdogStateName = "inbox-watchdog.json"

// inboxWatchdogNudge is the text typed into the coordinator pane on
// escalation. It names the condition rather than the content: the
// envelope itself is back in the inbox and the agent reads it there.
const inboxWatchdogNudge = "spore: an inbox delivery was never acknowledged and has been requeued. Check your inbox."

// InboxWatchdogResult is one watchdog pass. Skipped names why the pass
// did nothing; it is empty when the watchdog acted.
type InboxWatchdogResult struct {
	Skipped   string
	Requeued  int
	Rearmed   bool
	Escalated bool
	Strikes   int
}

// inboxWatchdogState is the per-project streak ledger. Strikes counts
// consecutive ticks that found the same lost delivery; Escalated
// latches the one-shot nudge so a persistent fault does not type
// repeatedly.
//
// Lost names the envelopes the watchdog put back, and it is what the
// streak hangs on. An earlier version counted strikes while an
// un-acknowledged record was present, which could never reach two: the
// requeue deletes the record, and during a lost wakeup nothing
// re-claims to write a new one. The envelope lying at the inbox top
// level across ticks is the durable evidence that the delivery still
// has not landed.
type inboxWatchdogState struct {
	Strikes   int       `json:"strikes"`
	Escalated bool      `json:"escalated"`
	Lost      []string  `json:"lost,omitempty"`
	LostSince time.Time `json:"lost_since,omitempty"`
	MuteSaid  bool      `json:"mute_said,omitempty"`
}

// inboxWatchdogDeps is every edge the watchdog touches outside its own
// state file, so a test can observe them instead of performing them.
type inboxWatchdogDeps struct {
	now           func() time.Time
	inboxDirs     func(projectRoot string) ([]string, error)
	lastActivity  func(projectRoot string) (time.Time, bool)
	claimerAlive  func(pid int) bool
	observerAlive func(projectRoot string) bool
	spawnObserver func(projectRoot string, dirs []string) error
	paneDraft     func(session string) (hasDraft bool, known bool)
	sendKeys      func(session, text string) error
	logf          func(format string, args ...any)
}

func defaultInboxWatchdogDeps() inboxWatchdogDeps {
	return inboxWatchdogDeps{
		now:           time.Now,
		inboxDirs:     coordinatorInboxDirs,
		lastActivity:  agentTranscriptActivity,
		claimerAlive:  watcherProcessAlive,
		observerAlive: observerAlive,
		spawnObserver: spawnObserver,
		paneDraft:     paneHasDraft,
		sendKeys:      tmuxNudge,
		logf:          func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) },
	}
}

// runInboxWatchdog is one pass of the lost-wakeup watchdog.
//
// A delivery is "lost" when an un-acknowledged record has been sitting
// in a coordinator inbox past the grace window AND the agent has
// written nothing since the claim. The second half is what keeps the
// watchdog off a live turn: the record alone cannot tell a dropped
// re-prompt from a re-prompt that landed and started a long turn, but
// the agent's own transcript can, because a delivered re-prompt is
// written there within a second.
//
// When the liveness probe cannot answer, the pass does nothing. Acting
// blind risks interrupting a working coordinator, and the delivery is
// already safe on disk.
func runInboxWatchdog(cfg Config, escalateAfter int, deps inboxWatchdogDeps) (InboxWatchdogResult, error) {
	if escalateAfter < 1 {
		escalateAfter = DefaultInboxWatchdogEscalateAfter
	}

	dirs, err := deps.inboxDirs(cfg.ProjectRoot)
	if err != nil {
		return InboxWatchdogResult{Skipped: "inbox dirs unresolved"}, nil
	}

	st, err := readInboxWatchdogState(cfg.ProjectRoot)
	if err != nil {
		return InboxWatchdogResult{}, err
	}
	outstanding := survivingPaths(st.Lost)
	if len(st.Lost) > 0 && len(outstanding) == 0 {
		// Every envelope the watchdog put back has been taken: the
		// agent woke, so this episode is over and the next one starts
		// its streak from zero.
		st = inboxWatchdogState{}
	}
	st.Lost = outstanding

	pending, claimedAt, claimPID := oldestPending(dirs)
	if len(pending) == 0 && len(st.Lost) == 0 {
		return InboxWatchdogResult{Skipped: "no outstanding delivery"}, clearInboxWatchdogState(cfg.ProjectRoot)
	}

	// The reference time is when the delivery that is still outstanding
	// was first claimed, not when this tick found it.
	since := claimedAt
	if since.IsZero() || (!st.LostSince.IsZero() && st.LostSince.Before(since)) {
		since = st.LostSince
	}

	if claimPID > 0 && deps.claimerAlive(claimPID) {
		return InboxWatchdogResult{Skipped: "claiming watcher still running"}, nil
	}

	now := deps.now()
	if now.Sub(since) < inboxWatchdogGrace {
		return InboxWatchdogResult{Skipped: "within grace window"}, nil
	}

	activity, ok := deps.lastActivity(cfg.ProjectRoot)
	if !ok {
		res := InboxWatchdogResult{Skipped: "no liveness signal"}
		if st.MuteSaid {
			return res, nil
		}
		deps.logf("inbox-watchdog: %s has an outstanding delivery but no liveness signal for its agent, "+
			"so the watchdog stands down and will not recover it", cfg.ProjectRoot)
		st.MuteSaid = true
		return res, writeInboxWatchdogState(cfg.ProjectRoot, st)
	}
	if activity.After(since) {
		return InboxWatchdogResult{Skipped: "session active since the claim"}, nil
	}

	st.MuteSaid = false
	st.Strikes++
	if st.LostSince.IsZero() {
		st.LostSince = since
	}
	res := InboxWatchdogResult{Strikes: st.Strikes}

	for _, dir := range pending {
		names, err := hooks.RequeuePending(dir)
		for _, n := range names {
			st.Lost = append(st.Lost, filepath.Join(dir, n))
		}
		res.Requeued += len(names)
		if err != nil {
			return res, fmt.Errorf("requeue %s: %w", dir, err)
		}
	}
	if res.Requeued > 0 {
		deps.logf("inbox-watchdog: requeued %d envelope(s) for %s (strike %d/%d)",
			res.Requeued, cfg.ProjectRoot, st.Strikes, escalateAfter)
	} else {
		deps.logf("inbox-watchdog: %d envelope(s) still undelivered for %s (strike %d/%d)",
			len(st.Lost), cfg.ProjectRoot, st.Strikes, escalateAfter)
	}

	if !deps.observerAlive(cfg.ProjectRoot) {
		if err := deps.spawnObserver(cfg.ProjectRoot, dirs); err != nil {
			deps.logf("inbox-watchdog: observer spawn failed: %v", err)
		} else {
			res.Rearmed = true
		}
	}

	if st.Strikes >= escalateAfter && !st.Escalated {
		session := CoordinatorSessionName(cfg.ProjectRoot)
		hasDraft, known := deps.paneDraft(session)
		switch {
		case !known:
			deps.logf("inbox-watchdog: nudge suppressed for %s: pane state unreadable, assuming an operator draft", session)
		case hasDraft:
			deps.logf("inbox-watchdog: nudge suppressed for %s: pane holds an unsent operator draft", session)
		default:
			if err := deps.sendKeys(session, inboxWatchdogNudge); err != nil {
				deps.logf("inbox-watchdog: nudge failed for %s: %v", session, err)
			} else {
				res.Escalated = true
				st.Escalated = true
				deps.logf("inbox-watchdog: escalated to a pane nudge for %s after %d consecutive ticks",
					session, st.Strikes)
			}
		}
	}

	return res, writeInboxWatchdogState(cfg.ProjectRoot, st)
}

// oldestPending returns the dirs holding an un-acknowledged record,
// the earliest claim time among them, and the pid that made that
// claim. The delivery that has been waiting longest is the one the
// grace and liveness checks apply to.
func oldestPending(dirs []string) ([]string, time.Time, int) {
	var out []string
	var oldest time.Time
	pid := 0
	for _, dir := range dirs {
		rec, ok, err := hooks.ReadPendingAck(dir)
		if err != nil || !ok || len(rec.Messages) == 0 {
			continue
		}
		out = append(out, dir)
		if oldest.IsZero() || rec.ClaimedAt.Before(oldest) {
			oldest = rec.ClaimedAt
			pid = rec.PID
		}
	}
	return out, oldest, pid
}

// survivingPaths keeps the entries that are still on disk. A requeued
// envelope that has gone has been taken by a drain, which is the
// delivery finally happening.
func survivingPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// coordinatorInboxDirs returns the dirs a coordinator's Stop-hook
// watcher watches: the poke channel and the message inbox. Mirrors
// watchInboxDirsFromEnv in cmd/spore, resolved from projectRoot
// instead of the session env.
func coordinatorInboxDirs(projectRoot string) ([]string, error) {
	poke, err := task.CoordinatorInboxDirForProject(projectRoot)
	if err != nil {
		return nil, err
	}
	msg, err := task.InboxDirForProject(projectRoot, CoordinatorSlug)
	if err != nil {
		return nil, err
	}
	if msg == poke {
		return []string{poke}, nil
	}
	return []string{poke, msg}, nil
}

// agentTranscriptActivity reports when the coordinator's agent last
// wrote to its session transcript. Claude Code appends a line per
// message under ~/.claude/projects/<cwd-with-slashes-as-dashes>/, so a
// re-prompt that actually landed shows up there within a second while a
// dropped one leaves the file untouched.
//
// The second return is false when no transcript tree exists, which is
// the case for any agent that does not keep one. The watchdog treats
// that as "cannot tell" and stands down.
func agentTranscriptActivity(projectRoot string) (time.Time, bool) {
	base, err := claudeConfigDir()
	if err != nil {
		return time.Time{}, false
	}
	dir := filepath.Join(base, "projects", claudeProjectDir(projectRoot))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	if newest.IsZero() {
		return time.Time{}, false
	}
	return newest, true
}

// claudeConfigDir is where Claude Code keeps its state. A project can
// be pointed at its own by setting CLAUDE_CONFIG_DIR, and a watchdog
// that assumed ~/.claude would find no transcripts for that project
// and stand down forever.
func claudeConfigDir() (string, error) {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// claudeProjectDir encodes a project root the way Claude Code names
// its transcript directory under <config>/projects/: every character
// that is not a letter, digit or hyphen becomes a hyphen. Verified
// against the directory names on this host and the cwd recorded inside
// their own transcripts, including dotted roots (every .worktrees
// checkout) and underscored ones.
func claudeProjectDir(projectRoot string) string {
	var b strings.Builder
	b.Grow(len(projectRoot))
	for _, r := range projectRoot {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func inboxWatchdogStatePath(projectRoot string) (string, error) {
	project, err := task.ProjectName(projectRoot)
	if err != nil {
		return "", err
	}
	return statefile.Path(project, inboxWatchdogStateName)
}

func readInboxWatchdogState(projectRoot string) (inboxWatchdogState, error) {
	p, err := inboxWatchdogStatePath(projectRoot)
	if err != nil {
		return inboxWatchdogState{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return inboxWatchdogState{}, nil
	}
	var st inboxWatchdogState
	if err := json.Unmarshal(b, &st); err != nil {
		return inboxWatchdogState{}, nil
	}
	return st, nil
}

func writeInboxWatchdogState(projectRoot string, st inboxWatchdogState) error {
	p, err := inboxWatchdogStatePath(projectRoot)
	if err != nil {
		return err
	}
	return statefile.WriteJSONAtomic(p, "inbox-watchdog", st)
}

func clearInboxWatchdogState(projectRoot string) error {
	p, err := inboxWatchdogStatePath(projectRoot)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
