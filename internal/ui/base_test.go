package ui

import (
	"strings"
	"testing"
)

func TestBaseFieldAppearsOnlyForANewBranch(t *testing.T) {
	m, _, _ := gitModel(t) // real repo with branches "main" (current) and "develop"
	m = press(m, "n")
	m = typeText(m, "develop") // existing
	f := m.form
	if f == nil {
		t.Fatal("n should open the form")
	}
	if f.loc.NewBranch {
		t.Error("an existing branch must not be treated as new")
	}
	if strings.Contains(f.view(80), "From") {
		t.Error("no base is chosen for an existing branch")
	}

	m = press(m, "esc", "n")
	m = typeText(m, "feature/new") // does not exist
	f = m.form
	if !f.loc.NewBranch {
		t.Error("an unknown branch must be treated as new")
	}
	if !strings.Contains(f.view(80), "From") {
		t.Error("a new branch must offer a base")
	}
}

func TestFormPassesTheBaseForANewBranch(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	m = typeText(m, "feature/new")
	f := m.form
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
	if !f.canWorktree {
		t.Fatalf("a git project should open the form with worktree support: %+v", f)
	}

	m = typeText(m, "feature/experiment") // unknown, so the From field is offered
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
	m, _, _ := gitModel(t)
	m = press(m, "n")
	m = typeText(m, "feature/new")
	f := m.form
	f.base.SetValue("")

	opts, _ := f.submit()
	if opts != nil {
		t.Fatal("an empty base must not create a terminal")
	}
	if !strings.Contains(f.err, "base") {
		t.Errorf("error = %q, want it to mention the base", f.err)
	}
}
