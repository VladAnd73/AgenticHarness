package dream

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func writeVerdictFile(t *testing.T, runDir string, n int, body string) {
	t.Helper()
	dir := verdictsDir(runDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strconv.Itoa(n)+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// TestWriteRunLeavesTargetsUntouchedWhenEveryPacketIsRefuted covers
// acceptance scenario 7: a run whose every packet is refuted must not
// modify anything, and the report must say why.
func TestWriteRunLeavesTargetsUntouchedWhenEveryPacketIsRefuted(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "state.md")

	writePacketFile(t, runDir, 1, `{
		"claim": "flag --foo exists",
		"type": "tool-behavior",
		"tier": "lesson",
		"target": "`+jsonPath(target)+`",
		"text": "### RULE: foo (2026-09-01)\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"refuted","reason":"no such flag in --help","proof":"ran spore dream --help"}`)

	// The real pipeline always gates a packet before it reaches a
	// reviewer, which is what seeds its ledger entry; write only ever
	// records against an entry gate already created.
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	report, err := WriteRun("proj", "run-1", runDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 0 {
		t.Fatalf("expected nothing written, got %+v", report.Written)
	}
	if len(report.Refused) != 1 || report.Refused[0].Reason != "no such flag in --help" {
		t.Fatalf("expected one refusal with its reason, got %+v", report.Refused)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target file must not have been created: %v", err)
	}
	reportBody := readReportFile(t, runDir)
	if !strings.Contains(reportBody, "no such flag in --help") {
		t.Errorf("report.md must list the refusal reason:\n%s", reportBody)
	}

	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(TypeToolBehavior, "flag --foo exists")
	if l.Entries[fp].Status != StatusRefuted {
		t.Fatalf("ledger status = %q, want %q", l.Entries[fp].Status, StatusRefuted)
	}
}

// TestWriteRunWritesAConfirmedLessonAndSnapshotsFirst exercises the
// lesson tier: the block lands in state.md in a form
// internal/coordinator/statedebt can parse, and a backup exists before
// the write happens.
func TestWriteRunWritesAConfirmedLessonAndSnapshotsFirst(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "state.md")
	os.WriteFile(target, []byte("# project state\n"), 0o644)

	writePacketFile(t, runDir, 1, `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"tier": "lesson",
		"target": "`+jsonPath(target)+`",
		"text": "### RULE: prefer small commits (2026-09-01)\n\nSplit large diffs.\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"operator said so verbatim","proof":"session sesn-1 at 2026-09-01T00:00:00Z"}`)

	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	report, err := WriteRun("proj", "run-1", runDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 1 {
		t.Fatalf("expected one write, got %+v", report.Written)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "### RULE: prefer small commits (2026-09-01)") {
		t.Fatalf("lesson block missing from state.md:\n%s", body)
	}
	if !strings.Contains(string(body), "# project state") {
		t.Fatalf("original content lost:\n%s", body)
	}

	if _, err := os.Stat(filepath.Join(runDir, "manifest.json")); err != nil {
		t.Fatalf("expected a manifest recording the pre-write snapshot: %v", err)
	}

	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(TypeOperatorPreference, "operator wants small commits")
	if l.Entries[fp].Status != StatusWritten {
		t.Fatalf("ledger status = %q, want %q", l.Entries[fp].Status, StatusWritten)
	}
}

// TestWriteRunCreatesStateFileWhenMissing covers the "a project missing
// a target is not an error" rule for the lesson tier.
func TestWriteRunCreatesStateFileWhenMissing(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "state.md")

	writePacketFile(t, runDir, 1, `{
		"claim": "operator wants small commits",
		"type": "operator-preference",
		"tier": "lesson",
		"target": "`+jsonPath(target)+`",
		"text": "### RULE: prefer small commits (2026-09-01)\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"x","proof":"y"}`)

	if _, err := WriteRun("proj", "run-1", runDir, 10); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("state.md must be created: %v", err)
	}
	if !strings.Contains(string(body), "### RULE: prefer small commits") {
		t.Fatalf("lesson block missing:\n%s", body)
	}
}

// TestWriteRunSkillProposalNeverTouchesRealSkillsDir covers acceptance
// scenario 9.
func TestWriteRunSkillProposalNeverTouchesRealSkillsDir(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	fakeHome := t.TempDir()
	skillsDir := filepath.Join(fakeHome, ".claude", "skills")

	writePacketFile(t, runDir, 1, `{
		"claim": "bringing up the backend needs three steps in order",
		"type": "process-pattern",
		"tier": "skill",
		"target": ".claude/skills/starting-the-backend/SKILL.md",
		"text": "---\nname: starting-the-backend\n---\n\nStep one, step two, step three.\n"
	}`)
	writeVerdictFile(t, runDir, 1, `{"verdict":"confirmed","reason":"x","proof":"y"}`)

	report, err := WriteRun("proj", "run-1", runDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.SkillProposals) != 1 {
		t.Fatalf("expected one skill proposal, got %+v", report.SkillProposals)
	}
	proposalPath := report.SkillProposals[0]
	if !strings.HasPrefix(proposalPath, filepath.Join(runDir, "skill-proposals")) {
		t.Fatalf("skill proposal %q must live under the run directory", proposalPath)
	}
	if _, err := os.ReadFile(proposalPath); err != nil {
		t.Fatalf("proposal file must exist: %v", err)
	}
	if _, err := os.Stat(skillsDir); !os.IsNotExist(err) {
		t.Fatalf("a real skills directory must never be created by this run")
	}
}

// TestWriteRunEnforcesMaxWritesPerRun covers the cap: a confirmed
// packet past the cap is held as a candidate, not discarded and not
// written.
func TestWriteRunEnforcesMaxWritesPerRun(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	dir := t.TempDir()

	for i := 1; i <= 3; i++ {
		target := filepath.Join(dir, "mem", "claim"+strconv.Itoa(i)+".md")
		writePacketFile(t, runDir, i, `{
			"claim": "distinct claim number `+strconv.Itoa(i)+`",
			"type": "host-state",
			"tier": "memory",
			"target": "`+jsonPath(target)+`",
			"text": "---\nname: claim-`+strconv.Itoa(i)+`\ndescription: claim number `+strconv.Itoa(i)+`\nmetadata:\n  type: project\n---\n\nBody.\n"
		}`)
		writeVerdictFile(t, runDir, i, `{"verdict":"confirmed","reason":"x","proof":"y"}`)
	}
	if _, err := GateRun("proj", "run-1", runDir, 1); err != nil {
		t.Fatal(err)
	}

	report, err := WriteRun("proj", "run-1", runDir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 2 {
		t.Fatalf("expected 2 written, got %d: %+v", len(report.Written), report.Written)
	}
	if len(report.Held) != 1 {
		t.Fatalf("expected 1 held over cap, got %d: %+v", len(report.Held), report.Held)
	}
	if report.Held[0].Claim != "distinct claim number 3" {
		t.Fatalf("expected the third packet held (numeric order), got %+v", report.Held[0])
	}

	l, err := LoadLedger("proj")
	if err != nil {
		t.Fatal(err)
	}
	heldFP := Fingerprint(TypeHostState, "distinct claim number 3")
	if got := l.Entries[heldFP]; got != nil && got.Status == StatusWritten {
		t.Fatalf("a packet held over the cap must not be recorded as written")
	}
}

// TestWriteRunUnreviewedPacketIsIgnored covers a packet the gate held
// back before it ever reached a reviewer: no verdict file exists, so
// WriteRun must skip it entirely rather than erroring.
func TestWriteRunUnreviewedPacketIsIgnored(t *testing.T) {
	writeTestEnv(t)
	runDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "state.md")
	writePacketFile(t, runDir, 1, `{
		"claim": "some inferred claim seen once",
		"type": "host-state",
		"tier": "lesson",
		"target": "`+jsonPath(target)+`",
		"text": "### RULE: x (2026-09-01)\n"
	}`)
	report, err := WriteRun("proj", "run-1", runDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 0 || len(report.Refused) != 0 || len(report.Held) != 0 {
		t.Fatalf("an unreviewed packet must be silently skipped, got %+v", report)
	}
}

func jsonPath(p string) string {
	return strings.ReplaceAll(p, `\`, `\\`)
}

func readReportFile(t *testing.T, runDir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(runDir, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
