package state

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFile is a helper to create a state file in dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", name, err)
	}
}

func TestFSReader_Running(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-3 * time.Minute).Unix()
	writeFile(t, dir, "pane_1", "running")
	writeFile(t, dir, "pane_1_started", fmt.Sprintf("%d", started))

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[1]
	if !ok {
		t.Fatal("pane 1 not in result")
	}
	if ps.Status != StatusRunning {
		t.Errorf("status = %q, want %q", ps.Status, StatusRunning)
	}
	// StartedAt must be approximately 3 minutes ago.
	if ps.StartedAt.IsZero() {
		t.Fatal("StartedAt is zero, want non-zero")
	}
	if time.Since(ps.StartedAt) < 3*time.Minute {
		t.Errorf("StartedAt = %v, want ~3m ago", ps.StartedAt)
	}
}

func TestFSReader_Idle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_2", "idle")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[2]
	if !ok {
		t.Fatal("pane 2 not in result")
	}
	if ps.Status != StatusIdle {
		t.Errorf("status = %q, want %q", ps.Status, StatusIdle)
	}
	if !ps.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want zero for idle", ps.StartedAt)
	}
}

func TestFSReader_Permission(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_3", "permission")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[3]
	if !ok {
		t.Fatal("pane 3 not in result")
	}
	if ps.Status != StatusPermission {
		t.Errorf("status = %q, want %q", ps.Status, StatusPermission)
	}
}

func TestFSReader_Ask(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_4", "ask")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[4]
	if !ok {
		t.Fatal("pane 4 not in result")
	}
	if ps.Status != StatusAsk {
		t.Errorf("status = %q, want %q", ps.Status, StatusAsk)
	}
}

// TestFSReader_NoDir: 存在しないディレクトリ → 空マップ（エラーなし）
func TestFSReader_NoDir(t *testing.T) {
	r := NewFSReader("/tmp/tmux-sidebar-test-nonexistent-dir-12345")
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected empty map, got %v", states)
	}
}

// TestFSReader_EmptyDir: ファイルなし → 空マップ
func TestFSReader_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected empty map, got %v", states)
	}
}

// TestFSReader_InvalidStatus: 不正値 → StatusUnknown、パニックしない
func TestFSReader_InvalidStatus(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_5", "unknown_garbage_value")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[5]
	if !ok {
		t.Fatal("pane 5 not in result")
	}
	if ps.Status != StatusUnknown {
		t.Errorf("status = %q, want StatusUnknown", ps.Status)
	}
}

// TestFSReader_EmptyStatus: 空ファイル → StatusUnknown、パニックしない
func TestFSReader_EmptyStatus(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_6", "")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[6]
	if !ok {
		t.Fatal("pane 6 not in result")
	}
	if ps.Status != StatusUnknown {
		t.Errorf("status = %q, want StatusUnknown", ps.Status)
	}
}

// TestFSReader_RunningWithoutStarted: pane_N_started がない → Elapsed = 0
func TestFSReader_RunningWithoutStarted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_7", "running")
	// pane_7_started は書かない

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[7]
	if !ok {
		t.Fatal("pane 7 not in result")
	}
	if ps.Status != StatusRunning {
		t.Errorf("status = %q, want running", ps.Status)
	}
	if !ps.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want zero (no started file)", ps.StartedAt)
	}
}

// TestFSReader_InvalidStarted: pane_N_started が数値でない → Elapsed = 0、パニックしない
func TestFSReader_InvalidStarted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_8", "running")
	writeFile(t, dir, "pane_8_started", "not-a-number")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[8]
	if !ok {
		t.Fatal("pane 8 not in result")
	}
	if ps.Status != StatusRunning {
		t.Errorf("status = %q, want running", ps.Status)
	}
	if !ps.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want zero (invalid started)", ps.StartedAt)
	}
}

// TestFSReader_NonPaneFileIgnored: pane_ で始まらないファイルは無視される
func TestFSReader_NonPaneFileIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "other_file", "running")
	writeFile(t, dir, "pane_9", "idle")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if _, ok := states[9]; !ok {
		t.Error("pane 9 should be in result")
	}
	// other_file should not create any entry
	if len(states) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(states), states)
	}
}

// TestFSReader_SymlinkIgnored: シムリンクは無視される（シムリンク攻撃DoS対策）
func TestFSReader_SymlinkIgnored(t *testing.T) {
	dir := t.TempDir()
	// Create a real pane file
	writeFile(t, dir, "pane_10", "idle")
	// Create a symlink pane_11 → pane_10 (simulates a malicious symlink)
	if err := os.Symlink(filepath.Join(dir, "pane_10"), filepath.Join(dir, "pane_11")); err != nil {
		t.Skipf("symlink creation failed (unsupported on this platform): %v", err)
	}

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	// Only the regular file pane_10 should be present; pane_11 (symlink) must be ignored.
	if _, ok := states[10]; !ok {
		t.Error("pane 10 (regular file) should be in result")
	}
	if _, ok := states[11]; ok {
		t.Error("pane 11 (symlink) should be ignored")
	}
}
