package hooks

import (
	"encoding/json"
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
const (
	pendingSubdir  = ".pending"
	ackRecordName  = ".pending-ack"
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
	return filepath.Join(inboxDir, ackRecordName)
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
	tmp, err := os.CreateTemp(inboxDir, ".pending-ack-*")
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
	if _, err := movePending(inboxDir, filepath.Join(inboxDir, "read")); err != nil {
		return err
	}
	return removeAckRecord(inboxDir)
}

// RequeuePending puts a drained-but-unacknowledged batch back on the
// inbox top level so the next drain re-delivers it, and drops the
// record. Returns how many envelopes were requeued. Idempotent: with
// nothing pending it returns 0.
func RequeuePending(inboxDir string) (int, error) {
	n, err := movePending(inboxDir, inboxDir)
	if err != nil {
		return n, err
	}
	return n, removeAckRecord(inboxDir)
}

// movePending relocates every staged *.json from pendingSubdir into
// dst, skipping any name that already exists there.
func movePending(inboxDir, dst string) (int, error) {
	pending := filepath.Join(inboxDir, pendingSubdir)
	entries, err := os.ReadDir(pending)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return 0, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	moved := 0
	for _, n := range names {
		if err := os.Rename(filepath.Join(pending, n), filepath.Join(dst, n)); err != nil {
			continue
		}
		moved++
	}
	return moved, nil
}

func removeAckRecord(inboxDir string) error {
	if err := os.Remove(ackRecordPath(inboxDir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
