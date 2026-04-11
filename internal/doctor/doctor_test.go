package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ── readRawSettings ───────────────────────────────────────────────────────────

func TestReadRawSettings_FileNotExist(t *testing.T) {
	raw, err := readRawSettings("/nonexistent/path/settings.json")
	if err != nil {
		t.Errorf("expected nil error for missing file, got: %v", err)
	}
	if len(raw) != 0 {
		t.Errorf("expected empty map for missing file, got %d keys", len(raw))
	}
}

func TestReadRawSettings_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `{"apiKey":"test","hooks":{}}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := readRawSettings(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := raw["apiKey"]; !ok {
		t.Error("apiKey should be preserved")
	}
	if _, ok := raw["hooks"]; !ok {
		t.Error("hooks key should be present")
	}
}

func TestReadRawSettings_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readRawSettings(path)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// ── hookEventPresent ──────────────────────────────────────────────────────────

func TestHookEventPresent_Missing(t *testing.T) {
	hooks := map[string]json.RawMessage{}
	if hookEventPresent(hooks, "UserPromptSubmit") {
		t.Error("hookEventPresent: expected false for missing event")
	}
}

func TestHookEventPresent_EmptyArray(t *testing.T) {
	hooks := map[string]json.RawMessage{
		"UserPromptSubmit": json.RawMessage(`[]`),
	}
	if hookEventPresent(hooks, "UserPromptSubmit") {
		t.Error("hookEventPresent: expected false for empty array")
	}
}

func TestHookEventPresent_WithEntries(t *testing.T) {
	hooks := map[string]json.RawMessage{
		"UserPromptSubmit": json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":"echo ok"}]}]`),
	}
	if !hookEventPresent(hooks, "UserPromptSubmit") {
		t.Error("hookEventPresent: expected true for non-empty array")
	}
}

// ── applySettingsFixes ────────────────────────────────────────────────────────

func TestApplySettingsFixes_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	// Override the path resolution by creating an empty claude dir
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	fixes := []hookFix{
		{event: "UserPromptSubmit", command: "echo running"},
	}
	if err := applySettingsFixes(fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}

	// Verify the file was created
	path := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid JSON written: %v", err)
	}
	hooks := getHooksMap(result)
	if !hookEventPresent(hooks, "UserPromptSubmit") {
		t.Error("UserPromptSubmit hook should be present after fix")
	}
}

func TestApplySettingsFixes_PreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	// Create existing settings.json with an unrelated key
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{"apiKey":"existing","hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"echo stop"}]}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply a fix for missing UserPromptSubmit
	fixes := []hookFix{
		{event: "UserPromptSubmit", command: "echo running"},
	}
	if err := applySettingsFixes(fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}

	path := filepath.Join(claudeDir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings.json missing: %v", err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// apiKey must be preserved
	var apiKey string
	if err := json.Unmarshal(result["apiKey"], &apiKey); err != nil || apiKey != "existing" {
		t.Errorf("apiKey not preserved: %s", result["apiKey"])
	}

	hooks := getHooksMap(result)

	// UserPromptSubmit was added
	if !hookEventPresent(hooks, "UserPromptSubmit") {
		t.Error("UserPromptSubmit hook should be present after fix")
	}
	// Stop was preserved
	if !hookEventPresent(hooks, "Stop") {
		t.Error("existing Stop hook should be preserved")
	}
}

// ── checkTmuxConf ─────────────────────────────────────────────────────────────

func TestTmuxConfContains_True(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tmux.conf")
	content := "set-hook -g after-new-window 'run-shell \"tmux-sidebar\"'"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !tmuxConfContains(path, "after-new-window") {
		t.Error("tmuxConfContains: expected true")
	}
}

func TestTmuxConfContains_False(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tmux.conf")
	if err := os.WriteFile(path, []byte("# empty config"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tmuxConfContains(path, "after-new-window") {
		t.Error("tmuxConfContains: expected false")
	}
}

func TestTmuxConfContains_MissingFile(t *testing.T) {
	if tmuxConfContains("/nonexistent/.tmux.conf", "after-new-window") {
		t.Error("tmuxConfContains: expected false for missing file")
	}
}
