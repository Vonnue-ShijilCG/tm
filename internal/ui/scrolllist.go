package ui

// Every list tm draws is taller than its window sooner or later: a workspace
// with more repositories than the terminal has rows, a project with more
// terminals than the Terminal Manager can show. Lip Gloss pads a short block
// up to its height but never cuts a long one, so a list that ignores the
// height does not clip — it pushes the status bar off the screen and takes the
// selected row with it.
//
// visibleEntries is the one answer to that, shared by the sidebar, the
// Terminal Manager and the changes view's file list. An entry is a group of
// lines that belong together (a wrapped project name, a terminal's two rows),
// and groups are never split across the edge.

// visibleEntries is the lines to draw for entries in height rows. Entries are
// dropped from the top only when the selected one would otherwise not fit, so
// the selection is always on screen: a list shorter than the window starts at
// its first entry, and a selection near the end sits on the last row with as
// much of what precedes it as fits above.
func visibleEntries(entries [][]string, selected, height int) []string {
	if height <= 0 {
		return nil
	}
	var lines []string
	for _, e := range entries[visibleStart(entries, selected, height):] {
		if len(lines)+len(e) > height {
			break
		}
		lines = append(lines, e...)
	}
	return lines
}

// visibleStart is the first entry visibleEntries draws: entries are dropped
// from the top only while the selected one would not otherwise fit. A click
// is mapped to a row through it, so the two can never disagree.
func visibleStart(entries [][]string, selected, height int) int {
	used := 0
	for i := 0; i <= selected && i < len(entries); i++ {
		used += len(entries[i])
	}
	start := 0
	for used > height && start < selected {
		used -= len(entries[start])
		start++
	}
	return start
}
