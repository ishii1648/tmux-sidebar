package dispatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// BranchFromPrompt produces a "feat/<slug>" branch name from the first line
// of prompt. Non-alphanumeric runes collapse into single dashes; the slug is
// truncated to 40 chars and lower-cased. Empty / non-ASCII-only prompts fall
// back to a timestamp slug so the output is always a valid branch name.
//
// Mirrors dispatch_launcher.fish's slug derivation so users get the same
// branch names regardless of which launcher path they use.
func BranchFromPrompt(prompt string) string {
	first := firstLine(prompt)
	slug := slugify(first)
	if slug == "" {
		slug = time.Now().Format("20060102-150405")
	}
	return "feat/" + slug
}

// ParseBranchPrefix detects the `:<branch>` checkout-mode prefix. When the
// trimmed first line starts with `:`, the rest of that line is interpreted as
// the branch name to check out (no new branch is created), and the remaining
// prompt body is whatever followed the first line. checkoutMode=true signals
// that the caller should pass --no-prompt to Launch (the user wants a worktree
// with the launcher idle, not a primed prompt).
func ParseBranchPrefix(prompt string) (branch string, checkoutMode bool, rest string) {
	first := firstLine(prompt)
	trimmed := strings.TrimSpace(first)
	if !strings.HasPrefix(trimmed, ":") {
		return "", false, prompt
	}
	branch = strings.TrimSpace(strings.TrimPrefix(trimmed, ":"))
	if branch == "" {
		return "", false, prompt
	}
	rest = ""
	if i := strings.IndexByte(prompt, '\n'); i >= 0 {
		rest = prompt[i+1:]
	}
	return branch, true, rest
}

// firstLine returns the leading line of s, treating any of \n, \r\n, or \r
// as a line break. The picker normalises paste content to \n before this
// runs; the \r support is defense-in-depth for direct CLI / API callers
// that may pass terminal-flavoured input.
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}

var slugRE = regexp.MustCompile(`-+`)

// slugify mirrors `string replace -ar '[^a-zA-Z0-9]' '-' | string replace -ar -- '-+' '-' | string trim -c '-' | string sub -l 40 | string lower` from dispatch_launcher.fish.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isASCIIAlphaNum(r) {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteByte('-')
		}
	}
	out := slugRE.ReplaceAllString(b.String(), "-")
	out = strings.Trim(out, "-")
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

func isASCIIAlphaNum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// Namer derives a git branch name from a task prompt. The dispatch.Launch
// flow calls this when the caller didn't pre-decide a branch (the popup
// picker leaves Branch empty so naming runs in the background dispatch
// process rather than blocking the popup). Implementations may shell out
// to a CLI (`claude -p`) or call an SDK; failures should be returned so
// callers can fall back to the deterministic BranchFromPrompt slug.
type Namer interface {
	Name(ctx context.Context, prompt string) (string, error)
}

// branchShapeRE caps Namer outputs at lengths that fit a 40-column tmux
// sidebar alongside a typical 8-14 char repo basename. Anything outside
// this shape is rejected and DeriveBranch falls back to slugify.
var branchShapeRE = regexp.MustCompile(`^(feat|fix|chore)/[a-z0-9][a-z0-9-]{1,24}$`)

// DeriveBranch picks a branch name for prompt. namer (when non-nil) is
// tried first; on any error or output that doesn't match branchShapeRE,
// the deterministic BranchFromPrompt slug is returned. Failures are
// silent — dispatch must never abort because naming was unavailable.
func DeriveBranch(ctx context.Context, namer Namer, prompt string) string {
	if namer != nil {
		name, err := namer.Name(ctx, prompt)
		if err == nil && branchShapeRE.MatchString(name) {
			return name
		}
	}
	return BranchFromPrompt(prompt)
}

// claudeBranchSystemPrompt instructs the model to emit one short
// type/slug branch name. The length budget is dictated by the 40-column
// tmux sidebar: session headers render as `▾ <repo>@<branch-with-/-as->`,
// and the column has to swallow the whole thing without wrapping.
const claudeBranchSystemPrompt = `You name git branches from a developer task prompt.

Output exactly one line, no prose, no quotes, no trailing punctuation:
    <type>/<slug>

Where:
- <type> is one of: feat (new feature), fix (bug fix), chore (refactor / config / maintenance)
- <slug> is 2-3 short hyphen-separated lowercase ASCII words

Length budget:
- The whole branch name (including the prefix) must fit a 40-column tmux
  sidebar alongside the repo basename. Aim for <=20 characters total;
  hard limit is 25.
- ASCII only. If the prompt is in another language, translate the gist
  into short English keywords for the slug.

Examples:
    "tenant module を stg/prod に追加" → feat/add-tenant-module
    "CI の flaky test を修正" → fix/flaky-ci-test
    "依存ライブラリを更新" → chore/update-deps
`

// ClaudeNamer derives branch names by shelling out to `claude -p
// --system-prompt <prompt> <user>`. The claude CLI must be installed and
// authenticated; on any failure (missing CLI, auth error, timeout,
// malformed output) Name returns an error so DeriveBranch falls back
// to BranchFromPrompt.
type ClaudeNamer struct {
	// Timeout caps the claude invocation. Defaults to 5 seconds when zero.
	Timeout time.Duration
}

// Name calls `claude -p --system-prompt <branch-naming-prompt> <user>`
// and returns the last non-empty line of stdout when it matches
// branchShapeRE. The system-prompt isolates the model from any other
// CLAUDE.md / hook context so the same user prompt produces stable
// names regardless of where the dispatch process runs.
func (c ClaudeNamer) Name(ctx context.Context, prompt string) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "-p",
		"--system-prompt", claudeBranchSystemPrompt, prompt)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude -p: %w", err)
	}
	line := lastNonEmptyLine(stdout.String())
	if line == "" {
		return "", errors.New("claude -p: empty output")
	}
	if !branchShapeRE.MatchString(line) {
		return "", fmt.Errorf("invalid branch shape: %q", line)
	}
	return line, nil
}

// lastNonEmptyLine returns the trailing non-blank line of s. Used to
// salvage a branch name when claude prefixes its output with stray
// preamble (rare but possible despite the system prompt).
func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" {
			return l
		}
	}
	return ""
}
