package config

import (
	"os"
	"strings"
	"testing"
)

func TestCheckFindsSharedRestatingTheDefault(t *testing.T) {
	// What every `swarm init` wrote before state_dir existed.
	path := write(t, "shared: .swarm/shared\nagents:\n  - name: a\n    command: [x]\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	found, err := Check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Check != "shared-restates-default" {
		t.Fatalf("Check() = %+v, want the shared finding", found)
	}
	if found[0].Severity != Warn {
		t.Errorf("severity = %q: nothing is broken today, it is a trap for later", found[0].Severity)
	}
}

// TestCheckFindsItWhateverTheStateDir: restating the default is redundant even
// when the value is right today — it is the *next* change of state_dir that the
// line would fail to follow.
func TestCheckFindsItWhateverTheStateDir(t *testing.T) {
	cfg, err := Load(write(t, "state_dir: .agents\nshared: .agents/shared\nagents:\n  - name: a\n    command: [x]\n"))
	if err != nil {
		t.Fatal(err)
	}
	found, err := Check(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("Check() = %+v, want the shared finding", found)
	}
}

func TestCheckStaysQuietWhenItShould(t *testing.T) {
	cases := map[string]string{
		"nothing written":       "agents:\n  - name: a\n    command: [x]\n",
		"shared somewhere else": "shared: /tmp/staged\nagents:\n  - name: a\n    command: [x]\n",
		"state_dir alone":       "state_dir: .agents\nagents:\n  - name: a\n    command: [x]\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(write(t, body))
			if err != nil {
				t.Fatal(err)
			}
			found, err := Check(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(found) != 0 {
				t.Errorf("Check() = %+v, want nothing", found)
			}
		})
	}
}

// TestApplyChangesNothingElse is the property that matters: a fix must leave the
// file it did not come to change, comments and all, and must not alter what the
// config resolves to.
func TestApplyChangesNothingElse(t *testing.T) {
	body := `session: demo

# Files injected or attached to messages are staged here so every agent can
# read them by path (this is how images reach an agent).
shared: .swarm/shared

# Added to the environment of every agent.
env: {}

agents:
  - name: a
    command: [x]      # a trailing comment
`
	path := write(t, body)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	before := cfg.Shared[len(cfg.Dir()):]

	found, err := Check(cfg)
	if err != nil || len(found) != 1 {
		t.Fatalf("Check() = %+v, %v", found, err)
	}
	if err := found[0].Apply(path); err != nil {
		t.Fatal(err)
	}

	got := read(t, path)
	// The key and the comment block that described it go together: a comment
	// explaining a setting that is no longer there is worse than the setting.
	for _, gone := range []string{"shared:", "staged here"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived the fix:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"session: demo", "# Added to the environment", "env: {}", "# a trailing comment"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was lost:\n%s", kept, got)
		}
	}

	after, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := after.Shared[len(after.Dir()):]; got != before {
		t.Errorf("the fix changed what shared resolves to: %s → %s", before, got)
	}
	if rest, err := Check(after); err != nil || len(rest) != 0 {
		t.Errorf("the config is still reported stale after its fix: %+v", rest)
	}
}

// TestApplyKeepsAnUnattachedComment: only a comment block standing on its own
// belongs to the key below it. One that continues a paragraph above does not.
func TestApplyKeepsAnUnattachedComment(t *testing.T) {
	body := "# about the session\nsession: demo\n# still about the session\nshared: .swarm/shared\nagents:\n  - name: a\n    command: [x]\n"
	path := write(t, body)
	cfg, _ := Load(path)
	found, _ := Check(cfg)
	if len(found) != 1 {
		t.Fatalf("want one finding, got %d", len(found))
	}
	if err := found[0].Apply(path); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Contains(got, "shared:") {
		t.Errorf("the key survived:\n%s", got)
	}
	if !strings.Contains(got, "# still about the session") {
		t.Errorf("a comment that was not the key's own was removed:\n%s", got)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
