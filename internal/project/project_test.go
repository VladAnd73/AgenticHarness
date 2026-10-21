package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	spore "github.com/versality/spore"
)

func TestNewScaffoldsAFreshDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")

	res, err := New(dir, spore.BundledSkills, spore.BundledCoordinatorRole)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("no git repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "spore-bootstrap", "SKILL.md")); err != nil {
		t.Errorf("skills not installed: %v", err)
	}
	role, err := os.ReadFile(filepath.Join(dir, "bootstrap", "coordinator", "role.md"))
	if err != nil {
		t.Fatalf("role.md: %v", err)
	}
	if string(role) != string(spore.BundledCoordinatorRole) {
		t.Errorf("role.md does not match the bundled role")
	}
	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf(".gitignore: %v", err)
	}
	if !hasLine(string(ignore), ".worktrees/") {
		t.Errorf(".gitignore lacks .worktrees/: %q", ignore)
	}
	out, err := exec.Command("git", "-C", dir, "check-ignore", "-q", ".worktrees/x/file").CombinedOutput()
	if err != nil {
		t.Errorf("git does not ignore .worktrees/: %v %s", err, out)
	}

	if res.Name != "demo" {
		t.Errorf("Name = %q, want demo", res.Name)
	}
	want := `"demo".path = "` + dir + `";`
	if !strings.Contains(res.HostStep, want) {
		t.Errorf("HostStep lacks %q:\n%s", want, res.HostStep)
	}
	if !strings.Contains(res.HostStep, "spore bootstrap") {
		t.Errorf("HostStep does not point at spore bootstrap, which writes CLAUDE.md:\n%s", res.HostStep)
	}
	if !strings.Contains(res.HostStep, "nixos-rebuild switch --flake") {
		t.Errorf("HostStep lacks the rebuild command:\n%s", res.HostStep)
	}
}

func TestNewKeepsExistingFilesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	custom := []byte("# my own role\n")
	if err := os.MkdirAll(filepath.Join(dir, "bootstrap", "coordinator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bootstrap", "coordinator", "role.md"), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("/bin/"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if _, err := New(dir, spore.BundledSkills, spore.BundledCoordinatorRole); err != nil {
			t.Fatalf("New #%d: %v", i, err)
		}
	}

	role, _ := os.ReadFile(filepath.Join(dir, "bootstrap", "coordinator", "role.md"))
	if string(role) != string(custom) {
		t.Errorf("custom role.md overwritten: %q", role)
	}
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(ignore) != "/bin/\n.worktrees/\n" {
		t.Errorf(".gitignore = %q", ignore)
	}
}

func TestNewRejectsNamesTheAttachShellCannotTarget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "has space")
	if _, err := New(dir, spore.BundledSkills, spore.BundledCoordinatorRole); err == nil {
		t.Fatal("New accepted a project name with a space")
	}
}

func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
