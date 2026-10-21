package hooks

import (
	"bytes"
	"errors"
	"path/filepath"
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

func TestWatcherPidFileIsPerSession(t *testing.T) {
	for _, tc := range []struct{ session, want string }{
		{"0b6c1e2a-77f0-4c55-9d36-1c0f3c2b9a10", ".watcher.0b6c1e2a-77f0-4c55-9d36-1c0f3c2b9a10.pid"},
		{"", ".watcher.pid"},
		{"../../etc", ".watcher.pid"},
	} {
		if got := watcherPidName(tc.session); got != tc.want {
			t.Errorf("watcherPidName(%q) = %q, want %q", tc.session, got, tc.want)
		}
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
