// Package ui provides the bubbletea TUI model for tmux-sidebar.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ishii1648/tmux-sidebar/internal/state"
	"github.com/ishii1648/tmux-sidebar/internal/tmux"
)

// FilterMode describes which windows are shown in the sidebar list.
type FilterMode int

const (
	FilterAll     FilterMode = iota // すべてのウィンドウを表示
	FilterWaiting                   // permission または ask 状態のウィンドウのみ
)

func (f FilterMode) String() string {
	switch f {
	case FilterWaiting:
		return "Waiting"
	default:
		return "All"
	}
}

// ItemKind distinguishes session-header rows from window rows.
type ItemKind int

const (
	ItemSession ItemKind = iota
	ItemWindow
)

// ListItem is a single rendered row in the sidebar list.
type ListItem struct {
	Kind        ItemKind
	SessionName string
	Window      *tmux.Window     // non-nil when Kind == ItemWindow
	PaneState   *state.PaneState // non-nil when a Claude pane exists in this window
}

// gitInfo holds cached git/PR information for a window.
type gitInfo struct {
	branch   string
	ahead    int    // number of commits ahead of origin/HEAD
	prState  string // "open", "draft", "merged", or ""
	prNumber int    // 0 if no PR
}

// Message types.

// tickMsg is sent by the 1-second polling ticker.
type tickMsg time.Time

// dataMsg carries refreshed tmux/state data.
type dataMsg struct {
	items        []ListItem
	currentWinID string
	err          error
}

// switchWindowMsg is sent when the user presses Enter on a window row.
type switchWindowMsg struct {
	sessionName string
	windowIndex int
}

// cursorTickMsg is sent by the 200ms cursor-sync ticker.
type cursorTickMsg time.Time

// currentWinMsg carries the refreshed active window ID.
type currentWinMsg struct {
	currentWinID string
}

// gitTickMsg is sent by the 10-second git polling ticker.
type gitTickMsg time.Time

// gitDataMsg carries refreshed git/PR info for all visible windows.
type gitDataMsg struct {
	data map[string]gitInfo // keyed by window ID
}

// Styles used for rendering.
var (
	styleCursor       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	styleSession      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	styleWindow       = lipgloss.NewStyle().PaddingLeft(1)
	styleBadgeRun     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleBadgeIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleBadgePerm    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleBadgeAsk     = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleHeader       = lipgloss.NewStyle().Bold(true).Underline(true)
	styleFilterActive = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleFilterFaint  = lipgloss.NewStyle().Faint(true)
	stylePRDraft      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	stylePROpen       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // green
	stylePRMerged     = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))  // magenta
)

// Model is the bubbletea Model for the sidebar.
type Model struct {
	tmuxClient   tmux.Client
	stateReader  state.Reader
	items        []ListItem
	cursor       int    // index into visibleItems()
	currentWinID string // window ID of the pane running this process
	filter       FilterMode
	width        int
	err          error
	gitData      map[string]gitInfo // keyed by window ID
	focused      bool               // true when this pane has terminal focus
	passive      bool               // true when user pressed q to pause interaction
}

// New creates a new Model.
func New(tc tmux.Client, sr state.Reader, width int) *Model {
	// TMUX_SIDEBAR_FORCE_FOCUS=1 bypasses terminal focus events (used in e2e tests
	// where isolated tmux servers have no attached client and cannot send focus events).
	forceFocus := os.Getenv("TMUX_SIDEBAR_FORCE_FOCUS") != ""
	return &Model{
		tmuxClient:  tc,
		stateReader: sr,
		width:       width,
		gitData:     map[string]gitInfo{},
		focused:     forceFocus,
	}
}

// Init starts the first data load, the 1-second ticker, and the 10-second git ticker.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadData(),
		tickCmd(),
		m.loadGitInfo(),
		gitTickCmd(),
		cursorTickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func gitTickCmd() tea.Cmd {
	return tea.Tick(10*time.Second, func(t time.Time) tea.Msg {
		return gitTickMsg(t)
	})
}

func cursorTickCmd() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg {
		return cursorTickMsg(t)
	})
}

func (m *Model) loadCurrentWin() tea.Cmd {
	return func() tea.Msg {
		cur, _ := m.tmuxClient.CurrentPane()
		return currentWinMsg{currentWinID: cur.WindowID}
	}
}

func (m *Model) loadData() tea.Cmd {
	return func() tea.Msg {
		sessions, err := m.tmuxClient.ListSessions()
		if err != nil {
			return dataMsg{err: err}
		}
		windows, err := m.tmuxClient.ListWindows()
		if err != nil {
			return dataMsg{err: err}
		}
		panes, err := m.tmuxClient.ListPanes()
		if err != nil {
			return dataMsg{err: err}
		}
		stateMap, err := m.stateReader.Read()
		if err != nil {
			// Non-fatal: show empty state
			stateMap = map[int]state.PaneState{}
		}

		// Build window→paneNumbers map
		winPanes := map[string][]int{} // windowID → pane numbers
		for _, p := range panes {
			winPanes[p.WindowID] = append(winPanes[p.WindowID], p.Number)
		}

		// Build session→windows map preserving order
		sessionOrder := make([]string, 0, len(sessions))
		sessionMap := map[string]tmux.Session{}
		for _, s := range sessions {
			sessionOrder = append(sessionOrder, s.ID)
			sessionMap[s.ID] = s
		}

		winBySession := map[string][]tmux.Window{}
		for _, w := range windows {
			winBySession[w.SessionID] = append(winBySession[w.SessionID], w)
		}

		var items []ListItem
		for _, sid := range sessionOrder {
			s := sessionMap[sid]
			if s.Name == "main" {
				continue
			}
			items = append(items, ListItem{
				Kind:        ItemSession,
				SessionName: s.Name,
			})
			for i := range winBySession[sid] {
				w := winBySession[sid][i]
				item := ListItem{
					Kind:        ItemWindow,
					SessionName: s.Name,
					Window:      &w,
				}
				// Check if any pane in this window has state
				for _, num := range winPanes[w.ID] {
					if ps, ok := stateMap[num]; ok {
						psCopy := ps
						item.PaneState = &psCopy
						break
					}
				}
				items = append(items, item)
			}
		}

		return dataMsg{items: items}
	}
}

// loadGitInfo fetches git branch/ahead/PR info for all visible windows in parallel.
// gh pr view is skipped when the branch is unchanged (uses cached PR data instead).
func (m *Model) loadGitInfo() tea.Cmd {
	visible := m.visibleItems()
	client := m.tmuxClient
	oldData := m.gitData // snapshot for branch comparison
	return func() tea.Msg {
		data := make(map[string]gitInfo)
		var mu sync.Mutex
		var wg sync.WaitGroup

		for _, item := range visible {
			if item.Kind != ItemWindow || item.Window == nil {
				continue
			}
			wg.Add(1)
			go func(item ListItem) {
				defer wg.Done()
				info := fetchGitInfo(client, item, oldData[item.Window.ID])
				if info.branch != "" || info.prNumber != 0 {
					mu.Lock()
					data[item.Window.ID] = info
					mu.Unlock()
				}
			}(item)
		}
		wg.Wait()
		return gitDataMsg{data: data}
	}
}

// fetchGitInfo runs git/gh commands for a single window item and returns the result.
// old is the previously cached gitInfo; gh pr view is skipped when branch is unchanged.
func fetchGitInfo(client tmux.Client, item ListItem, old gitInfo) gitInfo {
	var path string
	if item.PaneState != nil && item.PaneState.WorkDir != "" {
		path = item.PaneState.WorkDir
	} else {
		var err error
		path, err = client.PaneCurrentPath(item.Window.ID)
		if err != nil || path == "" {
			return gitInfo{}
		}
	}

	branchOut, err := exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return gitInfo{} // not a git repo
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "HEAD" {
		if shaOut, err := exec.Command("git", "-C", path, "rev-parse", "--short", "HEAD").Output(); err == nil {
			branch = "(" + strings.TrimSpace(string(shaOut)) + ")"
		}
	}

	aheadOut, _ := exec.Command("git", "-C", path, "rev-list", "--count", "HEAD", "^origin/HEAD").Output()
	ahead, _ := strconv.Atoi(strings.TrimSpace(string(aheadOut)))

	info := gitInfo{branch: branch, ahead: ahead}

	// Reuse cached PR data when branch is unchanged to avoid redundant API calls.
	if branch == old.branch {
		info.prState = old.prState
		info.prNumber = old.prNumber
		return info
	}

	if _, err := exec.LookPath("gh"); err == nil {
		cmd := exec.Command("gh", "pr", "view", "--json", "number,state,isDraft")
		cmd.Dir = path
		if prOut, err := cmd.Output(); err == nil {
			var prData struct {
				Number  int    `json:"number"`
				State   string `json:"state"`
				IsDraft bool   `json:"isDraft"`
			}
			if json.Unmarshal(prOut, &prData) == nil && prData.Number != 0 {
				s := strings.ToLower(prData.State)
				if prData.IsDraft {
					s = "draft"
				}
				info.prState = s
				info.prNumber = prData.Number
			}
		}
	}

	return info
}

// visibleItems returns the items list filtered by the current filter mode.
// For FilterAll it returns m.items directly (no allocation).
func (m *Model) visibleItems() []ListItem {
	if m.filter == FilterAll {
		return m.items
	}

	type sessionBuf struct {
		header  ListItem
		windows []ListItem
	}
	var result []ListItem
	var cur *sessionBuf

	for _, item := range m.items {
		switch item.Kind {
		case ItemSession:
			if cur != nil && len(cur.windows) > 0 {
				result = append(result, cur.header)
				result = append(result, cur.windows...)
			}
			cur = &sessionBuf{header: item}
		case ItemWindow:
			if cur != nil && m.matchesFilter(item) {
				cur.windows = append(cur.windows, item)
			}
		}
	}
	// Flush the last session
	if cur != nil && len(cur.windows) > 0 {
		result = append(result, cur.header)
		result = append(result, cur.windows...)
	}
	return result
}

// matchesFilter reports whether item should be included under the current filter.
func (m *Model) matchesFilter(item ListItem) bool {
	switch m.filter {
	case FilterWaiting:
		return item.PaneState != nil &&
			(item.PaneState.Status == state.StatusPermission || item.PaneState.Status == state.StatusAsk)
	default:
		return true
	}
}

// Update handles incoming messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		return m, tea.Batch(m.loadData(), tickCmd())

	case dataMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.items = msg.items
		m.err = nil
		// currentWinID is managed solely by currentWinMsg (loadCurrentWin 200ms poll).
		// Just clamp cursor to valid range after items update.
		maxCursor := m.maxWindowIndex()
		if m.cursor > maxCursor {
			m.cursor = maxCursor
		}
		visible := m.visibleItems()
		if m.cursor < len(visible) && visible[m.cursor].Kind != ItemWindow {
			m.resetCursorToFirstWindow()
		}
		return m, nil

	case cursorTickMsg:
		return m, tea.Batch(m.loadCurrentWin(), cursorTickCmd())

	case currentWinMsg:
		if msg.currentWinID != "" && msg.currentWinID != m.currentWinID {
			m.currentWinID = msg.currentWinID
			m.syncCursorToActiveWindow()
		}
		return m, nil

	case gitTickMsg:
		return m, tea.Batch(m.loadGitInfo(), gitTickCmd())

	case gitDataMsg:
		m.gitData = msg.data
		return m, nil

	case switchWindowMsg:
		return m, func() tea.Msg {
			_ = m.tmuxClient.SwitchWindow(msg.sessionName, msg.windowIndex)
			return nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width

	case tea.FocusMsg:
		m.focused = true

	case tea.BlurMsg:
		m.focused = false
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	}

	if !m.focused {
		return m, nil
	}

	// q/i toggle passive mode regardless of other state.
	switch msg.String() {
	case "q":
		m.passive = true
		return m, nil
	case "i":
		m.passive = false
		return m, nil
	}

	if m.passive {
		return m, nil
	}

	switch msg.String() {
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "tab":
		m.filter = (m.filter + 1) % 2
		m.resetCursorToFirstWindow()
	case "shift+tab":
		m.filter = (m.filter + 1) % 2
		m.resetCursorToFirstWindow()
	case "enter":
		return m, m.switchSelected()
	}
	return m, nil
}

// moveCursor advances the cursor by delta within visibleItems, skipping session-header rows.
func (m *Model) moveCursor(delta int) {
	visible := m.visibleItems()
	next := m.cursor + delta
	for {
		if next < 0 || next >= len(visible) {
			return
		}
		if visible[next].Kind == ItemWindow {
			m.cursor = next
			return
		}
		next += delta
	}
}

// maxWindowIndex returns the index of the last window item in visibleItems.
func (m *Model) maxWindowIndex() int {
	visible := m.visibleItems()
	for i := len(visible) - 1; i >= 0; i-- {
		if visible[i].Kind == ItemWindow {
			return i
		}
	}
	return 0
}

// resetCursorToFirstWindow sets the cursor to the first window item in visibleItems.
func (m *Model) resetCursorToFirstWindow() {
	visible := m.visibleItems()
	for i, item := range visible {
		if item.Kind == ItemWindow {
			m.cursor = i
			return
		}
	}
	m.cursor = 0
}

// syncCursorToActiveWindow sets the cursor to the currently active tmux window.
func (m *Model) syncCursorToActiveWindow() {
	visible := m.visibleItems()
	for i, item := range visible {
		if item.Kind == ItemWindow && item.Window != nil && item.Window.ID == m.currentWinID {
			m.cursor = i
			return
		}
	}
	// Fallback: clamp cursor to valid range
	if m.cursor >= len(visible) {
		m.cursor = len(visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// switchSelected builds a Cmd that switches to the currently selected window.
func (m *Model) switchSelected() tea.Cmd {
	visible := m.visibleItems()
	if m.cursor >= len(visible) {
		return nil
	}
	item := visible[m.cursor]
	if item.Kind != ItemWindow || item.Window == nil {
		return nil
	}
	return func() tea.Msg {
		return switchWindowMsg{
			sessionName: item.SessionName,
			windowIndex: item.Window.Index,
		}
	}
}

// View renders the sidebar.
func (m *Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("error: %v\n", m.err)
	}

	var sb strings.Builder

	// Title — ● when this pane is focused, ○ when another pane is active
	if m.focused {
		sb.WriteString(styleHeader.Render("● Sessions") + "\n")
	} else {
		sb.WriteString(lipgloss.NewStyle().Faint(true).Render("○ Sessions") + "\n")
	}

	// Filter tabs: [All] [Waiting]
	for _, f := range []FilterMode{FilterAll, FilterWaiting} {
		label := "[" + f.String() + "]"
		if f == m.filter {
			sb.WriteString(styleFilterActive.Render(label))
		} else {
			sb.WriteString(styleFilterFaint.Render(label))
		}
		sb.WriteString(" ")
	}
	sb.WriteString("\n")

	sep := strings.Repeat("─", m.width)
	if m.focused {
		sb.WriteString(sep + "\n")
	} else {
		sb.WriteString(lipgloss.NewStyle().Faint(true).Render(sep) + "\n")
	}

	// Session / window list
	visible := m.visibleItems()
	for i, item := range visible {
		switch item.Kind {
		case ItemSession:
			sb.WriteString(styleSession.Render(item.SessionName) + "\n")
		case ItemWindow:
			cursor := "  "
			if i == m.cursor && m.focused && !m.passive {
				cursor = styleCursor.Render("▶ ")
			}
			// Build suffix: badge + PR (right side, fixed width)
			suffix := ""
			if item.PaneState != nil {
				if b := renderBadge(item.PaneState); b != "" {
					suffix = " " + b
				}
			}
			if git, ok := m.gitData[item.Window.ID]; ok && git.prNumber != 0 {
				suffix += " " + renderPRBadge(git.prState, git.prNumber)
			}
			// Truncate window name to fit: cursor(2) + padding(1) + "N: " + name + suffix <= m.width
			prefix := fmt.Sprintf("%d: ", item.Window.Index)
			available := m.width - 2 - 1 - len(prefix) - lipgloss.Width(suffix)
			name := item.Window.Name
			if available <= 0 {
				name = ""
			} else if len(name) > available {
				name = name[:available]
			}
			label := prefix + name
			if item.Window.ID == m.currentWinID {
				label = lipgloss.NewStyle().Underline(true).Render(label)
			}
			sb.WriteString(cursor + styleWindow.Render(label+suffix) + "\n")
		}
	}

	// Footer: show passive hint or key hints depending on state
	if m.passive {
		sb.WriteString("\n" + lipgloss.NewStyle().Faint(true).MaxWidth(m.width).Render("[i] to activate") + "\n")
	} else if m.focused {
		sb.WriteString("\n" + lipgloss.NewStyle().Faint(true).MaxWidth(m.width).Render("Tab:filter  ^C:quit") + "\n")
	}
	return sb.String()
}

func renderBadge(ps *state.PaneState) string {
	switch ps.Status {
	case state.StatusRunning:
		var text string
		if ps.Elapsed < time.Minute {
			secs := int(ps.Elapsed.Seconds())
			text = fmt.Sprintf("🔄%ds", secs)
		} else {
			mins := int(ps.Elapsed.Minutes())
			text = fmt.Sprintf("🔄%dm", mins)
		}
		return styleBadgeRun.Render(text)
	case state.StatusIdle:
		return "" // idle は非表示
	case state.StatusPermission:
		return styleBadgePerm.Render("💬")
	case state.StatusAsk:
		return styleBadgeAsk.Render("💬")
	default:
		return ""
	}
}

func renderPRBadge(prState string, number int) string {
	text := fmt.Sprintf("#%d", number)
	switch prState {
	case "draft":
		return stylePRDraft.Render(text)
	case "open":
		return stylePROpen.Render(text)
	case "merged":
		return stylePRMerged.Render(text)
	default:
		return text
	}
}
