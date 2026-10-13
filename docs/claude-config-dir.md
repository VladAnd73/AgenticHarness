# Per-project Claude config directory

A project can point its agents at their own Claude Code configuration
directory instead of the host-wide `~/.claude`:

```toml
# spore.toml
[claude]
config_dir = "~/.claude-assistant"
```

Nothing else changes. A project that omits the key resolves `~/.claude`
at every call site, exactly as it did before the key existed.

## What the setting does

Two things, and only two.

**It seeds the session environment.** Both spawn sites pass
`CLAUDE_CONFIG_DIR=<dir>` to `tmux new-session` -- the worker site in
`internal/task/lifecycle.go` and the coordinator site in
`internal/fleet/coordinator.go`. Claude Code reads that variable
itself, so the agent keeps its whole state tree there. The host shims
(`spore-coordinator-launch`, `spore-worker-brief`) `exec` their agent
without touching the environment, so the seed survives them.

**It answers "where does this project's agent keep its state?"** for
every part of spore that reads that tree:

| Call site | What it reads |
| --- | --- |
| `internal/fleet/inboxwatchdog.go` | the coordinator's transcript, as a liveness probe |
| `internal/coordinator/verify/session.go` | the worker's transcript, as done-gate evidence |
| `internal/transcript/transcript.go` | the transcript a token-monitor hook fell back to |
| `cmd/spore/dream_cmd.go` | the session corpus a nightly digest reads |
| `internal/budget/budget.go` | the transcript corpus the spend estimate walks |
| `internal/budget/usage.go` | `.credentials.json` for the OAuth usage call |

All six go through `internal/claudecfg`.

## Why the project decides, not the process

The obvious resolver reads `CLAUDE_CONFIG_DIR` from its own
environment. That is wrong for the processes that matter most, because
they are judging somebody else's agent:

- `spore-fleet-tick` walks every project under `$HOME` one level deep
  and runs `spore fleet reconcile` in each, from a single shell with a
  single environment. One answer would reach every project it visited.
- The per-project systemd units share one `Environment=` template. The
  nix module's `extraEnv` is fleet-wide, so there is no way to give one
  project its own value there even deliberately.
- `spore-dream-<project>.service` runs with an empty `Environment=`.
- An operator typing `spore coordinator verify-done` is in whatever
  shell they happen to be in.

So resolution order is: the project's `[claude] config_dir`, then
`CLAUDE_CONFIG_DIR` from the process, then `~/.claude`. The project
setting first is what makes two processes agree about the same project
no matter which of them is asking. The env still decides for a project
that configures nothing, which is the behaviour the inbox watchdog
already shipped.

For the call sites that run inside an agent and hold no project root,
`claudecfg.ProjectRoot` recovers one from `SPORE_PROJECT_ROOT`, then
`CLAUDE_PROJECT_DIR`, then the working directory, collapsing a worktree
path to the main checkout so the project's `spore.toml` is the one that
gets read.

## Why a new `[claude]` section

Older spore binaries error on an unrecognised key inside
`[coordinator]`, discard the whole section, and silently strip that
project's `driver`, `model` and `brief`. Every TOML parser in the
kernel, old and new, skips a section header it does not recognise. A
new top-level section therefore costs nothing on a host running mixed
versions; a new key under `[coordinator]` would not.

Measured against the pre-warning parser, with a `brief` set alongside:

```
new-section    err=<nil>                                     brief="role.md"
new-coord-key  err=line 3: unknown key "config_dir" ...      brief=""
```

An unknown key inside `[claude]` is a warning on stderr and nothing
more, for the same reason.

## What does not follow the agent

Moving a project to a fresh directory moves the agent's state, not the
operator's setup. A new directory starts empty, and empty means these
are gone for that project until something puts them there:

- **Hooks.** Every spore hook on an infected host is registered in
  `~/.claude/settings.json`: the `SessionStart` state-md loader, the
  `PreToolUse` background-bash block, the `Stop` chain that runs the
  token monitors and the inbox watcher, and the `Notification` hook
  that pokes the coordinator. A project whose agents read a different
  settings file runs none of them. The coordinator stops draining its
  inbox.
- **Credentials.** `.credentials.json` lives in the config directory.
  A fresh one has no token, and getting one is an interactive login.
- **Global instructions, skills, agents, plugins.** `CLAUDE.md`,
  `skills/`, `agents/` and `plugins/` are all per-config-directory.
  Symlinking `plugins/` is destructive: the plugin system writes
  orphan-GC markers that a sweeper then acts on host-wide.
- **Project memory.** It lives at
  `<config-dir>/projects/<encoded-root>/memory/`, so it moves with the
  directory and the new one starts with none.
- **Trust state and permission allowlists.** Per-config-directory, so a
  fresh directory re-prompts.

Account-scoped things do survive, because they are not in the directory
at all: claude.ai connectors, and anything keyed on the Anthropic
account rather than the local install.

`spore infect` installs hooks and `settings.json` to a hard-coded
`/home/spore/.claude`. That is correct for host bootstrap, which runs
before any project exists, but it means the per-project directory is
the operator's to populate.

`spore budget` aggregates spend for the whole host from one transcript
corpus. With more than one config directory in play it sees only the
one its own project resolves, and under-counts the rest.
