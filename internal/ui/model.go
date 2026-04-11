// Package ui provides the bubbletea TUI model for tmux-sidebar.
package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ishii1648/tmux-sidebar/internal/state"
	"github.com/ishii1648/tmux-sidebar/internal/tmux"
)

// Mode describes whether the sidebar is actively controlled by the user.
type Mode int

const (
	// ModePassive renders the list but ignores keyboard input.
	ModePassive Mode = iota
	// ModeInteractive accepts j/k/Enter/q input.
	ModeInteractive
)

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

// tickMsg is sent by the polling ticker.
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

// Styles used for rendering.
var (
	styleCursor    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	styleSession   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	styleWindow    = lipgloss.NewStyle().PaddingLeft(2)
	styleBadgeRun  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleBadgeIdle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleBadgePerm = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleBadgeAsk  = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleHeader    = lipgloss.NewStyle().Bold(true).Underline(true)
)

// Model is the bubbletea Model for the sidebar.
type Model struct {
	tmuxClient   tmux.Client
	stateReader  state.Reader
	items        []ListItem
	cursor       int    // index into items (window rows only navigable)
	currentWinID string // window ID of the pane running this process
	mode         Mode
	width        int
	err          error
}

// New creates a new Model.
func New(tc tmux.Client, sr state.Reader, width int) *Model {
	return &Model{
		tmuxClient:  tc,
		stateReader: sr,
		width:       width,
		mode:        ModeInteractive,
	}
}

// Init starts the first data load and polling ticker.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadData(),
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
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
		cur, _ := m.tmuxClient.CurrentPane()

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

		return dataMsg{items: items, currentWinID: cur.WindowID}
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
		m.currentWinID = msg.currentWinID
		m.err = nil
		// Clamp cursor
		maxCursor := m.maxWindowIndex()
		if m.cursor > maxCursor {
			m.cursor = maxCursor
		}
		return m, nil

	case switchWindowMsg:
		return m, func() tea.Msg {
			_ = m.tmuxClient.SwitchWindow(msg.sessionName, msg.windowIndex)
			return nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// 'i' toggles between interactive and passive mode.
	if msg.String() == "i" {
		if m.mode == ModeInteractive {
			m.mode = ModePassive
		} else {
			m.mode = ModeInteractive
		}
		return m, nil
	}

	if m.mode == ModePassive {
		return m, nil
	}

	switch msg.String() {
	case "j", "down":
		m.moveCursor(1)
	case "k", "up":
		m.moveCursor(-1)
	case "enter":
		return m, m.switchSelected()
	case "q", "esc":
		m.mode = ModePassive
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// moveCursor advances the cursor by delta, skipping session-header rows.
func (m *Model) moveCursor(delta int) {
	next := m.cursor + delta
	for {
		if next < 0 || next >= len(m.items) {
			return
		}
		if m.items[next].Kind == ItemWindow {
			m.cursor = next
			return
		}
		next += delta
	}
}

// maxWindowIndex returns the index of the last window item.
func (m *Model) maxWindowIndex() int {
	for i := len(m.items) - 1; i >= 0; i-- {
		if m.items[i].Kind == ItemWindow {
			return i
		}
	}
	return 0
}

// switchSelected builds a Cmd that switches to the currently selected window.
func (m *Model) switchSelected() tea.Cmd {
	if m.cursor >= len(m.items) {
		return nil
	}
	item := m.items[m.cursor]
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
	sb.WriteString(styleHeader.Render("Sessions") + "\n")
	sb.WriteString(strings.Repeat("─", m.width) + "\n")

	for i, item := range m.items {
		switch item.Kind {
		case ItemSession:
			sb.WriteString(styleSession.Render(item.SessionName) + "\n")
		case ItemWindow:
			cursor := "  "
			if i == m.cursor && m.mode == ModeInteractive {
				cursor = styleCursor.Render("▶ ")
			}
			// Highlight current window
			label := fmt.Sprintf("%d: %s", item.Window.Index, item.Window.Name)
			if item.Window.ID == m.currentWinID {
				label = lipgloss.NewStyle().Underline(true).Render(label)
			}
			badge := ""
			if item.PaneState != nil {
				badge = " " + renderBadge(item.PaneState)
			}
			sb.WriteString(cursor + styleWindow.Render(label+badge) + "\n")
		}
	}

	modeStyle := lipgloss.NewStyle().Faint(true)
	if m.mode == ModePassive {
		sb.WriteString("\n" + modeStyle.Render("[passive]  i: activate") + "\n")
	} else {
		sb.WriteString("\n" + modeStyle.Render("[interactive]  i: passive") + "\n")
	}
	return sb.String()
}

func renderBadge(ps *state.PaneState) string {
	switch ps.Status {
	case state.StatusRunning:
		mins := int(ps.Elapsed.Minutes())
		text := fmt.Sprintf("[running %dm]", mins)
		return styleBadgeRun.Render(text)
	case state.StatusIdle:
		return styleBadgeIdle.Render("[idle]")
	case state.StatusPermission:
		return styleBadgePerm.Render("[permission]")
	case state.StatusAsk:
		return styleBadgeAsk.Render("[ask]")
	default:
		return ""
	}
}
