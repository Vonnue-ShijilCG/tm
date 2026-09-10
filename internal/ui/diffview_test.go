package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
