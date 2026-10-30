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

// watcherDir holds the pid files. It sits below the inbox because
// inotify watches the inbox top level: a pid file written there would
// wake every other watcher on the inbox.
const watcherDir = ".watchers"

// WatcherKey names the agent session a watcher belongs to, so a newer
// watcher only supersedes one of its own session. Any claude started
// inside an agent's tmux session inherits SPORE_TASK_INBOX, so keying
// on the inbox alone let a scratch session stop the agent's watcher.
// stdinSession is the Stop hook's session_id. Without it the watcher
// falls back to CLAUDE_CODE_SESSION_ID, then to the tmux pane, which
// holds one agent at a time. Empty means no identity: the watcher then
// supersedes nothing rather than risk stopping another session's.
func WatcherKey(stdinSession string) string {
	if sessionIDPattern.MatchString(stdinSession) {
		return stdinSession
	}
	if s := os.Getenv("CLAUDE_CODE_SESSION_ID"); sessionIDPattern.MatchString(s) {
		return s
	}
	if p := "pane-" + strings.TrimPrefix(os.Getenv("TMUX_PANE"), "%"); p != "pane-" && sessionIDPattern.MatchString(p) {
		return p
	}
	return ""
}

var (
	releaseMu  sync.Mutex
	releasePid = func() {}
)

// ReleaseWatcher drops this process's pid file. The SIGTERM path calls
// it before os.Exit, which skips deferred calls.
func ReleaseWatcher() {
	releaseMu.Lock()
	defer releaseMu.Unlock()
	releasePid()
	releasePid = func() {}
}

// supersedeWatcher makes the caller the only Stop-hook watcher for this
// session on inboxDir. claude-code spawns a new asyncRewake watcher at
// the end of every turn and never reaps the previous one, so without
// this they pile up for the full timeout. The older watcher gets
// SIGTERM; see MayExitOnSupersede for how it answers. It also sweeps
// pid files whose watcher is gone. The returned func drops the pid file
// if it still names this process.
func supersedeWatcher(inboxDir, key string) func() {
	sweepDeadWatchers(inboxDir)
	if key == "" {
		return func() {}
	}
	dir := filepath.Join(inboxDir, watcherDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}
	}
	path := filepath.Join(dir, key+".pid")
	unlock := lockWatchers(dir)
	defer unlock()
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
	release := func() {
		defer lockWatchers(dir)()
		if b, err := os.ReadFile(path); err == nil && string(b) == record {
			_ = os.Remove(path)
		}
	}
	releaseMu.Lock()
	releasePid = release
	releaseMu.Unlock()
	return ReleaseWatcher
}

// lockWatchers serialises pid-file writes against releases. Without it
// a superseded watcher's SIGTERM release could read its own record, then
// remove the file its successor renamed in a moment later, leaving the
// successor unfindable by the next watcher.
func lockWatchers(dir string) func() {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return func() {}
	}
	return func() { _ = f.Close() }
}

// sweepDeadWatchers removes pid files whose process is gone, including
// the top-level ones older spore releases wrote. A watcher killed hard
// never removes its own.
func sweepDeadWatchers(inboxDir string) {
	top, _ := filepath.Glob(filepath.Join(inboxDir, ".watcher*.pid"))
	sub, _ := filepath.Glob(filepath.Join(inboxDir, watcherDir, "*.pid"))
	for _, p := range append(top, sub...) {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if pid, start, ok := parsePidFile(b); ok {
			if cur, ok := procStartTime(pid); ok && cur == start {
				continue
			}
		}
		_ = os.Remove(p)
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
