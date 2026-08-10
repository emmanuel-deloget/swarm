package agent

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

// A stand-in for the shell these tests used to need.
//
// `sh -c "exit 1"` describes a child in the terms of a system that has one
// shell; Windows has neither that shell nor those commands, so every test
// using one was a test that could never run there. Running the test binary
// again is how the standard library spawns a helper process: no shell to
// find, no quoting to get wrong, and the behaviour is Go.

const probeFlag = "-swarm-probe"

// TestMain runs the probe when it is asked for, and the tests otherwise. The
// flag is unambiguous: go test only ever passes -test.* here.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == probeFlag {
		probeMain(os.Args[2:])
		return
	}
	os.Exit(m.Run())
}

// probe builds a command running the test binary as a child, from verbs
// carried out in order:
//
//	exit <code>          end with that status
//	sleep <seconds>      stay up
//	echo                 copy input to output until it ends, as cat did
//	write <file> <text>  write text, with $VARIABLES expanded as a shell would
//	fail <text>          complain on stderr and end with 1
func probe(t *testing.T, verbs ...string) []string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return append([]string{self, probeFlag}, verbs...)
}

func probeMain(args []string) {
	next := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	for i := 0; i < len(args); {
		switch args[i] {
		case "exit":
			code, _ := strconv.Atoi(next(i + 1))
			os.Exit(code)
		case "sleep":
			secs, _ := strconv.ParseFloat(next(i+1), 64)
			time.Sleep(time.Duration(secs * float64(time.Second)))
			i += 2
		case "echo":
			_, _ = io.Copy(os.Stdout, os.Stdin)
			i++
		case "write":
			// os.ExpandEnv is what makes "$SWARM_AGENT $PORT" mean here what
			// it meant to the shell: the point of that test is the
			// environment a hook is given.
			_ = os.WriteFile(next(i+1), []byte(os.ExpandEnv(next(i+2))+"\n"), 0o644)
			i += 3
		case "fail":
			fmt.Fprintln(os.Stderr, next(i+1))
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "probe: unknown verb %q\n", args[i])
			os.Exit(2)
		}
	}
}
