package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// The projects are discovered once, at start, so a repository cloned into the
// workspace while tm runs was invisible until a restart — and with a tm server
// a restart is the thing nobody does any more. The sidebar's ↻ button, and r
// in the sidebar, look again.

// The sidebar's buttons sit on the row under its title, which was blank, so
// they cost the project list nothing. A row of the terminal is the smallest
// target there is, so each button owns its half of the row, not just its
// label.
const (
	refreshLabel = "↻ refresh"
	helpLabel    = "? keys"
	buttonGap    = 3 // cells between the two labels
)

type sidebarButton int

const (
	buttonNone sidebarButton = iota
	buttonRefresh
	buttonHelp
)

// sidebarButtons draws the button row.
func sidebarButtons() string {
	return hintStyle.Render(refreshLabel) + textStyle.Render(strings.Repeat(" ", buttonGap)) + hintStyle.Render(helpLabel)
}

// sidebarButtonAt is the button a left click at the message's position
// pressed, if any.
func sidebarButtonAt(msg tea.MouseMsg) sidebarButton {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return buttonNone
	}
	row := sidebarStyle.GetBorderTopSize() + 1 // under the title
	left := sidebarStyle.GetBorderLeftSize() + sidebarStyle.GetPaddingLeft()
	if msg.Y != row || msg.X >= sidebarWidth-sidebarStyle.GetBorderRightSize() {
		return buttonNone
	}
	if msg.X < left+lipgloss.Width(refreshLabel)+buttonGap/2+1 {
		return buttonRefresh
	}
	return buttonHelp
}

// refreshProjects discovers the workspace's repositories again and merges
// them in (see workspace.Rediscover), keeping the sidebar's cursor and the
// project on screen where they were, and says what changed.
func (m *Model) refreshProjects() {
	found, err := project.Discover(m.root)
	if err != nil {
		m.notice = fmt.Sprintf("find projects: %v", err)
		return
	}
	at := func(i int) *workspace.Project {
		if i < 0 || i >= len(m.ws.Projects) {
			return nil
		}
		return m.ws.Projects[i]
	}
	cursor, selected := at(m.cursor), at(m.selected)

	added, removed := m.ws.Rediscover(found)

	m.cursor, m.selected = 0, -1
	for i, p := range m.ws.Projects {
		if p == cursor {
			m.cursor = i
		}
		if p == selected {
			m.selected = i
		}
	}
	if len(added)+len(removed) > 0 {
		m.persist() // first: it clears the notice, and the server's copy of the sidebar must match
		if m.notice != "" {
			return // a failed save says so, and that matters more
		}
	}
	switch {
	case len(added) == 0 && len(removed) == 0:
		m.notice = "no new projects"
	case len(removed) == 0:
		m.notice = "found " + projectList(added)
	case len(added) == 0:
		m.notice = "gone: " + projectList(removed)
	default:
		m.notice = "found " + projectList(added) + "; gone: " + projectList(removed)
	}
}

// projectList names up to three projects, then counts the rest.
func projectList(names []string) string {
	if len(names) > 3 {
		return fmt.Sprintf("%s and %d more", strings.Join(names[:3], ", "), len(names)-3)
	}
	return strings.Join(names, ", ")
}
