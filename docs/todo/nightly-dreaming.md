**Status**: fully implemented 2026-09-04, not deployed. Stages 1-2
(discover, digest, score, ledger, snapshot/revert, the `[dreams]` config
table, the embedded briefs, the mint, the composed run, and `spore dream
digest|runs|revert|rewind`) merged via PR #20. Stages 3-5 (gate the
proposer's evidence packets against the ledger, spawn an independent
adversarial reviewer per surviving packet, and write only `confirmed`
survivors) merged via PR #21. `[dreams]` is enabled on no project yet;
that flip is deliberately the operator's. No unit is installed on any
host. To deploy, see
[nightly-dreaming-deploy.md](nightly-dreaming-deploy.md). Named
follow-ups are under "Follow-ups" below.

# nightly dreaming: learn from spore sessions

## Problem

Agents on this host produce a large volume of session transcripts every
day, and everything learned inside a session dies with it. Lessons reach
the harness only when a human or a coordinator happens to notice a
pattern and write it down by hand. That is unreliable and it does not
scale: 460 transcripts totalling 433 MB exist today, 23 files and
19.4 MB of them touched in a single 24-hour window (measured
2026-08-31).

The result is repeated mistakes. Three concrete examples found while
adopting the `state-debt` convention on the same day:

- The `gh pr create` fork-base gotcha was rediscovered more than once
  before it reached memory.
- The done-gate false negative bit two separate deliveries (PR #9 and
  PR #13) before it was written down at all.
- A lesson recorded in `state.md` ("tell does not wake an idle
  coordinator") stayed there for seven weeks after its own fix had
  merged, actively misinforming every session that read it.

Anthropic ships an analogous feature for hosted agents, Dreams
(`https://platform.claude.com/docs/en/managed-agents/dreams`): it reads
a memory store plus past session transcripts and produces a curated
replacement. It does not apply here (it operates on Managed Agents
memory stores, which this host does not use), but the shape is right and
this design borrows it.

## Goal

A job that runs every night, reads the spore-managed sessions that are
new since the last run, and turns what it finds into durable harness
improvements: lessons, rules, memory entries, and candidate skills. Per
project, because lessons and skills are per project.

## Non-goals

- Reading non-spore sessions. The pilot's own panes and any ad-hoc
  `claude` session on this host are out of scope.
- Curating any hosted memory store. This is entirely local files.
- Installing skills automatically. Skills are proposed, never written
  into place (see Decisions).
- Editing project source. The job writes lessons, memory, and skill
  proposals only.

## Decisions locked (operator, 2026-08-31)

1. **Input scope**: spore-managed sessions only, meaning coordinator and
   worker sessions. Partitioned per project; every output is
   project-scoped.
2. **Authority**: lessons and memory entries are written automatically.
   Skills are proposed to a review file and never installed by the job.
3. **Evidence bar**: two-tier. An explicit operator correction is
   sufficient evidence on a single occurrence. Anything the job infers
   on its own must recur in at least two independent sessions.
4. **Read depth**: mechanical digest of every in-scope session, plus a
   full read of a small capped number of flagged sessions per project.
5. **Execution**: the deterministic half is Go (`spore dream`); the
   judgement half is a normal spore worker spawned by the fleet.
6. **Adversarial review**: no change is written until an independent
   reviewer confirms it against real documentation, code, or live host
   state. The reviewer is barred from answering from model knowledge and
   defaults to reject.

## Architecture

Five stages. Stages 1 and 2 are deterministic Go. Stages 3 to 5 run
inside one worker session per project.

    03:00 timer
      |
      1. discover     Go   find + classify new sessions
      2. digest       Go   extract high-signal slices, score, flag,
      |                    update ledger, mint one task per project
      |
      fleet spawns a worker per project
      |
      3. propose      worker    read digests, deep-read flagged,
      |                         emit evidence packets
      4. review       subagent  one per packet, fresh context,
      |                         adversarial, default reject
      5. write        worker    survivors only; snapshot first
      |
      report -> tell the project coordinator

### Stage 1: discover

Scan `~/.claude/projects/*/*.jsonl` for files modified since the
per-project watermark. Classify each session by two markers, both
verified live on 2026-08-31:

- **worker**: the entry `cwd` matches
  `^<home>/(?P<project>[^/]+)/\.worktrees/(?P<slug>[^/]+)$`.
- **coordinator**: the first user message begins with
  `# Coordinator role`. The project is the basename of `cwd`.
- **everything else**: dropped. This is what excludes the pilot pane and
  ad-hoc sessions, which share a `cwd` with the coordinator but carry no
  role marker.

Subagent activity is already inline in the parent transcript (entries
carry `isSidechain`), so it needs no separate discovery.

Output: a list of `(project, kind, slug, path, first_ts, last_ts)`.

### Stage 2: digest

Per session, extract only high-signal slices:

- every message from the operator, verbatim, with session id and
  timestamp (this is tier-one evidence for the two-tier bar)
- tool calls that returned an error, plus the following few turns, so
  the recovery is visible alongside the failure
- commands issued more than a threshold number of times in one session
- denied permissions and blocked hook results
- for workers: the task brief and the final report
- terminal state: done, blocked, or token exhaustion

Everything else is dropped. Successful reads, routine edits, and
assistant reasoning are the bulk of the bytes and hold the least signal.

Each session is then scored for deep-read priority on error density,
operator turn count, length, and whether it ended blocked or failed. The
top `deep_read_cap` sessions per project (default 5, see Configuration)
are flagged. The cap is enforced in Go so the worker cannot widen it.

Layout per run:

    ~/.local/state/spore/<project>/dreams/<run-id>/
      digest.md        human and model readable, all in-scope sessions
      sessions.json    machine index: paths, scores, flags
      backup/          pre-write copies of every file the run touches
      report.md        written at the end of stage 5
    ~/.local/state/spore/<project>/dreams/ledger.json
    ~/.local/state/spore/<project>/dreams/watermark

`<run-id>` is the UTC date plus a short random suffix, and is stamped
into every artifact the run writes.

Stage 2 ends by minting one task per project that has any in-scope
session, via the normal `spore task new` / `spore task start` path, so
the existing fleet spawns the worker. A project with nothing new gets no
task and no pane.

### Stage 3: propose

The worker reads `digest.md`, deep-reads the flagged sessions in full,
and emits candidate findings. It writes nothing else. Each candidate is
an evidence packet:

    claim:      one sentence
    type:       operator-preference | tool-behavior | host-state |
                code-behavior | process-pattern
    evidence:   pointers, not quotes - session id + timestamp for an
                operator message, file:line for code, the exact command
                that would prove a host claim, the doc URL for an API
    tier:       lesson-block | memory-entry | skill
    target:     the file it would change
    text:       the literal proposed content

The ledger gates which candidates may proceed. An
`operator-preference` candidate proceeds on first sighting. Every other
type must have `occurrences >= 2` across independent sessions; a first
sighting is recorded as a candidate and the run moves on.

### Stage 4: adversarial review

Every packet that clears the ledger goes to a reviewer in a **fresh
context** that receives the packet and the target file and nothing else.
It never sees the proposer's reasoning, so it cannot inherit the
proposer's confidence.

Three rules define the review:

1. **Re-derive, never trust.** Citations in the packet are pointers, not
   proof. The reviewer runs the command itself, opens the file at that
   line, reads the actual `--help` output or documentation. Text quoted
   in the packet carries no weight; a hallucinated quote must not be
   able to pass.
2. **Documentation over recall.** For any claim about how a tool, flag,
   command, or API behaves, the reviewer consults the real source:
   `--help`, the man page, the code in this repo, or the official
   documentation fetched over the network. Answering from model
   knowledge is prohibited. For a claim about this host, the standard is
   higher: run the command and quote the output.
3. **Default to reject.** Uncertain, unevidenced, or unverifiable is a
   refusal. The burden is entirely on the proposal.

Beyond truth, the reviewer must also refute:

- **stale claims**: the fix already landed, the flag was renamed, the
  gap is closed. This is the failure mode that produced the seven-week
  wrong lesson, so it is a required check, not an optional one.
- **overgeneralisation**: true of one odd session, written as a general
  rule.
- **duplicates**: already covered by an existing memory entry, lesson,
  rule, or skill.
- **leaks**: operator-machine paths, internal hostnames, or personal
  addresses in content bound for a committed file.

Verdicts:

- `confirmed` proceeds to stage 5.
- `refuted` means the reviewer positively established the claim is
  false. Recorded against the fingerprint and permanently dead: the
  finding is dropped before review on every later run.
- `unevidenced` means the reviewer could neither confirm nor refute.
  Drops back to candidate so a later night with better evidence may
  retry. A fingerprint that comes back `unevidenced` twice is also
  marked permanently dead, so an unprovable claim cannot be re-reviewed
  forever.

### Stage 5: write

Survivors only, and only after the run has copied every target file into
`backup/`.

| Tier | Destination | Mode |
| --- | --- | --- |
| lesson / rule | that project's `state.md`, in the state-debt block format | auto |
| memory entry | that project's memory dir, plus its `MEMORY.md` index line | auto |
| skill | `<run-dir>/skill-proposals/<name>.md` | proposed only |

A project missing a target is not an error. If a project has no
`state.md`, the run creates one with the lessons section only; if it has
no memory directory, the lesson tier is used and the memory candidate is
held rather than creating a memory tree the project never adopted.

Lesson blocks use the heading convention the scanner already reads:
`### CRITICAL LESSON: <title> (<date>)` or `### RULE: ...`, with a
`harness:` line when the lesson has been lifted. Every written item
carries its run id so it can be traced back and reverted.

The run then writes `report.md` (written, refuted with reasons, held as
candidates, skills awaiting review) and tells the project's coordinator.

## Safety

Neither `~/.claude/` nor any memory directory is a git repository
(verified 2026-08-31), so auto-writes have no version control to fall
back on. Three mechanisms compensate:

- **Snapshot before write.** Every file a run will touch is copied to
  `<run-dir>/backup/` first.
- **`spore dream revert <run-id>`.** Restores that run's snapshots and
  marks its ledger entries back to candidate.
- **Hard caps in Go**: sessions deep-read per project, and writes per
  run. A runaway night cannot rewrite the harness wholesale.

## Configuration

Per project, in the existing watch config
(`~/.config/spore/<project>/watch.toml`), following the `[releases]`
precedent:

    [dreams]
    enabled = true
    deep_read_cap = 5
    max_writes_per_run = 10
    recurrence_threshold = 2

The values shown are the defaults. `deep_read_cap` is 5 rather than the
3 this spec first named: the bar for an inferred claim is two
independent sessions, and three deep reads make that count the binding
constraint rather than the evidence. The number lives in two places
(`internal/dream.DefaultDeepReadCap` and the `[dreams]` default in
`internal/watch/config.go`) because the CLI passes the config value
straight through, and
`TestDefaultDeepReadCapMatchesTheWatchConfigDefault` fails if they ever
disagree again.

Kernel defaults must name no project and no skill, matching the rule the
release watcher already follows.

## Deploy

See [nightly-dreaming-deploy.md](nightly-dreaming-deploy.md). It was
verified on a live host on 2026-09-03 by installing the units, firing
them from a real timer, and reading the journal.

Summary: one hand-installed user oneshot per project,
`spore-dream-<project>.{service,timer}`, `OnCalendar=*-*-* 03:00:00`
with `RandomizedDelaySec=15min` and `Persistent=false`, no `Restart=`,
no path unit, running the local binary `~/.local/bin/spore-evolve`. No
sudo and no host rebuild. The worker half needs no deploy, since it goes
through the normal fleet.

Two things this spec got wrong and the deploy note corrects. First,
rebuilding `~/.local/bin/spore-evolve` re-points every other
hand-installed watcher unit on the host at the new build in one step, so
it must only ever be rebuilt from `main`. Second, `spore dream revert`
is not the undo for a bad night: a digest run snapshots nothing, so
reverting one fails by design. The undo is to clear the minted task and
then `spore dream rewind`.

## Follow-ups

Each of these came out of building stages 1 and 2. None is built. The
numbers are measured on this host unless stated.

1. **Build the prune.** Nothing reaps run directories under
   `$XDG_STATE_HOME/spore/<project>/dreams/`. A first run over a whole
   corpus wrote 33,494 bytes; an incremental one-session night wrote
   1,449 bytes, so steady state is roughly 1.5 KB to a few KB per night
   per project. This needs new Go and the rule cannot be "keep the
   newest K" or "older than N days" alone: the minted proposer reads the
   run directory and the fleet spawns it about 0.4 seconds after the
   mint, so the rule has to be "older than N days AND its minted task is
   done or gone from `tasks/`".
2. **Move three formats back into the packages that own them.**
   `cmd/spore/dream_cmd.go` currently redeclares all three.
   `dream.Rewind` is the urgent one: the CLI holds its own copy of the
   three-field watermark struct, so a rename inside `internal/dream`
   would leave rewind writing the old field names and the next night
   silently re-reading the whole corpus.
   `TestRewindRoundTripsTheWatermarkFormatDreamRunWrites` in
   `cmd/spore` now fails loudly if that happens, but the duplicate is
   still there. The other two are `dream.ListRuns` (the CLI decodes the
   manifest's `created_at` itself) and an exported watch-config path
   (the CLI recomputes `watch.toml`'s location in `watchTomlPath`).
3. **`RevertWithReport` creates the run directory before it looks for a
   manifest.** `internal/dream/backup.go:278` calls `RunDir`, which
   `MkdirAll`s, so asking to revert a run id nobody has heard of creates
   it. The CLI works around it with a stat-first guard
   (`cmd/spore/dream_cmd.go`); the defect is still in the package.
4. **`previous` holds exactly one step.** `internal/dream/run.go:232`
   writes `previous` from the value it is replacing and keeps no further
   history, so two consecutive nights whose tasks are never judged lose
   the older night irrecoverably and `rewind` cannot reach it.
5. **The adversarial reviewer stage does not exist.** The proposer brief
   is embedded and a reviewer brief is embedded
   (`internal/dream/briefs/`), but no code runs the reviewer. Until it
   does, `[dreams] enabled = true` means unreviewed proposals reaching a
   real fleet about 0.4 seconds after the mint.
6. **Discovery fails silently in several ways, and each looks like a
   quiet night.** `Discover` returns `nil, nil` when the projects root
   is missing (`internal/dream/discover.go:50`). `classify` swallows
   per-file errors (`discover.go:69`). The coordinator marker depends on
   the shared seed file's literal first line `# Coordinator role`
   (`discover.go:26`); the run does warn when it finds no coordinator at
   all, which is the only signal that the marker has drifted. An 8 MB
   JSONL line truncates a session's timestamps rather than erroring
   (`discover.go:91`).
7. **The two-tier evidence bar has no Go caller.** `Ledger.Observe`,
   `Ledger.Gate` and `Ledger.Record` are written and tested, but the
   only ledger call the nightly run makes is `LoadLedger` plus
   `formatKnownClaims`, which is read-only. The bar is enforced today
   only by prose in the proposer brief. Whichever task builds stage 3
   has to wire them in.
8. **Nothing caps the minted-task backlog.** `MintTask` writes an active
   task unconditionally, so consecutive nights accumulate one active
   dream task each and the fleet works through all of them. Observed:
   three active dream task files after three runs.
9. **`deep_read_cap = 0` does not mean zero.** `internal/watch` accepts
   0 and documents it as a legal "do none of this", but
   `internal/dream`'s `deepReadCap` treats a zero `Options.DeepReadCap`
   as "no cap named" and substitutes `DefaultDeepReadCap`. There is
   currently no way to express "no deep reads" through config. Fixing it
   means changing the documented meaning of a zero `Options` field, so
   it did not belong in a docs commit.

## Acceptance scenarios

Required by the worker-TDD rule. Each must be a test that fails before
implementation and passes after.

1. **Discovery excludes non-spore sessions.** Given a transcript
   directory holding one worker session, one coordinator session, and
   one ad-hoc session at a project root with no role marker, when
   discovery runs, then only the first two are returned, attributed to
   the right project and kind.
2. **Watermark advances.** Given a completed run, when the job runs
   again with no new transcripts, then it returns no sessions, mints no
   task, and leaves the ledger unchanged.
3. **Digest drops noise and keeps signal.** Given a session containing
   an operator correction, a failed tool call, and fifty successful
   reads, when the digest is built, then it contains the correction and
   the failure and none of the reads.
4. **Deep-read cap is enforced.** Given ten sessions all scoring above
   the flag threshold and `deep_read_cap = 3`, when digesting, then
   exactly three are flagged.
5. **Two-tier evidence bar.** Given an operator-preference candidate
   seen once and an inferred candidate seen once, when the ledger gates
   them, then the first proceeds and the second is held; when the
   inferred one appears in a second independent session, then it
   proceeds.
6. **Refuted findings never return.** Given a fingerprint refuted by the
   reviewer, when the same finding is proposed on a later run, then it
   is dropped before review.
7. **Nothing is written without a confirmed verdict.** Given a run whose
   every packet is refuted, when it completes, then no target file is
   modified and the report lists the refusals with reasons.
8. **Revert restores exactly.** Given a run that wrote to `state.md` and
   two memory files, when `spore dream revert <run-id>` is run, then all
   three files are byte-identical to their pre-run state and the ledger
   entries are back to candidate.
9. **Skills are never installed.** Given a confirmed skill candidate,
   when the run completes, then a file exists under `skill-proposals/`
   and no file was created in any `.claude/skills/` directory.
10. **Full flow, end to end.** Given a fixture transcript directory for
    one project containing a repeated operator correction and a
    genuinely stale claim, when the whole pipeline runs, then the
    correction is written as a lesson block that `spore coordinator
    state-debt` parses, the stale claim is refuted with a recorded
    reason, a backup exists for every written file, and the coordinator
    receives one report envelope.

## Risks and open questions

- **Reviewer network access.** Rule 2 requires fetching real
  documentation. Interactively-authenticated MCP servers may be
  unavailable to a job started by a timer. Implementation must confirm
  what the worker can actually reach at 03:00 and fall back to a direct
  fetch for known URLs. If nothing is reachable, an API-behavior claim
  is `unevidenced`, not confirmed.
- **Self-reference.** The dream worker's own transcript becomes input on
  a later night. Harmless but worth watching for a feedback loop where
  the job keeps rediscovering its own output; the fingerprint ledger
  should absorb it.
- **Fingerprint stability.** If fingerprints are too literal, the same
  lesson phrased differently reads as new and the recurrence counter
  never advances. This is the most likely thing to need tuning after
  the first week.
- **Cost.** Roughly 0.5 to 1M tokens per night before review, plus one
  small agent per surviving candidate. Worth re-measuring after the
  first week against a real run rather than this estimate.
