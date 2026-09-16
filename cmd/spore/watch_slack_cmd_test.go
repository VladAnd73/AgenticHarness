package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/task"
	"github.com/versality/spore/internal/watch"
)

func fakeSlackServer(t *testing.T, historyBody string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case "conversations.history":
			w.Write([]byte(historyBody))
		case "chat.getPermalink":
			w.Write([]byte(`{"ok":true,"permalink":"https://slack.example/p1"}`))
		case "conversations.replies":
			w.Write([]byte(`{"ok":true,"messages":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("SPORE_SLACK_API_BASE", srv.URL)
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	return srv.URL
}

func writeSlackToml(t *testing.T, cfgDir, project, body string) {
	t.Helper()
	p := filepath.Join(cfgDir, "spore", project)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "watch.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The CLI wires real delivery: a new thread lands a tell in the
// coordinator's OWN project message inbox (single fixed-project design -
// no cross-project TellProject) plus a poke, via the existing
// tellWithPoke wrapper.
func TestWatchSlackRealDeliveryPaths(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	cfgDir := t.TempDir()
	stateDir := t.TempDir()
	coordDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", coordDir)
	fakeSlackServer(t, `{"ok":true,"has_more":false,"messages":[
		{"ts":"1000.0002","user":"U1","text":"login is broken"}
	]}`)
	writeSlackToml(t, cfgDir, "hostproj", `
[slack]
enabled = true
channel_id = "C1"
`)
	st, err := watch.LoadSlackState("hostproj")
	if err != nil {
		t.Fatal(err)
	}
	st.Cursor = "1000.0000"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	// task.Tell resolves its target project from the process's cwd (via
	// ProjectName), not from the string handed to tellWithPoke - so the
	// test must actually be standing in a directory that resolves to
	// "hostproj" for the envelope to land where the assertions look for
	// it. A non-git temp dir falls back to its own basename.
	projRoot := filepath.Join(t.TempDir(), "hostproj")
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(projRoot); err != nil {
		t.Fatal(err)
	}

	tell := tellWithPoke("hostproj", task.Tell)
	taskActive := func(tasksDir, slug string) (bool, error) { return false, nil }
	rep, err := watch.RunSlack(projRoot, "hostproj", false, time.Now(), tell, taskActive)
	if err != nil {
		t.Fatal(err)
	}
	if rep.NewThreads != 1 {
		t.Fatalf("want 1 new thread, got %+v", rep)
	}
	msgInbox := filepath.Join(stateDir, "spore", "hostproj", "coordinator", "inbox")
	if n := countJSON(t, msgInbox); n != 1 {
		t.Fatalf("message envelopes in %s = %d, want 1", msgInbox, n)
	}
	wakeChannel := filepath.Join(coordDir, "hostproj", "inbox")
	if n := countJSON(t, wakeChannel); n != 1 {
		t.Fatalf("poke files in %s = %d, want 1", wakeChannel, n)
	}
}

// runWatchSlack (the CLI entry) parses args, honors --dry-run, prints the
// one-line summary, and returns exit 0.
func TestRunWatchSlackCLI(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	cfgDir := t.TempDir()
	stateDir := t.TempDir()
	coordDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("SPORE_COORDINATOR_STATE_DIR", coordDir)
	fakeSlackServer(t, `{"ok":true,"has_more":false,"messages":[
		{"ts":"1000.0002","user":"U1","text":"login is broken"}
	]}`)

	projRoot := filepath.Join(t.TempDir(), "hostproj")
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSlackToml(t, cfgDir, "hostproj", `
[slack]
enabled = true
channel_id = "C1"
`)
	st, err := watch.LoadSlackState("hostproj")
	if err != nil {
		t.Fatal(err)
	}
	st.Cursor = "1000.0000"
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	const summary = "slack-watch hostproj: 1 new thread(s), 0 repl(y/ies) relayed, 0 thread(s) pruned"
	out, code := captureRun(t, func() int {
		return runWatchSlack([]string{"slack", "--project-root", projRoot})
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%q", code, out)
	}
	if !strings.Contains(out, summary) {
		t.Fatalf("summary missing/wrong: %q", out)
	}
}
