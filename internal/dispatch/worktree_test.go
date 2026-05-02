package dispatch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a fresh git repo at dir with one initial commit on the
// configured default branch ("main"). Returns the repo path.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		// Avoid the user's global git config interfering.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	return dir
}

func TestCreateWorktreeNewBranch(t *testing.T) {
	repo := initRepo(t, filepath.Join(t.TempDir(), "src"))
	got, err := CreateWorktree(repo, "feat/foo-bar")
	if err != nil {
		t.Fatalf("CreateWorktree err = %v", err)
	}
	wantSuffix := "@feat-foo-bar"
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("worktree path %q should end with %q", got, wantSuffix)
	}
	if !dirExists(got) {
		t.Errorf("worktree dir %q does not exist", got)
	}
	// branch should now appear in worktree list
	wts, err := listWorktrees(repo)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range wts {
		if w.branch == "feat/foo-bar" {
			found = true
		}
	}
	if !found {
		t.Errorf("feat/foo-bar branch not in worktree list: %+v", wts)
	}
}

func TestCreateWorktreeResume(t *testing.T) {
	repo := initRepo(t, filepath.Join(t.TempDir(), "src"))
	first, err := CreateWorktree(repo, "feat/resume")
	if err != nil {
		t.Fatalf("first CreateWorktree err = %v", err)
	}
	second, err := CreateWorktree(repo, "feat/resume")
	if err != nil {
		t.Fatalf("second CreateWorktree err = %v", err)
	}
	if first != second {
		t.Errorf("expected resume to return first path %q, got %q", first, second)
	}
}

func TestCreateWorktreeCopiesSettingsLocal(t *testing.T) {
	repo := initRepo(t, filepath.Join(t.TempDir(), "src"))
	settingsDir := filepath.Join(repo, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"foo":1}`)
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.local.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := CreateWorktree(repo, "feat/copy")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(wt, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("settings.local.json should be copied: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch: %q vs %q", got, body)
	}
}

func TestCreateWorktreeBranchEmpty(t *testing.T) {
	if _, err := CreateWorktree("/tmp", ""); err == nil {
		t.Error("expected error for empty branch")
	}
}

// TestCreateWorktreeChecksOutExistingLocalBranch covers the `:<branch>`
// checkout-mode path in dispatch_launcher.fish: when the named branch
// already exists locally, the new worktree should check out that branch
// instead of creating a new one. The HEAD of the worktree must match the
// existing branch's commit.
func TestCreateWorktreeChecksOutExistingLocalBranch(t *testing.T) {
	repo := initRepo(t, filepath.Join(t.TempDir(), "src"))
	gitRun(t, repo, "checkout", "-b", "feat/already-here")
	if err := os.WriteFile(filepath.Join(repo, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "second")
	wantSHA := strings.TrimSpace(gitOut(t, repo, "rev-parse", "feat/already-here"))
	gitRun(t, repo, "checkout", "main")

	wt, err := CreateWorktree(repo, "feat/already-here")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	gotSHA := strings.TrimSpace(gitOut(t, wt, "rev-parse", "HEAD"))
	if gotSHA != wantSHA {
		t.Errorf("worktree HEAD = %s, want %s (existing branch)", gotSHA, wantSHA)
	}
	gotBranch := strings.TrimSpace(gitOut(t, wt, "rev-parse", "--abbrev-ref", "HEAD"))
	if gotBranch != "feat/already-here" {
		t.Errorf("worktree branch = %q, want feat/already-here", gotBranch)
	}
}

// TestCreateWorktreeChecksOutRemoteOnlyBranch validates the path where the
// branch exists only on origin (no local ref). dispatch.sh fetches it as a
// local ref before `git worktree add`, so the resulting worktree is on the
// requested branch with the remote's commit.
func TestCreateWorktreeChecksOutRemoteOnlyBranch(t *testing.T) {
	tmp := t.TempDir()
	upstream := initRepo(t, filepath.Join(tmp, "upstream"))
	// Create a feature branch on the upstream and push back to a bare clone
	// that downstream uses as origin.
	gitRun(t, upstream, "checkout", "-b", "feat/remote-only")
	if err := os.WriteFile(filepath.Join(upstream, "f"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, upstream, "add", ".")
	gitRun(t, upstream, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "remote work")
	wantSHA := strings.TrimSpace(gitOut(t, upstream, "rev-parse", "feat/remote-only"))
	// Reset upstream HEAD to main so the bare clone's HEAD targets main and
	// the downstream clone gets `main` (not `feat/remote-only`) as its only
	// local branch — leaving feat/remote-only as a remote-only ref.
	gitRun(t, upstream, "checkout", "main")

	bare := filepath.Join(tmp, "origin.git")
	if out, err := exec.Command("git", "clone", "--bare", upstream, bare).CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare: %v\n%s", err, out)
	}
	downstream := filepath.Join(tmp, "downstream")
	if out, err := exec.Command("git", "clone", bare, downstream).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	// `git clone` of a bare with multiple branches leaves the non-default
	// branches as remote refs only. Verify that the requested branch is
	// indeed remote-only before exercising CreateWorktree.
	if branchExistsLocal(downstream, "feat/remote-only") {
		t.Skip("local branch already exists; setup did not isolate the remote-only path")
	}
	if !branchExistsRemote(downstream, "feat/remote-only") {
		t.Skip("remote branch not detected; environment may have prevented the bare clone setup")
	}

	wt, err := CreateWorktree(downstream, "feat/remote-only")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	gotSHA := strings.TrimSpace(gitOut(t, wt, "rev-parse", "HEAD"))
	if gotSHA != wantSHA {
		t.Errorf("worktree HEAD = %s, want %s (remote branch tip)", gotSHA, wantSHA)
	}
}

// gitRun runs a git command in repo and fails the test on non-zero exit.
func gitRun(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitOut runs a git command and returns its stdout.
func gitOut(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}
