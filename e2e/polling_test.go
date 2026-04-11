//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// TestPollingUpdatesState verifies that when a state file is written after the
// sidebar has already started, the badge appears within the next poll cycle
// (≤ ~1 second).
func TestPollingUpdatesState(t *testing.T) {
	env := newTestEnv(t)

	env.newSession("poll-sess")
	env.newWindow("poll-sess", "worker")
	paneNum := env.paneNumber("poll-sess:1")

	// Start the sidebar with no state file for this pane.
	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "worker", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not show 'worker': %v", err)
	}

	// Confirm no [idle] badge is visible yet.
	output := env.capturePane("scratch")
	if strings.Contains(output, "[idle]") {
		t.Fatalf("unexpected [idle] badge before state file written:\n%s", output)
	}

	// Write the state file; the sidebar should pick it up on the next tick.
	env.setupStateFile(paneNum, "idle")
	if err := env.waitForText("scratch", "[idle]", 3*time.Second); err != nil {
		t.Fatalf("badge did not appear after writing state file: %v", err)
	}
}

// TestPollingRemovesState verifies that when a state file is deleted the badge
// disappears on the next poll cycle.
func TestPollingRemovesState(t *testing.T) {
	env := newTestEnv(t)

	env.newSession("rm-sess")
	env.newWindow("rm-sess", "cleanup")
	paneNum := env.paneNumber("rm-sess:1")

	// Start with an idle badge visible.
	env.setupStateFile(paneNum, "idle")
	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "[idle]", 5*time.Second); err != nil {
		t.Fatalf("initial [idle] badge did not appear: %v", err)
	}

	// Remove the state file; badge should disappear within the next poll.
	env.removeStateFile(paneNum)
	if err := env.waitForNoText("scratch", "[idle]", 3*time.Second); err != nil {
		t.Fatalf("badge did not disappear after removing state file: %v", err)
	}
}
