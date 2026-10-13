package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Delivery of an inbox envelope is two-phase. A drain claims the
// envelope out of the inbox top level (the rename is what makes the
// claim exclusive between concurrent watchers) and stages it in
// pendingSubdir together with a record of what it printed. The
// envelope only reaches read/ once the delivery is acknowledged.
//
// The acknowledgement is the next start of the Stop hook: that hook
// runs at the end of a turn, so a fresh watch-inbox process is proof
// that the agent woke and finished one. Until that proof arrives the
// record sits on disk, naming an envelope that was printed to a pipe
// nobody may have been reading.
// The record lives inside pendingSubdir rather than at the inbox top
// level because the top level is what inotify watches. A record written
// beside the envelopes would fire a spurious wake at every other
// watcher on that inbox on every single claim, on a path whose entire
// problem is spurious wakeups.
//
// Staging constrains the deploy order. A spore older than this release
// lists only top-level *.json and skips directories, so it reads an
// inbox holding a staged envelope as empty and never promotes,
// requeues or re-delivers it. Upgrade every binary on a host together
// and do not roll one back while an envelope is staged. See
// docs/inbox-delivery.md for the hand recovery.
const (
	pendingSubdir  = ".pending"
	ackRecordName  = ".ack"
	ackRecordPerms = 0o644
)

// PendingMessage is one claimed envelope inside a PendingAck: the file
// that was staged plus the text that was printed for it.
type PendingMessage struct {
	Name   string `json:"name"`
	TS     string `json:"ts"`
	Source string `json:"source"`
	Body   string `json:"body"`
}

// PendingAck is the durable record of a drained-but-unacknowledged
// batch. Its presence means an envelope left the inbox and was printed,
// with no evidence yet that the agent ever saw it.
type PendingAck struct {
	ClaimedAt time.Time        `json:"claimed_at"`
	PID       int              `json:"pid"`
	InboxDir  string           `json:"inbox_dir"`
	Messages  []PendingMessage `json:"messages"`
}

func ackRecordPath(inboxDir string) string {
	return filepath.Join(inboxDir, pendingSubdir, ackRecordName)
}

// ReadPendingAck loads the un-acknowledged record for inboxDir. The
// bool is false when no delivery is outstanding. A record that fails to
// parse is reported as an error rather than silently treated as absent,
// so a corrupt record does not look like a clean inbox.
func ReadPendingAck(inboxDir string) (PendingAck, bool, error) {
	b, err := os.ReadFile(ackRecordPath(inboxDir))
	if err != nil {
		if os.IsNotExist(err) {
			return PendingAck{}, false, nil
		}
		return PendingAck{}, false, err
	}
	var rec PendingAck
	if err := json.Unmarshal(b, &rec); err != nil {
		return PendingAck{}, false, err
	}
	return rec, true, nil
}

// recordPendingAck writes (or extends) the un-acknowledged record for
// inboxDir. Messages from an existing record are kept: a second claim
// with no acknowledgement in between must not drop the first batch.
func recordPendingAck(inboxDir string, msgs []PendingMessage, now time.Time) error {
	rec, _, err := ReadPendingAck(inboxDir)
	if err != nil {
		rec = PendingAck{}
	}
	rec.ClaimedAt = now.UTC()
	rec.PID = os.Getpid()
	rec.InboxDir = inboxDir
	rec.Messages = append(rec.Messages, msgs...)

	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	pending := filepath.Join(inboxDir, pendingSubdir)
	if err := os.MkdirAll(pending, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(pending, ".ack-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), ackRecordPerms); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), ackRecordPath(inboxDir))
}

// AckDelivered acknowledges whatever delivery is outstanding for
// inboxDir: staged envelopes are promoted into read/ and the record is
// dropped. Idempotent, and a no-op when nothing is pending.
func AckDelivered(inboxDir string) error {
	_, remaining, err := movePending(inboxDir, filepath.Join(inboxDir, "read"))
	if err != nil {
		return err
	}
	if remaining > 0 {
		return nil
	}
	return removeAckRecord(inboxDir)
}

// PendingAckPath is where the un-acknowledged record for inboxDir
// lives. Exported so operator tooling (and the end-to-end test) can
// name the file without re-deriving the layout.
func PendingAckPath(inboxDir string) string { return ackRecordPath(inboxDir) }

// RequeuePending puts a drained-but-unacknowledged batch back on the
// inbox top level so the next drain re-delivers it, and drops the
// record. Returns the names it put back, under which they now sit at
// the top level. Idempotent: with nothing pending it returns nothing.
//
// The record survives when an envelope could not be moved. The record
// is the only thing that tells the watchdog a staged envelope exists,
// so dropping it while the envelope is still in pendingSubdir would
// strand that envelope with nothing left to find it.
func RequeuePending(inboxDir string) ([]string, error) {
	moved, remaining, err := movePending(inboxDir, inboxDir)
	if err != nil {
		return moved, err
	}
	if remaining > 0 {
		return moved, nil
	}
	return moved, removeAckRecord(inboxDir)
}

// movePending relocates every staged *.json from pendingSubdir into
// dst and reports the names they landed under, plus how many stayed
// behind. Nothing at dst is ever replaced: a name already taken there
// gets a suffixed variant, because both files are envelopes and an
// inbox may not drop one silently.
func movePending(inboxDir, dst string) (moved []string, remaining int, err error) {
	pending := filepath.Join(inboxDir, pendingSubdir)
	entries, err := os.ReadDir(pending)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, 0, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		landed, err := moveWithoutClobber(filepath.Join(pending, n), dst, n)
		if err != nil {
			remaining++
			continue
		}
		moved = append(moved, landed)
	}
	return moved, remaining, nil
}

// moveWithoutClobber moves src into dir under name, or under a
// suffixed variant when that name is taken. It links before unlinking
// so the destination test and the claim are one atomic step: link
// fails with EEXIST rather than replacing, which os.Rename would do
// silently.
func moveWithoutClobber(src, dir, name string) (string, error) {
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s.dup%d%s",
				strings.TrimSuffix(name, ".json"), i, ".json")
		}
		dst := filepath.Join(dir, candidate)
		err := os.Link(src, dst)
		if err == nil {
			if err := os.Remove(src); err != nil {
				return "", err
			}
			return candidate, nil
		}
		if errors.Is(err, fs.ErrExist) {
			if i > maxDupSuffix {
				return "", err
			}
			continue
		}
		// Hard links are not available everywhere (and never across
		// filesystems). Fall back to a checked rename, which is
		// exposed to a vanishingly narrow race but still refuses to
		// overwrite a name it can see.
		if _, serr := os.Lstat(dst); serr == nil {
			if i > maxDupSuffix {
				return "", err
			}
			continue
		}
		if rerr := os.Rename(src, dst); rerr != nil {
			return "", rerr
		}
		return candidate, nil
	}
}

// maxDupSuffix bounds the collision search so a pathological
// destination cannot spin the loop forever.
const maxDupSuffix = 100

func removeAckRecord(inboxDir string) error {
	if err := os.Remove(ackRecordPath(inboxDir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
