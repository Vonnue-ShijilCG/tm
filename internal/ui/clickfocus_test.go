package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// A click on a pane does what alt+j / alt+arrows do: that pane takes focus,
// and typing goes to it.
func TestClickingAPaneFocusesIt(t *testing.T) {
	m, spawned := clickPanes(t)
	m = press(m, "alt+s")

	m = click(m, find(t, m, "LEFT")[0], find(t, m, "LEFT")[1])

	p := m.ws.Projects[0]
	if m.focus != focusMain || p.ActiveTerminal() != p.Terminals[0] {
		t.Fatalf("focus = %v, active = %v; want the left pane focused", m.focus, p.ActiveIndex())
	}
	m = press(m, "x")
	if got := strings.Join((*spawned)[0].text, ""); got != "x" {
		t.Errorf("left pane received %q, want the typed x", got)
	}
}

// A drag that starts in a pane without focus focuses it, and still copies
// only that pane's text.
func TestDragFromAnUnfocusedPaneFocusesAndCopiesIt(t *testing.T) {
	clip := clipboardCatcher(t)
	m, _ := clickPanes(t)

	at := find(t, m, "LEFT")
	m = drag(m, at, [2]int{at[0] + 3, at[1]}, [2]int{m.width - 1, at[1]})

	p := m.ws.Projects[0]
	if p.ActiveTerminal() != p.Terminals[0] || m.focus != focusMain {
		t.Error("the pane the drag started in should take focus")
	}
	if got := clip.String(); got != ansi.SetSystemClipboard("LEFT") {
		t.Errorf("clipboard = %q, want only the left pane's text", got)
	}
}

// clickPanes is two side-by-side panes, the right one focused (it was made
// last), showing LEFT and RIGHT.
func clickPanes(t *testing.T) (Model, *[]*fakeSession) {
	t.Helper()
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+v", "enter")
	(*spawned)[0].rowsText = []string{"LEFT"}
	(*spawned)[1].rowsText = []string{"RIGHT"}
	m.panes = nil
	p := m.ws.Projects[0]
	if p.ActiveTerminal() != p.Terminals[1] {
		t.Fatal("setup: the new right pane should have focus")
	}
	return m, spawned
}

// A click on the sidebar does what alt+s and j/k do: the sidebar takes focus
// and the highlight lands on the clicked project. Nothing is opened.
func TestClickingTheSidebarFocusesItAndMovesTheHighlight(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter") // a shell in backend, focused
	before := len(*spawned)

	at := find(t, m, "mobile")
	m = click(m, at[0], at[1])

	if m.focus != focusSidebar {
		t.Errorf("focus = %v, want the sidebar", m.focus)
	}
	if got := m.ws.Projects[m.cursor].Name; got != "mobile" {
		t.Errorf("highlight on %q, want mobile", got)
	}
	if len(*spawned) != before {
		t.Error("a click must not start a terminal")
	}
}

// Empty sidebar space, and the blank row above the buttons, focus the sidebar
// and leave the highlight where it was.
func TestClickingEmptySidebarSpaceKeepsTheHighlight(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter") // ecommerce, terminal focused
	buttons := find(t, m, "↻ refresh")

	for _, y := range []int{10, buttons[1] - 1} {
		m = press(m, "alt+s", "j", "enter") // back into a terminal, cursor moved on
		cursor := m.cursor
		m = click(m, 5, y)
		if m.focus != focusSidebar || m.cursor != cursor {
			t.Errorf("row %d: focus %v cursor %d, want the sidebar with the cursor still %d", y, m.focus, m.cursor, cursor)
		}
	}
}

// A name too long for one row wraps; a click on its second line is still a
// click on that project.
func TestClickingEitherLineOfAWrappedNameSelectsIt(t *testing.T) {
	long := "m-project-name-long-enough-to-wrap" // sorts between the others, so the highlight starts elsewhere
	m, _ := discoveredModel(t, "alpha", long, "omega")

	tail := find(t, m, "-wrap")
	m = click(m, tail[0], tail[1])

	if got := m.ws.Projects[m.cursor].Name; got != long {
		t.Errorf("highlight on %q, want the wrapped project", got)
	}
}

// A long list scrolls to keep the highlight on screen; a click must pick the
// project drawn on that row, not the one at that index.
func TestClickingAScrolledSidebarPicksTheProjectDrawnThere(t *testing.T) {
	var names []string
	for i := 0; i < 40; i++ {
		names = append(names, "proj"+string(rune('A'+i/10))+string(rune('0'+i%10)))
	}
	m, _ := discoveredModel(t, names...)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = press(next.(Model), "G")

	at := find(t, m, "projD7")
	m = click(m, at[0], at[1])

	if got := m.ws.Projects[m.cursor].Name; got != "projD7" {
		t.Errorf("highlight on %q, want projD7", got)
	}
}

// A click answers a pending alt+delete question the way any other key does:
// by cancelling it. Left armed, the next y typed anywhere deleted the
// terminal.
func TestClickCancelsAPendingDelete(t *testing.T) {
	m, spawned := clickPanes(t)
	m = press(m, "alt+delete")
	if m.deleting == nil {
		t.Fatal("setup: alt+delete should ask first")
	}

	at := find(t, m, "LEFT")
	m = click(m, at[0], at[1])
	m = press(m, "y")

	if m.deleting != nil || (*spawned)[1].closed {
		t.Error("a click should cancel the question; y then deleted the terminal")
	}
}

// Zoomed, only the focused pane is on screen, and a click there focuses it.
func TestClickInZoomFocusesTheVisiblePane(t *testing.T) {
	m, _ := clickPanes(t)
	m = press(m, "alt+z", "alt+s")

	m = click(m, 60, 10)

	p := m.ws.Projects[0]
	if m.focus != focusMain || p.ActiveTerminal() != p.Terminals[1] {
		t.Errorf("focus %v active %d, want the zoomed right pane focused", m.focus, p.ActiveIndex())
	}
}

// The button row is the buttons', not the list's: a click there never moves
// the highlight.
func TestClickingTheButtonRowKeepsTheHighlight(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j")
	at := find(t, m, "? keys")

	m = click(m, at[0], at[1])

	if m.cursor != 1 {
		t.Errorf("cursor = %d, want it left on 1", m.cursor)
	}
}
