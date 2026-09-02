package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := write(t, `
agents:
  - name: dev-1
    command: [claude]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Session != "default" {
		t.Errorf("session = %q, want default", cfg.Session)
	}
	a, ok := cfg.Agent("dev-1")
	if !ok {
		t.Fatal("dev-1 not found")
	}
	if a.Cols != 200 || a.Rows != 50 {
		t.Errorf("geometry = %dx%d, want 200x50", a.Cols, a.Rows)
	}
	if a.DeliveryMode != DeliveryPush {
		t.Errorf("delivery = %q, want push", a.DeliveryMode)
	}
	if !a.AutostartEnabled() {
		t.Error("autostart should default to true")
	}
	if a.RestartEnabled() {
		t.Error("restart_on_exit should default to false")
	}
	if a.Workdir != filepath.Dir(path) {
		t.Errorf("workdir = %q, want the config directory", a.Workdir)
	}
	if a.MessageTemplate == "" {
		t.Error("a message template should be inherited")
	}
}

func TestDefaultsAreInheritedAndOverridable(t *testing.T) {
	path := write(t, `
defaults:
  cols: 120
  rows: 30
  idle_after: 10s
  delivery: pull
  autostart: false
agents:
  - name: a
    command: [x]
  - name: b
    command: [y]
    cols: 80
    delivery: push
    autostart: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Agent("a")
	if a.Cols != 120 || a.Rows != 30 || a.IdleAfter != 10*time.Second {
		t.Errorf("a did not inherit the defaults: %+v", a)
	}
	if a.DeliveryMode != DeliveryPull || a.AutostartEnabled() {
		t.Errorf("a did not inherit delivery/autostart: %q %v", a.DeliveryMode, a.AutostartEnabled())
	}
	b, _ := cfg.Agent("b")
	if b.Cols != 80 || b.Rows != 30 {
		t.Errorf("b should override only cols: %dx%d", b.Cols, b.Rows)
	}
	if b.DeliveryMode != DeliveryPush || !b.AutostartEnabled() {
		t.Errorf("b should override delivery/autostart: %q %v", b.DeliveryMode, b.AutostartEnabled())
	}
}

func TestResolveTargets(t *testing.T) {
	path := write(t, `
groups:
  backend: [dev-1, dev-3]
agents:
  - name: dev-1
    role: dev
    command: [x]
  - name: dev-2
    role: dev
    command: [x]
  - name: dev-3
    role: review
    command: [x]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		target string
		want   []string
	}{
		{"dev-1", []string{"dev-1"}},
		{"all", []string{"dev-1", "dev-2", "dev-3"}},
		{"*", []string{"dev-1", "dev-2", "dev-3"}},
		{"@dev", []string{"dev-1", "dev-2"}},        // by role
		{"@review", []string{"dev-3"}},              // by role
		{"@backend", []string{"dev-1", "dev-3"}},    // by group
		{"dev-2,dev-1", []string{"dev-2", "dev-1"}}, // order is preserved
		{"@dev,dev-1", []string{"dev-1", "dev-2"}},  // no duplicates
		{" dev-1 , dev-3 ", []string{"dev-1", "dev-3"}},
	}
	for _, c := range cases {
		got, err := cfg.Resolve(c.target)
		if err != nil {
			t.Errorf("Resolve(%q): %v", c.target, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("Resolve(%q) = %v, want %v", c.target, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("Resolve(%q) = %v, want %v", c.target, got, c.want)
				break
			}
		}
	}

	for _, bad := range []string{"", "nope", "@nope", ","} {
		if _, err := cfg.Resolve(bad); err == nil {
			t.Errorf("Resolve(%q) should have failed", bad)
		}
	}
}

func TestLoadRejectsBadConfigs(t *testing.T) {
	cases := map[string]string{
		"no agents":         "session: x\n",
		"missing name":      "agents:\n  - command: [x]\n",
		"missing command":   "agents:\n  - name: a\n",
		"duplicate name":    "agents:\n  - name: a\n    command: [x]\n  - name: a\n    command: [y]\n",
		"bad delivery":      "agents:\n  - name: a\n    command: [x]\n    delivery: telepathy\n",
		"bad pattern":       "agents:\n  - name: a\n    command: [x]\n    patterns:\n      - match: \"([\"\n",
		"unknown group ref": "groups:\n  g: [ghost]\nagents:\n  - name: a\n    command: [x]\n",
		"unknown field":     "agents:\n  - name: a\n    command: [x]\n    colours: 8\n",
		"name with space":   "agents:\n  - name: \"a b\"\n    command: [x]\n",
		"lonely tls":        "web:\n  tls_cert: /tmp/c.pem\nagents:\n  - name: a\n    command: [x]\n",
	}
	for label, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: expected an error", label)
		}
	}
}

func TestPatternsAreCompiled(t *testing.T) {
	path := write(t, `
agents:
  - name: a
    command: [x]
    patterns:
      - match: "(?i)proceed\\?"
        state: approval
        notify: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Agent("a")
	if len(a.Patterns) != 1 {
		t.Fatalf("want 1 pattern, got %d", len(a.Patterns))
	}
	re := a.Patterns[0].Regexp()
	if re == nil {
		t.Fatal("the pattern was not compiled")
	}
	if !re.MatchString("Do you want to PROCEED? yes") {
		t.Error("the compiled pattern does not match what it should")
	}
}

func TestSharedAndWorkdirResolveAgainstConfig(t *testing.T) {
	// Quoted, because an absolute path on Windows carries a drive letter and a
	// colon, and a plain YAML scalar is the wrong place to find out whether
	// that parses.
	elsewhere := absElsewhere(t)
	path := write(t, `
workdir: sub
shared: elsewhere/files
agents:
  - name: a
    command: [x]
  - name: b
    command: [x]
    workdir: '`+elsewhere+`'
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(path)
	if cfg.Workdir != filepath.Join(base, "sub") {
		t.Errorf("workdir = %q", cfg.Workdir)
	}
	if cfg.Shared != filepath.Join(base, "elsewhere", "files") {
		t.Errorf("shared = %q", cfg.Shared)
	}
	a, _ := cfg.Agent("a")
	if a.Workdir != filepath.Join(base, "sub") {
		t.Errorf("agent workdir = %q, want the inherited one", a.Workdir)
	}
	b, _ := cfg.Agent("b")
	if b.Workdir != elsewhere {
		t.Errorf("an absolute workdir should be kept as is, got %q", b.Workdir)
	}
}

// absElsewhere is a path that is absolute wherever this runs, and not under the
// config. `/tmp` is neither on Windows: with no drive letter it is a relative
// path, and resolve joins it against the config's directory — which is the
// correct behaviour and the opposite of what the test meant to check.
//
// Called once and kept: t.TempDir() hands out a new directory each time, so a
// second call would compare a path the file never held.
func absElsewhere(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(t.TempDir(), "elsewhere"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestBusIsEnabledByDefault(t *testing.T) {
	cfg, err := Load(write(t, "agents:\n  - name: a\n    command: [x]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.BusEnabled() {
		t.Error("the bus should be on unless it is explicitly disabled")
	}

	off, err := Load(write(t, "bus:\n  enabled: false\nagents:\n  - name: a\n    command: [x]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if off.BusEnabled() {
		t.Error("bus.enabled: false should turn it off")
	}
}

func TestDetachKey(t *testing.T) {
	cfg, err := Load(write(t, "agents:\n  - name: a\n    command: [x]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DetachKey != DefaultDetachKey {
		t.Errorf("detach_key = %q, want the default %q", cfg.DetachKey, DefaultDetachKey)
	}

	// Anything the keys command understands is accepted, which is what lets you
	// get out of the way of tmux or asciinema.
	for _, key := range []string{"ctrl+g", "ctrl+]", "^q", "esc esc", "f12"} {
		cfg, err := Load(write(t, "detach_key: \""+key+"\"\nagents:\n  - name: a\n    command: [x]\n"))
		if err != nil {
			t.Errorf("detach_key %q was rejected: %v", key, err)
			continue
		}
		if cfg.DetachKey != key {
			t.Errorf("detach_key = %q, want %q", cfg.DetachKey, key)
		}
	}

	// A name nobody can type is refused at load time, not at detach time.
	if _, err := Load(write(t, "detach_key: \"ctrl+nonsense\"\nagents:\n  - name: a\n    command: [x]\n")); err == nil {
		t.Error("an unknown key name should be rejected")
	}
}

func TestHookRulesAreValidatedAtLoad(t *testing.T) {
	valid := `
hooks:
  enabled: true
  rules:
    - name: new-pr
      when:
        event: pull_request
      to: "@review"
      message: "PR {number}"
agents:
  - name: review-1
    role: review
    command: [claude]
`
	cfg, err := Load(write(t, valid))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hooks.Addr != "127.0.0.1:7778" {
		t.Errorf("addr = %q, want the loopback default", cfg.Hooks.Addr)
	}
	if cfg.Hooks.From != "webhook" {
		t.Errorf("from = %q, want webhook", cfg.Hooks.From)
	}
	if cfg.Hooks.MaxBody == 0 {
		t.Error("max_body should have a default")
	}

	// A rule naming a target nobody serves is worth reporting when the file is
	// read, not when the event arrives at three in the morning.
	bad := `
hooks:
  enabled: true
  rules:
    - to: "@nobody"
      message: "x"
agents:
  - name: review-1
    role: review
    command: [claude]
`
	if _, err := Load(write(t, bad)); err == nil {
		t.Error("a rule with an unknown target should be refused")
	}

	// So is a rule that cannot produce anything.
	noMessage := `
hooks:
  enabled: true
  rules:
    - to: review-1
agents:
  - name: review-1
    command: [claude]
`
	if _, err := Load(write(t, noMessage)); err == nil {
		t.Error("a rule with no message should be refused")
	}

	// And a condition whose regexp does not compile.
	badRegexp := `
hooks:
  enabled: true
  rules:
    - when:
        ref: "~[unclosed"
      to: review-1
      message: "x"
agents:
  - name: review-1
    command: [claude]
`
	if _, err := Load(write(t, badRegexp)); err == nil {
		t.Error("a broken regexp should be refused")
	}
}

// writeSecret drops a secret file with the given mode next to a config.
func writeSecret(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile applies the umask; force the mode we are testing.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func hookConfig(secretLine string) string {
	return `
hooks:
  enabled: true
  signature_header: X-Reqwire-Signature
` + secretLine + `
  rules:
    - to: review-1
      message: "x"
agents:
  - name: review-1
    command: [claude]
`
}

func TestSecretPathIsReadFromAFileOnlyItsOwnerCanRead(t *testing.T) {
	path := write(t, hookConfig("  secret_path: secret.txt"))
	dir := filepath.Dir(path)
	// A trailing newline is what `openssl rand -hex 32 > file` leaves behind,
	// and it must not become part of the secret.
	writeSecret(t, dir, "secret.txt", "un-secret-partage\n", 0o600)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hooks.Secret != "un-secret-partage" {
		t.Errorf("secret = %q, want the file contents without the newline", cfg.Hooks.Secret)
	}
	if !filepath.IsAbs(cfg.Hooks.SecretPath) {
		t.Errorf("secret_path = %q, want it resolved against the config file", cfg.Hooks.SecretPath)
	}
}

func TestSecretPathRefusesAReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Not a gap in coverage but the absence of the thing covered: Windows
		// has no POSIX modes, so there is no readable-by-others to refuse.
		// What the file allows lives in its ACL, and checking that is its own
		// piece of work — see docs/configuration.md, which says as much to
		// whoever configures it.
		t.Skip("secret_path permissions are a Unix guarantee")
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660, 0o666} {
		t.Run(fmt.Sprintf("%#o", mode), func(t *testing.T) {
			path := write(t, hookConfig("  secret_path: secret.txt"))
			writeSecret(t, filepath.Dir(path), "secret.txt", "s3cret", mode)

			_, err := Load(path)
			if err == nil {
				t.Fatalf("mode %#o should have been refused", mode)
			}
			if !strings.Contains(err.Error(), "chmod 600") {
				t.Errorf("the error should say how to fix it, got %v", err)
			}
		})
	}
}

func TestSecretPathAcceptsAReadOnlyFile(t *testing.T) {
	// 0400 is stricter than 0600, not weaker: refusing it would be pedantry.
	path := write(t, hookConfig("  secret_path: secret.txt"))
	writeSecret(t, filepath.Dir(path), "secret.txt", "s3cret", 0o400)
	if _, err := Load(path); err != nil {
		t.Errorf("mode 0400 should be accepted: %v", err)
	}
}

func TestSecretPathRejectsTheUnusable(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		path := write(t, hookConfig("  secret_path: nowhere.txt"))
		if _, err := Load(path); err == nil {
			t.Error("a missing secret file should be refused")
		}
	})
	t.Run("empty", func(t *testing.T) {
		path := write(t, hookConfig("  secret_path: secret.txt"))
		writeSecret(t, filepath.Dir(path), "secret.txt", "\n \n", 0o600)
		if _, err := Load(path); err == nil {
			t.Error("a blank secret file should be refused")
		}
	})
	t.Run("a directory", func(t *testing.T) {
		path := write(t, hookConfig("  secret_path: sub"))
		if err := os.Mkdir(filepath.Join(filepath.Dir(path), "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Error("a directory should be refused")
		}
	})
}

// TestOnlyOneSecretSource: preferring one source over another in silence is how
// a swarm keeps verifying against a secret its owner thought they had replaced.
func TestOnlyOneSecretSource(t *testing.T) {
	pairs := [][2]string{
		{"  secret: a", "  secret_env: SWARM_TEST_SECRET"},
		{"  secret: a", "  secret_path: secret.txt"},
		{"  secret_env: SWARM_TEST_SECRET", "  secret_path: secret.txt"},
	}
	for _, p := range pairs {
		path := write(t, hookConfig(p[0]+"\n"+p[1]))
		writeSecret(t, filepath.Dir(path), "secret.txt", "s3cret", 0o600)
		t.Setenv("SWARM_TEST_SECRET", "s3cret")
		if _, err := Load(path); err == nil {
			t.Errorf("%s with %s should be refused", strings.TrimSpace(p[0]), strings.TrimSpace(p[1]))
		}
	}
}

func TestSecretEnvIsTrimmed(t *testing.T) {
	t.Setenv("SWARM_TEST_SECRET", "  s3cret\n")
	path := write(t, hookConfig("  secret_env: SWARM_TEST_SECRET"))
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hooks.Secret != "s3cret" {
		t.Errorf("secret = %q, want it trimmed", cfg.Hooks.Secret)
	}
}

// TestADisabledListenerDoesNotBlockTheConfig: a hooks block that is switched off
// must not make the whole file unloadable because its secret has not been
// created yet. Otherwise `swarm ls` fails over a listener nobody asked to run —
// and a config shipped as a template cannot be read at all.
func TestADisabledListenerDoesNotBlockTheConfig(t *testing.T) {
	body := `
hooks:
  enabled: false
  secret_path: .swarm/hook-secret
  signature_header: X-Hub-Signature-256
  rules:
    - when: {event: pull_request.review_requested}
      to: review-1
      message: "a review was asked of you"
agents:
  - name: review-1
    command: [claude]
`
	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatalf("a disabled listener should not fail the load: %v", err)
	}
	if cfg.Hooks.Secret != "" {
		t.Error("nothing should have been read")
	}

	// The shape is still checked, though: a rule that could never work is worth
	// reporting whether or not the listener runs today.
	broken := `
hooks:
  enabled: false
  rules:
    - when: {event: x}
      to: "@nobody"
      message: "x"
agents:
  - name: review-1
    command: [claude]
`
	if _, err := Load(write(t, broken)); err == nil {
		t.Error("an unknown target should be refused even when hooks are off")
	}

	// And so are the secret sources, which cost nothing to check.
	both := `
hooks:
  enabled: false
  secret: a
  secret_path: b
  signature_header: X-Sig
agents:
  - name: review-1
    command: [claude]
`
	if _, err := Load(write(t, both)); err == nil {
		t.Error("two secret sources should be refused even when hooks are off")
	}

	// Enabled, the secret is read — and a missing file is fatal.
	enabled := `
hooks:
  enabled: true
  secret_path: .swarm/hook-secret
  signature_header: X-Hub-Signature-256
agents:
  - name: review-1
    command: [claude]
`
	if _, err := Load(write(t, enabled)); err == nil {
		t.Error("an enabled listener with no secret file should be refused")
	}
}

func TestWorkspaceModes(t *testing.T) {
	path := write(t, `
state_dir: .state
workdir: .
agents:
  - name: a                       # default: shared, in the common workdir
    command: [x]
  - name: b
    command: [x]
    workspace: clone              # nowhere named: under the state directory
  - name: c
    command: [x]
    workspace: clone
    workdir: ../clones/c          # named: provisioned in place
  - name: d
    command: [x]
    workspace: none
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(path)
	rel := func(p string) string {
		r, err := filepath.Rel(base, p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// Written with forward slashes and compared after conversion: a path is
	// joined with the separator of the machine it runs on, and a literal
	// ".state/workspaces/b" is an assertion about Unix rather than about
	// workspace modes.
	for _, c := range []struct{ name, mode, workdir string }{
		{"a", WorkspaceShared, "."},
		{"b", WorkspaceClone, filepath.FromSlash(".state/workspaces/b")},
		{"c", WorkspaceClone, filepath.FromSlash("../clones/c")},
		{"d", WorkspaceNone, "."},
	} {
		got, ok := cfg.Agent(c.name)
		if !ok {
			t.Fatalf("%s not found", c.name)
		}
		if got.Workspace != c.mode {
			t.Errorf("%s: workspace = %q, want %q", c.name, got.Workspace, c.mode)
		}
		if rel(got.Workdir) != c.workdir {
			t.Errorf("%s: workdir = %q, want %q", c.name, rel(got.Workdir), c.workdir)
		}
	}
}

func TestWorkspaceIsInherited(t *testing.T) {
	cfg, err := Load(write(t, "defaults:\n  workspace: clone\nagents:\n  - name: a\n    command: [x]\n"+
		"  - name: b\n    command: [x]\n    workspace: shared\n"))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := cfg.Agent("a")
	b, _ := cfg.Agent("b")
	if a.Workspace != WorkspaceClone {
		t.Errorf("a should inherit clone, got %q", a.Workspace)
	}
	if b.Workspace != WorkspaceShared {
		t.Errorf("b should override to shared, got %q", b.Workspace)
	}
}

func TestWorkspaceRejectsAnythingElse(t *testing.T) {
	if _, err := Load(write(t, "agents:\n  - name: a\n    command: [x]\n    workspace: worktree\n")); err == nil {
		t.Error("an unknown workspace mode should be refused at load")
	}
}

func TestOpeningMessage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "brief.txt"), []byte("from a file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "swarm.yaml")
	body := `
agents:
  - name: inline
    command: [x]
    message: |
      first line

      third line
  - name: fromfile
    command: [x]
    message_file: brief.txt
  - name: silent
    command: [x]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	inline, _ := cfg.Agent("inline")
	if inline.Message != "first line\n\nthird line\n" {
		t.Errorf("the block scalar came out as %q", inline.Message)
	}
	fromfile, _ := cfg.Agent("fromfile")
	if fromfile.Message != "from a file\n" {
		t.Errorf("message_file gave %q", fromfile.Message)
	}
	silent, _ := cfg.Agent("silent")
	if silent.Message != "" {
		t.Errorf("an agent with no message got %q", silent.Message)
	}
}

func TestOpeningMessageRejectsBothSources(t *testing.T) {
	body := "agents:\n  - name: a\n    command: [x]\n    message: hello\n    message_file: brief.txt\n"
	if _, err := Load(write(t, body)); err == nil {
		t.Error("naming both message and message_file should be refused")
	}
}

func TestOpeningMessageFileMustExist(t *testing.T) {
	body := "agents:\n  - name: a\n    command: [x]\n    message_file: nowhere.txt\n"
	if _, err := Load(write(t, body)); err == nil {
		t.Error("a missing message_file should be refused at load")
	}
}

func TestDeliveryDeferIsAccepted(t *testing.T) {
	cfg, err := Load(write(t, "agents:\n  - name: a\n    command: [x]\n    delivery: defer\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := cfg.Agent("a"); a.DeliveryMode != DeliveryDefer {
		t.Errorf("delivery = %q, want defer", a.DeliveryMode)
	}
}

func TestDeliveryByKind(t *testing.T) {
	cfg, err := Load(write(t, "delivery_by_kind:\n  fyi: pull\n  decision: push\nagents:\n  - name: a\n    command: [x]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeliveryByKind["fyi"] != DeliveryPull {
		t.Errorf("delivery_by_kind = %v", cfg.DeliveryByKind)
	}
	for _, bad := range []string{
		"delivery_by_kind:\n  nonsense: pull\nagents:\n  - name: a\n    command: [x]\n",
		"delivery_by_kind:\n  fyi: telepathy\nagents:\n  - name: a\n    command: [x]\n",
	} {
		if _, err := Load(write(t, bad)); err == nil {
			t.Errorf("should have been refused:\n%s", bad)
		}
	}
}

func TestMayReach(t *testing.T) {
	cfg, err := Load(write(t, `
groups:
  leads: [lead-1]
agents:
  - name: dev-1
    role: dev
    command: [x]
    can_send: ["@leads"]
  - name: dev-2
    role: dev
    command: [x]
  - name: lead-1
    role: lead
    command: [x]
`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		from, to string
		want     bool
	}{
		{"dev-1", "lead-1", true},  // named through a group
		{"dev-1", "dev-2", false},  // sideways, which is the point
		{"dev-2", "dev-1", true},   // no restriction declared
		{"lead-1", "dev-1", true},  // ditto
		{"user", "dev-1", true},    // you are not an agent, and not restricted
		{"webhook", "dev-1", true}, // nor is a webhook
	}
	for _, c := range cases {
		if got, _ := cfg.MayReach(c.from, c.to); got != c.want {
			t.Errorf("MayReach(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}

	// The refusal is read by an agent, so it has to say where to go instead.
	_, why := cfg.MayReach("dev-1", "dev-2")
	for _, want := range []string{"dev-1", "dev-2", "@leads"} {
		if !strings.Contains(why, want) {
			t.Errorf("the refusal should mention %q, got %q", want, why)
		}
	}
}

func TestCanSendIsCheckedAtLoad(t *testing.T) {
	body := "agents:\n  - name: a\n    command: [x]\n    can_send: [\"@nobody\"]\n"
	if _, err := Load(write(t, body)); err == nil {
		t.Error("can_send naming an unknown target should be refused at load")
	}
}
