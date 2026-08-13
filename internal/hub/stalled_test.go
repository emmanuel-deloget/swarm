package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
)

// on_stalled is the first thing swarm does about a state rather than merely
// showing it, so the tests are mostly about restraint: it asks, it asks a
// bounded number of times, and it does not make the situation worse by asking.

const stallFleet = `
web: {enabled: false}
bus: {stalled_after: 200ms}
defaults: {idle_after: 100ms}
agents:
  - name: alpha
    command: [probe-echo]
  - name: myself
    command: [probe-echo]
`

// withRules puts rules on a loaded fleet directly. The configuration refuses an
// `every` under a minute — rightly, a reminder every few seconds is not a
// reminder — and a test that waited that out would be a minute long.
func withRules(t *testing.T, h *Hub, rules ...config.StalledRule) {
	t.Helper()
	for i := range rules {
		if rules[i].To == "" {
			rules[i].To = config.StalledSelf
		}
		if rules[i].Kind == "" {
			rules[i].Kind = "fyi"
		}
		if rules[i].Max == 0 {
			rules[i].Max = 1
		}
	}
	h.cfg.Bus.OnStalled = rules
	h.stalled = &stalledActor{seen: map[stalledKey]stalledSeen{}}
}

// owedNow is the debt alpha is carrying, as the watcher would hand it over.
func owedNow(t *testing.T, h *Hub, name string) debtView {
	t.Helper()
	d, ok := h.bus.OwedSince(name)
	if !ok {
		t.Fatalf("%s owes nothing", name)
	}
	return debtView{Thread: d.Thread, From: d.From, Kind: d.Kind, Since: d.Since, Body: d.Body}
}

// TestAskingAStalledAgentDoesNotGiveItASecondDebt is the one that matters most.
// A question sent to an agent that is stuck opens another debt on top of the
// one it is stuck on; answering the new one settles neither, so the agent stays
// stalled and the same rule fires again. The configuration refuses that shape,
// and this is the behaviour behind the refusal.
func TestAskingAStalledAgentDoesNotGiveItASecondDebt(t *testing.T) {
	h := fleet(t, stallFleet)
	withRules(t, h, config.StalledRule{To: config.StalledSelf, Kind: "fyi"})

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "where is the fix?", nil); err != nil {
		t.Fatal(err)
	}
	before := len(h.bus.Owed("alpha"))

	h.act("alpha", owedNow(t, h, "alpha"))

	if after := len(h.bus.Owed("alpha")); after != before {
		t.Errorf("asking an agent where it is added a debt: %d before, %d after", before, after)
	}
}

// TestTheAgentIsToldWhatItOwesAndHowToEndIt: the message is the point. An agent
// stalled long enough to be asked has been compacted, so "what are you doing?"
// gets an honest shrug; what it needs is what the bus still knows.
func TestTheAgentIsToldWhatItOwesAndHowToEndIt(t *testing.T) {
	h := fleet(t, stallFleet)
	withRules(t, h, config.StalledRule{To: config.StalledSelf, Kind: "fyi"})

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "sessions or tokens?", nil); err != nil {
		t.Fatal(err)
	}
	h.act("alpha", owedNow(t, h, "alpha"))

	var got string
	for _, m := range h.bus.History("alpha", -1) {
		if m.From == stallSender {
			got = m.Body
		}
	}
	if got == "" {
		t.Fatal("nothing was sent to the stalled agent")
	}
	for _, want := range []string{
		"myself",              // who is waiting
		"sessions or tokens?", // what they asked
		"swarm done -thread",  // the way out
		"swarm send -kind fyi",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the message never mentions %q:\n%s", want, got)
		}
	}
}

// TestSomebodyElseCanBeToldInstead: the more useful half. A triage agent knows
// what the work was, so it can open a real question the stalled agent has to
// settle — which is exactly what swarm must not do by itself.
func TestSomebodyElseCanBeToldInstead(t *testing.T) {
	h := fleet(t, stallFleet)
	withRules(t, h, config.StalledRule{To: "myself", Kind: "fyi"})

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	h.act("alpha", owedNow(t, h, "alpha"))

	var told bool
	for _, m := range h.bus.History("myself", -1) {
		if m.From == stallSender && strings.Contains(m.Body, "alpha") {
			told = true
			if !strings.Contains(m.Body, "swarm why alpha") {
				t.Errorf("the triage agent is told without being told how to look:\n%s", m.Body)
			}
		}
	}
	if !told {
		t.Error("the other agent heard nothing")
	}
}

// TestARuleFiresOnceForOneDebt: without this, a stalled agent is asked the same
// question every time the watcher ticks.
func TestARuleFiresOnceForOneDebt(t *testing.T) {
	h := fleet(t, stallFleet)
	withRules(t, h, config.StalledRule{To: config.StalledSelf, Kind: "fyi"})

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	d := owedNow(t, h, "alpha")
	for range 5 {
		h.act("alpha", d)
	}

	if n := countFrom(h, "alpha", stallSender); n != 1 {
		t.Errorf("five ticks sent %d messages; a debt is asked about once", n)
	}
}

// TestRepeatsAreBoundedAndThenStop: an agent stuck over a weekend is worth a
// few reminders and no more.
func TestRepeatsAreBoundedAndThenStop(t *testing.T) {
	h := fleet(t, stallFleet)
	withRules(t, h, config.StalledRule{
		To: config.StalledSelf, Kind: "fyi", Every: time.Millisecond, Max: 3,
	})

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	d := owedNow(t, h, "alpha")
	for range 10 {
		time.Sleep(2 * time.Millisecond)
		h.act("alpha", d)
	}

	if n := countFrom(h, "alpha", stallSender); n != 3 {
		t.Errorf("a max of 3 sent %d messages", n)
	}
	// And it says it has given up, rather than going quiet in a way that looks
	// like it worked.
	var said bool
	for _, e := range h.Log().History(-1) {
		if strings.Contains(e.Text, "not asking again") {
			said = true
		}
	}
	if !said {
		t.Error("the rule stopped without saying so")
	}
}

// TestSettlingResetsTheCount: the next question deserves the same patience as
// the first.
func TestSettlingResetsTheCount(t *testing.T) {
	h := fleet(t, stallFleet)
	withRules(t, h, config.StalledRule{To: config.StalledSelf, Kind: "fyi"})

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "first", nil); err != nil {
		t.Fatal(err)
	}
	h.act("alpha", owedNow(t, h, "alpha"))
	if _, _, err := h.Done("alpha", 0, "done"); err != nil {
		t.Fatal(err)
	}
	h.forget("alpha") // what the watcher does when an agent stops owing

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "second", nil); err != nil {
		t.Fatal(err)
	}
	h.act("alpha", owedNow(t, h, "alpha"))

	if n := countFrom(h, "alpha", stallSender); n != 2 {
		t.Errorf("after settling, the new debt got %d of the 2 expected asks", n)
	}
}

// TestAfterHoldsARuleBack lets one list ask the agent now and tell somebody
// else only if it is still stuck later.
func TestAfterHoldsARuleBack(t *testing.T) {
	h := fleet(t, stallFleet)
	withRules(t,
		h,
		config.StalledRule{To: config.StalledSelf, Kind: "fyi"},
		config.StalledRule{To: "myself", Kind: "fyi", After: time.Hour},
	)

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	h.act("alpha", owedNow(t, h, "alpha"))

	if n := countFrom(h, "alpha", stallSender); n != 1 {
		t.Errorf("the immediate rule sent %d", n)
	}
	if n := countFrom(h, "myself", stallSender); n != 0 {
		t.Errorf("a rule with after: 1h fired straight away (%d)", n)
	}
}

// TestNoRulesMeansNothingIsSent: the behaviour swarm had before any of this,
// and the one it keeps for anybody who does not ask for more.
func TestNoRulesMeansNothingIsSent(t *testing.T) {
	h := fleet(t, stallFleet)

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	h.act("alpha", owedNow(t, h, "alpha"))

	if n := countFrom(h, "alpha", stallSender); n != 0 {
		t.Errorf("a fleet with no on_stalled rules sent %d messages", n)
	}
}

// TestTheAskIsPushedPastAPullMailbox: an agent that is not reading its mailbox
// is the whole reason this exists, so a message waiting politely in a queue is
// a message that never arrives.
func TestTheAskIsPushedPastAPullMailbox(t *testing.T) {
	pullFleet := `
web: {enabled: false}
bus: {stalled_after: 200ms}
defaults: {idle_after: 100ms}
agents:
  - name: alpha
    delivery: pull
    command: [probe-echo]
  - name: myself
    command: [probe-echo]
`
	h := fleet(t, pullFleet)
	withRules(t, h, config.StalledRule{To: config.StalledSelf, Kind: "fyi"})

	// Running, because pushing into a terminal needs one — and the point here
	// is precisely that the message reaches the terminal.
	a, _ := h.Agent("alpha")
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}

	if _, err := h.SendKind("myself", "alpha", bus.KindQuestion, "well?", nil); err != nil {
		t.Fatal(err)
	}
	h.act("alpha", owedNow(t, h, "alpha"))

	var pushed bool
	for _, m := range h.bus.History("alpha", -1) {
		if m.From == stallSender {
			pushed = m.Pushed
		}
	}
	if !pushed {
		t.Error("the ask was left in a pull mailbox, which is the one place it cannot work")
	}
}

func countFrom(h *Hub, agent, from string) int {
	n := 0
	for _, m := range h.bus.History(agent, -1) {
		if m.From == from {
			n++
		}
	}
	return n
}

// TestSwarmDoesNotChaseItsOwnNotices. Telling a triage agent with a `question`
// is deliberate — it owes an answer, so it will deal with it — but that debt
// makes the triage agent stalled in turn, and the rules would then ask it where
// it is on a message swarm sent it. Seen happening on a real fleet within a
// minute of the feature working.
func TestSwarmDoesNotChaseItsOwnNotices(t *testing.T) {
	h := fleet(t, stallFleet)
	withRules(t, h, config.StalledRule{To: config.StalledSelf, Kind: "fyi"})

	// A debt opened by swarm itself, as the escalation rule would.
	if _, err := h.SendOn(stallSender, "myself", bus.KindQuestion,
		"alpha has been quiet", nil, SendOptions{NewThread: true}); err != nil {
		t.Fatal(err)
	}
	h.act("myself", owedNow(t, h, "myself"))

	if n := countFrom(h, "myself", stallSender); n != 1 {
		t.Errorf("swarm sent %d messages: it asked about its own notice", n)
	}
}
