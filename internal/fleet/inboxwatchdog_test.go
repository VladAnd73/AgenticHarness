package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/hooks"
)

// watchdogHarness builds a project whose coordinator inbox holds one
// drained-but-unacknowledged envelope, plus a deps set whose every
// side-effecting edge is recorded instead of performed.
type watchdogHarness struct {
	root      string
	inbox     string
	deps      inboxWatchdogDeps
	spawned   int
	nudges    []string
	logs      []string
	observer  bool
	activity  time.Time
	activeOK  bool
	draft     bool
	draftOK   bool
	claimedAt time.Time
}

func newWatchdogHarness(t *testing.T) *watchdogHarness {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))

	inbox := filepath.Join(root, "state", "inbox")
	if err := os.MkdirAll(filepath.Join(inbox, "read"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := &watchdogHarness{root: root, inbox: inbox, draftOK: true}
	h.deps = inboxWatchdogDeps{
		now:          func() time.Time { return h.claimedAt.Add(10 * time.Minute) },
		inboxDirs:    func(string) ([]string, error) { return []string{h.inbox}, nil },
		lastActivity: func(string) (time.Time, bool) { return h.activity, h.activeOK },
		observerAlive: func(string) bool {
			return h.observer
		},
		spawnObserver: func(string, []string) error {
			h.spawned++
			h.observer = true
			return nil
		},
		paneDraft: func(string) (bool, bool) { return h.draft, h.draftOK },
		sendKeys: func(session, text string) error {
			h.nudges = append(h.nudges, session+"|"+text)
			return nil
		},
		logf: func(format string, args ...any) {
			h.logs = append(h.logs, strings.TrimSpace(fmt.Sprintf(format, args...)))
		},
	}
	return h
}

// claim stages an envelope the way a real drain does, leaving an
// un-acknowledged record behind.
func (h *watchdogHarness) claim(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.inbox, name),
		[]byte(`{"ts":"t","source":"slack-watcher","body":"`+body+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.ClaimPending(h.inbox, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := hooks.ReadPendingAck(h.inbox)
	if err != nil || !ok {
		t.Fatalf("claim left no record: ok=%v err=%v", ok, err)
	}
	h.claimedAt = rec.ClaimedAt
	// Default: the session has shown no sign of life since the claim.
	h.activity = rec.ClaimedAt.Add(-time.Minute)
	h.activeOK = true
}

func (h *watchdogHarness) cfg() Config {
	return Config{ProjectRoot: h.root, TasksDir: filepath.Join(h.root, "tasks")}
}

// Scenario 4: alive, idle, non-empty un-acknowledged record, no live
// watcher. One tick puts the envelope back in inbox/, re-arms a
// watcher, and does NOT touch the pane.
func TestInboxWatchdog_RequeuesAndRearmsWithoutNudging(t *testing.T) {
	h := newWatchdogHarness(t)
	h.claim(t, "100-1-1.json", "new thread")

	res, err := runInboxWatchdog(h.cfg(), 3, h.deps)
	if err != nil {
		t.Fatalf("runInboxWatchdog: %v", err)
	}
	if res.Requeued != 1 {
		t.Errorf("Requeued = %d, want 1", res.Requeued)
	}
	if _, err := os.Stat(filepath.Join(h.inbox, "100-1-1.json")); err != nil {
		t.Errorf("envelope not back in inbox/: %v", err)
	}
	if !res.Rearmed || h.spawned != 1 {
		t.Errorf("Rearmed=%v spawned=%d, want true/1", res.Rearmed, h.spawned)
	}
	if len(h.nudges) != 0 {
		t.Errorf("pane was touched on tick 1: %v", h.nudges)
	}
	if _, ok, _ := hooks.ReadPendingAck(h.inbox); ok {
		t.Error("record should be cleared by the requeue")
	}
}

// Scenario 5: the condition persists for N consecutive ticks. The Nth
// tick issues exactly one nudge and logs it; a further tick must not
// nudge again.
func TestInboxWatchdog_EscalatesOnceAtNthTick(t *testing.T) {
	h := newWatchdogHarness(t)
	const n = 3

	for tick := 1; tick <= n+2; tick++ {
		// Each tick re-finds the condition: the envelope was requeued
		// and nothing drained it, so the next drain re-claims it.
		if _, ok, _ := hooks.ReadPendingAck(h.inbox); !ok {
			h.claim(t, "100-1-1.json", "new thread")
		}
		res, err := runInboxWatchdog(h.cfg(), n, h.deps)
		if err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
		switch {
		case tick < n && res.Escalated:
			t.Fatalf("tick %d escalated early", tick)
		case tick == n && !res.Escalated:
			t.Fatalf("tick %d did not escalate (strikes=%d)", tick, res.Strikes)
		case tick > n && res.Escalated:
			t.Fatalf("tick %d escalated again", tick)
		}
	}

	if len(h.nudges) != 1 {
		t.Fatalf("nudges = %v, want exactly 1", h.nudges)
	}
	var logged bool
	for _, l := range h.logs {
		if strings.Contains(l, "escalat") {
			logged = true
		}
	}
	if !logged {
		t.Errorf("escalation not logged: %v", h.logs)
	}
}

// Scenario 6: the coordinator is mid-turn. Nothing is re-armed and no
// send-keys is issued. Never interrupt a live turn.
func TestInboxWatchdog_SkipsWhenSessionActiveSinceClaim(t *testing.T) {
	h := newWatchdogHarness(t)
	h.claim(t, "100-1-1.json", "new thread")
	// The session wrote to its transcript after the claim: the
	// re-prompt landed and a turn is running.
	h.activity = h.claimedAt.Add(2 * time.Second)

	res, err := runInboxWatchdog(h.cfg(), 3, h.deps)
	if err != nil {
		t.Fatalf("runInboxWatchdog: %v", err)
	}
	if res.Requeued != 0 || res.Rearmed || res.Escalated {
		t.Errorf("acted on a live turn: %+v", res)
	}
	if h.spawned != 0 || len(h.nudges) != 0 {
		t.Errorf("spawned=%d nudges=%v, want 0/none", h.spawned, h.nudges)
	}
	if _, ok, _ := hooks.ReadPendingAck(h.inbox); !ok {
		t.Error("record must survive: the turn still owes an acknowledgement")
	}
}

// The liveness probe is the only thing standing between the watchdog
// and a live turn. When it cannot answer, the watchdog must do nothing.
func TestInboxWatchdog_FailsClosedWithoutLivenessSignal(t *testing.T) {
	h := newWatchdogHarness(t)
	h.claim(t, "100-1-1.json", "new thread")
	h.activeOK = false

	res, err := runInboxWatchdog(h.cfg(), 3, h.deps)
	if err != nil {
		t.Fatalf("runInboxWatchdog: %v", err)
	}
	if res.Requeued != 0 || res.Rearmed || res.Escalated {
		t.Errorf("acted without a liveness signal: %+v", res)
	}
}

// The pane-draft constraint: an unsent draft is operator intent. The
// nudge is suppressed rather than typed on top of it, and the
// suppression is logged.
func TestInboxWatchdog_SuppressesNudgeWhenPaneHasDraft(t *testing.T) {
	h := newWatchdogHarness(t)
	h.draft = true

	for tick := 1; tick <= 3; tick++ {
		if _, ok, _ := hooks.ReadPendingAck(h.inbox); !ok {
			h.claim(t, "100-1-1.json", "new thread")
		}
		if _, err := runInboxWatchdog(h.cfg(), 3, h.deps); err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
	}
	if len(h.nudges) != 0 {
		t.Fatalf("typed over an operator draft: %v", h.nudges)
	}
	var logged bool
	for _, l := range h.logs {
		if strings.Contains(l, "draft") {
			logged = true
		}
	}
	if !logged {
		t.Errorf("suppression not logged: %v", h.logs)
	}
}

// Same rule when the pane cannot be read at all: unknown means assume a
// draft, because typing is the only unrecoverable option.
func TestInboxWatchdog_SuppressesNudgeWhenPaneUnreadable(t *testing.T) {
	h := newWatchdogHarness(t)
	h.draftOK = false

	for tick := 1; tick <= 3; tick++ {
		if _, ok, _ := hooks.ReadPendingAck(h.inbox); !ok {
			h.claim(t, "100-1-1.json", "new thread")
		}
		if _, err := runInboxWatchdog(h.cfg(), 3, h.deps); err != nil {
			t.Fatalf("tick %d: %v", tick, err)
		}
	}
	if len(h.nudges) != 0 {
		t.Fatalf("typed into an unreadable pane: %v", h.nudges)
	}
}

// A tick that finds no outstanding delivery resets the streak, so N
// means N CONSECUTIVE ticks.
func TestInboxWatchdog_StreakResetsWhenDeliveryAcknowledged(t *testing.T) {
	h := newWatchdogHarness(t)
	for tick := 1; tick <= 2; tick++ {
		h.claim(t, "100-1-1.json", "new thread")
		if _, err := runInboxWatchdog(h.cfg(), 3, h.deps); err != nil {
			t.Fatal(err)
		}
	}
	// A healthy tick: nothing pending.
	if _, err := runInboxWatchdog(h.cfg(), 3, h.deps); err != nil {
		t.Fatal(err)
	}
	h.claim(t, "200-1-1.json", "later")
	res, err := runInboxWatchdog(h.cfg(), 3, h.deps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Strikes != 1 {
		t.Errorf("Strikes = %d after a healthy tick, want 1", res.Strikes)
	}
	if res.Escalated {
		t.Error("escalated on a reset streak")
	}
}

// Scenario 7: with the fleet kill switch off, Reconcile short-circuits
// before the watchdog, so no re-arm and no nudge happen.
func TestReconcile_WatchdogInertWhenKillSwitchOff(t *testing.T) {
	h := newWatchdogHarness(t)
	h.claim(t, "100-1-1.json", "new thread")

	t.Setenv("HOME", h.root)
	flag, err := FlagPath()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(flag)

	res, err := Reconcile(h.cfg())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !res.Disabled {
		t.Fatal("expected Disabled")
	}
	if res.InboxWatchdog.Requeued != 0 || res.InboxWatchdog.Rearmed || res.InboxWatchdog.Escalated {
		t.Errorf("watchdog acted with the kill switch off: %+v", res.InboxWatchdog)
	}
	if _, ok, _ := hooks.ReadPendingAck(h.inbox); !ok {
		t.Error("record touched with the kill switch off")
	}
}

// Scenario 8: N comes from spore.toml, with a documented default when
// the key is absent.
func TestLoadInboxWatchdogEscalateAfter(t *testing.T) {
	t.Run("absent uses the documented default", func(t *testing.T) {
		root := t.TempDir()
		got, err := LoadInboxWatchdogEscalateAfter(root)
		if err != nil {
			t.Fatal(err)
		}
		if got != DefaultInboxWatchdogEscalateAfter {
			t.Errorf("got %d, want %d", got, DefaultInboxWatchdogEscalateAfter)
		}
	})

	t.Run("spore.toml value is honoured", func(t *testing.T) {
		root := t.TempDir()
		toml := "[coordinator]\ninbox_watchdog_escalate_after = 7\n"
		if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := LoadInboxWatchdogEscalateAfter(root)
		if err != nil {
			t.Fatal(err)
		}
		if got != 7 {
			t.Errorf("got %d, want 7", got)
		}
	})
}
