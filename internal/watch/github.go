package watch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// noChecksSentinel is the phrase gh prints to stderr (exit 1, empty stdout)
// when a PR has no checks at all. Verified live against a checkless PR.
const noChecksSentinel = "no checks reported"

// ghError carries the stderr of a failed gh invocation so callers can
// distinguish benign conditions (a PR with no checks) from real errors
// (auth, network) without string-matching the wrapped exec error.
type ghError struct {
	args   []string
	stderr string
	err    error
}

func (e *ghError) Error() string {
	msg := fmt.Sprintf("gh %v: %v", e.args, e.err)
	if s := strings.TrimSpace(e.stderr); s != "" {
		msg += ": " + s
	}
	return msg
}

func (e *ghError) Unwrap() error { return e.err }

// isNoChecks reports whether err is gh's benign "no checks reported" condition,
// meaning the PR has no checks - nothing failing - not a real error.
func isNoChecks(err error) bool {
	var ge *ghError
	return errors.As(err, &ge) && strings.Contains(ge.stderr, noChecksSentinel)
}

type PR struct {
	Number  int    `json:"number"`
	Branch  string `json:"headRefName"`
	HeadSHA string `json:"headRefOid"`
	IsDraft bool   `json:"isDraft"`
	URL     string `json:"url"`
}

type CheckRun struct {
	Name   string `json:"name"`
	Bucket string `json:"bucket"`
	Link   string `json:"link"`
}

func ghJSON(projectRoot string, out any, args ...string) error {
	bin := os.Getenv("SPORE_GH_BINARY")
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = projectRoot
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	b, err := cmd.Output()
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit || len(b) == 0 {
			return &ghError{args: args, stderr: errBuf.String(), err: err}
		}
	}
	if jerr := json.Unmarshal(b, out); jerr != nil {
		if err != nil {
			return &ghError{args: args, stderr: errBuf.String(), err: err}
		}
		return fmt.Errorf("gh %v: bad json: %w", args, jerr)
	}
	return nil
}

func OpenPRs(projectRoot string) ([]PR, error) {
	var prs []PR
	err := ghJSON(projectRoot, &prs, "pr", "list", "--state", "open",
		"--json", "number,headRefName,headRefOid,isDraft,url")
	return prs, err
}

func FailingChecks(projectRoot string, pr int) ([]CheckRun, error) {
	var all []CheckRun
	err := ghJSON(projectRoot, &all, "pr", "checks", strconv.Itoa(pr),
		"--json", "name,bucket,link")
	if err != nil {
		if isNoChecks(err) {
			return nil, nil
		}
		return nil, err
	}
	var failing []CheckRun
	for _, c := range all {
		if c.Bucket == "fail" {
			failing = append(failing, c)
		}
	}
	return failing, nil
}
