package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const watcherPidFile = ".watcher.pid"

// supersedeWatcher makes the caller the only Stop-hook watcher on
// inboxDir. claude-code spawns a new asyncRewake watcher at the end of
// every turn and never reaps the previous one, so without this they pile
// up for the full timeout. The older watcher gets SIGTERM, which the CLI
// turns into a clean exit 0 so it does not wake the agent. The returned
// func drops the pid file if it still names this process.
func supersedeWatcher(inboxDir string) func() {
	path := filepath.Join(inboxDir, watcherPidFile)
	self := os.Getpid()
	if b, err := os.ReadFile(path); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 && pid != self && isInboxWatcher(pid) {
			_ = syscall.Kill(pid, syscall.SIGTERM)
		}
	}
	tmp := path + "." + strconv.Itoa(self)
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(self)+"\n"), 0o644); err != nil {
		return func() {}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return func() {}
	}
	return func() {
		if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == strconv.Itoa(self) {
			_ = os.Remove(path)
		}
	}
}

// isInboxWatcher guards against pid reuse: only a live `spore hooks
// watch-inbox` that is not the reconciler's --observe arm is signalled.
// Where /proc is absent nothing is signalled.
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
