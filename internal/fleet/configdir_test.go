package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

// Scenario 1, the inbox watchdog's liveness probe. PR #44 taught this
// site to honour CLAUDE_CONFIG_DIR; with neither that nor a project
// setting it must still land on ~/.claude.
func TestClaudeConfigDirDefaultsToHomeClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	root := filepath.Join(home, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := claudeConfigDir(root)
	if err != nil {
		t.Fatalf("claudeConfigDir: %v", err)
	}
	if want := filepath.Join(home, ".claude"); got != want {
		t.Errorf("claudeConfigDir = %q, want %q", got, want)
	}
}

// Scenario 4. The reconciler runs from a systemd unit with its own
// environment, and `spore-fleet-tick` walks every project in $HOME
// from one shell. Asked about a project that pinned a directory, it
// must answer with that project's, not with its own.
func TestClaudeConfigDirResolvesTheJudgedProjectNotTheReconcilers(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "judged")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spore.toml"),
		[]byte("[claude]\nconfig_dir = \"/srv/cfg/judged\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "/srv/cfg/the-reconcilers-own")

	got, err := claudeConfigDir(root)
	if err != nil {
		t.Fatalf("claudeConfigDir: %v", err)
	}
	if got != "/srv/cfg/judged" {
		t.Errorf("claudeConfigDir = %q, want %q", got, "/srv/cfg/judged")
	}
}
