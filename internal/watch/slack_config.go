package watch

// SlackConfig is the [slack] table: which channel this project watches for
// new threads. One channel per project - no list, no second consumer yet.
type SlackConfig struct {
	Enabled   bool
	ChannelID string
}

// LoadSlackConfig reads the [slack] table. A missing file or absent section
// means disabled (the run is a no-op).
func LoadSlackConfig(project string) (SlackConfig, error) {
	sections, err := readWatchToml(project)
	if err != nil {
		return SlackConfig{}, err
	}
	s := sections["slack"]
	return SlackConfig{
		Enabled:   s["enabled"] == "true",
		ChannelID: parseScalarString(s["channel_id"]),
	}, nil
}
