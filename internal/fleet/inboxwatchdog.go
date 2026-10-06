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
// before the watchdog will consider acting. It covers the window
// between a watcher claiming an envelope and the agent writing the
// first line of the turn that claim triggered.
const inboxWatchdogGrace = 30 * time.Second

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
// consecutive ticks that found a lost delivery; Escalated latches the
// one-shot nudge so a persistent fault does not type repeatedly.
type inboxWatchdogState struct {
	Strikes   int  `json:"strikes"`
	Escalated bool `json:"escalated"`
}

// inboxWatchdogDeps is every edge the watchdog touches outside its own
// state file, so a test can observe them instead of performing them.
type inboxWatchdogDeps struct {
	now           func() time.Time
	inboxDirs     func(projectRoot string) ([]string, error)
	lastActivity  func(projectRoot string) (time.Time, bool)
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
		observerAlive: observerAlive,
		spawnObserver: spawnObserver,
		paneDraft:     paneHasDraft,
		sendKeys:      tmuxSendKeys,
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

	pending, claimedAt := oldestPending(dirs)
	if len(pending) == 0 {
		return InboxWatchdogResult{Skipped: "no outstanding delivery"}, clearInboxWatchdogState(cfg.ProjectRoot)
	}

	now := deps.now()
	if now.Sub(claimedAt) < inboxWatchdogGrace {
		return InboxWatchdogResult{Skipped: "within grace window"}, nil
	}

	activity, ok := deps.lastActivity(cfg.ProjectRoot)
	if !ok {
		return InboxWatchdogResult{Skipped: "no liveness signal"}, nil
	}
	if activity.After(claimedAt) {
		return InboxWatchdogResult{Skipped: "session active since the claim"}, nil
	}

	st, err := readInboxWatchdogState(cfg.ProjectRoot)
	if err != nil {
		return InboxWatchdogResult{}, err
	}
	st.Strikes++
	res := InboxWatchdogResult{Strikes: st.Strikes}

	for _, dir := range pending {
		n, err := hooks.RequeuePending(dir)
		if err != nil {
			return res, fmt.Errorf("requeue %s: %w", dir, err)
		}
		res.Requeued += n
	}
	deps.logf("inbox-watchdog: requeued %d envelope(s) for %s (strike %d/%d)",
		res.Requeued, cfg.ProjectRoot, st.Strikes, escalateAfter)

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

// oldestPending returns the dirs holding an un-acknowledged record and
// the earliest claim time among them: the delivery that has been
// waiting longest is the one the grace and liveness checks apply to.
func oldestPending(dirs []string) ([]string, time.Time) {
	var out []string
	var oldest time.Time
	for _, dir := range dirs {
		rec, ok, err := hooks.ReadPendingAck(dir)
		if err != nil || !ok || len(rec.Messages) == 0 {
			continue
		}
		out = append(out, dir)
		if oldest.IsZero() || rec.ClaimedAt.Before(oldest) {
			oldest = rec.ClaimedAt
		}
	}
	return out, oldest
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
	home, err := os.UserHomeDir()
	if err != nil {
		return time.Time{}, false
	}
	dir := filepath.Join(home, ".claude", "projects", strings.ReplaceAll(projectRoot, "/", "-"))
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
