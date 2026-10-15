package spore

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const smokeScript = "scripts/cc-upgrade-smoke.sh"

// smokeStubs describes the fake host the smoke script runs against.
// Each field stands in for one thing the real script reads off the
// live machine, so a test can break exactly one of them and watch the
// script notice.
type smokeStubs struct {
	guardDecision string
	denyNames     []string
	claudeStrings []string
	pokeOnNotify  bool
	replenishExit int
	replenishLine string
	watchExit     int
}

func defaultSmokeStubs() smokeStubs {
	return smokeStubs{
		guardDecision: "deny",
		denyNames:     []string{"SendMessage", "ListAgents"},
		claudeStrings: []string{
			"hookSpecificOutput", "additionalContext", "permissionDecision",
			"run_in_background", "transcript_path", "SendMessage", "ListAgents",
		},
		pokeOnNotify:  true,
		replenishExit: 0,
		replenishLine: "replenish-hook: active=0 spawned=0 kept=0 reaped=0 skipped=0",
		watchExit:     2,
	}
}

// buildSmokeSandbox writes a complete fake host into a temp dir and
// returns the environment that points the script at it. Nothing here
// touches the operator's real hooks, inbox or settings.
func buildSmokeSandbox(t *testing.T, stubs smokeStubs) []string {
	t.Helper()
	root := t.TempDir()

	// The nix build sandbox has no /usr/bin/env, so stub shebangs point
	// at whichever bash the test itself found.
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("locate bash: %v", err)
	}
	shebang := "#!" + bash + "\n"

	write := func(rel, body string, mode os.FileMode) string {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		return path
	}

	poke := "true"
	if stubs.pokeOnNotify {
		poke = `mkdir -p "$SPORE_COORDINATOR_STATE_DIR/$3/inbox" && printf '{}' > "$SPORE_COORDINATOR_STATE_DIR/$3/inbox/poke-$$.json"`
	}
	sporeBin := write("bin/spore", shebang+`case "$1 $2" in
  "--version ") echo "0.0.0-stub (sandbox)" ;;
  "worker token-monitor")
    cat >/dev/null
    if [ "${SPORE_WORKER_TOKEN_WRAP:-0}" -le 1 ]; then exit 2; fi
    exit 0 ;;
  "hooks watch-inbox") exit `+strconv.Itoa(stubs.watchExit)+` ;;
  "hooks notify-coordinator")
    cat >/dev/null
    `+poke+`
    exit 0 ;;
  "fleet replenish-hook")
    cat >/dev/null
    echo "`+stubs.replenishLine+`"
    exit `+strconv.Itoa(stubs.replenishExit)+` ;;
  *) echo "stub spore: unhandled $*" >&2; exit 64 ;;
esac
`, 0o755)

	// The real probe greps the shipped Claude Code bundle for the names
	// spore's hooks depend on. The stub is a tiny file carrying the same
	// names, so dropping one simulates an upstream rename.
	write("claudepkg/.claude-wrapped", strings.Join(stubs.claudeStrings, "\n")+"\n", 0o644)
	claudeBin := write("claudepkg/claude", shebang+`echo "9.9.9-stub (Claude Code)"
`, 0o755)

	write("hooks/load-state-md.pl", shebang+`cat >/dev/null
printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"stub state"}}\n'
`, 0o755)
	write("hooks/block-bg-bash.pl", shebang+`cat >/dev/null
printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"`+stubs.guardDecision+`"}}\n'
`, 0o755)

	settings, marshalErr := json.Marshal(map[string]any{
		"permissions": map[string]any{"deny": stubs.denyNames},
	})
	if marshalErr != nil {
		t.Fatalf("marshal stub settings: %v", marshalErr)
	}
	settingsPath := write("settings.json", string(settings), 0o644)

	write("projects/-stub-project/session.jsonl",
		`{"type":"assistant","message":{"usage":{"input_tokens":10,"output_tokens":5}}}`+"\n", 0o644)

	return append(hermeticEnv(),
		"SMOKE_SPORE_BIN="+sporeBin,
		"SMOKE_CLAUDE_BIN="+claudeBin,
		"SMOKE_HOOKS_DIR="+filepath.Join(root, "hooks"),
		"SMOKE_SETTINGS="+settingsPath,
		"SMOKE_PROJECTS_DIR="+filepath.Join(root, "projects"),
		"SMOKE_PROJECT=stubproj",
	)
}

// hermeticEnv strips the coordinator/worker variables this session runs
// under. Left in place they would steer the stub hooks at the operator's
// real state directory instead of the sandbox.
func hermeticEnv() []string {
	drop := map[string]bool{
		"SPORE_COORDINATOR_STATE_DIR": true,
		"SKYHELM_STATE_DIR":           true,
		"SPORE_TASK_INBOX":            true,
		"SPORE_TASK_SLUG":             true,
		"WT_PROJECT":                  true,
	}
	out := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); !ok || !drop[k] {
			out = append(out, kv)
		}
	}
	return out
}

func runSmokeScript(t *testing.T, stubs smokeStubs) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", smokeScript)
	cmd.Env = buildSmokeSandbox(t, stubs)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %s: %v\n%s", smokeScript, err, out)
		}
		code = exitErr.ExitCode()
	}
	return string(out), code
}

func TestSmokeScriptExitsZeroWhenEveryCheckHolds(t *testing.T) {
	out, code := runSmokeScript(t, defaultSmokeStubs())
	if code != 0 {
		t.Fatalf("want exit 0 on a healthy sandbox, got %d\n%s", code, out)
	}
	if strings.Contains(out, "CHANGED") {
		t.Fatalf("healthy sandbox reported CHANGED\n%s", out)
	}
}

func TestSmokeScriptExitsNonZeroWhenTheGuardStopsDenying(t *testing.T) {
	stubs := defaultSmokeStubs()
	stubs.guardDecision = "allow"

	out, code := runSmokeScript(t, stubs)
	if code == 0 {
		t.Fatalf("a guard that answers allow must fail the run, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "CHANGED") {
		t.Fatalf("want a CHANGED line naming the guard\n%s", out)
	}
	if !strings.Contains(out, "permissionDecision") {
		t.Fatalf("want the CHANGED line to name permissionDecision\n%s", out)
	}
}

func TestSmokeScriptExitsNonZeroWhenClaudeDropsADeniedToolName(t *testing.T) {
	stubs := defaultSmokeStubs()
	stubs.claudeStrings = removeString(stubs.claudeStrings, "SendMessage")

	out, code := runSmokeScript(t, stubs)
	if code == 0 {
		t.Fatalf("a renamed tool must fail the run, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "CHANGED") {
		t.Fatalf("want a CHANGED line for the missing tool name\n%s", out)
	}
	if !strings.Contains(out, "SendMessage") {
		t.Fatalf("want the CHANGED line to name SendMessage\n%s", out)
	}
}

func TestSmokeScriptExitsNonZeroWhenTheInboxWakeStopsFiring(t *testing.T) {
	stubs := defaultSmokeStubs()
	stubs.watchExit = 0

	out, code := runSmokeScript(t, stubs)
	if code == 0 {
		t.Fatalf("a watch-inbox that no longer wakes must fail the run, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "CHANGED") {
		t.Fatalf("want a CHANGED line for the inbox wake\n%s", out)
	}
}

func TestSmokeScriptExitsNonZeroWhenReplenishNeverReachesReconcile(t *testing.T) {
	stubs := defaultSmokeStubs()
	stubs.replenishLine = "replenish-hook: skipping reconcile (budget advice=ration)"

	out, code := runSmokeScript(t, stubs)
	if code == 0 {
		t.Fatalf("a replenish hook that short-circuits must fail the run, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "CHANGED") {
		t.Fatalf("want a CHANGED line for the skipped reconcile\n%s", out)
	}
}

func TestSmokeScriptExitsNonZeroWhenTheNotificationHookWritesNoPoke(t *testing.T) {
	stubs := defaultSmokeStubs()
	stubs.pokeOnNotify = false

	out, code := runSmokeScript(t, stubs)
	if code == 0 {
		t.Fatalf("a notification hook that writes no poke must fail the run, got exit 0\n%s", out)
	}
	if !strings.Contains(out, "CHANGED") {
		t.Fatalf("want a CHANGED line for the missing poke\n%s", out)
	}
}

func removeString(in []string, drop string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}
