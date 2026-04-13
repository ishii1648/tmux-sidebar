package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"
	"github.com/ishii1648/tmux-sidebar/internal/config"
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
  cleanup-if-only-sidebar   Kill window if only the sidebar pane remains
  restart                   Restart sidebar in all tmux windows
  doctor [--yes]            Check tmux configuration; --yes to auto-apply fixes
  version                   Print version

`)
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
		case "cleanup-if-only-sidebar":
			if err := runCleanupIfOnlySidebar(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar cleanup-if-only-sidebar: %v\n", err)
				os.Exit(1)
			}
			return
		case "restart":
			if err := runRestart(); err != nil {
				fmt.Fprintf(os.Stderr, "tmux-sidebar restart: %v\n", err)
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

	// Load per-machine config (~/.config/tmux-sidebar/hidden_sessions).
	cfg, err := config.Load(config.DefaultConfigPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-sidebar: config: %v\n", err)
		cfg = config.Config{}
	}

	// Determine our own window ID once at startup; it never changes while running.
	currentWinID := ""
	if cur, err := tc.CurrentPane(); err == nil {
		currentWinID = cur.WindowID
	}

	model := ui.New(tc, sr, width, currentWinID, cfg)

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

	// Write PID file so tmux hooks can send SIGUSR1 to notify of window changes.
	pidFile := ""
	if paneID != "" {
		pidFile = "/tmp/tmux-sidebar-" + strings.TrimPrefix(paneID, "%") + ".pid"
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
		defer os.Remove(pidFile)

		// Clean up PID file on SIGHUP/SIGTERM (kill-pane sends SIGHUP).
		// defer alone is insufficient because the process exits before defers run.
		cleanupCh := make(chan os.Signal, 1)
		signal.Notify(cleanupCh, syscall.SIGHUP, syscall.SIGTERM)
		go func() {
			<-cleanupCh
			os.Remove(pidFile)
			os.Exit(0)
		}()
		defer signal.Stop(cleanupCh)
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

	// fsnotify: watch the state directory and forward changes to bubbletea.
	stateDir := os.Getenv("TMUX_SIDEBAR_STATE_DIR")
	if stateDir == "" {
		stateDir = state.DefaultStateDir
	}
	if watcher, err := fsnotify.NewWatcher(); err == nil {
		// Ensure the directory exists before watching (hooks may not have run yet).
		_ = os.MkdirAll(stateDir, 0o755)
		if watchErr := watcher.Add(stateDir); watchErr == nil {
			go func() {
				defer watcher.Close()
				for {
					select {
					case _, ok := <-watcher.Events:
						if !ok {
							return
						}
						p.Send(ui.StateChangedMsg{})
					case _, ok := <-watcher.Errors:
						if !ok {
							return
						}
					}
				}
			}()
		} else {
			watcher.Close()
		}
	}

	// SIGUSR1: sent by tmux hooks when windows are added/removed.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	go func() {
		for range sigCh {
			p.Send(ui.TmuxChangedMsg{})
		}
	}()
	defer signal.Stop(sigCh)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tmux-sidebar: %v\n", err)
		os.Exit(1)
	}
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
	winOut, err := exec.Command("tmux", "display-message", "-p", "#{window_id}").Output()
	if err != nil {
		return fmt.Errorf("display-message: %w", err)
	}
	winID := strings.TrimSpace(string(winOut))

	if sidebarPaneID == "" {
		// Sidebar is closed → open it.
		if err := exec.Command("tmux", "split-window", "-hfb", "-l", "40", "-t", winID, "tmux-sidebar").Run(); err != nil {
			return fmt.Errorf("split-window: %w", err)
		}
		// -hfb always places the new pane at the leftmost position in the window.
		// Use {left} to select it reliably without depending on pane ID retrieval,
		// which is unreliable when invoked via run-shell.
		return exec.Command("tmux", "select-pane", "-t", winID+".{left}").Run()
	}
	// Sidebar is open → focus it.
	return exec.Command("tmux", "select-pane", "-t", winID+"."+sidebarPaneID).Run()
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

// runRestart kills the sidebar pane in every tmux window and re-creates them.
// This is useful after upgrading the tmux-sidebar binary.
//
// Usage:
//
//	tmux-sidebar restart
func runRestart() error {
	// List all panes across all windows to find sidebar panes.
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{window_id} #{pane_id} #{@pane_role}").Output()
	if err != nil {
		return nil
	}

	// Collect window IDs that have a sidebar, and kill the sidebar panes.
	windowsWithSidebar := []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		windowID, paneID, role := parts[0], parts[1], parts[2]
		if role != "sidebar" {
			continue
		}
		exec.Command("tmux", "kill-pane", "-t", paneID).Run()
		if !seen[windowID] {
			seen[windowID] = true
			windowsWithSidebar = append(windowsWithSidebar, windowID)
		}
	}

	if len(windowsWithSidebar) == 0 {
		fmt.Println("no sidebar panes found")
		return nil
	}

	// Re-create a sidebar in each window that had one.
	for _, wid := range windowsWithSidebar {
		newOut, err := exec.Command("tmux", "split-window", "-hfb", "-l", "40", "-t", wid, "-P", "-F", "#{pane_id}", "tmux-sidebar").Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to open sidebar in window %s: %v\n", wid, err)
			continue
		}
		newPaneID := strings.TrimSpace(string(newOut))
		if newPaneID != "" {
			exec.Command("tmux", "set-option", "-p", "-t", newPaneID, "@pane_role", "sidebar").Run()
		}
	}

	fmt.Printf("restarted %d sidebar(s)\n", len(windowsWithSidebar))
	return nil
}
