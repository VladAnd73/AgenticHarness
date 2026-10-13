package fleet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/task"
)

// Scenario 6, end to end, nothing in spore stubbed. A project pins its
// own config directory. The kernel spawns a real worker into a real
// tmux session; the agent process in that session writes its transcript
// where the directory it inherited says to. Then the real `spore`
// binary, run from a process with a bare environment -- the reconciler's
// and the operator's shape, not the agent's -- finds that transcript and
// reads it.
//
// The agent is a shell script rather than claude itself: claude needs
// credentials, a terminal and minutes of wall clock. It stands in for
// exactly one behaviour, writing a session transcript under
// $CLAUDE_CONFIG_DIR using Claude Code's own directory naming, which
// was read off this host's ~/.claude/projects before it was written.
// Every other part of the path is production code.
func TestWorkerWritesItsTranscriptInTheProjectsConfigDirEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the spore binary")
	}
	for _, bin := range []string{"git", "tmux", "go"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available: %v", bin, err)
		}
	}

	home := t.TempDir()
	cfgDir := filepath.Join(home, "cfg-proj")
	repo := filepath.Join(home, "proj")
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	os.Unsetenv("CLAUDE_CONFIG_DIR")

	sporeBin := buildSpore(t, filepath.Join(home, "bin"))

	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "spore.toml"),
		[]byte("[claude]\nconfig_dir = \""+cfgDir+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tasksDir := filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug := "demo"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"),
		[]byte("---\nstatus: draft\nslug: demo\ntitle: Demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPORE_AGENT_BINARY", writeTranscribingAgent(t, home))

	session, err := task.Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("task.Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	worktree := filepath.Join(repo, ".worktrees", slug)
	written := filepath.Join(cfgDir, "projects", claudeProjectDir(worktree), "session.jsonl")
	waitForFile(t, written)

	// The reading process gets a bare environment on purpose: no
	// CLAUDE_CONFIG_DIR, no SPORE_PROJECT_ROOT. Only the project's own
	// spore.toml can lead it to the directory the worker just wrote.
	cmd := exec.Command(sporeBin, "coordinator", "verify-done", slug, "--root", repo)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("spore coordinator verify-done: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "wt-merge") {
		t.Errorf("verify-done did not read the worker's transcript at %s\ngot:\n%s", written, out)
	}
}

// writeTranscribingAgent returns a stand-in agent that writes one
// transcript line under the config directory it was handed, naming the
// directory the way Claude Code names it, then parks.
func writeTranscribingAgent(t *testing.T, home string) string {
	t.Helper()
	script := filepath.Join(home, "transcribing-agent.sh")
	body := `#!/bin/sh
set -e
cwd=$(pwd)
encoded=$(printf '%s' "$cwd" | sed 's/[^a-zA-Z0-9-]/-/g')
dir="$CLAUDE_CONFIG_DIR/projects/$encoded"
mkdir -p "$dir"
printf '%s\n' '{"type":"assistant","timestamp":"2026-10-13T06:00:00Z","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"wt merge"}}]}}' > "$dir/session.jsonl"
sleep 86400
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	for range 80 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("worker never wrote %s", path)
}
