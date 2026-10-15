#!/usr/bin/env bash
# Claude Code upgrade smoke check for spore's hook surface.
#
# spore leans on several Claude Code internals that Claude Code never
# promised to keep stable: hook event names, hook response field names,
# the transcript file, and the names of the tools we deny. When one of
# them moves, nothing errors. The hook just stops having an effect, and
# you find out weeks later. Each check below exercises one of those
# coupling points against the live host.
#
# Run it once before an upgrade to capture a baseline, then again after,
# and diff:
#
#   scripts/cc-upgrade-smoke.sh > /tmp/before.txt
#   (operator runs the upgrade)
#   scripts/cc-upgrade-smoke.sh > /tmp/after.txt
#   diff /tmp/before.txt /tmp/after.txt
#
# Exit code: 0 when every check reports OK, 1 when any reports CHANGED.
# A CHANGED line is the thing to read; the exit code only saves you from
# missing it.
#
# Side effects: none outside a throwaway directory that is deleted on
# exit. Checks 4, 5 and 6 each build the environment their mechanism
# needs rather than borrowing the live one, so the script is safe to run
# at any time, from any session, as often as you like.
#
# Every host path is overridable so the test suite can run this against a
# sandbox. See docs/cc-upgrade-smoke.md for the full list.

set -u

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
fi

SPORE_BIN=${SMOKE_SPORE_BIN:-/run/current-system/sw/bin/spore}
CLAUDE_BIN=${SMOKE_CLAUDE_BIN:-$(command -v claude || true)}
HOOKS_DIR=${SMOKE_HOOKS_DIR:-$HOME/.claude/hooks}
SETTINGS=${SMOKE_SETTINGS:-$HOME/.claude/settings.json}
PROJECTS_DIR=${SMOKE_PROJECTS_DIR:-$HOME/.claude/projects}
PROJECT=${SMOKE_PROJECT:-spore}
COORD_INBOX=${SMOKE_COORDINATOR_INBOX:-$HOME/.local/state/spore/coordinator/$PROJECT/inbox}

TMP=$(mktemp -d "${TMPDIR:-/tmp}/cc-smoke.XXXXXX")
trap 'rm -rf "$TMP"' EXIT

FAILED=0

say() { printf '\n== %s\n' "$*"; }

verdict() { # verdict <label> <got> <want>
  if [ "$2" = "$3" ]; then
    printf '  %-34s %s  OK\n' "$1" "$2"
  else
    printf '  %-34s %s  WANT %s  <-- CHANGED\n' "$1" "$2" "$3"
    FAILED=$((FAILED + 1))
  fi
}

note() { printf '  %-34s %s\n' "$1" "$2"; }

# json_string reads JSON on stdin and prints the value of the first
# string field with the given name. The payloads here are small and
# well known, and a real JSON parser is not reachable from every
# environment this has to run in (the nix build sandbox has no python3).
json_string() {
  tr '\n' ' ' | grep -oE "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" |
    head -1 | sed 's/.*:[[:space:]]*"\(.*\)"/\1/'
}

say "versions"
note "claude" "$("$CLAUDE_BIN" --version 2>&1 | head -1)"
note "spore" "$("$SPORE_BIN" --version 2>&1 | head -1)"

say "1. transcript parsing (token monitors read a real number)"
# The monitor must read a nonzero token count out of a real .jsonl. A cap
# of 1 forces it to fire (exit 2); a huge cap must leave it silent.
TRANSCRIPT=$(find "$PROJECTS_DIR" -name '*.jsonl' -printf '%T@ %p\n' 2>/dev/null |
  sort -rn | head -1 | cut -d' ' -f2-)
if [ -z "${TRANSCRIPT:-}" ]; then
  verdict "a transcript to parse" "none under $PROJECTS_DIR" "one or more"
else
  note "using" "$(basename "$TRANSCRIPT")"
  PAYLOAD=$(printf '{"session_id":"smoke","transcript_path":"%s"}' "$TRANSCRIPT")
  # Two guards would make a naive invocation prove nothing: the worker
  # monitor skips any session with no SPORE_TASK_INBOX, and skips any
  # inbox that sits under the coordinator state dir. Point it at a
  # throwaway worker-shaped inbox so neither one short-circuits.
  FAKE_INBOX="$TMP/worker/inbox"
  mkdir -p "$FAKE_INBOX"
  printf '%s' "$PAYLOAD" | SPORE_TASK_INBOX="$FAKE_INBOX" SPORE_WORKER_TOKEN_WRAP=1 \
    "$SPORE_BIN" worker token-monitor >/dev/null 2>&1
  verdict "fires at a 1-token cap" "$?" 2
  printf '%s' "$PAYLOAD" | SPORE_TASK_INBOX="$FAKE_INBOX" SPORE_WORKER_TOKEN_WRAP=99000000 \
    "$SPORE_BIN" worker token-monitor >/dev/null 2>&1
  verdict "silent at a huge cap" "$?" 0
fi

say "2. SessionStart loader still injects state.md"
OUT=$(printf '{"session_id":"smoke","cwd":"%s"}' "$PWD" |
  timeout 10 "$HOOKS_DIR/load-state-md.pl" 2>/dev/null)
BYTES=$(printf '%s' "$OUT" | wc -c)
if printf '%s' "$OUT" | tr '\n' ' ' | grep -qE '"additionalContext"[[:space:]]*:[[:space:]]*"[^"]'; then
  HAS=yes
else
  HAS=no
fi
verdict "additionalContext present" "$HAS" yes
note "payload size" "$BYTES bytes (cache cost per boot)"

say "3. PreToolUse guard still denies background Bash"
OUT=$(printf '{"tool_name":"Bash","tool_input":{"command":"sleep 1","run_in_background":true}}' |
  timeout 10 "$HOOKS_DIR/block-bg-bash.pl" 2>/dev/null)
DEC=$(printf '%s' "$OUT" | json_string permissionDecision)
verdict "permissionDecision" "${DEC:-absent}" deny

say "4. watch-inbox wakes on a queued message"
# The env form, pointed at a throwaway inbox, walks the same wake path as
# the live Stop hook without touching a real inbox or guessing a path.
INBOX="$TMP/watch/inbox"
mkdir -p "$INBOX"
printf '{"slug":"smokeslug","ts":"2026-01-01T00:00:00Z","msg":"smoke"}' >"$INBOX/1.json"
SPORE_TASK_INBOX="$INBOX" SPORE_TASK_SLUG=smokeslug \
  timeout 15 "$SPORE_BIN" hooks watch-inbox >/dev/null 2>&1
verdict "exit on queued message" "$?" 2

say "5. Notification hook writes a poke (the coordinator wake)"
# SPORE_COORDINATOR_STATE_DIR redirects the poke into a throwaway tree,
# so this exercises the same write path without waking anyone. Count the
# whole tree, not one level: against a live inbox a poke lands in inbox/,
# is acked into inbox/.pending/ and ends in inbox/read/, so counting any
# single level undercounts.
POKE_STATE="$TMP/coordstate"
POKE_INBOX="$POKE_STATE/$PROJECT/inbox"
BEFORE=$(find "$POKE_INBOX" -name '*.json' 2>/dev/null | wc -l)
printf '{}' | SPORE_COORDINATOR_STATE_DIR="$POKE_STATE" \
  timeout 10 "$SPORE_BIN" hooks notify-coordinator "$PROJECT" >/dev/null 2>&1
RC=$?
AFTER=$(find "$POKE_INBOX" -name '*.json' 2>/dev/null | wc -l)
verdict "exit code" "$RC" 0
verdict "poke count grew" "$((AFTER - BEFORE))" 1
if [ -d "$COORD_INBOX" ] && [ -w "$COORD_INBOX" ]; then
  note "live coordinator inbox" "writable"
elif [ -d "$COORD_INBOX" ]; then
  note "live coordinator inbox" "present but not writable"
else
  note "live coordinator inbox" "absent ($COORD_INBOX)"
fi

say "6. replenish hook reaches reconcile and returns promptly"
# The hook no-ops unless the firing session looks like a coordinator
# ($SPORE_TASK_INBOX under $SPORE_COORDINATOR_STATE_DIR). Run from a
# worker session it returns in a millisecond having done nothing, which
# reads as a pass. Give it a throwaway coordinator shape and an empty
# task queue so the reconcile pass actually runs with nothing to spawn,
# then require the summary line that only that pass prints.
REPL_STATE="$TMP/replenish/state"
REPL_ROOT="$TMP/replenish/project"
mkdir -p "$REPL_STATE/inbox" "$REPL_ROOT/tasks"
START=$(date +%s%N)
OUT=$(cd "$REPL_ROOT" && printf '{"session_id":"smoke"}' |
  SPORE_COORDINATOR_STATE_DIR="$REPL_STATE" SPORE_TASK_INBOX="$REPL_STATE/inbox" \
    SPORE_FLEET_MAX_WORKERS=1 timeout 40 "$SPORE_BIN" fleet replenish-hook 2>&1)
RC=$?
MS=$((($(date +%s%N) - START) / 1000000))
verdict "exit code" "$RC" 0
case "$OUT" in
*spawned=*) verdict "ran the reconcile pass" yes yes ;;
*) verdict "ran the reconcile pass" no yes ;;
esac
note "reconcile said" "${OUT:-nothing}"
if [ "$MS" -lt 2000 ]; then
  note "duration" "${MS}ms  OK"
else
  verdict "duration under 2000ms" "${MS}ms" "under 2000ms"
fi

say "7. spore's own settings still deny cross-session messaging"
DENY=$(tr '\n' ' ' <"$SETTINGS" 2>/dev/null |
  grep -oE '"deny"[[:space:]]*:[[:space:]]*\[[^]]*\]' | head -1 |
  grep -oE '"[^"]+"' | sed '1d' | tr -d '"' | tr '\n' ' ' | sed 's/ *$//')
verdict "permissions.deny" "${DENY:-unreadable}" "SendMessage ListAgents"
INBOUND=$(json_string crossSessionInbound <"$SETTINGS" 2>/dev/null)
note "crossSessionInbound" "${INBOUND:-unset}"

say "8. Claude Code still ships the names spore's hooks speak"
# Checks 2, 3 and 7 can only see our half of each contract: they prove a
# hook still emits permissionDecision, or that settings.json still names
# SendMessage. None of them can see Claude Code renaming the field or the
# tool on its side, which is the failure that would pass silently. This
# check reads the other half, by looking for each name in the Claude Code
# bundle that is actually installed. A name that vanishes from the bundle
# while our side keeps using it is the rename, caught.
#
# Presence of a string is necessary, not sufficient: it catches a rename,
# not a change in what the field means.
WANT_NAMES="hookSpecificOutput additionalContext permissionDecision run_in_background transcript_path ${DENY:-}"
BUNDLE_DIR=""
if [ -n "${CLAUDE_BIN:-}" ] && [ -e "$CLAUDE_BIN" ]; then
  BUNDLE_DIR=$(dirname "$(readlink -f "$CLAUDE_BIN")")
fi
if [ -z "$BUNDLE_DIR" ]; then
  verdict "claude bundle" "not found" "a readable path"
else
  note "bundle" "$BUNDLE_DIR"
  PATTERN=$(printf '%s' "$WANT_NAMES" | tr ' ' '\n' | grep -v '^$' | paste -sd'|' -)
  FOUND=" $(grep -rahoE "$PATTERN" "$BUNDLE_DIR" 2>/dev/null | sort -u | tr '\n' ' ')"
  for NAME in $WANT_NAMES; do
    case "$FOUND" in
    *" $NAME "*) verdict "$NAME" present present ;;
    *) verdict "$NAME" absent present ;;
    esac
  done
fi

echo
if [ "$FAILED" -gt 0 ]; then
  printf 'RESULT: %d check(s) CHANGED. Read each CHANGED line above against\n' "$FAILED"
  printf 'the Claude Code release notes before trusting the hook it names.\n'
  exit 1
fi
printf 'RESULT: all checks OK.\n'
