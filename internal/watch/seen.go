package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/versality/spore/internal/statefile"
)

type State struct {
	Failures int               `json:"failures"`
	Seen     map[string]string `json:"seen"`
	// NotifiedSig / NotifiedAt track the last ill-health alert so an
	// unchanged, still-stuck condition is not re-notified every tick.
	// Absent from older pr-watch.json files; omitempty keeps them optional.
	NotifiedSig string `json:"notified_sig,omitempty"`
	NotifiedAt  string `json:"notified_at,omitempty"`

	path string
}

func Key(pr int, sha, check string) string {
	return fmt.Sprintf("%d:%s:%s", pr, sha, check)
}

func statePath(project string) (string, error) {
	return stateFile(project, "pr-watch.json")
}

// stateFile and writeJSONAtomic stay as unexported delegates so the rest
// of this package keeps calling the names it always has.
func stateFile(project, name string) (string, error) {
	return statefile.Path(project, name)
}

func LoadState(project string) (*State, error) {
	p, err := statePath(project)
	if err != nil {
		return nil, err
	}
	st := &State{Seen: map[string]string{}, path: p}
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
	if st.Seen == nil {
		st.Seen = map[string]string{}
	}
	st.path = p
	return st, nil
}

func (s *State) SeenKey(key string) bool {
	_, ok := s.Seen[key]
	return ok
}

func (s *State) MarkKey(key string) {
	s.Seen[key] = time.Now().UTC().Format(time.RFC3339)
}

func (s *State) Prune(liveKeys map[string]bool) {
	for k := range s.Seen {
		if !liveKeys[k] {
			delete(s.Seen, k)
		}
	}
}

func (s *State) Save() error {
	return writeJSONAtomic(s.path, "pr-watch", s)
}

func writeJSONAtomic(path, tmpPrefix string, v any) error {
	return statefile.WriteJSONAtomic(path, tmpPrefix, v)
}
