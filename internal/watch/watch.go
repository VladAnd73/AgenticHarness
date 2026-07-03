package watch

import (
	"fmt"
	"strings"
)

type Report struct {
	Alerts  int
	Skipped int
}

const runbook = `Runbook (per approved spec docs/todo/pr-e2e-watcher.md in the spore repo):
1. Mint a worker task: fetch origin, check out the PR branch, pull, run the FAILED specs locally (use the project's running-*-e2e / investigating-*-e2e-failures skills).
2. Worker tells you the local result.
3. Local PASS -> retry CI once: spore-with-secrets gh run rerun <run-id from the link> --failed. Watch the rerun; green = done silently, red = escalate to operator.
4. Local FAIL -> escalate to operator in your terminal with the worker's findings. Do not retry.
Max ONE CI retry per commit. Escalate instead of looping.`

// prAlert holds a rollup tell for one PR.
type prAlert struct {
	keys []string
	msg  string
}

func Run(projectRoot, project string, dryRun bool, tell func(slug, msg string) error) (Report, error) {
	var rep Report
	cfg, err := LoadConfig(project)
	if err != nil || !cfg.Enabled {
		return rep, err
	}
	st, err := LoadState(project)
	if err != nil {
		return rep, err
	}
	prs, err := OpenPRs(projectRoot)
	if err != nil {
		return rep, noteFailure(st, tell, err)
	}
	live := map[string]bool{}
	var alerts []prAlert
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		checks, err := FailingChecks(projectRoot, pr.Number)
		if err != nil {
			return rep, noteFailure(st, tell, err)
		}
		var newKeys []string
		var lines []string
		for _, c := range checks {
			if !nameMatches(c.Name, cfg.Checks) {
				continue
			}
			k := Key(pr.Number, pr.HeadSHA, c.Name)
			live[k] = true
			if st.SeenKey(k) {
				rep.Skipped++
				continue
			}
			newKeys = append(newKeys, k)
			lines = append(lines, fmt.Sprintf("  %s\n  run: %s", c.Name, c.Link))
		}
		if len(newKeys) == 0 {
			continue
		}
		msg := fmt.Sprintf(
			"pr-watch: PR #%d (%s) has %d failing e2e check(s)\n%s\npr: %s\n\n%s",
			pr.Number, pr.Branch, len(newKeys),
			strings.Join(lines, "\n"),
			pr.URL, runbook)
		alerts = append(alerts, prAlert{newKeys, msg})
	}
	if dryRun {
		rep.Alerts = len(alerts)
		return rep, nil
	}
	for _, a := range alerts {
		if err := tell("coordinator", a.msg); err != nil {
			_ = st.Save()
			return rep, err
		}
		for _, k := range a.keys {
			st.MarkKey(k)
		}
		if err := st.Save(); err != nil {
			return rep, err
		}
		rep.Alerts++
	}
	st.Prune(live)
	st.Failures = 0
	return rep, st.Save()
}

func nameMatches(name string, patterns []string) bool {
	l := strings.ToLower(name)
	for _, p := range patterns {
		if strings.Contains(l, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func noteFailure(st *State, tell func(string, string) error, cause error) error {
	st.Failures++
	// Save the incremented counter before telling: if Save fails the tell is
	// suppressed (silent gap) rather than firing again next round (duplicate).
	if err := st.Save(); err != nil {
		return err
	}
	if st.Failures == 3 {
		_ = tell("coordinator",
			fmt.Sprintf("pr-watch: unhealthy, 3 consecutive polling failures. Last error: %v", cause))
	}
	return cause
}
