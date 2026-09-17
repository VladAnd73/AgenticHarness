---
name: investigating-a-reported-bug
description: Use when a spore coordinator judges a watched Slack message (or any other bug report) as describing an actual bug or issue, not a question or a general request, and needs a grounded, cited answer before replying. Covers dispatching a full investigator worker, granting it read-only access to a pinned clone of any repo it names, dispatching a separate blind verifier that sees only the citation list, and composing the one verified Slack reply. Builds on triaging-a-watched-slack-thread for thread linking and dispatch bookkeeping - use both together for a Slack-sourced bug report.
---

# investigating-a-reported-bug

## Overview

A bug report deserves a real, checked answer, not a plausible-sounding
guess dressed up as more confident than it is. This skill runs that
check as two independent, full `spore task` workers - an investigator
that drafts a finding with citations, then a separate verifier that
blindly re-derives each citation against the exact repo state the
investigator saw. The coordinator only ever posts what survives
verification. Neither worker edits code, commits, pushes, opens a PR,
or files a ticket - this is read-only research, always.

This is the investigation step `triaging-a-watched-slack-thread` never
had - that skill only judges and routes a thread to a worker. Use both
together: judge and link the thread per that skill, then use THIS
skill to decide what kind of worker to dispatch when the message is a
bug report.

## When this fires

Per `triaging-a-watched-slack-thread`'s "judge it first" step: if the
message describes something broken, wrong, or unexpectedly behaving -
not a general request, a question you can answer directly, or noise -
dispatch the two-stage pipeline below instead of an ordinary single
worker. The other project's frontmatter and thread-linking steps
(`slack_thread` frontmatter line, `spore watch slack-set-thread`, one
ack post) still apply on top of this, unchanged.

## Stage 1: dispatch the investigator

Write the brief per **REQUIRED SUB-SKILL:** writing-a-spore-worker-brief.

**Deliverable:** one `spore task tell coordinator` report containing a
draft that ends in exactly one of three states, named explicitly:

1. a possible solution, backed by real citations, or
2. relevant leads plus a plainly-named point where it got stuck, or
3. an honest "checked X, Y, Z - found nothing relevant."

**Do NOT produce:** no code edits, no commit, no push, no PR, no
ticket filed, no repo cloned on its own authority, nothing between
those three states dressed up as more certain than it is.

Tell the investigator to reach for existing tools before inventing
search logic, roughly in this order:

- `consulting-marketer-product-knowledge` - is this even a bug, or
  expected behavior the reporter didn't know about?
- `sentry:sentry-debug-issue` - real stack traces and issues, if the
  report smells Sentry-eligible.
- Linear ticket search - is this a duplicate, or does a related ticket
  narrow the search?
- Plain grep / `git blame` over a repo it already has checked out.

**Repo access is coordinator-mediated, always.** If the investigator
believes the bug lives in a repo it doesn't have, it sends
`spore task tell coordinator "need repo <org>/<name>, believe the bug
is in <area> because <reason>"` and waits. It never runs `git clone`
itself, even against a public repo - the coordinator must pin a commit
SHA both stages will see (see "Shared, pinned repo access" below).

**Citation discipline:** every claim in the draft needs a real,
checkable pointer against the pinned repo state - `file:line`, a
Sentry issue link, a Linear ticket ID, or an exact log line. No
pointer, no claim: drop the claim rather than soften it into a hedge.

**Closing:** report-only, no commits in its own worktree.
`spore coordinator verify-done` is expected to return
`suspect-hallucination` for exactly this reason (see docs/evidence.md
and "Common mistakes" below) - confirm against the `tell` content and
close with `spore task done <slug> --force`.

## Shared, pinned repo access

When the investigator asks for a repo, the coordinator - never the
worker - is the one that clones it:

1. Pick a commit SHA to pin to (typically the default branch tip at
   the moment of the request, unless the report names a specific
   version).
2. Shallow-clone it read-only into one shared scratch path, pinned to
   that SHA. Use the github recipe's "Pin a shallow clone to a specific
   commit SHA" worked example (`spore recipes show github`) - a plain
   `git clone --depth 1` alone only gets you the branch TIP, not an
   arbitrary historical commit; pinning needs a `git fetch --depth 1
   origin <sha>` followed by `git checkout <sha>` after the initial
   shallow clone.
3. Tell the investigator the path and SHA:
   `spore task tell <investigator-slug> "repo ready: <path> at <sha>"`.
4. Keep that exact path+SHA - hand the identical pair to the verifier
   later. Never re-clone or let the real repo move under either stage;
   that is the whole point of pinning.
5. Delete the scratch clone once the investigation closes, either
   outcome.

## Stage 2: dispatch the verifier

Dispatched only after the investigator's `tell` lands.

**Deliverable:** one `spore task tell coordinator` report with a
verdict - each citation marked confirmed or rejected, and why -
degrading to the honest "found nothing relevant" state if every
citation strips.

**Do NOT produce:** no Slack access, no code edits, no commit, no
push, no PR, no ticket - and, the reason this stage exists as a
SEPARATE worker: no access to stage 1's narrative or reasoning, only
its citation list.

**Brief-writing rule specific to this stage:** before it goes in the
verifier's brief, strip the investigator's `tell` down to JUST the
citations - a bare list of pointers (`file:line`, Sentry link, Linear
ID, log line). No prose framing, no "I believe", no hint at which of
the three states stage 1 landed in. Handing over the narrative defeats
the blind re-derivation - a verifier that can see stage 1's reasoning
tends to agree with it instead of independently checking it.

**Task:** for each citation, re-open it against the SAME pinned
path+SHA handed to the investigator - never a fresh clone, never the
live repo - re-read the file at that line, re-fetch the Sentry/Linear
link, re-check the log line, and mark it confirmed or rejected. Any
claim that fails to reproduce is stripped, full stop; no softening a
rejection into a hedge. If every claim strips, the result IS "found
nothing relevant" - never silently empty, never a guess dressed up as
a finding.

**Closing:** same report-only pattern as stage 1 - `verify-done`
returns `suspect-hallucination`, expected, close with `--force`.

## Delivery: the coordinator composes, never a worker

Once the verifier's `tell` lands, the coordinator - not either worker -
composes ONE reply into the ORIGINAL Slack thread (`chat.postMessage`
with `thread_ts` - `spore recipes show slack`), stating plainly which
of the three outcome states it landed in. This is always the VERIFIED
result (the verifier's surviving claims), never the investigator's raw
draft - even while the verifier is still running, do not post the
investigator's draft as an interim update.

## Common mistakes

| Mistake | Reality |
|---|---|
| Posting the investigator's raw draft because the verifier hasn't finished yet | Wait for the verifier. Nothing goes to Slack before the citation-by-citation re-check - an unverified "possible solution" is exactly the plausible-sounding guess this pipeline exists to prevent. |
| Letting the investigator clone a repo on its own authority | Every repo it touches comes from the coordinator, pinned to a SHA. A worker-initiated clone breaks the pin the verifier depends on to check the SAME state the investigator saw. |
| Handing the verifier the investigator's narrative or reasoning along with the citations | Strip it to the bare citation list before it goes in the verifier's brief. A verifier that sees "I believe X because Y" is primed to agree, not independently re-derive. |
| Panicking at `suspect-hallucination` on either stage's `verify-done` | Expected for report-only tasks with no commits - see docs/evidence.md. Confirm against the `tell` content, close with `--force`. |
| Treating a verifier verdict that stripped every claim as a failure | It's the "found nothing relevant" state working as designed - post it plainly, never invent a softer answer. |
| Filing a Linear ticket automatically from a finding | Out of scope by design - Slack reply only. A human can still run `linear-bug-report` on top of a finding afterward. |

## See also

- `triaging-a-watched-slack-thread` - the judgment and thread-linking
  steps this skill assumes are already happening.
- `writing-a-spore-worker-brief` - brief structure for both stage
  briefs.
- github recipe (`spore recipes show github`) - the pinned-SHA clone
  mechanics.
- slack recipe (`spore recipes show slack`) - posting the final reply.
