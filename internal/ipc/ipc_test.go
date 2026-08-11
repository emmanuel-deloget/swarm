package ipc

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/hub"
	"github.com/emmanuel-deloget/swarm/internal/probe"
)

func TestMain(m *testing.M) {
	if probe.Run(os.Args[1:]) {
		return
	}
	os.Exit(m.Run())
}

func newFleet(t *testing.T, extra string) *hub.Hub {
	t.Helper()
	// The agents are this test binary, driven by verbs; see internal/probe.
	// Single-quoted in the YAML so a Windows path keeps its backslashes.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "swarm.yaml")
	body := `session: itest
defaults:
  idle_after: 300ms
web:
  enabled: false
bus:
  enabled: true
groups:
  dev: [dev-1, dev-2]
agents:
  - name: dev-1
    role: dev
    command: ['` + self + `', '-swarm-probe', 'print', 'ready', 'lines', 'saw:']
  - name: dev-2
    role: dev
    command: ['` + self + `', '-swarm-probe', 'print', 'ready', 'lines', 'saw:']
  - name: rev-1
    role: review
    delivery: pull
    command: ['` + self + `', '-swarm-probe', 'print', 'ready', 'lines', 'saw:']
` + extra
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.New(hub.Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	h.StartAll()
	t.Cleanup(func() { h.Shutdown(2 * time.Second) })

	for _, name := range h.Names() {
		a, err := h.Agent(name)
		if err != nil {
			t.Fatal(err)
		}
		waitForText(t, a.Text, "ready")
	}
	return h
}

func waitForText(t *testing.T, get func() string, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(get(), want) {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %q, screen was:\n%s", want, get())
}

func serve(t *testing.T, h *hub.Hub) *Server {
	t.Helper()
	srv, err := Listen(h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func call(t *testing.T, socket string, req Request) Response {
	t.Helper()
	resp, err := Call(socket, req)
	if err != nil {
		t.Fatalf("%s: %v", req.Cmd, err)
	}
	return resp
}

func TestPingAndInfo(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	if resp := call(t, srv.Path(), Request{Cmd: CmdPing}); !resp.OK {
		t.Fatal("ping failed")
	}
	resp := call(t, srv.Path(), Request{Cmd: CmdInfo})
	if resp.Session != "itest" {
		t.Errorf("session = %q", resp.Session)
	}
	if resp.Socket != srv.Path() || resp.StateDir == "" || resp.Shared == "" {
		t.Errorf("info is incomplete: %+v", resp)
	}
}

func TestListAndTargets(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	all := call(t, srv.Path(), Request{Cmd: CmdList})
	if len(all.Agents) != 3 {
		t.Fatalf("ls returned %d agents, want 3", len(all.Agents))
	}
	group := call(t, srv.Path(), Request{Cmd: CmdList, Target: "@dev"})
	if len(group.Agents) != 2 {
		t.Fatalf("ls @dev returned %d agents, want 2", len(group.Agents))
	}
	if _, err := Call(srv.Path(), Request{Cmd: CmdList, Target: "ghost"}); err == nil {
		t.Error("an unknown target should fail")
	}
}

func TestInjectReachesTheTerminal(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	resp := call(t, srv.Path(), Request{Cmd: CmdInject, Target: "dev-1", Text: "hello there", Submit: true})
	if len(resp.Results) != 1 || !resp.Results[0].OK {
		t.Fatalf("inject results: %+v", resp.Results)
	}
	a, _ := h.Agent("dev-1")
	waitForText(t, a.Text, "saw:hello there")

	// A group target hits everyone in it.
	call(t, srv.Path(), Request{Cmd: CmdInject, Target: "@dev", Text: "everyone", Submit: true})
	for _, name := range []string{"dev-1", "dev-2"} {
		a, _ := h.Agent(name)
		waitForText(t, a.Text, "saw:everyone")
	}
}

func TestKeysReachTheTerminal(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	// Type the word without submitting, then submit with a key.
	call(t, srv.Path(), Request{Cmd: CmdInject, Target: "dev-1", Text: "keyed", Submit: false})
	call(t, srv.Path(), Request{Cmd: CmdKeys, Target: "dev-1", Keys: "enter"})

	a, _ := h.Agent("dev-1")
	waitForText(t, a.Text, "saw:keyed")

	if _, err := Call(srv.Path(), Request{Cmd: CmdKeys, Target: "dev-1", Keys: "not-a-key"}); err == nil {
		t.Error("an unknown key name should fail")
	}
}

func TestPushDeliveryTypesIntoTheTerminal(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	resp := call(t, srv.Path(), Request{Cmd: CmdSend, Target: "dev-1", From: "triage", Text: "take ticket 12"})
	if len(resp.Messages) != 1 {
		t.Fatalf("send returned %d messages", len(resp.Messages))
	}
	if !resp.Messages[0].Pushed {
		t.Error("a push-mode recipient should have the message typed in")
	}
	a, _ := h.Agent("dev-1")
	waitForText(t, a.Text, "take ticket 12")

	// Nothing is left pending once it has been pushed.
	inbox := call(t, srv.Path(), Request{Cmd: CmdInbox, From: "dev-1"})
	if len(inbox.Messages) != 0 {
		t.Errorf("a pushed message should not stay pending: %+v", inbox.Messages)
	}
}

func TestPullDeliveryWaitsForTheInbox(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	resp := call(t, srv.Path(), Request{Cmd: CmdSend, Target: "rev-1", Text: "review this"})
	if resp.Messages[0].Pushed {
		t.Error("a pull-mode recipient should not be interrupted")
	}
	a, _ := h.Agent("rev-1")
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(a.Text(), "review this") {
		t.Fatal("the message was typed into a pull-mode agent")
	}

	peek := call(t, srv.Path(), Request{Cmd: CmdInbox, From: "rev-1", Peek: true})
	if len(peek.Messages) != 1 {
		t.Fatalf("peek returned %d messages", len(peek.Messages))
	}
	got := call(t, srv.Path(), Request{Cmd: CmdInbox, From: "rev-1"})
	if len(got.Messages) != 1 || got.Messages[0].Body != "review this" {
		t.Fatalf("inbox returned %+v", got.Messages)
	}
	again := call(t, srv.Path(), Request{Cmd: CmdInbox, From: "rev-1"})
	if len(again.Messages) != 0 {
		t.Error("collecting twice should return nothing the second time")
	}
}

func TestInboxCanWaitForAMessage(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	done := make(chan Response, 1)
	go func() {
		resp, err := Call(srv.Path(), Request{Cmd: CmdInbox, From: "rev-1", WaitMS: 5000})
		if err == nil {
			done <- resp
		}
	}()
	time.Sleep(200 * time.Millisecond)
	call(t, srv.Path(), Request{Cmd: CmdSend, Target: "rev-1", Text: "late arrival"})

	select {
	case resp := <-done:
		if len(resp.Messages) != 1 || resp.Messages[0].Body != "late arrival" {
			t.Fatalf("waiting inbox returned %+v", resp.Messages)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a waiting inbox never woke up")
	}
}

func TestBroadcastSkipsTheSender(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	resp := call(t, srv.Path(), Request{Cmd: CmdSend, Target: "all", From: "dev-1", Text: "standup"})
	for _, m := range resp.Messages {
		if m.To == "dev-1" {
			t.Error("an agent should not receive its own broadcast")
		}
	}
	if len(resp.Messages) != 2 {
		t.Fatalf("broadcast reached %d agents, want 2", len(resp.Messages))
	}
}

func TestScreenAndStage(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	plain := call(t, srv.Path(), Request{Cmd: CmdScreen, Target: "dev-1", Plain: true})
	if !strings.Contains(plain.Text, "ready") {
		t.Errorf("screen should show the agent output, got %q", plain.Text)
	}
	// The styled rendering carries the same content; whether it contains
	// escape sequences depends on the agent, and this one prints none.
	styled := call(t, srv.Path(), Request{Cmd: CmdScreen, Target: "dev-1"})
	if !strings.Contains(styled.Text, "ready") {
		t.Errorf("the styled screen lost the content: %q", styled.Text)
	}

	staged := call(t, srv.Path(), Request{Cmd: CmdStage, Name: "notes.txt", Data: []byte("payload")})
	data, err := os.ReadFile(staged.Path)
	if err != nil || string(data) != "payload" {
		t.Fatalf("stage did not write the file: %v", err)
	}
}

func TestLifecycle(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	call(t, srv.Path(), Request{Cmd: CmdStop, Target: "dev-2", GraceMS: 1000})
	a, _ := h.Agent("dev-2")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if state, _ := a.State(); state == "exited" || state == "stopped" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if state, _ := a.State(); state != "exited" && state != "stopped" {
		t.Fatalf("dev-2 state after stop = %q", state)
	}

	call(t, srv.Path(), Request{Cmd: CmdStart, Target: "dev-2"})
	waitForText(t, a.Text, "ready")

	call(t, srv.Path(), Request{Cmd: CmdRestart, Target: "dev-2", GraceMS: 1000})
	waitForText(t, a.Text, "ready")
	if a.Info().Restarts == 0 {
		t.Error("a restart should be counted")
	}
}

func TestEventsStream(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	c, err := Dial(srv.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	got := make(chan string, 16)
	go func() {
		_ = c.Stream(Request{Cmd: CmdEvents, Follow: true, Lines: 5}, func(r Response) bool {
			if r.Event != nil {
				got <- r.Event.Text
			}
			return true
		})
	}()
	time.Sleep(200 * time.Millisecond)
	call(t, srv.Path(), Request{Cmd: CmdInject, Target: "dev-1", Text: "streamed", Submit: true})

	deadline := time.After(5 * time.Second)
	for {
		select {
		case text := <-got:
			if strings.Contains(text, "streamed") {
				return
			}
		case <-deadline:
			t.Fatal("the injection never showed up in the event stream")
		}
	}
}

func TestSecondSwarmOnTheSameSessionIsRefused(t *testing.T) {
	h := newFleet(t, "")
	serve(t, h)

	if _, err := Listen(h); err == nil {
		t.Fatal("a second swarm on the same session should be refused")
	}
}

// TestCloseDoesNotWaitOnAClient: shutting down cannot depend on somebody else
// hanging up. An attach holds its connection open for as long as it is
// watching, and a client that simply goes away — a closed laptop, a killed
// terminal — leaves a socket nobody is going to finish reading. Close used to
// wait for those, so `swarm shutdown` could hang on a client that no longer
// existed. Found as a five-minute test timeout on the Windows runner, which is
// the same thing with a deadline.
func TestCloseDoesNotWaitOnAClient(t *testing.T) {
	h := newFleet(t, "")
	srv := serve(t, h)

	// A live, served connection: the ping proves the server is inside its read
	// loop for it rather than about to accept it.
	c, err := net.DialTimeout("unix", srv.Path(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := json.NewEncoder(c).Encode(Request{Cmd: CmdPing}); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err != nil || !resp.OK {
		t.Fatalf("ping: %v %+v", err, resp)
	}

	// And now nothing more is sent. Close must not care.
	done := make(chan struct{})
	go func() { _ = srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close waited on a client that had gone quiet")
	}
}
