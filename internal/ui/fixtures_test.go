package ui

import "testing"

// makeTerminal creates a terminal through the new-terminal form the way a
// user does: key opens the form (n, alt+n, alt+v, alt+h); with a branch the
// terminal gets its own worktree on that branch, without one it is a plain
// shell in the project checkout. Names are no longer typed or shown, but
// tests still refer to terminals by name, so the name is recorded on the
// new terminal's definition afterwards and saved.
func makeTerminal(t *testing.T, m Model, key, name, branch string) Model {
	t.Helper()
	before := 0
	if p := m.form; p != nil {
		t.Fatal("makeTerminal: a form is already open")
	}
	m = press(m, key)
	if m.form == nil {
		t.Fatalf("makeTerminal: %s did not open the form", key)
	}
	p := m.form.project
	before = len(p.Terminals)
	if branch == "" {
		m = press(m, "tab", "enter") // no branch means the project checkout
	} else {
		m = typeText(m, branch)
		m = press(m, "enter")
	}
	if m.form != nil || len(p.Terminals) != before+1 {
		t.Fatalf("makeTerminal(%s, %q, %q): form=%v terminals %d -> %d (%s)", key, name, branch, m.form != nil, before, len(p.Terminals), m.notice)
	}
	if name != "" {
		p.Terminals[len(p.Terminals)-1].Name = name
		m.persist()
	}
	return m
}
