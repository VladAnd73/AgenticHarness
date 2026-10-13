package budget

import (
	"os"
	"path/filepath"
	"testing"
)

// Scenario 1, the budget transcript corpus. No project setting and no
// process env must still resolve ~/.claude/projects.
func TestProjectsDirDefaultsToHomeClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("AGENT_BUDGET_PROJECTS")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Unsetenv("SPORE_PROJECT_ROOT")

	got, err := projectsDir()
	if err != nil {
		t.Fatalf("projectsDir: %v", err)
	}
	if want := filepath.Join(home, ".claude", "projects"); got != want {
		t.Errorf("projectsDir() = %q, want %q", got, want)
	}
}

// Scenario 1, the OAuth credential file.
func TestOAuthCredsPathDefaultsToHomeClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("AGENT_BUDGET_CREDS")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Unsetenv("SPORE_PROJECT_ROOT")

	got := oauthCredsPath()
	if want := filepath.Join(home, ".claude", ".credentials.json"); got != want {
		t.Errorf("oauthCredsPath() = %q, want %q", got, want)
	}
}

func TestProjectsDirHonoursTheProjectSetting(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spore.toml"),
		[]byte("[claude]\nconfig_dir = \"/srv/cfg/proj\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SPORE_PROJECT_ROOT", root)
	os.Unsetenv("AGENT_BUDGET_PROJECTS")
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	got, err := projectsDir()
	if err != nil {
		t.Fatalf("projectsDir: %v", err)
	}
	if want := filepath.Join("/srv/cfg/proj", "projects"); got != want {
		t.Errorf("projectsDir() = %q, want %q", got, want)
	}
}

func TestOAuthCredsPathHonoursTheProjectSetting(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "spore.toml"),
		[]byte("[claude]\nconfig_dir = \"/srv/cfg/proj\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SPORE_PROJECT_ROOT", root)
	os.Unsetenv("AGENT_BUDGET_CREDS")
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	got := oauthCredsPath()
	if want := filepath.Join("/srv/cfg/proj", ".credentials.json"); got != want {
		t.Errorf("oauthCredsPath() = %q, want %q", got, want)
	}
}
