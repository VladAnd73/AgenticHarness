package watch

import (
	"errors"
	"strings"
	"testing"
)

type told struct {
	slug, msg string
}

func setupRun(t *testing.T, ghScript string) (dir string, tells *[]told, tell func(string, string) error) {
	t.Helper()
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	writeWatchToml(t, cfgDir, "proj", "enabled = true\nchecks = [\"cypress\", \"e2e\"]\n")
	fakeGH(t, ghScript)
	var got []told
	return t.TempDir(), &got, func(slug, msg string) error {
		got = append(got, told{slug, msg})
		return nil
	}
}

const oneFailingPR = `
case "$2" in
list) echo '[{"number":7,"headRefName":"feat-x","headRefOid":"sha1","isDraft":false,"url":"https://github.com/o/r/pull/7"}]' ;;
checks) echo '[{"name":"cypress-run","bucket":"fail","link":"https://github.com/o/r/actions/runs/11/job/2"}]'; exit 8 ;;
esac`

func TestRunAlertsOnceAndDedups(t *testing.T) {
	root, tells, tell := setupRun(t, oneFailingPR)
	rep, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Alerts != 1 || len(*tells) != 1 {
		t.Fatalf("want 1 alert, got %+v / %d tells", rep, len(*tells))
	}
	if (*tells)[0].slug != "coordinator" {
		t.Fatalf("told %q, want coordinator", (*tells)[0].slug)
	}
	for _, want := range []string{"PR #7", "feat-x", "cypress-run", "runs/11"} {
		if !strings.Contains((*tells)[0].msg, want) {
			t.Fatalf("msg missing %q:\n%s", want, (*tells)[0].msg)
		}
	}
	rep2, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Alerts != 0 || len(*tells) != 1 {
		t.Fatal("second round must be silent (dedup)")
	}
}

func TestRunDisabledDoesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fakeGH(t, "exit 99")
	rep, err := Run(t.TempDir(), "proj", false, func(string, string) error {
		t.Fatal("must not tell")
		return nil
	})
	if err != nil || rep.Alerts != 0 {
		t.Fatalf("disabled run: %+v, %v", rep, err)
	}
}

func TestRunIllHealthAfterThreeFailures(t *testing.T) {
	root, tells, tell := setupRun(t, "exit 1")
	for i := 0; i < 3; i++ {
		if _, err := Run(root, "proj", false, tell); err == nil {
			t.Fatalf("round %d: want error", i)
		}
	}
	if len(*tells) != 1 || !strings.Contains((*tells)[0].msg, "unhealthy") {
		t.Fatalf("want exactly one ill-health tell, got %v", *tells)
	}
	if _, err := Run(root, "proj", false, tell); err == nil {
		t.Fatal("still failing")
	}
	if len(*tells) != 1 {
		t.Fatal("must not re-alert ill health every round")
	}
}

// twoPRsScript returns two PRs each with a matching failing check so the tell
// loop has two candidates to exercise partial-batch tell-error behavior.
const twoPRsScript = `
case "$2" in
list) echo '[{"number":1,"headRefName":"a","headRefOid":"s1","isDraft":false,"url":"u1"},{"number":2,"headRefName":"b","headRefOid":"s2","isDraft":false,"url":"u2"}]' ;;
checks) echo '[{"name":"cypress-run","bucket":"fail","link":"l"}]'; exit 8 ;;
esac`

func TestRunTellErrorPersistsDeliveredKey(t *testing.T) {
	root, _, _ := setupRun(t, twoPRsScript)
	tellCount := 0
	tellErr := errors.New("tell boom")
	tell := func(slug, msg string) error {
		tellCount++
		if tellCount == 2 {
			return tellErr
		}
		return nil
	}
	// First Run: tell succeeds for PR #1, fails for PR #2.
	_, err := Run(root, "proj", false, tell)
	if !errors.Is(err, tellErr) {
		t.Fatalf("want tellErr, got %v", err)
	}
	// Second Run: PR #1 alert must be suppressed (key was persisted after the
	// successful tell); only PR #2 should fire.
	var got []told
	tell2 := func(slug, msg string) error {
		got = append(got, told{slug, msg})
		return nil
	}
	rep2, err := Run(root, "proj", false, tell2)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Alerts != 1 {
		t.Fatalf("want 1 alert on retry, got %d", rep2.Alerts)
	}
	if len(got) != 1 || !strings.Contains(got[0].msg, "PR #2") {
		t.Fatalf("expected alert for PR #2 only, got %v", got)
	}
}

// draftPRScript is a PR with isDraft:true and a matching failing check.
const draftPRScript = `
case "$2" in
list) echo '[{"number":9,"headRefName":"wip","headRefOid":"d1","isDraft":true,"url":"u9"}]' ;;
checks) echo '[{"name":"cypress-run","bucket":"fail","link":"l"}]'; exit 8 ;;
esac`

func TestRunDraftSkipped(t *testing.T) {
	root, tells, tell := setupRun(t, draftPRScript)
	rep, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Alerts != 0 || len(*tells) != 0 {
		t.Fatalf("draft PR must produce no alert, got %+v / %d tells", rep, len(*tells))
	}
}

func TestRunNonMatchingCheckIgnored(t *testing.T) {
	root, tells, tell := setupRun(t, `
case "$2" in
list) echo '[{"number":8,"headRefName":"y","headRefOid":"s","isDraft":false,"url":"u"}]' ;;
checks) echo '[{"name":"unit-tests","bucket":"fail","link":"l"}]'; exit 8 ;;
esac`)
	rep, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Alerts != 0 || len(*tells) != 0 {
		t.Fatal("non-matching check must not alert")
	}
}
