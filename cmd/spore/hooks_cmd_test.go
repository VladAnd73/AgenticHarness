package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHooksNotifyCoordinatorNoArgsUsesEnv(t *testing.T) {
	state := t.TempDir()
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", state)
	t.Setenv("WT_PROJECT", "project")
	t.Setenv("SPORE_TASK_INBOX", filepath.Join(t.TempDir(), "worker", "inbox"))

	if code := runHooksNotifyCoordinator(nil); code != 0 {
		t.Fatalf("runHooksNotifyCoordinator(nil) = %d, want 0", code)
	}
	entries, err := os.ReadDir(filepath.Join(state, "project", "inbox"))
	if err != nil {
		t.Fatalf("read coordinator inbox: %v", err)
	}
	found := false
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			found = true
		}
	}
	if !found {
		t.Fatal("notify-coordinator env mode did not write a json poke")
	}
}

func TestWatchInboxDirsFromEnv_NoInboxNil(t *testing.T) {
	t.Setenv("SPORE_TASK_INBOX", "")
	t.Setenv("SPORE_TASK_SLUG", "coordinator")
	if dirs := watchInboxDirsFromEnv(); dirs != nil {
		t.Fatalf("got %v, want nil when SPORE_TASK_INBOX unset", dirs)
	}
}

func TestWatchInboxDirsFromEnv_WorkerSingleDir(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "worker", "inbox")
	t.Setenv("SPORE_TASK_INBOX", inbox)
	t.Setenv("SPORE_TASK_SLUG", "rower-x")
	dirs := watchInboxDirsFromEnv()
	if len(dirs) != 1 || dirs[0] != inbox {
		t.Fatalf("worker got %v, want single [%s]", dirs, inbox)
	}
}

func TestWatchInboxDirsFromEnv_CoordinatorAddsMessageInbox(t *testing.T) {
	state := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("SPORE_PROJECT_ROOT", root)
	poke := filepath.Join(t.TempDir(), "coordinator", "spore", "inbox")
	t.Setenv("SPORE_TASK_INBOX", poke)
	t.Setenv("SPORE_TASK_SLUG", "coordinator")

	dirs := watchInboxDirsFromEnv()
	wantMsg := filepath.Join(state, "spore", "proj", "coordinator", "inbox")
	if len(dirs) != 2 || dirs[0] != poke || dirs[1] != wantMsg {
		t.Fatalf("coordinator got %v, want [%s %s]", dirs, poke, wantMsg)
	}
}

func TestWatchInboxDirsFromEnv_CoordinatorDedupWhenEqual(t *testing.T) {
	state := t.TempDir()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("SPORE_PROJECT_ROOT", root)
	msg := filepath.Join(state, "spore", "proj", "coordinator", "inbox")
	t.Setenv("SPORE_TASK_INBOX", msg)
	t.Setenv("SPORE_TASK_SLUG", "coordinator")

	dirs := watchInboxDirsFromEnv()
	if len(dirs) != 1 || dirs[0] != msg {
		t.Fatalf("dedup got %v, want single [%s]", dirs, msg)
	}
}

func TestHooksWatchInboxNoArgsNoEnvSilentNoOp(t *testing.T) {
	t.Setenv("SPORE_TASK_INBOX", "")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	code := runHooksWatchInbox(nil)
	w.Close()
	stderr, _ := io.ReadAll(r)

	if code != 0 {
		t.Fatalf("runHooksWatchInbox(nil) = %d, want 0 with no slug and no SPORE_TASK_INBOX", code)
	}
	if len(stderr) != 0 {
		t.Fatalf("runHooksWatchInbox(nil) wrote stderr %q, want empty", stderr)
	}
}
