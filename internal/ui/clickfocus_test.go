package ui

import (
	"strings"
	"testing"

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
