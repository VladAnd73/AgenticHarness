**Status**: phase 1 (dream pipeline) built, tested, and run live against
the on-disk briefs; landed as `internal/evalharness` plus `spore eval`.
Phase 2 (rule pool, coordinator role files, skills) is scoped below,
not built.

# eval harness for spore's own prompt surfaces

## Problem

The 2026-09 Anthropic-prompting audit
(`docs/spore-prompting-audit-2026-09.md`) found concrete gaps in how
spore's own instruction surfaces follow Anthropic's prompting guidance
(`docs/anthropic-prompting-guidance-digest.md`) - missing worked
examples in the dream briefs, an undocumented `evidence_required:`
frontmatter field, orphaned rule fragments, and more. Every one of
those findings implies an edit to a prompt a real agent reads. None of
them had a way to measure whether the edit actually helped, versus
just feeling like an improvement. A prompt change that reads better to
a human and a prompt change that produces better agent behavior are
not the same claim, and this repo had no mechanism to tell them apart.

Anthropic's own test-and-evaluate methodology
(`https://platform.claude.com/docs/en/test-and-evaluate/develop-tests`)
is explicit about this: define success criteria that are specific and
measurable, build a test-case set that mirrors the real task
distribution, grade automatically wherever a structural signal exists
(exact match beats LLM-judge whenever a categorical answer is
checkable in code), and loop test-cases -> prompt -> iterate ->
validate -> ship. Nothing in spore did this for its own prompts before
this task.

## Goal

Eval every prompt surface the 2026-09 audit touched: the dream
pipeline's briefs (`internal/dream/briefs/proposer.md`, `reviewer.md`),
the rule pool (`rules/core/*.md`, composed via
`rules/consumers/spore.txt`), the coordinator role files
(`bootstrap/coordinator/role.md`, `dogfood-role.md`), and the bootstrap
skills (`bootstrap/skills/*/SKILL.md`). For each surface: run a real,
sandboxed Claude Code agent against a fixture scenario with a
known-correct expected behavior, grade the result, and make it
possible to compare a baseline version of the surface's text against a
candidate edit on the same fixtures - so "add worked examples to the
proposer brief" (the audit's finding 1) can be proven a real
improvement before it ships, not just argued for.

## Why phase 1 is dream-pipeline-only

The dream pipeline's two briefs are the one prompt surface in this
repo shaped like a normal eval target: bounded input (a digest.md plus
known-claims.md for the proposer; one packet's JSON plus a target path
for the reviewer) and bounded output (a set of packet files, or one
verdict file). A fixture can hand an agent exactly that input and check
exactly that output. Grading is close to code-exact-match: does
`packets/*.json` contain a DEMONSTRATED-labelled claim or not; does
`verdicts/*.json` say `confirmed` or not.

The rule pool, coordinator role files, and skills are not that shape.
They do not produce one checkable output - they condition a whole,
open-ended agentic session (which tools get called, which files get
touched, how the agent talks to the operator, over many turns with no
fixed stopping point). Evaluating them needs a scripted mini-task run
through a full session and a rubric applied to the resulting
transcript/diff, almost certainly with an LLM-judge for the soft
qualities involved (tone, adherence to a stated non-goal, whether a
boundary got respected under pressure) rather than a single exact-match
check. That is a materially bigger build - see "Phase 2" below - and
building it first, before proving the harness pattern on the cheap
bounded surface, would have meant debugging the sandboxing and grading
approach and the full-session-simulation problem at the same time.
Phase 1 proves the pattern (sandbox a run, spawn a real agent the same
way production does, grade automatically, compare baseline vs
candidate) on the surface where every part of that is checkable in
code; phase 2 reuses the sandboxing and comparison machinery and adds
the session-simulation and judging layer on top.

## What was built

### Package layout

- `internal/evalharness/` - the harness. `sandbox.go`, `spawn.go`,
  `grade.go`, `proposer.go`, `reviewer.go`, `scenario.go`,
  `fixtures.go` (a `go:embed` of the fixtures directory, the same
  pattern `internal/dream/briefs.go` uses for the briefs themselves).
- `internal/evalharness/fixtures/dream/proposer/<name>/session.jsonl` -
  three fixture session transcripts.
- `internal/evalharness/fixtures/dream/reviewer/<name>/{packet.json,repo/...}` -
  two fixture packets, each with a small self-contained fake repo the
  packet's evidence pointers resolve against.
- `internal/evalharness/fixtures/briefs/broken-reviewer-never-confirms/reviewer.md` -
  a deliberately broken reviewer brief, used only to prove the
  `--brief-override` mechanism actually swaps prompt content. See "Two
  broken-reviewer fixtures that didn't work" below for why this is the
  third variant tried, not the first.
- `cmd/spore/eval_cmd.go` - `spore eval list` / `spore eval run`.

### Scenarios (phase 1's five, all passing against the on-disk briefs
after two fixture fixes made from live-run evidence - see "Live run
results" and "Fixture bugs found by running the harness live" below)

| scenario | brief | fixture traces to | expected, graded by |
|---|---|---|---|
| `proposer-demonstrated` | proposer | a worker session where a real test run panics (a real `panic:` in `Failures`) and the session files it as a known, unresolved bug rather than fixing it on the spot | at least one packet cites `DEMONSTRATED` evidence - `dream.LoadPackets` + a scan of `Evidence[].Kind` |
| `proposer-discussed-not-demonstrated` | proposer | proposer.md's own "DISCUSSED is not DEMONSTRATED" section: a session where the *assistant's own planning note* proposes a design rule for a future rewrite, with no operator utterance and no code touched or tested | no packet may cite `DEMONSTRATED` evidence for it - same mechanism, inverted; per proposer.md, "a packet whose evidence is all DISCUSSED does not get written," so the fixture's stronger, more common outcome is zero packets, but the grader also accepts a packet that is honestly labelled `DISCUSSED` |
| `proposer-empty-night` | proposer | proposer.md's "An empty night is a result" | zero packets - `len(dream.LoadPackets(...)) == 0` |
| `reviewer-fabricated-citation` | reviewer | reviewer.md's "Re-derive, never trust" + "A fabricated citation must not survive you" | the reviewer's own verdict file says `refuted` or `unevidenced` - the fixture's evidence claims a retry loop "has no upper bound check," but the fixture repo's `lib/retry.go:10` line, when actually opened, sits inside a loop explicitly bounded by `i < MaxAttempts` |
| `reviewer-real-evidence` | reviewer | reviewer.md's "confirmed requires ... proof ... a third reader could re-run" | the verdict says `confirmed` - the fixture's evidence pointer (`lib/retry.go:4`, "`const MaxAttempts = 3`") is true when read |

Every expected answer above is a clean binary (DEMONSTRATED-or-not,
confirmed-or-not), so every scenario is graded in code, per the
digest's "automate when possible, exact-match beats LLM-judge when a
categorical answer exists" guidance. No LLM-judge was needed for phase
1 - there was always a structural signal to check (a JSON field, a
file's actual content) rather than a soft quality (tone, style,
coherence of reasoning) that only a judge model could assess. Phase 2
will very likely need one; see below.

### Fixture bugs found by running the harness live

Building a fixture from a plausible-sounding scenario description is
not the same as building one that actually exercises the rule it
claims to. Two of the five scenarios failed on their first live run,
against the unmodified on-disk briefs, and in both cases the bug was in
the fixture, not the brief:

- **`proposer-discussed-not-demonstrated`'s first version had the
  operator dictate the claim directly** ("make a note: the retry
  helper should always cap its attempts slice... write that into the
  plan as a design rule"). Per proposer.md's own rule, an operator's
  verbatim, quoted utterance *is* DEMONSTRATED evidence - operator
  messages are things that happened, not things merely discussed - so
  the live proposer correctly wrote a packet citing DEMONSTRATED
  evidence (an `operator-preference` type claim) and the grader
  correctly failed it, because the fixture, not the proposer, had
  confused "no code was touched" with "nothing was demonstrated." The
  fix: the fixture's claim now comes only from the *assistant's own*
  planning note, with no operator utterance asserting it and no code
  touched or tested - the DISCUSSED case the scenario was meant to
  test.
- **`proposer-demonstrated`'s first version had the bug found and fixed
  within the same session** (a real panic, immediately patched, tests
  now passing). The live proposer, applying its own "aim at what a
  future agent would do differently" instruction, correctly judged this
  as nothing left to act on and wrote zero packets - a reasonable
  reading of the brief, not a bug in it, but it meant the fixture never
  exercised the DEMONSTRATED-evidence code path the scenario claims to
  test. The fix: the fixture now files the same panic as a known,
  unresolved bug (out of time before a release cut) rather than fixing
  it in-session, giving a future agent something concrete to act on.

Neither fix touched `proposer.md` or `reviewer.md`. This is recorded
because it is the sharpest evidence in this whole task that live-agent
grading catches things a text-checklist proxy would not: both bugs
were invisible from reading the fixture transcripts and the brief
text side by side, and only showed up once a real agent, reasoning
under the brief's actual rules, disagreed with the fixture author's
assumption about what those rules implied.

### Two broken-reviewer fixtures that didn't work

Proving the `--brief-override` mechanism live (not just via a
unit-tested fake spawner) needs a broken reviewer brief that makes a
live agent's verdict actually flip on a scenario whose baseline passes.
Two attempts failed before a third worked, and the failures are worth
keeping on record because they say something about how robust a
Claude-based reviewer is to a weakened brief:

1. **"Always confirm, don't check anything."** The most direct broken
   brief: skip evidence-checking, always write `confirmed`. The live
   reviewer read this, recognized it as an instruction to fabricate an
   unevidenced confirmation, explicitly said so in its `reason` field,
   and refuted the claim anyway on its own re-derived evidence. A
   diligent model does not silently comply with "stop checking, lie
   about the outcome," even when the instruction arrives inside what
   looks like its own operating brief.
2. **"Trust the packet's description once you've confirmed the cited
   location exists."** A softer, less injection-shaped version:
   legitimate-sounding evidence-checking policy, not an outright "lie"
   instruction. This also failed to flip the verdict - the reviewer,
   in the course of confirming the cited line existed, could not help
   but notice what was actually on it (`lib/retry.go:10` is not even
   the loop bound), and refuted on that basis regardless of the
   relaxed policy. The claim in the `fabricated-citation` fixture is
   false in a way that is visible from a glance at a 15-line file, so
   almost any policy that involves opening the file at all still
   catches it.

The version that worked (`broken-reviewer-never-confirms`, used above)
does not ask the reviewer to accept anything false. It asks for excess
caution instead: verify normally, but write `unevidenced` regardless of
what verification shows, for every packet, as a blanket policy. This
is not an integrity violation, so a good-faith reviewer complies with
it, and it flips `reviewer-real-evidence`'s baseline `confirmed` result
to `unevidenced` - a different, and for this fixture's baseline, wrong
result, which is exactly what the acceptance scenario needs.

The lesson for phase 2 and for grading real brief candidates in
general: do not assume a broken-brief fixture "proves the mechanism
works" just because it reads as broken to a human. Whether a live
agent actually complies with a given brief change depends on whether
that change asks the agent to assert something false (resisted) or
merely to be less useful / more conservative (usually complied with).
A future worker evaluating a real candidate brief edit should expect
the same asymmetry: regressions that make the brief *say something
false* are hard to get an agent to enact even when told to; regressions
that make the brief *do less checking, more cautiously* are not.

### Reusing production code, not reinventing it

`BuildProposerRun` does not hand-build a `digest.md`. It writes the
fixture transcript into a fake session corpus and calls
`dream.Run(dream.Options{...})` - the real `Discover` -> `BuildDigest`
-> `renderDigest` -> `MintTask` pipeline production runs every night.
The minted task file's body (which is `dream.ProposerBrief` verbatim
plus the run's digest/known-claims paths, exactly what
`internal/task/lifecycle.go` would hand a real worker as its first
message) becomes the agent's prompt. This is what "exercise the same
invocation shape, not an invented one" means concretely: if
`dream.Run`'s digest rendering ever changes, this harness's prompts
change with it automatically, because it is the same code path.

`BuildReviewerRun` reconstructs the exact three things proposer.md's
own reviewer-spawn step specifies: "the reviewer brief text, the one
packet's JSON, and the packet's target path. Nothing else." (See
"Known approximation" below for the one place this could not be
literal.)

### Why headless `claude -p` instead of spore's tmux+worktree machinery

The task brief that started this work suspected reusing
`internal/task/lifecycle.go` / `internal/fleet/coordinator.go`'s
`git worktree add` + `tmux new-session` machinery was the wrong call
for an eval harness (built for long-lived, human-observed sessions;
adds real overhead per run and leaves stray state behind on a crash)
and guessed headless print mode was the right substitute - on the
condition that headless mode actually preserves the behavior that
matters (does it load the same skills, hooks, and settings a real
interactive session would?).

Checked, not assumed: `claude --help` documents `-p`/`--print` as
"start[ing] an interactive session by default, use -p/--print for
non-interactive output" - a change in *interactivity*, not in what
loads. The one flag that strips hooks, skills, plugin sync, and
CLAUDE.md auto-discovery is `--bare` ("Minimal mode: skip hooks, LSP,
plugin sync, attribution, auto-memory... Skills still resolve via
/skill-name"), which this harness never passes. Separately,
`internal/task/lifecycle.go`'s own comment confirms production's real
spawn shape: a worker task's file body becomes "the agent's first
positional arg" so "a default claude-code agent boots with the role as
its first user message" - which is exactly what
`exec.Command("claude", "-p", "--permission-mode", "bypassPermissions", prompt)`
does, just without a TTY. Headless, non-bare `-p` is a faithful match
to production's spawn shape, not an invented one; this is documented
here rather than asserted in code comments so a later worker can verify
the same way against whatever `claude --help` says by the time they
read this.

`--permission-mode bypassPermissions` is required because an
unattended eval run has no human to answer a permission prompt; every
scenario runs inside a `Sandbox` with no remote and no push-capable
token, which is what makes bypassing safe there specifically. Do not
copy this flag into a context that is not similarly sandboxed.

### Sandboxing (verification, not assertion)

Every scenario run gets a fresh `Sandbox`
(`internal/evalharness/sandbox.go`): a throwaway temp directory,
`git init`'d with no remote ever added, its own `dream.Options.Home`
and `ProjectsRoot` (so `Discover`'s worker-path matching never sees a
real project), its own `TasksDir` (so `dream.MintTask` writes to a
scratch tasks queue no live fleet is watching), and its own
`XDG_STATE_HOME` (so `dream`'s ledger and watermark - which
`internal/statefile` resolves straight from that environment variable,
with no `Options` field to override it - land in the sandbox, never in
this host's real `~/.local/state/spore/<project>/`). `Sandbox.Env()`
also strips `GH_TOKEN` and `GITHUB_TOKEN` from the spawned process's
environment (belt-and-suspenders: the sandbox also has no remote to
push to).

Verified, not just built: `TestNewSandboxCreatesAGitRepoWithNoRemote`
and `TestSandboxEnvExcludesGitHubTokensAndSetsIsolatedState`
(`internal/evalharness/sandbox_test.go`) assert this in code, and the
five live runs recorded below were bracketed with `git status` and
`git log -1` on this repo's real checkout before and after - unchanged
in both cases (see "Live run results"). One real leak happened during
development: an early version of `BuildProposerRun` called
`dream.Run` without first setting `XDG_STATE_HOME`, and it wrote a
real (if distinctly-named, non-colliding) `evalproj` directory under
this host's actual `~/.local/state/spore/`. Caught by inspection, not
by a test, before this was pushed; the fix (`os.Setenv` at the top of
`BuildProposerRun`, see the comment there) is what
`TestSandboxEnvExcludesGitHubTokensAndSetsIsolatedState` and the
`BuildProposerRun` tests now guard. Recorded here because it is the
concrete shape of the mistake this rule ("sandboxing is non-negotiable,
verify the mechanism") exists to catch.

### Baseline vs candidate

`RunOptions.ProposerBriefOverride` / `ReviewerBriefOverride` (wired to
`spore eval run <scenario> --brief-override <path>` on the CLI) replace
the embedded `dream.ProposerBrief` / `dream.ReviewerBrief` text inside
the minted prompt before it is sent to the agent. `BuildProposerRun`
proves the swap is real, not cosmetic, by string-matching the exact
embedded brief text out of the minted task body and replacing it - if
that prefix match fails (i.e. `dream.MintTask`'s body shape ever
changes and no longer starts with the brief verbatim), the call errors
loudly rather than silently sending the override "alongside" the
original.

This is unit-tested without a live agent
(`TestBuildProposerRunHonoursAnOverrideBriefInsteadOfTheEmbeddedOne`,
`TestRunScenarioSendsTheOverrideProposerBriefInsteadOfTheEmbeddedOne`,
and the reviewer equivalents): a fake spawner captures the exact prompt
text sent and asserts the override text is present and the original
brief text is absent. It is also exercised live in this build: see
"Live run results," `reviewer-real-evidence` run against
`fixtures/briefs/broken-reviewer-never-confirms/reviewer.md`.

A follow-up task will use this exact mechanism (same fixtures, same
`--brief-override` flag) to prove the audit's "add worked examples to
the proposer brief" finding is a real improvement before it ships -
not this task's job; the mechanism it needs already exists.

### Grading

`grade.go`'s `GradeProposerRun` / `GradeReviewerRun` read
`packets/*.json` / `verdicts/*.json` off disk via `dream.LoadPackets`
and a small verdict loader, and compare against one of five expected
shapes (`ExpectDemonstrated`, `ExpectNotDemonstrated`,
`ExpectNoPackets`, `ExpectApprove`, `ExpectRefuse`). All five are
exact-match checks over structured JSON fields the real dream code
already parses - no free-text comparison, no LLM-judge, per the
digest's stated preference for code grading wherever a categorical
signal exists.

### CLI

```
spore eval list
spore eval run <scenario> [--brief-override PATH] [--sandbox-parent DIR]
spore eval run --all [--sandbox-parent DIR]
```

Exit code is non-zero if any scenario fails or errors, so this
composes into a CI-style gate later without extra plumbing. Mirrors
`cmd/spore/dream_cmd.go`'s hand-rolled `flag.FlagSet` dispatch pattern
(this repo has no cobra dependency; `dream`, `task`, `fleet` etc. all
do it this way), including the `out, errOut io.Writer` injection that
makes it unit-testable without touching a real terminal.

### Test layers

Two, deliberately not one:

1. **Fast, deterministic, free** - `internal/evalharness/*_test.go` and
   `cmd/spore/eval_cmd_test.go` inject a fake `AgentSpawner` that never
   starts a real process. These run as part of `go test ./...` /
   `just check` on every change, same as any other Go test, and prove
   the orchestration (sandboxing, prompt construction, override
   swapping, grading logic) without API cost or non-determinism.
2. **Live, real-agent, not part of routine `just check`** - built and
   run manually for this task (see "Live run results" below) via
   `go build -o spore-eval-bin ./cmd/spore && spore-eval-bin eval run
   <scenario>`, which spawns a real `claude -p` process per scenario.
   This is the layer that actually answers "do the on-disk briefs pass
   their own eval," and it is what the acceptance scenarios in this
   task's brief required running at least once. It is not wired into
   `just check` because a live LLM call is slow, costs real API spend,
   and is not perfectly deterministic - the same tradeoff that keeps
   most eval suites out of a fast unit-test gate industry-wide. A
   later task can wire `spore eval run --all` into a slower,
   less-frequent CI gate (nightly, or on brief changes specifically) if
   that tradeoff is wanted; this task did not make that call.

## Live run results

Run with `go build -o spore-eval-bin ./cmd/spore` on this branch, then
`spore-eval-bin eval run <scenario> --sandbox-parent <dir>` for each of
the five phase-1 scenarios plus one override run, against the on-disk
`internal/dream/briefs/proposer.md` / `reviewer.md` (unmodified - this
task does not edit them). All six ran to completion as real,
unattended `claude -p` processes, no mocked agent output.

| scenario | result | reason |
|---|---|---|
| `proposer-demonstrated` | PASS | at least one packet cites DEMONSTRATED evidence |
| `proposer-discussed-not-demonstrated` | PASS | no packet wrongly cites DEMONSTRATED evidence |
| `proposer-empty-night` | PASS | no packets were written |
| `reviewer-fabricated-citation` | PASS | verdict=refuted: the cited line's actual content contradicts the packet's claim, and `MaxAttempts` is a fixed const that can never be the zero value the claim needs |
| `reviewer-real-evidence` | PASS | verdict=confirmed |
| `reviewer-real-evidence` with `--brief-override fixtures/briefs/broken-reviewer-never-confirms/reviewer.md` | FAIL (expected, and correctly so) | expected confirmed, got "unevidenced": the reviewer's own reason states it verified the claim and it held up, but wrote unevidenced anyway per the override brief's blanket policy - direct evidence the swapped brief text changed real behavior, not just a label |

5/5 registered scenarios pass against the current on-disk briefs. The
sixth row is not a registered scenario; it is the override mechanism's
own proof, and an intentional FAIL is the correct, expected outcome
there - see "Two broken-reviewer fixtures that didn't work" above for
why the first two override attempts did not produce this result and
what that says about how hard it is to get a diligent reviewer model to
assert something false versus just be more conservative.

Two of the five scenarios failed on their first live run and were
fixed by correcting the fixture, not the brief - see "Fixture bugs
found by running the harness live" above. Sandboxing was verified
before and after every run in this section: `git status --porcelain`
and `git log --oneline -1` on this worktree's real checkout were
unchanged throughout (still the pre-existing uncommitted diff this
task itself made, no new commits, no branches, no pushes) - see
"Sandboxing" above for the mechanism, not just this assertion.

## Known approximation

Production spawns the reviewer as a Claude-Code Agent-tool subagent
*from within* the proposer's own already-running session - an
in-process call with a genuinely fresh, isolated context, not a new
OS-level `claude` invocation. This harness cannot reach that mechanism
from outside a running session (there is no CLI verb for "spawn a
subagent and hand me back its answer"), so `BuildReviewerRun` spawns a
second top-level `claude -p` process instead, handed the exact same
three things (brief text, packet JSON, target path) the real subagent
would receive. The prompt content is identical to production's; the
process boundary it runs behind is not. This is judged close enough
for phase 1 because the reviewer brief's own instructions are entirely
about what to do with those three inputs and never depend on being a
subagent specifically (no reference to a parent session, a shared
context, or anything only a true subagent would have). Flag this if
phase 2 or a future revision of proposer.md/reviewer.md ever makes that
assumption load-bearing.

## Phase 2 (not built): rule pool, coordinator role files, skills

Scoped here, not attempted, per this task's brief.

What phase 2 needs that phase 1 did not:

- **A task/session simulator, not a single-brief spawn.** The rule
  pool and coordinator role files condition an entire agentic session
  across many turns and tool calls, not one bounded input/output. An
  eval scenario here looks like "give a worker this small, realistic
  task; let it run to completion in a real sandboxed worktree; capture
  the transcript and the diff." That is closer to what
  `internal/task/lifecycle.go`'s tmux+worktree machinery already does
  for production - phase 2 may be the place that machinery actually
  belongs, unlike phase 1.
- **A rubric per scenario, not a single field to check.** "Did the
  agent respect the coordinator's stated non-goal under pressure to
  act" or "did the agent use `AskUserQuestion` instead of a free-form
  question for an enumerated choice" are not JSON fields anywhere; they
  have to be read out of a transcript.
- **An LLM-judge, chosen deliberately.** Per the digest's explicit
  guidance to use a different model to evaluate than the one that
  generated the output being evaluated, and per this task's own
  instruction to check, not assume, what is actually available: this
  host's `claude` CLI accepts `--model <alias-or-full-name>` (aliases
  seen in `claude --help`: `fable`, `opus`, `sonnet`; this session runs
  as Sonnet 5) - a phase-2 harness generating scenario transcripts with
  one model and judging them with a different one (e.g. generate with
  Sonnet, judge with Opus, or vice versa) is a one-flag change on top
  of this phase's `ClaudeSpawner`, not a new spawn mechanism.
- **Skills specifically** may not need the full session simulator:
  `bootstrap/skills/*/SKILL.md` files are closer to proposer.md /
  reviewer.md in shape (a single document read at one decision point)
  than to the coordinator role files (which condition a whole
  session), so a skill-specific fixture might reuse more of phase 1's
  bounded-input/output pattern than the rule-pool/role-file surfaces
  will. Worth re-checking against each specific skill before assuming
  either shape.
- **What "pass" means for a rule-pool change is genuinely unclear
  yet.** A dream-brief scenario has one unambiguous correct verdict.
  "This rule-pool fragment is well-written" does not obviously reduce
  to one. Phase 2's first real subtask is probably defining what a
  passing rule-pool scenario even looks like, on a small number of the
  audit's own findings (the orphaned-fragment finding, the
  `ask-via-tool` scoping question) before building anything.

None of this was attempted here; it is recorded so a later worker does
not have to re-derive it from this task's brief and the audit doc from
scratch.
