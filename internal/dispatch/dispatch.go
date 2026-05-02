// Package dispatch is the Go port of dotfiles' dispatch.sh launch logic.
//
// It creates a git worktree (optional), spawns a tmux session/window in the
// resulting directory, writes the user's prompt to a file, and starts the
// chosen launcher (claude / codex) with that prompt piped in. dispatch.sh
// remains the canonical specification — this package replicates its
// observable behavior so the same commands work whether invoked from a
// Claude skill, the dotfiles popup launcher, or tmux-sidebar's picker.
//
// The split between the slash-command skill and the launcher is intentional:
// the LLM owns prompt understanding (branch naming, in-session decisions)
// while the launcher is purely deterministic plumbing. Reusing this Go
// implementation from both sides prevents the two paths from drifting.
package dispatch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Launcher is the agent runtime to start in the new pane.
type Launcher string

const (
	LauncherClaude Launcher = "claude"
	LauncherCodex  Launcher = "codex"
)

// Options describe a single dispatch invocation.
type Options struct {
	// Repo is a ghq short name ("owner/name", optionally with leading
	// "github.com/") or an absolute repo path.
	Repo string
	// Prompt is the prompt body. Mutually exclusive with PromptFile.
	Prompt string
	// PromptFile is the path to a file containing the prompt. Consumed and
	// removed after the launcher is started.
	PromptFile string

	// Branch is the git branch for the new worktree. Required unless
	// NoWorktree is true. When the branch already exists (locally or on
	// origin) it is checked out; otherwise it's created off origin/<default>.
	Branch string

	// Session, when non-empty, sets the tmux session name. When empty the
	// worktree's basename is used. SessionExplicit suppresses the
	// auto-suffix-on-collision behavior so callers that genuinely want to
	// reuse an existing session can do so.
	Session         string
	SessionExplicit bool

	// Window, when non-empty, sets the tmux window name. Defaults to
	// basename(repo_path).
	Window string

	// Launcher selects the agent to start. Defaults to LauncherClaude.
	Launcher Launcher

	// NoWorktree skips git worktree creation and runs in the resolved repo
	// directory. dispatch_launcher.fish (and now this package) honor a
	// per-user list at ~/.config/dispatch/no-worktree-repos so frequently
	// used scratch repos can opt out by configuration.
	NoWorktree bool
	// NoPrompt skips writing a prompt file and just starts the launcher
	// idle. Used by `:<branch>` checkout-mode flows that want a primed
	// worktree without firing a request immediately.
	NoPrompt bool

	// Switch makes the calling tmux client switch to the new session right
	// after creation, before the launcher is started. The picker sets this
	// because it controls when the client should attach: switching first
	// also unblocks the codex `waitForAttachedClient` poll so the OSC 11
	// background-color query (ADR-065) can resolve. The dispatch.sh CLI
	// equivalent leaves this off and relies on the user attaching manually.
	Switch bool
}

// ToArgs returns the argv tail for `tmux-sidebar dispatch` that, when
// re-parsed, reconstructs an equivalent Options. Used by callers that
// fire-and-forget dispatch through `tmux run-shell -b` (the picker does
// this so its popup can close immediately while the worktree creation
// and tmux session setup run in the tmux-managed background).
//
// Prompt (literal text) is intentionally not serialised — pass it via
// PromptFile after writing to a tempfile with WriteTempPrompt instead,
// which avoids shell-quoting newlines and metacharacters.
func (o Options) ToArgs() []string {
	args := []string{o.Repo}
	if o.Launcher != "" {
		args = append(args, "--launcher", string(o.Launcher))
	}
	if o.Branch != "" {
		args = append(args, "--branch", o.Branch)
	}
	if o.Window != "" {
		args = append(args, "--window", o.Window)
	}
	if o.Session != "" {
		args = append(args, "--session", o.Session)
	}
	if o.PromptFile != "" {
		args = append(args, "--prompt-file", o.PromptFile)
	}
	if o.NoWorktree {
		args = append(args, "--no-worktree")
	}
	if o.NoPrompt {
		args = append(args, "--no-prompt")
	}
	if o.Switch {
		args = append(args, "--switch")
	}
	return args
}

// WriteTempPrompt writes prompt to a fresh tempfile under os.TempDir() and
// returns the path. Used by the picker before fire-and-forget dispatch:
// the spawned `tmux-sidebar dispatch --prompt-file <path>` reads this file
// and removes it after the launcher starts.
//
// Failures are returned to the caller so an error can surface in the
// picker UI before the popup closes.
func WriteTempPrompt(prompt string) (string, error) {
	f, err := os.CreateTemp("", "tmux-sidebar-prompt-*")
	if err != nil {
		return "", fmt.Errorf("create prompt file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(prompt); err != nil {
		return "", fmt.Errorf("write prompt file: %w", err)
	}
	return f.Name(), nil
}

// Result is the structured output of a successful Launch.
type Result struct {
	SessionName string
	WindowIndex int
	PaneID      string
	RepoPath    string
	WorkDir     string
	Branch      string
}

// Launch performs the dispatch. The returned Result captures what was created
// for callers that want to switch the client to it (the picker does this).
//
// namer derives a branch name from the prompt when opts.Branch is empty
// and a worktree is being created. Pass nil to fall through directly to
// BranchFromPrompt's deterministic slugify. Naming runs in this process
// (the spawned `tmux-sidebar dispatch`), not in the popup picker, so
// `claude -p` latency does not block the popup.
func Launch(opts Options, namer Namer) (*Result, error) {
	if err := validate(&opts); err != nil {
		return nil, err
	}

	if opts.PromptFile != "" {
		data, err := os.ReadFile(opts.PromptFile)
		if err != nil {
			return nil, fmt.Errorf("read prompt file: %w", err)
		}
		opts.Prompt = string(data)
	}

	repoPath, err := ResolveRepo(opts.Repo)
	if err != nil {
		return nil, err
	}

	// no-worktree-repos config: when the resolved repo is on the list,
	// override NoWorktree to true. We track the trigger separately because
	// the trigger affects post-resolution behavior (checkout default branch).
	configMatched := false
	if !opts.NoWorktree {
		short := repoShortFromPath(repoPath, GhqRoot())
		if MatchesNoWorktreeConfig(short) {
			opts.NoWorktree = true
			configMatched = true
		}
	}

	windowName := opts.Window
	if windowName == "" {
		windowName = filepath.Base(repoPath)
	}

	workDir := repoPath
	switch {
	case !opts.NoWorktree:
		if opts.Branch == "" {
			// The popup picker leaves Branch empty in the normal flow so
			// branch naming runs here (tmux run-shell -b background) and
			// stays off the popup's critical path. Checkout-mode and
			// other no-prompt callers must still supply Branch
			// explicitly because there is no prompt to name from.
			if opts.NoPrompt || strings.TrimSpace(opts.Prompt) == "" {
				return nil, fmt.Errorf("branch is required when worktree is enabled and no prompt is given")
			}
			opts.Branch = DeriveBranch(context.Background(), namer, opts.Prompt)
		}
		path, err := CreateWorktree(repoPath, opts.Branch)
		if err != nil {
			return nil, err
		}
		workDir = path
	case configMatched:
		workDir = CheckoutDefaultBranch(repoPath)
	}

	var promptFile string
	if !opts.NoPrompt {
		promptFile, err = writePromptFile(workDir, opts.Prompt)
		if err != nil {
			return nil, err
		}
	}

	sessionName := opts.Session
	if sessionName == "" {
		sessionName = filepath.Base(workDir)
	}
	if !opts.SessionExplicit {
		sessionName = uniqueSessionName(sessionName)
	}

	paneID, err := createTmuxTarget(sessionName, windowName, workDir)
	if err != nil {
		return nil, err
	}

	// Lock window naming so claude/codex setting their terminal title does
	// not overwrite the explicit window name.
	_ = exec.Command("tmux", "set-option", "-wt", paneID, "allow-rename", "off").Run()
	_ = exec.Command("tmux", "select-pane", "-t", paneID, "-T", windowName).Run()

	windowIndex := readWindowIndex(paneID)

	// Switch the calling client before the codex wait below — otherwise
	// `waitForAttachedClient` polls forever (no client is on this brand-new
	// session yet). See ADR-065: codex needs the OSC 11 background-color
	// query answered, which requires an attached client.
	if opts.Switch {
		if err := exec.Command("tmux", "switch-client", "-t", sessionName).Run(); err != nil {
			return nil, fmt.Errorf("tmux switch-client: %w", err)
		}
	}

	// Wait for the pane shell to come up before send-keys.
	time.Sleep(500 * time.Millisecond)

	if opts.Launcher == LauncherCodex {
		waitForAttachedClient(sessionName, 5*time.Minute)
	}

	if err := sendLauncherKeys(paneID, workDir, promptFile, opts.Launcher, opts.NoPrompt); err != nil {
		return nil, err
	}

	if opts.PromptFile != "" {
		_ = os.Remove(opts.PromptFile)
	}

	// Success is intentionally silent: the new session shows up in the
	// sidebar within the reload tick (≤10s, or instantly with the
	// SIGUSR1 hook), which is the source of truth. A short-lived
	// display-message would just duplicate that information for a few
	// seconds and add noise to the status line. Failures still surface
	// via display-message from main.go's runDispatch error handler.
	return &Result{
		SessionName: sessionName,
		WindowIndex: windowIndex,
		PaneID:      paneID,
		RepoPath:    repoPath,
		WorkDir:     workDir,
		Branch:      opts.Branch,
	}, nil
}

// ── internals ────────────────────────────────────────────────────────────────

func validate(opts *Options) error {
	if opts.Repo == "" {
		return fmt.Errorf("repo is required")
	}
	if opts.Launcher == "" {
		opts.Launcher = LauncherClaude
	}
	switch opts.Launcher {
	case LauncherClaude, LauncherCodex:
	default:
		return fmt.Errorf("unknown launcher: %s (want claude or codex)", opts.Launcher)
	}
	if !opts.NoPrompt && opts.Prompt == "" && opts.PromptFile == "" {
		return fmt.Errorf("prompt is required (or pass --no-prompt)")
	}
	if opts.Prompt != "" && opts.PromptFile != "" {
		return fmt.Errorf("prompt and prompt-file are mutually exclusive")
	}
	return nil
}

// writePromptFile creates `<workDir>/.outputs/claude/dispatch-prompt-XXXXXX`
// and writes prompt to it. Returns the path on success.
func writePromptFile(workDir, prompt string) (string, error) {
	outputDir := filepath.Join(workDir, ".outputs", "claude")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir prompt dir: %w", err)
	}
	f, err := os.CreateTemp(outputDir, "dispatch-prompt-")
	if err != nil {
		return "", fmt.Errorf("create prompt file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(prompt); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// uniqueSessionName appends an HHMMSS suffix until tmux has-session reports
// no conflict. Bounded to 100 attempts.
func uniqueSessionName(base string) string {
	if !sessionExists(base) {
		return base
	}
	for i := 0; i < 100; i++ {
		candidate := base + "-" + time.Now().Format("150405")
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", candidate, i+1)
		}
		if !sessionExists(candidate) {
			return candidate
		}
		time.Sleep(time.Second) // ensure HHMMSS changes for the next try
	}
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func sessionExists(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// createTmuxTarget creates the new session (or window if the named session
// already exists) at workDir and returns the new pane id.
func createTmuxTarget(sessionName, windowName, workDir string) (string, error) {
	if !sessionExists(sessionName) {
		w, h := currentTerminalSize()
		args := []string{
			"new-session", "-d", "-P", "-F", "#{pane_id}",
			"-s", sessionName, "-n", windowName, "-c", workDir,
			"-x", strconv.Itoa(w), "-y", strconv.Itoa(h),
		}
		out, err := exec.Command("tmux", args...).Output()
		if err != nil {
			return "", fmt.Errorf("tmux new-session: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	args := []string{
		"new-window", "-P", "-F", "#{pane_id}",
		"-t", "=" + sessionName + ":", "-n", windowName, "-c", workDir,
	}
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux new-window: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// currentTerminalSize reads the active client's window size so detached
// new-session creation does not size to a tiny default that later
// proportionally scales sidebar widths.
func currentTerminalSize() (int, int) {
	out, err := exec.Command("tmux", "display-message", "-p", "#{window_width}\n#{window_height}").Output()
	if err != nil {
		return 200, 50
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\n")
	w, h := 200, 50
	if len(parts) >= 1 {
		if n, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil && n > 0 {
			w = n
		}
	}
	if len(parts) >= 2 {
		if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil && n > 0 {
			h = n
		}
	}
	return w, h
}

func readWindowIndex(paneID string) int {
	out, err := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{window_index}").Output()
	if err != nil {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(string(out))); err == nil {
		return n
	}
	return 0
}

// waitForAttachedClient polls until at least one client is attached to the
// session, or the timeout elapses. Codex needs an attached client at startup
// (ADR-065 / openai/codex#4744) so its OSC 11 background-color query is
// answered; otherwise its input area renders without the proper background.
func waitForAttachedClient(sessionName string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := exec.Command("tmux", "list-clients", "-t", "="+sessionName).Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// sendLauncherKeys composes the cd + launcher invocation as a single shell
// line and feeds it to the new pane via send-keys.
func sendLauncherKeys(paneID, workDir, promptFile string, launcher Launcher, noPrompt bool) error {
	var line string
	switch {
	case noPrompt:
		line = fmt.Sprintf("cd %s; %s", shellQuote(workDir), launcher)
	case launcher == LauncherClaude:
		line = fmt.Sprintf("cd %s; claude < %s", shellQuote(workDir), shellQuote(promptFile))
	case launcher == LauncherCodex:
		line = fmt.Sprintf("cd %s; codex -C %s \"$(/bin/cat %s)\"", shellQuote(workDir), shellQuote(workDir), shellQuote(promptFile))
	default:
		return fmt.Errorf("unsupported launcher: %s", launcher)
	}
	return exec.Command("tmux", "send-keys", "-t", paneID, line, "Enter").Run()
}

// shellQuote single-quotes s and escapes embedded single quotes. Inputs are
// paths from os.CreateTemp / repo discovery / user config, so they generally
// have no metacharacters; the quoting is defensive.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
