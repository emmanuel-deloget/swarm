package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/emmanuel-deloget/swarm/internal/agent"
	"github.com/emmanuel-deloget/swarm/internal/bus"
	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/hub"
	"github.com/emmanuel-deloget/swarm/internal/ipc"
	"github.com/emmanuel-deloget/swarm/internal/vterm"
)

func loadConfig(path string) (*config.Config, error) { return config.Load(path) }

// clientFlags are shared by every command that talks to a running swarm.
type clientFlags struct {
	socket string
	config string
	asJSON bool
}

func (c *clientFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.socket, "socket", "", "path to the swarm control socket")
	fs.StringVar(&c.config, "c", "", "path to swarm.yaml (used to locate the socket)")
	fs.BoolVar(&c.asJSON, "json", false, "print the raw JSON response")
}

func (c *clientFlags) dial() (*ipc.Client, error) {
	sock, err := resolveSocket(c.socket, c.config)
	if err != nil {
		return nil, err
	}
	return ipc.Dial(sock)
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("swarm "+name, flag.ExitOnError)
	return fs
}

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdList(args []string, detailed bool) error {
	var cf clientFlags
	fs := newFlagSet("ls")
	cf.register(fs)
	watch := fs.Duration("watch", 0, "refresh every interval (e.g. 2s) until interrupted")
	_ = parseArgs(fs, args, -1)

	target := fs.Arg(0)
	show := func() error {
		c, err := cf.dial()
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		resp, err := c.Do(ipc.Request{Cmd: ipc.CmdList, Target: target})
		if err != nil {
			return err
		}
		if cf.asJSON {
			return emitJSON(resp.Agents)
		}
		printAgents(resp.Agents, detailed)
		return nil
	}
	if *watch <= 0 {
		return show()
	}
	for {
		fmt.Print("\x1b[H\x1b[2J")
		if err := show(); err != nil {
			return err
		}
		time.Sleep(*watch)
	}
}

func printAgents(infos []agent.Info, detailed bool) {
	var rows [][]string
	if detailed {
		rows = append(rows, []string{"AGENT", "ROLE", "STATE", "PID", "UPTIME", "QUIET", "OUT", "MSG", "GIT", "SIZE", "COMMAND"})
	} else {
		rows = append(rows, []string{"AGENT", "ROLE", "STATE", "PID", "UPTIME", "MSG", "GIT"})
	}
	for _, in := range infos {
		if detailed {
			rows = append(rows, []string{
				in.Name, dash(in.Role), stateLabel(in), pidLabel(in.Pid), durLabel(in.Uptime),
				durLabel(in.Quiet), byteLabel(in.BytesOut), msgLabel(in.Unread), dash(in.Git),
				fmt.Sprintf("%dx%d", in.Cols, in.Rows), strings.Join(in.Command, " "),
			})
		} else {
			rows = append(rows, []string{
				in.Name, dash(in.Role), stateLabel(in), pidLabel(in.Pid), durLabel(in.Uptime),
				msgLabel(in.Unread), dash(in.Git),
			})
		}
	}
	printTable(rows)
}

// printTable aligns columns on their *visible* width. text/tabwriter counts
// bytes, so the colour codes in the state column would push everything out of
// line.
func printTable(rows [][]string) {
	if len(rows) == 0 {
		return
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				if w := ansi.StringWidth(cell); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}
	var b strings.Builder
	for _, row := range rows {
		for i, cell := range row {
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-ansi.StringWidth(cell)+2))
			}
		}
		b.WriteByte('\n')
	}
	fmt.Print(b.String())
}

func stateLabel(in agent.Info) string {
	// Stalled is said here rather than carried in State: the bus decides it,
	// not the agent, and putting it in State would change what "idle" means for
	// the delivery paths that key on it.
	label := string(in.State)
	if in.Stalled {
		label = "stalled"
	}
	if in.Attention != "" {
		label += "/" + in.Attention
	}
	if in.Exit != "" && in.State == agent.StateExited {
		label += " (" + in.Exit + ")"
	}
	color := ""
	switch {
	case in.Attention != "", in.Stalled:
		color = "\x1b[33m"
	case in.State == agent.StateWorking:
		color = "\x1b[32m"
	case in.State == agent.StateIdle:
		color = "\x1b[36m"
	case in.State == agent.StateExited:
		color = "\x1b[31m"
	}
	if color == "" {
		return label
	}
	return color + label + "\x1b[0m"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func pidLabel(pid int) string {
	if pid == 0 {
		return "-"
	}
	return fmt.Sprint(pid)
}

func durLabel(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.Round(time.Second).String()
}

func msgLabel(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("\x1b[35m%d\x1b[0m", n)
}

func byteLabel(n uint64) string {
	switch {
	case n > 1<<20:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	case n > 1<<10:
		return fmt.Sprintf("%.1fk", float64(n)/(1<<10))
	default:
		return fmt.Sprint(n)
	}
}

func cmdLifecycle(cmd string, args []string) error {
	var cf clientFlags
	fs := newFlagSet(cmd)
	cf.register(fs)
	grace := fs.Duration("grace", 5*time.Second, "how long to wait before killing the process")
	_ = parseArgs(fs, args, -1)

	target := fs.Arg(0)
	if target == "" {
		return fmt.Errorf("%s needs a target (an agent name, @group, or all)", cmd)
	}
	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	ipcCmd := map[string]string{"start": ipc.CmdStart, "stop": ipc.CmdStop, "restart": ipc.CmdRestart}[cmd]
	resp, err := c.Do(ipc.Request{Cmd: ipcCmd, Target: target, GraceMS: int(grace.Milliseconds())})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp.Results)
	}
	printResults(resp.Results)
	return nil
}

func printResults(results []hub.TargetResult) {
	for _, r := range results {
		if r.OK {
			fmt.Printf("\x1b[32m✓\x1b[0m %s %s\n", r.Agent, r.Info)
		} else {
			fmt.Printf("\x1b[31m✗\x1b[0m %s %s\n", r.Agent, r.Error)
		}
	}
}

func cmdInject(args []string) error {
	var cf clientFlags
	fs := newFlagSet("inject")
	cf.register(fs)
	submit := fs.Bool("submit", true, "send the newline that validates the prompt")
	raw := fs.Bool("raw", false, "write the bytes untouched (no sanitising, no bracketed paste)")
	noPaste := fs.Bool("no-paste", false, "disable bracketed paste for this injection")
	textFile := fs.String("text-file", "", "read the text to inject from a file (- for stdin)")
	var files stringList
	fs.Var(&files, "file", "stage a file and inject its path (repeatable); use for images")
	_ = parseArgs(fs, args, 1)

	target := fs.Arg(0)
	if target == "" {
		return fmt.Errorf("inject needs a target")
	}
	text := joinArgs(argsFrom(fs, 1))
	if *textFile != "" {
		data, err := readFileOrStdin(*textFile)
		if err != nil {
			return err
		}
		if text != "" {
			text += "\n"
		}
		text += string(data)
	}
	if text == "" && len(files) == 0 && !*submit {
		return fmt.Errorf("nothing to inject")
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	staged, err := stageAll(c, files)
	if err != nil {
		return err
	}

	req := ipc.Request{
		Cmd:    ipc.CmdInject,
		Target: target,
		Text:   text,
		Files:  staged,
		Submit: *submit,
		Raw:    *raw,
	}
	if *noPaste {
		no := false
		req.Paste = &no
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp.Results)
	}
	printResults(resp.Results)
	return nil
}

// stageAll copies local files into the shared directory and returns the paths
// agents should open.
func stageAll(c *ipc.Client, files []string) ([]string, error) {
	var out []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(ipc.Request{Cmd: ipc.CmdStage, Name: filepath.Base(f), Data: data})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Path)
	}
	return out, nil
}

func cmdKeys(args []string) error {
	var cf clientFlags
	fs := newFlagSet("keys")
	cf.register(fs)
	list := fs.Bool("list", false, "print the key names swarm understands and exit")
	// Flags are recognised anywhere here: the arguments are key names, not free
	// text, and no key name starts with a dash — so `swarm keys a1 esc -c x.yaml`
	// should work like any other command.
	_ = parseArgs(fs, args, -1)

	if *list {
		printKeyNames()
		return nil
	}

	target := fs.Arg(0)
	keys := joinArgs(argsFrom(fs, 1))
	if target == "" || keys == "" {
		return errors.New("usage: swarm keys <target> <key>...   (e.g. swarm keys dev-1 esc ctrl+c)")
	}
	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdKeys, Target: target, Keys: keys})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp.Results)
	}
	printResults(resp.Results)
	return nil
}

// printKeyNames documents the key vocabulary in one place: the names below plus
// the three patterns, and which of them can be bound rather than only sent.
func printKeyNames() {
	fmt.Println("Key names accepted by `swarm keys` and by detach_key:")
	fmt.Println()

	names := vterm.KeyNames()
	rows := make([][]string, 0, len(names))
	for _, n := range names {
		seq, err := vterm.KeySequence(n)
		if err != nil {
			continue
		}
		note := ""
		if !vterm.Bindable(n) {
			note = "can be sent, cannot be bound"
		}
		rows = append(rows, []string{"  " + n, quoteSeq(seq), note})
	}
	printTable(rows)

	fmt.Println()
	fmt.Println("Patterns, for anything not named above:")
	fmt.Println("  ctrl+<char>    ctrl+c, ctrl+d, ctrl+], ctrl+\\")
	fmt.Println("  ^<char>        ^c, ^d — the same thing, shorter")
	fmt.Println("  alt+<char>     alt+b; also alt+<name>, as in alt+left")
	fmt.Println("  <char>         a single character sends itself")
	fmt.Println()
	fmt.Println("Several keys in one go: swarm keys dev-1 esc ctrl+c enter")
	fmt.Println("A key marked above as \"cannot be bound\" is one a terminal never")
	fmt.Println("produces on its own, so detach_key would never fire on it.")
}

// quoteSeq renders a key's bytes readably: ^C rather than an invisible 0x03.
func quoteSeq(seq string) string {
	var b strings.Builder
	for _, c := range []byte(seq) {
		switch {
		case c == 0x1b:
			b.WriteString("ESC")
		case c == '\r':
			b.WriteString("CR")
		case c == '\n':
			b.WriteString("LF")
		case c == '\t':
			b.WriteString("TAB")
		case c == 0x7f:
			b.WriteString("DEL")
		case c == ' ':
			b.WriteString("SPACE")
		case c < 0x20:
			b.WriteByte('^')
			b.WriteByte(c + '@')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func cmdScreen(args []string) error {
	var cf clientFlags
	fs := newFlagSet("screen")
	cf.register(fs)
	plain := fs.Bool("plain", false, "strip colours and styling")
	_ = parseArgs(fs, args, -1)

	target := fs.Arg(0)
	if target == "" {
		return fmt.Errorf("screen needs a target")
	}
	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdScreen, Target: target, Plain: *plain})
	if err != nil {
		return err
	}
	fmt.Println(resp.Text)
	fmt.Print("\x1b[0m")
	return nil
}

func cmdSend(args []string, broadcast bool) error {
	name, textAfter := "send", 1
	if broadcast {
		// A broadcast has no target, so the message starts right away.
		name, textAfter = "broadcast", 0
	}
	var cf clientFlags
	fs := newFlagSet(name)
	cf.register(fs)
	from := fs.String("from", os.Getenv("SWARM_AGENT"), "sender name (defaults to $SWARM_AGENT, else 'user')")
	textFile := fs.String("text-file", "", "read the message body from a file (- for stdin)")
	kind := fs.String("kind", "", "what the message is for: "+kindList())
	final := fs.Bool("final", false, "close the matter: the bus refuses anyone the right to answer")
	newThread := fs.Bool("new-thread", false, "start a fresh conversation instead of continuing the last one")
	thread := fs.Uint64("thread", 0, "continue a particular conversation")
	var files stringList
	fs.Var(&files, "file", "attach a file: it is staged in the shared directory (repeatable)")
	_ = parseArgs(fs, args, textAfter)

	if *kind != "" && !bus.ValidKind(bus.Kind(*kind)) {
		return fmt.Errorf("unknown kind %q; try one of: %s", *kind, kindNames())
	}

	rest := fs.Args()
	target := "all"
	if !broadcast {
		if len(rest) == 0 {
			return fmt.Errorf("usage: swarm send <target> <message>")
		}
		target, rest = rest[0], rest[1:]
	}
	body := joinArgs(rest)
	if *textFile != "" {
		data, err := readFileOrStdin(*textFile)
		if err != nil {
			return err
		}
		if body != "" {
			body += "\n"
		}
		body += string(data)
	}
	if body == "" && len(files) == 0 {
		return fmt.Errorf("empty message")
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	staged, err := stageAll(c, files)
	if err != nil {
		return err
	}
	resp, err := c.Do(ipc.Request{
		Cmd:       ipc.CmdSend,
		Target:    target,
		From:      *from,
		Kind:      *kind,
		Final:     *final,
		Thread:    *thread,
		NewThread: *newThread,
		Text:      body,
		Files:     staged,
	})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp.Messages)
	}
	for _, m := range resp.Messages {
		how := "queued"
		if m.Pushed {
			how = "delivered"
		}
		fmt.Printf("\x1b[32m→\x1b[0m %s #%d %s\n", m.To, m.ID, how)
	}
	return nil
}

func cmdInbox(args []string) error {
	var cf clientFlags
	fs := newFlagSet("inbox")
	cf.register(fs)
	peek := fs.Bool("peek", false, "leave the messages unread")
	wait := fs.Duration("wait", 0, "block until a message arrives (0 = do not wait, -1s = forever)")
	_ = parseArgs(fs, args, -1)

	name := fs.Arg(0)
	if name == "" {
		name = os.Getenv("SWARM_AGENT")
	}
	if name == "" {
		return fmt.Errorf("inbox needs an agent name (inside an agent, $SWARM_AGENT is already set)")
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	waitMS := 0
	switch {
	case *wait < 0:
		waitMS = -1
	case *wait > 0:
		waitMS = int(wait.Milliseconds())
	}
	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdInbox, From: name, Peek: *peek, WaitMS: waitMS})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp.Messages)
	}
	if len(resp.Messages) == 0 {
		fmt.Println("no new message")
		return nil
	}
	printMessages(resp.Messages)
	return nil
}

func printMessages(msgs []bus.Message) {
	for _, m := range msgs {
		fmt.Printf("\x1b[1m#%d from %s\x1b[0m (%s)\n", m.ID, m.From, m.At.Format(time.RFC3339))
		fmt.Println(indent(m.Body, "  "))
		if len(m.Files) > 0 {
			fmt.Printf("  attached: %s\n", strings.Join(m.Files, " "))
		}
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func cmdStage(args []string) error {
	var cf clientFlags
	fs := newFlagSet("stage")
	cf.register(fs)
	_ = parseArgs(fs, args, -1)

	if fs.NArg() == 0 {
		return errors.New("usage: swarm stage <file> [more files]")
	}
	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	paths, err := stageAll(c, fs.Args())
	if err != nil {
		return err
	}
	for _, p := range paths {
		fmt.Println(p)
	}
	return nil
}

func cmdEvents(args []string) error {
	var cf clientFlags
	fs := newFlagSet("events")
	cf.register(fs)
	follow := fs.Bool("f", false, "keep streaming new events")
	lines := fs.Int("n", 50, "how many past events to show; 0 for none, -1 for all")
	_ = parseArgs(fs, args, -1)

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return c.Stream(ipc.Request{Cmd: ipc.CmdEvents, Follow: *follow, Lines: *lines}, func(r ipc.Response) bool {
		if cf.asJSON {
			if r.Event != nil {
				_ = emitJSON(r.Event)
			}
			for _, e := range r.Events {
				_ = emitJSON(e)
			}
			return true
		}
		for _, e := range r.Events {
			fmt.Println(formatEvent(e))
		}
		if r.Event != nil {
			fmt.Println(formatEvent(*r.Event))
		}
		return true
	})
}

func formatEvent(e event.Event) string {
	agentName := e.Agent
	if agentName == "" {
		agentName = "swarm"
	}
	color := "\x1b[90m"
	switch e.Severity() {
	case 2:
		color = "\x1b[31m"
	case 1:
		color = "\x1b[33m"
	}
	text := strings.Join(strings.Fields(strings.NewReplacer("\n", " ⏎ ", "\r", " ", "\t", " ").Replace(e.Text)), " ")
	return fmt.Sprintf("%s %s%-9s\x1b[0m %-14s %s",
		e.At.Format("15:04:05"), color, e.Kind, agentName, text)
}

func cmdInfo(args []string) error {
	var cf clientFlags
	fs := newFlagSet("info")
	cf.register(fs)
	_ = parseArgs(fs, args, -1)

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdInfo})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp)
	}
	fmt.Printf("session  %s\n", resp.Session)
	fmt.Printf("socket   %s\n", resp.Socket)
	fmt.Printf("state    %s\n", resp.StateDir)
	fmt.Printf("shared   %s\n", resp.Shared)
	if resp.WebURL != "" {
		url := resp.WebURL
		if resp.Token != "" {
			url += "?t=" + resp.Token
		}
		fmt.Printf("web      %s\n", url)
		if resp.Token != "" {
			fmt.Printf("token    %s\n", resp.Token)
		}
	} else {
		fmt.Printf("web      disabled\n")
	}
	return nil
}

func cmdShutdown(args []string) error {
	var cf clientFlags
	fs := newFlagSet("shutdown")
	cf.register(fs)
	_ = parseArgs(fs, args, -1)

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdShutdown})
	if err != nil {
		return err
	}
	fmt.Println(resp.Text)
	return nil
}

func cmdLogs(args []string) error {
	var cf clientFlags
	fs := newFlagSet("logs")
	cf.register(fs)
	follow := fs.Bool("f", false, "follow the file as it grows")
	raw := fs.Bool("raw", false, "keep the terminal escape sequences")
	_ = parseArgs(fs, args, -1)

	name := fs.Arg(0)
	if name == "" {
		return fmt.Errorf("usage: swarm logs <agent>")
	}
	c, err := cf.dial()
	if err != nil {
		return err
	}
	info, err := c.Do(ipc.Request{Cmd: ipc.CmdInfo})
	_ = c.Close()
	if err != nil {
		return err
	}
	path := filepath.Join(info.StateDir, "logs", name+".log")
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("no log for agent %q (%s)", name, path)
	}
	defer func() { _ = f.Close() }()

	write := func(p []byte) {
		if *raw {
			_, _ = os.Stdout.Write(p)
			return
		}
		_, _ = os.Stdout.WriteString(ansi.Strip(string(p)))
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			write(buf[:n])
		}
		if err == io.EOF {
			if !*follow {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
	}
}

func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// stringList collects a repeatable string flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// kindNames lists the message kinds, for an error that teaches rather than
// merely refuses.
func kindNames() string {
	var out []string
	for _, k := range bus.Kinds() {
		out = append(out, string(k))
	}
	return strings.Join(out, ", ")
}

// kindList names the kinds from the bus rather than from memory. Written out by
// hand, this list said "question, answer, fyi, request, decision, blocked" for
// as long as `done` existed without anyone noticing — and a flag that does not
// name a value is a value nobody uses.
func kindList() string {
	names := make([]string, 0, len(bus.Kinds()))
	for _, k := range bus.Kinds() {
		names = append(names, string(k))
	}
	return strings.Join(names, ", ")
}

// cmdDone settles what an agent was asked. It exists because a request had no
// way of ending: an answer closes a question, a decision closes a debate, and a
// demand for work closed nothing at all — so an agent that finished looked
// exactly like an agent that never started.
func cmdDone(args []string) error {
	var cf clientFlags
	fs := newFlagSet("done")
	cf.register(fs)
	from := fs.String("from", os.Getenv("SWARM_AGENT"), "who finished (defaults to $SWARM_AGENT)")
	thread := fs.Uint64("thread", 0, "settle one conversation; default is everything outstanding")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{
		Cmd:    ipc.CmdDone,
		From:   *from,
		Thread: *thread,
		Text:   strings.Join(fs.Args(), " "),
	})
	if err != nil {
		return err
	}
	fmt.Println(resp.Text)
	return nil
}
