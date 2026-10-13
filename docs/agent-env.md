# Per-project agent environment (`agent.env`)

A project can give its agents environment variables that the rest of
the host does not get. spore reads one file at spawn time and seeds it
into the tmux session it creates:

```
${XDG_CONFIG_HOME:-$HOME/.config}/spore/<project>/agent.env
```

`<project>` is `task.ProjectName`, which resolves a worktree back to
its main repository, so every worktree of one project shares one file.
Projects without the file behave exactly as before: no extra `-e`
argument, no output.

Both spawn routes are covered, and they are the only two: the
coordinator (`internal/fleet.EnsureCoordinator`) and the worker
(`internal/task.ensureSession`). `spore task start` and
`spore fleet reconcile` (including the systemd timer and path units)
both funnel through those, so nothing per-unit needs changing.

## What it is for

Anything that must differ per project rather than per host. The case
that drove it: an agent's Claude account, selected by
`CLAUDE_CODE_OAUTH_TOKEN`, which overrides the logged-in account even
under the default config directory.

Switching the account alone is not enough. The budget tracker keeps one
host-wide `state.json` under `$AGENT_BUDGET_STATE_DIR` (default
`~/.local/state/agent-budget`) holding a single usage snapshot with no
account field. Two accounts sharing it means both projects read the
wrong account's remaining quota, so a project that moves its account
should move `AGENT_BUDGET_STATE_DIR` in the same file.

## Why not `secrets.env`

`~/.config/spore/<project>/secrets.env` already exists, but it is read
only by commands wrapped in `spore-with-secrets`. Those secrets reach a
single command's environment and no further. Seeding that file at spawn
would instead put every credential a project owns into every agent
pane's environment for the pane's whole life, and into every child
process the agent spawns. `agent.env` keeps the two scopes apart: one
file for "a wrapped command may read this", one for "every agent in
this project runs with this".

Nothing stops an operator symlinking one to the other, and nothing
tries to. The split is a default, not an enforcement.

## Format

`KEY=VALUE`, one per line. `#` comments and blank lines are skipped.
Keys match `[A-Za-z_][A-Za-z0-9_]*`.

No shell is involved, so **nothing expands and nothing unescapes**. A
value reaches the agent byte for byte: `$HOME` stays the four
characters `$HOME`, and a backslash stays a backslash.

Quoting:

- A value wrapped in matching single or double quotes keeps its
  interior exactly, including leading and trailing spaces.
- An unquoted value is trimmed of trailing whitespace, because trailing
  spaces in a pasted token are invisible.
- An **unmatched** opening quote is an error, not a literal quote.

That last rule is the point of the whole format. A token carrying a
stray quote character is still a plausible-looking string, so it is
accepted everywhere up to the moment it fails to authenticate, and
nothing in the failure points back at the file. The format refuses to
produce a value the operator cannot see is wrong.

`export KEY=VALUE` is not accepted. It is a malformed line, and it
fails loudly rather than being quietly half-understood.

## How failures behave

Three different problems, three different responses, each chosen by
what the operator is left with.

**A malformed line refuses the spawn.** The file cannot be understood,
so spore cannot know whether the variable the operator cared about was
set. Skipping the line and carrying on would start the agent on the
default account - exactly the outcome the file exists to prevent - and
it would look like it worked.

Be clear about how visible that refusal actually is. Run by hand, it is
immediate: the command fails and prints the path and line number. Under
the `spore-fleet-reconcile` systemd timer it is not. The pass runs
headless every 60 seconds, the message goes to journald, and the unit
goes to `failed` - which on a host that already has units sitting in
`failed` unnoticed is not a channel anyone reads. So a refusal under the
timer means the affected agents quietly never spawn. What the refusal
does guarantee is that none of them comes up on the wrong identity.

That is also why the reconciler does not stop at a refusal. A coordinator
that will not spawn is reported and the pass carries on: matter sync, the
inbox watchdog, the reap loop and the worker pass do not depend on the
coordinator session, and letting one project's typo cancel all of that
every 60 seconds costs far more than it protects. The error is still
returned to the caller afterwards, so the unit still fails exactly as it
did before.

**A reserved key is dropped and the spawn continues.** `SPORE_*` and
`WT_*` are spore's own namespace; every variable either spawn site sets
carries one of those prefixes. Honouring an override would point an
agent at another project's root, inbox or state. Dropping it is already
the correct outcome, and spore's own value is the one in the session,
so there is nothing to gain by also refusing to start. The message
names the key and the line number.

**A mode looser than 0600 warns and the spawn continues.** Refusing
here would buy no security: the file is already readable at that mode
whether or not spore opens it, so a refusal protects nothing while
taking down the coordinator and every worker. The operator's recovery
under pressure would be to delete `agent.env` and lose the feature. A
warning naming the path and the mode gets the `chmod` done without that
trade. Note that the sibling `secrets.env` files on a typical host sit
at 0650, so this warning is expected to fire the first time.

**No message, warning or error ever contains a value.** They carry the
key and the line number only. This is a hard rule, not a courtesy: the
file holds credentials and these lines reach stderr, journald and the
operator's scrollback.

## What a seeded value is exposed to

Measured on tmux 3.7b:

- `/proc/<pane_pid>/environ` is mode 0400, owner only. This is the
  normal, intended exposure.
- `tmux show-environment -t <session>` prints the value to anything
  that can open the tmux socket. The socket is 0770 owner:group, so
  this is same-user plus that group.
- `tmux list-sessions` reveals nothing.
- `ps` normally reveals nothing. The `tmux` client that carries
  `-e KEY=value` on its argv exits within milliseconds.

The exception worth knowing: when the spawn is what **starts** the tmux
server, the server process inherits that client argv and keeps it in
its world-readable `/proc/<pid>/cmdline` for as long as the server
runs, which is typically days. Any user on the host can read it. Once
the server has at least one session this stops happening, so in
practice it only applies to the very first spawn on a host with no tmux
server at all. tmux offers no way to pass a session variable without
putting it on a command line, so the mitigation is to make sure a tmux
server is already running before the first seeded spawn.
