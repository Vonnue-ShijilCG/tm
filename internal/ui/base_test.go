package ui

import (
	"strings"
	"testing"
)

func TestBaseFieldAppearsOnlyForANewBranch(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "n") // form for a project the fake workspace treats as non-Git
	if m.form == nil {
		t.Fatal("n should open the form")
	}
	f := m.form
	// newTestModel's fixture project is a non-Git fake path, so canWorktree was
	// false when newForm ran and worktree defaulted to false with it; set both
	// here to exercise the worktree-with-a-new-branch case the test is after.
	f.canWorktree, f.branches, f.worktree = true, []string{"main", "feature/login"}, true

	f.branch.SetValue("feature/login") // existing
	if f.branchIsNew() {
		t.Error("an existing branch must not be treated as new")
	}
	if strings.Contains(f.view(80), "From") {
		t.Error("no base is chosen for an existing branch")
	}

	f.branch.SetValue("feature/new") // does not exist
	if !f.branchIsNew() {
		t.Error("an unknown branch must be treated as new")
	}
	if !strings.Contains(f.view(80), "From") {
		t.Error("a new branch must offer a base")
	}
}

func TestFormPassesTheBaseForANewBranch(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "n")
	f := m.form
	f.canWorktree, f.branches, f.worktree = true, []string{"main"}, true
	f.branch.SetValue("feature/new")
	f.base.SetValue("main")

	opts, cancelled := f.submit()
	if cancelled || opts == nil {
		t.Fatalf("submit returned opts=%v cancelled=%v (%s)", opts, cancelled, f.err)
	}
	if opts.Base != "main" {
		t.Errorf("opts.Base = %q, want main", opts.Base)
	}
}

func TestBaseFieldAutocompletesAgainstBranches(t *testing.T) {
	m, _, _ := gitModel(t) // real repo with branches "main" (current) and "develop"
	m = press(m, "n")
	f := m.form
	if !f.canWorktree || !f.worktree {
		t.Fatalf("a git project should open the form with worktree on: %+v", f)
	}

	f.branch.SetValue("feature/experiment") // unknown, so the From field is offered
	f.setField(fieldBase)
	f.base.SetValue("de") // matches only "develop", not "main"

	view := f.view(80)
	if !strings.Contains(view, "branches: develop") {
		t.Errorf("focusing From should suggest matching branches:\n%s", view)
	}
	if strings.Contains(view, "main") {
		t.Errorf("a non-matching branch must not be suggested:\n%s", view)
	}
}

func TestFormRefusesAnEmptyBase(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "n")
	f := m.form
	f.canWorktree, f.branches, f.worktree = true, []string{"main"}, true
	f.branch.SetValue("feature/new")
	f.base.SetValue("")

	opts, _ := f.submit()
	if opts != nil {
		t.Fatal("an empty base must not create a terminal")
	}
	if !strings.Contains(f.err, "base") {
		t.Errorf("error = %q, want it to mention the base", f.err)
	}
}
