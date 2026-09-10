package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

func TestDOpensTheChangesViewAndListsChangedFiles(t *testing.T) {
	m, _, root := gitModel(t)
	if err := os.WriteFile(filepath.Join(root, "app", "fresh.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	if m.diff == nil {
		t.Fatal("d should open the changes view")
	}
	view := m.View()
	if !strings.Contains(view, "Uncommitted changes") {
		t.Errorf("the view should name itself:\n%s", view)
	}
	if !strings.Contains(view, "fresh.txt") {
		t.Errorf("an untracked file must be listed:\n%s", view)
	}

	m = press(m, "esc")
	if m.diff != nil {
		t.Error("esc should close the changes view")
	}
}

func TestTheChangesViewSaysWhenNothingChanged(t *testing.T) {
	m, _, _ := gitModel(t)

	m = press(m, "d")

	if !strings.Contains(m.View(), "No uncommitted changes") {
		t.Errorf("a clean checkout should say so:\n%s", m.View())
	}
}

// The view is a modal like help: while it is open no key reaches the
// sidebar, the form or a terminal underneath.
func TestTheChangesViewConsumesEveryKey(t *testing.T) {
	m, _, _ := gitModel(t)

	m = press(m, "d", "q", "n")

	if m.quitting {
		t.Error("q inside the changes view must not quit")
	}
	if m.form != nil {
		t.Error("n inside the changes view must not open the form")
	}
	if m.diff == nil {
		t.Error("q and n should have left the changes view open")
	}
}

func TestAltDShowsTheFocusedTerminalsDirectory(t *testing.T) {
	m, _, root := gitModel(t)
	m = makeTerminal(t, m, "n", "", "develop") // a worktree terminal, focused
	worktree := filepath.Join(root, workspace.WorktreesDir, "app", "develop")
	if err := os.WriteFile(filepath.Join(worktree, "only-here.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "alt+d")

	if m.diff == nil {
		t.Fatal("alt+d in a terminal should open the changes view")
	}
	if m.diff.dir != worktree {
		t.Errorf("dir = %q, want the terminal's worktree %q", m.diff.dir, worktree)
	}
	view := m.View()
	if !strings.Contains(view, "only-here.txt") {
		t.Errorf("the worktree's change must be listed, not the project checkout's:\n%s", view)
	}
	if !strings.Contains(view, worktreeMark+"develop") {
		t.Errorf("the title should carry the branch:\n%s", view)
	}
}

func TestTheChangesViewMovesTheSelectionAndClamps(t *testing.T) {
	m, _, root := gitModel(t)
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(root, "app", name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m = press(m, "d")
	if got := selectedFileLine(m.View()); !strings.Contains(got, "a.txt") {
		t.Errorf("the first file should start selected, got %q", got)
	}

	m = press(m, "j")
	if got := selectedFileLine(m.View()); !strings.Contains(got, "b.txt") {
		t.Errorf("j should select the second file, got %q", got)
	}
	m = press(m, "j") // clamp at the last file
	if m.diff.cursor != 1 {
		t.Errorf("cursor past the end = %d, want 1", m.diff.cursor)
	}
	m = press(m, "k", "k") // clamp at the first
	if m.diff.cursor != 0 {
		t.Errorf("cursor before the start = %d, want 0", m.diff.cursor)
	}
}

// selectedFileLine is the one list line carrying the selection marker. The
// modal covers the whole window, so no other chrome can supply the glyph.
func selectedFileLine(view string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "›") {
			return line
		}
	}
	return ""
}

func TestTheChangesViewNamesWhereARenameCameFrom(t *testing.T) {
	m, _, root := gitModel(t)
	app := filepath.Join(root, "app")
	if err := os.WriteFile(filepath.Join(app, "old.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "old.txt"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "add"},
		{"mv", "old.txt", "new.txt"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", app}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	m = press(m, "d")

	view := m.View()
	if !strings.Contains(view, "new.txt") || !strings.Contains(view, "from old.txt") {
		t.Errorf("a rename should show its destination and where it came from:\n%s", view)
	}
}

func TestTheChangesViewKeepsTheSelectionOnScreen(t *testing.T) {
	m, _, root := gitModel(t)
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(root, "app", fmt.Sprintf("f%02d.txt", i)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 12}) // room for a handful of files
	m = next.(Model)

	m = press(m, "d")
	for i := 0; i < 19; i++ {
		m = press(m, "j")
	}

	view := m.View()
	if lines := strings.Count(view, "\n") + 1; lines != m.height {
		t.Errorf("the view is %d lines tall in a %d-line window:\n%s", lines, m.height, view)
	}
	if got := selectedFileLine(view); !strings.Contains(got, "f19.txt") {
		t.Errorf("the selected file must stay on screen, got %q", got)
	}
}

// A project that is not a repository, or a git that fails, is reported inside
// the view instead of stopping it from opening.
func TestTheChangesViewReportsAGitFailure(t *testing.T) {
	m, _ := newTestModel(t) // project paths that do not exist

	m = press(m, "d")

	if m.diff == nil || m.diff.err == "" {
		t.Fatalf("a failing git should open the view with an error, got %+v", m.diff)
	}
	view := m.View()
	if !strings.Contains(view, m.diff.err) {
		t.Errorf("the view should show the git error %q:\n%s", m.diff.err, view)
	}
	if strings.Contains(view, "No uncommitted changes") {
		t.Errorf("a failure must not read as a clean checkout:\n%s", view)
	}
}

func TestTheChangesViewStatusBarListsItsKeys(t *testing.T) {
	m, _, _ := gitModel(t)

	m = press(m, "d")

	status := m.viewStatus()
	for _, want := range []string{"next file", "prev file", "close"} {
		if !strings.Contains(status, want) {
			t.Errorf("status bar should offer %q:\n%s", want, status)
		}
	}
	if strings.Contains(status, "\n") {
		t.Errorf("status bar wraps:\n%s", status)
	}
}

// Lip Gloss's Width wraps an over-long line rather than clipping it. The
// frame pads short content up to its height, so a wrapped header only shows
// once the list fills the budget: then it adds a row and pushes the status
// bar off the window.
func TestTheChangesViewHeaderNeverWrapsInANarrowWindow(t *testing.T) {
	m, _, root := gitModel(t)
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(root, "app", fmt.Sprintf("f%02d.txt", i)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 13}) // narrower than title + project + branch + totals; the list fills the height exactly
	m = next.(Model)

	m = press(m, "d")

	view := m.View()
	if lines := strings.Count(view, "\n") + 1; lines != m.height {
		t.Errorf("the view is %d lines tall in a %d-line window:\n%s", lines, m.height, view)
	}
	if !strings.Contains(view, "20 files") {
		t.Errorf("the totals must survive a narrow window:\n%s", view)
	}
}

func TestTheChangesViewShowsOnlyTheFirstLineOfAGitError(t *testing.T) {
	d := &diffView{title: "app", err: "detected dubious ownership\nTo add an exception run:\n\tgit config --global --add safe.directory x"}

	out := d.view(80, 10)

	if lines := strings.Count(out, "\n") + 1; lines != 3 { // header, blank, one error line
		t.Errorf("a multi-line error rendered as %d lines:\n%s", lines, out)
	}
	if !strings.Contains(out, "dubious ownership") {
		t.Errorf("the first line of the error should be shown:\n%s", out)
	}
}

// gitRun runs a git command in dir and fails the test if it errors.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commitFile writes content to name inside the project checkout and commits
// it, so a later write to the same file is a modification rather than a new
// file.
func commitFile(t *testing.T, root, name, content string) string {
	t.Helper()
	app := filepath.Join(root, "app")
	if err := os.WriteFile(filepath.Join(app, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, app, "add", "-A")
	gitRun(t, app, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base")
	return app
}

// plainLine is the first line of view, colours stripped, that contains want;
// "" when none does.
func plainLine(view, want string) string {
	for _, line := range strings.Split(ansi.Strip(view), "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	return ""
}

func TestTheSelectedFileShowsBothVersionsAligned(t *testing.T) {
	m, _, root := gitModel(t)
	app := commitFile(t, root, "f.txt", "a\nb\nc\n")
	if err := os.WriteFile(filepath.Join(app, "f.txt"), []byte("a\nB CHANGED\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")
	view := m.View()

	// The two versions of line 2 face each other on one row, each behind its
	// own line number; a plain Contains on "b" would be met by tm's chrome.
	if row := plainLine(view, "B CHANGED"); !strings.Contains(row, "2 b ") || !strings.Contains(row, "2 B CHANGED") {
		t.Errorf("committed line 2 should face current line 2 on one row, got %q in:\n%s", row, view)
	}
	if row := plainLine(view, "1 a"); strings.Count(row, "1 a") != 2 {
		t.Errorf("unchanged lines are shown on both sides - this is a full-file view, got %q in:\n%s", row, view)
	}
	if !strings.Contains(view, "HEAD") || !strings.Contains(view, "WORKING TREE") {
		t.Errorf("the two columns should be labelled:\n%s", view)
	}
}

func TestANewFileHasNoCommittedSide(t *testing.T) {
	m, _, root := gitModel(t)
	if err := os.WriteFile(filepath.Join(root, "app", "fresh.txt"), []byte("only\nnew\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	d := m.diff
	if len(d.rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(d.rows), d.rows)
	}
	for i, r := range d.rows {
		if r.OldNum != 0 || r.OldText != "" {
			t.Errorf("row %d has a committed side %+v; a new file has none", i, r)
		}
		if !r.Changed || r.NewText == "" {
			t.Errorf("row %d = %+v, want an added line", i, r)
		}
	}
}

func TestADeletedFileHasNoCurrentSide(t *testing.T) {
	m, _, root := gitModel(t)
	app := commitFile(t, root, "gone.txt", "one\ntwo\n")
	if err := os.Remove(filepath.Join(app, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	d := m.diff
	if d.loadErr != "" || len(d.rows) != 2 {
		t.Fatalf("loadErr %q, %d rows, want 2 rows and no error: %+v", d.loadErr, len(d.rows), d.rows)
	}
	for i, r := range d.rows {
		if r.NewNum != 0 || r.NewText != "" || !r.Changed || r.OldText == "" {
			t.Errorf("row %d = %+v, want a removed line with no current side", i, r)
		}
	}
}

// A renamed file's committed content lives at the path it came from, and git
// only pairs the two sides of a rename when both are in the pathspec. Getting
// either wrong renders a move as a whole-file addition.
func TestARenamedFileIsComparedWithItsSource(t *testing.T) {
	m, _, root := gitModel(t)
	app := commitFile(t, root, "old.txt", "keep\nsame\nedit me\n")
	gitRun(t, app, "mv", "old.txt", "new.txt")
	if err := os.WriteFile(filepath.Join(app, "new.txt"), []byte("keep\nsame\nEDITED\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	d := m.diff
	if d.loadErr != "" || len(d.rows) != 3 {
		t.Fatalf("loadErr %q, %d rows, want 3: %+v", d.loadErr, len(d.rows), d.rows)
	}
	if r := d.rows[0]; r.Changed || r.OldNum != 1 || r.NewNum != 1 || r.OldText != "keep" {
		t.Errorf("row 0 = %+v, want the unchanged first line on both sides", r)
	}
	if r := d.rows[2]; !r.Changed || r.OldText != "edit me" || r.NewText != "EDITED" {
		t.Errorf("row 2 = %+v, want the edit with its committed text beside it", r)
	}
}

func TestMovingTheSelectionShowsThatFile(t *testing.T) {
	m, _, root := gitModel(t)
	for name, content := range map[string]string{"a.txt": "alpha\n", "b.txt": "bravo\n"} {
		if err := os.WriteFile(filepath.Join(root, "app", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m = press(m, "d")
	if view := m.View(); !strings.Contains(view, "alpha") || strings.Contains(view, "bravo") {
		t.Errorf("the first file's content should show, and only its:\n%s", view)
	}

	m = press(m, "j")
	if view := m.View(); !strings.Contains(view, "bravo") || strings.Contains(view, "alpha") {
		t.Errorf("after j the second file's content should show, and only its:\n%s", view)
	}
}

func TestABinaryFileShowsNoColumns(t *testing.T) {
	m, _, root := gitModel(t)
	app := commitFile(t, root, "blob.bin", "\x00\x01\x02\n")
	if err := os.WriteFile(filepath.Join(app, "blob.bin"), []byte("\x00\x03\x04\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	view := m.View()
	if !strings.Contains(view, "binary file") || strings.Contains(view, "WORKING TREE") {
		t.Errorf("a binary file should be named as such instead of aligned:\n%s", view)
	}
}

// Lip Gloss's Width wraps rather than clips, and the frame pads short content
// up to its height, so an over-long code line only shows once the columns
// fill the window: then it takes two rows and pushes the status bar off. The
// fixture is taller than the window so every pane row is in use, and the long
// line is on the committed side, where an uncut line would shove its
// counterpart past the frame instead of merely being cut there.
func TestCodeColumnsClipLongLinesInsteadOfWrapping(t *testing.T) {
	m, _, root := gitModel(t)
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	long := strings.Repeat("x", 300)
	app := commitFile(t, root, "long.txt", long+"\n"+sb.String())
	if err := os.WriteFile(filepath.Join(app, "long.txt"), []byte("short\n"+sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	view := m.View()
	if lines := strings.Count(view, "\n") + 1; lines != m.height {
		t.Errorf("the view is %d lines tall in a %d-line window:\n%s", lines, m.height, view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line %d is %d cells wide in a %d-column window", i, w, m.width)
		}
	}
	if row := plainLine(view, "xxxxxxxx"); !strings.Contains(row, "1 short") {
		t.Errorf("the long committed line should be cut so its replacement still faces it, got %q", row)
	}
}

// Truncate counts a tab as no cells while Render draws it as four, so a line
// cut with its tabs still in it comes out wider than its column and pushes
// the other column past the frame. Thirty tabs are enough to push the whole
// current side off a 120-column window.
func TestATabInACodeLineDoesNotWidenItsColumn(t *testing.T) {
	m, _, root := gitModel(t)
	app := commitFile(t, root, "tabs.go", strings.Repeat("\t", 30)+"x\n")
	if err := os.WriteFile(filepath.Join(app, "tabs.go"), []byte("short\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	view := m.View()
	if row := plainLine(view, "1 short"); row == "" {
		t.Errorf("the current line should still face its tab-indented counterpart:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line %d is %d cells wide in a %d-column window", i, w, m.width)
		}
	}
}

// splitLines splits on \n only, so every line of a CRLF file keeps a trailing
// \r that Truncate, Width and pad all count as zero cells; drawn, it sends the
// terminal back to column 0 and the right column lands on the file list. An
// escape sequence in a file would likewise be executed rather than shown. A
// tab is a control character too, but one that must survive as indentation.
func TestControlCharactersInAFileAreNotDrawn(t *testing.T) {
	m, _, root := gitModel(t)
	app := commitFile(t, root, "win.txt", "a\r\nb\r\n")
	if err := os.WriteFile(filepath.Join(app, "win.txt"), []byte("a\r\nB \x1b]0;title\x07 here\r\n\tindented\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	view := m.View()
	if strings.Contains(view, "\r") {
		t.Errorf("a carriage return from the file reached the screen:\n%q", view)
	}
	if strings.Contains(view, "\x1b]") {
		t.Errorf("an OSC sequence from the file reached the screen:\n%q", view)
	}
	if row := plainLine(view, "here"); !strings.Contains(row, "2 b") || !strings.Contains(row, "2 B ") {
		t.Errorf("the line with the escape should still be shown, aligned with its counterpart, got %q", row)
	}
	if row := plainLine(view, "indented"); !strings.Contains(row, "    indented") {
		t.Errorf("a tab should still indent its line, got %q", row)
	}
}

// The right column starts at the same cell on every row whatever the left
// side holds; that is what makes the two columns readable as columns.
func TestTheRightColumnStartsAtTheSameCellOnEveryRow(t *testing.T) {
	m, _, root := gitModel(t)
	app := commitFile(t, root, "f.txt", "x\na much longer committed line\n")
	if err := os.WriteFile(filepath.Join(app, "f.txt"), []byte("R1\nR2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	view := m.View()
	start := func(marker string) int {
		row := plainLine(view, marker)
		idx := strings.Index(row, marker)
		if idx < 0 {
			t.Fatalf("no row shows %q:\n%s", marker, ansi.Strip(view))
		}
		return lipgloss.Width(row[:idx])
	}
	if a, b := start("R1"), start("R2"); a != b {
		t.Errorf("right column starts at cell %d on one row and %d on the next:\n%s", a, b, ansi.Strip(view))
	}
}

// ShowHEAD folds every git error into "not in HEAD". For a file that is not
// new that answer is a failure, and aligning against an empty left side would
// render it as a plausible whole-file addition.
func TestAModifiedFileMissingFromHEADIsReportedNotShownAsNew(t *testing.T) {
	_, _, root := gitModel(t)
	app := filepath.Join(root, "app")
	if err := os.WriteFile(filepath.Join(app, "ghost.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &diffView{dir: app, files: []git.FileChange{{Path: "ghost.txt", Kind: git.Modified}}}

	d.load()

	if d.loadErr == "" || d.rows != nil {
		t.Errorf("loadErr %q, rows %+v; want an error and no rows", d.loadErr, d.rows)
	}
}

// A file staged as new and then removed from the working tree is a deletion
// as far as status is concerned, but HEAD never had it either. Nothing is
// wrong; there is just nothing on either side to show, and saying so beats
// both an error and an empty pane that reads as an empty file.
func TestAFileStagedThenRemovedIsInNeitherVersion(t *testing.T) {
	m, _, root := gitModel(t)
	app := filepath.Join(root, "app")
	if err := os.WriteFile(filepath.Join(app, "brief.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, app, "add", "brief.txt")
	if err := os.Remove(filepath.Join(app, "brief.txt")); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	if d := m.diff; !strings.Contains(d.loadErr, "neither HEAD nor the working tree") {
		t.Errorf("loadErr = %q, want a note that the file is in neither version", d.loadErr)
	}
}

// Paths come from git and a read error from the OS; both can carry text tm
// did not write. The same rule as for code lines: nothing file-derived may
// drive the terminal.
func TestControlCharactersInPathsAndLoadErrorsAreNotDrawn(t *testing.T) {
	osc := "\x1b]0;title\x07"
	d := &diffView{
		title:   "app",
		files:   []git.FileChange{{Path: "dir" + osc + "/new" + osc + ".txt", OldPath: "old" + osc + ".txt", Kind: git.Modified}},
		loadErr: "open " + osc + ": permission denied",
	}

	out := d.view(100, 12)

	if strings.Contains(out, "\x1b]") {
		t.Errorf("an OSC sequence from a path or error reached the screen:\n%q", out)
	}
	for _, want := range []string{"new", "dir", "from old", "permission denied"} {
		if !strings.Contains(ansi.Strip(out), want) {
			t.Errorf("stripping should keep the text %q:\n%s", want, ansi.Strip(out))
		}
	}
}

// bigFile commits 200 numbered lines to big.txt and then changes the given
// rows, so a change can sit well below the first screen.
func bigFile(t *testing.T, root string, changed ...int) {
	t.Helper()
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	app := commitFile(t, root, "big.txt", strings.Join(lines, "\n")+"\n")
	for _, i := range changed {
		lines[i] += " CHANGED"
	}
	if err := os.WriteFile(filepath.Join(app, "big.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// firstCodeRow is the view line under the column labels, colours stripped.
func firstCodeRow(t *testing.T, view string) string {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	for i, line := range lines {
		if strings.Contains(line, "WORKING TREE") && i+1 < len(lines) {
			return lines[i+1]
		}
	}
	t.Fatalf("no column labels in:\n%s", ansi.Strip(view))
	return ""
}

func TestTabMovesFocusAndScrollingIsLockedAcrossColumns(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 150)

	m = press(m, "d")
	if !m.diff.listFocus {
		t.Fatal("the file list should have focus when the view opens")
	}

	m = press(m, "tab")
	if m.diff.listFocus {
		t.Fatal("tab should move focus to the diff")
	}

	m = press(m, "down", "down", "down")
	if m.diff.top != 3 {
		t.Errorf("top = %d, want 3 after three downs", m.diff.top)
	}
	// Both columns start at the same row, so the fourth line faces itself.
	if row := firstCodeRow(t, m.View()); strings.Count(row, "4 line 4") != 2 {
		t.Errorf("after scrolling three rows the first row should show line 4 on both sides, got %q", row)
	}

	m = press(m, "right", "right")
	if m.diff.left != 2*diffHScrollStep {
		t.Errorf("left = %d, want %d; both columns share one horizontal offset",
			m.diff.left, 2*diffHScrollStep)
	}
}

// One offset for both sides: after a scroll right the same cells of the same
// line are gone from each column, so the columns still read as a pair.
func TestHorizontalScrollCutsBothColumnsTogether(t *testing.T) {
	m, _, root := gitModel(t)
	app := commitFile(t, root, "f.txt", "abcdefgh12345678\nx\n")
	if err := os.WriteFile(filepath.Join(app, "f.txt"), []byte("abcdefgh12345678\n"+strings.Repeat("y", 20)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d", "tab", "right")

	row := plainLine(m.View(), "12345678")
	if strings.Count(row, "1 12345678") != 2 || strings.Contains(row, "abcdefgh") {
		t.Errorf("one step right should cut %d cells from both sides, got %q", diffHScrollStep, row)
	}
	m = press(m, "left", "left") // clamps at the start
	if m.diff.left != 0 {
		t.Errorf("left = %d, want 0 after scrolling back past the start", m.diff.left)
	}
	// The offset stops at the widest line on either side, so the view never
	// runs on into empty columns that take as many presses to come back from.
	for i := 0; i < 10; i++ {
		m = press(m, "right")
	}
	if m.diff.left != 20 {
		t.Errorf("left = %d, want 20, the width of the widest line", m.diff.left)
	}
}

func TestNextChangeJumpsPastUnchangedLines(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 150)

	m = press(m, "d", "tab", "n")

	if want := 150 - diffJumpContext; m.diff.top != want {
		t.Errorf("top = %d, want %d; n should jump to the change at row 150 with %d rows of context above it, not scroll one line",
			m.diff.top, want, diffJumpContext)
	}
}

// Each press continues from the change the last one landed on, so n never
// finds the same change twice and N can return all the way to a change on
// the very first row. A fresh view starts its search at the top row, so the
// first n reaches a change there rather than skipping it.
func TestChangeJumpsVisitEachRunOnceAndReturnToTheFirst(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 0, 50, 150)

	m = press(m, "d", "tab")
	for i, step := range []struct {
		key  string
		want int
	}{
		{"n", 0}, // the change on row 0, already on screen
		{"n", 50 - diffJumpContext},
		{"n", 150 - diffJumpContext},
		{"N", 50 - diffJumpContext},
		{"N", 0},
		{"N", 0}, // nothing before the first change
	} {
		m = press(m, step.key)
		if m.diff.top != step.want {
			t.Errorf("step %d (%s): top = %d, want %d", i, step.key, m.diff.top, step.want)
		}
	}
}

func TestRefreshReReadsTheFileAndKeepsThePlace(t *testing.T) {
	m, _, root := gitModel(t)
	app := filepath.Join(root, "app")
	var sb strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&sb, "b%d\n", i)
	}
	for name, content := range map[string]string{"a.txt": "alpha\n", "b.txt": sb.String()} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m = press(m, "d", "j", "tab", "down", "down")
	if err := os.WriteFile(filepath.Join(app, "b.txt"), []byte(strings.Replace(sb.String(), "b5\n", "b5 REWRITTEN\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.View(), "REWRITTEN") {
		t.Fatal("the view must not follow the file live; only r re-reads it")
	}

	m = press(m, "r")

	if !strings.Contains(m.View(), "REWRITTEN") {
		t.Errorf("r should re-read the selected file:\n%s", ansi.Strip(m.View()))
	}
	if m.diff.cursor != 1 {
		t.Errorf("cursor = %d, want 1: the selection should stay on b.txt", m.diff.cursor)
	}
	if m.diff.listFocus {
		t.Error("r should leave focus where it was, on the diff")
	}
	if m.diff.top != 2 {
		t.Errorf("top = %d, want 2: a re-read of the same file keeps the scroll position", m.diff.top)
	}
}

// Focus is shown by the heading of the half that has it: FILES over the list,
// the column labels over the code. Lip Gloss emits no colour under go test,
// so the style choice is asserted against the style values, as the pane dot's
// is, and the heading's placement against the text.
func TestTheFocusedHalfIsMarkedInItsHeading(t *testing.T) {
	m, _, root := gitModel(t)
	if err := os.WriteFile(filepath.Join(root, "app", "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")
	if files, head, work := m.diff.headingStyles(); !reflect.DeepEqual(files, selectedStyle) ||
		!reflect.DeepEqual(head, delStyle) || !reflect.DeepEqual(work, addStyle) {
		t.Error("with the list focused FILES takes the focus colour and the labels keep their own")
	}
	// The heading shares the row with the column labels, so the first file
	// lines up with the first code row.
	if row := plainLine(m.View(), "WORKING TREE"); !strings.Contains(row, "FILES") {
		t.Errorf("FILES should head the list on the labels' row, got %q", row)
	}

	m = press(m, "tab")
	if files, head, work := m.diff.headingStyles(); !reflect.DeepEqual(files, hintStyle) ||
		!reflect.DeepEqual(head, selectedStyle) || !reflect.DeepEqual(work, selectedStyle) {
		t.Error("with the diff focused both labels take the focus colour and FILES is dimmed")
	}
}

// j/k mean different things on each side of tab, so the status bar says what
// they do for the half that has focus.
func TestTheStatusBarFollowsTheFocusedHalf(t *testing.T) {
	m, _, _ := gitModel(t)

	m = press(m, "d", "tab")

	status := m.viewStatus()
	if strings.Contains(status, "next file") || !strings.Contains(status, "next change") {
		t.Errorf("with the diff focused the status bar should offer the diff's keys, not the list's:\n%s", status)
	}
	if strings.Contains(status, "\n") {
		t.Errorf("status bar wraps:\n%s", status)
	}
}

// The heading costs the list a row. Forget that and the list emits one row
// too many, which body cuts off: the selected entry keeps its name but loses
// its counts line. The body must have an even number of rows: with an odd
// one a spare row absorbs the heading and the defect hides.
func TestTheSelectedEntryIsShownWhole(t *testing.T) {
	m, _, root := gitModel(t)
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(root, "app", fmt.Sprintf("f%02d.txt", i)), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 13}) // body of 8 rows: heading + 3 entries, and no room for a fourth
	m = next.(Model)

	m = press(m, "d")
	for i := 0; i < 19; i++ {
		m = press(m, "j")
	}

	lines := strings.Split(ansi.Strip(m.View()), "\n")
	for i, line := range lines {
		if strings.Contains(line, "› f19.txt") {
			if i+1 >= len(lines) || !strings.Contains(lines[i+1], "new file") {
				t.Errorf("the selected entry's counts line should follow its name, got %q", lines[min(i+1, len(lines)-1)])
			}
			return
		}
	}
	t.Errorf("the selected file is not on screen:\n%s", ansi.Strip(m.View()))
}

// codeRows is how many code rows the changes view shows in m's window: the
// window less the status bar, the modal's frame and the rows above the code.
func codeRows(m Model) int {
	return m.height - statusHeight - modalStyle.GetVerticalFrameSize() - diffChromeRows
}

func TestScrollingStopsAtTheEndOfTheFile(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 150)

	m = press(m, "d", "tab")
	for i := 0; i < 300; i++ {
		m = press(m, "down")
	}

	if want := 200 - codeRows(m); m.diff.top != want {
		t.Errorf("top = %d, want %d: the last row of the file sits on the last row of the view", m.diff.top, want)
	}
	// The bottom code row is the last line of the file: no blank rows are
	// scrolled into view and the file is not cut short.
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if bottom := lines[len(lines)-3]; !strings.Contains(bottom, "200 line 200") { // above the frame's bottom border and the status bar
		t.Errorf("the bottom code row should be line 200, got %q", bottom)
	}
}

// A change in the last screenful of the file is shown by scrolling to the
// end, never past it: the jump clamps as scrolling does.
// A fresh view has no jump to continue from, so n searches from the top row:
// a change on row 2 is found there rather than skipped for the one below it.
func TestNextChangeOnAFreshViewReachesAChangeNearTheTop(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 2, 5)

	m = press(m, "d", "tab", "n")
	if m.diff.top != 0 {
		t.Errorf("top = %d, want 0: the first n lands on the change on row 2, which is already on screen", m.diff.top)
	}
	m = press(m, "n")
	if want := 5 - diffJumpContext; m.diff.top != want {
		t.Errorf("top = %d, want %d: the second n moves on to the change on row 5", m.diff.top, want)
	}
}

// A landing near the start is clamped to top 0, so the change is not the
// usual two rows below top. The next jump must continue from the change
// itself, or one starting in those two rows is skipped.
func TestJumpsContinueFromAClampedLanding(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 0, 2, 5)

	m = press(m, "d", "tab")
	for i, want := range []int{0, 0, 5 - diffJumpContext} { // rows 0, 2 (both clamped to top 0), then 5
		m = press(m, "n")
		if m.diff.top != want {
			t.Errorf("n press %d: top = %d, want %d", i+1, m.diff.top, want)
		}
	}
}

// Scrolling by hand abandons the last jump: the next N searches up from the
// top row of the screen, so a change sitting on that row is already in view
// and the one above the screen is the previous one.
func TestScrollingByHandMovesTheJumpOrigin(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 0, 50, 150)

	m = press(m, "d", "tab", "n", "n", "n") // at the change on row 150
	for i := 0; i < 150-diffJumpContext-50; i++ {
		m = press(m, "up")
	}
	if m.diff.top != 50 {
		t.Fatalf("top = %d, want 50 after scrolling up: the change on row 50 on the top row", m.diff.top)
	}

	m = press(m, "N")

	if m.diff.top != 0 {
		t.Errorf("top = %d, want 0: N should find the change above the screen, on row 0, not the one on the top row", m.diff.top)
	}
}

// Enlarging the window after scrolling to the end would leave blank rows
// under the file until the next key; the view re-clamps as soon as it grows.
func TestEnlargingTheWindowKeepsTheFileEndOnTheLastRow(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 150)
	m = press(m, "d", "tab")
	for i := 0; i < 300; i++ {
		m = press(m, "down")
	}

	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = next.(Model)

	if want := 200 - codeRows(m); m.diff.top != want {
		t.Errorf("top = %d, want %d after the window grew", m.diff.top, want)
	}
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if bottom := lines[len(lines)-3]; !strings.Contains(bottom, "200 line 200") {
		t.Errorf("the bottom code row should be line 200, got %q", bottom)
	}
}

func TestAJumpNearTheEndStopsAtTheEndOfTheFile(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 199)

	m = press(m, "d", "tab", "n")

	if want := 200 - codeRows(m); m.diff.top != want {
		t.Errorf("top = %d, want %d: the change on the last line is shown with the file's end on the last row", m.diff.top, want)
	}
}

func TestSelectingAnotherFileStartsAtItsTop(t *testing.T) {
	m, _, root := gitModel(t)
	bigFile(t, root, 150)
	if err := os.WriteFile(filepath.Join(root, "app", "z.txt"), []byte("z\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d", "tab", "down", "down", "right", "tab", "j")

	if m.diff.top != 0 || m.diff.left != 0 {
		t.Errorf("top, left = %d, %d; another file should open at its start", m.diff.top, m.diff.left)
	}
}

// A re-read can find the diff shorter than the scroll position; the view must
// clamp rather than show rows that no longer exist. The file is untracked so
// the diff has no committed side to keep its row count up.
func TestRefreshClampsWhenTheFileShrinks(t *testing.T) {
	m, _, root := gitModel(t)
	path := filepath.Join(root, "app", "new.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 200)), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d", "tab")
	for i := 0; i < 100; i++ {
		m = press(m, "down")
	}
	if err := os.WriteFile(path, []byte("line 1 SHORT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = press(m, "r")

	if m.diff.top != 0 {
		t.Errorf("top = %d, want 0 for a one-line file", m.diff.top)
	}
	if row := firstCodeRow(t, m.View()); !strings.Contains(row, "1 line 1 SHORT") {
		t.Errorf("the re-read file should be on screen, got %q", row)
	}
}

// r restores the sideways offset, but the file it re-reads may have lost the
// long line that offset was looking along. Kept as it was, the offset would
// sit at or past the new widest line and both columns would come up blank —
// from the one key whose job is to show what changed. The offset survives
// while it still shows something and otherwise starts the line over.
func TestRefreshStartsTheLineOverWhenItsLongestLineShrank(t *testing.T) {
	m, _, root := gitModel(t)
	path := filepath.Join(root, "app", "new.txt")
	long := strings.Repeat("x", 40)
	if err := os.WriteFile(path, []byte(long+"\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = press(m, "d", "tab", "right", "right", "right", "right") // left 32 of 40

	if err := os.WriteFile(path, []byte(long+"\nSECOND\n"), 0o644); err != nil { // the long line is still there
		t.Fatal(err)
	}
	m = press(m, "r")
	if m.diff.left != 4*diffHScrollStep {
		t.Errorf("left = %d, want %d: the offset still shows the long line, so it is kept", m.diff.left, 4*diffHScrollStep)
	}

	// Now the longest line is exactly as wide as the offset: the boundary,
	// where an offset one too generous shows nothing.
	if err := os.WriteFile(path, []byte(strings.Repeat("y", 4*diffHScrollStep)+"\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = press(m, "r")
	if row := firstCodeRow(t, m.View()); !strings.Contains(row, "1 yyyy") {
		t.Errorf("after the long line shrank the re-read file should be shown from its start, got %q", row)
	}
}
