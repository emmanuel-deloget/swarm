// Package hook turns incoming webhooks into bus messages.
//
// It knows nothing about GitHub, GitLab or any other sender: a rule matches
// paths in the decoded JSON body and renders a message from the same paths.
// That keeps swarm as agnostic about event sources as it is about agents.
package hook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// maxValueLen caps what a single placeholder may expand to. A webhook body is
// written by whoever opened the pull request, and it ends up in an agent's
// prompt: an issue body runs to kilobytes, and there is no reason to paste all
// of it into a terminal.
const maxValueLen = 400

// headerPrefix addresses a header rather than the body, in a condition or in a
// message: "header.X-Reqwire-Event". Senders routinely put the event type
// there and nowhere else, so a rule that could only see the body would be
// unable to say which event it is about.
const headerPrefix = "header."

// Payload is one delivery: the headers that came with it and the decoded body.
type Payload struct {
	// Header holds the request headers, under their canonical names.
	Header map[string]string
	// Body is the decoded JSON document.
	Body any
}

// NewPayload decodes a delivery. Numbers are kept as written: a pull request is
// number 42, not 4.2e+01.
func NewPayload(header http.Header, raw []byte) (Payload, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var body any
	if err := dec.Decode(&body); err != nil {
		return Payload{}, err
	}
	p := Payload{Body: body, Header: make(map[string]string, len(header))}
	for name, values := range header {
		if len(values) > 0 {
			p.Header[http.CanonicalHeaderKey(name)] = values[0]
		}
	}
	return p, nil
}

// Rule matches a payload and says what to send, and to whom.
type Rule struct {
	// Name labels the rule in the event log and in `swarm hook test`.
	Name string `yaml:"name"`

	// When holds the conditions, all of which must hold: a path against an
	// expected value. A path addresses the body by default, or a header when it
	// starts with "header." — which is where the event type usually lives:
	//
	//	header.X-Reqwire-Event: requirement.created
	//	action: opened
	//
	// Three forms of value are understood:
	//
	//	action: opened     the value must be exactly that
	//	number: "*"        the path must merely exist
	//	ref: "~^refs/tags" the value must match the regexp after the tilde
	When map[string]string `yaml:"when"`

	// To is the bus target: an agent, "@group", "@role", "all". It is
	// deliberately not templated — a payload must never choose which agent it
	// wakes up.
	To string `yaml:"to"`

	// Message is the body, with {dotted.path} placeholders filled from the
	// payload.
	Message string `yaml:"message"`

	// res holds the compiled regexps of When, by path.
	res map[string]*regexp.Regexp

	// paths is When's keys, sorted. Conditions are evaluated in a fixed order
	// so that "the first one that failed" means the same thing twice running —
	// a log that blames a different condition on every delivery is useless.
	paths []string
}

// Label names the rule for a human, in the delivery log and in
// `swarm hook test`. An unnamed rule is identified by its position, the same
// way the config reports one, rather than by its target: "rule #3 → triage-1"
// says which line to go and look at, "triage-1 → triage-1" says nothing.
//
// at is the rule's index; -1 marks the unmatched fallback.
func (r *Rule) Label(at int) string {
	if r.Name != "" {
		return r.Name
	}
	if at < 0 {
		return "unmatched"
	}
	return fmt.Sprintf("rule #%d", at+1)
}

// Compile validates the rule and prepares its regexps.
func (r *Rule) Compile() error {
	if r.To == "" {
		return fmt.Errorf("to is required")
	}
	if r.Message == "" {
		return fmt.Errorf("message is required")
	}
	r.res = nil
	r.paths = make([]string, 0, len(r.When))
	for path, want := range r.When {
		r.paths = append(r.paths, path)
		if !strings.HasPrefix(want, "~") {
			continue
		}
		re, err := regexp.Compile(want[1:])
		if err != nil {
			return fmt.Errorf("condition %q: %w", path, err)
		}
		if r.res == nil {
			r.res = make(map[string]*regexp.Regexp)
		}
		r.res[path] = re
	}
	sort.Strings(r.paths)
	return nil
}

// Match reports whether every condition holds for the payload. A rule with no
// conditions matches anything.
func (r *Rule) Match(p Payload) bool {
	ok, _ := r.Explain(p)
	return ok
}

// Explain reports whether the rule matches and, when it does not, the first
// condition that failed with the value that was actually there. That sentence
// is the whole content of "why did nothing happen?", so it is what the webhook
// log and `swarm hook test -v` both print.
func (r *Rule) Explain(p Payload) (bool, string) {
	for _, path := range r.paths {
		want := r.When[path]
		got, ok := lookup(p, path)
		if !ok {
			return false, fmt.Sprintf("%s is absent, want %s", path, quote(want))
		}
		switch {
		case want == "*":
		case strings.HasPrefix(want, "~"):
			re, ok := r.res[path]
			if !ok || !re.MatchString(got) {
				return false, fmt.Sprintf("%s is %s, does not match %s", path, quote(got), quote(want[1:]))
			}
		default:
			if got != want {
				return false, fmt.Sprintf("%s is %s, want %s", path, quote(got), quote(want))
			}
		}
	}
	return true, ""
}

// quote shortens a value before showing it: a condition may be compared against
// a whole JSON object.
func quote(s string) string {
	if len(s) > 80 {
		s = truncate(s, 80)
	}
	return strconv.Quote(s)
}

// Render fills the message from the payload. It also reports the placeholders
// that found nothing, which is the usual reason a rule looks like it did not
// fire: `swarm hook test` shows them rather than leaving you guessing.
func (r *Rule) Render(p Payload) (body string, missing []string) {
	out := placeholder.ReplaceAllStringFunc(r.Message, func(m string) string {
		path := m[1 : len(m)-1]
		v, ok := lookup(p, path)
		if !ok {
			missing = append(missing, path)
			return ""
		}
		return truncate(v, maxValueLen)
	})
	return strings.TrimSpace(out), missing
}

// placeholder matches {dotted.path} in a message.
var placeholder = regexp.MustCompile(`\{([A-Za-z0-9_][A-Za-z0-9_.\-]*)\}`)

// Delivery is one message a payload produced.
type Delivery struct {
	Rule    string   `json:"rule"`
	To      string   `json:"to"`
	Body    string   `json:"body"`
	Missing []string `json:"missing,omitempty"`
}

// Verdict is what one rule decided about one payload, including why it stayed
// out of the way.
type Verdict struct {
	Rule    string `json:"rule"`
	Matched bool   `json:"matched"`
	// Why names the first condition that failed, empty when Matched.
	Why string `json:"why,omitempty"`
}

// Apply matches a payload against the rules and returns what should be sent.
// Every matching rule fires; unmatched is used only when none did, which is how
// a triage agent picks up whatever the rules did not anticipate.
//
// The server and `swarm hook test` both go through here, so what the simulation
// prints is what the listener would do.
func Apply(rules []Rule, unmatched *Rule, p Payload) []Delivery {
	deliveries, _ := ApplyVerbose(rules, unmatched, p)
	return deliveries
}

// ApplyVerbose is Apply plus a verdict per rule, for the webhook log.
func ApplyVerbose(rules []Rule, unmatched *Rule, p Payload) ([]Delivery, []Verdict) {
	var out []Delivery
	verdicts := make([]Verdict, 0, len(rules))
	for i := range rules {
		r := &rules[i]
		ok, why := r.Explain(p)
		verdicts = append(verdicts, Verdict{Rule: r.Label(i), Matched: ok, Why: why})
		if !ok {
			continue
		}
		body, missing := r.Render(p)
		out = append(out, Delivery{Rule: r.Label(i), To: r.To, Body: body, Missing: missing})
	}
	if len(out) == 0 && unmatched != nil {
		body, missing := unmatched.Render(p)
		out = append(out, Delivery{Rule: unmatched.Label(-1), To: unmatched.To, Body: body, Missing: missing})
		verdicts = append(verdicts, Verdict{Rule: unmatched.Label(-1), Matched: true, Why: "no other rule matched"})
	}
	return out, verdicts
}

// lookup resolves a path against a delivery. "header.X-Reqwire-Event" reads a
// header; anything else walks the body along a dotted path:
// "pull_request.head.ref", "commits.0.message". An empty path is the body
// itself.
func lookup(p Payload, path string) (string, bool) {
	if name, ok := strings.CutPrefix(path, headerPrefix); ok {
		v, ok := p.Header[http.CanonicalHeaderKey(name)]
		return v, ok
	}

	cur := p.Body
	if path != "" {
		for _, part := range strings.Split(path, ".") {
			switch v := cur.(type) {
			case map[string]any:
				next, ok := v[part]
				if !ok {
					return "", false
				}
				cur = next
			case []any:
				i, err := strconv.Atoi(part)
				if err != nil || i < 0 || i >= len(v) {
					return "", false
				}
				cur = v[i]
			default:
				return "", false
			}
		}
	}
	return valueString(cur)
}

// valueString renders a JSON value as text. Objects and arrays come back as
// compact JSON, so {pull_request} is usable even if it is rarely wise.
func valueString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	case bool:
		return strconv.FormatBool(t), true
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Do not cut a rune in half.
	cut := n
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xc0 != 0x80 }
