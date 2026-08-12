package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/hub"
)

// newTestHub builds a one-agent fleet whose agent is a shell echoing its input,
// which is enough to prove the round trip browser → pty → screen.
func newTestHub(t *testing.T) *hub.Hub {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "swarm.yaml")
	cfg := `session: test
web:
  enabled: false
bus:
  enabled: true
agents:
  - name: echo-1
    role: test
    command: [sh, -c, "printf 'ready\n'; while IFS= read -r l; do printf 'echo:%s\n' \"$l\"; done"]
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.New(hub.Options{Config: loaded})
	if err != nil {
		t.Fatal(err)
	}
	a, err := h.Agent("echo-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Shutdown(time.Second) })

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(a.Text(), "ready") {
			return h
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent never became ready")
	return nil
}

func newTestServer(t *testing.T, h *hub.Hub, opts Options) *httptest.Server {
	t.Helper()
	if opts.Token == "" {
		opts.Token = "secret"
	}
	s, err := New(h, opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestTokenIsRequired(t *testing.T) {
	h := newTestHub(t)
	ts := newTestServer(t, h, Options{})

	for _, path := range []string{"/", "/api/state", "/app.js"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s without a token = %d, want 401", path, res.StatusCode)
		}
	}

	res, err := http.Get(ts.URL + "/?t=wrong")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("a wrong token gave %d, want 401", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/?t=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the right token gave %d, want 200", res.StatusCode)
	}
	if len(res.Cookies()) == 0 {
		t.Error("the server should set a cookie so later requests need no query token")
	}
}

func TestStateListsAgents(t *testing.T) {
	h := newTestHub(t)
	ts := newTestServer(t, h, Options{})

	res, err := http.Get(ts.URL + "/api/state?t=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	var payload struct {
		Session string `json:"session"`
		Agents  []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"agents"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Session != "test" {
		t.Errorf("session = %q, want test", payload.Session)
	}
	if len(payload.Agents) != 1 || payload.Agents[0].Name != "echo-1" {
		t.Fatalf("unexpected agents: %+v", payload.Agents)
	}
}

func TestWebSocketShowsScreenAndAcceptsInput(t *testing.T) {
	h := newTestHub(t)
	ts := newTestServer(t, h, Options{})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?agent=echo-1&t=secret"
	conn, res, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// A successful upgrade leaves no body to read, but closing it when there is
	// one keeps the connection from being held open on a failed handshake.
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	defer func() { _ = conn.CloseNow() }()

	readFrame := func() wsFrame {
		t.Helper()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var f wsFrame
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return f
	}

	// The first frame is a full screen, and it already shows what the agent
	// printed before we connected.
	first := readFrame()
	if first.Type != "full" {
		t.Fatalf("first frame type = %q, want full", first.Type)
	}
	if !strings.Contains(strings.Join(first.Full, ""), "ready") {
		t.Fatalf("the initial screen should contain the agent's output, got %q", strings.Join(first.Full, ""))
	}
	if first.Cols == 0 || first.Rows == 0 {
		t.Errorf("the frame should carry the geometry, got %dx%d", first.Cols, first.Rows)
	}

	// Type into the agent the way the browser does.
	input, err := json.Marshal(wsInput{Type: "text", Data: "hello-web", Submit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, input); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The echo must come back as changed lines.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f := readFrame()
		var text string
		switch f.Type {
		case "full":
			text = strings.Join(f.Full, "")
		case "diff":
			for _, line := range f.Lines {
				text += line
			}
		}
		if strings.Contains(text, "echo:hello-web") {
			return
		}
	}
	t.Fatal("the agent never echoed what the browser typed")
}

func TestReadOnlyRefusesInput(t *testing.T) {
	h := newTestHub(t)
	ts := newTestServer(t, h, Options{ReadOnly: true})

	res, err := http.Post(ts.URL+"/api/action?t=secret", "application/json",
		strings.NewReader(`{"action":"inject","target":"echo-1","text":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("read-only action gave %d, want 403", res.StatusCode)
	}

	a, err := h.Agent("echo-1")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(a.Text(), "nope") {
		t.Fatal("a read-only server let input through")
	}
}

func TestUploadStagesFile(t *testing.T) {
	h := newTestHub(t)
	ts := newTestServer(t, h, Options{})

	body := &strings.Builder{}
	boundary := "swarmtestboundary"
	fmt.Fprintf(body, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"shot.png\"\r\n\r\n", boundary)
	body.WriteString("not-really-a-png")
	fmt.Fprintf(body, "\r\n--%s--\r\n", boundary)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/upload?t=secret", strings.NewReader(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("upload gave %d", res.StatusCode)
	}
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(payload.Path)
	if err != nil {
		t.Fatalf("the staged file should exist: %v", err)
	}
	if string(data) != "not-really-a-png" {
		t.Fatalf("staged content = %q", data)
	}
	if !strings.HasSuffix(payload.Path, "shot.png") {
		t.Errorf("the staged name should keep the original: %s", payload.Path)
	}
}

// TestTheStylesheetAdaptsToNarrowScreens: the page is meant to be usable from a
// phone — that is half of why the remote control exists — and the layout was
// fixed at one size. The terminal itself cannot adapt, being as wide as its
// agent was told to be, but everything around it can.
//
// A stylesheet cannot be checked by looking at it, so this checks the little
// that can be: that the breakpoints are served and that the rule which costs
// the most on a phone is there — the sidebar giving up its column.
func TestTheStylesheetAdaptsToNarrowScreens(t *testing.T) {
	h := newTestHub(t)
	ts := newTestServer(t, h, Options{})
	res, err := http.Get(ts.URL + "/style.css?t=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	css := string(body)

	for _, want := range []string{
		"@media (max-width: 900px)",
		"@media (max-width: 620px)",
		"@media (max-width: 420px)",
		"flex-direction: column", // the sidebar stops being a column of its own
	} {
		if !strings.Contains(css, want) {
			t.Errorf("the stylesheet has no %q", want)
		}
	}

	if strings.Count(css, "{") != strings.Count(css, "}") {
		t.Errorf("unbalanced braces: %d { and %d }", strings.Count(css, "{"), strings.Count(css, "}"))
	}
}
