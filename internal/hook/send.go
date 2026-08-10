package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Sender posts notices to one endpoint, in the background.
//
// Nothing here knows what the far end is: Telegram, a CI job, a shell script
// behind a reverse proxy — all the same, a URL that receives a signed document
// and answers 2xx. Which is also why the retry is deliberately modest. swarm
// tells the world what happened; making sure the world listened is the world's
// job, and a queue that survives restarts would be a message broker, which this
// is not.
type Sender struct {
	opts  OutOptions
	queue chan Notice
	wg    sync.WaitGroup
	stop  chan struct{}
	once  sync.Once
}

// OutOptions configures the outgoing side.
type OutOptions struct {
	// URL receives the POST. Required.
	URL string
	// Rules are tried against every notice; every match sends.
	Rules []OutRule
	// Secret signs the body, under SignatureHeader, exactly as an incoming
	// delivery is checked. Empty means unsigned, which is only reasonable
	// towards localhost.
	Secret          string
	SignatureHeader string
	// Token, when set, goes out as X-Swarm-Token.
	Token string

	// Timeout bounds one attempt, Retries how many further attempts a failure
	// earns, and RetryBackoff the first wait between them — it doubles.
	Timeout      time.Duration
	Retries      int
	RetryBackoff time.Duration

	// Queue bounds what is held while the far end is slow. Past it, notices are
	// dropped and said to be dropped: a fleet must not stall because a webhook
	// endpoint is down.
	Queue int

	// Trace records every attempt. Optional.
	Trace *Log
	// Emit reports to the event log. Optional.
	Emit func(text string)

	// Client is used for the POST; nil means a client with Timeout.
	Client *http.Client
}

// NewSender prepares a sender and starts its worker.
func NewSender(o OutOptions) (*Sender, error) {
	if o.URL == "" {
		return nil, fmt.Errorf("outgoing: url is required")
	}
	if o.Secret != "" && o.SignatureHeader == "" {
		return nil, fmt.Errorf("outgoing: signature_header is required when a secret is set")
	}
	for i := range o.Rules {
		if err := o.Rules[i].Compile(); err != nil {
			return nil, fmt.Errorf("outgoing rule %s: %w", o.Rules[i].Label(i), err)
		}
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.RetryBackoff <= 0 {
		o.RetryBackoff = 2 * time.Second
	}
	if o.Queue <= 0 {
		o.Queue = 256
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.Timeout}
	}

	s := &Sender{opts: o, queue: make(chan Notice, o.Queue), stop: make(chan struct{})}
	s.wg.Add(1)
	go s.run()
	return s, nil
}

// Label names a rule for a message, falling back to its position.
func (r *OutRule) Label(at int) string {
	if r.Name != "" {
		return r.Name
	}
	return fmt.Sprintf("#%d", at+1)
}

// Notify offers a notice to whichever rules match. It never blocks: a fleet
// must not wait on an endpoint.
func (s *Sender) Notify(n Notice) {
	if s == nil {
		return
	}
	p := n.payload()
	for i := range s.opts.Rules {
		r := &s.opts.Rules[i]
		ok, _ := r.Explain(p)
		if !ok {
			continue
		}
		body, _ := r.Render(p)
		out := n
		out.Rule = r.Label(i)
		out.Body = body
		select {
		case s.queue <- out:
		default:
			s.opts.Trace.Note("outgoing: queue full, dropped %s (%s)", out.Rule, out.Event)
			if s.opts.Emit != nil {
				s.opts.Emit("outgoing: queue full, dropped " + out.Rule)
			}
		}
	}
}

// Close drains what is queued and stops the worker.
func (s *Sender) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.stop)
		s.wg.Wait()
	})
}

func (s *Sender) run() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			// Drain what is already queued, then go. A notice earned its POST.
			for {
				select {
				case n := <-s.queue:
					s.post(n)
				default:
					return
				}
			}
		case n := <-s.queue:
			s.post(n)
		}
	}
}

// post sends one notice, retrying a failure with a doubling wait.
func (s *Sender) post(n Notice) {
	raw, err := json.Marshal(n)
	if err != nil {
		s.opts.Trace.Note("outgoing %s: %v", n.Rule, err)
		return
	}
	wait := s.opts.RetryBackoff
	for attempt := 0; ; attempt++ {
		code, err := s.attempt(raw)
		switch {
		case err == nil && code/100 == 2:
			s.opts.Trace.Note("outgoing %s → %s: answered %d", n.Rule, s.opts.URL, code)
			return
		case attempt >= s.opts.Retries:
			s.note(n, code, err, "giving up")
			if s.opts.Emit != nil {
				s.opts.Emit(fmt.Sprintf("outgoing %s failed: %s", n.Rule, outcome(code, err)))
			}
			return
		default:
			s.note(n, code, err, "trying again in "+wait.String())
		}
		select {
		case <-time.After(wait):
		case <-s.stop:
			// Shutting down: one last try rather than a silent loss.
			if code, err := s.attempt(raw); err == nil && code/100 == 2 {
				s.opts.Trace.Note("outgoing %s → %s: answered %d", n.Rule, s.opts.URL, code)
			}
			return
		}
		wait *= 2
	}
}

func (s *Sender) note(n Notice, code int, err error, what string) {
	s.opts.Trace.Note("outgoing %s → %s: %s — %s", n.Rule, s.opts.URL, outcome(code, err), what)
}

func outcome(code int, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("answered %d", code)
}

func (s *Sender) attempt(raw []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.opts.URL, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "swarm")
	if s.opts.Token != "" {
		req.Header.Set("X-Swarm-Token", s.opts.Token)
	}
	if s.opts.Secret != "" {
		req.Header.Set(s.opts.SignatureHeader, "sha256="+Signature(s.opts.Secret, raw))
	}

	resp, err := s.opts.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, nil
}
