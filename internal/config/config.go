// Package config loads per-machine configuration for tmux-sidebar.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultConfigPath returns the default config file path.
// Example: ~/.config/tmux-sidebar/hidden_sessions
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tmux-sidebar", "hidden_sessions")
}

// Config holds the per-machine configuration for tmux-sidebar.
type Config struct {
	// HiddenSessions is a set of session names to exclude from the sidebar.
	HiddenSessions map[string]struct{}
}

// Load reads the config file at path.
// If the file does not exist, an empty Config is returned (no error).
// Lines starting with '#' and blank lines are ignored.
func Load(path string) (Config, error) {
	cfg := Config{HiddenSessions: map[string]struct{}{}}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cfg.HiddenSessions[line] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}
	return cfg, nil
}

// IsHiddenSession reports whether sessionName should be excluded from the sidebar.
func (c *Config) IsHiddenSession(name string) bool {
	_, ok := c.HiddenSessions[name]
	return ok
}
