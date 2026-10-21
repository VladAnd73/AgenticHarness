package spore

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var staleRepoURL = regexp.MustCompile(`(?:marketertechnologies|versality)/spore`)

// moduleRef is the Go module path, not a URL: imports, `go build`
// targets and the buildCommit ldflag keep it after the repo moved.
var moduleRef = regexp.MustCompile(`"github\.com/versality/spore(?:/[^"\s]*)?"|=github\.com/versality/spore\.buildCommit`)

// historicalRepoFiles record past events or other repos and are left
// as written.
var historicalRepoFiles = map[string]bool{
	"CHANGELOG.md":                    true,
	"docs/todo/host-shims-via-nix.md": true,
}

func TestNoStaleRepoURLs(t *testing.T) {
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", ".wt", "tasks", "vendor", "result", "coverage":
				return filepath.SkipDir
			}
			return nil
		}
		if historicalRepoFiles[p] || p == "canonical_repo_test.go" || p == "go.mod" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			stripped := moduleRef.ReplaceAllString(line, "")
			if staleRepoURL.MatchString(stripped) {
				t.Errorf("%s:%d: stale repo URL, use VladAnd73/AgenticHarness: %s", p, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
