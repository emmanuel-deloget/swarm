package config

import (
	"strings"
	"testing"
	"time"
)

// The memory's settings are three-valued in the way that matters: absent takes
// a default, an explicit zero means something, and a small number is a mistake
// worth naming rather than accepting.

const memoryBase = `
web: {enabled: false}
agents:
  - {name: alpha, command: [cat]}
`

func TestAnAbsentMemoryBlockTakesTheDefaults(t *testing.T) {
	c, err := loadYAML(t, memoryBase)
	if err != nil {
		t.Fatal(err)
	}
	if c.Memory.Entries() != 50 || c.Memory.Chars != 200 {
		t.Errorf("the defaults are %d entries of %d characters", c.Memory.Entries(), c.Memory.Chars)
	}
	if c.Memory.TTL != 0 {
		t.Errorf("ttl defaults to %s, want forever", c.Memory.TTL)
	}
}

// TestATTLIsADurationAndNotANumber: yaml reads a bare 0 as an integer, which a
// duration will not take. The starter file says 0s for that reason, and this
// is what would notice if it stopped.
func TestATTLIsADurationAndNotANumber(t *testing.T) {
	c, err := loadYAML(t, memoryBase+`
memory:
  ttl: 12h
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Memory.TTL != 12*time.Hour {
		t.Errorf("ttl read as %s", c.Memory.TTL)
	}
	if _, err := loadYAML(t, memoryBase+"memory:\n  ttl: 0s\n"); err != nil {
		t.Errorf("an explicit forever was refused: %v", err)
	}
}

// TestATTLTooShortToReadIsRefused. An entry is written to be read later, and a
// fleet that set thirty seconds meant something else.
func TestATTLTooShortToReadIsRefused(t *testing.T) {
	_, err := loadYAML(t, memoryBase+`
memory:
  ttl: 30s
`)
	if err == nil {
		t.Fatal("a ttl of thirty seconds was accepted")
	}
	if !strings.Contains(err.Error(), "0 keeps entries forever") {
		t.Errorf("the refusal does not say what to write instead: %v", err)
	}
	if _, err := loadYAML(t, memoryBase+"memory:\n  ttl: -1h\n"); err == nil {
		t.Error("a negative ttl was accepted")
	}
}

// TestZeroEntriesIsOffAndAbsentIsNot: an int cannot tell the two apart, which
// is why Max is a pointer.
func TestZeroEntriesIsOffAndAbsentIsNot(t *testing.T) {
	c, err := loadYAML(t, memoryBase+`
memory:
  max: 0
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Memory.Entries() != 0 {
		t.Errorf("an explicit 0 gave %d entries", c.Memory.Entries())
	}
}
