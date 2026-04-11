// Package state reads Claude Code pane-state files written by ADR-007 hooks.
//
// State files live in /tmp/claude-pane-state/ (or a configurable directory):
//
//	pane_N          — status: "running" | "idle" | "permission" | "ask"
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
const DefaultStateDir = "/tmp/claude-pane-state"

// Status represents the state of a Claude Code pane.
type Status string

const (
	StatusRunning    Status = "running"
	StatusIdle       Status = "idle"
	StatusPermission Status = "permission"
	StatusAsk        Status = "ask"
	StatusUnknown    Status = ""
)

// PaneState holds the state for a single tmux pane.
type PaneState struct {
	Status  Status
	Elapsed time.Duration // only valid when Status == StatusRunning
}

// Reader is the interface for reading pane state.
type Reader interface {
	// Read returns a map of pane numbers to PaneState.
	// Panes without a state file are not included in the map.
	Read() (map[int]PaneState, error)
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
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[int]PaneState{}, nil
		}
		return nil, err
	}

	// First pass: collect status and started epoch per pane number.
	statuses := map[int]Status{}
	started := map[int]int64{}

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
		rest := name[len("pane_"):]

		if strings.HasSuffix(rest, "_started") {
			// pane_N_started
			numStr := strings.TrimSuffix(rest, "_started")
			num, err := strconv.Atoi(numStr)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(filepath.Join(r.dir, name))
			if err != nil {
				continue
			}
			epoch, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if err != nil {
				continue
			}
			started[num] = epoch
		} else {
			// pane_N
			num, err := strconv.Atoi(rest)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(filepath.Join(r.dir, name))
			if err != nil {
				continue
			}
			raw := strings.TrimSpace(string(data))
			switch Status(raw) {
			case StatusRunning, StatusIdle, StatusPermission, StatusAsk:
				statuses[num] = Status(raw)
			default:
				statuses[num] = StatusUnknown
			}
		}
	}

	result := make(map[int]PaneState, len(statuses))
	now := time.Now()
	for num, status := range statuses {
		ps := PaneState{Status: status}
		if status == StatusRunning {
			if epoch, ok := started[num]; ok {
				ps.Elapsed = now.Sub(time.Unix(epoch, 0)).Truncate(time.Minute)
			}
		}
		result[num] = ps
	}
	return result, nil
}
