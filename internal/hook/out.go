package hook

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Outgoing is the other direction: a fleet that can be told about the world
// should be able to tell the world about itself. The rules are the ones that
// come in, read backwards — conditions on paths into an event, a body rendered
// from the same paths, a signed POST.
//
// Nothing here knows what the far end is. Telegram, Slack, a CI job and a shell
// script behind a reverse proxy are the same thing to swarm: a URL that gets a
// signed document and answers 2xx.

// OutRule matches a fleet event and says what to send.
type OutRule struct {
	// Name labels the rule in the log.
	Name string `yaml:"name"`
	// When are conditions on paths into the event, exactly as for an incoming
	// rule: "event", "agent", "state", "git.branch". A value matches exactly,
	// "*" means merely present, and a leading ~ makes it a regexp.
	When map[string]string `yaml:"when"`
	// Body is the human-readable line, rendered from the same paths. It is sent
	// alongside the event rather than instead of it, so the far end can use
	// either.
	Body string `yaml:"body"`

	res   map[string]*regexp.Regexp
	paths []string
}

// Compile prepares the rule. A body is required: a notification nobody can read
// is a POST for nothing.
func (r *OutRule) Compile() error {
	if r.Body == "" {
		return fmt.Errorf("body is required")
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

// asRule borrows the incoming engine, which already knows how to explain a
// mismatch and render placeholders. Writing that twice is how the two
// directions drift apart.
func (r *OutRule) asRule() *Rule {
	return &Rule{
		Name: r.Name, When: r.When, Message: r.Body,
		res: r.res, paths: r.paths,
	}
}

// Explain reports whether the rule matches, and the first condition that failed
// when it does not.
func (r *OutRule) Explain(p Payload) (bool, string) { return r.asRule().Explain(p) }

// Render fills the body from the event.
func (r *OutRule) Render(p Payload) (string, []string) { return r.asRule().Render(p) }

// Notice is one thing worth telling the world about: the event, flattened so a
// rule can address it by path, and the body a rule rendered from it.
type Notice struct {
	Event string            `json:"event"`
	Agent string            `json:"agent,omitempty"`
	Text  string            `json:"text,omitempty"`
	Data  map[string]string `json:"data,omitempty"`
	At    time.Time         `json:"at"`

	// Rule and Body are filled in by the sender, from whichever rule matched.
	Rule string `json:"rule,omitempty"`
	Body string `json:"body,omitempty"`
}

// payload turns a notice into what a rule can address: "event", "agent",
// "text", and everything under "data.".
func (n Notice) payload() Payload {
	body := map[string]any{
		"event": n.Event,
		"agent": n.Agent,
		"text":  n.Text,
		"at":    n.At.Format(time.RFC3339),
	}
	if len(n.Data) > 0 {
		data := make(map[string]any, len(n.Data))
		for k, v := range n.Data {
			data[k] = v
		}
		body["data"] = data
	}
	return Payload{Body: body}
}
