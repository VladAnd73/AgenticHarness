package watch

import "testing"

func TestLoadReleasesConfigParsesSection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
enabled = true
checks = ["e2e"]

[releases]
enabled = true
repos = ["o/backend", "o/frontend"]
coordinators = ["frontend"]
`)
	cfg, err := LoadReleasesConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("want releases enabled")
	}
	wantRepos := []string{"o/backend", "o/frontend"}
	if len(cfg.Repos) != len(wantRepos) {
		t.Fatalf("repos = %v, want %v", cfg.Repos, wantRepos)
	}
	for i := range wantRepos {
		if cfg.Repos[i] != wantRepos[i] {
			t.Fatalf("repos[%d] = %q, want %q", i, cfg.Repos[i], wantRepos[i])
		}
	}
	if len(cfg.Coordinators) != 1 || cfg.Coordinators[0] != "frontend" {
		t.Fatalf("coordinators = %v, want [frontend]", cfg.Coordinators)
	}
}

func TestLoadReleasesConfigMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := LoadReleasesConfig("nope")
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if cfg.Enabled {
		t.Fatal("missing file must mean disabled")
	}
}

// The releases section's keys must not leak into the flat pr-watch Config.
// A watch.toml that enables ONLY releases must leave pr-watch disabled.
func TestReleasesSectionDoesNotLeakIntoPRConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
[releases]
enabled = true
repos = ["o/backend"]
coordinators = ["frontend"]
`)
	cfg, err := LoadConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("pr-watch must stay disabled when only [releases] enables")
	}
}
