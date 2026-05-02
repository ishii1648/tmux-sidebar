package dispatch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRepoAbsPath(t *testing.T) {
	dir := t.TempDir()
	got, err := ResolveRepo(dir)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	abs, _ := filepath.Abs(dir)
	if got != abs {
		t.Errorf("got %q, want %q", got, abs)
	}
}

func TestResolveRepoEmpty(t *testing.T) {
	if _, err := ResolveRepo(""); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestMatchesNoWorktreeConfig(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	dir := filepath.Join(tmpHome, ".config", "dispatch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# comment\ngithub.com/foo/bar\n\ngithub.com/baz/qux\n"
	if err := os.WriteFile(filepath.Join(dir, "no-worktree-repos"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !MatchesNoWorktreeConfig("github.com/foo/bar") {
		t.Error("foo/bar should match")
	}
	if !MatchesNoWorktreeConfig("github.com/baz/qux") {
		t.Error("baz/qux should match")
	}
	if MatchesNoWorktreeConfig("github.com/other/thing") {
		t.Error("other/thing should not match")
	}
}

func TestMatchesNoWorktreeConfigMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if MatchesNoWorktreeConfig("anything") {
		t.Error("missing config file should not match")
	}
}

func TestRepoShortFromPath(t *testing.T) {
	root := "/tmp/ghq"
	if got := repoShortFromPath("/tmp/ghq/github.com/foo/bar", root); got != "github.com/foo/bar" {
		t.Errorf("got %q", got)
	}
	if got := repoShortFromPath("/elsewhere/foo", root); got != "/elsewhere/foo" {
		t.Errorf("got %q", got)
	}
}
