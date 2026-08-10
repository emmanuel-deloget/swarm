package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder collects what the server hands to the bus.
type recorder struct {
	mu   sync.Mutex
	sent []string
}

func (r *recorder) send(from, target, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, from+" → "+target+": "+body)
	return nil
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.sent))
	copy(out, r.sent)
	return out
}

func (r *recorder) wait(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := len(r.sent)
		r.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return r.snapshot()
}

// waitLog reads the trace file until it holds everything expected, or gives up.
//
// Waiting on the recorder is not enough: the "delivered" note is written after
// Send returns, so a test that reads the file the moment the recorder fires can
// beat it there. That is a race the test loses only under load, which is the
// worst kind — it passes on a laptop and fails in CI.
func waitLog(t *testing.T, path string, wants ...string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			got = string(raw)
			missing := false
			for _, w := range wants {
				if !strings.Contains(got, w) {
					missing = true
					break
				}
			}
			if !missing {
				return got
			}
		}
		if time.Now().After(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newTestServer(t *testing.T, o Options) (*Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	o.Addr = "127.0.0.1:0"
	o.Send = rec.send
	s, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, rec
}

// post sends a payload and returns the status and the body, having closed it.
func post(t *testing.T, url, body string, header [2]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if header[0] != "" {
		req.Header.Set(header[0], header[1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, got
}

func TestServerDeliversWhatTheRulesProduce(t *testing.T) {
	rule := compile(t, Rule{
		Name:    "new-pr",
		When:    map[string]string{"event": "pull_request", "action": "opened"},
		To:      "@review",
		Message: "une nouvelle PR est apparue : {pull_request.html_url}",
	})
	s, rec := newTestServer(t, Options{Rules: []Rule{rule}})

	status, body := post(t, s.URL(), payload, [2]string{})
	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}
	// The answer says what was decided, so the sender's own delivery log is
	// enough to tell a rejected payload from one that matched nothing.
	var reply struct {
		Matched    int        `json:"matched"`
		Deliveries []Delivery `json:"deliveries"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Matched != 1 || len(reply.Deliveries) != 1 {
		t.Fatalf("reply = %+v, want one delivery", reply)
	}

	sent := rec.wait(t, 1)
	want := "webhook → @review: une nouvelle PR est apparue : https://example.invalid/pr/42"
	if len(sent) != 1 || sent[0] != want {
		t.Errorf("sent = %v, want [%q]", sent, want)
	}
}

func TestServerRejectsWithoutTheToken(t *testing.T) {
	rule := compile(t, Rule{To: "@review", Message: "x"})
	s, rec := newTestServer(t, Options{Rules: []Rule{rule}, Token: "s3cret"})

	cases := []struct {
		what   string
		header [2]string
		want   int
	}{
		{"no token", [2]string{}, http.StatusForbidden},
		{"a wrong token", [2]string{"X-Swarm-Token", "wrong"}, http.StatusForbidden},
		{"the right token", [2]string{"X-Swarm-Token", "s3cret"}, http.StatusAccepted},
		{"a bearer token", [2]string{"Authorization", "Bearer s3cret"}, http.StatusAccepted},
	}
	for _, c := range cases {
		if status, _ := post(t, s.URL(), payload, c.header); status != c.want {
			t.Errorf("%s returned %d, want %d", c.what, status, c.want)
		}
	}
	if got := rec.wait(t, 2); len(got) != 2 {
		t.Errorf("only the two authenticated payloads should have been delivered, got %d", len(got))
	}
}

func TestServerRefusesJunk(t *testing.T) {
	rule := compile(t, Rule{To: "@review", Message: "x"})
	s, rec := newTestServer(t, Options{Rules: []Rule{rule}, MaxBody: 128})

	if status, _ := post(t, s.URL(), "{not json", [2]string{}); status != http.StatusBadRequest {
		t.Errorf("bad JSON returned %d, want 400", status)
	}
	big := `{"body": "` + strings.Repeat("a", 512) + `"}`
	if status, _ := post(t, s.URL(), big, [2]string{}); status != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized body returned %d, want 413", status)
	}
	// Delivery is asynchronous, so give it a moment to fail to happen.
	time.Sleep(100 * time.Millisecond)
	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("nothing should have reached the bus, got %v", got)
	}
}

func TestServerRequiresAValidSignature(t *testing.T) {
	const secret = "s3cret"
	rule := compile(t, Rule{
		When:    map[string]string{"header.X-Reqwire-Event": "pr.opened"},
		To:      "@review",
		Message: "{header.X-Reqwire-Event}",
	})
	s, rec := newTestServer(t, Options{
		Rules:           []Rule{rule},
		Secret:          secret,
		SignatureHeader: "X-Reqwire-Signature",
	})

	sig := "sha256=" + Signature(secret, []byte(payload))

	// Unsigned is refused: an endpoint that accepts unsigned payloads once
	// accepts them always.
	if status, _ := post(t, s.URL(), payload, [2]string{"X-Reqwire-Event", "pr.opened"}); status != http.StatusForbidden {
		t.Errorf("an unsigned delivery returned %d, want 403", status)
	}
	if status, _ := post(t, s.URL(), payload, [2]string{"X-Reqwire-Signature", "sha256=deadbeef"}); status != http.StatusForbidden {
		t.Errorf("a bad signature returned %d, want 403", status)
	}
	// A valid signature over a different body must not carry over.
	if status, _ := post(t, s.URL(), `{"action":"closed"}`, [2]string{"X-Reqwire-Signature", sig}); status != http.StatusForbidden {
		t.Errorf("a signature from another body returned %d, want 403", status)
	}

	// Signed, with the header the rule matches on.
	req, err := http.NewRequest(http.MethodPost, s.URL(), strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Reqwire-Signature", sig)
	req.Header.Set("X-Reqwire-Event", "pr.opened")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("a signed delivery returned %s, want 202", res.Status)
	}

	sent := rec.wait(t, 1)
	if len(sent) != 1 || sent[0] != "webhook → @review: pr.opened" {
		t.Errorf("sent = %v, want the one signed delivery", sent)
	}
}

// TestSecretWithoutAHeaderIsRefused: a secret nobody looks for verifies nothing.
func TestSecretWithoutAHeaderIsRefused(t *testing.T) {
	if _, err := New(Options{Secret: "x", Send: func(string, string, string) error { return nil }}); err == nil {
		t.Error("a secret with no signature header should be refused")
	}
}

// TestTraceRecordsEveryOutcome: the log exists so that "nothing happened" can be
// told apart from "refused", "no rule matched" and "sent". If it only recorded
// the last of those it would be worth nothing, so each path is checked.
func TestTraceRecordsEveryOutcome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "webhooks.log")
	lg, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })

	rule := compile(t, Rule{
		Name:    "new-pr",
		When:    map[string]string{"event": "pull_request", "action": "opened"},
		To:      "@review",
		Message: "PR {pull_request.html_url}",
	})
	s, rec := newTestServer(t, Options{
		Rules:           []Rule{rule},
		Secret:          "s3cret",
		SignatureHeader: "X-Sig",
		Trace:           lg,
	})

	sig := "sha256=" + Signature("s3cret", []byte(payload))
	post(t, s.URL(), payload, [2]string{})                           // unsigned
	post(t, s.URL(), payload, [2]string{"X-Sig", "sha256=deadbeef"}) // bad signature
	post(t, s.URL(), `{"event":"deployment"}`, [2]string{"X-Sig",    // no rule
		"sha256=" + Signature("s3cret", []byte(`{"event":"deployment"}`))}) //
	post(t, s.URL(), payload, [2]string{"X-Sig", sig}) // accepted
	rec.wait(t, 1)

	wants := []string{
		"refused: missing X-Sig",
		"refused: bad signature",
		"accepted, no rule matched",
		"accepted, 1 delivery",
		// The verdict must say why, not merely that it did not match. Conditions
		// are evaluated in sorted order, so "action" is the one blamed.
		`action is absent, want "opened"`,
		// The payload must be there, on one line, ready for `swarm hook test`.
		`"action":"opened"`,
		"send     new-pr → @review: PR https://example.invalid/pr/42",
		// Written after Send returns, so the file has to be waited on rather
		// than read once.
		"delivered: new-pr → @review",
	}
	got := waitLog(t, path, wants...)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("the log should contain %q\n--- log ---\n%s", want, got)
		}
	}

	// A credential must not be written down; a digest may.
	if strings.Contains(got, "s3cret") {
		t.Error("the secret leaked into the log")
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Unix only: Windows reports every readable file as 0666, and who may
	// open it is decided by an ACL that mode bits cannot express.
	if perm := st.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("the log is mode %#o, want 0600: it holds whatever the sender sent", perm)
	}
}
