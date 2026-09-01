## Tier policy

Rules tier into root `CLAUDE.md` / `AGENTS.md` (project-wide), subdir
instruction mirrors (single-area, under 150 lines), `docs/<topic>.md`
(rationale and debugging notes), and `docs/todo/<slug>.md`
(multi-session specs, each starting with a `**Status**:` header).

An implementation plan is scaffolding, not documentation. Write it to
`docs/todo/<slug>-plan.md`, which is git-ignored: it lives on disk for
the run and you delete it once executed. Workers receive plan content in
their brief, not through the repo. The spec the plan serves stays
tracked, because it holds the decisions a later reader cannot re-derive.

Test for an inline comment:
would deleting it confuse a reader of the surrounding code plus loaded
rules? If no, drop it. Default to no comment.
