package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/emmanuel-deloget/swarm/internal/config"
)

func cmdConfig(args []string) error {
	if len(args) == 0 || args[0] == "check" {
		return cmdConfigCheck(argsFrom2(args))
	}
	return fmt.Errorf("usage: swarm config check [-fix]")
}

func argsFrom2(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	return args[1:]
}

func cmdConfigCheck(args []string) error {
	fs := newFlagSet("config check")
	cfgPath := fs.String("c", "", "path to swarm.yaml (default: nearest one, searching upwards)")
	fix := fs.Bool("fix", false, "apply the fixes instead of only reporting them")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}
	cfg, err := loadConfig(configPath(*cfgPath))
	if err != nil {
		return err
	}
	findings, err := config.Check(cfg)
	if err != nil {
		return err
	}
	if len(findings) == 0 {
		fmt.Printf("%s is up to date\n", cfg.Path())
		return nil
	}
	for _, f := range findings {
		fmt.Printf("%s: %s\n  %s\n  fix: %s\n", f.Severity, f.Check, f.Problem, f.Fix)
		if *fix && f.Fixable() {
			if err := f.Apply(cfg.Path()); err != nil {
				return err
			}
			fmt.Println("  applied")
		}
	}
	if !*fix {
		fmt.Printf("\nrun `swarm config check -fix` to apply %s\n", plural(len(findings), "this fix", "these fixes"))
	}
	return nil
}

// checkConfigAtStart reports a stale config before the fleet starts, and offers
// to fix it when somebody is there to answer. A finding that only warns never
// stops a start: swarm has to come up under systemd and in scripts, where there
// is nobody to ask and nothing was broken in the first place.
func checkConfigAtStart(cfg *config.Config) error {
	findings, err := config.Check(cfg)
	if err != nil || len(findings) == 0 {
		return nil //nolint:nilerr // a diagnostic that cannot run must not stop a start
	}

	interactive := term.IsTerminal(os.Stdin.Fd())
	for _, f := range findings {
		fmt.Fprintf(os.Stderr, "swarm: %s in %s\n  %s\n", f.Severity, cfg.Path(), f.Problem)

		if interactive && f.Fixable() && confirm(fmt.Sprintf("  %s?", f.Fix)) {
			if err := f.Apply(cfg.Path()); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "  done — reload with the next start")
			continue
		}
		if f.Severity == config.Blocking {
			return fmt.Errorf("%s cannot be read safely; fix it with `swarm config check -fix`", cfg.Path())
		}
		fmt.Fprintln(os.Stderr, "  left as it is (`swarm config check -fix` when you want to)")
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
