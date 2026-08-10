package hook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The other direction: a fleet that can be told about the world should be able
// to tell the world about itself. The rules are the incoming ones read
// backwards, so the vocabulary — paths, placeholders, signature, log — is the
// one already written.

type received struct {
	mu   sync.Mutex
	got  []Notice
	sigs []string
	fail int // answer 502 this many times first
}

func (r *received) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.fail > 0 {
			r.fail--
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		var n Notice
		_ = json.Unmarshal(raw, &n)
		r.got = append(r.got, n)
		r.sigs = append(r.sigs, req.Header.Get("X-Swarm-Signature"))
		// Verified here rather than trusted: the far end is meant to be able to
		// check this with the same code swarm checks incoming deliveries with.
		if sig := req.Header.Get("X-Swarm-Signature"); sig != "" {
			if !VerifySignature("s3cret", raw, strings.TrimPrefix(sig, "sha256=")) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func (r *received) wait(t *testing.T, n int) []Notice {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := len(r.got)
		r.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Notice, len(r.got))
	copy(out, r.got)
	return out
}

func sender(t *testing.T, rec *received, o OutOptions) *Sender {
	t.Helper()
	srv := httptest.NewServer(rec.handler())
	t.Cleanup(srv.Close)
	o.URL = srv.URL
	if o.RetryBackoff == 0 {
		o.RetryBackoff = 5 * time.Millisecond
	}
	s, err := NewSender(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestARuleMatchesAnEventAndRendersIt, from the same paths as an incoming one.
func TestARuleMatchesAnEventAndRendersIt(t *testing.T) {
	rec := &received{}
	s := sender(t, rec, OutOptions{
		Rules: []OutRule{{
			Name: "done",
			When: map[string]string{"event": "agent.done", "agent": "~^dev"},
			Body: "{agent} finished on {data.branch}",
		}},
	})

	s.Notify(Notice{Event: "agent.done", Agent: "dev-1", Data: map[string]string{"branch": "main"}})
	s.Notify(Notice{Event: "agent.idle", Agent: "dev-1"})    // wrong event
	s.Notify(Notice{Event: "agent.done", Agent: "review-1"}) // wrong agent
	got := rec.wait(t, 1)

	if len(got) != 1 {
		t.Fatalf("%d notices reached the endpoint, want exactly one: %+v", len(got), got)
	}
	if got[0].Body != "dev-1 finished on main" {
		t.Errorf("the body is %q", got[0].Body)
	}
	if got[0].Rule != "done" {
		t.Errorf("the notice does not name its rule: %q", got[0].Rule)
	}
}

// TestTheBodyIsSigned with the same digest an incoming delivery is checked
// with, so the far end can verify it with the code swarm already has.
func TestTheBodyIsSigned(t *testing.T) {
	rec := &received{}
	s := sender(t, rec, OutOptions{
		Secret:          "s3cret",
		SignatureHeader: "X-Swarm-Signature",
		Rules:           []OutRule{{Name: "all", When: map[string]string{"event": "*"}, Body: "{event}"}},
	})

	s.Notify(Notice{Event: "agent.error", Agent: "dev-1"})
	if got := rec.wait(t, 1); len(got) != 1 {
		t.Fatalf("%d notices arrived", len(got))
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if !strings.HasPrefix(rec.sigs[0], "sha256=") {
		t.Errorf("the signature header is %q", rec.sigs[0])
	}
}

// TestAFailureIsRetried, then given up on — an endpoint that restarts should
// not cost the event, and one that is gone should not cost the fleet.
func TestAFailureIsRetried(t *testing.T) {
	rec := &received{fail: 2}
	s := sender(t, rec, OutOptions{
		Retries: 3,
		Rules:   []OutRule{{Name: "all", When: map[string]string{"event": "*"}, Body: "{event}"}},
	})

	s.Notify(Notice{Event: "agent.done", Agent: "dev-1"})
	if got := rec.wait(t, 1); len(got) != 1 {
		t.Fatalf("two failures then success delivered %d notices", len(got))
	}
}

// TestItGivesUp rather than retrying for ever.
func TestItGivesUp(t *testing.T) {
	rec := &received{fail: 100}
	var noted []string
	var mu sync.Mutex
	s := sender(t, rec, OutOptions{
		Retries: 2,
		Rules:   []OutRule{{Name: "all", When: map[string]string{"event": "*"}, Body: "{event}"}},
		Emit: func(text string) {
			mu.Lock()
			defer mu.Unlock()
			noted = append(noted, text)
		},
	})

	s.Notify(Notice{Event: "agent.done", Agent: "dev-1"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(noted) > 0
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(noted) == 0 {
		t.Fatal("giving up was never reported")
	}
	if !strings.Contains(noted[0], "failed") {
		t.Errorf("the report is %q", noted[0])
	}
}

// TestABodyIsRequired: a notification nobody can read is a POST for nothing.
func TestABodyIsRequired(t *testing.T) {
	r := OutRule{Name: "empty", When: map[string]string{"event": "*"}}
	if err := r.Compile(); err == nil {
		t.Error("a rule with no body compiled")
	}
}

// TestASecretNeedsAHeader, failing closed as the listener does.
func TestASecretNeedsAHeader(t *testing.T) {
	_, err := NewSender(OutOptions{
		URL:    "http://127.0.0.1:1",
		Secret: "s3cret",
		Rules:  []OutRule{{Name: "all", When: map[string]string{"event": "*"}, Body: "x"}},
	})
	if err == nil {
		t.Error("a secret with no header was accepted")
	}
}
