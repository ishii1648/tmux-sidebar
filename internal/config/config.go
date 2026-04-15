// Package config loads per-machine configuration for tmux-sidebar.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultSidebarWidth is the default sidebar width in terminal columns.
// Reference: cmux uses 200px; this is the rough cell-count equivalent.
const DefaultSidebarWidth = 40

// MinSidebarWidth is the smallest width tolerated by the UI.
const MinSidebarWidth = 20

// DefaultConfigPath returns the default config file path.
// Example: ~/.config/tmux-sidebar/hidden_sessions
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tmux-sidebar", "hidden_sessions")
}

// WidthConfigPath returns the path to the sidebar width config file.
// Example: ~/.config/tmux-sidebar/width
func WidthConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tmux-sidebar", "width")
}

// Config holds the per-machine configuration for tmux-sidebar.
type Config struct {
	// HiddenSessions is a set of session names to exclude from the sidebar.
	HiddenSessions map[string]struct{}
	// Width is the sidebar width in terminal columns (absolute cell count).
	// Enforced on split-window creation and after tmux client resizes.
	Width int
}

// Load reads the config file at path.
// If the file does not exist, an empty Config is returned (no error).
// Lines starting with '#' and blank lines are ignored.
func Load(path string) (Config, error) {
	cfg := Config{HiddenSessions: map[string]struct{}{}, Width: loadWidth()}

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

// loadWidth reads the sidebar width from TMUX_SIDEBAR_WIDTH or
// ~/.config/tmux-sidebar/width. Falls back to DefaultSidebarWidth when
// unset, unparseable, or below MinSidebarWidth.
func loadWidth() int {
	if env := strings.TrimSpace(os.Getenv("TMUX_SIDEBAR_WIDTH")); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n >= MinSidebarWidth {
			return n
		}
	}
	path := WidthConfigPath()
	if path == "" {
		return DefaultSidebarWidth
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultSidebarWidth
	}
	if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && n >= MinSidebarWidth {
		return n
	}
	return DefaultSidebarWidth
}

// IsHiddenSession reports whether sessionName should be excluded from the sidebar.
func (c *Config) IsHiddenSession(name string) bool {
	_, ok := c.HiddenSessions[name]
	return ok
}
