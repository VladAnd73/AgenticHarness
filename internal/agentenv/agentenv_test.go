package agentenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const probeValue = "sk-ant-oat01-NOTAREALTOKEN"

func writeEnv(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile is subject to the process umask, so set the mode the
	// test actually asked for.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileMissingFileIsNotAnError(t *testing.T) {
	entries, msgs, err := LoadFile(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("want no entries, got %v", entries)
	}
	if len(msgs) != 0 {
		t.Fatalf("want no messages, got %v", msgs)
	}
}

func TestLoadFileParsesEntriesSkippingCommentsAndBlanks(t *testing.T) {
	path := writeEnv(t, "# a comment\n\nAGENTENV_PROBE=bar\n   \n  SECOND=two\n", 0o600)
	entries, msgs, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("want no messages, got %v", msgs)
	}
	want := []Entry{{Key: "AGENTENV_PROBE", Value: "bar"}, {Key: "SECOND", Value: "two"}}
	if len(entries) != len(want) {
		t.Fatalf("want %v, got %v", want, entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Fatalf("entry %d: want %v, got %v", i, want[i], entries[i])
		}
	}
}

func TestLoadFileStripsMatchedSurroundingQuotes(t *testing.T) {
	path := writeEnv(t, "DOUBLE=\"sk-ant-double\"\nSINGLE='sk-ant-single'\n", 0o600)
	entries, _, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %v", entries)
	}
	if entries[0].Value != "sk-ant-double" {
		t.Errorf("double-quoted: want sk-ant-double, got %q", entries[0].Value)
	}
	if entries[1].Value != "sk-ant-single" {
		t.Errorf("single-quoted: want sk-ant-single, got %q", entries[1].Value)
	}
}

// An unmatched quote is the "half-works" failure this format is
// designed against: a token carrying a stray quote authenticates
// nowhere and the operator has no way to see the difference.
func TestLoadFileRejectsUnmatchedQuote(t *testing.T) {
	path := writeEnv(t, "TOKEN=\""+probeValue+"\n", 0o600)
	_, _, err := LoadFile(path)
	if err == nil {
		t.Fatal("want an error for an unmatched quote, got nil")
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error should name the line number, got %q", err)
	}
	if strings.Contains(err.Error(), probeValue) {
		t.Errorf("error must not echo the value, got %q", err)
	}
}

func TestLoadFileTrimsTrailingWhitespaceOnUnquotedValues(t *testing.T) {
	path := writeEnv(t, "TOKEN=abc123   \nKEPT=\"  padded  \"\n", 0o600)
	entries, _, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries[0].Value != "abc123" {
		t.Errorf("unquoted value should be trimmed, got %q", entries[0].Value)
	}
	if entries[1].Value != "  padded  " {
		t.Errorf("quoted value should be preserved exactly, got %q", entries[1].Value)
	}
}

// No shell is involved, so nothing expands. A token containing $ or a
// backslash must reach the agent byte for byte.
func TestLoadFileDoesNotExpandOrUnescape(t *testing.T) {
	path := writeEnv(t, "A=$HOME\nB=a\\nb\nC=${NOPE}\n", 0o600)
	entries, _, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"$HOME", "a\\nb", "${NOPE}"}
	for i, w := range want {
		if entries[i].Value != w {
			t.Errorf("entry %d: want %q verbatim, got %q", i, w, entries[i].Value)
		}
	}
}

func TestLoadFileMalformedLineNamesLineNumberAndNotTheContent(t *testing.T) {
	path := writeEnv(t, "GOOD=ok\n\nexport TOKEN="+probeValue+"\n", 0o600)
	_, _, err := LoadFile(path)
	if err == nil {
		t.Fatal("want an error for a malformed line, got nil")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error should name line 3, got %q", err)
	}
	if strings.Contains(err.Error(), probeValue) {
		t.Errorf("error must not echo the line content, got %q", err)
	}
}

func TestLoadFileDropsReservedKeysNamingTheKeyNotTheValue(t *testing.T) {
	path := writeEnv(t, "SPORE_PROJECT_ROOT=/tmp/evil-root\nWT_PROJECT=evilproject\nKEPT=fine\n", 0o600)
	entries, msgs, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a reserved key must not abort the load, got %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "KEPT" {
		t.Fatalf("reserved keys should be dropped, got %v", entries)
	}
	joined := strings.Join(msgs, "\n")
	for _, key := range []string{"SPORE_PROJECT_ROOT", "WT_PROJECT"} {
		if !strings.Contains(joined, key) {
			t.Errorf("messages should name %s, got %q", key, joined)
		}
	}
	for _, value := range []string{"/tmp/evil-root", "evilproject"} {
		if strings.Contains(joined, value) {
			t.Errorf("messages must not echo the value %q, got %q", value, joined)
		}
	}
	if !strings.Contains(joined, "line 1") || !strings.Contains(joined, "line 2") {
		t.Errorf("messages should name the line numbers, got %q", joined)
	}
}

func TestLoadFileWarnsOnGroupOrWorldReadableModeWithoutRefusing(t *testing.T) {
	path := writeEnv(t, "TOKEN="+probeValue+"\n", 0o644)
	entries, msgs, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a loose mode must stay a warning, not a refusal, got %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries should still load, got %v", entries)
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "0644") {
		t.Errorf("warning should name the mode, got %q", joined)
	}
	if !strings.Contains(joined, "chmod 600") {
		t.Errorf("warning should say how to fix it, got %q", joined)
	}
	if strings.Contains(joined, probeValue) {
		t.Errorf("warning must not echo the value, got %q", joined)
	}
}

func TestLoadFileDoesNotWarnOnOwnerOnlyMode(t *testing.T) {
	path := writeEnv(t, "TOKEN="+probeValue+"\n", 0o600)
	_, msgs, err := LoadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("0600 should be silent, got %v", msgs)
	}
}

func TestTmuxArgsRendersDashEPairs(t *testing.T) {
	got := TmuxArgs([]Entry{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}})
	want := []string{"-e", "A=1", "-e", "B=2"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func TestTmuxArgsOnNoEntriesIsEmpty(t *testing.T) {
	if got := TmuxArgs(nil); len(got) != 0 {
		t.Fatalf("want no args, got %v", got)
	}
}

func TestPathFollowsXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := Path("myproject")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "spore", "myproject", FileName)
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}
