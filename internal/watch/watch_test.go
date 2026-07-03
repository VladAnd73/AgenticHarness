package watch

import (
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
