package task

import (
	"fmt"
	"os/exec"

	"github.com/versality/spore/internal/task/frontmatter"
)

// netnsBinary is the userspace-NAT binary that provides each worker its
// own private network namespace with outbound internet + DNS. pasta
// ships in the `passt` package (bundled in the spore flake). Verified
// primitive: `pasta --config-net -- <cmd>` runs <cmd> in a fresh
// user+net namespace with a configured uplink, DNS, and a private
// loopback, so N workers reuse identical ports blind to each other
// while host ports stay free. No sudo, no nested unshare needed.
const netnsBinary = "pasta"

// netnsWrapPrefix is prepended to the resolved worker command when
// [worker] isolate_network is on. Everything after `--` is the command
// pasta execs inside the namespace.
const netnsWrapPrefix = netnsBinary + " --config-net -- "

// lookPath is exec.LookPath, indirected so tests can stub binary
// discovery without touching the real PATH.
var lookPath = exec.LookPath

// wrapForIsolation returns the command tmux should run for a worker. When
// isolate is false it returns agent unchanged. When true it verifies the
// netns binary is on PATH (failing loud with an actionable error if not)
// and prefixes the pasta netns wrapper.
func wrapForIsolation(agent string, isolate bool) (string, error) {
	if !isolate {
		return agent, nil
	}
	if _, err := lookPath(netnsBinary); err != nil {
		return "", fmt.Errorf(
			"[worker] isolate_network is on but %q is not on PATH: install passt (nix: nixpkgs#passt, or add it to your host flake) or set isolate_network = false: %w",
			netnsBinary, err,
		)
	}
	return netnsWrapPrefix + agent, nil
}

// workerSpawnCommand resolves the command tmux runs for a worker: the
// agent binary (env > frontmatter > default) optionally wrapped in the
// netns launcher when the project's [worker] isolate_network is on. It
// is the single composition seam ensureSession relies on, kept pure so
// the off/on wrap and the missing-pasta fail-loud path are testable
// without spawning tmux. Coordinators never call this: they spawn via
// internal/fleet/coordinator.go, so isolation cannot leak onto them.
func workerSpawnCommand(projectRoot string, m frontmatter.Meta) (string, error) {
	agent, err := workerAgentCommand(m)
	if err != nil {
		return "", err
	}
	cfg, err := LoadWorkerConfig(projectRoot)
	if err != nil {
		return "", err
	}
	return wrapForIsolation(agent, cfg.IsolateNetwork)
}
