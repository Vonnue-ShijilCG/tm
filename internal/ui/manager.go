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
	notice  string // last result or error
	loadErr string // git problem listing worktrees, e.g. not a repository
}

// managerRow is either a terminal (terminal set) or an unused worktree
// (worktree set).
type managerRow struct {
	terminal *workspace.Row
	worktree *workspace.WorktreeInfo
	visible  bool // running terminal shown in the layout
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

func newManager(ws *workspace.Workspace, p *workspace.Project, root *pane) *manager {
	mg := &manager{project: p}
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
		mg.rows = append(mg.rows, managerRow{terminal: r, visible: r.Terminal != nil && root.contains(r.Terminal)})
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
			ws.ForgetSaved(mg.project, r.terminal.Definition)
			mg.notice = fmt.Sprintf("deleted saved terminal %q", rowName(*r.terminal))
			if r.terminal.Definition.Worktree != "" {
				mg.notice += " (worktree kept)"
			}
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
		if mg.selected() != nil {
			mg.confirm, mg.notice = true, ""
		}
	}
	return mgNone
}

func (mg *manager) view(width int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n %s\n\n", titleStyle.Render("Manage terminals · "+mg.project.Name))

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
		if i == terminals && i > 0 {
			b.WriteString("\n " + hintStyle.Render("Unused worktrees") + "\n")
		} else if i == terminals {
			b.WriteString("\n " + hintStyle.Render("Unused worktrees (no terminal uses them)") + "\n")
		}
		b.WriteString(mg.line(i, r, width) + "\n")
	}
	if r := mg.selected(); r != nil {
		if path := rowPath(*r); path != "" {
			b.WriteString("\n " + hintStyle.Render("path  "+leftTruncate(path, width-10)) + "\n")
		}
	}

	if mg.confirm {
		if r := mg.selected(); r != nil {
			b.WriteString("\n " + errorStyle.Render(mg.confirmText(*r)) + "\n " + errorStyle.Render("y confirm · any other key cancels") + "\n")
		}
	} else if mg.notice != "" {
		b.WriteString("\n " + hintStyle.Render(mg.notice) + "\n")
	}
	return b.String()
}

// line renders one row: marker, terminal, its state, and where it runs.
//
//	● 2 Login AI        visible   feature/login · worktree
//	○ 4 Server          hidden    feature/server · worktree
//	○   Payment         saved     feature/payment · worktree
func (mg *manager) line(i int, r managerRow, width int) string {
	marker, label, where, state := "○", "", "", ""
	switch {
	case r.worktree != nil:
		label, where = r.worktree.Name, r.worktree.Branch+" · worktree"
		switch {
		case r.worktree.Missing:
			state = "directory missing"
		case r.worktree.Dirty:
			state = "dirty"
		default:
			state = "clean"
		}
	case r.terminal.Terminal != nil:
		label = fmt.Sprintf("%d %s", r.terminal.Terminal.Num, rowName(*r.terminal))
		where, state = rowWhere(*r.terminal), "hidden"
		if r.visible {
			marker, state = "●", "visible"
		}
	default:
		label, where, state = "  "+rowName(*r.terminal), rowWhere(*r.terminal), "saved"
		switch {
		case r.terminal.Missing:
			state = "saved · worktree missing"
		case r.terminal.Stale:
			state = "saved · worktree not in git"
		}
	}
	labelWidth := max(min(20, width-4-2-10), 6) // narrow windows shorten the name column, not the state
	line := fmt.Sprintf("%s %-*s %-9s %s", marker, labelWidth, ansi.Truncate(label, labelWidth, "…"), state, where)
	line = ansi.Truncate(line, max(width-4, 1), "…")
	if i == mg.cursor {
		return " " + selectedStyle.Render("› "+line)
	}
	return "   " + line
}

func (mg *manager) confirmText(r managerRow) string {
	switch {
	case r.worktree != nil:
		wt := r.worktree
		switch {
		case wt.Missing:
			return fmt.Sprintf("Remove worktree %q (%s)? Its directory is already gone; this only cleans up git's record.", wt.Name, wt.Branch)
		case wt.Dirty:
			return fmt.Sprintf("Remove worktree %q (%s)? It has UNCOMMITTED CHANGES that will be lost.", wt.Name, wt.Branch)
		default:
			return fmt.Sprintf("Remove worktree %q (%s)? It is clean.", wt.Name, wt.Branch)
		}
	case r.terminal.Terminal != nil:
		s := fmt.Sprintf("Permanently delete terminal %q? Its process is stopped.", rowName(*r.terminal))
		if r.terminal.Definition.Worktree != "" {
			s += " The worktree is kept."
		}
		return s
	case r.terminal.Definition.Worktree != "":
		return fmt.Sprintf("Delete saved terminal %q? Its worktree is kept.", rowName(*r.terminal))
	default:
		return fmt.Sprintf("Delete saved terminal %q? Nothing on disk is touched.", rowName(*r.terminal))
	}
}

// rowName labels unnamed terminals "shell".
func rowName(r workspace.Row) string {
	if r.Definition.Name == "" {
		return "shell"
	}
	return r.Definition.Name
}

// rowWhere says where the terminal runs: its worktree and branch, or the
// project checkout.
func rowWhere(r workspace.Row) string {
	d := r.Definition
	if d.Worktree == "" {
		return "project checkout"
	}
	if d.Branch == "" {
		return "worktree"
	}
	return d.Branch + " · worktree"
}

func rowPath(r managerRow) string {
	if r.worktree != nil {
		return r.worktree.Path
	}
	return r.terminal.Definition.Worktree
}

// leftTruncate keeps the end of a path, which is the informative part.
func leftTruncate(s string, width int) string {
	if width < 4 || len(s) <= width {
		return s
	}
	return "…" + s[len(s)-width+1:]
}
