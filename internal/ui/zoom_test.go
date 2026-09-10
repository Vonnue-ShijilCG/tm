package ui

import (
	"reflect"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

func sizes(spawned *[]*fakeSession) [][2]int {
	out := make([][2]int, len(*spawned))
	for i, s := range *spawned {
		out[i] = [2]int{s.cols, s.rows}
	}
	return out
}

func TestZoomFillsAreaAndUnzoomRestoresLayout(t *testing.T) {
	m, spawned, p := twoPanes(t)
	m = press(m, "alt+h", "enter") // three panes: [1 | 2/3], focused 3
	rectsBefore := m.paneRects(p)
	sizesBefore := sizes(spawned)
	snapBefore := m.ws.Snapshot()
	leavesBefore := append([]*workspace.Terminal(nil), m.layouts[p].leaves()...)

	m = press(m, "alt+z")
	if !m.zoom {
		t.Fatal("alt+z should zoom")
	}
	area := m.mainArea()
	if s := (*spawned)[2]; s.cols != sessionCols(area) || s.rows != sessionRows(area) {
		t.Errorf("zoomed session should fill the area: %dx%d vs %dx%d", s.cols, s.rows, sessionCols(area), sessionRows(area))
	}
	view := m.View()
	if !strings.Contains(view, "SCREEN:/ws/backend:t3") || strings.Contains(view, "SCREEN:/ws/backend:t1") || strings.Contains(view, "SCREEN:/ws/backend:t2") {
		t.Errorf("zoomed view should show only terminal 3:\n%s", view)
	}
	if !strings.Contains(view, "· zoom") {
		t.Error("status should indicate zoom")
	}
	if (*spawned)[0].closed || (*spawned)[1].closed {
		t.Error("other terminals must keep running while zoomed")
	}
	if !reflect.DeepEqual(m.paneRects(p), rectsBefore) || !reflect.DeepEqual(m.layouts[p].leaves(), leavesBefore) {
		t.Error("zoom must not change the layout tree")
	}
	m = press(m, "hello")
	if got := strings.Join((*spawned)[2].text, ""); got != "hello" {
		t.Errorf("input should reach the zoomed terminal, got %q", got)
	}
	if len((*spawned)[0].text)+len((*spawned)[1].text) != 0 {
		t.Error("hidden terminals must not receive input")
	}

	m = press(m, "alt+z")
	if m.zoom {
		t.Fatal("second alt+z should unzoom")
	}
	if !reflect.DeepEqual(sizes(spawned), sizesBefore) {
		t.Errorf("unzoom should restore session sizes: %v vs %v", sizes(spawned), sizesBefore)
	}
	if !reflect.DeepEqual(m.paneRects(p), rectsBefore) {
		t.Error("unzoom should restore the previous layout exactly")
	}
	if !reflect.DeepEqual(m.ws.Snapshot(), snapBefore) {
		t.Error("zoom must not touch persisted state")
	}
	for i := 0; i < 6; i++ {
		m = press(m, "alt+z")
	}
	if m.zoom || !reflect.DeepEqual(m.paneRects(p), rectsBefore) {
		t.Error("repeated zoom/unzoom must be stable")
	}
}

func TestZoomFollowsFocus(t *testing.T) {
	m, spawned, p := twoPanes(t)
	m = press(m, "alt+z", "alt+left") // zoomed on 2, then focus 1
	if !m.zoom || p.ActiveTerminal() != p.Terminals[0] {
		t.Fatalf("focus change while zoomed should keep zoom and move it: zoom=%v active=%v", m.zoom, p.ActiveTerminal())
	}
	area := m.mainArea()
	if s := (*spawned)[0]; s.cols != sessionCols(area) || s.rows != sessionRows(area) {
		t.Errorf("newly focused terminal should now fill the area: %dx%d", s.cols, s.rows)
	}
	if v := m.View(); !strings.Contains(v, "SCREEN:/ws/backend:t1") || strings.Contains(v, "SCREEN:/ws/backend:t2") {
		t.Errorf("zoom should show the newly focused terminal:\n%s", v)
	}
	m = press(m, "alt+j") // cycling also moves the zoom
	if !m.zoom || p.ActiveTerminal() != p.Terminals[1] {
		t.Error("alt+j while zoomed should zoom the next terminal")
	}
	// The zoomed terminal exiting moves the zoom to the remaining one.
	_ = (*spawned)[1].Close()
	next, _ := m.Update(terminalExitedMsg{id: p.Terminals[1].ID})
	m = next.(Model)
	if !m.zoom || p.ActiveTerminal() != p.Terminals[0] {
		t.Errorf("zoom should stay on the remaining terminal: zoom=%v", m.zoom)
	}
	_ = (*spawned)[0].Close()
	next, _ = m.Update(terminalExitedMsg{id: p.Terminals[0].ID})
	m = next.(Model)
	if m.zoom || m.focus != focusSidebar {
		t.Error("zoom should clear when no terminal is left")
	}
}

func TestSidebarZKeyZoomsActiveTerminal(t *testing.T) {
	m, _, _ := twoPanes(t)
	m = press(m, "alt+s", "z")
	if !m.zoom || m.focus != focusMain {
		t.Errorf("z in the sidebar should zoom the project's active terminal and focus it: zoom=%v focus=%v", m.zoom, m.focus)
	}
	m = press(m, "alt+s", "z")
	if m.zoom {
		t.Error("z again should unzoom")
	}
	m2, spawned := newTestModel(t)
	m2 = press(m2, "z")
	if m2.zoom || len(*spawned) != 0 {
		t.Error("z without a terminal should do nothing")
	}
	_ = uv.KeyEnter
}
