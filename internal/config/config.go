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

// DefaultConfigPath returns the default hidden-sessions config file path.
// Example: ~/.config/tmux-sidebar/hidden_sessions
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tmux-sidebar", "hidden_sessions")
}

// PinnedConfigPath returns the path to the pinned-sessions config file.
// Example: ~/.config/tmux-sidebar/pinned_sessions
func PinnedConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tmux-sidebar", "pinned_sessions")
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
	// PinnedSessions is the ordered list of session names to pin to the top
	// of the sidebar. Order in the file equals display order.
	PinnedSessions []string
	// pinnedIndex maps a session name to its index in PinnedSessions for O(1) lookup.
	pinnedIndex map[string]int
	// Width is the sidebar width in terminal columns (absolute cell count).
	// Enforced on split-window creation and after tmux client resizes.
	Width int
}

// Load reads the config files. path points at the hidden_sessions file; the
// pinned_sessions file is read from the same directory. Missing files yield
// empty values (no error). Lines starting with '#' and blank lines are ignored.
func Load(path string) (Config, error) {
	cfg := Config{
		HiddenSessions: map[string]struct{}{},
		pinnedIndex:    map[string]int{},
		Width:          loadWidth(),
	}

	hidden, err := readListFile(path)
	if err != nil {
		return cfg, err
	}
	for _, name := range hidden {
		cfg.HiddenSessions[name] = struct{}{}
	}

	pinnedPath := pinnedPathFromHidden(path)
	if pinnedPath != "" {
		pinned, err := readListFile(pinnedPath)
		if err != nil {
			return cfg, err
		}
		cfg.setPinned(pinned)
	}

	return cfg, nil
}

// pinnedPathFromHidden derives the pinned_sessions path from a given
// hidden_sessions path. Used so callers that already supply hiddenPath get the
// matching pinned file without a separate lookup.
func pinnedPathFromHidden(hiddenPath string) string {
	if hiddenPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(hiddenPath), "pinned_sessions")
}

// readListFile reads a 1-entry-per-line file, ignoring blank lines and '#'
// comments. A missing file yields an empty slice (no error).
func readListFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return out, nil
}

// setPinned replaces the pinned slice and rebuilds the lookup index.
// Duplicate entries keep the first occurrence's index.
func (c *Config) setPinned(names []string) {
	c.PinnedSessions = c.PinnedSessions[:0]
	c.pinnedIndex = map[string]int{}
	for _, name := range names {
		if _, dup := c.pinnedIndex[name]; dup {
			continue
		}
		c.pinnedIndex[name] = len(c.PinnedSessions)
		c.PinnedSessions = append(c.PinnedSessions, name)
	}
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

// IsPinnedSession reports whether name is in the pinned set.
func (c *Config) IsPinnedSession(name string) bool {
	_, ok := c.pinnedIndex[name]
	return ok
}

// PinnedOrder returns the 0-based position of name in PinnedSessions, or -1
// when name is not pinned. Used by the UI to sort pinned sessions in file
// order regardless of tmux enumeration order.
func (c *Config) PinnedOrder(name string) int {
	if i, ok := c.pinnedIndex[name]; ok {
		return i
	}
	return -1
}

