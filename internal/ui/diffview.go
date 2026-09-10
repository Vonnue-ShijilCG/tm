package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
)

// diffView shows the uncommitted changes of one directory: the files that
// differ from HEAD, and the selected file rendered as it was committed beside
// how it is now. tm reads this when the view opens; it does not follow an
// agent's edits live, so the content cannot shift while it is being read.
type diffView struct {
	dir    string // the directory whose changes these are
	title  string // project name, and branch when there is one
	files  []git.FileChange
	cursor int    // selected file
	err    string // git problem, e.g. not a repository
}

const diffListWidth = 32

// newDiffView reads the changes in dir. A directory that is not a git
// repository, or a git that fails, is reported in the view rather than
// preventing it from opening.
func newDiffView(dir, title string) *diffView {
	d := &diffView{dir: dir, title: title}
	files, err := git.Changes(dir)
	if err != nil {
		d.err = err.Error()
		return d
	}
	d.files = files
	return d
}

// move changes the selected file, clamping at both ends.
func (d *diffView) move(delta int) {
	if len(d.files) == 0 {
		return
	}
	d.cursor = min(max(d.cursor+delta, 0), len(d.files)-1)
}

// view renders at most height lines: the frame it sits in pads to its height
// but never clips, so anything longer would push the status bar off screen.
func (d *diffView) view(width, height int) string {
	lines := []string{d.header(width), ""}
	switch {
	case d.err != "":
		// Some git failures are several lines ("dubious ownership … To add an
		// exception …"); only the first fits the one row budgeted for it.
		first, _, _ := strings.Cut(d.err, "\n")
		lines = append(lines, " "+errorStyle.Render(ansi.Truncate(first, width-1, "…")))
	case len(d.files) == 0:
		lines = append(lines, " "+hintStyle.Render(ansi.Truncate("No uncommitted changes in "+d.dir, width-1, "…")))
	default:
		lines = append(lines, d.list(diffListWidth, height-len(lines))...)
	}
	return strings.Join(lines, "\n")
}

// header names what is being shown and, when there is something, totals it.
// It is cut to one line: Lip Gloss wraps an over-long line rather than
// clipping it, and a second header row would push the status bar off the
// window. The totals are short and fixed, so the branch name gives way.
func (d *diffView) header(width int) string {
	left := titleStyle.Render("Uncommitted changes") + textStyle.Render("  "+d.title)
	if len(d.files) == 0 {
		return " " + ansi.Truncate(left, max(width-1, 1), "…")
	}
	added, deleted := 0, 0
	for _, f := range d.files {
		added += f.Added
		deleted += f.Deleted
	}
	right := hintStyle.Render(fmt.Sprintf("%d file%s  +%d  -%d", len(d.files), plural(len(d.files)), added, deleted))
	left = ansi.Truncate(left, max(width-lipgloss.Width(right)-3, 1), "…") // leading space, one gap cell, trailing cell
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return " " + left + textStyle.Render(strings.Repeat(" ", gap)) + right
}

// list renders the changed files in at most height lines. Entries are dropped
// from the top only when the selected one would otherwise not fit, so the
// selection is always on screen.
func (d *diffView) list(width, height int) []string {
	entries := make([][]string, len(d.files))
	used := 0 // lines from the first entry through the selected one
	for i, f := range d.files {
		entries[i] = d.entry(f, i == d.cursor, width)
		if i <= d.cursor {
			used += len(entries[i])
		}
	}
	start := 0
	for used > height && start < d.cursor {
		used -= len(entries[start])
		start++
	}
	var lines []string
	for _, e := range entries[start:] {
		if len(lines)+len(e) > height {
			break
		}
		lines = append(lines, e...)
	}
	return lines
}

// entry is one file: basename first with the directory beneath, so a long
// path is never truncated mid-word, then where a rename came from and the
// line counts.
func (d *diffView) entry(f git.FileChange, selected bool, width int) []string {
	marker, style := "  ", textStyle
	if selected {
		marker, style = "› ", selectedStyle
	}
	lines := []string{marker + style.Render(ansi.Truncate(filepath.Base(f.Path), width-3, "…"))}
	if dir := filepath.Dir(f.Path); dir != "." {
		lines = append(lines, "    "+hintStyle.Render(ansi.Truncate(dir, width-5, "…")))
	}
	if f.OldPath != "" {
		lines = append(lines, "    "+hintStyle.Render(ansi.Truncate("from "+f.OldPath, width-5, "…")))
	}
	return append(lines, "    "+d.counts(f))
}

// counts is one file's summary: line counts, or what makes it special.
func (d *diffView) counts(f git.FileChange) string {
	switch {
	case f.Binary:
		return hintStyle.Render("binary")
	case f.Kind == git.Deleted:
		return delStyle.Render("deleted")
	case f.Kind == git.Added:
		return addStyle.Render(fmt.Sprintf("+%d", f.Added)) + hintStyle.Render("  new file")
	default:
		return addStyle.Render(fmt.Sprintf("+%d ", f.Added)) + delStyle.Render(fmt.Sprintf("-%d", f.Deleted))
	}
}
