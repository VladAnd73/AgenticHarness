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
	type alert struct {
		key string
		msg string
	}
	var alerts []alert
	for _, pr := range prs {
		if pr.IsDraft {
			continue
		}
		checks, err := FailingChecks(projectRoot, pr.Number)
		if err != nil {
			return rep, noteFailure(st, tell, err)
		}
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
			msg := fmt.Sprintf(
				"pr-watch: PR #%d (%s) has failing check %q\nrun: %s\npr: %s\n\n%s",
				pr.Number, pr.Branch, c.Name, c.Link, pr.URL, runbook)
			alerts = append(alerts, alert{k, msg})
		}
	}
	if dryRun {
		rep.Alerts = len(alerts)
		return rep, nil
	}
	for _, a := range alerts {
		if err := tell("coordinator", a.msg); err != nil {
			// Persist keys already marked before returning so they are not
			// re-delivered next round (at-least-once: prefer duplicate-free
			// over lost, but never drop a successfully delivered alert).
			_ = st.Save()
			return rep, err
		}
		st.MarkKey(a.key)
		// Save after each successful tell so a crash mid-batch does not
		// replay already-delivered alerts.
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
