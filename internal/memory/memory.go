// Package memory is what a fleet knows and its agents keep forgetting.
//
// An agent's context is compacted, and what it was told an hour ago goes with
// it. The bus already answers that for one thing — `swarm why` is the part of
// a conversation that does not get compacted — and this answers it for the
// rest: the standing facts a fleet works from, which belong to nobody's thread
// and survive every restart.
//
// What is deliberate here is what it refuses. An entry is a key and one short
// line, and the limits are not advice: an agent asked politely for something
// short writes an essay about brevity. Naming the thing before describing it
// is most of the work — nobody titles a paragraph on the nature of time
// `gate-runtime` — and the key is also what makes an entry something that can
// be corrected or dropped later, which a wall of prose is not.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry is one thing the fleet knows.
type Entry struct {
	Key  string    `json:"key"`
	Fact string    `json:"fact"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
}

// Store is the fleet's memory, on disk and in one place.
type Store struct {
	mu      sync.Mutex
	path    string
	max     int
	chars   int
	entries map[string]Entry
}

// keyShape is what a key may be: short, flat, and typeable. No spaces, so a
// key cannot become a chapter heading.
var keyShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// New opens the memory at a path, with the limits a fleet asked for.
func New(path string, entries, chars int) *Store {
	s := &Store{path: path, max: entries, chars: chars, entries: map[string]Entry{}}
	s.load()
	return s
}

// On reports whether this fleet has a memory at all.
func (s *Store) On() bool { return s != nil && s.max > 0 }

// Limits are what an entry and the memory may be, for whoever has to say so.
func (s *Store) Limits() (entries, chars int) {
	if !s.On() {
		return 0, 0
	}
	return s.max, s.chars
}

// Remember writes one entry, replacing whatever that key held.
//
// Replacing rather than appending is the whole of "correct a memory": there is
// no edit command because writing the key again is the edit, and a fact that
// is still true gets its date refreshed by being restated.
func (s *Store) Remember(key, fact, by string) (Entry, error) {
	if !s.On() {
		return Entry{}, fmt.Errorf("this fleet has no memory; set memory.max in the configuration")
	}
	key = strings.TrimSpace(key)
	fact = strings.TrimSpace(fact)

	if !keyShape.MatchString(key) {
		return Entry{}, fmt.Errorf("%q is not a key: two to thirty-two characters of "+
			"a-z, 0-9 and -, which is what makes it something you can forget later", key)
	}
	if fact == "" {
		return Entry{}, fmt.Errorf("an entry needs something to say")
	}
	if i := strings.IndexAny(fact, "\n\r"); i >= 0 {
		return Entry{}, fmt.Errorf("an entry is one line, and this one has %d; "+
			"say the fact, and leave out why it matters", strings.Count(fact, "\n")+1)
	}
	if n := len([]rune(fact)); n > s.chars {
		return Entry{}, fmt.Errorf("an entry is at most %d characters and this one is %d; "+
			"say the fact, not the reasoning", s.chars, n)
	}
	if bad := prose(fact); bad != "" {
		return Entry{}, fmt.Errorf("an entry starts with %s, which is formatting rather "+
			"than fact; write the line plainly", bad)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, replacing := s.entries[key]; !replacing && len(s.entries) >= s.max {
		return Entry{}, fmt.Errorf("the memory holds %d entries, which is the limit; "+
			"`swarm forget <key>` makes room. `swarm recall` shows what is there, "+
			"oldest last", s.max)
	}
	e := Entry{Key: key, Fact: fact, By: by, At: time.Now()}
	s.entries[key] = e
	return e, s.save()
}

// prose names the formatting an entry opened with, or "" when it opened with
// none. Headings, bullets and bold are how a line becomes a document.
func prose(fact string) string {
	switch {
	case strings.HasPrefix(fact, "#"):
		return "a heading"
	case strings.HasPrefix(fact, "- "), strings.HasPrefix(fact, "* "):
		return "a bullet"
	case strings.HasPrefix(fact, "**"):
		return "bold"
	}
	return ""
}

// Forget drops an entry, and says so when there was nothing to drop.
func (s *Store) Forget(key string) error {
	if !s.On() {
		return fmt.Errorf("this fleet has no memory; set memory.max in the configuration")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[key]; !ok {
		return fmt.Errorf("nothing is remembered under %q; `swarm recall` lists the keys", key)
	}
	delete(s.entries, key)
	return s.save()
}

// Recall is what the fleet knows, most recently written first. A pattern
// matches a key or a fact, so an agent that half-remembers can still find it.
func (s *Store) Recall(pattern string) []Entry {
	if !s.On() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if pattern != "" &&
			!strings.Contains(strings.ToLower(e.Key), pattern) &&
			!strings.Contains(strings.ToLower(e.Fact), pattern) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// Len is how many entries are held.
func (s *Store) Len() int {
	if !s.On() {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

type onDisk struct {
	SavedAt time.Time `json:"saved_at"`
	Entries []Entry   `json:"entries"`
}

// save writes through a temporary file, so a memory is never half a memory.
// The caller holds the lock.
func (s *Store) save() error {
	out := onDisk{SavedAt: time.Now()}
	for _, e := range s.entries {
		out.Entries = append(out.Entries, e)
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Key < out.Entries[j].Key })
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// load restores what was remembered. A memory that cannot be read is reported
// by the caller rather than thrown away here: the file is the only copy.
func (s *Store) load() {
	body, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var st onDisk
	if json.Unmarshal(body, &st) != nil {
		return
	}
	for _, e := range st.Entries {
		s.entries[e.Key] = e
	}
}
