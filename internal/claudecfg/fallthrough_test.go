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
		"line past the ceiling": "[coordinator]\nbrief = \"" + strings.Repeat("x", maxTOMLLine+1) + "\"\n",
		"entry with no =":       "[coordinator]\nthis is not an entry\n",
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

// Scenario 2, called out on its own because it is strictly wider than
// "malformed": the file is valid TOML, the long line sits in a section
// this parser does not even look at, and there is no [claude] section
// anywhere. Only bufio.Scanner's 64KB token ceiling turns it into a
// failure. On main none of the read sites opened spore.toml at all, so
// this project must still resolve ~/.claude.
func TestDirFallsThroughWhenAnUnrelatedSectionHasAnOversizedLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv(Env)

	want := filepath.Join(home, ".claude")
	for name, size := range map[string]int{
		"past bufio.Scanner's default ceiling": 70000,
		"past this parser's ceiling":           maxTOMLLine + 1,
	} {
		root := writeTOML(t, "[other]\nnote = \""+strings.Repeat("x", size)+"\"\n")
		got, err := Dir(root)
		if err != nil {
			t.Errorf("%s: Dir: %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("%s: Dir = %q, want %q", name, got, want)
		}
	}
}

// The residual the ceiling leaves behind once Dir is tolerant: a
// project that DID configure a directory loses it to a long line in a
// section that has nothing to do with it. Falling through is right for
// a project that set nothing and wrong here -- the setting is present
// and readable, and the resolver must not quietly resolve somewhere
// else.
func TestAnOversizedUnrelatedLineDoesNotLoseTheSetting(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.Unsetenv(Env)

	root := writeTOML(t, "[other]\nnote = \""+strings.Repeat("x", 70000)+"\"\n"+
		"\n[claude]\nconfig_dir = \"/srv/cfg/assistant\"\n")
	got, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if got != "/srv/cfg/assistant" {
		t.Errorf("Dir = %q, want %q: a long line elsewhere lost the setting", got, "/srv/cfg/assistant")
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
