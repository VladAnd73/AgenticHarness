package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type watcherProc struct {
	cmd  *exec.Cmd
	done chan error
}

func buildSporeBin(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not available: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "spore")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	return bin
}

func watchInboxEnv(inbox string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "SPORE_") || strings.HasPrefix(kv, "WT_") ||
			strings.HasPrefix(kv, "CLAUDE_CODE_SESSION_ID=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "SPORE_TASK_INBOX="+inbox, "SPORE_TASK_SLUG=worker-a", "WATCH_TIMEOUT=60")
}

// startWatcher runs the watcher the way a Stop hook does: the hook input
// JSON, with the Claude session id, arrives on stdin.
func startWatcher(t *testing.T, bin string, env []string, session string) *watcherProc {
	t.Helper()
	return startWatcherWithInput(t, bin, env, `{"hook_event_name":"Stop","session_id":"`+session+`"}`)
}

func startWatcherWithInput(t *testing.T, bin string, env []string, input string) *watcherProc {
	t.Helper()
	cmd := exec.Command(bin, "hooks", "watch-inbox")
	cmd.Env = env
	cmd.Stdin = strings.NewReader(input)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w := &watcherProc{cmd: cmd, done: make(chan error, 1)}
	go func() { w.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	time.Sleep(300 * time.Millisecond)
	return w
}

func (w *watcherProc) running(t *testing.T, within time.Duration) bool {
	t.Helper()
	select {
	case err := <-w.done:
		w.done <- err
		return false
	case <-time.After(within):
		return true
	}
}

// Each Stop hook firing spawns a fresh watcher. Without a singleton the
// earlier ones live on for the full 7-day timeout, one per turn.
func TestWatchInboxStopHookFiringThriceLeavesOneWatcher(t *testing.T) {
	bin := buildSporeBin(t)
	env := watchInboxEnv(filepath.Join(t.TempDir(), "inbox"))

	var ws []*watcherProc
	for i := 0; i < 3; i++ {
		ws = append(ws, startWatcher(t, bin, env, "session-a"))
	}

	for i := 0; i < 2; i++ {
		select {
		case err := <-ws[i].done:
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Errorf("superseded watcher %d exited %d, want 0 so the agent is not woken", i, exitErr.ExitCode())
			}
		case <-time.After(5 * time.Second):
			t.Errorf("watcher %d still running after a newer one started", i)
		}
	}
	if !ws[2].running(t, 500*time.Millisecond) {
		t.Fatalf("newest watcher exited early: %v", <-ws[2].done)
	}
}

// A claude started in another window of the agent's tmux session
// inherits SPORE_TASK_INBOX. Its Stop hook must not stop the agent's
// own watcher, or the agent never wakes on the next tell.
func TestWatchInboxOtherSessionDoesNotStopTheAgentsWatcher(t *testing.T) {
	bin := buildSporeBin(t)
	env := watchInboxEnv(filepath.Join(t.TempDir(), "inbox"))

	agent := startWatcher(t, bin, env, "agent-session")
	startWatcher(t, bin, env, "scratch-session")

	if !agent.running(t, time.Second) {
		t.Fatalf("the agent's watcher was stopped by another session's Stop hook: %v", <-agent.done)
	}
}

// A pid file left behind by a killed watcher can name a pid that now
// belongs to some other session's watcher. That watcher must survive.
func TestWatchInboxIgnoresAStalePidFile(t *testing.T) {
	bin := buildSporeBin(t)
	inbox := filepath.Join(t.TempDir(), "inbox")
	other := startWatcher(t, bin, watchInboxEnv(filepath.Join(t.TempDir(), "other")), "other-session")

	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := strconv.Itoa(other.cmd.Process.Pid) + "\n"
	for _, name := range []string{".watcher.pid", ".watcher.agent-session.pid"} {
		if err := os.WriteFile(filepath.Join(inbox, name), []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	startWatcher(t, bin, watchInboxEnv(inbox), "agent-session")

	if !other.running(t, time.Second) {
		t.Fatalf("a stale pid file got an unrelated watcher killed: %v", <-other.done)
	}
}

// Codex, a slow stdin and older hook wiring give no session_id. Two such
// sessions on one inbox used to share one pid file and stop each
// other's watcher.
func TestWatchInboxSessionsWithoutSessionIDKeepTheirWatchers(t *testing.T) {
	bin := buildSporeBin(t)
	for _, tc := range []struct{ name, a, b string }{
		{"two panes", "TMUX_PANE=%1", "TMUX_PANE=%2"},
		{"no identity at all", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := watchInboxEnv(filepath.Join(t.TempDir(), "inbox"))
			first := startWatcherWithInput(t, bin, append(env, tc.a), "")
			second := startWatcherWithInput(t, bin, append(env, tc.b), "")
			if !first.running(t, time.Second) {
				t.Fatalf("the first session's watcher was stopped: %v", <-first.done)
			}
			if !second.running(t, 100*time.Millisecond) {
				t.Fatalf("the second session's watcher exited: %v", <-second.done)
			}
		})
	}
}

// A watcher stopped by claude-code (SIGTERM) must not leave its pid
// file behind; one per session piled up in every inbox.
func TestWatchInboxRemovesItsPidFileOnSIGTERM(t *testing.T) {
	bin := buildSporeBin(t)
	inbox := filepath.Join(t.TempDir(), "inbox")
	w := startWatcher(t, bin, watchInboxEnv(inbox), "session-x")
	pidFiles := func() []string {
		a, _ := filepath.Glob(filepath.Join(inbox, ".watcher*.pid"))
		b, _ := filepath.Glob(filepath.Join(inbox, ".watchers", "*"))
		return append(a, b...)
	}
	if len(pidFiles()) == 0 {
		t.Fatal("the running watcher wrote no pid file")
	}
	if err := w.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher ignored SIGTERM")
	}
	if left := pidFiles(); len(left) > 0 {
		t.Fatalf("pid files left after exit: %v", left)
	}
}
