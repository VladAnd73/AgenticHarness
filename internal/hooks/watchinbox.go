package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrWake signals that the inbox was drained and the agent should wake
// (exit code 2 in claude-code's Stop hook protocol).
var ErrWake = errors.New("inbox drained")

// WatchInbox is the Stop-hook implementation for inbox-based wakeup.
// It watches $WT_STATE/<slug>/inbox/ for arriving .json files, drains
// them into stdout formatted as "[ts] source: body", moves claimed
// files to inbox/read/, and returns ErrWake when files were drained.
// Returns nil on timeout (no files), or a real error on failure.
func WatchInbox(slug string) error {
	return WatchInboxAtDirs([]string{workerInbox(slug)})
}

// WatchInboxAt is the env-driven Stop-hook entry point. It targets
// inboxDir directly instead of computing it from a slug, so callers
// like `spore hooks watch-inbox` (no positional) can pass
// $SPORE_TASK_INBOX, which the consumer harness sets per spawn (rower:
// $WT_STATE/<slug>/inbox; coordinator: $SKYHELM_STATE_DIR/<project>/inbox).
func WatchInboxAt(inboxDir string) error {
	return WatchInboxAtDirs([]string{inboxDir})
}

// WatchInboxAtDirs is the multi-dir env-driven Stop-hook entry point. A
// wake fires when a .json arrives in ANY of inboxDirs; on wake every dir
// is drained (each keeps its own read/ subdir). Coordinators pass two
// dirs: the poke channel ($SPORE_TASK_INBOX) and their message inbox
// (task.InboxDir("coordinator")), so both worker/CLI tells and
// interactive-session pokes wake an idle coordinator.
func WatchInboxAtDirs(inboxDirs []string) error {
	return WatchInboxForSession("", inboxDirs)
}

// WatchInboxForSession is WatchInboxAtDirs for the Stop hook of Claude
// session `session`; only an older watcher of the same session is
// superseded.
func WatchInboxForSession(session string, inboxDirs []string) error {
	if len(inboxDirs) > 0 {
		if err := ensureInbox(inboxDirs[0]); err != nil {
			return err
		}
		defer supersedeWatcher(inboxDirs[0], session)()
	}
	return watchInboxAtDirs(inboxDirs, os.Stdout, os.Stderr, defaultWatchOpts())
}

type watchOpts struct {
	timeout     time.Duration
	settle      time.Duration
	confirm     time.Duration
	initWatcher func(dirs []string) (inboxWaiter, error)
	sleep       func(time.Duration)
}

// inboxWaiter abstracts inotify so tests can drive it without the
// kernel. Wait returns true on event, false on timeout.
type inboxWaiter interface {
	Wait() (woke bool, err error)
	Close() error
}

func defaultWatchOpts() watchOpts {
	return watchOpts{
		timeout: envDurationSeconds("WATCH_TIMEOUT", 604800),
		settle:  envDurationSeconds("WATCH_SETTLE", 1),
		// Long enough that a live Stop-hook watcher has certainly
		// claimed the arrival, short enough that a real lost wakeup
		// is reported while the operator is still in the room.
		confirm:     envDurationSeconds("WATCH_CONFIRM", 10),
		initWatcher: initPlatformWatcher,
		sleep:       time.Sleep,
	}
}

func watchInbox(slug string, stdout, stderr io.Writer, opts watchOpts) error {
	return watchInboxAt(workerInbox(slug), stdout, stderr, opts)
}

func watchInboxAt(inboxDir string, stdout, stderr io.Writer, opts watchOpts) error {
	return watchInboxAtDirs([]string{inboxDir}, stdout, stderr, opts)
}

func watchInboxAtDirs(inboxDirs []string, stdout, stderr io.Writer, opts watchOpts) error {
	for _, dir := range inboxDirs {
		if err := ensureInbox(dir); err != nil {
			return err
		}
		// This process starting IS the acknowledgement. The Stop hook
		// only runs when a turn ends, so a fresh watcher proves the
		// agent woke for whatever the previous one printed.
		if err := AckDelivered(dir); err != nil {
			return err
		}
	}

	if n, err := drainAll(inboxDirs, stdout); err != nil {
		return err
	} else if n > 0 {
		return ErrWake
	}

	w, werr := opts.initWatcher(inboxDirs)
	if werr != nil || w == nil {
		fmt.Fprintf(stderr, "watch-inbox: inotifywait unavailable; sleeping %ds\n",
			int(opts.timeout.Seconds()))
		opts.sleep(opts.timeout)
		if n, err := drainAll(inboxDirs, stdout); err != nil {
			return err
		} else if n > 0 {
			return ErrWake
		}
		return nil
	}
	defer w.Close()

	// Not every event is a tell: another session's watcher writes its
	// pid file here too. Keep waiting until something is claimed.
	deadline := time.Now().Add(opts.timeout)
	for {
		woke, _ := w.Wait()
		if woke {
			opts.sleep(opts.settle)
		}
		if n, err := drainAll(inboxDirs, stdout); err != nil {
			return err
		} else if n > 0 {
			return ErrWake
		}
		if !woke || !time.Now().Before(deadline) {
			return nil
		}
	}
}

// drainAll drains each dir in turn (each keeps its own read/ subdir) and
// returns the total number of files claimed across all of them.
func drainAll(inboxDirs []string, stdout io.Writer) (int, error) {
	return gate.claim(func() (int, error) {
		total := 0
		for _, dir := range inboxDirs {
			n, err := drainInbox(dir, stdout)
			if err != nil {
				return total, err
			}
			total += n
		}
		return total, nil
	})
}

// drainInbox lists *.json at the top level of inboxDir, atomically
// moves each into the pending staging dir, records the batch, and
// prints "[ts] source: body" for every file this caller claimed.
// Returns the claim count.
func drainInbox(inboxDir string, stdout io.Writer) (int, error) {
	msgs, err := ClaimPending(inboxDir, time.Now())
	if err != nil {
		return 0, err
	}
	for _, m := range msgs {
		fmt.Fprintf(stdout, "[%s] %s: %s\n", m.TS, m.Source, m.Body)
	}
	return len(msgs), nil
}

// ClaimPending moves every *.json at the inbox top level into the
// pending staging dir and writes the un-acknowledged record for the
// batch. The rename is what makes a claim exclusive: when several
// watchers wake on one arrival they all run this, and only the one
// that wins the rename gets a non-empty result. Returns the claimed
// messages, empty when another watcher won.
//
// Claiming is deliberately separate from printing. The record lands
// before anything is written to stdout, so a batch stays recoverable
// even if the process dies between the two.
func ClaimPending(inboxDir string, now time.Time) ([]PendingMessage, error) {
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return nil, nil
	}
	sort.Strings(names)

	pendingDir := filepath.Join(inboxDir, pendingSubdir)
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		return nil, err
	}
	var claimed []string
	for _, n := range names {
		if err := os.Rename(filepath.Join(inboxDir, n), filepath.Join(pendingDir, n)); err == nil {
			claimed = append(claimed, n)
		}
	}
	if len(claimed) == 0 {
		return nil, nil
	}
	msgs := make([]PendingMessage, 0, len(claimed))
	for _, n := range claimed {
		ts, source, body := readTellFile(filepath.Join(pendingDir, n))
		msgs = append(msgs, PendingMessage{Name: n, TS: ts, Source: source, Body: body})
	}
	if err := recordPendingAck(inboxDir, msgs, now); err != nil {
		return nil, err
	}
	return msgs, nil
}

func readTellFile(path string) (ts, source, body string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var ev struct {
		Ts     string `json:"ts"`
		Source string `json:"source"`
		Body   string `json:"body"`
		Slug   string `json:"slug"`
		Msg    string `json:"msg"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &ev); err != nil {
		return
	}
	if ev.Source == "" && ev.Msg != "" {
		ev.Source = "tell"
	}
	if ev.Body == "" {
		ev.Body = ev.Msg
	}
	return ev.Ts, ev.Source, ev.Body
}

func workerInbox(slug string) string {
	return filepath.Join(wtStateDir(), slug, "inbox")
}

func wtStateDir() string {
	if v := os.Getenv("WT_STATE"); v != "" {
		return v
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "wt")
	}
	return ""
}

func ensureInbox(inbox string) error {
	for _, sub := range []string{"", ".tmp", "read", pendingSubdir} {
		if err := os.MkdirAll(filepath.Join(inbox, sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func envDurationSeconds(key string, defaultSec int) time.Duration {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscan(v, &n); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return time.Duration(defaultSec) * time.Second
}
