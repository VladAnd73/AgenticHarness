package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setNow pins the package clock for deterministic cooldown tests.
func setNow(t *testing.T, tm time.Time) {
	t.Helper()
	prev := now
	now = func() time.Time { return tm }
	t.Cleanup(func() { now = prev })
}

// TestFailureSigShape pins the exact error string the dedup key is derived
// from. If gh arg order or the wrap format drifts, the key silently changes
// and every stuck condition re-alerts; this test catches that.
func TestFailureSigShape(t *testing.T) {
	fakeGH(t, "exit 1")
	_, err := OpenPRs(t.TempDir())
	if err == nil {
		t.Fatal("want error from failing gh")
	}
	want := "gh [pr list --state open --json number,headRefName,headRefOid,isDraft,url]: exit status 1"
	if got := failureSig(err); got != want {
		t.Fatalf("sig shape drift:\n got %q\nwant %q", got, want)
	}
}

// TestRunIllHealthReFiresAfterCooldown: a persistently stuck condition stays
// silent within the cooldown window but re-surfaces exactly once after it,
// then goes silent again (heartbeat). This is the anti-swallow guarantee.
func TestRunIllHealthReFiresAfterCooldown(t *testing.T) {
	root, tells, tell := setupRun(t, "exit 1")
	base := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	setNow(t, base)

	for i := 0; i < 3; i++ {
		if _, err := Run(root, "proj", false, tell); err == nil {
			t.Fatalf("round %d: want error", i)
		}
	}
	if len(*tells) != 1 {
		t.Fatalf("want 1 tell after 3 failures, got %d", len(*tells))
	}

	// Within cooldown: still failing, must stay silent.
	if _, err := Run(root, "proj", false, tell); err == nil {
		t.Fatal("still failing")
	}
	if len(*tells) != 1 {
		t.Fatalf("within cooldown must not re-alert, got %d tells", len(*tells))
	}

	// After cooldown: same condition re-surfaces exactly once.
	setNow(t, base.Add(failureCooldown+time.Minute))
	if _, err := Run(root, "proj", false, tell); err == nil {
		t.Fatal("still failing")
	}
	if len(*tells) != 2 {
		t.Fatalf("after cooldown must re-alert once, got %d tells", len(*tells))
	}

	// Cooldown resets after the re-fire: silent again at the same clock.
	if _, err := Run(root, "proj", false, tell); err == nil {
		t.Fatal("still failing")
	}
	if len(*tells) != 2 {
		t.Fatalf("cooldown must reset after re-fire, got %d tells", len(*tells))
	}
}

// TestRunIllHealthChangedSigFiresImmediately: a different error (here a
// changed exit status) is a new signature and must alert at once, ignoring
// the cooldown for the prior signature.
func TestRunIllHealthChangedSigFiresImmediately(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	if err := os.WriteFile(counter, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `
n=$(cat ` + counter + `)
echo $((n+1)) > ` + counter + `
if [ "$n" -lt 3 ]; then exit 1; else exit 2; fi`
	root, tells, tell := setupRun(t, script)
	setNow(t, time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))

	// Three exit-1 failures: fire once at the third.
	for i := 0; i < 3; i++ {
		if _, err := Run(root, "proj", false, tell); err == nil {
			t.Fatalf("round %d: want error", i)
		}
	}
	if len(*tells) != 1 {
		t.Fatalf("want 1 tell after 3 exit-1 failures, got %d", len(*tells))
	}

	// Fourth round exits with a different status -> new signature -> fire now.
	if _, err := Run(root, "proj", false, tell); err == nil {
		t.Fatal("still failing")
	}
	if len(*tells) != 2 {
		t.Fatalf("changed error must re-alert immediately, got %d tells", len(*tells))
	}
}

// TestRunRecoveryClearsFailureRecord: a clean poll resets the failure record
// so the next failure alerts immediately rather than waiting out the cooldown.
func TestRunRecoveryClearsFailureRecord(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "counter")
	if err := os.WriteFile(counter, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	// n<3 -> pr list fails; n==3 -> clean empty list (recovery); n>=4 -> fail again.
	script := `
n=$(cat ` + counter + `)
echo $((n+1)) > ` + counter + `
case "$2" in
list) if [ "$n" -eq 3 ]; then echo '[]'; else exit 1; fi ;;
checks) echo '[]' ;;
esac`
	root, tells, tell := setupRun(t, script)
	setNow(t, time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))

	for i := 0; i < 3; i++ {
		if _, err := Run(root, "proj", false, tell); err == nil {
			t.Fatalf("round %d: want error", i)
		}
	}
	if len(*tells) != 1 {
		t.Fatalf("want 1 tell after 3 failures, got %d", len(*tells))
	}

	// Recovery round: clean poll clears the failure record.
	if _, err := Run(root, "proj", false, tell); err != nil {
		t.Fatalf("recovery round must succeed, got %v", err)
	}
	st, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if st.Failures != 0 || st.NotifiedSig != "" || st.NotifiedAt != "" {
		t.Fatalf("recovery must clear failure record, got %+v", st)
	}

	// Fresh failures re-alert immediately even inside the old cooldown window.
	for i := 0; i < 3; i++ {
		if _, err := Run(root, "proj", false, tell); err == nil {
			t.Fatalf("post-recovery round %d: want error", i)
		}
	}
	if len(*tells) != 2 {
		t.Fatalf("post-recovery failure must re-alert, got %d tells", len(*tells))
	}
}

// TestRunDryRunFailureNeverTellsOrSaves: -dry-run is a pure probe. It surfaces
// the failure via the returned error but never tells the coordinator nor
// mutates the state file.
func TestRunDryRunFailureNeverTellsOrSaves(t *testing.T) {
	root, tells, tell := setupRun(t, "exit 1")
	for i := 0; i < 5; i++ {
		if _, err := Run(root, "proj", true, tell); err == nil {
			t.Fatal("dry-run must surface the failure via error")
		}
	}
	if len(*tells) != 0 {
		t.Fatalf("dry-run must never tell, got %d", len(*tells))
	}
	st, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if st.Failures != 0 || st.NotifiedSig != "" {
		t.Fatalf("dry-run must not save state, got %+v", st)
	}
}
