package dispatch

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GhqRoot returns `ghq root`'s output, falling back to ~/ghq when ghq is
// unavailable. Cached on first call would be reasonable but the cost is low
// and dispatch.sh re-resolves on each invocation.
func GhqRoot() string {
	if out, err := exec.Command("ghq", "root").Output(); err == nil {
		root := strings.TrimSpace(string(out))
		if root != "" {
			return root
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "ghq")
	}
	return ""
}

// ResolveRepo expands an input that may be a directory path or a ghq short
// name (e.g. "owner/name", optionally without "github.com/") into an absolute
// repo path. Mirrors dispatch.sh's resolve_repo.
func ResolveRepo(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("repo argument is empty")
	}
	if info, err := os.Stat(input); err == nil && info.IsDir() {
		abs, _ := filepath.Abs(input)
		return abs, nil
	}
	root := GhqRoot()
	if root == "" {
		return "", fmt.Errorf("cannot resolve repo: ghq root unavailable")
	}
	candidates := []string{
		filepath.Join(root, "github.com", input),
		filepath.Join(root, input),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("repo not found under ghq: %s", input)
}

// NoWorktreeConfigPath returns the path to the no-worktree-repos config.
func NoWorktreeConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "dispatch", "no-worktree-repos")
	}
	return ""
}

// MatchesNoWorktreeConfig reports whether repoShort (the ghq-relative name
// such as "github.com/owner/name") is listed in the no-worktree config file.
// Missing file is not an error — returns false.
func MatchesNoWorktreeConfig(repoShort string) bool {
	path := NoWorktreeConfigPath()
	if path == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == repoShort {
			return true
		}
	}
	return false
}

// repoShortFromPath returns the path relative to ghqRoot. Returns the
// absolute path unchanged when it isn't under ghqRoot (caller can still try
// matching against the literal value).
func repoShortFromPath(repoPath, ghqRoot string) string {
	if ghqRoot == "" {
		return repoPath
	}
	rel, err := filepath.Rel(ghqRoot, repoPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return repoPath
	}
	return rel
}
