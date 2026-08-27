package ui

import (
	"strings"
	"testing"

	"github.com/emmanuel-deloget/swarm/internal/bus"
)

func fleetShape() ([]string, map[string][]string, bus.Stats) {
	names := []string{"chair", "devone", "devtwo"}
	reach := map[string][]string{
		"chair":  {"devone", "devtwo"},
		"devone": {"chair"},
		"devtwo": {"chair"},
	}
	st := bus.Stats{Pairs: []bus.Pair{
		{From: "chair", To: "devone", Count: 20},
		{From: "devone", To: "chair", Count: 1},
	}}
	return names, reach, st
}

// TestTheMatrixSaysWhatIsForbiddenAndWhatIsSilent: the two are not the same and
// look the same in every other view. can_send saying no is a decision somebody
// made; silence is a fleet that has nothing to say to itself.
func TestTheMatrixSaysWhatIsForbiddenAndWhatIsSilent(t *testing.T) {
	names, reach, st := fleetShape()
	out := strings.Join(matrixLines(names, reach, st, 80), "\n")

	// devone may not reach devtwo, and does not.
	if !strings.Contains(out, glyphBarred) {
		t.Error("nothing is marked as forbidden, though can_send forbids it")
	}
	// chair may reach devtwo and has not, this window.
	if !strings.Contains(out, glyphSilent) {
		t.Error("nothing is marked as allowed and silent")
	}
	if !strings.Contains(out, "█") {
		t.Error("the busiest pair is not drawn at full")
	}
}

// TestEveryRowLinesUpWithItsHeading: the abbreviations are not all the same
// length, and a header that assumes they are walks out of line with the grid
// under it — which is exactly what the first version did.
func TestEveryRowLinesUpWithItsHeading(t *testing.T) {
	names := []string{"myself", "devone", "devtwo", "devthree"}
	reach := map[string][]string{}
	lines := matrixLines(names, reach, bus.Stats{}, 100)

	// The heading row and every agent row, measured without their colours.
	width := len([]rune(stripANSI(lines[1])))
	for i := 2; i < 2+len(names); i++ {
		if got := len([]rune(stripANSI(lines[i]))); got != width {
			t.Errorf("row %d is %d wide, the heading is %d:\n%s\n%s",
				i-1, got, width, stripANSI(lines[1]), stripANSI(lines[i]))
		}
	}
}

// TestAnAgentIsNotItsOwnCorrespondent: the diagonal is not a relationship, and
// drawing it as a silent one would say a fleet is quieter than it is.
func TestAnAgentIsNotItsOwnCorrespondent(t *testing.T) {
	names, reach, st := fleetShape()
	lines := matrixLines(names, reach, st, 80)
	for i, name := range names {
		row := stripANSI(lines[2+i])
		if !strings.HasPrefix(row, name) {
			t.Fatalf("row %d is not %s: %q", i, name, row)
		}
	}
}
