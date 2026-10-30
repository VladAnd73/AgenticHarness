package tokenmonitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The coordinator runs the respawn command on a server whose
// default-shell may be the spore-attach login shell. A single-string
// pane command would go through that shell and never start.
func TestRespawnCommandRunsOnAServerWithABrokenDefaultShell(t *testing.T) {
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skipf("tmux not available: %v", err)
	}
	root := t.TempDir()
	tmpdir, err := os.MkdirTemp("", "trs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpdir) })
	loginShell := filepath.Join(root, "spore-attach")
	marker := filepath.Join(root, "launched")
	launch := filepath.Join(root, "launch")
	for path, body := range map[string]string{
		loginShell: "#!/bin/sh\nexit 1\n",
		launch:     "#!/bin/sh\ntouch " + marker + "\nexec sleep 600\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var env []string
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "TMUX", "TMUX_PANE", "TMUX_TMPDIR", "SHELL":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TMUX_TMPDIR="+tmpdir, "SHELL="+loginShell, "HOME="+root)
	tmux := func(args ...string) {
		t.Helper()
		cmd := exec.Command(realTmux, append([]string{"-f", "/dev/null", "-L", "default"}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
	}
	tmux("new-session", "-d", "-s", "spore/p/coordinator", "sleep", "600")
	t.Cleanup(func() {
		cmd := exec.Command(realTmux, "-L", "default", "kill-server")
		cmd.Env = env
		_ = cmd.Run()
	})

	script := strings.ReplaceAll(coordinatorRespawnCommand, "/usr/local/bin/spore-coordinator-launch", launch)
	script = strings.ReplaceAll(script, `"$(tmux display-message -p '#S'):0"`, "=spore/p/coordinator:0")
	run := exec.Command("bash", "-c", script)
	run.Env = env
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("respawn: %v: %s", err, out)
	}
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the respawned pane never ran the launcher")
}
