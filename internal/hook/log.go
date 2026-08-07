package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// maxLoggedBody is how much of a payload the log keeps. Enough to paste into a
// file and hand to `swarm hook test`, which is the point.
const maxLoggedBody = 4096

// redacted are headers whose value is a credential rather than a fact about the
// delivery. The signature is not among them: it is a digest, and being able to
// compare it with `swarm hook sign` is exactly what settles a rejection.
var redacted = map[string]bool{
	"Authorization": true,
	"X-Swarm-Token": true,
	"Cookie":        true,
}

// Trace is everything one delivery went through, from the socket to the bus.
//
// It exists because a webhook that does nothing looks the same from the outside
// whether it never arrived, was refused, matched no rule, or was sent to an
// agent that ignored it. Those are four different problems with four different
// fixes, and only the listener can tell them apart.
type Trace struct {
	At         time.Time
	From       string
	Header     http.Header
	Body       []byte
	Outcome    string
	Code       int
	Verdicts   []Verdict
	Deliveries []Delivery
	Errors     []string
}

// Log appends traces to a file. Deliveries arrive concurrently, so each trace
// is formatted whole and written under one lock: interleaved half-blocks would
// be worse than no log at all.
type Log struct {
	mu sync.Mutex
	f  *os.File
	n  int
}

// OpenLog opens the webhook log, creating it if needed. It is 0600: a payload
// carries whatever the sender put in it.
func OpenLog(path string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Log{f: f}, nil
}

// Close closes the file.
func (l *Log) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// Write appends one delivery. A nil Log discards, so callers need no guard.
func (l *Log) Write(tr *Trace) {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	_, _ = l.f.WriteString(tr.format(l.n))
}

// Note appends a single line, for what happens after the request has been
// answered: the block above records what was decided, a note records how it
// actually went.
func (l *Log) Note(format string, args ...any) {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = fmt.Fprintf(l.f, "--- %s  %s\n\n", time.Now().Format(time.RFC3339Nano), fmt.Sprintf(format, args...))
}

func (tr *Trace) format(n int) string {
	var b strings.Builder
	at := tr.At
	if at.IsZero() {
		at = time.Now()
	}
	fmt.Fprintf(&b, "=== %s  delivery #%d  %s ===\n", at.Format(time.RFC3339Nano), n, tr.Outcome)
	if tr.From != "" {
		fmt.Fprintf(&b, "  from     %s\n", tr.From)
	}

	names := make([]string, 0, len(tr.Header))
	for name := range tr.Header {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := tr.Header.Get(name)
		if redacted[name] {
			value = "(redacted)"
		}
		fmt.Fprintf(&b, "  header   %s: %s\n", name, value)
	}

	if len(tr.Body) > 0 {
		fmt.Fprintf(&b, "  body     %d bytes\n", len(tr.Body))
		fmt.Fprintf(&b, "  %s\n", compactBody(tr.Body))
	}

	for _, v := range tr.Verdicts {
		if v.Matched {
			fmt.Fprintf(&b, "  rule     %-28s MATCH  %s\n", v.Rule, v.Why)
		} else {
			fmt.Fprintf(&b, "  rule     %-28s no     %s\n", v.Rule, v.Why)
		}
	}

	for _, d := range tr.Deliveries {
		fmt.Fprintf(&b, "  send     %s → %s: %s\n", d.Rule, d.To, oneLine(d.Body))
		if len(d.Missing) > 0 {
			fmt.Fprintf(&b, "           ! no value for: %s\n", strings.Join(d.Missing, ", "))
		}
	}
	for _, e := range tr.Errors {
		fmt.Fprintf(&b, "  error    %s\n", e)
	}
	if tr.Code != 0 {
		fmt.Fprintf(&b, "  answered %d\n", tr.Code)
	}
	b.WriteString("\n")
	return b.String()
}

// compactBody puts the payload on one line so it can be copied straight into a
// file for `swarm hook test`.
func compactBody(raw []byte) string {
	out := raw
	var buf bytes.Buffer
	if json.Compact(&buf, raw) == nil {
		out = buf.Bytes()
	}
	if len(out) > maxLoggedBody {
		return string(out[:maxLoggedBody]) + fmt.Sprintf("… (%d bytes truncated)", len(out)-maxLoggedBody)
	}
	return oneLine(string(out))
}

func oneLine[T ~string | ~[]byte](v T) string {
	s := strings.TrimSpace(string(v))
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	if len(s) > maxLoggedBody {
		s = s[:maxLoggedBody] + "…"
	}
	return s
}
