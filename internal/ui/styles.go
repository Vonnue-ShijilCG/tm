package ui

import "github.com/charmbracelet/lipgloss"

const sidebarWidth = 28

var (
	accent = lipgloss.Color("62")  // focused border / cursor
	dim    = lipgloss.Color("240") // unfocused border / hints

	// The main pane has no padding so the emulated screen fills it exactly.
	mainStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(dim)
	sidebarStyle = mainStyle.Padding(0, 1)

	titleStyle = lipgloss.NewStyle().Bold(true)

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(accent).
			Bold(true)

	selectedStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)

	hintStyle = lipgloss.NewStyle().Foreground(dim)

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
