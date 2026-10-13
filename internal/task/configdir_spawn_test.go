package task

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

// workerProject builds a real repo with one active task, ready to
// spawn. toml is written verbatim as the project's spore.toml; empty
// means the project configures nothing.
func workerProject(t *testing.T, toml string) (tasksDir, slug string) {
	t.Helper()
	for _, bin := range []string{"git", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available: %v", bin, err)
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("SPORE_AGENT_BINARY", "sleep 86400")
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	repo := filepath.Join(home, "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	if toml != "" {
		if err := os.WriteFile(filepath.Join(repo, "spore.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tasksDir = filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug = "demo"
	body := "---\nstatus: draft\nslug: demo\ntitle: Demo\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return tasksDir, slug
}

// Scenario 2. A worker spawned for a project that pinned a config
// directory carries it in its session environment. Both shims on an
// infected host (`spore-worker-brief`, `spore-coordinator-launch`)
// exec their agent without scrubbing the environment, so the seed at
// this site is what reaches claude itself.
func TestWorkerSpawnCarriesTheConfigDir(t *testing.T) {
	tasksDir, slug := workerProject(t, "[claude]\nconfig_dir = \"/srv/cfg/proj\"\n")

	session, err := Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	got, ok := sessionEnv(t, session, "CLAUDE_CONFIG_DIR")
	if !ok {
		t.Fatal("worker session carries no CLAUDE_CONFIG_DIR")
	}
	if got != "/srv/cfg/proj" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got, "/srv/cfg/proj")
	}
}

func TestWorkerSpawnSeedsNothingWithoutTheSetting(t *testing.T) {
	tasksDir, slug := workerProject(t, "")

	session, err := Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	if got, ok := sessionEnv(t, session, "CLAUDE_CONFIG_DIR"); ok {
		t.Errorf("worker session got CLAUDE_CONFIG_DIR=%q, want it unset", got)
	}
}
