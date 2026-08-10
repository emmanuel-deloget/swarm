// Package probe is the child process the tests drive instead of a shell.
//
// Tests described their agents as `sh -c "printf ready; ..."` — the terms of a
// system that has one shell. Windows has neither that shell nor those
// commands, so every test using one was a test that could never run there.
// Running the test binary again is how the standard library spawns a helper:
// no shell to find, no quoting to get wrong, and the behaviour is Go.
//
// It lives outside the test files because three packages need the same child,
// and a helper copied three times is a helper that drifts.
package probe

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/charmbracelet/x/term"
)

// Flag marks a run as the probe rather than the test suite. go test only ever
// passes -test.* as its first argument, so there is no ambiguity.
const Flag = "-swarm-probe"

// Argv builds the command line for a probe running the given binary.
func Argv(self string, verbs ...string) []string {
	return append([]string{self, Flag}, verbs...)
}

// Run carries out the verbs if this process was started as a probe, and
// reports whether it was. Call it first thing in TestMain:
//
//	func TestMain(m *testing.M) {
//		if probe.Run(os.Args[1:]) {
//			return
//		}
//		os.Exit(m.Run())
//	}
//
// The verbs run in order:
//
//	print <text>          write the text and a newline
//	lines <prefix>        echo every line read, behind that prefix
//	echo                  copy input to output until it ends, as cat did
//	numbered <n>          write line-01 .. line-NN
//	tick <seconds> <text> write the text at that interval, for ever
//	size <seconds>        write size=<rows> <cols> at that interval, for ever
//	catv                  show the bytes arriving, control characters visible
//	sendlines <target>    run `swarm send <target> <line>` for every line read
//	sleep <seconds>       stay up, fractions allowed
//	write <file> <text>   write text, with $VARIABLES expanded as a shell would
//	fail <text>           complain on stderr and end with 1
//	exit <code>           end with that status
func Run(args []string) bool {
	if len(args) == 0 || args[0] != Flag {
		return false
	}
	run(args[1:])
	return true
}

func run(args []string) {
	at := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	for i := 0; i < len(args); {
		switch args[i] {
		case "print":
			fmt.Println(at(i + 1))
			i += 2
		case "lines":
			prefix := at(i + 1)
			sc := bufio.NewScanner(os.Stdin)
			for sc.Scan() {
				fmt.Println(prefix + sc.Text())
			}
			i += 2
		case "echo":
			_, _ = io.Copy(os.Stdout, os.Stdin)
			i++
		case "numbered":
			n, _ := strconv.Atoi(at(i + 1))
			for l := 1; l <= n; l++ {
				fmt.Printf("line-%02d\n", l)
			}
			i += 2
		case "tick":
			// An agent that keeps printing without being asked anything: what
			// separates "quiet because it is waiting" from "quiet because it
			// stopped" cannot be measured on something that never speaks.
			secs, _ := strconv.ParseFloat(at(i+1), 64)
			text := at(i + 2)
			for {
				time.Sleep(time.Duration(secs * float64(time.Second)))
				fmt.Print(text)
			}
		case "size":
			// What `stty size` answered: the geometry the child believes it
			// has, which is the only way to check that a resize reached it.
			secs, _ := strconv.ParseFloat(at(i+1), 64)
			for {
				w, h, err := term.GetSize(os.Stdout.Fd())
				if err == nil {
					fmt.Printf("size=%d %d\n", h, w)
				}
				time.Sleep(time.Duration(secs * float64(time.Second)))
			}
		case "catv":
			catv()
			i++
		case "sendlines":
			target := at(i + 1)
			sc := bufio.NewScanner(os.Stdin)
			for sc.Scan() {
				out, err := exec.Command("swarm", "send", target, sc.Text()).CombinedOutput()
				if err != nil {
					fmt.Printf("send failed: %v: %s\n", err, out)
				}
			}
			i += 2
		case "sleep":
			secs, _ := strconv.ParseFloat(at(i+1), 64)
			time.Sleep(time.Duration(secs * float64(time.Second)))
			i += 2
		case "write":
			// os.ExpandEnv is what makes "$SWARM_AGENT $PORT" mean here what it
			// meant to the shell: the test using it is about the environment a
			// hook is given, so the expansion is the thing under test.
			_ = os.WriteFile(at(i+1), []byte(os.ExpandEnv(at(i+2))+"\n"), 0o644)
			i += 3
		case "fail":
			fmt.Fprintln(os.Stderr, at(i+1))
			os.Exit(1)
		case "exit":
			code, _ := strconv.Atoi(at(i + 1))
			os.Exit(code)
		default:
			fmt.Fprintf(os.Stderr, "probe: unknown verb %q\n", args[i])
			os.Exit(2)
		}
	}
}

// catv shows what arrives on the input, control characters made visible, the
// way `cat -v` did — and, like the `stty -isig` that preceded it in these
// tests, without letting the terminal turn any of those bytes into a signal.
//
// Raw mode is what buys that: it is also the only portable way to ask for it,
// since termios flags do not exist on Windows. The cost is that a carriage
// return arrives as ^M rather than ending a line, which is exactly what a
// terminal driver would otherwise have translated.
func catv() {
	if old, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
		defer func() { _ = term.Restore(os.Stdin.Fd(), old) }()
	}
	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		for _, b := range buf[:n] {
			switch {
			case b == '\n' || b == '\t':
				fmt.Printf("%c", b)
			case b < 0x20:
				fmt.Printf("^%c", b+0x40)
			case b == 0x7f:
				fmt.Print("^?")
			default:
				fmt.Printf("%c", b)
			}
		}
		if err != nil {
			return
		}
	}
}
