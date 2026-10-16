package spore

import (
	"fmt"
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
	for _, p := range claudeCodeBindingProblems(readBootstrapConfig(t)) {
		t.Errorf("%s %s", bootstrapConfig, p)
	}
}

// The check above has to turn on what the template does, not on how it
// is spelled. These cases are the ones an independent review built
// against the first version of it: a template with the defect, which
// passed, and two behaviour-preserving refactors, which failed.
func TestClaudeCodeBindingJudgesBehaviourNotSpelling(t *testing.T) {
	good := readBootstrapConfig(t)

	cases := []struct {
		name string
		src  string
		want string // the problem that must be reported; "" means none
	}{
		{"as shipped", good, ""},
		{"binding renamed", renameClaudeCodeBinding(good), ""},
		{"attribute quoted", quoteClaudeCodeAttr(good), ""},
		{
			"both sites on the host nixpkgs",
			bindClaudeCodeToHost(good),
			"does not take claude-code from spore's pin",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(claudeCodeBindingProblems(tc.src), "; ")
			if tc.want == "" && got != "" {
				t.Errorf("rejected a behaviour-preserving template: %s", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("reported %q, want a problem mentioning %q", got, tc.want)
			}
		})
	}
}

// claudeCodeBindingProblems reports, in words that name the defect, every
// way src fails the one-binding-from-spore rule. Empty means the
// template is sound.
func claudeCodeBindingProblems(src string) []string {
	var problems []string

	use := regexp.MustCompile(`claudeCodePackage\s*=\s*([A-Za-z0-9_.-]+)\s*;`).FindStringSubmatch(src)
	if use == nil {
		return append(problems, "sets claudeCodePackage to no plain binding")
	}
	name := use[1]

	packages, ok := systemPackagesBlock(src)
	if !ok {
		return append(problems, "has no terminated environment.systemPackages assignment")
	}
	if !hasNixToken(packages, name) {
		problems = append(problems, fmt.Sprintf("does not install %q in environment.systemPackages, "+
			"so the interactive claude and the fleet's claude can diverge", name))
	}

	value, ok := letBindingValue(src, name)
	if !ok {
		return append(problems, fmt.Sprintf("binds claudeCodePackage to %q, which is no let binding "+
			"this file defines", name))
	}
	if !claudeCodeFromSpore.MatchString(value) {
		problems = append(problems, fmt.Sprintf("binds %q to `%s`, which does not take claude-code "+
			"from spore's pin", name, strings.Join(strings.Fields(value), " ")))
	}

	// Every other mention of the host's claude-code is a second source
	// for the same package, so cut the one legitimate binding out before
	// looking for one.
	if strings.Contains(strings.Replace(src, value, "", 1), "pkgs.claude-code") {
		problems = append(problems, "reaches for the host nixpkgs claude-code outside that binding")
	}

	return problems
}

// claudeCodeFromSpore matches the ways the binding may name spore's
// claude-code: through the file's `sporePkgs` shorthand or spelled out,
// with the attribute bare or quoted.
var claudeCodeFromSpore = regexp.MustCompile(
	`(sporePkgs|inputs\.spore\.packages\.\$\{[^}]*\})\.(claude-code|"claude-code")`)

// letBindingValue returns the text of the `name = ...;` binding, without
// the name, the equals sign or the closing semicolon. It anchors to the
// start of a line so that a use site such as `claudeCodePackage =
// claudeCode;` is never mistaken for the definition.
func letBindingValue(src, name string) (string, bool) {
	loc := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(name) + `[ \t]*=`).FindStringIndex(src)
	if loc == nil {
		return "", false
	}
	end, ok := scanToTerminator(src, loc[1])
	if !ok {
		return "", false
	}
	return src[loc[1] : end-1], true
}

func readBootstrapConfig(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(bootstrapConfig)
	if err != nil {
		t.Fatalf("read %s: %v", bootstrapConfig, err)
	}
	return string(raw)
}

// renameClaudeCodeBinding renames the `claudeCode` binding and both its
// use sites, leaving behaviour identical. The trailing character class
// keeps `claudeCodePackage` out of the rename.
func renameClaudeCodeBinding(src string) string {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_])claudeCode([^A-Za-z0-9_])`).
		ReplaceAllString(src, "${1}cc${2}")
}

// quoteClaudeCodeAttr writes the attribute access with a quoted name,
// which is the same Nix.
func quoteClaudeCodeAttr(src string) string {
	return strings.Replace(src, "sporePkgs.claude-code", `sporePkgs."claude-code"`, 1)
}

// bindClaudeCodeToHost is the defect the test exists to catch: a second
// binding reaching straight back at the host's nixpkgs, both real use
// sites routed through it, and the original binding left behind as dead
// code. `legacyPackages.${...}.claude-code` is deliberate - it puts a
// token between `pkgs` and `claude-code` so a substring search for
// `pkgs.claude-code` does not see it.
func bindClaudeCodeToHost(src string) string {
	out := strings.Replace(src, "  claudeCode = sporePkgs.claude-code",
		"  hostClaude = inputs.nixpkgs.legacyPackages.${pkgs.stdenv.hostPlatform.system}.claude-code;\n"+
			"  claudeCode = sporePkgs.claude-code", 1)
	out = strings.Replace(out, "claudeCodePackage = claudeCode;", "claudeCodePackage = hostClaude;", 1)
	return strings.Replace(out, "\n    claudeCode\n", "\n    hostClaude\n", 1)
}

// systemPackagesBlock returns the text of the environment.systemPackages
// assignment: everything from the marker up to the semicolon that closes
// it. The value is a `[ ... ] ++ (with pkgs; [ ... ])` expression, so
// stopping at the first semicolon or the first `]` both cut it short;
// track bracket depth and stop at the semicolon outside every bracket.
func systemPackagesBlock(src string) (string, bool) {
	const marker = "environment.systemPackages"
	start := strings.Index(src, marker)
	if start < 0 {
		return "", false
	}
	end, ok := scanToTerminator(src, start)
	if !ok {
		return "", false
	}
	return src[start:end], true
}

// scanToTerminator returns the index just past the semicolon that closes
// the expression starting at from, skipping semicolons nested inside
// brackets, parentheses or braces.
func scanToTerminator(src string, from int) (int, bool) {
	depth := 0
	for i := from; i < len(src); i++ {
		switch src[i] {
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
		case ';':
			if depth == 0 {
				return i + 1, true
			}
		}
	}
	return 0, false
}

// hasNixToken reports whether name appears in block as a whole Nix
// token. A substring match would accept `claudeCodeFoo` and, worse,
// would accept the `claude-code` inside `pkgs.claude-code`.
func hasNixToken(block, name string) bool {
	pattern := regexp.MustCompile(`(^|[^A-Za-z0-9_.-])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9_.-])`)
	return pattern.MatchString(block)
}
