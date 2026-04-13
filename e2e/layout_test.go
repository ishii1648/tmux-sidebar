//go:build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSidebarWidthFixedAfterPaneKill verifies that when a non-sidebar pane is
// killed in a 3-pane window, cleanup-if-only-sidebar restores the sidebar to
// its fixed 40-column width.
func TestSidebarWidthFixedAfterPaneKill(t *testing.T) {
	env := newTestEnv(t)

	// The scratch session starts with a single pane.
	// Create a 3-pane layout: sidebar (left, 40 cols) | middle | right.
	// Step 1: split the scratch window to create "middle" pane (right of scratch).
	if _, err := env.tmuxCmd("split-window", "-h", "-t", "scratch"); err != nil {
		t.Fatalf("split middle pane: %v", err)
	}
	// Step 2: split again to create "right" pane (right of middle).
	if _, err := env.tmuxCmd("split-window", "-h", "-t", "scratch"); err != nil {
		t.Fatalf("split right pane: %v", err)
	}

	// Identify all pane IDs in the scratch window.
	panesOut, err := env.tmuxCmd("list-panes", "-t", "scratch", "-F", "#{pane_id} #{pane_index}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}

	// Parse pane list into index → pane_id map.
	paneByIndex := map[int]string{}
	for _, line := range strings.Split(strings.TrimSpace(panesOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		idx, _ := strconv.Atoi(fields[1])
		paneByIndex[idx] = fields[0]
	}

	// Pane index 1 = leftmost (will be our sidebar), index 2 = middle, index 3 = right.
	sidebarPaneID := paneByIndex[1]
	middlePaneID := paneByIndex[2]
	if sidebarPaneID == "" || middlePaneID == "" {
		t.Fatalf("could not identify sidebar/middle panes: %v", paneByIndex)
	}

	// Mark the leftmost pane as the sidebar.
	if _, err := env.tmuxCmd("set-option", "-p", "-t", sidebarPaneID, "@pane_role", "sidebar"); err != nil {
		t.Fatalf("set @pane_role: %v", err)
	}

	// Resize the sidebar to 40 columns (simulating split-window -l 40).
	if _, err := env.tmuxCmd("resize-pane", "-t", sidebarPaneID, "-x", "40"); err != nil {
		t.Fatalf("initial resize-pane: %v", err)
	}

	// Confirm the sidebar starts at 40 columns.
	widthBefore, err := env.paneWidth(sidebarPaneID)
	if err != nil {
		t.Fatalf("pane width before: %v", err)
	}
	if widthBefore != 40 {
		t.Fatalf("expected sidebar width 40 before kill, got %d", widthBefore)
	}

	// Kill the middle pane — tmux will redistribute space and may expand the sidebar.
	if _, err := env.tmuxCmd("kill-pane", "-t", middlePaneID); err != nil {
		t.Fatalf("kill-pane middle: %v", err)
	}

	// Small pause to let tmux redistribute pane widths.
	time.Sleep(200 * time.Millisecond)

	// Run cleanup-if-only-sidebar which should also restore sidebar width.
	binary := builtBinary(t)
	cmd := exec.Command(binary, "cleanup-if-only-sidebar")
	cmd.Env = append(cmd.Environ(), fmt.Sprintf("TMUX=%s,0,0", env.socket))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cleanup-if-only-sidebar: %v\n%s", err, out)
	}

	// Verify sidebar width is restored to 40.
	widthAfter, err := env.paneWidth(sidebarPaneID)
	if err != nil {
		t.Fatalf("pane width after: %v", err)
	}
	if widthAfter != 40 {
		t.Errorf("expected sidebar width 40 after cleanup-if-only-sidebar, got %d", widthAfter)
	}
}

// paneWidth returns the current width of the given pane ID.
func (e *testEnv) paneWidth(paneID string) (int, error) {
	out, err := e.tmuxCmd("display-message", "-p", "-t", paneID, "#{pane_width}")
	if err != nil {
		return 0, err
	}
	w, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parse pane_width %q: %w", out, err)
	}
	return w, nil
}
