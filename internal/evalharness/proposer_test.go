package evalharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/dream"
)

var fixedNow = time.Date(2026, 9, 8, 3, 0, 0, 0, time.UTC)

func TestBuildProposerRunMintsARealRunDirAndEmbedsTheRealBriefByDefault(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prompt, runDir, err := BuildProposerRun(sb, "demonstrated", "20260908-test", fixedNow, "")
	if err != nil {
		t.Fatalf("BuildProposerRun: %v", err)
	}
	if !strings.Contains(prompt, dream.ProposerBrief) {
		t.Fatalf("expected the prompt to embed the real proposer brief verbatim")
	}
	if !strings.Contains(prompt, runDir) {
		t.Fatalf("expected the prompt to name the run directory %s", runDir)
	}
	digest := readRunFile(t, runDir, "digest.md")
	if !strings.Contains(digest, "panic: runtime error") {
		t.Fatalf("expected the digest to carry the fixture's failure text, got:\n%s", digest)
	}
}

func TestBuildProposerRunHonoursAnOverrideBriefInsteadOfTheEmbeddedOne(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	override := "# Totally different proposer brief\n\nNever write any packet, ever."
	prompt, _, err := BuildProposerRun(sb, "demonstrated", "20260908-test", fixedNow, override)
	if err != nil {
		t.Fatalf("BuildProposerRun: %v", err)
	}
	if !strings.Contains(prompt, override) {
		t.Fatalf("expected the prompt to contain the override brief text")
	}
	if strings.Contains(prompt, dream.ProposerBrief) {
		t.Fatalf("expected the override to replace the embedded brief, not sit alongside it")
	}
}

func readRunFile(t *testing.T, runDir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(runDir, name))
	if err != nil {
		t.Fatalf("read %s/%s: %v", runDir, name, err)
	}
	return string(b)
}
