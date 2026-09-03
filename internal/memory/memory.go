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
//
// Two things keep it from silting up, and both measure use rather than age. An
// entry that nobody has written or asked for in ttl expires; a write into a
// full memory drops the least recently used to make room. What goes is
// therefore what the fleet stopped running on, not what it settled first.
// Nothing is lost quietly: memory.json is the state, and memory.log beside it
// is every change that ever made it, including what an entry used to say.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	// Was is who wrote the line this one replaced, and Rev counts how often
	// the key has been written over. Replacing is how a memory is corrected,
	// so it is also how a memory is quietly rewritten: a standing fact four
	// agents have taken turns restating is worth a look, and without these the
	// only trace of the one before is in the journal.
	Was string `json:"was,omitempty"`
	Rev int    `json:"rev,omitempty"`

	// Used is when the entry was last written or asked for by name. It is what
	// ttl measures and what eviction picks by. A fact nobody restates and
	// everybody reads is the one the fleet is actually running on, and writing
	// is a poor proxy for that.
	Used time.Time `json:"used,omitempty"`
}

// used is when the entry was last wanted. An entry written before there was
// such a field has only its date, which is what it would have had anyway.
func (e Entry) used() time.Time {
	if e.Used.IsZero() {
		return e.At
	}
	return e.Used
}

// Store is the fleet's memory, on disk and in one place.
type Store struct {
	mu      sync.Mutex
	path    string
	journal string
	max     int
	chars   int
	ttl     time.Duration
	// now is the clock, so a test can age a memory without waiting for it.
	now     func() time.Time
	entries map[string]Entry
}

// What a journal line says happened.
const (
	ActRemembered = "remembered"
	ActRevised    = "revised"
	ActForgotten  = "forgotten"
	ActExpired    = "expired"
	ActEvicted    = "evicted"
)

// Record is one line of the journal: what happened to the memory, and when.
//
// memory.json holds what is true now, which is all a fleet needs and no help
// at all afterwards — writing a key again leaves nothing behind, because that
// is what writing it again means. So "what did this used to say, who changed
// it, and what was dropped to make room" has no answer anywhere else. The
// journal is append-only and nothing in swarm reads it back: it is for
// whoever is asking that question later.
type Record struct {
	At  time.Time `json:"at"`
	Act string    `json:"act"`
	Key string    `json:"key"`
	// Fact is the line the record is about: what the entry now holds, or for a
	// removal, what went. By is whoever did it, and is empty only for an
	// expiry, which nobody did.
	Fact string `json:"fact,omitempty"`
	By   string `json:"by,omitempty"`
	// Prev is the line a revision replaced. Was is who wrote the line that is
	// no longer held — the one revised over, forgotten, evicted or expired —
	// which is not the same person as By and is the one an audit asks after.
	Prev string `json:"prev,omitempty"`
	Was  string `json:"was,omitempty"`
}

// keyShape is what a key may be: short, flat, and typeable. No spaces, so a
// key cannot become a chapter heading.
var keyShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

// New opens the memory at a path, with the limits a fleet asked for. A ttl of
// zero is forever. The journal goes beside it under the same name: memory.json
// and memory.log are the state and its history.
func New(path string, entries, chars int, ttl time.Duration) *Store {
	s := &Store{
		path:    path,
		journal: strings.TrimSuffix(path, filepath.Ext(path)) + ".log",
		max:     entries,
		chars:   chars,
		ttl:     ttl,
		now:     time.Now,
		entries: map[string]Entry{},
	}
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

// TTL is how long an entry survives without being written or asked for, and
// zero is forever.
func (s *Store) TTL() time.Duration {
	if !s.On() {
		return 0
	}
	return s.ttl
}

// Remember writes one entry, replacing whatever that key held.
//
// Replacing rather than appending is the whole of "correct a memory": there is
// no edit command because writing the key again is the edit, and a fact that
// is still true gets its date refreshed by being restated. What it replaced is
// not gone — the entry carries who wrote it before, and the journal carries
// the line itself.
//
// A full memory makes room rather than refusing, and evicted names what it
// dropped. The caller is who caused it, so the caller is who should hear.
func (s *Store) Remember(key, fact, by string) (e Entry, evicted *Entry, err error) {
	if !s.On() {
		return Entry{}, nil, fmt.Errorf("this fleet has no memory; set memory.max in the configuration")
	}
	key = strings.TrimSpace(key)
	fact = strings.TrimSpace(fact)

	if !keyShape.MatchString(key) {
		return Entry{}, nil, fmt.Errorf("%q is not a key: two to thirty-two characters of "+
			"a-z, 0-9 and -, which is what makes it something you can forget later", key)
	}
	if fact == "" {
		return Entry{}, nil, fmt.Errorf("an entry needs something to say")
	}
	if i := strings.IndexAny(fact, "\n\r"); i >= 0 {
		return Entry{}, nil, fmt.Errorf("an entry is one line, and this one has %d; "+
			"say the fact, and leave out why it matters", strings.Count(fact, "\n")+1)
	}
	if n := len([]rune(fact)); n > s.chars {
		return Entry{}, nil, fmt.Errorf("an entry is at most %d characters and this one is %d; "+
			"say the fact, not the reasoning", s.chars, n)
	}
	if bad := prose(fact); bad != "" {
		return Entry{}, nil, fmt.Errorf("an entry starts with %s, which is formatting rather "+
			"than fact; write the line plainly", bad)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweep(now)

	e = Entry{Key: key, Fact: fact, By: by, At: now, Used: now}
	rec := Record{At: now, Act: ActRemembered, Key: key, Fact: fact, By: by}
	var recs []Record

	if old, replacing := s.entries[key]; replacing {
		e.Was, e.Rev = old.By, old.Rev+1
		rec.Act, rec.Prev, rec.Was = ActRevised, old.Fact, old.By
	} else if len(s.entries) >= s.max {
		victim := s.leastRecentlyUsed()
		delete(s.entries, victim.Key)
		evicted = &victim
		recs = append(recs, Record{At: now, Act: ActEvicted,
			Key: victim.Key, Fact: victim.Fact, By: by, Was: victim.By})
	}

	s.entries[key] = e
	recs = append(recs, rec)
	if err := s.save(); err != nil {
		return e, evicted, err
	}
	s.note(recs...)
	return e, evicted, nil
}

// leastRecentlyUsed is what goes when there is no room: the entry the fleet
// has gone longest without writing or asking for. Ties fall back to the older
// entry and then to the key, so a memory filled in one second still evicts in
// a settled order rather than whichever way the map was walked.
func (s *Store) leastRecentlyUsed() Entry {
	var out Entry
	first := true
	for _, e := range s.entries {
		if first || lessUsed(e, out) {
			out, first = e, false
		}
	}
	return out
}

func lessUsed(a, b Entry) bool {
	if !a.used().Equal(b.used()) {
		return a.used().Before(b.used())
	}
	if !a.At.Equal(b.At) {
		return a.At.Before(b.At)
	}
	return a.Key < b.Key
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

// Forget drops an entry, and says so when there was nothing to drop. Who asked
// is journalled: an entry deleted by somebody other than its author is the
// interesting case, and the record would answer the wrong question if it named
// whoever wrote the line instead.
func (s *Store) Forget(key, by string) error {
	if !s.On() {
		return fmt.Errorf("this fleet has no memory; set memory.max in the configuration")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweep(now)

	e, ok := s.entries[key]
	if !ok {
		return fmt.Errorf("nothing is remembered under %q; `swarm recall` lists the keys", key)
	}
	delete(s.entries, key)
	if err := s.save(); err != nil {
		return err
	}
	s.note(Record{At: now, Act: ActForgotten, Key: key, Fact: e.Fact, By: by, Was: e.By})
	return nil
}

// Recall is what the fleet knows, most recently written first. A pattern
// matches a key or a fact, so an agent that half-remembers can still find it.
//
// A pattern is also a use: what it matched has its clock reset, so ttl and
// eviction measure how long the fleet has gone without wanting a fact rather
// than how long since somebody last typed it. A bare recall is a listing and
// refreshes nothing — it would refresh everything, every time, and there would
// be no least recently used left to find.
func (s *Store) Recall(pattern string) []Entry {
	if !s.On() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweep(now)

	pattern = strings.ToLower(strings.TrimSpace(pattern))
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if pattern != "" &&
			!strings.Contains(strings.ToLower(e.Key), pattern) &&
			!strings.Contains(strings.ToLower(e.Fact), pattern) {
			continue
		}
		if pattern != "" {
			e.Used = now
			s.entries[e.Key] = e
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	// Reading changes only when a fact was last wanted, which the journal does
	// not record: it is a history of what the memory said, not of who looked.
	if pattern != "" && len(out) > 0 {
		_ = s.save()
	}
	return out
}

// Len is how many entries are held.
func (s *Store) Len() int {
	if !s.On() {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.now())
	return len(s.entries)
}

// sweep drops what has gone stale, saves, and journals it. The caller holds
// the lock.
//
// Expiry is lazy, at the top of everything public, rather than a goroutine on
// a timer. A timer would have to pick a period, and the only moments a stale
// entry matters are the moments somebody asks.
func (s *Store) sweep(now time.Time) {
	if s.ttl <= 0 {
		return
	}
	var recs []Record
	for k, e := range s.entries {
		if now.Sub(e.used()) <= s.ttl {
			continue
		}
		delete(s.entries, k)
		recs = append(recs, Record{At: now, Act: ActExpired, Key: k, Fact: e.Fact, Was: e.By})
	}
	if len(recs) == 0 {
		return
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Key < recs[j].Key })
	if s.save() == nil {
		s.note(recs...)
	}
}

// note appends to the journal, best effort. A memory that could not be
// journalled is still a memory, and the file it failed to write to sits beside
// the one save has just written to: a failure that is real will be reported
// there, where it costs the caller something to ignore.
func (s *Store) note(recs ...Record) {
	if len(recs) == 0 {
		return
	}
	f, err := os.OpenFile(s.journal, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	for _, r := range recs {
		body, err := json.Marshal(r)
		if err != nil {
			continue
		}
		_, _ = f.Write(append(body, '\n'))
	}
}

type onDisk struct {
	SavedAt time.Time `json:"saved_at"`
	Entries []Entry   `json:"entries"`
}

// save writes through a temporary file, so a memory is never half a memory.
// The caller holds the lock.
func (s *Store) save() error {
	out := onDisk{SavedAt: s.now()}
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
