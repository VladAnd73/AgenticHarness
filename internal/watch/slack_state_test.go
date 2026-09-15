package watch

import (
	"testing"
	"time"
)

func TestLoadSlackStateMissingFileIsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, err := LoadSlackState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if st.Cursor != "" || len(st.Threads) != 0 {
		t.Fatalf("want empty state, got %+v", st)
	}
}

func TestSlackStateSaveAndReload(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, err := LoadSlackState("proj")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	st.Cursor = "1000.0001"
	st.Threads["1000.0001"] = SlackThread{LastActivity: now, LastReplyTS: "1000.0001"}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	after, err := LoadSlackState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if after.Cursor != "1000.0001" {
		t.Fatalf("cursor = %q, want 1000.0001", after.Cursor)
	}
	th, ok := after.Threads["1000.0001"]
	if !ok || !th.LastActivity.Equal(now) || th.LastReplyTS != "1000.0001" {
		t.Fatalf("thread not round-tripped correctly: %+v ok=%v", th, ok)
	}
}

func TestSlackStateSetTaskSlug(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, _ := LoadSlackState("proj")
	st.Threads["1000.0001"] = SlackThread{LastActivity: time.Now()}
	st.SetTaskSlug("1000.0001", "fix-login-bug")
	if st.Threads["1000.0001"].TaskSlug != "fix-login-bug" {
		t.Fatalf("task slug not set: %+v", st.Threads["1000.0001"])
	}
}

func TestSlackStatePrune(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, _ := LoadSlackState("proj")
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	st.Threads["stale"] = SlackThread{LastActivity: now.Add(-49 * time.Hour)}
	st.Threads["fresh"] = SlackThread{LastActivity: now.Add(-1 * time.Hour)}
	n := st.Prune(now, 48*time.Hour)
	if n != 1 {
		t.Fatalf("pruned %d, want 1", n)
	}
	if _, ok := st.Threads["stale"]; ok {
		t.Fatal("stale thread must be removed")
	}
	if _, ok := st.Threads["fresh"]; !ok {
		t.Fatal("fresh thread must survive")
	}
}
