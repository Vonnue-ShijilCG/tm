package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// manager is the terminal manager of one project: every terminal the
// project has — visible, hidden but running, or saved and not running —
// with its worktree and branch as secondary information. Enter shows or
// restores exactly the selected terminal, h hides it, d deletes it for
// good after confirmation. Worktrees tm owns that no terminal uses are
// listed apart, at the bottom, so they can be cleaned up deliberately;
// they are never presented as terminals.
type manager struct {
	project *workspace.Project
	rows    []managerRow
	cursor  int
	confirm bool   // waiting for y to act on rows[cursor]
	prompt  string // the question asked while confirm is set
	notice  string // last result or error
	loadErr string // git problem listing worktrees, e.g. not a repository
	branch  func(dir string) string
	unseen  func(*workspace.Terminal) bool // printed while off screen; see Model.unseen
}

// rowBranch is the Git context of a terminal row: the branch checked out
// where it runs (or would run), from the definition when the directory
// cannot be read.
func (mg *manager) rowBranch(r workspace.Row) string {
	dir := r.Definition.Worktree
	if dir == "" {
		dir = mg.project.Path
	}
	if b := mg.branch(dir); b != "" {
		return b
	}
	return r.Definition.Branch
}

// managerRow is either a terminal (terminal set) or an unused worktree
// (worktree set).
type managerRow struct {
	terminal *workspace.Row
	worktree *workspace.WorktreeInfo
	visible  bool   // running terminal shown in the layout
	branch   string // branch checked out where the terminal runs, "" if unknown
}

// managerAction is what the manager asks the model to do after a key.
type managerAction int

const (
	mgNone    managerAction = iota
	mgClose                 // esc
	mgShow                  // show / restore the selected terminal
	mgHide                  // hide the selected visible terminal
	mgNew                   // open the new-terminal form
	mgOpenAll               // restore every eligible saved terminal
	mgDelete                // confirmed: delete the selected running terminal for good
	mgChanged               // persisted state changed here (definition forgotten, worktree removed)
)

// newManager builds the manager for p. branch reports the branch checked
// out in a directory ("" when unknown), so rows can show Git context.
func newManager(ws *workspace.Workspace, p *workspace.Project, root *pane, branch func(dir string) string, unseen func(*workspace.Terminal) bool) *manager {
	mg := &manager{project: p, branch: branch, unseen: unseen}
	mg.reload(ws, root)
	return mg
}

// reload rebuilds the rows from the workspace and the visible layout.
func (mg *manager) reload(ws *workspace.Workspace, root *pane) {
	rows, unused, err := ws.Rows(mg.project)
	mg.rows, mg.loadErr = nil, ""
	switch {
	case errors.Is(err, git.ErrNotRepository):
		mg.loadErr = mg.project.Name + " is not a git repository: terminals here run in the project directory"
	case err != nil:
		mg.loadErr = err.Error()
	}
	for i := range rows {
		r := &rows[i]
		mg.rows = append(mg.rows, managerRow{terminal: r, visible: r.Terminal != nil && root.contains(r.Terminal), branch: mg.rowBranch(*r)})
	}
	for i := range unused {
		mg.rows = append(mg.rows, managerRow{worktree: &unused[i]})
	}
	if mg.cursor >= len(mg.rows) {
		mg.cursor = max(len(mg.rows)-1, 0)
	}
}

func (mg *manager) selected() *managerRow {
	if mg.cursor < 0 || mg.cursor >= len(mg.rows) {
		return nil
	}
	return &mg.rows[mg.cursor]
}

// selectedTerminal returns the running terminal of the selected row, if any.
func (mg *manager) selectedTerminal() *workspace.Terminal {
	if r := mg.selected(); r != nil && r.terminal != nil {
		return r.terminal.Terminal
	}
	return nil
}

// update handles a key and reports what the model should do next. Actions
// that only touch saved definitions or worktrees are done here.
func (mg *manager) update(msg tea.KeyMsg, ws *workspace.Workspace) managerAction {
	if mg.confirm {
		mg.confirm = false
		r := mg.selected()
		if r == nil || !key.Matches(msg, keys.Confirm) {
			mg.notice = "cancelled"
			return mgNone
		}
		switch {
		case r.worktree != nil:
			if err := ws.RemoveWorktree(mg.project, *r.worktree, true); err != nil {
				mg.notice = err.Error()
				return mgNone
			}
			mg.notice = fmt.Sprintf("removed worktree %q (branch %s kept)", r.worktree.Name, r.worktree.Branch)
			return mgChanged
		case r.terminal.Terminal != nil:
			return mgDelete
		default:
			def := r.terminal.Definition
			note := ws.DeleteTerminal(mg.project, def, nil)
			mg.notice = deletionNotice(def, nil, note)
			return mgChanged
		}
	}

	switch {
	case key.Matches(msg, keys.MgClose):
		return mgClose
	case key.Matches(msg, keys.MgDown):
		if mg.cursor < len(mg.rows)-1 {
			mg.cursor++
		}
	case key.Matches(msg, keys.MgUp):
		if mg.cursor > 0 {
			mg.cursor--
		}
	case key.Matches(msg, keys.MgShow):
		if r := mg.selected(); r != nil {
			if r.worktree != nil {
				mg.notice = "an unused worktree is not a terminal: d removes it, n creates a terminal"
				return mgNone
			}
			return mgShow
		}
	case key.Matches(msg, keys.MgHide):
		if r := mg.selected(); r != nil {
			switch {
			case r.worktree != nil || r.terminal.Terminal == nil:
				mg.notice = "only a running terminal can be hidden"
			case !r.visible:
				mg.notice = fmt.Sprintf("terminal %d is already hidden", r.terminal.Terminal.Num)
			default:
				return mgHide
			}
		}
	case key.Matches(msg, keys.MgNew):
		return mgNew
	case key.Matches(msg, keys.MgOpenAll):
		return mgOpenAll
	case key.Matches(msg, keys.MgDelete):
		if r := mg.selected(); r != nil {
			mg.confirm, mg.notice, mg.prompt = true, "", mg.confirmText(*r, ws)
		}
	}
	return mgNone
}

func (mg *manager) view(width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n %s\n %s\n\n", selectedStyle.Render(mg.project.Name), hintStyle.Render("Manage terminals"))

	if mg.loadErr != "" {
		b.WriteString(" " + hintStyle.Render(mg.loadErr) + "\n")
	}
	terminals := 0
	for _, r := range mg.rows {
		if r.terminal != nil {
			terminals++
		}
	}
	if terminals == 0 {
		b.WriteString(" " + hintStyle.Render("No terminals yet. Press n to create one.") + "\n")
	}
	for i, r := range mg.rows {
		if i == terminals {
			b.WriteString("\n " + hintStyle.Render("Unused worktrees (no terminal uses them)") + "\n")
		}
		for _, line := range mg.lines(i, r, width) {
			b.WriteString(line + "\n")
		}
	}
	if r := mg.selected(); r != nil && r.worktree != nil { // where the leftover lives is what cleanup needs to know
		b.WriteString("\n " + hintStyle.Render("path  "+leftTruncate(r.worktree.Path, width-10)) + "\n")
	}

	if mg.confirm {
		b.WriteString("\n " + errorStyle.Render(mg.prompt) + "\n " + errorStyle.Render("y confirm · any other key cancels") + "\n")
	} else if mg.notice != "" {
		b.WriteString("\n " + hintStyle.Render(mg.notice) + "\n")
	}
	return b.String()
}

// lines renders one row. A terminal takes two lines: its identity, the
// same `Terminal 2 · ⎇ feature/login` as its pane header, then the program
// it runs and its state, which is never dropped:
//
//	● Terminal 2 · ⎇ feature/login
//	    claude                                 hidden
//	○ Terminal · ⎇ feature/api
//	                                           saved
//
// A saved definition has no number yet. An unused worktree takes one line:
// directory name, branch and cleanliness.
func (mg *manager) lines(i int, r managerRow, width int) []string {
	indent, mark := "   ", "○"
	if i == mg.cursor {
		indent = " › "
	}
	budget := max(width-5, 1) // after the indent, marker and space
	if r.worktree != nil {
		wt := r.worktree
		state := "clean"
		switch {
		case wt.Missing:
			state = "directory missing"
		case wt.Dirty:
			state = "dirty"
		}
		label := wt.Name
		if wt.Branch != "" {
			label += " · " + wt.Branch
		}
		return []string{mg.styled(i, indent+mark+" "+columns(label, state, budget))}
	}

	label, program, state := "Terminal", "", "saved"
	if cmd := r.terminal.Definition.Command; cmd != "" {
		label = stripControlChars(cmd)
	}
	if t := r.terminal.Terminal; t != nil {
		label = terminalLabel(t)
		program, state = programOf(t), "hidden"
		if r.visible {
			mark, state = "●", "visible"
		} else if mg.unseen != nil && mg.unseen(t) {
			// This is where a hidden terminal is looked for, and the only
			// place its output can be reported: its pane is not on screen to
			// carry a mark of its own.
			state = "hidden · new output"
		}
	} else {
		switch {
		case r.terminal.Missing:
			state = "saved · worktree missing"
		case r.terminal.Stale:
			state = "saved · worktree not in git"
		}
	}
	return []string{
		mg.styled(i, indent+mark+" "+ansi.Truncate(identity(label, r.branch, r.terminal.Definition.Worktree != ""), budget, "…")),
		mg.styled(i, "     "+columns(program, state, budget)),
	}
}

// columns lays a left-aligned label and a right-hand state on one line of
// the given width, favouring the state when space is short.
func columns(label, state string, width int) string {
	labelWidth := max(width-len(state)-2, 4)
	return fmt.Sprintf("%-*s  %s", labelWidth, ansi.Truncate(label, labelWidth, "…"), state)
}

func (mg *manager) styled(i int, line string) string {
	if i == mg.cursor {
		return selectedStyle.Render(line)
	}
	return line
}

func (mg *manager) confirmText(r managerRow, ws *workspace.Workspace) string {
	if wt := r.worktree; wt != nil {
		switch {
		case wt.Missing:
			return fmt.Sprintf("Remove worktree %q (%s)? Its directory is already gone; this only cleans up git's record.", wt.Name, wt.Branch)
		case wt.Dirty:
			return fmt.Sprintf("Remove worktree %q (%s)? It has UNCOMMITTED CHANGES that will be lost.", wt.Name, wt.Branch)
		default:
			return fmt.Sprintf("Remove worktree %q (%s)? It is clean.", wt.Name, wt.Branch)
		}
	}
	return deletionPrompt(ws, mg.project, r.terminal.Definition, r.terminal.Terminal)
}

// leftTruncate keeps the end of a path, which is the informative part.
func leftTruncate(s string, width int) string {
	if width < 4 || len(s) <= width {
		return s
	}
	return "…" + s[len(s)-width+1:]
}
