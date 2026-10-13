package hooks

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Scenario 1: an idle session, an envelope arrives, watch-inbox drains
// it. The text is printed, the exit signal is ErrWake, and a durable
// un-acknowledged record of that batch exists on disk.
func TestWatchInbox_DrainWritesPendingAck(t *testing.T) {
	tmp := t.TempDir()
	inbox := filepath.Join(tmp, "inbox")
	makeInbox(t, inbox)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"2026-10-06T10:06:23Z","source":"slack-watcher","body":"new thread"}`)

	var stdout, stderr bytes.Buffer
	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func([]string) (inboxWaiter, error) {
			t.Fatal("watcher should not init when pre-drain found files")
			return nil, nil
		},
		sleep: func(time.Duration) {},
	}
	err := watchInboxAt(inbox, &stdout, &stderr, opts)
	if !errors.Is(err, ErrWake) {
		t.Fatalf("got err=%v, want ErrWake", err)
	}
	if !strings.Contains(stdout.String(), "[2026-10-06T10:06:23Z] slack-watcher: new thread") {
		t.Fatalf("stdout: %q", stdout.String())
	}

	rec, ok, err := ReadPendingAck(inbox)
	if err != nil {
		t.Fatalf("ReadPendingAck: %v", err)
	}
	if !ok {
		t.Fatal("no un-acknowledged record on disk after drain")
	}
	if len(rec.Messages) != 1 {
		t.Fatalf("rec.Messages = %d, want 1", len(rec.Messages))
	}
	if rec.Messages[0].Body != "new thread" {
		t.Errorf("body = %q, want %q", rec.Messages[0].Body, "new thread")
	}
	if rec.ClaimedAt.IsZero() {
		t.Error("ClaimedAt is zero")
	}
	// The envelope is claimed out of inbox/ but NOT yet consumed into
	// read/: read/ means "delivery acknowledged".
	if _, err := os.Stat(filepath.Join(inbox, "100-1-1.json")); !os.IsNotExist(err) {
		t.Error("envelope should have left the inbox top level")
	}
	if _, err := os.Stat(filepath.Join(inbox, "read", "100-1-1.json")); !os.IsNotExist(err) {
		t.Error("envelope must not reach read/ before the delivery is acknowledged")
	}
}

// Scenario 2: an un-acknowledged record exists; watch-inbox runs again.
// The Stop hook only runs at the end of a turn, so its start proves the
// session woke and completed one. The record is cleared and the
// envelope is promoted into read/.
func TestWatchInbox_NextRunClearsPendingAck(t *testing.T) {
	tmp := t.TempDir()
	inbox := filepath.Join(tmp, "inbox")
	makeInbox(t, inbox)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"s","body":"first"}`)

	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func([]string) (inboxWaiter, error) { return &fakeWaiter{woke: false}, nil },
		sleep:       func(time.Duration) {},
	}
	if err := watchInboxAt(inbox, &bytes.Buffer{}, &bytes.Buffer{}, opts); !errors.Is(err, ErrWake) {
		t.Fatalf("first run: got %v, want ErrWake", err)
	}
	if _, ok, _ := ReadPendingAck(inbox); !ok {
		t.Fatal("first run left no pending record")
	}

	// Second run: nothing new arrives, the waiter times out.
	if err := watchInboxAt(inbox, &bytes.Buffer{}, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("second run: got %v, want nil", err)
	}
	if _, ok, _ := ReadPendingAck(inbox); ok {
		t.Error("pending record survived a second watch-inbox run")
	}
	if _, err := os.Stat(filepath.Join(inbox, "read", "100-1-1.json")); err != nil {
		t.Errorf("acknowledged envelope should be promoted to read/: %v", err)
	}
}

// Scenario 3: the record exists and the session never woke. The hook
// process is gone. The full envelope content is still recoverable from
// disk -- both the body text and the staged file itself.
func TestPendingAck_ContentRecoverableAfterProcessGone(t *testing.T) {
	tmp := t.TempDir()
	inbox := filepath.Join(tmp, "inbox")
	makeInbox(t, inbox)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"slack-watcher","body":"do not lose me"}`)

	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func([]string) (inboxWaiter, error) { return &fakeWaiter{woke: false}, nil },
		sleep:       func(time.Duration) {},
	}
	if err := watchInboxAt(inbox, &bytes.Buffer{}, &bytes.Buffer{}, opts); !errors.Is(err, ErrWake) {
		t.Fatalf("got %v, want ErrWake", err)
	}
	// Process is gone: nothing else runs. Read what is on disk.
	rec, ok, err := ReadPendingAck(inbox)
	if err != nil || !ok {
		t.Fatalf("ReadPendingAck: ok=%v err=%v", ok, err)
	}
	if rec.Messages[0].Body != "do not lose me" {
		t.Errorf("body = %q", rec.Messages[0].Body)
	}
	staged := filepath.Join(inbox, pendingSubdir, "100-1-1.json")
	b, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("staged envelope unreadable: %v", err)
	}
	if !strings.Contains(string(b), "do not lose me") {
		t.Errorf("staged envelope content lost: %q", b)
	}
}

// RequeuePending is the watchdog's repair: the staged envelope goes
// back to the inbox top level and the record is dropped, so the next
// drain re-delivers it.
func TestRequeuePending_RestoresEnvelopeAndClearsRecord(t *testing.T) {
	tmp := t.TempDir()
	inbox := filepath.Join(tmp, "inbox")
	makeInbox(t, inbox)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"s","body":"redeliver"}`)

	opts := watchOpts{
		timeout: time.Second, settle: 0,
		initWatcher: func([]string) (inboxWaiter, error) { return &fakeWaiter{woke: false}, nil },
		sleep:       func(time.Duration) {},
	}
	if err := watchInboxAt(inbox, &bytes.Buffer{}, &bytes.Buffer{}, opts); !errors.Is(err, ErrWake) {
		t.Fatalf("got %v, want ErrWake", err)
	}

	moved, err := RequeuePending(inbox)
	if err != nil {
		t.Fatalf("RequeuePending: %v", err)
	}
	if len(moved) != 1 {
		t.Fatalf("requeued %v, want 1 envelope", moved)
	}
	if _, err := os.Stat(filepath.Join(inbox, "100-1-1.json")); err != nil {
		t.Errorf("envelope not back in inbox/: %v", err)
	}
	if _, ok, _ := ReadPendingAck(inbox); ok {
		t.Error("record survived requeue")
	}
	// Idempotent: a second requeue finds nothing to do.
	if again, err := RequeuePending(inbox); err != nil || len(again) != 0 {
		t.Errorf("second requeue: moved=%v err=%v, want none/nil", again, err)
	}
}

// Belief A, under test. The operator's hypothesis was that the
// 2026-10-06 re-prompt was dropped because the hook had been blocked
// for ~24 hours. The live host shows four concurrent watch-inbox
// children under one claude PID: every turn end spawns another and
// none are reaped. This test pins the mechanism that follows -- one
// arrival wakes all of them, exactly one wins the rename, and all the
// losers exit WITHOUT requesting a wake. If the winner is a watcher
// whose stdout the agent is no longer reading, the message is lost no
// matter how long anything was blocked.
func TestDrainInbox_ConcurrentWatchersExactlyOneClaims(t *testing.T) {
	tmp := t.TempDir()
	inbox := filepath.Join(tmp, "inbox")
	makeInbox(t, inbox)
	writeTell(t, filepath.Join(inbox, "100-1-1.json"),
		`{"ts":"t","source":"s","body":"contested"}`)

	const watchers = 8
	var wg sync.WaitGroup
	results := make([]int, watchers)
	start := make(chan struct{})
	for i := range watchers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			n, err := drainInbox(inbox, &bytes.Buffer{})
			if err != nil {
				t.Errorf("drain %d: %v", i, err)
			}
			results[i] = n
		}(i)
	}
	close(start)
	wg.Wait()

	claims := 0
	for _, n := range results {
		claims += n
	}
	if claims != 1 {
		t.Fatalf("total claims = %d across %d watchers, want exactly 1", claims, watchers)
	}
	// And the one claim left exactly one durable record, so the
	// watchdog can recover regardless of which watcher won.
	rec, ok, err := ReadPendingAck(inbox)
	if err != nil || !ok {
		t.Fatalf("ReadPendingAck: ok=%v err=%v", ok, err)
	}
	if len(rec.Messages) != 1 {
		t.Fatalf("rec.Messages = %d, want 1", len(rec.Messages))
	}
}
