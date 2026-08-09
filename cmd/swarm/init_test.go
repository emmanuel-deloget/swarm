package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/config"
)

// The starter file claims to have every setting present as a commented example.
// That claim rots quietly: a key added to the config is a key nobody writes into
// the file that is supposed to teach it, and a commented block that no longer
// parses is worse than a missing one — it is discovered by the person who
// uncomments it.

// TestTheStarterLoads is the floor: what `swarm init` writes must run.
func TestTheStarterLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(starterConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("the file swarm init writes does not load: %v", err)
	}
}

// TestTheExampleIsTheStarter: swarm.example.yaml is in the repository so it can
// be read before installing anything. Shipping a stale copy teaches the wrong
// file.
func TestTheExampleIsTheStarter(t *testing.T) {
	shipped, err := os.ReadFile("../../swarm.example.yaml")
	if err != nil {
		t.Skipf("no shipped example: %v", err)
	}
	if string(shipped) != starterConfig {
		t.Error("swarm.example.yaml has drifted from what `swarm init` writes; " +
			"regenerate it with `swarm init`")
	}
}

// blockStart matches a commented top-level key, which is where an example block
// begins.
var blockStart = regexp.MustCompile(`^[a-z_]+:\s*$`)

// TestEveryCommentedBlockLoadsWhenUncommented reads the file the way a user
// does: uncomment the block you want, and run. A block that only looks right is
// the failure this catches.
func TestEveryCommentedBlockLoadsWhenUncommented(t *testing.T) {
	// The agents the example blocks refer to, so a target resolves.
	const fleet = "agents:\n" +
		"  - name: dev-1\n    role: dev\n    command: [cat]\n" +
		"  - name: review-1\n    role: review\n    command: [cat]\n" +
		"  - name: triage-1\n    command: [cat]\n\n"

	dir := t.TempDir()
	// hooks: names a secret file, and the block tells you to create it first.
	secrets := filepath.Join(dir, ".swarm")
	if err := os.MkdirAll(secrets, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secrets, "hook-secret"), []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, body := range commentedBlocks(starterConfig) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".yaml")
			if err := os.WriteFile(path, []byte(fleet+body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); err != nil {
				t.Errorf("uncommenting the %s block gives a file that does not load: %v", name, err)
			}
		})
	}
}

// commentedBlocks returns each commented YAML block by its top-level key, with
// the comment markers taken off — which is exactly what a user does to it.
func commentedBlocks(src string) map[string]string {
	out := map[string]string{}
	var name string
	var body []string
	flush := func() {
		if name != "" {
			out[name] = strings.Join(body, "\n")
		}
		name, body = "", nil
	}
	for _, line := range strings.Split(src, "\n") {
		if !strings.HasPrefix(line, "#") {
			flush()
			continue
		}
		text := strings.TrimPrefix(strings.TrimPrefix(line, "#"), " ")
		switch {
		case name == "" && blockStart.MatchString(text):
			name = strings.TrimSuffix(strings.TrimSpace(text), ":")
			body = []string{text}
		case name != "":
			if strings.TrimSpace(line) == "#" {
				body = append(body, "")
				continue
			}
			body = append(body, text)
		}
	}
	flush()
	return out
}

// TestTheStarterMentionsWhatTheFleetCanDo names the keys whose absence would
// leave a reader unaware that the mechanism exists at all. It is a list of
// features, not of fields: adding a key to the config need not touch this test,
// but adding a *capability* should.
func TestTheStarterMentionsWhatTheFleetCanDo(t *testing.T) {
	for _, key := range []string{
		"state_dir", "workspace", "on_start", "on_exit", "{alloc_port}",
		"message:", "can_send", "delivery_by_kind", "max_turns", "escalate_to",
		"agents_template", "delivery: defer",
	} {
		if !strings.Contains(starterConfig, key) {
			t.Errorf("the starter never mentions %q, so nobody reading it learns the feature exists", key)
		}
	}
}
