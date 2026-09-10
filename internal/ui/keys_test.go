package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
)

// TestTerminalModeKeyPolicy pins down which keys the application reserves
// while a terminal has focus and which reach the PTY untouched.
func TestTerminalModeKeyPolicy(t *testing.T) {
	alt := func(r string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r), Alt: true} }
	runes := func(r string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)} }

	passThrough := map[string]tea.KeyMsg{
		"z":         runes("z"),
		"s":         runes("s"),
		"w":         runes("w"),
		"m":         runes("m"),
		"a":         runes("a"),
		"v":         runes("v"),
		"h":         runes("h"),
		"tab":       {Type: tea.KeyTab},
		"shift+tab": {Type: tea.KeyShiftTab},
		"ctrl+c":    {Type: tea.KeyCtrlC},
		"ctrl+\\":   {Type: tea.KeyCtrlBackslash},
		"ctrl+d":    {Type: tea.KeyCtrlD},
		"ctrl+z":    {Type: tea.KeyCtrlZ},
		"left":      {Type: tea.KeyLeft},
		"up":        {Type: tea.KeyUp},
		"alt+f":     alt("f"), // an Alt chord tm does not use reaches the program (readline forward-word)
		"esc":       {Type: tea.KeyEsc},
	}
	for name, msg := range passThrough {
		m, spawned, _ := twoPanes(t)
		s := (*spawned)[1]
		before := len(s.keys) + len(s.text)
		next, cmd := m.Update(msg)
		m = next.(Model)
		if cmd != nil || m.focus != focusMain || m.zoom || m.form != nil || m.panel != nil || m.help || len(*spawned) != 2 {
			t.Errorf("%s must be plain PTY input, but changed application state", name)
		}
		if len(s.keys)+len(s.text) != before+1 {
			t.Errorf("%s should be forwarded to the focused session exactly once", name)
		}
	}

	reserved := map[string]tea.KeyMsg{
		"alt+j": alt("j"), "alt+k": alt("k"), "alt+n": alt("n"), "alt+m": alt("m"), "alt+x": alt("x"),
		"alt+s": alt("s"),
		"alt+v": alt("v"), "alt+h": alt("h"), "alt+z": alt("z"), "alt+=": alt("="), "alt+-": alt("-"),
		"alt+left": {Type: tea.KeyLeft, Alt: true}, "alt+right": {Type: tea.KeyRight, Alt: true},
		"alt+up": {Type: tea.KeyUp, Alt: true}, "alt+down": {Type: tea.KeyDown, Alt: true},
	}
	for name, msg := range reserved {
		m, spawned, _ := twoPanes(t)
		s := (*spawned)[1]
		next, _ := m.Update(msg)
		m = next.(Model)
		if len(s.keys)+len(s.text) != 0 {
			t.Errorf("%s is reserved by tm and must not reach the PTY, got keys=%v text=%v", name, s.keys, s.text)
		}
		_ = m
	}
	_ = uv.KeyEnter
}

// TestSidebarLettersAreApplicationKeys checks the plain-letter shortcuts in
// sidebar mode, where no PTY has focus.
func TestSidebarLettersAreApplicationKeys(t *testing.T) {
	m, spawned, _ := twoPanes(t)
	m = press(m, "alt+s")
	m = press(m, "s")
	if !m.help {
		t.Error("s should open help in the sidebar")
	}
	m = press(m, "esc", "m")
	if m.panel == nil {
		t.Error("m should open the terminal manager in the sidebar")
	}
	m = press(m, "esc", "z")
	if !m.zoom {
		t.Error("z should zoom in the sidebar")
	}
	m = press(m, "alt+s", "v")
	if m.form == nil || m.pendingSplit != splitSide {
		t.Error("v should open the split-beside form in the sidebar")
	}
	m = press(m, "esc", "h")
	if m.form == nil || m.pendingSplit != splitStack {
		t.Error("h should open the split-below form in the sidebar")
	}
	m = press(m, "esc")
	if len(*spawned) != 2 {
		t.Error("none of these keys may spawn a terminal by themselves")
	}
	if !strings.Contains(press(m, "s").View(), "zoom") {
		t.Error("help must document zoom")
	}
}
