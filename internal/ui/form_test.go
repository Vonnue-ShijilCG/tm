package ui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// gitModel builds a model over <root>/app (a real repo with branch "develop").
func gitModel(t *testing.T) (Model, *[]*fakeSession, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	app := filepath.Join(root, "app")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", app},
		{"-C", app, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", app, "branch", "develop"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	spawned := &[]*fakeSession{}
	m := New(root, nil)
	m.ws = workspace.New(root, []project.Project{{Name: "app", Path: app}}, func(dir string, cols, rows int) (workspace.Session, error) {
		f := newFakeSession(dir, cols, rows)
		*spawned = append(*spawned, f)
		return f, nil
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return next.(Model), spawned, root
}

func typeText(m Model, s string) Model {
	for _, r := range s {
		m = press(m, string(r))
	}
	return m
}

func TestFormCreatesWorktreeTerminal(t *testing.T) {
	m, spawned, root := gitModel(t)

	m = press(m, "n")
	if m.form == nil || !m.form.canWorktree {
		t.Fatalf("n on a git project should open the form: %+v", m.form)
	}
	view := m.View()
	for _, want := range []string{"New terminal · app", "Branch", "project checkout"} {
		if !strings.Contains(view, want) {
			t.Errorf("form view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Name") {
		t.Errorf("the form no longer asks for a name:\n%s", view)
	}
	if strings.Contains(view, "Worktree") {
		t.Errorf("the worktree toggle should be gone:\n%s", view)
	}

	m = typeText(m, "develop") // the branch field has focus straight away; an existing branch
	if !strings.Contains(m.View(), "(existing)") {
		t.Errorf("typing an existing branch should say so:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "new worktree") {
		t.Errorf("an existing branch with no worktree yet should report a new worktree:\n%s", m.View())
	}
	m = press(m, "enter")

	if m.form != nil {
		t.Fatalf("form should close after a successful create, err=%q", m.form.err)
	}
	wantDir := filepath.Join(root, workspace.WorktreesDir, "app", "develop") // the worktree directory follows the branch
	if len(*spawned) != 1 || (*spawned)[0].dir != wantDir {
		t.Fatalf("shell should start in %s, spawned=%+v", wantDir, *spawned)
	}
	term := m.ws.Projects[0].Terminals[0]
	if term.Worktree == nil || term.Worktree.Branch != "develop" {
		t.Errorf("terminal = %+v", term)
	}
	if m.focus != focusMain {
		t.Error("new terminal should be focused")
	}
	view = m.View()
	for _, want := range []string{"Terminal 1 · ⎇ develop", "app · Terminal 1 · ⎇ develop"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(m.paneTitle(m.ws.Projects[0], term), "worktree") || strings.Contains(m.viewStatus(), "worktree") {
		t.Error("neither header nor status bar says worktree")
	}
}

func TestFormNewBranchAndReusingItOpensTheSameWorktree(t *testing.T) {
	m, spawned, root := gitModel(t)

	m = press(m, "n")
	m = typeText(m, "feature/payment") // does not exist yet
	if !strings.Contains(m.View(), "(new branch)") {
		t.Errorf("typing an unknown branch should say new branch:\n%s", m.View())
	}
	m = press(m, "enter")
	if m.form != nil || len(*spawned) != 1 {
		t.Fatalf("create with new branch failed: form=%+v spawned=%d", m.form, len(*spawned))
	}

	// The same branch again from terminal mode: alt+n opens the form, and
	// since the branch already has a worktree, create reuses it rather than
	// failing (git allows a branch in only one checkout).
	m = press(m, "alt+n")
	if m.form == nil || m.form.back != focusMain {
		t.Fatal("alt+n should open the form and remember to return to the terminal")
	}
	m = typeText(m, "feature/payment")
	if !strings.Contains(m.View(), "(existing)") {
		t.Errorf("typing a branch that now exists should say so:\n%s", m.View())
	}
	m = press(m, "enter")
	if m.form != nil {
		t.Fatalf("reusing an existing worktree should succeed and close the form, form=%+v", m.form)
	}
	if len(*spawned) != 2 {
		t.Fatalf("reuse should still spawn a second terminal, spawned=%d", len(*spawned))
	}
	wantDir := filepath.Join(root, workspace.WorktreesDir, "app", "feature-payment")
	if (*spawned)[0].dir != wantDir || (*spawned)[1].dir != wantDir {
		t.Errorf("both terminals should share the worktree dir %s, spawned=%+v", wantDir, *spawned)
	}
	if m.focus != focusMain {
		t.Error("new terminal should be focused")
	}
}

func TestFormEmptyBranchOpensTheProjectCheckout(t *testing.T) {
	m, spawned, _ := gitModel(t)
	app := m.ws.Projects[0]

	m = press(m, "n")
	m = press(m, "enter") // branch left empty: the project's own checkout, no toggle to flip
	if m.form != nil || len(*spawned) != 1 || (*spawned)[0].dir != app.Path {
		t.Fatalf("an empty branch should start a plain shell in the project dir, form=%v spawned=%+v", m.form, *spawned)
	}
	if app.Terminals[0].Worktree != nil {
		t.Error("a terminal in the project checkout must not have a worktree")
	}
}

func TestFormOnNonGitProjectCreatesPlainTerminal(t *testing.T) {
	m, spawned := newTestModel(t) // fake projects: paths do not exist, so not git

	m = press(m, "j", "n")
	if m.form == nil || m.form.canWorktree {
		t.Fatalf("form should open without worktree support: %+v", m.form)
	}
	if !strings.Contains(m.View(), "not a git repository") || strings.Contains(m.View(), "Name") {
		t.Errorf("form should explain why worktrees are unavailable and ask for nothing else:\n%s", m.View())
	}
	m = press(m, "tab") // nothing to edit: a no-op with only Command in the field list
	m = press(m, "enter")
	if m.form != nil || len(*spawned) != 1 || (*spawned)[0].dir != "/ws/ecommerce" {
		t.Fatalf("plain terminal should be created: form=%v spawned=%+v", m.form, *spawned)
	}
	if !strings.Contains(m.View(), " Terminal 1 ") {
		t.Errorf("header shows the terminal identity without a branch:\n%s", m.View())
	}
}

func TestFormEscFromSidebarReturnsToSidebar(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "n", "esc")
	if m.form != nil || m.focus != focusSidebar || len(*spawned) != 0 {
		t.Fatalf("esc should cancel without creating anything: form=%v focus=%v spawned=%d", m.form, m.focus, len(*spawned))
	}
	m = press(m, "j")
	if m.cursor != 1 {
		t.Error("sidebar navigation should work after cancelling")
	}
}
