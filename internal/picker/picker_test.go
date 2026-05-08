package picker

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ishii1648/tmux-sidebar/internal/dispatch"
	"github.com/ishii1648/tmux-sidebar/internal/repo"
	"github.com/muesli/termenv"
)

// TestMain pins lipgloss's color profile so styled spans (notably the
// reverse-video block cursor) emit SGR escapes inside `go test`, where
// stdout isn't a TTY and lipgloss would otherwise downgrade to ASCII and
// drop styling. Tests still call stripANSI / renderForTest to assert
// against plain text.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

// ansiRE strips lipgloss-emitted SGR escapes so renderPromptInput's
// visual layout can be asserted as plain runes.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// renderForTest wraps the prompt block-cursor span with `<X>` markers so
// the cursor's position is observable as plain text after the rest of
// the SGR escapes are stripped. Computes the cursor span's prefix and
// suffix from a live `styleCursorBlock.Render(\x00)` call so the test
// helper stays correct regardless of which SGR codes lipgloss chooses
// for the configured colour profile (4-bit `\x1b[44m` vs 256-colour
// `\x1b[48;5;n` vs RGB `\x1b[48;2;r;g;b`).
func renderForTest(s string) string {
	sample := styleCursorBlock.Render("\x00")
	sentinel := strings.IndexByte(sample, 0)
	if sentinel <= 0 || sentinel >= len(sample)-1 {
		return stripANSI(s)
	}
	prefix, suffix := sample[:sentinel], sample[sentinel+1:]

	var b strings.Builder
	for {
		i := strings.Index(s, prefix)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		rest := s[i+len(prefix):]
		j := strings.Index(rest, suffix)
		if j < 0 {
			b.WriteString(prefix)
			b.WriteString(rest)
			break
		}
		b.WriteString("<")
		b.WriteString(rest[:j])
		b.WriteString(">")
		s = rest[j+len(suffix):]
	}
	return stripANSI(b.String())
}

// fakeRunner records every call so assertions can verify side-effects.
type fakeRunner struct {
	calls        []string
	switchErr    error
	dispatchErr  error
	dispatchOpts dispatch.Options
}

func (f *fakeRunner) SwitchClient(name string) error {
	f.calls = append(f.calls, "switch-client "+name)
	return f.switchErr
}
func (f *fakeRunner) SpawnDispatch(opts dispatch.Options) error {
	f.calls = append(f.calls, "spawn-dispatch "+opts.Repo+" branch="+opts.Branch+" launcher="+string(opts.Launcher))
	f.dispatchOpts = opts
	return f.dispatchErr
}

func sampleRepos() []repo.Repo {
	return []repo.Repo{
		{Path: "/r/foo", Name: "github.com/x/foo", Basename: "foo"},
		{Path: "/r/bar", Name: "github.com/x/bar", Basename: "bar"},
	}
}

func TestPickerExistingSessionSwitches(t *testing.T) {
	runner := &fakeRunner{}
	m := New(sampleRepos(), []string{"foo"}, runner)

	// repos sort by Basename → bar, foo. Move cursor to "foo".
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.quitting {
		t.Fatalf("expected picker to quit after switch")
	}
	if got, want := runner.calls, []string{"switch-client foo"}; !equalSlice(got, want) {
		t.Errorf("calls = %v want %v", got, want)
	}
}

func TestPickerNewRepoAdvancesToPromptStep(t *testing.T) {
	runner := &fakeRunner{}
	m := New(sampleRepos(), nil, runner)
	// cursor on "bar" (first after sort), no open session → Enter advances
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.step != stepPrompt {
		t.Fatalf("step = %v want stepPrompt", m.step)
	}
	if m.quitting {
		t.Fatal("should not quit on Step 1 advance")
	}
	if len(runner.calls) != 0 {
		t.Errorf("no runner calls expected on advance, got %v", runner.calls)
	}
}

func TestPickerDispatchFlowClaude(t *testing.T) {
	runner := &fakeRunner{}
	m := New(sampleRepos(), nil, runner)
	// Step 1: Enter on "bar" → prompt step (claude is the default launcher)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range []rune("add") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeySpace})
	for _, r := range []rune("thing") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, cmd := updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.quitting {
		t.Fatalf("expected picker to quit immediately after spawn-dispatch")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}
	if runner.dispatchOpts.Repo != "/r/bar" {
		t.Errorf("Repo = %q", runner.dispatchOpts.Repo)
	}
	if runner.dispatchOpts.Launcher != dispatch.LauncherClaude {
		t.Errorf("Launcher = %q want claude", runner.dispatchOpts.Launcher)
	}
	// Branch is left empty in the normal flow so the spawned dispatch
	// process can derive it (claude -p with slugify fallback) without
	// blocking the popup. Checkout mode is what carries an explicit
	// Branch — see TestPickerCheckoutMode.
	if runner.dispatchOpts.Branch != "" {
		t.Errorf("Branch = %q want empty (named by dispatch process)", runner.dispatchOpts.Branch)
	}
	// Picker no longer auto-switches the calling client into the new
	// session — keeping the user's current work in focus is preferred
	// over the convenience of jumping into the freshly dispatched
	// session. The display-message from dispatch is the success signal.
	if runner.dispatchOpts.Switch {
		t.Errorf("Switch should be false (picker must not hijack the user's current pane)")
	}
	// Prompt is shipped via PromptFile (not the literal Prompt field) so
	// shell quoting in run-shell -b can't mangle newlines / specials.
	if runner.dispatchOpts.PromptFile == "" {
		t.Fatalf("PromptFile should be set; opts = %+v", runner.dispatchOpts)
	}
	t.Cleanup(func() { os.Remove(runner.dispatchOpts.PromptFile) })
	body, err := os.ReadFile(runner.dispatchOpts.PromptFile)
	if err != nil {
		t.Fatalf("read prompt file: %v", err)
	}
	if string(body) != "add thing" {
		t.Errorf("prompt file body = %q want \"add thing\"", body)
	}
	if runner.dispatchOpts.Prompt != "" {
		t.Errorf("Prompt should be empty (passed via file): %q", runner.dispatchOpts.Prompt)
	}
}

func TestPickerTabTogglesLauncherStepRepo(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	if m.launcher != dispatch.LauncherClaude {
		t.Fatalf("default launcher = %q want claude", m.launcher)
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.launcher != dispatch.LauncherCodex {
		t.Errorf("after Tab launcher = %q want codex", m.launcher)
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.launcher != dispatch.LauncherClaude {
		t.Errorf("after second Tab launcher = %q want claude", m.launcher)
	}
}

func TestPickerTabTogglesLauncherStepPrompt(t *testing.T) {
	runner := &fakeRunner{}
	m := New(sampleRepos(), nil, runner)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyTab})   // claude → codex
	if m.launcher != dispatch.LauncherCodex {
		t.Fatalf("launcher = %q want codex", m.launcher)
	}
	for _, r := range []rune("hi") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if runner.dispatchOpts.Launcher != dispatch.LauncherCodex {
		t.Errorf("dispatched launcher = %q want codex", runner.dispatchOpts.Launcher)
	}
	t.Cleanup(func() { os.Remove(runner.dispatchOpts.PromptFile) })
}

func TestPickerCheckoutMode(t *testing.T) {
	runner := &fakeRunner{}
	m := New(sampleRepos(), nil, runner)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	for _, r := range []rune(":existing-branch") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})

	if runner.dispatchOpts.Branch != "existing-branch" {
		t.Errorf("Branch = %q want existing-branch", runner.dispatchOpts.Branch)
	}
	if !runner.dispatchOpts.NoPrompt {
		t.Errorf("NoPrompt should be true for checkout mode")
	}
	if runner.dispatchOpts.PromptFile != "" {
		t.Errorf("PromptFile should be empty in checkout mode: %q", runner.dispatchOpts.PromptFile)
	}
}

func TestPickerNewlineKeysInsertNewline(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyMsg
	}{
		{"ctrl+j", tea.KeyMsg{Type: tea.KeyCtrlJ}},
		// Note: shift+enter / alt+enter are tested via msg.String() in
		// isNewlineKey, which can't be exercised by constructing a
		// KeyMsg directly without runtime parsing — terminal-dependent
		// behaviour is documented and verified manually.
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(sampleRepos(), nil, &fakeRunner{})
			m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt
			for _, r := range []rune("first") {
				m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			m, _ = updateAsModel(m, c.key)
			for _, r := range []rune("second") {
				m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			if m.prompt != "first\nsecond" {
				t.Errorf("prompt = %q want \"first\\nsecond\"", m.prompt)
			}
			if m.quitting {
				t.Errorf("newline key must not start dispatch (quitting=%v)", m.quitting)
			}
		})
	}
}

func TestPickerSpawnErrorShownNotQuit(t *testing.T) {
	runner := &fakeRunner{dispatchErr: errors.New("run-shell boom")}
	m := New(sampleRepos(), nil, runner)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt
	for _, r := range []rune("x") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.quitting {
		t.Fatal("should not quit when SpawnDispatch fails")
	}
	if m.errMsg == "" {
		t.Fatal("expected error message")
	}
}

func TestPickerEmptyPromptShowsError(t *testing.T) {
	runner := &fakeRunner{}
	m := New(sampleRepos(), nil, runner)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // empty submit
	if m.errMsg == "" {
		t.Error("expected error for empty prompt")
	}
	if m.quitting {
		t.Error("should not quit on empty prompt")
	}
}

func TestPickerEscFromRepoQuits(t *testing.T) {
	runner := &fakeRunner{}
	m := New(sampleRepos(), nil, runner)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEscape})
	if !m.quitting {
		t.Fatal("expected quitting after Esc on repo step")
	}
	if len(runner.calls) != 0 {
		t.Errorf("unexpected calls: %v", runner.calls)
	}
}

func TestPickerEscFromPromptReturnsToRepo(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})  // → prompt
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEscape}) // back
	if m.step != stepRepo {
		t.Fatalf("step = %v want stepRepo", m.step)
	}
	if m.quitting {
		t.Fatal("should not quit on Esc from prompt step")
	}
}

func TestPickerSwitchErrorShownNotQuit(t *testing.T) {
	runner := &fakeRunner{switchErr: errors.New("boom")}
	m := New(sampleRepos(), []string{"foo"}, runner)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyDown}) // foo
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.quitting {
		t.Fatal("should not quit when switch fails")
	}
	if m.errMsg == "" {
		t.Fatal("expected error message")
	}
}

func TestPickerPasteNormalizesNewlines(t *testing.T) {
	runner := &fakeRunner{}
	m := New(sampleRepos(), nil, runner)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	// Simulate a bracketed-paste of multi-line content where the terminal
	// translated LF to CR (or sent CRLF). The picker should normalise both
	// to LF so dispatch.firstLine works.
	m, _ = updateAsModel(m, tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune("line one\rline two\r\nline three"),
		Paste: true,
	})
	if got := m.prompt; got != "line one\nline two\nline three" {
		t.Errorf("prompt = %q want \"line one\\nline two\\nline three\"", got)
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	// Branch is empty: naming runs in the spawned dispatch process. The
	// prompt file content (asserted below) is what carries the lines
	// that dispatch will use for both naming and the launcher input.
	if runner.dispatchOpts.Branch != "" {
		t.Errorf("Branch = %q want empty (named by dispatch process)", runner.dispatchOpts.Branch)
	}
	t.Cleanup(func() { os.Remove(runner.dispatchOpts.PromptFile) })
	body, err := os.ReadFile(runner.dispatchOpts.PromptFile)
	if err != nil {
		t.Fatalf("read prompt file: %v", err)
	}
	if string(body) != "line one\nline two\nline three" {
		t.Errorf("prompt file = %q (newlines must reach launcher)", body)
	}
}

func TestPickerNonPasteRunesNotNormalized(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	// A bare \r keystroke without Paste should land verbatim — this
	// shouldn't happen in practice (terminals send Enter for CR), but the
	// guard documents the intent.
	m, _ = updateAsModel(m, tea.KeyMsg{
		Type:  tea.KeyRunes,
		Runes: []rune{'\r'},
	})
	if m.prompt != "\r" {
		t.Errorf("prompt = %q, normalisation should be paste-only", m.prompt)
	}
}

func TestFuzzyFilterAndCursorReset(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	for _, r := range []rune("ba") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.filtered) != 1 || m.filtered[0].Basename != "bar" {
		t.Fatalf("filtered = %+v", m.filtered)
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if len(m.filtered) != 2 {
		t.Fatalf("after backspace filtered = %+v", m.filtered)
	}
}

// TestPromptCursorLeftRightMovesAndInserts exercises the left/right arrow
// keys in stepPrompt. The pre-fix code ignored these keys entirely and the
// cursor was pinned to the end of the buffer, so users could not edit
// earlier characters or insert in the middle of an existing prompt.
func TestPromptCursorLeftRightMovesAndInserts(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	for _, r := range []rune("abc") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.promptCursor != 3 {
		t.Fatalf("after typing abc cursor = %d want 3", m.promptCursor)
	}
	// Move cursor left twice → between 'a' and 'b'.
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.promptCursor != 1 {
		t.Fatalf("after 2x Left cursor = %d want 1", m.promptCursor)
	}
	// Insert 'X' at the cursor → prompt should be "aXbc".
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	if m.prompt != "aXbc" {
		t.Errorf("prompt = %q want \"aXbc\"", m.prompt)
	}
	if m.promptCursor != 2 {
		t.Errorf("cursor = %d want 2 (after the inserted X)", m.promptCursor)
	}
	// Right past end is clamped — cursor should stop at len("aXbc")=4.
	for i := 0; i < 10; i++ {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.promptCursor != 4 {
		t.Errorf("after right past end cursor = %d want 4", m.promptCursor)
	}
	// Left past start is clamped at 0.
	for i := 0; i < 10; i++ {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	}
	if m.promptCursor != 0 {
		t.Errorf("after left past start cursor = %d want 0", m.promptCursor)
	}
}

// TestPromptCursorBackspaceAndDelete exercises Backspace and Delete with a
// mid-buffer cursor — both must respect the cursor position rather than
// always trimming the tail of the buffer.
func TestPromptCursorBackspaceAndDelete(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	for _, r := range []rune("abcde") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Move cursor to between 'b' and 'c' (position 2).
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.promptCursor != 2 {
		t.Fatalf("cursor = %d want 2", m.promptCursor)
	}
	// Backspace removes 'b' — prompt becomes "acde", cursor decrements.
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.prompt != "acde" {
		t.Errorf("after backspace prompt = %q want \"acde\"", m.prompt)
	}
	if m.promptCursor != 1 {
		t.Errorf("after backspace cursor = %d want 1", m.promptCursor)
	}
	// Delete removes 'c' (the rune to the right) — prompt becomes "ade",
	// cursor unchanged.
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyDelete})
	if m.prompt != "ade" {
		t.Errorf("after delete prompt = %q want \"ade\"", m.prompt)
	}
	if m.promptCursor != 1 {
		t.Errorf("after delete cursor = %d want 1", m.promptCursor)
	}
	// Backspace at the start of the buffer is a no-op.
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyHome})
	if m.promptCursor != 0 {
		t.Fatalf("after Home cursor = %d want 0", m.promptCursor)
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.prompt != "ade" || m.promptCursor != 0 {
		t.Errorf("backspace at start should be no-op; got prompt=%q cursor=%d", m.prompt, m.promptCursor)
	}
	// End jumps to len(prompt). Delete past end is a no-op.
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnd})
	if m.promptCursor != 3 {
		t.Fatalf("after End cursor = %d want 3", m.promptCursor)
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyDelete})
	if m.prompt != "ade" || m.promptCursor != 3 {
		t.Errorf("delete past end should be no-op; got prompt=%q cursor=%d", m.prompt, m.promptCursor)
	}
}

// TestPromptCursorMultiByteMovement ensures arrow keys move by runes, not
// bytes, so Japanese (3 UTF-8 bytes per rune) is not split mid-codepoint.
func TestPromptCursorMultiByteMovement(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	for _, r := range []rune("こんにちは") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.promptCursor != 5 {
		t.Fatalf("cursor = %d want 5", m.promptCursor)
	}
	// Two Lefts → cursor between に(2) and ち(3).
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.promptCursor != 3 {
		t.Fatalf("cursor = %d want 3", m.promptCursor)
	}
	// Insert 'X' between に and ち → "こんにXちは".
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	if m.prompt != "こんにXちは" {
		t.Errorf("prompt = %q want %q", m.prompt, "こんにXちは")
	}
	if m.promptCursor != 4 {
		t.Errorf("cursor = %d want 4", m.promptCursor)
	}
	// Backspace removes the multi-byte に, not just one byte.
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyLeft}) // → between に and X
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.prompt != "こんXちは" {
		t.Errorf("prompt = %q want %q", m.prompt, "こんXちは")
	}
}

// TestRenderPromptInputCursorMidBuffer asserts the cursor glyph lands at
// the right column when the cursor is *not* at the end of the buffer —
// the case the pre-fix code couldn't render at all.
func TestRenderPromptInputCursorMidBuffer(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		cursor int
		width  int
		want   string
	}{
		{
			name:   "cursor at start of single-line buffer",
			prompt: "hello",
			cursor: 0,
			width:  80,
			want:   "  > <h>ello\n",
		},
		{
			name:   "cursor in the middle of a single line",
			prompt: "hello",
			cursor: 2,
			width:  80,
			want:   "  > he<l>lo\n",
		},
		{
			name:   "cursor at soft-wrap boundary lands on next line",
			prompt: "abcdefghijkl",
			cursor: 8, // boundary between segments "abcdefgh" and "ijkl"
			width:  14,
			want:   "  > abcdefgh\n      <i>jkl\n",
		},
		{
			name:   "cursor at hard-break boundary lands at end of prev line",
			prompt: "abc\ndef",
			cursor: 3, // immediately before the '\n' — no rune at cursor, falls back to ▏
			width:  80,
			want:   "  > abc▏\n    │ def\n",
		},
		{
			name:   "cursor at start of second logical line",
			prompt: "abc\ndef",
			cursor: 4, // immediately after the '\n'
			width:  80,
			want:   "  > abc\n    │ <d>ef\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderForTest(renderPromptInput(tc.prompt, tc.cursor, tc.width))
			if got != tc.want {
				t.Errorf("renderPromptInput\n got=%q\nwant=%q", got, tc.want)
			}
		})
	}
}

// TestPromptBackspaceMultiByteRune ensures backspace deletes one rune (not one
// byte) so multi-byte input like Japanese is not corrupted into replacement
// glyphs (e.g. ◆) on the next render.
func TestPromptBackspaceMultiByteRune(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	for _, r := range []rune("こんにちは") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.prompt != "こんにち" {
		t.Errorf("prompt = %q want %q", m.prompt, "こんにち")
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.prompt != "" {
		t.Errorf("prompt = %q want empty after deleting all runes", m.prompt)
	}
}

// TestRepoQueryBackspaceMultiByteRune mirrors the prompt test for the repo
// filter input on Step 1.
func TestRepoQueryBackspaceMultiByteRune(t *testing.T) {
	m := New(sampleRepos(), nil, &fakeRunner{})
	for _, r := range []rune("あい") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.query != "あ" {
		t.Errorf("query = %q want %q", m.query, "あ")
	}
}

func TestRenderPromptInputWrap(t *testing.T) {
	// width=14 ⇒ contentWidth=8; "abcdefgh" is exactly 8 cols.
	cases := []struct {
		name   string
		prompt string
		width  int
		want   string
	}{
		{
			name:   "empty prompt still draws prefix and cursor",
			prompt: "",
			width:  80,
			want:   "  > ▏\n",
		},
		{
			name:   "short single line — no wrap",
			prompt: "hello",
			width:  80,
			want:   "  > hello▏\n",
		},
		{
			name:   "soft wrap — single break",
			prompt: "abcdefghijkl",
			width:  14,
			want:   "  > abcdefgh\n      ijkl▏\n",
		},
		{
			name:   "hard break + soft wrap mix",
			prompt: "abcdefghijkl\nshort",
			width:  14,
			want:   "  > abcdefgh\n      ijkl\n    │ short▏\n",
		},
		{
			name:   "japanese wide chars wrap at column boundary",
			prompt: "あいうえお",
			width:  14, // contentWidth=8, 4 kanji fit, 5th wraps
			want:   "  > あいうえ\n      お▏\n",
		},
		{
			name:   "width=0 fallback — no wrap",
			prompt: "this is a long line that would have wrapped",
			width:  0,
			want:   "  > this is a long line that would have wrapped▏\n",
		},
		{
			name:   "trailing newline — empty continuation segment with cursor",
			prompt: "abc\n",
			width:  80,
			want:   "  > abc\n    │ ▏\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Existing cases assume the cursor is at the end of the
			// buffer (legacy behavior before mid-line cursor support).
			cursor := len([]rune(tc.prompt))
			got := stripANSI(renderPromptInput(tc.prompt, cursor, tc.width))
			if got != tc.want {
				t.Errorf("renderPromptInput\n got=%q\nwant=%q", got, tc.want)
			}
		})
	}
}

func TestRenderPromptInputCursorOnLastSegmentOnly(t *testing.T) {
	// Two soft-wrapped segments should yield exactly one cursor glyph,
	// at the end of the trailing line.
	out := stripANSI(renderPromptInput("abcdefghij", 10, 14))
	if n := strings.Count(out, "▏"); n != 1 {
		t.Errorf("cursor count = %d want 1; output = %q", n, out)
	}
	if !strings.HasSuffix(strings.TrimSuffix(out, "\n"), "ij▏") {
		t.Errorf("cursor not at end of last segment: %q", out)
	}
}

func TestViewPromptSoftWrapSnapshot(t *testing.T) {
	// Drive the model into stepPrompt with a known width and verify the
	// wrapped layout reaches viewPrompt's output. Assertion stays
	// substring-based so unrelated layout (header / separator) changes
	// don't drag this test along.
	m := New(sampleRepos(), nil, &fakeRunner{})
	m.width = 14
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // → prompt step
	for _, r := range []rune("abcdefghijkl") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	out := stripANSI(m.viewPrompt())
	wantBlock := "  > abcdefgh\n      ijkl▏\n"
	if !strings.Contains(out, wantBlock) {
		t.Errorf("viewPrompt missing wrapped block %q in output:\n%s", wantBlock, out)
	}
}

// TestComputeRepoViewportKeepsCursorVisible exercises the pure window
// calculation directly so each scroll case is asserted in isolation. The
// viewport is the fix for "cursor invisible past XX more"; before the
// change, the cursor could move past maxRows but the renderer always
// rendered items[0..maxRows], so the active row disappeared from view.
func TestComputeRepoViewportKeepsCursorVisible(t *testing.T) {
	tests := []struct {
		name        string
		cursor      int
		total       int
		maxRows     int
		savedStart  int
		wantStart   int
		wantEnd     int
		wantHasUp   bool
		wantHasDown bool
	}{
		{
			name: "all fit no markers", cursor: 2, total: 5, maxRows: 10, savedStart: 0,
			wantStart: 0, wantEnd: 5, wantHasUp: false, wantHasDown: false,
		},
		{
			name: "cursor at top with overflow", cursor: 0, total: 20, maxRows: 5, savedStart: 0,
			wantStart: 0, wantEnd: 4, wantHasUp: false, wantHasDown: true,
		},
		{
			name: "cursor moves below window scrolls down", cursor: 4, total: 20, maxRows: 5, savedStart: 0,
			// hasUp + hasDown both reserved → 3 item slots, cursor must
			// be the last visible row.
			wantStart: 2, wantEnd: 5, wantHasUp: true, wantHasDown: true,
		},
		{
			name: "cursor at bottom no down marker", cursor: 19, total: 20, maxRows: 5, savedStart: 0,
			// At the last item: down marker drops, freeing one row.
			wantStart: 16, wantEnd: 20, wantHasUp: true, wantHasDown: false,
		},
		{
			name: "cursor moves above window scrolls up", cursor: 0, total: 20, maxRows: 5, savedStart: 10,
			wantStart: 0, wantEnd: 4, wantHasUp: false, wantHasDown: true,
		},
		{
			name: "cursor inside saved window is sticky", cursor: 12, total: 20, maxRows: 5, savedStart: 10,
			wantStart: 10, wantEnd: 13, wantHasUp: true, wantHasDown: true,
		},
		{
			name: "stale savedStart past end clamps", cursor: 0, total: 5, maxRows: 10, savedStart: 50,
			wantStart: 0, wantEnd: 5, wantHasUp: false, wantHasDown: false,
		},
		{
			name: "single-item past viewport is reachable", cursor: 4, total: 5, maxRows: 4, savedStart: 0,
			// total > maxRows by exactly one — cursor must still land
			// inside the visible slice without the renderer hiding it.
			wantStart: 2, wantEnd: 5, wantHasUp: true, wantHasDown: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end, hasUp, hasDown := computeRepoViewport(tc.cursor, tc.total, tc.maxRows, tc.savedStart)
			if start != tc.wantStart || end != tc.wantEnd || hasUp != tc.wantHasUp || hasDown != tc.wantHasDown {
				t.Errorf("computeRepoViewport(cursor=%d,total=%d,maxRows=%d,saved=%d) = (%d,%d,%v,%v) want (%d,%d,%v,%v)",
					tc.cursor, tc.total, tc.maxRows, tc.savedStart,
					start, end, hasUp, hasDown,
					tc.wantStart, tc.wantEnd, tc.wantHasUp, tc.wantHasDown)
			}
			if tc.cursor < tc.total {
				if tc.cursor < start || tc.cursor >= end {
					t.Errorf("cursor=%d not visible in [%d,%d)", tc.cursor, start, end)
				}
			}
		})
	}
}

// TestViewRepoScrollsCursorIntoView is the integration counterpart: drive
// the picker past the visible window via key presses and assert the
// rendered output contains the cursor row plus an "↑ N more" marker. The
// pre-fix renderer would emit "↓ N more" with the cursor invisible.
func TestViewRepoScrollsCursorIntoView(t *testing.T) {
	repos := make([]repo.Repo, 0, 30)
	for i := 0; i < 30; i++ {
		// Names sort by Basename → use zero-padded suffix so the
		// observed order matches the index here.
		base := "repo-" + zeroPad(i)
		repos = append(repos, repo.Repo{
			Path:     "/r/" + base,
			Name:     "github.com/x/" + base,
			Basename: base,
		})
	}
	m := New(repos, nil, &fakeRunner{})
	// Force a small window so the scrolling threshold is easy to hit.
	m.height = 9 // viewportRows = 9 - 5 = 4
	// Move cursor past maxRows so the renderer must scroll to keep it
	// visible. After 10 KeyDown presses cursor=10, well beyond the
	// initial 4-row window.
	for i := 0; i < 10; i++ {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != 10 {
		t.Fatalf("cursor = %d want 10", m.cursor)
	}
	out := stripANSI(m.viewRepo())
	cursorLine := "▶ github.com/x/repo-" + zeroPad(10)
	if !strings.Contains(out, cursorLine) {
		t.Errorf("cursor row missing from view; output:\n%s", out)
	}
	if !strings.Contains(out, "↑ ") {
		t.Errorf("expected up marker since window scrolled; output:\n%s", out)
	}
	if !strings.Contains(out, "↓ ") {
		t.Errorf("expected down marker (still 19 items below); output:\n%s", out)
	}
}

func zeroPad(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	tens := i / 10
	ones := i % 10
	return string(rune('0'+tens)) + string(rune('0'+ones))
}

// updateAsModel calls Update and casts the returned tea.Model back to *Model.
func updateAsModel(m *Model, msg tea.Msg) (*Model, tea.Cmd) {
	out, cmd := m.Update(msg)
	return out.(*Model), cmd
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
