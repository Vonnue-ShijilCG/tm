package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// clickFocus makes a left click do what the keys do: a click on a pane
// focuses it, as alt+j and alt+arrows do, and a click on the sidebar
// focuses the sidebar, as alt+s does, with the highlight moved to the
// project clicked, as j/k would. It never opens, starts or reruns anything,
// and the click itself is not passed to the program.
func (m *Model) clickFocus(msg tea.MouseMsg) {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return
	}
	if msg.X < sidebarWidth {
		m.focus = focusSidebar
		if i := m.sidebarProjectAt(msg.Y); i >= 0 {
			m.cursor = i
		}
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
	// Focus is part of the saved layout, as the keys save it. persist clears
	// the status line on success, and a click is no reason to lose what it
	// said — "copied 2 lines" is usually what a click to another pane follows.
	notice := m.notice
	m.persist()
	if m.notice == "" {
		m.notice = notice
	}
}

// projectNameLines is a project's name as the sidebar draws it, wrapped
// rather than truncated: a name is how a project is recognised, and the
// sidebar is narrow enough that real names do not fit. inner is the
// sidebar's content width; the first two cells hold the marker.
func projectNameLines(name string, inner int) []string {
	return strings.Split(ansi.Wrap(name, max(inner-2, 1), ""), "\n")
}

// sidebarProjectAt is the index of the project drawn on screen row y, or -1.
// It measures the frame viewSidebar draws: the border, the title and its
// blank row, then the list in the rows the footer leaves, scrolled by
// visibleStart.
func (m Model) sidebarProjectAt(y int) int {
	height := m.height - statusHeight - sidebarStyle.GetVerticalFrameSize()
	if height >= sidebarFooterMin {
		height -= sidebarFooterRows
	}
	height -= 2 // the title and the blank row under it
	row := y - sidebarStyle.GetBorderTopSize() - 2
	if row < 0 || row >= height {
		return -1
	}
	inner := sidebarWidth - sidebarStyle.GetHorizontalFrameSize()
	entries := make([][]string, len(m.ws.Projects))
	for i, p := range m.ws.Projects {
		entries[i] = projectNameLines(p.Name, inner)
	}
	used := 0
	for i := visibleStart(entries, m.cursor, height); i < len(entries); i++ {
		if used+len(entries[i]) > height {
			break
		}
		if row < used+len(entries[i]) {
			return i
		}
		used += len(entries[i])
	}
	return -1
}
