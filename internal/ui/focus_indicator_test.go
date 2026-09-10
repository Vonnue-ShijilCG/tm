package ui

import (
	"reflect"
	"strings"
	"testing"
)

// Terminal identity is shown once, on each pane's header. Focus is carried
// by the status dot's colour now, not by a filled bar; Lip Gloss emits no
// colour under go test, so the dot's colour selection is asserted against
// the style values directly, and the header text separately.
func TestPaneHeadersAreTheOnlyTerminalIndicators(t *testing.T) {
	if !reflect.DeepEqual(dotStyle(true, true), dotFocused) || !reflect.DeepEqual(dotStyle(true, false), dotFocused) {
		t.Error("a focused pane's dot must always be the focus colour, running or not")
	}
	if !reflect.DeepEqual(dotStyle(false, true), dotRunning) {
		t.Error("an unfocused, running pane's dot must be the running colour")
	}
	if !reflect.DeepEqual(dotStyle(false, false), dotExited) {
		t.Error("an unfocused, exited pane's dot must be the exited colour")
	}

	m, _, p := twoPanes(t)
	for _, focusFirst := range []bool{false, true} {
		if focusFirst {
			m = press(m, "alt+left")
		}
		view := m.View()
		panes := m.viewPanes(p, p.ActiveTerminal())
		for _, header := range []string{" " + statusDot + " Terminal 1", " " + statusDot + " Terminal 2"} {
			if strings.Count(panes, header) != 1 {
				t.Errorf("each visible terminal appears exactly once in the main view, as its pane header; %q found %d times:\n%s", header, strings.Count(panes, header), panes)
			}
		}
		if want := m.paneTitle(p, p.ActiveTerminal()); !strings.Contains(view, want) {
			t.Errorf("focused pane header %q missing from the view", want)
		}
	}
	// The panes start on the first row of the main area: no bar above them.
	rs := m.paneRects(p)
	for term, r := range rs {
		if r.y != 0 {
			t.Errorf("pane %d should start at the top of the area, got y=%d", term.Num, r.y)
		}
	}
	if area := m.mainArea(); area.h != m.height-statusHeight-mainAreaStyle.GetVerticalFrameSize() {
		t.Errorf("main area height %d should be the window minus status bar and frame", area.h)
	}
}
