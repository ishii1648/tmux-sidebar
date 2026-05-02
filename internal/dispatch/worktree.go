package dispatch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CreateWorktree creates a git worktree for branchName under the gw_add
// naming convention `<main_worktree>@<branch_dir_name>` (where
// branch_dir_name is the branch with `/` → `-`).
//
// Behavior matches dispatch.sh's create_worktree:
//   - if branchName is already checked out in another worktree → return that path (resume)
//   - if the desired path exists but tracks a different branch → append `-HHMMSS[-N]`
//   - if branch exists locally or on origin → use it
//   - otherwise → create a new branch off origin/<default>
//   - copy `.claude/settings.local.json` from the main worktree if present
func CreateWorktree(repoPath, branchName string) (string, error) {
	if branchName == "" {
		return "", fmt.Errorf("branch name is empty")
	}
	wts, err := listWorktrees(repoPath)
	if err != nil {
		return "", err
	}
	if len(wts) == 0 {
		return "", fmt.Errorf("could not detect main worktree of %s", repoPath)
	}
	main := wts[0].path

	// Resume: branch already checked out somewhere.
	for _, w := range wts {
		if w.branch == branchName {
			if dirExists(w.path) {
				return w.path, nil
			}
		}
	}

	worktreePath := main + "@" + strings.ReplaceAll(branchName, "/", "-")
	if dirExists(worktreePath) {
		base := worktreePath
		worktreePath = base + "-" + time.Now().Format("150405")
		i := 2
		for dirExists(worktreePath) {
			worktreePath = fmt.Sprintf("%s-%s-%d", base, time.Now().Format("150405"), i)
			i++
			if i > 100 {
				return "", fmt.Errorf("could not resolve worktree path collision: %s", base)
			}
		}
	}

	// Best-effort fetch; ignore failures (offline / private repo).
	_ = exec.Command("git", "-C", repoPath, "fetch", "origin").Run()

	defaultBranch := defaultBranch(repoPath)
	localExists := branchExistsLocal(repoPath, branchName)
	remoteExists := branchExistsRemote(repoPath, branchName)

	if localExists || remoteExists {
		if remoteExists && !localExists {
			_ = exec.Command("git", "-C", repoPath, "fetch", "origin", branchName+":"+branchName).Run()
		}
		if out, err := exec.Command("git", "-C", repoPath, "worktree", "add", worktreePath, branchName).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git worktree add: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	} else {
		base := "origin/" + defaultBranch
		if defaultBranch == "" {
			base = "HEAD"
		}
		if out, err := exec.Command("git", "-C", repoPath, "worktree", "add", worktreePath, "-b", branchName, base).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git worktree add -b: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}

	copySettingsLocal(main, worktreePath)
	return worktreePath, nil
}

// CheckoutDefaultBranch returns the main worktree path. When the working
// tree is clean it also switches to the repo's default branch (origin/HEAD →
// main → master). Mirrors dispatch.sh's checkout_default_branch and is only
// invoked when no-worktree-repos config triggered the no-worktree path.
func CheckoutDefaultBranch(repoPath string) string {
	wts, err := listWorktrees(repoPath)
	if err != nil || len(wts) == 0 {
		return repoPath
	}
	main := wts[0].path
	if !dirExists(main) {
		return repoPath
	}
	def := defaultBranch(main)
	if def == "" {
		if branchExistsLocal(main, "main") {
			def = "main"
		} else if branchExistsLocal(main, "master") {
			def = "master"
		}
	}
	if def == "" {
		return main
	}
	cur := currentBranch(main)
	if cur == def {
		return main
	}
	if !workingTreeClean(main) {
		return main
	}
	_ = exec.Command("git", "-C", main, "checkout", def).Run()
	return main
}

// ── git helpers ─────────────────────────────────────────────────────────────

type worktreeEntry struct {
	path   string
	branch string // empty for detached
}

// listWorktrees returns `git worktree list --porcelain` parsed into entries.
// The first entry is always the main worktree.
func listWorktrees(repoPath string) ([]worktreeEntry, error) {
	out, err := exec.Command("git", "-C", repoPath, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var entries []worktreeEntry
	var cur worktreeEntry
	flush := func() {
		if cur.path != "" {
			entries = append(entries, cur)
		}
		cur = worktreeEntry{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			cur.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			cur.branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	flush()
	return entries, nil
}

// defaultBranch returns the repo's default branch via origin/HEAD, or "" if
// the symbolic ref isn't set.
func defaultBranch(repoPath string) string {
	out, err := exec.Command("git", "-C", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD").Output()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	return strings.TrimPrefix(s, "refs/remotes/origin/")
}

func branchExistsLocal(repoPath, name string) bool {
	return exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+name).Run() == nil
}

func branchExistsRemote(repoPath, name string) bool {
	out, err := exec.Command("git", "-C", repoPath, "ls-remote", "--heads", "origin", name).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func currentBranch(repoPath string) string {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func workingTreeClean(repoPath string) bool {
	if exec.Command("git", "-C", repoPath, "diff", "--quiet").Run() != nil {
		return false
	}
	if exec.Command("git", "-C", repoPath, "diff", "--cached", "--quiet").Run() != nil {
		return false
	}
	return true
}

// copySettingsLocal copies .claude/settings.local.json from src to dst when
// it exists in src. dst's .claude/ directory is created if needed. Errors
// are silent because the file is optional.
func copySettingsLocal(src, dst string) {
	srcPath := filepath.Join(src, ".claude", "settings.local.json")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return
	}
	dstDir := filepath.Join(dst, ".claude")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dstDir, "settings.local.json"), data, 0o644)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
