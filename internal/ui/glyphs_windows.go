//go:build windows

package ui

// Words instead of symbols, because a Windows console may have no glyph for
// them: ↵ (U+21B5) and ⌨ (U+2328) are missing from the raster fonts the older
// console still offers, and a key drawn as an empty rectangle names nothing.
// Windows Terminal has them, but the bar has to read on both.
const (
	glyphEnter    = "enter"
	glyphKeyboard = ""
)
