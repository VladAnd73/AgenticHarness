# PR e2e watcher: design

**Status**: approved by operator 2026-07-03. Local-only: lives on
branch `evolve_Vlad_harness`, this host only, not propagated for now.

## Goal

The operator is a QA engineer who monitors PR checks (Cypress /
Playwright e2e) across repos. Automate the watch-investigate-retry
loop: a recurring check finds new PRs with failing e2e specs and
wakes the project's coordinator, which drives investigation and
either retries CI or escalates to the operator.

## Decisions locked with the operator

- Scope: all repos on this host, gated per-repo by config; a repo
  is watchable when it has a coordinator here.
- Trigger: failing check runs whose name matches a per-repo
  configurable filter (e.g. cypress, playwright, e2e). Other red
  checks (lint, build) are ignored.
- Response flow: worker reproduces the failed specs locally on the
  PR branch first. Pass locally -> retry all failed CI jobs. Fail
  locally -> escalate to operator for guidance.
- Cadence: poll every 15 minutes. Webhooks rejected (host not
  reachable from the internet, no repo-admin token scope); CI-side
  notify steps rejected for now (requires editing consumer repos).
- Escalation channel: terminal only for now. Slack is a parked
  draft task (`wire-slack-escalation-channel-for-coordinator-aler`).
- Delivery: implemented in the spore kernel (approach A), but NOT
  released for now; stays on `evolve_Vlad_harness` on this host.

## Architecture

### Watcher: `spore watch prs`

New kernel subcommand, run per project by a user-level systemd
timer every 15 minutes (same pattern as `spore-fleet-reconcile-
<proj>.timer`). No sudo required for user units.

Per run, for one project:

1. Load config. Missing or `enabled = false` -> exit 0 silently.
2. `gh pr list` (open, non-draft) + check runs per PR head, via
   `spore-with-secrets` so the layered `GH_TOKEN` applies.
3. Filter failing check runs by the configured name patterns
   (case-insensitive substring match).
4. Dedup against seen-state; drop anything already reported for
   the same PR + head commit + check name.
5. For each new failure: record it in seen-state, then wake the
   project's coordinator via the message inbox (`spore task tell
   coordinator ...` semantics) with PR number, branch, check name,
   and the failed run URL.

### Config (host-side, operator-owned)

`~/.config/spore/<project>/watch.toml`:

```toml
enabled = true
# substring match, case-insensitive, against check-run names
checks = ["cypress", "playwright", "e2e"]
```

Host-side rather than in-repo: which repos this host watches is a
host decision, and enabling a repo must not require pushing to it.

### Seen-state (reporting dedup)

`~/.local/state/spore/<project>/pr-watch.json`. Keyed by
`PR number + head SHA + check name`; pure reporting dedup. A new
push (new head SHA) naturally resets the cycle. The retry-once cap
is enforced by the coordinator runbook carried in each alert (the
coordinator triggers the rerun and watches its outcome in-session).
Entries for closed/merged PRs are pruned on each run.

### Coordinator response (runbook)

On a watcher message, the project coordinator:

1. Mints a worker task: check out the PR branch, pull, run the
   failed specs locally (marketer-frontend workers use the
   existing running-/investigating-e2e skills).
2. Worker reports the local result back (`spore task tell`).
3. Local pass -> coordinator retries all failed CI jobs
   (`gh run rerun <run-id> --failed`) and watches the rerun
   outcome in-session. CI green afterwards -> done, no operator
   noise. CI red again -> escalate; never a second retry.
4. Local fail -> escalate: surface PR, failing spec, worker's
   findings, and a recommended next step in the coordinator's
   terminal. Wait for operator guidance.

One worker task per failing PR, run serially: the local e2e stack
is single-occupancy on this host.

## Failure handling

| Situation | Behavior |
| --- | --- |
| PR is a draft | ignored |
| new commit on PR | fresh cycle, may alert again |
| same failure, same commit | silent (already reported) |
| retry already spent | no second retry; escalate |
| PR has no checks | not an error; the PR contributes zero failing checks and the poll continues (gh prints "no checks reported", exit 1, empty stdout) |
| one PR's checks query errors | skip that PR, keep evaluating and alerting the rest (the poll is not aborted). That PR keeps its seen dedup keys, so the same failing check does not re-alert once the query recovers. The failure still advances the ill-health counter: only a fully clean poll (no per-PR errors) resets it, so repeated partial outages still reach the 3-strike alert |
| GitHub/token error (top-level `pr list`, or every open PR's checks query errors) | skip round; after 3 consecutive bad rounds, report watcher ill-health to coordinator |
| coordinator asleep | inbox message waits; handled on next wake |
| many failing PRs | one task per PR, serial execution |
| rerun permission denied | never silently skip: escalate instead |

## Build and rollout

- Step 0, spike: prove `gh run rerun --failed` works with the
  current token against a real marketer-frontend run, and prove a
  watcher-style message wakes the marketer-frontend coordinator.
- Step 1: implement in Go on `evolve_Vlad_harness` (worker agents
  write it; coordinator reviews). Unit tests with canned GitHub
  JSON; `spore lint` + `go test` green via nix develop.
- Step 2: dogfood with marketer-frontend only. The host's nix
  spore binary predates the feature, so the timer's ExecStart
  points at a branch-built binary path until the feature ships in
  a release.
- Step 3: tune filters/noise, then enable other repos that have
  coordinators.
- Explicitly deferred: releasing the feature, nix-module timer
  wiring for other hosts, Slack escalation, webhook/CI-notify
  instant triggers.

## Out of scope

- Auto-fixing test code or commenting on PRs.
- Watching non-e2e checks.
- Any push to any remote; all work stays local to this host.
