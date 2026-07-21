package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/versality/spore/internal/hooks"
	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/watch"
)

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

	st, err := watch.LoadState("hostproj")
	if err != nil {
		t.Fatal(err)
	}
	st.MarkRelease("o/backend", "v1.0.0")
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
