package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// gitDirs makes a workspace whose children named here look like repositories.
func gitDirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// discoveredModel is a Model over a real directory, so a rescan has
// something to find.
func discoveredModel(t *testing.T, names ...string) (Model, string) {
	t.Helper()
	root := t.TempDir()
	gitDirs(t, root, names...)
	found, err := project.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := New(root, found).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model), root
}

// click presses and releases the left button at a screen cell.
func click(m Model, x, y int) Model {
	m = mouse(m, tea.MouseActionPress, x, y)
	return mouse(m, tea.MouseActionRelease, x, y)
}

// A repository cloned into the workspace while tm runs shows up when the ↻
// button is clicked, without a restart, and the cursor stays on the project
// it was on even though the new one sorts above it.
func TestRefreshButtonFindsNewProjects(t *testing.T) {
	m, root := discoveredModel(t, "bravo", "delta")
	m = press(m, "j") // cursor on delta
	gitDirs(t, root, "alpha")

	at := find(t, m, "↻ refresh")
	m = click(m, at[0], at[1])

	if !strings.Contains(ansi.Strip(m.View()), "alpha") {
		t.Errorf("alpha should be in the sidebar after a refresh:\n%s", ansi.Strip(m.View()))
	}
	if m.notice != "found alpha" {
		t.Errorf("notice = %q, want it to name what was found", m.notice)
	}
	if got := m.ws.Projects[m.cursor].Name; got != "delta" {
		t.Errorf("cursor moved to %q, want it to stay on delta", got)
	}
}

// r in the sidebar does what the button does, and says so when nothing new
// turned up.
func TestRefreshKeySaysWhenNothingIsNew(t *testing.T) {
	m, _ := discoveredModel(t, "bravo")

	m = press(m, "r")

	if m.notice != "no new projects" {
		t.Errorf("notice = %q, want no new projects", m.notice)
	}
}

// The ? button beside it opens the help screen, for whoever does not know s.
func TestHelpButtonOpensTheKeyMap(t *testing.T) {
	m, _ := discoveredModel(t, "bravo")

	at := find(t, m, "? keys")
	m = click(m, at[0], at[1])

	if !m.help {
		t.Error("clicking ? keys should open the help screen")
	}
}

// The buttons sit on the sidebar's last row with a blank row above them, and
// the title keeps its blank row under it, so neither crowds the list.
func TestSidebarButtonsSitAtTheBottomWithRoomAround(t *testing.T) {
	m, _ := discoveredModel(t, "bravo")
	rows := strings.Split(ansi.Strip(m.View()), "\n")

	at := find(t, m, "↻ refresh")
	if want := m.height - statusHeight - 2; at[1] != want {
		t.Errorf("buttons on row %d, want the sidebar's last row %d", at[1], want)
	}
	if strings.TrimSpace(strings.Trim(rows[at[1]-1][:sidebarWidth], "│")) != "" {
		t.Errorf("the row above the buttons should be blank: %q", rows[at[1]-1])
	}
	title := find(t, m, "Projects")
	if strings.TrimSpace(strings.Trim(rows[title[1]+1][:sidebarWidth], "│")) != "" {
		t.Errorf("the row under the title should be blank: %q", rows[title[1]+1])
	}
}

// A window too short for the buttons gives their rows back to the projects.
func TestShortWindowDropsTheSidebarButtons(t *testing.T) {
	m, _ := discoveredModel(t, "bravo")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 6})
	m = next.(Model)

	view := ansi.Strip(m.View())
	if strings.Contains(view, "refresh") || !strings.Contains(view, "bravo") {
		t.Errorf("a 6-row window should show the project and no buttons:\n%s", view)
	}
}

// A refresh that removes every project leaves the window its own height and
// the buttons where clicks find them: that is exactly when ↻ is the way back.
func TestRefreshToAnEmptyWorkspaceStaysInTheWindow(t *testing.T) {
	m, root := discoveredModel(t, "bravo")
	if err := os.RemoveAll(filepath.Join(root, "bravo")); err != nil {
		t.Fatal(err)
	}
	m = press(m, "r")

	if n := len(strings.Split(m.View(), "\n")); n != m.height {
		t.Fatalf("View is %d rows, want the window's %d:\n%s", n, m.height, ansi.Strip(m.View()))
	}
	gitDirs(t, root, "charlie")
	at := find(t, m, "↻ refresh")
	m = click(m, at[0], at[1])
	if !strings.Contains(m.notice, "charlie") {
		t.Errorf("clicking ↻ in an empty workspace: notice %q, want charlie found", m.notice)
	}
}

// When the highlighted project is the one removed, the highlight stays near
// where it was rather than jumping to the top.
func TestRefreshRemovingTheHighlightedProjectKeepsItNearby(t *testing.T) {
	m, root := discoveredModel(t, "alpha", "bravo", "charlie", "delta")
	m = press(m, "j", "j") // charlie
	if err := os.RemoveAll(filepath.Join(root, "charlie")); err != nil {
		t.Fatal(err)
	}

	m = press(m, "r")

	if got := m.ws.Projects[m.cursor].Name; got != "delta" && got != "bravo" {
		t.Errorf("highlight on %q, want a neighbour of the removed charlie", got)
	}
}

// The sidebar's border is not part of a button.
func TestTheSidebarBorderIsNotTheRefreshButton(t *testing.T) {
	m, _ := discoveredModel(t, "bravo")
	at := find(t, m, "↻ refresh")

	m = click(m, 0, at[1])

	if m.notice != "" {
		t.Errorf("a click on the border refreshed: %q", m.notice)
	}
}

// A repository deleted from disk while terminals still run (or are saved) in
// it stays listed — a rescan never forgets a terminal — and the notice says
// so instead of "no new projects".
func TestRefreshSaysWhenAMissingProjectIsKept(t *testing.T) {
	m, root := discoveredModel(t, "alpha", "bravo")
	m.ws.Projects[1].Saved = []workspace.SavedTerminal{{Command: "npm test"}}
	if err := os.RemoveAll(filepath.Join(root, "bravo")); err != nil {
		t.Fatal(err)
	}

	m = press(m, "r")

	if !strings.Contains(m.notice, "bravo") || !strings.Contains(m.notice, "kept") {
		t.Errorf("notice = %q, want it to say bravo is missing but kept", m.notice)
	}
}
