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
