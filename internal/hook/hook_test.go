package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestWriteRunningClaude(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 3, 10, 0, 0, 0, time.UTC)
	if err := Write(Options{
		Status:   "running",
		PaneID:   "%42",
		StateDir: dir,
		Now:      func() time.Time { return now },
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "pane_42")); got != "running\nclaude\n" {
		t.Errorf("pane_42 = %q, want %q", got, "running\nclaude\n")
	}
	wantStarted := fmt.Sprintf("%d\n", now.Unix())
	if got := readFile(t, filepath.Join(dir, "pane_42_started")); got != wantStarted {
		t.Errorf("pane_42_started = %q, want %q", got, wantStarted)
	}
	if _, err := os.Stat(filepath.Join(dir, "pane_42_path")); err != nil {
		t.Errorf("pane_42_path should exist on first running transition: %v", err)
	}
}

func TestWriteIdleCodex(t *testing.T) {
	dir := t.TempDir()
	if err := Write(Options{
		Status:   "idle",
		Kind:     "codex",
		PaneID:   "%7",
		StateDir: dir,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "pane_7")); got != "idle\ncodex\n" {
		t.Errorf("pane_7 = %q, want %q", got, "idle\ncodex\n")
	}
	if _, err := os.Stat(filepath.Join(dir, "pane_7_started")); !os.IsNotExist(err) {
		t.Errorf("pane_7_started should NOT exist for idle: stat err=%v", err)
	}
}

func TestWritePathWriteOnce(t *testing.T) {
	dir := t.TempDir()
	pathFile := filepath.Join(dir, "pane_3_path")
	if err := os.WriteFile(pathFile, []byte("/old/cwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Write(Options{
		Status:   "running",
		PaneID:   "%3",
		StateDir: dir,
		Stdin:    strings.NewReader(`{"cwd":"/new/cwd"}`),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := readFile(t, pathFile); got != "/old/cwd\n" {
		t.Errorf("pane_3_path = %q, want %q (write-once preserved)", got, "/old/cwd\n")
	}
}

func TestWriteParsesStdinJSON(t *testing.T) {
	dir := t.TempDir()
	payload := `{"session_id":"abc-123","cwd":"/work/repo"}`
	if err := Write(Options{
		Status:   "running",
		PaneID:   "%1",
		StateDir: dir,
		Stdin:    strings.NewReader(payload),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dir, "pane_1_session_id"))); got != "abc-123" {
		t.Errorf("session_id = %q, want abc-123", got)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dir, "pane_1_path"))); got != "/work/repo" {
		t.Errorf("path = %q, want /work/repo", got)
	}
}

func TestWriteNonJSONStdinIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := Write(Options{
		Status:   "idle",
		PaneID:   "%2",
		StateDir: dir,
		Stdin:    strings.NewReader("not json at all"),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Status file is still written; no session_id since payload was unparseable.
	if got := readFile(t, filepath.Join(dir, "pane_2")); got != "idle\nclaude\n" {
		t.Errorf("pane_2 = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "pane_2_session_id")); !os.IsNotExist(err) {
		t.Errorf("session_id should not exist when stdin is non-JSON: %v", err)
	}
}

func TestWriteSessionIDUpdatesOnIdle(t *testing.T) {
	// Claude Code's session_id is present in every hook event; idle/Stop
	// hooks should refresh it too so the sidebar preview tracks correctly.
	dir := t.TempDir()
	if err := Write(Options{
		Status:   "idle",
		PaneID:   "%9",
		StateDir: dir,
		Stdin:    strings.NewReader(`{"session_id":"refreshed"}`),
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dir, "pane_9_session_id"))); got != "refreshed" {
		t.Errorf("session_id = %q, want refreshed", got)
	}
}

func TestWriteEnvFallbackForSessionID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_SESSION_ID", "from-env")
	if err := Write(Options{
		Status:   "running",
		PaneID:   "%5",
		StateDir: dir,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dir, "pane_5_session_id"))); got != "from-env" {
		t.Errorf("session_id from env = %q, want from-env", got)
	}
}

func TestWriteRejectsBadStatus(t *testing.T) {
	dir := t.TempDir()
	err := Write(Options{Status: "bogus", PaneID: "%1", StateDir: dir})
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "invalid status") {
		t.Errorf("error = %v, want contains 'invalid status'", err)
	}
}

func TestWriteRejectsBadKind(t *testing.T) {
	dir := t.TempDir()
	err := Write(Options{Status: "idle", Kind: "neither", PaneID: "%1", StateDir: dir})
	if err == nil {
		t.Fatal("expected error for invalid kind")
	}
}

func TestWriteRejectsEmptyPaneID(t *testing.T) {
	err := Write(Options{Status: "idle", PaneID: ""})
	if err == nil {
		t.Fatal("expected error when TMUX_PANE empty")
	}
}

func TestWriteRejectsMalformedPaneID(t *testing.T) {
	err := Write(Options{Status: "idle", PaneID: "not-a-pane"})
	if err == nil {
		t.Fatal("expected error for malformed pane id")
	}
}

func TestWriteUsesEnvStateDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_SIDEBAR_STATE_DIR", dir)
	if err := Write(Options{Status: "idle", PaneID: "%4"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "pane_4")); got != "idle\nclaude\n" {
		t.Errorf("pane_4 in env-overridden dir = %q", got)
	}
}
