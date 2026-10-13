package claudecfg

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTOML(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	if content != "" {
		if err := os.WriteFile(filepath.Join(root, "spore.toml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// Scenario 1, the operator's hard requirement at the resolver itself:
// a project that configures nothing, in a process that exports
// nothing, resolves the same ~/.claude it resolves today.
func TestDirDefaultsToHomeClaudeWhenNothingIsSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv(Env)

	for _, root := range []string{"", writeTOML(t, ""), writeTOML(t, "[coordinator]\nbrief = \"role.md\"\n")} {
		got, err := Dir(root)
		if err != nil {
			t.Fatalf("Dir(%q): %v", root, err)
		}
		if want := filepath.Join(home, ".claude"); got != want {
			t.Errorf("Dir(%q) = %q, want %q", root, got, want)
		}
	}
}

func TestDirUsesTheProjectSetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv(Env)

	root := writeTOML(t, "[claude]\nconfig_dir = \"/srv/cfg/assistant\"\n")
	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != "/srv/cfg/assistant" {
		t.Errorf("Dir = %q, want %q", got, "/srv/cfg/assistant")
	}
}

// The process env still decides for a project that says nothing, which
// is the shape PR #44 shipped in the inbox watchdog.
func TestDirFallsBackToTheProcessEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(Env, "/srv/cfg/ambient")

	got, err := Dir(writeTOML(t, ""))
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != "/srv/cfg/ambient" {
		t.Errorf("Dir = %q, want %q", got, "/srv/cfg/ambient")
	}
}

// Scenario 4. The reconciler judges an agent that is not its own, so
// the project's setting has to beat whatever the reconciler's own
// process happens to export. Without this precedence a `spore-fleet-tick`
// pass -- one shell walking every project in $HOME -- would hand the
// same directory to all of them.
func TestProjectSettingBeatsTheProcessEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(Env, "/srv/cfg/the-reconcilers-own")

	root := writeTOML(t, "[claude]\nconfig_dir = \"/srv/cfg/the-judged-project\"\n")
	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != "/srv/cfg/the-judged-project" {
		t.Errorf("Dir = %q, want %q", got, "/srv/cfg/the-judged-project")
	}
}

// Scenario 5. One process runs inside the agent and inherits the
// directory; another (the reconciler) has a bare environment. Both ask
// about the same project and must name the same directory.
func TestTwoProcessesAgreeOnTheSameProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := writeTOML(t, "[claude]\nconfig_dir = \"/srv/cfg/assistant\"\n")

	t.Setenv(Env, "/srv/cfg/assistant")
	inAgent, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir (in-agent): %v", err)
	}

	os.Unsetenv(Env)
	fromReconciler, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir (reconciler): %v", err)
	}

	if inAgent != fromReconciler {
		t.Errorf("in-agent %q != reconciler %q", inAgent, fromReconciler)
	}
}

func TestDirExpandsTildeAndProjectRelativePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv(Env)

	root := writeTOML(t, "[claude]\nconfig_dir = \"~/.claude-assistant\"\n")
	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join(home, ".claude-assistant"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}

	rel := writeTOML(t, "[claude]\nconfig_dir = \".claude\"\n")
	got, err = Dir(rel)
	if err != nil {
		t.Fatalf("Dir (relative): %v", err)
	}
	if want := filepath.Join(rel, ".claude"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

func TestProjectsDirHangsOffTheResolvedDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv(Env)

	root := writeTOML(t, "[claude]\nconfig_dir = \"/srv/cfg/assistant\"\n")
	got, err := ProjectsDir(root)
	if err != nil {
		t.Fatalf("ProjectsDir: %v", err)
	}
	if want := filepath.Join("/srv/cfg/assistant", "projects"); got != want {
		t.Errorf("ProjectsDir = %q, want %q", got, want)
	}
}

// An unknown key inside [claude] is a warning, never an error. Hosts
// run several spore versions at once, and failing the parse would hand
// a project running an older binary an empty config -- the exact
// regression PR #44 fixed for [coordinator].
func TestUnknownKeyIsIgnoredNotFatal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv(Env)

	root := writeTOML(t, "[claude]\nsomething_newer = \"x\"\nconfig_dir = \"/srv/cfg/a\"\n")
	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != "/srv/cfg/a" {
		t.Errorf("Dir = %q, want %q", got, "/srv/cfg/a")
	}
}

// ConfigDirSetting is what the spawn sites gate on: it reports only
// what the project asked for, so a project that asked for nothing gets
// no CLAUDE_CONFIG_DIR in its session env and keeps today's behaviour.
func TestConfigDirSettingIsEmptyWithoutTheKey(t *testing.T) {
	t.Setenv(Env, "/srv/cfg/ambient")

	got, err := ConfigDirSetting(writeTOML(t, "[coordinator]\nbrief = \"role.md\"\n"))
	if err != nil {
		t.Fatalf("ConfigDirSetting: %v", err)
	}
	if got != "" {
		t.Errorf("ConfigDirSetting = %q, want empty", got)
	}
}

func TestConfigDirSettingReportsTheResolvedPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := ConfigDirSetting(writeTOML(t, "[claude]\nconfig_dir = \"~/.claude-assistant\"\n"))
	if err != nil {
		t.Fatalf("ConfigDirSetting: %v", err)
	}
	if want := filepath.Join(home, ".claude-assistant"); got != want {
		t.Errorf("ConfigDirSetting = %q, want %q", got, want)
	}
}

func TestMissingProjectIsNotAnError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv(Env)

	got, err := Dir(filepath.Join(home, "no-such-project"))
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join(home, ".claude"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}
