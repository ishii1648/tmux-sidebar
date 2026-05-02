// Package picker implements the popup wizard that creates a new tmux session
// from a ghq repository plus a launcher (claude / codex). It is the
// tmux-sidebar `N` key UI and mirrors dotfiles' dispatch_launcher.fish so
// the same workflow is available without fish/fzf.
//
// The picker is launched by pane mode via `tmux display-popup -E` running
// `tmux-sidebar new --context=<file>`. It is a separate Bubble Tea program
// from the sidebar; the two communicate only through the temp context file
// and tmux state.
package picker

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ishii1648/tmux-sidebar/internal/dispatch"
	"github.com/ishii1648/tmux-sidebar/internal/repo"
)

// step is the picker's wizard position.
type step int

const (
	stepRepo   step = iota // pick a repo + toggle launcher with Tab
	stepPrompt             // enter the dispatch prompt
)

// dispatchResultMsg carries the outcome of an async Runner.Dispatch call.
// dispatching is reset to false on receipt; on success the picker exits
// with statusMsg, on error the message stays visible for the user to see.
type dispatchResultMsg struct {
	name string
	err  error
}

// spinnerTickMsg drives the spinner animation while dispatching.
type spinnerTickMsg struct{}

const spinnerInterval = 100 * time.Millisecond

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func spinnerTick() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// Runner abstracts the tmux / dispatch invocations the picker performs.
// Tests substitute a fake; production wires ExecRunner.
type Runner interface {
	// SwitchClient moves the calling tmux client to the named session.
	// Used both for "repo already open → switch instead of dispatch" on
	// Step 1 and for jumping to the freshly dispatched session on Step 2.
	SwitchClient(name string) error
	// Dispatch creates a worktree + tmux session and starts the configured
	// launcher with the given prompt. Returns the new session name on
	// success so the caller can switch to it.
	Dispatch(opts dispatch.Options) (string, error)
}

// Model is the picker's Bubble Tea model.
type Model struct {
	ctx          Context
	openSessions map[string]struct{} // session names already open

	repos    []repo.Repo
	filtered []repo.Repo

	step     step
	query    string
	cursor   int
	launcher dispatch.Launcher // current launcher selection (claude / codex)

	// prompt is the in-progress prompt body when step==stepPrompt.
	// Multi-line is supported via paste (LF preserved) and via
	// shift+enter / alt+enter / ctrl+j when the terminal differentiates
	// them from plain Enter.
	prompt string

	width  int
	height int

	errMsg    string // shown on the bottom line; cleared on next key
	statusMsg string // shown after a successful exec while quitting
	quitting  bool
	runner    Runner

	// dispatching is true between Enter on Step 2 and the dispatchResultMsg
	// returning. While true the prompt keys are ignored and the view shows
	// a spinner so users know the picker is working, not hung.
	dispatching    bool
	dispatchTarget string // repo basename being dispatched; shown in the spinner line
	spinFrame      int
}

// New creates a Model. repos is the discovered ghq list (caller fetches
// before constructing so failures surface synchronously). runner executes
// tmux / dispatch commands on Enter.
func New(ctx Context, repos []repo.Repo, runner Runner) *Model {
	repo.SortByBasename(repos)
	openSessions := map[string]struct{}{}
	for _, s := range ctx.Sessions {
		openSessions[s.Name] = struct{}{}
	}
	m := &Model{
		ctx:          ctx,
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
	case dispatchResultMsg:
		m.dispatching = false
		if msg.err != nil {
			m.errMsg = "dispatch failed: " + msg.err.Error()
			return m, nil
		}
		m.statusMsg = "dispatched into " + msg.name
		m.quitting = true
		return m, tea.Quit
	case spinnerTickMsg:
		// Advance the spinner frame and schedule the next tick — but only
		// while still dispatching. On completion the tick chain dies on
		// its own (no Cmd returned), so we don't leak goroutines.
		if m.dispatching {
			m.spinFrame = (m.spinFrame + 1) % len(spinnerFrames)
			return m, spinnerTick()
		}
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
		return m, nil
	case tea.KeyUp, tea.KeyCtrlP:
		m.moveCursor(-1)
		return m, nil
	case tea.KeyDown, tea.KeyCtrlN:
		m.moveCursor(1)
		return m, nil
	case tea.KeyBackspace:
		if len(m.query) > 0 {
			m.query = m.query[:len(m.query)-1]
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
	// While a dispatch is in flight, drop input — the spinner is showing
	// progress, and accidental Enter/Esc/etc. could either re-fire the
	// dispatch or leave the model in an inconsistent state.
	if m.dispatching {
		return m, nil
	}
	// Multi-line shortcuts: shift+enter / alt+enter / ctrl+j insert a
	// literal newline rather than firing dispatch. Detection requires the
	// terminal to differentiate these from plain Enter (kitty keyboard
	// protocol or similar). On terminals that don't, shift+enter still
	// arrives as KeyEnter and submits — the user can then fall back to
	// paste for multi-line input.
	if isNewlineKey(msg) {
		m.prompt += "\n"
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
	case tea.KeyBackspace:
		if len(m.prompt) > 0 {
			m.prompt = m.prompt[:len(m.prompt)-1]
		}
		return m, nil
	case tea.KeySpace:
		m.prompt += " "
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
		m.prompt += s
		return m, nil
	}
	return m, nil
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

// execDispatch parses branch / checkout-mode out of the prompt buffer and
// fires the runner's Dispatch in a goroutine. Returns a tea.Cmd that ends
// in dispatchResultMsg, plus a tea.Cmd that drives the spinner so the user
// has visible feedback while git worktree / tmux session creation runs
// (which takes several seconds for fresh worktrees).
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
		// Picker controls the switch ordering: switching the client up
		// front lets codex's OSC 11 query resolve (ADR-065) before the
		// launcher even starts, instead of deadlocking the post-create
		// wait loop for 5 minutes.
		Switch: true,
	}
	switch {
	case checkout:
		opts.Branch = branch
		opts.NoPrompt = true
	default:
		opts.Branch = dispatch.BranchFromPrompt(body)
		opts.Prompt = body
	}
	m.dispatching = true
	m.dispatchTarget = r.Basename
	m.spinFrame = 0
	runner := m.runner
	dispatchCmd := func() tea.Msg {
		name, err := runner.Dispatch(opts)
		return dispatchResultMsg{name: name, err: err}
	}
	return tea.Batch(dispatchCmd, spinnerTick())
}

// applyFilter recomputes m.filtered from m.query and clamps the cursor.
func (m *Model) applyFilter() {
	m.filtered = repo.Filter(m.repos, m.query)
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
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
	maxRows := m.viewportRows()
	for i, r := range m.filtered {
		if i >= maxRows {
			sb.WriteString(styleFaint.Render(fmt.Sprintf("  ↓ %d more", len(m.filtered)-maxRows)) + "\n")
			break
		}
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
	if len(m.filtered) == 0 {
		sb.WriteString(styleFaint.Render("  (no matching repos)") + "\n")
	}
	if m.errMsg != "" {
		sb.WriteString(styleError.Render(m.errMsg) + "\n")
	}
	return sb.String()
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

	if m.dispatching {
		// Replace the input + branch preview with a status line while the
		// async Dispatch runs. The spinner gives users a "still working"
		// signal — fresh worktree creation can take several seconds and a
		// frozen-looking screen would otherwise feel like a hang.
		spin := spinnerFrames[m.spinFrame%len(spinnerFrames)]
		sb.WriteString("  " + styleStatus.Render(spin+" dispatching "+m.dispatchTarget+"...") + "\n")
		return sb.String()
	}

	sb.WriteString(renderPromptInput(m.prompt))

	// Branch derivation hint (faint, below the input).
	trimmed := strings.TrimSpace(m.prompt)
	switch {
	case trimmed == "":
		// no hint when empty — keep the input area uncluttered
	case isCheckout(trimmed):
		branch, _, _ := dispatch.ParseBranchPrefix(trimmed)
		sb.WriteString("\n  " + styleFaint.Render("checkout: "+branch+"  (no prompt sent)") + "\n")
	default:
		sb.WriteString("\n  " + styleFaint.Render("branch: "+dispatch.BranchFromPrompt(trimmed)) + "\n")
	}

	if m.errMsg != "" {
		sb.WriteString("\n  " + styleError.Render(m.errMsg) + "\n")
	}
	return sb.String()
}

// renderPromptInput renders the prompt buffer as one or more `> ` lines.
// First line gets the bold `> ` prefix; continuation lines get a faint two
// space indent so they line up under the cursor column. The cursor `▏` is
// only shown on the last line. Empty prompt still draws one prefix line so
// the user sees the input area.
func renderPromptInput(prompt string) string {
	lines := strings.Split(prompt, "\n")
	var b strings.Builder
	for i, line := range lines {
		var prefix string
		if i == 0 {
			prefix = "  " + stylePrompt.Render("> ")
		} else {
			prefix = "    " + styleFaint.Render("│ ")
		}
		b.WriteString(prefix)
		b.WriteString(line)
		if i == len(lines)-1 {
			b.WriteString("▏")
		}
		b.WriteString("\n")
	}
	return b.String()
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
	// styleStatus is used for the spinner line during dispatch — yellow
	// to signal "in progress" without competing with red error styling.
	styleStatus = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
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

// Dispatch invokes internal/dispatch.Launch in-process. We don't fork
// `tmux-sidebar dispatch` because the picker is the same binary; calling
// the Go function directly is faster and surfaces structured errors.
func (ExecRunner) Dispatch(opts dispatch.Options) (string, error) {
	res, err := dispatch.Launch(opts)
	if err != nil {
		return "", err
	}
	return res.SessionName, nil
}
