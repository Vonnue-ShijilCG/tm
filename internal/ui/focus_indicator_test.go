package ui

import (
	"reflect"
	"strings"
	"testing"
)

// Terminal identity is shown once, on each pane's header, and the focused
// pane's header is the only focus indicator. (Lip Gloss emits no colour
// under go test, so the styles are compared, not rendered.)
func TestPaneHeadersAreTheOnlyTerminalIndicators(t *testing.T) {
	if !reflect.DeepEqual(headerStyle(true), cursorStyle) || !reflect.DeepEqual(headerStyle(false), hintStyle) {
		t.Error("the focused pane header must use the cursor highlight, others the hint style")
	}

	m, _, p := twoPanes(t)
	for _, focusFirst := range []bool{false, true} {
		if focusFirst {
			m = press(m, "alt+left")
		}
		view := m.View()
		panes := m.viewPanes(p, p.ActiveTerminal())
		for _, header := range []string{" Terminal 1 ", " Terminal 2 "} {
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
	if area := m.mainArea(); area.h != m.height-statusHeight-mainStyle.GetVerticalFrameSize() {
		t.Errorf("main area height %d should be the window minus status bar and frame", area.h)
	}
}
