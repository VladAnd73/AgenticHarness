package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "gh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_GH_BINARY", p)
}

func TestOpenPRs(t *testing.T) {
	fakeGH(t, `echo '[{"number":42,"headRefName":"fix-login","headRefOid":"abc123","isDraft":false,"url":"https://github.com/o/r/pull/42"}]'`)
	prs, err := OpenPRs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 42 || prs[0].Branch != "fix-login" || prs[0].HeadSHA != "abc123" {
		t.Fatalf("bad parse: %+v", prs)
	}
}

func TestFailingChecksKeepsOnlyFailBucket(t *testing.T) {
	fakeGH(t, `echo '[{"name":"cypress-run","bucket":"fail","link":"https://github.com/o/r/actions/runs/9/job/1"},{"name":"lint","bucket":"pass","link":""}]'`)
	checks, err := FailingChecks(t.TempDir(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Name != "cypress-run" {
		t.Fatalf("bad filter: %+v", checks)
	}
}

func TestGHErrorPropagates(t *testing.T) {
	fakeGH(t, `exit 1`)
	if _, err := OpenPRs(t.TempDir()); err == nil {
		t.Fatal("want error from failing gh")
	}
}

func TestFailingChecksNonZeroExitStillParses(t *testing.T) {
	fakeGH(t, `echo '[{"name":"e2e","bucket":"fail","link":"x"}]'; exit 8`)
	checks, err := FailingChecks(t.TempDir(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 {
		t.Fatalf("want 1 failing check, got %+v", checks)
	}
}

func TestFailingChecksNoChecksReportedIsEmpty(t *testing.T) {
	// gh prints this sentinel to stderr, exits 1, empty stdout when a PR has no
	// checks at all. A PR with no checks has nothing failing, not an error.
	fakeGH(t, `echo "no checks reported on the 'feat-x' branch" >&2; exit 1`)
	checks, err := FailingChecks(t.TempDir(), 3)
	if err != nil {
		t.Fatalf("no-checks PR must not error, got %v", err)
	}
	if len(checks) != 0 {
		t.Fatalf("no-checks PR must have zero failing checks, got %+v", checks)
	}
}
