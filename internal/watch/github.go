package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

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
	b, err := cmd.Output()
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit || len(b) == 0 {
			return fmt.Errorf("gh %v: %w", args, err)
		}
	}
	if jerr := json.Unmarshal(b, out); jerr != nil {
		if err != nil {
			return fmt.Errorf("gh %v: %w", args, err)
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
