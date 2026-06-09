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

func TestFSReader_RunningClaude(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-3 * time.Minute).Unix()
	writeFile(t, dir, "pane_1", "running\nclaude\n")
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
	if ps.Agent != AgentClaude {
		t.Errorf("agent = %q, want %q", ps.Agent, AgentClaude)
	}
	// Elapsed is truncated to minutes; must be >= 3m since we set started 3m ago.
	if ps.Elapsed < 3*time.Minute {
		t.Errorf("elapsed = %v, want >= 3m", ps.Elapsed)
	}
}

func TestFSReader_IdleCodex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_2", "idle\ncodex\n")

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
	if ps.Agent != AgentCodex {
		t.Errorf("agent = %q, want %q", ps.Agent, AgentCodex)
	}
	if ps.Elapsed != 0 {
		t.Errorf("elapsed = %v, want 0 for idle", ps.Elapsed)
	}
}

func TestFSReader_PermissionClaude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_3", "permission\nclaude\n")

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
	if ps.Agent != AgentClaude {
		t.Errorf("agent = %q, want %q", ps.Agent, AgentClaude)
	}
}

func TestFSReader_AskClaude(t *testing.T) {
	dir := t.TempDir()
	// ask は claude 専用（codex には ask 状態が無い）。
	writeFile(t, dir, "pane_4", "ask\nclaude\n")

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
	if ps.Agent != AgentClaude {
		t.Errorf("agent = %q, want %q", ps.Agent, AgentClaude)
	}
}

// TestFSReader_AgentMissing: 1行のみ（agent 行なし）→ Agent = ""
func TestFSReader_AgentMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_12", "idle")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[12]
	if !ok {
		t.Fatal("pane 12 not in result")
	}
	if ps.Status != StatusIdle {
		t.Errorf("status = %q, want %q", ps.Status, StatusIdle)
	}
	if ps.Agent != "" {
		t.Errorf("agent = %q, want \"\" (missing line)", ps.Agent)
	}
}

// TestFSReader_AgentInvalid: 2行目が claude/codex 以外 → Agent = ""
func TestFSReader_AgentInvalid(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_13", "running\nbogus\n")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	ps, ok := states[13]
	if !ok {
		t.Fatal("pane 13 not in result")
	}
	if ps.Status != StatusRunning {
		t.Errorf("status = %q, want %q", ps.Status, StatusRunning)
	}
	if ps.Agent != "" {
		t.Errorf("agent = %q, want \"\" (invalid agent token)", ps.Agent)
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
	writeFile(t, dir, "pane_7", "running\nclaude\n")

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
	if ps.Elapsed != 0 {
		t.Errorf("elapsed = %v, want 0 (no started file)", ps.Elapsed)
	}
}

// TestFSReader_InvalidStarted: pane_N_started が数値でない → Elapsed = 0、パニックしない
func TestFSReader_InvalidStarted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_8", "running\nclaude\n")
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
	if ps.Elapsed != 0 {
		t.Errorf("elapsed = %v, want 0 (invalid started)", ps.Elapsed)
	}
}

// TestFSReader_NonPaneFileIgnored: pane_ で始まらないファイルは無視される
func TestFSReader_NonPaneFileIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "other_file", "running")
	writeFile(t, dir, "pane_9", "idle\ncodex\n")

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if _, ok := states[9]; !ok {
		t.Error("pane 9 should be in result")
	}
	if len(states) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(states), states)
	}
}

// TestFSReader_ReadAndGC_RemovesStaleFiles: live set に含まれない pane の
// 状態ファイルはすべて（_started / _path / _session_id 含む）削除される。
func TestFSReader_ReadAndGC_RemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	// pane_1 alive, pane_2 stale (all 4 suffixes), pane_3 stale (status only).
	writeFile(t, dir, "pane_1", "running\nclaude\n")
	writeFile(t, dir, "pane_1_started", fmt.Sprintf("%d", time.Now().Unix()))
	writeFile(t, dir, "pane_2", "idle\ncodex\n")
	writeFile(t, dir, "pane_2_started", "0")
	writeFile(t, dir, "pane_2_path", "/some/dir")
	writeFile(t, dir, "pane_2_session_id", "abc-123")
	writeFile(t, dir, "pane_3", "idle\nclaude\n")

	live := map[int]struct{}{1: {}}

	r := NewFSReader(dir)
	states, err := r.ReadAndGC(live)
	if err != nil {
		t.Fatalf("ReadAndGC: %v", err)
	}
	if _, ok := states[1]; !ok {
		t.Error("pane 1 should be in result")
	}
	if _, ok := states[2]; ok {
		t.Error("pane 2 should not be in result (stale)")
	}
	if _, ok := states[3]; ok {
		t.Error("pane 3 should not be in result (stale)")
	}

	for _, name := range []string{
		"pane_2", "pane_2_started", "pane_2_path", "pane_2_session_id", "pane_3",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should have been removed, stat err=%v", name, err)
		}
	}
	for _, name := range []string{"pane_1", "pane_1_started"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should still exist: %v", name, err)
		}
	}
}

// TestFSReader_ReadAndGC_KeepsAllLiveFiles: live set に含まれる pane の
// すべての suffix が保持され、Read 結果にもすべて反映される。
func TestFSReader_ReadAndGC_KeepsAllLiveFiles(t *testing.T) {
	dir := t.TempDir()
	started := time.Now().Add(-2 * time.Minute).Unix()
	writeFile(t, dir, "pane_5", "running\nclaude\n")
	writeFile(t, dir, "pane_5_started", fmt.Sprintf("%d", started))
	writeFile(t, dir, "pane_5_path", "/work/repo")
	writeFile(t, dir, "pane_5_session_id", "uuid-555")

	r := NewFSReader(dir)
	states, err := r.ReadAndGC(map[int]struct{}{5: {}})
	if err != nil {
		t.Fatalf("ReadAndGC: %v", err)
	}
	ps, ok := states[5]
	if !ok {
		t.Fatal("pane 5 not in result")
	}
	if ps.WorkDir != "/work/repo" {
		t.Errorf("WorkDir = %q, want /work/repo", ps.WorkDir)
	}
	if ps.SessionID != "uuid-555" {
		t.Errorf("SessionID = %q, want uuid-555", ps.SessionID)
	}
	if ps.Elapsed < 2*time.Minute {
		t.Errorf("Elapsed = %v, want >= 2m", ps.Elapsed)
	}

	for _, name := range []string{"pane_5", "pane_5_started", "pane_5_path", "pane_5_session_id"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should still exist: %v", name, err)
		}
	}
}

// TestFSReader_ReadAndGC_NilDisablesGC: live==nil の呼び出しは Read と同じく
// 削除を行わない。誤用に対する安全装置。
func TestFSReader_ReadAndGC_NilDisablesGC(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_7", "idle\nclaude\n")
	writeFile(t, dir, "pane_7_path", "/x")

	r := NewFSReader(dir)
	if _, err := r.ReadAndGC(nil); err != nil {
		t.Fatalf("ReadAndGC(nil): %v", err)
	}
	for _, name := range []string{"pane_7", "pane_7_path"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should still exist when live==nil: %v", name, err)
		}
	}
}

// TestFSReader_ReadAndGC_IgnoresNonPaneFiles: pane_ prefix を持たないファイルは
// GC 対象外（誤って共有ファイルを消さない）。
func TestFSReader_ReadAndGC_IgnoresNonPaneFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_1", "idle\nclaude\n")
	writeFile(t, dir, "README", "hello")
	writeFile(t, dir, "other.lock", "x")

	r := NewFSReader(dir)
	if _, err := r.ReadAndGC(map[int]struct{}{1: {}}); err != nil {
		t.Fatalf("ReadAndGC: %v", err)
	}
	for _, name := range []string{"README", "other.lock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s (non-pane) should be untouched: %v", name, err)
		}
	}
}

// TestFSReader_SymlinkIgnored: シムリンクは無視される（シムリンク攻撃DoS対策）
func TestFSReader_SymlinkIgnored(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pane_10", "idle\nclaude\n")
	if err := os.Symlink(filepath.Join(dir, "pane_10"), filepath.Join(dir, "pane_11")); err != nil {
		t.Skipf("symlink creation failed (unsupported on this platform): %v", err)
	}

	r := NewFSReader(dir)
	states, err := r.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if _, ok := states[10]; !ok {
		t.Error("pane 10 (regular file) should be in result")
	}
	if _, ok := states[11]; ok {
		t.Error("pane 11 (symlink) should be ignored")
	}
}
