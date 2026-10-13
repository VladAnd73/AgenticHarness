package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/watch"
)

const oneFailingPRForGate = `
case "$2" in
list) echo '[{"number":7,"headRefName":"feat-x","headRefOid":"sha1","isDraft":false,"url":"https://github.com/o/r/pull/7"}]' ;;
checks) echo '[{"name":"cypress-run","bucket":"fail","link":"https://github.com/o/r/actions/runs/11/job/2"}]'; exit 8 ;;
esac`

// setupPRWatch wires a fake gh plus an enabled watch.toml and returns the
// project root the watcher should run against. The root deliberately has no
// tasks/ directory: a systemd-timer watcher runs wherever systemd put it.
func setupPRWatch(t *testing.T, project string) (root, stateDir string) {
	t.Helper()
	cfgDir := t.TempDir()
	stateDir = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", t.TempDir())
	writeReleaseToml(t, cfgDir, project, "enabled = true\nchecks = [\"cypress\", \"e2e\"]\n")
	writeReleaseGH(t, oneFailingPRForGate)

	root = filepath.Join(t.TempDir(), project)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, stateDir
}

// Scenario 7, first half: the unattended watcher path must not be broken by
// the unknown-slug gate. A watcher runs from a cwd with no tasks/ of its own
// and addresses "coordinator", which has no task file anywhere. With the real
// task.Tell wiring the alert must still be delivered.
func TestWatcherTellSurvivesTheUnknownSlugGate(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	root, stateDir := setupPRWatch(t, "hostproj")
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	rep, err := watch.Run(root, "hostproj", false, tellWithPoke("hostproj", task.Tell))
	if err != nil {
		t.Fatalf("watch.Run with the real tell wiring = %v, want nil", err)
	}
	if rep.Alerts != 1 {
		t.Fatalf("alerts = %d, want 1", rep.Alerts)
	}
	inbox := filepath.Join(stateDir, "spore", "hostproj", "coordinator", "inbox")
	if n := countJSON(t, inbox); n != 1 {
		t.Fatalf("envelopes in %s = %d, want 1", inbox, n)
	}
}

// Scenario 7, second half: when a tell IS refused as an unknown slug, the
// watcher surfaces the failure and leaves its state file loadable with the
// undelivered key unmarked, so the next run retries instead of swallowing
// the alert.
func TestWatcherSurfacesUnknownSlugWithoutWedgingState(t *testing.T) {
	root, _ := setupPRWatch(t, "hostproj")

	refusing := func(slug, msg string) error {
		return fmt.Errorf("%w: %q has no task file", task.ErrUnknownSlug, slug)
	}
	_, err := watch.Run(root, "hostproj", false, refusing)
	if !errors.Is(err, task.ErrUnknownSlug) {
		t.Fatalf("watch.Run error = %v, want it to wrap task.ErrUnknownSlug", err)
	}

	st, err := watch.LoadState("hostproj")
	if err != nil {
		t.Fatalf("state file unreadable after a refused tell: %v", err)
	}
	if len(st.Seen) != 0 {
		t.Fatalf("seen keys = %v, want none marked for an undelivered alert", st.Seen)
	}

	// The next run retries the same alert rather than deduping it away.
	var delivered int
	rep, err := watch.Run(root, "hostproj", false, func(slug, msg string) error {
		delivered++
		return nil
	})
	if err != nil {
		t.Fatalf("retry run = %v, want nil", err)
	}
	if rep.Alerts != 1 || delivered != 1 {
		t.Fatalf("retry delivered %d alert(s) (report %+v), want 1", delivered, rep)
	}
}
