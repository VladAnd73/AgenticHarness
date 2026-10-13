package fleet

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/versality/spore/internal/agentenv"
	"github.com/versality/spore/internal/task"
)

// agentEnvProject builds an isolated project plus an isolated
// XDG_CONFIG_HOME, and returns the project root. body is written to
// that project's agent.env; an empty body writes no file at all.
//
// SPORE_COORDINATOR_STATE_DIR is unset deliberately: it is exported
// into every spore agent session, so a test that leaves it set reaches
// the operator's live coordinator inbox instead of a temp dir.
func agentEnvProject(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	requireToolchain(t)

	dirs := newTestDirs(t)
	gitInit(t, dirs.project)
	mustEnable(t)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SPORE_AGENT_BINARY", "sleep 60")
	t.Setenv("SPORE_COORDINATOR_AGENT", "sleep 60")
	os.Unsetenv("SPORE_COORDINATOR_STATE_DIR")

	if body != "" {
		project, err := task.ProjectName(dirs.project)
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
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}

	t.Cleanup(func() { killSporeSessions(dirs.project) })
	return dirs.project
}

// paneEnviron reads the real environment of the process tmux started
// in session. tmux show-environment reports server and session state,
// not what the child actually received, so it cannot answer this.
//
// It polls rather than reading once. `tmux new-session -d` returns as
// soon as the server has forked the pane process, which has not
// necessarily reached execve yet, and /proc/<pid>/environ is empty for
// the width of that window. A single read that lands there reports
// every variable as missing even though tmux delivered them all, which
// is a false failure for a positive assertion and a false pass for a
// negative one. SPORE_TASK_SLUG is the sentinel: every coordinator
// spawn seeds it, so seeing it proves the post-execve environment is
// in view.
func paneEnviron(t *testing.T, session string) map[string]string {
	t.Helper()
	out, err := exec.Command("tmux", "-L", testTmuxSocket, "list-panes", "-t", session, "-F", "#{pane_pid}").Output()
	if err != nil {
		t.Fatalf("list-panes %s: %v", session, err)
	}
	pid := strings.TrimSpace(string(out))

	deadline := time.Now().Add(10 * time.Second)
	for {
		env, readErr := readEnviron(pid)
		if readErr == nil && env["SPORE_TASK_SLUG"] != "" {
			return env
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never exposed a spawned environment: %d variables, no SPORE_TASK_SLUG (last read error %v)",
				pid, len(env), readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readEnviron(pid string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join("/proc", pid, "environ"))
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, kv := range strings.Split(string(raw), "\x00") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stderr = saved
	w.Close()
	return <-done
}

// A2
func TestCoordinatorSpawnSeedsAgentEnvIntoTheSpawnedProcess(t *testing.T) {
	root := agentEnvProject(t, "AGENTENV_PROBE=bar\n", 0o600)

	session, spawned, err := EnsureCoordinator(root)
	if err != nil {
		t.Fatalf("EnsureCoordinator: %v", err)
	}
	if !spawned {
		t.Fatal("expected a fresh spawn; the test inspected a session it did not create")
	}

	env := paneEnviron(t, session)
	if got := env["AGENTENV_PROBE"]; got != "bar" {
		t.Fatalf("AGENTENV_PROBE in spawned process: want %q, got %q", "bar", got)
	}
}

// A1
func TestCoordinatorSpawnWithoutAgentEnvSeedsNothingExtra(t *testing.T) {
	root := agentEnvProject(t, "", 0)

	var session string
	var spawned bool
	stderr := captureStderr(t, func() {
		var err error
		session, spawned, err = EnsureCoordinator(root)
		if err != nil {
			t.Errorf("EnsureCoordinator: %v", err)
		}
	})
	if t.Failed() {
		t.FailNow()
	}
	if !spawned {
		t.Fatal("expected a fresh spawn")
	}
	if stderr != "" {
		t.Errorf("a project with no agent.env must stay silent, got %q", stderr)
	}

	env := paneEnviron(t, session)
	if _, ok := env["AGENTENV_PROBE"]; ok {
		t.Error("AGENTENV_PROBE must not be set when the project has no agent.env")
	}
	if env["SPORE_PROJECT_ROOT"] != root {
		t.Errorf("spore's own vars must still be seeded: want %q, got %q", root, env["SPORE_PROJECT_ROOT"])
	}
}

// A4
func TestCoordinatorSpawnRejectsReservedKeyAndKeepsSporesOwnValue(t *testing.T) {
	const evil = "/tmp/agentenv-hijacked-root"
	root := agentEnvProject(t, "SPORE_PROJECT_ROOT="+evil+"\nAGENTENV_PROBE=kept\n", 0o600)

	var session string
	stderr := captureStderr(t, func() {
		var err error
		session, _, err = EnsureCoordinator(root)
		if err != nil {
			t.Errorf("a reserved key must not stop the spawn: %v", err)
		}
	})
	if t.Failed() {
		t.FailNow()
	}

	if !strings.Contains(stderr, "SPORE_PROJECT_ROOT") {
		t.Errorf("stderr should name the rejected key, got %q", stderr)
	}
	if strings.Contains(stderr, evil) {
		t.Errorf("stderr must not echo the value, got %q", stderr)
	}

	env := paneEnviron(t, session)
	if env["SPORE_PROJECT_ROOT"] != root {
		t.Errorf("spore's own value must win: want %q, got %q", root, env["SPORE_PROJECT_ROOT"])
	}
	if env["AGENTENV_PROBE"] != "kept" {
		t.Errorf("non-reserved keys in the same file must still be seeded, got %q", env["AGENTENV_PROBE"])
	}
}

// A5
func TestCoordinatorSpawnRefusesOnMalformedLineWithoutEchoingIt(t *testing.T) {
	const secret = "sk-ant-oat01-NOTAREALTOKEN"
	root := agentEnvProject(t, "GOOD=ok\n\nexport TOKEN="+secret+"\n", 0o600)

	_, _, err := EnsureCoordinator(root)
	if err == nil {
		t.Fatal("want a refusal for a malformed agent.env, got nil")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error should name the line number, got %q", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error must not echo the line content, got %q", err)
	}
	if hasSession(CoordinatorSessionName(root)) {
		t.Error("no session should exist after a refused spawn")
	}
}

// A7
func TestCoordinatorSpawnWarnsOnLooseModeButStillSeeds(t *testing.T) {
	const secret = "sk-ant-oat01-NOTAREALTOKEN"
	root := agentEnvProject(t, "AGENTENV_PROBE="+secret+"\n", 0o644)

	var session string
	stderr := captureStderr(t, func() {
		var err error
		session, _, err = EnsureCoordinator(root)
		if err != nil {
			t.Errorf("a loose mode must warn, not refuse: %v", err)
		}
	})
	if t.Failed() {
		t.FailNow()
	}

	if !strings.Contains(stderr, "0644") {
		t.Errorf("warning should name the mode, got %q", stderr)
	}
	if !strings.Contains(stderr, "chmod 600") {
		t.Errorf("warning should say how to fix it, got %q", stderr)
	}
	if strings.Contains(stderr, secret) {
		t.Errorf("warning must not echo the value, got %q", stderr)
	}
	if env := paneEnviron(t, session); env["AGENTENV_PROBE"] != secret {
		t.Error("the value should still be seeded after a warning")
	}
}
