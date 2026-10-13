package verify

import (
	"os"
	"path/filepath"
	"testing"
)

// Scenario 1, the done-gate's evidence reader. With no --projects
// flag, no project setting and no process env, it must keep globbing
// ~/.claude/projects.
func TestFindSessionFileDefaultsToHomeClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	dir := filepath.Join(home, ".claude", "projects", "-home-spore-proj--worktrees-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := findSessionFile("demo", "", home); got != want {
		t.Errorf("findSessionFile = %q, want %q", got, want)
	}
}

// The done-gate runs in the coordinator, which may be judging a
// worktree of a project whose agents keep their transcripts elsewhere.
func TestFindSessionFileHonoursTheProjectSetting(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	cfg := filepath.Join(home, "cfg-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spore.toml"),
		[]byte("[claude]\nconfig_dir = \""+cfg+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	dir := filepath.Join(cfg, "projects", "-home-spore-proj--worktrees-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := findSessionFile("demo", "", root); got != want {
		t.Errorf("findSessionFile = %q, want %q", got, want)
	}
}
