package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A recipe nobody can load is a recipe that teaches the wrong thing. Every
// swarm.yaml under examples/ goes through the real loader here, so a key that
// is renamed, a target that stops existing or a regexp that stops compiling
// breaks the build instead of waiting for a reader to hit it.
//
// This is the same bargain as the licence notices and the list of environment
// variables: the document is checked by the thing it describes.
func TestEveryExampleLoads(t *testing.T) {
	dirs, err := filepath.Glob("../../examples/*/swarm.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) < 5 {
		t.Fatalf("found %d examples; has the directory moved?", len(dirs))
	}

	for _, path := range dirs {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			// A recipe that names an environment variable for its secret needs
			// one to exist: the loader reads it rather than trusting the name.
			// Setting them here is also the list of what a recipe expects.
			for _, name := range secretEnvNames(t, path) {
				t.Setenv(name, "not-the-real-secret")
			}
			if _, err := Load(path); err != nil {
				t.Errorf("%s does not load: %v", path, err)
			}
		})
	}
}

var secretEnvLine = regexp.MustCompile(`(?m)^\s*secret_env:\s*(\S+)`)

func secretEnvNames(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range secretEnvLine.FindAllStringSubmatch(string(body), -1) {
		out = append(out, strings.Trim(m[1], `"'`))
	}
	return out
}
