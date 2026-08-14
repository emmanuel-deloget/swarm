package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo makes a repository with one commit and returns its path.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Skipf("no usable git here: %v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "a.txt"}, {"commit", "-qm", "first"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func TestAWorktreeIsItsOwnDirectoryAndBranch(t *testing.T) {
	src := repo(t)
	dir := filepath.Join(t.TempDir(), "worktrees", "worker-1")

	if err := AddWorktree(dir, src, BranchName("worker-1"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Errorf("the worktree does not have the repository's files: %v", err)
	}
	// Its own branch, namespaced so it is recognisable a week later.
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "swarm/worker-1" {
		t.Errorf("the worktree is on %q", got)
	}
	// And the main checkout is untouched: that is the whole point.
	main, err := exec.Command("git", "-C", src, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(main), "swarm/") {
		t.Errorf("making a worktree moved the main checkout to %s", main)
	}
}

// Idempotent, like Provision: restarting a hub must not touch an agent's work,
// and a worktree that survived one is the same worktree.
func TestAddingAWorktreeTwiceLeavesTheFirstAlone(t *testing.T) {
	src := repo(t)
	dir := filepath.Join(t.TempDir(), "worker-1")

	if err := AddWorktree(dir, src, BranchName("worker-1"), ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddWorktree(dir, src, BranchName("worker-1"), ""); err != nil {
		t.Fatalf("adding it again failed instead of doing nothing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "work.txt")); err != nil {
		t.Error("adding a worktree over an existing one threw away what was in it")
	}
}

func TestBaseRefPrefersTheRemoteAndFallsBack(t *testing.T) {
	src := repo(t)

	// No remote here, so there is nothing to be fresh from.
	if got := BaseRef(src, ""); got != "HEAD" {
		t.Errorf("with no remote the base is %q", got)
	}
	// Asking for head is asking for what is checked out now, whatever exists.
	if got := BaseRef(src, "head"); got != "HEAD" {
		t.Errorf("head resolved to %q", got)
	}
}

// The safety property is git's refusal, not ours. These check that swarm never
// asks it to make an exception.
func TestRemovingRefusesToTakeWorkWithIt(t *testing.T) {
	src := repo(t)
	dir := filepath.Join(t.TempDir(), "worker-1")
	if err := AddWorktree(dir, src, BranchName("worker-1"), ""); err != nil {
		t.Fatal(err)
	}

	// An untracked file: work that exists nowhere else.
	if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("hours of it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(dir); err == nil {
		t.Fatal("a worktree with untracked work in it was removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.txt")); err != nil {
		t.Error("the work was deleted anyway")
	}

	// A modified tracked file is the same answer.
	if err := os.Remove(filepath.Join(dir, "draft.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(dir); err == nil {
		t.Error("a worktree with uncommitted changes was removed")
	}
}

func TestRemovingACleanWorktreeKeepsItsCommits(t *testing.T) {
	src := repo(t)
	dir := filepath.Join(t.TempDir(), "worker-1")
	branch := BranchName("worker-1")
	if err := AddWorktree(dir, src, branch, ""); err != nil {
		t.Fatal(err)
	}

	// Committed but never pushed: the case that matters most.
	if err := os.WriteFile(filepath.Join(dir, "done.txt"), []byte("the work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "done.txt"}, {"commit", "-qm", "the work"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	if err := RemoveWorktree(dir); err != nil {
		t.Fatalf("a clean worktree was not removed: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the directory is still there")
	}
	// The commit survives, on its branch: removing a worktree is not losing
	// work, which is what makes collecting one acceptable at all.
	out, err := exec.Command("git", "-C", src, "log", "--oneline", branch).Output()
	if err != nil {
		t.Fatalf("the branch is gone with the worktree: %v", err)
	}
	if !strings.Contains(string(out), "the work") {
		t.Errorf("the commit did not survive:\n%s", out)
	}
}

// Deleting a branch is the one operation that can lose commits, so it only
// happens when the remote already has every one of them.
func TestABranchThatWasNeverPushedIsKept(t *testing.T) {
	src := repo(t)
	branch := BranchName("worker-1")
	dir := filepath.Join(t.TempDir(), "worker-1")
	if err := AddWorktree(dir, src, branch, ""); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(dir); err != nil {
		t.Fatal(err)
	}

	deleted, err := DeleteBranch(src, branch)
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Error("a branch the remote has never seen was deleted")
	}
	out, _ := exec.Command("git", "-C", src, "branch", "--list", branch).Output()
	if !strings.Contains(string(out), branch) {
		t.Error("the branch is gone")
	}
}

func TestABranchFullyPushedIsDeleted(t *testing.T) {
	src := repo(t)
	branch := BranchName("worker-1")

	// A remote, and the branch pushed to it.
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", remote).CombinedOutput(); err != nil {
		t.Skipf("no usable git here: %v %s", err, out)
	}
	for _, args := range [][]string{
		{"remote", "add", "origin", remote},
		{"branch", branch},
		{"push", "-q", "origin", branch},
	} {
		if out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	deleted, err := DeleteBranch(src, branch)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("a branch whose every commit is on the remote was kept")
	}
}

// Pushed once and committed to since: the remote is behind, so deleting would
// lose the difference.
func TestABranchAheadOfTheRemoteIsKept(t *testing.T) {
	src := repo(t)
	branch := BranchName("worker-1")
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", remote).CombinedOutput(); err != nil {
		t.Skipf("no usable git here: %v %s", err, out)
	}
	dir := filepath.Join(t.TempDir(), "worker-1")
	if err := AddWorktree(dir, src, branch, ""); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", append([]string{"-C", src}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "-C", dir, "push", "-q", "origin", branch).CombinedOutput(); err != nil {
		t.Fatalf("push: %v %s", err, out)
	}
	// One more commit, not pushed.
	if err := os.WriteFile(filepath.Join(dir, "more.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "more.txt"}, {"commit", "-qm", "later"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := RemoveWorktree(dir); err != nil {
		t.Fatal(err)
	}

	deleted, err := DeleteBranch(src, branch)
	if err != nil {
		t.Fatal(err)
	}
	if deleted {
		t.Error("a branch with a commit the remote does not have was deleted")
	}
}

func TestALockedWorktreeIsProtectedUntilUnlocked(t *testing.T) {
	src := repo(t)
	dir := filepath.Join(t.TempDir(), "worker-1")
	if err := AddWorktree(dir, src, BranchName("worker-1"), ""); err != nil {
		t.Fatal(err)
	}
	if err := LockWorktree(dir, "worker-1 is running"); err != nil {
		t.Fatal(err)
	}

	// Somebody else's cleanup cannot take it.
	out, err := exec.Command("git", "-C", src, "worktree", "remove", dir).CombinedOutput()
	if err == nil {
		t.Fatal("a locked worktree was removed by a plain git command")
	}
	if !strings.Contains(strings.ToLower(string(out)), "lock") {
		t.Errorf("the refusal is not about the lock: %s", out)
	}

	// Ours unlocks it on the way through.
	if err := RemoveWorktree(dir); err != nil {
		t.Errorf("swarm could not remove its own locked worktree: %v", err)
	}
}

// The refusal is read by somebody looking for their work, so it says what is in
// the way rather than what git's exit status was.
func TestTheRefusalReadsLikeASentence(t *testing.T) {
	src := repo(t)
	dir := filepath.Join(t.TempDir(), "worker-1")
	if err := AddWorktree(dir, src, BranchName("worker-1"), ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RemoveWorktree(dir)
	if err == nil {
		t.Fatal("the worktree was removed")
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Errorf("the message leads with git's exit status: %v", err)
	}
	if !strings.Contains(err.Error(), "untracked") {
		t.Errorf("the message does not say what is in the way: %v", err)
	}
}
