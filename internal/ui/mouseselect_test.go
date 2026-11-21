package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// clipboardCatcher replaces the writer the selection sends OSC 52 to, so a
// test can read what would have reached the terminal.
func clipboardCatcher(t *testing.T) *strings.Builder {
	t.Helper()
	var b strings.Builder
	prev := clipboard
	clipboard = &b
	t.Cleanup(func() { clipboard = prev })
	return &b
}

func mouse(m Model, a tea.MouseAction, x, y int) Model {
	next, _ := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: a})
	return next.(Model)
}

// drag presses at from, moves through every point in via, and lets go at the
// last one.
func drag(m Model, from [2]int, via ...[2]int) Model {
	m = mouse(m, tea.MouseActionPress, from[0], from[1])
	for _, p := range via {
		m = mouse(m, tea.MouseActionMotion, p[0], p[1])
	}
	last := from
	if len(via) > 0 {
		last = via[len(via)-1]
	}
	return mouse(m, tea.MouseActionRelease, last[0], last[1])
}

// find is the screen cell where text first appears in the window, read from
// View the way a user sees it.
func find(t *testing.T, m Model, text string) [2]int {
	t.Helper()
	for y, line := range strings.Split(m.View(), "\n") {
		plain := ansi.Strip(line)
		if i := strings.Index(plain, text); i >= 0 {
			return [2]int{ansi.StringWidth(plain[:i]), y}
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, ansi.Strip(m.View()))
	return [2]int{}
}

// selectPanes opens two terminals side by side showing the given rows, the
// first on the left.
func selectPanes(t *testing.T, left, right []string) Model {
	t.Helper()
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+v", "enter")
	if len(*spawned) != 2 {
		t.Fatalf("spawned %d, want 2", len(*spawned))
	}
	(*spawned)[0].rowsText = left
	(*spawned)[1].rowsText = right
	m.panes = map[*workspace.Terminal]paneRender{} // drop cards drawn before the rows were set
	return m
}

// The bug this replaces: the host terminal's selection spans the whole
// buffer row, so a drag across one pane's output ran on into the card border
// and the pane beside it. A drag that wanders off the pane — right into the
// neighbour and down past the bottom — must copy only the pane it began in.
func TestDragCopiesOnlyThePaneItStartedIn(t *testing.T) {
	clip := clipboardCatcher(t)
	m := selectPanes(t,
		[]string{"$ npm test", "FAIL src/auth.test.js", "  Cannot read token"},
		[]string{"RIGHT-ONE", "RIGHT-TWO", "RIGHT-THREE"})

	start := find(t, m, "FAIL")
	far := find(t, m, "RIGHT-THREE")
	m = drag(m, start, [2]int{start[0] + 3, start[1]}, [2]int{far[0] + 5, far[1] + 10})

	got := clip.String()
	if strings.Contains(got, ansi.SetSystemClipboard("RIGHT")) || strings.Contains(got, "RIGHT") {
		t.Fatalf("the neighbouring pane leaked into the copy: %q", got)
	}
	want := "FAIL src/auth.test.js\n  Cannot read token"
	if !strings.Contains(got, ansi.SetSystemClipboard(want)) {
		t.Errorf("clipboard = %q, want the selection clamped to the left pane: %q", got, want)
	}
	if !strings.Contains(m.notice, "copied 2 lines") {
		t.Errorf("notice = %q, want it to say two lines were copied", m.notice)
	}
}

// Selection is by character, like the terminal's own: a drag over one word
// copies that word.
func TestDragCopiesFromTheCellPressedToTheCellReleased(t *testing.T) {
	clip := clipboardCatcher(t)
	m := selectPanes(t, []string{"hello world again"}, []string{"x"})

	w := find(t, m, "world")
	m = drag(m, w, [2]int{w[0] + 4, w[1]})

	if got := clip.String(); got != ansi.SetSystemClipboard("world") {
		t.Errorf("clipboard = %q, want just the word dragged over", got)
	}
}

// Dragging backwards selects the same text as dragging forwards.
func TestDragBackwardsSelectsTheSameText(t *testing.T) {
	clip := clipboardCatcher(t)
	m := selectPanes(t, []string{"hello world again"}, []string{"x"})

	w := find(t, m, "world")
	m = drag(m, [2]int{w[0] + 4, w[1]}, w)

	if got := clip.String(); got != ansi.SetSystemClipboard("world") {
		t.Errorf("clipboard = %q, want the word whichever way it was dragged", got)
	}
}

// A click is not a selection: it copies nothing, freezes nothing, and must not
// overwrite what the user had on the clipboard.
func TestClickWithoutDragCopiesNothing(t *testing.T) {
	clip := clipboardCatcher(t)
	m := selectPanes(t, []string{"hello world"}, []string{"x"})

	m = drag(m, find(t, m, "world"))

	if clip.Len() != 0 {
		t.Errorf("a click wrote to the clipboard: %q", clip.String())
	}
	if m.sel != nil {
		t.Error("a click left a selection behind")
	}
}

// While a selection is shown every row of the window stays exactly the
// window's width: the highlight is drawn inside the pane and nowhere else.
func TestSelectionHighlightStaysInsideThePane(t *testing.T) {
	clipboardCatcher(t)
	m := selectPanes(t,
		[]string{"left one", "left two", "left three"},
		[]string{"RIGHT-ONE", "RIGHT-TWO", "RIGHT-THREE"})

	start := find(t, m, "left one")
	m = drag(m, start, [2]int{m.width - 1, m.height - 1})

	if m.sel == nil {
		t.Fatal("the selection should stay shown after letting go")
	}
	view := m.View()
	for i, line := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(line); w != m.width {
			t.Errorf("row %d is %d cells wide, want %d", i, w, m.width)
		}
	}
	if !strings.Contains(ansi.Strip(view), "RIGHT-TWO") {
		t.Error("the neighbouring pane should still be drawn beside the selection")
	}
	if !strings.Contains(ansi.Strip(view), copyMark) {
		t.Error("the selected pane's header should say it is frozen")
	}
}

// Any key dismisses the selection and still reaches the program.
func TestTypingDismissesTheSelection(t *testing.T) {
	clipboardCatcher(t)
	m := selectPanes(t, []string{"hello world"}, []string{"x"})
	w := find(t, m, "world")
	m = drag(m, w, [2]int{w[0] + 2, w[1]})
	if m.sel == nil {
		t.Fatal("drag should leave a selection")
	}

	m = press(m, "a")

	if m.sel != nil {
		t.Error("a key should dismiss the selection")
	}
}
