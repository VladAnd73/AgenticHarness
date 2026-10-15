# Claude Code upgrade smoke check

`scripts/cc-upgrade-smoke.sh` answers one question: after upgrading the
host's Claude Code, does spore's hook surface still work?

The question matters because spore depends on parts of Claude Code that
Claude Code never promised to keep stable: the names of hook events, the
field names inside a hook's JSON reply, the layout of the transcript
file, and the names of the tools we deny. If any of those is renamed,
nothing errors. The hook keeps running, keeps exiting 0, and quietly
stops having an effect. You find out weeks later, from a symptom that
looks unrelated.

## Running it

```
scripts/cc-upgrade-smoke.sh
```

It takes a few seconds, writes nothing outside a temporary directory it
deletes on exit, and is safe to run at any time, from any session, as
often as you like. It does not wake the coordinator and does not spawn
workers.

Exit code 0 means every check reported `OK`. Exit code 1 means at least
one reported `CHANGED`.

Around an upgrade, capture both sides and compare:

```
scripts/cc-upgrade-smoke.sh > /tmp/before.txt
# operator runs the upgrade (on this host, a nixos-rebuild)
scripts/cc-upgrade-smoke.sh > /tmp/after.txt
diff /tmp/before.txt /tmp/after.txt
```

The diff is the more useful artifact. The exit code only tells you that
something is wrong today; the diff tells you what the upgrade changed,
including values that moved without crossing a threshold.

Lines that are expected to differ between any two runs, upgrade or not:
the `using` transcript filename, the `payload size` byte count, the
`duration` milliseconds, and the `bundle` path (which contains the
Claude Code version). Everything else should be identical.

## Reading a CHANGED line

Every verdict line has the same shape:

```
  additionalContext present          yes  OK
  permissionDecision                 allow  WANT deny  <-- CHANGED
```

The label names the thing measured, then the value observed, then (when
they disagree) the value expected. A `CHANGED` line is not proof of a
bug by itself. It is a claim that something spore relies on no longer
holds, and it needs one of three answers:

1. **The upgrade changed it.** Search the Claude Code release notes
   between your two versions for the name in the label. A rename or a
   removal there is your answer, and spore needs a fix.
2. **The host changed it.** Someone edited `~/.claude/settings.json` or
   a hook script. Check 7 in particular reads settings that a human
   edits.
3. **The check is running in the wrong shape.** See the traps below.

## What each check proves

| # | Check | Mechanism it exercises |
|---|---|---|
| 1 | transcript parsing | `spore worker token-monitor` reads a real token count out of a real `.jsonl` |
| 2 | SessionStart loader | `load-state-md.pl` emits `hookSpecificOutput.additionalContext` |
| 3 | PreToolUse guard | `block-bg-bash.pl` answers `permissionDecision: deny` for a background Bash call |
| 4 | inbox wake | `spore hooks watch-inbox` exits 2 when an envelope is queued |
| 5 | coordinator poke | `spore hooks notify-coordinator` writes a poke envelope |
| 6 | fleet replenish | `spore fleet replenish-hook` completes a reconcile pass and returns promptly |
| 7 | messaging guard | `~/.claude/settings.json` still denies `SendMessage` and `ListAgents` |
| 8 | contract names | the installed Claude Code bundle still carries every name our hooks speak |

Check 7 asks whether those two names are in the deny list, not whether
the deny list is exactly those two names. Any other permission rules you
keep there are your business and will not turn the run red.

Check 1 is a pair, and the pair is what makes it a test. A cap of one
token forces the monitor to fire (exit 2), which can only happen if it
parsed a count of at least one. A huge cap must leave it silent (exit
0), which can only happen if it parsed a finite count rather than
erroring. Either result alone would be weak; together they pin the
parse.

Check 8 exists because checks 2, 3 and 7 can only see spore's half of
each contract. They prove our hook still emits `permissionDecision`, or
that our settings still name `SendMessage`. None of them can see Claude
Code renaming that field or that tool on its side, and that rename is
exactly the failure that would pass silently. Check 8 reads the other
half, by searching the installed Claude Code bundle for each name.

Check 8's limit, stated plainly: finding a string proves the name still
exists somewhere in the product. It does not prove the field still
means what it meant. It catches a rename, which is the failure mode we
have actually seen coming; it does not catch a change in semantics.

## Two traps that make a naive version prove nothing

Both were found by running the earlier version of this script and
getting a green that was not real. The script now works around both,
and the workarounds are the reason some checks look more elaborate than
the thing they test.

**The token monitor skips most sessions.** `spore worker token-monitor`
exits 0, silently, for any session with no `$SPORE_TASK_INBOX`, and for
any session whose inbox sits under the coordinator state directory
(`internal/worker/tokenmonitor`, `Config.IsCoordinator`). Invoked
without thought it returns a healthy-looking 0 having done nothing.
Check 1 points it at a throwaway worker-shaped inbox so neither guard
fires.

**The replenish hook no-ops outside a coordinator session.**
`spore fleet replenish-hook` returns immediately unless
`$SPORE_TASK_INBOX` sits under `$SPORE_COORDINATOR_STATE_DIR`
(`isCoordinatorSession` in `cmd/spore/fleet_cmd.go`). Run from a worker
it is fast and silent because it short-circuits at the first guard, not
because the reconcile pass is healthy. Check 6 gives it a throwaway
coordinator shape and an empty task queue, then requires the
`spawned=` summary line that only a real reconcile pass prints.

A third trap applies if you ever point this at a live inbox: a poke
moves through `inbox/`, `inbox/.pending/` and `inbox/read/` as the ack
protocol carries it, so counting any single level undercounts. Check 5
counts the whole tree.

## Overriding the host paths

Every path the script reads is overridable, which is how the test suite
runs it against a sandbox of stub binaries instead of the live machine.

| Variable | Default |
|---|---|
| `SMOKE_SPORE_BIN` | `/run/current-system/sw/bin/spore` |
| `SMOKE_CLAUDE_BIN` | whatever `claude` resolves to on `PATH` |
| `SMOKE_HOOKS_DIR` | `$HOME/.claude/hooks` |
| `SMOKE_SETTINGS` | `$HOME/.claude/settings.json` |
| `SMOKE_PROJECTS_DIR` | `$HOME/.claude/projects` |
| `SMOKE_PROJECT` | `spore` |
| `SMOKE_COORDINATOR_INBOX` | `$HOME/.local/state/spore/coordinator/$SMOKE_PROJECT/inbox` |

`SMOKE_COORDINATOR_INBOX` is only reported on, never written to. The
poke in check 5 goes to a throwaway tree.

The script needs nothing beyond bash, coreutils and grep. It reads JSON
with a small grep helper rather than a real parser, because the test
that drives it runs inside the nix build sandbox, which has no python3.
The payloads involved are small and of a shape we control.

## Why this is one script, not a spore subcommand

Everything it reads is host-level: the `claude` binary, `~/.claude/`,
the `spore` binary on the system path. None of it is per-project, so a
single copy here covers every consumer project on the host and nothing
needs embedding into the spore binary or installing into a consumer's
tree. It is an operator tool run by hand around an upgrade, not a thing
any agent calls.

## Its own tests

`scripts_smoke_test.go` builds a complete fake host in a temporary
directory (stub `spore`, stub Claude Code bundle, stub hooks, stub
settings) and runs the script against it. One test confirms a healthy
sandbox exits 0 with no `CHANGED` line. Seven more each break exactly
one mechanism and confirm the script both prints `CHANGED` and exits
non-zero. One confirms the opposite direction: an unrelated deny rule in
the stub settings must leave the run green. A last one patches an extra
line into the script's header comment and confirms `--help` still prints
all of it. Nothing in the suite touches the real host state.
