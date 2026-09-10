package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Pane boundaries must be legible without counting cells: every pane header is
// a filled bar across the pane.
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
