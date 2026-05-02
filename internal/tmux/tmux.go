// Package tmux provides types and functions to interact with the tmux process manager.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
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

// PaneInfo holds combined session/window/pane data from a single tmux list-panes query.
type PaneInfo struct {
	SessionID       string
	SessionName     string
	WindowID        string
	WindowIndex     int
	WindowName      string
	PaneID          string
	PaneIndex       int
	PaneNumber      int
	WindowActive    bool      // true if this is the current window in its session
	SessionAttached bool      // true if the session has a client attached
	SessionCreated  time.Time // session creation time (zero if tmux didn't supply it)
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
	// ListAll returns all session/window/pane information in a single tmux call.
	ListAll() ([]PaneInfo, error)
	// KillSession kills the specified tmux session.
	KillSession(sessionName string) error
	// KillWindow kills the specified tmux window.
	KillWindow(sessionName string, windowIndex int) error
	// CapturePane captures the visible content of the active pane in the given target.
	// target may be a session ("name"), a window ("name:index"), or a pane id.
	CapturePane(target string) (string, error)
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
	// TMUX_SIDEBAR_SOCKET lets e2e tests inject an explicit socket name so that
	// all tmux calls go to the isolated test server regardless of the TMUX env var.
	if socket := os.Getenv("TMUX_SIDEBAR_SOCKET"); socket != "" {
		args = append([]string{"-L", socket}, args...)
	}
	cmd := exec.Command("tmux", args...)
	out, err := cmd.Output()
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

// PaneCurrentPath returns the current working directory of the first non-sidebar pane
// in the given window. It avoids returning the sidebar pane's own directory, which would
// cause git/PR info to be fetched for the wrong repository.
func (c *ExecClient) PaneCurrentPath(windowID string) (string, error) {
	out, err := runTmux("list-panes", "-t", windowID, "-F",
		"#{pane_current_path}"+tmuxDelim+"#{@pane_role}")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, tmuxDelim, 2)
		if len(parts) != 2 {
			continue
		}
		if parts[1] == "sidebar" {
			continue
		}
		if parts[0] != "" {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("no non-sidebar pane found in window %s", windowID)
}

// parseAllPanes parses the output of `tmux list-panes -a` with 10 fields.
// The trailing field is #{session_created} as a Unix epoch second; an
// unparseable / missing value yields a zero time.Time so downstream code
// (the "fresh session" highlight) silently treats it as "long ago".
func parseAllPanes(out string) []PaneInfo {
	if out == "" {
		return nil
	}
	var panes []PaneInfo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, tmuxDelim)
		if len(parts) != 10 {
			continue
		}
		winIdx, err := strconv.Atoi(parts[3])
		if err != nil {
			continue
		}
		paneIdx, err := strconv.Atoi(parts[6])
		if err != nil {
			continue
		}
		paneID := parts[5]
		num := 0
		if len(paneID) > 1 && paneID[0] == '%' {
			num, _ = strconv.Atoi(paneID[1:])
		}
		var created time.Time
		if epoch, err := strconv.ParseInt(parts[9], 10, 64); err == nil && epoch > 0 {
			created = time.Unix(epoch, 0)
		}
		panes = append(panes, PaneInfo{
			SessionID:       parts[0],
			SessionName:     parts[1],
			WindowID:        parts[2],
			WindowIndex:     winIdx,
			WindowName:      parts[4],
			PaneID:          paneID,
			PaneIndex:       paneIdx,
			PaneNumber:      num,
			WindowActive:    parts[7] == "1",
			SessionAttached: parts[8] == "1",
			SessionCreated:  created,
		})
	}
	return panes
}

// KillSession kills the specified tmux session.
func (c *ExecClient) KillSession(sessionName string) error {
	_, err := runTmux("kill-session", "-t", sessionName)
	return err
}

// KillWindow kills the specified tmux window.
func (c *ExecClient) KillWindow(sessionName string, windowIndex int) error {
	target := fmt.Sprintf("%s:%d", sessionName, windowIndex)
	_, err := runTmux("kill-window", "-t", target)
	return err
}

// CapturePane captures the visible content of the active pane in target and
// returns it as a string. -p prints to stdout; -J joins wrapped lines so the
// dump reads like the user saw it.
func (c *ExecClient) CapturePane(target string) (string, error) {
	out, err := runTmux("capture-pane", "-p", "-J", "-t", target)
	if err != nil {
		return "", err
	}
	return out, nil
}

// ListAll returns all session/window/pane information in a single tmux list-panes call.
// The result includes WindowActive and SessionAttached flags to identify the currently
// focused window without a separate display-message call. SessionCreated is included
// so the sidebar can highlight sessions created in the last few seconds (the
// "fresh session" affordance that signals dispatch completion without status-line
// notifications).
func (c *ExecClient) ListAll() ([]PaneInfo, error) {
	out, err := runTmux("list-panes", "-a", "-F",
		"#{session_id}"+tmuxDelim+"#{session_name}"+tmuxDelim+"#{window_id}"+tmuxDelim+
			"#{window_index}"+tmuxDelim+"#{window_name}"+tmuxDelim+"#{pane_id}"+tmuxDelim+
			"#{pane_index}"+tmuxDelim+"#{window_active}"+tmuxDelim+"#{session_attached}"+tmuxDelim+
			"#{session_created}")
	if err != nil {
		return nil, err
	}
	return parseAllPanes(out), nil
}
