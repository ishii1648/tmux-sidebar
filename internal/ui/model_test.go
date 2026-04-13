package ui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ishii1648/tmux-sidebar/internal/state"
	"github.com/ishii1648/tmux-sidebar/internal/tmux"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeTmuxClient struct{}

func (f *fakeTmuxClient) ListSessions() ([]tmux.Session, error)    { return nil, nil }
func (f *fakeTmuxClient) ListWindows() ([]tmux.Window, error)      { return nil, nil }
func (f *fakeTmuxClient) ListPanes() ([]tmux.Pane, error)          { return nil, nil }
func (f *fakeTmuxClient) CurrentPane() (tmux.CurrentPane, error)   { return tmux.CurrentPane{}, nil }
func (f *fakeTmuxClient) SwitchWindow(_ string, _ int) error       { return nil }
func (f *fakeTmuxClient) PaneCurrentPath(_ string) (string, error) { return "", nil }
func (f *fakeTmuxClient) ListAll() ([]tmux.PaneInfo, error)        { return nil, nil }
func (f *fakeTmuxClient) KillSession(_ string) error               { return nil }
func (f *fakeTmuxClient) KillWindow(_ string, _ int) error         { return nil }

type fakeStateReader struct{ states map[int]state.PaneState }

func (f *fakeStateReader) Read() (map[int]state.PaneState, error) { return f.states, nil }

// ── helpers ──────────────────────────────────────────────────────────────────

// newTestModel builds a Model with pre-populated items for white-box unit tests.
// focused=true simulates the sidebar pane having terminal focus.
func newTestModel(items []ListItem, cursor int, focused bool) *Model {
	return &Model{
		tmuxClient:  &fakeTmuxClient{},
		stateReader: &fakeStateReader{states: map[int]state.PaneState{}},
		items:       items,
		cursor:      cursor,
		focused:     focused,
		width:       40,
	}
}

// sampleItems returns a list that contains two sessions with windows:
//
//	[0] ItemSession  "session-a"
//	[1] ItemWindow   @1 index=0 "main"
//	[2] ItemWindow   @2 index=1 "work"
//	[3] ItemSession  "session-b"
//	[4] ItemWindow   @3 index=0 "idle-win"
func sampleItems() []ListItem {
	return []ListItem{
		{Kind: ItemSession, SessionName: "session-a"},
		{Kind: ItemWindow, SessionName: "session-a", Window: &tmux.Window{ID: "@1", Index: 0, Name: "main"}},
		{Kind: ItemWindow, SessionName: "session-a", Window: &tmux.Window{ID: "@2", Index: 1, Name: "work"}},
		{Kind: ItemSession, SessionName: "session-b"},
		{Kind: ItemWindow, SessionName: "session-b", Window: &tmux.Window{ID: "@3", Index: 0, Name: "idle-win"}},
	}
}

// ansiRE matches common ANSI SGR escape sequences.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[mGKHF]`)

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// key returns a KeyMsg for a printable rune.
func key(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// ── cursor movement ──────────────────────────────────────────────────────────

func TestCursorMove_j_MovesToNextWindow(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true) // cursor at items[1]

	m.Update(key('j'))
	if m.cursor != 2 {
		t.Errorf("after j: cursor = %d, want 2", m.cursor)
	}
}

func TestCursorMove_j_SkipsSessionHeader(t *testing.T) {
	m := newTestModel(sampleItems(), 2, true) // cursor at items[2]

	m.Update(key('j'))
	// items[3] is a session header; must be skipped to land on items[4]
	if m.cursor != 4 {
		t.Errorf("after j from 2: cursor = %d, want 4 (session header skipped)", m.cursor)
	}
}

func TestCursorMove_j_StaysAtEnd(t *testing.T) {
	m := newTestModel(sampleItems(), 4, true) // cursor at last window

	m.Update(key('j'))
	if m.cursor != 4 {
		t.Errorf("j at end: cursor = %d, want 4 (no change)", m.cursor)
	}
}

func TestCursorMove_k_MovesToPrevWindow(t *testing.T) {
	m := newTestModel(sampleItems(), 2, true) // cursor at items[2]

	m.Update(key('k'))
	if m.cursor != 1 {
		t.Errorf("after k: cursor = %d, want 1", m.cursor)
	}
}

func TestCursorMove_k_SkipsSessionHeader(t *testing.T) {
	m := newTestModel(sampleItems(), 4, true) // cursor at items[4]

	m.Update(key('k'))
	// items[3] is a session header; must be skipped to land on items[2]
	if m.cursor != 2 {
		t.Errorf("after k from 4: cursor = %d, want 2 (session header skipped)", m.cursor)
	}
}

func TestCursorMove_k_StaysAtStart(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true) // cursor at first window

	m.Update(key('k'))
	if m.cursor != 1 {
		t.Errorf("k at start: cursor = %d, want 1 (no change)", m.cursor)
	}
}

func TestCursorMove_DownKey(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("down arrow: cursor = %d, want 2", m.cursor)
	}
}

func TestCursorMove_UpKey(t *testing.T) {
	m := newTestModel(sampleItems(), 2, true)

	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("up arrow: cursor = %d, want 1", m.cursor)
	}
}

// ── unfocused (blur) ─────────────────────────────────────────────────────────
// Input is always processed regardless of focus state; tmux only routes keys to
// the active pane, so if input arrives the pane IS active.

func TestBlur_jMovesCursor(t *testing.T) {
	m := newTestModel(sampleItems(), 1, false)

	_, cmd := m.Update(key('j'))
	if m.cursor != 2 {
		t.Errorf("cursor should move on j even when unfocused: got %d, want 2", m.cursor)
	}
	if cmd != nil {
		t.Errorf("expected nil Cmd on j, got non-nil")
	}
}

func TestBlur_EnterSwitches(t *testing.T) {
	m := newTestModel(sampleItems(), 1, false)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Errorf("expected non-nil Cmd on Enter even when unfocused")
	}
}

// ── focus / blur messages ────────────────────────────────────────────────────

func TestFocusMsg_SetsFocusedTrue(t *testing.T) {
	m := newTestModel(sampleItems(), 1, false)

	m.Update(tea.FocusMsg{})
	if !m.focused {
		t.Errorf("after FocusMsg: focused = false, want true")
	}
}

func TestBlurMsg_SetsFocusedFalse(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	m.Update(tea.BlurMsg{})
	if m.focused {
		t.Errorf("after BlurMsg: focused = true, want false")
	}
}

// ── Enter key ────────────────────────────────────────────────────────────────

func TestEnter_ReturnsSwitchWindowMsg(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true) // cursor at @1 index=0 "main"

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil Cmd from Enter on window item")
	}

	msg := cmd()
	swMsg, ok := msg.(switchWindowMsg)
	if !ok {
		t.Fatalf("Cmd returned %T, want switchWindowMsg", msg)
	}
	if swMsg.sessionName != "session-a" {
		t.Errorf("sessionName = %q, want %q", swMsg.sessionName, "session-a")
	}
	if swMsg.windowIndex != 0 {
		t.Errorf("windowIndex = %d, want 0", swMsg.windowIndex)
	}
}

func TestEnter_DifferentWindow(t *testing.T) {
	m := newTestModel(sampleItems(), 4, true) // cursor at @3 index=0 "idle-win"

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil Cmd")
	}

	msg := cmd()
	swMsg, ok := msg.(switchWindowMsg)
	if !ok {
		t.Fatalf("Cmd returned %T, want switchWindowMsg", msg)
	}
	if swMsg.sessionName != "session-b" {
		t.Errorf("sessionName = %q, want %q", swMsg.sessionName, "session-b")
	}
	if swMsg.windowIndex != 0 {
		t.Errorf("windowIndex = %d, want 0", swMsg.windowIndex)
	}
}

// ── View ─────────────────────────────────────────────────────────────────────

func TestView_FocusedShowsCursorAndHeader(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	view := stripANSI(m.View())
	if !strings.Contains(view, "▶") {
		t.Errorf("focused View should contain '▶' cursor:\n%s", view)
	}
	if !strings.Contains(view, "●") {
		t.Errorf("focused View should contain '●' in header:\n%s", view)
	}
}

func TestView_UnfocusedHidesCursorAndChangesHeader(t *testing.T) {
	m := newTestModel(sampleItems(), 1, false)

	view := stripANSI(m.View())
	// Unfocused: cursor indicator ▶ is still rendered (but styled faint) to preserve
	// the user's position, so we check only that the header uses ○ (not ●).
	if !strings.Contains(view, "○") {
		t.Errorf("unfocused View should contain '○' in header:\n%s", view)
	}
	if strings.Contains(view, "●") {
		t.Errorf("unfocused View should NOT contain '●' in header:\n%s", view)
	}
}

func TestView_FocusedShowsFooter(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	view := stripANSI(m.View())
	if !strings.Contains(view, "Tab:filter") {
		t.Errorf("focused View should show footer hints:\n%s", view)
	}
}

func TestView_FooterAlwaysVisible(t *testing.T) {
	// Footer key hints are always shown (focused and unfocused) for discoverability.
	for _, focused := range []bool{true, false} {
		m := newTestModel(sampleItems(), 1, focused)
		view := stripANSI(m.View())
		if !strings.Contains(view, "Tab:filter") {
			t.Errorf("View (focused=%v) should always show footer hints:\n%s", focused, view)
		}
	}
}

func TestView_ContainsSessionNames(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	view := stripANSI(m.View())
	for _, want := range []string{"session-a", "session-b"} {
		if !strings.Contains(view, want) {
			t.Errorf("View should contain %q:\n%s", want, view)
		}
	}
}

func TestView_ContainsWindowNames(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	view := stripANSI(m.View())
	for _, want := range []string{"main", "work", "idle-win"} {
		if !strings.Contains(view, want) {
			t.Errorf("View should contain window name %q:\n%s", want, view)
		}
	}
}

func TestView_ContainsStateBadges(t *testing.T) {
	elapsed := 3 * time.Minute
	runState := state.PaneState{Status: state.StatusRunning, Elapsed: elapsed}
	idleState := state.PaneState{Status: state.StatusIdle}
	permState := state.PaneState{Status: state.StatusPermission}
	askState := state.PaneState{Status: state.StatusAsk}

	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "run"}, PaneState: &runState},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@2", Index: 1, Name: "idl"}, PaneState: &idleState},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@3", Index: 2, Name: "perm"}, PaneState: &permState},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@4", Index: 3, Name: "ask"}, PaneState: &askState},
	}
	m := newTestModel(items, 1, true)

	view := stripANSI(m.View())
	// Running badge shows elapsed time (emoji + minutes); idle is hidden; permission/ask show 💬.
	if !strings.Contains(view, "3m") {
		t.Errorf("View should contain running badge with minutes (3m):\n%s", view)
	}
	// idle is intentionally hidden
	if strings.Contains(view, "idle") {
		t.Errorf("View should NOT show idle badge:\n%s", view)
	}
	// permission and ask both use the 💬 badge
	permCount := strings.Count(view, "💬")
	if permCount < 2 {
		t.Errorf("View should contain at least 2 💬 badges (permission + ask), got %d:\n%s", permCount, view)
	}
}

func TestView_RunningBadgeSubMinute(t *testing.T) {
	// 45秒経過: 秒数表示になること
	subMinState := state.PaneState{Status: state.StatusRunning, Elapsed: 45 * time.Second}
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "run"}, PaneState: &subMinState},
	}
	m := newTestModel(items, 1, true)

	view := stripANSI(m.View())
	if !strings.Contains(view, "45s") {
		t.Errorf("View should contain running badge with seconds (45s) for sub-minute elapsed:\n%s", view)
	}
	if strings.Contains(view, "0m") {
		t.Errorf("View should NOT show 0m for sub-minute elapsed:\n%s", view)
	}
}

func TestView_NoBadgeWhenNoPaneState(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "plain"}, PaneState: nil},
	}
	m := newTestModel(items, 1, true)

	view := stripANSI(m.View())
	for _, badge := range []string{"🔄", "💬"} {
		if strings.Contains(view, badge) {
			t.Errorf("View should NOT contain badge %q when PaneState is nil:\n%s", badge, view)
		}
	}
}

// ── filter / visibleItems ────────────────────────────────────────────────────

// sampleItemsWithStates returns items that have mixed PaneState values for
// filter tests:
//
//	[0] ItemSession  "session-a"
//	[1] ItemWindow   @1  "run-win"   (StatusRunning)
//	[2] ItemWindow   @2  "plain-win" (no state)
//	[3] ItemSession  "session-b"
//	[4] ItemWindow   @3  "wait-win"  (StatusPermission)
func sampleItemsWithStates() []ListItem {
	running := state.PaneState{Status: state.StatusRunning}
	perm := state.PaneState{Status: state.StatusPermission}
	return []ListItem{
		{Kind: ItemSession, SessionName: "session-a"},
		{Kind: ItemWindow, SessionName: "session-a", Window: &tmux.Window{ID: "@1", Index: 0, Name: "run-win"}, PaneState: &running},
		{Kind: ItemWindow, SessionName: "session-a", Window: &tmux.Window{ID: "@2", Index: 1, Name: "plain-win"}},
		{Kind: ItemSession, SessionName: "session-b"},
		{Kind: ItemWindow, SessionName: "session-b", Window: &tmux.Window{ID: "@3", Index: 0, Name: "wait-win"}, PaneState: &perm},
	}
}

func TestFilterAll_ShowsAllItems(t *testing.T) {
	m := newTestModel(sampleItemsWithStates(), 1, true)
	// FilterAll is the zero value — default.
	visible := m.visibleItems()
	if len(visible) != 5 {
		t.Errorf("FilterAll: len(visibleItems) = %d, want 5", len(visible))
	}
}

func TestFilterWaiting_ShowsOnlyPermissionAndAsk(t *testing.T) {
	ask := state.PaneState{Status: state.StatusAsk}
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "ask-win"}, PaneState: &ask},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@2", Index: 1, Name: "idle-win"},
			PaneState: &state.PaneState{Status: state.StatusIdle}},
	}
	m := newTestModel(items, 1, true)
	m.filter = FilterWaiting
	visible := m.visibleItems()
	if len(visible) != 2 {
		t.Errorf("FilterWaiting: len = %d, want 2", len(visible))
	}
	if visible[1].Window.Name != "ask-win" {
		t.Errorf("FilterWaiting: expected ask-win, got %s", visible[1].Window.Name)
	}
}

func TestTabKey_CyclesFilterForward(t *testing.T) {
	m := newTestModel(sampleItemsWithStates(), 1, true)
	// FilterAll → FilterWaiting
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.filter != FilterWaiting {
		t.Errorf("after Tab: filter = %v, want FilterWaiting", m.filter)
	}
	// FilterWaiting → FilterAll
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.filter != FilterAll {
		t.Errorf("after Tab×2: filter = %v, want FilterAll", m.filter)
	}
}

func TestShiftTabKey_CyclesFilterBackward(t *testing.T) {
	m := newTestModel(sampleItemsWithStates(), 1, true)
	// FilterAll → FilterWaiting
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.filter != FilterWaiting {
		t.Errorf("after Shift+Tab: filter = %v, want FilterWaiting", m.filter)
	}
	// FilterWaiting → FilterAll
	m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.filter != FilterAll {
		t.Errorf("after Shift+Tab×2: filter = %v, want FilterAll", m.filter)
	}
}

func TestFilterChange_ResetsCursorToFirstWindow(t *testing.T) {
	m := newTestModel(sampleItemsWithStates(), 4, true) // cursor at wait-win
	m.Update(tea.KeyMsg{Type: tea.KeyTab})              // → FilterWaiting
	// FilterWaiting shows: [0] session-b, [1] wait-win
	// cursor must be on the first window = index 1
	if m.cursor != 1 {
		t.Errorf("cursor after filter change = %d, want 1", m.cursor)
	}
}

func TestFilterChange_UnfocusedTabChangesFilter(t *testing.T) {
	m := newTestModel(sampleItemsWithStates(), 1, false)
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.filter != FilterWaiting {
		t.Errorf("Tab when unfocused should change filter: got %v, want %v", m.filter, FilterWaiting)
	}
}

func TestView_ShowsFilterTabs(t *testing.T) {
	m := newTestModel(sampleItemsWithStates(), 1, true)
	view := stripANSI(m.View())
	for _, label := range []string{"[All]", "[Waiting]"} {
		if !strings.Contains(view, label) {
			t.Errorf("View should contain filter tab %q:\n%s", label, view)
		}
	}
	if strings.Contains(view, "[Running]") {
		t.Errorf("View should NOT contain [Running] tab:\n%s", view)
	}
}

func TestView_RunningBadgeShowsMinutes(t *testing.T) {
	elapsed := 5 * time.Minute
	runState := state.PaneState{Status: state.StatusRunning, Elapsed: elapsed}
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "w"}, PaneState: &runState},
	}
	m := newTestModel(items, 1, true)

	view := stripANSI(m.View())
	// Badge format: 🔄<N>m for whole minutes.
	want := "5m"
	if !strings.Contains(view, want) {
		t.Errorf("View should contain %q:\n%s", want, view)
	}
}

// ── ctrl+c ───────────────────────────────────────────────────────────────────

func TestCtrlC_QuitsWhenFocused(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c when focused should return a Quit Cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("cmd() returned %T, want tea.QuitMsg", msg)
	}
}

func TestCtrlC_QuitsWhenUnfocused(t *testing.T) {
	m := newTestModel(sampleItems(), 1, false)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c when unfocused should return a Quit Cmd")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("cmd() returned %T, want tea.QuitMsg", msg)
	}
}

// ── PR badge ─────────────────────────────────────────────────────────────────

func TestView_PRBadgeShownInline(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "feat"}},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@2", Index: 1, Name: "other"}},
	}
	m := newTestModel(items, 1, true)
	m.gitData = map[string]gitInfo{
		"@1": {branch: "feat", prState: "open", prNumber: 42},
	}

	view := stripANSI(m.View())
	if !strings.Contains(view, "#42") {
		t.Errorf("View should contain #42 PR badge:\n%s", view)
	}
	// "@2" has no PR — must not show any stray badge
	if strings.Contains(view, "#0") {
		t.Errorf("View should NOT contain #0 badge:\n%s", view)
	}
}

func TestView_PRBadgeNotShownWhenNoPR(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "w"}},
	}
	m := newTestModel(items, 1, true)
	// gitData has an entry but prNumber == 0
	m.gitData = map[string]gitInfo{
		"@1": {branch: "main", prNumber: 0},
	}

	view := stripANSI(m.View())
	if strings.Contains(view, "#") {
		t.Errorf("View should NOT contain # badge when prNumber is 0:\n%s", view)
	}
}

func TestView_NoPRBadgeWhenGitDataAbsent(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "w"}},
	}
	m := newTestModel(items, 1, true)
	// gitData is nil (zero value from newTestModel)

	view := stripANSI(m.View())
	if strings.Contains(view, "#") {
		t.Errorf("View should NOT contain # badge when gitData is empty:\n%s", view)
	}
}

// ── scroll ──────────────────────────────────────────────────────────────────

// manyItems builds a list with N windows under a single session.
func manyItems(n int) []ListItem {
	items := []ListItem{{Kind: ItemSession, SessionName: "s"}}
	for i := 0; i < n; i++ {
		items = append(items, ListItem{
			Kind:        ItemWindow,
			SessionName: "s",
			Window:      &tmux.Window{ID: fmt.Sprintf("@%d", i+1), Index: i, Name: fmt.Sprintf("win-%d", i)},
		})
	}
	return items
}

func TestScroll_ViewportLimitsRenderedRows(t *testing.T) {
	items := manyItems(20) // 1 session header + 20 windows = 21 rows
	m := newTestModel(items, 1, true)
	m.height = 10 // header(3) + viewport(5) + footer(2) = 10

	view := stripANSI(m.View())
	// Only first 5 items (session header + win-0..win-3) should render
	if !strings.Contains(view, "win-0") {
		t.Errorf("should contain win-0:\n%s", view)
	}
	// win-10 should NOT be visible
	if strings.Contains(view, "win-10") {
		t.Errorf("win-10 should be scrolled out:\n%s", view)
	}
	// "more" indicator should appear
	if !strings.Contains(view, "more") {
		t.Errorf("should show scroll indicator:\n%s", view)
	}
}

func TestScroll_CursorDownScrollsView(t *testing.T) {
	items := manyItems(20)
	m := newTestModel(items, 1, true)
	m.height = 10 // viewport = 5

	// Move cursor down many times to trigger scroll
	for i := 0; i < 10; i++ {
		m.Update(key('j'))
	}
	// Cursor should be past the initial viewport
	if m.cursor <= 5 {
		t.Errorf("cursor should have moved past initial viewport, got %d", m.cursor)
	}
	// Offset should have advanced
	if m.offset == 0 {
		t.Errorf("offset should have advanced from 0")
	}
}

func TestScroll_CursorUpScrollsBack(t *testing.T) {
	items := manyItems(20)
	m := newTestModel(items, 1, true)
	m.height = 10

	// Scroll down first
	for i := 0; i < 10; i++ {
		m.Update(key('j'))
	}
	savedOffset := m.offset

	// Scroll back up
	for i := 0; i < 10; i++ {
		m.Update(key('k'))
	}
	if m.offset >= savedOffset {
		t.Errorf("offset should decrease when scrolling up, got %d (was %d)", m.offset, savedOffset)
	}
}

func TestScroll_NoHeightNoRestriction(t *testing.T) {
	items := manyItems(20)
	m := newTestModel(items, 1, true)
	// height = 0 (default) — no scroll restriction
	view := stripANSI(m.View())
	if !strings.Contains(view, "win-19") {
		t.Errorf("with no height, all items should render:\n%s", view)
	}
}

// ── search / text filter (always-on) ────────────────────────────────────────

func TestSearch_TypingFiltersItems(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	// Just type — search is always on
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})

	visible := m.visibleItems()
	windowCount := 0
	for _, item := range visible {
		if item.Kind == ItemWindow {
			windowCount++
			if item.Window.Name != "work" {
				t.Errorf("expected only 'work' window, got %q", item.Window.Name)
			}
		}
	}
	if windowCount != 1 {
		t.Errorf("expected 1 matching window, got %d", windowCount)
	}
}

func TestSearch_EscClearsSearch(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})

	if m.searchQuery != "" {
		t.Errorf("Esc should clear search query, got %q", m.searchQuery)
	}
	if len(m.visibleItems()) != len(sampleItems()) {
		t.Errorf("after Esc, all items should be visible")
	}
}

func TestSearch_BackspaceDeletesChar(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	if m.searchQuery != "x" {
		t.Errorf("after backspace: query = %q, want 'x'", m.searchQuery)
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'W'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})

	visible := m.visibleItems()
	windowCount := 0
	for _, item := range visible {
		if item.Kind == ItemWindow {
			windowCount++
		}
	}
	// "WO" should match "work" (case-insensitive)
	if windowCount != 1 {
		t.Errorf("case-insensitive search: expected 1 window, got %d", windowCount)
	}
}

func TestSearch_MatchesSessionName(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	for _, r := range "session-b" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	visible := m.visibleItems()
	windowCount := 0
	for _, item := range visible {
		if item.Kind == ItemWindow {
			windowCount++
			if item.SessionName != "session-b" {
				t.Errorf("expected session-b windows, got session %q", item.SessionName)
			}
		}
	}
	if windowCount != 1 {
		t.Errorf("expected 1 window in session-b, got %d", windowCount)
	}
}

func TestSearch_JKNavigateWhenEmpty(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	// j/k should navigate when search query is empty
	m.Update(key('j'))
	if m.cursor != 2 {
		t.Errorf("j with empty query should navigate: cursor = %d, want 2", m.cursor)
	}
	m.Update(key('k'))
	if m.cursor != 1 {
		t.Errorf("k with empty query should navigate: cursor = %d, want 1", m.cursor)
	}
}

func TestSearch_JKTypeWhenNonEmpty(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)

	// Type something first, then j should be part of query
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m.Update(key('j'))
	if m.searchQuery != "aj" {
		t.Errorf("j with non-empty query should type: query = %q, want 'aj'", m.searchQuery)
	}
}

func TestView_SearchPromptAlwaysVisible(t *testing.T) {
	for _, focused := range []bool{true, false} {
		m := newTestModel(sampleItems(), 1, focused)
		view := stripANSI(m.View())
		if !strings.Contains(view, "> ") {
			t.Errorf("View (focused=%v) should show search prompt '> ':\n%s", focused, view)
		}
	}
}

func TestView_SearchPromptShowsQuery(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.searchQuery = "test"

	view := stripANSI(m.View())
	if !strings.Contains(view, "> test") {
		t.Errorf("View should show search query in prompt:\n%s", view)
	}
}

func TestView_FooterShowsEscHint(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	view := stripANSI(m.View())
	if !strings.Contains(view, "Esc:clear") {
		t.Errorf("footer should contain Esc:clear hint:\n%s", view)
	}
}
