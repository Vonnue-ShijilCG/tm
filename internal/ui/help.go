package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// viewHelp renders every binding from helpSections in as few columns as fit
// the available height (two at least), clipped so the layout never overflows.
//
// Columns are sized to their own content, and each column's key gutter to its
// own longest key, because an equal split is decided by the widest column in
// the set and every other column pays for it. At 100x30 that is the
// difference between every label reading whole and every label truncated.
//
// Nothing here wraps behind the layout's back. Lip Gloss wraps rather than
// clipping, so one over-long row becomes two and the screen grows past the
// window — which is what a narrow terminal used to do here. Rows and section
// titles are both cut. The one deliberate wrap is a description longer than
// helpDescWidth, which descLines breaks into rows that every count of the
// screen's height and width already includes.
//
// The screen has no slack left: at 80x24 the sections now need five columns,
// and titles are cut to fit them. Another section would have to come with a
// way to scroll or page this screen.
func viewHelp(width, height int) string {
	return viewHelpFor(helpSectionsFor(false), width, height)
}

// viewHelpFor is viewHelp for the given sections: with a tm server, the
// sidebar's quit keys read differently.
func viewHelpFor(sections []helpSection, width, height int) string {
	bodyHeight := max(height-2, 1) // title + blank line
	columns := distribute(sections, bodyHeight)
	gapWidth := 2
	widths := columnWidths(columns, width-gapWidth*(len(columns)-1))

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
		rendered = append(rendered, renderSections(col, widths[i], tallest))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, rendered...)

	out := titleStyle.Render("tm — keyboard shortcuts") + "\n\n" + body
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Cut every row to the frame. Lip Gloss wraps an over-long line rather
	// than clipping it, so one row too wide becomes two and the whole screen
	// grows past the window — which is what a narrow terminal used to do here.
	for i, row := range rows {
		rows[i] = ansi.Truncate(row, width, "")
	}
	if height > 0 && len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

// helpDescWidth is the most a description takes on one row: the longest
// one-line description, "manage terminals". A longer one continues on the
// rows below it (descLines). Without that, the server's "leave (terminals
// keep running)" would widen the sidebar's column by fourteen cells, which
// at 100x30 the other columns would pay for with a truncated label each.
const helpDescWidth = 16

// descLines breaks desc at spaces into rows of at most helpDescWidth cells;
// a single word longer than that keeps its row and is cut like any other.
func descLines(desc string) []string {
	if lipgloss.Width(desc) <= helpDescWidth {
		return []string{desc}
	}
	var lines []string
	cur := ""
	for _, w := range strings.Fields(desc) {
		switch {
		case cur == "":
			cur = w
		case lipgloss.Width(cur+" "+w) > helpDescWidth:
			lines = append(lines, cur)
			cur = w
		default:
			cur += " " + w
		}
	}
	return append(lines, cur)
}

// columnWidths sizes each column to its own content rather than dividing the
// space equally. Sections differ in how long their labels are, and an equal
// split is decided by the widest column in the set: with four columns at
// 100 cells that left 22 each, one cell short of "alt+del  delete forever",
// so a label was truncated in every column to fit the one that needed the
// room. Columns keep their natural width while the total fits, and are cut
// back proportionally when it does not.
func columnWidths(columns [][]helpSection, budget int) []int {
	widths := make([]int, len(columns))
	total := 0
	for i, col := range columns {
		keyWidth := helpKeyWidth(col)
		for _, s := range col {
			widths[i] = max(widths[i], lipgloss.Width(s.Title))
			for _, b := range s.Bindings {
				for _, line := range descLines(b.Help().Desc) {
					widths[i] = max(widths[i], keyWidth+1+lipgloss.Width(line))
				}
			}
		}
		total += widths[i]
	}
	if total <= budget || total == 0 {
		return widths
	}
	// Too wide for the window: every column gives up the same share, and the
	// rows renderSections draws are truncated to what is left.
	left := budget
	for i := range widths {
		w := max(widths[i]*budget/total, 8)
		widths[i] = min(w, max(left-(len(widths)-i-1)*8, 8))
		left -= widths[i]
	}
	return widths
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

// sectionRows is the lines a section takes: its title plus its bindings,
// each as many rows as its description wraps to.
func sectionRows(s helpSection) int {
	rows := 1
	for _, b := range s.Bindings {
		rows += len(descLines(b.Help().Desc))
	}
	return rows
}

// columnRows is the lines a column takes as renderSections lays it out: its
// sections, with one blank line between each pair.
func columnRows(sections []helpSection) int {
	rows := 0
	for _, s := range sections {
		rows += sectionRows(s)
	}
	return rows + max(len(sections)-1, 0)
}

// helpKeyWidth is the width of a column's key gutter: its own longest key,
// with a floor so a column of short keys still reads as a column. Sizing it
// per column rather than once for the whole screen is what makes room for
// another section — "shift+tab / ↑" is thirteen cells and used to be paid for
// by every column, including ones whose longest key is "alt+d".
func helpKeyWidth(sections []helpSection) int {
	w := 7
	for _, s := range sections {
		for _, b := range s.Bindings {
			w = max(w, lipgloss.Width(b.Help().Key))
		}
	}
	return w
}

// renderSections lays one column out as a width x height block on tm's
// ground: the padding after each line and below the last section is drawn
// by a style that carries the ground, so it does not fall through to the
// terminal's colour after a styled run's reset.
func renderSections(sections []helpSection, width, height int) string {
	var b strings.Builder
	keyWidth := helpKeyWidth(sections)
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		// Truncated, not wrapped: Lip Gloss wraps, and a title split across two
		// rows pushes every line below it down and out of the column.
		b.WriteString(selectedStyle.Render(ansi.Truncate(s.Title, width, "")) + "\n")
		for _, binding := range s.Bindings {
			h := binding.Help()
			for i, line := range descLines(h.Desc) {
				k := h.Key
				if i > 0 {
					k = "" // a continuation sits under the description, not the key
				}
				b.WriteString(ansi.Truncate(fmt.Sprintf("%-*s %s", keyWidth, k, hintStyle.Render(line)), width, "…") + "\n")
			}
		}
	}
	return textStyle.Width(width).MaxWidth(width).Height(height).Render(strings.TrimRight(b.String(), "\n"))
}
