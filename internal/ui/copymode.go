package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// Copy mode exists because panes are drawn side by side in one terminal
// buffer. A row of that buffer spans every pane, so dragging the mouse across
// one pane's output selects the card borders and the neighbouring pane's text
// along with it — every multiplexer has to solve this, and tm had not. Here
// the rows come from the emulator's own grid, so a copy is that pane's text
// and nothing else.
//
// Selection is by whole lines. A line is the unit an error message, a command
// and a path all arrive in, and it sidesteps the column arithmetic that wide
// runes and grapheme clusters would otherwise demand of every cursor move.

// copyMark tells the pane header that its rows are frozen and a selection is
// being made, so the screen standing still is explained.
const copyMark = "COPY"

// copyMode is a selection being made in one terminal's pane.
type copyMode struct {
	term   *workspace.Terminal
	lines  []string // the pane's rows when copy mode opened; a frozen copy, so the program cannot move the text under the cursor
	cursor int      // row the cursor is on
	anchor int      // the other end of the selection; -1 while only the cursor row is selected
	copied string   // what the last y copied, for the status bar to report
}

// newCopyMode freezes what the pane is showing and puts the cursor on its last
// non-empty row, which is where a program's most recent output is.
func newCopyMode(t *workspace.Terminal, offset int) *copyMode {
	c := &copyMode{term: t, lines: t.Session.Lines(offset), anchor: -1}
	for i := len(c.lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(c.lines[i]) != "" {
			c.cursor = i
			break
		}
	}
	return c
}

// move walks the cursor, clamping at both ends.
func (c *copyMode) move(delta int) {
	if len(c.lines) == 0 {
		return
	}
	c.cursor = min(max(c.cursor+delta, 0), len(c.lines)-1)
}

// mark starts a selection at the cursor, or drops one already in progress.
func (c *copyMode) mark() {
	if c.anchor >= 0 {
		c.anchor = -1
		return
	}
	c.anchor = c.cursor
}

// span is the half-open range of rows the selection covers. With no anchor it
// is the cursor's row alone, so y always copies something.
func (c *copyMode) span() (from, to int) {
	from, to = c.cursor, c.cursor
	if c.anchor >= 0 {
		from, to = min(c.anchor, c.cursor), max(c.anchor, c.cursor)
	}
	return from, to + 1
}

// text is the selected rows, trailing blank ones dropped: a selection dragged
// past the end of the output should not paste a run of empty lines.
func (c *copyMode) text() string {
	from, to := c.span()
	rows := append([]string(nil), c.lines[from:min(to, len(c.lines))]...)
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	return strings.Join(rows, "\n")
}

// selected reports whether row i of the pane is inside the selection.
func (c *copyMode) selected(i int) bool {
	from, to := c.span()
	return i >= from && i < to
}

// copy puts the selection on the system clipboard and reports what it sent.
// It returns the empty string when there was nothing to copy.
func (c *copyMode) copy() string {
	text := c.text()
	if text == "" {
		return ""
	}
	writeClipboard(text)
	lines := strings.Count(text, "\n") + 1
	c.copied = fmt.Sprintf("copied %d line%s", lines, plural(lines))
	return c.copied
}

// writeClipboard hands text to the terminal with OSC 52. tm asks the terminal
// to do it rather than calling a clipboard tool, because that is the only way
// that works over SSH: the escape travels back down the connection the user is
// typing over and lands in the clipboard of the machine in front of them. The
// cost is that terminals may refuse it — many ship with it disabled — and
// there is no reply to check, so tm cannot report failure.
func writeClipboard(text string) {
	fmt.Fprint(clipboard, ansi.SetSystemClipboard(text))
}

// clipboard is where the OSC 52 sequence goes: the terminal tm is drawn on.
// A variable so a test can read what was sent without a terminal to send it to.
var clipboard io.Writer = os.Stdout

// view draws the frozen pane with the selection marked, in place of the live
// screen. The rows are the ones copy mode opened on, so what is highlighted is
// exactly what y will copy.
func (c *copyMode) view(width, height int) []string {
	lines := make([]string, 0, height)
	// Keep the cursor on screen by scrolling the frozen rows under it.
	top := 0
	if height > 0 && c.cursor >= height {
		top = c.cursor - height + 1
	}
	for i := top; i < len(c.lines) && len(lines) < height; i++ {
		style := textStyle
		switch {
		case i == c.cursor:
			style = copyCursorStyle
		case c.selected(i):
			style = copySelectedStyle
		}
		lines = append(lines, pad(style.Render(ansi.Truncate(plainText(c.lines[i]), width, "")), width))
	}
	return lines
}
