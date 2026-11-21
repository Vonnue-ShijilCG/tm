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

// handleMouse routes the mouse to whatever is under the pointer: the wheel
// to the changes view when it is open, else to the terminal pane; the left
// button to the sidebar's refresh button or to a selection in a pane (see
// handleSelect). Every other mouse event is ignored.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.diff != nil {
		if msg.Action != tea.MouseActionPress {
			return nil
		}
		return m.diffWheel(msg)
	}
	if m.help || m.form != nil || m.panel != nil {
		return nil
	}
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && m.deleting != nil {
		// A click cancels a pending alt+delete as any other key does. Left
		// armed, the next y typed anywhere — in another pane or project —
		// deleted the terminal.
		m.deleting, m.deletePrompt = nil, ""
	}
	switch m.sidebarButtonAt(msg) {
	case buttonRefresh:
		m.sel = nil
		m.refreshProjects()
		return nil
	case buttonHelp:
		m.sel = nil
		m.help = true
		return nil
	}
	m.clickFocus(msg)
	if m.handleSelect(msg) || msg.Action != tea.MouseActionPress {
		return nil
	}
	var up bool
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		up = true
	case tea.MouseButtonWheelDown:
	default:
		return nil
	}
	p := m.selectedProject()
	if p == nil {
		return nil
	}
	t, r := m.paneAt(p, msg.X, msg.Y)
	if t == nil {
		return nil
	}
	if m.scroll == nil {
		m.scroll = map[*workspace.Terminal]int{}
	}
	if t.Session.MouseTracking() { // the program handles the wheel itself (Claude Code, htop, Vim with mouse)
		t.Session.SendWheel(up, msg.X-r.x, msg.Y-r.y-paneHeaderHeight)
		return nil
	}
	if t.Session.IsAltScreen() {
		code := uv.KeyDown
		if up {
			code = uv.KeyUp
		}
		for i := 0; i < wheelStep; i++ {
			t.Session.SendKey(uv.KeyPressEvent{Code: code})
		}
		return nil
	}
	off := m.scroll[t]
	if up {
		off = min(off+wheelStep, t.Session.ScrollbackLen())
	} else {
		off = max(off-wheelStep, 0)
	}
	if off == 0 {
		delete(m.scroll, t)
		return nil
	}
	m.scroll[t] = off
	return nil
}

// diffWheel scrolls the changes view. The pointer picks what moves: over the
// file list the wheel changes the selected file, and the columns follow it to
// that file; over the columns it scrolls them, both at once, because they
// share one offset and a line must stay facing its counterpart. Sideways
// notches — shift+wheel on most terminals, a trackpad's other axis — move the
// shared horizontal offset, exactly as ← and → do.
//
// The list moves one file per notch rather than wheelStep: an entry is two or
// three rows tall, so one file is already a wheel notch's worth of movement,
// and each step re-reads a file from git.
func (m *Model) diffWheel(msg tea.MouseMsg) tea.Cmd {
	if msg.Y >= m.height-statusHeight { // the status bar, below the view
		return nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelLeft:
		m.diff.scrollX(-diffHScrollStep)
	case tea.MouseButtonWheelRight:
		m.diff.scrollX(diffHScrollStep)
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		step := wheelStep
		files := 1
		if msg.Button == tea.MouseButtonWheelUp {
			step, files = -step, -files
		}
		if msg.X < modalStyle.GetBorderLeftSize()+modalStyle.GetPaddingLeft()+diffListWidth {
			return m.diff.move(files)
		}
		m.diff.scroll(step, m.diffRows())
	}
	return nil
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
