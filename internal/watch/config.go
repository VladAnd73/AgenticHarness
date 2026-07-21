package watch

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Enabled bool
	Checks  []string
}

// ReleasesConfig is the [releases] table: repos to watch and the project
// coordinators to poke on any new release.
type ReleasesConfig struct {
	Enabled      bool
	Repos        []string
	Coordinators []string
}

func LoadConfig(project string) (Config, error) {
	sections, err := readWatchToml(project)
	if err != nil {
		return Config{}, err
	}
	// Top-level pr-watch keys live in the anonymous "" section, so the
	// [releases] table cannot leak its own enabled/checks into pr-watch.
	top := sections[""]
	return Config{
		Enabled: top["enabled"] == "true",
		Checks:  parseStringList(top["checks"]),
	}, nil
}

// LoadReleasesConfig reads the [releases] table. A missing file or absent
// section means disabled (the run is a no-op).
func LoadReleasesConfig(project string) (ReleasesConfig, error) {
	sections, err := readWatchToml(project)
	if err != nil {
		return ReleasesConfig{}, err
	}
	rel := sections["releases"]
	return ReleasesConfig{
		Enabled:      rel["enabled"] == "true",
		Repos:        parseStringList(rel["repos"]),
		Coordinators: parseStringList(rel["coordinators"]),
	}, nil
}

// readWatchToml parses watch.toml into a section -> key -> raw-value map. The
// anonymous top-level section is keyed by "". A missing file yields an empty
// (non-nil) map with no error.
func readWatchToml(project string) (map[string]map[string]string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	sections := map[string]map[string]string{"": {}}
	b, err := os.ReadFile(filepath.Join(base, "spore", project, "watch.toml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return sections, nil
		}
		return nil, err
	}
	section := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(stripTOMLComment(line))
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if sections[section] == nil {
				sections[section] = map[string]string{}
			}
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		sections[section][key] = val
	}
	return sections, nil
}

func parseStringList(val string) []string {
	val = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(val), "]"), "[")
	var out []string
	for _, part := range strings.Split(val, ",") {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func stripTOMLComment(line string) string {
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
