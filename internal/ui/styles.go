package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
)

const sidebarWidth = 28

// paneGutter is the blank column between the sidebar and the first card, so the
// sidebar stands clear of the terminals the way the cards stand clear of each other.
const paneGutter = 1

var (
	accent = lipgloss.Color("69")  // focused border / cursor: brighter than before, to carry against the black pane ground
	dim    = lipgloss.Color("240") // hints and the status bar
	edge   = lipgloss.Color("245") // unfocused border: lifted off dim so an inactive pane keeps a visible edge
	added  = lipgloss.Color("108") // a running terminal's status dot

	// tm's own chrome sits on the same ground the panes are drawn on, taken
	// from the same constant, so the sidebar, the gaps between splits and a
	// pane's own output read as one surface rather than three.
	ground = lipgloss.Color(terminal.PaneGround)

	// Thin and rounded: with every pane on its own ground and its own frame,
	// separation comes from the card edges and the gaps between them, not
	// from the weight of a line.
	mainStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(edge).Background(ground)
	sidebarStyle = mainStyle.Padding(0, 1)

	// mainAreaStyle is the ground the terminal cards float on. It has no
	// border: each card carries its own, and a frame around a set of frames
	// reads as clutter. mainStyle keeps its border for the help screen, the
	// form and the Terminal Manager, whose content has no frame of its own.
	mainAreaStyle = lipgloss.NewStyle().Background(ground)

	// cardStyle is one terminal's box. It has no padding so the emulated
	// screen meets its border exactly.
	cardStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(edge).Background(ground)

	titleStyle = lipgloss.NewStyle().Bold(true)

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(accent).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)

	hintStyle = lipgloss.NewStyle().Foreground(dim)

	// A pane's status dot: blue while it has focus, green while its process
	// runs, dim once it has exited.
	dotFocused = lipgloss.NewStyle().Foreground(accent)
	dotRunning = lipgloss.NewStyle().Foreground(added)
	dotExited  = lipgloss.NewStyle().Foreground(dim)

	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	statusStyle = lipgloss.NewStyle().Foreground(dim).Padding(0, 1)
)

// framed applies the focus colour and sizes a pane so its outer box is
// exactly width x height cells. Lip Gloss's Width/Height include padding
// but not the border, so only the border is subtracted here.
func framed(style lipgloss.Style, focused bool, width, height int) lipgloss.Style {
	if focused {
		style = style.BorderForeground(accent)
	}
	return style.
		Width(width - style.GetHorizontalBorderSize()).
		Height(height - style.GetVerticalBorderSize())
}
