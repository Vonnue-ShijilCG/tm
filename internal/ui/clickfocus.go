package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// clickFocus makes a left click do what the keys do: a click on a pane
// focuses it, as alt+j and alt+arrows do. It never opens, starts or reruns
// anything, and the click itself is not passed to the program.
func (m *Model) clickFocus(msg tea.MouseMsg) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return
	}
	p := m.selectedProject()
	if p == nil {
		return
	}
	t, _ := m.paneAt(p, msg.X, msg.Y)
	if t == nil {
		return
	}
	m.focus = focusMain
	if p.ActiveTerminal() == t {
		return
	}
	selectTerminal(p, t)
	m.applyLayout(p)
	m.persist() // focus is part of the saved layout, as the keys save it
}
