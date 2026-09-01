package dream

import (
	"path/filepath"
	"strings"
	"testing"
)

// metaUserLine is a system-injected user entry: a skill body, a hook
// message, a session-start context dump. Real transcripts mark these
// with isMeta or promptSource "system"; the operator never typed them.
func metaUserLine(cwd, ts, text, promptSource string, isMeta bool) string {
	meta := ""
	if isMeta {
		meta = `"isMeta":true,`
	}
	src := ""
	if promptSource != "" {
		src = `"promptSource":"` + promptSource + `",`
	}
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"` + ts + `",` + meta + src +
		`"message":{"role":"user","content":[{"type":"text","text":"` + text + `"}]}}`
}

func toolErrorLine(cwd, ts, text string) string {
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"` + ts + `",` +
		`"message":{"role":"user","content":[{"type":"tool_result","is_error":true,` +
		`"content":"` + text + `"}]}}`
}

func TestBuildDigestKeepsSignalDropsNoise(t *testing.T) {
	root := t.TempDir()
	home := "/home/agent"
	cwd := home + "/proj/.worktrees/fix-a"

	lines := []string{userLine(cwd, "2026-09-01T01:00:00Z", "# Goal")}
	for i := 0; i < 50; i++ {
		lines = append(lines, `{"type":"assistant","cwd":"`+cwd+
			`","timestamp":"2026-09-01T01:01:00Z","message":{"role":"assistant",`+
			`"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/x"}}]}}`)
	}
	lines = append(lines,
		userLine(cwd, "2026-09-01T01:02:00Z", "no, use the kernel flow instead"),
		`{"type":"user","cwd":"`+cwd+`","timestamp":"2026-09-01T01:03:00Z",`+
			`"message":{"role":"user","content":[{"type":"tool_result","is_error":true,`+
			`"content":"bash: fleebnort: command not found"}]}}`)

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl", lines...)
	s := Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}

	d, err := BuildDigest(s, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.OperatorMessages) != 1 ||
		!strings.Contains(d.OperatorMessages[0].Text, "kernel flow") {
		t.Fatalf("operator correction not captured: %+v", d.OperatorMessages)
	}
	if len(d.Failures) != 1 ||
		!strings.Contains(d.Failures[0].Text, "fleebnort") {
		t.Fatalf("failure not captured: %+v", d.Failures)
	}
	out := FormatDigest([]SessionDigest{d})
	if strings.Contains(out, "/x") {
		t.Fatal("successful reads leaked into the digest")
	}
}

func TestBuildDigestReadsTerminalState(t *testing.T) {
	if got := endState("BLOCKED: cannot reach the backend", false); got != "blocked" {
		t.Fatalf("blocked not detected: %q", got)
	}
	if got := endState("all green", true); got != "tokens" {
		t.Fatalf("wrap-up not detected: %q", got)
	}
	if got := endState("DONE, tests pass", false); got != "done" {
		t.Fatalf("done not detected: %q", got)
	}
	if got := endState("still thinking", false); got != "unknown" {
		t.Fatalf("expected unknown, got %q", got)
	}
}

// TestBuildDigestDropsSystemInjectedText pins the shape real transcripts
// actually have: most text-bearing user entries are harness injections
// (skill bodies, task notifications, Stop-hook feedback), not the
// operator speaking. Counting them as operator messages both buries the
// real corrections and blows the digest's size budget.
func TestBuildDigestDropsSystemInjectedText(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl",
		metaUserLine(cwd, "2026-09-01T01:00:00Z",
			"--- status: active slug: fix-a --- # Goal", "typed", false),
		metaUserLine(cwd, "2026-09-01T01:01:00Z",
			"Base directory for this skill: /home/agent/.claude/skills/foo", "", true),
		metaUserLine(cwd, "2026-09-01T01:02:00Z",
			"<task-notification><summary>Monitor event fired</summary></task-notification>",
			"system", false),
		metaUserLine(cwd, "2026-09-01T01:03:00Z",
			"Stop hook feedback: WORKER TOKEN MONITOR (wrap): context 305944 "+
				"tokens >= wrap cap 120000 on tier=unknown.", "", true),
		userLine(cwd, "2026-09-01T01:04:00Z", "actually use the kernel flow"))

	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Brief, "slug: fix-a") {
		t.Fatalf("brief not captured: %q", d.Brief)
	}
	if len(d.OperatorMessages) != 1 ||
		!strings.Contains(d.OperatorMessages[0].Text, "kernel flow") {
		t.Fatalf("want only the operator's own message, got: %+v", d.OperatorMessages)
	}
	out := FormatDigest([]SessionDigest{d})
	if strings.Contains(out, "Base directory for this skill") ||
		strings.Contains(out, "Monitor event fired") {
		t.Fatalf("harness injection leaked into the digest:\n%s", out)
	}
	if d.End != "tokens" {
		t.Fatalf("real token-monitor wrap message not detected, End=%q", d.End)
	}
}

// TestBuildDigestClassifiesRejectedToolUseAsDenial pins the other
// real-transcript surprise: an operator denial or a hook block arrives
// as an is_error tool_result, never as user text, so matching on user
// text alone leaves the Denials bucket permanently empty.
func TestBuildDigestClassifiesRejectedToolUseAsDenial(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl",
		userLine(cwd, "2026-09-01T01:00:00Z", "# Goal"),
		toolErrorLine(cwd, "2026-09-01T01:01:00Z",
			"The user doesn't want to proceed with this tool use. "+
				"The tool use was rejected."),
		toolErrorLine(cwd, "2026-09-01T01:02:00Z",
			"Long-running jobs must run in a tmux window, not Bash run_in_background."),
		toolErrorLine(cwd, "2026-09-01T01:03:00Z", "bash: fleebnort: command not found"))

	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Denials) != 2 {
		t.Fatalf("want 2 denials (operator reject + hook block), got: %+v", d.Denials)
	}
	if len(d.Failures) != 1 ||
		!strings.Contains(d.Failures[0].Text, "fleebnort") {
		t.Fatalf("want only the genuine failure, got: %+v", d.Failures)
	}
}

func TestBuildDigestCountsRepeatedCommands(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/agent/proj/.worktrees/fix-a"

	lines := []string{userLine(cwd, "2026-09-01T01:00:00Z", "# Goal")}
	for i := 0; i < 4; i++ {
		lines = append(lines, `{"type":"assistant","cwd":"`+cwd+
			`","timestamp":"2026-09-01T01:01:00Z","message":{"role":"assistant",`+
			`"content":[{"type":"tool_use","name":"Bash","input":{"command":"just check"}}]}}`)
	}
	lines = append(lines, `{"type":"assistant","cwd":"`+cwd+
		`","timestamp":"2026-09-01T01:02:00Z","message":{"role":"assistant",`+
		`"content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]}}`)

	p := writeTranscript(t, filepath.Join(root, "d"), "s.jsonl", lines...)
	d, err := BuildDigest(Session{Project: "proj", Kind: KindWorker, Slug: "fix-a", Path: p}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.RepeatedCommands) != 1 ||
		d.RepeatedCommands[0].Text != "4x just check" {
		t.Fatalf("repeated command not summarised: %+v", d.RepeatedCommands)
	}
}
