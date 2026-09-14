**Status**: design brainstormed with the operator 2026-09-14. Not yet
a worker task. This doc is the implementation contract.

# slack-thread-watcher: turn Slack threads into investigations

## Goal

A new headless watch kind, `spore watch slack`, that polls one Slack
channel once a minute. When someone opens a new thread in that
channel, it wakes a project's coordinator to investigate. Follow-up
replies in a thread go to whichever worker is currently investigating
it, or to the coordinator if no worker is active. The watcher itself
never judges content - it only detects "new thread" / "new reply" /
"thread gone stale" and relays. All interpretation (is this worth
investigating, is this a stop request, what to do with an update)
happens in the coordinator/worker's own agent turn, the same as any
other task.

## Decisions locked with the operator

- **Target project**: single fixed project, not a cross-project
  router. First (only) consumer is marketer-frontend.
- **Trigger filter**: none. Every new top-level thread in the channel
  wakes the coordinator; the coordinator's own judgment decides
  whether it is worth a worker.
- **Bot visibility**: the coordinator/worker posts an ack when it
  starts investigating and a summary when it stops (needs Slack
  `chat:write`, not just read access). This is a coordinator-side
  behavior, not part of the watcher itself - see "Out of scope".
- **Investigation authority**: read-only. Any worker minted from this
  path investigates and reports; it does not edit code or open a PR.
  Enforced by what the coordinator puts in the worker's brief, not by
  new kernel mechanism.
- **Stop authority**: anyone who posts in the thread can say stop; the
  watcher relays it like any other reply and the coordinator/worker
  decides whether the message means "stop."
- **Backlog scope**: no history sweep. On first run the watcher seeds
  its cursor to "now" and only reacts to threads opened after that.
- **Idle expiry**: stop tracking a thread 48 hours after its last
  activity. A reply after that window is not picked up - this is a
  deliberate cutoff to bound API calls and state size, not a bug.

## Architecture

### Watcher: `spore watch slack`

New kernel subcommand, mirroring `runWatchReleases` in
`cmd/spore/watch_cmd.go` and the `internal/watch` package layout
(`config.go`, `seen.go`-style state, `github.go`-style API client).
Run by a user-level systemd timer every 1 minute, same pattern as the
other watchers (`spore-pr-watch-<proj>.timer` etc).

Per run, for one project:

1. Load `[slack]` config for the project. Missing or `enabled = false`
   -> exit 0 silently.
2. Load watcher state (`slack-watch.json`): channel cursor + a map of
   `thread_ts -> {task_slug (may be empty), last_activity}`.
3. **New top-level messages**: `conversations.history` for the
   channel, `oldest = cursor`. Each result whose `thread_ts == ts` (a
   genuine new thread root, not a reply) is a new thread:
   - Relay via `task.TellProject(project, "coordinator", msg)` with
     the message text, author, and permalink
     (`chat.getPermalink`).
   - Record `thread_ts -> {task_slug: "", last_activity: now}` in
     state (empty task_slug: watcher does not know yet whether the
     coordinator will mint a worker).
   - Poke via `hooks.NotifyCoordinator(project)`.
   Advance the cursor to the latest `ts` seen, regardless of type.
4. **New replies on tracked threads**: for every thread in the state
   map whose `last_activity` is within 48h, call
   `conversations.replies(thread_ts)` since the thread's own stored
   last-seen reply ts. For each new reply:
   - Look up `task_slug` for that thread. If a task_slug is set, check
     its status via `task.List(filepath.Join(cwd, "tasks"))` (the same
     in-process call `runWatchReleases` already makes after
     `watchContext` chdirs into the project root - NOT a shelled-out
     `spore task ls`, which is cwd-relative with no project-scoping
     flag and would silently read the wrong directory from any other
     invocation; see the `project_spore_task_cwd` lesson). Status
     `active` -> `task.TellProject(project, task_slug, msg)`.
     Otherwise (no task_slug, or the task is no longer `active`) ->
     `task.TellProject(project, "coordinator", msg)`, noting "no
     active worker for this thread" in the relayed text so the
     coordinator doesn't have to re-derive that.
   - Update `last_activity` for the thread to now.
5. **Prune**: drop any thread from the map whose `last_activity` is
   older than 48h. Pruned threads are no longer polled via
   `conversations.replies` (bounds the per-tick API call count) and a
   reply arriving after the prune is not detected - matches the
   locked decision above.
6. Save state.

The watcher never itself decides whether a message means "stop" or
"this needs a worker." It only tells the coordinator (or the mapped
worker) that something new happened.

### How `task_slug` gets set

The watcher does not mint tasks and does not know which task (if any)
a coordinator started for a given thread. The coordinator is
responsible for writing the mapping back after it decides: when it
starts a worker for a thread, it updates
`slack-watch.json`'s entry for that `thread_ts` with the new
`task_slug`, so the NEXT poll routes replies straight to that worker.
This means `slack-watch.json` is read-modify-write from two actors
(the watcher process and the coordinator agent) - use the same atomic
write pattern as `seen.go`'s `writeJSONAtomic` on both sides, and
accept last-writer-wins on the rare case of a true race (a poll tick
landing in the same second as the coordinator's own write); staleness
here self-heals within one more poll cycle.

### Config

`[slack]` table in the project's `watch.toml`
(`~/.config/spore/<project>/watch.toml`):

```toml
[slack]
enabled = true
channel_id = "C0BTMKY5AE4"
```

- `channel_id`: the Slack channel to watch. One channel per project
  for now - no need to support a list until a second consumer asks.
- `enabled`: master off-switch; absent/false is a no-op.

Bot token: `SLACK_BOT_TOKEN`, in the project's layered secrets file
(`~/.config/spore/<project>/secrets.env`), read via
`spore-with-secrets`. Required scopes on the Slack app: `channels:history`
(or `groups:history` if the channel is private) to read, `chat:write`
if the coordinator/worker side posts acks (see "Out of scope" - the
watcher's own read path only needs history, not write).

### CLI

`spore watch slack [--project-root DIR] [--dry-run]`, mirroring the
`releases` subcommand. `--dry-run` reports what it would relay/prune
without telling, poking, or saving state.

### Scheduling / deployment

Systemd user timer + oneshot service, `OnUnitActiveSec=1min`, mirroring
`spore-pr-watch-<proj>.timer`. `ExecStart=spore-with-secrets <spore
binary> watch slack`. Deployment (unit files, `watch.toml` entry,
creating the Slack app + bot token, inviting the bot to the channel)
is a host-config step after the PR merges - the Slack-app creation is
an operator action (Slack admin), not part of the worker's code task.

## Testing (Worker TDD - write these first, red then green)

Inject the clock (`now`) and the tell/poke funcs, as the existing
watch tests do. The Slack side needs a different seam than
`github.go`'s: there is no Slack CLI to shell out to (the earlier
`--agent`/headless investigation ruled out the MCP connector for
non-interactive use, and the plan is a bot token over the Web API
directly), so define a small `slackClient` interface (something like
`History(oldest string) ([]Message, error)`, `Replies(threadTS,
oldest string) ([]Message, error)`, `Permalink(ts string) (string,
error)`) and inject a fake implementing it in tests, the real HTTP
client in production - do not try to mirror `SPORE_GH_BINARY`.
Acceptance scenarios:

1. End-to-end: given a channel with one new top-level message since
   the cursor, when the watcher runs, then it tells the project
   coordinator with the message text + permalink, pokes the
   coordinator, and records the thread in state with an empty
   `task_slug`.
2. First-run seeding: given no prior state, when the watcher runs for
   the first time, then it seeds the cursor to "now" and relays
   nothing from channel history that predates this run.
3. Reply routed to active worker: given a tracked thread whose
   `task_slug` names a task whose frontmatter status (read via
   `task.List` on the project's `tasks/` dir) is `active`, when a new
   reply lands in that thread, then it is told to that task's slug,
   not the coordinator.
4. Reply routed to coordinator when no active worker: given a tracked
   thread with an empty `task_slug`, or one naming a task that is no
   longer `active`, when a new reply lands, then it is told to the
   coordinator, and the relayed text notes no worker is currently
   assigned.
5. Idle prune: given a tracked thread whose `last_activity` is more
   than 48h old, when the watcher runs, then that thread is dropped
   from state and no longer polled for replies.
6. Reply after prune is silently missed: given a pruned thread that
   receives a new reply, when the watcher runs, then nothing is
   relayed for it (documents the deliberate cutoff, not a bug to fix
   later).
7. Coordinator-set mapping survives: given the coordinator has written
   a `task_slug` into a thread's state entry between two watcher runs,
   when the watcher next runs, then it reads and honors that
   `task_slug` for routing.
8. Disabled: given `[slack] enabled = false` (or absent), the run is a
   no-op.
9. dry-run: reports intent, writes no state, sends nothing.
10. Non-thread messages ignored: given a channel message that is
    itself a reply (its `ts` is not a fresh thread root and it is not
    already tracked - e.g. it is older than the cursor, or its
    `thread_ts` is unrecognized), when the watcher runs, then it is
    not treated as a new thread.

At least scenario 1 exercises the full flow (config -> Slack API ->
dedup/tracking -> tell + poke -> state) end to end.

## Out of scope

- **Coordinator-side behavior**: deciding whether to mint a worker for
  a new thread, writing the worker's read-only brief, posting Slack
  acks/summaries, interpreting a reply as a stop request and closing
  the task, writing the resulting `task_slug` back into
  `slack-watch.json`. This is a marketer-frontend coordinator-role
  change (role.md delta), not kernel code.
- **Slack app provisioning**: creating the Slack app, bot token,
  scopes, and inviting the bot into the channel. Operator action.
- **Fleet concurrency cap**: marketer-frontend's `spore.toml` has no
  cap on simultaneous workers today. Several new threads landing close
  together could mint several workers at once. Flagged as low-risk
  (these are read-only investigations, no code changes) and left to
  coordinator judgment rather than building a cap now. Revisit if it
  becomes a real problem.
- **Cross-project routing**: this design is single fixed-project. If a
  second project ever wants the same mechanism, `[slack]` becomes a
  per-project config block same as today - no router needed - one
  watcher instance per project, not a shared multiplexer.
