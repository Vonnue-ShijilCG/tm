package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/config"
	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// manyProjects is a workspace with more repositories than any window has rows.
func manyProjects(t *testing.T, n, width, height int) Model {
	t.Helper()
	ps := make([]project.Project, n)
	for i := range ps {
		ps[i] = project.Project{Name: fmt.Sprintf("repository-number-%02d", i), Path: fmt.Sprintf("/ws/r%02d", i)}
	}
	m := New(t.TempDir(), nil)
	m.ws = workspace.New("/ws", ps, func(dir string, cols, rows int) (workspace.Session, error) {
		return newFakeSession(dir, cols, rows), nil
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(Model)
}

func viewRows(m Model) []string { return strings.Split(ansi.Strip(m.View()), "\n") }

// Lip Gloss pads a short block up to its height but never cuts a long one, so
// a list that ignores the height does not clip: it draws past the bottom of
// the window, pushing the status bar off the screen. A workspace with more
// repositories than rows used to render six lines too many.
func TestTheSidebarNeverDrawsPastTheWindow(t *testing.T) {
	for _, height := range []int{24, 14, 8} {
		m := manyProjects(t, 24, 100, height)
		for _, cursor := range []int{0, 11, 23} {
			m.cursor = cursor
			rows := viewRows(m)
			if len(rows) != height {
				t.Errorf("height %d, cursor %d: view is %d rows:\n%s", height, cursor, len(rows), strings.Join(rows, "\n"))
			}
			if last := rows[len(rows)-1]; !strings.Contains(last, "quit") {
				t.Errorf("height %d, cursor %d: the status bar is not the last row, got %q", height, cursor, last)
			}
		}
	}
}

// Clipping is only half of it: the project the cursor is on has to be one of
// the rows that survive, or moving down walks the selection off the screen.
func TestTheSidebarKeepsTheSelectedProjectOnScreen(t *testing.T) {
	m := manyProjects(t, 24, 100, 24)

	for i := 0; i < 23; i++ {
		m = press(m, "j")
		want := fmt.Sprintf("repository-number-%02d", i+1)
		if !strings.Contains(ansi.Strip(m.View()), want) {
			t.Fatalf("after %d presses the selected project %s is off screen:\n%s", i+1, want, ansi.Strip(m.View()))
		}
	}
	// And back up again: the window follows the cursor in both directions.
	for i := 22; i >= 0; i-- {
		m = press(m, "k")
		want := fmt.Sprintf("repository-number-%02d", i)
		if !strings.Contains(ansi.Strip(m.View()), want) {
			t.Fatalf("moving back up, %s is off screen:\n%s", want, ansi.Strip(m.View()))
		}
	}
}

// managerModel opens the manager on a project filled to the terminal limit, in
// a window too short to show every row: six terminals are twelve rows before
// the heading, the project name and whatever sits under the list.
func managerModel(t *testing.T, height int) Model {
	t.Helper()
	m, _ := newTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: height})
	m = press(next.(Model), "enter")
	for i := 1; i < config.DefaultMaxTerminals; i++ {
		m = press(m, "alt+n", "enter")
	}
	m = press(m, "alt+m")
	if m.panel == nil {
		t.Fatal("alt+m should open the manager")
	}
	// The list's own budget, not the window's: the status bar, the pane border
	// and the manager's four heading rows are spoken for before it.
	budget := height - statusHeight - mainStyle.GetVerticalFrameSize() - 4
	if lines := len(m.panel.rows) * 2; lines <= budget {
		t.Fatalf("setup: %d list lines fit the %d-line budget, so nothing would overflow", lines, budget)
	}
	return m
}

// The Terminal Manager is the other list that grows without limit: one row per
// terminal, two lines each, in a window that does not change size.
func TestTheManagerNeverDrawsPastTheWindow(t *testing.T) {
	m := managerModel(t, 14)

	for cursor := 0; cursor < len(m.panel.rows); cursor++ {
		m.panel.cursor = cursor
		rows := viewRows(m)
		if len(rows) != m.height {
			t.Errorf("cursor %d: view is %d rows in a %d-row window:\n%s", cursor, len(rows), m.height, strings.Join(rows, "\n"))
		}
	}
}

// Clipping is only half of it here too: the row the cursor is on must survive,
// or j walks the selection off the bottom and the keys act on something the
// user cannot see.
func TestTheManagerKeepsTheSelectedRowOnScreen(t *testing.T) {
	m := managerModel(t, 14)

	for i := 1; i < len(m.panel.rows); i++ {
		m = press(m, "j")
		want := terminalLabel(m.panel.rows[m.panel.cursor].terminal.Terminal)
		if !strings.Contains(ansi.Strip(m.View()), want) {
			t.Fatalf("after %d presses the selected row %q is off screen:\n%s", i, want, ansi.Strip(m.View()))
		}
	}
}

// A confirmation asks about the row the cursor is on. It is drawn below the
// list, so the list has to give up a row for it rather than the other way
// round — a question with its answer scrolled off is worse than a short list.
func TestTheManagerKeepsItsPromptWhenTheListIsLong(t *testing.T) {
	m := press(managerModel(t, 14), "d") // delete forever: asks first

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "y confirm") {
		t.Errorf("the confirmation must survive a full list:\n%s", view)
	}
	if rows := viewRows(m); len(rows) != m.height {
		t.Errorf("view is %d rows in a %d-row window:\n%s", len(rows), m.height, view)
	}
}
