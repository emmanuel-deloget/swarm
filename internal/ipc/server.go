package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/hub"
	"github.com/emmanuel-deloget/swarm/internal/sockpath"
)

// Server exposes the hub on a Unix socket.
type Server struct {
	hub *Hub
	ln  net.Listener

	path    string
	pointer string
	// OnShutdown is called when a client asks the swarm to quit.
	OnShutdown func()

	wg     sync.WaitGroup
	closed chan struct{}
	once   sync.Once

	// conns are the connections being served. Close needs them: waiting for a
	// client to hang up on its own is waiting on somebody else's good manners,
	// and an attach holds its connection open for as long as it is watching.
	connMu sync.Mutex
	conns  map[net.Conn]struct{}
}

// Hub is the subset of *hub.Hub the server needs. Keeping it as an interface
// makes the server testable with a fake fleet.
type Hub = hub.Hub

// Listen starts the control socket. A stale socket left by a crashed swarm is
// removed, but a live one is reported as an error so two swarms never fight
// over the same session.
func Listen(h *Hub) (*Server, error) {
	path := h.SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		if alive(path) {
			return nil, fmt.Errorf("a swarm is already running on %s", path)
		}
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// The socket is the control plane of the fleet: keep it private.
	_ = os.Chmod(path, 0o600)

	s := &Server{
		hub: h, ln: ln, path: path, pointer: h.SocketPointer(),
		closed: make(chan struct{}), conns: map[net.Conn]struct{}{},
	}
	if err := sockpath.WritePointer(s.pointer, path); err != nil {
		_ = ln.Close()
		return nil, err
	}
	go s.accept()
	return s, nil
}

func alive(path string) bool {
	c, err := net.DialTimeout("unix", path, 300*time.Millisecond)
	if err != nil {
		return false
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if err := json.NewEncoder(c).Encode(Request{Cmd: CmdPing}); err != nil {
		return false
	}
	var resp Response
	return json.NewDecoder(c).Decode(&resp) == nil && resp.OK
}

// Path returns the socket path.
func (s *Server) Path() string { return s.path }

// Close stops accepting and removes the socket.
func (s *Server) Close() error {
	s.once.Do(func() {
		close(s.closed)
		_ = s.ln.Close()
		// Then the connections themselves, so an unfinished exchange ends in a
		// read error rather than in a shutdown that never returns.
		s.connMu.Lock()
		for c := range s.conns {
			_ = c.Close()
		}
		s.connMu.Unlock()
		_ = os.Remove(s.path)
		if s.pointer != "" {
			_ = os.Remove(s.pointer)
		}
	})
	s.wg.Wait()
	return nil
}

func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.closed:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		s.wg.Add(1)
		s.track(conn, true)
		go func() {
			defer s.wg.Done()
			defer s.track(conn, false)
			s.serve(conn)
		}()
	}
}

// track adds or removes a connection from the set Close will shut.
func (s *Server) track(c net.Conn, add bool) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if add {
		s.conns[c] = struct{}{}
		return
	}
	delete(s.conns, c)
}

func (s *Server) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	var encMu sync.Mutex
	send := func(r Response) error {
		encMu.Lock()
		defer encMu.Unlock()
		return enc.Encode(r)
	}

	// A connection carries as many requests as the client wants: `swarm inject
	// -file x.png` stages the file and injects its path over the same one.
	// Streaming commands take the connection over and end it themselves.
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				_ = send(errorResponse(fmt.Errorf("bad request: %w", err)))
			}
			return
		}

		switch req.Cmd {
		case CmdEvents:
			s.handleEvents(req, send)
			return
		case CmdBusTail:
			s.handleBusTail(req, send)
			return
		case CmdAttach:
			s.handleAttach(req, dec, send)
			return
		default:
			resp := s.handle(req)
			resp.Done = true
			if err := send(resp); err != nil {
				return
			}
		}
	}
}

func (s *Server) handle(req Request) Response {
	h := s.hub
	switch req.Cmd {
	case CmdPing:
		return Response{OK: true}

	case CmdInfo:
		url, token := h.WebURL()
		return Response{
			OK:        true,
			Session:   h.Config().Session,
			DetachKey: h.Config().DetachKey,
			Socket:    s.path,
			StateDir:  h.StateDir(),
			WebURL:    url,
			Token:     token,
			Shared:    h.Config().Shared,
		}

	case CmdList:
		if req.Target == "" {
			return Response{OK: true, Agents: h.Infos()}
		}
		agents, err := h.Resolve(req.Target)
		if err != nil {
			return errorResponse(err)
		}
		pending := h.Bus().PendingAll()
		infos := make([]agent.Info, 0, len(agents))
		for _, a := range agents {
			info := a.Info()
			info.Unread = pending[info.Name]
			infos = append(infos, info)
		}
		return Response{OK: true, Agents: infos}

	case CmdStart:
		res, err := h.Start(target(req))
		return targetResponse(res, err)

	case CmdStop:
		res, err := h.Stop(target(req), grace(req))
		return targetResponse(res, err)

	case CmdRestart:
		res, err := h.Restart(target(req), grace(req))
		return targetResponse(res, err)

	case CmdInject:
		text := req.Text
		if len(req.Files) > 0 {
			// Injecting a file means injecting a path an agent can open.
			paths := ""
			for i, f := range req.Files {
				if i > 0 {
					paths += " "
				}
				paths += f
			}
			if text == "" {
				text = paths
			} else {
				text += " " + paths
			}
		}
		// An agent typing into another agent is the fleet talking to itself,
		// whatever command it used. Routing it through the bus is what makes
		// can_send mean something, puts it in `swarm bus tail`, and lets a
		// pause hold it — none of which a raw injection did.
		if _, ok := h.Config().Agent(req.From); ok {
			if why := busCannotExpress(req); why != "" {
				return errorResponse(fmt.Errorf(
					"an injection from an agent is carried on the bus, which %s. "+
						"Use `swarm send` for a message", why))
			}
			msgs, err := h.SendKind(req.From, target(req), bus.KindNote, req.Text, req.Files)
			if err != nil {
				return errorResponse(err)
			}
			return Response{OK: true, Messages: msgs}
		}
		res, err := h.Inject(target(req), text, agent.InjectOptions{
			Submit: req.Submit,
			Raw:    req.Raw,
			Paste:  req.Paste,
		})
		return targetResponse(res, err)

	case CmdKeys:
		// Keys are not a message and cannot be carried, so this one is checked
		// rather than routed: an agent may press keys only where it may write.
		if _, ok := h.Config().Agent(req.From); ok {
			if agents, err := h.Resolve(target(req)); err == nil {
				for _, a := range agents {
					if allowed, why := h.Config().MayReach(req.From, a.Name()); !allowed {
						return errorResponse(errors.New(why))
					}
				}
			}
		}
		res, err := h.Keys(target(req), req.Keys)
		return targetResponse(res, err)

	case CmdSend:
		if req.From != "" {
			if _, ok := h.Config().Agent(req.From); !ok {
				return errorResponse(fmt.Errorf(
					"no agent named %q; the sender of a message is one of them, "+
						"or nobody, which means you", req.From))
			}
		}
		msgs, err := h.SendOn(req.From, target(req), bus.Kind(req.Kind), req.Text, req.Files,
			hub.SendOptions{Final: req.Final, Thread: req.Thread, NewThread: req.NewThread})
		if err != nil {
			return errorResponse(err)
		}
		return Response{OK: true, Messages: msgs}

	case CmdBusPause:
		switch req.Text {
		case "pause":
			h.Pause(req.Keys)
		case "resume":
			h.Resume(req.Flush)
		}
		return Response{OK: true, Paused: h.Paused()}

	case CmdDone:
		settled, msgs, err := h.Done(req.From, req.Thread, req.Text)
		if err != nil {
			return errorResponse(err)
		}
		switch {
		case settled == 0:
			return Response{OK: true, Text: "nothing was outstanding"}
		case len(msgs) == 0:
			// Settled, but whoever asked has no mailbox — the user, or a
			// webhook. Saying "nothing was outstanding" here would be a lie.
			return Response{OK: true, Text: fmt.Sprintf("settled %d, nobody to tell", settled)}
		default:
			return Response{OK: true, Messages: msgs,
				Text: fmt.Sprintf("settled %d, told %d", settled, len(msgs))}
		}

	case CmdSpawn:
		// Target is the template, Text the task, From whoever asked — an agent
		// through the shim, or empty for a person.
		name, err := h.Spawn(req.From, req.Target, req.Text)
		if err != nil {
			return errorResponse(err)
		}
		return Response{OK: true, Text: name}

	case CmdWhy:
		// Target rather than From: the usual caller is an agent asking about
		// itself, but a person asking about an agent is the same question.
		who := req.Target
		if who == "" {
			who = req.From
		}
		if who == "" {
			return errorResponse(errors.New("why needs an agent name " +
				"(inside an agent, $SWARM_AGENT is already set)"))
		}
		w, err := h.Why(who)
		if err != nil {
			return errorResponse(err)
		}
		return Response{OK: true, Why: &w}

	case CmdThreads:
		return Response{OK: true, Threads: h.Bus().Threads(h.Config().Bus.MaxTurns)}

	case CmdBusStats:
		since := req.Since
		if since <= 0 {
			since = time.Hour
		}
		st := h.Bus().StatsSince(time.Now().Add(-since))
		return Response{OK: true, Stats: &st}

	case CmdInbox:
		name := req.From
		if name == "" {
			name = req.Target
		}
		if name == "" {
			return errorResponse(errors.New("inbox needs an agent name (set SWARM_AGENT or pass one)"))
		}
		wait := time.Duration(0)
		switch {
		case req.WaitMS < 0:
			wait = 24 * time.Hour
		case req.WaitMS > 0:
			wait = time.Duration(req.WaitMS) * time.Millisecond
		}
		msgs, note, err := h.Inbox(name, req.Peek, wait, s.closed)
		if err != nil {
			return errorResponse(err)
		}
		return Response{OK: true, Messages: msgs, Text: note}

	case CmdScreen:
		agents, err := h.Resolve(target(req))
		if err != nil {
			return errorResponse(err)
		}
		out := ""
		for i, a := range agents {
			if i > 0 {
				out += "\n"
			}
			if len(agents) > 1 {
				out += fmt.Sprintf("\x1b[0m=== %s ===\n", a.Name())
			}
			if req.Plain {
				out += a.Text()
			} else {
				out += a.Render()
			}
		}
		return Response{OK: true, Text: out}

	case CmdStage:
		path, err := h.StageFile(req.Name, req.Data)
		if err != nil {
			return errorResponse(err)
		}
		return Response{OK: true, Path: path}

	case CmdShutdown:
		if s.OnShutdown != nil {
			go s.OnShutdown()
		}
		return Response{OK: true, Text: "shutting down"}
	}
	// Said differently from the client's own "unknown command", because the
	// two are different problems with the same shape: a swarm runs for days
	// while the binary that talks to it is upgraded underneath, and a reader
	// who cannot tell which end is behind will reinstall the one that is fine.
	return errorResponse(fmt.Errorf("the running swarm does not know the command %q; "+
		"it was started with an older build than the one you are using — restart it to catch up",
		req.Cmd))
}

func (s *Server) handleEvents(req Request, send func(Response) error) {
	// req.Lines is taken as it comes: the client carries the default, so zero
	// here means the caller asked for no backlog rather than for nothing in
	// particular.
	history := s.hub.Log().History(req.Lines)
	if err := send(Response{OK: true, Events: history, Done: !req.Follow}); err != nil || !req.Follow {
		return
	}
	ch, cancel := s.hub.Log().Subscribe(256)
	defer cancel()
	for {
		select {
		case <-s.closed:
			_ = send(Response{OK: true, Done: true})
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			ev := e
			if err := send(Response{OK: true, Event: &ev}); err != nil {
				return
			}
		}
	}
}

// handleBusTail streams what the bus carries. Following polls rather than
// subscribes: a mailbox already wakes its own waiters, and adding a second
// fan-out to the bus for a diagnostic would be machinery the fleet pays for
// whether or not anybody is watching.
func (s *Server) handleBusTail(req Request, send func(Response) error) {
	msgs := s.hub.Bus().Recent(req.Lines)
	if err := send(Response{OK: true, Messages: msgs, Done: !req.Follow}); err != nil || !req.Follow {
		return
	}
	// Where to follow from. With no history shown, the newest id already on the
	// bus — starting from zero would hand over everything at the first tick,
	// which is what asking for none was meant to avoid.
	last := s.hub.Bus().LastID()
	if len(msgs) > 0 {
		last = msgs[len(msgs)-1].ID
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.closed:
			_ = send(Response{OK: true, Done: true})
			return
		case <-tick.C:
			fresh := s.hub.Bus().Since(last)
			if len(fresh) == 0 {
				continue
			}
			last = fresh[len(fresh)-1].ID
			if err := send(Response{OK: true, Messages: fresh}); err != nil {
				return
			}
		}
	}
}

// handleAttach turns the connection into a two-way terminal link: screen bytes
// flow out, keystrokes flow in.
func (s *Server) handleAttach(req Request, dec *json.Decoder, send func(Response) error) {
	a, err := s.hub.Agent(req.Target)
	if err != nil {
		_ = send(errorResponse(err))
		return
	}
	term := a.Terminal()
	if term == nil {
		_ = send(errorResponse(fmt.Errorf("agent %s is not running", req.Target)))
		return
	}
	if req.Cols > 0 && req.Rows > 0 {
		_ = a.Resize(req.Cols, req.Rows)
	}

	sub := term.Subscribe(1 << 20)
	defer sub.Close()
	if err := send(Response{OK: true, Text: sub.Snapshot, Resync: true}); err != nil {
		return
	}

	done := make(chan struct{})
	// Client → agent: keystrokes and resizes.
	go func() {
		defer close(done)
		for {
			var in Request
			if err := dec.Decode(&in); err != nil {
				return
			}
			switch {
			case len(in.Data) > 0:
				if err := a.WriteRaw(in.Data); err != nil {
					return
				}
			case in.Cols > 0 && in.Rows > 0:
				_ = a.Resize(in.Cols, in.Rows)
			}
		}
	}()

	// Agent → client.
	go func() {
		for {
			data, resync, err := sub.Next()
			if err != nil {
				_ = send(Response{OK: true, Done: true, Text: "agent exited"})
				return
			}
			var resp Response
			if resync {
				resp = Response{OK: true, Resync: true, Text: sub.Resnapshot()}
			} else {
				resp = Response{OK: true, Data: data}
			}
			if err := send(resp); err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-s.closed:
	case <-term.Done():
		_ = send(Response{OK: true, Done: true, Text: "agent exited"})
	}
}

func target(req Request) string {
	if req.Target == "" {
		return "all"
	}
	return req.Target
}

func grace(req Request) time.Duration {
	if req.GraceMS <= 0 {
		return 5 * time.Second
	}
	return time.Duration(req.GraceMS) * time.Millisecond
}

// busCannotExpress names what an injection asks for that a bus message has no
// way to carry, or "" when there is nothing. Dropping the option quietly would
// be worse: -submit=false exists precisely so the newline is not sent.
func busCannotExpress(req Request) string {
	switch {
	case !req.Submit:
		return "always submits (-submit=false has no equivalent)"
	case req.Raw:
		return "sanitises what it carries (-raw has no equivalent)"
	case req.Paste != nil:
		return "leaves bracketed paste to the recipient (-no-paste has no equivalent)"
	}
	return ""
}

func targetResponse(res []hub.TargetResult, err error) Response {
	if err != nil {
		return errorResponse(err)
	}
	ok := false
	for _, r := range res {
		if r.OK {
			ok = true
			break
		}
	}
	resp := Response{OK: ok, Results: res}
	if !ok && len(res) > 0 {
		resp.Error = res[0].Error
	}
	return resp
}
