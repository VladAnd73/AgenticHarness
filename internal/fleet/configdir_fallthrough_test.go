package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scenario 4, the whole chain with nothing stubbed between the
// watchdog and the filesystem: runInboxWatchdog -> the real
// agentTranscriptActivity -> claudecfg.Dir -> an unreadable
// spore.toml. A project with no [claude] section keeps its
// transcripts under ~/.claude, so the probe must find them and the
// watchdog must stay armed. Standing down here disables the watchdog
// permanently and logs nothing an operator would see.
func TestInboxWatchdogStaysArmedWhenSporeTOMLIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 0o000 is still readable")
	}
	h := newWatchdogHarness(t)
	h.claim(t, "100-1-1.json", "new thread")
	h.deps.lastActivity = agentTranscriptActivity

	home := t.TempDir()
	t.Setenv("HOME", home)
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	writeTranscript(t, filepath.Join(home, ".claude", "projects", claudeProjectDir(h.root)))

	tomlPath := filepath.Join(h.root, "spore.toml")
	if err := os.WriteFile(tomlPath, []byte("[coordinator]\nbrief = \"role.md\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tomlPath, 0o000); err != nil {
		t.Fatal(err)
	}

	res, err := runInboxWatchdog(h.cfg(), 3, h.deps)
	if err != nil {
		t.Fatalf("runInboxWatchdog: %v", err)
	}
	if strings.Contains(res.Skipped, "liveness") {
		t.Fatalf("watchdog stood down for want of a liveness signal: %+v", res)
	}
	for _, l := range h.logs {
		if strings.Contains(l, "no liveness signal") {
			t.Fatalf("watchdog reported itself inert: %q", l)
		}
	}
}
