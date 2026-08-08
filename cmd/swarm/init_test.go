package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/config"
)

// TestExampleMatchesTheStarter keeps swarm.example.yaml — the file someone
// browses on the repository page before installing anything — identical to what
// `swarm init` writes. Two copies of the same documentation drift apart
// silently, and the stale one is always the one being read.
func TestExampleMatchesTheStarter(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "swarm.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != starterConfig {
		t.Error("swarm.example.yaml has drifted from the starter config; " +
			"regenerate it with `swarm init -force swarm.example.yaml`")
	}
}

// TestStarterConfigLoads: the file `swarm init` writes must start a swarm as it
// stands, with no editing beyond the agent's command.
func TestStarterConfigLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(starterConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the starter config does not load: %v", err)
	}
	if len(cfg.Agents) != 1 {
		t.Errorf("the starter should define one agent, got %d", len(cfg.Agents))
	}
	if cfg.Web.Enabled {
		t.Error("the starter should not open a web port")
	}
	if cfg.Hooks.Enabled {
		t.Error("the starter should not open a webhook port")
	}
}

// TestStarterExamplesCanBeUncommented is the promise the file makes: everything
// it does not enable is present as a commented example, and uncommenting one is
// enough to use it. An example that does not load is worse than none — it costs
// the reader the time to find out it was wrong.
func TestStarterExamplesCanBeUncommented(t *testing.T) {
	lines := strings.Split(starterConfig, "\n")

	// The commented top-level blocks: a "# <key>:" line and the "#  …" under it.
	for i := 0; i < len(lines); i++ {
		key, ok := commentedKey(lines[i])
		if !ok {
			continue
		}
		end := i + 1
		for end < len(lines) && strings.HasPrefix(lines[end], "#  ") {
			end++
		}
		t.Run(key, func(t *testing.T) {
			out := append([]string{}, lines[:i]...)
			for _, l := range lines[i:end] {
				out = append(out, uncomment(l))
			}
			out = append(out, lines[end:]...)
			mustLoad(t, strings.Join(out, "\n"))
		})
		i = end - 1
	}
}

// commentedKey reports a line of the form "# key:" — a whole block offered for
// uncommenting.
func commentedKey(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "# ")
	if !ok || !strings.HasSuffix(rest, ":") {
		return "", false
	}
	key := strings.TrimSuffix(rest, ":")
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", false
	}
	return key, true
}

// uncomment removes the comment marker while keeping the indentation, which is
// what anyone does with a block like this.
func uncomment(line string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " "))]
	rest := strings.TrimLeft(line, " ")
	rest = strings.TrimPrefix(rest, "#")
	rest = strings.TrimPrefix(rest, " ")
	return indent + rest
}

func mustLoad(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()

	// Turning the hooks block on means turning signature checking on, so the
	// secret it names has to be there. That is a prerequisite of the example,
	// not a defect in it — the file says how to create one.
	if strings.Contains(body, "secret_path: .swarm/hook-secret") {
		if err := os.MkdirAll(filepath.Join(dir, ".swarm"), 0o755); err != nil {
			t.Fatal(err)
		}
		secret := filepath.Join(dir, ".swarm", "hook-secret")
		if err := os.WriteFile(secret, []byte("not-a-real-secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Errorf("uncommenting this block breaks the config: %v", err)
	}
}

// TestIgnoresPattern: people write the same rule several ways, and adding a
// second line that means the same thing is worse than adding nothing.
func TestIgnoresPattern(t *testing.T) {
	for _, body := range []string{".swarm/", ".swarm", "/.swarm/", "/.swarm", "build/\n.swarm/\n"} {
		if !ignoresPattern(body, ".swarm/") {
			t.Errorf("ignoresPattern(%q) = false, want true", body)
		}
	}
	for _, body := range []string{"", "build/", ".swarmish/", "# .swarm/", ".swarm/logs"} {
		if ignoresPattern(body, ".swarm/") {
			t.Errorf("ignoresPattern(%q) = true, want false", body)
		}
	}
}

func TestOfferGitignore(t *testing.T) {
	// Outside a repository there is nothing to ignore, and no file is made.
	t.Run("outside a git repository", func(t *testing.T) {
		dir := t.TempDir()
		if err := offerGitignore(dir, ".swarm", true); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
			t.Error("a .gitignore was created outside a repository")
		}
	})

	t.Run("creates the file", func(t *testing.T) {
		dir := gitRepo(t)
		if err := offerGitignore(dir, ".swarm", true); err != nil {
			t.Fatal(err)
		}
		if got := read(t, filepath.Join(dir, ".gitignore")); !strings.Contains(got, ".swarm/") {
			t.Errorf(".gitignore = %q", got)
		}
	})

	t.Run("keeps what was there", func(t *testing.T) {
		dir := gitRepo(t)
		path := filepath.Join(dir, ".gitignore")
		// No trailing newline: appending naively would corrupt the last rule.
		if err := os.WriteFile(path, []byte("/build"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := offerGitignore(dir, ".swarm", true); err != nil {
			t.Fatal(err)
		}
		got := read(t, path)
		if !strings.HasPrefix(got, "/build\n") {
			t.Errorf("the existing rule was damaged: %q", got)
		}
		if !strings.Contains(got, "\n.swarm/\n") {
			t.Errorf(".gitignore = %q", got)
		}
	})

	t.Run("adds nothing twice", func(t *testing.T) {
		dir := gitRepo(t)
		path := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(path, []byte(".swarm\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := offerGitignore(dir, ".swarm", true); err != nil {
			t.Fatal(err)
		}
		if got := read(t, path); got != ".swarm\n" {
			t.Errorf("an equivalent rule was already there, got %q", got)
		}
	})

	t.Run("honours -swarm-dir", func(t *testing.T) {
		dir := gitRepo(t)
		if err := offerGitignore(dir, ".agents", true); err != nil {
			t.Fatal(err)
		}
		got := read(t, filepath.Join(dir, ".gitignore"))
		if !strings.Contains(got, ".agents/") || strings.Contains(got, ".swarm") {
			t.Errorf(".gitignore = %q", got)
		}
	})

	// Without -yes and with nothing to read on stdin, the answer is no rather
	// than a wait nobody can end.
	t.Run("declines when it cannot ask", func(t *testing.T) {
		dir := gitRepo(t)
		if err := offerGitignore(dir, ".swarm", false); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
			t.Error("the file was written without an answer")
		}
	})
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
