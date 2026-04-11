//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestCursorMovement verifies that j/k keys move the ▶ cursor marker between
// window rows, skipping session-header rows.
func TestCursorMovement(t *testing.T) {
	env := newTestEnv(t)

	// Add a session with two named windows so there are several rows to navigate.
	env.newSession("nav")
	env.newWindow("nav", "alpha")
	env.newWindow("nav", "beta")

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "Sessions", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not start: %v", err)
	}

	// Initial cursor is at index 0 (scratch session header), so ▶ is not shown.
	// Press j once: cursor advances to the first window item.
	env.sendKeys("scratch", "j")
	if err := env.waitForText("scratch", "▶", 2*time.Second); err != nil {
		t.Fatalf("▶ did not appear after first j: %v", err)
	}

	snap1 := env.capturePane("scratch")

	// Press j again: cursor advances to the next window item.
	env.sendKeys("scratch", "j")
	time.Sleep(300 * time.Millisecond) // allow re-render
	snap2 := env.capturePane("scratch")
	if snap1 == snap2 {
		t.Error("cursor did not move after second j")
	}

	// Press k: cursor should move back to the previous window item.
	env.sendKeys("scratch", "k")
	time.Sleep(300 * time.Millisecond)
	snap3 := env.capturePane("scratch")
	if snap2 == snap3 {
		t.Error("cursor did not move back after k")
	}
}

// TestPassiveMode verifies that q enters passive mode (shows "[i] to activate",
// hides ▶) and that i returns to interactive mode.
func TestPassiveMode(t *testing.T) {
	env := newTestEnv(t)

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "Sessions", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not start: %v", err)
	}

	// Move cursor onto a window row so ▶ is visible.
	env.sendKeys("scratch", "j")
	if err := env.waitForText("scratch", "▶", 2*time.Second); err != nil {
		t.Fatalf("▶ did not appear after j: %v", err)
	}

	// Press q → sidebar enters passive mode.
	env.sendKeys("scratch", "q")
	if err := env.waitForText("scratch", "[i] to activate", 2*time.Second); err != nil {
		t.Fatalf("[i] to activate did not appear after q: %v", err)
	}
	// ▶ should be gone in passive mode.
	if err := env.waitForNoText("scratch", "▶", 1*time.Second); err != nil {
		t.Fatalf("▶ should disappear in passive mode: %v", err)
	}

	// Press i → sidebar re-enters interactive mode.
	env.sendKeys("scratch", "i")
	if err := env.waitForNoText("scratch", "[i] to activate", 2*time.Second); err != nil {
		t.Fatalf("[i] to activate should disappear after i: %v", err)
	}
}
