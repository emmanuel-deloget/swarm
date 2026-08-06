package config

import (
	"os"
	"path/filepath"
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
	path := write(t, `
workdir: sub
shared: elsewhere/files
agents:
  - name: a
    command: [x]
  - name: b
    command: [x]
    workdir: /tmp
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
	if b.Workdir != "/tmp" {
		t.Errorf("an absolute workdir should be kept as is, got %q", b.Workdir)
	}
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
