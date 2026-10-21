package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// watcherPidName keys the pid file on the Claude session. Any claude
// started inside an agent's tmux session inherits SPORE_TASK_INBOX, so
// keying on the inbox alone let a scratch session stop the agent's
// watcher. Without a usable session id it falls back to one file per
// inbox.
func watcherPidName(session string) string {
	if !sessionIDPattern.MatchString(session) {
		return ".watcher.pid"
	}
	return ".watcher." + session + ".pid"
}

// supersedeWatcher makes the caller the only Stop-hook watcher for this
// session on inboxDir. claude-code spawns a new asyncRewake watcher at
// the end of every turn and never reaps the previous one, so without
// this they pile up for the full timeout. The older watcher gets
// SIGTERM; see MayExitOnSupersede for how it answers. The returned func
// drops the pid file if it still names this process.
func supersedeWatcher(inboxDir, session string) func() {
	path := filepath.Join(inboxDir, watcherPidName(session))
	self := os.Getpid()
	selfStart, ok := procStartTime(self)
	if !ok {
		return func() {}
	}
	if b, err := os.ReadFile(path); err == nil {
		if pid, start, ok := parsePidFile(b); ok && pid != self && isInboxWatcher(pid) {
			// The start time guards against pid reuse: a pid left by
			// a killed watcher may now belong to another one.
			if cur, ok := procStartTime(pid); ok && cur == start {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
	}
	record := strconv.Itoa(self) + " " + selfStart + "\n"
	tmp := path + "." + strconv.Itoa(self)
	if err := os.WriteFile(tmp, []byte(record), 0o644); err != nil {
		return func() {}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return func() {}
	}
	return func() {
		if b, err := os.ReadFile(path); err == nil && string(b) == record {
			_ = os.Remove(path)
		}
	}
}

func parsePidFile(b []byte) (pid int, start string, ok bool) {
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return 0, "", false
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil || pid <= 0 {
		return 0, "", false
	}
	return pid, f[1], true
}

// procStartTime returns field 22 of /proc/<pid>/stat, the process start
// time in clock ticks since boot.
func procStartTime(pid int) (string, bool) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", false
	}
	// comm (field 2) may hold spaces; fields resume after its last ')'.
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return "", false
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return "", false
	}
	return f[19], true
}

// isInboxWatcher: only a live `spore hooks watch-inbox` that is not the
// reconciler's --observe arm is signalled. Where /proc is absent
// nothing is signalled.
func isInboxWatcher(pid int) bool {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	args := bytes.Split(bytes.TrimRight(b, "\x00"), []byte{0})
	var hooks, watch bool
	for _, a := range args {
		switch string(a) {
		case "hooks":
			hooks = true
		case "watch-inbox":
			watch = hooks
		case "--observe":
			return false
		}
	}
	return watch
}

// handoverGate orders a superseded watcher's exit against its claims.
// Once a batch is claimed the pending record says "printed, awaiting
// ack"; exiting quietly then would let the newer watcher ack a batch
// the agent never saw.
type handoverGate struct {
	mu      sync.Mutex
	claimed bool
}

func (g *handoverGate) claim(fn func() (int, error)) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	n, err := fn()
	if n > 0 {
		g.claimed = true
	}
	return n, err
}

// release reports whether the watcher may exit without waking the
// agent. On true it keeps the lock, so no claim starts afterwards.
func (g *handoverGate) release() bool {
	g.mu.Lock()
	if g.claimed {
		g.mu.Unlock()
		return false
	}
	return true
}

var gate handoverGate

// MayExitOnSupersede is the SIGTERM answer of a superseded watcher. True:
// exit 0 now, nothing was claimed and nothing will be. False: a batch is
// already claimed; let the watcher finish and exit 2 so the agent wakes.
func MayExitOnSupersede() bool { return gate.release() }
