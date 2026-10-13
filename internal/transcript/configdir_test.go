package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

// Scenario 1, this site. A project that configures no directory, in a
// process that exports none, must keep reading ~/.claude/projects.
func TestFindFallbackTranscriptDefaultsToHomeClaude(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "proj")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PROJECT_DIR", cwd)
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	dir := filepath.Join(home, ".claude", "projects", encodeForTest(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := FindFallbackTranscript(); got != want {
		t.Errorf("FindFallbackTranscript() = %q, want %q", got, want)
	}
}

func encodeForTest(cwd string) string {
	out := []rune(cwd)
	for i, r := range out {
		if r == '/' {
			out[i] = '-'
		}
	}
	return string(out)
}

// The hook runs inside the agent, so the project it belongs to decides
// which config directory its transcript lives under.
func TestFindFallbackTranscriptHonoursTheProjectSetting(t *testing.T) {
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
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	t.Setenv("SPORE_PROJECT_ROOT", root)
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	dir := filepath.Join(cfg, "projects", encodeForTest(root))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := FindFallbackTranscript(); got != want {
		t.Errorf("FindFallbackTranscript() = %q, want %q", got, want)
	}
}
