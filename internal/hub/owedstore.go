package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/event"
)

// Debts on disk.
//
// What an agent has been asked and has not settled is the one piece of state
// that a restart used to destroy while being exactly what a restart makes
// someone want. An agent that has been stalled for days is a reason to upgrade
// the binary and restart the fleet — and the restart took the answer with it,
// so `swarm why` would say "nothing is waiting on it" about an agent that was
// still, plainly, waiting.
//
// The agents survive their own restart: they keep their sessions and their
// history. It was only swarm that forgot, which is the wrong way round for the
// thing whose job is to remember what the agents cannot.
//
// Nothing else is persisted. Messages are not: they are bounded anyway, and a
// history that came back from disk would be a second, older answer to a
// question `swarm bus tail` already answers. Debts are different because they
// are unbounded by design — they last until they are settled, and settling is
// the only thing that should end one.

// owedFile is where the debts are kept, inside the state directory that already
// holds the socket, the logs and the shared files.
const owedFile = "owed.json"

// owedSaveEvery is how often the debts are written when they have changed.
// Short enough that a kill -9 loses at most a few seconds of it, long enough
// that an idle fleet is not writing a file all day.
const owedSaveEvery = 3 * time.Second

// owedState is the file's contents.
type owedState struct {
	// Session guards against a state directory shared by two fleets: debts
	// belong to the conversation they were opened in, and an agent named dev-1
	// in another session is a different agent.
	Session string `json:"session"`
	// NextThread is the bus's counter, saved so a restored debt on thread 42
	// cannot be settled by the forty-second conversation after the restart.
	NextThread uint64      `json:"next_thread"`
	SavedAt    time.Time   `json:"saved_at"`
	Owing      []bus.Owing `json:"owing"`
}

func (h *Hub) owedPath() string { return filepath.Join(h.stateDir, owedFile) }

// saveOwed writes the debts out, atomically: a half-written file read at the
// next start would be worse than no file, since it would look like an answer.
func (h *Hub) saveOwed() error {
	owing, next := h.bus.Snapshot()
	body, err := json.MarshalIndent(owedState{
		Session:    h.cfg.Session,
		NextThread: next,
		SavedAt:    time.Now(),
		Owing:      owing,
	}, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')

	tmp := h.owedPath() + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.owedPath())
}

// loadOwed restores what was owed when the last process stopped.
//
// It reports rather than decides. A debt that came back is a claim about the
// past, and the past can have moved on without it: somebody may have answered
// out of band, the work may be long done, the ticket may be dead. Dropping
// them on an age rule would be guessing in the other direction, so everything
// comes back and everything is said — the count, the oldest, and any debt
// belonging to an agent this fleet no longer has.
func (h *Hub) loadOwed() {
	body, err := os.ReadFile(h.owedPath())
	if err != nil {
		if !os.IsNotExist(err) {
			h.log.Emit(event.KindError, "", "could not read what was owed: "+err.Error())
		}
		return
	}

	var st owedState
	if err := json.Unmarshal(body, &st); err != nil {
		h.log.Emit(event.KindError, "", fmt.Sprintf(
			"%s is not readable (%v); nothing was restored, and swarm cannot say "+
				"what any agent still owes from before this start", owedFile, err))
		return
	}
	if st.Session != "" && st.Session != h.cfg.Session {
		h.log.Emit(event.KindPattern, "", fmt.Sprintf(
			"%s belongs to session %q, not %q; nothing restored",
			owedFile, st.Session, h.cfg.Session))
		return
	}
	if len(st.Owing) == 0 {
		return
	}

	// An agent that no longer exists cannot settle anything, so restoring its
	// debt would create one nobody can close. Said out loud, because a debt
	// vanishing quietly is how someone concludes the work was finished.
	var keep []bus.Owing
	var orphans []string
	for _, o := range st.Owing {
		if _, ok := h.cfg.Agent(o.Agent); ok {
			keep = append(keep, o)
			continue
		}
		orphans = append(orphans, fmt.Sprintf("%s (owed %s to %s since %s)",
			o.Agent, o.Kind, o.From, o.Since.Format("2 Jan 15:04")))
	}

	n := h.bus.Restore(keep, st.NextThread)
	if n > 0 {
		oldest := time.Now()
		for _, o := range keep {
			if o.Since.Before(oldest) {
				oldest = o.Since
			}
		}
		h.log.Emit(event.KindPattern, "", fmt.Sprintf(
			"restored %d outstanding %s from before this start, oldest %s ago — "+
				"`swarm why <agent>` says what, `swarm done` clears what is no longer true",
			n, plural(n, "request", "requests"), time.Since(oldest).Round(time.Second)))
	}
	for _, o := range orphans {
		h.log.Emit(event.KindPattern, "", "dropped a debt for an agent this fleet "+
			"no longer has: "+o)
	}
}

// watchOwed writes the debts out as they change.
//
// On a timer rather than on every message: the bus changes under a lock held on
// the path that delivers messages, and a disk write there would put the file
// system between two agents talking. The cost of the timer is the last few
// seconds before a kill -9, which is the difference between remembering a debt
// and remembering it slightly late.
func (h *Hub) watchOwed(every time.Duration) {
	defer close(h.owedDone)
	tick := time.NewTicker(every)
	defer tick.Stop()
	var last string
	for {
		select {
		case <-h.owedStop:
			h.flushOwed(&last)
			return
		case <-tick.C:
			h.flushOwed(&last)
		}
	}
}

// flushOwed writes only when something changed, so an idle fleet does not
// rewrite the same file every few seconds for days.
func (h *Hub) flushOwed(last *string) {
	owing, next := h.bus.Snapshot()
	key := fmt.Sprintf("%d|%v", next, owing)
	if key == *last {
		return
	}
	// A fleet where nothing has ever been asked writes no file at all. Only
	// once one exists does it need rewriting to say that the debts are gone.
	if len(owing) == 0 {
		if _, err := os.Stat(h.owedPath()); os.IsNotExist(err) {
			*last = key
			return
		}
	}
	if err := h.saveOwed(); err != nil {
		h.log.Emit(event.KindError, "", "could not write what is owed: "+err.Error())
		return
	}
	*last = key
}

// plural is spelled here rather than shared: internal packages that borrow a
// word from each other end up sharing a package for it.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
