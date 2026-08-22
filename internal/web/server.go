// Package web serves the remote control: a single page that shows every
// agent's terminal and lets you type into it from another machine — or from a
// phone.
//
// The page carries no terminal-emulator library. swarm already emulates the
// terminals, so the server sends ready-made HTML lines and only the ones that
// changed since the last frame.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/hub"
	"github.com/emmanuel-deloget/swarm/internal/licenses"
)

//go:embed assets
var assets embed.FS

// Options configures the server.
type Options struct {
	// Addr is the listen address, "host:port".
	Addr string
	// Token authenticates every request. It is required.
	Token string
	// ReadOnly serves the terminals but refuses any input.
	ReadOnly bool
	// TLSCert and TLSKey enable HTTPS.
	TLSCert, TLSKey string
}

// Server is the HTTP remote control.
type Server struct {
	h    *hub.Hub
	opts Options

	tmpl *template.Template
	mux  *http.ServeMux
	srv  *http.Server
	ln   net.Listener
}

// New prepares the server without listening yet.
func New(h *hub.Hub, o Options) (*Server, error) {
	if o.Token == "" {
		return nil, errors.New("web: a token is required")
	}
	tmpl, err := template.ParseFS(assets, "assets/index.html", "assets/licenses.html")
	if err != nil {
		return nil, err
	}
	s := &Server{h: h, opts: o, tmpl: tmpl, mux: http.NewServeMux()}
	s.routes()
	s.srv = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.guard(s.handleIndex))
	s.mux.HandleFunc("/api/state", s.guard(s.handleState))
	s.mux.HandleFunc("/api/events", s.guard(s.handleEvents))
	s.mux.HandleFunc("/api/action", s.guard(s.handleAction))
	s.mux.HandleFunc("/api/upload", s.guard(s.handleUpload))
	s.mux.HandleFunc("/ws", s.guard(s.handleWS))
	s.mux.HandleFunc("/licenses", s.guard(s.handleLicenses))
	s.mux.HandleFunc("/style.css", s.guard(s.serveAsset("assets/style.css", "text/css; charset=utf-8")))
	s.mux.HandleFunc("/app.js", s.guard(s.serveAsset("assets/app.js", "text/javascript; charset=utf-8")))

	// The bundled terminal font. One route per face, built from a list, so a
	// request can only ever name a file that exists — a handler that took the
	// name from the path would be a directory of this binary's own assets
	// addressable by anyone holding the token.
	for _, face := range []string{"JuliaMono-Regular", "JuliaMono-Bold"} {
		s.mux.HandleFunc("/fonts/"+face+".woff2",
			s.guard(s.serveFont("assets/fonts/"+face+".woff2")))
	}
}

// Start begins listening. It returns once the socket is bound, so the caller
// can print the URL immediately.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("web: %w", err)
	}
	s.ln = ln
	go func() {
		var err error
		if s.opts.TLSCert != "" {
			err = s.srv.ServeTLS(ln, s.opts.TLSCert, s.opts.TLSKey)
		} else {
			err = s.srv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.h.Log().Emit(event.KindError, "", "web server stopped: "+err.Error())
		}
	}()
	return nil
}

// URL is the address to open in a browser.
func (s *Server) URL() string {
	if s.ln == nil {
		return ""
	}
	scheme := "http"
	if s.opts.TLSCert != "" {
		scheme = "https"
	}
	addr := s.ln.Addr().String()
	// A wildcard bind is not something you can click on; show a host that is.
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "::" || host == "0.0.0.0" || host == "" {
			host, err = os.Hostname()
			if err != nil || host == "" {
				host = "localhost"
			}
			addr = net.JoinHostPort(host, port)
		}
	}
	return scheme + "://" + addr
}

// Close shuts the server down.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// guard enforces the token. It accepts it in the query string (so a single URL
// is enough to share access), in a header, or in the cookie it sets on the
// first successful request.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("t")
		if token == "" {
			token = r.Header.Get("X-Swarm-Token")
		}
		if token == "" {
			if c, err := r.Cookie("swarm_token"); err == nil {
				token = c.Value
			}
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.opts.Token)) != 1 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			http.Error(w, "swarm: bad or missing token\n\nopen the URL printed by `swarm info`.", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("t") != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     "swarm_token",
				Value:    token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
				Secure:   s.opts.TLSCert != "",
			})
		}
		next(w, r)
	}
}

func (s *Server) serveAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := assets.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
}

// serveFont serves one face of the bundled font.
//
// Separate from serveAsset for its caching alone: the four faces are half a
// megabyte, they change only when this binary does, and a phone that re-fetches
// them on every visit pays for them every time. private rather than public
// because the token that got the request here can travel in the URL, and that
// is not a thing to hand to a shared cache.
func (s *Server) serveFont(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := assets.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "font/woff2")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		_, _ = w.Write(data)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	cfg := s.h.Config()
	data := struct {
		Session  string
		ReadOnly bool
		Agents   []agent.Info
	}{
		Session:  cfg.Session,
		ReadOnly: s.opts.ReadOnly,
		Agents:   s.h.Infos(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page is the control plane of a developer machine: never cache it,
	// never let another site frame it.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := s.tmpl.Execute(w, data); err != nil {
		s.h.Log().Emit(event.KindError, "", "web: "+err.Error())
	}
}

// handleLicenses renders the terms of everything bundled in this binary.
//
// A page of its own rather than a third view in the app: it is long, it never
// changes while the process runs, and it is the sort of thing someone opens
// once to answer a question and then leaves. `swarm licenses` prints the same
// list for anyone who would rather pipe it somewhere.
func (s *Server) handleLicenses(w http.ResponseWriter, _ *http.Request) {
	all, err := licenses.All()
	if err != nil {
		http.Error(w, "swarm: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type row struct {
		Title, About, Kind, File, Text, Anchor string
	}
	data := struct{ Notices []row }{}
	for _, n := range all {
		data.Notices = append(data.Notices, row{
			Title: n.Title(), About: n.About, Kind: n.Kind(), File: n.File, Text: n.Text,
			// The module path is the anchor, with the characters a fragment
			// cannot carry taken out.
			Anchor: strings.NewReplacer("/", "-", ".", "-", " ", "-").Replace(n.Name),
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := s.tmpl.ExecuteTemplate(w, "licenses.html", data); err != nil {
		s.h.Log().Emit(event.KindError, "", "web: "+err.Error())
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"session":   s.h.Config().Session,
		"agents":    s.h.Infos(),
		"read_only": s.opts.ReadOnly,
	})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	n := 100
	if v := r.URL.Query().Get("n"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			n = i
		}
	}
	writeJSON(w, s.h.Log().History(n))
}

type actionRequest struct {
	Action string   `json:"action"`
	Target string   `json:"target"`
	Text   string   `json:"text"`
	Keys   string   `json:"keys"`
	Files  []string `json:"files"`
	Submit *bool    `json:"submit"`
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.ReadOnly {
		http.Error(w, "this swarm is served read-only", http.StatusForbidden)
		return
	}
	var req actionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Target == "" {
		req.Target = "all"
	}
	submit := true
	if req.Submit != nil {
		submit = *req.Submit
	}

	var (
		results []hub.TargetResult
		err     error
		message string
	)
	switch req.Action {
	case "inject":
		text := req.Text
		if len(req.Files) > 0 {
			text = strings.TrimSpace(text + " " + strings.Join(req.Files, " "))
		}
		results, err = s.h.Inject("", req.Target, text, agent.InjectOptions{Submit: submit})
	case "keys":
		results, err = s.h.Keys("", req.Target, req.Keys)
	case "send":
		var msgs []bus.Message
		msgs, err = s.h.Send("user", req.Target, req.Text, req.Files)
		message = fmt.Sprintf("sent to %d agent(s)", len(msgs))
	case "start":
		results, err = s.h.Start(req.Target)
	case "stop":
		results, err = s.h.Stop(req.Target, 5*time.Second)
	case "restart":
		results, err = s.h.Restart(req.Target, 5*time.Second)
	default:
		http.Error(w, "unknown action "+req.Action, http.StatusBadRequest)
		return
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "results": results, "message": message})
}

// handleUpload stages a file — typically an image pasted or dropped in the
// browser — and returns the path agents can open.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.opts.ReadOnly {
		http.Error(w, "this swarm is served read-only", http.StatusForbidden)
		return
	}
	const maxUpload = 32 << 20
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	data := make([]byte, 0, header.Size)
	buf := make([]byte, 64*1024)
	for {
		n, err := file.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if err != nil {
			break
		}
		if len(data) > maxUpload {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
	}
	path, err := s.h.StageFile(header.Filename, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": path})
}

// wsFrame is what the server pushes to a browser.
type wsFrame struct {
	Type string `json:"type"`
	// Full is a complete screen, sent on connect and after a resize.
	Lines map[string]string `json:"lines,omitempty"`
	Full  []string          `json:"full,omitempty"`
	Cols  int               `json:"cols,omitempty"`
	Rows  int               `json:"rows,omitempty"`
	Info  *agent.Info       `json:"info,omitempty"`
	Text  string            `json:"text,omitempty"`
}

// wsInput is what a browser sends back.
type wsInput struct {
	Type   string `json:"type"`
	Data   string `json:"data,omitempty"`
	Keys   string `json:"keys,omitempty"`
	Submit bool   `json:"submit,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("agent")
	a, err := s.h.Agent(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Same-origin only: the token in a URL should not be usable by
		// another page in the browser.
		OriginPatterns: nil,
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Browser → agent.
	go func() {
		defer cancel()
		for {
			var in wsInput
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			if err := json.Unmarshal(data, &in); err != nil {
				continue
			}
			if s.opts.ReadOnly {
				continue
			}
			switch in.Type {
			case "data":
				_ = a.WriteRaw([]byte(in.Data))
			case "keys":
				_ = a.SendKeys(in.Keys)
			case "text":
				_, _ = a.Inject(in.Data, agent.InjectOptions{Submit: in.Submit})
			case "resize":
				if in.Cols > 0 && in.Rows > 0 {
					_ = a.Resize(in.Cols, in.Rows)
				}
			}
		}
	}()

	// Agent → browser: a fixed cadence, sending only the lines that moved.
	// It is much cheaper than streaming the raw pty and it survives a client
	// that misses a frame.
	// The grid view opens one socket per agent; those refresh slowly so that
	// watching the whole fleet at once stays cheap.
	period := 120 * time.Millisecond
	if r.URL.Query().Get("rate") == "slow" {
		period = 600 * time.Millisecond
	}
	var (
		prev     []string
		prevInfo agent.Info
		ticker   = time.NewTicker(period)
	)
	defer ticker.Stop()

	writeFrame := func(f wsFrame) error {
		data, err := json.Marshal(f)
		if err != nil {
			return err
		}
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return conn.Write(wctx, websocket.MessageText, data)
	}

	// The first frame goes out without waiting for a tick: a page that just
	// opened should not sit on "connecting..." for a frame interval.
	first := true
	for {
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
		first = false

		info := a.Info()
		lines := a.HTMLLines()
		cols, rows := info.Cols, info.Rows

		switch {
		case lines == nil:
			if prev != nil || prevInfo.State != info.State {
				prev = nil
				prevInfo = info
				if err := writeFrame(wsFrame{Type: "off", Info: &info, Text: offlineText(info)}); err != nil {
					return
				}
			}
		case len(prev) != len(lines):
			prev = lines
			prevInfo = info
			if err := writeFrame(wsFrame{Type: "full", Full: lines, Cols: cols, Rows: rows, Info: &info}); err != nil {
				return
			}
		default:
			changed := map[string]string{}
			for i := range lines {
				if lines[i] != prev[i] {
					changed[strconv.Itoa(i)] = lines[i]
				}
			}
			infoChanged := info.State != prevInfo.State || info.Attention != prevInfo.Attention ||
				info.Unread != prevInfo.Unread || info.Pid != prevInfo.Pid
			if len(changed) == 0 && !infoChanged {
				continue
			}
			prev = lines
			prevInfo = info
			f := wsFrame{Type: "diff", Lines: changed, Cols: cols, Rows: rows}
			if infoChanged {
				f.Info = &info
			}
			if err := writeFrame(f); err != nil {
				return
			}
		}
	}
}

func offlineText(in agent.Info) string {
	if in.Exit != "" {
		return in.Name + " is not running (" + in.Exit + ")"
	}
	return in.Name + " is not running"
}
