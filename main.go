package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ishii1648/tmux-sidebar/internal/doctor"
	"github.com/ishii1648/tmux-sidebar/internal/state"
	"github.com/ishii1648/tmux-sidebar/internal/tmux"
	"github.com/ishii1648/tmux-sidebar/internal/ui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "focus-guard":
			if err := runFocusGuard(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar focus-guard: %v\n", err)
				os.Exit(1)
			}
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
		case "toggle":
			if err := runToggleSidebar(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar toggle: %v\n", err)
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
	if paneOut, err := exec.Command("tmux", "display-message", "-p", "#{pane_id}").Output(); err == nil {
		paneID := strings.TrimSpace(string(paneOut))
		if paneID != "" {
			exec.Command("tmux", "set-option", "-p", "-t", paneID, "window-style", "default").Run()
			defer func() {
				exec.Command("tmux", "set-option", "-p", "-t", paneID, "-u", "window-style").Run()
			}()
		}
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

// runFocusGuard is the after-select-pane hook handler.
// If the currently focused pane has the custom option @pane_role set to
// "sidebar", it immediately switches back to the previously active pane so
// that normal cursor movement skips the sidebar pane.
//
// Intentional focus via "tmux-sidebar focus-sidebar" is allowed: that
// subcommand sets the session option @sidebar_focus_requested=1 before
// calling select-pane, and this guard clears the flag and exits without
// redirecting.
//
// Usage in tmux.conf:
//
//	set-hook -g after-select-pane 'run-shell "tmux-sidebar focus-guard"'
func runFocusGuard() error {
	out, err := exec.Command("tmux", "display-message", "-p", "#{@pane_role}").Output()
	if err != nil {
		// Not inside a tmux session — nothing to do.
		return nil
	}
	if strings.TrimSpace(string(out)) == "sidebar" {
		// Allow intentional focus set by runFocusSidebar.
		flagOut, _ := exec.Command("tmux", "display-message", "-p", "#{@sidebar_focus_requested}").Output()
		if strings.TrimSpace(string(flagOut)) == "1" {
			exec.Command("tmux", "set-option", "-g", "-u", "@sidebar_focus_requested").Run() //nolint:errcheck
			return nil
		}
		return exec.Command("tmux", "select-pane", "-l").Run()
	}
	return nil
}

// runFocusSidebar focuses the sidebar pane in the current window.
// It sets the session option @sidebar_focus_requested so that focus-guard
// allows the focus rather than immediately redirecting back.
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
	if err := exec.Command("tmux", "set-option", "-g", "@sidebar_focus_requested", "1").Run(); err != nil {
		return fmt.Errorf("set flag: %w", err)
	}
	if err := exec.Command("tmux", "select-pane", "-t", sidebarPaneID).Run(); err != nil {
		return fmt.Errorf("select-pane: %w", err)
	}
	return exec.Command("tmux", "set-option", "-g", "-u", "@sidebar_focus_requested").Run()
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
		newOut, err := exec.Command("tmux", "split-window", "-hfb", "-l", "35", "-P", "-F", "#{pane_id}", "tmux-sidebar").Output()
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
	// select-pane してフラグを即座にクリア（after-select-window の focus-guard が
	// フラグを見てサイドバーへのフォーカスを「許可」し続けないようにするため）
	if err := exec.Command("tmux", "set-option", "-g", "@sidebar_focus_requested", "1").Run(); err != nil {
		return fmt.Errorf("set flag: %w", err)
	}
	if err := exec.Command("tmux", "select-pane", "-t", sidebarPaneID).Run(); err != nil {
		return fmt.Errorf("select-pane: %w", err)
	}
	return exec.Command("tmux", "set-option", "-g", "-u", "@sidebar_focus_requested").Run()
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
	newOut, err := exec.Command("tmux", "split-window", "-hfb", "-l", "35", "-P", "-F", "#{pane_id}", "tmux-sidebar").Output()
	if err != nil {
		return fmt.Errorf("split-window: %w", err)
	}
	paneID := strings.TrimSpace(string(newOut))
	if paneID == "" {
		return nil
	}
	return exec.Command("tmux", "set-option", "-p", "-t", paneID, "@pane_role", "sidebar").Run()
}
