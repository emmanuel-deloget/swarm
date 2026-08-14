package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
// It asks before it acts. `git status --porcelain` says whether the directory
// holds anything that exists only there, in a format git documents as stable
// across versions and configurations — and, unlike the sentence `git worktree
// remove` prints when it refuses, one that is not translated. Reading that
// prose was the first version of this, and it would have called a refusal a
// system error on a machine whose git speaks French.
//
// Asking first also separates the two failures cleanly. A worktree with work in
// it is a decision, returned as it is. Once it is known to be clean, anything
// that goes wrong afterwards is the file system, and that is worth retrying —
// on Windows a directory written moments earlier often cannot be deleted yet,
// a handle held by the indexer or the virus scanner, and it clears on its own.
//
// --force is never passed. Commits are a separate matter: removing a worktree
// keeps its branch, so committed work survives regardless. Only DeleteBranch
// can lose that, and it refuses to.
func RemoveWorktree(dir string) error {
	repo, err := repoRoot(dir)
	if err != nil {
		return err
	}
	// Best effort: a worktree locked by us must be unlocked before it can be
	// removed, and one that was never locked simply refuses here.
	_ = UnlockWorktree(dir)

	if held, what := holdsWork(dir); held {
		return fmt.Errorf("%s holds work that removing it would delete:\n%s", dir, what)
	}

	out, err := git(repo, "worktree", "remove", dir)
	if err == nil {
		return nil
	}
	last := gitSays(out, err)

	// Asking git again is useless, and that is what the first version of this
	// did. On Windows the failed attempt is not a no-op: git deletes what it
	// can, fails on the directory itself, and leaves something that is no
	// longer a worktree — so every retry answers "not a working tree" and
	// buries the real cause.
	//
	// Doing it directly is safe here, and only here: holdsWork has already
	// established that nothing in the directory exists only there. What is
	// left is a file system that will not let go of a directory it was
	// finished with, which is a Windows behaviour and clears by itself.
	if err := removeDirAndPrune(repo, dir); err != nil {
		return fmt.Errorf("could not remove %s: %w (git said: %s)", dir, err, last)
	}
	return nil
}

// removeDirAndPrune deletes the directory and drops the registration git keeps
// for it.
//
// The caller has established that nothing in there exists only there, so this
// is not a decision about somebody's work — it is finishing a removal the file
// system interrupted. Retried on Windows, where a directory written moments ago
// is routinely held open for a second or two by the indexer or the scanner.
func removeDirAndPrune(repo, dir string) error {
	tries, wait := removeRetries()
	var last error
	for attempt := range tries {
		last = os.RemoveAll(dir)
		if last == nil {
			// The registration outlives the directory; prune drops it.
			_, _ = git(repo, "worktree", "prune")
			return nil
		}
		if attempt < tries-1 {
			time.Sleep(wait)
		}
	}
	return last
}

// holdsWork reports whether a worktree contains anything that exists only
// there, and what, so a refusal can name it rather than gesture at it.
func holdsWork(dir string) (bool, string) {
	out, err := git(dir, "status", "--porcelain")
	if err != nil {
		// Unreadable: assume there is something to protect. Being wrong this
		// way costs a directory nobody deleted; being wrong the other way
		// costs the work in it.
		return true, "its state could not be read"
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return false, ""
	}
	return true, out
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

// gitSays is what git printed, or the error when it printed nothing.
func gitSays(out string, err error) string {
	msg := strings.TrimSpace(out)
	msg = strings.TrimPrefix(msg, "fatal: ")
	if msg == "" {
		return err.Error()
	}
	return msg
}
