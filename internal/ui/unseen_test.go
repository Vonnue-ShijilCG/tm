package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// speak delivers the message a terminal's output produces, the way its watcher
// command does.
func speak(m Model, t *workspace.Terminal) Model {
	next, _ := m.Update(terminalOutputMsg{id: t.ID, s: t.Session})
	return next.(Model)
}

// A pane you can see needs no marker: its output is already in front of you.
// Marking one would make the signal meaningless, since every pane on screen
// would carry it.
func TestOutputFromAVisiblePaneIsNotMarked(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter")
	p, term := m.activeTerminal()

	m = speak(m, term)

	if m.unseen[term] {
		t.Error("a pane on screen was marked as having printed out of sight")
	}
	if m.stirring(p) {
		t.Error("the project was marked while its only terminal is visible")
	}
}

// Alt+x hides a running terminal. Whatever it prints then prints where nobody
// is looking, which is the case this marker exists for.
func TestOutputFromAHiddenTerminalIsMarkedAndShownInTheManager(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter")
	_, term := m.activeTerminal()
	m = press(m, "alt+n", "enter") // a second terminal, so hiding the first leaves one on screen
	m = press(m, "alt+j")          // back to the first
	if _, focused := m.activeTerminal(); focused != term {
		m = press(m, "alt+j")
	}
	m = press(m, "alt+x")

	m = speak(m, term)

	if !m.unseen[term] {
		t.Fatal("a hidden terminal's output should be marked")
	}
	m = press(m, "alt+m")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "hidden · new output") {
		t.Errorf("the manager should report the hidden terminal's output:\n%s", view)
	}
}

// The mark is cleared by showing the pane, not by any key in particular:
// drawing it is what "seen" means.
func TestShowingAPaneClearsItsMark(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter")
	first, term := m.activeTerminal()
	m = press(m, "alt+s", "j", "enter") // another project fills the main area
	m = speak(m, term)
	if !m.unseen[term] {
		t.Fatal("setup: a terminal off screen should be marked")
	}

	m = press(m, "alt+s", "k", "enter") // back to the project that spoke
	if p := m.selectedProject(); p != first {
		t.Fatalf("setup: selected %v, want the first project", p)
	}
	m.View()

	if m.unseen[term] {
		t.Error("the pane is on screen again; its mark should be gone")
	}
	if m.stirring(first) {
		t.Error("the project is the one being shown; its sidebar marker should be plain again")
	}
}

// Another project's terminals are not on screen at all, which is the case that
// matters most: a build finishing in a repo you are not looking at.
func TestOutputFromAnotherProjectMarksItInTheSidebar(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter") // open the first project
	first, term := m.activeTerminal()
	m = press(m, "alt+s", "j", "enter") // move to the second and open it
	if p := m.selectedProject(); p == first {
		t.Fatal("setup: the second project should be selected")
	}

	m = speak(m, term)

	if !m.stirring(first) {
		t.Error("the project that is not being shown should be marked")
	}
	// Lip Gloss emits no colour under go test, so the sidebar must be asked
	// for it explicitly, the way the painting tests do.
	withTrueColor(t)
	want := lipgloss.NewStyle().Foreground(stir).Background(ground).Render("●")
	if !strings.Contains(m.viewSidebar(), want) {
		t.Errorf("the sidebar marker should carry the amber:\n%q", ansi.Strip(m.viewSidebar()))
	}
}
