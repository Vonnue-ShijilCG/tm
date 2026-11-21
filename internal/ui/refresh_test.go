package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
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
