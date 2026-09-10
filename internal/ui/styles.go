package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
)

const sidebarWidth = 28

var (
	accent = lipgloss.Color("69")  // focused border / cursor: brighter than before, to carry against the black pane ground
	dim    = lipgloss.Color("240") // hints and the status bar
	edge   = lipgloss.Color("245") // unfocused border: lifted off dim so an inactive pane keeps a visible edge
	rule   = lipgloss.Color("245") // dividers between panes
	bar    = lipgloss.Color("238") // unfocused pane header bar: a step above the black ground, so it reads as the pane's top edge

	// tm's own chrome sits on the same ground the panes are drawn on, taken
	// from the same constant, so the sidebar, the gaps between splits and a
	// pane's own output read as one surface rather than three.
	ground = lipgloss.Color(terminal.PaneGround)

	// The main pane has no padding so the emulated screen fills it exactly.
	// The border is thick to match the dividers between split panes, which are
	// heavy box-drawing characters; a light frame around heavy dividers looks
	// like two different tools drew the screen.
	mainStyle    = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(edge).Background(ground)
	sidebarStyle = mainStyle.Padding(0, 1)

	titleStyle = lipgloss.NewStyle().Bold(true)

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(accent).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)

	hintStyle = lipgloss.NewStyle().Foreground(dim)

	// Pane headers are filled bars: they are a pane's only top edge, so they
	// are drawn across its full width whether it has focus or not.
	paneBarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(bar)

	dividerStyle = lipgloss.NewStyle().Foreground(rule)

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
