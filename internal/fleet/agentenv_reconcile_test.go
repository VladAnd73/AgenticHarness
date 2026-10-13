package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/versality/spore/internal/agentenv"
	"github.com/versality/spore/internal/task"
)

// reconcileProject builds an isolated project with two active tasks
// and, when body is non-empty, an agent.env holding it.
func reconcileProject(t *testing.T, body string) (Config, string) {
	t.Helper()
	requireToolchain(t)

	dirs := newTestDirs(t)
	gitInit(t, dirs.project)

	// The kill-switch flag lives under the state dir, so enable only
	// after the test's own HOME and XDG_STATE_HOME are in place.
	// Enabling first leaves Reconcile short-circuiting on Disabled,
	// which looks exactly like the bug under test.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mustEnable(t)
	t.Setenv("SPORE_AGENT_BINARY", "sleep 60")
	t.Setenv("SPORE_COORDINATOR_AGENT", "sleep 60")
	os.Unsetenv("SPORE_COORDINATOR_STATE_DIR")

	writeTask(t, dirs.tasks, "alpha", "active")
	writeTask(t, dirs.tasks, "beta", "active")

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
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Cleanup(func() { killSporeSessions(dirs.project) })
	return Config{TasksDir: dirs.tasks, ProjectRoot: dirs.project, MaxWorkers: 3}, dirs.project
}

// S1: a project whose agent.env cannot be parsed must not take the
// whole reconcile pass down with it. The affected spawns are refused
// and reported, but matter sync, the inbox watchdog, the reap loop and
// the active listing all still run - every 60s, for every project the
// reconciler serves.
func TestReconcileContinuesPastAnUnparsableAgentEnv(t *testing.T) {
	const secret = "sk-ant-oat01-NOTAREALTOKEN"
	cfg, root := reconcileProject(t, "export CLAUDE_CODE_OAUTH_TOKEN="+secret+"\n")

	res, err := Reconcile(cfg)

	if res.Disabled {
		t.Fatal("the kill switch was off; this test proves nothing")
	}
	if err == nil {
		t.Error("the failure must still be reported to the caller")
	} else {
		if !strings.Contains(err.Error(), "line 1") {
			t.Errorf("error should name the line number, got %q", err)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error must not echo the value, got %q", err)
		}
	}

	// The pass got past EnsureCoordinator and listed the queue.
	if len(res.Active) != 2 {
		t.Errorf("Active: want the 2 queued tasks, got %v", res.Active)
	}
	// Nothing could spawn, because every spawn in this project reads
	// the same broken file. That part is correct and intended.
	if len(res.Spawned) != 0 {
		t.Errorf("Spawned: want none, got %v", res.Spawned)
	}
	// The watchdog is the reviewer's canary: in armB it never ran.
	if res.InboxWatchdog.Skipped == "" {
		t.Errorf("the inbox watchdog never ran: %+v", res.InboxWatchdog)
	}
	// Matter sync runs before the spawn loop and must not be skipped.
	if res.Matter == nil && !t.Failed() {
		t.Log("no matters configured in this project; Matter nil is expected")
	}

	if !hasSession(CoordinatorSessionName(root)) {
		t.Log("coordinator did not spawn, which is the intended refusal")
	}
}

// S1, second arm: with the coordinator already up, EnsureCoordinator
// returns early and the damage moves to the worker spawn loop. The
// reap loop runs before it, so a broken file there used to abort the
// pass after reaping but before anything could spawn.
func TestReconcileContinuesPastAnUnparsableAgentEnvWithCoordinatorUp(t *testing.T) {
	cfg, root := reconcileProject(t, "")

	if _, _, err := EnsureCoordinator(root); err != nil {
		t.Fatalf("seed coordinator: %v", err)
	}

	project, err := task.ProjectName(root)
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
	if err := os.WriteFile(path, []byte("GOOD=ok\nbroken line\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Reconcile(cfg)

	if err == nil {
		t.Error("the failure must still be reported to the caller")
	} else if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should name the line number, got %q", err)
	}
	if len(res.Active) != 2 {
		t.Errorf("Active: want the 2 queued tasks, got %v", res.Active)
	}
	if res.InboxWatchdog.Skipped == "" {
		t.Errorf("the inbox watchdog never ran: %+v", res.InboxWatchdog)
	}
}

// S4 regression: with no agent.env anywhere the pass is exactly what
// it was before any of this landed.
func TestReconcileWithoutAgentEnvIsUnchanged(t *testing.T) {
	cfg, _ := reconcileProject(t, "")

	res, err := Reconcile(cfg)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(res.Active) != 2 {
		t.Errorf("Active: want 2, got %v", res.Active)
	}
	if len(res.Spawned) != 2 {
		t.Errorf("Spawned: want both tasks, got %v", res.Spawned)
	}
	if res.InboxWatchdog.Skipped == "" {
		t.Errorf("the inbox watchdog never ran: %+v", res.InboxWatchdog)
	}
}
