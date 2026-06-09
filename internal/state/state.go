// Package state reads agent pane-state files written by ADR-063 hooks
// (Claude Code / Codex CLI 両対応).
//
// State files live in /tmp/agent-pane-state/ (or a configurable directory):
//
//	pane_N          — line 1: status ("running" | "idle" | "permission" | "ask")
//	                  line 2: agent kind ("claude" | "codex"; missing or unknown → "")
//	pane_N_started  — unix epoch timestamp when running started
package state

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultStateDir is the directory where state files are written by hooks.
const DefaultStateDir = "/tmp/agent-pane-state"

// Status represents the state of an agent pane.
type Status string

const (
	StatusRunning    Status = "running"
	StatusIdle       Status = "idle"
	StatusPermission Status = "permission"
	StatusAsk        Status = "ask"
	StatusUnknown    Status = ""
)

// Agent kinds written on line 2 of pane_N. Anything else parses to "".
const (
	AgentClaude = "claude"
	AgentCodex  = "codex"
)

// PaneState holds the state for a single tmux pane.
type PaneState struct {
	Status    Status
	Agent     string        // "claude" | "codex" | "" (unknown / legacy)
	Elapsed   time.Duration // only valid when Status == StatusRunning
	WorkDir   string        // initial working directory of the agent session (from pane_N_path)
	SessionID string        // agent session UUID (from pane_N_session_id)
}

// Reader is the interface for reading pane state.
type Reader interface {
	// Read returns a map of pane numbers to PaneState.
	// Panes without a state file are not included in the map.
	Read() (map[int]PaneState, error)
	// ReadAndGC behaves like Read but additionally removes state files whose
	// pane number is not in live. The unlink is best-effort: failures are
	// ignored so a partially writable directory never breaks the read path.
	ReadAndGC(live map[int]struct{}) (map[int]PaneState, error)
}

// FSReader reads pane state from the filesystem.
type FSReader struct {
	dir string
}

// NewFSReader creates an FSReader that reads from the given directory.
// If dir is empty, DefaultStateDir is used.
func NewFSReader(dir string) *FSReader {
	if dir == "" {
		dir = DefaultStateDir
	}
	return &FSReader{dir: dir}
}

// Read scans the state directory and returns a map of pane number → PaneState.
func (r *FSReader) Read() (map[int]PaneState, error) {
	return r.read(nil)
}

// ReadAndGC behaves like Read but also unlinks state files whose pane number
// is not present in live. The unlink is best-effort: any per-file error (EACCES
// on /tmp because of sticky-bit ownership, ENOENT from a racing reader, etc.)
// is ignored so the read path never fails because GC could not run cleanly.
//
// Passing live == nil disables GC and is equivalent to Read.
func (r *FSReader) ReadAndGC(live map[int]struct{}) (map[int]PaneState, error) {
	if live == nil {
		// A nil map would treat every pane as stale and wipe the directory.
		// Callers that want plain reads should use Read; map ReadAndGC(nil)
		// to that to make accidental misuse harmless.
		return r.read(nil)
	}
	return r.read(live)
}

// read is the shared scan implementation. When live != nil, files whose pane
// number is not in live are unlinked best-effort; their content is also
// skipped (no ReadFile) so a bloated directory doesn't pay for re-reading
// stale data on the way to deleting it.
func (r *FSReader) read(live map[int]struct{}) (map[int]PaneState, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[int]PaneState{}, nil
		}
		return nil, err
	}

	// First pass: collect status, agent, started epoch, workdir, and session ID per pane number.
	statuses := map[int]Status{}
	agents := map[int]string{}
	started := map[int]int64{}
	workdirs := map[int]string{}
	sessionIDs := map[int]string{}
	var stalePaths []string

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "pane_") {
			continue
		}
		// Skip symlinks and special files (devices, pipes) to prevent DoS via
		// /tmp being world-writable: a malicious user could symlink pane_N to
		// /dev/zero causing os.ReadFile to loop indefinitely.
		if !entry.Type().IsRegular() {
			continue
		}
		num, ok := parsePaneNumber(name)
		if !ok {
			continue
		}
		if live != nil {
			if _, alive := live[num]; !alive {
				stalePaths = append(stalePaths, filepath.Join(r.dir, name))
				continue
			}
		}
		rest := name[len("pane_"):]

		if strings.HasSuffix(rest, "_started") {
			data, err := os.ReadFile(filepath.Join(r.dir, name))
			if err != nil {
				continue
			}
			epoch, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if err != nil {
				continue
			}
			started[num] = epoch
		} else if strings.HasSuffix(rest, "_session_id") {
			data, err := os.ReadFile(filepath.Join(r.dir, name))
			if err != nil {
				continue
			}
			sessionIDs[num] = strings.TrimSpace(string(data))
		} else if strings.HasSuffix(rest, "_path") {
			data, err := os.ReadFile(filepath.Join(r.dir, name))
			if err != nil {
				continue
			}
			workdirs[num] = strings.TrimSpace(string(data))
		} else {
			// pane_N: line 1 is status, line 2 (optional) is agent kind.
			data, err := os.ReadFile(filepath.Join(r.dir, name))
			if err != nil {
				continue
			}
			lines := strings.Split(string(data), "\n")
			statusLine := ""
			if len(lines) > 0 {
				statusLine = strings.TrimSpace(lines[0])
			}
			switch Status(statusLine) {
			case StatusRunning, StatusIdle, StatusPermission, StatusAsk:
				statuses[num] = Status(statusLine)
			default:
				statuses[num] = StatusUnknown
			}
			agentLine := ""
			if len(lines) > 1 {
				agentLine = strings.TrimSpace(lines[1])
			}
			switch agentLine {
			case AgentClaude, AgentCodex:
				agents[num] = agentLine
			default:
				agents[num] = ""
			}
		}
	}

	for _, p := range stalePaths {
		_ = os.Remove(p)
	}

	result := make(map[int]PaneState, len(statuses))
	now := time.Now()
	for num, status := range statuses {
		ps := PaneState{Status: status, Agent: agents[num]}
		if status == StatusRunning {
			if epoch, ok := started[num]; ok {
				elapsed := now.Sub(time.Unix(epoch, 0))
				if elapsed < time.Minute {
					ps.Elapsed = elapsed.Truncate(time.Second)
				} else {
					ps.Elapsed = elapsed.Truncate(time.Minute)
				}
			}
		}
		if dir, ok := workdirs[num]; ok {
			ps.WorkDir = dir
		}
		if sid, ok := sessionIDs[num]; ok {
			ps.SessionID = sid
		}
		result[num] = ps
	}
	return result, nil
}

// parsePaneNumber extracts N from any of the recognised state file names:
//
//	pane_N, pane_N_started, pane_N_path, pane_N_session_id
//
// Returns false when the name does not match. Centralised so GC stale-detection
// and per-suffix parsing agree on what counts as a pane file.
func parsePaneNumber(name string) (int, bool) {
	if !strings.HasPrefix(name, "pane_") {
		return 0, false
	}
	rest := name[len("pane_"):]
	for _, suffix := range []string{"_started", "_session_id", "_path"} {
		if strings.HasSuffix(rest, suffix) {
			rest = strings.TrimSuffix(rest, suffix)
			break
		}
	}
	num, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return num, true
}
