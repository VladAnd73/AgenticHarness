package watch

import (
	"encoding/json"
	"fmt"
	"os"
)

// ReleaseState is the release-watcher's dedup store: the last-notified release
// tag per watched repo, keyed by owner/repo. It lives in its own file
// (release-watch.json), separate from the pr-watcher's pr-watch.json, so the
// two watchers never load-modify-rename the same file and clobber each other's
// just-written state during overlapping runs.
type ReleaseState struct {
	Tags map[string]string `json:"tags"`
	path string
}

// LoadReleaseState reads release-watch.json for project. A missing file is a
// clean first run (empty state), not an error.
func LoadReleaseState(project string) (*ReleaseState, error) {
	p, err := stateFile(project, "release-watch.json")
	if err != nil {
		return nil, err
	}
	st := &ReleaseState{Tags: map[string]string{}, path: p}
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
	if st.Tags == nil {
		st.Tags = map[string]string{}
	}
	st.path = p
	return st, nil
}

// Tag returns the last-notified release tag for repo (owner/repo) and whether
// it has ever been observed.
func (s *ReleaseState) Tag(repo string) (string, bool) {
	tag, ok := s.Tags[repo]
	return tag, ok
}

// Mark records tag as the last-notified release for repo.
func (s *ReleaseState) Mark(repo, tag string) {
	s.Tags[repo] = tag
}

func (s *ReleaseState) Save() error {
	return writeJSONAtomic(s.path, "release-watch", s)
}
