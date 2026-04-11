package ui

import (
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

func (f *fakeTmuxClient) ListSessions() ([]tmux.Session, error)  { return nil, nil }
func (f *fakeTmuxClient) ListWindows() ([]tmux.Window, error)    { return nil, nil }
func (f *fakeTmuxClient) ListPanes() ([]tmux.Pane, error)        { return nil, nil }
func (f *fakeTmuxClient) CurrentPane() (tmux.CurrentPane, error) { return tmux.CurrentPane{}, nil }
func (f *fakeTmuxClient) SwitchWindow(_ string, _ int) error     { return nil }

type fakeStateReader struct{ states map[int]state.PaneState }

func (f *fakeStateReader) Read() (map[int]state.PaneState, error) { return f.states, nil }

// ── helpers ──────────────────────────────────────────────────────────────────

// newTestModel builds a Model with pre-populated items for white-box unit tests.
func newTestModel(items []ListItem, cursor int, mode Mode) *Model {
	return &Model{
		tmuxClient:  &fakeTmuxClient{},
		stateReader: &fakeStateReader{states: map[int]state.PaneState{}},
		items:       items,
		cursor:      cursor,
		mode:        mode,
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
	m := newTestModel(sampleItems(), 1, ModeInteractive) // cursor at items[1]

	m.Update(key('j'))
	if m.cursor != 2 {
		t.Errorf("after j: cursor = %d, want 2", m.cursor)
	}
}

func TestCursorMove_j_SkipsSessionHeader(t *testing.T) {
	m := newTestModel(sampleItems(), 2, ModeInteractive) // cursor at items[2]

	m.Update(key('j'))
	// items[3] is a session header; must be skipped to land on items[4]
	if m.cursor != 4 {
		t.Errorf("after j from 2: cursor = %d, want 4 (session header skipped)", m.cursor)
	}
}

func TestCursorMove_j_StaysAtEnd(t *testing.T) {
	m := newTestModel(sampleItems(), 4, ModeInteractive) // cursor at last window

	m.Update(key('j'))
	if m.cursor != 4 {
		t.Errorf("j at end: cursor = %d, want 4 (no change)", m.cursor)
	}
}

func TestCursorMove_k_MovesToPrevWindow(t *testing.T) {
	m := newTestModel(sampleItems(), 2, ModeInteractive) // cursor at items[2]

	m.Update(key('k'))
	if m.cursor != 1 {
		t.Errorf("after k: cursor = %d, want 1", m.cursor)
	}
}

func TestCursorMove_k_SkipsSessionHeader(t *testing.T) {
	m := newTestModel(sampleItems(), 4, ModeInteractive) // cursor at items[4]

	m.Update(key('k'))
	// items[3] is a session header; must be skipped to land on items[2]
	if m.cursor != 2 {
		t.Errorf("after k from 4: cursor = %d, want 2 (session header skipped)", m.cursor)
	}
}

func TestCursorMove_k_StaysAtStart(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive) // cursor at first window

	m.Update(key('k'))
	if m.cursor != 1 {
		t.Errorf("k at start: cursor = %d, want 1 (no change)", m.cursor)
	}
}

func TestCursorMove_DownKey(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive)

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("down arrow: cursor = %d, want 2", m.cursor)
	}
}

func TestCursorMove_UpKey(t *testing.T) {
	m := newTestModel(sampleItems(), 2, ModeInteractive)

	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("up arrow: cursor = %d, want 1", m.cursor)
	}
}

// ── passive mode ─────────────────────────────────────────────────────────────

func TestPassiveMode_jIgnored(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModePassive)

	_, cmd := m.Update(key('j'))
	if m.cursor != 1 {
		t.Errorf("cursor moved in passive mode: got %d, want 1", m.cursor)
	}
	if cmd != nil {
		t.Errorf("expected nil Cmd in passive mode, got non-nil")
	}
}

func TestPassiveMode_EnterIgnored(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModePassive)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.cursor != 1 {
		t.Errorf("cursor moved on Enter in passive mode: got %d, want 1", m.cursor)
	}
	if cmd != nil {
		t.Errorf("expected nil Cmd on Enter in passive mode, got non-nil")
	}
}

func TestPassiveMode_qIgnored(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModePassive)

	m.Update(key('q'))
	// mode should remain passive (was already passive)
	if m.mode != ModePassive {
		t.Errorf("mode = %v, want ModePassive", m.mode)
	}
}

// ── mode switching ───────────────────────────────────────────────────────────

func TestQKey_SwitchesToPassive(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive)

	m.Update(key('q'))
	if m.mode != ModePassive {
		t.Errorf("after q: mode = %v, want ModePassive", m.mode)
	}
}

func TestEscKey_SwitchesToPassive(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive)

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != ModePassive {
		t.Errorf("after Esc: mode = %v, want ModePassive", m.mode)
	}
}

func TestIKey_SwitchesToInteractive(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModePassive)

	m.Update(key('i'))
	if m.mode != ModeInteractive {
		t.Errorf("after i from passive: mode = %v, want ModeInteractive", m.mode)
	}
}

func TestIKey_TogglesFromInteractive(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive)

	m.Update(key('i'))
	if m.mode != ModePassive {
		t.Errorf("after i from interactive: mode = %v, want ModePassive", m.mode)
	}
}

// ── Enter key ────────────────────────────────────────────────────────────────

func TestEnter_ReturnsSwitchWindowMsg(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive) // cursor at @1 index=0 "main"

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
	m := newTestModel(sampleItems(), 4, ModeInteractive) // cursor at @3 index=0 "idle-win"

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

func TestView_ContainsCursor(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive) // cursor at items[1]

	view := stripANSI(m.View())
	if !strings.Contains(view, "▶") {
		t.Errorf("View should contain '▶' cursor:\n%s", view)
	}
}

func TestView_ContainsSessionNames(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive)

	view := stripANSI(m.View())
	for _, want := range []string{"session-a", "session-b"} {
		if !strings.Contains(view, want) {
			t.Errorf("View should contain %q:\n%s", want, view)
		}
	}
}

func TestView_ContainsWindowNames(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive)

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
	m := newTestModel(items, 1, ModeInteractive)

	view := stripANSI(m.View())
	for _, want := range []string{"[running", "[idle]", "[permission]", "[ask]"} {
		if !strings.Contains(view, want) {
			t.Errorf("View should contain badge %q:\n%s", want, view)
		}
	}
}

func TestView_NoBadgeWhenNoPaneState(t *testing.T) {
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "plain"}, PaneState: nil},
	}
	m := newTestModel(items, 1, ModeInteractive)

	view := stripANSI(m.View())
	for _, badge := range []string{"[running", "[idle]", "[permission]", "[ask]"} {
		if strings.Contains(view, badge) {
			t.Errorf("View should NOT contain badge %q when PaneState is nil:\n%s", badge, view)
		}
	}
}

func TestView_PassiveModeHint(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModePassive)

	view := stripANSI(m.View())
	// passive モードでは [passive] インジケータが表示される
	if !strings.Contains(view, "[passive]") {
		t.Errorf("View should show [passive] indicator:\n%s", view)
	}
	// passive モードではカーソル ▶ は表示されない
	if strings.Contains(view, "▶") {
		t.Errorf("View should NOT show '▶' cursor in passive mode:\n%s", view)
	}
}

func TestView_InteractiveModeHint(t *testing.T) {
	m := newTestModel(sampleItems(), 1, ModeInteractive)

	view := stripANSI(m.View())
	// interactive モードでは [interactive] インジケータが表示される
	if !strings.Contains(view, "[interactive]") {
		t.Errorf("View should show [interactive] indicator:\n%s", view)
	}
}

func TestView_RunningBadgeShowsMinutes(t *testing.T) {
	elapsed := 5 * time.Minute
	runState := state.PaneState{Status: state.StatusRunning, Elapsed: elapsed}
	items := []ListItem{
		{Kind: ItemSession, SessionName: "s"},
		{Kind: ItemWindow, SessionName: "s", Window: &tmux.Window{ID: "@1", Index: 0, Name: "w"}, PaneState: &runState},
	}
	m := newTestModel(items, 1, ModeInteractive)

	view := stripANSI(m.View())
	want := "[running 5m]"
	if !strings.Contains(view, want) {
		t.Errorf("View should contain %q:\n%s", want, view)
	}
}
