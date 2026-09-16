**Status**: design brainstormed with the operator 2026-09-16. Not yet
a worker task. This doc is the implementation contract.

# slack-bug-investigation: ground a CS bug report before anyone answers it

## Goal

When a customer-support agent mentions a bug or issue in a watched
Slack thread, the harness should do a real, grounded first-pass
investigation and reply in that same thread with exactly one of:

1. a possible solution, backed by real citations, or
2. relevant leads plus a plainly-named point where it got stuck, or
3. an honest "checked X, Y, Z - found nothing relevant."

Never a plausible-sounding guess dressed up as more confident than it
is. The whole point of this feature is that a developer (or the CS
agent) can trust what it says without re-verifying it themselves -
every claim must trace to something real: a `file:line`, a Sentry
issue, a Linear ticket, an exact log line. No pointer, no claim.

This builds on the already-live `triaging-a-watched-slack-thread`
skill and the `assistant` project's Slack watcher (see
`slack-thread-watcher.md`) - it adds the actual investigation step
that skill never had; today it only routes threads to a worker, it
never looks into anything.

## Decisions locked with the operator

- **Owning project**: `assistant` (already exists, already watches
  Slack, already bootstrapped). Not a new project. Its coordinator
  dispatches the investigation the same way any coordinator dispatches
  a task - no new kernel role, no `internal/fleet`/`internal/composer`
  changes expected.
- **Repo access**: org-wide, not limited to what's already checked out
  on this host. If the investigator believes the bug lives in a repo
  it doesn't have, it asks its coordinator, which shallow-clones it
  **read-only** from GitHub. `assistant` having no GitHub remote of its
  own (per its own bootstrap) is unrelated - this is about reading
  OTHER repos, never assistant's own.
- **Verification shape**: two independent, FULL `spore task` workers -
  an investigator, then a separate verifier - not a single
  self-checking worker, and not a same-session Agent-tool subagent.
  Reasoning locked with the operator: the coordinator may be doing
  other things, or may respawn, between the two stages; a full task
  survives that the way an in-session subagent would not.
- **Delivery**: reply in the original Slack thread only. No automatic
  Linear ticket filing - if a finding is worth a ticket, that's a
  separate, human-triggered use of the existing `linear-bug-report`
  skill, not something this pipeline does on its own.
- **Authority**: strictly read-only, always. Neither stage edits code,
  commits, pushes, opens a PR, or files a ticket in ANY repo it looks
  at. This is investigation, not remediation.

## Architecture

### Trigger and judgment

Unchanged from `triaging-a-watched-slack-thread`'s existing step 1:
the coordinator judges every new-thread `tell` before dispatching
anything. Only a message that actually describes a bug or issue
starts this pipeline - a question, a greeting, or noise still gets the
coordinator's ordinary judgment, not an investigation.

### Stage 1: investigator (`spore task`, full worker, no PR)

Given the bug report text, the worker:

- Reaches for existing skills/tools before inventing new search logic:
  `consulting-marketer-product-knowledge` (is this even a bug, or
  expected behavior?), Sentry (`sentry-debug-issue` - real stack
  traces/issues), Linear (duplicate/related tickets), plain
  grep/git-blame over the app source it already has.
- If it believes the bug lives in a repo it doesn't have checked out,
  it asks its coordinator for one by `tell` and waits - it never
  clones anything on its own authority.
- Tags every claim it drafts with a real, checkable pointer - a
  `file:line`, a Sentry issue link, a Linear ticket ID, an exact log
  line. A claim with no pointer does not go in the draft.
- Ends in exactly one of the three states from "Goal" above, stated
  explicitly - never something in between dressed up as more certain
  than it is.
- Does not edit code, commit, push, open a PR, or file a ticket.
  Reports its draft (findings + citations, NOT a final answer yet) to
  its coordinator by `tell`.
- Closing this task: it will have made no commits (it's read-only
  research), so `spore coordinator verify-done` is expected to return
  `suspect-hallucination` - a known false negative for report-only
  tasks (see `state.md`'s CRITICAL LESSON on this). Confirm against
  the `tell` content and close with `--force`, same as any other
  report-only task.

### Shared, pinned repo access

When the investigator requests a repo, the coordinator - not the
worker - shallow-clones it read-only from GitHub into one shared
scratch path, **pinned to a commit SHA**, and hands that same path+SHA
to both the investigator and, later, the verifier. Pinning matters:
the verifier must check the exact file state the investigator saw, not
a moved target if the real repo changes mid-investigation. Scratch
clones are deleted once the investigation closes.

**Open prerequisite, operator-owned**: this needs a GitHub token
scoped for read access across the org's repos. The existing `GH_TOKEN`
in this host's vaults is scoped per-project/per-org already (see
project memory on Linear/GitHub token scoping) - do not assume it
already covers this. Confirm scope, or provision a new token, before
this ships.

### Stage 2: verifier (`spore task`, full worker, no PR)

Dispatched by the coordinator once stage 1 reports its draft.

- Gets ONLY the citation list from stage 1 - not its narrative or
  reasoning. This is a blind re-derivation, the same shape as the
  nightly-dreaming pipeline's proposer/reviewer split
  (`nightly-dreaming.md`) and `internal/evalharness`'s own
  `reviewer-fabricated-citation` / `reviewer-real-evidence` scenarios.
- Independently re-opens every cited pointer against the pinned repo
  state (re-reads the file at that line, re-fetches the Sentry/Linear
  link, re-checks the log) and marks each claim confirmed or rejected.
- Any claim that fails to reproduce is stripped. If every claim gets
  stripped, the result degrades to the honest "found nothing relevant"
  state - it never silently posts nothing, and never waters a
  rejection down into a softer guess.
- Reports its verdict (surviving claims, stripped claims, and why) to
  the coordinator by `tell`. No Slack access, no code edits, same
  report-only closing pattern as stage 1.

### Delivery

The coordinator - not either worker - composes and posts the final
result into the original Slack thread (`chat.postMessage` with
`thread_ts`, the existing `slack` recipe), stating which of the three
outcome states it is. This extends `triaging-a-watched-slack-thread`'s
existing "nothing auto-posts, coordinator relays" rule with: the
coordinator only ever posts the VERIFIED result, never the
investigator's raw draft.

### Skill surface

A new skill (or a clearly separated section inside
`triaging-a-watched-slack-thread` - worth deciding once it's drafted
and it's clear which reads better) covering: how to judge a message as
a bug report, how to write the investigator brief, how to request and
receive a pinned repo clone, how to write the verifier brief (handing
over only citations, never narrative), and how to compose the final
Slack reply from the verifier's verdict. No `internal/fleet` or
`internal/composer` changes are expected - this is skill content plus
existing task/tell/recipe mechanics, the same shape as the slack
recipe and the triaging skill already shipped in PR #34.

## Testing (Worker TDD - write these first, red then green)

- Given a fixture bug report and a pinned fixture repo state
  containing one real, checkable bug and one plausible-but-wrong
  citation, when the verifier stage runs, then it confirms the real
  citation and rejects the fabricated one.
- Given a fixture bug report with nothing matching in any available
  source, when the investigator runs, then it reports the explicit
  "found nothing relevant" state rather than a strained guess.
- Given a citation list where the verifier rejects every claim, when
  the coordinator composes the Slack reply, then it posts the honest
  "found nothing" message, not an empty or falsely-confident one.
- Given a report-only task (investigator or verifier) with no commits
  in its worktree, when `spore coordinator verify-done` runs, then it
  returns `suspect-hallucination` and this is expected, not a bug -
  confirm against the `tell` content and close with `--force`.

**Open implementation-time decision, not settled here**: should the
verifier's grading machinery extend `internal/evalharness`'s
`Scenario.Kind` enum (it already generalizes over "which brief, which
fixture, which expectation" for the dream pipeline's proposer/reviewer
pair), or live in a small parallel package? `internal/evalharness/session.go`'s
own doc comment warns against building "a general session-simulator
framework." Recommendation: extend `Kind` only if, once actually
reading the current code, it's a clean fit that doesn't distort the
dream-pipeline-specific parts of the package; otherwise build a
narrowly-scoped parallel harness rather than force a fit. Whoever
implements this should make the call with the code in front of them,
not from this doc alone.

## Out of scope

- Auto-filing Linear tickets. Decided against for now - Slack reply
  only. A human can still use `linear-bug-report` on top of a finding.
- Any code fix, PR, or commit produced by the investigation pipeline
  itself. It is read-only research, always, in every repo it touches.
- Repos not on GitHub, or not reachable by the operator's token.
- A cross-org token with WRITE access anywhere - read-only clone only,
  ever.
- Extending the org-wide clone mechanism to any project other than
  `assistant`'s investigation pipeline. Marketer-frontend and other
  projects keep using their own existing skills against their own
  repo; this pipeline does not change how they work.
