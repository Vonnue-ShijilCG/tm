package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
