package hooks

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// drainOnce runs one real watcher pass over inbox with a waiter that
// times out immediately, so the pre-drain claims whatever is there.
func drainOnce(t *testing.T, inbox string) {
	t.Helper()
	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func([]string) (inboxWaiter, error) { return &fakeWaiter{woke: false}, nil },
		sleep:       func(time.Duration) {},
	}
	if err := watchInboxAt(inbox, &bytes.Buffer{}, &bytes.Buffer{}, opts); !errors.Is(err, ErrWake) {
		t.Fatalf("drain: got %v, want ErrWake", err)
	}
}

// Finding 4. movePending's comment promises it skips a name that
// already exists at the destination; os.Rename silently replaced it, so
// a second envelope that happened to reuse a staged name was destroyed
// with no error and no log. Nothing an inbox holds may be dropped
// without a trace.
func TestMovePending_NeverDestroysAnEnvelopeOnANameCollision(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	makeInbox(t, inbox)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"s","body":"claimed body"}`)
	drainOnce(t, inbox)

	// A later envelope lands at the top level under the same name.
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"s","body":"arrived later"}`)

	if _, err := RequeuePending(inbox); err != nil {
		t.Fatalf("RequeuePending: %v", err)
	}

	bodies := map[string]bool{}
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(inbox, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"claimed body", "arrived later"} {
			if strings.Contains(string(b), want) {
				bodies[want] = true
			}
		}
	}
	if !bodies["claimed body"] || !bodies["arrived later"] {
		t.Fatalf("an envelope was destroyed by the requeue: survivors = %v", bodies)
	}
}

// Finding 4, second half. A staged envelope that cannot be moved must
// keep its record: the record is the only thing that tells the watchdog
// the envelope exists, so dropping it strands the envelope in .pending
// forever.
func TestRequeuePending_KeepsTheRecordWhenAnEnvelopeCannotMove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	inbox := filepath.Join(t.TempDir(), "inbox")
	makeInbox(t, inbox)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"s","body":"stuck"}`)
	drainOnce(t, inbox)

	pending := filepath.Join(inbox, pendingSubdir)
	if err := os.Chmod(pending, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(pending, 0o755) })

	moved, err := RequeuePending(inbox)
	if err != nil {
		t.Fatalf("RequeuePending: %v", err)
	}
	if len(moved) != 0 {
		t.Fatalf("moved = %v, want none (the rename cannot succeed)", moved)
	}
	if _, ok, _ := ReadPendingAck(inbox); !ok {
		t.Fatal("record dropped while the envelope is still staged: it is now invisible to the watchdog")
	}
}

// Finding 9. The un-acknowledged record and its temp file used to be
// created inside the directory inotify watches, so every claim fired
// two spurious wake events at every other watcher on that inbox. The
// top level must hold envelopes and subdirectories, nothing else.
func TestPendingAck_StaysOutOfTheWatchedDirectory(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	makeInbox(t, inbox)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"s","body":"quiet please"}`)
	drainOnce(t, inbox)

	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("claim left %q in the watched directory", e.Name())
		}
	}
	rec, ok, err := ReadPendingAck(inbox)
	if err != nil || !ok {
		t.Fatalf("record unreadable after the move: ok=%v err=%v", ok, err)
	}
	if rec.Messages[0].Body != "quiet please" {
		t.Errorf("body = %q", rec.Messages[0].Body)
	}
}

// scriptedWaiter drives observeInbox through a fixed sequence of
// arrivals: each Wait runs the next step and reports a wake, and once
// the steps run out it reports a timeout so the caller can return.
type scriptedWaiter struct {
	steps []func()
	n     int
}

func (s *scriptedWaiter) Wait() (bool, error) {
	if s.n >= len(s.steps) {
		return false, nil
	}
	s.steps[s.n]()
	s.n++
	return true, nil
}

func (s *scriptedWaiter) Close() error { return nil }

// Finding 11. The observer existed to cover the window where no
// Stop-hook watcher is armed, but it returned after the first arrival
// and left the inbox unwatched again -- the exact hole it was written
// to close.
func TestObserveInbox_KeepsWatchingAfterTheFirstArrival(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	makeInbox(t, inbox)

	w := &scriptedWaiter{steps: []func(){
		func() { writeTell(t, filepath.Join(inbox, "100-1-1.json"), `{"body":"first"}`) },
		func() { writeTell(t, filepath.Join(inbox, "200-1-1.json"), `{"body":"second"}`) },
	}}
	opts := watchOpts{
		timeout: time.Second, settle: 0, confirm: 0,
		initWatcher: func([]string) (inboxWaiter, error) { return w, nil },
		sleep:       func(time.Duration) {},
	}
	calls := 0
	if err := observeInbox([]string{inbox}, func() error { calls++; return nil }, opts); err != nil {
		t.Fatalf("observeInbox: %v", err)
	}
	if calls != 2 {
		t.Fatalf("onNew called %d time(s) for 2 arrivals", calls)
	}
}

// Finding 11. An arrival a live Stop-hook watcher drains is not a lost
// wakeup, and the observer must not type into the pane for it. The
// confirm window is what separates the two: a watcher claims within a
// second, so an envelope still sitting at the top level after it is
// evidence nothing is watching.
func TestObserveInbox_SkipsTheNudgeWhenAWatcherDrainsTheArrival(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	makeInbox(t, inbox)
	envelope := filepath.Join(inbox, "100-1-1.json")

	w := &scriptedWaiter{steps: []func(){
		func() { writeTell(t, envelope, `{"body":"drained by a live watcher"}`) },
	}}
	sleeps := 0
	opts := watchOpts{
		timeout: time.Second, settle: 0, confirm: time.Second,
		initWatcher: func([]string) (inboxWaiter, error) { return w, nil },
		sleep: func(time.Duration) {
			sleeps++
			// The settle passes with the envelope still there; a real
			// watcher claims it during the confirm window.
			if sleeps == 2 {
				if err := os.Remove(envelope); err != nil {
					t.Error(err)
				}
			}
		},
	}
	calls := 0
	if err := observeInbox([]string{inbox}, func() error { calls++; return nil }, opts); err != nil {
		t.Fatalf("observeInbox: %v", err)
	}
	if calls != 0 {
		t.Fatalf("nudged for an arrival a watcher drained (onNew called %d time(s))", calls)
	}
}
