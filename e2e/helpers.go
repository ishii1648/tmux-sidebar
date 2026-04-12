//go:build e2e

// Package e2e provides end-to-end tests for tmux-sidebar using an isolated
// tmux server and tmux capture-pane for output verification.
//
// Run with:
//
//	go test -v -tags e2e -timeout 60s ./e2e/...
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── binary build (once per test binary run) ───────────────────────────────────

var (
	buildOnce  sync.Once
	builtBin   string
	builtError error
)

// builtBinary returns the path to the compiled tmux-sidebar binary,
// building it on the first call.
func builtBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		repoRoot, _ := filepath.Abs("..")
		dir, err := os.MkdirTemp("", "tmux-sidebar-e2e-*")
		if err != nil {
			builtError = fmt.Errorf("MkdirTemp: %w", err)
			return
		}
		builtBin = filepath.Join(dir, "tmux-sidebar")
		cmd := exec.Command("go", "build", "-o", builtBin, ".")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			builtError = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if builtError != nil {
		t.Fatalf("build binary: %v", builtError)
	}
	return builtBin
}

// ── testEnv ──────────────────────────────────────────────────────────────────

// testEnv holds resources for one E2E test: an isolated tmux server,
// the compiled binary, and a temporary state-files directory.
type testEnv struct {
	t        *testing.T
	socket   string // tmux -L <socket> server name
	binary   string // path to compiled tmux-sidebar
	stateDir string // TMUX_SIDEBAR_STATE_DIR for this test
}

// newTestEnv builds the binary (once) and starts a fresh isolated tmux server.
// The server is killed automatically via t.Cleanup.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	binary := builtBinary(t)
	stateDir := t.TempDir()

	// Use nanosecond timestamp for a unique socket name (≤ 20 chars).
	raw := fmt.Sprintf("%d", time.Now().UnixNano())
	socket := "e2e" + raw[len(raw)-14:]

	// Start isolated tmux server with a scratch session.
	if err := exec.Command("tmux", "-L", socket,
		"new-session", "-d", "-s", "scratch", "-x", "120", "-y", "40").Run(); err != nil {
		t.Fatalf("start tmux server (socket=%s): %v", socket, err)
	}
	// Enable focus-events so the sidebar receives FocusMsg when it enables focus tracking.
	exec.Command("tmux", "-L", socket, "set-option", "-g", "focus-events", "on").Run()

	env := &testEnv{t: t, socket: socket, binary: binary, stateDir: stateDir}
	t.Cleanup(func() {
		exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	return env
}

// ── tmux helpers ─────────────────────────────────────────────────────────────

// tmuxCmd runs a tmux command against this env's isolated server.
func (e *testEnv) tmuxCmd(args ...string) (string, error) {
	full := append([]string{"-L", e.socket}, args...)
	out, err := exec.Command("tmux", full...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// newSession creates a new detached session in the isolated server.
func (e *testEnv) newSession(name string) {
	e.t.Helper()
	if _, err := e.tmuxCmd("new-session", "-d", "-s", name, "-x", "120", "-y", "40"); err != nil {
		e.t.Fatalf("new-session %q: %v", name, err)
	}
}

// newWindow creates a named window in the given session.
func (e *testEnv) newWindow(session, name string) {
	e.t.Helper()
	if _, err := e.tmuxCmd("new-window", "-t", session, "-n", name); err != nil {
		e.t.Fatalf("new-window %q/%q: %v", session, name, err)
	}
}

// paneNumber returns the numeric part of pane_id (e.g. 5 for "%5") for target.
func (e *testEnv) paneNumber(target string) int {
	e.t.Helper()
	out, err := e.tmuxCmd("display-message", "-p", "-t", target, "#{pane_id}")
	if err != nil {
		e.t.Fatalf("paneNumber %q: %v", target, err)
	}
	id := strings.TrimSpace(out) // "%5"
	if len(id) > 1 && id[0] == '%' {
		var num int
		fmt.Sscanf(id[1:], "%d", &num)
		return num
	}
	return 0
}

// ── state file helpers ────────────────────────────────────────────────────────

// setupStateFile writes a pane_N status file.
func (e *testEnv) setupStateFile(paneNum int, status string) {
	e.t.Helper()
	path := filepath.Join(e.stateDir, fmt.Sprintf("pane_%d", paneNum))
	if err := os.WriteFile(path, []byte(status), 0644); err != nil {
		e.t.Fatalf("write pane_%d: %v", paneNum, err)
	}
}

// setupStateFileStarted writes a pane_N_started epoch file.
func (e *testEnv) setupStateFileStarted(paneNum int, epochSecs int64) {
	e.t.Helper()
	path := filepath.Join(e.stateDir, fmt.Sprintf("pane_%d_started", paneNum))
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d", epochSecs)), 0644); err != nil {
		e.t.Fatalf("write pane_%d_started: %v", paneNum, err)
	}
}

// removeStateFile deletes a pane_N status file (for polling tests).
func (e *testEnv) removeStateFile(paneNum int) {
	e.t.Helper()
	os.Remove(filepath.Join(e.stateDir, fmt.Sprintf("pane_%d", paneNum)))
	os.Remove(filepath.Join(e.stateDir, fmt.Sprintf("pane_%d_started", paneNum)))
}

// ── sidebar process ───────────────────────────────────────────────────────────

// runSidebar starts the sidebar binary in target (which must have a shell ready).
// It sets TMUX_SIDEBAR_STATE_DIR and TMUX_SIDEBAR_NO_ALT_SCREEN so that
// capture-pane can read the output directly from the normal screen buffer.
func (e *testEnv) runSidebar(target string) {
	e.t.Helper()
	// Send the command as literal text, then Enter.
	cmd := fmt.Sprintf("TMUX_SIDEBAR_STATE_DIR='%s' TMUX_SIDEBAR_NO_ALT_SCREEN=1 TMUX_SIDEBAR_FORCE_FOCUS=1 TMUX_SIDEBAR_SOCKET='%s' '%s'",
		e.stateDir, e.socket, e.binary)
	if _, err := e.tmuxCmd("send-keys", "-t", target, "-l", cmd); err != nil {
		e.t.Fatalf("send-keys cmd: %v", err)
	}
	if _, err := e.tmuxCmd("send-keys", "-t", target, "Enter"); err != nil {
		e.t.Fatalf("send-keys Enter: %v", err)
	}
}

// ── capture / key send / polling ─────────────────────────────────────────────

// capturePane returns the current visible content of the target pane.
func (e *testEnv) capturePane(target string) string {
	out, _ := e.tmuxCmd("capture-pane", "-p", "-t", target)
	return out
}

// sendKeys sends key(s) to the target pane (tmux key names, e.g. "j", "Enter", "q").
func (e *testEnv) sendKeys(target, keys string) {
	e.t.Helper()
	if _, err := e.tmuxCmd("send-keys", "-t", target, keys); err != nil {
		e.t.Fatalf("send-keys %q → %s: %v", keys, target, err)
	}
}

// waitForText polls capturePane until text appears or timeout expires.
func (e *testEnv) waitForText(target, text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(e.capturePane(target), text) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out (%v) waiting for %q in %s\ncurrent:\n%s",
		timeout, text, target, e.capturePane(target))
}

// waitForNoText polls until text disappears or timeout expires.
func (e *testEnv) waitForNoText(target, text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !strings.Contains(e.capturePane(target), text) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out (%v) waiting for %q to disappear from %s\ncurrent:\n%s",
		timeout, text, target, e.capturePane(target))
}
