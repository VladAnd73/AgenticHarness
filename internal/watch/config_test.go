package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func writeWatchToml(t *testing.T, dir, project, body string) {
	t.Helper()
	p := filepath.Join(dir, "spore", project)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "watch.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := LoadConfig("nope")
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if cfg.Enabled {
		t.Fatal("missing file must mean disabled")
	}
}

func TestLoadConfigParsesEnabledAndChecks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
# comment
enabled = true
checks = ["cypress", "playwright", "e2e"]
`)
	cfg, err := LoadConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("want enabled")
	}
	want := []string{"cypress", "playwright", "e2e"}
	if len(cfg.Checks) != len(want) {
		t.Fatalf("checks = %v, want %v", cfg.Checks, want)
	}
	for i := range want {
		if cfg.Checks[i] != want[i] {
			t.Fatalf("checks[%d] = %q, want %q", i, cfg.Checks[i], want[i])
		}
	}
}
