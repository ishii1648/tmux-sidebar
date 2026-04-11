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
		return exec.Command("tmux", "select-pane", "-l").Run()
	}
	return nil
}
