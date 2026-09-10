package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// TestMain points the configuration at an empty directory so tests see the
// default terminal limit regardless of the developer's own config file.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tm-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func testProjects() []project.Project {
	return []project.Project{
		{Name: "backend", Path: "/ws/backend"},
		{Name: "ecommerce", Path: "/ws/ecommerce"},
		{Name: "mobile", Path: "/ws/mobile"},
	}
}

func press(m Model, keys ...string) Model {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyMsg{Type: tea.KeyShiftTab}
		case "ctrl+\\":
			msg = tea.KeyMsg{Type: tea.KeyCtrlBackslash}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "alt+j", "alt+k", "alt+n", "alt+x":
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k[4:]), Alt: true}
		case "alt+delete":
			msg = tea.KeyMsg{Type: tea.KeyDelete, Alt: true}
		case "alt+esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc, Alt: true}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func TestVimNavigationMovesCursor(t *testing.T) {
	m, _ := newTestModel(t)

	m = press(m, "j", "j")
	if m.cursor != 2 {
		t.Fatalf("after jj cursor = %d, want 2", m.cursor)
	}
	m = press(m, "j") // clamp at bottom
	if m.cursor != 2 {
		t.Fatalf("cursor should clamp at last item, got %d", m.cursor)
	}
	m = press(m, "k")
	if m.cursor != 1 {
		t.Fatalf("after k cursor = %d, want 1", m.cursor)
	}
	m = press(m, "g")
	if m.cursor != 0 {
		t.Fatalf("after g cursor = %d, want 0", m.cursor)
	}
	m = press(m, "G")
	if m.cursor != 2 {
		t.Fatalf("after G cursor = %d, want 2", m.cursor)
	}
	m = press(m, "k", "k", "k") // clamp at top
	if m.cursor != 0 {
		t.Fatalf("cursor should clamp at first item, got %d", m.cursor)
	}
}

func TestSidebarListsProjectsAndMarksSelection(t *testing.T) {
	m, _ := newTestModel(t)
	if m.selected != -1 {
		t.Fatalf("nothing should be selected initially, got %d", m.selected)
	}
	m = press(m, "j", "enter")
	if m.selected != 1 {
		t.Fatalf("selected = %d, want 1", m.selected)
	}
	view := m.View()
	for _, want := range []string{"backend", "ecommerce", "mobile"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestFocusFollowsSessions(t *testing.T) {
	m, _ := newTestModel(t)
	if m.focus != focusSidebar {
		t.Fatal("sidebar should be focused initially")
	}
	m = press(m, "tab", "l")
	if m.focus != focusSidebar {
		t.Fatal("tab/l must not focus an empty main pane")
	}
	m = press(m, "enter", "alt+esc")
	m = press(m, "tab")
	if m.focus != focusMain {
		t.Fatal("tab should focus the terminal once a shell is running")
	}
	m = press(m, "alt+esc", "l")
	if m.focus != focusMain {
		t.Fatal("l should focus the terminal too")
	}
}

func TestQuit(t *testing.T) {
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyCtrlC},
	} {
		m, _ := newTestModel(t)
		_, cmd := m.Update(k)
		if cmd == nil {
			t.Fatalf("%s should return a quit command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s command should produce tea.QuitMsg", k)
		}
	}
}

func TestEmptyWorkspaceRendersHint(t *testing.T) {
	m := New("/ws", nil)
	m.ws = workspace.New("/ws", nil, func(string, int, int) (workspace.Session, error) { t.Fatal("must not spawn"); return nil, nil })
	m.width, m.height = 80, 24
	m = press(m, "j", "n", "enter") // must not panic on empty list
	if !strings.Contains(m.View(), "No Git repositories") {
		t.Fatalf("empty workspace should show a hint:\n%s", m.View())
	}
}
