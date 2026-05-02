package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ishii1648/tmux-sidebar/internal/config"
	"github.com/ishii1648/tmux-sidebar/internal/state"
	"github.com/ishii1648/tmux-sidebar/internal/tmux"
	"github.com/muesli/termenv"
)

// Force a color profile so AdaptiveColor styles always emit SGR escape
// sequences under `go test`, which otherwise strips color because there's no
// TTY attached.
func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeTmuxClient struct {
	panes []tmux.PaneInfo

	killedSessions []string
	killedWindows  []string // formatted as "session:index"
	captureContent string   // returned from CapturePane
	captureTargets []string
}

func (f *fakeTmuxClient) ListSessions() ([]tmux.Session, error)    { return nil, nil }
func (f *fakeTmuxClient) ListWindows() ([]tmux.Window, error)      { return nil, nil }
func (f *fakeTmuxClient) ListPanes() ([]tmux.Pane, error)          { return nil, nil }
func (f *fakeTmuxClient) CurrentPane() (tmux.CurrentPane, error)   { return tmux.CurrentPane{}, nil }
func (f *fakeTmuxClient) SwitchWindow(_ string, _ int) error       { return nil }
func (f *fakeTmuxClient) PaneCurrentPath(_ string) (string, error) { return "", nil }
func (f *fakeTmuxClient) ListAll() ([]tmux.PaneInfo, error)        { return f.panes, nil }
func (f *fakeTmuxClient) KillSession(name string) error {
	f.killedSessions = append(f.killedSessions, name)
	return nil
}
func (f *fakeTmuxClient) KillWindow(name string, index int) error {
	f.killedWindows = append(f.killedWindows, fmt.Sprintf("%s:%d", name, index))
	return nil
}
func (f *fakeTmuxClient) CapturePane(target string) (string, error) {
	f.captureTargets = append(f.captureTargets, target)
	return f.captureContent, nil
}

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
		gitData:     map[string]gitInfo{},
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
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('j'))
	if m.cursor != 2 {
		t.Errorf("after j: cursor = %d, want 2", m.cursor)
	}
}

func TestCursorMove_j_SkipsSessionHeader(t *testing.T) {
	m := newTestModel(sampleItems(), 2, true)
	m.Update(key('j'))
	if m.cursor != 4 {
		t.Errorf("after j from 2: cursor = %d, want 4 (session header skipped)", m.cursor)
	}
}

func TestCursorMove_j_StaysAtEnd(t *testing.T) {
	m := newTestModel(sampleItems(), 4, true)
	m.Update(key('j'))
	if m.cursor != 4 {
		t.Errorf("j at end: cursor = %d, want 4 (no change)", m.cursor)
	}
}

func TestCursorMove_k_MovesToPrevWindow(t *testing.T) {
	m := newTestModel(sampleItems(), 2, true)
	m.Update(key('k'))
	if m.cursor != 1 {
		t.Errorf("after k: cursor = %d, want 1", m.cursor)
	}
}

func TestCursorMove_k_SkipsSessionHeader(t *testing.T) {
	m := newTestModel(sampleItems(), 4, true)
	m.Update(key('k'))
	if m.cursor != 2 {
		t.Errorf("after k from 4: cursor = %d, want 2 (session header skipped)", m.cursor)
	}
}

func TestCursorMove_k_StaysAtStart(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
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

func TestBlur_jIgnored(t *testing.T) {
	m := newTestModel(sampleItems(), 1, false)
	_, cmd := m.Update(key('j'))
	if m.cursor != 1 {
		t.Errorf("cursor moved when unfocused: got %d, want 1", m.cursor)
	}
	if cmd != nil {
		t.Errorf("expected nil Cmd when unfocused, got non-nil")
	}
}

func TestBlur_EnterIgnored(t *testing.T) {
	m := newTestModel(sampleItems(), 1, false)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cursor != 1 {
		t.Errorf("cursor moved on Enter when unfocused: got %d, want 1", m.cursor)
	}
	if cmd != nil {
		t.Errorf("expected nil Cmd on Enter when unfocused, got non-nil")
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

func TestBlurMsg_ClearsSearchQuery(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('/'))
	m.Update(key('a'))
	if m.searchQuery == "" {
		t.Fatal("precondition: searchQuery should not be empty after typing")
	}
	m.Update(tea.BlurMsg{})
	if m.searchQuery != "" {
		t.Errorf("after BlurMsg: searchQuery = %q, want empty", m.searchQuery)
	}
	if m.inputMode != modeNormal {
		t.Errorf("after BlurMsg: inputMode should reset to normal")
	}
}

func TestBlurMsg_SnapsCursorBackToActiveWindow(t *testing.T) {
	// Regression: when the user navigates the cursor with j/k to a window
	// in another session ("preview while focused") and then crosses tmux
	// sessions via switch-client, the dataMsg handler skips the cursor
	// update because the sidebar's own session's active window did not
	// change. The manual cross-session position then persists, so when
	// the user comes back to this session the cursor is still pointing
	// at the previously-hovered window.
	//
	// BlurMsg fires when focus leaves the sidebar (which always happens
	// on session crossing) — that's the right moment to discard any
	// stale "preview" position and re-anchor to the active window.
	m := newTestModel(sampleItems(), 1, true)
	m.activeWinID = "@1"  // own session's current window
	m.cursorWinID = "@99" // user manually navigated to some other window
	m.cursor = 4          // index pointing somewhere else

	m.Update(tea.BlurMsg{})

	if m.cursorWinID != "@1" {
		t.Errorf("after BlurMsg: cursorWinID = %q, want %q (snap back to active)", m.cursorWinID, "@1")
	}
	// cursor index must reflect the snapped-back position so the next
	// View() draws ▶ on the active row.
	if m.cursor != 1 {
		t.Errorf("after BlurMsg: cursor = %d, want 1 (index of @1 in sampleItems)", m.cursor)
	}
}

func TestBlurMsg_NoOpWhenActiveWinIDUnknown(t *testing.T) {
	// If activeWinID has not been set yet (very first dataMsg hasn't
	// arrived), BlurMsg must not stomp cursorWinID with "" — the cursor
	// would lose its anchor and relocateCursor would fall through to
	// the first-window fallback.
	m := newTestModel(sampleItems(), 2, true)
	m.activeWinID = ""
	m.cursorWinID = "@2"

	m.Update(tea.BlurMsg{})

	if m.cursorWinID != "@2" {
		t.Errorf("BlurMsg with empty activeWinID stomped cursorWinID: got %q, want %q", m.cursorWinID, "@2")
	}
}

// ── Enter key ────────────────────────────────────────────────────────────────

func TestEnter_ReturnsSwitchWindowMsg(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
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
	m := newTestModel(sampleItems(), 4, true)
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
	if !strings.Contains(view, "/:search") {
		t.Errorf("focused View should show normal-mode footer hints:\n%s", view)
	}
}

func TestView_FooterAlwaysVisible(t *testing.T) {
	for _, focused := range []bool{true, false} {
		m := newTestModel(sampleItems(), 1, focused)
		view := stripANSI(m.View())
		if !strings.Contains(view, "/:search") {
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
	if !strings.Contains(view, "3m") {
		t.Errorf("View should contain running badge with minutes (3m):\n%s", view)
	}
	if strings.Contains(view, "idle") {
		t.Errorf("View should NOT show idle badge:\n%s", view)
	}
	permCount := strings.Count(view, "💬")
	if permCount < 2 {
		t.Errorf("View should contain at least 2 💬 badges (permission + ask), got %d:\n%s", permCount, view)
	}
}

func TestView_AgentTagSwitchesByAgent(t *testing.T) {
	claudePS := state.PaneState{Status: state.StatusIdle, Agent: state.AgentClaude}
	codexPS := state.PaneState{Status: state.StatusIdle, Agent: state.AgentCodex}
	unknownPS := state.PaneState{Status: state.StatusIdle, Agent: ""}

	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "claude-w"}, PaneState: &claudePS},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@2", Index: 1, Name: "codex-w"}, PaneState: &codexPS},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@3", Index: 2, Name: "fallback-w"}, PaneState: &unknownPS},
	}
	m := newTestModel(items, 1, true)
	view := stripANSI(m.View())

	if !strings.Contains(view, "[c]") {
		t.Errorf("View should contain [c] tag for Claude pane:\n%s", view)
	}
	if !strings.Contains(view, "[x]") {
		t.Errorf("View should contain [x] tag for Codex pane:\n%s", view)
	}
	// Claude (1) + unknown fallback (1) = 2 instances of [c].
	if got := strings.Count(view, "[c]"); got != 2 {
		t.Errorf("[c] count = %d, want 2 (claude + unknown fallback):\n%s", got, view)
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

func TestView_RunningBadgeShowsMinutes(t *testing.T) {
	elapsed := 5 * time.Minute
	runState := state.PaneState{Status: state.StatusRunning, Elapsed: elapsed}
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "w"}, PaneState: &runState},
	}
	m := newTestModel(items, 1, true)
	view := stripANSI(m.View())
	want := "5m"
	if !strings.Contains(view, want) {
		t.Errorf("View should contain %q:\n%s", want, view)
	}
}

// 1分未満の経過時間は分ではなく秒でバッジ表示する。
func TestView_RunningBadgeShowsSecondsUnderOneMinute(t *testing.T) {
	cases := []struct {
		name    string
		elapsed time.Duration
		want    string
		notWant string
	}{
		{name: "zero", elapsed: 0, want: "0s", notWant: "0m"},
		{name: "30s", elapsed: 30 * time.Second, want: "30s", notWant: "0m"},
		{name: "59s", elapsed: 59 * time.Second, want: "59s", notWant: "0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runState := state.PaneState{Status: state.StatusRunning, Elapsed: tc.elapsed}
			items := []ListItem{
				{Kind: ItemSession, SessionName: "s"},
				{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "w"}, PaneState: &runState},
			}
			m := newTestModel(items, 1, true)
			view := stripANSI(m.View())
			if !strings.Contains(view, tc.want) {
				t.Errorf("View should contain %q:\n%s", tc.want, view)
			}
			if strings.Contains(view, tc.notWant) {
				t.Errorf("View should NOT contain %q:\n%s", tc.notWant, view)
			}
		})
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
	view := stripANSI(m.View())
	if strings.Contains(view, "#") {
		t.Errorf("View should NOT contain # badge when gitData is empty:\n%s", view)
	}
}

// ── scroll ──────────────────────────────────────────────────────────────────

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
	items := manyItems(20)
	m := newTestModel(items, 1, true)
	m.height = 20
	view := stripANSI(m.View())
	if !strings.Contains(view, "win-0") {
		t.Errorf("should contain win-0:\n%s", view)
	}
	if strings.Contains(view, "win-10") {
		t.Errorf("win-10 should be scrolled out:\n%s", view)
	}
	if !strings.Contains(view, "more") {
		t.Errorf("should show scroll indicator:\n%s", view)
	}
}

func TestScroll_CursorDownScrollsView(t *testing.T) {
	items := manyItems(20)
	m := newTestModel(items, 1, true)
	m.height = 20
	for i := 0; i < 10; i++ {
		m.Update(key('j'))
	}
	if m.cursor <= 5 {
		t.Errorf("cursor should have moved past initial viewport, got %d", m.cursor)
	}
	if m.offset == 0 {
		t.Errorf("offset should have advanced from 0")
	}
}

func TestScroll_CursorUpScrollsBack(t *testing.T) {
	items := manyItems(20)
	m := newTestModel(items, 1, true)
	m.height = 20
	for i := 0; i < 10; i++ {
		m.Update(key('j'))
	}
	savedOffset := m.offset
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
	view := stripANSI(m.View())
	if !strings.Contains(view, "win-19") {
		t.Errorf("with no height, all items should render:\n%s", view)
	}
}

// ── search / text filter ────────────────────────────────────────────────────

func TestSearch_TypingFiltersItems(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('/'))
	for _, r := range "work" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
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
	m.Update(key('/'))
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	if m.searchQuery == "" {
		t.Fatal("precondition: searchQuery should not be empty after typing")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if m.searchQuery != "" {
		t.Errorf("Esc should clear search query, got %q", m.searchQuery)
	}
	if m.inputMode != modeNormal {
		t.Errorf("Esc should return to normal mode, got %v", m.inputMode)
	}
	if len(m.visibleItems()) != len(sampleItems()) {
		t.Errorf("after Esc, all items should be visible")
	}
}

func TestSearch_BackspaceDeletesChar(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('/'))
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.searchQuery != "x" {
		t.Errorf("after backspace: query = %q, want 'x'", m.searchQuery)
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('/'))
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'W'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
	visible := m.visibleItems()
	windowCount := 0
	for _, item := range visible {
		if item.Kind == ItemWindow {
			windowCount++
		}
	}
	if windowCount != 1 {
		t.Errorf("case-insensitive search: expected 1 window, got %d", windowCount)
	}
}

func TestSearch_MatchesSessionName(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('/'))
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
	m.Update(key('/'))
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
	m.inputMode = modeSearch
	m.searchQuery = "test"
	view := stripANSI(m.View())
	if !strings.Contains(view, "> test") {
		t.Errorf("View should show search query in prompt:\n%s", view)
	}
}

func TestView_FooterShowsKeyHints(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	view := stripANSI(m.View())
	if !strings.Contains(view, "/:search") {
		t.Errorf("footer should contain /:search hint:\n%s", view)
	}
}

// ── wrapText ────────────────────────────────────────────────────────────────

func TestWrapText_ASCIIOnly(t *testing.T) {
	lines := wrapText("hello world foo bar", 12)
	want := []string{"hello world", "foo bar"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(lines), len(want), lines)
	}
	for i, l := range lines {
		if l != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, l, want[i])
		}
	}
}

func TestWrapText_CJKBreaksWithinWord(t *testing.T) {
	// 21 CJK chars = 42 visual columns; width=40 should break.
	input := "だと以下理由により利用できないので修正案を検討して"
	lines := wrapText(input, 40)
	if len(lines) < 2 {
		t.Fatalf("expected CJK word to be broken into 2+ lines, got %d: %v", len(lines), lines)
	}
	// Reassemble and verify no characters are lost.
	joined := strings.Join(lines, "")
	if joined != input {
		t.Errorf("reassembled text = %q, want %q", joined, input)
	}
}

func TestWrapText_CJKFitsExactly(t *testing.T) {
	// 20 CJK chars = 40 visual columns; width=40 should fit on one line.
	input := "あいうえおかきくけこさしすせそたちつてと"
	lines := wrapText(input, 40)
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d: %v", len(lines), lines)
	}
	if lines[0] != input {
		t.Errorf("line = %q, want %q", lines[0], input)
	}
}

func TestWrapText_MixedASCIIAndCJK(t *testing.T) {
	input := "helmfile mcp が helm v4 だと以下理由により利用できないので修正案を検討して"
	lines := wrapText(input, 40)
	// Should be at least 2 lines because the total visual width is well over 40.
	if len(lines) < 2 {
		t.Fatalf("expected 2+ lines, got %d: %v", len(lines), lines)
	}
	// Verify no characters are lost (join with spaces for whitespace-separated words).
	joined := strings.Join(lines, " ")
	if !strings.Contains(joined, "を検討して") {
		t.Errorf("text should contain 'を検討して', got: %s", joined)
	}
}

func TestWrapText_EmptyAndNewlines(t *testing.T) {
	lines := wrapText("a\n\nb", 40)
	want := []string{"a", "", "b"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(lines), len(want), lines)
	}
	for i, l := range lines {
		if l != want[i] {
			t.Errorf("line[%d] = %q, want %q", i, l, want[i])
		}
	}
}

func TestBreakWord_SplitsAtBoundary(t *testing.T) {
	word := "あいうえおかきくけこ" // 10 chars = 20 visual cols
	lines := breakWord(word, 10)     // max 5 chars per line
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "あいうえお" {
		t.Errorf("line[0] = %q, want %q", lines[0], "あいうえお")
	}
	if lines[1] != "かきくけこ" {
		t.Errorf("line[1] = %q, want %q", lines[1], "かきくけこ")
	}
}

// ── active window background ────────────────────────────────────────────────

// windowLine returns the rendered line (including ANSI) that contains the
// given window name. Fails the test if not found.
func windowLine(t *testing.T, view, name string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(stripANSI(line), name) {
			return line
		}
	}
	t.Fatalf("window %q not found in view:\n%s", name, view)
	return ""
}

func TestView_ActiveWindowHasBackground(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.activeWinID = "@2" // "work"
	view := m.View()

	activeLine := windowLine(t, view, "work")
	inactiveLine := windowLine(t, view, "main")

	// Background SGR uses parameters starting with 48 (48;5;n or 48;2;r;g;b).
	bgRE := regexp.MustCompile(`\x1b\[[0-9;]*48[;m]`)
	if !bgRE.MatchString(activeLine) {
		t.Errorf("active window line should contain a background SGR (48;...):\n%q", activeLine)
	}
	if bgRE.MatchString(inactiveLine) {
		t.Errorf("inactive window line should NOT contain a background SGR:\n%q", inactiveLine)
	}
}

func TestView_CursorAndActiveAreIndependent(t *testing.T) {
	// cursor on "work" (@2), active window on "main" (@1)
	m := newTestModel(sampleItems(), 2, true)
	m.activeWinID = "@1"
	view := m.View()

	activeLine := windowLine(t, view, "main")
	cursorLine := windowLine(t, view, "work")

	bgRE := regexp.MustCompile(`\x1b\[[0-9;]*48[;m]`)
	if !bgRE.MatchString(activeLine) {
		t.Errorf("active row (main) should have background:\n%q", activeLine)
	}
	if bgRE.MatchString(cursorLine) {
		t.Errorf("cursor-but-not-active row (work) should NOT have background:\n%q", cursorLine)
	}
	if !strings.Contains(stripANSI(cursorLine), "▶") {
		t.Errorf("cursor row should still carry the ▶ marker:\n%q", cursorLine)
	}
}

// ── PR badge right-alignment ────────────────────────────────────────────────

func TestPaintActiveRow_EndsWithReset(t *testing.T) {
	inner := styleCursor.Render("▶ ") + " plain " + lipgloss.NewStyle().Foreground(colRunning).Render("🔄2m")
	out := paintActiveRow(inner, 40)
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Errorf("paintActiveRow must terminate with \\x1b[0m, got tail %q", out[max(0, len(out)-10):])
	}
	// After every internal reset, bg SGR must be re-applied.
	bg := activeBgSGR()
	if bg == "" {
		t.Skip("no color profile")
	}
	// Every "\x1b[0m" except the last must be followed by bg.
	remaining := out
	trimmed := strings.TrimSuffix(remaining, "\x1b[0m")
	if strings.Count(trimmed, "\x1b[0m") == 0 {
		t.Errorf("expected at least one internal reset; got %q", trimmed)
	}
	parts := strings.Split(trimmed, "\x1b[0m")
	for i := 0; i < len(parts)-1; i++ {
		next := parts[i+1]
		if !strings.HasPrefix(next, bg) {
			t.Errorf("internal reset #%d not followed by bg %q; following = %q", i, bg, next[:min(20, len(next))])
		}
	}
}

// ── activeWinID detection (multi-session) ──────────────────────────────────

// newLoadDataModel builds a Model wired to fakeTmuxClient with the given panes
// and the given currentSessionID, suitable for exercising loadData() in isolation.
func newLoadDataModel(panes []tmux.PaneInfo, currentSessionID string) *Model {
	return &Model{
		tmuxClient:       &fakeTmuxClient{panes: panes},
		stateReader:      &fakeStateReader{states: map[int]state.PaneState{}},
		currentSessionID: currentSessionID,
		winPaneNums:      map[string][]int{},
		gitData:          map[string]gitInfo{},
		promptCache:      map[string]string{},
	}
}

// pane builds a PaneInfo with sensible defaults for tests.
func pane(sessID, winID string, winActive, sessAttached bool, paneNum int) tmux.PaneInfo {
	return tmux.PaneInfo{
		SessionID:       sessID,
		SessionName:     "sess" + sessID,
		WindowID:        winID,
		WindowIndex:     0,
		WindowName:      "win" + winID,
		PaneID:          fmt.Sprintf("%%%d", paneNum),
		PaneIndex:       0,
		PaneNumber:      paneNum,
		WindowActive:    winActive,
		SessionAttached: sessAttached,
	}
}

func TestLoadData_ActiveWinIDPicksOwnSessionsActiveWindow(t *testing.T) {
	// Two sessions, both attached, each with its own active window.
	// Order is intentional: session $2 is iterated first so the broken
	// "first attached active" logic would pick @9 instead of @1.
	panes := []tmux.PaneInfo{
		pane("$2", "@9", true, true, 9),
		pane("$1", "@1", true, true, 1),
		pane("$1", "@2", false, true, 2),
	}
	m := newLoadDataModel(panes, "$1")
	msg, ok := m.loadData()().(dataMsg)
	if !ok {
		t.Fatalf("loadData did not return dataMsg")
	}
	if msg.err != nil {
		t.Fatalf("loadData err: %v", msg.err)
	}
	if msg.activeWinID != "@1" {
		t.Errorf("activeWinID = %q, want %q (own session's current window)", msg.activeWinID, "@1")
	}
}

func TestLoadData_ActiveWinIDForSidebarInOtherSession(t *testing.T) {
	// Same data as above but the sidebar lives in $2 — must pick @9.
	panes := []tmux.PaneInfo{
		pane("$1", "@1", true, true, 1),
		pane("$1", "@2", false, true, 2),
		pane("$2", "@9", true, true, 9),
	}
	m := newLoadDataModel(panes, "$2")
	msg, ok := m.loadData()().(dataMsg)
	if !ok {
		t.Fatalf("loadData did not return dataMsg")
	}
	if msg.activeWinID != "@9" {
		t.Errorf("activeWinID = %q, want %q (own session's current window)", msg.activeWinID, "@9")
	}
}

func TestGitTick_AlsoRefreshesActiveWinID(t *testing.T) {
	// Regression: SIGUSR1 from tmux hooks may fail to deliver (missing hook config,
	// /tmp permissions, etc.). The 10s git tick must also refresh activeWinID so
	// the active-window highlight does not stay stuck on a stale window forever.
	m := newLoadDataModel(nil, "$1")
	_, cmd := m.Update(gitTickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("gitTickMsg should return a Cmd")
	}
	// Drive the batched Cmd until we see a dataMsg — that proves loadData is wired up.
	// tea.Batch returns a BatchMsg which contains pointers to functions; we walk them.
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg from gitTickMsg, got %T", msg)
	}
	sawDataMsg := false
	for _, sub := range batch {
		if sub == nil {
			continue
		}
		if _, ok := sub().(dataMsg); ok {
			sawDataMsg = true
			break
		}
	}
	if !sawDataMsg {
		t.Errorf("gitTickMsg batch must include a Cmd producing dataMsg (loadData) so activeWinID is refreshed periodically")
	}
}

func TestLoadData_ActiveWinIDWhenOwnSessionDetached(t *testing.T) {
	// Sidebar's session is currently detached (no client attached) but
	// tmux still tracks a current window for it. The sidebar must surface
	// that window so cursor position remains correct when the user re-attaches.
	panes := []tmux.PaneInfo{
		pane("$1", "@1", true, false, 1),
		pane("$2", "@9", true, true, 9),
	}
	m := newLoadDataModel(panes, "$1")
	msg, ok := m.loadData()().(dataMsg)
	if !ok {
		t.Fatalf("loadData did not return dataMsg")
	}
	if msg.activeWinID != "@1" {
		t.Errorf("activeWinID = %q, want %q (own session's current window even when detached)", msg.activeWinID, "@1")
	}
}

// ── relocateCursor fallback ────────────────────────────────────────────────

func TestRelocateCursor_FallsBackToActiveWinIDBeforeCurrentWinID(t *testing.T) {
	// Regression: when cursorWinID points to a window that has just disappeared
	// (deleted, hidden, filtered out) AND activeWinID did not change, the data
	// handler does not re-sync cursorWinID. relocateCursor must then prefer
	// activeWinID (where the user actually is) over currentWinID (where this
	// sidebar's pane lives) — otherwise the cursor "stays at the original tmux
	// window" instead of following the user.
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "user-here"}},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@9", Index: 1, Name: "sidebar-home"}},
	}
	m := newTestModel(items, 0, true)
	m.cursorWinID = "@5"  // window the cursor was on; just got deleted
	m.activeWinID = "@1"  // user's actual current tmux window
	m.currentWinID = "@9" // window where this sidebar's pane lives
	m.relocateCursor()
	if m.cursorWinID != "@1" {
		t.Errorf("cursorWinID = %q, want %q (cursor must follow active window, not jump to sidebar's home)", m.cursorWinID, "@1")
	}
	if m.cursor != 1 {
		t.Errorf("cursor index = %d, want 1 (index of @1 in items)", m.cursor)
	}
}

func TestRelocateCursor_FallsBackToCurrentWinIDWhenActiveAlsoMissing(t *testing.T) {
	// activeWinID is not in the visible items (e.g., it lives in a hidden
	// session) — relocateCursor must fall back further to currentWinID.
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@9", Index: 0, Name: "sidebar-home"}},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@2", Index: 1, Name: "other"}},
	}
	m := newTestModel(items, 0, true)
	m.cursorWinID = "@5"  // missing
	m.activeWinID = "@7"  // also missing (e.g., in hidden session)
	m.currentWinID = "@9" // present
	m.relocateCursor()
	if m.cursorWinID != "@9" {
		t.Errorf("cursorWinID = %q, want %q (must fall back to currentWinID)", m.cursorWinID, "@9")
	}
}

func TestRelocateCursor_PreservesValidCursorWinID(t *testing.T) {
	// Sanity check: when cursorWinID still exists in the items (e.g., the user
	// manually moved the cursor and nothing else changed), it must stay where
	// the user put it — the activeWinID fallback only kicks in when the cursor
	// target has disappeared.
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "active"}},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@2", Index: 1, Name: "manual"}},
	}
	m := newTestModel(items, 0, true)
	m.cursorWinID = "@2" // user moved here
	m.activeWinID = "@1"
	m.currentWinID = "@1"
	m.relocateCursor()
	if m.cursorWinID != "@2" {
		t.Errorf("cursorWinID = %q, want %q (manual position should stick)", m.cursorWinID, "@2")
	}
	if m.cursor != 2 {
		t.Errorf("cursor index = %d, want 2", m.cursor)
	}
}

func TestView_PRBadgeRightAligned(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "feat"}},
	}
	m := newTestModel(items, 1, true)
	m.width = 40
	m.gitData = map[string]gitInfo{
		"@1": {branch: "feat", prState: "open", prNumber: 42},
	}

	view := m.View()
	line := stripANSI(windowLine(t, view, "feat"))
	line = strings.TrimRight(line, " ")

	if !strings.HasSuffix(line, "#42") {
		t.Errorf("PR badge should be at the right edge; line = %q", line)
	}
	// Expect multiple padding spaces between the name and the suffix
	// (not just the single mandatory gap), since the name is short.
	if !strings.Contains(line, "feat   ") {
		t.Errorf("expected padding between name and suffix; line = %q", line)
	}
}

// ── modal input (Phase 1) ───────────────────────────────────────────────────

func TestModal_NormalIgnoresPlainText(t *testing.T) {
	// In normal mode a stray rune that does not match a registered command
	// must NOT enter the search query. This is the load-bearing change of
	// Phase 1: it is what frees up letters like d/D/p for commands.
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('a'))
	m.Update(key('z'))
	if m.searchQuery != "" {
		t.Errorf("normal mode must not capture text; query = %q", m.searchQuery)
	}
	if m.inputMode != modeNormal {
		t.Errorf("normal mode plain text must not switch modes; got %v", m.inputMode)
	}
}

func TestModal_SlashEntersSearchMode(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('/'))
	if m.inputMode != modeSearch {
		t.Errorf("after /: inputMode = %v, want modeSearch", m.inputMode)
	}
	if m.searchQuery != "" {
		t.Errorf("after /: query should start empty, got %q", m.searchQuery)
	}
}

func TestModal_SlashThenTextFiltersList(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('/'))
	for _, r := range "work" {
		m.Update(key(r))
	}
	if m.searchQuery != "work" {
		t.Errorf("after /work: query = %q, want 'work'", m.searchQuery)
	}
	visible := m.visibleItems()
	windows := 0
	for _, item := range visible {
		if item.Kind == ItemWindow {
			windows++
		}
	}
	if windows != 1 {
		t.Errorf("expected 1 matching window after filter, got %d", windows)
	}
}

func TestModal_EscReturnsToNormal(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('/'))
	m.Update(key('w'))
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if m.inputMode != modeNormal {
		t.Errorf("Esc should return to normal; got %v", m.inputMode)
	}
	if m.searchQuery != "" {
		t.Errorf("Esc should clear query; got %q", m.searchQuery)
	}
}

func TestModal_NormalKeysPreserved(t *testing.T) {
	// j / k / Enter / Tab must still behave the way they used to in normal
	// mode after the modal split. Tab is currently unbound — the assertion
	// here is that it does not get swallowed as text.
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('j'))
	if m.cursor != 2 {
		t.Errorf("j in normal mode should move cursor; got %d", m.cursor)
	}
	m.Update(key('k'))
	if m.cursor != 1 {
		t.Errorf("k in normal mode should move cursor back; got %d", m.cursor)
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Errorf("Enter in normal mode should return a switch Cmd")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.searchQuery != "" {
		t.Errorf("Tab must not be captured as search text; got %q", m.searchQuery)
	}
}

// ── close / confirm (Phase 2) ───────────────────────────────────────────────

func TestClose_DRequestsKillWindowConfirm(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('d'))
	if m.confirm != confirmKillWindow {
		t.Errorf("after d: confirm = %v, want confirmKillWindow", m.confirm)
	}
	if m.confirmItem.Window == nil || m.confirmItem.Window.ID != "@1" {
		t.Errorf("confirmItem should reference cursor's window @1, got %+v", m.confirmItem.Window)
	}
}

func TestClose_ShiftDRequestsKillSessionConfirm(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('D'))
	if m.confirm != confirmKillSession {
		t.Errorf("after D: confirm = %v, want confirmKillSession", m.confirm)
	}
	if m.confirmItem.SessionName != "session-a" {
		t.Errorf("confirmItem should target session-a, got %q", m.confirmItem.SessionName)
	}
}

func TestClose_NCancelsConfirm(t *testing.T) {
	fc := &fakeTmuxClient{}
	m := newTestModel(sampleItems(), 1, true)
	m.tmuxClient = fc
	m.Update(key('d'))
	m.Update(key('n'))
	if m.confirm != confirmNone {
		t.Errorf("n should clear confirm state, got %v", m.confirm)
	}
	if len(fc.killedWindows) != 0 {
		t.Errorf("n should not kill anything; killedWindows = %v", fc.killedWindows)
	}
}

func TestClose_EscCancelsConfirm(t *testing.T) {
	fc := &fakeTmuxClient{}
	m := newTestModel(sampleItems(), 1, true)
	m.tmuxClient = fc
	m.Update(key('d'))
	m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if m.confirm != confirmNone {
		t.Errorf("Esc should clear confirm, got %v", m.confirm)
	}
	if len(fc.killedWindows) != 0 {
		t.Errorf("Esc should not kill anything; killedWindows = %v", fc.killedWindows)
	}
}

func TestClose_YInvokesKillWindow(t *testing.T) {
	t.Setenv("TMUX_SIDEBAR_GRAVEYARD_DIR", t.TempDir())
	fc := &fakeTmuxClient{captureContent: "captured pane content"}
	m := newTestModel(sampleItems(), 1, true)
	m.tmuxClient = fc
	m.Update(key('d'))
	_, cmd := m.Update(key('y'))
	if cmd == nil {
		t.Fatal("y should return a kill Cmd")
	}
	msg := cmd()
	res, ok := msg.(killResultMsg)
	if !ok {
		t.Fatalf("Cmd returned %T, want killResultMsg", msg)
	}
	if res.err != nil {
		t.Errorf("kill returned err: %v", res.err)
	}
	if len(fc.killedWindows) != 1 || fc.killedWindows[0] != "session-a:0" {
		t.Errorf("expected kill of session-a:0, got %v", fc.killedWindows)
	}
	if len(fc.captureTargets) != 1 || fc.captureTargets[0] != "session-a:0" {
		t.Errorf("capture-pane should run before kill on session-a:0, got %v", fc.captureTargets)
	}
	if res.gravePath == "" {
		t.Errorf("expected non-empty gravePath when capture succeeds")
	}
}

func TestClose_YInvokesKillSession(t *testing.T) {
	t.Setenv("TMUX_SIDEBAR_GRAVEYARD_DIR", t.TempDir())
	fc := &fakeTmuxClient{captureContent: "captured"}
	m := newTestModel(sampleItems(), 4, true) // cursor on session-b's window
	m.tmuxClient = fc
	m.Update(key('D'))
	_, cmd := m.Update(key('y'))
	if cmd == nil {
		t.Fatal("y should return a kill Cmd")
	}
	cmd()
	if len(fc.killedSessions) != 1 || fc.killedSessions[0] != "session-b" {
		t.Errorf("expected kill of session-b, got %v", fc.killedSessions)
	}
}

func TestClose_ConfirmBlocksOtherKeys(t *testing.T) {
	// While a confirm is pending, j must not move the cursor — the user
	// should not be able to "drift past" a destructive prompt.
	m := newTestModel(sampleItems(), 1, true)
	m.Update(key('d'))
	cursorBefore := m.cursor
	m.Update(key('j'))
	if m.cursor != cursorBefore {
		t.Errorf("j during confirm should be ignored; cursor moved %d → %d", cursorBefore, m.cursor)
	}
	if m.confirm != confirmKillWindow {
		t.Errorf("confirm state should persist through ignored keys")
	}
}

func TestConfirmPrompt_IdleSimple(t *testing.T) {
	idle := state.PaneState{Status: state.StatusIdle}
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "main"}, PaneState: &idle},
	}
	m := newTestModel(items, 1, true)
	m.Update(key('d'))
	view := stripANSI(m.View())
	if !strings.Contains(view, "kill window 'main'? [y/N]") {
		t.Errorf("idle confirm should be simple; view:\n%s", view)
	}
}

func TestConfirmPrompt_RunningShowsElapsed(t *testing.T) {
	run := state.PaneState{Status: state.StatusRunning, Elapsed: 3 * time.Minute}
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "build"}, PaneState: &run},
	}
	m := newTestModel(items, 1, true)
	m.Update(key('d'))
	view := stripANSI(m.View())
	if !strings.Contains(view, "running 3m") {
		t.Errorf("running confirm should mention elapsed; view:\n%s", view)
	}
}

func TestConfirmPrompt_PermissionWarns(t *testing.T) {
	perm := state.PaneState{Status: state.StatusPermission}
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "perm"}, PaneState: &perm},
	}
	m := newTestModel(items, 1, true)
	m.Update(key('d'))
	view := stripANSI(m.View())
	if !strings.Contains(view, "agent waiting") {
		t.Errorf("permission/ask confirm should warn 'agent waiting'; view:\n%s", view)
	}
}

func TestKillResultMsg_SetsMessageAndReloads(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	_, cmd := m.Update(killResultMsg{gravePath: "/tmp/graveyard/foo.txt"})
	if !strings.Contains(m.message, "/tmp/graveyard/foo.txt") {
		t.Errorf("message should mention graveyard path, got %q", m.message)
	}
	if cmd == nil {
		t.Errorf("killResultMsg should trigger a reload Cmd")
	}
}

func TestKillResultMsg_ErrorMessage(t *testing.T) {
	m := newTestModel(sampleItems(), 1, true)
	m.Update(killResultMsg{err: fmt.Errorf("boom")})
	if !strings.Contains(m.message, "boom") {
		t.Errorf("message should include error text, got %q", m.message)
	}
}

// ── pin (Phase 3) ───────────────────────────────────────────────────────────

// pinPanes returns three sessions (work / infra / scratch) with one window
// each, in tmux enumeration order. Used by pin tests so the order of work →
// infra → scratch represents the default unpinned layout.
func pinPanes() []tmux.PaneInfo {
	return []tmux.PaneInfo{
		pane("$1", "@1", true, true, 1),
		pane("$2", "@2", true, true, 2),
		pane("$3", "@3", true, true, 3),
	}
}

// withSessionNames overrides the auto-generated session names ("sess$1" etc.)
// from pane(...) so tests can assert against meaningful names.
func withSessionNames(panes []tmux.PaneInfo, names map[string]string) []tmux.PaneInfo {
	out := make([]tmux.PaneInfo, len(panes))
	for i, p := range panes {
		if n, ok := names[p.SessionID]; ok {
			p.SessionName = n
		}
		out[i] = p
	}
	return out
}

// pinFixture writes hidden_sessions / pinned_sessions to a temp dir and
// returns (cfg, hiddenPath). Pin-related tests build cfg through this path
// so they exercise the same Load codepath that production uses.
func pinFixture(t *testing.T, pinned []string, hidden []string) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	hiddenPath := filepath.Join(dir, "hidden_sessions")
	hiddenContent := ""
	if len(hidden) > 0 {
		hiddenContent = strings.Join(hidden, "\n") + "\n"
	}
	if err := os.WriteFile(hiddenPath, []byte(hiddenContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(pinned) > 0 {
		pinnedPath := filepath.Join(dir, "pinned_sessions")
		if err := os.WriteFile(pinnedPath, []byte(strings.Join(pinned, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(hiddenPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, hiddenPath
}

// loadModelWithPanes builds a Model wired with given panes and config and
// invokes loadData() once so m.items reflects the desired layout. Routing
// the dataMsg through Update applies relocateCursor so m.cursor lands on a
// real window row, mirroring the production codepath.
func loadModelWithPanes(panes []tmux.PaneInfo, cfg config.Config, hiddenPath string) *Model {
	m := newLoadDataModel(panes, "$1")
	m.cfg = cfg
	m.hiddenPath = hiddenPath
	m.focused = true
	m.width = 40
	msg := m.loadData()().(dataMsg)
	m.Update(msg)
	return m
}

func TestPin_LoadDataPlacesPinnedFirst(t *testing.T) {
	panes := withSessionNames(pinPanes(), map[string]string{
		"$1": "work", "$2": "infra", "$3": "scratch",
	})
	cfg, hiddenPath := pinFixture(t, []string{"scratch"}, nil)
	m := loadModelWithPanes(panes, cfg, hiddenPath)

	// Expect order: scratch (pinned) → divider → work → infra (tmux order)
	var names []string
	sawDivider := false
	for _, item := range m.items {
		switch item.Kind {
		case ItemSession:
			names = append(names, item.SessionName)
		case ItemDivider:
			sawDivider = true
		}
	}
	want := []string{"scratch", "work", "infra"}
	if len(names) != len(want) {
		t.Fatalf("session order = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("session[%d] = %q, want %q", i, names[i], n)
		}
	}
	if !sawDivider {
		t.Errorf("expected ItemDivider between pinned and unpinned")
	}
}

func TestPin_NoDividerWhenAllPinnedOrAllUnpinned(t *testing.T) {
	panes := withSessionNames(pinPanes(), map[string]string{
		"$1": "work", "$2": "infra", "$3": "scratch",
	})
	// All unpinned: no divider.
	cfg1, hp1 := pinFixture(t, nil, nil)
	m := loadModelWithPanes(panes, cfg1, hp1)
	for _, it := range m.items {
		if it.Kind == ItemDivider {
			t.Errorf("no divider expected when nothing is pinned")
		}
	}
	// All pinned: no divider either.
	cfg2, hp2 := pinFixture(t, []string{"work", "infra", "scratch"}, nil)
	m2 := loadModelWithPanes(panes, cfg2, hp2)
	for _, it := range m2.items {
		if it.Kind == ItemDivider {
			t.Errorf("no divider expected when everything is pinned")
		}
	}
}

func TestPin_PinnedOrderFollowsConfigOrder(t *testing.T) {
	// Config order: scratch → work, but tmux enum order is work → infra → scratch.
	// Pinned section must use config order (scratch first), not tmux order.
	panes := withSessionNames(pinPanes(), map[string]string{
		"$1": "work", "$2": "infra", "$3": "scratch",
	})
	cfg, hp := pinFixture(t, []string{"scratch", "work"}, nil)
	m := loadModelWithPanes(panes, cfg, hp)

	var pinnedNames []string
	for _, item := range m.items {
		if item.Kind == ItemDivider {
			break
		}
		if item.Kind == ItemSession {
			pinnedNames = append(pinnedNames, item.SessionName)
		}
	}
	want := []string{"scratch", "work"}
	if len(pinnedNames) != len(want) {
		t.Fatalf("pinned section = %v, want %v", pinnedNames, want)
	}
	for i, n := range want {
		if pinnedNames[i] != n {
			t.Errorf("pinned[%d] = %q, want %q", i, pinnedNames[i], n)
		}
	}
}

func TestPin_HiddenWinsOverPinned(t *testing.T) {
	// "scratch" is both hidden and pinned — hidden must win, scratch is dropped.
	panes := withSessionNames(pinPanes(), map[string]string{
		"$1": "work", "$2": "infra", "$3": "scratch",
	})
	cfg, hp := pinFixture(t, []string{"scratch"}, []string{"scratch"})
	m := loadModelWithPanes(panes, cfg, hp)

	for _, item := range m.items {
		if item.Kind == ItemSession && item.SessionName == "scratch" {
			t.Errorf("hidden session 'scratch' must not appear even when pinned")
		}
	}
	// Also no divider — scratch was the only pinned item, and it's filtered out.
	for _, item := range m.items {
		if item.Kind == ItemDivider {
			t.Errorf("divider should be absent when pinned set is fully hidden")
		}
	}
}

func TestPin_BlocksSessionKill(t *testing.T) {
	// Pin = delete protection. `D` on a pinned session must NOT enter the
	// confirm state and must NOT call kill.
	fc := &fakeTmuxClient{}
	m := newTestModel(sampleItems(), 1, true) // cursor on session-a window
	m.tmuxClient = fc
	m.cfg, _ = pinFixture(t, []string{"session-a"}, nil)

	m.Update(key('D'))
	if m.confirm != confirmNone {
		t.Errorf("D on pinned session should not arm confirm; got %v", m.confirm)
	}
	if !strings.Contains(m.message, "pinned") || !strings.Contains(m.message, "session-a") {
		t.Errorf("expected message to explain why kill was blocked; got %q", m.message)
	}
	// Even pressing y afterward must not kill anything.
	m.Update(key('y'))
	if len(fc.killedSessions) != 0 {
		t.Errorf("kill must not happen after blocked D; killedSessions = %v", fc.killedSessions)
	}
}

func TestPin_SessionKillAllowedAfterUnpinOnDisk(t *testing.T) {
	// Removing the entry from pinned_sessions on disk and triggering a
	// reload (loadData) drops the protection — `D` should then work.
	// This exercises the file-only management contract: no `p` key, the
	// only way to unpin is to edit the file.
	t.Setenv("TMUX_SIDEBAR_GRAVEYARD_DIR", t.TempDir())
	cfg, hiddenPath := pinFixture(t, []string{"sess$1"}, nil)
	// Build a model that goes through loadData() so cfg comes from disk.
	panes := []tmux.PaneInfo{pane("$1", "@1", true, true, 1)}
	m := loadModelWithPanes(panes, cfg, hiddenPath)
	// Replace the tmux client (preserving panes so subsequent loadData calls
	// still see the same layout) and add capture content for kill snapshots.
	fc := &fakeTmuxClient{panes: panes, captureContent: "x"}
	m.tmuxClient = fc
	if !m.cfg.IsPinnedSession("sess$1") {
		t.Fatalf("precondition: sess$1 should be pinned via fixture")
	}

	// While pinned, `D` is blocked.
	m.Update(key('D'))
	if m.confirm != confirmNone {
		t.Fatal("precondition: D should be blocked while pinned")
	}

	// Edit pinned_sessions on disk to remove the entry.
	pinnedPath := filepath.Join(filepath.Dir(hiddenPath), "pinned_sessions")
	if err := os.WriteFile(pinnedPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// Trigger a reload (what gitTickMsg / SIGUSR1 do in production).
	msg := m.loadData()().(dataMsg)
	m.Update(msg)
	if m.cfg.IsPinnedSession("sess$1") {
		t.Fatalf("after on-disk unpin + reload, IsPinnedSession should be false")
	}

	// Now `D` should work.
	m.Update(key('D'))
	if m.confirm != confirmKillSession {
		t.Fatalf("after on-disk unpin, D should arm confirm; got %v", m.confirm)
	}
	_, cmd := m.Update(key('y'))
	if cmd == nil {
		t.Fatal("y should return a kill Cmd")
	}
	cmd()
	if len(fc.killedSessions) != 1 || fc.killedSessions[0] != "sess$1" {
		t.Errorf("expected kill of sess$1; got %v", fc.killedSessions)
	}
}

func TestPin_WindowKillNotBlockedByPin(t *testing.T) {
	// `d` (window kill) is intentionally not blocked. Pin is a session-level
	// concept; window-level destructive operations remain governed by the
	// state-based confirm strength. This documents that boundary.
	t.Setenv("TMUX_SIDEBAR_GRAVEYARD_DIR", t.TempDir())
	fc := &fakeTmuxClient{captureContent: "x"}
	m := newTestModel(sampleItems(), 1, true)
	m.tmuxClient = fc
	m.cfg, _ = pinFixture(t, []string{"session-a"}, nil)

	m.Update(key('d'))
	if m.confirm != confirmKillWindow {
		t.Errorf("d should arm window-kill confirm even when session is pinned; got %v", m.confirm)
	}
}

func TestPin_NoToggleKey(t *testing.T) {
	// `p` is no longer bound. Pressing it must be a no-op and must not
	// touch any pinned_sessions file (which we don't even have a path to).
	m := newTestModel(sampleItems(), 1, true)
	m.cfg, _ = pinFixture(t, nil, nil)
	cursorBefore := m.cursor
	m.Update(key('p'))
	if m.cursor != cursorBefore {
		t.Errorf("p must not move the cursor; got %d → %d", cursorBefore, m.cursor)
	}
	if m.cfg.IsPinnedSession("session-a") {
		t.Errorf("p must not change pin state in-memory")
	}
}

func TestPin_LoadDataRereadsConfigFromDisk(t *testing.T) {
	// Edit pinned_sessions on disk → loadData → m.cfg reflects the change.
	// This is the load-bearing property of the file-only management
	// contract: without it, edits would only apply on sidebar restart.
	panes := withSessionNames(pinPanes(), map[string]string{
		"$1": "work", "$2": "infra", "$3": "scratch",
	})
	cfg, hiddenPath := pinFixture(t, nil, nil)
	m := loadModelWithPanes(panes, cfg, hiddenPath)
	if m.cfg.IsPinnedSession("scratch") {
		t.Fatal("precondition: nothing pinned at start")
	}
	pinnedPath := filepath.Join(filepath.Dir(hiddenPath), "pinned_sessions")
	if err := os.WriteFile(pinnedPath, []byte("scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Re-run loadData like a periodic reload would.
	msg := m.loadData()().(dataMsg)
	m.Update(msg)
	if !m.cfg.IsPinnedSession("scratch") {
		t.Errorf("after on-disk pin + reload, scratch should be pinned")
	}
}

func TestView_PinnedSessionShowsEmoji(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "work"},
		{Kind: ItemWindow, SessionName: "work", Window: &tmux.Window{ID: "@1", Index: 0, Name: "main"}},
	}
	m := newTestModel(items, 1, true)
	m.cfg, _ = pinFixture(t, []string{"work"}, nil)
	view := stripANSI(m.View())
	if !strings.Contains(view, "📌 work") {
		t.Errorf("pinned session should be prefixed with 📌; view:\n%s", view)
	}
	if strings.Contains(view, "▾ work") {
		t.Errorf("pinned session should not use ▾ prefix; view:\n%s", view)
	}
}

func TestView_DividerRenderedBetweenGroups(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "pinned-s"},
		{Kind: ItemWindow, SessionName: "pinned-s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "p"}},
		{Kind: ItemDivider},
		{Kind: ItemSession, SessionName: "free-s"},
		{Kind: ItemWindow, SessionName: "free-s", Window: &tmux.Window{ID: "@2", Index: 0, Name: "f"}},
	}
	m := newTestModel(items, 1, true)
	m.cfg, _ = pinFixture(t, []string{"pinned-s"}, nil)
	view := stripANSI(m.View())
	// The divider should appear after the pinned section's window row.
	pinnedIdx := strings.Index(view, "p")
	dividerIdx := strings.Index(view, strings.Repeat("─", 40))
	freeIdx := strings.Index(view, "free-s")
	if pinnedIdx < 0 || dividerIdx < 0 || freeIdx < 0 {
		t.Fatalf("missing landmarks: pinnedIdx=%d dividerIdx=%d freeIdx=%d\n%s", pinnedIdx, dividerIdx, freeIdx, view)
	}
	if !(pinnedIdx < dividerIdx && dividerIdx < freeIdx) {
		t.Errorf("expected pinned-window < divider < free-session in view; got %d < %d < %d\n%s", pinnedIdx, dividerIdx, freeIdx, view)
	}
}

func TestPin_CursorMoveSkipsDivider(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "p"},
		{Kind: ItemWindow, SessionName: "p", Window: &tmux.Window{ID: "@1", Index: 0, Name: "p1"}},
		{Kind: ItemDivider},
		{Kind: ItemSession, SessionName: "u"},
		{Kind: ItemWindow, SessionName: "u", Window: &tmux.Window{ID: "@2", Index: 0, Name: "u1"}},
	}
	m := newTestModel(items, 1, true)
	m.Update(key('j'))
	if m.cursor != 4 {
		t.Errorf("j must skip divider+session header; cursor = %d, want 4", m.cursor)
	}
	m.Update(key('k'))
	if m.cursor != 1 {
		t.Errorf("k must skip divider+session header upward; cursor = %d, want 1", m.cursor)
	}
}

func TestSanitizeLabel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"with space", "with_space"},
		{"slash/in/name", "slash_in_name"},
		{"colon:thing", "colon_thing"},
		{"", "unnamed"},
	}
	for _, tc := range cases {
		if got := sanitizeLabel(tc.in); got != tc.want {
			t.Errorf("sanitizeLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
