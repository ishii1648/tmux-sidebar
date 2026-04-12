// Package tmux provides types and functions to interact with the tmux process manager.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// tmuxDelim is used as a field separator in tmux format strings.
// Using \x01 avoids conflicts with characters in session/window names.
const tmuxDelim = "\x01"

// Session represents a tmux session.
type Session struct {
	ID   string // e.g. "$1"
	Name string
}

// Window represents a tmux window within a session.
type Window struct {
	SessionID   string
	SessionName string
	ID          string // e.g. "@1"
	Index       int
	Name        string
}

// Pane represents a tmux pane within a window.
type Pane struct {
	SessionID string
	WindowID  string
	ID        string // e.g. "%101"
	Index     int
	// Number is the numeric part of ID (101 for "%101").
	Number int
}

// CurrentPane holds information about the pane running this process.
type CurrentPane struct {
	SessionID string
	WindowID  string
	PaneID    string
}

// Client is the interface for interacting with tmux.
type Client interface {
	ListSessions() ([]Session, error)
	ListWindows() ([]Window, error)
	ListPanes() ([]Pane, error)
	CurrentPane() (CurrentPane, error)
	SwitchWindow(sessionName string, windowIndex int) error
	// PaneCurrentPath returns the current working directory of the active pane
	// in the given window. windowID should be in tmux window ID format (e.g. "@1").
	PaneCurrentPath(windowID string) (string, error)
}

// ExecClient implements Client by running tmux subcommands via exec.Command.
type ExecClient struct{}

// NewExecClient creates a new ExecClient.
func NewExecClient() *ExecClient {
	return &ExecClient{}
}

// parseSessions parses the output of `tmux list-sessions` using tmuxDelim as separator.
func parseSessions(out string) []Session {
	if out == "" {
		return nil
	}
	var sessions []Session
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, tmuxDelim)
		if len(parts) != 2 {
			continue
		}
		sessions = append(sessions, Session{ID: parts[0], Name: parts[1]})
	}
	return sessions
}

// parseWindows parses the output of `tmux list-windows -a` using tmuxDelim as separator.
func parseWindows(out string) []Window {
	if out == "" {
		return nil
	}
	var windows []Window
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, tmuxDelim)
		if len(parts) != 5 {
			continue
		}
		idx, err := strconv.Atoi(parts[3])
		if err != nil {
			continue
		}
		windows = append(windows, Window{
			SessionID:   parts[0],
			SessionName: parts[1],
			ID:          parts[2],
			Index:       idx,
			Name:        parts[4],
		})
	}
	return windows
}

// parsePanes parses the output of `tmux list-panes -a` using tmuxDelim as separator.
// pane_id is expected in the form "%N"; the numeric part N is stored in Pane.Number.
func parsePanes(out string) []Pane {
	if out == "" {
		return nil
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, tmuxDelim)
		if len(parts) != 4 {
			continue
		}
		paneIdx, err := strconv.Atoi(parts[3])
		if err != nil {
			continue
		}
		paneID := parts[2]
		num := 0
		if len(paneID) > 1 && paneID[0] == '%' {
			num, _ = strconv.Atoi(paneID[1:])
		}
		panes = append(panes, Pane{
			SessionID: parts[0],
			WindowID:  parts[1],
			ID:        paneID,
			Index:     paneIdx,
			Number:    num,
		})
	}
	return panes
}

func runTmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// ListSessions returns all tmux sessions.
func (c *ExecClient) ListSessions() ([]Session, error) {
	out, err := runTmux("list-sessions", "-F", "#{session_id}"+tmuxDelim+"#{session_name}")
	if err != nil {
		return nil, err
	}
	return parseSessions(out), nil
}

// ListWindows returns all windows across all sessions.
func (c *ExecClient) ListWindows() ([]Window, error) {
	out, err := runTmux("list-windows", "-a", "-F",
		"#{session_id}"+tmuxDelim+"#{session_name}"+tmuxDelim+"#{window_id}"+tmuxDelim+"#{window_index}"+tmuxDelim+"#{window_name}")
	if err != nil {
		return nil, err
	}
	return parseWindows(out), nil
}

// ListPanes returns all panes across all sessions and windows.
func (c *ExecClient) ListPanes() ([]Pane, error) {
	out, err := runTmux("list-panes", "-a", "-F",
		"#{session_id}"+tmuxDelim+"#{window_id}"+tmuxDelim+"#{pane_id}"+tmuxDelim+"#{pane_index}")
	if err != nil {
		return nil, err
	}
	return parsePanes(out), nil
}

// CurrentPane returns identity information for the pane running this process.
// It uses the TMUX_PANE environment variable (set by tmux in every pane) to
// target the request directly, avoiding a hang when no tmux client is attached.
func (c *ExecClient) CurrentPane() (CurrentPane, error) {
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		return CurrentPane{}, fmt.Errorf("TMUX_PANE not set")
	}
	out, err := runTmux("display-message", "-p", "-t", paneID,
		"#{session_id}"+tmuxDelim+"#{window_id}"+tmuxDelim+"#{pane_id}")
	if err != nil {
		return CurrentPane{}, err
	}
	parts := strings.Split(out, tmuxDelim)
	if len(parts) != 3 {
		return CurrentPane{}, fmt.Errorf("unexpected display-message output: %q", out)
	}
	return CurrentPane{
		SessionID: parts[0],
		WindowID:  parts[1],
		PaneID:    parts[2],
	}, nil
}

// SwitchWindow switches the tmux client to the given session and window index.
func (c *ExecClient) SwitchWindow(sessionName string, windowIndex int) error {
	target := fmt.Sprintf("%s:%d", sessionName, windowIndex)
	_, err := runTmux("switch-client", "-t", target)
	return err
}

// PaneCurrentPath returns the current working directory of the active pane in the given window.
func (c *ExecClient) PaneCurrentPath(windowID string) (string, error) {
	return runTmux("display-message", "-t", windowID, "-p", "#{pane_current_path}")
}
