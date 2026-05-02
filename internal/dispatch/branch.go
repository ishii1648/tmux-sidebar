package dispatch

import (
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
