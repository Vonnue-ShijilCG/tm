package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// viewHelp renders every binding from helpSections in as few columns as
// fit the available height (two at least), clipped so the layout never
// overflows.
func viewHelp(width, height int) string {
	sections := helpSections()
	bodyHeight := max(height-2, 1) // title + blank line
	columns := distribute(sections, bodyHeight)
	gapWidth := 2
	colWidth := max((width-gapWidth*(len(columns)-1))/len(columns), 16)

	// Every column and every gap is a grounded block of the same height.
	// JoinHorizontal pads a shorter block with bare spaces, which would leave
	// the cells under a short column, and under a one-line gap, in the
	// terminal's own colour.
	tallest := 0
	for _, col := range columns {
		tallest = max(tallest, columnRows(col))
	}
	rendered := make([]string, 0, 2*len(columns))
	for i, col := range columns {
		if i > 0 {
			rendered = append(rendered, gap(gapWidth, tallest))
		}
		rendered = append(rendered, renderSections(col, colWidth, tallest))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)

	out := titleStyle.Render("tm — keyboard shortcuts") + "\n\n" + body
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if height > 0 && len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

// distribute packs sections into the smallest number of columns (2..4)
// whose tallest column fits height, keeping section order. A section costs
// its title plus its bindings, and one blank line separates sections.
func distribute(sections []helpSection, height int) [][]helpSection {
	rows := sectionRows
	for cols := 2; ; cols++ {
		var out [][]helpSection
		var cur []helpSection
		used := 0
		for _, s := range sections {
			if len(cur) > 0 && used+1+rows(s) > height { // +1 for the separating blank line
				out = append(out, cur)
				cur, used = nil, 0
			}
			if len(cur) > 0 {
				used++
			}
			cur = append(cur, s)
			used += rows(s)
		}
		out = append(out, cur)
		if len(out) <= cols || cols == 4 {
			for len(out) < cols && len(out) > 0 { // spread thin content over the minimum columns
				last := out[len(out)-1]
				if len(last) < 2 {
					break
				}
				out[len(out)-1] = last[:len(last)-1]
				out = append(out, last[len(last)-1:])
			}
			return out
		}
	}
}

// sectionRows is the lines a section takes: its title plus its bindings.
func sectionRows(s helpSection) int { return len(s.Bindings) + 1 }

// columnRows is the lines a column takes as renderSections lays it out: its
// sections, with one blank line between each pair.
func columnRows(sections []helpSection) int {
	rows := 0
	for _, s := range sections {
		rows += sectionRows(s)
	}
	return rows + max(len(sections)-1, 0)
}

// helpKeyWidth is the key column width, sized for the longest label.
const helpKeyWidth = 13

// renderSections lays one column out as a width x height block on tm's
// ground: the padding after each line and below the last section is drawn
// by a style that carries the ground, so it does not fall through to the
// terminal's colour after a styled run's reset.
func renderSections(sections []helpSection, width, height int) string {
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(selectedStyle.Render(s.Title) + "\n")
		for _, binding := range s.Bindings {
			h := binding.Help()
			b.WriteString(ansi.Truncate(fmt.Sprintf("%-*s %s", helpKeyWidth, h.Key, hintStyle.Render(h.Desc)), width, "…") + "\n")
		}
	}
	return textStyle.Width(width).MaxWidth(width).Height(height).Render(strings.TrimRight(b.String(), "\n"))
}
