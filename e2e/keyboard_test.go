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

// TestSearchMode verifies that `/` enters search mode and Esc returns to normal.
// The pre-Phase-1 "any printable key starts search" behavior was replaced with
// a vim-style modal: in normal mode plain text is ignored.
func TestSearchMode(t *testing.T) {
	env := newTestEnv(t)

	env.newSession("find")
	env.newWindow("find", "target")

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "●", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not become focused: %v", err)
	}
	if err := env.waitForText("scratch", "target", 3*time.Second); err != nil {
		t.Fatalf("sidebar did not load target window: %v", err)
	}

	// Plain text in normal mode must NOT enter the query — Phase 1 invariant.
	env.sendKeys("scratch", "t")
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(env.capturePane("scratch"), "> t▏") {
		t.Fatalf("plain 't' in normal mode must not enter search query")
	}

	env.sendKeys("scratch", "/")
	if err := env.waitForText("scratch", "> ▏", 2*time.Second); err != nil {
		t.Fatalf("/ should enter empty search prompt: %v", err)
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
	if err := env.waitForText("scratch", "/:search", 2*time.Second); err != nil {
		t.Fatalf("Esc should restore normal-mode hint:\n%s", env.capturePane("scratch"))
	}
}

// navigateCursorTo presses j/k until the cursor reaches a row containing text,
// or fails the test. Tries both directions because the cursor's starting row
// depends on which session was iterated first.
func (e *testEnv) navigateCursorTo(target, text string) {
	e.t.Helper()
	if err := e.waitForCursorOn(target, text, 1*time.Second); err == nil {
		return
	}
	// Try walking up first, then down — covers both relative positions.
	for _, dir := range []string{"k", "j"} {
		for i := 0; i < 8; i++ {
			e.sendKeys(target, dir)
			if err := e.waitForCursorOn(target, text, 500*time.Millisecond); err == nil {
				return
			}
		}
	}
	e.t.Fatalf("could not place cursor on %q in %s\ncurrent:\n%s",
		text, target, e.capturePane(target))
}

// TestCloseIdleWindow verifies the d → y path: confirm prompt appears for an
// idle window, y triggers the kill, and the window disappears from the sidebar.
func TestCloseIdleWindow(t *testing.T) {
	env := newTestEnv(t)
	env.newSession("dwork")
	env.newWindow("dwork", "doomed")

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "●", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not become focused: %v", err)
	}
	if err := env.waitForText("scratch", "doomed", 3*time.Second); err != nil {
		t.Fatalf("sidebar did not load doomed window: %v", err)
	}

	env.navigateCursorTo("scratch", "doomed")

	env.sendKeys("scratch", "d")
	if err := env.waitForText("scratch", "kill window 'doomed'", 2*time.Second); err != nil {
		t.Fatalf("confirm prompt did not appear: %v", err)
	}

	env.sendKeys("scratch", "y")
	if err := env.waitForNoText("scratch", "doomed", 5*time.Second); err != nil {
		t.Fatalf("doomed window should be removed after y:\n%s", env.capturePane("scratch"))
	}
}

// TestCloseCancel verifies that pressing n on a confirm prompt does NOT kill
// anything and returns the sidebar to normal mode.
func TestCloseCancel(t *testing.T) {
	env := newTestEnv(t)
	env.newSession("safe")
	env.newWindow("safe", "keep-me")

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "●", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not become focused: %v", err)
	}
	if err := env.waitForText("scratch", "keep-me", 3*time.Second); err != nil {
		t.Fatalf("sidebar did not load keep-me: %v", err)
	}
	env.navigateCursorTo("scratch", "keep-me")

	env.sendKeys("scratch", "d")
	if err := env.waitForText("scratch", "kill window 'keep-me'", 2*time.Second); err != nil {
		t.Fatalf("confirm prompt did not appear: %v", err)
	}

	env.sendKeys("scratch", "n")
	if err := env.waitForText("scratch", "/:search", 2*time.Second); err != nil {
		t.Fatalf("after n, footer should return to normal hint:\n%s", env.capturePane("scratch"))
	}

	output := env.capturePane("scratch")
	if !strings.Contains(output, "keep-me") {
		t.Fatalf("keep-me window should still be visible after n:\n%s", output)
	}
}
