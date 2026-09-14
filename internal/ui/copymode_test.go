package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// clipboardCatcher replaces the writer copy mode sends OSC 52 to, so a test
// can read what would have reached the terminal.
func clipboardCatcher(t *testing.T) *strings.Builder {
	t.Helper()
	var b strings.Builder
	prev := clipboard
	clipboard = &b
	t.Cleanup(func() { clipboard = prev })
	return &b
}

// copyModel opens a terminal whose screen is the given rows and enters copy
// mode on it.
func copyModel(t *testing.T, rows ...string) (Model, *[]*fakeSession) {
	t.Helper()
	m, spawned := newTestModel(t)
	m = press(m, "enter")
	(*spawned)[0].rowsText = rows
	return press(m, "alt+c"), spawned
}

// Copying is why this mode exists: a row of the terminal's buffer spans every
// pane, so the host terminal's own selection cannot pick out one pane's text.
// What lands on the clipboard must be the pane's lines and nothing else — no
// card border, no neighbour.
func TestCopyModeSendsTheSelectedLinesToTheClipboard(t *testing.T) {
	clip := clipboardCatcher(t)
	m, _ := copyModel(t, "$ npm test", "FAIL src/auth.test.js", "  Cannot read 'token'", "")

	if m.copy == nil {
		t.Fatal("alt+c should open copy mode")
	}
	// The cursor starts on the last row with anything on it.
	if m.copy.cursor != 2 {
		t.Fatalf("cursor = %d, want 2: the last row with output on it", m.copy.cursor)
	}

	m = press(m, "k", "v", "j") // select the two FAIL lines
	m = press(m, "y")

	if m.copy != nil {
		t.Error("y should copy and close")
	}
	want := "FAIL src/auth.test.js\n  Cannot read 'token'"
	if got := clip.String(); !strings.Contains(got, ansi.SetSystemClipboard(want)) {
		t.Errorf("clipboard sequence does not carry %q:\n%q", want, got)
	}
	if !strings.Contains(m.notice, "copied 2 lines") {
		t.Errorf("notice = %q, want it to report two lines copied", m.notice)
	}
}

// With no selection started, y copies the cursor's line alone, so the mode is
// useful for one line without pressing v first.
func TestCopyModeWithNoSelectionCopiesTheCursorLine(t *testing.T) {
	clip := clipboardCatcher(t)
	m, _ := copyModel(t, "first", "second", "third")

	m = press(m, "y")

	if got := clip.String(); !strings.Contains(got, ansi.SetSystemClipboard("third")) {
		t.Errorf("clipboard should carry the cursor's line alone:\n%q", got)
	}
}

// Esc leaves without touching the clipboard: a selection begun by accident
// must not overwrite what the user had copied earlier.
func TestEscLeavesCopyModeWithoutCopying(t *testing.T) {
	clip := clipboardCatcher(t)
	m, _ := copyModel(t, "one", "two")

	m = press(m, "v", "k", "esc")

	if m.copy != nil {
		t.Error("esc should close copy mode")
	}
	if clip.String() != "" {
		t.Errorf("esc wrote to the clipboard: %q", clip.String())
	}
}

// The rows are frozen when copy mode opens. A program that keeps printing
// would otherwise move the text out from under the cursor between choosing a
// line and copying it.
func TestCopyModeShowsFrozenRowsWhileTheProgramKeepsPrinting(t *testing.T) {
	clip := clipboardCatcher(t)
	m, spawned := newTestModel(t)
	m = press(m, "enter")
	(*spawned)[0].rowsText = []string{"before"}
	m = press(m, "alt+c")

	(*spawned)[0].rowsText = []string{"after"} // the program prints on

	if view := ansi.Strip(m.View()); !strings.Contains(view, "before") || strings.Contains(view, "after") {
		t.Errorf("copy mode should show the rows it opened on:\n%s", view)
	}
	m = press(m, "y")
	if got := clip.String(); !strings.Contains(got, ansi.SetSystemClipboard("before")) {
		t.Errorf("the copy should be of the frozen rows:\n%q", got)
	}
}

// Copy mode consumes every key, so plain j and k mean something here while
// they are PTY input everywhere else in terminal focus.
func TestCopyModeConsumesKeysThatWouldReachTheProgram(t *testing.T) {
	m, spawned := copyModel(t, "one", "two", "three")
	sent := func() int { return len((*spawned)[0].keys) + len((*spawned)[0].text) }
	before := sent()

	m = press(m, "j", "k", "n")

	if m.copy == nil {
		t.Fatal("copy mode should still be open")
	}
	if after := sent(); after != before {
		t.Errorf("%d keystrokes reached the program; copy mode should consume them", after-before)
	}
	if m.form != nil {
		t.Error("n opened the new-terminal form from inside copy mode")
	}
}

// The pane says its rows are frozen, so a screen that has stopped moving is
// explained rather than looking hung.
func TestCopyModeMarksThePaneHeader(t *testing.T) {
	m, _ := copyModel(t, "one", "two")

	if view := ansi.Strip(m.View()); !strings.Contains(view, copyMark) {
		t.Errorf("the pane header should carry %q:\n%s", copyMark, view)
	}
}

// A selection dragged past the end of the output would otherwise paste a run
// of empty lines.
func TestCopyDropsTrailingBlankLines(t *testing.T) {
	clip := clipboardCatcher(t)
	m, _ := copyModel(t, "text", "", "", "")

	m = press(m, "g", "v", "G", "y") // everything, blanks included

	if got := clip.String(); !strings.Contains(got, ansi.SetSystemClipboard("text")) {
		t.Errorf("the blank rows should not have been copied:\n%q", got)
	}
}
