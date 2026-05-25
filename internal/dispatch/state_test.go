package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteInitialPaneStateCodexPromptStartsRunning(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_SIDEBAR_STATE_DIR", dir)

	if err := writeInitialPaneState("%12", "/work/repo", LauncherCodex, false); err != nil {
		t.Fatalf("writeInitialPaneState: %v", err)
	}

	if got := readDispatchTestFile(t, filepath.Join(dir, "pane_12")); got != "running\ncodex\n" {
		t.Errorf("pane_12 = %q, want running/codex", got)
	}
	if got := strings.TrimSpace(readDispatchTestFile(t, filepath.Join(dir, "pane_12_path"))); got != "/work/repo" {
		t.Errorf("pane_12_path = %q, want /work/repo", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "pane_12_started")); err != nil {
		t.Errorf("pane_12_started should exist: %v", err)
	}
}

func TestWriteInitialPaneStateNoPromptStartsIdleTag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMUX_SIDEBAR_STATE_DIR", dir)

	if err := writeInitialPaneState("%13", "/work/repo", LauncherCodex, true); err != nil {
		t.Fatalf("writeInitialPaneState: %v", err)
	}

	if got := readDispatchTestFile(t, filepath.Join(dir, "pane_13")); got != "idle\ncodex\n" {
		t.Errorf("pane_13 = %q, want idle/codex", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "pane_13_started")); !os.IsNotExist(err) {
		t.Errorf("pane_13_started should not exist for idle launch: %v", err)
	}
}

func readDispatchTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
