package task

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/agentenv"
)

// agentEnvWorker builds an isolated project with one draft task plus
// an isolated XDG_CONFIG_HOME holding body as the project's agent.env
// (an empty body writes no file). It returns the tasks dir and slug.
func agentEnvWorker(t *testing.T, body string, agentCmd string) (tasksDir, slug string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available: %v", err)
	}

	repo := filepath.Join(t.TempDir(), strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")

	t.Chdir(repo)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SPORE_AGENT_BINARY", agentCmd)
	os.Unsetenv("SPORE_COORDINATOR_STATE_DIR")

	tasksDir = filepath.Join(repo, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	slug = "probe"
	meta := "---\nstatus: draft\nslug: probe\ntitle: Probe\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(tasksDir, slug+".md"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	if body != "" {
		project, err := ProjectName(repo)
		if err != nil {
			t.Fatal(err)
		}
		path, err := agentenv.Path(project)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return tasksDir, slug
}

func workerPaneEnviron(t *testing.T, session string) map[string]string {
	t.Helper()
	out, err := exec.Command("tmux", "-L", testTmuxSocket, "list-panes", "-t", session, "-F", "#{pane_pid}").Output()
	if err != nil {
		t.Fatalf("list-panes %s: %v", session, err)
	}
	pid := strings.TrimSpace(string(out))
	raw, err := os.ReadFile(filepath.Join("/proc", pid, "environ"))
	if err != nil {
		t.Fatalf("read /proc/%s/environ: %v", pid, err)
	}
	env := map[string]string{}
	for _, kv := range strings.Split(string(raw), "\x00") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

// A3
func TestWorkerSpawnSeedsAgentEnvIntoTheSpawnedProcess(t *testing.T) {
	tasksDir, slug := agentEnvWorker(t, "AGENTENV_PROBE=bar\n", "sleep 60")

	session, err := Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	if got := workerPaneEnviron(t, session)["AGENTENV_PROBE"]; got != "bar" {
		t.Fatalf("AGENTENV_PROBE in spawned worker: want %q, got %q", "bar", got)
	}
}

// A3, negative half: no file means no extra variable.
func TestWorkerSpawnWithoutAgentEnvSeedsNothingExtra(t *testing.T) {
	tasksDir, slug := agentEnvWorker(t, "", "sleep 60")

	session, err := Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	if _, ok := workerPaneEnviron(t, session)["AGENTENV_PROBE"]; ok {
		t.Error("AGENTENV_PROBE must not be set when the project has no agent.env")
	}
}

func md5OfFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "<absent>"
		}
		t.Fatal(err)
	}
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// A6: the scenario the whole feature exists for. Budget state is one
// host-wide file by default, so two projects on two accounts would
// read each other's remaining quota. agent.env must be able to move a
// project's budget state somewhere else, and doing so must leave the
// host-wide file alone.
func TestWorkerSpawnRoutesBudgetStateThroughAgentEnv(t *testing.T) {
	spore := buildSporeBinary(t)

	// Captured before agentEnvWorker repoints HOME: the pane inherits
	// the real HOME from the tmux server, so this is the file a
	// misrouted capture would actually land in.
	hostState := filepath.Join(os.Getenv("HOME"), ".local", "state", "agent-budget", "state.json")
	before := md5OfFile(t, hostState)

	budgetDir := t.TempDir()
	outDir := t.TempDir()
	script := filepath.Join(outDir, "agent.sh")
	const headerLine = `{"identity":"agentenv-test","headers":{"anthropic-ratelimit-unified-status":"allowed"}}`
	// Refusing to run when the variable is absent keeps the red run
	// from appending to the operator's real budget spool: the pane
	// inherits the real HOME from the tmux server, not from the test.
	body := fmt.Sprintf(`#!/bin/sh
if [ -z "$AGENT_BUDGET_STATE_DIR" ]; then
  printf 'AGENT_BUDGET_STATE_DIR-unset' > %[1]q/result
  sleep 60
  exit 0
fi
printf '%%s\n' %[2]q | %[3]q budget capture > %[1]q/capture.log 2>&1
printf '%%s' "$?" > %[1]q/result
sleep 60
`, outDir, headerLine, spore)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	tasksDir, slug := agentEnvWorker(t, "AGENT_BUDGET_STATE_DIR="+budgetDir+"\n", script)

	session, err := Start(tasksDir, slug)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", testTmuxSocket, "kill-session", "-t", session).Run()
	})

	resultPath := filepath.Join(outDir, "result")
	var result string
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(resultPath); err == nil {
			result = string(b)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if result == "" {
		t.Fatal("the spawned worker never ran: no result file")
	}
	if result == "AGENT_BUDGET_STATE_DIR-unset" {
		t.Fatal("AGENT_BUDGET_STATE_DIR from agent.env never reached the worker")
	}
	if result != "0" {
		log, _ := os.ReadFile(filepath.Join(outDir, "capture.log"))
		t.Fatalf("spore budget capture exited %s: %s", result, log)
	}

	entries, err := os.ReadDir(budgetDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("budget state should have landed in the agent.env directory, got %v (err %v)", entries, err)
	}

	if after := md5OfFile(t, hostState); after != before {
		t.Errorf("the host-wide budget state was modified: %s -> %s", before, after)
	}
}

func buildSporeBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not available: %v", err)
	}
	out := filepath.Join(t.TempDir(), "spore")
	cmd := exec.Command("go", "build", "-o", out, "github.com/versality/spore/cmd/spore")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v: %s", err, b)
	}
	return out
}
