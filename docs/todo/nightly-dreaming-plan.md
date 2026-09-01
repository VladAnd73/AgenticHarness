**Status**: not started. Plan for the spec in
[nightly-dreaming.md](nightly-dreaming.md).

# Nightly Dreaming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps
> use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A nightly job that reads new spore-managed sessions per
project and turns what it finds into lessons, memory entries, and
candidate skills, with an adversarial reviewer gating every write.

**Architecture:** A new Go package `internal/dream` does the
deterministic half (discover sessions, digest them, score and flag,
maintain a candidate ledger, snapshot files, mint one task per project).
The judgement half runs as a normal spore worker spawned by the existing
fleet, driven by two embedded briefs: a proposer brief and an
adversarial reviewer brief. A `spore dream` subcommand and a per-project
systemd timer tie it together.

**Tech Stack:** Go (stdlib only, matching the rest of spore), the
existing `internal/task` and `internal/watch` packages, `go:embed` for
the briefs, systemd user units for scheduling.

**Spec:** `docs/todo/nightly-dreaming.md`

## Global Constraints

Copied from `CLAUDE.md` and the spec. Every task's requirements include
these.

- ASCII only. No em-dashes, no en-dashes, no emojis. Use a hyphen, a
  colon, parentheses, or a new sentence.
- Comments must earn their place: a hidden constraint, a non-obvious
  invariant, a reason for a surprising choice. Default to no comment.
- Go stdlib only. Do not add dependencies.
- No source file over 800 lines (the `filesize` lint).
- Commit as `git -c user.name='Claude (spore)' -c
  user.email='vlad@marketer.tech'`. Never run `git config user.*`.
- Branch from `main` as `wt/<slug>`. Never commit direct to main, never
  push without explicit instruction.
- `just check` must be green before delivery: `fmt-check`, `lint`,
  `test`, `vuln`, `nix-check`.
- Worker TDD: write the failing test first, run it, confirm it fails for
  the right reason, then implement.
- Opensource-bound: no internal hostnames, no operator-machine paths, no
  personal email beyond what `git config user.email` resolves to. Use
  `$HOME` or a passed-in root, never a literal home path.
- Do not rename `dispatcher` or `runner`.
- Deterministic tests: inject clocks and roots, never read the real
  `$HOME` or the real transcript tree from a test.

## File Structure

Create:

| File | Responsibility |
| --- | --- |
| `internal/statefile/statefile.go` | Shared state-path resolution and atomic JSON write, extracted from `internal/watch`. |
| `internal/dream/discover.go` | Find and classify spore-managed sessions. |
| `internal/dream/digest.go` | Extract high-signal slices from one session. |
| `internal/dream/score.go` | Score sessions and flag a capped set for deep reading. |
| `internal/dream/ledger.go` | Candidate ledger: fingerprints, occurrence counts, verdict statuses, the two-tier gate. |
| `internal/dream/backup.go` | Snapshot files before a write; revert a run. |
| `internal/dream/mint.go` | Mint and start one task per project. |
| `internal/dream/run.go` | Compose the stages into one run. |
| `internal/dream/briefs/proposer.md` | Embedded brief for stages 3 and 5. |
| `internal/dream/briefs/reviewer.md` | Embedded brief for stage 4. |
| `cmd/spore/dream_cmd.go` | CLI: `spore dream digest`, `spore dream revert`. |

Modify:

| File | Change |
| --- | --- |
| `internal/watch/seen.go` | Delegate `stateFile` and `writeJSONAtomic` to `internal/statefile`. |
| `internal/watch/config.go` | Add `DreamsConfig` and `LoadDreamsConfig`. |
| `cmd/spore/main.go:166` | Add the `dream` case next to `watch`. |
| `docs/todo/nightly-dreaming.md` | Flip the status header when done. |

---

### Task 1: Shared state-file helpers

`internal/watch` owns two helpers the dream package needs. Extract them
rather than copying, so both packages resolve state paths identically.

**Files:**
- Create: `internal/statefile/statefile.go`
- Create: `internal/statefile/statefile_test.go`
- Modify: `internal/watch/seen.go:32-42` (`stateFile`), `:91-114`
  (`writeJSONAtomic`)

**Interfaces:**
- Produces: `statefile.Path(project, name string) (string, error)`,
  `statefile.WriteJSONAtomic(path, tmpPrefix string, v any) error`

- [ ] **Step 1: Write the failing test**

```go
package statefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathPrefersXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/xdg")
	got, err := Path("spore", "dream.json")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/xdg/spore/spore/dream.json"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/h")
	got, err := Path("spore", "dream.json")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/h/.local/state/spore/spore/dream.json"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPathErrorsWithoutHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	if _, err := Path("spore", "x.json"); err == nil {
		t.Fatal("expected error when neither XDG_STATE_HOME nor HOME is set")
	}
}

func TestWriteJSONAtomicCreatesDirsAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "deep", "out.json")
	if err := WriteJSONAtomic(p, "test", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{\n  \"a\": 1\n}" {
		t.Fatalf("unexpected content: %q", string(b))
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("expected only the final file, got %d entries", len(entries))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/statefile/ -v`
Expected: FAIL to build, `undefined: Path`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package statefile resolves per-project spore state paths and writes
// JSON to them atomically. The watch and dream subsystems both keep
// per-project state; sharing this keeps their path resolution identical.
package statefile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Path returns the path to name inside project's spore state dir,
// honouring XDG_STATE_HOME and falling back to $HOME/.local/state.
func Path(project, name string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("neither XDG_STATE_HOME nor HOME set")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "spore", project, name), nil
}

// WriteJSONAtomic marshals v and renames it into place, so a crashed or
// overlapping run never leaves a half-written state file behind.
func WriteJSONAtomic(path, tmpPrefix string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+tmpPrefix+"-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/statefile/ -v`
Expected: PASS, four tests.

- [ ] **Step 5: Point watch at the shared helpers**

In `internal/watch/seen.go`, replace the bodies (keep the unexported
names so no other watch file changes):

```go
func stateFile(project, name string) (string, error) {
	return statefile.Path(project, name)
}

func writeJSONAtomic(path, tmpPrefix string, v any) error {
	return statefile.WriteJSONAtomic(path, tmpPrefix, v)
}
```

Add `"github.com/versality/spore/internal/statefile"` to the imports and
drop any import left unused.

- [ ] **Step 6: Verify watch still passes unchanged**

Run: `nix develop -c go test ./internal/watch/ ./internal/statefile/`
Expected: PASS. No watch test may need editing; if one does, the
extraction changed behavior and must be corrected.

- [ ] **Step 7: Commit**

```bash
git add internal/statefile internal/watch/seen.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "refactor(statefile): extract shared state path and atomic write"
```

---

### Task 2: Session discovery and classification

**Files:**
- Create: `internal/dream/discover.go`
- Create: `internal/dream/discover_test.go`

**Interfaces:**
- Produces: `dream.Kind`, `dream.Session`,
  `dream.Discover(projectsRoot, home string, since time.Time) ([]Session, error)`

Classification rules, both verified live on 2026-08-31:

- worker: entry `cwd` matches `<home>/<project>/.worktrees/<slug>`
- coordinator: first user message begins with `# Coordinator role`;
  project is `filepath.Base(cwd)`
- anything else: skipped

- [ ] **Step 1: Write the failing test**

```go
package dream

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTranscript(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func userLine(cwd, ts, text string) string {
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"` + ts +
		`","message":{"role":"user","content":"` + text + `"}}`
}

func TestDiscoverClassifiesWorkerAndCoordinatorAndSkipsAdHoc(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"

	writeTranscript(t, filepath.Join(root, "-home-agent-proj--worktrees-fix-a"),
		"w.jsonl",
		userLine(home+"/proj/.worktrees/fix-a", "2026-09-01T01:00:00Z", "# Goal"))

	writeTranscript(t, filepath.Join(root, "-home-agent-proj"),
		"c.jsonl",
		userLine(home+"/proj", "2026-09-01T02:00:00Z", "# Coordinator role (shared)"))

	writeTranscript(t, filepath.Join(root, "-home-agent-proj"),
		"adhoc.jsonl",
		userLine(home+"/proj", "2026-09-01T03:00:00Z", "hey can you look at this"))

	got, err := Discover(root, home, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 in-scope sessions, got %d: %+v", len(got), got)
	}

	byKind := map[Kind]Session{}
	for _, s := range got {
		byKind[s.Kind] = s
	}
	w, ok := byKind[KindWorker]
	if !ok {
		t.Fatal("no worker session discovered")
	}
	if w.Project != "proj" || w.Slug != "fix-a" {
		t.Fatalf("worker misclassified: project=%q slug=%q", w.Project, w.Slug)
	}
	c, ok := byKind[KindCoordinator]
	if !ok {
		t.Fatal("no coordinator session discovered")
	}
	if c.Project != "proj" || c.Slug != "coordinator" {
		t.Fatalf("coordinator misclassified: project=%q slug=%q", c.Project, c.Slug)
	}
}

func TestDiscoverSkipsSessionsAtOrBeforeWatermark(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"
	writeTranscript(t, filepath.Join(root, "-home-agent-proj--worktrees-old"),
		"o.jsonl",
		userLine(home+"/proj/.worktrees/old", "2026-08-01T00:00:00Z", "# Goal"))

	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	got, err := Discover(root, home, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected nothing after watermark, got %d", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/dream/ -run TestDiscover -v`
Expected: FAIL to build, `undefined: Discover`.

- [ ] **Step 3: Write minimal implementation**

```go
// Package dream turns spore-managed session transcripts into candidate
// harness improvements. This file finds and classifies the sessions;
// everything downstream consumes the Session values it returns.
package dream

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Kind string

const (
	KindCoordinator Kind = "coordinator"
	KindWorker      Kind = "worker"
)

// coordinatorMarker is the opening line of the shared coordinator role,
// which every coordinator session receives as its first user message.
// It is the only reliable way to tell a coordinator from an ad-hoc
// session, because both run with the project root as cwd.
const coordinatorMarker = "# Coordinator role"

type Session struct {
	Project string
	Kind    Kind
	Slug    string
	Path    string
	First   time.Time
	Last    time.Time
}

type rawEntry struct {
	Type      string          `json:"type"`
	Cwd       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// Discover walks projectsRoot for transcripts whose last activity is
// after since, and returns only spore-managed sessions.
func Discover(projectsRoot, home string, since time.Time) ([]Session, error) {
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projectsRoot, e.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			s, ok, err := classify(filepath.Join(dir, f.Name()), home)
			if err != nil || !ok {
				continue
			}
			if !s.Last.After(since) {
				continue
			}
			out = append(out, s)
		}
	}
	return out, nil
}

func classify(path, home string) (Session, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, false, err
	}
	defer f.Close()

	s := Session{Path: path}
	var cwd, firstUser string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e rawEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if cwd == "" && e.Cwd != "" {
			cwd = e.Cwd
		}
		if ts, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			if s.First.IsZero() || ts.Before(s.First) {
				s.First = ts
			}
			if ts.After(s.Last) {
				s.Last = ts
			}
		}
		if firstUser == "" && e.Type == "user" {
			firstUser = messageText(e.Message)
		}
	}
	if cwd == "" {
		return Session{}, false, nil
	}

	if project, slug, ok := workerPath(cwd, home); ok {
		s.Kind, s.Project, s.Slug = KindWorker, project, slug
		return s, true, nil
	}
	if strings.HasPrefix(strings.TrimSpace(firstUser), coordinatorMarker) {
		s.Kind, s.Project, s.Slug = KindCoordinator, filepath.Base(cwd), "coordinator"
		return s, true, nil
	}
	return Session{}, false, nil
}

// workerPath matches <home>/<project>/.worktrees/<slug> exactly. A
// deeper path (a subdirectory inside a worktree) is not a session root
// and is deliberately rejected.
func workerPath(cwd, home string) (project, slug string, ok bool) {
	rel, err := filepath.Rel(home, cwd)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 || parts[1] != ".worktrees" {
		return "", "", false
	}
	if parts[0] == "" || parts[0] == ".." || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m struct {
		Content any `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	switch c := m.Content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, part := range c {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if t, ok := pm["text"].(string); ok {
				b.WriteString(t)
				b.WriteString(" ")
			}
		}
		return b.String()
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/dream/ -run TestDiscover -v`
Expected: PASS, both tests. This is acceptance scenarios 1 and 2 (the
discovery half).

- [ ] **Step 5: Commit**

```bash
git add internal/dream/discover.go internal/dream/discover_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): discover and classify spore-managed sessions"
```

---

### Task 3: Digest extraction

**Files:**
- Create: `internal/dream/digest.go`
- Create: `internal/dream/digest_test.go`

**Interfaces:**
- Consumes: `Session` from Task 2
- Produces: `dream.Slice`, `dream.SessionDigest`,
  `dream.BuildDigest(s Session, repeatThreshold int) (SessionDigest, error)`,
  `dream.FormatDigest(ds []SessionDigest) string`

- [ ] **Step 1: Write the failing test**

```go
func TestBuildDigestKeepsSignalDropsNoise(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"
	cwd := home + "/proj/.worktrees/fix-a"

	lines := []string{userLine(cwd, "2026-09-01T01:00:00Z", "# Goal")}
	for i := 0; i < 50; i++ {
		lines = append(lines, `{"type":"assistant","cwd":"`+cwd+
			`","timestamp":"2026-09-01T01:01:00Z","message":{"role":"assistant",`+
			`"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x"}}]}}`)
	}
	lines = append(lines,
		userLine(cwd, "2026-09-01T01:02:00Z", "no, use the kernel flow instead"),
		`{"type":"user","cwd":"`+cwd+`","timestamp":"2026-09-01T01:03:00Z",`+
			`"message":{"role":"user","content":[{"type":"tool_result","is_error":true,`+
			`"content":"bash: fleebnort: command not found"}]}}`)

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl", lines...)
	s := Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}

	d, err := BuildDigest(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.OperatorMessages) != 1 ||
		!strings.Contains(d.OperatorMessages[0].Text, "kernel flow") {
		t.Fatalf("operator correction not captured: %+v", d.OperatorMessages)
	}
	if len(d.Failures) != 1 ||
		!strings.Contains(d.Failures[0].Text, "fleebnort") {
		t.Fatalf("failure not captured: %+v", d.Failures)
	}
	out := FormatDigest([]SessionDigest{d})
	if strings.Contains(out, "/x") {
		t.Fatal("successful reads leaked into the digest")
	}
}

func TestBuildDigestReadsTerminalState(t *testing.T) {
	if got := endState("BLOCKED: cannot reach the backend", false); got != "blocked" {
		t.Fatalf("blocked not detected: %q", got)
	}
	if got := endState("all green", true); got != "tokens" {
		t.Fatalf("wrap-up not detected: %q", got)
	}
	if got := endState("DONE, tests pass", false); got != "done" {
		t.Fatalf("done not detected: %q", got)
	}
	if got := endState("still thinking", false); got != "unknown" {
		t.Fatalf("expected unknown, got %q", got)
	}
}
```

Note the first user message is the brief and is captured as `Brief`, not
as an operator message, which is why the count above is 1 and not 2.

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/dream/ -run TestBuildDigest -v`
Expected: FAIL to build, `undefined: BuildDigest`.

- [ ] **Step 3: Write minimal implementation**

```go
type Slice struct {
	Kind string
	TS   time.Time
	Text string
}

type SessionDigest struct {
	Session          Session
	Brief            string
	OperatorMessages []Slice
	Failures         []Slice
	RepeatedCommands []Slice
	Denials          []Slice
	FinalReport      string
	End              string
	Score            int
	DeepRead         bool
}

// BuildDigest keeps only the slices that carry a lesson: what the
// operator said, what failed, what was retried, what was denied. The
// bulk of a transcript is successful tool calls and reasoning, which is
// dropped so a night's worth of sessions fits in a model's context.
func BuildDigest(s Session, repeatThreshold int) (SessionDigest, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return SessionDigest{}, err
	}
	defer f.Close()

	d := SessionDigest{Session: s, End: "unknown"}
	cmdCounts := map[string]int{}
	var lastAssistant string
	sawWrapUp := false

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var e rawEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, e.Timestamp)
		switch e.Type {
		case "user":
			text := messageText(e.Message)
			if errText, isErr := toolError(e.Message); isErr {
				d.Failures = append(d.Failures,
					Slice{Kind: "tool-error", TS: ts, Text: errText})
				continue
			}
			if denied(text) {
				d.Denials = append(d.Denials, Slice{Kind: "denial", TS: ts, Text: text})
				continue
			}
			if strings.Contains(text, "wrap-up") || strings.Contains(text, "token threshold") {
				sawWrapUp = true
			}
			if strings.TrimSpace(text) == "" {
				continue
			}
			if d.Brief == "" {
				d.Brief = text
				continue
			}
			d.OperatorMessages = append(d.OperatorMessages,
				Slice{Kind: "operator", TS: ts, Text: text})
		case "assistant":
			if cmd, ok := bashCommand(e.Message); ok {
				cmdCounts[cmd]++
			}
			if t := messageText(e.Message); strings.TrimSpace(t) != "" {
				lastAssistant = t
			}
		}
	}
	d.FinalReport = lastAssistant
	d.End = endState(lastAssistant, sawWrapUp)
	for cmd, n := range cmdCounts {
		if n >= repeatThreshold {
			d.RepeatedCommands = append(d.RepeatedCommands,
				Slice{Kind: "repeated", Text: fmt.Sprintf("%dx %s", n, cmd)})
		}
	}
	sort.Slice(d.RepeatedCommands, func(i, j int) bool {
		return d.RepeatedCommands[i].Text < d.RepeatedCommands[j].Text
	})
	return d, nil
}

// endState reads how the session finished. It feeds scoring: a session
// that ended blocked or ran out of context is far likelier to hold a
// lesson than one that simply finished.
func endState(finalReport string, sawWrapUp bool) string {
	switch {
	case strings.Contains(finalReport, "BLOCKED"):
		return "blocked"
	case sawWrapUp:
		return "tokens"
	case strings.Contains(finalReport, "DONE"):
		return "done"
	}
	return "unknown"
}

func toolError(raw json.RawMessage) (string, bool) {
	var m struct {
		Content []struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Content any    `json:"content"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	for _, c := range m.Content {
		if c.Type == "tool_result" && c.IsError {
			return fmt.Sprint(c.Content), true
		}
	}
	return "", false
}

func bashCommand(raw json.RawMessage) (string, bool) {
	var m struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				Command string `json:"command"`
			} `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	for _, c := range m.Content {
		if c.Type == "tool_use" && c.Name == "Bash" && c.Input.Command != "" {
			return c.Input.Command, true
		}
	}
	return "", false
}

func denied(text string) bool {
	return strings.Contains(text, "permission to use") ||
		strings.Contains(text, "requested permissions") ||
		strings.Contains(text, "blocked by hook") ||
		strings.Contains(text, "hook feedback")
}

// FormatDigest renders the digests as the markdown the proposer reads.
func FormatDigest(ds []SessionDigest) string {
	var b strings.Builder
	for _, d := range ds {
		fmt.Fprintf(&b, "## %s / %s (%s)\n\n", d.Session.Project,
			d.Session.Slug, d.Session.Kind)
		fmt.Fprintf(&b, "session: %s\nended: %s\ndeep-read: %v\n\n",
			filepath.Base(d.Session.Path), d.End, d.DeepRead)
		writeSlices(&b, "Operator messages", d.OperatorMessages)
		writeSlices(&b, "Failures", d.Failures)
		writeSlices(&b, "Repeated commands", d.RepeatedCommands)
		writeSlices(&b, "Denials", d.Denials)
	}
	return b.String()
}

func writeSlices(b *strings.Builder, title string, ss []Slice) {
	if len(ss) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s\n\n", title)
	for _, s := range ss {
		fmt.Fprintf(b, "- [%s] %s\n", s.TS.Format(time.RFC3339), oneLine(s.Text))
	}
	b.WriteString("\n")
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 500 {
		return s[:500] + " ..."
	}
	return s
}
```

Add `"fmt"` and `"sort"` to the file's imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/dream/ -run TestBuildDigest -v`
Expected: PASS. This is acceptance scenario 3.

- [ ] **Step 5: Commit**

```bash
git add internal/dream/digest.go internal/dream/digest_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): extract high-signal digest from a session"
```

---

### Task 4: Scoring and the deep-read cap

**Files:**
- Create: `internal/dream/score.go`
- Create: `internal/dream/score_test.go`

**Interfaces:**
- Consumes: `SessionDigest` from Task 3
- Produces: `dream.FlagDeepReads(ds []SessionDigest, cap int)` (mutates
  `Score` and `DeepRead` in place)

- [ ] **Step 1: Write the failing test**

```go
func TestFlagDeepReadsHonoursCap(t *testing.T) {
	var ds []SessionDigest
	for i := 0; i < 10; i++ {
		ds = append(ds, SessionDigest{
			Session:          Session{Slug: fmt.Sprintf("s%d", i)},
			OperatorMessages: []Slice{{Text: "x"}, {Text: "y"}},
			Failures:         []Slice{{Text: "boom"}},
			End:              "blocked",
		})
	}
	FlagDeepReads(ds, 3)
	n := 0
	for _, d := range ds {
		if d.DeepRead {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("cap not enforced: %d sessions flagged, want 3", n)
	}
}

func TestFlagDeepReadsPicksHighestScoringFirst(t *testing.T) {
	ds := []SessionDigest{
		{Session: Session{Slug: "quiet"}},
		{Session: Session{Slug: "messy"},
			Failures: []Slice{{}, {}, {}}, End: "blocked"},
	}
	FlagDeepReads(ds, 1)
	if !ds[1].DeepRead || ds[0].DeepRead {
		t.Fatalf("expected only the messy session flagged: %+v", ds)
	}
}

func TestFlagDeepReadsZeroCapFlagsNothing(t *testing.T) {
	ds := []SessionDigest{{Session: Session{Slug: "a"}, Failures: []Slice{{}}}}
	FlagDeepReads(ds, 0)
	if ds[0].DeepRead {
		t.Fatal("zero cap must flag nothing")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/dream/ -run TestFlagDeepReads -v`
Expected: FAIL to build, `undefined: FlagDeepReads`.

- [ ] **Step 3: Write minimal implementation**

```go
// FlagDeepReads scores every digest and marks at most cap of them for a
// full read. The cap lives here, in Go, so the worker cannot widen it.
func FlagDeepReads(ds []SessionDigest, cap int) {
	for i := range ds {
		ds[i].Score = score(ds[i])
		ds[i].DeepRead = false
	}
	if cap <= 0 {
		return
	}
	order := make([]int, len(ds))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if ds[ia].Score != ds[ib].Score {
			return ds[ia].Score > ds[ib].Score
		}
		return ds[ia].Session.Slug < ds[ib].Session.Slug
	})
	for n, idx := range order {
		if n >= cap || ds[idx].Score == 0 {
			break
		}
		ds[idx].DeepRead = true
	}
}

func score(d SessionDigest) int {
	s := 3*len(d.Failures) + 2*len(d.OperatorMessages) +
		2*len(d.Denials) + len(d.RepeatedCommands)
	if d.End == "blocked" || d.End == "tokens" {
		s += 5
	}
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/dream/ -run TestFlagDeepReads -v`
Expected: PASS, three tests. This is acceptance scenario 4.

- [ ] **Step 5: Commit**

```bash
git add internal/dream/score.go internal/dream/score_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): score sessions and cap deep reads"
```

---

### Task 5: The candidate ledger

This is where the two-tier evidence bar lives.

**Files:**
- Create: `internal/dream/ledger.go`
- Create: `internal/dream/ledger_test.go`

**Interfaces:**
- Consumes: `statefile.Path`, `statefile.WriteJSONAtomic` from Task 1
- Produces: `dream.Status` constants, `dream.Entry`, `dream.Ledger`,
  `dream.Fingerprint(claimType, claim string) string`,
  `(*Ledger).Observe(claimType, claim, sessionID, day string) *Entry`,
  `(*Ledger).Gate(e *Entry, threshold int) bool`,
  `(*Ledger).Record(fp string, st Status, reason, runID string)`,
  `dream.LoadLedger(project string) (*Ledger, error)`, `(*Ledger).Save() error`

- [ ] **Step 1: Write the failing test**

```go
func TestGateOperatorPreferencePassesOnFirstSighting(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeOperatorPreference, "prefer small commits", "sesn-1", "2026-09-01")
	if !l.Gate(e, 2) {
		t.Fatal("an operator preference must pass on first sighting")
	}
}

func TestGateInferredNeedsTwoIndependentSessions(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeToolBehavior, "gh pr create targets upstream", "sesn-1", "2026-09-01")
	if l.Gate(e, 2) {
		t.Fatal("an inferred claim must not pass on first sighting")
	}
	e = l.Observe(TypeToolBehavior, "gh pr create targets upstream", "sesn-1", "2026-09-02")
	if l.Gate(e, 2) {
		t.Fatal("the same session twice is not two independent sessions")
	}
	e = l.Observe(TypeToolBehavior, "gh pr create targets upstream", "sesn-2", "2026-09-02")
	if !l.Gate(e, 2) {
		t.Fatal("two independent sessions must pass the gate")
	}
}

func TestRefutedNeverReturns(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeToolBehavior, "flag --foo exists", "sesn-1", "2026-09-01")
	l.Record(e.Fingerprint, StatusRefuted, "no such flag in --help", "run-1")
	e = l.Observe(TypeToolBehavior, "flag --foo exists", "sesn-9", "2026-09-05")
	if l.Gate(e, 1) {
		t.Fatal("a refuted fingerprint must never pass again")
	}
}

func TestTwoUnevidencedVerdictsKillTheFingerprint(t *testing.T) {
	l := newTestLedger(t)
	e := l.Observe(TypeHostState, "the timer fires at 03:00", "sesn-1", "2026-09-01")
	l.Record(e.Fingerprint, StatusUnevidenced, "could not reach docs", "run-1")
	e = l.Observe(TypeHostState, "the timer fires at 03:00", "sesn-2", "2026-09-02")
	if !l.Gate(e, 1) {
		t.Fatal("one unevidenced verdict must allow a retry")
	}
	l.Record(e.Fingerprint, StatusUnevidenced, "still unreachable", "run-2")
	e = l.Observe(TypeHostState, "the timer fires at 03:00", "sesn-3", "2026-09-03")
	if l.Gate(e, 1) {
		t.Fatal("a second unevidenced verdict must kill the fingerprint")
	}
}

func TestFingerprintIgnoresCosmeticRewording(t *testing.T) {
	a := Fingerprint(TypeToolBehavior, "gh pr create targets upstream, not the fork")
	b := Fingerprint(TypeToolBehavior, "  GH PR CREATE targets upstream not the fork.  ")
	if a != b {
		t.Fatalf("cosmetic rewording changed the fingerprint: %s vs %s", a, b)
	}
}

func TestLedgerRoundTrips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	e := l.Observe(TypeProcessPattern, "workers forget to fetch", "s1", "2026-09-01")
	l.Record(e.Fingerprint, StatusWritten, "", "run-1")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	l2, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := l2.Entries[e.Fingerprint]
	if !ok || got.Status != StatusWritten || got.RunID != "run-1" {
		t.Fatalf("entry did not survive the round trip: %+v", got)
	}
}

func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	return l
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/dream/ -run 'TestGate|TestRefuted|TestTwo|TestFingerprint|TestLedger' -v`
Expected: FAIL to build, `undefined: LoadLedger`.

- [ ] **Step 3: Write minimal implementation**

```go
type Status string

const (
	StatusCandidate   Status = "candidate"
	StatusWritten     Status = "written"
	StatusRefuted     Status = "refuted"
	StatusUnevidenced Status = "unevidenced"
	StatusDead        Status = "dead"
)

const (
	TypeOperatorPreference = "operator-preference"
	TypeToolBehavior       = "tool-behavior"
	TypeHostState          = "host-state"
	TypeCodeBehavior       = "code-behavior"
	TypeProcessPattern     = "process-pattern"
)

type Entry struct {
	Fingerprint      string   `json:"fingerprint"`
	Claim            string   `json:"claim"`
	Type             string   `json:"type"`
	Sessions         []string `json:"sessions"`
	FirstSeen        string   `json:"first_seen"`
	LastSeen         string   `json:"last_seen"`
	Status           Status   `json:"status"`
	Reason           string   `json:"reason,omitempty"`
	UnevidencedCount int      `json:"unevidenced_count,omitempty"`
	RunID            string   `json:"run_id,omitempty"`
}

type Ledger struct {
	Entries map[string]*Entry `json:"entries"`
	path    string
}

func LoadLedger(project string) (*Ledger, error) {
	p, err := statefile.Path(project, filepath.Join("dreams", "ledger.json"))
	if err != nil {
		return nil, err
	}
	l := &Ledger{Entries: map[string]*Entry{}, path: p}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w", p, err)
	}
	if l.Entries == nil {
		l.Entries = map[string]*Entry{}
	}
	l.path = p
	return l, nil
}

func (l *Ledger) Save() error {
	return statefile.WriteJSONAtomic(l.path, "dream-ledger", l)
}

// Fingerprint normalises a claim so cosmetic rewording maps to the same
// entry. Too literal and the recurrence counter never advances; this is
// the knob most likely to need tuning after the first week of runs.
func Fingerprint(claimType, claim string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(claim) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	norm := strings.Join(strings.Fields(b.String()), " ")
	sum := sha256.Sum256([]byte(claimType + "\x00" + norm))
	return hex.EncodeToString(sum[:6])
}

// Observe records one sighting of a claim and returns its entry. A
// repeat sighting from a session already counted does not raise the
// occurrence count: the bar is independent sessions, not repetitions.
func (l *Ledger) Observe(claimType, claim, sessionID, day string) *Entry {
	fp := Fingerprint(claimType, claim)
	e, ok := l.Entries[fp]
	if !ok {
		e = &Entry{
			Fingerprint: fp,
			Claim:       claim,
			Type:        claimType,
			FirstSeen:   day,
			Status:      StatusCandidate,
		}
		l.Entries[fp] = e
	}
	e.LastSeen = day
	for _, s := range e.Sessions {
		if s == sessionID {
			return e
		}
	}
	e.Sessions = append(e.Sessions, sessionID)
	return e
}

// Gate applies the two-tier evidence bar: an operator preference passes
// on first sighting, anything inferred needs threshold independent
// sessions. Dead and refuted fingerprints never pass.
func (l *Ledger) Gate(e *Entry, threshold int) bool {
	switch e.Status {
	case StatusRefuted, StatusDead, StatusWritten:
		return false
	}
	if e.Type == TypeOperatorPreference {
		return true
	}
	return len(e.Sessions) >= threshold
}

func (l *Ledger) Record(fp string, st Status, reason, runID string) {
	e, ok := l.Entries[fp]
	if !ok {
		return
	}
	e.Reason = reason
	e.RunID = runID
	if st == StatusUnevidenced {
		e.UnevidencedCount++
		if e.UnevidencedCount >= 2 {
			e.Status = StatusDead
			return
		}
		e.Status = StatusCandidate
		return
	}
	e.Status = st
}
```

Imports needed: `crypto/sha256`, `encoding/hex`, `encoding/json`,
`fmt`, `os`, `path/filepath`, `strings`, and
`github.com/versality/spore/internal/statefile`.

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/dream/ -v`
Expected: PASS. This is acceptance scenarios 5 and 6.

- [ ] **Step 5: Commit**

```bash
git add internal/dream/ledger.go internal/dream/ledger_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): candidate ledger with two-tier evidence bar"
```

---

### Task 6: Snapshot and revert

**Files:**
- Create: `internal/dream/backup.go`
- Create: `internal/dream/backup_test.go`

**Interfaces:**
- Produces: `dream.RunDir(project, runID string) (string, error)`,
  `dream.Snapshot(runDir string, files []string) error`,
  `dream.Revert(project, runID string) ([]string, error)`

- [ ] **Step 1: Write the failing test**

```go
func TestSnapshotThenRevertRestoresBytes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	work := t.TempDir()
	a := filepath.Join(work, "state.md")
	b := filepath.Join(work, "mem.md")
	os.WriteFile(a, []byte("before-a"), 0o644)
	os.WriteFile(b, []byte("before-b"), 0o644)

	dir, err := RunDir("proj", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(dir, []string{a, b}); err != nil {
		t.Fatal(err)
	}

	os.WriteFile(a, []byte("after-a"), 0o644)
	os.WriteFile(b, []byte("after-b"), 0o644)

	restored, err := Revert("proj", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 {
		t.Fatalf("expected 2 files restored, got %d", len(restored))
	}
	if got, _ := os.ReadFile(a); string(got) != "before-a" {
		t.Fatalf("a not restored: %q", got)
	}
	if got, _ := os.ReadFile(b); string(got) != "before-b" {
		t.Fatalf("b not restored: %q", got)
	}
}

func TestSnapshotRecordsAbsentFilesSoRevertDeletesThem(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	work := t.TempDir()
	newFile := filepath.Join(work, "created.md")

	dir, _ := RunDir("proj", "run-2")
	if err := Snapshot(dir, []string{newFile}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(newFile, []byte("created by the run"), 0o644)

	if _, err := Revert("proj", "run-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Fatal("a file the run created must be removed on revert")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/dream/ -run TestSnapshot -v`
Expected: FAIL to build, `undefined: RunDir`.

- [ ] **Step 3: Write minimal implementation**

```go
type manifestEntry struct {
	Original string `json:"original"`
	Backup   string `json:"backup,omitempty"`
	Absent   bool   `json:"absent,omitempty"`
}

func RunDir(project, runID string) (string, error) {
	p, err := statefile.Path(project, filepath.Join("dreams", runID))
	if err != nil {
		return "", err
	}
	return p, os.MkdirAll(p, 0o755)
}

// Snapshot copies every file the run intends to write into runDir's
// backup tree. A file that does not exist yet is recorded as absent, so
// revert removes it rather than leaving the run's creation behind.
func Snapshot(runDir string, files []string) error {
	backupDir := filepath.Join(runDir, "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}
	var manifest []manifestEntry
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(abs))
		name := hex.EncodeToString(sum[:8]) + ".bak"
		b, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				manifest = append(manifest, manifestEntry{Original: abs, Absent: true})
				continue
			}
			return err
		}
		if err := os.WriteFile(filepath.Join(backupDir, name), b, 0o644); err != nil {
			return err
		}
		manifest = append(manifest, manifestEntry{Original: abs, Backup: name})
	}
	return statefile.WriteJSONAtomic(
		filepath.Join(runDir, "manifest.json"), "dream-manifest", manifest)
}

func Revert(project, runID string) ([]string, error) {
	dir, err := RunDir(project, runID)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("no manifest for run %s: %w", runID, err)
	}
	var manifest []manifestEntry
	if err := json.Unmarshal(b, &manifest); err != nil {
		return nil, err
	}
	var restored []string
	for _, m := range manifest {
		if m.Absent {
			if err := os.Remove(m.Original); err != nil && !os.IsNotExist(err) {
				return restored, err
			}
			restored = append(restored, m.Original)
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, "backup", m.Backup))
		if err != nil {
			return restored, err
		}
		if err := os.WriteFile(m.Original, content, 0o644); err != nil {
			return restored, err
		}
		restored = append(restored, m.Original)
	}
	return restored, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/dream/ -run TestSnapshot -v`
Expected: PASS, both tests. This is acceptance scenario 8.

- [ ] **Step 5: Commit**

```bash
git add internal/dream/backup.go internal/dream/backup_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): snapshot targets and revert a run"
```

---

### Task 7: The `[dreams]` config table

**Files:**
- Modify: `internal/watch/config.go` (add after `LoadReleasesConfig`)
- Modify: `internal/watch/config_test.go`

`watch.toml` already has one owner, `internal/watch`. Keep it that way
rather than teaching `internal/dream` to parse TOML.

**Interfaces:**
- Produces: `watch.DreamsConfig{Enabled bool; DeepReadCap, MaxWritesPerRun, RecurrenceThreshold int}`,
  `watch.LoadDreamsConfig(project string) (DreamsConfig, error)`

- [ ] **Step 1: Write the failing test**

```go
func TestLoadDreamsConfigReadsTable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "spore", "proj"), 0o755)
	os.WriteFile(filepath.Join(dir, "spore", "proj", "watch.toml"), []byte(
		"enabled = true\n\n[dreams]\nenabled = true\ndeep_read_cap = 5\n"+
			"max_writes_per_run = 20\nrecurrence_threshold = 3\n"), 0o644)

	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.DeepReadCap != 5 || got.MaxWritesPerRun != 20 ||
		got.RecurrenceThreshold != 3 {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestLoadDreamsConfigDefaultsWhenAbsent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	got, err := LoadDreamsConfig("proj")
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled {
		t.Fatal("a missing [dreams] table must mean disabled")
	}
	if got.DeepReadCap != 3 || got.MaxWritesPerRun != 10 ||
		got.RecurrenceThreshold != 2 {
		t.Fatalf("defaults not applied: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/watch/ -run TestLoadDreamsConfig -v`
Expected: FAIL to build, `undefined: LoadDreamsConfig`.

- [ ] **Step 3: Write minimal implementation**

```go
// DreamsConfig is the [dreams] table. Absent means disabled; the
// numeric knobs still carry their defaults so a config that only sets
// enabled behaves sensibly.
type DreamsConfig struct {
	Enabled             bool
	DeepReadCap         int
	MaxWritesPerRun     int
	RecurrenceThreshold int
}

func LoadDreamsConfig(project string) (DreamsConfig, error) {
	sections, err := readWatchToml(project)
	if err != nil {
		return DreamsConfig{}, err
	}
	d := sections["dreams"]
	return DreamsConfig{
		Enabled:             d["enabled"] == "true",
		DeepReadCap:         parseIntDefault(d["deep_read_cap"], 3),
		MaxWritesPerRun:     parseIntDefault(d["max_writes_per_run"], 10),
		RecurrenceThreshold: parseIntDefault(d["recurrence_threshold"], 2),
	}, nil
}

func parseIntDefault(val string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || n < 0 {
		return def
	}
	return n
}
```

Add `"strconv"` to the imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/watch/ -v`
Expected: PASS, including the pre-existing watch tests.

- [ ] **Step 5: Commit**

```bash
git add internal/watch/config.go internal/watch/config_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(watch): add [dreams] config table"
```

---

### Task 8: The embedded briefs

The briefs are the prompt half of the feature. They are data, not code,
so they get their own files and are embedded.

**Files:**
- Create: `internal/dream/briefs/proposer.md`
- Create: `internal/dream/briefs/reviewer.md`
- Create: `internal/dream/briefs.go`
- Create: `internal/dream/briefs_test.go`

**Interfaces:**
- Produces: `dream.ProposerBrief string`, `dream.ReviewerBrief string`

- [ ] **Step 1: Write the failing test**

The test guards the properties that make the design work, so a later
edit cannot quietly remove the adversarial rules.

```go
func TestBriefsCarryTheLoadBearingRules(t *testing.T) {
	for _, want := range []string{
		"You may not write anything",
		"evidence packet",
		"operator-preference",
	} {
		if !strings.Contains(ProposerBrief, want) {
			t.Errorf("proposer brief is missing %q", want)
		}
	}
	for _, want := range []string{
		"Re-derive, never trust",
		"Documentation over recall",
		"Default to reject",
		"stale",
	} {
		if !strings.Contains(ReviewerBrief, want) {
			t.Errorf("reviewer brief is missing %q", want)
		}
	}
}

func TestBriefsNameNoProjectAndNoSkill(t *testing.T) {
	for name, brief := range map[string]string{
		"proposer": ProposerBrief, "reviewer": ReviewerBrief,
	} {
		for _, banned := range []string{"marketer", "crm-gateway", "/home/"} {
			if strings.Contains(brief, banned) {
				t.Errorf("%s brief leaks %q; kernel assets stay generic", name, banned)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/dream/ -run TestBriefs -v`
Expected: FAIL to build, `undefined: ProposerBrief`.

- [ ] **Step 3: Write the embed shim**

```go
package dream

import _ "embed"

//go:embed briefs/proposer.md
var ProposerBrief string

//go:embed briefs/reviewer.md
var ReviewerBrief string
```

- [ ] **Step 4: Write `briefs/proposer.md`**

```markdown
# Dream proposer

You are reading the digest of one project's spore sessions from the last
run. Your job is to find what the harness should learn, and to propose
it. You may not write anything to state.md, memory, or any skill
directory. A later stage does that, and only for proposals that survive
review.

## Read

1. `digest.md` in this run directory. It holds the operator messages,
   failures, repeated commands, and denials from every in-scope session.
2. The sessions marked `deep-read: true`. Read those transcripts in
   full. Do not read any other transcript in full; the cap is
   deliberate.

## Produce

For each thing worth learning, write one evidence packet to
`packets/<n>.json` in this run directory:

    {
      "claim":    "one sentence, specific enough to be falsifiable",
      "type":     "operator-preference | tool-behavior | host-state |
                   code-behavior | process-pattern",
      "evidence": ["session <id> at <timestamp>", "file.go:120",
                   "the command that would prove this"],
      "tier":     "lesson | memory | skill",
      "target":   "path the change would land in",
      "text":     "the literal content to write"
    }

Rules:

- Evidence is pointers, not quotes. Say where the proof is, so the
  reviewer can go and get it. A quote you paste carries no weight.
- One claim per packet. A packet making two claims cannot be half
  confirmed.
- `operator-preference` means the operator said it. Anything you
  inferred yourself is one of the other types, and the ledger will hold
  it until it recurs.
- Prefer the smallest durable form. A lesson block beats a memory entry;
  a memory entry beats a skill. Propose a skill only when the knowledge
  is a procedure someone would follow step by step.
- If a session taught you nothing, say so and produce no packet. An
  empty night is a valid outcome and is cheaper than a bad lesson.
```

- [ ] **Step 5: Write `briefs/reviewer.md`**

```markdown
# Dream reviewer (adversarial)

You are reviewing one evidence packet. You did not write it and you
cannot see the reasoning that produced it. Your default answer is no.

Your job is to try to REFUTE the claim. If you cannot refute it, and you
positively confirmed it, you approve. Anything else is not an approval.

## The three rules

1. **Re-derive, never trust.** The packet's evidence is a set of
   pointers. Go and get the proof yourself: run the command, open the
   file at that line, read the actual output. Text quoted inside the
   packet is worth nothing. A fabricated citation must not survive you.
2. **Documentation over recall.** For any claim about how a tool, flag,
   command, or API behaves, consult the real source: `--help`, the man
   page, the code in this repository, or the official documentation. You
   are not permitted to answer from your own knowledge of how the tool
   probably works. Where the claim is about this machine, run the
   command and quote the output.
3. **Default to reject.** Uncertain is a refusal. Unverifiable is a
   refusal. The burden is entirely on the packet.

## Also refute

- **Stale.** Is this still true today? The fix may have landed, the flag
  may have been renamed, the gap may be closed. A claim that was true
  when the session ran and is false now is the worst kind, because it
  reads as authoritative. Check the current state, not the session's.
- **Overgeneralised.** True of one odd session, written as a general
  rule.
- **Duplicate.** Already covered by an existing lesson, rule, memory
  entry, or skill. Read the target file before deciding.
- **Leaky.** Machine-specific paths, internal hostnames, or personal
  addresses in content bound for a file that gets committed.

## Verdict

Write `verdicts/<n>.json`:

    {
      "verdict": "confirmed | refuted | unevidenced",
      "reason":  "one or two sentences",
      "proof":   "the command you ran or the file and line you read,
                  with the actual output"
    }

`confirmed` requires a filled `proof`. `refuted` means you established
the claim is false. `unevidenced` means you could not establish either
way, including when a source you needed was unreachable.
```

- [ ] **Step 6: Run test to verify it passes**

Run: `nix develop -c go test ./internal/dream/ -run TestBriefs -v`
Expected: PASS, both tests.

- [ ] **Step 7: Commit**

```bash
git add internal/dream/briefs.go internal/dream/briefs internal/dream/briefs_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): embed proposer and adversarial reviewer briefs"
```

---

### Task 9: Minting one task per project

**Files:**
- Create: `internal/dream/mint.go`
- Create: `internal/dream/mint_test.go`

**Interfaces:**
- Consumes: `task.Slugify`, `task.Allocate`, `frontmatter.Write`
- Produces: `dream.MintTask(tasksDir, project, runID, runDir string, deepRead []string) (string, error)`

The project field must be set explicitly. `task.ProjectName("")`
resolves from the working directory, which is the known cwd-routing bug
(`docs/todo` draft `task-tell-routes-by-cwd-ignoring-spore-project-roo`);
a job minting tasks for another project must not depend on it.

- [ ] **Step 1: Write the failing test**

```go
func TestMintTaskWritesDraftForTheNamedProject(t *testing.T) {
	tasksDir := t.TempDir()
	slug, err := MintTask(tasksDir, "otherproj", "20260901-ab12",
		"/state/dreams/20260901-ab12", []string{"sesn-1", "sesn-2"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(tasksDir, slug+".md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if !strings.Contains(body, "project: otherproj") {
		t.Fatalf("project not pinned to the named project:\n%s", body)
	}
	if !strings.Contains(body, "status: draft") {
		t.Fatal("minted task must start as a draft")
	}
	if !strings.Contains(body, "20260901-ab12") {
		t.Fatal("run id must reach the brief")
	}
	if !strings.Contains(body, "sesn-1") || !strings.Contains(body, "sesn-2") {
		t.Fatal("deep-read session list must reach the brief")
	}
}

func TestMintTaskAllocatesAroundACollision(t *testing.T) {
	tasksDir := t.TempDir()
	first, err := MintTask(tasksDir, "p", "run-1", "/d", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MintTask(tasksDir, "p", "run-1", "/d", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("a second mint must allocate a distinct slug")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/dream/ -run TestMintTask -v`
Expected: FAIL to build, `undefined: MintTask`.

- [ ] **Step 3: Write minimal implementation**

```go
// MintTask writes a draft task carrying the proposer brief. The project
// is passed in, never derived from the working directory: this job runs
// from a timer and mints tasks for projects other than its own cwd.
func MintTask(tasksDir, project, runID, runDir string, deepRead []string) (string, error) {
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		return "", err
	}
	title := "dream " + runID
	slug, err := task.Allocate(tasksDir, task.Slugify(title))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("\n" + ProposerBrief + "\n")
	fmt.Fprintf(&b, "\n## This run\n\nrun id: %s\nrun dir: %s\n", runID, runDir)
	if len(deepRead) == 0 {
		b.WriteString("deep-read sessions: none\n")
	} else {
		b.WriteString("deep-read sessions:\n")
		for _, s := range deepRead {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	m := frontmatter.Meta{
		Status:  "draft",
		Slug:    slug,
		Title:   title,
		Created: time.Now().UTC().Format(time.RFC3339),
		Project: project,
	}
	path := filepath.Join(tasksDir, slug+".md")
	if err := os.WriteFile(path, frontmatter.Write(m, []byte(b.String())), 0o644); err != nil {
		return "", err
	}
	return slug, nil
}
```

Import `github.com/versality/spore/internal/frontmatter` and
`github.com/versality/spore/internal/task`.

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/dream/ -run TestMintTask -v`
Expected: PASS, both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/dream/mint.go internal/dream/mint_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): mint a per-project dream task"
```

---

### Task 10: Compose the run

**Files:**
- Create: `internal/dream/run.go`
- Create: `internal/dream/run_test.go`

**Interfaces:**
- Consumes: everything above
- Produces: `dream.Options`, `dream.Report`,
  `dream.Run(opts Options) (Report, error)`

- [ ] **Step 1: Write the failing test**

This is acceptance scenario 10, the full mechanical flow.

```go
func TestRunEndToEndProducesDigestAndTask(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	home := "/home/agent"
	projects := t.TempDir()
	cwd := home + "/proj/.worktrees/fix-a"
	writeTranscript(t, filepath.Join(projects, "-home-agent-proj--worktrees-fix-a"),
		"s.jsonl",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		userLine(cwd, "2026-09-01T01:05:00Z", "no, always fetch origin first"))

	tasksDir := t.TempDir()
	rep, err := Run(Options{
		ProjectsRoot: projects,
		Home:         home,
		Project:      "proj",
		TasksDir:     tasksDir,
		Now:          time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC),
		RunID:        "20260902-test",
		DeepReadCap:  3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 1 {
		t.Fatalf("expected 1 in-scope session, got %d", rep.Sessions)
	}
	if rep.TaskSlug == "" {
		t.Fatal("expected a task to be minted")
	}
	digest, err := os.ReadFile(filepath.Join(rep.RunDir, "digest.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(digest), "always fetch origin first") {
		t.Fatalf("operator correction missing from digest:\n%s", digest)
	}
	if _, err := os.Stat(filepath.Join(tasksDir, rep.TaskSlug+".md")); err != nil {
		t.Fatal("task file not written")
	}
}

func TestRunSecondPassSeesNothingNew(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	home := "/home/agent"
	projects := t.TempDir()
	cwd := home + "/proj/.worktrees/fix-a"
	writeTranscript(t, filepath.Join(projects, "-home-agent-proj--worktrees-fix-a"),
		"s.jsonl", userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"))

	opts := Options{
		ProjectsRoot: projects, Home: home, Project: "proj",
		TasksDir: t.TempDir(), Now: time.Date(2026, 9, 2, 3, 0, 0, 0, time.UTC),
		RunID: "run-a", DeepReadCap: 3,
	}
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	opts.RunID = "run-b"
	opts.TasksDir = t.TempDir()
	rep, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sessions != 0 || rep.TaskSlug != "" {
		t.Fatalf("watermark did not hold: %+v", rep)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./internal/dream/ -run TestRun -v`
Expected: FAIL to build, `undefined: Run`.

- [ ] **Step 3: Write minimal implementation**

```go
type Options struct {
	ProjectsRoot string
	Home         string
	Project      string
	TasksDir     string
	Now          time.Time
	RunID        string
	DeepReadCap  int
	DryRun       bool
}

type Report struct {
	Project  string
	RunID    string
	RunDir   string
	Sessions int
	DeepRead int
	TaskSlug string
}

type watermark struct {
	Last string `json:"last"`
}

// Run executes the deterministic half: discover, digest, score, write
// the run directory, advance the watermark, and mint the task that the
// fleet will pick up.
func Run(opts Options) (Report, error) {
	rep := Report{Project: opts.Project, RunID: opts.RunID}

	wmPath, err := statefile.Path(opts.Project, filepath.Join("dreams", "watermark.json"))
	if err != nil {
		return rep, err
	}
	var wm watermark
	if b, err := os.ReadFile(wmPath); err == nil {
		_ = json.Unmarshal(b, &wm)
	}
	since, _ := time.Parse(time.RFC3339, wm.Last)

	sessions, err := Discover(opts.ProjectsRoot, opts.Home, since)
	if err != nil {
		return rep, err
	}
	var mine []Session
	for _, s := range sessions {
		if s.Project == opts.Project {
			mine = append(mine, s)
		}
	}
	rep.Sessions = len(mine)
	if len(mine) == 0 {
		return rep, nil
	}

	digests := make([]SessionDigest, 0, len(mine))
	newest := since
	for _, s := range mine {
		d, err := BuildDigest(s, 3)
		if err != nil {
			continue
		}
		digests = append(digests, d)
		if s.Last.After(newest) {
			newest = s.Last
		}
	}
	if newest.IsZero() {
		newest = opts.Now
	}
	FlagDeepReads(digests, opts.DeepReadCap)

	var deep []string
	for _, d := range digests {
		if d.DeepRead {
			rep.DeepRead++
			deep = append(deep, filepath.Base(d.Session.Path))
		}
	}

	dir, err := RunDir(opts.Project, opts.RunID)
	if err != nil {
		return rep, err
	}
	rep.RunDir = dir
	if opts.DryRun {
		return rep, nil
	}
	if err := os.WriteFile(filepath.Join(dir, "digest.md"),
		[]byte(FormatDigest(digests)), 0o644); err != nil {
		return rep, err
	}

	slug, err := MintTask(opts.TasksDir, opts.Project, opts.RunID, dir, deep)
	if err != nil {
		return rep, err
	}
	rep.TaskSlug = slug

	wm.Last = newest.Format(time.RFC3339)
	if err := statefile.WriteJSONAtomic(wmPath, "dream-watermark", wm); err != nil {
		return rep, err
	}
	return rep, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./internal/dream/ -v`
Expected: PASS, whole package. This is acceptance scenarios 2 and 10
(mechanical half).

- [ ] **Step 5: Commit**

```bash
git add internal/dream/run.go internal/dream/run_test.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): compose the nightly run"
```

---

### Task 11: CLI wiring

**Files:**
- Create: `cmd/spore/dream_cmd.go`
- Create: `cmd/spore/dream_cmd_test.go`
- Modify: `cmd/spore/main.go:166` (add `dream` beside `watch`)

**Interfaces:**
- Consumes: `dream.Run`, `dream.Revert`, `watch.LoadDreamsConfig`,
  `task.Start`
- Produces: `spore dream digest [--project-root DIR] [--dry-run]`,
  `spore dream revert <run-id> [--project NAME]`

- [ ] **Step 1: Write the failing test**

```go
func TestDreamUsageOnUnknownSubcommand(t *testing.T) {
	if code := runDream([]string{"wat"}); code == 0 {
		t.Fatal("unknown subcommand must not exit 0")
	}
}

func TestDreamRevertRequiresRunID(t *testing.T) {
	if code := runDream([]string{"revert"}); code == 0 {
		t.Fatal("revert without a run id must not exit 0")
	}
}

func TestDreamDigestNoopWhenDisabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if code := runDream([]string{"digest", "--project-root", t.TempDir()}); code != 0 {
		t.Fatalf("a disabled project must be a clean no-op, got exit %d", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `nix develop -c go test ./cmd/spore/ -run TestDream -v`
Expected: FAIL to build, `undefined: runDream`.

- [ ] **Step 3: Write minimal implementation**

```go
func runDream(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: spore dream <digest|revert> [flags]")
		return 1
	}
	switch args[0] {
	case "digest":
		return runDreamDigest(args[1:])
	case "revert":
		return runDreamRevert(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: spore dream <digest|revert> [flags]")
		return 1
	}
}

func runDreamDigest(args []string) int {
	fs := flag.NewFlagSet("dream digest", flag.ContinueOnError)
	root := fs.String("project-root", "", "project root (default: cwd)")
	dryRun := fs.Bool("dry-run", false, "report without writing or minting")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "spore dream digest:", err)
		return 2
	}
	cwd, project, code := watchContext(*root)
	if code != 0 {
		return code
	}
	cfg, err := watch.LoadDreamsConfig(project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore dream digest:", err)
		return 1
	}
	if !cfg.Enabled {
		return 0
	}
	now := time.Now().UTC()
	rep, err := dream.Run(dream.Options{
		ProjectsRoot: filepath.Join(os.Getenv("HOME"), ".claude", "projects"),
		Home:         os.Getenv("HOME"),
		Project:      project,
		TasksDir:     filepath.Join(cwd, "tasks"),
		Now:          now,
		RunID:        now.Format("20060102") + "-" + shortSuffix(now),
		DeepReadCap:  cfg.DeepReadCap,
		DryRun:       *dryRun,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore dream digest:", err)
		return 1
	}
	fmt.Printf("dream %s: %d sessions, %d deep-read, task=%s\n",
		rep.RunID, rep.Sessions, rep.DeepRead, orNone(rep.TaskSlug))
	if rep.TaskSlug != "" && !*dryRun {
		if _, err := task.Start(filepath.Join(cwd, "tasks"), rep.TaskSlug); err != nil {
			fmt.Fprintln(os.Stderr, "spore dream digest: start:", err)
			return 1
		}
	}
	return 0
}

func runDreamRevert(args []string) int {
	fs := flag.NewFlagSet("dream revert", flag.ContinueOnError)
	project := fs.String("project", "", "project name (default: from cwd)")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		fmt.Fprintln(os.Stderr, "spore dream revert:", err)
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: spore dream revert <run-id> [--project NAME]")
		return 1
	}
	name := *project
	if name == "" {
		_, p, code := watchContext("")
		if code != 0 {
			return code
		}
		name = p
	}
	restored, err := dream.Revert(name, fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "spore dream revert:", err)
		return 1
	}
	for _, f := range restored {
		fmt.Println("restored", f)
	}
	return 0
}

func shortSuffix(now time.Time) string {
	sum := sha256.Sum256([]byte(now.Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:2])
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
```

In `cmd/spore/main.go`, beside `case "watch":`:

```go
	case "dream":
		os.Exit(runDream(os.Args[2:]))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `nix develop -c go test ./cmd/spore/ -run TestDream -v`
Expected: PASS, three tests.

- [ ] **Step 5: Verify the command is reachable**

Run: `nix develop -c go run ./cmd/spore dream`
Expected: the usage line on stderr, exit 1.

- [ ] **Step 6: Commit**

```bash
git add cmd/spore/dream_cmd.go cmd/spore/dream_cmd_test.go cmd/spore/main.go
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "feat(dream): wire spore dream digest and revert"
```

---

### Task 12: Deploy and documentation

No new Go. This task makes the feature runnable on the host and closes
the spec.

**Files:**
- Create: `docs/todo/nightly-dreaming-deploy.md`
- Modify: `docs/todo/nightly-dreaming.md` (status header)

- [ ] **Step 1: Write the deploy note**

Record the exact steps, following the release-watcher precedent. The
watch stack runs the local binary, not the store binary, so no rebuild
and no sudo is needed.

```markdown
**Status**: reference

# Deploying the nightly dream job

1. Rebuild the local binary:
   `cd <spore checkout> && nix develop -c go build -o ~/.local/bin/spore-evolve ./cmd/spore`
2. Enable per project in `~/.config/spore/<project>/watch.toml`:

       [dreams]
       enabled = true
       deep_read_cap = 3
       max_writes_per_run = 10
       recurrence_threshold = 2

3. Install `spore-dream-<project>.service` and `.timer` under
   `~/.config/systemd/user/`, mirroring
   `spore-release-watch-<project>.*`. `OnCalendar=*-*-* 03:00:00`.
   Service runs `~/.local/bin/spore-evolve dream digest --project-root <root>`.
4. `systemctl --user daemon-reload && systemctl --user enable --now spore-dream-<project>.timer`
5. Verify without waiting for 03:00:
   `~/.local/bin/spore-evolve dream digest --project-root <root> --dry-run`
   then `systemctl --user start spore-dream-<project>.service` and read
   `journalctl --user -u spore-dream-<project>.service -n 50`.

Never hand-edit a home-manager-managed unit in place; if these become
HM-managed, edit at the module level instead.
```

- [ ] **Step 2: Run the whole gate**

Run: `nix develop -c just check`
Expected: green (`fmt-check`, `lint`, `test`, `vuln`, `nix-check`).

- [ ] **Step 3: Flip the spec status**

In `docs/todo/nightly-dreaming.md`, replace the status line with:

```markdown
**Status**: implemented (mechanical half + briefs); deploy pending, see
[nightly-dreaming-deploy.md](nightly-dreaming-deploy.md).
```

- [ ] **Step 4: Commit**

```bash
git add docs/todo/nightly-dreaming.md docs/todo/nightly-dreaming-deploy.md
git -c user.name='Claude (spore)' -c user.email='vlad@marketer.tech' \
  commit -m "docs(dream): deploy note and spec status"
```

- [ ] **Step 5: Report, do not push**

Report to the coordinator with `spore task tell coordinator`, run from
the project root so it routes correctly. The operator pushes the branch
and opens the PR.

---

## Coverage against the spec

| Spec section | Task |
| --- | --- |
| Stage 1 discover | 2 |
| Stage 2 digest, scoring, cap, run dir layout | 3, 4, 10 |
| Stage 2 watermark | 10 |
| Stage 2 mint task per project | 9 |
| Stage 3 propose (evidence packets) | 8 (proposer brief) |
| Stage 4 adversarial review | 8 (reviewer brief) |
| Stage 5 write tiers | 8 (proposer brief), enforced by ledger in 5 |
| Ledger and two-tier bar | 5 |
| Safety: snapshot, revert, caps | 6, 4 |
| Configuration | 7 |
| Deploy | 12 |

Acceptance scenarios: 1 and 2 in Task 2 and Task 10, 3 in Task 3, 4 in
Task 4, 5 and 6 in Task 5, 7 in Task 8 (brief) and Task 5 (gate), 8 in
Task 6, 9 in Task 8 (brief), 10 in Task 10.

## Known gap for the executor

Scenarios 7 and 9 are enforced by brief text, not by Go. The write stage
runs inside the worker, so "nothing is written without a confirmed
verdict" and "skills are never installed" are instructions rather than
machine gates. If the first weeks of real runs show the worker writing
without a verdict, the fix is to move the write stage into Go behind a
verdict check, which is a follow-up task and not in scope here.
