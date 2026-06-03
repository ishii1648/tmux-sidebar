//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// hook invokes the real `tmux-sidebar hook` subcommand the way an agent's hook
// configuration would: $TMUX_PANE points at the agent pane and
// TMUX_SIDEBAR_STATE_DIR points at this env's state dir.
func (e *testEnv) hook(paneNum int, args ...string) {
	e.t.Helper()
	full := append([]string{"hook"}, args...)
	cmd := exec.Command(e.binary, full...)
	cmd.Env = append(os.Environ(),
		"TMUX_PANE=%"+strconv.Itoa(paneNum),
		"TMUX_SIDEBAR_STATE_DIR="+e.stateDir,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("hook %v: %v\n%s", args, err, out)
	}
}

func (e *testEnv) startedValue(paneNum int) (string, bool) {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.stateDir, fmt.Sprintf("pane_%d_started", paneNum)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// TestHookClaudeTurnKeepsRunningBadge drives a full Claude turn through the real
// hook subcommand using the option-3 hook commands (PreToolUse=running,
// PostToolUse=running, Stop=idle --reset) and verifies, against a live tmux
// sidebar, that:
//
//   - the running badge stays lit across the per-tool PostToolUse→PreToolUse
//     cycles (no idle flicker mid-turn),
//   - the elapsed clock is preserved across those cycles (pane_N_started not
//     rewritten), so the badge keeps showing the original elapsed,
//   - Stop (idle --reset) drops the badge and removes pane_N_started,
//   - the next turn's running starts a fresh elapsed clock.
func TestHookClaudeTurnKeepsRunningBadge(t *testing.T) {
	env := newTestEnv(t)

	env.newSession("dev")
	env.newWindow("dev", "build")
	paneNum := env.paneNumber("dev:1")

	// Seed the elapsed origin to 3 minutes ago so accumulation is visible as
	// "🔄3m" rather than a hard-to-distinguish "🔄0s".
	seeded := time.Now().Add(-3 * time.Minute).Unix()
	env.setupStateFileStarted(paneNum, seeded)

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "build", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not load sessions: %v", err)
	}

	// Simulate a turn that uses two tools. PreToolUse and PostToolUse both
	// write running under option 3.
	env.hook(paneNum, "running") // PreToolUse, tool 1
	env.hook(paneNum, "running") // PostToolUse, tool 1 done
	env.hook(paneNum, "running") // PreToolUse, tool 2
	env.hook(paneNum, "running") // PostToolUse, tool 2 done

	// Badge stays running and keeps the seeded elapsed (3m), never resetting to
	// 0s and never flickering to idle.
	if err := env.waitForText("scratch", "🔄3m", 5*time.Second); err != nil {
		t.Fatalf("running badge with preserved elapsed did not appear: %v", err)
	}
	if got, ok := env.startedValue(paneNum); !ok || got != strconv.FormatInt(seeded, 10) {
		t.Fatalf("pane_%d_started = %q (ok=%v), want preserved %d", paneNum, got, ok, seeded)
	}

	// Stop ends the turn: badge disappears and the elapsed origin is cleared.
	env.hook(paneNum, "idle", "--reset")
	if err := env.waitForNoText("scratch", "🔄", 5*time.Second); err != nil {
		t.Fatalf("badge did not clear after Stop: %v", err)
	}
	if _, ok := env.startedValue(paneNum); ok {
		t.Fatalf("pane_%d_started should be removed after idle --reset", paneNum)
	}

	// Next turn starts a fresh elapsed clock.
	env.hook(paneNum, "running")
	if err := env.waitForText("scratch", "🔄", 5*time.Second); err != nil {
		t.Fatalf("running badge did not reappear on next turn: %v", err)
	}
	got, ok := env.startedValue(paneNum)
	if !ok {
		t.Fatalf("pane_%d_started should be recreated on next turn", paneNum)
	}
	if got == strconv.FormatInt(seeded, 10) {
		t.Fatalf("pane_%d_started = %q, want a fresh timestamp (not the old %d)", paneNum, got, seeded)
	}
}

// TestHookUserPromptSubmitReanchorsAfterInterrupt reproduces issues/0018: a turn
// that ended without a Stop hook (Esc interrupt / crash) leaves a stale
// pane_N_started. The next user turn fires UserPromptSubmit (`running --reset`),
// which must re-anchor the elapsed clock to "now" instead of accumulating the
// idle gap into the badge.
func TestHookUserPromptSubmitReanchorsAfterInterrupt(t *testing.T) {
	env := newTestEnv(t)

	env.newSession("dev")
	env.newWindow("dev", "build")
	paneNum := env.paneNumber("dev:1")

	// Stale anchor: a previous turn started 4h ago and was interrupted (no Stop
	// --reset ever ran), so pane_N_started is still pointing at the old origin.
	stale := time.Now().Add(-4 * time.Hour).Unix()
	env.setupStateFileStarted(paneNum, stale)

	env.runSidebar("scratch")
	if err := env.waitForText("scratch", "build", 5*time.Second); err != nil {
		t.Fatalf("sidebar did not load sessions: %v", err)
	}

	// New user prompt → UserPromptSubmit re-anchors the clock.
	env.hook(paneNum, "running", "--reset")

	// The badge must NOT show the ~240m accumulated gap.
	if err := env.waitForNoText("scratch", "🔄240m", 5*time.Second); err != nil {
		t.Fatalf("badge still showed the stale accumulated elapsed: %v", err)
	}
	// And pane_N_started must have been overwritten, not preserved.
	got, ok := env.startedValue(paneNum)
	if !ok {
		t.Fatalf("pane_%d_started should exist after running --reset", paneNum)
	}
	if got == strconv.FormatInt(stale, 10) {
		t.Fatalf("pane_%d_started = %q, want re-anchored fresh timestamp (not the stale %d)", paneNum, got, stale)
	}
}
