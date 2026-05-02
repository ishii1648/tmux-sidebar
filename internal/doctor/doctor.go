// Package doctor implements the "tmux-sidebar doctor" subcommand, which
// diagnoses and auto-repairs hook / option configuration for tmux-sidebar
// (Claude Code settings.json + tmux.conf) against README §1–§8.
package doctor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/ishii1648/tmux-sidebar/internal/config"
	"github.com/ishii1648/tmux-sidebar/internal/state"
)

// ── styles ────────────────────────────────────────────────────────────────────

var (
	styleOK    = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	styleInfo  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	styleError = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleDim   = lipgloss.NewStyle().Faint(true)
)

// ── check result ─────────────────────────────────────────────────────────────

type severity int

const (
	sevOK severity = iota
	sevInfo
	sevWarn
	sevError
)

type checkResult struct {
	label  string
	sev    severity
	detail string
}

func (c checkResult) render() string {
	var badge string
	switch c.sev {
	case sevOK:
		badge = styleOK.Render("[OK]   ")
	case sevInfo:
		badge = styleInfo.Render("[OPT]  ")
	case sevWarn:
		badge = styleWarn.Render("[WARN] ")
	case sevError:
		badge = styleError.Render("[ERROR]")
	}
	return fmt.Sprintf("  %s  %-26s %s", badge, c.label, styleDim.Render(c.detail))
}

// ── settings.json hook fix ────────────────────────────────────────────────────

// hookFix describes a Claude Code settings.json hook to add.
type hookFix struct {
	event   string
	command string
}

// stateRunningCmd / stateIdleCmd return inline shell snippets matching
// ADR-063 Phase A: pane_N has 2 lines (status + agent kind) and pane_N_started
// / pane_N_path / pane_N_session_id sidecar files are written.
func stateRunningCmd() string {
	return `num=$(echo "$TMUX_PANE" | tr -d '%'); dir=` + state.DefaultStateDir +
		`; mkdir -p "$dir"; printf 'running\nclaude\n' > "$dir/pane_${num}"; ` +
		`date +%s > "$dir/pane_${num}_started"; ` +
		`[ -f "$dir/pane_${num}_path" ] || pwd > "$dir/pane_${num}_path"; ` +
		`if [ -n "$CLAUDE_SESSION_ID" ]; then echo "$CLAUDE_SESSION_ID" > "$dir/pane_${num}_session_id"; fi`
}

func stateIdleCmd() string {
	return `num=$(echo "$TMUX_PANE" | tr -d '%'); dir=` + state.DefaultStateDir +
		`; mkdir -p "$dir"; printf 'idle\nclaude\n' > "$dir/pane_${num}"`
}

// requiredHooks lists the Claude Code settings.json hooks doctor maintains.
// Mirrors README §8: PreToolUse=running, PostToolUse=idle, Stop=idle.
var requiredHooks = []hookFix{
	{event: "PreToolUse", command: stateRunningCmd()},
	{event: "PostToolUse", command: stateIdleCmd()},
	{event: "Stop", command: stateIdleCmd()},
}

// legacyStateDir is the pre-ADR-063 path doctor used to write to. We detect
// and purge it from settings.json on auto-fix.
const legacyStateDir = "/tmp/claude-pane-state"

// ── settings.json helpers ────────────────────────────────────────────────────

func settingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

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

type hookEntryJSON struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookGroupJSON struct {
	Matcher string          `json:"matcher"`
	Hooks   []hookEntryJSON `json:"hooks"`
}

func unmarshalHookGroups(raw json.RawMessage) []hookGroupJSON {
	if len(raw) == 0 {
		return nil
	}
	var groups []hookGroupJSON
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil
	}
	return groups
}

func hookEventPresent(hooks map[string]json.RawMessage, event string) bool {
	val, ok := hooks[event]
	if !ok {
		return false
	}
	return len(unmarshalHookGroups(val)) > 0
}

func extractHookCommands(hooks map[string]json.RawMessage, event string) []string {
	raw, ok := hooks[event]
	if !ok {
		return nil
	}
	var cmds []string
	for _, g := range unmarshalHookGroups(raw) {
		for _, h := range g.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	return cmds
}

// upsertHookGroup non-destructively inserts `command` into an event's hook-group
// list. Existing entries that target legacyStateDir are dropped (obsolete after
// ADR-063 Phase A); other unrelated hooks are kept intact. If the exact command
// is already present (post-purge), the input is returned unchanged.
func upsertHookGroup(existing json.RawMessage, command string) json.RawMessage {
	groups := unmarshalHookGroups(existing)

	purged := make([]hookGroupJSON, 0, len(groups))
	for _, g := range groups {
		var keep []hookEntryJSON
		for _, h := range g.Hooks {
			if strings.Contains(h.Command, legacyStateDir) {
				continue
			}
			keep = append(keep, h)
		}
		if len(keep) > 0 {
			purged = append(purged, hookGroupJSON{Matcher: g.Matcher, Hooks: keep})
		}
	}

	for _, g := range purged {
		for _, h := range g.Hooks {
			if strings.TrimSpace(h.Command) == strings.TrimSpace(command) {
				data, _ := json.Marshal(purged)
				return data
			}
		}
	}

	purged = append(purged, hookGroupJSON{
		Matcher: "",
		Hooks:   []hookEntryJSON{{Type: "command", Command: command}},
	})
	data, _ := json.Marshal(purged)
	return data
}

// newHookGroupJSON returns a JSON-encoded hook group array containing one
// command. Retained for backwards compatibility; callers should prefer
// upsertHookGroup so that unrelated existing hooks are preserved.
func newHookGroupJSON(command string) json.RawMessage {
	return upsertHookGroup(nil, command)
}

// ── tmux.conf parsing ─────────────────────────────────────────────────────────

// confDoc is the parsed tmux.conf with comments stripped and continuation
// lines joined into single logical lines.
type confDoc struct {
	path  string
	found bool
	lines []string
}

func loadTmuxConf() *confDoc {
	confPath, found := findTmuxConf()
	doc := &confDoc{path: confPath, found: found}
	if !found {
		return doc
	}
	data, err := os.ReadFile(confPath)
	if err != nil {
		doc.found = false
		return doc
	}

	var logical []string
	var sb strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasSuffix(line, `\`) {
			sb.WriteString(strings.TrimSuffix(line, `\`))
			sb.WriteByte(' ')
			continue
		}
		sb.WriteString(line)
		logical = append(logical, sb.String())
		sb.Reset()
	}
	if sb.Len() > 0 {
		logical = append(logical, sb.String())
	}

	cleaned := make([]string, 0, len(logical))
	for _, l := range logical {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		cleaned = append(cleaned, l)
	}
	doc.lines = cleaned
	return doc
}

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

// tmuxConfContains is retained for compatibility with existing tests. It does
// a naive substring search on the raw file contents.
func tmuxConfContains(path, needle string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

// hasHook reports whether tmux.conf configures `set-hook ... <event>` whose
// joined logical line additionally contains `needle` (substring; "" disables
// the substring requirement).
func (d *confDoc) hasHook(event, needle string) bool {
	for _, line := range d.lines {
		if !strings.Contains(line, "set-hook") {
			continue
		}
		if !strings.Contains(line, event) {
			continue
		}
		if needle != "" && !strings.Contains(line, needle) {
			continue
		}
		return true
	}
	return false
}

// hookLine returns the first joined line containing both `set-hook` and the
// given event, or "".
func (d *confDoc) hookLine(event string) string {
	for _, line := range d.lines {
		if strings.Contains(line, "set-hook") && strings.Contains(line, event) {
			return line
		}
	}
	return ""
}

// hookUsesAppend reports whether tmux.conf uses `set-hook -ga <event>`.
// README explicitly warns against this — `-ga` accumulates across `source-file`.
func (d *confDoc) hookUsesAppend(event string) bool {
	re := regexp.MustCompile(`set-hook\s+-ga\b[^#]*\b` + regexp.QuoteMeta(event) + `\b`)
	for _, line := range d.lines {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// hasOption reports whether tmux.conf contains a `set-option` / `set` line
// asserting `key value`.
func (d *confDoc) hasOption(key, value string) bool {
	pat := regexp.MustCompile(`(?:^|\s)(?:set-option|set)\b[^#]*\b` + regexp.QuoteMeta(key) + `\b[^#]*\b` + regexp.QuoteMeta(value) + `\b`)
	for _, line := range d.lines {
		if pat.MatchString(line) {
			return true
		}
	}
	return false
}

// contains reports whether any non-comment logical line contains `needle`.
func (d *confDoc) contains(needle string) bool {
	for _, line := range d.lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

// ── tmux hook coverage definitions ────────────────────────────────────────────

type tmuxHookCheck struct {
	label  string
	event  string
	needle string
	sev    severity // severity reported when missing
	hint   string
}

var requiredTmuxHooks = []tmuxHookCheck{
	// §1 自動生成
	{
		label: "after-new-window", event: "after-new-window", needle: "tmux-sidebar", sev: sevError,
		hint: `set-hook -g after-new-window 'run-shell "[ $(tmux list-panes -t \"#{window_id}\" | wc -l) -eq 1 ] && { tmux split-window -hfb -d -l 40 -t \"#{window_id}\" tmux-sidebar; tmux set-option -p -t \"#{window_id}.{left}\" @pane_role sidebar; } || true"'`,
	},
	{
		label: "after-new-session", event: "after-new-session", needle: "tmux-sidebar", sev: sevError,
		hint: `set-hook -g after-new-session 'run-shell "[ $(tmux list-panes -t \"#{window_id}\" | wc -l) -eq 1 ] && { tmux split-window -hfb -d -l 40 -t \"#{window_id}\" tmux-sidebar; tmux set-option -p -t \"#{window_id}.{left}\" @pane_role sidebar; } || true"'`,
	},

	// §2 誤フォーカス防止 + カーソル追従
	{
		label: "after-select-window", event: "after-select-window", needle: "tmux-sidebar-", sev: sevError,
		hint: "see README §2 — add an after-select-window hook (SIGUSR1 + sidebar focus guard)",
	},
	{
		label: "client-session-changed", event: "client-session-changed", needle: "tmux-sidebar-", sev: sevError,
		hint: "see README §2 — add a client-session-changed hook (SIGUSR1 + sidebar focus guard)",
	},

	// §3 サイドバーのみ残ったウィンドウの自動削除
	{
		label: "pane-exited cleanup", event: "pane-exited", needle: "cleanup-if-only-sidebar", sev: sevWarn,
		hint: `set-hook -g pane-exited 'run-shell "tmux-sidebar cleanup-if-only-sidebar"'`,
	},
	{
		label: "after-kill-pane cleanup", event: "after-kill-pane", needle: "cleanup-if-only-sidebar", sev: sevWarn,
		hint: `set-hook -g after-kill-pane 'run-shell "tmux-sidebar cleanup-if-only-sidebar"'`,
	},

	// §4 ディスプレイ移動時のサイドバー幅維持
	{
		label: "client-resized", event: "client-resized", needle: "@pane_role", sev: sevWarn,
		hint: "see README §4 — add a client-resized hook that re-applies `resize-pane -x` to all sidebar panes",
	},

	// §5 SIGUSR1 即時更新通知
	{
		label: "window-linked", event: "window-linked", needle: "tmux-sidebar-", sev: sevWarn,
		hint: `set-hook -g window-linked 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 $(cat \"$f\") 2>/dev/null; done"'`,
	},
	{
		label: "window-unlinked", event: "window-unlinked", needle: "tmux-sidebar-", sev: sevWarn,
		hint: `set-hook -g window-unlinked 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 $(cat \"$f\") 2>/dev/null; done"'`,
	},
	{
		label: "session-created", event: "session-created", needle: "tmux-sidebar-", sev: sevWarn,
		hint: `set-hook -g session-created 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 $(cat \"$f\") 2>/dev/null; done"'`,
	},
	{
		label: "session-closed", event: "session-closed", needle: "tmux-sidebar-", sev: sevWarn,
		hint: `set-hook -g session-closed 'run-shell "for f in /tmp/tmux-sidebar-*.pid; do [ -f \"$f\" ] && kill -USR1 $(cat \"$f\") 2>/dev/null; done"'`,
	},
}

// ── diagnosis: settings.json ──────────────────────────────────────────────────

func checkClaudeSettings() (results []checkResult, fixes []hookFix) {
	path, err := settingsPath()
	if err != nil {
		results = append(results, checkResult{label: "settings.json", sev: sevError, detail: err.Error()})
		return
	}

	fileExists := true
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fileExists = false
	}

	raw, err := readRawSettings(path)
	if err != nil {
		results = append(results, checkResult{label: "settings.json", sev: sevError, detail: err.Error()})
		return
	}
	hooks := getHooksMap(raw)

	for _, fix := range requiredHooks {
		switch {
		case !fileExists:
			results = append(results, checkResult{label: fix.event, sev: sevWarn, detail: "settings.json not found"})
			fixes = append(fixes, fix)
		case !hookEventPresent(hooks, fix.event):
			results = append(results, checkResult{label: fix.event, sev: sevWarn, detail: "hook not configured"})
			fixes = append(fixes, fix)
		default:
			legacy := false
			for _, c := range extractHookCommands(hooks, fix.event) {
				if strings.Contains(c, legacyStateDir) {
					legacy = true
					break
				}
			}
			if legacy {
				results = append(results, checkResult{
					label: fix.event, sev: sevWarn,
					detail: "writes to legacy " + legacyStateDir + " — ADR-063 Phase A moved this to " + state.DefaultStateDir,
				})
				fixes = append(fixes, fix)
			} else {
				results = append(results, checkResult{label: fix.event, sev: sevOK, detail: "hook configured"})
			}
		}
	}
	return
}

// checkLegacyClaudeHooks emits an info line when a stale UserPromptSubmit hook
// (left over from doctor versions before PreToolUse was canonical) is detected.
func checkLegacyClaudeHooks() []checkResult {
	path, err := settingsPath()
	if err != nil {
		return nil
	}
	raw, err := readRawSettings(path)
	if err != nil {
		return nil
	}
	hooks := getHooksMap(raw)
	if !hookEventPresent(hooks, "UserPromptSubmit") {
		return nil
	}
	for _, c := range extractHookCommands(hooks, "UserPromptSubmit") {
		if strings.Contains(c, legacyStateDir) || strings.Contains(c, state.DefaultStateDir) {
			return []checkResult{{
				label: "UserPromptSubmit (legacy)", sev: sevInfo,
				detail: "previously installed by doctor; superseded by PreToolUse — safe to remove",
			}}
		}
	}
	return nil
}

// ── diagnosis: tmux.conf ──────────────────────────────────────────────────────

func checkTmuxHooks(d *confDoc) []checkResult {
	results := make([]checkResult, 0, len(requiredTmuxHooks))
	for _, h := range requiredTmuxHooks {
		switch {
		case !d.found:
			results = append(results, checkResult{label: h.label, sev: h.sev, detail: "tmux.conf not found"})
		case d.hookUsesAppend(h.event):
			results = append(results, checkResult{
				label: h.label, sev: sevWarn,
				detail: "uses `set-hook -ga` — README warns this accumulates on `source-file`; use `-g`",
			})
		case d.hasHook(h.event, h.needle):
			results = append(results, checkResult{label: h.label, sev: sevOK, detail: "configured"})
		default:
			results = append(results, checkResult{
				label: h.label, sev: h.sev,
				detail: "missing — " + h.hint,
			})
		}
	}
	return results
}

func checkTmuxOptions(d *confDoc) []checkResult {
	if !d.found {
		return []checkResult{{label: "focus-events", sev: sevWarn, detail: "tmux.conf not found"}}
	}
	if d.hasOption("focus-events", "on") {
		return []checkResult{{label: "focus-events", sev: sevOK, detail: "set-option -g focus-events on"}}
	}
	return []checkResult{{
		label: "focus-events", sev: sevWarn,
		detail: "missing — add `set-option -g focus-events on` (sidebar focus state needs it)",
	}}
}

func checkTmuxBindings(d *confDoc) []checkResult {
	if !d.found {
		return nil
	}
	results := make([]checkResult, 0, 2)
	if d.contains("tmux-sidebar toggle") {
		results = append(results, checkResult{label: "toggle binding", sev: sevOK, detail: "configured"})
	} else {
		results = append(results, checkResult{
			label: "toggle binding", sev: sevInfo,
			detail: "not set — `bind-key e run-shell 'tmux-sidebar toggle'`",
		})
	}
	if d.contains("tmux-sidebar focus-or-open") {
		results = append(results, checkResult{label: "focus-or-open binding", sev: sevOK, detail: "configured"})
	} else {
		results = append(results, checkResult{
			label: "focus-or-open binding", sev: sevInfo,
			detail: "not set — `bind-key -n <key> run-shell 'tmux-sidebar focus-or-open'`",
		})
	}
	return results
}

// parseTmuxVersion extracts (major, minor) from `tmux -V` output. Examples:
//
//	"tmux 3.4"          → (3, 4, true)
//	"tmux 3.2a"         → (3, 2, true)
//	"tmux next-3.5"     → (3, 5, true)
//	"tmux master"       → (0, 0, false)
//
// Returns ok=false when no major.minor pattern is found.
func parseTmuxVersion(s string) (major, minor int, ok bool) {
	re := regexp.MustCompile(`(\d+)\.(\d+)`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(m[1])
	min, err2 := strconv.Atoi(m[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, min, true
}

// extractFirstInt finds the integer following the given flag (e.g. "-l 40").
func extractFirstInt(line, flag string) int {
	re := regexp.MustCompile(regexp.QuoteMeta(flag) + `\s+(\d+)`)
	if m := re.FindStringSubmatch(line); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func checkWidthConsistency(d *confDoc) []checkResult {
	if !d.found {
		return nil
	}
	sources := map[string]int{}
	if line := d.hookLine("after-new-window"); line != "" {
		if n := extractFirstInt(line, "-l"); n > 0 {
			sources["after-new-window split-window -l"] = n
		}
	}
	if line := d.hookLine("after-new-session"); line != "" {
		if n := extractFirstInt(line, "-l"); n > 0 {
			sources["after-new-session split-window -l"] = n
		}
	}
	if line := d.hookLine("client-resized"); line != "" {
		if n := extractFirstInt(line, "-x"); n > 0 {
			sources["client-resized resize-pane -x"] = n
		}
	}
	cfg, _ := config.Load(config.DefaultConfigPath())
	if cfg.Width >= config.MinSidebarWidth {
		sources["sidebar runtime"] = cfg.Width
	}

	if len(sources) <= 1 {
		return nil
	}

	keys := make([]string, 0, len(sources))
	for k := range sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	first := sources[keys[0]]
	consistent := true
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, sources[k]))
		if sources[k] != first {
			consistent = false
		}
	}

	if consistent {
		return []checkResult{{label: "width consistency", sev: sevOK, detail: fmt.Sprintf("all sources = %d", first)}}
	}
	return []checkResult{{label: "width consistency", sev: sevWarn, detail: "mismatch: " + strings.Join(parts, ", ")}}
}

// ── runtime checks ────────────────────────────────────────────────────────────

func checkRuntime() []checkResult {
	var results []checkResult

	if out, err := exec.Command("tmux", "-V").Output(); err == nil {
		ver := strings.TrimSpace(string(out))
		results = append(results, checkResult{label: "tmux", sev: sevOK, detail: ver})
		// Popup picker (Phase 4) needs `display-popup`, introduced in tmux 3.2.
		// Treat older versions as a warning rather than an error so the rest of
		// the sidebar (which works on 3.0+) keeps functioning.
		if maj, min, ok := parseTmuxVersion(ver); ok {
			if maj < 3 || (maj == 3 && min < 2) {
				results = append(results, checkResult{
					label: "tmux popup support", sev: sevWarn,
					detail: fmt.Sprintf("%d.%d found; popup picker (`N`) requires tmux 3.2+", maj, min),
				})
			} else {
				results = append(results, checkResult{
					label: "tmux popup support", sev: sevOK,
					detail: "display-popup available",
				})
			}
		}
	} else {
		results = append(results, checkResult{label: "tmux", sev: sevError, detail: "not found in PATH"})
	}

	if path, err := exec.LookPath("tmux-sidebar"); err == nil {
		results = append(results, checkResult{label: "tmux-sidebar", sev: sevOK, detail: path})
	} else {
		results = append(results, checkResult{
			label: "tmux-sidebar", sev: sevWarn,
			detail: "not in PATH — tmux `run-shell` will fail. Install to a directory in PATH (e.g. /usr/local/bin)",
		})
	}

	if entries, err := os.ReadDir(legacyStateDir); err == nil && len(entries) > 0 {
		results = append(results, checkResult{
			label: "legacy state dir", sev: sevWarn,
			detail: fmt.Sprintf("%s still has %d files (ADR-063 Phase A moved state to %s) — re-run `doctor --yes` to upgrade hooks, then `rm -rf %s`",
				legacyStateDir, len(entries), state.DefaultStateDir, legacyStateDir),
		})
	}

	if widthPath := config.WidthConfigPath(); widthPath != "" {
		if data, err := os.ReadFile(widthPath); err == nil {
			s := strings.TrimSpace(string(data))
			n, perr := strconv.Atoi(s)
			switch {
			case perr != nil:
				results = append(results, checkResult{
					label: "width config", sev: sevWarn,
					detail: fmt.Sprintf("%s: %q is not an integer (falling back to %d)", widthPath, s, config.DefaultSidebarWidth),
				})
			case n < config.MinSidebarWidth:
				results = append(results, checkResult{
					label: "width config", sev: sevWarn,
					detail: fmt.Sprintf("%s: %d < min %d (falling back to %d)", widthPath, n, config.MinSidebarWidth, config.DefaultSidebarWidth),
				})
			default:
				results = append(results, checkResult{
					label: "width config", sev: sevOK,
					detail: fmt.Sprintf("%s = %d", widthPath, n),
				})
			}
		}
	}

	if hsPath := config.DefaultConfigPath(); hsPath != "" {
		if _, err := os.Stat(hsPath); err == nil {
			cfg, lerr := config.Load(hsPath)
			if lerr != nil {
				results = append(results, checkResult{
					label: "hidden_sessions", sev: sevWarn,
					detail: fmt.Sprintf("%s: %v", hsPath, lerr),
				})
			} else {
				results = append(results, checkResult{
					label: "hidden_sessions", sev: sevOK,
					detail: fmt.Sprintf("%s (%d entries)", hsPath, len(cfg.HiddenSessions)),
				})
			}
		}
	}

	return results
}

// ── fix application ───────────────────────────────────────────────────────────

// backupFile copies path → path+".bak" if path exists. Returns the bak path
// on success ("" if nothing was copied).
func backupFile(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer src.Close()

	bak := path + ".bak"
	dst, err := os.Create(bak)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	return bak, nil
}

func applySettingsFixes(fixes []hookFix) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}

	bak, err := backupFile(path)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}

	raw, err := readRawSettings(path)
	if err != nil {
		return err
	}
	hooks := getHooksMap(raw)

	for _, fix := range fixes {
		hooks[fix.event] = upsertHookGroup(hooks[fix.event], fix.command)
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

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	fmt.Printf("Updated %s\n", path)
	if bak != "" {
		fmt.Printf("Backup saved to %s\n", bak)
	}
	return nil
}

// ── entry point ───────────────────────────────────────────────────────────────

type checkSection struct {
	title   string
	results []checkResult
}

// Run executes the doctor diagnostic.
// If autoApply is true, missing settings.json hooks are added without prompting.
func Run(autoApply bool) error {
	fmt.Println("tmux-sidebar doctor")
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println()

	runtime := checkSection{title: "Runtime", results: checkRuntime()}

	settingsResults, fixes := checkClaudeSettings()
	settingsResults = append(settingsResults, checkLegacyClaudeHooks()...)
	settingsTitle := "Claude Code settings.json"
	if p, err := settingsPath(); err == nil {
		settingsTitle = "Claude Code settings.json (" + p + ")"
	}
	settings := checkSection{title: settingsTitle, results: settingsResults}

	doc := loadTmuxConf()
	tmuxResults := []checkResult{}
	tmuxResults = append(tmuxResults, checkTmuxHooks(doc)...)
	tmuxResults = append(tmuxResults, checkTmuxOptions(doc)...)
	tmuxResults = append(tmuxResults, checkTmuxBindings(doc)...)
	tmuxResults = append(tmuxResults, checkWidthConsistency(doc)...)
	tmuxTitle := "tmux.conf (not found)"
	if doc.found {
		tmuxTitle = "tmux.conf (" + doc.path + ")"
	}
	tmuxSection := checkSection{title: tmuxTitle, results: tmuxResults}

	sections := []checkSection{runtime, settings, tmuxSection}

	var errCount, warnCount int
	for _, s := range sections {
		fmt.Printf("Checking %s\n", s.title)
		for _, r := range s.results {
			fmt.Println(r.render())
			switch r.sev {
			case sevError:
				errCount++
			case sevWarn:
				warnCount++
			}
		}
		fmt.Println()
	}

	if len(fixes) == 0 {
		switch {
		case errCount == 0 && warnCount == 0:
			fmt.Println(styleOK.Render("All checks passed."))
		case errCount > 0:
			fmt.Printf("%s, %s — settings.json is up to date. Address tmux.conf manually using the hints above.\n",
				styleError.Render(fmt.Sprintf("%d error(s)", errCount)),
				styleWarn.Render(fmt.Sprintf("%d warning(s)", warnCount)))
		default:
			fmt.Printf("%s — settings.json is up to date. Address tmux.conf manually using the hints above.\n",
				styleWarn.Render(fmt.Sprintf("%d warning(s)", warnCount)))
		}
		return nil
	}

	fmt.Printf("%d settings.json hook(s) to add or upgrade:\n", len(fixes))
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
