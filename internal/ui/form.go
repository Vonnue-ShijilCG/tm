package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// newTermForm collects the options for a new terminal: for Git projects an
// optional dedicated worktree with a branch to check out or create; for
// other projects there is nothing to choose and Enter simply creates it.
type newTermForm struct {
	project *workspace.Project
	back    focus // where Esc returns

	branch   textinput.Model
	base     textinput.Model
	command  textinput.Model
	worktree bool

	canWorktree bool     // project is a Git repository
	branches    []string // existing local branches, for hints
	current     string   // checked-out branch of the project

	field int // one of the field* constants
	err   string
}

const (
	fieldBranch = iota
	fieldBase
	fieldCommand
	fieldWorktree
)

func newForm(ws *workspace.Workspace, p *workspace.Project, back focus) *newTermForm {
	f := &newTermForm{
		project: p,
		back:    back,
		branch:  newInput("e.g. feature/payment"),
		base:    newInput("e.g. main"),
		command: newInput("e.g. npm run dev (optional)"),
	}
	// Commands run much longer than branch names (an agent invocation with
	// flags, a long npm/go command), so give this field alone a larger cap;
	// newInput's default of 80 stays right for branch and base.
	f.command.CharLimit = 256
	if branches, current, err := ws.Branches(p); err == nil {
		f.canWorktree, f.branches, f.current, f.worktree = true, branches, current, true
		f.base.SetValue(ws.DefaultBranch(p))
	}
	f.setField(f.fields()[0])
	return f
}

func newInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.CharLimit = 80
	ti.Width = 40
	return ti
}

// fields returns the navigable fields in display order. A non-Git project has
// no branch to choose; the worktree row is always present, disabled when it
// cannot apply, so the form always has something to land on. The base is
// only asked for when it matters: creating a worktree on a branch that does
// not exist yet.
func (f *newTermForm) fields() []int {
	if !f.canWorktree {
		return []int{fieldCommand, fieldWorktree}
	}
	if f.worktree && f.branchIsNew() {
		return []int{fieldBranch, fieldBase, fieldCommand, fieldWorktree}
	}
	return []int{fieldBranch, fieldCommand, fieldWorktree}
}

// branchIsNew reports whether the typed branch does not exist yet, which is
// the only case where a base has to be chosen.
func (f *newTermForm) branchIsNew() bool {
	v := strings.TrimSpace(f.branch.Value())
	if v == "" {
		return false
	}
	for _, br := range f.branches {
		if br == v {
			return false
		}
	}
	return true
}

// moveField steps through fields() by delta, wrapping.
func (f *newTermForm) moveField(delta int) {
	fs := f.fields()
	cur := 0
	for i, id := range fs {
		if id == f.field {
			cur = i
		}
	}
	f.setField(fs[((cur+delta)%len(fs)+len(fs))%len(fs)])
}

// normalizeField moves focus onto a field that currently exists, in case the
// set changed while the user was typing.
func (f *newTermForm) normalizeField() {
	for _, id := range f.fields() {
		if id == f.field {
			return
		}
	}
	f.setField(f.fields()[0])
}

// update handles a key while the form is open. It returns the options to
// create when the user submits, and whether the form was cancelled.
func (f *newTermForm) update(msg tea.KeyMsg) (opts *workspace.Options, cancelled bool) {
	f.normalizeField()
	switch {
	case key.Matches(msg, keys.FormCancel):
		return nil, true
	case key.Matches(msg, keys.FormSubmit):
		return f.submit()
	case key.Matches(msg, keys.FormNext):
		f.moveField(1)
	case key.Matches(msg, keys.FormPrev):
		f.moveField(-1)
	case f.field == fieldWorktree:
		if key.Matches(msg, keys.FormToggle) && f.canWorktree {
			f.worktree = !f.worktree
		}
	case f.field == fieldBranch && f.canWorktree:
		f.branch, _ = f.branch.Update(msg)
	case f.field == fieldBase:
		f.base, _ = f.base.Update(msg)
	case f.field == fieldCommand:
		f.command, _ = f.command.Update(msg)
	}
	return nil, false
}

func (f *newTermForm) submit() (*workspace.Options, bool) {
	opts := &workspace.Options{Command: strings.TrimSpace(f.command.Value())}
	// A single-line textinput sanitises newlines on typing and paste, so this
	// cannot currently trigger; it guards the invariant if that ever changes.
	if strings.ContainsAny(opts.Command, "\n\r") {
		f.err = "a command must be a single line"
		f.setField(fieldCommand)
		return nil, false
	}
	if f.canWorktree && f.worktree {
		opts.Worktree = true
		opts.Branch = strings.TrimSpace(f.branch.Value())
		if opts.Branch == "" {
			f.err = "a branch is required to create a worktree"
			f.setField(fieldBranch)
			return nil, false
		}
		if f.branchIsNew() {
			opts.Base = strings.TrimSpace(f.base.Value())
			if opts.Base == "" {
				f.err = "a base is required for a new branch"
				f.setField(fieldBase)
				return nil, false
			}
		}
	}
	return opts, false
}

func (f *newTermForm) setField(i int) {
	f.field = i
	f.branch.Blur()
	f.base.Blur()
	f.command.Blur()
	switch i {
	case fieldBranch:
		f.branch.Focus()
	case fieldBase:
		f.base.Focus()
	case fieldCommand:
		f.command.Focus()
	}
}

func (f *newTermForm) view(width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n %s\n\n", titleStyle.Render("New terminal · "+f.project.Name))

	if f.canWorktree {
		branchHint := ""
		if v := strings.TrimSpace(f.branch.Value()); v != "" {
			branchHint = hintStyle.Render("  (new branch)")
			for _, br := range f.branches {
				if br == v {
					branchHint = hintStyle.Render("  (existing)")
				}
			}
		}
		b.WriteString(f.row(fieldBranch, "Branch", f.branch.View()+branchHint))
		if f.worktree && f.branchIsNew() {
			b.WriteString(f.row(fieldBase, "From", f.base.View()))
		}
		b.WriteString(f.row(fieldCommand, "Command", f.command.View()))

		wt := "no"
		if f.worktree {
			wt = "yes"
			if path, err := workspace.WorktreePath(".", f.project.Name, strings.TrimSpace(f.branch.Value())); err == nil {
				wt += hintStyle.Render("  → " + path)
			}
		}
		b.WriteString(f.row(fieldWorktree, "Worktree", wt))

		if f.field == fieldBranch {
			b.WriteString("\n " + hintStyle.Render("branches: "+f.matchingBranches(f.branch.Value(), width-12)) + "\n")
		} else if f.field == fieldBase {
			b.WriteString("\n " + hintStyle.Render("branches: "+f.matchingBranches(f.base.Value(), width-12)) + "\n")
		}
	} else {
		b.WriteString(f.row(fieldCommand, "Command", f.command.View()))
		b.WriteString(f.row(fieldWorktree, "Worktree", hintStyle.Render("no (not a git repository)")))
		b.WriteString("\n " + hintStyle.Render("enter opens a shell in the project directory") + "\n")
	}

	if f.err != "" {
		b.WriteString("\n " + errorStyle.Render(f.err) + "\n")
	}
	return b.String()
}

func (f *newTermForm) row(field int, label, value string) string {
	marker := "  "
	if f.field == field {
		marker = selectedStyle.Render("› ")
		label = selectedStyle.Render(label)
	}
	return fmt.Sprintf(" %s%-10s %s\n", marker, label, value)
}

// matchingBranches lists existing branches containing the typed text.
func (f *newTermForm) matchingBranches(value string, width int) string {
	typed := strings.ToLower(strings.TrimSpace(value))
	var matches []string
	for _, br := range f.branches {
		if strings.Contains(strings.ToLower(br), typed) {
			if br == f.current {
				br += "*"
			}
			matches = append(matches, br)
		}
	}
	if len(matches) == 0 {
		return "none match"
	}
	s := strings.Join(matches, ", ")
	if width > 0 && len(s) > width {
		s = s[:max(width-1, 0)] + "…"
	}
	return s
}
