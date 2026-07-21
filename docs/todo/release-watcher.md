**Status**: in progress 2026-07-21. Worker task:
`release-watcher`. Design brainstormed with the operator
2026-07-21; this doc is the implementation contract.

# release-watcher: poke coordinators when a watched repo cuts a release

## Goal

A new headless watch kind, `spore watch releases`, that polls GitHub
once per hour (08:00-18:00 UTC) for new releases on a configured set of
repos. When a repo publishes a release newer than the last one the
watcher notified about, it pokes a configured list of project
coordinators and tells each one to spin up a worker that syncs the
Notion Product Knowledge KB for that release.

It reuses the pr-watcher's communication method exactly: a message
envelope via the per-project coordinator message inbox, plus a
contentless poke into that coordinator's wake channel
(`hooks.NotifyCoordinator`). See `internal/watch/` and
`cmd/spore/watch_cmd.go` (`tellWithPoke`) for the pattern to mirror.

## Fire rule (operator decision)

Per-repo poke. Watch each configured repo independently; fire a separate
poke the moment a repo's latest published release tag differs from the
tag last notified for that repo. Two repos releasing = two pokes. There
is deliberately NO "wait until both backend and frontend released"
coupling.

## Config

Lives in the host project's `watch.toml`
(`~/.config/spore/<project>/watch.toml`), new `[releases]` table,
separate from the existing top-level pr-watch keys:

```toml
[releases]
enabled = true
repos = ["marketertechnologies/marketer", "marketertechnologies/marketer-frontend"]
coordinators = ["marketer-frontend"]
```

- `repos`: GitHub `owner/repo` slugs to watch.
- `coordinators`: spore PROJECT names whose coordinator is poked +
  told on any new release. Flat routing: every new release pokes every
  listed coordinator.
- `enabled`: master off-switch; absent/false means the run is a no-op.

Extend `internal/watch/config.go` (`LoadConfig`) to parse the
`[releases]` section. The current parser is flat key=value with no
table support, so add minimal section handling (or a dedicated
`LoadReleasesConfig`). Keep the existing pr-watch config behavior
unchanged.

## Release detection

Per configured repo, per run, via `gh` (already available under
`spore-with-secrets`):

```
gh release view --repo <owner>/<repo> --json tagName,url,publishedAt
```

`gh release view` with no tag argument returns GitHub's "latest"
release, which already excludes drafts and prereleases, so no separate
prerelease filtering is needed. Treat a repo with zero releases as
benign "nothing to report" (no error, no poke): gh exits non-zero with
a `release not found` stderr. Follow the
`ghError` / `isNoChecks` sentinel pattern already in
`internal/watch/github.go` for distinguishing benign from real errors.
Read the gh binary from `SPORE_GH_BINARY` (test seam) as `ghJSON` does.

## Dedup state

Store the last-notified release tag PER repo, in the host project's
watch state (same location/pattern as the PR seen-set; see
`internal/watch/seen.go` + `LoadState`/`SaveState`). Keyed by
`owner/repo`.

- First observation of a repo (no stored tag): seed the baseline
  silently, do NOT poke. This prevents a poke storm on first install /
  after adding a repo.
- Subsequent run where latest tag != stored tag: fire, then store the
  new tag.
- Unchanged tag: silent.
- A repo whose release query errors (real error) is skipped for this
  run WITHOUT advancing its stored tag, so a transient gh/network
  failure does not swallow a release; it fires next successful run.

## Poke + message delivery

For each fired repo, for each project in `coordinators`:

1. Message envelope to that coordinator's PROJECT message inbox
   (`~/.local/state/spore/<project>/coordinator/inbox`).
2. Contentless poke to that coordinator's wake channel
   (`hooks.NotifyCoordinator(<project>)`).

CAVEAT the worker must handle: `hooks.NotifyCoordinator(project)` already
takes the target project name, so the poke is cross-project-safe. But
`task.Tell(slug, msg)` resolves the message inbox from the CURRENT
project only (`InboxDir` -> current `StateDir`). For the host project
itself (marketer-frontend == the only current coordinator) that
resolves correctly, but to honor the configurable list for OTHER
projects, deliver the message to each coordinator's project inbox
explicitly (a project-name-aware inbox path, mirroring
`NotifyCoordinator`). Implement the general form; do not hardcode
marketer-frontend.

Poke is best-effort after the envelope is written (warn on stderr, do
not re-fire next cycle), matching `tellWithPoke`.

### Message body

Names the repo, tag, and URL, and instructs the coordinator to use a
WORKER (operator requirement) to do the KB sync. Example:

> New release on `marketertechnologies/marketer` (tag `v2.5.0`):
> <release-url>. Start a worker to sync the Notion Product Knowledge KB
> for this release.

The coordinator maps this to its own KB-sync skill
(`syncing-marketer-product-knowledge-on-release`); the watcher stays
generic and names no skill.

## CLI

`spore watch releases [--project-root DIR] [--dry-run]`, mirroring
`runWatch`'s `prs` subcommand in `cmd/spore/watch_cmd.go`. `--dry-run`
reports what it would fire without telling, poking, or saving state.
Print a one-line summary (`release-watch <project>: N poke(s), M
repo(s) unchanged`).

## Scheduling / deployment

Systemd user timer + oneshot service under the host project, mirroring
`spore-pr-watch-marketer-frontend.{service,timer}` but with a calendar
schedule instead of an interval:

```
# timer
OnCalendar=*-*-* 08..18:00:00
Persistent=false
```

That fires hourly at 08:00 through 18:00 inclusive, UTC (host zone) =
11 fires/day. Service is `Type=oneshot`, `WorkingDirectory=/home/spore/
marketer-frontend`, `ExecStart=spore-with-secrets <spore-binary> watch
releases`. Deployment (writing the unit files, enabling the timer,
creating watch.toml) is a host-config step the coordinator does after
the PR merges; it is NOT part of the worker's code task.

## Testing (Worker TDD - write these first, red then green)

Inject the clock (`now`), the gh binary (`SPORE_GH_BINARY` fake), and
the tell/poke funcs, as the existing watch tests do. Acceptance
scenarios:

1. End-to-end: given a watched repo whose latest tag is newer than the
   stored tag, when the watcher runs, then it delivers a message
   envelope naming the repo/tag/url AND a poke to each configured
   coordinator, and stores the new tag.
2. First-run seeding: given a repo with no stored tag, when the watcher
   runs, then it stores the baseline and does NOT poke.
3. Unchanged: given a repo whose latest tag equals the stored tag, when
   the watcher runs, then no message, no poke, state unchanged.
4. Per-repo isolation: given two repos, one new and one unchanged, when
   the watcher runs, then exactly one poke fires (for the new one).
5. No releases / benign: given a repo with zero releases, when the
   watcher runs, then no error, no poke.
6. Real error is safe: given a repo whose query errors (non-benign),
   when the watcher runs, then that repo is skipped and its stored tag
   is NOT advanced (fires next good run).
7. Disabled: given `[releases] enabled = false` (or absent), the run is
   a no-op.
8. Poke targets: the poke lands in the configured coordinator's wake
   channel and the message in that coordinator's project message inbox.
9. dry-run: reports intent, writes no state, sends nothing.

At least scenario 1 exercises the full flow (config -> gh -> dedup ->
message + poke -> state) end to end.

## Out of scope

- The coordinator-side KB-sync skill and worker (operator owns; skill
  `syncing-marketer-product-knowledge-on-release` already exists).
- Deploying the timer/service/watch.toml on the host (coordinator
  post-merge step).
