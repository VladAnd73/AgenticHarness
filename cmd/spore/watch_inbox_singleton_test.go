package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Each Stop hook firing spawns a fresh watcher. Without a singleton the
// earlier ones live on for the full 7-day timeout, one per turn.
func TestWatchInboxStopHookFiringThriceLeavesOneWatcher(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not available: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "spore")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	inbox := filepath.Join(t.TempDir(), "inbox")
	env := append(os.Environ(),
		"SPORE_TASK_INBOX="+inbox,
		"SPORE_TASK_SLUG=worker-a",
		"WATCH_TIMEOUT=60",
	)

	var cmds []*exec.Cmd
	done := make([]chan error, 3)
	for i := range done {
		cmd := exec.Command(bin, "hooks", "watch-inbox")
		cmd.Env = env
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
		ch := make(chan error, 1)
		go func() { ch <- cmd.Wait() }()
		done[i] = ch
		time.Sleep(300 * time.Millisecond)
	}
	t.Cleanup(func() {
		for _, c := range cmds {
			_ = c.Process.Kill()
		}
	})

	for i := 0; i < 2; i++ {
		select {
		case err := <-done[i]:
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Errorf("superseded watcher %d exited %d, want 0 so the agent is not woken", i, exitErr.ExitCode())
			}
		case <-time.After(5 * time.Second):
			t.Errorf("watcher %d still running after a newer one started", i)
		}
	}
	select {
	case err := <-done[2]:
		t.Fatalf("newest watcher exited early: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}
