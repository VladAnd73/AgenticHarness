package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// SlackThread tracks one tracked thread's routing state: who owns it (once
// the coordinator decides) and how far we've read its replies.
type SlackThread struct {
	TaskSlug     string    `json:"task_slug,omitempty"`
	LastActivity time.Time `json:"last_activity"`
	LastReplyTS  string    `json:"last_reply_ts"`
}

// SlackState is the slack-watcher's per-project dedup + routing store: the
// channel cursor (last top-level message ts processed) and, per tracked
// thread, who should receive the next reply. Read-modify-write from two
// actors - the watcher process (new threads, replies, pruning) and the
// "spore watch slack-set-thread" CLI a coordinator runs after deciding
// whether to mint a worker - both go through Save's atomic write, so a rare
// same-tick race is last-writer-wins and self-heals next poll.
type SlackState struct {
	Cursor  string                 `json:"cursor"`
	Threads map[string]SlackThread `json:"threads"`

	path string
}

func slackStatePath(project string) (string, error) {
	return stateFile(project, "slack-watch.json")
}

// LoadSlackState reads slack-watch.json for project. A missing file is a
// clean first run, not an error.
func LoadSlackState(project string) (*SlackState, error) {
	p, err := slackStatePath(project)
	if err != nil {
		return nil, err
	}
	st := &SlackState{Threads: map[string]SlackThread{}, path: p}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w", p, err)
	}
	if st.Threads == nil {
		st.Threads = map[string]SlackThread{}
	}
	st.path = p
	return st, nil
}

func (s *SlackState) Save() error {
	return writeJSONAtomic(s.path, "slack-watch", s)
}

// SetTaskSlug records which task owns threadTS, so future replies route to
// it directly instead of falling back to the coordinator. A no-op turns
// into a fresh entry if threadTS was not already tracked (defensive - the
// normal path always creates the entry when the thread first opens).
func (s *SlackState) SetTaskSlug(threadTS, taskSlug string) {
	th := s.Threads[threadTS]
	th.TaskSlug = taskSlug
	s.Threads[threadTS] = th
}

// Prune drops threads whose LastActivity is more than ttl before now,
// returning how many were dropped. Pruned threads are no longer polled for
// replies - a reply arriving after the prune is not detected, by design.
func (s *SlackState) Prune(now time.Time, ttl time.Duration) int {
	n := 0
	for ts, th := range s.Threads {
		if now.Sub(th.LastActivity) > ttl {
			delete(s.Threads, ts)
			n++
		}
	}
	return n
}
