package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Pane boundaries must be legible without counting cells: every pane header is
// a filled bar across the pane, and the dividers between panes are heavy.
// (Lip Gloss emits no colour under go test, so styles are inspected, not
// rendered; widths are real either way.)

func TestPaneHeadersAreFilledBars(t *testing.T) {
	for _, focused := range []bool{true, false} {
		if _, unset := headerStyle(focused).GetBackground().(lipgloss.NoColor); unset {
			t.Errorf("focused=%v: header has no background, so the pane has no visible top edge", focused)
		}
		if w := lipgloss.Width(paneHeader("Terminal 1", 24, focused)); w != 24 {
			t.Errorf("focused=%v: header bar is %d cells wide, want the full pane width 24", focused, w)
		}
	}
	if headerStyle(true).GetBackground() == headerStyle(false).GetBackground() {
		t.Error("the focused pane's header must still stand out from the others")
	}
}

func TestLongTitlesStillFitTheHeaderBar(t *testing.T) {
	if w := lipgloss.Width(paneHeader(strings.Repeat("npm run dev ", 8), 20, true)); w != 20 {
		t.Errorf("an over-long title made the bar %d cells wide, want 20", w)
	}
}

func TestSideBySidePanesAreSeparatedByAHeavyDivider(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "alt+v", "enter")
	p := m.ws.Projects[0]

	panes := m.viewPanes(p, p.ActiveTerminal())
	if !strings.Contains(panes, verticalDivider) {
		t.Errorf("side-by-side panes should be split by %q:\n%s", verticalDivider, panes)
	}
	if strings.Contains(panes, "│") {
		t.Error("the thin divider should be gone from the pane area")
	}
}

func TestStackedPanesAreSeparatedByAHeavyDivider(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "alt+h", "enter")
	p := m.ws.Projects[0]

	panes := m.viewPanes(p, p.ActiveTerminal())
	if !strings.Contains(panes, horizontalDivider) {
		t.Errorf("stacked panes should be split by %q:\n%s", horizontalDivider, panes)
	}
}
