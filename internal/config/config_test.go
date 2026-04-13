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
