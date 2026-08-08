package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Severity says what to do about a finding.
type Severity string

const (
	// Warn: the config still works, but it says something that no longer means
	// what it looks like it means. Starting is fine.
	Warn Severity = "warning"
	// Blocking: the config cannot be interpreted safely. Starting is not.
	Blocking Severity = "blocking"
)

// Finding is something a config says that no longer means what it says.
//
// swarm's defaults change as keys are added, and a value written when an older
// default was in force can quietly stop meaning what its author intended —
// without ever becoming an error. This is how those are found and named.
type Finding struct {
	Check    string
	Severity Severity
	// Problem is one sentence: what the config says, and why that is now
	// misleading.
	Problem string
	// Fix says what would change in the file.
	Fix string
	// apply rewrites the file. Every fix is surgical on the text rather than a
	// round-trip through the parser: a config is mostly comments, and
	// re-serialising it would throw them away or reformat what is left.
	apply func(src []byte) []byte
}

// Check reports what is stale about a loaded config.
func Check(c *Config) ([]Finding, error) {
	if c.path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	// What the file literally says, before any default was applied — the only
	// way to tell "written down" from "inherited".
	written, err := writtenKeys(raw)
	if err != nil {
		return nil, err
	}

	var out []Finding
	if f, ok := checkSharedRestatesDefault(c, written); ok {
		out = append(out, f)
	}
	return out, nil
}

// checkSharedRestatesDefault: `shared` used to default to ".swarm/shared" with
// the path spelled out, so every config `swarm init` wrote before state_dir
// existed carries that value. It is now the default *derived from* state_dir —
// so a config that restates it will not follow the day state_dir changes, and
// the staged files will stay behind in a directory nothing else uses.
func checkSharedRestatesDefault(c *Config, written map[string]string) (Finding, bool) {
	shared, ok := written["shared"]
	if !ok {
		return Finding{}, false
	}
	base := c.Dir()
	if resolve(base, shared) != filepath.Join(c.StateDir, "shared") {
		return Finding{}, false
	}
	return Finding{
		Check:    "shared-restates-default",
		Severity: Warn,
		Problem: fmt.Sprintf("`shared: %s` restates the default, which now follows `state_dir`. "+
			"Left as it is, changing `state_dir` would move everything except the staged files.", shared),
		Fix:   "remove the `shared` line, so it follows `state_dir`",
		apply: func(src []byte) []byte { return dropTopLevelKey(src, "shared") },
	}, true
}

// Apply writes the fix to the config file.
func (f Finding) Apply(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, f.apply(src), st.Mode().Perm())
}

// writtenKeys returns the top-level scalar keys the file actually contains, with
// their values. A real parse would give the same thing plus every default, which
// is exactly what has to be told apart here.
func writtenKeys(src []byte) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(string(src), "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '-' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.ContainsAny(key, " \t") {
			continue
		}
		value = strings.TrimSpace(value)
		if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		value = strings.Trim(value, `"'`)
		if value != "" {
			out[key] = value
		}
	}
	return out, nil
}

// dropTopLevelKey removes a `key: value` line, along with the comment block
// directly above it when that block is clearly attached to the key — that is,
// when a blank line separates it from whatever comes before. Leaving a comment
// describing a setting that is no longer there is worse than the setting.
func dropTopLevelKey(src []byte, key string) []byte {
	lines := strings.Split(string(src), "\n")
	at := -1
	for i, line := range lines {
		if strings.HasPrefix(line, key+":") {
			at = i
			break
		}
	}
	if at < 0 {
		return src
	}

	from := at
	for from > 0 && strings.HasPrefix(strings.TrimSpace(lines[from-1]), "#") {
		from--
	}
	// Only take the comments if they stand on their own, separated from what is
	// above by a blank line (or by the start of the file).
	if from > 0 && strings.TrimSpace(lines[from-1]) != "" {
		from = at
	}
	// And swallow the blank line that followed the key, so no gap is left.
	to := at + 1
	if to < len(lines) && strings.TrimSpace(lines[to]) == "" {
		to++
	}

	return []byte(strings.Join(append(lines[:from:from], lines[to:]...), "\n"))
}
