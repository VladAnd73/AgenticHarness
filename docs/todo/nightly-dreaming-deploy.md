**Status**: reference. Verified on a live host 2026-09-03 by installing
the units, firing them from a real timer, and reading the journal. Every
command output quoted here was produced by that run.

# Deploying the nightly dream job

`spore dream digest` is the deterministic half of nightly dreaming. It
reads this machine's own session transcripts, writes a digest, and mints
one proposer task that the fleet picks up. This note is how to put it on
a timer, and what it costs you when you get a step wrong.

Read "Before you deploy anything" first. The single most damaging step
in this runbook is the binary rebuild, and it is step 1.

## Before you deploy anything

Three preconditions, in order.

**1. The arc must be merged to `main`.** The dream unit runs the local
binary at `~/.local/bin/spore-evolve`. That same binary is what every
other hand-installed watcher unit on this host executes. Rebuilding it
re-points all of them at your new build in one step. So rebuild it only
from `main`, never from a feature branch and never from an integration
branch: a branch that has not merged carries whatever else is on it into
the watchers as well.

To see the blast radius on your own host before you rebuild:

    grep -l 'spore-evolve' ~/.config/systemd/user/*.service

Every unit that lists is a unit you are about to upgrade. On the host
this was written on, two watcher services matched, both with active
timers, neither related to dreaming.

There are two spore binaries on a host like this, with two separate
deploy paths, and this note only ever touches the second one:

| | store binary | local binary |
|---|---|---|
| path | `/run/current-system/sw/bin/spore` | `~/.local/bin/spore-evolve` |
| shipped by | flake input bump plus `nixos-rebuild switch` | `nix develop -c go build` |
| needs sudo | yes, operator only | no |
| runs | the `spore` CLI and the fleet reconciler that spawns workers | the hand-installed watcher timers |

Anything in the **worker-spawn** path runs under the store binary and
moves only when the operator rebuilds; `environment.systemPackages` and
`services.spore-fleet.package` reference the same derivation, so one
bump moves both. `spore dream digest` is not in the worker-spawn path,
so the local-binary route is legitimately available to it. That is why
this note uses it.

The two do meet at one seam, and it is worth knowing where. The digest
runs under the local binary and mints a task file; the fleet that reads
that file and spawns a worker for it runs under the store binary, which
may be many commits older. The seam is safe today because the mint
writes only the five frontmatter fields every spore task already
carries (`status`, `slug`, `title`, `created`, `project`) and this arc
changes neither `internal/task/frontmatter` nor `internal/fleet`. If a
later change to the mint adds a field, the store binary is what has to
understand it, and that means an operator rebuild, not a `go build`.

**2. The adversarial reviewer stage does not exist yet.** The proposer
brief is embedded and a reviewer brief is embedded, but no stage runs
the reviewer. Setting `enabled = true` today means a real proposer agent
writes unreviewed proposals. See "When to turn a project on".

**3. `$HOME/.claude/projects` must be readable by the unit.** That is
the session corpus. A user unit inherits it; a system unit does not.

## Cost of one run

Measured on this host 2026-09-03 with a corpus of 476 transcripts
totalling 483 MB:

    real  0m4.1s     user  0m4.0s     sys  0m0.25s

The work is CPU, not disk: `classify` parses every transcript in the
corpus on every run, whatever the watermark says, and only then filters
by project. One run is therefore about 4 seconds of one core, on an
8-core host. Two projects at 03:00 do not contend for anything that
matters.

Disk growth per run is small. A first run over a whole corpus wrote
33,494 bytes; an incremental one-session night wrote 1,449 bytes. There
is no prune yet, so it grows without bound. See the follow-up list in
`nightly-dreaming.md`.

## Step 1: rebuild the local binary

Only from `main`, and only after reading "Before you deploy anything".

    cd <spore checkout>
    git switch main && git pull --ff-only
    nix develop -c just check
    nix develop -c go build -o ~/.local/bin/spore-evolve ./cmd/spore
    ~/.local/bin/spore-evolve dream --help

No sudo and no host rebuild: the hand-installed watch stack runs the
local binary, not the one in the Nix store.

Then confirm you did not break the units you just upgraded. Start each
one that matched the grep above once by hand and read its journal. A
`Type=oneshot` reports its exit status to `systemctl start`, so a broken
upgrade shows up immediately:

    systemctl --user start <some-other-watcher>.service
    journalctl --user -u <some-other-watcher>.service -n 20

**To try an unmerged change, do not rebuild this path.** Build to a
scratch path and run the command by hand:

    nix develop -c go build -o /tmp/spore-probe ./cmd/spore
    /tmp/spore-probe dream digest --project-root <project root> --dry-run

`--dry-run` writes nothing and mints nothing.

## Step 2: create the config, left disabled

Per project, in `~/.config/spore/<project>/watch.toml`, following the
`[releases]` precedent:

    [dreams]
    enabled = false
    deep_read_cap = 5
    max_writes_per_run = 10
    recurrence_threshold = 2

Leave `enabled = false` until you have read "When to turn a project on".
A disabled project is a complete no-op: it prints one line naming this
file and exits 0 without taking the lock or writing anything.

All three numbers may be omitted; the values above are the defaults.
`deep_read_cap` is 5 because the bar for an inferred claim is two
independent sessions, and three deep reads make that count the binding
constraint rather than the evidence.

## Step 3: install the unit

The watcher units on this host are hand-installed plain files under
`~/.config/systemd/user/`, and installing one needs no sudo and no host
rebuild. That is the path documented here because it is the path that
can be verified the same hour it is written. The reproducible
alternative is a Nix module; see the follow-up list in
`nightly-dreaming.md` for why it is not this note.

`~/.config/systemd/user/spore-dream-<project>.service`:

    [Unit]
    Description=Nightly dream [<project>]

    [Service]
    Type=oneshot
    WorkingDirectory=<project root>
    ExecStart=%h/.local/bin/spore-evolve dream digest --project-root <project root>

`~/.config/systemd/user/spore-dream-<project>.timer`:

    [Unit]
    Description=Nightly dream timer [<project>]

    [Timer]
    OnCalendar=*-*-* 03:00:00
    RandomizedDelaySec=15min
    Persistent=false

    [Install]
    WantedBy=timers.target

Then:

    systemctl --user daemon-reload
    systemctl --user enable --now spore-dream-<project>.timer
    systemctl --user list-timers spore-dream-<project>.timer

**Never hand-edit a home-manager-managed unit in place.** Both kinds
live in this directory: home-manager units are symlinks into
`/nix/store` and are read-only, hand-installed units are plain files.
Check before you edit:

    ls -l ~/.config/systemd/user/spore-dream-<project>.service

If that is ever a symlink, the unit has become module-managed and the
edit belongs at the module level.

### Why each field, and what is deliberately absent

`--project-root` must be the **main repo path, never a worktree**. A
worktree is rejected at the mint, exit 1:

    dream-error: spore dream digest: dream: mint: tasks dir
    <root>/.worktrees/probe/tasks belongs to project "probe", not
    "<project>"; a task minted here would be picked up by the wrong
    project's fleet

Note the failure only happens once the run reaches the mint. A
**disabled** project pointed at a worktree prints the disabled line and
exits 0, so a bad `--project-root` can sit unnoticed until the day you
enable the project. Check it while the project is still disabled by
running the command by hand with `--dry-run` from the main repo.

`OnCalendar`, not `OnUnitInactiveSec`. A fixed wall clock time is the
point: `OnUnitInactiveSec` drifts, and every drift moves the run further
from the hour nobody is working.

`RandomizedDelaySec=15min`. The measured digest cost above does not
justify this on its own; 4 seconds of one core on an 8-core host is not
contention. It is here for what happens *after* the digest: the mint
creates a task and the fleet spawns a worker for it about 0.4 seconds
later. N projects firing at the same second means N agents starting at
once, all competing for the same worker slots. That is worth spreading.
Verified in action: with the delay set, `list-timers` showed the next
elapse at `03:00:00` plus the roll, and the service ran at the rolled
time.

`Persistent=false`. A missed night is skipped, not caught up. Two
reasons. First, skipping is free: the window is the watermark, not the
calendar, so the next run digests every session since the last
successful run however many nights that spans. Verified: a run whose
watermark said `since=2026-09-03T02:05:00Z` picked up a session
timestamped `04:05:00Z` on the following run with no gap. Second, a
catch-up run fires at whatever hour the host next boots, mints a
proposer task, and the fleet spawns an agent for it within a second, in
the middle of somebody's working session.

**No `Restart=`.** A failed digest waits for tomorrow. Every retry that
gets as far as the mint produces another proposer task for the same
night, and nothing caps that backlog: consecutive runs each mint a new
task file and no code refuses a second one.

**No path unit, and no trigger other than the timer.** Every digest that
gets past the mint creates a task. Anything that fires on file changes
mints one task per change.

**No `spore-with-secrets` wrapper.** The other watcher units on this
host wrap their `ExecStart` in it because they call GitHub. `dream
digest` reaches no outside system. Verified: the unit above ran to
completion, minted a task, and advanced the watermark with no wrapper
and no secrets in its environment.

**No locking to add.** `digest`, `revert` and `rewind` already take an
flock at `$XDG_STATE_HOME/spore/<project>/dreams/lock`, and a second
caller exits 0 with a warning:

    dream-warn: <project>: another dream run holds the lock:
    <state>/spore/<project>/dreams/lock, so this run did nothing

One oneshot per project would serialise the timer against itself, but
not against a human typing `spore dream digest` while the timer fires,
which is the case the flock covers.

### Optional: sandbox the unit

The unit above matches its hand-installed siblings and runs unsandboxed.
To bound what a mistyped `--project-root` can touch, add:

    ProtectSystem=strict
    ReadWritePaths=%S/spore/<project>
    ReadWritePaths=<project root>

Both write paths must exist before the unit starts, so create them in
this step:

    mkdir -p ${XDG_STATE_HOME:-$HOME/.local/state}/spore/<project>

Getting this wrong fails loudly, which is the only reason it is worth
writing down. Both failure modes were verified on the host.

A `ReadWritePaths` entry that does not exist stops the unit before the
binary runs. `systemctl --user start` exits non-zero and the journal
names the path:

    Failed to set up mount namespacing: <root>/tasks-typo:
      No such file or directory
    Failed at step NAMESPACE spawning <binary>: No such file or directory
    Main process exited, code=exited, status=226/NAMESPACE

A write path that exists but is not listed fails at the first write,
exit 1, with the file named:

    dream-error: spore dream digest: open
      <root>/tasks/dream-20260903-f4d4.md: read-only file system

The run directory is discarded when the mint fails, so a failure like
this leaves nothing behind to clean up.

## Step 4: verify without waiting for 03:00

With the project still disabled, start the service once:

    systemctl --user start spore-dream-<project>.service
    journalctl --user -u spore-dream-<project>.service -n 20

Expected, and verified verbatim on this host:

    Starting Nightly dream [<project>]...
    dream <project>: disabled, nothing to do; set enabled = true under
      [dreams] in <config>/spore/<project>/watch.toml
    Finished Nightly dream [<project>].

That exercises everything the wiring can get wrong: the binary path, the
working directory, the environment, and the write paths. It writes
nothing. If the state directory does not exist beforehand, it still does
not exist afterwards.

If the binary predates the `dream` command, this is what you see, and
`systemctl start` exits non-zero:

    spore: unknown command "dream"
    ...
    Main process exited, code=exited, status=2/INVALIDARGUMENT

That is the ordering constraint from "Before you deploy anything"
enforcing itself. Go back to step 1.

## Reading the journal

One stdout line per run, starting `dream <project> <run-id>:`. A real
line from a real run:

    dream <project> 20260903-29d5: sessions=1/1 digested=1 deep-read=1
    cap=2 digest=402B omitted=0 claims=0 unreadable=0
    task=dream-20260903-29d5 since=2026-09-03T02:05:00Z
    watermark=2026-09-03T04:05:00Z

Anything a human should act on carries a token. `dream-warn:` is a run
that finished with something worth knowing. `dream-error:` is a run that
failed. The grep for a week of journal:

    journalctl --user -u 'spore-dream-*' | grep -E 'dream-(warn|error):'

The tokens are the filter, not the stream. The summary goes to stdout
and the warnings go to stderr, but journald gives both the same
`PRIORITY=6` and the same `_TRANSPORT=stdout`, so no journalctl flag
separates them. Verified by reading the journal in JSON.

**Do not alarm on exit 0.** A quiet night, a disabled project and a
contended lock all exit 0 by design. Only a non-zero exit is a fault:

- exit 0: the run did what it could, including doing nothing.
- exit 1: the run failed. Look for `dream-error:`.
- exit 2: usage error. The unit's `ExecStart` is wrong.

Warnings are capped at 20 per run, followed by a count of the rest, so
one lost corpus cannot bury the summary line.

## Undoing a bad night

**`spore dream revert` is not the undo for a bad night.** A digest run
snapshots nothing, so it records nothing to undo, and reverting one
fails by design:

    dream-error: spore dream revert: no manifest for run 20260903-26bd:
      open <state>/.../20260903-26bd/manifest.json: no such file or
      directory
    nothing was put back. A digest run snapshots nothing, so it records
    nothing to undo; the stage that writes into the harness is what will.

`revert` becomes useful only once the stage that writes into the harness
exists and seals a manifest.

Today's undo is two steps. First stop the proposer: mark the minted task
done, or delete its file from `<project root>/tasks/`. Then put its
sessions back in scope:

    spore dream rewind --project <project>

Verified round trip:

    dream rewind <project>: last 2026-09-03T04:05:00Z ->
    2026-09-03T02:05:00Z; the sessions run 20260903-29d5 consumed are in
    scope again

and the next run reported `since=2026-09-03T02:05:00Z`, so the rewound
night was read again.

`rewind` only goes back one step. Two consecutive unjudged nights lose
the older one for good.

To find a run id without knowing it:

    spore dream runs --project <project>

## When to turn a project on

`enabled = true` points a real proposer agent at a real fleet, and the
fleet spawns it about 0.4 seconds after the mint. There is no
adversarial reviewer stage yet, so what the proposer writes is
unreviewed.

Turn a project on only when all of these hold:

1. The arc is merged to `main` and `~/.local/bin/spore-evolve` was
   rebuilt from `main`.
2. You ran `spore dream digest --project-root <project root> --dry-run`
   by hand and read the summary line.
3. `--project-root` is the main repo, confirmed, not a worktree.
4. You are willing to read the proposer's output yourself, because
   nothing else will.

Then set `enabled = true` in the project's `watch.toml`. No daemon
reload is needed: the config is read at run time.

The first enabled run has no watermark, so it digests the whole corpus
in one go. Do that one by hand and watch it, rather than letting a timer
do it at 03:00.

## Flag names

`--transcripts` is the session corpus, default `$HOME/.claude/projects`.
It is deliberately not called `--projects-root`, because
`--project-root` and `--projects-root` differ by one character and mean
completely different directories: one is a repo, the other is a
transcript store.

## Removing the job

    systemctl --user disable --now spore-dream-<project>.timer
    rm ~/.config/systemd/user/spore-dream-<project>.{service,timer}
    systemctl --user daemon-reload
    systemctl --user list-unit-files 'spore-dream-*'

The last command must list nothing. State under
`$XDG_STATE_HOME/spore/<project>/dreams/` and any minted task files
survive; delete them separately if you want them gone.
