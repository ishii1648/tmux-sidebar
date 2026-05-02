package picker

import (
	"errors"
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
	dispatchName string // session name returned from Dispatch
	dispatchOpts dispatch.Options
}

func (f *fakeRunner) SwitchClient(name string) error {
	f.calls = append(f.calls, "switch-client "+name)
	return f.switchErr
}
func (f *fakeRunner) Dispatch(opts dispatch.Options) (string, error) {
	f.calls = append(f.calls, "dispatch "+opts.Repo+" branch="+opts.Branch+" launcher="+string(opts.Launcher))
	f.dispatchOpts = opts
	if f.dispatchErr != nil {
		return "", f.dispatchErr
	}
	if f.dispatchName == "" {
		return "fake-session", nil
	}
	return f.dispatchName, nil
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
	runner := &fakeRunner{dispatchName: "bar@feat-add-thing"}
	m := New(Context{}, sampleRepos(), runner)
	// Step 1: Enter on "bar" → prompt step (claude is the default launcher)
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	// Type "add thing"
	for _, r := range []rune("add") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeySpace})
	for _, r := range []rune("thing") {
		m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = updateAsModel(m, tea.KeyMsg{Type: tea.KeyEnter})

	if !m.quitting {
		t.Fatalf("expected quit after dispatch")
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
	if runner.dispatchOpts.Prompt != "add thing" {
		t.Errorf("Prompt = %q", runner.dispatchOpts.Prompt)
	}
	if !runner.dispatchOpts.Switch {
		t.Errorf("Switch should be true (picker controls switch ordering)")
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
}

func TestPickerCheckoutMode(t *testing.T) {
	runner := &fakeRunner{dispatchName: "bar@existing"}
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
	runner := &fakeRunner{dispatchName: "bar@feat-line-one"}
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
	if runner.dispatchOpts.Prompt != "line one\nline two\nline three" {
		t.Errorf("Prompt = %q (newlines must reach launcher)", runner.dispatchOpts.Prompt)
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
