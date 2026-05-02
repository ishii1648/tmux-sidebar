package picker

import (
	"errors"
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ishii1648/tmux-sidebar/internal/dispatch"
	"github.com/ishii1648/tmux-sidebar/internal/repo"
)

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
	ctx := Context{Sessions: []SessionInfo{{Name: "foo", Path: "/r/foo"}}}
	m := New(ctx, sampleRepos(), runner)

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
	m := New(Context{}, sampleRepos(), runner)
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
	m := New(Context{}, sampleRepos(), runner)
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
	if runner.dispatchOpts.Branch != "feat/add-thing" {
		t.Errorf("Branch = %q", runner.dispatchOpts.Branch)
	}
	if !runner.dispatchOpts.Switch {
		t.Errorf("Switch should be true (picker controls switch ordering)")
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
	m := New(Context{}, sampleRepos(), &fakeRunner{})
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
	m := New(Context{}, sampleRepos(), runner)
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
	m := New(Context{}, sampleRepos(), runner)
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
			m := New(Context{}, sampleRepos(), &fakeRunner{})
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
	m := New(Context{}, sampleRepos(), runner)
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
	m := New(Context{}, sampleRepos(), runner)
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
	m := New(Context{}, sampleRepos(), runner)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEscape})
	if !m.quitting {
		t.Fatal("expected quitting after Esc on repo step")
	}
	if len(runner.calls) != 0 {
		t.Errorf("unexpected calls: %v", runner.calls)
	}
}

func TestPickerEscFromPromptReturnsToRepo(t *testing.T) {
	m := New(Context{}, sampleRepos(), &fakeRunner{})
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
	ctx := Context{Sessions: []SessionInfo{{Name: "foo"}}}
	m := New(ctx, sampleRepos(), runner)
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
	m := New(Context{}, sampleRepos(), runner)
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
	if runner.dispatchOpts.Branch != "feat/line-one" {
		t.Errorf("Branch = %q want feat/line-one (first line slug)", runner.dispatchOpts.Branch)
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
	m := New(Context{}, sampleRepos(), &fakeRunner{})
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
	m := New(Context{}, sampleRepos(), &fakeRunner{})
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
