package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func source(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "."},
		{"config", "user.name", "Someone"},
		{"config", "user.email", "someone@example.invalid"},
		{"config", "user.signingkey", "DEADBEEF"},
		{"config", "commit.gpgsign", "true"},
		{"config", "credential.helper", "store"},
		{"config", "swarm.notcarried", "should stay behind"},
		{"remote", "add", "origin", "https://example.invalid/upstream.git"},
	} {
		if out, err := git(dir, args...); err != nil {
			t.Fatalf("git %v: %v%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := git(dir, "-c", "commit.gpgsign=false", "commit", "-qam", "first"); err != nil {
		if out2, err2 := git(dir, "add", "-A"); err2 != nil {
			t.Fatalf("git add: %v%s", err2, out2)
		}
		if out2, err2 := git(dir, "-c", "commit.gpgsign=false", "commit", "-qm", "first"); err2 != nil {
			t.Fatalf("git commit: %v%s (%v%s)", err2, out2, err, out)
		}
	}
	return dir
}

func get(t *testing.T, dir, key string) string {
	t.Helper()
	out, _ := git(dir, "config", "--local", "--get", key)
	return strings.TrimSpace(out)
}

func TestProvisionMakesAUsableClone(t *testing.T) {
	src := source(t)
	dir := filepath.Join(t.TempDir(), "workspaces", "dev-1")

	if err := Provision(dir, src); err != nil {
		t.Fatal(err)
	}
	if !isRepo(dir) {
		t.Fatal("no repository was made")
	}

	// origin has to be the upstream, not the local path: pushing to a non-bare
	// checkout with that branch out is refused by git.
	if got := get(t, dir, "remote.origin.url"); got != "https://example.invalid/upstream.git" {
		t.Errorf("origin = %q, want the source's upstream", got)
	}

	// Without these an agent commits under the wrong name, unsigned — or fails.
	for key, want := range map[string]string{
		"user.name":         "Someone",
		"user.email":        "someone@example.invalid",
		"user.signingkey":   "DEADBEEF",
		"commit.gpgsign":    "true",
		"credential.helper": "store",
	} {
		if got := get(t, dir, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// And only those: an allow-list, not a copy of the file.
	if got := get(t, dir, "swarm.notcarried"); got != "" {
		t.Errorf("a setting outside the allow-list was carried: %q", got)
	}

	if out, err := git(dir, "log", "--oneline"); err != nil || !strings.Contains(out, "first") {
		t.Errorf("the history did not come across: %v%s", err, out)
	}
}

// TestProvisionLeavesAnExistingCheckoutAlone is what makes restarting an agent
// safe, and what lets a fleet of hand-made clones adopt `workspace: clone`
// without anything being touched.
func TestProvisionLeavesAnExistingCheckoutAlone(t *testing.T) {
	src := source(t)
	dir := filepath.Join(t.TempDir(), "dev-1")
	if err := Provision(dir, src); err != nil {
		t.Fatal(err)
	}

	// Uncommitted work, and a setting somebody chose by hand.
	scratch := filepath.Join(dir, "in-progress.txt")
	if err := os.WriteFile(scratch, []byte("do not lose me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "config", "--local", "user.email", "changed@example.invalid"); err != nil {
		t.Fatal(err)
	}

	if err := Provision(dir, src); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Error("uncommitted work was destroyed by a second provision")
	}
	if got := get(t, dir, "user.email"); got != "changed@example.invalid" {
		t.Errorf("a hand-made setting was overwritten: %q", got)
	}
}

func TestProvisionNeedsARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	err := Provision(filepath.Join(t.TempDir(), "dev-1"), t.TempDir())
	if err == nil {
		t.Fatal("cloning from a directory that is not a repository should fail")
	}
	if !strings.Contains(err.Error(), "not in a git repository") {
		t.Errorf("the error should say what is missing, got %v", err)
	}
}

func TestCarriedIsAnAllowList(t *testing.T) {
	for _, key := range []string{"user.name", "user.signingkey", "gpg.format", "commit.gpgsign", "credential.helper"} {
		if !carried(key) {
			t.Errorf("%s should be carried", key)
		}
	}
	// These are the clone's own, freshly written by git, and overwriting them
	// would break the very thing being provisioned.
	for _, key := range []string{"remote.origin.url", "branch.main.remote", "core.bare", "swarm.anything"} {
		if carried(key) {
			t.Errorf("%s must not be carried", key)
		}
	}
}

func TestReadReportsTheWorkingCopy(t *testing.T) {
	src := source(t)

	st, ok := Read(src)
	if !ok {
		t.Fatal("a repository should be readable")
	}
	if st.Branch == "" {
		t.Error("no branch reported")
	}
	if st.Dirty {
		t.Error("a fresh checkout should be clean")
	}
	if st.Upstream {
		t.Error("there is no tracking branch here")
	}

	// A tracked file changed makes it dirty; an untracked one must not, or an
	// agent's scratch output would mark every workspace forever.
	if err := os.WriteFile(filepath.Join(src, "untracked.tmp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := Read(src); st.Dirty {
		t.Error("an untracked file should not count as dirty")
	}
	if err := os.WriteFile(filepath.Join(src, "README"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ := Read(src); !st.Dirty {
		t.Error("a changed tracked file should count as dirty")
	}
}

func TestReadSaysNoOutsideARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, ok := Read(t.TempDir()); ok {
		t.Error("a directory that is not a repository should report nothing")
	}
}

func TestSummary(t *testing.T) {
	cases := []struct {
		in   State
		want string
	}{
		{State{}, ""},
		{State{Branch: "main"}, "main"},
		{State{Branch: "main", Dirty: true}, "main*"},
		{State{Branch: "fix/x", Ahead: 3}, "fix/x 3↑"},
		{State{Branch: "main", Behind: 12}, "main 12↓"},
		{State{Branch: "main", Dirty: true, Ahead: 1, Behind: 2}, "main* 1↑ 2↓"},
	}
	for _, c := range cases {
		if got := c.in.Summary(); got != c.want {
			t.Errorf("Summary(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}
