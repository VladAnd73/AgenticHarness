package watch

import (
	"os"
	"path/filepath"
	"strings"
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

// The CLI told an operator whose project was disabled where to enable
// it by recomputing this path itself (cmd/spore's watchTomlPath). A
// second, independent copy of this resolution drifting from the one
// readWatchToml actually uses would send an operator to edit a file
// nothing reads.
func TestTomlPathMatchesWhereLoadDreamsConfigReads(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := TomlPath("proj")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[dreams]\nenabled = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatalf("LoadDreamsConfig did not read the file TomlPath named: %s", path)
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

func TestLoadDreamsConfigReadsTable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
enabled = true

[dreams]
enabled = true
deep_read_cap = 5
max_writes_per_run = 20
recurrence_threshold = 3
`)
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	want := DreamsConfig{Enabled: true, DeepReadCap: 5, MaxWritesPerRun: 20, RecurrenceThreshold: 3}
	if got != want {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

func TestLoadDreamsConfigDefaultsWhenAbsent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatalf("missing file must not error, got %v", err)
	}
	if got.Enabled {
		t.Fatal("a missing [dreams] table must mean disabled")
	}
	want := DreamsConfig{DeepReadCap: 5, MaxWritesPerRun: 10, RecurrenceThreshold: 2}
	if got != want {
		t.Fatalf("defaults not applied: %+v, want %+v", got, want)
	}
}

func TestLoadDreamsConfigEnabledOnlyKeepsNumericDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", "[dreams]\nenabled = true\n")
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	want := DreamsConfig{Enabled: true, DeepReadCap: 5, MaxWritesPerRun: 10, RecurrenceThreshold: 2}
	if got != want {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

func TestDreamsTableLeavesExistingLoadersUntouched(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
enabled = true
checks = ["regression", "e2e#smoke"] # trailing comment

[releases]
enabled = true
repos = ["org/one", "org/two"]
coordinators = ["proj"]
instruction = "Sync the KB."

[dreams]
enabled = true
deep_read_cap = 5
`)
	cfg, err := LoadConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("top-level enabled must survive the dreams table")
	}
	if len(cfg.Checks) != 2 || cfg.Checks[0] != "regression" || cfg.Checks[1] != "e2e#smoke" {
		t.Fatalf("checks = %v, want [regression e2e#smoke]", cfg.Checks)
	}

	rel, err := LoadReleasesConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !rel.Enabled {
		t.Fatal("releases enabled must survive the dreams table")
	}
	if len(rel.Repos) != 2 || rel.Repos[0] != "org/one" || rel.Repos[1] != "org/two" {
		t.Fatalf("repos = %v, want [org/one org/two]", rel.Repos)
	}
	if len(rel.Coordinators) != 1 || rel.Coordinators[0] != "proj" {
		t.Fatalf("coordinators = %v, want [proj]", rel.Coordinators)
	}
	if rel.Instruction != "Sync the KB." {
		t.Fatalf("instruction = %q, want %q", rel.Instruction, "Sync the KB.")
	}
}

// A cautious operator may quote the boolean, and TOML allows a trailing
// comment. Both are the same value as a bare true; neither may silently
// disable the feature.
func TestLoadDreamsConfigAcceptsQuotedAndCommentedEnabled(t *testing.T) {
	for _, line := range []string{
		`enabled = "true"`,
		`enabled = 'true'`,
		`enabled = true # nightly`,
		`enabled=true`,
		`enabled =    true`,
	} {
		t.Run(line, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			writeWatchToml(t, dir, "proj", "[dreams]\n"+line+"\n")
			got, err := LoadDreamsConfig("proj")
			if err != nil {
				t.Fatal(err)
			}
			if !got.Enabled {
				t.Fatalf("%s must enable dreaming, got %+v", line, got)
			}
		})
	}
}

// A typo in a knob must surface, not fall back to a default. An unattended
// feature that writes to memory files gives the operator no other signal.
func TestLoadDreamsConfigRejectsUnparseableAndNegativeKnobs(t *testing.T) {
	for _, body := range []string{
		"[dreams]\nenabled = true\ndeep_read_cap = banana\n",
		"[dreams]\nenabled = true\ndeep_read_cap = 3.5\n",
		"[dreams]\nenabled = true\ndeep_read_cap = -1\n",
		"[dreams]\nenabled = true\nmax_writes_per_run = -10\n",
		"[dreams]\nenabled = true\nrecurrence_threshold = lots\n",
	} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			writeWatchToml(t, dir, "proj", body)
			_, err := LoadDreamsConfig("proj")
			if err == nil {
				t.Fatalf("want an error for %q", body)
			}
			if !strings.Contains(err.Error(), "dreams") {
				t.Fatalf("error %q must name the table", err)
			}
		})
	}
}

// Zero is a legal setting, not a typo: it means "do none of this".
func TestLoadDreamsConfigAcceptsZeroKnobs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
[dreams]
enabled = true
deep_read_cap = 0
max_writes_per_run = 0
recurrence_threshold = 0
`)
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	want := DreamsConfig{Enabled: true}
	if got != want {
		t.Fatalf("config = %+v, want %+v", got, want)
	}
}

func TestLoadConfigPreservesHashInQuotedValue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	writeWatchToml(t, dir, "proj", `
enabled = true
checks = ["e2e#smoke"] # comment with hash
`)
	cfg, err := LoadConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Checks) != 1 {
		t.Fatalf("checks = %v, want 1 element", cfg.Checks)
	}
	if cfg.Checks[0] != "e2e#smoke" {
		t.Fatalf("checks[0] = %q, want %q", cfg.Checks[0], "e2e#smoke")
	}
}
