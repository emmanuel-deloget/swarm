// Package workspace provisions a durable working copy for an agent.
//
// swarm does not manage git worktrees: those tools solve the opposite problem,
// running agents in parallel precisely because they cannot talk to each other.
// What it does provide is one thing that no agent can arrange for itself — a
// checkout that already has the dependencies, the ports and the git identity it
// needs, because that is plumbing rather than a decision.
//
// A clone rather than a worktree for a durable workspace, and for a hard reason:
// two worktrees cannot have the same branch checked out. Where several agents
// may sit on the main branch between tasks, that rules worktrees out. A clone
// has its own branches, its own index and its own configuration.
//
// Worktrees do have a place, and it arrived with the ephemeral agent: one made
// for a single task, on its own branch, ending when the work does. That is in
// worktree.go, and it is the only kind of worktree swarm makes.
package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// carried are the local git settings a fresh clone does not inherit but cannot
// work without. A clone gets its remote and its branches; it does not get who
// you are or how you sign, so an agent committing from one produces unsigned
// commits under the wrong name — or fails outright.
//
// An allow-list, never a copy of the whole file: `git clone` has just written a
// correct remote.origin.url and branch mapping, and overwriting those would
// break the clone it was meant to furnish.
func carried(key string) bool {
	switch {
	case strings.HasPrefix(key, "user."), strings.HasPrefix(key, "gpg."):
		return true
	}
	switch key {
	case "commit.gpgsign", "tag.gpgsign", "credential.helper", "init.defaultbranch":
		return true
	}
	return false
}

// Provision makes dir a working copy of the repository holding from.
//
// It is idempotent by construction: a directory that is already a repository is
// left exactly as it is, so restarting an agent never touches its work. That is
// also why nothing here fetches, rebases or merges — swarm reports on a
// repository, it does not drive one.
func Provision(dir, from string) error {
	if isRepo(dir) {
		return nil
	}
	src, err := repoRoot(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}

	// Clone from the local path: on one filesystem git hardlinks the object
	// store, so this costs the working tree rather than the history.
	if out, err := git("", "clone", "--quiet", src, dir); err != nil {
		return fmt.Errorf("cloning %s into %s: %w%s", src, dir, err, out)
	}

	// Point origin at what the source calls origin. Left as it is, origin would
	// be the local repository, and pushing to a non-bare checkout with that
	// branch out is refused by git.
	if url, err := git(src, "config", "--get", "remote.origin.url"); err == nil {
		if url = strings.TrimSpace(url); url != "" {
			if out, err := git(dir, "remote", "set-url", "origin", url); err != nil {
				return fmt.Errorf("pointing origin at %s: %w%s", url, err, out)
			}
		}
	}

	return carryConfig(src, dir)
}

// carryConfig copies the settings from carried() out of the source repository.
func carryConfig(src, dir string) error {
	list, err := git(src, "config", "--local", "--list")
	if err != nil {
		// A source without local settings is not a problem; there is simply
		// nothing to carry.
		return nil //nolint:nilerr // nothing to carry is not a failure
	}
	for _, line := range strings.Split(list, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || !carried(key) {
			continue
		}
		if out, err := git(dir, "config", "--local", key, value); err != nil {
			return fmt.Errorf("carrying %s: %w%s", key, err, out)
		}
	}
	return nil
}

// isRepo reports whether dir is already a checkout — a directory for a clone,
// a file for a worktree, either way somebody's work that is not ours to redo.
func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// InRepo reports whether dir is inside a git repository.
//
// Exported for the configuration, which refuses `workspace: worktree` where
// there is no repository to make one in — at load, so the answer arrives before
// a fleet is half-started rather than as an agent failing to launch.
func InRepo(dir string) bool {
	_, err := repoRoot(dir)
	return err == nil
}

// repoRoot finds the repository holding dir.
func repoRoot(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s is not in a git repository, so there is nothing to clone: %w", dir, err)
	}
	return strings.TrimSpace(out), nil
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// State is what a working copy looks like right now. swarm reports it and never
// acts on it: no fetch, no rebase, no merge. Which agent works where, and how
// far its base has drifted, is worth seeing; deciding what to do about it is the
// agent's business.
type State struct {
	// Dir is the directory the state was read from, which for an agent free to
	// move is where its process actually is rather than where it was started.
	Dir string
	// Branch is the checked-out branch, "HEAD" when detached, empty when Dir is
	// not a repository at all.
	Branch string
	// Dirty reports uncommitted changes to tracked files. Untracked files do not
	// count: an agent's scratch output would otherwise mark every workspace.
	Dirty bool
	// Ahead and Behind compare against the upstream — as of the last fetch,
	// which is all a repository knows. Zero when there is no upstream.
	Ahead, Behind int
	// Upstream reports whether a tracking branch exists, so "0 behind" can be
	// told from "nothing to compare against".
	Upstream bool
}

// Read collects the state of the working copy at dir.
//
// One `git status` rather than three commands: branch, divergence and dirtiness
// all come out of the same call, which matters when it runs for every agent on
// a timer.
func Read(dir string) (State, bool) {
	st := State{Dir: dir}
	out, err := git(dir, "status", "--porcelain=v2", "--branch", "--untracked-files=no")
	if err != nil {
		return State{}, false
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			st.Branch = strings.TrimSpace(strings.TrimPrefix(line, "# branch.head "))
		case strings.HasPrefix(line, "# branch.upstream "):
			st.Upstream = true
		case strings.HasPrefix(line, "# branch.ab "):
			// A line git only prints when there is an upstream, so a parse
			// failure leaves both counts at zero — which is what "nothing to
			// compare against" already looks like.
			_, _ = fmt.Sscanf(strings.TrimPrefix(line, "# branch.ab "), "+%d -%d", &st.Ahead, &st.Behind)
		case line == "":
		case line[0] != '#':
			// Any entry at all means tracked files have changed.
			st.Dirty = true
		}
	}
	return st, st.Branch != ""
}

// Summary is the state in a few characters, for a column that has to share its
// line: "main*  3↑ 12↓". Empty when there is nothing worth saying.
func (s State) Summary() string {
	if s.Branch == "" {
		return ""
	}
	out := s.Branch
	if s.Dirty {
		out += "*"
	}
	if s.Ahead > 0 {
		out += fmt.Sprintf(" %d↑", s.Ahead)
	}
	if s.Behind > 0 {
		out += fmt.Sprintf(" %d↓", s.Behind)
	}
	return out
}
