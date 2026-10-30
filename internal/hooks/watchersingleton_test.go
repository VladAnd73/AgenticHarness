package hooks

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A watcher that already claimed a batch must finish waking the agent
// even when a newer watcher signals it, or the batch is acked unseen.
func TestHandoverGateKeepsAClaimedBatch(t *testing.T) {
	var g handoverGate
	if _, err := g.claim(func() (int, error) { return 1, nil }); err != nil {
		t.Fatal(err)
	}
	if g.release() {
		t.Fatal("release allowed a quiet exit after a batch was claimed")
	}
}

// Once a watcher has agreed to exit, it must not start a claim.
func TestHandoverGateBlocksClaimsAfterRelease(t *testing.T) {
	var g handoverGate
	if !g.release() {
		t.Fatal("release refused with nothing claimed")
	}
	ran := make(chan struct{})
	go func() {
		_, _ = g.claim(func() (int, error) { close(ran); return 1, nil })
	}()
	select {
	case <-ran:
		t.Fatal("a claim ran after the watcher agreed to exit")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatcherKeyFallsBackToTheAgentsIdentity(t *testing.T) {
	for _, tc := range []struct{ stdin, envSession, pane, want string }{
		{"0b6c1e2a-77f0-4c55-9d36-1c0f3c2b9a10", "other", "%3", "0b6c1e2a-77f0-4c55-9d36-1c0f3c2b9a10"},
		{"", "env-session", "%3", "env-session"},
		{"", "", "%3", "pane-3"},
		{"../../etc", "", "", ""},
		{"", "", "", ""},
	} {
		t.Setenv("CLAUDE_CODE_SESSION_ID", tc.envSession)
		t.Setenv("TMUX_PANE", tc.pane)
		if got := WatcherKey(tc.stdin); got != tc.want {
			t.Errorf("WatcherKey(%q) with session env %q pane %q = %q, want %q", tc.stdin, tc.envSession, tc.pane, got, tc.want)
		}
	}
}

// ack.go keeps its records out of the inbox top level because inotify
// watches it. A watcher's pid file must stay out of it too.
func TestWatcherPidFileDoesNotWakeOtherWatchers(t *testing.T) {
	inbox := t.TempDir()
	if err := ensureInbox(inbox); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_TIMEOUT", "1")
	w, err := initPlatformWatcher([]string{inbox})
	if err != nil {
		t.Skipf("no inotify: %v", err)
	}
	defer w.Close()

	release := supersedeWatcher(inbox, "session-b")
	defer release()

	if woke, _ := w.Wait(); woke {
		t.Fatal("writing a pid file woke another watcher on the inbox")
	}
}

// A watcher killed hard never removes its pid file. The next watcher on
// the inbox sweeps files whose process is gone.
func TestWatcherSweepsPidFilesOfDeadWatchers(t *testing.T) {
	inbox := t.TempDir()
	if err := ensureInbox(inbox); err != nil {
		t.Fatal(err)
	}
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	record := []byte(strconv.Itoa(dead.Process.Pid) + " 1\n")
	stale := []string{
		filepath.Join(inbox, watcherDir, "gone-session.pid"),
		filepath.Join(inbox, ".watcher.gone-session.pid"),
		filepath.Join(inbox, ".watcher.pid"),
	}
	for _, p := range stale {
		if err := os.WriteFile(p, record, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	release := supersedeWatcher(inbox, "session-b")
	defer release()

	for _, p := range stale {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("stale pid file %s survived", p)
		}
	}
	if _, err := os.Stat(filepath.Join(inbox, watcherDir, "session-b.pid")); err != nil {
		t.Errorf("own pid file missing: %v", err)
	}
}

type seqWaiter struct {
	calls int
	steps []func() bool
}

func (s *seqWaiter) Wait() (bool, error) {
	if s.calls >= len(s.steps) {
		return false, nil
	}
	s.calls++
	return s.steps[s.calls-1](), nil
}

func (s *seqWaiter) Close() error { return nil }

// Another session's watcher writes its pid file into the inbox, which
// fires this watcher's inotify wait with nothing to claim. It must keep
// waiting, not exit and leave the agent with no watcher.
func TestWatchInboxKeepsWaitingAfterAWakeWithNoTell(t *testing.T) {
	_, slug, inbox := setupInbox(t)
	w := &seqWaiter{steps: []func() bool{
		func() bool { return true },
		func() bool {
			writeTell(t, filepath.Join(inbox, "late.json"), `{"ts":"l","source":"s","body":"late"}`)
			return true
		},
	}}
	opts := watchOpts{
		timeout: time.Minute, settle: 0,
		initWatcher: func([]string) (inboxWaiter, error) { return w, nil },
		sleep:       func(time.Duration) {},
	}
	var stdout bytes.Buffer
	err := watchInbox(slug, &stdout, &bytes.Buffer{}, opts)
	if !errors.Is(err, ErrWake) || !strings.Contains(stdout.String(), "[l] s: late") {
		t.Fatalf("err=%v stdout=%q; want the later tell delivered", err, stdout.String())
	}
}
