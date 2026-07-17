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

// firstPRNoChecksScript: PR #1 (first in the list) has no checks (gh exits 1,
// prints the "no checks reported" sentinel to stderr, empty stdout); PR #2 has
// a failing e2e check. Mirrors the reported bug: a no-CI PR sorted ahead of a
// real e2e PR.
const firstPRNoChecksScript = `
case "$2" in
list) echo '[{"number":1,"headRefName":"bump","headRefOid":"s1","isDraft":false,"url":"https://github.com/o/r/pull/1"},{"number":2,"headRefName":"real-e2e","headRefOid":"s2","isDraft":false,"url":"https://github.com/o/r/pull/2"}]' ;;
checks)
  case "$3" in
  1) echo "no checks reported on the 'bump' branch" >&2; exit 1 ;;
  2) echo '[{"name":"e2e-run","bucket":"fail","link":"https://github.com/o/r/actions/runs/9/job/1"}]'; exit 8 ;;
  esac ;;
esac`

func TestRunNoChecksPRDoesNotBlindLaterPR(t *testing.T) {
	root, tells, tell := setupRun(t, firstPRNoChecksScript)
	rep, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Alerts != 1 || len(*tells) != 1 {
		t.Fatalf("want 1 alert for the later PR, got %+v / %d tells", rep, len(*tells))
	}
	if !strings.Contains((*tells)[0].msg, "PR #2") {
		t.Fatalf("alert must be for PR #2:\n%s", (*tells)[0].msg)
	}
}

// firstPRAuthErrorScript: PR #1 errors with a non-benign auth failure (gh exit
// 4, empty stdout, stderr is NOT the no-checks sentinel); PR #2 has a failing
// e2e check.
const firstPRAuthErrorScript = `
case "$2" in
list) echo '[{"number":1,"headRefName":"a","headRefOid":"s1","isDraft":false,"url":"https://github.com/o/r/pull/1"},{"number":2,"headRefName":"b","headRefOid":"s2","isDraft":false,"url":"https://github.com/o/r/pull/2"}]' ;;
checks)
  case "$3" in
  1) echo "gh: authentication failed" >&2; exit 4 ;;
  2) echo '[{"name":"e2e-run","bucket":"fail","link":"https://github.com/o/r/actions/runs/9/job/1"}]'; exit 8 ;;
  esac ;;
esac`

func TestRunPerPRErrorSkippedNotAborted(t *testing.T) {
	root, tells, tell := setupRun(t, firstPRAuthErrorScript)
	rep, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatalf("a single PR error must not abort the poll: %v", err)
	}
	if rep.Alerts != 1 || len(*tells) != 1 {
		t.Fatalf("want 1 alert for the healthy PR, got %+v / %d tells", rep, len(*tells))
	}
	if !strings.Contains((*tells)[0].msg, "PR #2") {
		t.Fatalf("alert must be for PR #2:\n%s", (*tells)[0].msg)
	}
}

func TestRunNoChecksResetsFailureCounter(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	writeWatchToml(t, cfgDir, "proj", "enabled = true\nchecks = [\"e2e\"]\n")
	// Two healthy PRs: one with a passing check, one with no checks at all.
	fakeGH(t, `
case "$2" in
list) echo '[{"number":1,"headRefName":"a","headRefOid":"s1","isDraft":false,"url":"u1"},{"number":2,"headRefName":"b","headRefOid":"s2","isDraft":false,"url":"u2"}]' ;;
checks)
  case "$3" in
  1) echo '[{"name":"e2e-run","bucket":"pass","link":""}]' ;;
  2) echo "no checks reported on the 'b' branch" >&2; exit 1 ;;
  esac ;;
esac`)
	// Seed a non-zero failure counter left by a prior bad round.
	st, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	st.Failures = 2
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(t.TempDir(), "proj", false, func(string, string) error {
		t.Fatal("healthy poll must not tell")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Alerts != 0 {
		t.Fatalf("healthy poll: want 0 alerts, got %+v", rep)
	}
	after, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if after.Failures != 0 {
		t.Fatalf("clean poll must reset failures to 0, got %d", after.Failures)
	}
}

// partialErrorScript: PR #1's checks query errors (auth, exit 4); PR #2's
// query succeeds with a non-failing check. A partial-error poll: neither a
// total wipeout nor a fully clean poll.
const partialErrorScript = `
case "$2" in
list) echo '[{"number":1,"headRefName":"a","headRefOid":"s1","isDraft":false,"url":"u1"},{"number":2,"headRefName":"b","headRefOid":"s2","isDraft":false,"url":"u2"}]' ;;
checks)
  case "$3" in
  1) echo "gh: authentication failed" >&2; exit 4 ;;
  2) echo '[{"name":"e2e-run","bucket":"pass","link":""}]' ;;
  esac ;;
esac`

// Regression 1: a partial-error poll must not reset the ill-health record, and
// repeated partial failures must still escalate to the 3-strike alert.
func TestRunPartialErrorPreservesFailureRecord(t *testing.T) {
	root, tells, tell := setupRun(t, partialErrorScript)
	// Seed two prior failures so one more partial round crosses the 3-strike
	// threshold - provided the partial poll advances rather than wipes the record.
	st, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	st.Failures = 2
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, "proj", false, tell); err != nil {
		t.Fatalf("partial poll must not abort: %v", err)
	}
	after, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if after.Failures != 3 {
		t.Fatalf("partial error must advance the failure counter (2 -> 3), got %d", after.Failures)
	}
	if len(*tells) != 1 || !strings.Contains((*tells)[0].msg, "unhealthy") {
		t.Fatalf("reaching 3 consecutive failures must fire one ill-health tell, got %v", *tells)
	}
}

// Regression 2: a PR whose checks query errors this round must keep its seen
// dedup keys, so the same failing check on the same commit does not re-alert
// once the query recovers.
func TestRunTransientErrorPreservesSeenKeys(t *testing.T) {
	counterFile := filepath.Join(t.TempDir(), "counter")
	if err := os.WriteFile(counterFile, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	// PR #1 is the target: round 1 fails e2e-login, round 2 errors transiently,
	// round 3 recovers still failing e2e-login on the same commit. PR #2 always
	// answers cleanly so round 2 is a partial error, not a wipeout.
	script := `#!/bin/sh
case "$2" in
list) echo '[{"number":1,"headRefName":"target","headRefOid":"s1","isDraft":false,"url":"https://github.com/o/r/pull/1"},{"number":2,"headRefName":"other","headRefOid":"s2","isDraft":false,"url":"u2"}]' ;;
checks)
  case "$3" in
  1)
    n=$(cat ` + counterFile + `)
    echo $((n+1)) > ` + counterFile + `
    if [ "$n" -eq 1 ]; then
      echo "gh: server error" >&2; exit 5
    else
      echo '[{"name":"e2e-login","bucket":"fail","link":"https://ci/runs/1/job/1"}]'; exit 8
    fi
    ;;
  2) echo '[{"name":"unit","bucket":"pass","link":""}]' ;;
  esac ;;
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
	if rep1.Alerts != 1 || len(got1) != 1 || !strings.Contains(got1[0].msg, "e2e-login") {
		t.Fatalf("round 1: want 1 alert for e2e-login, got %+v / %v", rep1, got1)
	}

	var got2 []told
	if _, err := Run(root, "proj", false, func(slug, msg string) error {
		got2 = append(got2, told{slug, msg})
		return nil
	}); err != nil {
		t.Fatalf("round 2 (partial error) must not abort: %v", err)
	}
	if len(got2) != 0 {
		t.Fatalf("round 2: transient error must not alert, got %v", got2)
	}

	var got3 []told
	if _, err := Run(root, "proj", false, func(slug, msg string) error {
		got3 = append(got3, told{slug, msg})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got3) != 0 {
		t.Fatalf("round 3: same check on same commit must NOT re-alert (seen key dropped by prune), got %v", got3)
	}
}

// allPRsErrorScript: pr list succeeds but every PR's checks query fails with a
// non-benign error - a total wipeout, not a single transient skip.
const allPRsErrorScript = `
case "$2" in
list) echo '[{"number":1,"headRefName":"a","headRefOid":"s1","isDraft":false,"url":"u1"},{"number":2,"headRefName":"b","headRefOid":"s2","isDraft":false,"url":"u2"}]' ;;
checks) echo "gh: authentication failed" >&2; exit 4 ;;
esac`

func TestRunAllPRsErrorIsIllHealth(t *testing.T) {
	root, tells, tell := setupRun(t, allPRsErrorScript)
	// A wipeout round routes through noteFailure and surfaces the error rather
	// than silently reporting a clean poll.
	if _, err := Run(root, "proj", false, tell); err == nil {
		t.Fatal("total wipeout must surface an error")
	}
	if len(*tells) != 0 {
		t.Fatalf("must not alert before 3 consecutive failures, got %v", *tells)
	}
	after, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if after.Failures != 1 {
		t.Fatalf("wipeout must increment the failure counter, got %d", after.Failures)
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
