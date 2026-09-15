package watch

import "testing"

func TestLoadSlackConfigParsesSection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
enabled = true
checks = ["e2e"]

[slack]
enabled = true
channel_id = "C0BTMKY5AE4"
`)
	cfg, err := LoadSlackConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("want slack enabled")
	}
	if cfg.ChannelID != "C0BTMKY5AE4" {
		t.Fatalf("channel_id = %q, want C0BTMKY5AE4", cfg.ChannelID)
	}
}

func TestLoadSlackConfigMissingSectionIsDisabled(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `enabled = true`)
	cfg, err := LoadSlackConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("absent [slack] section must be disabled")
	}
}

func TestLoadSlackConfigExplicitlyDisabledStillParsesChannelID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
[slack]
enabled = false
channel_id = "C0BTMKY5AE4"
`)
	cfg, err := LoadSlackConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("explicit enabled = false must be disabled")
	}
	if cfg.ChannelID != "C0BTMKY5AE4" {
		t.Fatalf("channel_id = %q, want C0BTMKY5AE4", cfg.ChannelID)
	}
}
