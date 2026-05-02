// Package repo enumerates ghq-managed repositories and provides a small
// fuzzy filter for picker UIs.
package repo

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Repo is a single repository discovered under ghq.
type Repo struct {
	// Path is the absolute path to the repo working tree (output of `ghq list -p`).
	Path string
	// Name is the relative slug (e.g. "github.com/foo/bar"), output of `ghq list`.
	Name string
	// Basename is the last path segment, used as the default tmux session name
	// (e.g. "bar" for "github.com/foo/bar"). May contain "@feat-foo" suffix when
	// the repo is a git worktree.
	Basename string
}

// List returns all ghq-managed repositories. Empty result on missing ghq or
// empty repo set; errors only on unexpected ghq failure.
func List() ([]Repo, error) {
	if _, err := exec.LookPath("ghq"); err != nil {
		return nil, fmt.Errorf("ghq not found in PATH")
	}
	out, err := exec.Command("ghq", "list", "-p").Output()
	if err != nil {
		return nil, fmt.Errorf("ghq list -p: %w", err)
	}
	return parseList(string(out)), nil
}

// parseList converts `ghq list -p` output into Repo entries. Each line is an
// absolute path; we derive Name (last 3 segments) and Basename from the path.
//
// gw_add-style worktrees live alongside the main repo with `<basename>@<branch>`
// paths and are dropped here so the picker only offers main repos. Worktrees
// are reachable via `:<branch>` checkout-mode in dispatch instead.
func parseList(out string) []Repo {
	var repos []Repo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		basename := filepath.Base(line)
		if strings.Contains(basename, "@") {
			continue
		}
		repos = append(repos, Repo{
			Path:     line,
			Name:     deriveName(line),
			Basename: basename,
		})
	}
	return repos
}

// deriveName takes an absolute path like /Users/sho/ghq/github.com/foo/bar and
// returns the trailing host/owner/name slug. Falls back to filepath.Base on
// short paths.
func deriveName(absPath string) string {
	clean := filepath.ToSlash(absPath)
	parts := strings.Split(clean, "/")
	// Keep last 3 segments when available (host / owner / repo).
	if len(parts) >= 3 {
		return strings.Join(parts[len(parts)-3:], "/")
	}
	return filepath.Base(absPath)
}

// FuzzyMatch tests whether all runes of query appear in target in order
// (case-insensitive subsequence). Empty query matches everything.
func FuzzyMatch(target, query string) bool {
	if query == "" {
		return true
	}
	t := strings.ToLower(target)
	q := strings.ToLower(query)
	ti := 0
	for _, qr := range q {
		if unicode.IsSpace(qr) {
			continue
		}
		found := false
		for ti < len(t) {
			tr := rune(t[ti])
			ti++
			if tr == qr {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Filter returns the subset of repos whose Name or Basename matches query
// under FuzzyMatch. Result preserves input order.
func Filter(repos []Repo, query string) []Repo {
	if query == "" {
		out := make([]Repo, len(repos))
		copy(out, repos)
		return out
	}
	out := make([]Repo, 0, len(repos))
	for _, r := range repos {
		if FuzzyMatch(r.Name, query) || FuzzyMatch(r.Basename, query) {
			out = append(out, r)
		}
	}
	return out
}

// SortByBasename sorts repos in-place by their Basename, then by Name to break
// ties deterministically.
func SortByBasename(repos []Repo) {
	sort.SliceStable(repos, func(i, j int) bool {
		if repos[i].Basename != repos[j].Basename {
			return repos[i].Basename < repos[j].Basename
		}
		return repos[i].Name < repos[j].Name
	})
}
