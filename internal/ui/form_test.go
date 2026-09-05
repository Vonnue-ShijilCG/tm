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
	if m.form == nil || !m.form.canWorktree || !m.form.worktree {
		t.Fatalf("n on a git project should open the form with worktree enabled: %+v", m.form)
	}
	view := m.View()
	for _, want := range []string{"New terminal · app", "Name", "Branch", "Worktree", "yes"} {
		if !strings.Contains(view, want) {
			t.Errorf("form view missing %q:\n%s", want, view)
		}
	}

	m = typeText(m, "Login")
	m = press(m, "tab")
	m = typeText(m, "develop") // existing branch
	if !strings.Contains(m.View(), "(existing)") {
		t.Errorf("typing an existing branch should say so:\n%s", m.View())
	}
	m = press(m, "enter")

	if m.form != nil {
		t.Fatalf("form should close after a successful create, err=%q", m.form.err)
	}
	wantDir := filepath.Join(root, workspace.WorktreesDir, "app", "login")
	if len(*spawned) != 1 || (*spawned)[0].dir != wantDir {
		t.Fatalf("shell should start in %s, spawned=%+v", wantDir, *spawned)
	}
	term := m.ws.Projects[0].Terminals[0]
	if term.Name != "Login" || term.Worktree == nil || term.Worktree.Branch != "develop" {
		t.Errorf("terminal = %+v", term)
	}
	if m.focus != focusMain {
		t.Error("new terminal should be focused")
	}
	view = m.View()
	for _, want := range []string{"1 Login [develop]", "app · terminal 1 Login · worktree develop"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestFormNewBranchAndDuplicateNameError(t *testing.T) {
	m, spawned, _ := gitModel(t)

	m = press(m, "n")
	m = typeText(m, "Payment")
	m = press(m, "tab")
	m = typeText(m, "feature/payment") // does not exist yet
	if !strings.Contains(m.View(), "(new branch)") {
		t.Errorf("typing an unknown branch should say new branch:\n%s", m.View())
	}
	m = press(m, "enter")
	if m.form != nil || len(*spawned) != 1 {
		t.Fatalf("create with new branch failed: form=%+v spawned=%d", m.form, len(*spawned))
	}

	// Same name again from terminal mode: alt+n opens the form, create fails,
	// the form stays open with the error and nothing was spawned.
	m = press(m, "alt+n")
	if m.form == nil || m.form.back != focusMain {
		t.Fatal("alt+n should open the form and remember to return to the terminal")
	}
	m = typeText(m, "Payment")
	m = press(m, "tab")
	m = typeText(m, "feature/other")
	m = press(m, "enter")
	if m.form == nil || !strings.Contains(m.form.err, "already exists") {
		t.Fatalf("duplicate worktree name should keep the form open with an error, form=%+v", m.form)
	}
	if !strings.Contains(m.View(), "already exists") {
		t.Error("error should be rendered in the form")
	}
	if len(*spawned) != 1 {
		t.Error("failed create must not spawn a shell")
	}

	m = press(m, "esc")
	if m.form != nil || m.focus != focusMain {
		t.Errorf("esc should cancel back to the terminal, form=%v focus=%v", m.form, m.focus)
	}
}

func TestFormWorktreeToggleOffAndMissingBranch(t *testing.T) {
	m, spawned, _ := gitModel(t)
	app := m.ws.Projects[0]

	m = press(m, "n")
	m = press(m, "enter") // worktree on, branch empty
	if m.form == nil || !strings.Contains(m.form.err, "branch is required") || len(*spawned) != 0 {
		t.Fatalf("empty branch should be rejected, form=%+v spawned=%d", m.form, len(*spawned))
	}

	m = press(m, "tab", " ") // move to the worktree field and toggle it off
	if m.form.field != fieldWorktree || m.form.worktree {
		t.Fatalf("space on the worktree field should toggle it off (field=%d worktree=%v)", m.form.field, m.form.worktree)
	}
	m = press(m, "enter")
	if m.form != nil || len(*spawned) != 1 || (*spawned)[0].dir != app.Path {
		t.Fatalf("with worktree off a plain shell should start in the project dir, spawned=%+v", *spawned)
	}
	if app.Terminals[0].Worktree != nil {
		t.Error("plain terminal must not have a worktree")
	}
}

func TestFormOnNonGitProjectCreatesPlainTerminal(t *testing.T) {
	m, spawned := newTestModel(t) // fake projects: paths do not exist, so not git

	m = press(m, "j", "n")
	if m.form == nil || m.form.canWorktree {
		t.Fatalf("form should open without worktree support: %+v", m.form)
	}
	if !strings.Contains(m.View(), "not a git repository") {
		t.Errorf("form should explain why worktrees are unavailable:\n%s", m.View())
	}
	m = typeText(m, "Shell")
	m = press(m, "tab") // only one field: stays on name
	m = press(m, "enter")
	if m.form != nil || len(*spawned) != 1 || (*spawned)[0].dir != "/ws/ecommerce" {
		t.Fatalf("plain terminal should be created: form=%v spawned=%+v", m.form, *spawned)
	}
	if got := m.ws.Projects[1].Terminals[0].Name; got != "Shell" {
		t.Errorf("terminal name = %q, want Shell", got)
	}
	if !strings.Contains(m.View(), "1 Shell ") {
		t.Errorf("tab should show the name:\n%s", m.View())
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
