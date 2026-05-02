// Package hook implements the writer side of the agent pane-state protocol
// consumed by internal/state.FSReader. It is invoked from the
// `tmux-sidebar hook <status>` subcommand so that Claude Code / Codex CLI
// settings can configure hooks with a single command instead of inlining a
// shell script.
//
// Pairing: this package writes; internal/state reads. The two MUST stay in
// sync on file format (`pane_N` line shape, sidecar file names).
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ishii1648/tmux-sidebar/internal/state"
)

// Options configures a single hook invocation.
type Options struct {
	// Status is the value written on line 1 of pane_N. Required.
	Status string

	// Kind is the agent kind written on line 2 of pane_N
	// ("claude" | "codex"). Empty defaults to "claude".
	Kind string

	// PaneID is the tmux pane id (`$TMUX_PANE`, e.g. "%42"). Required.
	PaneID string

	// Stdin is the hook payload reader. Claude Code passes a JSON object
	// with `session_id` / `cwd` here. Pass nil to skip JSON parsing.
	Stdin io.Reader

	// StateDir overrides the destination directory. Empty defaults to
	// $TMUX_SIDEBAR_STATE_DIR or state.DefaultStateDir.
	StateDir string

	// Now returns the current time. Tests inject a fixed clock; nil means
	// time.Now.
	Now func() time.Time
}

// validKinds: empty defaults to claude.
var validKinds = map[string]bool{
	state.AgentClaude: true,
	state.AgentCodex:  true,
}

// validStatuses mirrors the set parsed by state.FSReader.
var validStatuses = map[string]bool{
	string(state.StatusRunning):    true,
	string(state.StatusIdle):       true,
	string(state.StatusPermission): true,
	string(state.StatusAsk):        true,
}

// hookPayload mirrors the subset of Claude Code's hook JSON we care about.
// Codex CLI may pass a different shape; unknown keys are ignored.
type hookPayload struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

// Write performs the hook side-effects. It is idempotent for the
// status-file portion (overwrite) and write-once for pane_N_path.
func Write(opts Options) error {
	if !validStatuses[opts.Status] {
		return fmt.Errorf("invalid status %q (want running|idle|permission|ask)", opts.Status)
	}
	kind := opts.Kind
	if kind == "" {
		kind = state.AgentClaude
	}
	if !validKinds[kind] {
		return fmt.Errorf("invalid kind %q (want claude|codex)", kind)
	}
	num, err := paneNumber(opts.PaneID)
	if err != nil {
		return err
	}

	dir := opts.StateDir
	if dir == "" {
		dir = os.Getenv("TMUX_SIDEBAR_STATE_DIR")
	}
	if dir == "" {
		dir = state.DefaultStateDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	payload := readPayload(opts.Stdin)

	statusPath := filepath.Join(dir, fmt.Sprintf("pane_%d", num))
	body := opts.Status + "\n" + kind + "\n"
	if err := os.WriteFile(statusPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", statusPath, err)
	}

	if opts.Status == string(state.StatusRunning) {
		now := time.Now
		if opts.Now != nil {
			now = opts.Now
		}
		startedPath := filepath.Join(dir, fmt.Sprintf("pane_%d_started", num))
		startedBody := strconv.FormatInt(now().Unix(), 10) + "\n"
		if err := os.WriteFile(startedPath, []byte(startedBody), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", startedPath, err)
		}

		// pane_N_path: write only on first running transition so the path
		// reflects the agent's launch directory, not whatever cwd the hook
		// happened to fire from later.
		pathPath := filepath.Join(dir, fmt.Sprintf("pane_%d_path", num))
		if _, err := os.Stat(pathPath); os.IsNotExist(err) {
			workdir := payload.Cwd
			if workdir == "" {
				if cwd, gerr := os.Getwd(); gerr == nil {
					workdir = cwd
				}
			}
			if workdir != "" {
				if werr := os.WriteFile(pathPath, []byte(workdir+"\n"), 0o644); werr != nil {
					return fmt.Errorf("write %s: %w", pathPath, werr)
				}
			}
		}
	}

	// session_id can update at any status (Claude Code rotates ids per
	// session); always overwrite when we have one.
	sessionID := payload.SessionID
	if sessionID == "" {
		sessionID = os.Getenv("CLAUDE_SESSION_ID")
	}
	if sessionID != "" {
		sidPath := filepath.Join(dir, fmt.Sprintf("pane_%d_session_id", num))
		if err := os.WriteFile(sidPath, []byte(sessionID+"\n"), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", sidPath, err)
		}
	}

	return nil
}

// paneNumber strips the leading "%" from a tmux pane id and returns the
// numeric portion. Empty / malformed input is reported so the hook fails
// loudly rather than silently writing to pane_0.
func paneNumber(paneID string) (int, error) {
	if paneID == "" {
		return 0, fmt.Errorf("TMUX_PANE is empty (run inside tmux)")
	}
	s := strings.TrimPrefix(paneID, "%")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse pane id %q: %w", paneID, err)
	}
	return n, nil
}

// readPayload best-effort parses Claude Code hook JSON from stdin. Returns
// a zero-value struct on any error (including non-JSON input from Codex).
func readPayload(r io.Reader) hookPayload {
	var p hookPayload
	if r == nil {
		return p
	}
	// Cap at 1 MiB to avoid pathological hook payloads exhausting memory.
	limited := io.LimitReader(r, 1<<20)
	data, err := io.ReadAll(limited)
	if err != nil || len(data) == 0 {
		return p
	}
	_ = json.Unmarshal(data, &p)
	return p
}
