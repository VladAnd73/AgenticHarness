package watch

import (
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	k := Key(42, "abc123", "cypress-run")
	if st.SeenKey(k) {
		t.Fatal("fresh state must not contain key")
	}
	st.MarkKey(k)
	st.Failures = 2
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	st2, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !st2.SeenKey(k) {
		t.Fatal("key must survive reload")
	}
	if st2.Failures != 2 {
		t.Fatalf("failures = %d, want 2", st2.Failures)
	}
}

func TestStatePrune(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, _ := LoadState("proj")
	live := Key(1, "aaa", "e2e")
	dead := Key(2, "bbb", "e2e")
	st.MarkKey(live)
	st.MarkKey(dead)
	st.Prune(map[string]bool{live: true})
	if !st.SeenKey(live) {
		t.Fatal("live key pruned")
	}
	if st.SeenKey(dead) {
		t.Fatal("dead key survived prune")
	}
}
