package ui

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormReportsTheProjectCheckoutWhenNoBranchIsTyped(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	if m.form == nil {
		t.Fatal("n should open the form")
	}

	view := m.form.view(80)

	if !strings.Contains(view, "project checkout") {
		t.Errorf("an empty branch should report the project checkout:\n%s", view)
	}
	if strings.Contains(view, "Worktree") {
		t.Errorf("the worktree toggle should be gone:\n%s", view)
	}
}

func TestFormReportsANewWorktreeForAnUnknownBranch(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	m = typeText(m, "feature/brand-new")

	view := m.form.view(80)

	if !strings.Contains(view, "new worktree") {
		t.Errorf("an unknown branch should report a new worktree:\n%s", view)
	}
	if !strings.Contains(view, "From") {
		t.Errorf("an unknown branch still needs a base:\n%s", view)
	}
}

// TestFormWarnsAboutAForeignWorktree covers a branch already checked out
// somewhere tm did not create, but still inside the workspace root — e.g. a
// worktree a person (or Claude Code) added by hand under the project
// directory itself, outside tm's own worktree directory. tm can only see
// terminals it started, so it must say so rather than imply the directory is
// idle; and since the path is workspace-relative, tm can still persist and
// reopen a terminal there across a restart, so the form does not refuse it.
func TestFormWarnsAboutAForeignWorktree(t *testing.T) {
	m, _, _ := gitModel(t)
	p := m.ws.Projects[0]

	foreign := filepath.Join(m.root, "external", "manual-checkout") // inside the workspace root, outside <root>/.terminal-worktrees
	if out, err := exec.Command("git", "-C", p.Path, "worktree", "add", "-q", "-b", "feature/manual", foreign).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	m = press(m, "n")
	m = typeText(m, "feature/manual")

	view := m.form.view(80)
	if !strings.Contains(view, foreign) {
		t.Errorf("Location should name the foreign worktree's path:\n%s", view)
	}
	if !strings.Contains(view, "not created by tm") {
		t.Errorf("Location should mark a worktree tm did not create:\n%s", view)
	}
	if !strings.Contains(view, "may be in use elsewhere") {
		t.Errorf("Location should warn that tm cannot see terminals there:\n%s", view)
	}
	if strings.Contains(view, "outside the workspace") {
		t.Errorf("a worktree inside the workspace root must not be reported as out of root:\n%s", view)
	}

	m = press(m, "enter")
	if m.form != nil {
		t.Fatalf("a foreign worktree inside the workspace should still be usable: %s", m.form.err)
	}
}

// TestFormRefusesAWorktreeOutsideTheWorkspace covers a branch checked out in
// a worktree entirely outside the workspace root - e.g. `git worktree add
// ~/wt-foo -b x` run by hand. The state file only ever stores
// workspace-relative worktree paths (state.ValidRelPath), so opening a
// terminal there could never survive a restart: sanitized() would silently
// drop the whole entry and its layout leaf on the next load. The form must
// refuse up front instead of losing the pane later with no notice.
func TestFormRefusesAWorktreeOutsideTheWorkspace(t *testing.T) {
	m, spawned, _ := gitModel(t)
	p := m.ws.Projects[0]

	outside := filepath.Join(t.TempDir(), "wt-foo") // deliberately outside the workspace root entirely
	if out, err := exec.Command("git", "-C", p.Path, "worktree", "add", "-q", "-b", "feature/outside", outside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	m = press(m, "n")
	m = typeText(m, "feature/outside")

	view := m.form.view(80)
	if !strings.Contains(view, "outside the workspace") {
		t.Errorf("Location should say the worktree is outside the workspace:\n%s", view)
	}
	if !strings.Contains(view, "tm can only manage worktrees inside this workspace") {
		t.Errorf("the form should explain why up front:\n%s", view)
	}

	m = press(m, "enter")
	if m.form == nil {
		t.Fatal("submitting should be refused, not silently create a terminal that cannot survive a restart")
	}
	if !strings.Contains(m.form.err, "tm can only manage worktrees inside this workspace") {
		t.Errorf("form err = %q", m.form.err)
	}
	if len(*spawned) != 0 {
		t.Errorf("no terminal should have been spawned, got %+v", *spawned)
	}
}

func TestADuplicateCommandInTheSameDirectoryAsksFirst(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	m = press(m, "tab") // to Command
	m = typeText(m, "claude")
	m = press(m, "enter")
	if m.form != nil {
		t.Fatalf("the first terminal should have been created: %s", m.notice)
	}

	m = press(m, "alt+s", "n")
	m = press(m, "tab")
	m = typeText(m, "claude")
	m = press(m, "enter")

	if m.form == nil {
		t.Fatal("a duplicate command should ask before creating a second terminal")
	}
	if !strings.Contains(m.form.view(80), "already running here") {
		t.Errorf("the form should say the command is already running:\n%s", m.form.view(80))
	}

	m = press(m, "enter")
	if m.form != nil {
		t.Fatalf("a second enter should confirm and create: %s", m.notice)
	}
}

func TestADifferentCommandInTheSameDirectoryDoesNotAsk(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	m = press(m, "tab")
	m = typeText(m, "claude")
	m = press(m, "enter")

	m = press(m, "alt+s", "n")
	m = press(m, "tab")
	m = typeText(m, "npm run dev")
	m = press(m, "enter")

	if m.form != nil {
		t.Fatalf("a different command should create without asking: %s", m.form.view(80))
	}
}
