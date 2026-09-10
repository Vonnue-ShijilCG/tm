package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAltMOpensManagerForTheFocusedTerminalsProject(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter") // backend running, terminal focused
	backend := m.ws.Projects[0]
	m = press(m, "alt+m")
	if m.panel == nil || m.panel.project != backend {
		t.Fatalf("alt+m should open the manager for backend, got %+v", m.panel)
	}
	if m.focus != focusMain || len(*spawned) != 1 {
		t.Errorf("opening the manager must not change focus or spawn: focus=%v spawned=%d", m.focus, len(*spawned))
	}
	if (*spawned)[0].keys != nil || (*spawned)[0].text != nil {
		t.Error("alt+m must not reach the PTY")
	}
}

func TestAltMUsesTheFocusedProjectNotTheSidebarCursor(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "alt+s", "j", "tab") // backend running and focused; sidebar cursor on ecommerce
	backend, ecommerce := m.ws.Projects[0], m.ws.Projects[1]
	if m.cursor != 1 || m.selected != 0 || m.focus != focusMain {
		t.Fatalf("setup: cursor=%d selected=%d focus=%v", m.cursor, m.selected, m.focus)
	}
	m = press(m, "alt+m")
	if m.panel == nil || m.panel.project != backend || m.panel.project == ecommerce {
		t.Errorf("alt+m must use the focused terminal's project (backend), got %v", m.panel)
	}
	// m from the sidebar still follows the cursor: esc closes the manager
	// (opened by alt+m, back to the terminal), alt+s then leaves it.
	m = press(m, "esc", "alt+s", "m")
	if m.panel == nil || m.panel.project != ecommerce {
		t.Errorf("sidebar m should open the manager for the highlighted project (ecommerce), got %v", m.panel)
	}
}

func TestEscFromManagerReturnsToThePreviousTerminalContext(t *testing.T) {
	m, spawned, p := twoPanes(t) // terminal 2 focused
	m = press(m, "alt+left")     // focus terminal 1
	before := m.layouts[p].leaves()
	m = press(m, "alt+m")
	if m.panel == nil {
		t.Fatal("alt+m should open the manager")
	}
	m = press(m, "j", "k") // move around without acting
	m = press(m, "esc")
	if m.panel != nil || m.focus != focusMain || m.selected != 0 || p.ActiveTerminal() != p.Terminals[0] {
		t.Errorf("esc should return to terminal 1 in backend: panel=%v focus=%v selected=%d active=%v", m.panel != nil, m.focus, m.selected, p.ActiveTerminal())
	}
	if got := m.layouts[p].leaves(); len(got) != len(before) || got[0] != before[0] || got[1] != before[1] {
		t.Error("the layout must be exactly as before")
	}
	if len(*spawned) != 2 {
		t.Errorf("nothing may be spawned: %d", len(*spawned))
	}
	m = press(m, "x") // typing resumes in terminal 1
	if got := (*spawned)[0].text; len(got) != 1 || got[0] != "x" {
		t.Errorf("input should reach terminal 1 again, got %v", got)
	}
	// alt+s leaves the terminal for the sidebar, and esc from the
	// sidebar-opened manager returns to the sidebar too.
	m = press(m, "alt+s", "m", "esc")
	if m.panel != nil || m.focus != focusSidebar {
		t.Error("esc from a sidebar-opened manager returns to the sidebar")
	}
}

func TestPlainMInsideATerminalIsForwarded(t *testing.T) {
	m, spawned, _ := twoPanes(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	m = next.(Model)
	if m.panel != nil {
		t.Fatal("a plain m must not open the manager while a terminal has focus")
	}
	if got := (*spawned)[1].text; len(got) != 1 || got[0] != "m" {
		t.Errorf("m should reach the focused PTY, got %v", got)
	}
}
