package watch

import (
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Enabled bool
	Checks  []string
}

func LoadConfig(project string) (Config, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	b, err := os.ReadFile(filepath.Join(base, "spore", project, "watch.toml"))
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var cfg Config
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "enabled":
			cfg.Enabled = val == "true"
		case "checks":
			cfg.Checks = parseStringList(val)
		}
	}
	return cfg, nil
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
