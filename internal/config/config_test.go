package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_FileNotExist(t *testing.T) {
	cfg, err := Load("/nonexistent/path/hidden_sessions")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if len(cfg.HiddenSessions) != 0 {
		t.Errorf("expected empty HiddenSessions, got %v", cfg.HiddenSessions)
	}
}

func TestLoad_ParsesLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hidden_sessions")
	content := `# comment
main
scratch

  # another comment
  spaced-session
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"main", "scratch", "spaced-session"}
	for _, name := range want {
		if !cfg.IsHiddenSession(name) {
			t.Errorf("expected %q to be hidden", name)
		}
	}
	if cfg.IsHiddenSession("other") {
		t.Error("expected \"other\" not to be hidden")
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hidden_sessions")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.HiddenSessions) != 0 {
		t.Errorf("expected empty HiddenSessions for empty file, got %v", cfg.HiddenSessions)
	}
}

// ── pinned_sessions ─────────────────────────────────────────────────────────

func TestLoad_PinnedSessionsParsesAndPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	hiddenPath := filepath.Join(dir, "hidden_sessions")
	pinnedPath := filepath.Join(dir, "pinned_sessions")
	if err := os.WriteFile(hiddenPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	pinnedContent := `# pinned in display order
work
infra
  scratch
`
	if err := os.WriteFile(pinnedPath, []byte(pinnedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(hiddenPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"work", "infra", "scratch"}
	if len(cfg.PinnedSessions) != len(want) {
		t.Fatalf("PinnedSessions = %v, want %v", cfg.PinnedSessions, want)
	}
	for i, n := range want {
		if cfg.PinnedSessions[i] != n {
			t.Errorf("PinnedSessions[%d] = %q, want %q", i, cfg.PinnedSessions[i], n)
		}
		if !cfg.IsPinnedSession(n) {
			t.Errorf("IsPinnedSession(%q) = false, want true", n)
		}
		if got := cfg.PinnedOrder(n); got != i {
			t.Errorf("PinnedOrder(%q) = %d, want %d", n, got, i)
		}
	}
	if cfg.IsPinnedSession("not-here") {
		t.Errorf("IsPinnedSession(not-here) should be false")
	}
	if got := cfg.PinnedOrder("not-here"); got != -1 {
		t.Errorf("PinnedOrder(not-here) = %d, want -1", got)
	}
}

func TestLoad_PinnedFileMissingIsNotError(t *testing.T) {
	dir := t.TempDir()
	hiddenPath := filepath.Join(dir, "hidden_sessions")
	if err := os.WriteFile(hiddenPath, []byte("h\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(hiddenPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.PinnedSessions) != 0 {
		t.Errorf("expected empty PinnedSessions when file is missing, got %v", cfg.PinnedSessions)
	}
}

func TestPinnedConfigPath_NotEmpty(t *testing.T) {
	if PinnedConfigPath() == "" {
		t.Error("PinnedConfigPath returned empty string (HOME unavailable?)")
	}
}
