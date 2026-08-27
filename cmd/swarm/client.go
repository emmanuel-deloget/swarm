package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
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
	c.registerWithout(fs)
	fs.BoolVar(&c.asJSON, "json", false, "print the raw JSON response")
}

// registerWithout leaves out -json, for a command that has no response to
// print one of. Offering the flag and ignoring it is worse than not offering
// it: a script gets prose where it asked for JSON, and nothing says so.
func (c *clientFlags) registerWithout(fs *flag.FlagSet) {
	fs.StringVar(&c.socket, "socket", "", "path to the swarm control socket")
	fs.StringVar(&c.config, "c", "", "path to swarm.yaml (used to locate the socket)")
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

// whoAmI settles who is asking. Inside an agent the shim sets $SWARM_AGENT,
// and that is the answer whatever -from says: an identity a caller can choose
// is not an identity, and can_send bounds nothing if the sender picks its own
// name. Disagreeing with it is refused rather than ignored, so a script that
// meant something by -from finds out.
//
// A person's shell has no $SWARM_AGENT, so -from stays theirs. This is the
// same line `swarm spawn` already draws: empty means a person asked.
func whoAmI(fromFlag string) (string, error) {
	me := os.Getenv("SWARM_AGENT")
	if me == "" {
		return fromFlag, nil
	}
	if fromFlag != "" && fromFlag != me {
		return "", fmt.Errorf("-from says %q but you are %s; a message you send "+
			"is from you. Drop -from", fromFlag, me)
	}
	return me, nil
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

// sparkline draws a series as one line of eighths. Empty slices are the lowest
// mark rather than a blank, so the line reads as a floor with things happening
// above it — a gap and a quiet moment look different and mean different things.
func sparkline(series []int) string {
	// Runes, not bytes: each of these is three bytes, and scaling by the byte
	// length walks off the end of the marks.
	marks := []rune("▁▂▃▄▅▆▇█")
	if len(series) == 0 {
		return ""
	}
	top := 0
	for _, n := range series {
		if n > top {
			top = n
		}
	}
	var b strings.Builder
	for _, n := range series {
		if top == 0 {
			b.WriteRune(marks[0])
			continue
		}
		b.WriteRune(marks[n*(len(marks)-1)/top])
	}
	return b.String()
}

// meter draws a proportion as a bar. Ten cells: enough to read a glance at,
// short enough to sit in a table beside everything else.
func meter(n, ceiling, width int) string {
	if ceiling <= 0 || width <= 0 {
		return ""
	}
	filled := n * width / ceiling
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	// A balance that is spent but not gone still shows something, because an
	// empty bar and no bar at all read the same and mean different things.
	if filled == 0 && n > 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// talkLabel is what an agent put on the bus, drawn rather than counted.
//
// The MSG column beside it counts unread mail, which a fleet delivering by
// push never has: eleven agents once showed a dash apiece while two hundred
// messages an hour went between them. This is the column that would have said
// so.
func talkLabel(in agent.Info) string {
	if len(in.Talked) == 0 || in.Talking == 0 {
		return "-"
	}
	return fmt.Sprintf("%s %d", sparkline(in.Talked), in.Talking)
}

// budgetLabel is an agent's allowance as hit points: the bar first, because a
// proportion is what the eye reads, and the numbers after for whoever wants
// them.
func budgetLabel(in agent.Info) string {
	if in.BudgetMax <= 0 {
		return "-"
	}
	return fmt.Sprintf("%s %d/%d", meter(in.Budget, in.BudgetMax, 10), in.Budget, in.BudgetMax)
}

func printAgents(infos []agent.Info, detailed bool) {
	var rows [][]string
	if detailed {
		rows = append(rows, []string{"AGENT", "ROLE", "STATE", "PID", "UPTIME", "QUIET", "OUT", "MSG", "TALK", "BUDGET", "GIT", "SIZE", "COMMAND"})
	} else {
		rows = append(rows, []string{"AGENT", "ROLE", "STATE", "PID", "UPTIME", "MSG", "TALK", "GIT"})
	}
	for _, in := range infos {
		if detailed {
			rows = append(rows, []string{
				in.Name, dash(in.Role), stateLabel(in), pidLabel(in.Pid), durLabel(in.Uptime),
				durLabel(in.Quiet), byteLabel(in.BytesOut), msgLabel(in.Unread),
				talkLabel(in), budgetLabel(in), dash(in.Git),
				fmt.Sprintf("%dx%d", in.Cols, in.Rows), strings.Join(in.Command, " "),
			})
		} else {
			rows = append(rows, []string{
				in.Name, dash(in.Role), stateLabel(in), pidLabel(in.Pid), durLabel(in.Uptime),
				msgLabel(in.Unread), talkLabel(in), dash(in.Git),
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
	sender, err := whoAmI("")
	if err != nil {
		return err
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	ipcCmd := map[string]string{"start": ipc.CmdStart, "stop": ipc.CmdStop, "restart": ipc.CmdRestart}[cmd]
	resp, err := c.Do(ipc.Request{Cmd: ipcCmd, From: sender, Target: target,
		GraceMS: int(grace.Milliseconds())})
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

	sender, err := whoAmI("")
	if err != nil {
		return err
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
		From:   sender,
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
		if len(resp.Messages) > 0 {
			return emitJSON(resp.Messages)
		}
		return emitJSON(resp.Results)
	}
	if len(resp.Messages) > 0 {
		// An agent's injection is carried on the bus, so it reports like a
		// send: the recipient's own delivery mode decided what happened to it.
		printDelivered(resp.Messages)
		return nil
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
	read := fs.Bool("read", false, "print the bytes this terminal sends for each key, and exit")
	// Flags are recognised anywhere here: the arguments are key names, not free
	// text, and no key name starts with a dash — so `swarm keys a1 esc -c x.yaml`
	// should work like any other command.
	_ = parseArgs(fs, args, -1)

	if *list {
		printKeyNames()
		return nil
	}
	if *read {
		return readKeys()
	}

	target := fs.Arg(0)
	keys := joinArgs(argsFrom(fs, 1))
	if target == "" || keys == "" {
		return errors.New("usage: swarm keys <target> <key>...   (e.g. swarm keys dev-1 esc ctrl+c)")
	}
	sender, err := whoAmI("")
	if err != nil {
		return err
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdKeys, From: sender, Target: target, Keys: keys})
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
	fmt.Println("  alt+<char>     alt+b, alt+enter")
	fmt.Println("  <char>         a single character sends itself")
	fmt.Println("  <mods>+<nav>   ctrl+left, shift+home, ctrl+shift+pgup —")
	fmt.Println("                 any of ctrl, shift and alt, on up, down,")
	fmt.Println("                 left, right, home, end, pgup, pagedown,")
	fmt.Println("                 insert and delete")
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
	if cf.asJSON {
		return emitJSON(resp)
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
	from := fs.String("from", "", "sender name; inside an agent it is always that agent, else 'user'")
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
	sender, err := whoAmI(*from)
	if err != nil {
		return err
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
		From:      sender,
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
	printDelivered(resp.Messages)
	printBudget(resp.Budget)
	return nil
}

// printBudget says what is left to spend, when a fleet bounds it. On stderr,
// because a script reading the delivery lines should not have to filter it out
// — and an agent reads both.
func printBudget(b *hub.Budget) {
	if b == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "swarm: %d of %d left to say\n", b.Left, b.Max)
}

// printDelivered says where each copy of a message went, and whether it was
// typed in or is waiting. Shared with inject, which becomes a send when an
// agent is the one asking.
func printDelivered(msgs []bus.Message) {
	for _, m := range msgs {
		how := "queued"
		if m.Pushed {
			how = "delivered"
		}
		fmt.Printf("\x1b[32m→\x1b[0m %s #%d %s\n", m.To, m.ID, how)
	}
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
	if resp.Text != "" {
		fmt.Fprintln(os.Stderr, "swarm: "+resp.Text)
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
	if cf.asJSON {
		return emitJSON(paths)
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

	sender, err := whoAmI("")
	if err != nil {
		return err
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	resp, err := c.Do(ipc.Request{Cmd: ipc.CmdShutdown, From: sender})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp)
	}
	fmt.Println(resp.Text)
	return nil
}

func cmdLogs(args []string) error {
	var cf clientFlags
	fs := newFlagSet("logs")
	cf.registerWithout(fs)
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
	from := fs.String("from", "", "who finished; inside an agent it is always that agent")
	thread := fs.Uint64("thread", 0, "settle one conversation; default is everything outstanding")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}

	sender, err := whoAmI(*from)
	if err != nil {
		return err
	}

	c, err := cf.dial()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	resp, err := c.Do(ipc.Request{
		Cmd:    ipc.CmdDone,
		From:   sender,
		Thread: *thread,
		Text:   strings.Join(fs.Args(), " "),
	})
	if err != nil {
		return err
	}
	if cf.asJSON {
		return emitJSON(resp)
	}
	fmt.Println(resp.Text)
	return nil
}

// readKeys prints what this terminal sends for each key pressed, until ctrl+c.
//
// swarm decides what a key means from the bytes it receives, and terminals do
// not agree on which bytes those are. A Windows console produces a plain
// backslash for ctrl+\, so the key that used to detach typed into the agent
// instead — found by pressing it, after being reasoned about wrongly twice
// from a machine with no console. This turns that kind of question into a
// measurement anyone can take, and paste into a bug report.
func readKeys() error {
	stdin := os.Stdin.Fd()
	if !term.IsTerminal(stdin) {
		return errors.New("keys -read needs a terminal on stdin")
	}
	old, err := term.MakeRaw(stdin)
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(stdin, old) }()
	restoreVT := enableVTOutput()
	defer restoreVT()

	// Mouse reporting too, since "the wheel does nothing" is the same kind of
	// question: a terminal that does not report it converts the wheel into up
	// and down arrows instead, and the two are told apart by what arrives here.
	fmt.Print("\x1b[?1002h\x1b[?1006h")
	defer fmt.Print("\x1b[?1002l\x1b[?1006l")

	fmt.Print("press keys — or move the mouse — to see what this terminal sends;" +
		" ctrl+c to stop\r\n\r\n")
	buf := make([]byte, 64)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			b := buf[:n]
			fmt.Printf("%-24s %s\r\n", quoteKeyBytes(b), nameForKeyBytes(b))
			if bytes.Contains(b, []byte{0x03}) {
				return nil
			}
		}
		if err != nil {
			return nil
		}
	}
}

// quoteKeyBytes renders a key press as hex and as the escape sequence a person
// would recognise it by.
func quoteKeyBytes(b []byte) string {
	var hex, pretty strings.Builder
	for _, c := range b {
		fmt.Fprintf(&hex, "%02x ", c)
		switch {
		case c == 0x1b:
			pretty.WriteString("ESC")
		case c < 0x20:
			fmt.Fprintf(&pretty, "^%c", c+0x40)
		case c == 0x7f:
			pretty.WriteString("^?")
		default:
			pretty.WriteByte(c)
		}
	}
	return strings.TrimSpace(hex.String()) + "  " + pretty.String()
}

// printableRest is every other printable ASCII character, tried after the
// lower-case letters.
const printableRest = " !\"#$%&'()*+,-./0123456789:;<=>?@" +
	"ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`{|}~"

// nameForKeyBytes says which key name swarm would give those bytes, so what a
// terminal sends can be compared with what a binding expects.
//
// KeyNames alone is not enough to answer that: most of what a keyboard sends
// is covered by the patterns rather than by a listed name — a bare character,
// ctrl+<char>, alt+<char>. Asking only the list made this report "no key name"
// for ^G, which is the default detach key on Windows and works perfectly. A
// measuring instrument that calls the working case unknown is worse than none.
func nameForKeyBytes(b []byte) string {
	if name, ok := mouseEventName(b); ok {
		return name
	}
	// The listed names first, so ^I is reported as tab rather than as ctrl+i:
	// both send it, and the one with a key of its own is the one meant.
	for _, name := range vterm.KeyNames() {
		if seq, err := vterm.KeySequence(name); err == nil && seq == string(b) {
			return name
		}
	}
	// The modified navigation keys, which are a pattern too: ctrl+left and its
	// family are built from the plain name and a parameter.
	for _, base := range []string{"up", "down", "right", "left", "home", "end",
		"pgup", "pagedown", "insert", "delete"} {
		for _, mods := range []string{"ctrl+", "shift+", "alt+",
			"ctrl+shift+", "ctrl+alt+", "shift+alt+", "ctrl+shift+alt+"} {
			if seq, err := vterm.KeySequence(mods + base); err == nil && seq == string(b) {
				return mods + base
			}
		}
	}
	// Lower case first: ctrl+a and ctrl+A send the same byte, and the lower one
	// is how a person writes it.
	for _, set := range []string{"abcdefghijklmnopqrstuvwxyz", printableRest} {
		for _, r := range set {
			for _, name := range []string{string(r), "ctrl+" + string(r), "alt+" + string(r)} {
				if seq, err := vterm.KeySequence(name); err == nil && seq == string(b) {
					return name
				}
			}
		}
	}
	return "(no key name sends these bytes)"
}

// mouseEventName describes an SGR mouse report — ESC[<b;x;y M for a press, m
// for a release — since "no key name sends these bytes" is a poor answer to
// someone asking why the wheel does nothing.
func mouseEventName(b []byte) (string, bool) {
	body, ok := strings.CutPrefix(string(b), "\x1b[<")
	if !ok || len(body) < 2 {
		return "", false
	}
	final := body[len(body)-1]
	if final != 'M' && final != 'm' {
		return "", false
	}
	parts := strings.Split(body[:len(body)-1], ";")
	if len(parts) != 3 {
		return "", false
	}
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", false
	}

	// The low two bits name the button; 4, 8 and 16 are shift, alt and ctrl;
	// 32 means the pointer moved with a button down; 64 is the wheel.
	var what string
	switch {
	case code&64 != 0:
		what = map[int]string{0: "wheel up", 1: "wheel down", 2: "wheel left", 3: "wheel right"}[code&3]
	case code&32 != 0:
		what = "drag"
	default:
		what = map[int]string{0: "left", 1: "middle", 2: "right"}[code&3]
	}
	if what == "" {
		what = fmt.Sprintf("button %d", code&3)
	}
	for bit, name := range map[int]string{4: "shift+", 8: "alt+", 16: "ctrl+"} {
		if code&bit != 0 {
			what = name + what
		}
	}
	action := "press"
	if final == 'm' {
		action = "release"
	}
	if code&64 != 0 {
		action = "" // a wheel notch has no release to distinguish it from
	}
	if action != "" {
		what += " " + action
	}
	return fmt.Sprintf("mouse %s at %s,%s", what, parts[1], parts[2]), true
}
