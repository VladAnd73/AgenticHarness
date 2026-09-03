# Recipe: Linear GraphQL API

Read and write access to a Linear workspace from a coordinator or
worker pane. Linear has a single GraphQL endpoint; there is no REST
surface.

## Requirements

Operator-managed env var, sourced via `spore-with-secrets`:

- `LINEAR_HOMKEY_API_KEY` -- a Linear **personal API key** from
  Settings -> Security & access -> Personal API keys. A personal key
  inherits the creating user's permissions (so read+write if that
  account can write the target team). Workspace OAuth tokens also work
  but authenticate differently (see Auth gotcha).

  Note the spelling: `HOMKEY`, no second `E`. Every example below uses
  this variable.

- `LINEAR_API_KEY` -- the **retired** workspace. Kept for historical
  comparison only; nothing live should be read or written through it.
  Use it only when you deliberately need pre-migration state.

Drop it in `~/.config/spore/<project>/secrets.env` (per-project) or
`~/.config/spore/secrets.env` (global). Mode 0600 on the file, 0700
on the dir.

Which workspace a key points at is not guessable from the key itself.
Resolve it before trusting a result:

```
query { organization { name urlKey } }
```

## Auth gotcha

Personal API keys go in the `Authorization` header **directly, with no
`Bearer` prefix**:

```
-H "Authorization: $LINEAR_HOMKEY_API_KEY"
```

OAuth access tokens (from the OAuth2 flow, not personal keys) DO use
`Authorization: Bearer <token>`. Sending a personal key with `Bearer`,
or an OAuth token without it, returns `HTTP 400 "authentication
failed"` -- easy to misread as a bad key.

## Endpoint

Everything -- queries and mutations -- is one POST:

```
POST https://api.linear.app/graphql
Content-Type: application/json
{"query": "...", "variables": {...}}
```

## Worked examples

All assume `spore-with-secrets` is on PATH and
`LINEAR_HOMKEY_API_KEY` resolves.

### Verify auth + resolve a team

```
spore-with-secrets bash -lc '
curl -sS -X POST https://api.linear.app/graphql \
  -H "Authorization: $LINEAR_HOMKEY_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"query\":\"query { viewer { id name email } teams(filter:{ key:{ eq:\\\"HK\\\" } }) { nodes { id key name issueCount } } }\"}"
' | jq .
```

A successful call returns the authenticated user and the matched team.
`viewer` confirms identity; HTTP 400 means the key/prefix combo is
wrong.

### List a team's issues (the "board")

A Linear board is a team's issue view. Filter issues by team key:

```
spore-with-secrets bash -lc '
curl -sS -X POST https://api.linear.app/graphql \
  -H "Authorization: $LINEAR_HOMKEY_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"query\":\"query { issues(filter:{ team:{ key:{ eq:\\\"HK\\\" } } }, first: 50, orderBy: updatedAt) { nodes { identifier title state { name } assignee { name } updatedAt } } }\"}"
' | jq .
```

Pagination uses Relay cursors: pass `after: \"<endCursor>\"` and read
`pageInfo { hasNextPage endCursor }`.

### Fetch one issue

```
spore-with-secrets bash -lc '
curl -sS -X POST https://api.linear.app/graphql \
  -H "Authorization: $LINEAR_HOMKEY_API_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"query\":\"query { issue(id:\\\"HK-123\\\") { identifier title description state { name } } }\"}"
' | jq .
```

`description` is markdown (not ADF/HTML -- unlike Jira). The
human-readable `HK-123` identifier is accepted wherever an issue `id`
is expected.

### Identifiers are NOT stable across the two workspaces

Both workspaces have a team keyed `HK`, and they reuse the same numbers
for different tickets. They fork at exactly `HK-509`:

- `HK-508` and below: the same identifier is the same ticket in both.
- `HK-509` and above: the same identifier is a DIFFERENT ticket. Do not
  translate by number. Re-resolve by title.

There is no arithmetic shortcut. The offset looks like a clean +1 for
the first few tickets past the fork and then stops holding, so a
number-shifting rule silently returns the wrong ticket.

An identifier above the fork that a query cannot find is the ordinary
case, not an error: the retired workspace stops at `HK-517` while the
live one keeps growing.

## Writes (mutations)

Available when the key's account has write access. Mutations return a
`success` boolean plus the mutated node. Examples (run only when the
operator has greenlit writes to this surface):

- Comment: `mutation { commentCreate(input:{ issueId:"<uuid|HK-123>", body:"..." }) { success comment { id } } }`
- Create issue: `mutation { issueCreate(input:{ teamId:"<team-uuid>", title:"...", description:"..." }) { success issue { identifier } } }`
- Move state: resolve the target `workflowState` id for the team
  first (`team { states { nodes { id name type } } }`), then
  `issueUpdate(id:"HK-123", input:{ stateId:"<state-uuid>" })`.

`teamId`/`stateId` want UUIDs, not the `HK` key -- resolve them via a
query first. Bodies are markdown strings, no document-format wrapper.

## Hygiene

- Never echo `$LINEAR_HOMKEY_API_KEY` to a pane or log. Shape-check with
  `${#v}` / `${v:0:4}` when debugging.
- Personal keys are revocable instantly from the same Linear settings
  page; revoking is the kill switch.
- GraphQL errors come back `HTTP 200` with an `errors[]` array, not a
  non-2xx status -- always inspect the body, not just the status code.
