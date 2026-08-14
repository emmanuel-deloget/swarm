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
