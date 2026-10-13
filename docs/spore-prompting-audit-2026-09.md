# Spore prompting audit (2026-09)

This audits spore's own instruction surfaces (rule pool, role files, skills,
recipes, bootstrap runbooks, the dream pipeline's agent briefs) against the
verified guidance in `docs/anthropic-prompting-guidance-digest.md` (four
Anthropic platform-docs pages: general prompting best practices, reducing
hallucinations, increasing output consistency, reducing prompt leak). The
digest is a faithful extraction, not an opinion; this document is the
opinion, and every finding below quotes both the current spore text and the
digest guidance it relates to. Goal: reduce hallucination and improve
results across the whole spore solution. This is a report only - no rule
files, role files, skills, or recipes were changed to produce it.

## Verdict

1. **The dream pipeline's briefs (`internal/dream/briefs/proposer.md`,
   `reviewer.md`) are the strongest hallucination-defense prompts in the
   repo** - the DISCUSSED-vs-DEMONSTRATED distinction, the default-to-refuse
   reviewer stance, and "evidence is pointers, not quotes" all instantiate
   (and in places exceed) the digest's Page 2 techniques. Their one real gap
   against the digest: no worked examples, despite few-shot examples being
   the single most load-bearing technique on Page 1 and the pipeline's own
   trigger incident being exactly a classification error a labeled
   good/bad packet pair would calibrate against.
2. **The evidence contract (`evidence/verify.go`, the `task-evidence` lint)
   is a code-enforced anti-hallucination mechanism with no analog in the
   digest** (the digest only covers prompt-level techniques) **- and the one
   skill that teaches coordinators to write task briefs
   (`writing-a-spore-worker-brief`) never mentions the
   `evidence_required:` frontmatter field that switches it on.** The
   mechanism exists and is stronger than anything prompting alone can do,
   but a coordinator who only reads that skill will not know to declare it.
3. **Claude Code's own harness-level system prompt already implements the
   digest's "Balancing autonomy and safety" sample block near verbatim**
   (the "Executing actions with care" section every session receives), so
   spore's rule pool correctly does not duplicate it - this is a case where
   the guidance transfers, but at the platform layer, not the project layer.
4. **Three rule-pool fragments are orphaned**: `rules/core/no-emdash.md`,
   `rules/core/commit-no-attrib.md`, and `rules/core/ask-via-tool.md` exist
   on disk but are not listed in `rules/consumers/spore.txt`, so they never
   reach a rendered `CLAUDE.md`/`AGENTS.md`. Their content happens to be
   duplicated elsewhere (`writing-style.md`, `alignment-mode.md`) so nothing
   is functionally missing today, but it is exactly the kind of silent drift
   the digest's consistency guidance (Page 3) warns a maintained prompt
   should not accumulate.
5. **The worker-brief-writing skill's "name a belief and tell the worker to
   attack it" pattern is a battle-tested, domain-specific analog to the
   digest's adversarial/self-check techniques** (Page 1's "ask Claude to
   self-check," Page 2's chain-of-thought verification) but the skill itself
   has no worked example of a full brief, only a field-by-field template and
   a mistakes table - a second real gap in the same "no few-shot examples"
   shape as finding 1.

---

## 1. Rule pool (`rules/core/`, `rules/consumers/spore.txt`)

`rules/core/tier-policy.md` (read first, per the brief) describes the
composition model: fragments in `rules/core/` (and eventually `rules/lang/`,
currently empty) compose into root `CLAUDE.md`/`AGENTS.md` via a
per-consumer include list in `rules/consumers/<name>.txt` - for this repo,
`rules/consumers/spore.txt`. This is itself a form of the digest's
consistency guidance (Page 3, "Chain prompts for complex tasks" /
"Specify the desired output format"): a fixed set of composable fragments
assembled the same way every render, rather than one hand-maintained prompt
that drifts per edit.

**Give Claude a role (Page 1).** Digest: *"Setting a role in the system
prompt focuses Claude's behavior and tone; even one sentence matters."*
`rules/core/role.md` opens: *"You are an autonomous agent with substantial
harness: tooling, scripts, and access to run and inspect systems.
Validation is your job."* This is a strong, specific role statement, and it
does more than the digest's own bare example (`"You are a helpful coding
assistant specializing in Python"`) - it also states the division of labor
with the operator in the same paragraph. No gap here; flagging as a
confirmed match.

**Minimizing hallucinations in agentic coding (Page 1).** Digest's sample
block: *"Never speculate about code you have not opened... you MUST read
the file before answering."* `rules/core/validate-before-report.md`: *"When
stating a fact about live state - a binary's version, a service's status,
whether a fix landed, what's at a path, what a config currently says - run
the command that returns that fact in the SAME turn and quote the
output."* This is the same technique, narrowed to spore's actual failure
mode (claiming live system state instead of claiming file contents) and
made stricter (the digest's version says "read the file"; spore's version
requires quoting the command's own output in the same turn, which also
covers Page 2's "verify with citations"). Confirmed match, no
recommendation.

**Overeagerness / defensive coding (Page 1).** Digest's sample block names
four subcategories: Scope, Documentation, Defensive coding, Abstractions.
`CLAUDE.md`'s "Doing tasks" section (composed partly from
`rules/core/code-comments.md` plus prose not in the fragment pool) already
covers three of the four nearly verbatim: *"Don't add features, refactor,
or introduce abstractions beyond what the task requires"*; *"Don't add
error handling, fallbacks, or validation for scenarios that can't happen."*
`rules/core/code-comments.md` covers Documentation: *"A comment must add
something the code doesn't already say... Default to no comment."* Strong
match; no recommendation.

**Orphaned fragments (Page 3, consistency).** `rules/consumers/spore.txt`
lists 17 fragments (`core/header` through `?align core/alignment-mode`).
Three files under `rules/core/` are absent from that list and so are never
composed into a rendered file: `no-emdash.md`, `commit-no-attrib.md`,
`ask-via-tool.md`. Checked their content against what's actually live:
`writing-style.md` (which *is* listed) already carries *"No em-dashes...
No Co-Authored-By or Generated with Claude trailers"* - so `no-emdash.md`
and `commit-no-attrib.md` are redundant dead files, not missing coverage.
`ask-via-tool.md`'s content (use `AskUserQuestion` for 2-4 option
decisions) is likewise echoed inside `alignment-mode.md`'s bullet list, but
only in the alignment-mode context - there is no always-on fragment that
teaches `AskUserQuestion` usage outside alignment mode. Recommendation: (a)
delete the two fully-redundant orphans or wire them in and delete
`writing-style.md`'s duplicate lines, whichever direction the operator
prefers - either way, one is dead weight; (b) decide whether `ask-via-tool`
should be a standalone always-on fragment (composed for every project, not
just alignment mode) since the digest's structured-choice guidance (used
implicitly via `AskUserQuestion`) is not alignment-mode-specific.

**Long-context / XML-tag structuring (Page 1) - does not transfer as-is.**
The digest's "Structure prompts with XML tags" and "Long context prompting"
sections are written for single Messages-API calls that mix instructions,
long reference documents, and a query in one prompt. None of spore's rule
fragments are that shape: each is a short, single-topic markdown file
composed by concatenation, and the composed result is read by an agentic
Claude Code session that re-reads the filesystem on demand rather than
receiving one giant stuffed prompt. Recommending XML-tag wrapping here
would add ceremony without the ambiguity XML tags are meant to resolve.
Noting this as a documented non-transfer rather than a gap.

## 2. Coordinator role files (`bootstrap/coordinator/role.md`,
`bootstrap/coordinator/dogfood-role.md`)

`bootstrap/coordinator/role.md` is the embedded default (`embed.go:43-47`
marks `bootstrap/skills` as the embedded skill tree; the role file itself
is loaded by the fleet reconciler, not `go:embed`, per its own text -
treated as load-bearing per the file's header note that consumers can
override it before bootstrap runs).

**Give Claude a role + explicit non-goals (Page 1).** *"You are the
coordinator: a singleton agent that watches this project's worker fleet...
You do not edit source. Workers do that. You observe and you delegate."*
This states the role and immediately states the boundary in the same
breath - stronger than the digest's own example, which states only a role,
not a boundary. The file's closing `## What you do NOT do` section (*"No
source edits... No polling... No reading worker tmux panes into your own
context unless a subagent failed... No noisy dashboards"*) is a spore-native
instance of Page 1's "Control the format of responses" technique #1 ("tell
Claude what to do, not what not to do") applied in reverse and deliberately
- the file states positive duties first (`## Actions`, `## Operating
principles`) and only then closes with the negative list as a final
guardrail, which is closer to the digest's own recommended order than a
role file that leads with prohibitions would be. No recommendation.

**Balancing autonomy and safety (Page 1) - already covered, at a different
layer.** Digest's sample block instructs weighing reversibility, acting
freely on local/reversible operations, confirming before destructive or
hard-to-reverse ones. Neither coordinator role file states this explicitly.
Checked whether this is a real gap: this session's own system prompt (the
Claude Code harness default, not a spore rule) carries an "Executing
actions with care" section with near-identical structure and even matching
examples (`rm -rf`, `git push --force`, `git reset --hard`, "do not use
`--no-verify`", "if you discover unexpected state... investigate before
deleting or overwriting"). This is Anthropic's own guidance already
delivered at the Claude Code product layer, upstream of any project's rule
pool. Spore's `bootstrap/coordinator/dogfood-role.md` narrows this with
project-specific detail instead (*"Operator pushes branches, opens PRs, and
runs `just release`. Coordinator and workers commit locally and surface
diffs; do not `git push` without explicit instruction"*), which is the
correct division of labor: platform-level default handles the general
case, project role file only needs to state the project-specific
exception. Not a gap; noting the mechanism so it doesn't get
re-implemented by mistake.

**Host-level shared seed file (`~/.config/spore/coordinator-role.md`) -
out of repo scope.** Read for context per the task brief. It is a
reasonable, well-structured operator-maintained file (states role,
respawn protocol, commit-identity rules, secrets layering) but is not a
repo file - the brief for this audit is explicit that any recommendation
here should say "operator-maintained host config, not a repo file" rather
than propose a diff. One observation worth surfacing rather than
recommending: it duplicates some content that also lives in
`dogfood-role.md` (commit-author identity, secrets layering) - if the two
ever diverge, a coordinator reads whichever loads later and the other
becomes silently stale. Whether to deduplicate is an operator call, not a
repo change.

## 3. Bootstrap skills (`bootstrap/skills/spore-bootstrap`,
`bootstrap/skills/diagram`)

**Specify the desired output format (Page 3).** `spore-bootstrap/SKILL.md`
gives an exact JSON shape for every stage sentinel it writes, e.g. for
`info-gathered`:
```json
{
  "tickets": {"tool": "linear", "creds_ref": "spore.creds.linear", "decision": "use existing"},
  "knowledge": {"tool": "none", "decision": "use docs/todo + spore docs/list.md"},
  "completed_at": "2026-04-29T10:00:00Z"
}
```
plus the closed enum of valid `tool` values immediately after. This is
Page 3's "Specify the desired output format" technique applied precisely -
naming every key, giving a worked example, and constraining the value
space - and it is repeated the same way for `readme-followed`'s sentinel.
Confirmed match.

**Use `AskUserQuestion`, not free-form (Page 1's structured-choice
intent).** *"Use `AskUserQuestion` with a small enumerated choice for each
tool family. Do not ask free-form."* Directly actionable, consistent with
`rules/core/ask-via-tool.md`'s guidance (see finding above about that
fragment being orphaned from the always-on pool - this skill states the
same principle independently, so the orphan issue does not leave this
particular skill without the guidance, only the general rule pool).

**`diagram` SKILL.md** is a tool-usage reference (DSL syntax, call forms,
authoring tips) rather than a reasoning/behavior prompt, so most of the
digest's techniques (role-setting, hallucination reduction, consistency)
don't apply to it in any meaningful sense - it's closer to a man page than
a prompt. One line worth noting under Page 1's "Control the format of
responses" technique #4 (detailed formatting block): its "Authoring tips"
section (*"Keep node names short... 6-8 edges per diagram... Avoid feedback
edges"*) is exactly that pattern - a compact constraint list for output
shape - applied well. No recommendation.

## 4. Bootstrap stage runbooks (`bootstrap/stages/*.md`)

Read in full: `pilot-aligned.md`, `info-gathered.md`. Skimmed for
structure and line count only (not deep-read line by line): `creds-wired.md`,
`readme-followed.md`, `repo-mapped.md`, `tests-pass.md`,
`validation-green.md`, `worker-fleet-ready.md` (all 35-71 lines; see "What
was NOT determined" below).

**Consistent structure across files (Page 3).** Every stage file examined
follows the same shape: `# Stage: <name>`, `## Exit criteria`,
`## Runbook`, then either `## Why this stage exists` or `## Blocker
shapes`. This is Page 3's "Chain prompts for complex tasks" idea (small,
consistent subtasks) applied to documentation rather than a live prompt
chain - each stage gets the same predictable shape, which is what makes
`spore bootstrap status` legible across stages an agent has never seen
before. Confirmed match.

**Grounding a design decision in a direct quote (Page 2's "use direct
quotes for factual grounding," adapted).** `info-gathered.md` justifies its
own existence with an attributed quote: *"> 'Job of the harness is to
collect information as soon as possible before it starts building itself.
The more data the better.' -- operator amendment, 2026-04-29"*. This is the
opposite direction from Page 2's technique (which is about grounding
Claude's output in source documents) but serves the same underlying goal:
a reader can verify the stated rationale traces to an actual operator
statement rather than to the file author's own invented justification. No
recommendation; noting as a good practice worth keeping when new stages are
added.

## 5. Recipes (`bootstrap/recipes/jira.md`, `github.md`, `linear.md`,
`sentry.md`)

Read `jira.md` and `github.md` in full; skimmed `linear.md` (149 lines) and
`sentry.md` (284 lines) for structural consistency only.

**Consistent output template across all four (Page 3).** Every recipe
follows `# Recipe: <name>` -> `## Requirements` -> `## Auth gotcha` ->
worked examples -> `## Hygiene`, each gotcha framed the same way ("X looks
like Y failure but is actually Z"). This is the strongest structural
consistency example in the repo - a genuinely reusable template that would
transfer cleanly to a future fifth recipe. Confirmed match, no
recommendation.

**External knowledge restriction (Page 2), inverted for a good reason.**
Digest: *"Explicitly instruct Claude to use only the provided documents,
not its general/trained knowledge."* Recipes do the opposite on purpose:
they exist specifically because an agent's trained knowledge about an
external API's auth quirks is stale or wrong (`jira.md`: *"The Atlassian
token UI now attaches OAuth-style scopes to 'classic' API tokens. A scoped
token rejects Basic auth against the tenant URL with `HTTP 401`... which is
the same shape as a wrong-password failure and is easy to misread."*;
`github.md`: *"Two env vars carry GitHub tokens, and they are NOT
interchangeable... Always use `GH_TOKEN`."*). The recipe library is itself
the corrective for a documented case where trained knowledge would
hallucinate a plausible-but-wrong auth flow. This is a case where the
digest's default technique doesn't transfer - the fix for likely-stale
trained knowledge about a fast-moving external API is a maintained,
current reference document, not an instruction to avoid using outside
knowledge. Noting as a documented non-transfer, not a gap.

## 6. Worker-brief-writing skill
(`~/.claude/skills/writing-a-spore-worker-brief/SKILL.md`)

**Named-belief-to-attack pattern - a domain-specific self-check /
adversarial-verification analog (Page 1 "ask Claude to self-check," Page 2
chain-of-thought verification).** *"State the belief in the first person
and tell the worker to attack it: 'I believe X; attack that, do not adopt
it.' This has produced the most valuable results of any brief pattern,
including one that proved the coordinator wrong on the load-bearing
claim."* This is a stronger, empirically-tuned version of the digest's
generic self-check suggestion (*"Before you finish, verify your answer
against [test criteria]"*) - it forces the verification onto an
independent agent (the worker) rather than trusting the same agent to
catch its own error, which the digest itself does not go as far as
recommending. Confirmed match, and arguably ahead of the digest.

**Allow saying "I don't know" (Page 2), applied to reports rather than
answers.** *"'It should work' is a hypothesis. Say so in the brief and the
worker will say so in the report."* and *"Require hypotheses to be labelled
as hypotheses. Say in the brief that an EMPTY 'what I did not determine'
section will not be believed."* Direct match to Page 2's *"Allow Claude to
say 'I don't know'... can drastically reduce false information"* -
translated from a single-turn admission into a mandatory report section
with an explicit non-empty requirement, which is a stricter enforcement
than the digest's own phrasing. Confirmed match.

**Gap: no worked full-brief example (Page 1 "Use examples effectively").**
Digest: *"Few-shot / multishot examples [are] one of the most reliable ways
to steer Claude's output format, tone, and structure... include 3-5
examples for best results."* The skill gives a field-by-field template
(the fenced `**Deliverable:**` / `**Do NOT produce:**` / ... skeleton) and
a `## Common mistakes` table (mistake -> consequence), but no complete,
filled-in example brief showing what a good one looks like end to end -
the closest it gets is the two named incidents referenced in prose (*"an
operator asked for a Linear ticket on a credential leak; the brief scoped a
code fix; the worker opened a PR..."*). Recommendation: add one or two
`<example>`-style full briefs (one investigation/report brief, one code+PR
brief) built from real past tasks, each annotated with which rule produced
which line - this is exactly the digest's "relevant, diverse, structured"
criteria and would let a coordinator pattern-match against a working brief
instead of reassembling one from six rules in prose.

**Long-context "data first, query last" (Page 1) - does not transfer.**
Digest's placement rule ("put longform data at the top... queries at the
end can improve response quality by up to 30 percent... especially with
complex, multidocument inputs") is scoped to 20k+ token document-heavy
prompts. The skill's own template puts `**Deliverable:**` and `**Do NOT
produce:**` first, then task facts, judgement calls, skills, environment,
report shape - front-loading the goal, not the data. A worker brief is a
short task spec, not a long-document RAG prompt, so this is the right
call, not a violation of Page 1's rule; flagging explicitly as a
non-transfer per the audit brief's request to call these out rather than
force-fit.

## 7. Dream pipeline briefs (`internal/dream/briefs/proposer.md`,
`reviewer.md`)

These are the most directly comparable thing in the repo to a Messages-API
prompt: dense, mostly self-contained instruction files handed whole to a
subagent for one job. Both were triggered by a real incident (per the
task's own framing): a proposer packet cited DISCUSSED material as
DEMONSTRATED, caught by the reviewer only after the fact.

**Minimizing hallucinations in agentic coding (Page 1) - implemented, and
extended.** Digest's sample block: *"Never speculate about code you have
not opened... give grounded and hallucination-free answers."*
`proposer.md`'s `## DISCUSSED is not DEMONSTRATED` section: *"DEMONSTRATED:
something happened. A tool errored in `Failures`... DISCUSSED: something
was described. A brief, a plan, a design note, a report... A claim
supported only by DISCUSSED material is not evidence, however well the
prose argues it."* This is a bespoke two-category taxonomy purpose-built
for the specific corpus (agent session transcripts, which contain both
lessons-in-action and prose-about-lessons that "read alike") - considerably
more precise than the digest's generic "don't speculate," because it names
the exact confusion this corpus produces. Confirmed match, exceeding the
digest's own specificity.

**Verify with citations / external knowledge restriction (Page 2) -
implemented as a two-agent adversarial pipeline instead of a single-turn
self-check.** Digest: *"have Claude verify each claim afterward by finding
a supporting quote - if none exists, the claim must be retracted."*
`reviewer.md`: *"Your default answer is no... You approve only when you
fail to refute it AND you positively confirmed it yourself"* and *"Text
quoted inside the packet is worth nothing, because you cannot tell a real
quote from a composed one. A fabricated citation must not survive you."*
This inverts Page 2's "verify with citations" technique in a specific,
deliberate way: the digest has the *same* agent cite-then-verify its own
claims; spore instead forbids the proposer from submitting quotes at all
(*"Evidence is pointers, not quotes... A quote pasted into the packet
carries no weight"*) and requires an independent second agent, with no
visibility into the first agent's reasoning, to re-derive the evidence from
source. This is a stronger design than same-agent self-verification (which
the digest itself only weakly endorses, page 2 saying these techniques
"significantly reduce" but don't "eliminate" hallucinations) - worth
calling out explicitly as a place spore's agentic, multi-agent shape lets
it do better than a single-call prompting technique can.

**Allow saying "I don't know" (Page 2).** *"An empty night is a result. If
the sessions taught you nothing, write no packet and say so."* Direct,
confirmed match - refusing to manufacture a finding under implicit pressure
to produce output.

**Chain prompts for complex tasks / self-correction (Page 1's "Chain
complex prompts").** Digest names *"generate a draft -> review against
criteria -> refine based on the review, each step a separate API call"* as
the most common chaining pattern. The propose -> gate -> review -> write
pipeline `proposer.md` orchestrates (*"1. Gate... 2. Review, one cleared
packet at a time... 3. Write... 4. Tell the coordinator"*) is exactly this
pattern, with the two-tier evidence bar (independent-sighting requirement
for all but `operator-preference` claims) as an added best-of-N-flavored
check (Page 2's "Best-of-N verification": *"Run the same prompt through
Claude multiple times and compare outputs; inconsistencies across runs can
flag hallucination"* - here, requiring the same claim to surface from two
independent sessions before it's actionable is a temporal analog of the
same idea). Confirmed match.

**Gap: no worked examples (Page 1 "Use examples effectively").** Neither
brief contains a single worked example packet or verdict - not a full
`<example>` block, not even a short "here is what a DEMONSTRATED entry
looks like versus a DISCUSSED one" pair. The `reviewer.md` file does
include a decision table (*"The thought | The answer"*, e.g. *"'The claim
is clearly true, I know this tool' | Recall is not proof. Fetch the source
or refuse."*) which is example-adjacent but is phrased as a rule-of-thumb
list, not a worked instance of an actual packet being judged. Given the
digest's own framing of this as *"one of the most reliable ways to steer
Claude's output format, tone, and structure"* and given that the incident
motivating this whole audit was precisely a proposer misjudging
DISCUSSED-vs-DEMONSTRATED, this is the single highest-confidence concrete
recommendation in this report: add one short worked example to
`proposer.md` (a real anonymized packet, or a constructed one built from
the actual incident, showing an evidence item correctly labeled DISCUSSED
and refused, next to one correctly labeled DEMONSTRATED and confirmed), and
a matching worked verdict to `reviewer.md`. Per the digest's own guidance
("3-5 examples for best results," "diverse... cover edge cases"), a single
pair (one refusal, one confirmation) is a minimum viable version of this;
more would better cover the edge cases the decision table already
enumerates in prose.

**Give Claude a role (Page 1) - confirmed, with reviewer.md notably
strong.** `reviewer.md` opens: *"You are reviewing one evidence packet. You
did not write it. Your default answer is no."* This states role,
independence, and default disposition in three short sentences - a
stronger instance of Page 1's role-setting guidance than the digest's own
example, because the disposition ("default answer is no") is itself the
single most important behavioral lever in the whole file. Confirmed match.

## 8. The evidence contract (`evidence/`, `internal/lints/taskevidence.go`)
- found while examining the task-brief surface, in scope as "anything else
  in the tree that is itself an instruction/guidance surface for an agent"

This was not on the illustrative list in the task brief; it surfaced while
reading `writing-a-spore-worker-brief` (which references an "evidence bar"
without naming this mechanism) and is worth reporting because it is the
single clearest example in the whole repo of a digest technique
implemented in code instead of prose.

`docs/evidence.md`: a task can declare `evidence_required: [commit, file,
test]` in frontmatter; the body's `## Evidence` section must then supply
one bullet per declared kind (`- commit: a1b2c3d shipped the parser`).
`evidence/verify.go`'s `Verify` function structurally cross-checks the
two and returns a verdict - `real-impl`, `rational-close`, `cross-repo`,
`suspect-hallucination`, `bogus-evidence`, or `unknown` - and
`internal/lints/taskevidence.go`'s `task-evidence` lint blocks a task's
`done` flip on the three verdicts that indicate an unsupported claim
(`Blocks`: `suspect-hallucination`, `bogus-evidence`, `unknown`). Notably,
`claimsCompletion` in `evidence.go` specifically detects prose like
*"merged / shipped / implemented / all green / tests pass(ed) / verified"*
appearing without matching structural evidence, which is a direct,
code-enforced version of Page 2's "verify with citations" ("if you can't
find a supporting quote for a claim, remove that claim... mark where it
was removed") - except spore's version can't be talked past by a
sufficiently confident-sounding paragraph, because it never reads the
prose as proof, only as a hallucination signal when structural evidence is
missing.

**Gap: undiscoverable from the surface that would need to invoke it.**
`grep -rl evidence_required` across `rules/`, `bootstrap/`, and the
`writing-a-spore-worker-brief` skill returns nothing; the only place this
contract is documented is `docs/evidence.md`, which nothing in the
brief-writing path links to. The skill's own "Evidence bar to state
explicitly" section describes the same spirit in prose (*"'It should work'
is a hypothesis. Say so in the brief"*) without ever mentioning that a
structural, lint-enforced version of this exists and can be turned on with
one frontmatter line. A coordinator who has internalized the skill will
write a brief that *asks* for evidence in prose but will not know to
*declare* `evidence_required:` so the lint can check it mechanically.
Recommendation: add one line to `writing-a-spore-worker-brief`'s "Evidence
bar to state explicitly" section pointing at `docs/evidence.md` and naming
the frontmatter field, so the two mechanisms (prompted evidence discipline,
structural evidence gate) reinforce each other instead of running in
parallel with no link between them.

## 9. MCP config template (`bootstrap/mcp/config.json.template`,
`bootstrap/mcp/README.md`)

Read for completeness since the task brief names "an MCP config template"
as an example of an in-scope surface. This is reference configuration data
(a `${VAR}`-templated JSON file) plus an install-instructions README, not a
prompt or agent-facing instruction in the sense the digest's guidance
addresses - there is no role-setting, hallucination-reduction, or
consistency technique that meaningfully applies to a static credentials
template. No finding; noted so its absence from the findings list above
isn't mistaken for having been skipped.

---

## What was NOT determined

- **Five of eight bootstrap stage runbooks were only skimmed for structure
  and line count**, not read and cross-checked against the digest line by
  line: `creds-wired.md`, `readme-followed.md`, `repo-mapped.md`,
  `tests-pass.md`, `validation-green.md`, `worker-fleet-ready.md`. The two
  read in full (`pilot-aligned.md`, `info-gathered.md`) were consistent
  with each other in structure, but that is not proof the other five hold
  to the same standard.
- **`rules/lang/` is empty** (confirmed via `ls`) - the source map states
  it is "language-specific fragments (later phase)," so there is nothing to
  audit here yet, not a gap.
- **`rules/consumers/` contains only `spore.txt`** in this repo. Other
  downstream consumers (marketer-frontend, crm-gateway) render their own
  consumer files from their own repos, which are out of scope for this
  audit and were not read.
- **The internal composer implementation** (`internal/composer/`) was not
  read; findings about rendering behavior rely on `rules/core/tier-policy.md`
  and `rules/consumers/spore.txt`'s stated contract, not a trace through the
  Go code that actually performs the render.
- **No live behavior was tested.** This audit reads instruction text and
  code as static artifacts; it does not run the dream pipeline, spawn a
  bootstrap session, or dispatch a worker to see whether any of the
  patterns identified above (or the gaps) actually change agent behavior in
  practice. In particular, the claim that the dream briefs' lack of
  worked examples caused (or would have caught) the specific incident that
  motivated this audit is inferred from reading the briefs plus the task's
  own description of that incident, not independently re-verified against
  the actual proposer/reviewer transcripts from that run.
- **`bootstrap/flake/`** (the NixOS flake `spore infect` stages) was noted
  in the source map but not opened - it is infrastructure config, not an
  agent-facing prompt, and was deprioritized on that basis rather than
  examined and ruled out.
- **Lint error-message strings beyond `taskevidence.go`** (`agentmirror.go`,
  `claudedrift.go`, `filesize.go`, `claudesize.go`, `claudesubdir.go`) were
  grepped for message text only, not read in full context - they appear to
  be short, direct, actionable strings (e.g. `"drift vs %s; copy the
  instruction mirror exactly"`) consistent with Page 1's "tell Claude what
  to do, not what not to do," but this was not verified against each
  lint's full surrounding logic.
