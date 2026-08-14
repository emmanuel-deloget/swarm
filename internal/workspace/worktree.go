package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Git worktrees, for agents that exist for one task.
//
// The package comment says a durable per-agent workspace has to be a clone, and
// that is still true: two worktrees cannot have the same branch checked out, so
// several agents sitting on the main branch between tasks rules worktrees out.
//
// An ephemeral agent is not between tasks. It is created for one piece of work,
// on its own branch, and it ends when the work does — which is the shape a
// worktree was designed for, and the reason this exists now and not before.

// AddWorktree makes dir a worktree of the repository holding from, on a new
// branch.
//
// Idempotent like Provision: a directory that is already a checkout is left
// exactly as it is. Restarting a hub must not touch an agent's work, and a
// worktree that survived one is the same worktree.
func AddWorktree(dir, from, branch, base string) error {
	if isRepo(dir) {
		return nil
	}
	repo, err := repoRoot(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if base == "" {
		base = BaseRef(repo, "")
	}
	if out, err := git(repo, "worktree", "add", "--quiet", "-b", branch, dir, base); err != nil {
		return fmt.Errorf("making a worktree at %s from %s: %w%s", dir, base, err, out)
	}
	return nil
}

// BaseRef is what a new worktree branches from.
//
// "head" takes the repository's current commit, which is what you want for an
// instance meant to work on top of work in progress. Anything else — the
// default — starts from the remote's default branch, so an instance begins on
// what is actually shared rather than on whatever happened to be checked out
// when it was spawned.
//
// Falls back to HEAD when there is no remote to ask, which is the case for a
// repository that has never been pushed.
func BaseRef(repo, mode string) string {
	if mode == "head" {
		return "HEAD"
	}
	if out, err := git(repo, "rev-parse", "--verify", "--quiet", "origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			return ref
		}
	}
	return "HEAD"
}

// BranchName is the branch a worktree gets. Namespaced, so that what swarm made
// is recognisable in `git branch` a week later, when the agent that owned it is
// long gone.
func BranchName(agent string) string { return "swarm/" + agent }

// LockWorktree marks a worktree as in use, so that a cleanup running elsewhere
// cannot remove it while an agent is working in it. Failure is not fatal: the
// lock is a courtesy to other tools, not something swarm's own collection
// depends on.
func LockWorktree(dir, reason string) error {
	repo, err := repoRoot(dir)
	if err != nil {
		return err
	}
	if out, err := git(repo, "worktree", "lock", "--reason", reason, dir); err != nil {
		return fmt.Errorf("locking %s: %w%s", dir, err, out)
	}
	return nil
}

// UnlockWorktree releases it.
func UnlockWorktree(dir string) error {
	repo, err := repoRoot(dir)
	if err != nil {
		return err
	}
	if out, err := git(repo, "worktree", "unlock", dir); err != nil {
		return fmt.Errorf("unlocking %s: %w%s", dir, err, out)
	}
	return nil
}

// RemoveWorktree takes a worktree away, and refuses to take work with it.
//
// The refusal is git's, not ours: `git worktree remove` will not delete a
// directory holding modified or untracked files unless --force is passed, and
// this never passes it. That single rule is the whole safety property — there
// is no detection here to get wrong, and no flag to add later without noticing
// what it means.
//
// Commits are a separate matter. Removing a worktree keeps the branch and
// everything on it, so work that was committed survives even when it was never
// pushed. Only DeleteBranch can lose that, and it refuses to.
func RemoveWorktree(dir string) error {
	repo, err := repoRoot(dir)
	if err != nil {
		return err
	}
	// Best effort: a worktree locked by us must be unlocked before it can be
	// removed, and one that was never locked simply refuses here.
	_ = UnlockWorktree(dir)

	if out, err := git(repo, "worktree", "remove", dir); err != nil {
		return fmt.Errorf("%s still holds work that removing it would delete: %w%s", dir, err, out)
	}
	return nil
}

// DeleteBranch removes a branch, and only if the remote already has every
// commit on it.
//
// This is the one place in swarm where work can actually disappear: a worktree
// removed keeps its branch, but a branch deleted takes its commits. So the
// question asked is not "is it merged" — a branch can be unmerged and still be
// safe if it is pushed, and merged locally while nobody else has seen it.
//
// Reports whether it went, so a caller can say what happened rather than
// guessing.
func DeleteBranch(repo, branch string) (deleted bool, err error) {
	// A ref that does not exist makes rev-parse fail, and that is an answer
	// rather than a failure: no remote branch means the commits are nowhere
	// else, so the branch stays.
	upstream, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err != nil || strings.TrimSpace(upstream) == "" {
		return false, nil //nolint:nilerr // never pushed is not an error, it is a no
	}
	local, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, nil //nolint:nilerr // no such branch, nothing to delete
	}
	if strings.TrimSpace(local) != strings.TrimSpace(upstream) {
		return false, nil // the remote is behind, so deleting would lose the difference
	}
	if out, err := git(repo, "branch", "-D", branch); err != nil {
		return false, fmt.Errorf("deleting %s: %w%s", branch, err, out)
	}
	return true, nil
}

// RepoRoot is the repository holding dir, for callers that need to name it.
func RepoRoot(dir string) (string, error) { return repoRoot(dir) }

// Worktrees lists the worktrees of a repository, other than the main checkout.
func Worktrees(repo string) ([]string, error) {
	out, err := git(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("listing worktrees of %s: %w%s", repo, err, out)
	}
	var dirs []string
	main := true
	for _, line := range strings.Split(out, "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		if main {
			main = false // the first one listed is the checkout itself
			continue
		}
		dirs = append(dirs, path)
	}
	return dirs, nil
}
