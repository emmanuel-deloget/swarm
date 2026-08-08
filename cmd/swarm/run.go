package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/config"
	"github.com/emmanuel-deloget/swarm/internal/event"
	"github.com/emmanuel-deloget/swarm/internal/hook"
	"github.com/emmanuel-deloget/swarm/internal/hub"
	"github.com/emmanuel-deloget/swarm/internal/ipc"
	"github.com/emmanuel-deloget/swarm/internal/ui"
	"github.com/emmanuel-deloget/swarm/internal/vterm"
	"github.com/emmanuel-deloget/swarm/internal/web"
)

func cmdRun(args []string) error {
	fs := newFlagSet("run")
	cfgPath := fs.String("c", "", "path to swarm.yaml (default: nearest one, searching upwards)")
	noTUI := fs.Bool("no-tui", false, "run headless: no terminal UI, drive it with the swarm CLI")
	noWeb := fs.Bool("no-web", false, "disable the web remote control")
	noHooks := fs.Bool("no-hooks", false, "disable the inbound webhook listener")
	webAddr := fs.String("web-addr", "", "override the web listen address")
	token := fs.String("web-token", "", "override the web token")
	noStart := fs.Bool("no-start", false, "create the agents but do not launch them")
	detachKey := fs.String("detach-key", "", "key that leaves an attached agent (default: detach_key in the config)")
	grace := fs.Duration("grace", 5*time.Second, "grace period given to agents on shutdown")
	_ = parseArgs(fs, args, -1)

	path := *cfgPath
	if path == "" {
		path = findConfig()
	}
	if path == "" {
		return fmt.Errorf("no swarm.yaml found; create one with `swarm init`")
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if *noWeb {
		cfg.Web.Enabled = false
	}
	if *noHooks {
		cfg.Hooks.Enabled = false
	}
	if *webAddr != "" {
		cfg.Web.Addr = *webAddr
		cfg.Web.Enabled = true
	}
	if *token != "" {
		cfg.Web.Token = *token
	}
	if *detachKey != "" {
		if err := vterm.CheckBindable(*detachKey); err != nil {
			return fmt.Errorf("-detach-key: %w (see `swarm keys -list`)", err)
		}
		cfg.DetachKey = *detachKey
	}

	if err := checkConfigAtStart(cfg); err != nil {
		return err
	}

	h, err := hub.New(hub.Options{Config: cfg, StateDir: cfg.StateDir, EventHistory: 2000})
	if err != nil {
		return err
	}

	srv, err := ipc.Listen(h)
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	if err := writeAgentGuide(h); err != nil {
		h.Log().Emit(event.KindError, "", "could not write the agent guide: "+err.Error())
	}

	quit := make(chan struct{})
	var closeOnce bool
	stop := func() {
		if !closeOnce {
			closeOnce = true
			close(quit)
		}
	}
	srv.OnShutdown = stop

	var webSrv *web.Server
	if cfg.Web.Enabled {
		tok := cfg.Web.Token
		if tok == "" {
			tok = hub.NewToken()
		}
		webSrv, err = web.New(h, web.Options{
			Addr:     cfg.Web.Addr,
			Token:    tok,
			ReadOnly: cfg.Web.ReadOnly,
			TLSCert:  cfg.Web.TLSCert,
			TLSKey:   cfg.Web.TLSKey,
		})
		if err != nil {
			return err
		}
		if err := webSrv.Start(); err != nil {
			return err
		}
		defer func() { _ = webSrv.Close() }()
		h.SetWebURL(webSrv.URL(), tok)
		h.Log().Emit(event.KindInfo, "", "web remote control on "+webSrv.URL()+"?t="+tok)
	}

	if !*noStart {
		go h.StartAll()
	}

	var hookSrv *hook.Server
	if cfg.Hooks.Enabled {
		var trace *hook.Log
		if cfg.HookLogEnabled() {
			path := filepath.Join(h.StateDir(), "logs", "webhooks.log")
			trace, err = hook.OpenLog(path)
			if err != nil {
				return err
			}
			defer func() { _ = trace.Close() }()
			h.Log().Emit(event.KindInfo, "", "webhook deliveries recorded in "+path)
		}
		hookSrv, err = hook.New(hook.Options{
			Addr:            cfg.Hooks.Addr,
			Token:           cfg.Hooks.Token,
			Secret:          cfg.Hooks.Secret,
			SignatureHeader: cfg.Hooks.SignatureHeader,
			From:            cfg.Hooks.From,
			Rules:           cfg.Hooks.Rules,
			Unmatched:       cfg.Hooks.Unmatched,
			MaxBody:         cfg.Hooks.MaxBody,
			Log:             h.Log(),
			Trace:           trace,
			Send: func(from, target, body string) error {
				_, err := h.Send(from, target, body, nil)
				return err
			},
		})
		if err != nil {
			return err
		}
		if err := hookSrv.Start(); err != nil {
			return err
		}
		defer func() { _ = hookSrv.Close() }()
		h.Log().Emit(event.KindInfo, "", fmt.Sprintf("webhooks on %s (%d rules)", hookSrv.URL(), len(cfg.Hooks.Rules)))
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	if *noTUI {
		fmt.Printf("swarm %q running\n", cfg.Session)
		fmt.Printf("  socket %s\n", srv.Path())
		if webSrv != nil {
			url, tok := h.WebURL()
			fmt.Printf("  web    %s?t=%s\n", url, tok)
		}
		fmt.Printf("  agents %s\n", strings.Join(h.Names(), " "))
		fmt.Println("drive it with `swarm ls`, `swarm inject`, `swarm attach`; stop it with `swarm shutdown`")
		select {
		case <-sigs:
		case <-quit:
		}
	} else {
		go func() {
			select {
			case <-sigs:
				stop()
			case <-quit:
			}
		}()
		if err := ui.Run(h, quit); err != nil {
			h.Shutdown(*grace)
			return err
		}
	}

	fmt.Println("stopping agents...")
	h.Shutdown(*grace)
	return nil
}

// writeAgentGuide drops a short protocol description in the state directory.
// Point your agents' instructions at it (or paste it in their prompt) and they
// can talk to each other without you relaying anything.
func writeAgentGuide(h *hub.Hub) error {
	cfg := h.Config()
	var b strings.Builder
	b.WriteString("# Talking to the swarm\n\n")
	b.WriteString("You are running inside `swarm`, next to other agents. The `swarm`\n")
	b.WriteString("command is on your PATH and already knows who you are:\n\n")
	b.WriteString("| variable | meaning |\n|---|---|\n")
	b.WriteString("| `$SWARM_AGENT` | your name |\n")
	b.WriteString("| `$SWARM_ROLE` | your role |\n")
	b.WriteString("| `$SWARM_PEERS` | the other agents, comma separated |\n")
	b.WriteString("| `$SWARM_SHARED` | a directory every agent can read and write |\n\n")
	b.WriteString("## Commands\n\n")
	b.WriteString("```sh\n")
	b.WriteString("swarm ls                        # who is here and what they are doing\n")
	b.WriteString("swarm send <agent> \"message\"    # write to one agent\n")
	b.WriteString("swarm send @review \"message\"    # write to a whole group\n")
	b.WriteString("swarm broadcast \"message\"       # write to everyone\n")
	b.WriteString("swarm inbox                     # read the messages addressed to you\n")
	b.WriteString("swarm inbox -wait 30s           # ... or wait for one\n")
	b.WriteString("swarm send <agent> -file diff.patch \"have a look\"   # attach a file\n")
	b.WriteString("swarm stage <file>              # copy a file where everyone can read it\n")
	b.WriteString("```\n\n")
	b.WriteString("Messages you receive appear either directly in your prompt (push) or\n")
	b.WriteString("when you run `swarm inbox` (pull), depending on how you are configured.\n\n")
	b.WriteString("## This fleet\n\n")
	b.WriteString("| agent | role | delivery |\n|---|---|---|\n")
	for i := range cfg.Agents {
		a := &cfg.Agents[i]
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", a.Name, dash(a.Role), a.DeliveryMode)
	}
	if len(cfg.Groups) > 0 {
		b.WriteString("\nGroups: ")
		var groups []string
		for name := range cfg.Groups {
			groups = append(groups, "`@"+name+"`")
		}
		b.WriteString(strings.Join(groups, ", "))
		b.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(h.StateDir(), "AGENTS.md"), []byte(b.String()), 0o644)
}
