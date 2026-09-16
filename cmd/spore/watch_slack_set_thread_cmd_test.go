package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/versality/spore/internal/watch"
)

func TestRunWatchSlackSetThreadUpdatesMapping(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	projRoot := filepath.Join(t.TempDir(), "hostproj")
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	st, err := watch.LoadSlackState("hostproj")
	if err != nil {
		t.Fatal(err)
	}
	st.Threads["1000.0002"] = watch.SlackThread{}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	out, code := captureRun(t, func() int {
		return runWatchSlackSetThread([]string{"slack-set-thread", "--project-root", projRoot, "1000.0002", "investigate-login-bug"})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%q", code, out)
	}
	after, err := watch.LoadSlackState("hostproj")
	if err != nil {
		t.Fatal(err)
	}
	if after.Threads["1000.0002"].TaskSlug != "investigate-login-bug" {
		t.Fatalf("task slug not persisted: %+v", after.Threads["1000.0002"])
	}
}

func TestRunWatchSlackSetThreadUnknownThreadErrors(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	t.Setenv("XDG_STATE_HOME", t.TempDir())
	projRoot := filepath.Join(t.TempDir(), "hostproj")
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	_, code := captureRun(t, func() int {
		return runWatchSlackSetThread([]string{"slack-set-thread", "--project-root", projRoot, "9999.9999", "some-task"})
	})
	if code == 0 {
		t.Fatal("want non-zero exit for an unknown thread")
	}
}

func TestRunWatchSlackSetThreadWrongArgCountErrors(t *testing.T) {
	_, code := captureRun(t, func() int {
		return runWatchSlackSetThread([]string{"slack-set-thread", "only-one-arg"})
	})
	if code == 0 {
		t.Fatal("want non-zero exit for wrong arg count")
	}
}
