package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// A pane header is no longer a filled bar — the card's border is the pane's
// top edge now — but it must still respect the width it is given, whatever
// the focus or running state.
// (Lip Gloss emits no colour under go test, so only widths are asserted
// here; see focus_indicator_test.go for the status dot's colour logic.)

func TestPaneHeaderFitsTheGivenWidth(t *testing.T) {
	for _, focused := range []bool{true, false} {
		for _, run := range []bool{true, false} {
			if w := lipgloss.Width(paneHeader("Terminal 1", " · main", 24, focused, run)); w != 24 {
				t.Errorf("focused=%v running=%v: header is %d cells wide, want the full pane width 24", focused, run, w)
			}
		}
	}
}

func TestLongTitlesStillFitTheHeaderBar(t *testing.T) {
	if w := lipgloss.Width(paneHeader(strings.Repeat("npm run dev ", 8), " · main", 20, true, true)); w != 20 {
		t.Errorf("an over-long title made the header %d cells wide, want 20", w)
	}
}
