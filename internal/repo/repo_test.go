package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	out := "/Users/sho/ghq/github.com/foo/bar\n/Users/sho/ghq/github.com/baz/qux\n\n"
	got := parseList(out, "/Users/sho/ghq")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != "github.com/foo/bar" || got[0].Basename != "bar" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Path != "/Users/sho/ghq/github.com/baz/qux" {
		t.Errorf("got[1].Path = %q", got[1].Path)
	}
}

func TestParseListExcludesWorktrees(t *testing.T) {
	out := strings.Join([]string{
		"/Users/sho/ghq/github.com/foo/bar",
		"/Users/sho/ghq/github.com/foo/bar@feat-x",                  // worktree
		"/Users/sho/ghq/github.com/foo/bar@feat-y",                  // worktree
		"/Users/sho/ghq/github.com/baz/qux",                         // main
		"/Users/sho/ghq/github.com/baz/qux@hotfix-1",                // worktree
		"/Users/sho/ghq/github.com/foo/bar@feat/with-slash",         // worktree, slashed branch
		"/Users/sho/ghq/github.com/foo/bar@feat/deep/branch-name",   // worktree, multi-slash branch
	}, "\n")
	got := parseList(out, "/Users/sho/ghq")
	if len(got) != 2 {
		t.Fatalf("len = %d (entries = %+v), want 2 main repos", len(got), got)
	}
	if got[0].Basename != "bar" || got[1].Basename != "qux" {
		t.Errorf("basenames = [%q, %q], want [bar, qux]", got[0].Basename, got[1].Basename)
	}
}

// Without ghqRoot, parseList falls back to a length-bounded scan of the
// trailing path segments. Make sure that scan still drops slashed-branch
// worktrees and is not fooled by '@' high up the path.
func TestParseListWithoutRootFallback(t *testing.T) {
	out := strings.Join([]string{
		"/Users/foo@bar/ghq/github.com/foo/bar",                   // main, '@' in ancestor must NOT disqualify
		"/Users/foo@bar/ghq/github.com/foo/bar@feat/with-slash",   // worktree
		"/Users/foo@bar/ghq/github.com/baz/qux@hotfix",            // worktree
	}, "\n")
	got := parseList(out, "")
	if len(got) != 1 {
		t.Fatalf("len = %d (entries = %+v), want 1 main repo", len(got), got)
	}
	if got[0].Basename != "bar" {
		t.Errorf("basename = %q, want bar", got[0].Basename)
	}
}

// isGitWorktree must drop entries whose `.git` is a worktree pointer file
// even when the directory name doesn't follow the `<repo>@<branch>`
// convention (e.g. `git worktree add ../<repo>-<topic>`).
func TestIsGitWorktree(t *testing.T) {
	tmp := t.TempDir()

	mainRepo := filepath.Join(tmp, "main")
	if err := os.MkdirAll(filepath.Join(mainRepo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}

	worktreeRepo := filepath.Join(tmp, "worktree-pr123")
	if err := os.MkdirAll(worktreeRepo, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(worktreeRepo, ".git"),
		[]byte("gitdir: "+filepath.Join(mainRepo, ".git", "worktrees", "worktree-pr123")+"\n"),
		0o644,
	); err != nil {
		t.Fatalf("write worktree pointer: %v", err)
	}

	noGit := filepath.Join(tmp, "not-a-repo")
	if err := os.MkdirAll(noGit, 0o755); err != nil {
		t.Fatalf("mkdir noGit: %v", err)
	}

	if isGitWorktree(mainRepo) {
		t.Errorf("isGitWorktree(main) = true, want false (.git is a directory)")
	}
	if !isGitWorktree(worktreeRepo) {
		t.Errorf("isGitWorktree(worktree) = false, want true (.git is a file)")
	}
	if isGitWorktree(noGit) {
		t.Errorf("isGitWorktree(noGit) = true, want false (no .git present)")
	}
}

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		target, query string
		want          bool
	}{
		{"github.com/ishii1648/tmux-sidebar", "tms", true},
		{"github.com/ishii1648/tmux-sidebar", "TMUX", true},
		{"github.com/ishii1648/tmux-sidebar", "xyz", false},
		{"foo", "", true},
		{"foo", "foobar", false},
		{"github.com/foo/bar", "g/f/b", true},
	}
	for _, c := range cases {
		if got := FuzzyMatch(c.target, c.query); got != c.want {
			t.Errorf("FuzzyMatch(%q,%q) = %v want %v", c.target, c.query, got, c.want)
		}
	}
}

func TestFilter(t *testing.T) {
	repos := []Repo{
		{Name: "github.com/foo/bar", Basename: "bar"},
		{Name: "github.com/foo/baz", Basename: "baz"},
		{Name: "gitlab.com/qux/bar", Basename: "bar"},
	}
	got := Filter(repos, "fooba")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	all := Filter(repos, "")
	if len(all) != 3 {
		t.Fatalf("empty query len = %d", len(all))
	}
}

func TestSortByBasename(t *testing.T) {
	repos := []Repo{
		{Name: "z", Basename: "bar"},
		{Name: "a", Basename: "foo"},
		{Name: "b", Basename: "bar"},
	}
	SortByBasename(repos)
	if repos[0].Name != "b" || repos[1].Name != "z" || repos[2].Name != "a" {
		t.Errorf("sorted = %+v", repos)
	}
}
