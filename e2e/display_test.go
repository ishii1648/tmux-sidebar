//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// TestDisplayShowsSessionsAndBadges verifies that session names, window names,
// and state badges ([idle] / [ask] / [permission]) are rendered in the sidebar.
func TestDisplayShowsSessionsAndBadges(t *testing.T) {
	env := newTestEnv(t)

	// Create two sessions with explicitly named windows.
	env.newSession("work")
	env.newWindow("work", "editor")

	env.newSession("infra")
	env.newWindow("infra", "log")

	// Assign state badges to named windows.
	idlePane := env.paneNumber("work:1")
	askPane := env.paneNumber("infra:1")
	env.setupStateFile(idlePane, "idle")
	env.setupStateFile(askPane, "ask")

	env.runSidebar("scratch")
	// Wait for actual session data to appear (window name "editor" is unique;
	// session name "work" risks matching a GitHub Actions working directory path).
	if err := env.waitForText("scratch", "editor", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not load sessions: %v", err)
	}

	output := env.capturePane("scratch")
	for _, want := range []string{
		"work", "editor",
		"infra", "log",
		"[idle]", "[ask]",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("want %q in capture output:\n%s", want, output)
		}
	}
}

// TestDisplayPermissionBadge verifies that the [permission] badge is rendered.
func TestDisplayPermissionBadge(t *testing.T) {
	env := newTestEnv(t)

	env.newSession("ops")
	env.newWindow("ops", "deploy")

	paneNum := env.paneNumber("ops:1")
	env.setupStateFile(paneNum, "permission")

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "[permission]", 5*time.Second); err != nil {
		t.Fatalf("permission badge did not appear: %v", err)
	}
}

// TestDisplayRunningBadge verifies that the [running Nm] badge is rendered for
// a pane that has a running state file and a _started epoch file.
func TestDisplayRunningBadge(t *testing.T) {
	env := newTestEnv(t)

	env.newSession("dev")
	env.newWindow("dev", "build")

	paneNum := env.paneNumber("dev:1")
	env.setupStateFile(paneNum, "running")
	// Pretend the pane started 3 minutes ago.
	env.setupStateFileStarted(paneNum, time.Now().Add(-3*time.Minute).Unix())

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "[running", 5*time.Second); err != nil {
		t.Fatalf("running badge did not appear: %v", err)
	}

	output := env.capturePane("scratch")
	if !strings.Contains(output, "build") {
		t.Errorf("want 'build' window name in output:\n%s", output)
	}
}
