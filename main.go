package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ishii1648/tmux-sidebar/internal/doctor"
	"github.com/ishii1648/tmux-sidebar/internal/state"
	"github.com/ishii1648/tmux-sidebar/internal/tmux"
	"github.com/ishii1648/tmux-sidebar/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Println("tmux-sidebar", version)
			return
		case "--help", "-h":
			fmt.Print(`Usage: tmux-sidebar [subcommand]

Subcommands:
  (none)                    Start the TUI sidebar
  close                     Close sidebar if open
  toggle                    Open sidebar if closed, close if open
  focus-or-open             Focus sidebar if open, open if closed
  focus-sidebar             Move focus to the sidebar pane
  select-pane (L|R)         Select pane in direction, skipping the sidebar
  ensure-not-focused        If sidebar is focused, move focus to another pane
  cleanup-if-only-sidebar   Kill window if only the sidebar pane remains
  doctor [--yes]            Check tmux configuration; --yes to auto-apply fixes
  version                   Print version

`)
			return
		case "focus-sidebar":
			if err := runFocusSidebar(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar focus-sidebar: %v\n", err)
				os.Exit(1)
			}
			return
		case "focus-or-open":
			if err := runFocusOrOpen(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar focus-or-open: %v\n", err)
				os.Exit(1)
			}
			return
		case "close":
			if err := runCloseSidebar(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar close: %v\n", err)
				os.Exit(1)
			}
			return
		case "toggle":
			if err := runToggleSidebar(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar toggle: %v\n", err)
				os.Exit(1)
			}
			return
		case "select-pane":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "tmux-sidebar select-pane: requires direction (L or R)")
				os.Exit(1)
			}
			if err := runSelectPane(os.Args[2]); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar select-pane: %v\n", err)
				os.Exit(1)
			}
			return
		case "ensure-not-focused":
			if err := runEnsureNotFocused(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar ensure-not-focused: %v\n", err)
				os.Exit(1)
			}
			return
		case "cleanup-if-only-sidebar":
			if err := runCleanupIfOnlySidebar(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar cleanup-if-only-sidebar: %v\n", err)
				os.Exit(1)
			}
			return
		case "doctor":
			autoApply := len(os.Args) > 2 && os.Args[2] == "--yes"
			if err := doctor.Run(autoApply); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar doctor: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	// Determine pane width: prefer $COLUMNS, fall back to 80.
	width := 80
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			width = n
		}
	}

	tc := tmux.NewExecClient()
	// TMUX_SIDEBAR_STATE_DIR overrides the default state directory.
	// Useful for testing or non-standard environments.
	sr := state.NewFSReader(os.Getenv("TMUX_SIDEBAR_STATE_DIR"))

	model := ui.New(tc, sr, width)

	// Prevent tmux from greying out the sidebar pane when it loses focus.
	// window-style is set at pane level so only this pane is affected; the
	// override is removed when the program exits.
	// Use TMUX_PANE env var instead of display-message to get the correct pane ID.
	// display-message without -t returns the current CLIENT's active pane, not this pane.
	paneID := os.Getenv("TMUX_PANE")
	if paneID != "" {
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-style", "default").Run()
		exec.Command("tmux", "set-option", "-p", "-t", paneID, "@pane_role", "sidebar").Run()
		defer func() {
			exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "window-style").Run()
		}()
	}

	var opts []tea.ProgramOption
	// TMUX_SIDEBAR_NO_ALT_SCREEN disables alt-screen mode (used in E2E tests
	// so that tmux capture-pane can read the sidebar output directly).
	if os.Getenv("TMUX_SIDEBAR_NO_ALT_SCREEN") == "" {
		opts = append(opts, tea.WithAltScreen())
	}
	// Enable terminal focus events so the sidebar can show active/inactive state.
	// Requires `set-option -g focus-events on` in tmux.conf.
	opts = append(opts, tea.WithReportFocus())

	p := tea.NewProgram(model, opts...)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tmux-sidebar: %v\n", err)
		os.Exit(1)
	}
}

// runFocusSidebar focuses the sidebar pane in the current window.
//
// Usage in tmux.conf (bind to any key, e.g. via a terminal-emulator mapping):
//
//	bind-key -n <key> run-shell 'tmux-sidebar focus-sidebar'
func runFocusSidebar() error {
	out, err := exec.Command("tmux", "list-panes", "-F", "#{pane_id} #{@pane_role}").Output()
	if err != nil {
		return fmt.Errorf("list-panes: %w", err)
	}
	var sidebarPaneID string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "sidebar" {
			sidebarPaneID = parts[0]
			break
		}
	}
	if sidebarPaneID == "" {
		// No sidebar in this window — nothing to do.
		return nil
	}
	return exec.Command("tmux", "select-pane", "-t", sidebarPaneID).Run()
}

// runFocusOrOpen focuses the sidebar pane if it exists, or opens a new one and
// focuses it if it does not.
//
// Usage in tmux.conf (bind to any key without requiring the tmux prefix):
//
//	bind-key -n <key> run-shell 'tmux-sidebar focus-or-open'
func runFocusOrOpen() error {
	out, err := exec.Command("tmux", "list-panes", "-F", "#{pane_id} #{@pane_role}").Output()
	if err != nil {
		// Not inside a tmux session — nothing to do.
		return nil
	}
	var sidebarPaneID string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "sidebar" {
			sidebarPaneID = parts[0]
			break
		}
	}
	if sidebarPaneID == "" {
		// Sidebar is closed → open it.
		newOut, err := exec.Command("tmux", "split-window", "-hfb", "-l", "40", "-P", "-F", "#{pane_id}", "tmux-sidebar").Output()
		if err != nil {
			return fmt.Errorf("split-window: %w", err)
		}
		sidebarPaneID = strings.TrimSpace(string(newOut))
		if sidebarPaneID == "" {
			return nil
		}
		if err := exec.Command("tmux", "set-option", "-p", "-t", sidebarPaneID, "@pane_role", "sidebar").Run(); err != nil {
			return fmt.Errorf("set pane_role: %w", err)
		}
	}
	// Sidebar is open → focus it.
	return exec.Command("tmux", "select-pane", "-t", sidebarPaneID).Run()
}

// runToggleSidebar opens the sidebar if it does not exist, or closes it if it does.
//
// Usage in tmux.conf (bind to any key without requiring the tmux prefix):
//
//	bind-key -n C-s run-shell 'tmux-sidebar toggle'
func runToggleSidebar() error {
	out, err := exec.Command("tmux", "list-panes", "-F", "#{pane_id} #{@pane_role}").Output()
	if err != nil {
		// Not inside a tmux session — nothing to do.
		return nil
	}
	var sidebarPaneID string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "sidebar" {
			sidebarPaneID = parts[0]
			break
		}
	}
	if sidebarPaneID != "" {
		// Sidebar is open → close it.
		return exec.Command("tmux", "kill-pane", "-t", sidebarPaneID).Run()
	}
	// Sidebar is closed → open it.
	newOut, err := exec.Command("tmux", "split-window", "-hfb", "-l", "40", "-P", "-F", "#{pane_id}", "tmux-sidebar").Output()
	if err != nil {
		return fmt.Errorf("split-window: %w", err)
	}
	paneID := strings.TrimSpace(string(newOut))
	if paneID == "" {
		return nil
	}
	return exec.Command("tmux", "set-option", "-p", "-t", paneID, "@pane_role", "sidebar").Run()
}

// runCloseSidebar closes the sidebar pane in the current window, if one exists.
func runCloseSidebar() error {
	out, err := exec.Command("tmux", "list-panes", "-F", "#{pane_id} #{@pane_role}").Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == "sidebar" {
			return exec.Command("tmux", "kill-pane", "-t", parts[0]).Run()
		}
	}
	return nil
}

// runSelectPane selects a pane in the given direction (L or R), skipping the sidebar.
//
// Usage in tmux.conf:
//
//	bind h run-shell 'tmux-sidebar select-pane L'
//	bind l run-shell 'tmux-sidebar select-pane R'
func runSelectPane(direction string) error {
	if direction != "L" && direction != "R" {
		return fmt.Errorf("direction must be L or R")
	}
	// Collect panes with their x-positions.
	out, err := exec.Command("tmux", "list-panes", "-F", "#{pane_id} #{pane_left} #{pane_active} #{@pane_role}").Output()
	if err != nil {
		return nil
	}
	type paneInfo struct {
		id     string
		left   int
		active bool
		role   string
	}
	var panes []paneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		left, _ := strconv.Atoi(parts[1])
		role := ""
		if len(parts) >= 4 {
			role = parts[3]
		}
		panes = append(panes, paneInfo{
			id:     parts[0],
			left:   left,
			active: parts[2] == "1",
			role:   role,
		})
	}
	// Sort by horizontal position so index-based L/R movement is spatially correct.
	sort.Slice(panes, func(i, j int) bool { return panes[i].left < panes[j].left })

	// Build a list of non-sidebar panes only.
	var targets []paneInfo
	for _, p := range panes {
		if p.role != "sidebar" {
			targets = append(targets, p)
		}
	}

	// Find the currently active pane among non-sidebar panes.
	activeIdx := -1
	for i, p := range targets {
		if p.active {
			activeIdx = i
			break
		}
	}
	if activeIdx == -1 {
		// Active pane is the sidebar — fall back to normal select-pane.
		return exec.Command("tmux", "select-pane", "-"+direction).Run()
	}

	var nextIdx int
	if direction == "L" {
		nextIdx = activeIdx - 1
	} else {
		nextIdx = activeIdx + 1
	}
	if nextIdx < 0 || nextIdx >= len(targets) {
		// Already at the edge — do nothing.
		return nil
	}
	return exec.Command("tmux", "select-pane", "-t", targets[nextIdx].id).Run()
}

// runEnsureNotFocused moves focus away from the sidebar pane if it is currently focused.
// Intended to be called from the after-select-window hook so that switching windows
// never leaves the cursor on the sidebar.
//
// Usage in tmux.conf:
//
//	set-hook -g after-select-window 'run-shell "tmux-sidebar ensure-not-focused"'
func runEnsureNotFocused() error {
	// Check whether the active pane is the sidebar.
	out, err := exec.Command("tmux", "display-message", "-p", "#{@pane_role}").Output()
	if err != nil || strings.TrimSpace(string(out)) != "sidebar" {
		return nil
	}
	// Active pane is the sidebar — select the first non-sidebar pane.
	panesOut, err := exec.Command("tmux", "list-panes", "-F", "#{pane_id} #{@pane_role}").Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(panesOut)), "\n") {
		parts := strings.Fields(line)
		role := ""
		if len(parts) >= 2 {
			role = parts[1]
		}
		if role != "sidebar" {
			return exec.Command("tmux", "select-pane", "-t", parts[0]).Run()
		}
	}
	return nil
}

// runCleanupIfOnlySidebar scans all windows across all sessions and kills any window
// whose only remaining pane is the sidebar.
//
// Background: after-kill-pane / pane-exited hooks fire in the context of the active
// client window, not the window where the pane was removed, so #{window_id} cannot
// be used to identify the affected window reliably. Scanning all windows is the
// simplest correct approach — window counts are typically small.
//
// Usage in tmux.conf:
//
//	set-hook -g pane-exited      'run-shell "tmux-sidebar cleanup-if-only-sidebar"'
//	set-hook -g after-kill-pane  'run-shell "tmux-sidebar cleanup-if-only-sidebar"'
func runCleanupIfOnlySidebar() error {
	// List all windows across all sessions.
	windowsOut, err := exec.Command("tmux", "list-windows", "-a", "-F", "#{window_id}").Output()
	if err != nil {
		return nil
	}
	for _, wid := range strings.Split(strings.TrimSpace(string(windowsOut)), "\n") {
		if wid == "" {
			continue
		}
		// Use pane_id so each non-empty line represents exactly one pane,
		// regardless of whether @pane_role is set (unset role appears as empty string).
		panesOut, err := exec.Command("tmux", "list-panes", "-t", wid, "-F", "#{pane_id} #{@pane_role}").Output()
		if err != nil {
			continue
		}
		nonSidebarCount := 0
		for _, line := range strings.Split(string(panesOut), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Fields(line)
			role := ""
			if len(parts) >= 2 {
				role = parts[1]
			}
			if role != "sidebar" {
				nonSidebarCount++
			}
		}
		if nonSidebarCount == 0 {
			exec.Command("tmux", "kill-window", "-t", wid).Run()
		}
	}
	return nil
}
