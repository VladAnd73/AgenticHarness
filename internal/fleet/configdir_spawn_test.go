package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sessionEnv(t *testing.T, session, name string) (string, bool) {
	t.Helper()
	out, err := exec.Command("tmux", "-L", testTmuxSocket, "show-environment", "-t", session, name).CombinedOutput()
	line := strings.TrimSpace(string(out))
	if err != nil {
		return "", false
	}
	// tmux prints "-NAME" for a variable marked as removed.
	if strings.HasPrefix(line, "-") {
		return "", false
	}
	_, val, ok := strings.Cut(line, "=")
	if !ok {
		return "", false
	}
	return val, true
}

func coordinatorProject(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("SPORE_COORDINATOR_AGENT", "sleep 86400")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Unsetenv("SPORE_COORDINATOR_STATE_DIR")

	root := filepath.Join(home, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// Scenario 3. A coordinator spawned for a project that pinned a config
// directory carries it in its session environment, which is what the
// agent, every hook under it, and every `spore` call it makes inherit.
func TestCoordinatorSpawnCarriesTheConfigDir(t *testing.T) {
	root := coordinatorProject(t)
	cfgDir := filepath.Join(root, "..", "cfg-proj")
	if err := os.WriteFile(filepath.Join(root, "spore.toml"),
		[]byte("[claude]\nconfig_dir = \""+filepath.Clean(cfgDir)+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session, spawned, err := EnsureCoordinator(root)
	if err != nil {
		t.Fatalf("EnsureCoordinator: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})
	if !spawned {
		t.Fatal("expected a fresh spawn")
	}

	got, ok := sessionEnv(t, session, "CLAUDE_CONFIG_DIR")
	if !ok {
		t.Fatal("coordinator session carries no CLAUDE_CONFIG_DIR")
	}
	if want := filepath.Clean(cfgDir); got != want {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got, want)
	}
}

// The other half of the operator's hard requirement, at the spawn
// site: a project that pinned nothing must get no CLAUDE_CONFIG_DIR
// seeded at all, so its sessions inherit exactly what they inherit
// today.
func TestCoordinatorSpawnSeedsNothingWithoutTheSetting(t *testing.T) {
	root := coordinatorProject(t)

	session, _, err := EnsureCoordinator(root)
	if err != nil {
		t.Fatalf("EnsureCoordinator: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	if got, ok := sessionEnv(t, session, "CLAUDE_CONFIG_DIR"); ok {
		t.Errorf("coordinator session got CLAUDE_CONFIG_DIR=%q, want it unset", got)
	}
}
