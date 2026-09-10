package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// wheel delivers one mouse wheel notch at screen cell (x, y).
func wheel(m Model, up bool, x, y int) Model {
	b := tea.MouseButtonWheelDown
	if up {
		b = tea.MouseButtonWheelUp
	}
	next, _ := m.Update(tea.MouseMsg{X: x, Y: y, Button: b, Action: tea.MouseActionPress})
	return next.(Model)
}

// center returns a screen cell inside t's pane body (below the header).
func center(t *testing.T, m Model, p *workspace.Project, term *workspace.Terminal) (int, int) {
	t.Helper()
	r, ok := m.paneScreenRects(p)[term]
	if !ok {
		t.Fatalf("terminal %d has no visible pane", term.Num)
	}
	return r.x + r.w/2, r.y + paneHeaderHeight + (r.h-paneHeaderHeight)/2
}

func TestWheelUpOverPaneScrollsItsHistoryAndWheelDownReturnsToLive(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	(*spawned)[0].history = 100
	x, y := center(t, m, p, term)

	m = wheel(m, true, x, y)
	if got := m.scroll[term]; got != wheelStep {
		t.Fatalf("after one notch scroll = %d, want %d", got, wheelStep)
	}
	if view := m.View(); !strings.Contains(view, "↑ 3") || !strings.Contains(view, "SCROLLED:3:/ws/ecommerce") {
		t.Errorf("scrolled pane should show the marker and the history view:\n%s", view)
	}
	m = wheel(m, true, x, y)
	if got := m.scroll[term]; got != 2*wheelStep {
		t.Fatalf("after two notches scroll = %d, want %d", got, 2*wheelStep)
	}

	m = wheel(m, false, x, y)
	m = wheel(m, false, x, y)
	if got := m.scroll[term]; got != 0 {
		t.Fatalf("after scrolling back scroll = %d, want 0", got)
	}
	if view := m.View(); strings.Contains(view, "↑") || !strings.Contains(view, "SCREEN:/ws/ecommerce") {
		t.Errorf("live pane should render the live screen without a marker:\n%s", view)
	}
	m = wheel(m, false, x, y) // past the bottom stays live
	if got := m.scroll[term]; got != 0 {
		t.Fatalf("wheel down at live scroll = %d, want 0", got)
	}
}

func TestWheelClampsToTheHistoryLength(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	x, y := center(t, m, p, term)

	m = wheel(m, true, x, y) // nothing to show yet
	if got := m.scroll[term]; got != 0 {
		t.Fatalf("with no history scroll = %d, want 0", got)
	}

	(*spawned)[0].history = 4
	m = wheel(m, true, x, y)
	m = wheel(m, true, x, y)
	if got := m.scroll[term]; got != 4 {
		t.Fatalf("scroll = %d, want clamped to history length 4", got)
	}
}

func TestTypingIntoAScrolledPaneReturnsToLiveThenReachesTheShell(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	(*spawned)[0].history = 100
	x, y := center(t, m, p, term)
	m = wheel(m, true, x, y)

	m = press(m, "x")

	if got := m.scroll[term]; got != 0 {
		t.Errorf("scroll after typing = %d, want 0", got)
	}
	if text := (*spawned)[0].text; len(text) != 1 || text[0] != "x" {
		t.Errorf("shell received %q, want [\"x\"]", text)
	}
}

func TestWheelOverAnUnfocusedPaneScrollsThatPaneOnly(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	m = press(m, "alt+esc")
	m = makeTerminal(t, m, "v", "", "")
	p, focused := m.activeTerminal()
	if len(p.Terminals) != 2 {
		t.Fatalf("want 2 terminals, got %d", len(p.Terminals))
	}
	other := p.Terminals[0]
	if other == focused {
		other = p.Terminals[1]
	}
	for _, s := range *spawned {
		s.history = 100
	}

	x, y := center(t, m, p, other)
	m = wheel(m, true, x, y)

	if got := m.scroll[other]; got != wheelStep {
		t.Errorf("unfocused pane scroll = %d, want %d", got, wheelStep)
	}
	if got := m.scroll[focused]; got != 0 {
		t.Errorf("focused pane scroll = %d, want 0", got)
	}
	if p.ActiveTerminal() != focused {
		t.Errorf("wheel moved focus to terminal %d", p.ActiveTerminal().Num)
	}
}

func TestWheelWhileZoomedScrollsTheZoomedTerminal(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter", "alt+esc")
	m = makeTerminal(t, m, "v", "", "")
	_, focused := m.activeTerminal()
	for _, s := range *spawned {
		s.history = 100
	}
	m = press(m, "alt+esc", "z")
	if !m.zoom {
		t.Fatal("z in the sidebar should zoom")
	}

	zr := m.screenRect(m.mainArea())
	m = wheel(m, true, zr.x+zr.w/2, zr.y+zr.h/2)

	if got := m.scroll[focused]; got != wheelStep {
		t.Errorf("zoomed terminal scroll = %d, want %d", got, wheelStep)
	}
}

func TestWheelIsForwardedToAProgramThatTracksTheMouse(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	s := (*spawned)[0]
	s.history, s.alt, s.mouse = 100, true, true // e.g. Claude Code, htop, or Vim with mouse=a
	r := m.paneScreenRects(p)[term]
	x, y := r.x+7, r.y+paneHeaderHeight+2

	m = wheel(m, true, x, y)
	m = wheel(m, false, x, y)

	if got := m.scroll[term]; got != 0 {
		t.Errorf("scroll = %d, want 0: the program owns the wheel", got)
	}
	if len(s.keys) != 0 {
		t.Errorf("shell received arrow keys %v, want the wheel as a mouse event instead", s.keys)
	}
	want := []wheelCall{{up: true, x: 7, y: 2}, {up: false, x: 7, y: 2}} // pane-relative, below the header
	if len(s.wheels) != len(want) || s.wheels[0] != want[0] || s.wheels[1] != want[1] {
		t.Errorf("program received wheel events %v, want %v", s.wheels, want)
	}
}

func TestWheelOnAlternateScreenWithoutMouseTrackingSendsArrowKeys(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	s := (*spawned)[0]
	s.history, s.alt = 100, true
	x, y := center(t, m, p, term)

	m = wheel(m, true, x, y)
	m = wheel(m, false, x, y)

	if got := m.scroll[term]; got != 0 {
		t.Errorf("alt-screen pane scroll = %d, want 0", got)
	}
	if len(s.keys) != 2*wheelStep {
		t.Fatalf("shell received %d keys, want %d", len(s.keys), 2*wheelStep)
	}
	for i, k := range s.keys {
		want := uv.KeyUp
		if i >= wheelStep {
			want = uv.KeyDown
		}
		if k.Code != want {
			t.Errorf("key %d = %v, want %v", i, k.Code, want)
		}
	}
}

func TestWheelOutsideThePanesDoesNothing(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	_, term := m.activeTerminal()
	(*spawned)[0].history = 100

	m = wheel(m, true, 2, 2) // sidebar

	if got := m.scroll[term]; got != 0 {
		t.Errorf("scroll after wheel over the sidebar = %d, want 0", got)
	}
	if len((*spawned)[0].keys) != 0 {
		t.Errorf("shell received keys from a wheel over the sidebar: %v", (*spawned)[0].keys)
	}
}

func TestResizeReturnsScrolledPanesToLive(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	(*spawned)[0].history = 100
	x, y := center(t, m, p, term)
	m = wheel(m, true, x, y)

	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)

	if got := m.scroll[term]; got != 0 {
		t.Errorf("scroll after resize = %d, want 0", got)
	}
}

func TestWheelStillFindsThePaneUnderThePointerWithoutAnOuterFrame(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter")
	m = makeTerminal(t, m, "alt+v", "", "")

	p := m.selectedProject()
	rects := m.paneScreenRects(p)
	if len(rects) != 2 {
		t.Fatalf("want two panes, got %d", len(rects))
	}
	for term, r := range rects {
		// The pointer at a pane's own top-left cell must find that pane.
		got, _ := m.paneAt(p, r.x, r.y)
		if got != term {
			t.Errorf("pointer at (%d,%d) found the wrong pane", r.x, r.y)
		}
	}
	// The leftmost pane starts right of the sidebar and its one-cell gutter:
	// with no frame around the pane area there is no border cell to skip, but
	// the gutter still separates the sidebar from the first card.
	minX := 1 << 30
	for _, r := range rects {
		minX = min(minX, r.x)
	}
	if minX != sidebarWidth+paneGutter {
		t.Errorf("leftmost pane starts at x=%d, want %d (sidebar width + gutter, no outer border)", minX, sidebarWidth+paneGutter)
	}
}
