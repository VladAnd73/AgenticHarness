package claudecfg

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr replaced by a pipe and returns
// what fn wrote there.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = saved
	w.Close()
	out := <-done
	r.Close()
	return out
}

// Scenario 1. A spore.toml that cannot be read is a failure of the
// per-project lookup, not of resolution: the call sites this package
// replaced never read spore.toml at all, so they could not fail this
// way. A project with no [claude] section must keep resolving
// ~/.claude whatever state its spore.toml is in.
func TestDirFallsThroughWhenSporeTOMLIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0o000 is still readable")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv(Env)

	root := writeTOML(t, "[coordinator]\nbrief = \"role.md\"\n")
	if err := os.Chmod(filepath.Join(root, "spore.toml"), 0o000); err != nil {
		t.Fatal(err)
	}

	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join(home, ".claude"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

// Scenario 1, continued: the process env is still what decides for a
// project that configured nothing, so an unreadable spore.toml must
// fall through to it and not past it.
func TestDirFallsThroughToTheProcessEnvWhenSporeTOMLIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0o000 is still readable")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(Env, "/srv/cfg/ambient")

	root := writeTOML(t, "[coordinator]\nbrief = \"role.md\"\n")
	if err := os.Chmod(filepath.Join(root, "spore.toml"), 0o000); err != nil {
		t.Fatal(err)
	}

	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != "/srv/cfg/ambient" {
		t.Errorf("Dir = %q, want %q", got, "/srv/cfg/ambient")
	}
}

// Scenario 2. A spore.toml the parser chokes on, with no [claude]
// section anywhere in it, must not break resolution either. A single
// line past bufio.Scanner's 64KB token ceiling is the case that
// reaches scanner.Err() regardless of which section it sits in.
func TestDirFallsThroughWhenSporeTOMLIsMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv(Env)
	want := filepath.Join(home, ".claude")

	for name, content := range map[string]string{
		"oversized line":  "[coordinator]\nbrief = \"" + strings.Repeat("x", 70000) + "\"\n",
		"entry with no =": "[coordinator]\nthis is not an entry\n",
	} {
		got, err := Dir(writeTOML(t, content))
		if err != nil {
			t.Errorf("%s: Dir: %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("%s: Dir = %q, want %q", name, got, want)
		}
	}
}

// Scenario 3. Tolerance at Dir must not reach the happy path: a
// project that did configure a directory still gets it, ahead of both
// the env and the default.
func TestDirStillPrefersAValidProjectSetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(Env, "/srv/cfg/ambient")

	root := writeTOML(t, "[claude]\nconfig_dir = \"/srv/cfg/assistant\"\n")
	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != "/srv/cfg/assistant" {
		t.Errorf("Dir = %q, want %q", got, "/srv/cfg/assistant")
	}
}

// A fallback that is silent forever masks a real typo forever. Dir
// says what it ignored, on the same stderr channel the unknown-key
// warning already uses.
func TestDirWarnsAboutTheSettingItCouldNotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0o000 is still readable")
	}
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv(Env)

	root := writeTOML(t, "[coordinator]\nbrief = \"role.md\"\n")
	if err := os.Chmod(filepath.Join(root, "spore.toml"), 0o000); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() {
		if _, err := Dir(root); err != nil {
			t.Errorf("Dir: %v", err)
		}
	})
	if !strings.Contains(out, "spore.toml") {
		t.Errorf("stderr %q names neither the file it could not read nor the fallback", out)
	}
}

// ConfigDirSetting stays strict. A caller that wants the failure loud
// -- a bootstrap check, an operator-facing command -- still gets it;
// only Dir, which every read site goes through, is tolerant.
func TestConfigDirSettingStillReportsTheFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0o000 is still readable")
	}
	root := writeTOML(t, "[coordinator]\nbrief = \"role.md\"\n")
	if err := os.Chmod(filepath.Join(root, "spore.toml"), 0o000); err != nil {
		t.Fatal(err)
	}

	if _, err := ConfigDirSetting(root); err == nil {
		t.Fatal("ConfigDirSetting swallowed an unreadable spore.toml")
	}
}
