// Package ui provides the bubbletea TUI model for tmux-sidebar.
package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ishii1648/tmux-sidebar/internal/config"
	"github.com/ishii1648/tmux-sidebar/internal/session"
	"github.com/ishii1648/tmux-sidebar/internal/state"
	"github.com/ishii1648/tmux-sidebar/internal/tmux"
	"github.com/mattn/go-runewidth"
)

// inputMode is the modal input state. Phase 1: normal accepts single-key
// commands (j/k/d/D/.../Enter), search accepts text into searchQuery.
type inputMode int

const (
	modeNormal inputMode = iota
	modeSearch
)

// confirmAction is a sub-state of normal mode that gates a destructive
// operation behind a y/N prompt. Until the user answers, all other
// commands are blocked.
type confirmAction int

const (
	confirmNone confirmAction = iota
	confirmKillWindow
	confirmKillSession
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
	// ItemDivider is the horizontal rule rendered between the pinned and
	// unpinned session groups. Cursor logic skips it like a session header.
	ItemDivider
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
	branch      string
	ahead       int       // number of commits ahead of origin/HEAD
	prState     string    // "open", "draft", "merged", or ""
	prNumber    int       // 0 if no PR
	prFetchedAt time.Time // when gh pr view was last called (zero means never)
}

// promptMsg carries a fetched initial prompt for the preview area.
type promptMsg struct {
	cacheKey string
	prompt   string
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
	activeWinID string           // currently active tmux window ID (empty on error)
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

// killResultMsg is sent after a kill-window/kill-session attempt finishes.
// gravePath is non-empty on success when capture-pane wrote a snapshot.
// killedSession is set (and only set) for a successful session kill — the
// Update handler uses it to drop the name from pinned_sessions so the file
// does not accumulate stale entries.
type killResultMsg struct {
	gravePath     string
	err           error
	killedSession string
}

// Styles used for rendering. Colors use AdaptiveColor so the sidebar works on
// both light and dark terminal backgrounds.
var (
	colAccent   = lipgloss.AdaptiveColor{Light: "#0066cc", Dark: "#3b9eff"}
	colMuted    = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}
	colRunning  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	colPending  = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	colWaiting  = lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#a371f7"}
	colCodex    = lipgloss.AdaptiveColor{Light: "#0e7490", Dark: "#22d3ee"}
	colActiveBg = lipgloss.AdaptiveColor{Light: "#ddf4ff", Dark: "#0a3069"}

	styleCursor     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styleSession    = lipgloss.NewStyle().Foreground(colMuted)
	styleWindow     = lipgloss.NewStyle().PaddingLeft(1)
	styleBadgeRun   = lipgloss.NewStyle().Foreground(colRunning)
	styleBadgePerm  = lipgloss.NewStyle().Foreground(colPending)
	styleBadgeAsk   = lipgloss.NewStyle().Foreground(colWaiting)
	styleAgentCodex = lipgloss.NewStyle().Foreground(colCodex)
	styleHeader     = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleFaint      = lipgloss.NewStyle().Foreground(colMuted)
	stylePRDraft    = lipgloss.NewStyle().Foreground(colMuted)
	stylePROpen     = lipgloss.NewStyle().Foreground(colRunning)
	stylePRMerged   = lipgloss.NewStyle().Foreground(colWaiting)
)

// Model is the bubbletea Model for the sidebar.
type Model struct {
	tmuxClient       tmux.Client
	stateReader      state.Reader
	cfg              config.Config
	items            []ListItem
	winPaneNums      map[string][]int // windowID → pane numbers; updated by loadData
	cursor           int              // index into visibleItems()
	cursorWinID      string           // window ID the cursor is currently on (tracks user selection across data refreshes)
	currentWinID     string           // window ID of the pane running this process
	activeWinID      string           // last known active tmux window ID (updated on each loadData)
	filter           FilterMode
	width            int
	height           int // terminal height (from WindowSizeMsg)
	offset           int // scroll offset into visibleItems()
	err              error
	gitData          map[string]gitInfo // keyed by window ID
	focused          bool               // true when this pane has terminal focus
	inputMode        inputMode          // modal state: normal (commands) vs search (text)
	confirm          confirmAction      // pending y/N confirmation; confirmNone when idle
	confirmItem      ListItem           // window/session targeted by the pending confirmation
	message          string             // transient one-line status (e.g. graveyard path); cleared on next normal-mode key
	searchQuery      string             // search query text; only populated while inputMode == modeSearch
	promptCache      map[string]string  // agent:sessionID → initial prompt text (cached)
	cursorPrompt     string             // initial prompt for the window currently under cursor
	currentSessionID string             // tmux session ID of the pane running this process (e.g. "$3")
	pinnedPath       string             // path to pinned_sessions for write-back on `p` toggle
}

// New creates a new Model. currentSessionID and currentWinID identify this
// sidebar's own pane; they are determined once at startup and never change.
// pinnedPath is the destination of write-back when the user toggles a pin
// (`p` key); pass config.PinnedConfigPath() in production.
func New(tc tmux.Client, sr state.Reader, width int, currentSessionID, currentWinID string, cfg config.Config, pinnedPath string, initialFocused bool) *Model {
	return &Model{
		tmuxClient:       tc,
		stateReader:      sr,
		cfg:              cfg,
		width:            width,
		gitData:          map[string]gitInfo{},
		winPaneNums:      map[string][]int{},
		promptCache:      map[string]string{},
		focused:          initialFocused,
		currentSessionID: currentSessionID,
		currentWinID:     currentWinID,
		pinnedPath:       pinnedPath,
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

		// Split sessions into pinned (config order) and unpinned (tmux order).
		// Hidden sessions are dropped before the split so a session that is
		// both hidden and pinned never appears.
		var pinnedSIDs, unpinnedSIDs []string
		for _, sid := range sessionOrder {
			sname := sessionNames[sid]
			if m.cfg.IsHiddenSession(sname) {
				continue
			}
			if m.cfg.IsPinnedSession(sname) {
				pinnedSIDs = append(pinnedSIDs, sid)
			} else {
				unpinnedSIDs = append(unpinnedSIDs, sid)
			}
		}
		// Sort pinned sessions by their order in pinned_sessions, not by tmux
		// enumeration. Stable sort on the int key preserves enumeration order
		// for ties (shouldn't happen with unique names but cheap insurance).
		sortPinnedByConfigOrder(pinnedSIDs, sessionNames, &m.cfg)

		appendSession := func(items []ListItem, sid string) []ListItem {
			sname := sessionNames[sid]
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
			return items
		}

		var items []ListItem
		for _, sid := range pinnedSIDs {
			items = appendSession(items, sid)
		}
		// Divider only when both groups are non-empty — a lone group should
		// not be visually offset.
		if len(pinnedSIDs) > 0 && len(unpinnedSIDs) > 0 {
			items = append(items, ListItem{Kind: ItemDivider})
		}
		for _, sid := range unpinnedSIDs {
			items = appendSession(items, sid)
		}

		// Determine the currently active window for this sidebar's own session.
		// `window_active=1` is per-session: every session has exactly one current
		// window, regardless of attach state. Filtering by SessionID prevents
		// cross-session bleed-through where sidebars in different sessions all
		// follow whichever attached session happens to come first in the pane list.
		activeWinID := ""
		for _, p := range allPanes {
			if p.SessionID == m.currentSessionID && p.WindowActive {
				activeWinID = p.WindowID
				break
			}
		}

		return dataMsg{items: items, winPaneNums: winPanes, activeWinID: activeWinID}
	}
}

// sortPinnedByConfigOrder sorts session IDs in-place by the index of their
// session name in cfg.PinnedSessions. Sessions whose name is not pinned end
// up at the end (PinnedOrder returns -1; treat as +∞ for sorting).
func sortPinnedByConfigOrder(sids []string, names map[string]string, cfg *config.Config) {
	sort.SliceStable(sids, func(i, j int) bool {
		oi := cfg.PinnedOrder(names[sids[i]])
		oj := cfg.PinnedOrder(names[sids[j]])
		if oi < 0 {
			oi = int(^uint(0) >> 1)
		}
		if oj < 0 {
			oj = int(^uint(0) >> 1)
		}
		return oi < oj
	})
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

	// Reuse cached PR data when branch is unchanged or fetched recently (< 5 min).
	// gh pr view is an API call (1-2s) so we avoid calling it on every 10s tick.
	prCacheTTL := 5 * time.Minute
	if old.prFetchedAt.IsZero() == false && time.Since(old.prFetchedAt) < prCacheTTL && branch == old.branch {
		info.prState = old.prState
		info.prNumber = old.prNumber
		info.prFetchedAt = old.prFetchedAt
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
	info.prFetchedAt = time.Now()

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

	case promptMsg:
		// Cache only non-empty results: a transient empty (transcript not yet
		// written/flushed) must not pin "" forever and block the eventual real
		// prompt from ever displaying.
		if msg.prompt != "" {
			m.promptCache[msg.cacheKey] = msg.prompt
		}
		if key := m.cursorPromptKey(); key == msg.cacheKey {
			m.cursorPrompt = msg.prompt
		}
		return m, nil

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
		// If the active window changed, move the cursor to follow it.
		// This implements "window追従": switching tmux windows moves the sidebar cursor.
		if msg.activeWinID != "" && msg.activeWinID != m.activeWinID {
			m.activeWinID = msg.activeWinID
			m.cursorWinID = msg.activeWinID
		}
		// Re-locate cursor by the window ID it was on before the refresh.
		// This prevents the cursor from jumping to a different window when
		// items are added/removed and indices shift.
		m.relocateCursor()
		m.adjustScroll()
		return m, m.updateCursorPrompt()

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
		return m, m.updateCursorPrompt()

	case gitTickMsg:
		// Also refresh tmux data so activeWinID converges within 10s even when
		// SIGUSR1 from tmux hooks fails to deliver (missing/broken hook config,
		// /tmp permissions, etc.). Without this the active-window highlight can
		// stay stuck indefinitely after a window switch.
		return m, tea.Batch(m.loadData(), m.loadGitInfo(), gitTickCmd())

	case gitDataMsg:
		// Merge instead of overwrite: loadGitInfo only fetches visible windows,
		// so a full replace would discard PR data for filtered-out windows.
		for k, v := range msg.data {
			m.gitData[k] = v
		}
		return m, nil

	case switchWindowMsg:
		return m, func() tea.Msg {
			_ = m.tmuxClient.SwitchWindow(msg.sessionName, msg.windowIndex)
			return nil
		}

	case killResultMsg:
		switch {
		case msg.err != nil:
			m.message = "kill failed: " + msg.err.Error()
		case msg.gravePath != "":
			m.message = "saved: " + msg.gravePath
		default:
			m.message = "killed"
		}
		// Drop the killed session from pinned_sessions so the file stays in
		// sync with reality. Without this, a same-named session created later
		// would automatically be pinned again from the stale entry.
		if msg.err == nil && msg.killedSession != "" && m.cfg.IsPinnedSession(msg.killedSession) {
			updated := m.cfg.TogglePinned(msg.killedSession)
			if m.pinnedPath != "" {
				_ = config.WritePinnedSessions(m.pinnedPath, updated)
			}
		}
		return m, m.loadData()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.adjustScroll()

	case tea.FocusMsg:
		m.focused = true

	case tea.BlurMsg:
		m.focused = false
		m.searchQuery = ""
		m.inputMode = modeNormal
		m.confirm = confirmNone
		m.message = ""
		// Snap the cursor back to this sidebar's own session's active window.
		// Manual j/k positions are "preview while focused" — once the user
		// leaves (e.g., switches tmux sessions or selects another pane), any
		// stale cross-session position would otherwise persist until the
		// active window changes. Without this, after `switch-client` the
		// cursor stays on whatever window the user last hovered.
		if m.activeWinID != "" {
			m.cursorWinID = m.activeWinID
			m.relocateCursor()
		}
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if !m.focused {
		return m, nil
	}

	// Confirm sub-state takes precedence over both modes — until the user
	// answers y or n/Esc, every other key is intentionally ignored so a
	// stray j cannot move past a destructive prompt.
	if m.confirm != confirmNone {
		return m.handleConfirmKey(msg)
	}

	switch m.inputMode {
	case modeSearch:
		return m.handleSearchKey(msg)
	default:
		return m.handleNormalKey(msg)
	}
}

func (m *Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Any normal-mode key clears the transient status message — it is meant
	// to inform a single subsequent action, not linger forever.
	m.message = ""

	switch msg.Type {
	case tea.KeyEnter:
		return m, m.switchSelected()
	case tea.KeyUp:
		m.moveCursor(-1)
		return m, m.updateCursorPrompt()
	case tea.KeyDown:
		m.moveCursor(1)
		return m, m.updateCursorPrompt()
	case tea.KeyRunes:
		if len(msg.Runes) != 1 {
			return m, nil
		}
		switch msg.Runes[0] {
		case '/':
			m.inputMode = modeSearch
			m.searchQuery = ""
			return m, nil
		case 'j':
			m.moveCursor(1)
			return m, m.updateCursorPrompt()
		case 'k':
			m.moveCursor(-1)
			return m, m.updateCursorPrompt()
		case 'd':
			m.requestKillWindow()
			return m, nil
		case 'D':
			m.requestKillSession()
			return m, nil
		case 'p':
			return m, m.togglePin()
		}
	}
	return m, nil
}

func (m *Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.searchQuery = ""
		m.inputMode = modeNormal
		m.resetCursorToFirstWindow()
		return m, nil
	case tea.KeyEnter:
		return m, m.switchSelected()
	case tea.KeyBackspace:
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			m.resetCursorToFirstWindow()
		}
		return m, nil
	case tea.KeyUp:
		m.moveCursor(-1)
		return m, m.updateCursorPrompt()
	case tea.KeyDown:
		m.moveCursor(1)
		return m, m.updateCursorPrompt()
	case tea.KeyRunes:
		m.searchQuery += string(msg.Runes)
		m.resetCursorToFirstWindow()
		return m, nil
	}
	return m, nil
}

func (m *Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.confirm = confirmNone
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) != 1 {
			return m, nil
		}
		switch msg.Runes[0] {
		case 'y', 'Y':
			action := m.confirm
			item := m.confirmItem
			m.confirm = confirmNone
			switch action {
			case confirmKillWindow:
				return m, m.killWindowCmd(item)
			case confirmKillSession:
				return m, m.killSessionCmd(item)
			}
		case 'n', 'N':
			m.confirm = confirmNone
			return m, nil
		}
	}
	return m, nil
}

// togglePin flips the pin state of the session that owns the cursor's window
// and persists the new pinned_sessions file. Triggers a reload so the view
// re-sorts immediately. Cursor stays anchored to the same window via
// cursorWinID across the reload.
func (m *Model) togglePin() tea.Cmd {
	visible := m.visibleItems()
	if m.cursor >= len(visible) {
		return nil
	}
	item := visible[m.cursor]
	if item.Kind != ItemWindow || item.SessionName == "" {
		return nil
	}
	name := item.SessionName
	updated := m.cfg.TogglePinned(name)
	if m.pinnedPath != "" {
		if err := config.WritePinnedSessions(m.pinnedPath, updated); err != nil {
			m.message = "pin: " + err.Error()
			// Roll back the in-memory change so view stays in sync with disk.
			m.cfg.TogglePinned(name)
			return nil
		}
	}
	if m.cfg.IsPinnedSession(name) {
		m.message = "pinned: " + name
	} else {
		m.message = "unpinned: " + name
	}
	return m.loadData()
}

// requestKillWindow stages a confirmation prompt for the window under the cursor.
func (m *Model) requestKillWindow() {
	visible := m.visibleItems()
	if m.cursor >= len(visible) {
		return
	}
	item := visible[m.cursor]
	if item.Kind != ItemWindow || item.Window == nil {
		return
	}
	m.confirm = confirmKillWindow
	m.confirmItem = item
}

// requestKillSession stages a confirmation prompt for the session that owns
// the cursor's current window.
func (m *Model) requestKillSession() {
	visible := m.visibleItems()
	if m.cursor >= len(visible) {
		return
	}
	item := visible[m.cursor]
	if item.Kind != ItemWindow || item.SessionName == "" {
		return
	}
	m.confirm = confirmKillSession
	m.confirmItem = item
}

// killWindowCmd captures the target pane to the graveyard and then kills the
// window. The capture is best-effort: a failed capture must not block the kill.
func (m *Model) killWindowCmd(item ListItem) tea.Cmd {
	client := m.tmuxClient
	return func() tea.Msg {
		target := fmt.Sprintf("%s:%d", item.SessionName, item.Window.Index)
		label := fmt.Sprintf("%s_%d_%s", sanitizeLabel(item.SessionName), item.Window.Index, sanitizeLabel(item.Window.Name))
		gravePath, _ := captureToGraveyard(client, target, label)
		if err := client.KillWindow(item.SessionName, item.Window.Index); err != nil {
			return killResultMsg{err: err}
		}
		return killResultMsg{gravePath: gravePath}
	}
}

// killSessionCmd captures the active pane of the session and then kills it.
func (m *Model) killSessionCmd(item ListItem) tea.Cmd {
	client := m.tmuxClient
	return func() tea.Msg {
		target := item.SessionName
		label := fmt.Sprintf("session_%s", sanitizeLabel(item.SessionName))
		gravePath, _ := captureToGraveyard(client, target, label)
		if err := client.KillSession(item.SessionName); err != nil {
			return killResultMsg{err: err}
		}
		return killResultMsg{gravePath: gravePath, killedSession: item.SessionName}
	}
}

// graveyardDir returns the directory where pane snapshots are stored before a
// kill. Override via TMUX_SIDEBAR_GRAVEYARD_DIR (used by e2e and unit tests).
func graveyardDir() string {
	if d := os.Getenv("TMUX_SIDEBAR_GRAVEYARD_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "tmux-sidebar", "graveyard")
}

// captureToGraveyard captures the active pane of target via tmux capture-pane
// and writes it to a file under the graveyard dir. Returns the file path on
// success. Errors are returned but treated as non-fatal by the callers — the
// kill must still happen even if the snapshot failed.
func captureToGraveyard(client tmux.Client, target, label string) (string, error) {
	dir := graveyardDir()
	if dir == "" {
		return "", fmt.Errorf("graveyard dir unavailable")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	content, err := client.CapturePane(target)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s_%s.txt", time.Now().Format("20060102-150405"), label)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// sanitizeLabel replaces filesystem-hostile characters in s so it can be used
// as part of a graveyard file name.
func sanitizeLabel(s string) string {
	if s == "" {
		return "unnamed"
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', ' ', '\t', '\n', '\r':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// footerLines is the number of fixed lines below the item list
// (blank + key hints).
const footerLines = 2

// previewLines is the fixed number of text lines in the bottom preview area.
const previewLines = 7

// headerLines returns the number of fixed lines above the item list.
// Normal mode: title + hint = 2 lines.
// Search mode: title + query + separator = 3 lines.
func (m *Model) headerLines() int {
	if m.inputMode == modeSearch {
		return 3
	}
	return 2
}

// viewportHeight returns the number of item rows that fit on screen.
// Returns 0 when the terminal height is unknown or too small.
func (m *Model) viewportHeight() int {
	// 1 extra line for the preview separator
	total := m.headerLines() + footerLines + previewLines + 1
	if m.height <= total {
		return 0
	}
	return m.height - total
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
			if visible[next].Window != nil {
				m.cursorWinID = visible[next].Window.ID
			}
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
			if item.Window != nil {
				m.cursorWinID = item.Window.ID
			}
			m.offset = 0
			m.adjustScroll()
			return
		}
	}
	m.cursor = 0
	m.offset = 0
}

// relocateCursor finds the window that the cursor was previously on (by cursorWinID)
// and moves the cursor to its new index. Falls back to activeWinID (the user's
// current tmux window), then currentWinID (this sidebar's own window), then the
// first window.
//
// activeWinID must come before currentWinID in the chain: when the cursor was
// pointing at a window that has just disappeared (deleted, hidden-session
// filter, search filter), the user expects "follow tmux", which is activeWinID.
// currentWinID is this sidebar's own pane's window — using it first would pin
// the cursor to where the sidebar lives instead of where the user actually is.
func (m *Model) relocateCursor() {
	visible := m.visibleItems()
	firstWindow := -1
	for i, item := range visible {
		if item.Kind != ItemWindow || item.Window == nil {
			continue
		}
		if firstWindow < 0 {
			firstWindow = i
		}
		if m.cursorWinID != "" && item.Window.ID == m.cursorWinID {
			m.cursor = i
			return
		}
	}
	// cursorWinID not found — fall back to activeWinID first so the cursor
	// follows the user's current tmux window.
	if m.activeWinID != "" {
		for i, item := range visible {
			if item.Kind == ItemWindow && item.Window != nil && item.Window.ID == m.activeWinID {
				m.cursor = i
				m.cursorWinID = item.Window.ID
				return
			}
		}
	}
	// Then fall back to currentWinID (this sidebar's own pane's window).
	for i, item := range visible {
		if item.Kind == ItemWindow && item.Window != nil && item.Window.ID == m.currentWinID {
			m.cursor = i
			m.cursorWinID = item.Window.ID
			return
		}
	}
	// Last resort: first window
	if firstWindow >= 0 {
		m.cursor = firstWindow
		m.cursorWinID = visible[firstWindow].Window.ID
	} else {
		m.cursor = 0
		m.cursorWinID = ""
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

func (m *Model) cursorPaneState() *state.PaneState {
	visible := m.visibleItems()
	if m.cursor >= len(visible) {
		return nil
	}
	item := visible[m.cursor]
	if item.Kind != ItemWindow || item.PaneState == nil {
		return nil
	}
	return item.PaneState
}

func promptCacheKey(agent, sessionID string) string {
	return agent + ":" + sessionID
}

func (m *Model) cursorPromptKey() string {
	ps := m.cursorPaneState()
	if ps == nil || ps.SessionID == "" {
		return ""
	}
	return promptCacheKey(ps.Agent, ps.SessionID)
}

// updateCursorPrompt updates cursorPrompt based on the current cursor position.
// If the prompt is cached, it is used immediately. Otherwise, a background fetch is started.
func (m *Model) updateCursorPrompt() tea.Cmd {
	ps := m.cursorPaneState()
	if ps == nil || ps.SessionID == "" {
		m.cursorPrompt = ""
		return nil
	}
	cacheKey := promptCacheKey(ps.Agent, ps.SessionID)
	if cached, ok := m.promptCache[cacheKey]; ok && cached != "" {
		m.cursorPrompt = cached
		return nil
	}
	// Fetch asynchronously
	m.cursorPrompt = ""
	return func() tea.Msg {
		prompt, err := session.ExtractInitialPromptForAgent(ps.Agent, ps.SessionID)
		if err != nil {
			return promptMsg{cacheKey: cacheKey, prompt: ""}
		}
		return promptMsg{cacheKey: cacheKey, prompt: prompt}
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
		sb.WriteString(styleFaint.Render("○ Sessions") + "\n")
	}

	// Search prompt (no decorative separators; one faint rule under the query
	// when the user is actively searching)
	if m.inputMode == modeSearch {
		sb.WriteString(styleCursor.Render("> ") + m.searchQuery + "▏\n")
		sb.WriteString(styleFaint.Render(strings.Repeat("─", m.width)) + "\n")
	} else {
		sb.WriteString(styleFaint.Render("> /:search d:close D:kill p:pin") + "\n")
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
			label := "▾ " + item.SessionName
			if m.cfg.IsPinnedSession(item.SessionName) {
				label = "📌 " + item.SessionName
			}
			sb.WriteString(styleSession.Render(label) + "\n")
		case ItemDivider:
			sb.WriteString(styleFaint.Render(strings.Repeat("─", m.width)) + "\n")
		case ItemWindow:
			cursor := "  "
			if i == m.cursor {
				if m.focused {
					cursor = styleCursor.Render("▶ ")
				} else {
					cursor = styleFaint.Render("▶ ")
				}
			}
			// Build suffix: badge + PR (right-aligned on the row)
			suffix := ""
			if item.PaneState != nil {
				suffix += renderAgentTag(item.PaneState.Agent)
				if b := renderBadge(item.PaneState); b != "" {
					suffix += b
				}
			}
			if git, ok := m.gitData[item.Window.ID]; ok && git.prNumber != 0 {
				suffix += " " + renderPRBadge(git.prState, git.prNumber)
			}
			// Reserve at least one space between name and suffix.
			// Layout: [cursor(2)][space(1)][prefix][name][pad][suffix]
			prefix := fmt.Sprintf("%d: ", item.Window.Index)
			suffixW := lipgloss.Width(suffix)
			fixedW := 2 + 1 + runewidth.StringWidth(prefix) + suffixW
			minGap := 1
			if suffixW == 0 {
				minGap = 0
			}
			available := m.width - fixedW - minGap
			name := item.Window.Name
			if available <= 0 {
				name = ""
			} else if runewidth.StringWidth(name) > available {
				name = runewidth.Truncate(name, available, "")
			}
			label := prefix + name
			left := cursor + styleWindow.Render(label)
			pad := m.width - lipgloss.Width(left) - suffixW
			if pad < minGap {
				pad = minGap
			}
			row := left + strings.Repeat(" ", pad) + suffix
			if item.Window.ID == m.activeWinID {
				row = paintActiveRow(row, m.width)
			}
			sb.WriteString(row + "\n")
		}
	}

	// Scroll indicator
	if vp > 0 && endIdx < len(visible) {
		sb.WriteString(styleFaint.Render(fmt.Sprintf("  ↓ %d more", len(visible)-endIdx)) + "\n")
	} else {
		sb.WriteString("\n")
	}

	// Footer key hints (above preview area). The single line is shared with
	// confirm prompts and one-shot status messages — only one of these is
	// active at a time, so reusing the row keeps the viewport height stable.
	sb.WriteString(m.renderFooter() + "\n")

	// Preview area: separated by a line, showing initial prompt in normal color
	previewH := previewLines
	sb.WriteString(styleFaint.Render(strings.Repeat("─", m.width)) + "\n")
	if m.cursorPrompt != "" {
		lines := wrapText(m.cursorPrompt, m.width)
		maxLines := previewH - 1
		truncated := len(lines) > maxLines
		if truncated {
			lines = lines[:maxLines]
		}
		for i, line := range lines {
			if truncated && i == len(lines)-1 {
				// Last visible line: truncate and append "..."
				if runewidth.StringWidth(line) > m.width-3 {
					line = runewidth.Truncate(line, m.width-3, "")
				}
				line += "..."
			}
			sb.WriteString(line + "\n")
		}
		for i := len(lines); i < maxLines; i++ {
			sb.WriteString("\n")
		}
	} else {
		for i := 0; i < previewH-1; i++ {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// renderFooter returns the single footer line shown just above the preview
// separator. Priority: confirm prompt > status message > default key hints.
// Truncated to the sidebar width so it never wraps onto a second row (which
// would shift the viewport).
func (m *Model) renderFooter() string {
	switch {
	case m.confirm != confirmNone:
		return styleHeader.MaxWidth(m.width).Render(m.confirmPromptText())
	case m.message != "":
		return styleFaint.MaxWidth(m.width).Render(m.message)
	default:
		return styleFaint.MaxWidth(m.width).Render(footerHint(m.inputMode))
	}
}

func footerHint(mode inputMode) string {
	if mode == modeSearch {
		return "Esc:cancel ^C:quit"
	}
	return "/:search d:close D:kill p:pin ^C:quit"
}

// confirmPromptText builds the y/N question shown in the footer. The wording
// scales with the agent state per docs/spec.md: idle → simple, running →
// elapsed, permission/ask → strong warning. The preview area still shows the
// initial prompt for permission/ask, so the footer only needs to convey
// urgency.
func (m *Model) confirmPromptText() string {
	item := m.confirmItem
	if item.Window == nil {
		return ""
	}
	scope := "window"
	target := item.Window.Name
	if m.confirm == confirmKillSession {
		scope = "session"
		target = item.SessionName
	}
	if item.PaneState != nil {
		switch item.PaneState.Status {
		case state.StatusRunning:
			return fmt.Sprintf("running %s — kill %s '%s'? [y/N]", formatElapsed(item.PaneState.Elapsed), scope, target)
		case state.StatusPermission, state.StatusAsk:
			return fmt.Sprintf("agent waiting — kill %s '%s'? [y/N]", scope, target)
		}
	}
	return fmt.Sprintf("kill %s '%s'? [y/N]", scope, target)
}

func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// paintActiveRow wraps a pre-rendered row with the active-background color so
// the highlight extends to the right edge. lipgloss.Style.Render does not
// re-apply the outer background after inner "\x1b[0m" resets, so we inject
// the bg SGR after every reset and pad the line to the full width.
func paintActiveRow(row string, width int) string {
	bgOpen := activeBgSGR()
	if bgOpen == "" {
		return row
	}
	if w := lipgloss.Width(row); w < width {
		row += strings.Repeat(" ", width-w)
	}
	row = bgOpen + strings.ReplaceAll(row, "\x1b[0m", "\x1b[0m"+bgOpen) + "\x1b[0m"
	return row
}

// activeBgSGR returns the opening SGR for colActiveBg under the current
// terminal profile, or "" if no color is available.
func activeBgSGR() string {
	sample := lipgloss.NewStyle().Background(colActiveBg).Render(" ")
	if i := strings.IndexByte(sample, 'm'); i >= 0 {
		return sample[:i+1]
	}
	return ""
}

// renderAgentTag returns the per-agent identifier shown before the status
// badge: "[c]" for Claude (既存色 = 無着色 fallback) and "[x]" colored cyan
// for Codex. Unknown / missing agent falls back to the Claude-style tag so
// pre-existing state files keep rendering as before.
func renderAgentTag(agent string) string {
	switch agent {
	case state.AgentCodex:
		return styleAgentCodex.Render("[x]")
	default:
		return "[c]"
	}
}

func renderBadge(ps *state.PaneState) string {
	switch ps.Status {
	case state.StatusRunning:
		if ps.Elapsed < time.Minute {
			secs := int(ps.Elapsed.Seconds())
			return styleBadgeRun.Render(fmt.Sprintf("🔄%ds", secs))
		}
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

// wrapText wraps text to the given display width, breaking on whitespace and
// within long words at character boundaries using visual (column) width.
func wrapText(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		var line string
		lineW := 0
		for i, w := range words {
			ww := runewidth.StringWidth(w)
			if i == 0 {
				// First word: if it fits, just set it; otherwise break it.
				if ww <= width {
					line = w
					lineW = ww
				} else {
					broken := breakWord(w, width)
					lines = append(lines, broken[:len(broken)-1]...)
					line = broken[len(broken)-1]
					lineW = runewidth.StringWidth(line)
				}
				continue
			}
			if lineW+1+ww <= width {
				line += " " + w
				lineW += 1 + ww
			} else {
				lines = append(lines, line)
				if ww <= width {
					line = w
					lineW = ww
				} else {
					broken := breakWord(w, width)
					lines = append(lines, broken[:len(broken)-1]...)
					line = broken[len(broken)-1]
					lineW = runewidth.StringWidth(line)
				}
			}
		}
		lines = append(lines, line)
	}
	return lines
}

// breakWord splits a single word into lines that each fit within the given
// display width, breaking at character (rune) boundaries.
func breakWord(word string, width int) []string {
	var lines []string
	var cur strings.Builder
	curW := 0
	for _, r := range word {
		rw := runewidth.RuneWidth(r)
		if curW+rw > width && cur.Len() > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
			curW = 0
		}
		cur.WriteRune(r)
		curW += rw
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
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
