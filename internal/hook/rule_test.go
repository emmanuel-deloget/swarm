package hook

import (
	"net/http"
	"strings"
	"testing"
)

// payload is a representative delivery body. The event type travels in a
// header, as it does for most senders.
const payload = `{
  "event": "pull_request",
  "action": "opened",
  "number": 42,
  "draft": false,
  "repository": {"full_name": "eho/chimera"},
  "pull_request": {
    "title": "fix the parser",
    "html_url": "https://example.invalid/pr/42",
    "head": {"ref": "fix/parser"}
  },
  "reviewers": ["r1", "r2"]
}`

// decode builds a delivery from a body, with optional "Name: value" headers.
func decode(t *testing.T, raw string, headers ...string) Payload {
	t.Helper()
	h := http.Header{}
	for _, kv := range headers {
		name, value, _ := strings.Cut(kv, ":")
		h.Set(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	p, err := NewPayload(h, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func compile(t *testing.T, r Rule) Rule {
	t.Helper()
	if err := r.Compile(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMatchOnPayloadFields(t *testing.T) {
	doc := decode(t, payload)

	cases := []struct {
		name string
		when map[string]string
		want bool
	}{
		{"the user's example", map[string]string{"event": "pull_request", "action": "opened"}, true},
		{"every condition must hold", map[string]string{"event": "pull_request", "action": "closed"}, false},
		{"a missing path never matches", map[string]string{"issue": "*"}, false},
		{"a star only asks for presence", map[string]string{"pull_request.title": "*"}, true},
		{"numbers compare as written", map[string]string{"number": "42"}, true},
		{"booleans compare as text", map[string]string{"draft": "false"}, true},
		{"nested paths", map[string]string{"repository.full_name": "eho/chimera"}, true},
		{"array indices", map[string]string{"reviewers.1": "r2"}, true},
		{"a tilde is a regexp", map[string]string{"pull_request.head.ref": "~^fix/"}, true},
		{"a regexp that does not match", map[string]string{"pull_request.head.ref": "~^feat/"}, false},
		{"no condition matches anything", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := compile(t, Rule{To: "@review", Message: "x", When: c.when})
			if got := r.Match(doc); got != c.want {
				t.Errorf("Match() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRenderFillsPlaceholders(t *testing.T) {
	r := compile(t, Rule{
		To:      "@review",
		Message: "PR #{number} sur {repository.full_name} : {pull_request.html_url}",
	})
	body, missing := r.Render(decode(t, payload))
	want := "PR #42 sur eho/chimera : https://example.invalid/pr/42"
	if body != want {
		t.Errorf("Render() = %q, want %q", body, want)
	}
	if len(missing) != 0 {
		t.Errorf("nothing should be missing, got %v", missing)
	}
}

// TestRenderReportsMissingPaths covers the usual disappointment: the rule fired
// but the message came out with a hole in it. Naming the path is what turns
// that into a two-second fix.
func TestRenderReportsMissingPaths(t *testing.T) {
	r := compile(t, Rule{To: "@review", Message: "{pull_request.author.login} opened it"})
	body, missing := r.Render(decode(t, payload))
	if len(missing) != 1 || missing[0] != "pull_request.author.login" {
		t.Errorf("missing = %v, want the unresolved path", missing)
	}
	if strings.Contains(body, "{") {
		t.Errorf("an unresolved placeholder should not survive into the message: %q", body)
	}
}

// TestRenderTruncatesLongValues: a webhook body is written by whoever opened the
// pull request, and the message ends up in a terminal.
func TestRenderTruncatesLongValues(t *testing.T) {
	doc := decode(t, `{"body": "`+strings.Repeat("a", 5000)+`"}`)
	r := compile(t, Rule{To: "@review", Message: "{body}"})
	body, _ := r.Render(doc)
	if len(body) > maxValueLen+8 {
		t.Errorf("a %d byte value should have been cut, got %d bytes", 5000, len(body))
	}
	if !strings.HasSuffix(body, "…") {
		t.Errorf("a truncated value should say so, got %q", body[max(0, len(body)-16):])
	}
}

func TestApplyFiresEveryMatchingRule(t *testing.T) {
	rules := []Rule{
		compile(t, Rule{Name: "review", When: map[string]string{"action": "opened"}, To: "@review", Message: "a"}),
		compile(t, Rule{Name: "log", When: map[string]string{"event": "pull_request"}, To: "@review", Message: "b"}),
		compile(t, Rule{Name: "never", When: map[string]string{"action": "closed"}, To: "@review", Message: "c"}),
	}
	got := Apply(rules, nil, decode(t, payload))
	if len(got) != 2 {
		t.Fatalf("Apply() returned %d deliveries, want 2: %+v", len(got), got)
	}
	if got[0].Rule != "review" || got[1].Rule != "log" {
		t.Errorf("rules should fire in order, got %q then %q", got[0].Rule, got[1].Rule)
	}
}

// TestUnmatchedIsTheTriageFallback: the point of routing unanticipated events at
// an agent is that it happens only when the rules had nothing to say.
func TestUnmatchedIsTheTriageFallback(t *testing.T) {
	fallback := compile(t, Rule{Name: "triage", To: "@review", Message: "unknown event: {event}"})
	rules := []Rule{compile(t, Rule{Name: "pr", When: map[string]string{"action": "opened"}, To: "@review", Message: "a"})}

	got := Apply(rules, &fallback, decode(t, payload))
	if len(got) != 1 || got[0].Rule != "pr" {
		t.Fatalf("the fallback should stay quiet when a rule matched, got %+v", got)
	}

	got = Apply(rules, &fallback, decode(t, `{"event": "deployment"}`))
	if len(got) != 1 || got[0].Rule != "triage" {
		t.Fatalf("the fallback should catch what no rule matched, got %+v", got)
	}
	if got[0].Body != "unknown event: deployment" {
		t.Errorf("body = %q", got[0].Body)
	}
}

// TestMatchOnHeaders: senders routinely put the event type in a header and
// nowhere else, so a rule that could only read the body would be unable to say
// which event it is about.
func TestMatchOnHeaders(t *testing.T) {
	p := decode(t, payload, "X-Reqwire-Event: requirement.created")

	cases := []struct {
		name string
		when map[string]string
		want bool
	}{
		{"a header condition", map[string]string{"header.X-Reqwire-Event": "requirement.created"}, true},
		{"the wrong value", map[string]string{"header.X-Reqwire-Event": "requirement.deleted"}, false},
		{"an absent header", map[string]string{"header.X-Reqwire-Delivery": "*"}, false},
		{"header names are case insensitive", map[string]string{"header.x-reqwire-event": "requirement.created"}, true},
		{"a regexp on a header", map[string]string{"header.X-Reqwire-Event": "~^requirement\\."}, true},
		{"headers and body together", map[string]string{
			"header.X-Reqwire-Event": "requirement.created",
			"action":                 "opened",
		}, true},
		{"the body half failing is enough to fail", map[string]string{
			"header.X-Reqwire-Event": "requirement.created",
			"action":                 "closed",
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := compile(t, Rule{To: "@review", Message: "x", When: c.when})
			if got := r.Match(p); got != c.want {
				t.Errorf("Match() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRenderFromHeaders(t *testing.T) {
	p := decode(t, payload, "X-Reqwire-Event: requirement.created")
	r := compile(t, Rule{To: "@review", Message: "{header.X-Reqwire-Event} sur {repository.full_name}"})
	body, missing := r.Render(p)
	if want := "requirement.created sur eho/chimera"; body != want {
		t.Errorf("Render() = %q, want %q", body, want)
	}
	if len(missing) != 0 {
		t.Errorf("nothing should be missing, got %v", missing)
	}
}

// TestExplainIsStable: the log blames one condition, and it must be the same
// one every time — map iteration order would otherwise make two identical
// payloads produce two different diagnoses.
func TestExplainIsStable(t *testing.T) {
	r := compile(t, Rule{To: "@review", Message: "x", When: map[string]string{
		"aaa": "1", "bbb": "2", "ccc": "3", "ddd": "4", "eee": "5",
	}})
	p := decode(t, `{}`)
	_, first := r.Explain(p)
	for range 20 {
		if _, why := r.Explain(p); why != first {
			t.Fatalf("Explain is not stable: %q then %q", first, why)
		}
	}
}
