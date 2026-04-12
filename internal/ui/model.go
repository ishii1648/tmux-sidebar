// Package ui provides the bubbletea TUI model for tmux-sidebar.
package ui

import (
	"encoding/json"
	"fmt"
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

// StateChangedMsg is sent (from main via p.Send) when a state file changes.
type StateChangedMsg struct{}

// TmuxChangedMsg is sent (from main via p.Send) when tmux window layout changes.
type TmuxChangedMsg struct{}

// minuteTickMsg is sent by the 1-minute ticker to refresh running-badge elapsed time.
type minuteTickMsg time.Time

// dataMsg carries refreshed tmux/state data.
type dataMsg struct {
	items       []ListItem
	winPaneNums map[string][]int // windowID → pane numbers (for state-only updates)
	err         error
}

// stateOnlyMsg carries a refreshed state map without touching tmux.
type stateOnlyMsg struct {
	stateMap map[int]state.PaneState
}

// switchWindowMsg is sent when the user presses Enter on a window row.
type switchWindowMsg struct {
	sessionName string
	windowIndex int
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
	winPaneNums  map[string][]int  // windowID → pane numbers; updated by loadData
	cursor       int               // index into visibleItems()
	currentWinID string            // window ID of the pane running this process
	filter       FilterMode
	width        int
	height       int               // terminal height (from WindowSizeMsg)
	offset       int               // scroll offset into visibleItems()
	err          error
	gitData      map[string]gitInfo // keyed by window ID
	focused      bool               // true when this pane has terminal focus
	searchQuery  string             // current search query text (always-on incremental filter)
}

// New creates a new Model. currentWinID is the window ID of this sidebar's own pane;
// it is determined once at startup and never changes.
func New(tc tmux.Client, sr state.Reader, width int, currentWinID string) *Model {
	return &Model{
		tmuxClient:   tc,
		stateReader:  sr,
		width:        width,
		gitData:      map[string]gitInfo{},
		winPaneNums:  map[string][]int{},
		focused:      false, // start unfocused; becomes true only when FocusMsg arrives
		currentWinID: currentWinID,
	}
}

// Init starts the first data load, the 1-minute badge-refresh ticker, and the 10-second git ticker.
// Live updates arrive via StateChangedMsg (fsnotify) and TmuxChangedMsg (SIGUSR1)
// injected by main through tea.Program.Send — no polling needed.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadData(),
		minuteTickCmd(),
		m.loadGitInfo(),
		gitTickCmd(),
	)
}

func minuteTickCmd() tea.Cmd {
	return tea.Tick(time.Minute, func(t time.Time) tea.Msg {
		return minuteTickMsg(t)
	})
}

func gitTickCmd() tea.Cmd {
	return tea.Tick(10*time.Second, func(t time.Time) tea.Msg {
		return gitTickMsg(t)
	})
}

func (m *Model) loadData() tea.Cmd {
	return func() tea.Msg {
		allPanes, err := m.tmuxClient.ListAll()
		if err != nil {
			return dataMsg{err: err}
		}
		stateMap, err := m.stateReader.Read()
		if err != nil {
			// Non-fatal: show empty state
			stateMap = map[int]state.PaneState{}
		}

		// Collect session order and names (first occurrence wins)
		var sessionOrder []string
		sessionSeen := map[string]bool{}
		sessionNames := map[string]string{}
		for _, p := range allPanes {
			if !sessionSeen[p.SessionID] {
				sessionSeen[p.SessionID] = true
				sessionOrder = append(sessionOrder, p.SessionID)
				sessionNames[p.SessionID] = p.SessionName
			}
		}

		// Build window→pane numbers map
		winPanes := map[string][]int{}
		for _, p := range allPanes {
			winPanes[p.WindowID] = append(winPanes[p.WindowID], p.PaneNumber)
		}

		// Collect window info per session, preserving order
		winOrder := map[string][]string{}
		winSeen := map[string]bool{}
		winInfo := map[string]tmux.Window{}
		for _, p := range allPanes {
			if !winSeen[p.WindowID] {
				winSeen[p.WindowID] = true
				winOrder[p.SessionID] = append(winOrder[p.SessionID], p.WindowID)
				winInfo[p.WindowID] = tmux.Window{
					SessionID:   p.SessionID,
					SessionName: p.SessionName,
					ID:          p.WindowID,
					Index:       p.WindowIndex,
					Name:        p.WindowName,
				}
			}
		}

		var items []ListItem
		for _, sid := range sessionOrder {
			sname := sessionNames[sid]
			if sname == "main" {
				continue
			}
			items = append(items, ListItem{
				Kind:        ItemSession,
				SessionName: sname,
			})
			for _, wid := range winOrder[sid] {
				w := winInfo[wid]
				item := ListItem{
					Kind:        ItemWindow,
					SessionName: sname,
					Window:      &w,
				}
				for _, num := range winPanes[wid] {
					if ps, ok := stateMap[num]; ok {
						psCopy := ps
						item.PaneState = &psCopy
						break
					}
				}
				items = append(items, item)
			}
		}

		return dataMsg{items: items, winPaneNums: winPanes}
	}
}

// loadStateOnly reads state files without spawning any tmux process.
func (m *Model) loadStateOnly() tea.Cmd {
	return func() tea.Msg {
		stateMap, _ := m.stateReader.Read()
		return stateOnlyMsg{stateMap: stateMap}
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

// visibleItems returns the items list filtered by the current filter mode and search query.
// For FilterAll with no search query it returns m.items directly (no allocation).
func (m *Model) visibleItems() []ListItem {
	if m.filter == FilterAll && m.searchQuery == "" {
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
			if cur != nil && m.matchesFilter(item) && m.matchesSearch(item) {
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

// matchesSearch reports whether item matches the current search query (case-insensitive substring).
func (m *Model) matchesSearch(item ListItem) bool {
	if m.searchQuery == "" {
		return true
	}
	q := strings.ToLower(m.searchQuery)
	if strings.Contains(strings.ToLower(item.SessionName), q) {
		return true
	}
	if item.Window != nil && strings.Contains(strings.ToLower(item.Window.Name), q) {
		return true
	}
	return false
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

	case TmuxChangedMsg:
		// tmux window layout changed (SIGUSR1) — reload tmux + state together.
		return m, m.loadData()

	case StateChangedMsg:
		// A state file changed (fsnotify) — reload state only, no tmux process.
		return m, m.loadStateOnly()

	case minuteTickMsg:
		// Refresh running-badge elapsed time (changes at most once per minute).
		return m, tea.Batch(m.loadStateOnly(), minuteTickCmd())

	case dataMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.items = msg.items
		m.winPaneNums = msg.winPaneNums
		m.err = nil
		// Clamp cursor to the visible list
		maxCursor := m.maxWindowIndex()
		if m.cursor > maxCursor {
			m.cursor = maxCursor
		}
		visible := m.visibleItems()
		if m.cursor < len(visible) && visible[m.cursor].Kind != ItemWindow {
			m.resetCursorToCurrentWindow()
		}
		m.adjustScroll()
		return m, nil

	case stateOnlyMsg:
		// Update PaneState on each window item using the cached pane-number map.
		for i, item := range m.items {
			if item.Kind != ItemWindow || item.Window == nil {
				continue
			}
			m.items[i].PaneState = nil
			for _, num := range m.winPaneNums[item.Window.ID] {
				if ps, ok := msg.stateMap[num]; ok {
					psCopy := ps
					m.items[i].PaneState = &psCopy
					break
				}
			}
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
		m.height = msg.Height
		m.adjustScroll()

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

	switch msg.Type {
	case tea.KeyEscape:
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.resetCursorToFirstWindow()
		}
		return m, nil
	case tea.KeyEnter:
		return m, m.switchSelected()
	case tea.KeyBackspace:
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			m.resetCursorToFirstWindow()
		}
		return m, nil
	case tea.KeyTab:
		m.filter = (m.filter + 1) % 2
		m.searchQuery = ""
		m.resetCursorToCurrentWindow()
		return m, nil
	case tea.KeyShiftTab:
		m.filter = (m.filter + 1) % 2
		m.searchQuery = ""
		m.resetCursorToCurrentWindow()
		return m, nil
	case tea.KeyUp:
		m.moveCursor(-1)
		return m, nil
	case tea.KeyDown:
		m.moveCursor(1)
		return m, nil
	case tea.KeyRunes:
		r := msg.Runes
		// j/k navigation only when search is empty
		if m.searchQuery == "" && len(r) == 1 {
			switch r[0] {
			case 'j':
				m.moveCursor(1)
				return m, nil
			case 'k':
				m.moveCursor(-1)
				return m, nil
			}
		}
		m.searchQuery += string(r)
		m.resetCursorToFirstWindow()
		return m, nil
	}
	return m, nil
}

// headerLines returns the number of fixed lines above the item list
// (title + filter tabs + separator).
const headerLines = 3

// footerLines returns the number of fixed lines below the item list
// (blank + key hints).
const footerLines = 2

// viewportHeight returns the number of item rows that fit on screen.
// Returns 0 when the terminal height is unknown or too small.
func (m *Model) viewportHeight() int {
	if m.height <= headerLines+footerLines {
		return 0
	}
	return m.height - headerLines - footerLines
}

// adjustScroll ensures the cursor is within the visible viewport,
// adjusting offset as needed.
func (m *Model) adjustScroll() {
	vp := m.viewportHeight()
	if vp <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+vp {
		m.offset = m.cursor - vp + 1
	}
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
			m.adjustScroll()
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

// resetCursorToCurrentWindow sets the cursor to the current window (by currentWinID) in visibleItems,
// falling back to the first window item if not found.
func (m *Model) resetCursorToFirstWindow() {
	visible := m.visibleItems()
	for i, item := range visible {
		if item.Kind == ItemWindow {
			m.cursor = i
			m.offset = 0
			m.adjustScroll()
			return
		}
	}
	m.cursor = 0
	m.offset = 0
}

func (m *Model) resetCursorToCurrentWindow() {
	visible := m.visibleItems()
	firstWindow := -1
	for i, item := range visible {
		if item.Kind != ItemWindow {
			continue
		}
		if firstWindow < 0 {
			firstWindow = i
		}
		if item.Window.ID == m.currentWinID {
			m.cursor = i
			m.offset = 0
			m.adjustScroll()
			return
		}
	}
	if firstWindow >= 0 {
		m.cursor = firstWindow
	} else {
		m.cursor = 0
	}
	m.offset = 0
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

	// Filter tabs
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

	// Search prompt (always visible, replaces separator)
	faintSep := lipgloss.NewStyle().Faint(true).Render(strings.Repeat("─", m.width))
	if m.searchQuery != "" {
		sb.WriteString(faintSep + "\n")
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6")).Render("> ") + m.searchQuery + "▏\n")
		sb.WriteString(faintSep + "\n")
	} else if m.focused {
		sb.WriteString(faintSep + "\n")
		sb.WriteString(lipgloss.NewStyle().Faint(true).Render("> type to filter...") + "\n")
		sb.WriteString(faintSep + "\n")
	} else {
		sb.WriteString(faintSep + "\n")
	}

	// Session / window list
	visible := m.visibleItems()
	vp := m.viewportHeight()
	startIdx := m.offset
	endIdx := len(visible)
	if vp > 0 && startIdx+vp < endIdx {
		endIdx = startIdx + vp
	}
	if startIdx > endIdx {
		startIdx = endIdx
	}
	for i := startIdx; i < endIdx; i++ {
		item := visible[i]
		switch item.Kind {
		case ItemSession:
			sb.WriteString(styleSession.Render(item.SessionName) + "\n")
		case ItemWindow:
			cursor := "  "
			if i == m.cursor {
				if m.focused {
					cursor = styleCursor.Render("▶ ")
				} else {
					cursor = lipgloss.NewStyle().Faint(true).Render("▶ ")
				}
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
			sb.WriteString(cursor + styleWindow.Render(label+suffix) + "\n")
		}
	}

	// Footer: scroll indicator + key hints
	if vp > 0 && endIdx < len(visible) {
		sb.WriteString(lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("  ↓ %d more", len(visible)-endIdx)) + "\n")
	} else {
		sb.WriteString("\n")
	}
	sb.WriteString(lipgloss.NewStyle().Faint(true).MaxWidth(m.width).Render("Tab:filter Esc:clear ^C:quit") + "\n")
	return sb.String()
}

func renderBadge(ps *state.PaneState) string {
	switch ps.Status {
	case state.StatusRunning:
		mins := int(ps.Elapsed.Minutes())
		return styleBadgeRun.Render(fmt.Sprintf("🔄%dm", mins))
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
