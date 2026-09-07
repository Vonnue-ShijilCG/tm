package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func TestHelpOpensFromSidebarAndEscRestoresState(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "s")
	if !m.help {
		t.Fatal("s in the sidebar should open help")
	}
	view := m.View()
	for _, want := range []string{"keyboard shortcuts", "SIDEBAR", "TERMINAL", "NEW TERMINAL", "MANAGE TERMINALS", "MOUSE", "HELP",
		"alt+x", "shift+tab", "close help", "m ", "hide terminal", "show / restore", "delete forever", "open/resume",
		"wheel", "scroll history", // the wheel is not a key binding but belongs on the shortcut screen
		"tab / l", "g / home", "G / end", "esc / q", "tab / ↓", "shift+tab / ↑"} { // alternates are shown, not just the first key
		if !strings.Contains(view, want) {
			t.Errorf("help view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Projects") {
		t.Error("help should replace the normal layout")
	}
	if lines := strings.Count(view, "\n") + 1; lines > m.height {
		t.Errorf("help view has %d lines, taller than the %d-row window", lines, m.height)
	}
	small, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if v := small.(Model).View(); strings.Count(v, "\n")+1 > 24 {
		t.Errorf("help view does not fit 80x24 (%d lines):\n%s", strings.Count(v, "\n")+1, v)
	}
	for _, want := range []string{"SIDEBAR", "MANAGE TERMINALS", "close help"} {
		if !strings.Contains(small.(Model).View(), want) {
			t.Errorf("80x24 help view missing %q", want)
		}
	}

	// Keys other than close are ignored while help is open.
	m = press(m, "j", "n", "enter")
	if !m.help || len(*spawned) != 0 || m.cursor != 1 {
		t.Errorf("keys leaked through help: help=%v spawned=%d cursor=%d", m.help, len(*spawned), m.cursor)
	}
	m = press(m, "esc")
	if m.help || m.focus != focusSidebar || m.cursor != 1 {
		t.Errorf("esc should close help and restore the sidebar state: help=%v focus=%v cursor=%d", m.help, m.focus, m.cursor)
	}
}

func TestHelpKeyGoesToTerminalWhenFocused(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "s")
	if m.help {
		t.Fatal("s must reach the shell while a terminal is focused")
	}
	if got := strings.Join((*spawned)[0].text, ""); got != "s" {
		t.Errorf("shell received %q, want \"s\"", got)
	}
}

// Every binding in keyMap must be documented in some help section, so a
// new shortcut cannot be added without showing up in the help screen.
func TestEveryBindingIsInHelp(t *testing.T) {
	documented := map[string]bool{}
	for _, s := range helpSections() {
		for _, b := range s.Bindings {
			documented[b.Help().Key+"|"+b.Help().Desc] = true
		}
	}
	v := reflect.ValueOf(keys)
	for i := 0; i < v.NumField(); i++ {
		b := v.Field(i).Interface().(key.Binding)
		if !documented[b.Help().Key+"|"+b.Help().Desc] {
			t.Errorf("binding %s (%s) is not listed in helpSections", v.Type().Field(i).Name, b.Help().Key)
		}
	}
}

func TestStatusBarFitsOneLine(t *testing.T) {
	m, _ := newTestModel(t)
	for _, step := range [][]string{{"enter"}, {"esc", "n"}, {"esc", "m"}, {"esc", "s"}} {
		m = press(m, step...)
		status := m.viewStatus()
		if strings.Contains(status, "\n") {
			t.Errorf("status bar after %v wraps:\n%s", step, status)
		}
	}
	_ = tea.KeyMsg{}
}
