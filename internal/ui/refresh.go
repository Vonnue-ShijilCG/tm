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

// The sidebar's buttons are its last row, drawn as chips on the pane
// headers' band so they read as things to press rather than as hints. A
// terminal row is the smallest target there is, so each button owns its half
// of the row, not just its label.
const (
	refreshLabel = " ↻ refresh "
	helpLabel    = " ? keys "
	buttonGap    = 1 // cells between the two chips

	sidebarFooterRows = 2                         // a blank row, then the buttons
	sidebarFooterMin  = 2 + 1 + sidebarFooterRows // title, blank, one project, footer
)

var buttonStyle = lipgloss.NewStyle().Foreground(bright).Background(band)

type sidebarButton int

const (
	buttonNone sidebarButton = iota
	buttonRefresh
	buttonHelp
)

// sidebarButtons draws the button row.
func sidebarButtons() string {
	return buttonStyle.Render(refreshLabel) + textStyle.Render(strings.Repeat(" ", buttonGap)) + buttonStyle.Render(helpLabel)
}

// sidebarButtonAt is the button a left click at the message's position
// pressed, if any. It must measure the frame View draws.
func (m Model) sidebarButtonAt(msg tea.MouseMsg) sidebarButton {
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return buttonNone
	}
	height := m.height - statusHeight - sidebarStyle.GetVerticalFrameSize() // the sidebar's rows
	if height < sidebarFooterMin {
		return buttonNone
	}
	row := sidebarStyle.GetBorderTopSize() + height - 1
	left := sidebarStyle.GetBorderLeftSize() + sidebarStyle.GetPaddingLeft()
	if msg.Y != row || msg.X >= sidebarWidth-sidebarStyle.GetBorderRightSize() {
		return buttonNone
	}
	if msg.X < left+lipgloss.Width(refreshLabel)+buttonGap {
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

	// A highlight whose project went stays where it was in the list, on
	// what is now there, rather than jumping to the top of a long one.
	m.cursor, m.selected = min(m.cursor, max(len(m.ws.Projects)-1, 0)), -1
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
