package ui

import (
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// Dragging the mouse over a pane selects text in that pane, and letting go
// copies it. tm does this itself because panes are drawn side by side in one
// terminal buffer: a row of that buffer spans every pane, so the host
// terminal's own selection (shift+drag, which tm never sees) runs through the
// card borders and into the neighbouring pane. Here the selection is clamped
// to the pane the drag started in, and the text is read from that pane's own
// rows (plainRows), so a copy is that pane's text and nothing else.
//
// The pane is frozen from the press until the selection is dismissed (the
// next key or click), so a program that keeps printing cannot move text out
// from under the pointer, and what is highlighted is exactly what was copied.

// copyMark tells the pane header its screen is frozen under a selection.
const copyMark = "COPY"

// cell is a position inside a pane's screen, in cells from its top-left.
type cell struct{ x, y int }

// before reports whether c comes before d in reading order.
func (c cell) before(d cell) bool { return c.y < d.y || (c.y == d.y && c.x < d.x) }

// selection is a mouse selection in one terminal's pane.
type selection struct {
	term       *workspace.Terminal
	screen     []string // the pane's rendered rows at the press, drawn while the selection lasts
	lines      []string // the same rows as plain text (plainRows), which is what is copied
	start, end cell     // where the drag began and where the pointer is now
	dragging   bool     // the button is still down
	moved      bool     // the pointer left the cell it was pressed on: a click is not a selection
}

// span is the selection in reading order, both ends inclusive.
func (s *selection) span() (from, to cell) {
	if s.end.before(s.start) {
		return s.end, s.start
	}
	return s.start, s.end
}

// cols returns the half-open range of columns selected on row y, in a row
// width cells wide, and whether row y is selected at all.
func (s *selection) cols(y, width int) (left, right int, ok bool) {
	from, to := s.span()
	if y < from.y || y > to.y {
		return 0, 0, false
	}
	left, right = 0, width
	if y == from.y {
		left = from.x
	}
	if y == to.y {
		right = to.x + 1
	}
	return left, max(right, left), true
}

// text is the selected text: each row's trailing blanks dropped, as a
// terminal's own selection does, and blank rows at the end dropped so a drag
// past the last output does not paste a run of empty lines.
func (s *selection) text() string {
	var rows []string
	for y, line := range s.lines {
		if l, r, ok := s.cols(y, ansi.StringWidth(line)); ok {
			rows = append(rows, strings.TrimRight(ansi.Cut(line, l, r), " "))
		}
	}
	for len(rows) > 0 && rows[len(rows)-1] == "" {
		rows = rows[:len(rows)-1]
	}
	return strings.Join(rows, "\n")
}

// view draws the frozen rows with the selection highlighted, width cells wide
// and at most height rows. Outside the selection the rows keep the colours the
// program gave them; inside, the text is redrawn plain on the highlight.
func (s *selection) view(width, height int) []string {
	out := make([]string, 0, height)
	for y := 0; y < len(s.screen) && y < height; y++ {
		line := s.screen[y]
		l, r, ok := s.cols(y, width)
		if ok {
			l, r = min(l, width), min(r, width)
			picked := plainText(ansi.Strip(ansi.Cut(line, l, r)))
			picked += strings.Repeat(" ", max(r-l-ansi.StringWidth(picked), 0))
			line = ansi.Cut(line, 0, l) + copySelectedStyle.Render(picked) + ansi.Cut(line, r, width)
		}
		out = append(out, pad(ansi.Truncate(line, width, ""), width))
	}
	return out
}

// plainRows is the text of rendered rows: what the frozen picture shows, so
// what is copied is exactly what is highlighted. Reading it from the picture
// rather than asking Session.Lines matters with a tm server, where Lines is a
// round trip that can lag the pushed picture by a frame, and answers nothing
// — after blocking the press for its 2 s timeout — when the server has
// stopped responding.
func plainRows(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(plainText(ansi.Strip(r)), " ")
	}
	return out
}

// paneContent is the screen rectangle of t's emulated screen: its card less
// the border and the header row.
func paneContent(card rect) rect {
	return rect{
		x: card.x + cardStyle.GetBorderLeftSize(),
		y: card.y + cardStyle.GetBorderTopSize() + paneHeaderHeight,
		w: max(card.w-cardStyle.GetHorizontalBorderSize(), 1),
		h: max(card.h-cardStyle.GetVerticalBorderSize()-paneHeaderHeight, 1),
	}
}

// clampTo is the cell of r under screen position (x, y), held inside r. That
// clamp is the point of tm doing the selection: however far the pointer goes,
// the selection stays in the pane it started in.
func clampTo(r rect, x, y int) cell {
	return cell{x: min(max(x-r.x, 0), r.w-1), y: min(max(y-r.y, 0), r.h-1)}
}

// handleSelect runs the left button over the panes. It reports whether the
// event was its own.
func (m *Model) handleSelect(msg tea.MouseMsg) bool {
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button != tea.MouseButtonLeft {
			m.sel = nil // the wheel moves the screen out from under a selection
			return false
		}
		m.sel = nil
		p := m.selectedProject()
		if p == nil {
			return false
		}
		t, card := m.paneAt(p, msg.X, msg.Y)
		if t == nil {
			return false
		}
		content := paneContent(card)
		if !content.contains(msg.X, msg.Y) {
			return true // the border or the header: nothing to select
		}
		off := m.scroll[t]
		var screen string
		if off > 0 {
			screen = t.Session.RenderScrolled(off)
		} else {
			screen = t.Session.Render(p.ActiveTerminal() == t && m.focus == focusMain)
		}
		at := clampTo(content, msg.X, msg.Y)
		rows := strings.Split(screen, "\n")
		m.sel = &selection{
			term:     t,
			screen:   rows,
			lines:    plainRows(rows),
			start:    at,
			end:      at,
			dragging: true,
		}
		return true

	case tea.MouseActionMotion:
		s := m.sel
		if s == nil || !s.dragging {
			return false
		}
		if card, ok := m.paneRect(s.term); ok {
			s.end = clampTo(paneContent(card), msg.X, msg.Y)
			s.moved = s.moved || s.end != s.start
		}
		return true

	case tea.MouseActionRelease:
		s := m.sel
		if s == nil || !s.dragging {
			return false
		}
		s.dragging = false
		if !s.moved {
			m.sel = nil // a click selects nothing and freezes nothing
			return true
		}
		if text := s.text(); text != "" {
			writeClipboard(text)
			lines := strings.Count(text, "\n") + 1
			m.notice = fmt.Sprintf("copied %d line%s", lines, plural(lines))
		}
		return true
	}
	return false
}

// paneRect is the screen rectangle of t's card in the project on screen.
func (m Model) paneRect(t *workspace.Terminal) (rect, bool) {
	p := m.selectedProject()
	if p == nil {
		return rect{}, false
	}
	if m.zoom {
		if p.ActiveTerminal() == t {
			return m.screenRect(m.mainArea()), true
		}
		return rect{}, false
	}
	r, ok := m.paneScreenRects(p)[t]
	return r, ok
}

// writeClipboard hands text to the terminal with OSC 52. tm asks the terminal
// to do it rather than calling a clipboard tool, because that is the only way
// that works over SSH: the escape travels back down the connection the user is
// typing over and lands in the clipboard of the machine in front of them. The
// cost is that terminals may refuse it — many ship with it disabled, and
// GNOME Terminal ignores it entirely — with no reply to check, which is why
// the desktop's clipboard tool is handed the text as well.
func writeClipboard(text string) {
	fmt.Fprint(clipboard, ansi.SetSystemClipboard(text))
	localClipboard(text) // for terminals that ignore OSC 52; see clipboard.go
}

// clipboard is where the OSC 52 sequence goes: the terminal tm is drawn on.
// A variable so a test can read what was sent without a terminal to send it to.
var clipboard io.Writer = os.Stdout
