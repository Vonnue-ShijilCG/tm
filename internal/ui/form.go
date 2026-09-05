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
	worktree bool

	canWorktree bool     // project is a Git repository
	branches    []string // existing local branches, for hints
	current     string   // checked-out branch of the project

	field int // 0 branch, 1 worktree
	err   string
}

const (
	fieldBranch = iota
	fieldWorktree
)

func newForm(ws *workspace.Workspace, p *workspace.Project, back focus) *newTermForm {
	f := &newTermForm{project: p, back: back, branch: newInput("e.g. feature/payment")}
	if branches, current, err := ws.Branches(p); err == nil {
		f.canWorktree, f.branches, f.current, f.worktree = true, branches, current, true
	}
	f.branch.Focus()
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

func (f *newTermForm) fieldCount() int {
	if f.canWorktree {
		return 2
	}
	return 1 // the (disabled) worktree row, so field arithmetic stays valid
}

// update handles a key while the form is open. It returns the options to
// create when the user submits, and whether the form was cancelled.
func (f *newTermForm) update(msg tea.KeyMsg) (opts *workspace.Options, cancelled bool) {
	switch {
	case key.Matches(msg, keys.FormCancel):
		return nil, true
	case key.Matches(msg, keys.FormSubmit):
		return f.submit()
	case key.Matches(msg, keys.FormNext):
		f.setField((f.field + 1) % f.fieldCount())
	case key.Matches(msg, keys.FormPrev):
		f.setField((f.field + f.fieldCount() - 1) % f.fieldCount())
	case f.field == fieldWorktree:
		if key.Matches(msg, keys.FormToggle) && f.canWorktree {
			f.worktree = !f.worktree
		}
	case f.field == fieldBranch && f.canWorktree:
		f.branch, _ = f.branch.Update(msg)
	}
	return nil, false
}

func (f *newTermForm) submit() (*workspace.Options, bool) {
	opts := &workspace.Options{}
	if f.canWorktree && f.worktree {
		opts.Worktree = true
		opts.Branch = strings.TrimSpace(f.branch.Value())
		if opts.Branch == "" {
			f.err = "a branch is required to create a worktree"
			f.setField(fieldBranch)
			return nil, false
		}
	}
	return opts, false
}

func (f *newTermForm) setField(i int) {
	f.field = i
	f.branch.Blur()
	if i == fieldBranch {
		f.branch.Focus()
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

		wt := "no"
		if f.worktree {
			wt = "yes"
			if path, err := workspace.WorktreePath(".", f.project.Name, strings.TrimSpace(f.branch.Value())); err == nil {
				wt += hintStyle.Render("  → " + path)
			}
		}
		b.WriteString(f.row(fieldWorktree, "Worktree", wt))

		if f.field == fieldBranch {
			b.WriteString("\n " + hintStyle.Render("branches: "+f.matchingBranches(width-12)) + "\n")
		}
	} else {
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
func (f *newTermForm) matchingBranches(width int) string {
	typed := strings.ToLower(strings.TrimSpace(f.branch.Value()))
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
