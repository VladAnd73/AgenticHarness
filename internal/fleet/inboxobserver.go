package fleet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/versality/spore/internal/statefile"
	"github.com/versality/spore/internal/task"
)

const inboxObserverPidName = "inbox-observer.pid"

// promptGlyphs are the characters an agent TUI uses to open its input
// line. paneHasDraft looks for the last line starting with one of them
// and treats whatever follows as the operator's unsent text.
var promptGlyphs = []string{"❯", ">"}

func observerPidPath(projectRoot string) (string, error) {
	project, err := task.ProjectName(projectRoot)
	if err != nil {
		return "", err
	}
	return statefile.Path(project, inboxObserverPidName)
}

// observerAlive reports whether the observer this reconciler last
// spawned for projectRoot is still running. It deliberately knows
// nothing about the agent's own Stop-hook watchers: those are children
// of the agent process, the reconciler cannot manage them, and stale
// ones outlive their usefulness. Tracking only our own pid keeps the
// re-arm to at most one extra process per project.
func observerAlive(projectRoot string) bool {
	p, err := observerPidPath(projectRoot)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	// A recycled pid belonging to something else must not count as an
	// armed observer.
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	return strings.Contains(strings.ReplaceAll(string(cmdline), "\x00", " "), "watch-inbox")
}

// spawnObserver starts a detached inbox observer for projectRoot. The
// observer watches the coordinator's inbox dirs without claiming
// anything and nudges the pane when a NEW envelope lands, which closes
// the hole the 2026-10-06 incident opened: after the Stop-hook watchers
// all exited on one arrival, nothing was watching and later envelopes
// piled up unnoticed.
//
// It ignores whatever is already in the inbox at startup, so the
// envelope the watchdog just requeued does not trigger an immediate
// nudge. Re-arming is not allowed to touch the pane; only the
// escalation at tick N is.
func spawnObserver(projectRoot string, dirs []string) error {
	if len(dirs) == 0 {
		return fmt.Errorf("no inbox dirs")
	}
	self, err := sporeBinary()
	if err != nil {
		return err
	}
	session := CoordinatorSessionName(projectRoot)
	cmd := exec.Command(self, "hooks", "watch-inbox", "--observe", "--wake-session", session)
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(),
		"SPORE_TASK_SLUG="+CoordinatorSlug,
		"SPORE_PROJECT_ROOT="+projectRoot,
		"SPORE_TASK_INBOX="+dirs[0],
	)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	p, perr := observerPidPath(projectRoot)
	if perr != nil {
		return perr
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// sporeBinary resolves the spore executable to re-exec as an observer.
// The reconciler is normally the spore binary itself, and re-execing
// the same build keeps the observer's inbox format in step with the
// drain that produced the record. When the running executable is
// something else (a test harness, a wrapper) that reasoning does not
// hold, so fall back to whatever spore is on PATH.
func sporeBinary() (string, error) {
	if self, err := os.Executable(); err == nil && filepath.Base(self) == "spore" {
		return self, nil
	}
	return exec.LookPath("spore")
}

// paneHasDraft reports whether the session's agent pane holds unsent
// operator text. The second return is false when the pane could not be
// read or no input line could be located: callers must treat that as
// "assume a draft", because typing over one destroys operator intent
// and nothing else the watchdog does is destructive.
func paneHasDraft(session string) (hasDraft bool, known bool) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", session).Output()
	if err != nil {
		return false, false
	}
	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		rest, ok := afterPromptGlyph(lines[i])
		if !ok {
			continue
		}
		return strings.TrimSpace(strings.ReplaceAll(rest, " ", " ")) != "", true
	}
	return false, false
}

// afterPromptGlyph returns the text following the input-line glyph, and
// whether line was an input line at all. Box-drawing borders around the
// input are stripped first.
func afterPromptGlyph(line string) (string, bool) {
	s := strings.Trim(line, " │")
	for _, g := range promptGlyphs {
		if strings.HasPrefix(s, g) {
			return strings.TrimSuffix(strings.TrimPrefix(s, g), "│"), true
		}
	}
	return "", false
}

// tmuxSendKeys types text into session's pane and submits it. The text
// is sent with -l so no part of it is read as a key name.
func tmuxSendKeys(session, text string) error {
	if out, err := exec.Command("tmux", "send-keys", "-t", session, "-l", text).CombinedOutput(); err != nil {
		return fmt.Errorf("send-keys: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("tmux", "send-keys", "-t", session, "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("send-keys Enter: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// NudgeSession types the watchdog nudge into session's pane, but only
// when the pane is readable and provably holds no unsent operator
// draft. An unsent draft is operator intent; typing on top of it would
// submit something they never finished. Suppression is reported as a
// nil error because nothing failed: the nudge was simply not safe.
func NudgeSession(session string) error {
	hasDraft, known := paneHasDraft(session)
	if !known || hasDraft {
		fmt.Fprintf(os.Stderr,
			"inbox-observer: nudge suppressed for %s (draft=%v readable=%v)\n",
			session, hasDraft, known)
		return nil
	}
	return tmuxSendKeys(session, inboxWatchdogNudge)
}
