package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// Scrollback: the mouse wheel over a pane scrolls that pane's history,
// whether or not it has focus. A program that enabled mouse tracking gets
// the wheel as a mouse event instead, exactly as a terminal emulator would
// deliver it. Programs on the alternate screen without mouse tracking
// (plain less, Vim without mouse) keep no history, so the wheel becomes
// arrow keys for them, as terminal emulators do by default. Scroll
// positions are runtime only; typing into a pane or resizing it returns it
// to the live screen.

// wheelStep is how many lines one wheel notch scrolls.
const wheelStep = 3

// scrollMark precedes the scrolled-back line count in a pane header.
const scrollMark = "↑"

// handleMouse routes wheel events to the pane under the pointer. Every
// other mouse event is ignored.
func (m *Model) handleMouse(msg tea.MouseMsg) {
	if msg.Action != tea.MouseActionPress || m.help || m.form != nil || m.panel != nil || m.diff != nil {
		return
	}
	var up bool
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		up = true
	case tea.MouseButtonWheelDown:
	default:
		return
	}
	p := m.selectedProject()
	if p == nil {
		return
	}
	t, r := m.paneAt(p, msg.X, msg.Y)
	if t == nil {
		return
	}
	if m.scroll == nil {
		m.scroll = map[*workspace.Terminal]int{}
	}
	if t.Session.MouseTracking() { // the program handles the wheel itself (Claude Code, htop, Vim with mouse)
		t.Session.SendWheel(up, msg.X-r.x, msg.Y-r.y-paneHeaderHeight)
		return
	}
	if t.Session.IsAltScreen() {
		code := uv.KeyDown
		if up {
			code = uv.KeyUp
		}
		for i := 0; i < wheelStep; i++ {
			t.Session.SendKey(uv.KeyPressEvent{Code: code})
		}
		return
	}
	off := m.scroll[t]
	if up {
		off = min(off+wheelStep, t.Session.ScrollbackLen())
	} else {
		off = max(off-wheelStep, 0)
	}
	if off == 0 {
		delete(m.scroll, t)
		return
	}
	m.scroll[t] = off
}

// paneAt returns the terminal whose pane covers screen cell (x, y) and the
// pane's screen rectangle, or nil.
func (m Model) paneAt(p *workspace.Project, x, y int) (*workspace.Terminal, rect) {
	if m.zoom {
		if t, r := p.ActiveTerminal(), m.screenRect(m.mainArea()); t != nil && r.contains(x, y) {
			return t, r
		}
		return nil, rect{}
	}
	for t, r := range m.paneScreenRects(p) {
		if r.contains(x, y) {
			return t, r
		}
	}
	return nil, rect{}
}

// paneScreenRects is paneRects translated to screen coordinates.
func (m Model) paneScreenRects(p *workspace.Project) map[*workspace.Terminal]rect {
	rs := m.paneRects(p)
	for t, r := range rs {
		rs[t] = m.screenRect(r)
	}
	return rs
}

// screenRect translates a rectangle inside the main area (whose origin is
// the top-left content cell of the main pane) to screen coordinates.
func (m Model) screenRect(r rect) rect {
	r.x += sidebarWidth + paneGutter + mainAreaStyle.GetBorderLeftSize()
	r.y += mainAreaStyle.GetBorderTopSize()
	return r
}

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}
