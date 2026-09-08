package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// newTermForm collects the options for a new terminal. The branch a user
// types decides where it runs: an existing branch's own worktree, a new
// worktree for a branch that does not exist yet, or the project checkout
// when the branch is left empty. For a non-Git project there is nothing to
// choose and Enter simply creates it.
type newTermForm struct {
	project *workspace.Project
	back    focus // where Esc returns

	branch  textinput.Model
	base    textinput.Model
	command textinput.Model

	canWorktree bool     // project is a Git repository
	branches    []string // existing local branches, for hints
	current     string   // checked-out branch of the project

	snap workspace.LocationSource // repository state, gathered once when the form opens
	loc  workspace.Location       // where the terminal will run; refreshed on every branch keystroke

	field int // one of the field* constants
	err   string

	// confirmDup is the command awaiting confirmation because it already
	// runs at this location, or "" when nothing is pending. A second Enter
	// with the same command confirms; any other key clears it, since the
	// command may have changed underneath it.
	confirmDup string
}

const (
	fieldBranch = iota
	fieldBase
	fieldCommand
)

// fields returns the navigable fields in display order. A non-Git project has
// no branch or base to choose, so only the command remains. The base is only
// asked for when it matters: creating a worktree on a branch that does not
// exist yet.
func (f *newTermForm) fields() []int {
	if !f.canWorktree {
		return []int{fieldCommand}
	}
	if f.loc.NewBranch {
		return []int{fieldBranch, fieldBase, fieldCommand}
	}
	return []int{fieldBranch, fieldCommand}
}

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
	// Locator gathers repository state once, in one pass, so that Locate can
	// resolve every keystroke below with no git subprocesses.
	f.snap = ws.Locator(p)
	f.loc = f.snap.Locate("")
	if branches, current, err := ws.Branches(p); err == nil {
		f.canWorktree, f.branches, f.current = true, branches, current
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
	if f.confirmDup != "" && !key.Matches(msg, keys.FormSubmit) {
		f.confirmDup, f.err = "", ""
	}
	switch {
	case key.Matches(msg, keys.FormCancel):
		return nil, true
	case key.Matches(msg, keys.FormSubmit):
		return f.submit()
	case key.Matches(msg, keys.FormNext):
		f.moveField(1)
	case key.Matches(msg, keys.FormPrev):
		f.moveField(-1)
	case f.field == fieldBranch && f.canWorktree:
		f.branch, _ = f.branch.Update(msg)
		f.loc = f.snap.Locate(strings.TrimSpace(f.branch.Value()))
	case f.field == fieldBase:
		f.base, _ = f.base.Update(msg)
	case f.field == fieldCommand:
		f.command, _ = f.command.Update(msg)
	}
	return nil, false
}

func (f *newTermForm) submit() (*workspace.Options, bool) {
	if f.loc.OutOfRoot {
		f.err = "tm can only manage worktrees inside this workspace; pick a different branch"
		return nil, false
	}
	opts := &workspace.Options{Command: strings.TrimSpace(f.command.Value())}
	// A single-line textinput sanitises newlines on typing and paste, so this
	// cannot currently trigger; it guards the invariant if that ever changes.
	if strings.ContainsAny(opts.Command, "\n\r") {
		f.err = "a command must be a single line"
		f.setField(fieldCommand)
		return nil, false
	}
	switch f.loc.Kind {
	case workspace.LocationWorktree, workspace.LocationNewWorktree:
		opts.Worktree = true
		opts.Branch = strings.TrimSpace(f.branch.Value())
		if f.loc.NewBranch {
			opts.Base = strings.TrimSpace(f.base.Value())
			if opts.Base == "" {
				f.err = "a base is required for a new branch"
				f.setField(fieldBase)
				return nil, false
			}
		}
	}
	// A command already running at this exact location asks for confirmation
	// before starting a second copy of it; different commands never do. The
	// user confirms by pressing Enter again with the same command, which is
	// how this guard is satisfied the second time through.
	if opts.Command != "" && f.confirmDup != opts.Command {
		for _, t := range f.loc.Terminals {
			if t.Command == opts.Command {
				f.confirmDup = opts.Command
				f.err = fmt.Sprintf("%s is already running here. Press enter again to start another.", opts.Command)
				return nil, false
			}
		}
	}
	f.confirmDup = ""
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
		if f.loc.NewBranch {
			b.WriteString(f.row(fieldBase, "From", f.base.View()))
		}
		b.WriteString(f.row(fieldCommand, "Command", f.command.View()))
		b.WriteString(f.row(-1, "Location", f.locationLine()))

		if f.field == fieldBranch {
			b.WriteString("\n " + hintStyle.Render("branches: "+f.matchingBranches(f.branch.Value(), width-12)) + "\n")
		} else if f.field == fieldBase {
			b.WriteString("\n " + hintStyle.Render("branches: "+f.matchingBranches(f.base.Value(), width-12)) + "\n")
		}
	} else {
		b.WriteString(f.row(fieldCommand, "Command", f.command.View()))
		b.WriteString(f.row(-1, "Location", f.locationLine()))
		b.WriteString("\n " + hintStyle.Render("enter opens a shell in the project directory") + "\n")
	}

	if f.err != "" {
		b.WriteString("\n " + errorStyle.Render(f.err) + "\n")
	}
	return b.String()
}

// locationLine describes where the terminal will run, and what tm already has
// running there. tm sees only its own terminals, so an empty list is reported
// as "no tm terminals" rather than as an idle directory.
func (f *newTermForm) locationLine() string {
	var head string
	switch f.loc.Kind {
	case workspace.LocationPlainDir:
		head = "project directory " + hintStyle.Render("(not a git repository)")
	case workspace.LocationCheckout:
		head = "project checkout"
		if f.loc.Branch != "" {
			head += " · on " + f.loc.Branch
		}
	case workspace.LocationWorktree:
		head = "worktree · " + f.loc.Path
		switch {
		case f.loc.OutOfRoot:
			head += hintStyle.Render("  (outside the workspace)")
		case f.loc.Foreign:
			head += hintStyle.Render("  (not created by tm)")
		}
	case workspace.LocationNewWorktree:
		return "new worktree · " + f.loc.Path
	}
	if f.loc.OutOfRoot {
		return head + "\n" + strings.Repeat(" ", 14) + hintStyle.Render("tm can only manage worktrees inside this workspace — pick a different branch")
	}
	if len(f.loc.Terminals) == 0 {
		if f.loc.Foreign {
			return head + "\n" + strings.Repeat(" ", 14) + hintStyle.Render("no tm terminals here — may be in use elsewhere")
		}
		return head
	}
	var names []string
	for _, t := range f.loc.Terminals {
		names = append(names, terminalLabel(t))
	}
	return head + "\n" + strings.Repeat(" ", 14) + hintStyle.Render(fmt.Sprintf("%d terminal(s) here: %s", len(names), strings.Join(names, ", ")))
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
