package picker

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ishii1648/tmux-sidebar/internal/dispatch"
	"github.com/ishii1648/tmux-sidebar/internal/repo"
)

// ansiRE strips lipgloss-emitted SGR escapes so renderPromptInput's
// visual layout can be asserted as plain runes.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

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
			got := stripANSI(renderPromptInput(tc.prompt, tc.width))
			if got != tc.want {
				t.Errorf("renderPromptInput\n got=%q\nwant=%q", got, tc.want)
			}
		})
	}
}

func TestRenderPromptInputCursorOnLastSegmentOnly(t *testing.T) {
	// Two soft-wrapped segments should yield exactly one cursor glyph,
	// at the end of the trailing line.
	out := stripANSI(renderPromptInput("abcdefghij", 14))
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
