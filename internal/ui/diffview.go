package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

	top    int // first visible row of the diff
	left   int // horizontal offset, shared by both columns
	widest int // cells in the widest row text on either side; left stops there
	change int // row of the change the last jump landed on; -1 once the view has moved by other means

	listing bool // the file list is being read
	reading bool // the selected file is being read

	// listSeq and fileSeq count requests. A command carries the value it was
	// issued with and drops its result once the counter has moved past it —
	// and, for a file, does not spawn git at all. They are atomic because the
	// commands run off the event loop while Update writes the view.
	listSeq *atomic.Int64
	fileSeq *atomic.Int64

	keep *keepPlace // set by a refresh: where to put the view back when the results land
}

// keepPlace is what a refresh remembers across the two reads it triggers. The
// selection follows its path rather than its index, so the file being watched
// stays selected however the list around it grew or shrank; its scroll
// position comes back with it.
type keepPlace struct {
	path      string
	top, left int
	found     bool // the path was still in the list, so top and left still mean something
}

// Results of the two reads, delivered to Update like any other message.
type (
	diffListMsg struct {
		seq   int64
		files []git.FileChange
		err   string
	}
	diffFileMsg struct {
		seq     int64
		rows    []diff.Row
		widest  int
		loadErr string
	}
)

const (
	diffListWidth   = 32
	verticalDivider = "│"
	// gutterWidth is the line-number column and the space after it.
	gutterWidth = 5
	// diffChromeRows are the rows above the code: the header, a blank line
	// and the headings. The key handler subtracts them from the window to
	// learn how many code rows are on screen, so scrolling can clamp.
	diffChromeRows = 3
	// diffHScrollStep is how far one left/right press moves. Whole words
	// rather than single columns, because a line of code is scanned in words.
	diffHScrollStep = 8
	// diffJumpContext is how many rows a jump leaves above the change it
	// lands on, so the change is read with what precedes it.
	diffJumpContext = 2
)

// diffLoadDelay holds a file read back so a held-down j does not spawn two
// git processes for every file it passes over. The read that survives is the
// one for the file the cursor settled on; the rest never run.
const diffLoadDelay = 60 * time.Millisecond

// newDiffView opens on dir and returns the command that reads it. The view
// appears at once and fills in when git answers: every other key handler in
// tm returns immediately, and `git status` on a large repository takes long
// enough to be felt as a freeze. A directory that is not a git repository, or
// a git that fails, is reported in the view rather than preventing it from
// opening.
func newDiffView(dir, title string) (*diffView, tea.Cmd) {
	d := &diffView{dir: dir, title: title, change: -1, listSeq: &atomic.Int64{}, fileSeq: &atomic.Int64{}}
	return d, d.readCmd()
}

// readCmd lists the directory's changes from scratch.
func (d *diffView) readCmd() tea.Cmd {
	d.listing, d.err = true, ""
	dir, counter := d.dir, d.listSeq
	seq := counter.Add(1)
	return func() tea.Msg {
		files, err := git.Changes(dir)
		if counter.Load() != seq {
			return nil // the view moved on, or closed
		}
		if err != nil {
			return diffListMsg{seq: seq, err: err.Error()}
		}
		return diffListMsg{seq: seq, files: files}
	}
}

// applyList takes a readCmd's answer and asks for the file it selects.
func (d *diffView) applyList(msg diffListMsg) tea.Cmd {
	d.listing, d.files, d.err, d.cursor = false, msg.files, msg.err, 0
	if k := d.keep; k != nil {
		for i, f := range d.files {
			if f.Path == k.path {
				d.cursor, k.found = i, true
				break
			}
		}
	}
	return d.loadCmd(0)
}

// applyFile takes a loadCmd's answer. A refresh puts the view back where it
// was, but only while the offset still shows something: a line that shrank
// past it would leave both columns blank — from the one key whose job is to
// show what changed. The caller clamps top, since the file may have got
// shorter.
func (d *diffView) applyFile(msg diffFileMsg) {
	d.reading, d.rows, d.widest, d.loadErr = false, msg.rows, msg.widest, msg.loadErr
	d.top, d.left, d.change = 0, 0, -1
	if k := d.keep; k != nil {
		if k.found {
			d.top = k.top
			if k.left < d.widest {
				d.left = k.left
			}
		}
		d.keep = nil
	}
}

// refresh re-reads the directory: tm does not follow an agent's edits live,
// so this is how the user asks for what has been written since. The
// selection follows its path rather than its index, and its scroll position
// survives, so re-reading the file being watched does not lose the place in
// it even when the list above it has grown or shrunk. The caller clamps top,
// since the file may have got shorter; the sideways offset is kept only while
// it still shows something, because a line that shrank past it would leave
// both columns blank — from the one key whose job is to show what changed.
func (d *diffView) refresh() tea.Cmd {
	d.keep = &keepPlace{top: d.top, left: d.left}
	if len(d.files) > 0 {
		d.keep.path = d.files[d.cursor].Path
	}
	return d.readCmd()
}

// scroll moves the diff vertically, clamping so the view cannot run past
// either end of the file; height is the number of code rows on screen. A
// delta of 0 only re-clamps, after a re-read or a resize, and leaves the
// last jump in place; scrolling by hand abandons it.
func (d *diffView) scroll(delta, height int) {
	if delta != 0 {
		d.change = -1
	}
	d.top = min(max(d.top+delta, 0), max(len(d.rows)-height, 0))
}

// scrollX moves both columns sideways together; there is one offset, so they
// stay showing the same columns of the same lines. It stops at the widest
// line: past it there is nothing to see, and every press further would be
// one more to come back.
func (d *diffView) scrollX(delta int) {
	d.left = min(max(d.left+delta, 0), d.widest)
}

// jumpChange moves to the next (dir 1) or previous (dir -1) run of changed
// rows. Without it a single edit in a long file would have to be found by
// scrolling, which is what makes a full-file view practical. Each press
// continues from the change the last one landed on: a jump leaves context
// above its change, so searching from top would find the same change again
// and never move on. With no jump to continue from, the top row of the
// screen is the origin — searched itself going down, so a fresh view reaches
// a change on its first rows, and skipped going up, since it is in view.
func (d *diffView) jumpChange(dir, height int) {
	start := d.top
	switch {
	case d.change >= 0:
		start = d.change + dir
	case dir < 0:
		start = d.top - 1
	}
	for i := start; i >= 0 && i < len(d.rows); i += dir {
		if d.rows[i].Changed && (i == 0 || !d.rows[i-1].Changed) {
			d.change = i
			d.top = min(max(i-diffJumpContext, 0), max(len(d.rows)-height, 0))
			return
		}
	}
}

// move changes the selected file, clamping at both ends, and asks for the file
// it lands on so the columns always show the selection.
func (d *diffView) move(delta int) tea.Cmd {
	if len(d.files) == 0 {
		return nil
	}
	next := min(max(d.cursor+delta, 0), len(d.files)-1)
	if next == d.cursor {
		return nil
	}
	d.cursor = next
	return d.loadCmd(diffLoadDelay)
}

// loadCmd asks for the selected file's committed and working versions. delay
// holds the read back so a held-down j does not spawn two git processes for
// every file it passes over: after waiting, a command that finds the cursor
// has moved on returns without running git at all.
func (d *diffView) loadCmd(delay time.Duration) tea.Cmd {
	d.rows, d.loadErr, d.top, d.left, d.widest, d.change = nil, "", 0, 0, 0, -1
	counter := d.fileSeq
	seq := counter.Add(1)
	if len(d.files) == 0 {
		// No file will land, so nothing is left to restore a place into; a
		// refresh's plan must not survive to move the next file that loads.
		d.reading, d.keep = false, nil
		return nil
	}
	d.reading = true
	dir, f := d.dir, d.files[d.cursor]
	return func() tea.Msg {
		if delay > 0 {
			time.Sleep(delay)
			if counter.Load() != seq {
				return nil // the cursor moved on; no git for a file nobody is looking at
			}
		}
		msg := readFile(dir, f)
		if counter.Load() != seq {
			return nil
		}
		msg.seq = seq
		return msg
	}
}

// readFile reads one file's committed and working versions and aligns them.
// It touches nothing on the view, because it runs off the event loop.
//
// A file with no committed version — one just created — aligns against an
// empty left side, which renders as all additions.
func readFile(dir string, f git.FileChange) diffFileMsg {
	if f.Binary {
		return diffFileMsg{loadErr: "binary file"}
	}
	// A renamed file's committed content lives at the path it came FROM; the
	// destination has nothing in HEAD. Without this a move would render as a
	// whole-file addition, hiding whatever actually changed in it.
	from := f.Path
	if f.OldPath != "" {
		from = f.OldPath
	}
	old, found, err := git.ShowHEAD(dir, from)
	if err != nil {
		return diffFileMsg{loadErr: err.Error()}
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
			return diffFileMsg{loadErr: "in neither HEAD nor the working tree: staged as new, then removed"}
		}
		return diffFileMsg{loadErr: fmt.Sprintf("cannot read %s from HEAD", from)}
	}
	fresh := ""
	if f.Kind != git.Deleted {
		data, err := os.ReadFile(filepath.Join(dir, f.Path))
		if err != nil {
			return diffFileMsg{loadErr: err.Error()}
		}
		fresh = string(data)
	}
	// Both paths, when this is a rename: git only detects a rename if both
	// sides are inside the pathspec. With the destination alone it reports a
	// whole-file addition, which would contradict the committed content
	// ShowHEAD just returned and misalign the two columns.
	unified, err := git.DiffHEAD(dir, f.Path, f.OldPath)
	if err != nil {
		return diffFileMsg{loadErr: err.Error()}
	}
	hunks := diff.ParseHunks(unified)
	// An untracked file has no diff at all — git diff HEAD ignores it — so
	// there are no hunks and Align would show it as unchanged. One hunk
	// covering the whole file says what git would have said had it been added.
	lines := splitLines(fresh)
	if f.Kind == git.Added && len(hunks) == 0 {
		hunks = []diff.Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: len(lines)}}
	}
	msg := diffFileMsg{rows: diff.Align(splitLines(old), lines, hunks)}
	for _, r := range msg.rows {
		msg.widest = max(msg.widest, lipgloss.Width(plainText(r.OldText)), lipgloss.Width(plainText(r.NewText)))
	}
	return msg
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
		// exception …"); only the first fits the one row budgeted for it. It
		// is git's stderr, which echoes repository paths, so it is stripped
		// like every other repository-derived string here.
		first, _, _ := strings.Cut(d.err, "\n")
		lines = append(lines, " "+errorStyle.Render(ansi.Truncate(stripControlChars(first), width-1, "…")))
	case d.listing:
		lines = append(lines, " "+hintStyle.Render(ansi.Truncate("Reading "+d.dir+"…", width-1, "…")))
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
// per row and at most height of them, starting at row top. Both columns are
// the same width and show the same rows, so a line always faces its
// counterpart, and one offset scrolls them together.
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
	if d.reading {
		// Under the labels rather than instead of them: the columns keep
		// their headings, so only the code area changes while git answers.
		return append(lines, hintStyle.Render(" reading…"))
	}
	for i := d.top; i < len(d.rows) && len(lines) < height; i++ {
		r := d.rows[i]
		lines = append(lines, side(r.OldNum, r.OldText, r.Changed, delStyle, half, d.left)+textStyle.Render(" ")+
			side(r.NewNum, r.NewText, r.Changed, addStyle, half, d.left))
	}
	return lines
}

// side renders one column of one row: a line number gutter, then the text
// from cell left onwards, coloured when the line is part of a change and
// blank when it is padding. A long line is cut, never wrapped: wrapped, it
// would take two rows on one side and one on the other, and the columns
// would drift apart from there.
func side(num int, text string, changed bool, style lipgloss.Style, width, left int) string {
	gutter := strings.Repeat(" ", gutterWidth-1)
	if num > 0 {
		gutter = fmt.Sprintf("%*d", gutterWidth-1, num)
	}
	if !changed || num == 0 {
		style = textStyle
	}
	body := ansi.Truncate(ansi.TruncateLeft(plainText(text), left, ""), max(width-gutterWidth, 0), "")
	return pad(hintStyle.Render(gutter)+style.Render(" "+body), width)
}

// plainText is a file's line as it is measured and drawn. Tabs become spaces
// before anything is cut: Truncate counts a tab as no cells while Render
// draws it as four, so a line cut with tabs still in it comes out wider than
// its column and shoves the other column along. Every other control
// character goes, after the tabs so indentation survives: a CRLF file keeps
// a \r on every line that Truncate, Width and pad all count as nothing, yet
// drawn it returns the cursor to column 0 and the right column lands on the
// file list; an escape sequence in a file would be executed by the terminal
// rather than shown.
func plainText(text string) string {
	return stripControlChars(strings.ReplaceAll(text, "\t", "    "))
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
// d.title here and d.dir in view are exempt from stripControlChars: they name
// where tm runs — a workspace child directory, a branch name (git refuses
// control characters in refs), a tm-sanitised worktree path — not what is
// inside the repository, which is the boundary every other string in this
// view is stripped at.
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

// list renders a heading and the changed files in at most height lines.
// Entries are dropped from the top only when the selected one would otherwise
// not fit, so the selection is always on screen. The heading shares a row with
// the column labels, so the first file and the first code row line up.
func (d *diffView) list(width, height int) []string {
	height-- // the heading's row
	entries := make([][]string, len(d.files))
	for i, f := range d.files {
		entries[i] = d.entry(f, i == d.cursor, width)
	}
	return append([]string{"  " + selectedStyle.Render("FILES")}, visibleEntries(entries, d.cursor, height)...)
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
