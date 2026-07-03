package watch

import (
	"errors"
	"os"
	"path/filepath"
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

// twoFailingChecksScript returns one PR with two matching failing e2e checks.
const twoFailingChecksScript = `
case "$2" in
list) echo '[{"number":5,"headRefName":"feat-z","headRefOid":"sha9","isDraft":false,"url":"https://github.com/o/r/pull/5"}]' ;;
checks) echo '[{"name":"e2e / Regression (3)","bucket":"fail","link":"https://github.com/o/r/actions/runs/30/job/3"},{"name":"e2e / Regression (7)","bucket":"fail","link":"https://github.com/o/r/actions/runs/30/job/7"}]'; exit 8 ;;
esac`

func TestRunRollsUpChecksPerPR(t *testing.T) {
	root, tells, tell := setupRun(t, twoFailingChecksScript)
	rep, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	// Two matching checks on one PR must produce exactly ONE tell.
	if rep.Alerts != 1 || len(*tells) != 1 {
		t.Fatalf("want 1 alert for 2 checks, got %+v / %d tells", rep, len(*tells))
	}
	msg := (*tells)[0].msg
	for _, want := range []string{"PR #5", "feat-z", "2 failing", "Regression (3)", "Regression (7)", "runs/30"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("msg missing %q:\n%s", want, msg)
		}
	}
	// Second round must be silent; both keys are now seen.
	rep2, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Alerts != 0 || rep2.Skipped != 2 {
		t.Fatalf("second round: want 0 alerts 2 skipped, got %+v", rep2)
	}
	if len(*tells) != 1 {
		t.Fatal("second round must not send another tell")
	}
}

// counterFile is used by TestRunNewCheckOnAlertedPRTriggersRollup to simulate
// a PR that gains a second failing check on the second polling round.
func TestRunNewCheckOnAlertedPRTriggersRollup(t *testing.T) {
	// The fake gh script reads a counter file to decide which checks to return.
	// Round 1 (counter==0): one check. Round 2+ (counter>=1): two checks.
	counterFile := filepath.Join(t.TempDir(), "counter")
	if err := os.WriteFile(counterFile, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$2" in
list) echo '[{"number":3,"headRefName":"inc","headRefOid":"shaX","isDraft":false,"url":"https://github.com/o/r/pull/3"}]' ;;
checks)
  n=$(cat ` + counterFile + `)
  echo $((n+1)) > ` + counterFile + `
  if [ "$n" -eq 0 ]; then
    echo '[{"name":"e2e-login","bucket":"fail","link":"https://ci/runs/1/job/1"}]'
  else
    echo '[{"name":"e2e-login","bucket":"fail","link":"https://ci/runs/1/job/1"},{"name":"e2e-dashboard","bucket":"fail","link":"https://ci/runs/1/job/2"}]'
  fi
  exit 8 ;;
esac`
	root, _, _ := setupRun(t, script)

	var got1 []told
	rep1, err := Run(root, "proj", false, func(slug, msg string) error {
		got1 = append(got1, told{slug, msg})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep1.Alerts != 1 || len(got1) != 1 {
		t.Fatalf("round 1: want 1 alert, got %+v / %d tells", rep1, len(got1))
	}
	if !strings.Contains(got1[0].msg, "e2e-login") {
		t.Fatalf("round 1: msg missing e2e-login:\n%s", got1[0].msg)
	}

	// Round 2: e2e-dashboard is new; e2e-login is already seen.
	// Expect one tell listing only the new check.
	var got2 []told
	rep2, err := Run(root, "proj", false, func(slug, msg string) error {
		got2 = append(got2, told{slug, msg})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Alerts != 1 || len(got2) != 1 {
		t.Fatalf("round 2: want 1 alert for new check, got %+v / %d tells", rep2, len(got2))
	}
	if !strings.Contains(got2[0].msg, "e2e-dashboard") {
		t.Fatalf("round 2: msg missing e2e-dashboard:\n%s", got2[0].msg)
	}
	if strings.Contains(got2[0].msg, "e2e-login") {
		t.Fatalf("round 2: must not re-alert already-seen e2e-login:\n%s", got2[0].msg)
	}
}
