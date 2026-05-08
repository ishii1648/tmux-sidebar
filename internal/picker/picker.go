// Package picker implements the popup wizard that creates a new tmux session
// from a ghq repository plus a launcher (claude / codex). It mirrors
// dotfiles' dispatch_launcher.fish so the same workflow is available without
// fish/fzf.
//
// The picker is invoked via `tmux-sidebar new`, which is intended to be
// bound from tmux.conf (typically via `tmux display-popup -E ...`). It is a
// separate Bubble Tea program from the sidebar; the two share state only
// through tmux itself (e.g. listing open sessions).
package picker

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ishii1648/tmux-sidebar/internal/dispatch"
	"github.com/ishii1648/tmux-sidebar/internal/repo"
	"github.com/mattn/go-runewidth"
)

// step is the picker's wizard position.
type step int

const (
	stepRepo   step = iota // pick a repo + toggle launcher with Tab
	stepPrompt             // enter the dispatch prompt
)

// Runner abstracts the tmux / dispatch invocations the picker performs.
// Tests substitute a fake; production wires ExecRunner.
type Runner interface {
	// SwitchClient moves the calling tmux client to the named session.
	// Used on Step 1 when the chosen repo already has a session — the
	// picker switches to it directly without going through dispatch.
	SwitchClient(name string) error
	// SpawnDispatch fires `tmux-sidebar dispatch <opts>` in the
	// background via `tmux run-shell -b`, mirroring how
	// dispatch_launcher.fish hands work off to dispatch.sh. The picker
	// quits immediately after this returns so the popup does not block
	// the user while git worktree creation and tmux session setup run.
	// Errors during the spawned dispatch surface via tmux
	// display-message from the dispatch process itself.
	SpawnDispatch(opts dispatch.Options) error
}

// Model is the picker's Bubble Tea model.
type Model struct {
	openSessions map[string]struct{} // session names already open

	repos    []repo.Repo
	filtered []repo.Repo

	step         step
	query        string
	cursor       int
	scrollOffset int               // first visible repo row index in viewRepo
	launcher     dispatch.Launcher // current launcher selection (claude / codex)

	// prompt is the in-progress prompt body when step==stepPrompt.
	// Multi-line is supported via paste (LF preserved) and via
	// shift+enter / alt+enter / ctrl+j when the terminal differentiates
	// them from plain Enter.
	prompt string
	// promptCursor is the insertion point as a rune index into prompt.
	// 0 ≤ promptCursor ≤ len([]rune(prompt)). All editing operations
	// (insert / delete / arrow keys) use rune indices so multi-byte input
	// like Japanese is moved as one unit.
	promptCursor int

	width  int
	height int

	errMsg    string // shown on the bottom line; cleared on next key
	statusMsg string // shown after a successful exec while quitting
	quitting  bool
	runner    Runner
}

// New creates a Model. repos is the discovered ghq list (caller fetches
// before constructing so failures surface synchronously). openSessionNames
// lists session names that already exist so the picker can dim duplicates
// and switch to them instead of dispatching. runner executes tmux /
// dispatch commands on Enter.
func New(repos []repo.Repo, openSessionNames []string, runner Runner) *Model {
	repo.SortByBasename(repos)
	openSessions := map[string]struct{}{}
	for _, name := range openSessionNames {
		openSessions[name] = struct{}{}
	}
	m := &Model{
		openSessions: openSessions,
		repos:        repos,
		runner:       runner,
		launcher:     dispatch.LauncherClaude,
	}
	m.applyFilter()
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}
	m.errMsg = ""
	switch m.step {
	case stepRepo:
		return m.handleRepoKey(msg)
	case stepPrompt:
		return m.handlePromptKey(msg)
	}
	return m, nil
}

func (m *Model) handleRepoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyTab:
		m.toggleLauncher()
		return m, nil
	case tea.KeyEnter:
		if len(m.filtered) == 0 {
			return m, nil
		}
		r := m.filtered[m.cursor]
		// Open session for this repo? Switch instead of dispatching.
		if _, exists := m.openSessions[r.Basename]; exists {
			if err := m.runner.SwitchClient(r.Basename); err != nil {
				m.errMsg = "switch failed: " + err.Error()
				return m, nil
			}
			m.statusMsg = "switched to " + r.Basename
			m.quitting = true
			return m, tea.Quit
		}
		m.step = stepPrompt
		m.prompt = ""
		m.promptCursor = 0
		return m, nil
	case tea.KeyUp, tea.KeyCtrlP:
		m.moveCursor(-1)
		return m, nil
	case tea.KeyDown, tea.KeyCtrlN:
		m.moveCursor(1)
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
			m.applyFilter()
		}
		return m, nil
	case tea.KeyRunes:
		m.query += string(msg.Runes)
		m.applyFilter()
		return m, nil
	}
	return m, nil
}

func (m *Model) handlePromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Multi-line shortcuts: shift+enter / alt+enter / ctrl+j insert a
	// literal newline rather than firing dispatch. Detection requires the
	// terminal to differentiate these from plain Enter (kitty keyboard
	// protocol or similar). On terminals that don't, shift+enter still
	// arrives as KeyEnter and submits — the user can then fall back to
	// paste for multi-line input.
	if isNewlineKey(msg) {
		m.insertAtCursor("\n")
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEscape:
		m.step = stepRepo
		return m, nil
	case tea.KeyTab:
		m.toggleLauncher()
		return m, nil
	case tea.KeyEnter:
		return m, m.execDispatch()
	case tea.KeyLeft, tea.KeyCtrlB:
		if m.promptCursor > 0 {
			m.promptCursor--
		}
		return m, nil
	case tea.KeyRight, tea.KeyCtrlF:
		if m.promptCursor < promptRuneLen(m.prompt) {
			m.promptCursor++
		}
		return m, nil
	case tea.KeyHome, tea.KeyCtrlA:
		m.promptCursor = 0
		return m, nil
	case tea.KeyEnd, tea.KeyCtrlE:
		m.promptCursor = promptRuneLen(m.prompt)
		return m, nil
	case tea.KeyBackspace:
		m.deleteBeforeCursor()
		return m, nil
	case tea.KeyDelete:
		m.deleteAtCursor()
		return m, nil
	case tea.KeySpace:
		m.insertAtCursor(" ")
		return m, nil
	case tea.KeyRunes:
		// bracketed paste delivers the whole pasted blob (including line
		// breaks) as a single KeyRunes msg with Paste=true. Terminals
		// historically translate LF to CR inside paste — normalise back
		// to LF so dispatch.firstLine and the rendering code only have
		// to handle one canonical newline form. Plain typing produces
		// runes one at a time so the Paste flag distinguishes paste
		// from a single-byte CR keystroke.
		s := string(msg.Runes)
		if msg.Paste {
			s = normalizeNewlines(s)
		}
		m.insertAtCursor(s)
		return m, nil
	}
	return m, nil
}

// promptRuneLen returns the rune count of m.prompt — the maximum valid
// cursor position. Centralised so insert / move / delete operations can't
// drift on what "end of buffer" means.
func promptRuneLen(s string) int {
	return len([]rune(s))
}

// insertAtCursor inserts s at the current cursor position and advances the
// cursor to the end of the inserted text (rune-aware).
func (m *Model) insertAtCursor(s string) {
	if s == "" {
		return
	}
	runes := []rune(m.prompt)
	c := m.promptCursor
	if c < 0 {
		c = 0
	}
	if c > len(runes) {
		c = len(runes)
	}
	insert := []rune(s)
	out := make([]rune, 0, len(runes)+len(insert))
	out = append(out, runes[:c]...)
	out = append(out, insert...)
	out = append(out, runes[c:]...)
	m.prompt = string(out)
	m.promptCursor = c + len(insert)
}

// deleteBeforeCursor removes the rune immediately to the left of the
// cursor (Backspace). No-op when the cursor is at position 0.
func (m *Model) deleteBeforeCursor() {
	if m.promptCursor <= 0 {
		return
	}
	runes := []rune(m.prompt)
	if m.promptCursor > len(runes) {
		m.promptCursor = len(runes)
		return
	}
	c := m.promptCursor
	m.prompt = string(runes[:c-1]) + string(runes[c:])
	m.promptCursor = c - 1
}

// deleteAtCursor removes the rune to the right of the cursor (Delete /
// forward delete). No-op when the cursor is at the end of the buffer.
func (m *Model) deleteAtCursor() {
	runes := []rune(m.prompt)
	if m.promptCursor >= len(runes) {
		return
	}
	c := m.promptCursor
	if c < 0 {
		c = 0
	}
	m.prompt = string(runes[:c]) + string(runes[c+1:])
	// cursor stays at c — the rune that was at c+1 is now at c
}

// normalizeNewlines collapses any combination of CR and LF into LF so a
// pasted multi-line payload looks the same regardless of terminal quirks.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// isNewlineKey reports whether msg should insert a literal `\n` instead of
// submitting. Three forms are recognised so users can pick whichever their
// terminal sends as a distinguishable key:
//   - Ctrl+J (LF, KeyCtrlJ): always distinct from Enter
//   - Shift+Enter, Alt+Enter: differentiated only when the terminal
//     emits a kitty/CSI-u keyboard protocol sequence; otherwise they
//     collapse to Enter
func isNewlineKey(msg tea.KeyMsg) bool {
	if msg.Type == tea.KeyCtrlJ {
		return true
	}
	switch msg.String() {
	case "shift+enter", "alt+enter":
		return true
	}
	return false
}

// toggleLauncher cycles the launcher between claude and codex. Bound to Tab
// on both steps so the user can change their mind after seeing repo / prompt
// context without backtracking through the wizard.
func (m *Model) toggleLauncher() {
	if m.launcher == dispatch.LauncherClaude {
		m.launcher = dispatch.LauncherCodex
	} else {
		m.launcher = dispatch.LauncherClaude
	}
}

// execDispatch validates the prompt, materialises it (or the
// `:<branch>` checkout flag) into dispatch.Options, then hands the work
// off to runner.SpawnDispatch which fires `tmux-sidebar dispatch` via
// `tmux run-shell -b`. Once the spawn returns, the picker quits — the
// popup closes immediately and the worktree / session setup runs as a
// tmux-managed background process. Errors during the spawn (e.g. the
// run-shell call itself failing) are shown in the picker before quit;
// errors inside the spawned dispatch surface via tmux display-message
// from the dispatch process.
func (m *Model) execDispatch() tea.Cmd {
	if len(m.filtered) == 0 {
		m.errMsg = "no repo selected"
		return nil
	}
	prompt := strings.TrimSpace(m.prompt)
	if prompt == "" {
		m.errMsg = "prompt is empty"
		return nil
	}
	r := m.filtered[m.cursor]
	branch, checkout, body := dispatch.ParseBranchPrefix(prompt)
	opts := dispatch.Options{
		Repo:     r.Path,
		Launcher: m.launcher,
		// Switch is left off so the user's current pane / session is not
		// hijacked when they fire a new dispatch. Success is signalled
		// via `tmux display-message` from the dispatch subprocess; the
		// user attaches manually (e.g. via `prefix s` or sidebar) when
		// they're ready. Codex still calls waitForAttachedClient — it
		// will block in the dispatch process until the user attaches or
		// the 5-min timeout fires (matches dispatch.sh CLI behavior).
	}
	if checkout {
		opts.Branch = branch
		opts.NoPrompt = true
	} else {
		// Leave opts.Branch empty so the spawned dispatch process picks
		// the name (claude -p with slugify fallback). Doing it here
		// would force the popup to wait several seconds for the LLM,
		// which is the exact regression that the fire-and-forget
		// design (run-shell -b) was introduced to avoid.
		// Ship the prompt as a tempfile path. The spawned dispatch
		// reads and removes it; serialising the literal text through
		// the shell would mangle newlines and metacharacters.
		path, err := dispatch.WriteTempPrompt(body)
		if err != nil {
			m.errMsg = "prompt: " + err.Error()
			return nil
		}
		opts.PromptFile = path
	}
	if err := m.runner.SpawnDispatch(opts); err != nil {
		if opts.PromptFile != "" {
			_ = os.Remove(opts.PromptFile)
		}
		m.errMsg = "spawn failed: " + err.Error()
		return nil
	}
	m.statusMsg = "dispatching " + r.Basename + "..."
	m.quitting = true
	return tea.Quit
}

// applyFilter recomputes m.filtered from m.query and clamps the cursor.
// Scroll offset resets here too: a fresh filter produces a fresh result
// list, so any previously-saved viewport position no longer makes sense.
func (m *Model) applyFilter() {
	m.filtered = repo.Filter(m.repos, m.query)
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.scrollOffset = 0
}

func (m *Model) moveCursor(delta int) {
	next := m.cursor + delta
	if next < 0 || next >= len(m.filtered) {
		return
	}
	m.cursor = next
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.quitting {
		// On exit, render only the trailing status / error so the popup
		// closes cleanly without a stray view.
		if m.errMsg != "" {
			return styleError.Render(m.errMsg) + "\n"
		}
		if m.statusMsg != "" {
			return styleSuccess.Render(m.statusMsg) + "\n"
		}
		return ""
	}
	switch m.step {
	case stepRepo:
		return m.viewRepo()
	case stepPrompt:
		return m.viewPrompt()
	}
	return ""
}

func (m *Model) viewRepo() string {
	var sb strings.Builder
	sb.WriteString(styleFaint.Render("  tab: launcher 切替  enter: select  esc: cancel") + "\n")
	sb.WriteString("  " + renderLauncherChoice(m.launcher) + "\n")
	sb.WriteString(styleFaint.Render(strings.Repeat("─", clamp(m.width, 40, 100))) + "\n")
	sb.WriteString(stylePrompt.Render("> ") + m.query + "▏\n")

	total := len(m.filtered)
	if total == 0 {
		sb.WriteString(styleFaint.Render("  (no matching repos)") + "\n")
		if m.errMsg != "" {
			sb.WriteString(styleError.Render(m.errMsg) + "\n")
		}
		return sb.String()
	}

	maxRows := m.viewportRows()
	start, end, hasUp, hasDown := computeRepoViewport(m.cursor, total, maxRows, m.scrollOffset)
	m.scrollOffset = start

	if hasUp {
		sb.WriteString(styleFaint.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i := start; i < end; i++ {
		r := m.filtered[i]
		cursor := "  "
		if i == m.cursor {
			cursor = styleCursor.Render("▶ ")
		}
		label := r.Name
		if _, open := m.openSessions[r.Basename]; open {
			label += "  (open)"
			sb.WriteString(cursor + styleFaint.Render(label) + "\n")
		} else {
			sb.WriteString(cursor + label + "\n")
		}
	}
	if hasDown {
		sb.WriteString(styleFaint.Render(fmt.Sprintf("  ↓ %d more", total-end)) + "\n")
	}

	if m.errMsg != "" {
		sb.WriteString(styleError.Render(m.errMsg) + "\n")
	}
	return sb.String()
}

// computeRepoViewport decides which slice of m.filtered to render so the
// cursor stays visible, and reports whether "↑ N more" / "↓ N more"
// markers should be drawn above / below the slice.
//
// savedStart is the previous scroll offset; it provides hysteresis so the
// window does not jump around when the cursor moves within the visible
// range. Markers consume one row each, so the effective item capacity is
// maxRows - (1 if hasUp) - (1 if hasDown). Capacity depends on start
// (because hasDown depends on what fits below), so the descent loop
// iterates a few times until stable. It always converges within a couple
// of passes; the iteration cap is just a defensive guard.
func computeRepoViewport(cursor, total, maxRows, savedStart int) (start, end int, hasUp, hasDown bool) {
	if total <= 0 {
		return 0, 0, false, false
	}
	if total <= maxRows {
		return 0, total, false, false
	}

	// capacity returns the number of item rows available given a start
	// offset, after reserving rows for whichever markers are needed.
	capacity := func(s int) int {
		c := maxRows
		if s > 0 {
			c-- // room for "↑ N more"
		}
		if s+c < total {
			c-- // room for "↓ N more"
		}
		if c < 1 {
			c = 1
		}
		return c
	}

	start = savedStart
	if start < 0 {
		start = 0
	}
	if start > total-1 {
		start = total - 1
	}

	// Cursor moved above the saved window: scroll up so it becomes the
	// first visible row.
	if cursor < start {
		start = cursor
	}
	// Cursor moved below the saved window: scroll down so it becomes the
	// last visible row. capacity changes with start (down-marker may
	// flip), so iterate to a fixed point.
	for iter := 0; iter < 5; iter++ {
		c := capacity(start)
		if cursor < start+c {
			break
		}
		newStart := cursor - c + 1
		if newStart <= start {
			break
		}
		start = newStart
	}
	if start < 0 {
		start = 0
	}

	hasUp = start > 0
	slots := maxRows
	if hasUp {
		slots--
	}
	end = start + slots
	if end < total {
		// Items remain below the slice — reserve a row for the down
		// marker.
		slots--
		end = start + slots
		hasDown = true
	}
	if end > total {
		end = total
		hasDown = false
	}
	return
}

// viewPrompt renders the dispatch prompt input. Layout mirrors
// dispatch_launcher.fish's two-line header followed by the input area:
//
//	tab: モード切替  enter: 実行  `:<branch>` で既存 remote branch を checkout
//	claude / codex  <repo>
//	─────────────────────
//	> ▏
func (m *Model) viewPrompt() string {
	var sb strings.Builder
	sb.WriteString(styleFaint.Render("  tab: モード切替  enter: 実行  `:<branch>` で既存 remote branch を checkout") + "\n")

	repoName := ""
	if len(m.filtered) > 0 {
		repoName = m.filtered[m.cursor].Basename
	}
	sb.WriteString("  " + renderLauncherChoice(m.launcher) + "  " + repoName + "\n")
	sb.WriteString(styleFaint.Render("  "+strings.Repeat("─", clamp(m.width, 30, 80))) + "\n")
	sb.WriteString("\n")

	sb.WriteString(renderPromptInput(m.prompt, m.promptCursor, m.width))

	// Show a hint only for `:<branch>` checkout mode — that branch name is
	// exactly what dispatch will use, so it's worth previewing. The default
	// flow's branch is decided by the spawned dispatch process (claude -p
	// with slugify fallback), so any preview here would just be a guess
	// that differs from the real result.
	trimmed := strings.TrimSpace(m.prompt)
	if isCheckout(trimmed) {
		branch, _, _ := dispatch.ParseBranchPrefix(trimmed)
		sb.WriteString("\n  " + styleFaint.Render("checkout: "+branch+"  (no prompt sent)") + "\n")
	}

	if m.errMsg != "" {
		sb.WriteString("\n  " + styleError.Render(m.errMsg) + "\n")
	}
	return sb.String()
}

// renderPromptInput renders the prompt buffer as one or more `> ` lines.
// Three prefix flavors visually distinguish the segment kinds so a reader
// can tell at a glance which lines are typed-newline boundaries vs. mere
// terminal-width overflows:
//   - first segment of the whole buffer:    `  > `   (bold prompt)
//   - first segment after a hard `\n` break: `    │ ` (faint guide)
//   - continuation segment from soft wrap:   `      ` (plain indent)
//
// Wrap is performed at runewidth boundaries so CJK / wide chars don't spill
// past the popup edge. width<=0 disables wrap (initial render before
// WindowSizeMsg arrives); the line is emitted verbatim and the terminal's
// implicit wrap handles overflow as before. cursor is a rune index into
// prompt; the `▏` glyph is drawn between runes at that position. At a
// soft-wrap boundary (where one segment's end equals the next segment's
// start in the same logical line) the cursor renders at the *start* of
// the next segment so the next inserted character lands where the user
// expects it.
func renderPromptInput(prompt string, cursor int, width int) string {
	// contentWidth is sized against the *widest* prefix (continuation = 6
	// cols) so a soft-wrapped line never overruns the popup. Using the
	// first-segment width (4 cols) here would let continuation lines spill
	// 2 cols past the edge — exactly the artifact this function is meant
	// to eliminate.
	contentWidth := 0
	if width > 0 {
		contentWidth = width - 6
		if contentWidth < 1 {
			contentWidth = 1
		}
	}

	type segment struct {
		text       string
		runeOffset int  // start position in the full prompt (rune index)
		runeLen    int  // number of runes in `text`
		isFirst    bool // first segment of the whole buffer
		hardBreak  bool // first segment of a logical line (after \n)
	}
	var segments []segment
	runePos := 0
	for li, logical := range strings.Split(prompt, "\n") {
		pieces := []string{logical}
		if contentWidth > 0 {
			pieces = wrapByWidth(logical, contentWidth)
		}
		linePos := runePos
		for pi, p := range pieces {
			pLen := len([]rune(p))
			segments = append(segments, segment{
				text:       p,
				runeOffset: linePos,
				runeLen:    pLen,
				isFirst:    li == 0 && pi == 0,
				hardBreak:  pi == 0,
			})
			linePos += pLen
		}
		// +1 advances past the '\n' between logical lines. The trailing
		// increment after the last line is harmless: no further segments
		// are appended so runePos isn't read again.
		runePos = linePos + 1
	}

	// Locate the cursor segment. At a soft-wrap boundary (cursor sits at
	// the join between two segments of the same logical line) prefer the
	// next segment so the caret visually lands where the next typed rune
	// will appear, not at the far end of the previous wrapped line.
	cursorSeg := -1
	cursorOffset := 0
	for i, s := range segments {
		if s.runeOffset > cursor {
			break
		}
		if cursor > s.runeOffset+s.runeLen {
			continue
		}
		// cursor falls within [runeOffset, runeOffset+runeLen]
		if cursor == s.runeOffset+s.runeLen && i+1 < len(segments) {
			next := segments[i+1]
			// next.runeOffset == s.runeOffset+s.runeLen ⇒ same logical
			// line (soft wrap). Hard breaks bump the offset by 1 for the
			// '\n', so they don't trigger this branch.
			if next.runeOffset == s.runeOffset+s.runeLen {
				continue
			}
		}
		cursorSeg = i
		cursorOffset = cursor - s.runeOffset
		break
	}
	if cursorSeg < 0 && len(segments) > 0 {
		// Out-of-range cursor (defensive): pin to end of last segment.
		cursorSeg = len(segments) - 1
		cursorOffset = segments[cursorSeg].runeLen
	}

	var b strings.Builder
	for i, seg := range segments {
		var prefix string
		switch {
		case seg.isFirst:
			prefix = "  " + stylePrompt.Render("> ")
		case seg.hardBreak:
			prefix = "    " + styleFaint.Render("│ ")
		default:
			prefix = "      "
		}
		b.WriteString(prefix)
		if i == cursorSeg {
			runes := []rune(seg.text)
			if cursorOffset < 0 {
				cursorOffset = 0
			}
			if cursorOffset > len(runes) {
				cursorOffset = len(runes)
			}
			b.WriteString(string(runes[:cursorOffset]))
			b.WriteString("▏")
			b.WriteString(string(runes[cursorOffset:]))
		} else {
			b.WriteString(seg.text)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// wrapByWidth splits s into chunks whose display width does not exceed
// width, breaking at rune boundaries. No word-aware logic — the prompt
// input is free-form text where cutting mid-word matches user
// expectations of "fill the popup row, then continue". Always returns at
// least one element so callers can iterate without an empty-input guard.
func wrapByWidth(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	var cur strings.Builder
	curW := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if curW+rw > width && cur.Len() > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
	}
	lines = append(lines, cur.String())
	return lines
}

// renderLauncherChoice renders the "<active> / <other>" launcher pair with
// the active launcher highlighted. Mirrors dispatch_launcher.fish:
//
//	dispatch_launcher 風: bold green = active, faint = inactive
func renderLauncherChoice(active dispatch.Launcher) string {
	claude, codex := "claude", "codex"
	if active == dispatch.LauncherClaude {
		return styleActive.Render(claude) + styleFaint.Render(" / ") + styleFaint.Render(codex)
	}
	return styleFaint.Render(claude) + styleFaint.Render(" / ") + styleActive.Render(codex)
}

func isCheckout(prompt string) bool {
	_, ck, _ := dispatch.ParseBranchPrefix(prompt)
	return ck
}

// viewportRows returns the maximum number of repo rows that fit in the popup.
// Falls back to a safe default when the size is unknown.
func (m *Model) viewportRows() int {
	if m.height <= 0 {
		return 16
	}
	// header(1) + launcher(1) + sep(1) + query(1) + error(1) = 5
	rows := m.height - 5
	if rows < 4 {
		rows = 4
	}
	return rows
}

// styles
var (
	stylePrompt  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	styleFaint   = lipgloss.NewStyle().Faint(true)
	styleCursor  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	// styleActive highlights the selected launcher in the toggle pair
	// (dispatch_launcher.fish uses bold bright-green for the same role).
	styleActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
)

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ── ExecRunner ────────────────────────────────────────────────────────────────

// ExecRunner is the production Runner that calls tmux / dispatch in process.
type ExecRunner struct{}

// SwitchClient switches the calling client to the named session.
func (ExecRunner) SwitchClient(name string) error {
	out, err := exec.Command("tmux", "switch-client", "-t", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux switch-client: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SpawnDispatch fires `tmux-sidebar dispatch <opts>` via `tmux run-shell
// -b`, mirroring how dispatch_launcher.fish hands work to dispatch.sh.
// The benefit over an in-process call is that the picker popup can close
// the moment this returns — git worktree creation and tmux session
// startup (which can take several seconds) run as a tmux-managed
// background process and stay alive after the popup exits. Dispatch
// errors that occur after the spawn are reported by the dispatch
// process itself via tmux display-message.
func (ExecRunner) SpawnDispatch(opts dispatch.Options) error {
	bin, err := os.Executable()
	if err != nil || bin == "" {
		bin = "tmux-sidebar"
	}
	parts := []string{shellQuote(bin), "dispatch"}
	for _, a := range opts.ToArgs() {
		parts = append(parts, shellQuote(a))
	}
	// Redirect both stdout and stderr to /dev/null. tmux run-shell -b
	// collects child output and dumps it into the calling client's
	// active pane on completion — without this, the structured
	// STATUS:/SESSION:/... lines that runDispatch prints for CLI
	// scrapers land at the top of the freshly attached session's pane
	// (visible above the launcher's prompt). Errors still reach the
	// user via the explicit `tmux display-message` call in main.go's
	// runDispatch error handler, so muting stderr here is safe.
	cmdLine := strings.Join(parts, " ") + " >/dev/null 2>&1"
	out, err := exec.Command("tmux", "run-shell", "-b", cmdLine).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux run-shell: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// shellQuote single-quotes s, escaping embedded single quotes. Used to
// build the run-shell command line so paths/options with spaces or
// metacharacters reach the dispatch subcommand intact.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
