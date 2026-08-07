package hook

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/event"
)

// SendFunc delivers one rendered message on the bus. hub.Hub.Send is what it
// wraps: a webhook is just another sender, so it inherits target resolution,
// per-agent push/pull and the message history for free.
type SendFunc func(from, target, body string) error

// Options configures the listener.
type Options struct {
	// Addr is where to listen, "host:port".
	Addr string

	// Token, when set, must be presented as "X-Swarm-Token", as a bearer token,
	// or as ?t= in the query.
	Token string

	// Secret, when set, enables HMAC-SHA256 verification of the raw body. A
	// request without a valid signature is refused: an endpoint that accepts
	// unsigned payloads once accepts them always.
	Secret string

	// SignatureHeader carries the digest, "X-Reqwire-Signature". Required when
	// Secret is set.
	SignatureHeader string

	// From is the sender name the agents see. Defaults to "webhook".
	From string

	// Rules are tried in order; every match fires.
	Rules []Rule

	// Unmatched fires only when no rule matched.
	Unmatched *Rule

	// MaxBody caps the request body in bytes.
	MaxBody int64

	// Send delivers the messages. Required.
	Send SendFunc

	// Log records what arrived and what it produced. Optional.
	Log *event.Log

	// Trace records every delivery in full to a file. Optional; a nil Log
	// discards, so no guard is needed at the call sites.
	Trace *Log
}

// Server is the inbound webhook listener.
//
// It is deliberately not part of the web remote control: that one is guarded by
// a token which travels in URLs, and it can type into every terminal. A webhook
// endpoint has to be reachable by whatever sends the events, and those two
// exposures have no business sharing a socket.
type Server struct {
	opts Options
	srv  *http.Server
	ln   net.Listener
}

// New prepares the server without listening yet.
func New(o Options) (*Server, error) {
	if o.Send == nil {
		return nil, errors.New("hook: a send function is required")
	}
	if o.From == "" {
		o.From = "webhook"
	}
	if o.MaxBody <= 0 {
		o.MaxBody = 1 << 20
	}
	if o.Secret != "" && o.SignatureHeader == "" {
		return nil, errors.New("hook: a signature header is required with a secret")
	}
	s := &Server{opts: o}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return s, nil
}

// Start begins listening. It returns once the socket is bound, so the caller can
// print the URL immediately.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("hook: %w", err)
	}
	s.ln = ln
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.emit(event.KindError, "hook listener stopped: "+err.Error())
		}
	}()
	return nil
}

// URL is the address to point a webhook at.
func (s *Server) URL() string {
	if s.ln == nil {
		return ""
	}
	return "http://" + s.ln.Addr().String() + "/"
}

// Close stops the listener.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Close()
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "post a JSON payload", http.StatusMethodNotAllowed)
		return
	}

	tr := &Trace{At: time.Now(), From: r.RemoteAddr, Header: r.Header.Clone()}

	// The body has to be read before anything can be decided about it: the
	// signature covers the raw bytes, so it cannot be checked any earlier.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opts.MaxBody))
	if err != nil {
		tr.Outcome, tr.Code = "refused: body too large", http.StatusRequestEntityTooLarge
		tr.Errors = append(tr.Errors, fmt.Sprintf("limit is %d bytes", s.opts.MaxBody))
		s.opts.Trace.Write(tr)
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	tr.Body = raw

	if why, ok := s.authorized(r, raw); !ok {
		tr.Outcome, tr.Code = "refused: "+why, http.StatusForbidden
		s.opts.Trace.Write(tr)
		s.emit(event.KindError, "hook: refused a delivery: "+why)
		http.Error(w, why, http.StatusForbidden)
		return
	}

	payload, err := NewPayload(r.Header, raw)
	if err != nil {
		tr.Outcome, tr.Code = "refused: invalid JSON", http.StatusBadRequest
		tr.Errors = append(tr.Errors, err.Error())
		s.opts.Trace.Write(tr)
		s.emit(event.KindError, "hook: bad JSON payload: "+err.Error())
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	deliveries, verdicts := ApplyVerbose(s.opts.Rules, s.opts.Unmatched, payload)
	tr.Verdicts, tr.Deliveries = verdicts, deliveries
	if len(deliveries) == 0 {
		tr.Outcome, tr.Code = "accepted, no rule matched", http.StatusAccepted
		s.opts.Trace.Write(tr)
		s.emit(event.KindInfo, "hook: payload matched no rule")
		writeJSON(w, http.StatusAccepted, map[string]any{"matched": 0})
		return
	}

	tr.Outcome, tr.Code = fmt.Sprintf("accepted, %d delivery(ies)", len(deliveries)), http.StatusAccepted
	s.opts.Trace.Write(tr)

	// Answer before delivering: a sender that does not get its 200 quickly
	// retries, and a retried webhook becomes a duplicate message.
	writeJSON(w, http.StatusAccepted, map[string]any{"matched": len(deliveries), "deliveries": deliveries})
	go s.deliver(deliveries)
}

func (s *Server) deliver(deliveries []Delivery) {
	for _, d := range deliveries {
		if err := s.opts.Send(s.opts.From, d.To, d.Body); err != nil {
			s.emit(event.KindError, fmt.Sprintf("hook %s: %v", d.Rule, err))
			s.opts.Trace.Note("delivery failed: %s → %s: %v", d.Rule, d.To, err)
			continue
		}
		s.opts.Trace.Note("delivered: %s → %s", d.Rule, d.To)
		if len(d.Missing) > 0 {
			s.emit(event.KindInfo, fmt.Sprintf("hook %s: no value for %s", d.Rule, strings.Join(d.Missing, ", ")))
		}
	}
}

// authorized checks the token and the signature, each if configured, and says
// which one failed. The reason is logged and returned: a webhook that is being
// rejected shows up in the sender's delivery log as a bare 403, and "bad
// signature" versus "bad token" is the whole difference between a wrong secret
// and a wrong header name.
func (s *Server) authorized(r *http.Request, raw []byte) (string, bool) {
	if s.opts.Token != "" && !s.tokenOK(r) {
		return "bad token", false
	}
	if s.opts.Secret != "" {
		presented := r.Header.Get(s.opts.SignatureHeader)
		if presented == "" {
			return "missing " + s.opts.SignatureHeader, false
		}
		if !VerifySignature(s.opts.Secret, raw, presented) {
			return "bad signature", false
		}
	}
	return "", true
}

// tokenOK checks the shared token. The header forms come first because a query
// string ends up in every proxy log on the way.
func (s *Server) tokenOK(r *http.Request) bool {
	got := r.Header.Get("X-Swarm-Token")
	if got == "" {
		if b, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			got = b
		}
	}
	if got == "" {
		got = r.URL.Query().Get("t")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.opts.Token)) == 1
}

func (s *Server) emit(kind event.Kind, text string) {
	if s.opts.Log != nil {
		s.opts.Log.Emit(kind, "", text)
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
