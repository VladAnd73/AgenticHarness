package spore

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const bootstrapConfig = "bootstrap/flake/configuration.nix"

// The shipped template installs Claude Code twice: once on the
// interactive PATH via environment.systemPackages, and once for the
// fleet unit via services.spore-fleet.claudeCodePackage. Nothing makes
// the two agree. If they ever name different packages, workers run one
// build while every command an operator types runs another, and no
// error is raised either way. Require one binding, named in both
// places, and require it to come from spore's own pin instead of the
// host's nixpkgs revision.
func TestBootstrapTemplateUsesOneClaudeCodeBinding(t *testing.T) {
	src := readBootstrapConfig(t)

	if strings.Contains(src, "pkgs.claude-code") {
		t.Errorf("%s still takes claude-code from the host nixpkgs (pkgs.claude-code); "+
			"the version must come from spore's pin", bootstrapConfig)
	}

	binding := regexp.MustCompile(`claudeCodePackage\s*=\s*([A-Za-z0-9_.-]+)\s*;`).FindStringSubmatch(src)
	if binding == nil {
		t.Fatalf("%s sets claudeCodePackage to no plain binding", bootstrapConfig)
	}
	name := binding[1]

	packages := systemPackagesBlock(t, src)
	if !hasNixToken(packages, name) {
		t.Errorf("environment.systemPackages in %s does not install %q, "+
			"so the interactive claude and the fleet's claude can diverge",
			bootstrapConfig, name)
	}
}

// The binding the previous test pins both uses to still has to resolve
// to spore's packages set. A let binding pointing back at the host's
// nixpkgs would satisfy "one binding" while changing nothing.
func TestBootstrapTemplateTakesClaudeCodeFromSpore(t *testing.T) {
	src := readBootstrapConfig(t)

	want := "claudeCode = sporePkgs.claude-code;"
	if !strings.Contains(src, want) {
		t.Errorf("%s does not bind %q; claude-code must come from the flake's pin",
			bootstrapConfig, want)
	}
}

func readBootstrapConfig(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(bootstrapConfig)
	if err != nil {
		t.Fatalf("read %s: %v", bootstrapConfig, err)
	}
	return string(raw)
}

// systemPackagesBlock returns the text of the environment.systemPackages
// assignment: everything from the marker up to the semicolon that closes
// it. The value is a `[ ... ] ++ (with pkgs; [ ... ])` expression, so
// stopping at the first semicolon or the first `]` both cut it short;
// track bracket depth and stop at the semicolon outside every bracket.
func systemPackagesBlock(t *testing.T, src string) string {
	t.Helper()
	const marker = "environment.systemPackages"
	start := strings.Index(src, marker)
	if start < 0 {
		t.Fatalf("%s has no %s assignment", bootstrapConfig, marker)
	}
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
		case ';':
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	t.Fatalf("%s: %s assignment is unterminated", bootstrapConfig, marker)
	return ""
}

// hasNixToken reports whether name appears in block as a whole Nix
// token. A substring match would accept `claudeCodeFoo` and, worse,
// would accept the `claude-code` inside `pkgs.claude-code`.
func hasNixToken(block, name string) bool {
	pattern := regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_.-])`)
	return pattern.MatchString(block)
}
