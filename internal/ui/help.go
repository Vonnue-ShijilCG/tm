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
	gap := 2
	colWidth := max((width-gap*(len(columns)-1))/len(columns), 16)

	rendered := make([]string, 0, 2*len(columns))
	for i, col := range columns {
		if i > 0 {
			rendered = append(rendered, strings.Repeat(" ", gap))
		}
		rendered = append(rendered, renderSections(col, colWidth))
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
	rows := func(s helpSection) int { return len(s.Bindings) + 1 } // title + bindings
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

// helpKeyWidth is the key column width, sized for the longest label.
const helpKeyWidth = 13

func renderSections(sections []helpSection, width int) string {
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
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(strings.TrimRight(b.String(), "\n"))
}
