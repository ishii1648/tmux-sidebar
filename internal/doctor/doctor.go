// Package doctor implements the "tmux-sidebar doctor" subcommand, which
// diagnoses and auto-repairs hook configuration for tmux-sidebar.
package doctor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── styles ────────────────────────────────────────────────────────────────────

var (
	styleOK    = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	styleWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	styleError = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleDim   = lipgloss.NewStyle().Faint(true)
)

// ── check result ─────────────────────────────────────────────────────────────

type severity int

const (
	sevOK severity = iota
	sevWarn
	sevError
)

type checkResult struct {
	label    string
	sev      severity
	detail   string
}

func (c checkResult) render() string {
	var badge string
	switch c.sev {
	case sevOK:
		badge = styleOK.Render("[OK]   ")
	case sevWarn:
		badge = styleWarn.Render("[WARN] ")
	case sevError:
		badge = styleError.Render("[ERROR]")
	}
	return fmt.Sprintf("  %s  %-26s %s", badge, c.label, styleDim.Render(c.detail))
}

// ── hook fix ─────────────────────────────────────────────────────────────────

// hookFix describes a settings.json hook event to add.
type hookFix struct {
	event   string
	command string
}

// Required hook commands. These match the inline shell fragments shown in README.
var requiredHooks = []hookFix{
	{
		event: "UserPromptSubmit",
		command: `num=$(echo "$TMUX_PANE" | tr -d '%'); dir=/tmp/claude-pane-state; mkdir -p "$dir"; echo running > "$dir/pane_${num}"; date +%s > "$dir/pane_${num}_started"`,
	},
	{
		event:   "Stop",
		command: `num=$(echo "$TMUX_PANE" | tr -d '%'); dir=/tmp/claude-pane-state; mkdir -p "$dir"; echo idle > "$dir/pane_${num}"`,
	},
	{
		event:   "PostToolUse",
		command: `num=$(echo "$TMUX_PANE" | tr -d '%'); dir=/tmp/claude-pane-state; mkdir -p "$dir"; echo idle > "$dir/pane_${num}"`,
	},
}

// Required tmux.conf lines (searched as substrings).
var requiredTmuxHooks = []struct {
	label  string
	needle string
	hint   string
}{
	{
		label:  "after-new-window",
		needle: "after-new-window",
		hint:   `set-hook -g after-new-window 'run-shell "tmux split-window -hfb -l 35 -e @pane_role=sidebar tmux-sidebar"'`,
	},
	{
		label:  "focus-sidebar binding",
		needle: "focus-sidebar",
		hint:   `bind-key -n <key> run-shell 'tmux-sidebar focus-sidebar'`,
	},
	{
		label:  "toggle binding",
		needle: "toggle",
		hint:   `bind-key -n C-s run-shell 'tmux-sidebar toggle'`,
	},
}

// ── settings.json helpers ────────────────────────────────────────────────────

// settingsPath returns the path to ~/.claude/settings.json.
func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// readRawSettings reads settings.json as a generic map.
// If the file does not exist, an empty map is returned (no error).
func readRawSettings(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]json.RawMessage{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return raw, nil
}

// getHooksMap extracts the "hooks" sub-object from raw settings.
// Returns an empty map when the key is absent or not an object.
func getHooksMap(raw map[string]json.RawMessage) map[string]json.RawMessage {
	hooksRaw, ok := raw["hooks"]
	if !ok {
		return map[string]json.RawMessage{}
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(hooksRaw, &hooks); err != nil {
		return map[string]json.RawMessage{}
	}
	return hooks
}

// hookEventPresent reports whether the given event key exists in the hooks map
// with at least one hook entry.
func hookEventPresent(hooks map[string]json.RawMessage, event string) bool {
	val, ok := hooks[event]
	if !ok {
		return false
	}
	var groups []json.RawMessage
	if err := json.Unmarshal(val, &groups); err != nil {
		return false
	}
	return len(groups) > 0
}

// newHookGroupJSON returns a JSON-encoded hook group array for a single command.
func newHookGroupJSON(command string) json.RawMessage {
	type hookEntry struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	type hookGroup struct {
		Matcher string      `json:"matcher"`
		Hooks   []hookEntry `json:"hooks"`
	}
	group := []hookGroup{
		{
			Matcher: "",
			Hooks:   []hookEntry{{Type: "command", Command: command}},
		},
	}
	data, _ := json.Marshal(group)
	return data
}

// ── tmux.conf helpers ─────────────────────────────────────────────────────────

// findTmuxConf returns the first existing tmux configuration file.
// Returns ("", false) when none is found.
func findTmuxConf() (string, bool) {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".tmux.conf"),
		filepath.Join(home, ".config", "tmux", "tmux.conf"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// tmuxConfContains reports whether the given file contains needle as a substring.
func tmuxConfContains(path, needle string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

// ── diagnosis ─────────────────────────────────────────────────────────────────

// checkClaudeSettings checks ~/.claude/settings.json for required hooks.
// Returns check results and a list of hook events that need to be added.
func checkClaudeSettings() (results []checkResult, fixes []hookFix) {
	path, err := settingsPath()
	if err != nil {
		results = append(results, checkResult{
			label:  "settings.json",
			sev:    sevError,
			detail: err.Error(),
		})
		return
	}

	fileExists := true
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fileExists = false
	}

	raw, err := readRawSettings(path)
	if err != nil {
		results = append(results, checkResult{
			label:  "settings.json",
			sev:    sevError,
			detail: err.Error(),
		})
		return
	}

	hooks := getHooksMap(raw)

	for _, fix := range requiredHooks {
		switch {
		case !fileExists:
			results = append(results, checkResult{
				label:  fix.event,
				sev:    sevWarn,
				detail: "settings.json not found",
			})
			fixes = append(fixes, fix)
		case !hookEventPresent(hooks, fix.event):
			results = append(results, checkResult{
				label:  fix.event,
				sev:    sevWarn,
				detail: "hook not configured",
			})
			fixes = append(fixes, fix)
		default:
			results = append(results, checkResult{
				label:  fix.event,
				sev:    sevOK,
				detail: "hook configured",
			})
		}
	}
	return
}

// checkTmuxConf checks tmux.conf for required hooks.
func checkTmuxConf() []checkResult {
	confPath, found := findTmuxConf()

	var results []checkResult
	for _, h := range requiredTmuxHooks {
		switch {
		case !found:
			results = append(results, checkResult{
				label:  h.label,
				sev:    sevWarn,
				detail: "tmux.conf not found",
			})
		case tmuxConfContains(confPath, h.needle):
			results = append(results, checkResult{
				label:  h.label,
				sev:    sevOK,
				detail: "hook found in " + confPath,
			})
		default:
			results = append(results, checkResult{
				label:  h.label,
				sev:    sevWarn,
				detail: fmt.Sprintf("not found — add: %s", h.hint),
			})
		}
	}
	return results
}

// ── fix application ───────────────────────────────────────────────────────────

// applySettingsFixes writes the missing hooks into ~/.claude/settings.json,
// non-destructively merging with any existing content.
func applySettingsFixes(fixes []hookFix) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}

	raw, err := readRawSettings(path)
	if err != nil {
		return err
	}

	hooks := getHooksMap(raw)

	for _, fix := range fixes {
		hooks[fix.event] = newHookGroupJSON(fix.command)
	}

	hooksEncoded, err := json.Marshal(hooks)
	if err != nil {
		return fmt.Errorf("encode hooks: %w", err)
	}
	raw["hooks"] = hooksEncoded

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Printf("Updated %s\n", path)
	return nil
}

// ── public entry point ────────────────────────────────────────────────────────

// Run executes the doctor diagnostic.
// If autoApply is true, missing settings.json hooks are added without prompting.
func Run(autoApply bool) error {
	fmt.Println("tmux-sidebar doctor")
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	// ── ~/.claude/settings.json ──────────────────
	path, _ := settingsPath()
	fmt.Printf("Checking %s\n", path)
	settingsResults, fixes := checkClaudeSettings()
	for _, r := range settingsResults {
		fmt.Println(r.render())
	}
	fmt.Println()

	// ── tmux.conf ────────────────────────────────
	confPath, confFound := findTmuxConf()
	if confFound {
		fmt.Printf("Checking %s\n", confPath)
	} else {
		fmt.Println("Checking ~/.tmux.conf (not found)")
	}
	tmuxResults := checkTmuxConf()
	for _, r := range tmuxResults {
		fmt.Println(r.render())
	}
	fmt.Println()

	// ── summary ───────────────────────────────────
	// Count issues across all checks
	warnCount := 0
	for _, r := range append(settingsResults, tmuxResults...) {
		if r.sev != sevOK {
			warnCount++
		}
	}

	// ── apply settings.json fixes ─────────────────
	if len(fixes) == 0 {
		if warnCount == 0 {
			fmt.Println(styleOK.Render("All checks passed."))
		} else {
			fmt.Printf("%s — settings.json is up to date. Fix tmux.conf manually using the hints above.\n",
				styleWarn.Render(fmt.Sprintf("%d warning(s)", warnCount)))
		}
		return nil
	}

	fmt.Printf("%d settings.json hook(s) to add:\n", len(fixes))
	for _, fix := range fixes {
		fmt.Printf("  + %s\n", fix.event)
	}
	fmt.Println()

	if !autoApply {
		fmt.Print("Apply changes to settings.json? [y/N]: ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() || strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	return applySettingsFixes(fixes)
}
