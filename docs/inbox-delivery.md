# Inbox delivery and the lost-wakeup watchdog

How a message reaches an idle agent, what happens when the wake request
is dropped, and what the reconciler does about it.

## Why this exists

On 2026-10-06 a coordinator on this host sat idle for 17.7 hours with
unread mail. A Slack watcher dropped an envelope into its inbox, the
blocked Stop-hook watcher woke, moved the envelope out of the inbox,
printed it, and exited 2 to request a re-prompt. The re-prompt never
reached the pane. Because the watcher process had exited, nothing was
watching the inbox either, so two further envelopes piled up unnoticed.
The agent learned about any of it only when the operator typed into the
pane by hand, and recovered the first message only by reading the
`read/` directory manually.

Two things were wrong. The envelope was destroyed (moved to `read/`)
before anything knew the agent had woken, which made a dropped wake
lossy rather than merely late. And nothing outside the agent noticed
that the agent had gone quiet while holding mail.

## On-disk layout

An inbox directory holds:

```
inbox/
|-- <nanos>-<pid>-<n>.json   envelopes waiting to be delivered
|-- .tmp/                    staging for envelope writers
|-- .pending/                claimed, printed, not yet acknowledged
|   |-- <name>.json          the envelope itself
|   `-- .ack                 the un-acknowledged record for this batch
`-- read/                    delivered and acknowledged
```

Only the top level is watched by inotify. The record lives inside
`.pending/` for that reason: a record written beside the envelopes
would fire a spurious wake at every other watcher on that inbox on
every single claim.

## Delivery is two phase

**Claim.** A drain renames every top-level `*.json` into `.pending/`.
The rename is what makes a claim exclusive: when several stacked
watchers wake on one arrival they all try, and only the winner gets a
non-empty batch. The winner writes `.pending/.ack` naming the files and
the text it is about to print, then prints, then exits 2.

**Acknowledge.** The next start of a Stop-hook watcher promotes
everything in `.pending/` into `read/` and deletes the record. The Stop
hook only runs when a turn ends, so a fresh watcher is evidence that
the agent woke and completed one.

Until that proof arrives the record sits on disk naming an envelope
that was printed into a pipe nobody may have been reading. That is what
makes a dropped wake recoverable.

### What the acknowledgement does and does not prove

It proves a turn ended. It does not prove the agent saw this particular
delivery. Any turn ending for an unrelated reason - the operator typed,
a poke arrived on the other inbox directory - promotes a never-displayed
envelope into `read/` and deletes its record.

This is a design limit, not a defect, and it is not a regression: the
older drain moved straight to `read/` with no record at all. Closing it
needs a delivery id carried into the wake text and matched against the
turn, which is a larger change than this one.

### Delivery is at least once

A requeued envelope is printed again by the next drain. An agent may
therefore see the same message twice. That is deliberate: a duplicate
is cheap, a lost message cost 17.7 hours.

## The watchdog

`fleet.Reconcile` runs roughly every 60s per project and calls one
watchdog pass. A pass acts only when all of these hold:

- An envelope is outstanding: either an un-acknowledged record exists,
  or an envelope this watchdog previously put back is still sitting at
  the inbox top level.
- The process that claimed it is no longer running.
- The oldest outstanding claim is older than the grace window.
- The agent's own transcript shows no write since that claim.

When they do, the pass moves the staged envelopes back to the inbox top
level, re-arms an observer if none is running, and records a strike.
After `inbox_watchdog_escalate_after` consecutive strikes it escalates
once to a `tmux send-keys` nudge into the coordinator pane.

The strike streak hangs on the envelope, not on the record. The requeue
deletes the record, and during a lost wakeup nothing re-claims to write
a new one, so a streak counted on record presence could never pass one.
The envelope lying at the inbox top level across ticks is the durable
evidence that the delivery still has not landed.

### The liveness signal, and its cost

The watchdog decides "this agent is idle" from the modification time of
the newest `*.jsonl` under `<claude config>/projects/<encoded root>/`.
`CLAUDE_CONFIG_DIR` is honoured; the directory name folds every
character that is not a letter, digit or hyphen into a hyphen, matching
how Claude Code names it.

When no transcript tree exists the probe cannot answer and the watchdog
stands down - an agent that keeps no transcript (a `codex` driver, for
instance) gets no watchdog at all. That is deliberate: acting blind
risks interrupting a working coordinator, and the delivery is already
safe on disk. The watchdog logs this once per project so the operator
is not quietly uncovered.

The grace window is fifteen minutes because the transcript is written
one line per message: a long tool call writes nothing for its whole
duration. Measured across 3724 real assistant-to-tool-result intervals
on this host the median gap was 0.13s and the p99 26.2s, but the
longest was 120.2s and this harness permits Bash timeouts of 600s. A
grace below that ceiling eventually reads a working agent as a dead one
and types into its pane.

### The pane-draft rule

An unsent draft in a coordinator pane is real operator intent. The
nudge is suppressed unless the pane is readable AND its input line is
provably empty, and "cannot tell" counts as "assume a draft". The input
line is only believed within the bottom ten rows of the capture,
because the same prompt glyph opens the selected row of a selection
dialog anywhere in the pane.

Residual gap: reading the pane and sending keys are two separate round
trips to tmux. The pane is re-read immediately before the send, which
narrows the window to a single exec, but an operator who starts typing
inside it would still have the nudge appended to their line. tmux
offers no primitive that would close this.

## Configuration

```toml
[coordinator]
inbox_watchdog_escalate_after = 3
```

How many consecutive reconcile ticks may find this coordinator sitting
on an un-acknowledged delivery before the watchdog escalates from a
silent requeue to a pane nudge. Default 3, which at the ~60s tick is
roughly three minutes on top of the grace window. Absent, unparseable
or below 1 falls back to the default.

Unknown keys in `[coordinator]`, and values this binary cannot use, are
warnings rather than errors. Hosts run several spore versions at once,
and a hard failure would hand every caller a zero config: one key from
a newer release would silently strip `driver`, `model` and `brief` from
a coordinator an older binary spawns.

## Deploy order

**Upgrade every spore binary on a host together, and do not roll back
while an envelope is staged.**

A new drain stages envelopes into `.pending/`. An older binary lists
only top-level `*.json` and skips directories, so it sees an empty
inbox and never promotes, requeues or re-delivers what is staged. If
the reconciler on that host is also old there is no watchdog either,
and the envelope sits there until some new-binary Stop hook runs on
that inbox. There is no version guard and no automatic migration.

To recover by hand, move the files back yourself:

```
mv ~/.local/state/spore/<project>/inbox/.pending/*.json \
   ~/.local/state/spore/<project>/inbox/
rm ~/.local/state/spore/<project>/inbox/.pending/.ack
```

The other direction is clean: a new binary over an inbox an old one
left behind needs no migration.

Do not add `inbox_watchdog_escalate_after` to a consumer `spore.toml`
until every binary on that host understands it. Older binaries warn
rather than fail only from this release onward.
