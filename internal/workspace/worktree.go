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
