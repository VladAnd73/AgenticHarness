---
name: triaging-a-watched-slack-thread
description: Use when a spore coordinator receives a "New Slack thread from..." or "Slack update on thread..." tell from the watch-slack timer, or is about to run `spore task done` on a task that may have started from one.
---

# triaging-a-watched-slack-thread

## Overview

A watched Slack thread is a request channel like any other `tell` -
the coordinator's ordinary judgment and brief-writing apply
unchanged. The one thing that's genuinely different: the arc from
"new thread" to "result posted back" can span a coordinator respawn.
The `tell` that dispatches a worker and the `tell` that reports it
done are very often different coordinator sessions. Nothing about
this workflow may live only in one turn's context - it has to be
readable off disk by a session that remembers none of this.

## New thread: `tell` reads "New Slack thread from U123: "..." - <permalink>"

1. **Judge it first.** Not every message needs a worker. A question
   you can answer directly, or noise, doesn't need a task - decide,
   don't default to dispatching.
2. **If it needs a worker**, write a real brief (**REQUIRED
   SUB-SKILL:** writing-a-spore-worker-brief) and `spore task start`
   it as usual. Then hand-edit the new `tasks/<slug>.md` to add one
   frontmatter line: `slack_thread: <permalink>`. There's no CLI flag
   for this - `spore task new` doesn't take arbitrary frontmatter, so
   add the line yourself. It's safe: `internal/task/frontmatter`
   preserves any key it doesn't recognize in `Extra` and round-trips
   it on every future parse/write, so it survives edits by other
   tooling and by a future coordinator session.
3. **Link the thread** so future replies come back annotated:
   `spore watch slack-set-thread <thread_ts> <task_slug>`. This only
   changes what the reply MESSAGE says (names the worker) - every
   reply still always `tell`s the coordinator, never the worker
   directly (see `internal/watch/slack.go`). The tell text only gives
   you the permalink, not the raw `thread_ts` this command needs -
   derive it: strip the leading `p` from the permalink's last path
   segment, then insert a `.` six digits from the end (verified live
   2026-09-16: permalink `.../p1789549306348279` -> ts
   `1789549306.348279`).
4. **Post one acknowledgment** into the thread naming the task slug,
   so the reporter knows it's tracked (`chat.postMessage` with
   `thread_ts` set - see the `slack` recipe, `spore recipes show
   slack`). Resolve their Slack user ID to a real name first if you
   can (`users.info`, same recipe) instead of leaving `U0123ABC` in
   front of a human.

## Reply: `tell` reads "Slack update on thread <ts> from U123: "..." (...)"

Always addressed to `"coordinator"`, never straight to the worker -
that's `RunSlack`'s design (PR #33, 2026-09-16), not a bug. The
message already says whether a worker is active on the thread and
names its slug. **Nothing is auto-posted anywhere.** If the reply
changes something the worker needs to know, or needs an answer back
in Slack, that's on you to relay - `spore task tell <slug>` for the
worker, `chat.postMessage` (slack recipe) for Slack.

## Closing the loop - may be a different session than the one that dispatched it

Before running `spore task done` on ANY task, check its frontmatter
for a `slack_thread` line. If one is there:

1. Post a short result to that thread first - one line plus whatever
   link matters (PR, ticket, direct answer). Slack recipe again.
2. Then close the task as usual.

Finding a `slack_thread` line on a task you don't remember dispatching
is expected, not a bug - that's the whole point of writing it to disk
instead of trusting session memory.

## Common mistakes

| Mistake | Reality |
|---|---|
| Assuming a reply auto-posts into Slack | Nothing spore does ever posts to Slack on its own. `RunSlack` only ever `tell`s the coordinator. Every message that reaches the channel is a `chat.postMessage` call you make. |
| Dispatching a worker for every new thread | Judge first. A question or non-actionable message doesn't need a task. |
| Trusting `slack-set-thread` alone to "remember to post the result" | That CLI only fixes reply ROUTING - it records the mapping in `slack-watch.json`, not on the task, and it never posts to Slack. Write `slack_thread` into the task file too, or a cold session at done-time has nothing to check. |
| Leaving the raw Slack user ID in a brief | Resolve it with `users.info` (slack recipe) so a human reading the brief later isn't stuck decoding an ID. |
| Treating this as one continuous session | The arc from thread to posted result can and usually will span a respawn. Anything that matters must be on disk - the task file, `slack-watch.json` - never only in this turn's context. |
