package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TellProject delivers to a project named explicitly, not the current cwd's
// project, so a watcher can message OTHER projects' coordinator inboxes.
func TestTellProjectDeliversByProjectName(t *testing.T) {
	// cwd is some unrelated dir; the target project is named, not inferred.
	t.Chdir(t.TempDir())
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	if err := TellProject("frontend", "coordinator", "sync it"); err != nil {
		t.Fatalf("TellProject: %v", err)
	}

	inbox := filepath.Join(state, "spore", "frontend", "coordinator", "inbox")
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox entries = %d, want 1", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(inbox, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Slug string `json:"slug"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Slug != "coordinator" || got.Msg != "sync it" {
		t.Fatalf("payload = %+v, want slug=coordinator msg='sync it'", got)
	}
}
