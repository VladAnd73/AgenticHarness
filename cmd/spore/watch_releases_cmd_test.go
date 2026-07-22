package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/watch"
)

// captureRun runs fn with os.Stdout redirected to a pipe and returns what it
// printed plus its exit code. It restores the working directory afterward,
// since the watch subcommand chdirs to --project-root.
func captureRun(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := fn()
	_ = w.Close()
	os.Stdout = orig
	b, _ := io.ReadAll(r)
	return string(b), code
}

func writeReleaseGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "gh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_GH_BINARY", p)
}

func writeReleaseToml(t *testing.T, cfgDir, project, body string) {
	t.Helper()
	p := filepath.Join(cfgDir, "spore", project)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "watch.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Scenario 8: with the REAL tell/poke wiring the CLI uses (task.TellProject +
// hooks.NotifyCoordinator), a fired release lands a poke in the coordinator's
// wake channel and a message in that coordinator's project message inbox.
func TestReleaseWatchRealDeliveryPaths(t *testing.T) {
	cfgDir := t.TempDir()
	stateDir := t.TempDir()
	coordDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", coordDir)

	writeReleaseToml(t, cfgDir, "hostproj", `
[releases]
enabled = true
repos = ["o/backend"]
coordinators = ["frontend"]
`)
	writeReleaseGH(t, `echo '{"tagName":"v2.0.0","url":"https://github.com/o/backend/releases/tag/v2.0.0","publishedAt":"2026-07-21T10:00:00Z"}'`)

	st, err := watch.LoadReleaseState("hostproj")
	if err != nil {
		t.Fatal(err)
	}
	st.Mark("o/backend", "v1.0.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	tell := func(coord, msg string) error { return task.TellProject(coord, "coordinator", msg) }
	poke := func(coord string) error { return hooks.NotifyCoordinator(coord) }

	rep, err := watch.RunReleases(t.TempDir(), "hostproj", false, tell, poke)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pokes != 1 {
		t.Fatalf("want 1 poke, got %+v", rep)
	}

	msgInbox := filepath.Join(stateDir, "spore", "frontend", "coordinator", "inbox")
	if n := countJSON(t, msgInbox); n != 1 {
		t.Fatalf("message envelopes in %s = %d, want 1", msgInbox, n)
	}
	wakeChannel := filepath.Join(coordDir, "frontend", "inbox")
	if n := countJSON(t, wakeChannel); n != 1 {
		t.Fatalf("poke files in %s = %d, want 1", wakeChannel, n)
	}
}

// runWatchReleases (the CLI entry) parses args, honors --dry-run, prints the
// one-line summary, advances state on a real run, and returns exit 0.
func TestRunWatchReleasesCLI(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	cfgDir := t.TempDir()
	stateDir := t.TempDir()
	coordDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", coordDir)

	// A non-git dir whose basename is the project name, so task.ProjectName
	// falls back to the basename ("hostproj").
	projRoot := filepath.Join(t.TempDir(), "hostproj")
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	writeReleaseToml(t, cfgDir, "hostproj", `
[releases]
enabled = true
repos = ["o/backend"]
coordinators = ["frontend"]
`)
	writeReleaseGH(t, `echo '{"tagName":"v2.0.0","url":"https://github.com/o/backend/releases/tag/v2.0.0","publishedAt":"2026-07-21T10:00:00Z"}'`)

	st, err := watch.LoadReleaseState("hostproj")
	if err != nil {
		t.Fatal(err)
	}
	st.Mark("o/backend", "v1.0.0")
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	const summary = "release-watch hostproj: 1 poke(s), 0 repo(s) unchanged"
	msgInbox := filepath.Join(stateDir, "spore", "frontend", "coordinator", "inbox")

	out, code := captureRun(t, func() int {
		return runWatchReleases([]string{"releases", "--project-root", projRoot, "--dry-run"})
	})
	if code != 0 {
		t.Fatalf("dry-run exit = %d, want 0; out=%q", code, out)
	}
	if !strings.Contains(out, summary) {
		t.Fatalf("dry-run summary missing/wrong: %q", out)
	}
	if n := countJSON(t, msgInbox); n != 0 {
		t.Fatalf("dry-run must send nothing, envelopes=%d", n)
	}
	after, _ := watch.LoadReleaseState("hostproj")
	if tag, _ := after.Tag("o/backend"); tag != "v1.0.0" {
		t.Fatalf("dry-run must not advance state, tag=%q want v1.0.0", tag)
	}

	out, code = captureRun(t, func() int {
		return runWatchReleases([]string{"releases", "--project-root", projRoot})
	})
	if code != 0 {
		t.Fatalf("run exit = %d, want 0; out=%q", code, out)
	}
	if !strings.Contains(out, summary) {
		t.Fatalf("run summary missing/wrong: %q", out)
	}
	if n := countJSON(t, msgInbox); n != 1 {
		t.Fatalf("envelopes = %d, want 1", n)
	}
	after, _ = watch.LoadReleaseState("hostproj")
	if tag, _ := after.Tag("o/backend"); tag != "v2.0.0" {
		t.Fatalf("real run must advance state to v2.0.0, got %q", tag)
	}
}
