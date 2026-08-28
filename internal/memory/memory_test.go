package memory

import (
	"path/filepath"
	"strings"
	"testing"
)

func store(t *testing.T, entries, chars int) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "memory.json"), entries, chars)
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
		_, err := s.Remember(c.key, c.fact, "dev-1")
		if err == nil {
			t.Errorf("%s was accepted", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: the refusal says %q, want it to mention %q", c.what, err, c.says)
		}
	}
	// And the shape that is wanted goes in.
	if _, err := s.Remember("gate-runtime", "make integration takes 8-12 min", "dev-1"); err != nil {
		t.Errorf("a key and one short line was refused: %v", err)
	}
}

// TestWritingAKeyAgainReplacesIt: there is no edit command because this is the
// edit, and a fact that is still true has its date refreshed by being restated.
func TestWritingAKeyAgainReplacesIt(t *testing.T) {
	s := store(t, 10, 200)
	first, err := s.Remember("spec-281", "v8 approved", "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Remember("spec-281", "v9 approved", "myself")
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

// TestAFullMemoryRefusesRatherThanTidies: a memory that drops its oldest entry
// to make room is a cache. What is wanted is that somebody decide what is no
// longer true.
func TestAFullMemoryRefusesRatherThanTidies(t *testing.T) {
	s := store(t, 2, 200)
	for _, k := range []string{"one", "two"} {
		if _, err := s.Remember(k, "a fact", "dev-1"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Remember("three", "a fact", "dev-1")
	if err == nil {
		t.Fatal("a full memory took another entry")
	}
	if !strings.Contains(err.Error(), "forget") {
		t.Errorf("the refusal does not name the way out: %v", err)
	}
	if s.Len() != 2 {
		t.Errorf("the memory holds %d entries, want the two it was told to", s.Len())
	}
	// Replacing one that is already there is not adding one.
	if _, err := s.Remember("one", "a newer fact", "dev-1"); err != nil {
		t.Errorf("a full memory refused to correct itself: %v", err)
	}
	// And forgetting makes room.
	if err := s.Forget("two"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remember("three", "a fact", "dev-1"); err != nil {
		t.Errorf("room was made and the write still failed: %v", err)
	}
}

// TestForgettingWhatWasNeverThereSaysSo: an agent that mistypes a key should
// learn that, not be told it succeeded.
func TestForgettingWhatWasNeverThereSaysSo(t *testing.T) {
	s := store(t, 10, 200)
	err := s.Forget("never-written")
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
	first := New(path, 10, 200)
	if _, err := first.Remember("gate-runtime", "8-12 min", "dev-1"); err != nil {
		t.Fatal(err)
	}
	second := New(path, 10, 200)
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
	if _, err := s.Remember("gate", "a fact", "dev-1"); err == nil {
		t.Error("a fleet with no memory took an entry")
	} else if !strings.Contains(err.Error(), "memory.max") {
		t.Errorf("the refusal does not name the setting: %v", err)
	}
}
