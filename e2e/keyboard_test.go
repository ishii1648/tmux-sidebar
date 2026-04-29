//go:build e2e

package e2e

import (
	"strings"
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
	// Wait for focus and session data before pressing keys.
	if err := env.waitForText("scratch", "●", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not become focused: %v", err)
	}
	if err := env.waitForText("scratch", "nav", 3*time.Second); err != nil {
		t.Fatalf("sidebar did not load sessions: %v", err)
	}

	env.sendKeys("scratch", "k")
	if err := env.waitForCursorOn("scratch", "beta", 2*time.Second); err != nil {
		t.Fatalf("cursor did not move to beta: %v", err)
	}

	env.sendKeys("scratch", "k")
	if err := env.waitForCursorOn("scratch", "alpha", 2*time.Second); err != nil {
		t.Fatalf("cursor did not move to alpha: %v", err)
	}

	env.sendKeys("scratch", "j")
	if err := env.waitForCursorOn("scratch", "beta", 2*time.Second); err != nil {
		t.Fatalf("cursor did not move back to beta: %v", err)
	}
}

// TestSearchMode verifies that printable keys enter the always-on search mode
// and Esc clears the query.
func TestSearchMode(t *testing.T) {
	env := newTestEnv(t)

	env.newSession("find")
	env.newWindow("find", "target")

	env.runSidebar("scratch")
	// Wait for focus before pressing keys.
	if err := env.waitForText("scratch", "●", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not become focused: %v", err)
	}
	if err := env.waitForText("scratch", "target", 3*time.Second); err != nil {
		t.Fatalf("sidebar did not load target window: %v", err)
	}

	env.sendKeys("scratch", "t")
	if err := env.waitForText("scratch", "> t▏", 2*time.Second); err != nil {
		t.Fatalf("search query did not appear: %v", err)
	}
	output := env.capturePane("scratch")
	if !strings.Contains(output, "target") {
		t.Fatalf("filtered output should contain target window:\n%s", output)
	}

	env.sendKeys("scratch", "Escape")
	if err := env.waitForText("scratch", "> type to filter...", 2*time.Second); err != nil {
		t.Fatalf("search query did not clear after Escape: %v", err)
	}
}
