# Recipe: Slack Web API

Posting and lookups from a coordinator or worker pane against a
Slack bot token: acknowledge a thread, post a result, resolve a
Slack user ID to a name.

Out of scope:

- Reading channel history or thread replies. `internal/watch/slack.go`
  (`spore watch slack`) already does this on a timer for any project
  with `[slack] enabled = true` in its `watch.toml` - you should not
  need to call `conversations.history` / `conversations.replies`
  directly. `Permalink` (`chat.getPermalink`) is also already wrapped
  there.
- Socket Mode / event-based delivery. Not built. The watcher polls;
  see `internal/watch/slack.go`'s doc comment for why (no persistent
  process, unlike every other spore watcher).
- Anything needing scopes beyond `channels:history`, `channels:read`,
  `groups:history`, `groups:read`, `users:read`, `chat:write` - e.g.
  reactions, file upload, admin APIs. Not requested, not tested.

## Requirements

Operator-managed env var, sourced via `spore-with-secrets`:

- `SLACK_BOT_TOKEN` -- a Slack bot token (`xoxb-...`) with at least
  the scopes above.

Per-project, not global: each project watching a different
workspace/app needs its own token. Lives at
`~/.config/spore/<project>/secrets.env`, mode 0600, dir mode 0700.
Never put a Slack token in the global secrets file - unlike `GH_TOKEN`,
a Slack app is workspace-specific and has no business reaching a
second project's channel.

There is no Slack CLI to shell out to (unlike `gh`); every call here
is a raw `curl` against `https://slack.com/api/<method>`, same base
`internal/watch/slack_api.go` uses (overridable via
`SPORE_SLACK_API_BASE` for tests, not something you touch at the
recipe level).

## Auth gotcha

Every call needs `Authorization: Bearer $SLACK_BOT_TOKEN` - Slack does
not accept the token as a query param or POST field for
`Authorization`-protected methods. Resolve the token fresh per call
via `spore-with-secrets`; do not export it into a long-lived shell.

`spore-with-secrets` resolves the project from `$SPORE_PROJECT_ROOT`
(falls back to `git rev-parse --show-toplevel`'s basename). From a
coordinator pane this is already set correctly by the spawn
environment; from an ad-hoc shell, set it explicitly or the token
resolves to the wrong project's file (or nothing):

```
SLACK_TOKEN=$(SPORE_PROJECT_ROOT=/home/spore/<project> spore-with-secrets bash -c 'echo $SLACK_BOT_TOKEN')
```

## Worked examples

All verified live against a real workspace (HomeKey, 2026-09-16).

### Post a message (ack or result)

```
curl -s -X POST "https://slack.com/api/chat.postMessage" \
  -H "Authorization: Bearer $SLACK_TOKEN" \
  -H "Content-Type: application/json; charset=utf-8" \
  -d '{"channel":"<channel_id>","thread_ts":"<thread_ts>","text":"<message>"}'
```

Add `"thread_ts": "<thread_ts>"` to reply IN the thread (what you
almost always want here) - omit it to post a new top-level message
instead. Check the response body for `"ok":true`; a Slack API error
still returns HTTP 200 with `"ok":false` and an `"error"` field, so a
bare exit-code check will not catch a failure.

### Resolve a user ID to a name

```
curl -s -H "Authorization: Bearer $SLACK_TOKEN" \
  "https://slack.com/api/users.info?user=<user_id>" | \
  grep -o '"real_name":"[^"]*"'
```

Needs `users:read` (already in this recipe's scope list). Slack
messages carry the raw user ID (`U0123ABC`) in `user`, never a display
name - resolve it once per brief/ack rather than leaving the raw ID
in front of a human.

## Rate limits

No fixed tier assumption - verify per app. Slack throttles
`conversations.history` to 1 req/min for non-Marketplace apps created
after 2025-05-29; internal/workspace-only apps get Tier 3 (50+/min).
Confirmed empirically for the `harness-debugger` app (created via
manifest in the HomeKey workspace, 2026-09-16): 5 back-to-back
`conversations.history` calls all returned `200 ok:true`, no
`429`/`Retry-After` - well above Tier 1. To check a new app yourself,
fire 3-5 calls back to back and look for a 429; do not assume from
"looks internal."

`chat.postMessage` has its own, separate, more generous limit (roughly
1 msg/sec per channel under normal Tier 3/4) - the ack/result pattern
in this recipe (one post per event) stays far under it regardless of
app tier.

## Hygiene

- Never echo `$SLACK_BOT_TOKEN` to a pane or log. Check shape only
  (`${#v}`, `${v:0:9}` - a bot token starts with `xoxb-`).
- Slack API errors are always HTTP 200 with `"ok":false` - always
  check the body, never just the curl exit code.
- Revoke/rotate from the app's page at
  `https://api.slack.com/apps` -> the app -> "OAuth & Permissions".
