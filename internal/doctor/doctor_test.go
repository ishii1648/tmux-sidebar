package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// ── extractHookCommands ───────────────────────────────────────────────────────

func TestExtractHookCommands(t *testing.T) {
	hooks := map[string]json.RawMessage{
		"PreToolUse": json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":"echo a"},{"type":"command","command":"echo b"}]}]`),
	}
	cmds := extractHookCommands(hooks, "PreToolUse")
	if len(cmds) != 2 || cmds[0] != "echo a" || cmds[1] != "echo b" {
		t.Errorf("extractHookCommands: got %v", cmds)
	}
	if cmds := extractHookCommands(hooks, "Missing"); cmds != nil {
		t.Errorf("expected nil for missing event, got %v", cmds)
	}
}

// ── upsertHookGroup ───────────────────────────────────────────────────────────

func TestUpsertHookGroup_AddsToEmpty(t *testing.T) {
	out := upsertHookGroup(nil, "echo new")
	groups := unmarshalHookGroups(out)
	if len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Command != "echo new" {
		t.Errorf("unexpected output: %s", string(out))
	}
}

func TestUpsertHookGroup_PreservesUnrelated(t *testing.T) {
	existing := json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":"echo other-tool"}]}]`)
	out := upsertHookGroup(existing, "echo new")
	groups := unmarshalHookGroups(out)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups (preserved + new), got %d: %s", len(groups), string(out))
	}
	got := []string{groups[0].Hooks[0].Command, groups[1].Hooks[0].Command}
	if got[0] != "echo other-tool" || got[1] != "echo new" {
		t.Errorf("unexpected commands: %v", got)
	}
}

func TestUpsertHookGroup_DropsLegacy(t *testing.T) {
	legacy := `num=$(echo "$TMUX_PANE" | tr -d '%'); dir=` + legacyStateDir + `; echo running > "$dir/pane_${num}"`
	existing := json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":` + jsonString(legacy) + `}]}]`)
	out := upsertHookGroup(existing, "echo new")
	groups := unmarshalHookGroups(out)
	if len(groups) != 1 {
		t.Fatalf("expected legacy to be dropped, got %d groups: %s", len(groups), string(out))
	}
	if groups[0].Hooks[0].Command != "echo new" {
		t.Errorf("unexpected surviving command: %s", groups[0].Hooks[0].Command)
	}
}

func TestUpsertHookGroup_NoDuplicate(t *testing.T) {
	existing := json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":"echo new"}]}]`)
	out := upsertHookGroup(existing, "echo new")
	groups := unmarshalHookGroups(out)
	if len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Errorf("expected duplicate to be deduped, got %s", string(out))
	}
}

func TestRemoveHookCommand_PreservesUnrelated(t *testing.T) {
	existing := json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":"tmux-sidebar hook idle --kind codex"},{"type":"command","command":"echo keep"}]}]`)
	out := removeHookCommand(existing, "tmux-sidebar hook idle --kind codex")
	groups := unmarshalHookGroups(out)
	if len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Fatalf("expected one unrelated hook to remain, got %s", string(out))
	}
	if got := groups[0].Hooks[0].Command; got != "echo keep" {
		t.Errorf("remaining command = %q, want echo keep", got)
	}
}

func TestRemoveHookCommand_DropsEmptyEvent(t *testing.T) {
	existing := json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":"tmux-sidebar hook idle --kind codex"}]}]`)
	if out := removeHookCommand(existing, "tmux-sidebar hook idle --kind codex"); out != nil {
		t.Errorf("expected nil when all hooks are removed, got %s", string(out))
	}
}

// upsertHookGroup must upgrade an older same-identity command in place rather
// than leaving a duplicate. Stop migrates `hook idle` → `hook idle --reset`.
func TestUpsertHookGroup_ReplacesSameIdentity(t *testing.T) {
	existing := json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":"tmux-sidebar hook idle"},{"type":"command","command":"echo keep"}]}]`)
	out := upsertHookGroup(existing, "tmux-sidebar hook idle --reset")
	groups := unmarshalHookGroups(out)
	var cmds []string
	for _, g := range groups {
		for _, h := range g.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	want := map[string]bool{"echo keep": true, "tmux-sidebar hook idle --reset": true}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands (no duplicate), got %v", cmds)
	}
	for _, c := range cmds {
		if !want[c] {
			t.Errorf("unexpected command %q in %v", c, cmds)
		}
	}
}

// Migrating PostToolUse idle → running changes the status, so the same-identity
// purge isn't enough: any non-canonical tmux-sidebar hook command on the event
// must be dropped so two writers don't race on pane_N.
func TestUpsertHookGroup_ReplacesDifferentStatus(t *testing.T) {
	existing := json.RawMessage(`[{"matcher":"","hooks":[{"type":"command","command":"tmux-sidebar hook idle"},{"type":"command","command":"echo keep"}]}]`)
	out := upsertHookGroup(existing, "tmux-sidebar hook running")
	var cmds []string
	for _, g := range unmarshalHookGroups(out) {
		for _, h := range g.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands (no duplicate writer), got %v", cmds)
	}
	want := map[string]bool{"echo keep": true, "tmux-sidebar hook running": true}
	for _, c := range cmds {
		if !want[c] {
			t.Errorf("unexpected command %q in %v", c, cmds)
		}
	}
}

func TestHookCmdParts(t *testing.T) {
	cases := []struct {
		cmd          string
		status, kind string
		ok           bool
	}{
		{"tmux-sidebar hook idle", "idle", "claude", true},
		{"tmux-sidebar hook idle --reset", "idle", "claude", true},
		{"tmux-sidebar hook idle --kind codex --reset", "idle", "codex", true},
		{"tmux-sidebar hook running --kind=codex", "running", "codex", true},
		{"echo hello", "", "", false},
		{"tmux-sidebar hook", "", "", false},
	}
	for _, c := range cases {
		s, k, ok := hookCmdParts(c.cmd)
		if s != c.status || k != c.kind || ok != c.ok {
			t.Errorf("hookCmdParts(%q) = (%q,%q,%v), want (%q,%q,%v)", c.cmd, s, k, ok, c.status, c.kind, c.ok)
		}
	}
}

// ── stateRunningCmd / stateIdleCmd ────────────────────────────────────────────

func TestStateCommandsAreSubcommand(t *testing.T) {
	if got := stateRunningCmd(); got != "tmux-sidebar hook running" {
		t.Errorf("running cmd = %q, want tmux-sidebar hook running", got)
	}
	if got := stateIdleCmd(); got != "tmux-sidebar hook idle" {
		t.Errorf("idle cmd = %q, want tmux-sidebar hook idle", got)
	}
	if got := stateStopCmd(); got != "tmux-sidebar hook idle --reset" {
		t.Errorf("stop cmd = %q, want tmux-sidebar hook idle --reset", got)
	}
	if got := stateTurnStartCmd(); got != "tmux-sidebar hook running --reset" {
		t.Errorf("turn-start cmd = %q, want tmux-sidebar hook running --reset", got)
	}
	if got := stateStopCodexCmd(); got != "tmux-sidebar hook idle --kind codex --reset" {
		t.Errorf("stop codex cmd = %q, want tmux-sidebar hook idle --kind codex --reset", got)
	}
}

func TestInlineShellHookSig(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`num=$(echo "$TMUX_PANE" | tr -d '%'); dir=/tmp/agent-pane-state; mkdir -p "$dir"; printf 'running\nclaude\n' > "$dir/pane_${num}"`, true},
		{"tmux-sidebar hook running", false},
		{"echo unrelated", false},
		{`pane_${num} alone, no formatter`, false},
	}
	for _, tc := range cases {
		if got := inlineShellHookSig(tc.cmd); got != tc.want {
			t.Errorf("inlineShellHookSig(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestRequiredClaudeHooksMatchReadme(t *testing.T) {
	want := map[string]bool{"UserPromptSubmit": false, "PreToolUse": false, "PostToolUse": false, "Stop": false}
	for _, h := range requiredClaudeHooks {
		if _, ok := want[h.event]; !ok {
			t.Errorf("unexpected event in requiredClaudeHooks: %s", h.event)
		}
		want[h.event] = true
		if !strings.HasPrefix(h.command, "tmux-sidebar hook ") {
			t.Errorf("claude hook command should call subcommand, got %q", h.command)
		}
		if strings.Contains(h.command, "--kind") {
			t.Errorf("claude hook command should not need --kind, got %q", h.command)
		}
	}
	for ev, found := range want {
		if !found {
			t.Errorf("requiredClaudeHooks missing %s", ev)
		}
	}
}

func TestRequiredCodexHooksUseKindFlag(t *testing.T) {
	want := map[string]bool{"PreToolUse": false, "PermissionRequest": false, "Stop": false}
	for _, h := range requiredCodexHooks {
		if _, ok := want[h.event]; !ok {
			t.Errorf("unexpected event in requiredCodexHooks: %s", h.event)
		}
		want[h.event] = true
		if !strings.Contains(h.command, "--kind codex") {
			t.Errorf("codex hook command must include --kind codex, got %q", h.command)
		}
	}
	for ev, found := range want {
		if !found {
			t.Errorf("requiredCodexHooks missing %s", ev)
		}
	}
}

func TestCodexSettingsPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	got, err := codexSettingsPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, ".codex", "hooks.json")
	if got != want {
		t.Errorf("codexSettingsPath = %q, want %q", got, want)
	}
}

// ── applySettingsFixes ────────────────────────────────────────────────────────

func TestApplySettingsFixes_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")

	fixes := []hookFix{
		{event: "PreToolUse", command: "echo running"},
	}
	if err := applySettingsFixes(path, fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings.json not created: %v", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid JSON written: %v", err)
	}
	hooks := getHooksMap(result)
	if !hookEventPresent(hooks, "PreToolUse") {
		t.Error("PreToolUse hook should be present after fix")
	}

	// No backup should be created when there was no existing file.
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("expected no backup when source did not exist; got err=%v", err)
	}
}

func TestApplySettingsFixes_PreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(claudeDir, "settings.json")
	initial := `{"apiKey":"existing","hooks":{"Stop":[{"matcher":"","hooks":[{"type":"command","command":"echo stop"}]}]}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	fixes := []hookFix{
		{event: "PreToolUse", command: "echo running"},
	}
	if err := applySettingsFixes(path, fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings.json missing: %v", err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	var apiKey string
	if err := json.Unmarshal(result["apiKey"], &apiKey); err != nil || apiKey != "existing" {
		t.Errorf("apiKey not preserved: %s", result["apiKey"])
	}

	hooks := getHooksMap(result)
	if !hookEventPresent(hooks, "PreToolUse") {
		t.Error("PreToolUse hook should be present after fix")
	}
	if !hookEventPresent(hooks, "Stop") {
		t.Error("existing Stop hook should be preserved")
	}

	// Backup should exist with the original content.
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("expected backup at %s.bak: %v", path, err)
	}
	if string(bak) != initial {
		t.Errorf("backup content mismatch: got %q want %q", string(bak), initial)
	}
}

func TestApplySettingsFixes_UpgradesLegacy(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(claudeDir, "settings.json")
	legacyCmd := `num=1; dir=` + legacyStateDir + `; echo running > "$dir/pane_1"`
	initial := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{{
				"matcher": "",
				"hooks":   []map[string]any{{"type": "command", "command": legacyCmd}},
			}},
		},
	}
	b, _ := json.Marshal(initial)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	fixes := []hookFix{
		{event: "PreToolUse", command: stateRunningCmd()},
	}
	if err := applySettingsFixes(path, fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), legacyStateDir) {
		t.Errorf("legacy path should be purged, but still present: %s", string(data))
	}
	if !strings.Contains(string(data), "tmux-sidebar hook running") {
		t.Errorf("subcommand fix should be present, got: %s", string(data))
	}
}

// ── backupFile ────────────────────────────────────────────────────────────────

func TestBackupFile_NoSource(t *testing.T) {
	dir := t.TempDir()
	bak, err := backupFile(filepath.Join(dir, "missing"))
	if err != nil {
		t.Errorf("expected nil error for missing source, got: %v", err)
	}
	if bak != "" {
		t.Errorf("expected empty bak path for missing source, got %q", bak)
	}
}

func TestBackupFile_CopiesContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	bak, err := backupFile(src)
	if err != nil {
		t.Fatalf("backupFile: %v", err)
	}
	if bak != src+".bak" {
		t.Errorf("unexpected bak path: %s", bak)
	}
	got, err := os.ReadFile(bak)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("backup content mismatch: %q", string(got))
	}
}

// ── tmuxConfContains (kept for back-compat) ───────────────────────────────────

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

// ── confDoc parsing ───────────────────────────────────────────────────────────

func newConf(t *testing.T, body string) *confDoc {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".tmux.conf")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	return loadTmuxConf()
}

func TestLoadTmuxConf_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := loadTmuxConf()
	if d.found {
		t.Error("expected not-found")
	}
}

func TestConfDoc_StripsComments(t *testing.T) {
	d := newConf(t, "# set-hook -g after-new-window 'commented out'\nset-hook -g after-new-window 'real'\n")
	if !d.hasHook("after-new-window", "real") {
		t.Error("expected to find real after-new-window hook")
	}
	if d.hasHook("after-new-window", "commented out") {
		t.Error("comment line should be stripped")
	}
}

func TestConfDoc_JoinsContinuations(t *testing.T) {
	body := "set-hook -g client-resized \\\n  'run-shell \"tmux list-panes -aF @pane_role | resize-pane -x 40\"'\n"
	d := newConf(t, body)
	if !d.hasHook("client-resized", "@pane_role") {
		t.Errorf("continuation lines should be joined; lines=%v", d.lines)
	}
}

func TestConfDoc_HookUsesAppend(t *testing.T) {
	d := newConf(t, "set-hook -ga window-linked 'run-shell \"echo x\"'\n")
	if !d.hookUsesAppend("window-linked") {
		t.Error("expected -ga to be detected")
	}
	if d.hookUsesAppend("window-unlinked") {
		t.Error("should not detect -ga for unrelated event")
	}
}

func TestConfDoc_HasOption(t *testing.T) {
	d := newConf(t, "set-option -g focus-events on\n")
	if !d.hasOption("focus-events", "on") {
		t.Error("expected focus-events on")
	}
	if d.hasOption("focus-events", "off") {
		t.Error("should not match off when set to on")
	}

	d2 := newConf(t, "set -g focus-events on\n")
	if !d2.hasOption("focus-events", "on") {
		t.Error("expected `set -g` shorthand to be recognised")
	}

	d3 := newConf(t, "# set -g focus-events on\n")
	if d3.hasOption("focus-events", "on") {
		t.Error("commented-out option should not match")
	}
}

// ── checkTmuxHooks ────────────────────────────────────────────────────────────

func TestCheckTmuxHooks_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := loadTmuxConf()
	results := checkTmuxHooks(d)
	if len(results) != len(requiredTmuxHooks) {
		t.Errorf("expected %d results, got %d", len(requiredTmuxHooks), len(results))
	}
	for _, r := range results {
		if r.detail != "tmux.conf not found" {
			t.Errorf("unexpected detail when conf is missing: %q", r.detail)
		}
	}
}

func TestCheckTmuxHooks_FullCoverage(t *testing.T) {
	body := `
set-hook -g after-new-window 'run-shell "tmux split-window -hfb -d -l 40 tmux-sidebar"'
set-hook -g after-new-session 'run-shell "tmux split-window -hfb -d -l 40 tmux-sidebar"'
set-hook -g after-select-window 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do : ; done"'
set-hook -g client-session-changed 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do : ; done"'
set-hook -g pane-exited 'run-shell "tmux-sidebar cleanup-if-only-sidebar"'
set-hook -g after-kill-pane 'run-shell "tmux-sidebar cleanup-if-only-sidebar"'
set-hook -g client-resized 'run-shell "tmux list-panes -aF @pane_role | resize-pane -x 40"'
set-hook -g window-linked 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do : ; done"'
set-hook -g window-unlinked 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do : ; done"'
set-hook -g session-created 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do : ; done"'
set-hook -g session-closed 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do : ; done"'
`
	d := newConf(t, body)
	results := checkTmuxHooks(d)
	for _, r := range results {
		if r.sev != sevOK {
			t.Errorf("expected OK for %s, got sev=%d detail=%q", r.label, r.sev, r.detail)
		}
	}
}

func TestCheckTmuxHooks_DetectsAppend(t *testing.T) {
	body := "set-hook -ga after-new-window 'run-shell \"tmux split-window -hfb tmux-sidebar\"'\n"
	d := newConf(t, body)
	results := checkTmuxHooks(d)
	var found bool
	for _, r := range results {
		if r.label == "after-new-window" && r.sev == sevWarn && strings.Contains(r.detail, "-ga") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -ga warning for after-new-window: %+v", results)
	}
}

// ── checkTmuxOptions ──────────────────────────────────────────────────────────

func TestCheckTmuxOptions_FocusEventsOn(t *testing.T) {
	d := newConf(t, "set-option -g focus-events on\n")
	results := checkTmuxOptions(d)
	if len(results) != 1 || results[0].sev != sevOK {
		t.Errorf("expected single OK result, got %+v", results)
	}
}

func TestCheckTmuxOptions_FocusEventsMissing(t *testing.T) {
	d := newConf(t, "# nothing relevant\n")
	results := checkTmuxOptions(d)
	if len(results) != 1 || results[0].sev != sevWarn {
		t.Errorf("expected single WARN result, got %+v", results)
	}
}

// ── checkTmuxBindings ─────────────────────────────────────────────────────────

func TestCheckTmuxBindings(t *testing.T) {
	d := newConf(t, "bind-key e run-shell 'tmux-sidebar toggle'\nbind-key -n F1 run-shell 'tmux-sidebar focus-or-open'\n")
	results := checkTmuxBindings(d)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.sev != sevOK {
			t.Errorf("expected OK for %s, got sev=%d", r.label, r.sev)
		}
	}
}

func TestCheckTmuxBindings_OptionalsMissing(t *testing.T) {
	d := newConf(t, "# nothing\n")
	results := checkTmuxBindings(d)
	for _, r := range results {
		if r.sev != sevInfo {
			t.Errorf("expected INFO for missing optional binding %s, got sev=%d", r.label, r.sev)
		}
	}
}

// ── checkWidthConsistency ─────────────────────────────────────────────────────

func TestWidthConsistency_AllAgree(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("TMUX_SIDEBAR_WIDTH", "")
	cfgDir := filepath.Join(tmpHome, ".config", "tmux-sidebar")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "width"), []byte("40\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `
set-hook -g after-new-window 'run-shell "tmux split-window -hfb -d -l 40 tmux-sidebar"'
set-hook -g after-new-session 'run-shell "tmux split-window -hfb -d -l 40 tmux-sidebar"'
set-hook -g client-resized 'run-shell "resize-pane -x 40"'
`
	if err := os.WriteFile(filepath.Join(tmpHome, ".tmux.conf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d := loadTmuxConf()
	results := checkWidthConsistency(d)
	if len(results) != 1 || results[0].sev != sevOK {
		t.Errorf("expected single OK result, got %+v", results)
	}
}

func TestWidthConsistency_Mismatch(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("TMUX_SIDEBAR_WIDTH", "")
	body := `
set-hook -g after-new-window 'run-shell "tmux split-window -hfb -d -l 40 tmux-sidebar"'
set-hook -g client-resized 'run-shell "resize-pane -x 35"'
`
	if err := os.WriteFile(filepath.Join(tmpHome, ".tmux.conf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d := loadTmuxConf()
	results := checkWidthConsistency(d)
	if len(results) != 1 || results[0].sev != sevWarn {
		t.Errorf("expected single WARN result, got %+v", results)
	}
	if !strings.Contains(results[0].detail, "mismatch") {
		t.Errorf("expected mismatch detail, got %q", results[0].detail)
	}
}

// ── checkClaudeSettings (legacy detection) ────────────────────────────────────

func TestCheckClaudeSettings_FlagsLegacy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyCmd := `num=1; dir=` + legacyStateDir + `; echo running > "$dir/pane_1"`
	initial := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{{
				"matcher": "",
				"hooks":   []map[string]any{{"type": "command", "command": legacyCmd}},
			}},
		},
	}
	b, _ := json.Marshal(initial)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	target := agentTarget{kind: "claude", titlePrefix: "Claude", pathFn: claudeSettingsPath, requiredHooks: requiredClaudeHooks}
	results, fixes := checkAgentSettings(target)
	var preToolUse checkResult
	for _, r := range results {
		if r.label == "PreToolUse" {
			preToolUse = r
		}
	}
	if preToolUse.sev != sevWarn {
		t.Errorf("expected sevWarn for legacy PreToolUse, got %+v", preToolUse)
	}
	if !strings.Contains(preToolUse.detail, "legacy") {
		t.Errorf("expected legacy hint in detail, got %q", preToolUse.detail)
	}
	var fixed bool
	for _, f := range fixes {
		if f.event == "PreToolUse" {
			fixed = true
		}
	}
	if !fixed {
		t.Error("expected PreToolUse to be queued for fix when legacy detected")
	}
}

// ── checkAgentSettings: codex backend ─────────────────────────────────────────

func TestCheckAgentSettings_CodexNotConfigured(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	target := agentTarget{kind: "codex", titlePrefix: "Codex", pathFn: codexSettingsPath, requiredHooks: requiredCodexHooks}
	results, fixes := checkAgentSettings(target)
	if len(results) != len(requiredCodexHooks) {
		t.Fatalf("expected %d results, got %d", len(requiredCodexHooks), len(results))
	}
	for _, r := range results {
		if r.sev != sevWarn {
			t.Errorf("expected sevWarn for missing Codex hook %s, got %+v", r.label, r)
		}
	}
	if len(fixes) != len(requiredCodexHooks) {
		t.Errorf("expected all Codex hooks queued for fix, got %d", len(fixes))
	}
}

func TestCheckAgentSettings_CodexKindMismatch(t *testing.T) {
	// Settings file at the Codex location but the command lacks --kind codex.
	// Doctor should flag it as kind-mismatch so pane_N's agent line stays
	// correct after auto-fix.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Each event has a `tmux-sidebar hook` call WITHOUT --kind codex.
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse":        []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "tmux-sidebar hook running"}}}},
			"PermissionRequest": []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "tmux-sidebar hook permission"}}}},
			"Stop":              []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "tmux-sidebar hook idle"}}}},
		},
	}
	b, _ := json.Marshal(settings)
	if err := os.WriteFile(filepath.Join(codexDir, "hooks.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	target := agentTarget{kind: "codex", titlePrefix: "Codex", pathFn: codexSettingsPath, requiredHooks: requiredCodexHooks}
	results, fixes := checkAgentSettings(target)
	for _, r := range results {
		if r.sev != sevWarn || !strings.Contains(r.detail, "subcommand mismatch") {
			t.Errorf("expected subcommand-mismatch warning for %s, got %+v", r.label, r)
		}
	}
	if len(fixes) != 3 {
		t.Errorf("expected 3 fixes queued, got %d", len(fixes))
	}
}

// A stale idle (or stray --reset) on Claude PostToolUse must be flagged by the
// normalized exact compare and replaced in place with the canonical command
// (running) — the substring check missed it, and the replace must not leave a
// second writer behind even though the status differs (idle → running).
func TestCheckAgentSettings_ClaudeStrayResetOnPostToolUse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateTurnStartCmd()}}}},
			"PreToolUse":       []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateRunningCmd()}}}},
			"PostToolUse":      []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "tmux-sidebar hook idle --reset"}}}},
			"Stop":             []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateStopCmd()}}}},
		},
	}
	b, _ := json.Marshal(settings)
	path := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	target := agentTarget{kind: "claude", titlePrefix: "Claude", pathFn: claudeSettingsPath, requiredHooks: requiredClaudeHooks}
	results, fixes := checkAgentSettings(target)
	var post checkResult
	for _, r := range results {
		if r.label == "PostToolUse" {
			post = r
		}
	}
	if post.sev != sevWarn || !strings.Contains(post.detail, "subcommand mismatch") {
		t.Fatalf("expected PostToolUse mismatch warning, got %+v", post)
	}
	if len(fixes) != 1 || fixes[0].event != "PostToolUse" || fixes[0].remove {
		t.Fatalf("expected one PostToolUse upgrade fix, got %+v", fixes)
	}

	if err := applySettingsFixes(path, fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}
	raw, _ := readRawSettings(path)
	cmds := extractHookCommands(getHooksMap(raw), "PostToolUse")
	if len(cmds) != 1 || cmds[0] != stateRunningCmd() {
		t.Fatalf("PostToolUse should be exactly [%q], got %v", stateRunningCmd(), cmds)
	}
}

// A bare UserPromptSubmit `running` (no --reset) must be upgraded in place to the
// canonical `running --reset`, otherwise a new turn would not re-anchor
// pane_N_started and the elapsed clock could accumulate across interrupted turns
// (issues/0018).
func TestCheckAgentSettings_ClaudeUserPromptSubmitMissingReset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateRunningCmd()}}}},
			"PreToolUse":       []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateRunningCmd()}}}},
			"PostToolUse":      []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateRunningCmd()}}}},
			"Stop":             []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateStopCmd()}}}},
		},
	}
	b, _ := json.Marshal(settings)
	path := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	target := agentTarget{kind: "claude", titlePrefix: "Claude", pathFn: claudeSettingsPath, requiredHooks: requiredClaudeHooks}
	results, fixes := checkAgentSettings(target)
	var ups checkResult
	for _, r := range results {
		if r.label == "UserPromptSubmit" {
			ups = r
		}
	}
	if ups.sev != sevWarn || !strings.Contains(ups.detail, "subcommand mismatch") {
		t.Fatalf("expected UserPromptSubmit mismatch warning, got %+v", ups)
	}
	if len(fixes) != 1 || fixes[0].event != "UserPromptSubmit" || fixes[0].remove {
		t.Fatalf("expected one UserPromptSubmit upgrade fix, got %+v", fixes)
	}

	if err := applySettingsFixes(path, fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}
	raw, _ := readRawSettings(path)
	cmds := extractHookCommands(getHooksMap(raw), "UserPromptSubmit")
	if len(cmds) != 1 || cmds[0] != stateTurnStartCmd() {
		t.Fatalf("UserPromptSubmit should be exactly [%q], got %v", stateTurnStartCmd(), cmds)
	}
}

// A stray `running --reset` on PreToolUse must be flagged and replaced with the
// canonical `running`: with the issues/0018 semantics, `running --reset`
// re-anchors pane_N_started every tool, resetting the elapsed clock to 0 each
// time (the 0015 bug redux). The normalized exact compare catches it.
func TestCheckAgentSettings_ClaudeStrayResetOnPreToolUse(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateTurnStartCmd()}}}},
			"PreToolUse":       []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "tmux-sidebar hook running --reset"}}}},
			"PostToolUse":      []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateRunningCmd()}}}},
			"Stop":             []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateStopCmd()}}}},
		},
	}
	b, _ := json.Marshal(settings)
	path := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	target := agentTarget{kind: "claude", titlePrefix: "Claude", pathFn: claudeSettingsPath, requiredHooks: requiredClaudeHooks}
	results, fixes := checkAgentSettings(target)
	var pre checkResult
	for _, r := range results {
		if r.label == "PreToolUse" {
			pre = r
		}
	}
	if pre.sev != sevWarn || !strings.Contains(pre.detail, "subcommand mismatch") {
		t.Fatalf("expected PreToolUse mismatch warning, got %+v", pre)
	}
	if len(fixes) != 1 || fixes[0].event != "PreToolUse" || fixes[0].remove {
		t.Fatalf("expected one PreToolUse upgrade fix, got %+v", fixes)
	}

	if err := applySettingsFixes(path, fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}
	raw, _ := readRawSettings(path)
	cmds := extractHookCommands(getHooksMap(raw), "PreToolUse")
	if len(cmds) != 1 || cmds[0] != stateRunningCmd() {
		t.Fatalf("PreToolUse should be exactly [%q], got %v", stateRunningCmd(), cmds)
	}
}

// A required event that already holds the user's own unrelated hooks (so
// hookEventPresent is true) but lacks the canonical state-writer must still be
// flagged and fixed — otherwise doctor reports OK and never installs the writer.
// This is the common real case for UserPromptSubmit (an SSH banner hook) and
// PostToolUse (a Skill counter).
func TestCheckAgentSettings_ClaudeMissingWriterAmongUnrelatedHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"hooks": map[string]any{
			// UserPromptSubmit has an unrelated hook but no tmux-sidebar writer.
			"UserPromptSubmit": []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "echo banner"}}}},
			"PreToolUse":       []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateRunningCmd()}}}},
			"PostToolUse":      []map[string]any{{"matcher": "Skill", "hooks": []map[string]any{{"type": "command", "command": "~/.claude/scripts/skill-call-counter.sh"}}}},
			"Stop":             []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateStopCmd()}}}},
		},
	}
	b, _ := json.Marshal(settings)
	path := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	target := agentTarget{kind: "claude", titlePrefix: "Claude", pathFn: claudeSettingsPath, requiredHooks: requiredClaudeHooks}
	results, fixes := checkAgentSettings(target)

	byEvent := map[string]checkResult{}
	for _, r := range results {
		byEvent[r.label] = r
	}
	for _, ev := range []string{"UserPromptSubmit", "PostToolUse"} {
		if byEvent[ev].sev != sevWarn || !strings.Contains(byEvent[ev].detail, "missing") {
			t.Errorf("%s should warn about missing writer, got %+v", ev, byEvent[ev])
		}
	}
	if byEvent["PreToolUse"].sev != sevOK || byEvent["Stop"].sev != sevOK {
		t.Errorf("PreToolUse/Stop should be OK, got %+v / %+v", byEvent["PreToolUse"], byEvent["Stop"])
	}
	fixedEvents := map[string]bool{}
	for _, f := range fixes {
		fixedEvents[f.event] = true
	}
	if !fixedEvents["UserPromptSubmit"] || !fixedEvents["PostToolUse"] {
		t.Fatalf("expected UserPromptSubmit and PostToolUse queued for fix, got %v", fixes)
	}

	if err := applySettingsFixes(path, fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}
	raw, _ := readRawSettings(path)
	hooks := getHooksMap(raw)
	ups := extractHookCommands(hooks, "UserPromptSubmit")
	if !slices.Contains(ups, stateTurnStartCmd()) || !slices.Contains(ups, "echo banner") {
		t.Errorf("UserPromptSubmit should keep banner + add writer, got %v", ups)
	}
	post := extractHookCommands(hooks, "PostToolUse")
	if !slices.Contains(post, stateRunningCmd()) || !slices.Contains(post, "~/.claude/scripts/skill-call-counter.sh") {
		t.Errorf("PostToolUse should keep counter + add writer, got %v", post)
	}
}

func TestIsStaleCodexPostToolUse(t *testing.T) {
	stale := []string{
		"tmux-sidebar hook idle",
		"tmux-sidebar hook idle --kind codex",
		"tmux-sidebar hook idle --kind codex --reset",
		"tmux-sidebar hook idle --reset",
	}
	for _, c := range stale {
		if !isStaleCodexPostToolUse(c) {
			t.Errorf("expected %q to be stale", c)
		}
	}
	notStale := []string{
		"tmux-sidebar hook running --kind codex",
		"echo hello",
		"agent-telemetry hook stop",
	}
	for _, c := range notStale {
		if isStaleCodexPostToolUse(c) {
			t.Errorf("expected %q NOT to be stale", c)
		}
	}
}

func TestCheckAgentSettings_CodexStalePostToolUseQueuedForRemoval(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	codexDir := filepath.Join(dir, ".codex")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse":        []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "tmux-sidebar hook running --kind codex"}}}},
			"PermissionRequest": []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "tmux-sidebar hook permission --kind codex"}}}},
			"PostToolUse":       []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": "tmux-sidebar hook idle --kind codex"}}}},
			"Stop":              []map[string]any{{"matcher": "", "hooks": []map[string]any{{"type": "command", "command": stateStopCodexCmd()}}}},
		},
	}
	b, _ := json.Marshal(settings)
	path := filepath.Join(codexDir, "hooks.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	target := agentTarget{kind: "codex", titlePrefix: "Codex", pathFn: codexSettingsPath, requiredHooks: requiredCodexHooks}
	results, fixes := checkAgentSettings(target)
	var stale checkResult
	for _, r := range results {
		if r.label == "PostToolUse" {
			stale = r
		}
	}
	if stale.sev != sevWarn || !strings.Contains(stale.detail, "stale") {
		t.Fatalf("expected stale PostToolUse warning, got %+v", stale)
	}
	if len(fixes) != 1 || !fixes[0].remove || fixes[0].event != "PostToolUse" {
		t.Fatalf("expected one remove fix for PostToolUse, got %+v", fixes)
	}

	if err := applySettingsFixes(path, fixes); err != nil {
		t.Fatalf("applySettingsFixes: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PostToolUse") {
		t.Errorf("PostToolUse should be removed, got %s", string(data))
	}
}

// ── tmux version parsing ─────────────────────────────────────────────────────

func TestParseTmuxVersion(t *testing.T) {
	cases := []struct {
		in       string
		maj, min int
		ok       bool
	}{
		{"tmux 3.4", 3, 4, true},
		{"tmux 3.2a", 3, 2, true},
		{"tmux next-3.5", 3, 5, true},
		{"tmux 2.9", 2, 9, true},
		{"tmux master", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		maj, min, ok := parseTmuxVersion(c.in)
		if maj != c.maj || min != c.min || ok != c.ok {
			t.Errorf("parseTmuxVersion(%q) = (%d, %d, %v), want (%d, %d, %v)",
				c.in, maj, min, ok, c.maj, c.min, c.ok)
		}
	}
}

// ── helper ────────────────────────────────────────────────────────────────────

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
