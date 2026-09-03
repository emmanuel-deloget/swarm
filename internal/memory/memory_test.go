package memory

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func store(t *testing.T, entries, chars int) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "memory.json"), entries, chars, 0)
}

// write is Remember for the tests that do not care what was evicted.
func write(t *testing.T, s *Store, key, fact, by string) Entry {
	t.Helper()
	e, _, err := s.Remember(key, fact, by)
	if err != nil {
		t.Fatalf("remember %s: %v", key, err)
	}
	return e
}

// at pins the store's clock, so a memory can be aged without waiting for it.
func at(s *Store, base time.Time) func(time.Duration) {
	s.now = func() time.Time { return base }
	return func(d time.Duration) { s.now = func() time.Time { return base.Add(d) } }
}

// journal reads back what the store recorded, in order.
func journal(t *testing.T, s *Store) []Record {
	t.Helper()
	f, err := os.Open(s.journal)
	if err != nil {
		t.Fatalf("no journal: %v", err)
	}
	defer func() { _ = f.Close() }()
	var out []Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("journal line %q: %v", sc.Text(), err)
		}
		out = append(out, r)
	}
	return out
}

// TestTheShapeIsRefusedRatherThanRequested is the whole point of this package.
// An agent asked politely for something short writes an essay about brevity,
// so every one of these is a refusal at the point of writing.
func TestTheShapeIsRefusedRatherThanRequested(t *testing.T) {
	s := store(t, 10, 60)
	cases := []struct{ what, key, fact, says string }{
		{"an essay", "gate", strings.Repeat("a", 61), "at most 60"},
		{"two lines", "gate", "one\ntwo", "one line"},
		{"a heading", "gate", "# Gates", "a heading"},
		{"a bullet", "gate", "- gates take time", "a bullet"},
		{"bold", "gate", "**gates** take time", "bold"},
		{"a key with spaces", "On The Nature Of Gates", "short", "not a key"},
		{"a key in caps", "GATE", "short", "not a key"},
		{"nothing at all", "gate", "   ", "needs something"},
	}
	for _, c := range cases {
		_, _, err := s.Remember(c.key, c.fact, "dev-1")
		if err == nil {
			t.Errorf("%s was accepted", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: the refusal says %q, want it to mention %q", c.what, err, c.says)
		}
	}
	// And the shape that is wanted goes in.
	if _, _, err := s.Remember("gate-runtime", "make integration takes 8-12 min", "dev-1"); err != nil {
		t.Errorf("a key and one short line was refused: %v", err)
	}
}

// TestWritingAKeyAgainReplacesIt: there is no edit command because this is the
// edit, and a fact that is still true has its date refreshed by being restated.
func TestWritingAKeyAgainReplacesIt(t *testing.T) {
	s := store(t, 10, 200)
	first, _, err := s.Remember("spec-281", "v8 approved", "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.Remember("spec-281", "v9 approved", "myself")
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Errorf("the memory holds %d entries after replacing one", s.Len())
	}
	if got := s.Recall("spec")[0].Fact; got != "v9 approved" {
		t.Errorf("the entry says %q, want what replaced it", got)
	}
	if !second.At.After(first.At) && second.At.Equal(first.At) {
		t.Error("restating a fact did not refresh its date")
	}
	if got := s.Recall("spec")[0].By; got != "myself" {
		t.Errorf("the entry is credited to %q, want whoever wrote it last", got)
	}
}

// TestAFullMemoryEvictsTheLeastRecentlyUsed: refusing was the first answer,
// and it asked an agent to pick somebody else's fact to delete from inside a
// command that was about to fail. What the memory can decide on its own is
// which fact the fleet has gone longest without wanting.
func TestAFullMemoryEvictsTheLeastRecentlyUsed(t *testing.T) {
	s := store(t, 3, 200)
	tick := at(s, time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC))
	for i, k := range []string{"one", "two", "three"} {
		tick(time.Duration(i) * time.Minute)
		write(t, s, k, "a fact", "dev-1")
	}
	// "one" is the oldest, but the fleet still asks for it. "two" is what
	// nobody has wanted since.
	tick(10 * time.Minute)
	if got := s.Recall("one"); len(got) != 1 {
		t.Fatalf("recall one returned %d entries", len(got))
	}

	tick(11 * time.Minute)
	e, evicted, err := s.Remember("four", "a fact", "dev-2")
	if err != nil {
		t.Fatalf("a full memory refused the write: %v", err)
	}
	if evicted == nil {
		t.Fatal("something was dropped and the caller was not told which")
	}
	if evicted.Key != "two" {
		t.Errorf("%s was evicted, want the least recently used", evicted.Key)
	}
	if e.Key != "four" {
		t.Errorf("the write landed on %s", e.Key)
	}
	if s.Len() != 3 {
		t.Errorf("the memory holds %d entries, want the three it was told to", s.Len())
	}
	if len(s.Recall("one")) != 1 {
		t.Error("the entry the fleet keeps asking for is the one that went")
	}
	// And the eviction is on the record, line and all.
	var evictions int
	for _, r := range journal(t, s) {
		if r.Act == ActEvicted {
			evictions++
			if r.Key != "two" || r.Fact == "" {
				t.Errorf("the journal recorded %+v", r)
			}
			if r.By != "dev-2" || r.Was != "dev-1" {
				t.Errorf("the eviction says by=%q was=%q, want the writer over the "+
					"author of what went", r.By, r.Was)
			}
		}
	}
	if evictions != 1 {
		t.Errorf("the journal holds %d evictions, want one", evictions)
	}
}

// TestReplacingIsNotAdding: a full memory that evicts on every correction
// would lose an entry every time somebody fixed a typo.
func TestReplacingIsNotAdding(t *testing.T) {
	s := store(t, 2, 200)
	write(t, s, "one", "a fact", "dev-1")
	write(t, s, "two", "a fact", "dev-1")
	_, evicted, err := s.Remember("one", "a newer fact", "dev-1")
	if err != nil {
		t.Fatalf("a full memory refused to correct itself: %v", err)
	}
	if evicted != nil {
		t.Errorf("correcting an entry evicted %s", evicted.Key)
	}
	if s.Len() != 2 {
		t.Errorf("the memory holds %d entries", s.Len())
	}
}

// TestARevisionSaysWhatItReplaced: writing a key again is how a memory is
// corrected, so it is also how one is quietly rewritten. The entry says whose
// line went and how many times the key has turned over; the journal says what
// the line was.
func TestARevisionSaysWhatItReplaced(t *testing.T) {
	s := store(t, 10, 200)
	write(t, s, "spec-281", "v8 approved", "dev-1")
	second := write(t, s, "spec-281", "v9 approved", "myself")
	if second.Was != "dev-1" || second.Rev != 1 {
		t.Errorf("the revision says was=%q rev=%d, want dev-1 and 1", second.Was, second.Rev)
	}
	third := write(t, s, "spec-281", "v9 withdrawn", "chair")
	if third.Was != "myself" || third.Rev != 2 {
		t.Errorf("the second revision says was=%q rev=%d", third.Was, third.Rev)
	}

	var revisions []Record
	for _, r := range journal(t, s) {
		if r.Act == ActRevised {
			revisions = append(revisions, r)
		}
	}
	if len(revisions) != 2 {
		t.Fatalf("the journal holds %d revisions, want two", len(revisions))
	}
	if revisions[0].Prev != "v8 approved" || revisions[0].Was != "dev-1" {
		t.Errorf("the journal lost what was replaced: %+v", revisions[0])
	}
	if revisions[1].Prev != "v9 approved" {
		t.Errorf("the journal lost the second line: %+v", revisions[1])
	}
	// A first write is not a revision.
	write(t, s, "gate", "8-12 min", "dev-1")
	for _, r := range journal(t, s) {
		if r.Key == "gate" && r.Act != ActRemembered {
			t.Errorf("a first write was journalled as %q", r.Act)
		}
	}
}

// TestNothingExpiresWithoutATTL: zero is forever, and it is the default. A
// fleet that never set it should find its memory where it left it.
func TestNothingExpiresWithoutATTL(t *testing.T) {
	s := store(t, 10, 200)
	tick := at(s, time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC))
	write(t, s, "gate", "8-12 min", "dev-1")
	tick(300 * 24 * time.Hour)
	if s.Len() != 1 {
		t.Error("an entry expired in a memory with no ttl")
	}
}

// TestAnEntryNobodyWantsExpires, and one the fleet keeps asking for does not.
// The difference is the point: expiry measures use, not age.
func TestAnEntryNobodyWantsExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.json")
	s := New(path, 10, 200, time.Hour)
	tick := at(s, time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC))
	write(t, s, "asked-for", "a fact", "dev-1")
	write(t, s, "abandoned", "a fact", "dev-1")

	// Half an hour on, somebody looks one of them up by name.
	tick(30 * time.Minute)
	if len(s.Recall("asked-for")) != 1 {
		t.Fatal("the entry was not found")
	}
	// Seventy minutes in, the one nobody asked for is an hour past its last
	// use. A bare recall is a listing, so it does not save it.
	tick(70 * time.Minute)
	held := s.Recall("")
	if len(held) != 1 || held[0].Key != "asked-for" {
		t.Fatalf("recall returned %v, want only the one that was asked for", keys(held))
	}
	// Two hours after it was last wanted, that one goes too.
	tick(2*time.Hour + 31*time.Minute)
	if got := s.Recall(""); len(got) != 0 {
		t.Errorf("recall returned %v, want nothing", keys(got))
	}

	var expired []string
	for _, r := range journal(t, s) {
		if r.Act == ActExpired {
			expired = append(expired, r.Key)
		}
	}
	if len(expired) != 2 {
		t.Errorf("the journal holds %v, want both expiries", expired)
	}
	// And an expiry makes room, which is the other half of what it is for.
	tick(3 * time.Hour)
	if s.Len() != 0 {
		t.Errorf("the memory holds %d entries after everything expired", s.Len())
	}
}

// TestABareRecallRefreshesNothing: it would refresh everything, every time,
// and there would be no least recently used left to find.
func TestABareRecallRefreshesNothing(t *testing.T) {
	s := store(t, 2, 200)
	tick := at(s, time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC))
	write(t, s, "one", "a fact", "dev-1")
	tick(time.Minute)
	write(t, s, "two", "a fact", "dev-1")
	tick(5 * time.Minute)
	_ = s.Recall("")
	tick(6 * time.Minute)
	_, evicted, err := s.Remember("three", "a fact", "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if evicted == nil || evicted.Key != "one" {
		t.Errorf("evicted %v, want the one written first — a listing is not a use", evicted)
	}
}

// TestTheJournalOutlivesTheProcess: it is the history, so a restart that
// truncated it would leave the state as the only account of itself.
func TestTheJournalOutlivesTheProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.json")
	first := New(path, 10, 200, 0)
	write(t, first, "gate", "8-12 min", "dev-1")

	second := New(path, 10, 200, 0)
	write(t, second, "gate", "10-14 min", "dev-2")
	if err := second.Forget("gate", "chair"); err != nil {
		t.Fatal(err)
	}

	recs := journal(t, second)
	var acts []string
	for _, r := range recs {
		acts = append(acts, r.Act)
	}
	want := []string{ActRemembered, ActRevised, ActForgotten}
	if strings.Join(acts, ",") != strings.Join(want, ",") {
		t.Errorf("the journal reads %v, want %v", acts, want)
	}
	if recs[2].Fact != "10-14 min" {
		t.Errorf("the forgetting recorded %q, want the line that went", recs[2].Fact)
	}
	// Who deleted it and whose line it was are different people, and an audit
	// asks after both.
	if recs[2].By != "chair" || recs[2].Was != "dev-2" {
		t.Errorf("the forgetting says by=%q was=%q, want chair over dev-2's line",
			recs[2].By, recs[2].Was)
	}
}

func keys(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Key)
	}
	return out
}

// TestForgettingWhatWasNeverThereSaysSo: an agent that mistypes a key should
// learn that, not be told it succeeded.
func TestForgettingWhatWasNeverThereSaysSo(t *testing.T) {
	s := store(t, 10, 200)
	err := s.Forget("never-written", "dev-1")
	if err == nil {
		t.Fatal("forgetting nothing reported success")
	}
	if !strings.Contains(err.Error(), "recall") {
		t.Errorf("the refusal does not say how to find the keys: %v", err)
	}
}

// TestMemorySurvivesTheProcess: the fleet restarts and the agents keep their
// sessions; it would be the wrong way round for swarm to be the one that
// forgot.
func TestMemorySurvivesTheProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.json")
	first := New(path, 10, 200, 0)
	if _, _, err := first.Remember("gate-runtime", "8-12 min", "dev-1"); err != nil {
		t.Fatal(err)
	}
	second := New(path, 10, 200, 0)
	held := second.Recall("")
	if len(held) != 1 || held[0].Fact != "8-12 min" {
		t.Errorf("a new process recalled %v", held)
	}
}

// TestNoMemoryIsNotAnEmptyMemory: a fleet that switched it off should be told
// so rather than shown an empty list it could write into.
func TestNoMemoryIsNotAnEmptyMemory(t *testing.T) {
	s := store(t, 0, 200)
	if s.On() {
		t.Error("max 0 left the memory on")
	}
	if _, _, err := s.Remember("gate", "a fact", "dev-1"); err == nil {
		t.Error("a fleet with no memory took an entry")
	} else if !strings.Contains(err.Error(), "memory.max") {
		t.Errorf("the refusal does not name the setting: %v", err)
	}
}
