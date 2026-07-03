package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type State struct {
	Failures int               `json:"failures"`
	Seen     map[string]string `json:"seen"`

	path string
}

func Key(pr int, sha, check string) string {
	return fmt.Sprintf("%d:%s:%s", pr, sha, check)
}

func statePath(project string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("neither XDG_STATE_HOME nor HOME set")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "spore", project, "pr-watch.json"), nil
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
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// Write to a temp file in the same directory, then rename atomically so a
	// crash mid-write cannot corrupt the live state file.
	tmp, err := os.CreateTemp(dir, ".pr-watch-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
