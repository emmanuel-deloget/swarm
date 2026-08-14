package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/event"
)

// What the hub remembers about instances between runs.
//
// Two things, and neither is the instances themselves: they die with their
// ptys, and bringing one back would be bringing back an agent that knows
// nothing of the task it owes.
//
// The counter, because names must never be reused. The bus, the debts and the
// logs all refer to agents by name, so a worker-1 that meant one agent this
// morning and another this afternoon makes the history false — and the debts
// now survive a restart, which is exactly when the collision would happen.
//
// The dead, because a debt can outlive the agent that owed it. An instance
// spawned by a person keeps its debt when it dies, on purpose; `swarm why` then
// has to be able to say the agent is dead rather than that it never existed,
// and after a restart nothing else would know it had.

const spawnFile = "ephemeral.json"

// spawnState is the file's contents.
type spawnState struct {
	Session string         `json:"session"`
	SavedAt time.Time      `json:"saved_at"`
	Next    map[string]int `json:"next"`
	Gone    []Gone         `json:"gone,omitempty"`
}

func (h *Hub) spawnPath() string { return filepath.Join(h.stateDir, spawnFile) }

// saveSpawn writes the counters and the dead out, atomically.
func (h *Hub) saveSpawn() error {
	h.spawn.mu.Lock()
	st := spawnState{
		Session: h.cfg.Session,
		SavedAt: time.Now(),
		Next:    make(map[string]int, len(h.spawn.next)),
		Gone:    append([]Gone(nil), h.spawn.gone...),
	}
	for k, v := range h.spawn.next {
		st.Next[k] = v
	}
	// Instances that are alive right now die with this process, and their names
	// must not come back either.
	for name, in := range h.spawn.live {
		st.Gone = append(st.Gone, Gone{
			Name: name, Template: in.Template, Parent: in.Parent, Task: in.Task,
			Thread: in.Thread, Born: in.Born, Died: time.Now(), Why: "the swarm stopped",
		})
	}
	h.spawn.dirty = false
	h.spawn.mu.Unlock()

	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := h.spawnPath() + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.spawnPath())
}

// loadSpawn reads back the counters and the dead.
func (h *Hub) loadSpawn() {
	body, err := os.ReadFile(h.spawnPath())
	if err != nil {
		if !os.IsNotExist(err) {
			h.log.Emit(event.KindError, "", "could not read "+spawnFile+": "+err.Error())
		}
		return
	}
	var st spawnState
	if err := json.Unmarshal(body, &st); err != nil {
		// Not fatal, but not quiet either: starting over from zero would hand
		// out names that have already been used.
		h.log.Emit(event.KindError, "", fmt.Sprintf(
			"%s is not readable (%v); instance names may repeat ones from before "+
				"this start, which makes the bus history ambiguous", spawnFile, err))
		return
	}
	if st.Session != "" && st.Session != h.cfg.Session {
		return
	}

	h.spawn.mu.Lock()
	defer h.spawn.mu.Unlock()
	for k, v := range st.Next {
		if v > h.spawn.next[k] {
			h.spawn.next[k] = v
		}
	}
	h.spawn.gone = st.Gone
	if keep := h.cfg.Ephemeral.Remember; len(h.spawn.gone) > keep {
		h.spawn.gone = h.spawn.gone[len(h.spawn.gone)-keep:]
	}
}

// watchSpawn writes the state out when it changes, on the same timer and for
// the same reason as the debts: the counter moves under a lock held while a
// fleet is being changed, and a disk write there would put the file system in
// the middle of it.
func (h *Hub) watchSpawn(every time.Duration) {
	defer close(h.spawnDone)
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-h.spawnStop:
			h.flushSpawn()
			return
		case <-tick.C:
			h.flushSpawn()
		}
	}
}

func (h *Hub) flushSpawn(force ...bool) {
	h.spawn.mu.Lock()
	dirty := h.spawn.dirty
	h.spawn.mu.Unlock()
	if !dirty && len(force) == 0 {
		return
	}
	if err := h.saveSpawn(); err != nil {
		h.log.Emit(event.KindError, "", "could not write "+spawnFile+": "+err.Error())
	}
}
