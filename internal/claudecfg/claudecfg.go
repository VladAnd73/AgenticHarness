// Package claudecfg resolves the Claude Code configuration directory a
// given project's agents run under.
//
// The kernel reads that directory from several places -- session
// transcripts, OAuth credentials, the dream corpus, the done-gate's
// evidence, the inbox watchdog's liveness probe -- and until now every
// one of them hard-coded ~/.claude. A project that wants its agents on
// their own directory sets it once, in its own spore.toml, and every
// reader agrees.
//
// Resolution order, and the order is the point:
//
//  1. `[claude] config_dir` in <projectRoot>/spore.toml.
//  2. CLAUDE_CONFIG_DIR in the process environment.
//  3. ~/.claude.
//
// The project setting has to come first because the processes that ask
// are often not the ones being asked about. `spore-fleet-tick` walks
// every project under $HOME from a single shell, and the per-project
// systemd units share one environment template; a resolver that asked
// "what is MY config dir" would hand the same answer to every project
// it judged. The env still decides for a project that configures
// nothing, which keeps the shape the inbox watchdog already shipped.
//
// A project that sets nothing, in a process that exports nothing,
// resolves ~/.claude -- byte for byte what every call site returned
// before this package existed. That holds whatever state the project's
// spore.toml is in: a step of the order that cannot be evaluated is
// skipped, not fatal. Only ConfigDirSetting reports such a failure to
// its caller, for the callers that want it loud.
package claudecfg

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Env is the variable Claude Code itself honours, and the one spore
// seeds into a session it spawns for a project that configured a
// directory.
const Env = "CLAUDE_CONFIG_DIR"

// Dir resolves the config directory for projectRoot. An empty
// projectRoot skips the per-project lookup and resolves from the
// environment alone, for the handful of callers that genuinely have no
// project in hand.
//
// A per-project lookup that fails falls through to the rest of the
// order rather than failing the call. The reason is the requirement
// this package was written under: a project with no [claude] section
// must resolve exactly what it resolved before the package existed.
// Every read site this replaced ignored spore.toml entirely, so an
// unreadable or unparseable one could not fail them; propagating the
// error would hand them a new way to break over a file they never
// cared about. It is reported on stderr instead, because a fallback
// nobody can see masks a typo forever.
func Dir(projectRoot string) (string, error) {
	if projectRoot != "" {
		set, err := ConfigDirSetting(projectRoot)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "%v: ignoring the project setting, "+
				"resolving the config directory from the environment instead\n", err)
		case set != "":
			return set, nil
		}
	}
	if v := os.Getenv(Env); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

// ProjectsDir is the per-session transcript tree under the resolved
// config directory.
func ProjectsDir(projectRoot string) (string, error) {
	base, err := Dir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "projects"), nil
}

// CredentialsPath is the OAuth credential file under the resolved
// config directory.
func CredentialsPath(projectRoot string) (string, error) {
	base, err := Dir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ".credentials.json"), nil
}

// ProjectRoot reports which project the current process belongs to,
// for the call sites that run inside an agent (hooks, the budget
// tracker) and hold no project root of their own. SPORE_PROJECT_ROOT
// is what the kernel seeds into every session it spawns;
// CLAUDE_PROJECT_DIR is what Claude Code seeds; the working directory
// covers an operator typing a command inside a project tree.
//
// A worktree path collapses to the main checkout, because that is
// where the project's spore.toml is maintained and a worktree branched
// before the setting landed would not carry it.
func ProjectRoot() string {
	for _, v := range []string{os.Getenv("SPORE_PROJECT_ROOT"), os.Getenv("CLAUDE_PROJECT_DIR")} {
		if v != "" {
			return mainCheckout(v)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return mainCheckout(cwd)
	}
	return ""
}

func mainCheckout(dir string) string {
	if i := strings.Index(dir, "/.worktrees/"); i >= 0 {
		return dir[:i]
	}
	return dir
}

// ConfigDirSetting returns the directory this project asked for, or ""
// when it asked for nothing. Spawn sites gate on this rather than on
// Dir: a project that configured nothing must get no CLAUDE_CONFIG_DIR
// in its session environment at all, so its sessions keep inheriting
// whatever they inherit today.
func ConfigDirSetting(projectRoot string) (string, error) {
	tomlPath := filepath.Join(projectRoot, "spore.toml")
	b, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("claudecfg: read %s: %w", tomlPath, err)
	}
	raw, warnings, err := parseClaudeTOML(string(b))
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "claudecfg: %s: %s\n", tomlPath, w)
	}
	if err != nil {
		return "", fmt.Errorf("claudecfg: parse %s: %w", tomlPath, err)
	}
	if raw == "" {
		return "", nil
	}
	return expand(raw, projectRoot)
}

// expand turns the configured value into an absolute path: a leading
// ~ against $HOME, anything else relative against the project root, so
// a project can keep its agents' directory inside its own tree.
func expand(v, projectRoot string) (string, error) {
	if v == "~" || strings.HasPrefix(v, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(v, "~"), "/")), nil
	}
	if filepath.IsAbs(v) {
		return filepath.Clean(v), nil
	}
	return filepath.Join(projectRoot, v), nil
}

// maxTOMLLine is how long a single spore.toml line may be before the
// scanner gives up. bufio.Scanner defaults to 64KB, which is plenty
// for any entry a person writes but not for an entry a generator
// wrote, and a line over the ceiling fails the whole parse -- so a
// long value in a section this parser does not even read would lose a
// [claude] setting that is present and perfectly readable. A megabyte
// is far above anything legitimate and still bounds the buffer; past
// it, Dir's fall-through is the backstop.
const maxTOMLLine = 1 << 20

// parseClaudeTOML reads the [claude] section of the same tiny TOML
// subset the rest of the kernel parses: bare or quoted scalars,
// `# comment` lines, blank lines. Sections other than [claude] are
// ignored, which is what makes the setting safe to add to a spore.toml
// that older binaries also read: every other section parser in the
// kernel skips a header it does not recognise.
//
// An unknown key inside [claude] is a warning and nothing more, for
// the reason the [coordinator] parser learned the hard way: a host
// runs several spore versions at once, and failing the parse would
// strip the whole section for the older one.
func parseClaudeTOML(content string) (string, []string, error) {
	var configDir string
	var warnings []string
	inSection := false
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), maxTOMLLine)
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(stripComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = strings.TrimSpace(line[1:len(line)-1]) == "claude"
			continue
		}
		if !inSection {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return "", warnings, fmt.Errorf("line %d: malformed entry %q", lineNum, line)
		}
		key := strings.TrimSpace(line[:eq])
		val := stripQuotes(strings.TrimSpace(line[eq+1:]))
		switch key {
		case "config_dir":
			configDir = val
		default:
			warnings = append(warnings, fmt.Sprintf(
				"line %d: ignoring unknown key %q in [claude]", lineNum, key))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", warnings, err
	}
	return configDir, warnings, nil
}

func stripComment(line string) string {
	inQuote := byte(0)
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case inQuote != 0:
			if ch == inQuote {
				inQuote = 0
			}
		case ch == '"' || ch == '\'':
			inQuote = ch
		case ch == '#':
			return line[:i]
		}
	}
	return line
}

func stripQuotes(v string) string {
	if len(v) >= 2 {
		first, last := v[0], v[len(v)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
