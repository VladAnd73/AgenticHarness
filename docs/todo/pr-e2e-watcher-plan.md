# PR e2e Watcher Implementation Plan

**Status**: ready for execution. Spec: `docs/todo/pr-e2e-watcher.md`.
Branch: `evolve_Vlad_harness`, local-only, never pushed.

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `spore watch prs` polls open PRs for failing e2e checks and
wakes the project coordinator with a self-contained triage runbook.

**Architecture:** New `internal/watch` package (config loader,
seen-state dedup store, gh JSON client, orchestrator) + a thin
`cmd/spore/watch_cmd.go`. Delivery to the coordinator reuses
`task.Tell`. Scheduling is a hand-installed user systemd timer on
this host only (no nix module work, feature is not released).

**Tech Stack:** Go stdlib only (os/exec + encoding/json), `gh` CLI at
runtime, systemd user units.

## Global Constraints

- ASCII only; no em-dashes anywhere (lint enforces).
- Comments only where the code cannot say it (comment-noise lint).
- Max 800 lines per file (file-size lint).
- Go toolchain only via `nix develop -c` on this host.
- After each task: `nix develop -c go test ./internal/watch/` and
  `spore lint` must be green before commit.
- Commits: `git -c user.name='Claude (spore)' -c
  user.email='crm-service-harness-aaaaudc5h5f7mhwdmhjaueuz2m@marketer-grid.org.slack.com'
  commit ...`. Never `git config user.*`. Never push.
- All work on branch `evolve_Vlad_harness`.
- No external write actions except the two live spikes in Task 6,
  which the coordinator runs itself (not a worker).

---

### Task 1: watch config loader

**Files:**
- Create: `internal/watch/config.go`
- Test: `internal/watch/config_test.go`

**Interfaces:**
- Produces: `watch.Config{Enabled bool; Checks []string}` and
  `watch.LoadConfig(project string) (Config, error)`. Reads
  `$XDG_CONFIG_HOME/spore/<project>/watch.toml` (fallback
  `~/.config/spore/<project>/watch.toml`). Missing file returns
  zero Config, nil error.

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/watch/ -run TestLoadConfig -v`
Expected: FAIL (package does not compile: LoadConfig undefined)

- [ ] **Step 3: Write minimal implementation**

```go
package watch

import (
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Enabled bool
	Checks  []string
}

func LoadConfig(project string) (Config, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	b, err := os.ReadFile(filepath.Join(base, "spore", project, "watch.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "enabled":
			cfg.Enabled = val == "true"
		case "checks":
			cfg.Checks = parseStringList(val)
		}
	}
	return cfg, nil
}

func parseStringList(val string) []string {
	val = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(val), "]"), "[")
	var out []string
	for _, part := range strings.Split(val, ",") {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/watch/ -run TestLoadConfig -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add internal/watch/config.go internal/watch/config_test.go
git -c user.name='Claude (spore)' -c user.email='crm-service-harness-aaaaudc5h5f7mhwdmhjaueuz2m@marketer-grid.org.slack.com' \
  commit -m "feat(watch): per-project watch.toml config loader"
```

---

### Task 2: seen-state store

**Files:**
- Create: `internal/watch/seen.go`
- Test: `internal/watch/seen_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces:
  - `watch.Key(pr int, sha, check string) string` -> `"<pr>:<sha>:<check>"`
  - `watch.LoadState(project string) (*State, error)` from
    `$XDG_STATE_HOME/spore/<project>/pr-watch.json` (fallback
    `~/.local/state/...`); missing file -> empty state.
  - `(*State).SeenKey(key string) bool`, `(*State).MarkKey(key string)`
  - `(*State).Prune(liveKeys map[string]bool)` drops entries whose
    key is not live (closed PRs, superseded commits).
  - `(*State).Failures int` field (consecutive gh failure counter,
    used by Task 4).
  - `(*State).Save() error` writes back to the same path.

- [ ] **Step 1: Write the failing test**

```go
package watch

import (
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	k := Key(42, "abc123", "cypress-run")
	if st.SeenKey(k) {
		t.Fatal("fresh state must not contain key")
	}
	st.MarkKey(k)
	st.Failures = 2
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	st2, err := LoadState("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !st2.SeenKey(k) {
		t.Fatal("key must survive reload")
	}
	if st2.Failures != 2 {
		t.Fatalf("failures = %d, want 2", st2.Failures)
	}
}

func TestStatePrune(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st, _ := LoadState("proj")
	live := Key(1, "aaa", "e2e")
	dead := Key(2, "bbb", "e2e")
	st.MarkKey(live)
	st.MarkKey(dead)
	st.Prune(map[string]bool{live: true})
	if !st.SeenKey(live) {
		t.Fatal("live key pruned")
	}
	if st.SeenKey(dead) {
		t.Fatal("dead key survived prune")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/watch/ -run TestState -v`
Expected: FAIL (LoadState, Key undefined)

- [ ] **Step 3: Write minimal implementation**

```go
package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	Failures int               `json:"failures"`
	Seen     map[string]string `json:"seen"`

	path string
}

func Key(pr int, sha, check string) string {
	return fmt.Sprintf("%d:%s:%s", pr, sha, check)
}

func statePath(project string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("neither XDG_STATE_HOME nor HOME set")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "spore", project, "pr-watch.json"), nil
}

func LoadState(project string) (*State, error) {
	p, err := statePath(project)
	if err != nil {
		return nil, err
	}
	st := &State{Seen: map[string]string{}, path: p}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w", p, err)
	}
	if st.Seen == nil {
		st.Seen = map[string]string{}
	}
	st.path = p
	return st, nil
}

func (s *State) SeenKey(key string) bool {
	_, ok := s.Seen[key]
	return ok
}

func (s *State) MarkKey(key string) {
	s.Seen[key] = time.Now().UTC().Format(time.RFC3339)
}

func (s *State) Prune(liveKeys map[string]bool) {
	for k := range s.Seen {
		if !liveKeys[k] {
			delete(s.Seen, k)
		}
	}
}

func (s *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/watch/ -run TestState -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add internal/watch/seen.go internal/watch/seen_test.go
git -c user.name='Claude (spore)' -c user.email='crm-service-harness-aaaaudc5h5f7mhwdmhjaueuz2m@marketer-grid.org.slack.com' \
  commit -m "feat(watch): pr-watch.json seen-state store with prune"
```

---

### Task 3: gh JSON client

**Files:**
- Create: `internal/watch/github.go`
- Test: `internal/watch/github_test.go`

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces:
  - `watch.PR{Number int; Branch string; HeadSHA string; IsDraft bool; URL string}`
  - `watch.CheckRun{Name string; Bucket string; Link string}`
  - `watch.OpenPRs(projectRoot string) ([]PR, error)` via
    `gh pr list --state open --json number,headRefName,headRefOid,isDraft,url`
  - `watch.FailingChecks(projectRoot string, pr int) ([]CheckRun, error)`
    via `gh pr checks <pr> --json name,bucket,link`, keeping only
    `Bucket == "fail"`.
  - Test seam: env var `SPORE_GH_BINARY` overrides the `gh` binary
    (same pattern as `SPORE_AGENT_BINARY` in internal/fleet tests).

- [ ] **Step 1: Write the failing test**

The fake gh is a shell script that echoes canned JSON depending on
its first argument.

```go
package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeGH(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "gh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPORE_GH_BINARY", p)
}

func TestOpenPRs(t *testing.T) {
	fakeGH(t, `echo '[{"number":42,"headRefName":"fix-login","headRefOid":"abc123","isDraft":false,"url":"https://github.com/o/r/pull/42"}]'`)
	prs, err := OpenPRs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 42 || prs[0].Branch != "fix-login" || prs[0].HeadSHA != "abc123" {
		t.Fatalf("bad parse: %+v", prs)
	}
}

func TestFailingChecksKeepsOnlyFailBucket(t *testing.T) {
	fakeGH(t, `echo '[{"name":"cypress-run","bucket":"fail","link":"https://github.com/o/r/actions/runs/9/job/1"},{"name":"lint","bucket":"pass","link":""}]'`)
	checks, err := FailingChecks(t.TempDir(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Name != "cypress-run" {
		t.Fatalf("bad filter: %+v", checks)
	}
}

func TestGHErrorPropagates(t *testing.T) {
	fakeGH(t, `exit 1`)
	if _, err := OpenPRs(t.TempDir()); err == nil {
		t.Fatal("want error from failing gh")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/watch/ -run 'TestOpenPRs|TestFailingChecks|TestGHError' -v`
Expected: FAIL (OpenPRs, FailingChecks undefined)

- [ ] **Step 3: Write minimal implementation**

```go
package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

type PR struct {
	Number  int    `json:"number"`
	Branch  string `json:"headRefName"`
	HeadSHA string `json:"headRefOid"`
	IsDraft bool   `json:"isDraft"`
	URL     string `json:"url"`
}

type CheckRun struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
	Link   string `json:"link"`
}

func ghJSON(projectRoot string, out any, args ...string) error {
	bin := os.Getenv("SPORE_GH_BINARY")
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = projectRoot
	b, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gh %v: %w", args, err)
	}
	return json.Unmarshal(b, out)
}

func OpenPRs(projectRoot string) ([]PR, error) {
	var prs []PR
	err := ghJSON(projectRoot, &prs, "pr", "list", "--state", "open",
		"--json", "number,headRefName,headRefOid,isDraft,url")
	return prs, err
}

func FailingChecks(projectRoot string, pr int) ([]CheckRun, error) {
	var all []CheckRun
	err := ghJSON(projectRoot, &all, "pr", "checks", strconv.Itoa(pr),
		"--json", "name,bucket,link")
	if err != nil {
		return nil, err
	}
	var failing []CheckRun
	for _, c := range all {
		if c.Bucket == "fail" {
			failing = append(failing, c)
		}
	}
	return failing, nil
}
```

Note: `gh pr checks` exits non-zero when checks are failing; that is
expected, not an error. Handle it: if `err` is an `*exec.ExitError`
and stdout parsed as JSON, proceed. Concretely, in `ghJSON` capture
stdout via `cmd.Output()`, and on `*exec.ExitError` with non-empty
stdout attempt the unmarshal anyway; only return the error if
unmarshal also fails. Add this test:

```go
func TestFailingChecksNonZeroExitStillParses(t *testing.T) {
	fakeGH(t, `echo '[{"name":"e2e","bucket":"fail","link":"x"}]'; exit 8`)
	checks, err := FailingChecks(t.TempDir(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 {
		t.Fatalf("want 1 failing check, got %+v", checks)
	}
}
```

`ghJSON` with the exit-code tolerance:

```go
func ghJSON(projectRoot string, out any, args ...string) error {
	bin := os.Getenv("SPORE_GH_BINARY")
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = projectRoot
	b, err := cmd.Output()
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit || len(b) == 0 {
			return fmt.Errorf("gh %v: %w", args, err)
		}
	}
	if jerr := json.Unmarshal(b, out); jerr != nil {
		if err != nil {
			return fmt.Errorf("gh %v: %w", args, err)
		}
		return fmt.Errorf("gh %v: bad json: %w", args, jerr)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/watch/ -v`
Expected: PASS (all watch tests so far)

- [ ] **Step 5: Commit**

```bash
git add internal/watch/github.go internal/watch/github_test.go
git -c user.name='Claude (spore)' -c user.email='crm-service-harness-aaaaudc5h5f7mhwdmhjaueuz2m@marketer-grid.org.slack.com' \
  commit -m "feat(watch): gh JSON client for open PRs and failing checks"
```

---

### Task 4: orchestrator

**Files:**
- Create: `internal/watch/watch.go`
- Test: `internal/watch/watch_test.go`

**Interfaces:**
- Consumes: `LoadConfig` (Task 1), `LoadState`/`Key` (Task 2),
  `OpenPRs`/`FailingChecks` (Task 3).
- Produces: `watch.Run(projectRoot, project string, dryRun bool,
  tell func(slug, msg string) error) (Report, error)` with
  `Report{Alerts int; Skipped int}`. The CLI (Task 5) passes
  `task.Tell` as `tell`.
- Behavior contract (mirrors spec):
  - config missing/disabled -> Report{}, nil, no gh calls.
  - drafts skipped; check-name match is case-insensitive substring
    against `cfg.Checks`.
  - seen key -> skip silently. New key -> mark, save state, tell.
  - gh error -> increment `State.Failures`, save; at exactly 3
    consecutive failures, tell coordinator once about ill health.
    Any successful round resets `Failures` to 0.
  - Prune state to live keys each successful round.
  - dryRun: report what would alert, but no tell and no state write.

- [ ] **Step 1: Write the failing test**

```go
package watch

import (
	"fmt"
	"strings"
	"testing"
)

type told struct {
	slug, msg string
}

func setupRun(t *testing.T, ghScript string) (dir string, tells *[]told, tell func(string, string) error) {
	t.Helper()
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	writeWatchToml(t, cfgDir, "proj", "enabled = true\nchecks = [\"cypress\", \"e2e\"]\n")
	fakeGH(t, ghScript)
	var got []told
	return t.TempDir(), &got, func(slug, msg string) error {
		got = append(got, told{slug, msg})
		return nil
	}
}

const oneFailingPR = `
case "$2" in
list) echo '[{"number":7,"headRefName":"feat-x","headRefOid":"sha1","isDraft":false,"url":"https://github.com/o/r/pull/7"}]' ;;
checks) echo '[{"name":"cypress-run","bucket":"fail","link":"https://github.com/o/r/actions/runs/11/job/2"}]'; exit 8 ;;
esac`

func TestRunAlertsOnceAndDedups(t *testing.T) {
	root, tells, tell := setupRun(t, oneFailingPR)
	rep, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Alerts != 1 || len(*tells) != 1 {
		t.Fatalf("want 1 alert, got %+v / %d tells", rep, len(*tells))
	}
	if (*tells)[0].slug != "coordinator" {
		t.Fatalf("told %q, want coordinator", (*tells)[0].slug)
	}
	for _, want := range []string{"PR #7", "feat-x", "cypress-run", "runs/11"} {
		if !strings.Contains((*tells)[0].msg, want) {
			t.Fatalf("msg missing %q:\n%s", want, (*tells)[0].msg)
		}
	}
	rep2, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Alerts != 0 || len(*tells) != 1 {
		t.Fatal("second round must be silent (dedup)")
	}
}

func TestRunDisabledDoesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fakeGH(t, "exit 99")
	rep, err := Run(t.TempDir(), "proj", false, func(string, string) error {
		t.Fatal("must not tell")
		return nil
	})
	if err != nil || rep.Alerts != 0 {
		t.Fatalf("disabled run: %+v, %v", rep, err)
	}
}

func TestRunIllHealthAfterThreeFailures(t *testing.T) {
	root, tells, tell := setupRun(t, "exit 1")
	for i := 0; i < 3; i++ {
		if _, err := Run(root, "proj", false, tell); err == nil {
			t.Fatalf("round %d: want error", i)
		}
	}
	if len(*tells) != 1 || !strings.Contains((*tells)[0].msg, "unhealthy") {
		t.Fatalf("want exactly one ill-health tell, got %v", *tells)
	}
	if _, err := Run(root, "proj", false, tell); err == nil {
		t.Fatal("still failing")
	}
	if len(*tells) != 1 {
		t.Fatal("must not re-alert ill health every round")
	}
}

func TestRunNonMatchingCheckIgnored(t *testing.T) {
	root, tells, tell := setupRun(t, `
case "$2" in
list) echo '[{"number":8,"headRefName":"y","headRefOid":"s","isDraft":false,"url":"u"}]' ;;
checks) echo '[{"name":"unit-tests","bucket":"fail","link":"l"}]'; exit 8 ;;
esac`)
	rep, err := Run(root, "proj", false, tell)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Alerts != 0 || len(*tells) != 0 {
		t.Fatal("non-matching check must not alert")
	}
}
```

The test file also needs `"fmt"` dropped from imports if unused
after assembly; goimports via `nix develop -c gofmt -l` will say.

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/watch/ -run TestRun -v`
Expected: FAIL (Run undefined)

- [ ] **Step 3: Write minimal implementation**

```go
package watch

import (
	"fmt"
	"strings"
)

type Report struct {
	Alerts  int
	Skipped int
}

const runbook = `Runbook (per approved spec docs/todo/pr-e2e-watcher.md in the spore repo):
1. Mint a worker task: fetch origin, check out the PR branch, pull, run the FAILED specs locally (use the project's running-*-e2e / investigating-*-e2e-failures skills).
2. Worker tells you the local result.
3. Local PASS -> retry CI once: spore-with-secrets gh run rerun <run-id from the link> --failed. Watch the rerun; green = done silently, red = escalate to operator.
4. Local FAIL -> escalate to operator in your terminal with the worker's findings. Do not retry.
Max ONE CI retry per commit. Escalate instead of looping.`

func Run(projectRoot, project string, dryRun bool, tell func(slug, msg string) error) (Report, error) {
	var rep Report
	cfg, err := LoadConfig(project)
	if err != nil || !cfg.Enabled {
		return rep, err
	}
	st, err := LoadState(project)
	if err != nil {
		return rep, err
	}
	prs, err := OpenPRs(projectRoot)
	if err != nil {
		return rep, noteFailure(st, tell, err)
	}
	live := map[string]bool{}
	type alert struct {
		key string
		msg string
	}
	var alerts []alert
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		checks, err := FailingChecks(projectRoot, pr.Number)
		if err != nil {
			return rep, noteFailure(st, tell, err)
		}
		for _, c := range checks {
			if !nameMatches(c.Name, cfg.Checks) {
				continue
			}
			k := Key(pr.Number, pr.HeadSHA, c.Name)
			live[k] = true
			if st.SeenKey(k) {
				rep.Skipped++
				continue
			}
			msg := fmt.Sprintf(
				"pr-watch: PR #%d (%s) has failing check %q\nrun: %s\npr: %s\n\n%s",
				pr.Number, pr.Branch, c.Name, c.Link, pr.URL, runbook)
			alerts = append(alerts, alert{k, msg})
		}
	}
	if dryRun {
		rep.Alerts = len(alerts)
		return rep, nil
	}
	for _, a := range alerts {
		if err := tell("coordinator", a.msg); err != nil {
			return rep, err
		}
		st.MarkKey(a.key)
		rep.Alerts++
	}
	st.Prune(live)
	st.Failures = 0
	return rep, st.Save()
}

func nameMatches(name string, patterns []string) bool {
	l := strings.ToLower(name)
	for _, p := range patterns {
		if strings.Contains(l, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func noteFailure(st *State, tell func(string, string) error, cause error) error {
	st.Failures++
	if st.Failures == 3 {
		_ = tell("coordinator",
			fmt.Sprintf("pr-watch: unhealthy, 3 consecutive polling failures. Last error: %v", cause))
	}
	if err := st.Save(); err != nil {
		return err
	}
	return cause
}
```

Design note baked into the code: the ill-health tell fires exactly
at the third consecutive failure (not >=3), so a long outage alerts
once, and any successful round resets the counter.

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/watch/ -v`
Expected: PASS (all watch package tests)

- [ ] **Step 5: Run lint**

Run: `spore lint`
Expected: exit 0, no output

- [ ] **Step 6: Commit**

```bash
git add internal/watch/watch.go internal/watch/watch_test.go
git -c user.name='Claude (spore)' -c user.email='crm-service-harness-aaaaudc5h5f7mhwdmhjaueuz2m@marketer-grid.org.slack.com' \
  commit -m "feat(watch): orchestrator with dedup, runbook tell, ill-health alert"
```

---

### Task 5: CLI wiring

**Files:**
- Create: `cmd/spore/watch_cmd.go`
- Modify: `cmd/spore/main.go` (add one case to the subcommand switch,
  around line 164, and one usage line)

**Interfaces:**
- Consumes: `watch.Run` (Task 4), `task.Tell` and `task.ProjectName`
  (existing, internal/task).
- Produces: `spore watch prs [--project-root DIR] [--dry-run]`.
  Default project root: cwd. IMPORTANT: `task.Tell` resolves the
  state dir from the process working directory, so `runWatch` must
  `os.Chdir(projectRoot)` before calling `watch.Run` when
  `--project-root` is given. Exit 0 on success (including disabled
  config), 1 on error.

- [ ] **Step 1: Write watch_cmd.go**

```go
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/watch"
)

func runWatch(args []string) int {
	if len(args) < 1 || args[0] != "prs" {
		fmt.Fprintln(os.Stderr, "usage: spore watch prs [--project-root DIR] [--dry-run]")
		return 1
	}
	fs := flag.NewFlagSet("watch prs", flag.ExitOnError)
	root := fs.String("project-root", "", "project root (default: cwd)")
	dryRun := fs.Bool("dry-run", false, "report without telling or saving state")
	_ = fs.Parse(args[1:])

	if *root != "" {
		if err := os.Chdir(*root); err != nil {
			fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
			return 1
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return 1
	}
	project := task.ProjectName(cwd)
	rep, err := watch.Run(cwd, project, *dryRun, task.Tell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spore watch: %v\n", err)
		return 1
	}
	fmt.Printf("pr-watch %s: %d alert(s), %d already-seen\n", project, rep.Alerts, rep.Skipped)
	return 0
}
```

Before writing, verify `task.ProjectName`'s exact signature at
`internal/task/state.go:94` (the scout reports
`ProjectName(projectRoot)`; if it returns `(string, error)`, handle
the error like the other cases above).

- [ ] **Step 2: Register in main.go**

In the switch in `cmd/spore/main.go` (after the `recipes` case):

```go
case "watch":
	os.Exit(runWatch(args))
```

Add `watch prs` to the usage/help text next to its siblings.

- [ ] **Step 3: Build and smoke-run**

Run: `nix develop -c go build ./... && nix develop -c go run ./cmd/spore watch prs --dry-run`
(from `/home/spore/spore`; no watch.toml for project `spore` exists)
Expected: `pr-watch spore: 0 alert(s), 0 already-seen`, exit 0

- [ ] **Step 4: Full checks**

Run: `nix develop -c go test ./... && spore lint`
Expected: all green

- [ ] **Step 5: Commit**

```bash
git add cmd/spore/watch_cmd.go cmd/spore/main.go
git -c user.name='Claude (spore)' -c user.email='crm-service-harness-aaaaudc5h5f7mhwdmhjaueuz2m@marketer-grid.org.slack.com' \
  commit -m "feat(watch): spore watch prs subcommand"
```

---

### Task 6: live spikes (coordinator runs these itself, not a worker)

**Files:** none (evidence goes to `state.md` + chat).

**Interfaces:**
- Consumes: the built branch binary from Task 5.
- Produces: two verified facts required before Task 7:
  (a) the vault token can re-run failed CI jobs on
  marketer-frontend, (b) a watch-style `tell` wakes the
  marketer-frontend coordinator.

- [ ] **Step 1: Rerun permission probe**

Find a completed failed run (read-only):
`cd /home/spore/marketer-frontend && spore-with-secrets gh run list --status failure --limit 1 --json databaseId,displayTitle`

Then attempt the rerun (the ONE external write in this plan; the
run is already failed, rerunning is what the operator's flow does):
`spore-with-secrets gh run rerun <databaseId> --failed`
Expected: exit 0. If HTTP 403: record "rerun denied" in the spec's
failure table row (escalate instead of retry) and continue - the
feature still works, coordinator escalates instead.

- [ ] **Step 2: Tell-wake probe**

`cd /home/spore/marketer-frontend && spore task tell coordinator "pr-watch spike: reply 'ack' to the spore coordinator via its inbox"`
Expected: within ~60s the marketer-frontend coordinator processes
the envelope (watch its pane tail via
`tmux capture-pane -p -t spore/marketer-frontend/coordinator | tail -20`).
Record round-trip result.

- [ ] **Step 3: Record both outcomes in state.md** (spore repo root),
under a "pr-e2e-watcher" section: rerun allowed yes/no, wake
round-trip seconds.

---

### Task 7: deploy on this host (marketer-frontend only)

**Files (all outside the repo, operator-owned host state):**
- Create: `~/.config/spore/marketer-frontend/watch.toml`
- Create: `~/.config/systemd/user/spore-pr-watch-marketer-frontend.service`
- Create: `~/.config/systemd/user/spore-pr-watch-marketer-frontend.timer`
- Create: `~/.local/bin/spore-evolve` (branch-built binary)

**Interfaces:**
- Consumes: everything above.
- Produces: a live 15-minute watch loop for marketer-frontend.

- [ ] **Step 1: Build the branch binary**

```bash
cd /home/spore/spore && nix develop -c go build -o ~/.local/bin/spore-evolve ./cmd/spore
~/.local/bin/spore-evolve --version
```

Expected: version prints; binary exists.

- [ ] **Step 2: Write watch.toml**

```toml
enabled = true
checks = ["cypress", "playwright", "e2e"]
```

at `~/.config/spore/marketer-frontend/watch.toml` (dir already
exists with 0700 from secrets layering; do not touch secrets.env).

- [ ] **Step 3: Manual dry-run against the real repo**

```bash
cd /home/spore/marketer-frontend && spore-with-secrets ~/.local/bin/spore-evolve watch prs --dry-run
```

Expected: `pr-watch marketer-frontend: N alert(s), 0 already-seen`
with N matching the actually-failing e2e checks visible on
`spore-with-secrets gh pr list`. Cross-check one PR by hand.

- [ ] **Step 4: Write the systemd user units**

`~/.config/systemd/user/spore-pr-watch-marketer-frontend.service`:

```ini
[Unit]
Description=PR e2e watch [marketer-frontend]

[Service]
Type=oneshot
WorkingDirectory=/home/spore/marketer-frontend
ExecStart=/run/current-system/sw/bin/spore-with-secrets /home/spore/.local/bin/spore-evolve watch prs
```

`~/.config/systemd/user/spore-pr-watch-marketer-frontend.timer`:

```ini
[Unit]
Description=PR e2e watch timer [marketer-frontend]

[Timer]
OnBootSec=5min
OnUnitActiveSec=15min

[Install]
WantedBy=timers.target
```

- [ ] **Step 5: Enable and verify live**

```bash
systemctl --user daemon-reload
systemctl --user enable --now spore-pr-watch-marketer-frontend.timer
systemctl --user list-timers 'spore-pr-watch*'
systemctl --user start spore-pr-watch-marketer-frontend.service
journalctl --user -u spore-pr-watch-marketer-frontend.service -n 20 --no-pager
```

Expected: timer listed with a next-elapse ~15min out; the manual
`start` logs one `pr-watch marketer-frontend: ...` line, exit 0.
If a real failure exists, verify the envelope landed:
`ls ~/.local/state/spore/marketer-frontend/coordinator/inbox/`.

- [ ] **Step 6: Update state.md and report to operator**

Record in `state.md`: deployment paths, timer name, how to disable
(`systemctl --user disable --now spore-pr-watch-marketer-frontend.timer`),
and that the binary is a branch build at `~/.local/bin/spore-evolve`
(rebuild after any watch change). Post a one-line summary to the
operator.

---

## Self-Review (done at plan-writing time)

- Spec coverage: config/enable (T1), dedup + prune + new-commit
  reset (T2, key includes SHA), draft skip + name filter + tell +
  ill-health (T4), CLI + timer cadence (T5, T7), spike for rerun
  permission + wake (T6), serial-worker rule and retry cap live in
  the runbook text (T4). Escalation channel = terminal = coordinator
  behavior, no code needed. Deferred items untouched.
- Spec deviation (intentional, spec updated in same commit): the
  seen-state no longer tracks "retry spent"; the retry-once cap is
  enforced by the runbook the coordinator receives, because the
  rerun outcome is watched by the coordinator in-session.
- Placeholders: none; every code step is complete.
- Type consistency: `Config/State/PR/CheckRun/Report` signatures
  match across tasks; `Key` used in T2 and T4 identically.
