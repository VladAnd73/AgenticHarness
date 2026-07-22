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

// stateFile resolves a per-project state file under the spore state dir.
func stateFile(project, name string) (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home := os.Getenv("HOME")
		if home == "" {
			return "", fmt.Errorf("neither XDG_STATE_HOME nor HOME set")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "spore", project, name), nil
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

// writeJSONAtomic marshals v to path via a same-directory temp file and an
// atomic rename, so a crash mid-write cannot corrupt the live state file.
// tmpPrefix names the temp file for easy identification of a stray write.
func writeJSONAtomic(path, tmpPrefix string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+tmpPrefix+"-*.json")
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
	return os.Rename(tmp.Name(), path)
}
