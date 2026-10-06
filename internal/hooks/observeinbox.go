package hooks

import (
	"os"
	"path/filepath"
	"strings"
)

// ObserveInbox watches inboxDirs for a NEW envelope without claiming
// anything, then calls onNew once and returns.
//
// It exists because of how the Stop-hook watchers die. One arrival
// wakes every blocked watcher, exactly one wins the rename, and all the
// losers exit having claimed nothing. After that moment no watcher is
// left, so if the winner's wake request is dropped the inbox goes
// unwatched until the agent happens to take a turn for some other
// reason. On 2026-10-06 that left two further envelopes sitting
// unnoticed for fourteen minutes.
//
// An observer re-arms that gap from outside the agent. It never
// renames and never records, so it cannot steal a delivery from the
// agent's own watcher. Envelopes already present when it starts are
// ignored: re-arming must not act on the batch the watchdog just
// requeued.
func ObserveInbox(inboxDirs []string, onNew func() error) error {
	return observeInbox(inboxDirs, onNew, defaultWatchOpts())
}

func observeInbox(inboxDirs []string, onNew func() error, opts watchOpts) error {
	for _, dir := range inboxDirs {
		if err := ensureInbox(dir); err != nil {
			return err
		}
	}
	seen := inboxSnapshot(inboxDirs)

	w, err := opts.initWatcher(inboxDirs)
	if err != nil || w == nil {
		opts.sleep(opts.timeout)
		if newArrival(inboxDirs, seen) {
			return onNew()
		}
		return nil
	}
	defer w.Close()

	woke, _ := w.Wait()
	if !woke {
		return nil
	}
	opts.sleep(opts.settle)
	if newArrival(inboxDirs, seen) {
		return onNew()
	}
	return nil
}

// inboxSnapshot records the envelopes already at the top level of each
// dir, keyed by full path.
func inboxSnapshot(inboxDirs []string) map[string]bool {
	seen := map[string]bool{}
	for _, dir := range inboxDirs {
		for _, n := range topLevelEnvelopes(dir) {
			seen[filepath.Join(dir, n)] = true
		}
	}
	return seen
}

func newArrival(inboxDirs []string, seen map[string]bool) bool {
	for _, dir := range inboxDirs {
		for _, n := range topLevelEnvelopes(dir) {
			if !seen[filepath.Join(dir, n)] {
				return true
			}
		}
	}
	return false
}

func topLevelEnvelopes(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}
