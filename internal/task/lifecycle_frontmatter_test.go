package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/versality/spore/internal/task/frontmatter"
)

// The triaging-a-watched-slack-thread skill hand-adds an unrecognized
// slack_thread frontmatter key and relies on it surviving every future
// status flip untouched. Block goes through flipStatus (parse, mutate
// Status, re-Write), the real write path a coordinator's later `spore
// task done` would also use - prove Extra actually round-trips there,
// not just inside the frontmatter package's own tests.
func TestBlockPreservesUnrecognizedFrontmatterKey(t *testing.T) {
	tasksDir := t.TempDir()
	taskPath := filepath.Join(tasksDir, "x.md")
	body := "---\nstatus: active\nslug: x\ntitle: X\n" +
		"slack_thread: https://example.slack.com/archives/C1/p1789549306348279\n" +
		"---\n"
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Block(tasksDir, "x"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	raw, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := frontmatter.Parse(raw)
	if err != nil {
		t.Fatalf("Parse after Block: %v", err)
	}
	if m.Status != "blocked" {
		t.Errorf("status = %q, want blocked", m.Status)
	}
	want := "https://example.slack.com/archives/C1/p1789549306348279"
	if got := m.Extra["slack_thread"]; got != want {
		t.Errorf("slack_thread = %q, want %q (unrecognized key must survive the status-flip write)", got, want)
	}
}
