package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/emmanuel-deloget/swarm/internal/hook"
)

const hookUsage = `swarm hook — inbound webhooks

  swarm hook test <payload.json>   show what the rules would send, without sending
  swarm hook post <payload.json>   POST the payload to the running listener
  swarm hook sign <payload.json>   print the signature the listener expects

  -H "Name: value"                 add a header (repeatable); the event type
                                   usually travels in one, and rules can match it

"-" reads the payload from standard input.
`

func cmdHook(args []string) error {
	if len(args) == 0 {
		fmt.Print(hookUsage)
		return nil
	}
	switch args[0] {
	case "test", "dry-run":
		return cmdHookTest(args[1:])
	case "post", "send":
		return cmdHookPost(args[1:])
	case "sign":
		return cmdHookSign(args[1:])
	case "help", "-h", "--help":
		fmt.Print(hookUsage)
		return nil
	default:
		return fmt.Errorf("unknown hook command %q\n\n%s", args[0], hookUsage)
	}
}

// headerFlag collects repeated -H options.
type headerFlag []string

func (h *headerFlag) String() string { return strings.Join(*h, ", ") }

func (h *headerFlag) Set(v string) error {
	if !strings.Contains(v, ":") {
		return fmt.Errorf("a header looks like \"Name: value\", got %q", v)
	}
	*h = append(*h, v)
	return nil
}

func (h headerFlag) header() (http.Header, error) {
	out := http.Header{}
	for _, raw := range h {
		name, value, _ := strings.Cut(raw, ":")
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("a header needs a name, got %q", raw)
		}
		out.Set(name, strings.TrimSpace(value))
	}
	return out, nil
}

// cmdHookTest applies the configured rules to a saved payload and prints what
// would be sent. It goes through hook.Apply, the same function the listener
// uses, so this is a simulation and not a second implementation that could
// drift — and it needs no running swarm, no open port and no real pull request.
func cmdHookTest(args []string) error {
	fs := newFlagSet("hook test")
	cfgPath := fs.String("c", "", "path to swarm.yaml (default: nearest one, searching upwards)")
	verbose := fs.Bool("v", false, "also show the rules that did not match, and their conditions")
	var headers headerFlag
	fs.Var(&headers, "H", "add a header, \"Name: value\" (repeatable)")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: swarm hook test [-H \"Name: value\"] <payload.json>")
	}
	cfg, err := loadConfig(configPath(*cfgPath))
	if err != nil {
		return err
	}
	payload, err := readPayloadWith(rest[0], headers)
	if err != nil {
		return err
	}

	deliveries, verdicts := hook.ApplyVerbose(cfg.Hooks.Rules, cfg.Hooks.Unmatched, payload)
	if *verbose {
		for _, v := range verdicts {
			mark := "✗"
			if v.Matched {
				mark = "✓"
			}
			fmt.Printf("%s %-30s %s\n", mark, v.Rule, v.Why)
		}
		fmt.Println()
	}
	if len(deliveries) == 0 {
		fmt.Println("no rule matched, and no unmatched rule is configured: nothing would be sent")
		return nil
	}
	for _, d := range deliveries {
		names, err := cfg.Resolve(d.To)
		if err != nil {
			return err
		}
		fmt.Printf("%s → %s (%s)\n", d.Rule, d.To, strings.Join(names, " "))
		for _, line := range strings.Split(d.Body, "\n") {
			fmt.Printf("    %s\n", line)
		}
		if len(d.Missing) > 0 {
			fmt.Printf("    ! no value in the payload for: %s\n", strings.Join(d.Missing, ", "))
		}
	}
	return nil
}

// cmdHookPost sends a payload to the listener, signing it the way the sender
// would, so a rule can be tried end-to-end without reaching for curl and
// reimplementing the HMAC by hand.
func cmdHookPost(args []string) error {
	fs := newFlagSet("hook post")
	cfgPath := fs.String("c", "", "path to swarm.yaml (default: nearest one, searching upwards)")
	url := fs.String("url", "", "override the listener URL")
	var headers headerFlag
	fs.Var(&headers, "H", "add a header, \"Name: value\" (repeatable)")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: swarm hook post [-H \"Name: value\"] <payload.json>")
	}
	cfg, err := loadConfig(configPath(*cfgPath))
	if err != nil {
		return err
	}
	raw, err := readPayload(rest[0])
	if err != nil {
		return err
	}
	extra, err := headers.header()
	if err != nil {
		return err
	}

	target := *url
	if target == "" {
		target = "http://" + cfg.Hooks.Addr + "/"
	}
	req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for name, values := range extra {
		req.Header.Set(name, values[0])
	}
	if cfg.Hooks.Token != "" {
		req.Header.Set("X-Swarm-Token", cfg.Hooks.Token)
	}
	if cfg.Hooks.Secret != "" {
		req.Header.Set(cfg.Hooks.SignatureHeader, "sha256="+hook.Signature(cfg.Hooks.Secret, raw))
	}

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))

	var pretty bytes.Buffer
	if json.Indent(&pretty, body, "", "  ") == nil {
		body = pretty.Bytes()
	}
	fmt.Printf("%s\n%s\n", res.Status, strings.TrimSpace(string(body)))
	if res.StatusCode >= 400 {
		return fmt.Errorf("the listener refused the payload")
	}
	return nil
}

// cmdHookSign prints the digest swarm would compute for a payload, in both
// encodings. Compare it with the header of a delivery your sender actually
// made: whichever line matches tells you the encoding, and if neither does the
// secret is wrong — which is otherwise indistinguishable from a wrong format.
func cmdHookSign(args []string) error {
	fs := newFlagSet("hook sign")
	cfgPath := fs.String("c", "", "path to swarm.yaml (default: nearest one, searching upwards)")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: swarm hook sign <payload.json>")
	}
	cfg, err := loadConfig(configPath(*cfgPath))
	if err != nil {
		return err
	}
	if cfg.Hooks.Secret == "" {
		return fmt.Errorf("no secret configured: set hooks.secret_path, hooks.secret_env or hooks.secret")
	}
	raw, err := readPayload(rest[0])
	if err != nil {
		return err
	}
	fmt.Printf("header  %s\n", cfg.Hooks.SignatureHeader)
	fmt.Printf("hex     %s\n", hook.Signature(cfg.Hooks.Secret, raw))
	fmt.Printf("base64  %s\n", hook.SignatureBase64(cfg.Hooks.Secret, raw))
	fmt.Println("\nswarm accepts either, with or without a \"sha256=\" label.")
	return nil
}

func readPayload(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func readPayloadWith(path string, headers headerFlag) (hook.Payload, error) {
	raw, err := readPayload(path)
	if err != nil {
		return hook.Payload{}, err
	}
	h, err := headers.header()
	if err != nil {
		return hook.Payload{}, err
	}
	p, err := hook.NewPayload(h, raw)
	if err != nil {
		return hook.Payload{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// configPath falls back to the nearest config file when none was given.
func configPath(flag string) string {
	if flag != "" {
		return flag
	}
	return findConfig()
}
