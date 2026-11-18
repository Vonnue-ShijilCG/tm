package ui

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
		"wheel", "scroll back", // the wheel is not a key binding but belongs on the shortcut screen
		"tab / l", "g / home", "G / end", "esc / q", "tab / ↓", "k / ↑"} { // alternates are shown, not just the first key
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
	// At 80x24 the bindings no longer fit in four columns, so the screen packs
	// five narrow ones and section titles are cut to their column. Every
	// section is still there and identifiable, but only the full-size screen
	// shows every label whole — see viewHelp.
	for _, want := range []string{"SIDEBAR", "MANAGE", "close help"} {
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
	for _, step := range [][]string{{"enter"}, {"alt+s", "n"}, {"esc", "m"}, {"esc", "s"}} {
		m = press(m, step...)
		status := m.viewStatus()
		if strings.Contains(status, "\n") {
			t.Errorf("status bar after %v wraps:\n%s", step, status)
		}
	}
	_ = tea.KeyMsg{}
}

// TestStatusBarNeverExceedsTheWindowWidth guards the backstop in viewStatus:
// the key hints alone (before any notice/prompt/info is even considered) can
// be wider than a narrow window, and the label is sacrificed down to nothing
// before the hints are ever shortened, so without a final clamp the assembled
// line can still overflow. The width is derived from keys.go's own sidebar
// hints — the single source of truth for their text — rather than a
// hardcoded number, so this can't rot silently if a binding's help text
// changes.
func TestStatusBarNeverExceedsTheWindowWidth(t *testing.T) {
	m, _ := newTestModel(t)

	var hints []string
	for _, b := range keys.sidebarHelp() {
		h := b.Help()
		hints = append(hints, fmt.Sprintf("%s %s", h.Key, hintStyle.Render(h.Desc)))
	}
	hintsWidth := ansi.StringWidth(strings.Join(hints, hintStyle.Render(" │ ")))

	m.width = hintsWidth / 2 // narrower than the hints alone; nothing else can fit either
	if w := ansi.StringWidth(m.viewStatus()); w > m.width {
		t.Errorf("status bar is %d cells wide, want at most the window's %d", w, m.width)
	}
}

// With a server, q leaves the terminals running and Q is what ends them; the
// help screen has to say so, because the same key now means something else.
// Without one, nothing about the screen changes.
func TestHelpScreenExplainsServerKeys(t *testing.T) {
	ts := startTestServer(t)
	attached := press(newAttachedModel(t, ts, t.TempDir()), "s")
	plain, _ := newTestModel(t)
	plain = press(plain, "s")

	if got := keys.Leave.Help(); got.Key != "q/ctrl+c" || got.Desc != "leave (terminals keep running)" {
		t.Errorf("Leave reads %q %q", got.Key, got.Desc)
	}
	// Drawn in two rows, the second under the first's description.
	leave := regexp.MustCompile(`q/ctrl\+c +leave \(terminals .*\n│ +keep running\)`)
	stop := regexp.MustCompile(`\bQ\s+stop all`)
	quit := regexp.MustCompile(`q/ctrl\+c\s+quit`)

	view := ansi.Strip(attached.View())
	if !leave.MatchString(view) || !stop.MatchString(view) {
		t.Errorf("help with a server should list leave and stop all:\n%s", view)
	}
	if quit.MatchString(view) {
		t.Errorf("help with a server still says q quits:\n%s", view)
	}

	view = ansi.Strip(plain.View())
	if !quit.MatchString(view) {
		t.Errorf("help without a server should say q quits:\n%s", view)
	}
	if leave.MatchString(view) || stop.MatchString(view) || strings.Contains(view, "stop all") {
		t.Errorf("help without a server lists server keys:\n%s", view)
	}

	for name, m := range map[string]Model{"attached": attached, "plain": plain} {
		if lines := strings.Count(m.View(), "\n") + 1; lines > m.height {
			t.Errorf("%s help is %d lines in a %d-row window", name, lines, m.height)
		}
	}
}
