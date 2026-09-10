package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/diff"
	"github.com/Vonnue-ShijilCG/tm/internal/git"
)

// diffView shows the uncommitted changes of one directory: the files that
// differ from HEAD, and the selected file rendered as it was committed beside
// how it is now. tm reads the list when the view opens and a file when it is
// selected; it does not follow an agent's edits live, so the content cannot
// shift while it is being read.
type diffView struct {
	dir     string // the directory whose changes these are
	title   string // project name, and branch when there is one
	files   []git.FileChange
	cursor  int        // selected file
	err     string     // git problem, e.g. not a repository
	rows    []diff.Row // the selected file, aligned; nil until loaded
	loadErr string     // problem reading the selected file
}

const (
	diffListWidth   = 32
	verticalDivider = "│"
	// gutterWidth is the line-number column and the space after it.
	gutterWidth = 5
)

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
	d.load()
	return d
}

// move changes the selected file, clamping at both ends, and loads the file
// it lands on so the columns always show the selection.
func (d *diffView) move(delta int) {
	if len(d.files) == 0 {
		return
	}
	next := min(max(d.cursor+delta, 0), len(d.files)-1)
	if next == d.cursor {
		return
	}
	d.cursor = next
	d.load()
}

// load reads the selected file's committed and working versions and aligns
// them. A file with no committed version — one just created — aligns against
// an empty left side, which renders as all additions.
func (d *diffView) load() {
	d.rows, d.loadErr = nil, ""
	if len(d.files) == 0 {
		return
	}
	f := d.files[d.cursor]
	if f.Binary {
		d.loadErr = "binary file"
		return
	}
	// A renamed file's committed content lives at the path it came FROM; the
	// destination has nothing in HEAD. Without this a move would render as a
	// whole-file addition, hiding whatever actually changed in it.
	from := f.Path
	if f.OldPath != "" {
		from = f.OldPath
	}
	old, found, err := git.ShowHEAD(d.dir, from)
	if err != nil {
		d.loadErr = err.Error()
		return
	}
	// ShowHEAD folds every git error into "not in HEAD". Only a new file is
	// allowed that answer: for any other, aligning against an empty left side
	// would draw a plausible whole-file addition where a failure belongs.
	if !found && f.Kind != git.Added {
		if f.Kind == git.Deleted {
			// Staged as new, then removed without git rm: status calls it a
			// deletion, but HEAD never had it. Nothing failed; there is
			// simply nothing on either side, and an empty pane under the
			// two labels would read as an empty file.
			d.loadErr = "in neither HEAD nor the working tree: staged as new, then removed"
		} else {
			d.loadErr = fmt.Sprintf("cannot read %s from HEAD", from)
		}
		return
	}
	fresh := ""
	if f.Kind != git.Deleted {
		data, err := os.ReadFile(filepath.Join(d.dir, f.Path))
		if err != nil {
			d.loadErr = err.Error()
			return
		}
		fresh = string(data)
	}
	// Both paths, when this is a rename: git only detects a rename if both
	// sides are inside the pathspec. With the destination alone it reports a
	// whole-file addition, which would contradict the committed content
	// ShowHEAD just returned and misalign the two columns.
	unified, err := git.DiffHEAD(d.dir, f.Path, f.OldPath)
	if err != nil {
		d.loadErr = err.Error()
		return
	}
	hunks := diff.ParseHunks(unified)
	// An untracked file has no diff at all — git diff HEAD ignores it — so
	// there are no hunks and Align would show it as unchanged. One hunk
	// covering the whole file says what git would have said had it been added.
	lines := splitLines(fresh)
	if f.Kind == git.Added && len(hunks) == 0 {
		hunks = []diff.Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: len(lines)}}
	}
	d.rows = diff.Align(splitLines(old), lines, hunks)
}

// splitLines splits file content into lines without a trailing empty element.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
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
		lines = append(lines, d.body(width, height-len(lines))...)
	}
	return strings.Join(lines, "\n")
}

// body is the file list, a divider and the two code columns, exactly height
// rows. Rows are assembled by hand rather than with JoinHorizontal so every
// cell, including the padding that squares the list off, carries the ground.
func (d *diffView) body(width, height int) []string {
	divider := dividerStyle.Render(" " + verticalDivider + " ")
	list := d.list(diffListWidth, height)
	panes := d.panes(width-diffListWidth-lipgloss.Width(divider), height)
	lines := make([]string, 0, height)
	for i := 0; i < height; i++ {
		entry, pane := "", ""
		if i < len(list) {
			entry = list[i]
		}
		if i < len(panes) {
			pane = panes[i]
		}
		// The columns clip their own lines; this cut only guards a window too
		// narrow to hold the list at all, where a wrap would cost a row.
		lines = append(lines, ansi.Truncate(pad(entry, diffListWidth)+divider+pane, width, ""))
	}
	return lines
}

// panes renders the committed and current versions side by side, one string
// per row and at most height of them. Both columns are the same width and
// show the same rows, so a line always faces its counterpart.
func (d *diffView) panes(width, height int) []string {
	if d.loadErr != "" {
		// The error can embed a path the OS echoed back, so it is file-derived
		// text like everything else on this side of the divider.
		return []string{hintStyle.Render(ansi.Truncate(stripControlChars(d.loadErr), max(width, 1), "…"))}
	}
	half := (width - 1) / 2
	labels := pad(delStyle.Render("HEAD")+hintStyle.Render("  committed"), half) + textStyle.Render(" ") +
		addStyle.Render("WORKING TREE") + hintStyle.Render("  yours now")
	lines := []string{ansi.Truncate(labels, max(width, 1), "")}
	for _, r := range d.rows {
		if len(lines) >= height {
			break
		}
		lines = append(lines, side(r.OldNum, r.OldText, r.Changed, delStyle, half)+textStyle.Render(" ")+
			side(r.NewNum, r.NewText, r.Changed, addStyle, half))
	}
	return lines
}

// side renders one column of one row: a line number gutter, then the text,
// coloured when the line is part of a change and blank when it is padding.
// A long line is cut, never wrapped: wrapped, it would take two rows on one
// side and one on the other, and the columns would drift apart from there.
func side(num int, text string, changed bool, style lipgloss.Style, width int) string {
	gutter := strings.Repeat(" ", gutterWidth-1)
	if num > 0 {
		gutter = fmt.Sprintf("%*d", gutterWidth-1, num)
	}
	if !changed || num == 0 {
		style = textStyle
	}
	// Tabs become spaces before the line is cut: Truncate counts a tab as no
	// cells while Render draws it as four, so a line cut with tabs still in
	// it comes out wider than its column and shoves the other column along.
	// Every other control character goes, after the tabs so indentation
	// survives: a CRLF file keeps a \r on every line that Truncate, Width
	// and pad all count as nothing, yet drawn it returns the cursor to column
	// 0 and the right column lands on the file list; an escape sequence in a
	// file would be executed by the terminal rather than shown.
	text = stripControlChars(strings.ReplaceAll(text, "\t", "    "))
	body := ansi.Truncate(text, max(width-gutterWidth, 0), "")
	return pad(hintStyle.Render(gutter)+style.Render(" "+body), width)
}

// pad fills s to width cells with grounded spaces, so a short line leaves no
// bare cells where the terminal's own background would show through.
func pad(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + textStyle.Render(strings.Repeat(" ", gap))
	}
	return s
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
// line counts. Paths are stripped of control characters like every other
// file-derived text in the view; git quotes them by default, but the view
// should not depend on a setting it does not control.
func (d *diffView) entry(f git.FileChange, selected bool, width int) []string {
	marker, style := "  ", textStyle
	if selected {
		marker, style = "› ", selectedStyle
	}
	path := stripControlChars(f.Path)
	lines := []string{marker + style.Render(ansi.Truncate(filepath.Base(path), width-3, "…"))}
	if dir := filepath.Dir(path); dir != "." {
		lines = append(lines, "    "+hintStyle.Render(ansi.Truncate(dir, width-5, "…")))
	}
	if f.OldPath != "" {
		lines = append(lines, "    "+hintStyle.Render(ansi.Truncate("from "+stripControlChars(f.OldPath), width-5, "…")))
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
