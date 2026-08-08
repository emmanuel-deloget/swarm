// Package ipc is the control channel between the running swarm and every
// `swarm <command>` invocation, including the ones agents make themselves.
//
// The wire format is one JSON object per line over a Unix socket: easy to
// debug with socat, and enough to carry the streaming commands (attach,
// events) as a sequence of frames.
package ipc

import (
	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/hub"
	"time"
)

// Commands understood by the server.
const (
	CmdPing     = "ping"
	CmdInfo     = "info"
	CmdList     = "ls"
	CmdStart    = "start"
	CmdStop     = "stop"
	CmdRestart  = "restart"
	CmdInject   = "inject"
	CmdKeys     = "keys"
	CmdSend     = "send"
	CmdInbox    = "inbox"
	CmdScreen   = "screen"
	CmdStage    = "stage"
	CmdEvents   = "events"
	CmdBusTail  = "bus-tail"
	CmdBusStats = "bus-stats"
	CmdBusPause = "bus-pause"
	CmdThreads  = "bus-threads"
	CmdAttach   = "attach"
	CmdShutdown = "shutdown"
)

// Request is a command sent to the swarm.
type Request struct {
	Cmd string `json:"cmd"`

	// Target selects agents: a name, "@group", "@role", "all", or a
	// comma-separated list.
	Target string `json:"target,omitempty"`

	// From identifies the sender of a bus message. Agents fill it from
	// $SWARM_AGENT; the user's CLI leaves it empty, which means "user".
	From string `json:"from,omitempty"`

	// Text is the payload of inject and send.
	Text string `json:"text,omitempty"`

	// Kind classifies a bus message: question, answer, fyi, request, decision,
	// blocked. Empty is a plain note.
	Kind string `json:"kind,omitempty"`

	// Keys is a whitespace-separated list of key names for CmdKeys.
	Keys string `json:"keys,omitempty"`

	// Files are absolute paths attached to a message or injected as paths.
	Files []string `json:"files,omitempty"`

	// Submit sends the newline that validates injected text.
	Submit bool `json:"submit,omitempty"`
	// Raw writes bytes without sanitising or bracketed paste.
	Raw bool `json:"raw,omitempty"`
	// Paste overrides the agent's bracketed-paste setting.
	Paste *bool `json:"paste,omitempty"`

	// Peek leaves inbox messages pending.
	Peek bool `json:"peek,omitempty"`
	// WaitMS blocks inbox until a message arrives; -1 waits forever.
	WaitMS int `json:"wait_ms,omitempty"`

	// Lines bounds how much history events and screen return.
	Lines int `json:"lines,omitempty"`
	// Follow keeps the connection open and streams new frames.
	Follow bool `json:"follow,omitempty"`

	// Final refuses anyone the right to answer this message.
	Final bool `json:"final,omitempty"`
	// Thread continues a conversation; NewThread starts one.
	Thread    uint64 `json:"thread,omitempty"`
	NewThread bool   `json:"new_thread,omitempty"`

	// Flush hands over what piled up while the bus was paused.
	Flush bool `json:"flush,omitempty"`

	// Since bounds a bus summary, as a duration back from now.
	Since time.Duration `json:"since,omitempty"`
	// Plain asks for unstyled text instead of an ANSI rendering.
	Plain bool `json:"plain,omitempty"`

	// Name and Data stage a file in the shared directory.
	Name string `json:"name,omitempty"`
	Data []byte `json:"data,omitempty"`

	// Cols and Rows resize the target terminal (attach, or an explicit
	// resize on inject).
	Cols int `json:"cols,omitempty"`
	Rows int `json:"rows,omitempty"`

	// GraceMS is the grace period for stop and restart.
	GraceMS int `json:"grace_ms,omitempty"`
}

// Response is what the swarm answers. Streaming commands send several, and
// the final one has Done set.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Done  bool   `json:"done,omitempty"`

	Results  []hub.TargetResult `json:"results,omitempty"`
	Agents   []agent.Info       `json:"agents,omitempty"`
	Messages []bus.Message      `json:"messages,omitempty"`
	Stats    *bus.Stats         `json:"stats,omitempty"`
	Threads  []bus.Thread       `json:"threads,omitempty"`
	Paused   string             `json:"paused,omitempty"`
	Events   []event.Event      `json:"events,omitempty"`
	Event    *event.Event       `json:"event,omitempty"`

	Text string `json:"text,omitempty"`
	Path string `json:"path,omitempty"`

	// Data carries terminal bytes during an attach.
	Data []byte `json:"data,omitempty"`
	// Resync tells an attached client to clear and take Text as the new
	// screen, after it fell too far behind.
	Resync bool `json:"resync,omitempty"`

	Session   string `json:"session,omitempty"`
	Socket    string `json:"socket,omitempty"`
	DetachKey string `json:"detach_key,omitempty"`
	StateDir  string `json:"state_dir,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
	Token     string `json:"token,omitempty"`
	Shared    string `json:"shared,omitempty"`
}

func errorResponse(err error) Response {
	return Response{OK: false, Error: err.Error(), Done: true}
}
