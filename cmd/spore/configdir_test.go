package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Scenario 1, the dream corpus. With no --transcripts flag, no project
// setting and no process env, dream must keep reading ~/.claude/projects.
func TestDreamTranscriptsDefaultsToHomeClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	root := filepath.Join(home, "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	got := dreamTranscripts("", root)
	if want := filepath.Join(home, ".claude", "projects"); got != want {
		t.Errorf("dreamTranscripts = %q, want %q", got, want)
	}
}

// The nightly dream runs from a systemd unit whose Environment= is
// literally empty, so the project it digests is the only thing that
// can name its corpus.
func TestDreamTranscriptsHonoursTheProjectSetting(t *testing.T) {
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
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	got := dreamTranscripts("", root)
	if want := filepath.Join("/srv/cfg/proj", "projects"); got != want {
		t.Errorf("dreamTranscripts = %q, want %q", got, want)
	}
}
