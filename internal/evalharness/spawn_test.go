package evalharness

import (
	"context"
	"strings"
	"testing"
)

func TestClaudeSpawnerBuildsAHeadlessBypassPermissionsCommand(t *testing.T) {
	var captured []string
	s := ClaudeSpawner{
		runCommand: func(ctx context.Context, dir string, env []string, args []string) (string, int, error) {
			captured = args
			return "ok", 0, nil
		},
	}
	req := SpawnRequest{Prompt: "hello world", WorkDir: "/tmp/somewhere"}
	res, err := s.Spawn(context.Background(), req)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Stdout != "ok" || res.ExitCode != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "--print") && !strings.Contains(joined, "-p") {
		t.Fatalf("expected a print/headless flag in args, got %v", captured)
	}
	if !strings.Contains(joined, "bypassPermissions") {
		t.Fatalf("expected an unattended permission mode in args, got %v", captured)
	}
	if captured[len(captured)-1] != "hello world" {
		t.Fatalf("expected the prompt as the last arg, got %v", captured)
	}
}

// TestClaudeSpawnerIncludesExtraArgsBeforeThePrompt covers the
// full-session scenario's need for flags ClaudeSpawner's fixed argv
// does not include (--output-format stream-json to capture a
// transcript, --setting-sources project to skip this host's own
// ~/.claude/settings.json). The prompt must still end up last: it is a
// positional arg, not a flag value.
func TestClaudeSpawnerIncludesExtraArgsBeforeThePrompt(t *testing.T) {
	var captured []string
	s := ClaudeSpawner{
		runCommand: func(ctx context.Context, dir string, env []string, args []string) (string, int, error) {
			captured = args
			return "ok", 0, nil
		},
	}
	req := SpawnRequest{
		Prompt:    "hello world",
		WorkDir:   "/tmp/somewhere",
		ExtraArgs: []string{"--output-format", "stream-json", "--verbose", "--setting-sources", "project"},
	}
	if _, err := s.Spawn(context.Background(), req); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "--output-format stream-json") {
		t.Fatalf("expected extra args in argv, got %v", captured)
	}
	if !strings.Contains(joined, "--setting-sources project") {
		t.Fatalf("expected extra args in argv, got %v", captured)
	}
	if captured[len(captured)-1] != "hello world" {
		t.Fatalf("expected the prompt to remain the last arg, got %v", captured)
	}
}

// TestClaudeSpawnerSeparatesThePromptWithDashDash guards against a real
// bug this task's fixture hit live: a worker-shaped task body starts
// with YAML frontmatter ("---\nstatus: active\n..."), and without a
// bare "--" argument before it, claude's own CLI parser reads the
// leading "---" as an unknown option and exits before doing anything.
// Production's real spawn (internal/task/lifecycle.go, confirmed live
// by inspecting a running coordinator's argv) inserts exactly this "--"
// before the task body; ClaudeSpawner must match it.
func TestClaudeSpawnerSeparatesThePromptWithDashDash(t *testing.T) {
	var captured []string
	s := ClaudeSpawner{
		runCommand: func(ctx context.Context, dir string, env []string, args []string) (string, int, error) {
			captured = args
			return "ok", 0, nil
		},
	}
	req := SpawnRequest{Prompt: "---\nstatus: active\n---\n\nbody", WorkDir: "/tmp/somewhere"}
	if _, err := s.Spawn(context.Background(), req); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(captured) < 2 {
		t.Fatalf("expected at least a separator and the prompt, got %v", captured)
	}
	if captured[len(captured)-2] != "--" {
		t.Fatalf("expected \"--\" immediately before the prompt, got %v", captured)
	}
	if captured[len(captured)-1] != req.Prompt {
		t.Fatalf("expected the prompt as the last arg, got %v", captured)
	}
}
