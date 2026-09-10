// Package ui contains the Bubble Tea model for the tm terminal UI.
package ui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

type focus int

const (
	focusSidebar focus = iota
	focusMain
)

const statusHeight = 1

// spawnShell is the default workspace.Spawner: the user's shell in a PTY.
func spawnShell(dir string, cols, rows int) (workspace.Session, error) {
	s, err := terminal.Start(terminal.Config{Command: terminal.DefaultShell(), Dir: dir, Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Messages produced by the per-terminal watcher command.
type (
	terminalOutputMsg struct {
		id int
		s  workspace.Session
	}
	terminalExitedMsg struct {
		id  int
		err error
	}
)

// Model is the root Bubble Tea model: the sidebar cursor, the focused pane
// and the workspace tree of projects and their terminals.
type Model struct {
	root string
	ws   *workspace.Workspace

	cursor   int // highlighted row in the sidebar
	selected int // project whose terminals fill the main pane, -1 when none
	focus    focus
	form     *newTermForm // open "new terminal" dialog, nil otherwise
	panel    *manager     // open terminal manager, nil otherwise
	diff     *diffView    // open uncommitted-changes view, nil otherwise
	help     bool         // help screen shown over everything
	notice   string       // one-line message in the status bar (state file problems)
	quitting bool         // set once quit began; later exit messages must not rewrite state

	zoom bool // render only the focused terminal at full size; the layout tree is untouched

	scroll map[*workspace.Terminal]int // lines scrolled back into history per pane; absent or 0 = live

	deleting     *workspace.Terminal          // alt+delete pressed: waiting for y to delete this terminal for good
	deletePrompt string                       // the question shown while deleting is set
	layouts      map[*workspace.Project]*pane // visible panes per project; nil root = none (running terminals not in the tree are hidden)
	allVisible   map[*workspace.Project]*pane // last tree that showed every running terminal, so a hidden pane can return to its exact place
	branches     *branchLookup                // checked-out branch per directory, for pane headers and the manager
	pendingSplit splitDir                     // where the form's new terminal goes; 0 = auto

	width, height int
}

// New builds a Model for the given workspace root and its projects, and
// restores saved terminal definitions from the workspace state file.
func New(root string, projects []project.Project) Model {
	m := Model{root: root, ws: workspace.New(root, projects, spawnShell), selected: -1, layouts: map[*workspace.Project]*pane{}, allVisible: map[*workspace.Project]*pane{}, scroll: map[*workspace.Terminal]int{}, branches: newBranchLookup()}
	if err := m.ws.Restore(); err != nil {
		m.notice = "could not read saved state: " + err.Error()
	}
	if l := m.ws.CurrentLimit(); l.Notice != "" { // an invalid max_terminals is reported at startup and again on each use
		m.notice = l.Notice
	}
	return m
}

// noteLimit shows the problem found the last time the terminal limit was
// read, if any, unless the action just performed has something more
// pressing to say (a restore summary, a refusal). Called after actions that
// read the limit, since a successful persist clears the notice.
func (m *Model) noteLimit() {
	if n := m.ws.LimitNotice(); n != "" && m.notice == "" {
		m.notice = n
	}
}

// openForm starts the new-terminal dialog for p unless p already runs as
// many terminals as the configuration allows; then the status bar says so
// and nothing opens. dir is where the terminal goes (0 = automatic).
func (m *Model) openForm(p *workspace.Project, dir splitDir, back focus) {
	if err := m.ws.CanCreateTerminal(p); err != nil {
		m.notice = err.Error()
		return
	}
	m.noteLimit()
	m.pendingSplit, m.form = dir, newForm(m.ws, p, back)
}

// persist saves the workspace configuration; failures are shown, not fatal.
// Projects with running terminals record what is on screen first (see
// Workspace.RecordLayout for when the saved layout is kept instead);
// projects without keep whatever arrangement was restored from the file.
func (m *Model) persist() {
	for _, p := range m.ws.Projects {
		if t := p.ActiveTerminal(); t != nil {
			m.ws.RecordLayout(p, layoutFromPane(m.layouts[p]), t.Key())
		}
	}
	if err := m.ws.Save(); err != nil {
		m.notice = "could not save state: " + err.Error()
		return
	}
	m.notice = ""
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for _, p := range m.ws.Projects {
			m.applyLayout(p)
		}
		if m.diff != nil {
			m.diff.scroll(0, m.diffRows()) // a taller window must not show blank rows under the file
		}

	case tea.MouseMsg:
		m.handleMouse(msg)

	case tea.KeyMsg:
		if m.help {
			if key.Matches(msg, keys.HelpClose) {
				m.help = false
			}
			return m, nil
		}
		if m.diff != nil {
			return m.updateDiff(msg)
		}
		if m.panel != nil {
			return m.updateManager(msg)
		}
		if m.form != nil {
			return m.updateForm(msg)
		}
		if p, t := m.activeTerminal(); t != nil && m.focus == focusMain {
			return m.updateTerminal(msg, p, t)
		}
		return m.updateSidebar(msg)

	case terminalOutputMsg:
		// Re-arm only if this terminal is still registered with that session.
		if _, t := m.ws.Find(msg.id); t != nil && t.Session == msg.s {
			return m, watchTerminal(t)
		}

	case terminalExitedMsg:
		if m.quitting {
			return m, nil // sessions closed by quit; the saved state already has them
		}
		if p, t := m.ws.Remove(msg.id); t != nil {
			p.Notice = fmt.Sprintf("terminal %d %s", t.Num, exitNotice(msg.err))
			m.dropPane(p, t)
			m.refocus(p)
			m.applyLayout(p) // the neighbour takes the freed space
			m.persist()
		}
		m.settleFocus()
		if m.panel != nil {
			m.panel.reload(m.ws, m.layouts[m.panel.project]) // states may have changed
		}
	}
	return m, nil
}

// updateTerminal routes keys while a terminal has focus. Only the Alt
// shortcuts are handled here; everything else, including a lone Esc,
// goes to the program.
func (m Model) updateTerminal(msg tea.KeyMsg, p *workspace.Project, t *workspace.Terminal) (tea.Model, tea.Cmd) {
	if m.deleting != nil {
		return m.confirmDelete(msg, p)
	}
	switch {
	case key.Matches(msg, keys.Sidebar):
		m.focus = focusSidebar
	case key.Matches(msg, keys.NextTerm):
		m.cycleVisible(p, 1)
		m.applyLayout(p) // zoom follows focus
		m.persist()      // focus is part of the saved layout
	case key.Matches(msg, keys.PrevTerm):
		m.cycleVisible(p, -1)
		m.applyLayout(p)
		m.persist()
	case key.Matches(msg, keys.NewTerm):
		m.openForm(p, 0, focusMain)
	case key.Matches(msg, keys.ManageTerm):
		m.panel = newManager(m.ws, p, m.layouts[p], m.branches.get) // p is the focused terminal's project; focus is kept for esc
	case key.Matches(msg, keys.DiffTerm):
		m.openDiff(p, t)
	case key.Matches(msg, keys.PaneSide):
		m.openForm(p, splitSide, focusMain)
	case key.Matches(msg, keys.PaneStack):
		m.openForm(p, splitStack, focusMain)
	case key.Matches(msg, keys.PaneLeft):
		m.focusNeighbor(p, t, dirLeft)
	case key.Matches(msg, keys.PaneRight):
		m.focusNeighbor(p, t, dirRight)
	case key.Matches(msg, keys.PaneUp):
		m.focusNeighbor(p, t, dirUp)
	case key.Matches(msg, keys.PaneDown):
		m.focusNeighbor(p, t, dirDown)
	case key.Matches(msg, keys.PaneGrow):
		if m.layouts[p].resize(t, resizeStep) {
			m.applyLayout(p)
			m.persist()
		}
	case key.Matches(msg, keys.PaneShrink):
		if m.layouts[p].resize(t, -resizeStep) {
			m.applyLayout(p)
			m.persist()
		}
	case key.Matches(msg, keys.PaneZoom):
		m.toggleZoom(p)
	case key.Matches(msg, keys.HideTerm):
		m.hide(p, t) // the process keeps running; the manager (m) shows it again
	case key.Matches(msg, keys.DeleteTerm):
		m.deleting, m.deletePrompt = t, deletionPrompt(m.ws, p, t.Definition(), t) // destructive: ask first
	default:
		delete(m.scroll, t) // typing returns a scrolled pane to live first
		forwardKey(t.Session, msg)
	}
	return m, nil
}

// confirmDelete handles the key after alt+delete: y deletes the terminal's
// definition for good, with the dedicated worktree it owned; anything else cancels.
func (m Model) confirmDelete(msg tea.KeyMsg, p *workspace.Project) (tea.Model, tea.Cmd) {
	t := m.deleting
	m.deleting = nil
	if !key.Matches(msg, keys.Confirm) {
		return m, nil
	}
	m.deleteTerminal(p, t)
	return m, nil
}

// deleteTerminal is the one destructive operation on a running terminal,
// shared by alt+delete and the manager's d: the process is stopped, the
// definition forgotten, the pane removed, and the dedicated worktree the
// terminal owned removed with it (see Workspace.DeleteTerminal). The
// project checkout is never touched.
func (m *Model) deleteTerminal(p *workspace.Project, t *workspace.Terminal) {
	if _, found := m.ws.Find(t.ID); found == nil {
		return
	}
	def := t.Definition()
	_ = t.Session.Close() // its exit message finds no terminal and is ignored
	note := m.ws.DeleteTerminal(p, def, t)
	m.dropPane(p, t)
	m.refocus(p)
	m.applyLayout(p)
	m.settleFocus()
	m.persist()
	m.notice = deletionNotice(def, t, note)
}

// hide takes t out of p's visible layout and nothing else: the process,
// the definition and any worktree stay exactly as they are. Focus moves to
// a visible terminal; with none left the main pane says so and the
// sidebar takes focus.
func (m *Model) hide(p *workspace.Project, t *workspace.Terminal) {
	if root := m.layouts[p]; len(root.leaves()) == len(p.Terminals) {
		m.allVisible[p] = root.clone() // remember where everything was
	}
	m.dropPane(p, t)
	m.refocus(p)
	m.applyLayout(p)
	m.settleFocus()
	m.persist()
}

// show makes the running terminal t visible and focused. When the saved
// layout describes exactly the terminals that would then be visible it is
// reapplied (a hidden pane returns to its place); otherwise t is placed by
// the usual rules: a balanced grid grows, a user-shaped layout has its
// focused pane split.
func (m *Model) show(p *workspace.Project, t *workspace.Terminal) {
	root := m.layouts[p]
	if !root.contains(t) {
		var want []*workspace.Terminal // in creation order, so same-key shells keep their places
		for _, cur := range p.Terminals {
			if cur == t || root.contains(cur) {
				want = append(want, cur)
			}
		}
		if before := m.allVisible[p]; before != nil && sameTerminals(before.leaves(), want) && before.fits(m.mainArea()) {
			m.layouts[p] = before.clone() // exactly the arrangement before the hide
		} else if tree := m.fromSaved(p, want); tree != nil {
			m.layouts[p] = tree
		} else {
			m.place(p, m.visibleActive(p), t, 0)
		}
	}
	selectTerminal(p, t)
	m.applyLayout(p)
}

// fromSaved rebuilds the pane tree from the project's saved layout when
// that layout places exactly the terminals in want; it returns nil
// otherwise.
func (m *Model) fromSaved(p *workspace.Project, want []*workspace.Terminal) *pane {
	if p.Layout == nil || len(p.Layout.Leaves()) != len(want) {
		return nil
	}
	used := map[*workspace.Terminal]bool{}
	tree := paneFromLayout(p.Layout, want, used)
	if len(used) != len(want) || len(tree.leaves()) != len(want) || !tree.fits(m.mainArea()) {
		return nil
	}
	return tree
}

// sameTerminals reports whether a and b hold the same terminals.
func sameTerminals(a, b []*workspace.Terminal) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[*workspace.Terminal]bool{}
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		if !set[t] {
			return false
		}
	}
	return true
}

// visibleActive is the focused terminal if it is visible, else the first
// visible one, else nil.
func (m *Model) visibleActive(p *workspace.Project) *workspace.Terminal {
	root := m.layouts[p]
	if t := p.ActiveTerminal(); t != nil && root.contains(t) {
		return t
	}
	if leaves := root.leaves(); len(leaves) > 0 {
		return leaves[0]
	}
	return nil
}

// refocus keeps the focused terminal visible: when the active terminal is
// hidden (or gone) focus moves to the next visible terminal in creation
// order, wrapping around.
func (m *Model) refocus(p *workspace.Project) {
	root := m.layouts[p]
	if t := p.ActiveTerminal(); t == nil || root.contains(t) {
		return
	}
	n := len(p.Terminals)
	for i := 1; i <= n; i++ {
		if t := p.Terminals[(p.ActiveIndex()+i)%n]; root.contains(t) {
			selectTerminal(p, t)
			return
		}
	}
}

// cycleVisible moves focus by delta among the visible terminals only, in
// terminal order (by number); hidden terminals are never brought
// back by navigation.
func (m *Model) cycleVisible(p *workspace.Project, delta int) {
	root := m.layouts[p]
	var visible []*workspace.Terminal
	cur := 0
	for _, t := range p.Terminals {
		if root.contains(t) {
			if t == p.ActiveTerminal() {
				cur = len(visible)
			}
			visible = append(visible, t)
		}
	}
	if n := len(visible); n > 0 {
		selectTerminal(p, visible[((cur+delta)%n+n)%n])
	}
}

// settleFocus returns focus to the sidebar when the selected project has
// nothing visible to type into.
func (m *Model) settleFocus() {
	if p, t := m.activeTerminal(); t == nil || m.layouts[p] == nil {
		m.focus, m.zoom = focusSidebar, false
	}
}

func selectTerminal(p *workspace.Project, t *workspace.Terminal) {
	for i, cur := range p.Terminals {
		if cur == t {
			p.Select(i)
		}
	}
}

// focusNeighbor moves focus to the visible pane next to t in direction d.
func (m *Model) focusNeighbor(p *workspace.Project, t *workspace.Terminal, d direction) {
	rs := m.paneRects(p)
	if next := neighbor(rs, t, d); next != nil {
		selectTerminal(p, next)
		m.applyLayout(p) // zoom follows focus
		m.persist()
	}
}

// place adds a freshly created terminal to the project's layout. With no
// direction asked for and an automatic layout showing every other terminal,
// the balanced grid grows by one. Otherwise — an explicit alt+v / alt+h, or
// a layout the user has shaped — the focused pane is split in dir
// (auto-chosen when 0), or taken over when it is too small to split.
func (m *Model) place(p *workspace.Project, focused, nt *workspace.Terminal, dir splitDir) {
	root := m.layouts[p]
	if dir == 0 && len(root.leaves()) == len(p.Terminals)-1 {
		if grown := m.grownGrid(root, nt); grown != nil {
			m.layouts[p] = grown
			m.applyLayout(p)
			return
		}
	}
	if root == nil || focused == nil || !root.contains(focused) {
		if root == nil {
			m.layouts[p] = leaf(nt)
		} else {
			m.layouts[p] = root.insert(root.leaves()[0], nt, splitStack)
		}
		m.applyLayout(p)
		return
	}
	r := m.paneRects(p)[focused]
	if dir == 0 {
		dir = autoDir(r)
	}
	if !canSplit(r, dir) {
		other := splitSide + splitStack - dir
		if canSplit(r, other) {
			dir = other
		} else {
			root.swap(focused, nt) // no room: the new terminal replaces the old one in this pane
			m.applyLayout(p)
			return
		}
	}
	m.layouts[p] = root.insert(focused, nt, dir)
	m.applyLayout(p)
}

// grownGrid returns root with nt added as a balanced grid, or nil when root
// is not an automatic layout or the grown grid would squeeze a pane below
// the minimum size (the caller then falls back to splitting a pane, which
// may hide a terminal — the small-window behaviour).
func (m *Model) grownGrid(root *pane, nt *workspace.Terminal) *pane {
	area := m.mainArea()
	if root == nil {
		return leaf(nt)
	}
	g, ok := gridOf(root)
	if !ok || !g.balanced() {
		return nil
	}
	grown := g.grow(nt, area).tree()
	if !grown.fits(area) {
		return nil
	}
	return grown
}

// dropPane removes t from p's layout (hiding, exiting or deleting it). The
// neighbour takes the space; when that leaves an automatic layout showing
// every other running terminal unbalanced (a middle column or row went),
// the grid is rebuilt from the remaining terminals in reading order so it
// stays a grid. Manual layouts are only ever collapsed.
func (m *Model) dropPane(p *workspace.Project, t *workspace.Terminal) {
	delete(m.scroll, t)
	root := m.layouts[p]
	if root == nil || !root.contains(t) {
		return
	}
	g, wasAuto := gridOf(root)
	for _, cur := range p.Terminals {
		if cur != t && !root.contains(cur) {
			wasAuto = false // a hidden terminal: the tree is not the whole picture
		}
	}
	wasAuto = wasAuto && g.balanced()
	collapsed := root.remove(t)
	if wasAuto && collapsed != nil && !automatic(collapsed) {
		remaining := g.without(t).rowMajor()
		rebuilt := gridFor(remaining, chooseCols(len(remaining), m.mainArea())).tree()
		if rebuilt.fits(m.mainArea()) {
			collapsed = rebuilt
		}
	}
	m.layouts[p] = collapsed
}

// mainArea is the region inside the main pane's frame.
func (m Model) mainArea() rect {
	return rect{0, 0, max(m.width-sidebarWidth-paneGutter-mainAreaStyle.GetHorizontalFrameSize(), 1),
		max(m.height-statusHeight-mainAreaStyle.GetVerticalFrameSize(), 1)}
}

func (m Model) paneRects(p *workspace.Project) map[*workspace.Terminal]rect {
	rs := map[*workspace.Terminal]rect{}
	m.layouts[p].rects(m.mainArea(), rs)
	return rs
}

// applyLayout resizes every visible session to its pane (minus the header
// row). Hidden terminals keep their size until shown again. While zoomed, the
// selected project's focused terminal gets the whole area instead.
func (m *Model) applyLayout(p *workspace.Project) {
	if m.width == 0 {
		return
	}
	for _, t := range p.Terminals { // the emulator reflows on resize, so history offsets no longer line up
		delete(m.scroll, t)
	}
	if t := p.ActiveTerminal(); m.zoom && t != nil && p == m.selectedProject() {
		area := m.mainArea()
		t.Session.Resize(sessionCols(area), sessionRows(area))
		return
	}
	for t, r := range m.paneRects(p) {
		t.Session.Resize(sessionCols(r), sessionRows(r))
	}
}

const paneHeaderHeight = 1

// sessionCols and sessionRows are the cells a terminal actually gets: its
// pane, minus the card border it is drawn inside and the header row.
func sessionCols(r rect) int {
	return max(r.w-cardStyle.GetHorizontalBorderSize(), 1)
}

func sessionRows(r rect) int {
	return max(r.h-cardStyle.GetVerticalBorderSize()-paneHeaderHeight, 1)
}

// updateDiff handles a key while the changes view is open. Like the help
// screen it consumes every key, so nothing reaches a terminal underneath.
// Up and down go to whichever half has focus; the rest act on the diff.
func (m Model) updateDiff(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	height := m.diffRows()
	d := m.diff
	switch {
	case key.Matches(msg, keys.DiffClose):
		m.diff = nil
	case key.Matches(msg, keys.DiffTab):
		d.listFocus = !d.listFocus
	case key.Matches(msg, keys.DiffRefresh):
		d.refresh()
		d.scroll(0, height) // the file may have got shorter
	case key.Matches(msg, keys.DiffNextChange):
		d.jumpChange(1, height)
	case key.Matches(msg, keys.DiffPrevChange):
		d.jumpChange(-1, height)
	case key.Matches(msg, keys.DiffLeft):
		d.scrollX(-diffHScrollStep)
	case key.Matches(msg, keys.DiffRight):
		d.scrollX(diffHScrollStep)
	case key.Matches(msg, keys.DiffDown):
		if d.listFocus {
			d.move(1)
		} else {
			d.scroll(1, height)
		}
	case key.Matches(msg, keys.DiffUp):
		if d.listFocus {
			d.move(-1)
		} else {
			d.scroll(-1, height)
		}
	}
	return m, nil
}

// diffRows is how many code rows the changes view shows: the window less the
// status bar, the modal's frame and the rows above the code. Scrolling clamps
// against it, so it must measure the frame View draws.
func (m Model) diffRows() int {
	return max(m.height-statusHeight-modalStyle.GetVerticalFrameSize()-diffChromeRows, 1)
}

// openDiff shows the uncommitted changes of wherever the user's attention is:
// the focused terminal's directory when t is set, else p's own checkout.
func (m *Model) openDiff(p *workspace.Project, t *workspace.Terminal) {
	dir, title := p.Path, p.Name
	if t != nil {
		dir = t.Dir(p)
	}
	if branch := m.branches.get(dir); branch != "" {
		title += "  " + worktreeMark + branch
	}
	m.diff = newDiffView(dir, title)
}

// updateSidebar handles navigation and program-level keys.
func (m Model) updateSidebar(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.ws.Projects) - 1
	switch {
	case key.Matches(msg, keys.Quit):
		m.quitting = true
		m.persist()
		m.ws.CloseAll()
		return m, tea.Quit
	case key.Matches(msg, keys.Focus):
		if p, t := m.activeTerminal(); t != nil && m.layouts[p] != nil { // something visible to type into
			m.refocus(p)
			m.focus = focusMain
		}
	case key.Matches(msg, keys.Down):
		if m.cursor < last {
			m.cursor++
		}
	case key.Matches(msg, keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, keys.Top):
		m.cursor = 0
	case key.Matches(msg, keys.Bottom):
		if last >= 0 {
			m.cursor = last
		}
	case key.Matches(msg, keys.New):
		if last >= 0 {
			m.openForm(m.ws.Projects[m.cursor], 0, focusSidebar)
		}
	case key.Matches(msg, keys.SplitSide):
		if last >= 0 {
			m.openForm(m.ws.Projects[m.cursor], splitSide, focusSidebar)
		}
	case key.Matches(msg, keys.SplitStack):
		if last >= 0 {
			m.openForm(m.ws.Projects[m.cursor], splitStack, focusSidebar)
		}
	case key.Matches(msg, keys.Manage):
		if last >= 0 {
			p := m.ws.Projects[m.cursor]
			m.panel = newManager(m.ws, p, m.layouts[p], m.branches.get)
		}
	case key.Matches(msg, keys.Diff):
		if last >= 0 {
			m.openDiff(m.ws.Projects[m.cursor], nil)
		}
	case key.Matches(msg, keys.Help):
		m.help = true
	case key.Matches(msg, keys.Zoom):
		if p, t := m.activeTerminal(); t != nil {
			m.focus = focusMain
			m.toggleZoom(p)
		}
	case key.Matches(msg, keys.Select):
		if last >= 0 {
			return m.enterProject(m.ws.Projects[m.cursor])
		}
	}
	return m, nil
}

// enterProject is what Enter on a project does: go to its workspace. A
// project with running terminals is focused as it is — hidden terminals
// stay hidden, unless nothing at all is visible, in which case the active
// one is shown. A project with saved definitions and nothing running gets
// all of them back at once, in their saved layout (see Model.restore); when
// none of them can actually run, the project falls back to a fresh shell
// and the notice explains why. An empty project gets its first shell.
func (m Model) enterProject(p *workspace.Project) (tea.Model, tea.Cmd) {
	switch {
	case p.ActiveTerminal() != nil:
		m.selected, m.focus = m.projectIndex(p), focusMain
		if m.layouts[p] == nil {
			m.show(p, p.ActiveTerminal())
		} else {
			m.refocus(p)
			m.applyLayout(p)
		}
		return m, nil
	case len(p.Saved) > 0:
		next, cmd, res := m.restore(p)
		if len(res.Opened) == 0 {
			notice := restoreNotice(res)
			model, cmd := m.newTerminal(p)
			fresh := model.(Model)
			if notice != "" {
				fresh.notice = notice // keep the "unavailable" summary; newTerminal's save would clear it
			}
			return fresh, cmd
		}
		return next, cmd
	default:
		return m.newTerminal(p)
	}
}

// showOpened puts a terminal that was just started into the project's
// view: the project is selected, the terminal shown and focused, the
// manager closed, the state saved. It returns the terminal's watcher.
func (m *Model) showOpened(p *workspace.Project, t *workspace.Terminal) tea.Cmd {
	m.selected, m.focus, m.panel = m.projectIndex(p), focusMain, nil
	m.show(p, t)
	m.persist()
	return watchTerminal(t)
}

// updateForm drives the new-terminal dialog. Submitting creates the
// terminal (and its worktree); errors keep the form open so the user can fix
// the input.
func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	opts, cancelled := m.form.update(msg)
	switch {
	case cancelled:
		m.focus, m.form = m.form.back, nil
		if _, t := m.activeTerminal(); t == nil {
			m.focus = focusSidebar
		}
	case opts != nil:
		p := m.form.project
		m.selected = m.projectIndex(p)
		focused := p.ActiveTerminal()
		cols, rows := m.terminalSize()
		t, err := m.ws.CreateTerminal(p, *opts, cols, rows)
		if err != nil {
			m.form.err = err.Error()
			return m, nil
		}
		m.form, m.focus = nil, focusMain
		m.place(p, focused, t, m.pendingSplit)
		m.persist()
		m.noteLimit()
		return m, watchTerminal(t)
	}
	return m, nil
}

// newTerminal starts a plain shell for p (used by Enter on a project that
// has no terminal yet), shows p and focuses the new terminal.
func (m Model) newTerminal(p *workspace.Project) (tea.Model, tea.Cmd) {
	m.selected = m.projectIndex(p)
	focused := p.ActiveTerminal()
	cols, rows := m.terminalSize()
	t, err := m.ws.NewTerminal(p, cols, rows)
	if err != nil {
		p.Notice = "could not start shell: " + err.Error()
		if p.ActiveTerminal() == nil {
			m.focus = focusSidebar
		}
		return m, nil
	}
	m.focus = focusMain
	m.place(p, focused, t, 0)
	m.persist()
	m.noteLimit()
	return m, watchTerminal(t)
}

// toggleZoom flips zoom for the focused terminal. Zoom only changes what is
// rendered and which session gets the full area; the layout tree is kept, so
// unzooming restores the previous arrangement exactly.
func (m *Model) toggleZoom(p *workspace.Project) {
	m.zoom = !m.zoom
	m.applyLayout(p)
}

// updateManager drives the terminal manager. Enter acts on the selected
// terminal only; a restores every eligible saved terminal.
func (m Model) updateManager(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.panel.project
	switch m.panel.update(msg, m.ws) {
	case mgClose:
		m.panel = nil
	case mgOpenAll:
		next, cmd, res := m.restore(p)
		if len(res.Opened) == 0 { // nothing started: the manager stays open and explains
			next.panel.notice = restoreDetails(res)
			if next.panel.notice == "" {
				next.panel.notice = "nothing to restore"
			}
			next.notice = ""
			next.panel.reload(next.ws, next.layouts[p])
		}
		return next, cmd
	case mgChanged:
		m.persist()
		m.panel.reload(m.ws, m.layouts[p])
	case mgNew:
		if err := m.ws.CanCreateTerminal(p); err != nil { // at the limit: stay here and say why
			m.panel.notice = err.Error()
			break
		}
		m.panel = nil
		m.openForm(p, 0, focusSidebar)
	case mgHide:
		if t := m.panel.selectedTerminal(); t != nil {
			m.hide(p, t)
			m.panel.reload(m.ws, m.layouts[p])
			m.panel.notice = fmt.Sprintf("terminal %d hidden; it keeps running", t.Num)
		}
	case mgDelete:
		if t := m.panel.selectedTerminal(); t != nil {
			m.deleteTerminal(p, t)
			m.panel.reload(m.ws, m.layouts[p])
			m.panel.notice, m.notice = m.notice, ""
		}
	case mgShow:
		r := m.panel.selected()
		if r == nil || r.terminal == nil {
			break
		}
		if t := r.terminal.Terminal; t != nil { // running, visible or hidden: just show it
			m.selected, m.focus, m.panel = m.projectIndex(p), focusMain, nil
			m.show(p, t)
			m.persist()
			return m, nil
		}
		cols, rows := m.terminalSize()
		t, err := m.ws.OpenSaved(p, r.terminal.Definition, cols, rows) // saved: start only this one
		if err != nil {
			m.panel.notice = err.Error()
			break
		}
		cmd := m.showOpened(p, t)
		m.noteLimit()
		return m, cmd
	}
	return m, nil
}

// restore is the explicit "restore all" (a in the manager): it reopens
// every saved terminal of p that can run (reusing worktrees), rebuilds the
// saved layout around them, restores focus and reports what could not be
// restored in the status bar. With nothing opened the model is unchanged
// apart from the notice.
func (m Model) restore(p *workspace.Project) (Model, tea.Cmd, workspace.RestoreResult) {
	cols, rows := m.terminalSize()
	res, err := m.ws.OpenAll(p, cols, rows)
	if err != nil {
		m.notice = err.Error()
		return m, nil, res
	}
	opened := res.Opened
	if len(opened) == 0 {
		m.notice = restoreNotice(res)
		return m, nil, res
	}
	m.selected = m.projectIndex(p)
	cmds := make([]tea.Cmd, 0, len(opened))
	for _, t := range opened {
		cmds = append(cmds, watchTerminal(t))
	}
	lastPlaced := m.arrange(p, opened)
	if focused := m.terminalByKey(p, p.Focus); focused != nil && m.layouts[p].contains(focused) {
		lastPlaced = focused // the saved focus wins when that terminal is visible
	}
	if lastPlaced != nil {
		selectTerminal(p, lastPlaced)
	}
	m.refocus(p)
	m.applyLayout(p)
	m.focus, m.panel = focusMain, nil
	m.persist()
	m.notice = restoreNotice(res) // after persist, which clears the notice on success
	m.noteLimit()
	return m, tea.Batch(cmds...), res
}

// restoreNotice summarises a restore: "" when everything came back.
func restoreNotice(res workspace.RestoreResult) string {
	details := restoreDetails(res)
	if details == "" {
		return ""
	}
	n := len(res.Opened)
	return fmt.Sprintf("restored %d terminal%s; %s", n, plural(n), details)
}

// restoreDetails lists what a restore could not do: "" when nothing.
func restoreDetails(res workspace.RestoreResult) string {
	var parts []string
	if len(res.Skipped) > 0 {
		parts = append(parts, fmt.Sprintf("%d unavailable: %s", len(res.Skipped), strings.Join(res.Skipped, ", ")))
	}
	if res.NotOpened > 0 {
		parts = append(parts, fmt.Sprintf("%d not opened: max_terminals (%d) reached", res.NotOpened, res.Limit.Max))
	}
	return strings.Join(parts, "; ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// arrange lays out p after open-all: the saved layout is rebuilt from the
// running terminals it still references, then every running terminal not in
// it (including ones opened before) is placed: a grid grows as a grid, a
// user-shaped layout has its largest pane split. Panes squeezed below the
// minimum are hidden. It returns the last terminal placed, or nil.
func (m *Model) arrange(p *workspace.Project, opened []*workspace.Terminal) *workspace.Terminal {
	var last *workspace.Terminal
	used := map[*workspace.Terminal]bool{}
	if restored := paneFromLayout(p.Layout, p.Terminals, used); restored != nil {
		m.layouts[p] = restored
		m.applyLayout(p)
		leaves := restored.leaves()
		last = leaves[len(leaves)-1]
	}
	for _, t := range p.Terminals { // running terminals the saved layout did not cover
		if !m.layouts[p].contains(t) && m.placeBalanced(p, t) {
			last = t
		}
	}
	m.enforceMinimums(p)
	m.applyLayout(p)
	if last != nil && !m.layouts[p].contains(last) {
		if leaves := m.layouts[p].leaves(); len(leaves) > 0 {
			last = leaves[len(leaves)-1]
		}
	}
	_ = opened
	return last
}

func (m Model) terminalByKey(p *workspace.Project, key string) *workspace.Terminal {
	if key == "" {
		return nil
	}
	for _, t := range p.Terminals {
		if t.Key() == key {
			return t
		}
	}
	return nil
}

// placeBalanced inserts nt into a layout the saved arrangement did not
// cover it in: an automatic layout grows as a grid; a user-shaped one gets
// nt by splitting the largest pane that can still be split (auto
// direction). It reports false when every pane is too small, in which case
// nt stays hidden and reachable through alt+j / alt+k.
func (m *Model) placeBalanced(p *workspace.Project, nt *workspace.Terminal) bool {
	root := m.layouts[p]
	if grown := m.grownGrid(root, nt); grown != nil {
		m.layouts[p] = grown
		m.applyLayout(p)
		return true
	}
	rs := m.paneRects(p)
	var best *workspace.Terminal
	var bestDir splitDir
	bestArea := -1
	for _, t := range root.leaves() { // leaves() order keeps ties deterministic
		r := rs[t]
		dir := autoDir(r)
		if !canSplit(r, dir) {
			dir = splitSide + splitStack - dir
			if !canSplit(r, dir) {
				continue
			}
		}
		if area := r.w * r.h; area > bestArea {
			best, bestDir, bestArea = t, dir, area
		}
	}
	if best == nil {
		return false
	}
	m.layouts[p] = root.insert(best, nt, bestDir)
	m.applyLayout(p)
	return true
}

// watchTerminal blocks until the terminal has new output or exits.
func watchTerminal(t *workspace.Terminal) tea.Cmd {
	s := t.Session
	return func() tea.Msg {
		select {
		case <-s.Done():
			return terminalExitedMsg{id: t.ID, err: s.Err()}
		case <-s.Output():
			return terminalOutputMsg{id: t.ID, s: s}
		}
	}
}

func (m Model) selectedProject() *workspace.Project {
	if m.selected < 0 || m.selected >= len(m.ws.Projects) {
		return nil
	}
	return m.ws.Projects[m.selected]
}

// activeTerminal returns the selected project and its active terminal.
func (m Model) activeTerminal() (*workspace.Project, *workspace.Terminal) {
	p := m.selectedProject()
	if p == nil {
		return nil, nil
	}
	return p, p.ActiveTerminal()
}

func (m Model) projectIndex(p *workspace.Project) int {
	for i, cur := range m.ws.Projects {
		if cur == p {
			return i
		}
	}
	return -1
}

func exitNotice(err error) string {
	if err == nil {
		return "exited"
	}
	return "exited: " + err.Error()
}

// terminalSize is the screen size of every terminal: the main pane's inner
// area.
func (m Model) terminalSize() (cols, rows int) {
	cols = m.width - sidebarWidth - paneGutter - mainAreaStyle.GetHorizontalFrameSize()
	rows = m.height - statusHeight - mainAreaStyle.GetVerticalFrameSize()
	return max(cols, 1), max(rows, 1)
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading..."
	}

	paneHeight := m.height - statusHeight
	if m.help {
		screen := framed(modalStyle, true, m.width, paneHeight).Render(
			viewHelp(m.width-modalStyle.GetHorizontalFrameSize(), paneHeight-modalStyle.GetVerticalFrameSize()))
		return lipgloss.JoinVertical(lipgloss.Left, screen, m.viewStatus())
	}
	if m.diff != nil {
		screen := framed(modalStyle, true, m.width, paneHeight).Render(
			m.diff.view(m.width-modalStyle.GetHorizontalFrameSize(), paneHeight-modalStyle.GetVerticalFrameSize()))
		return lipgloss.JoinVertical(lipgloss.Left, screen, m.viewStatus())
	}
	sidebar := framed(sidebarStyle, m.focus == focusSidebar, sidebarWidth, paneHeight).Render(m.viewSidebar())
	style, focused := mainAreaStyle, false
	if m.panel != nil || m.form != nil {
		style, focused = mainStyle, m.focus == focusMain
	}
	mainPane := framed(style, focused, m.width-sidebarWidth-paneGutter, paneHeight).Render(m.viewMain())

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, gap(1, paneHeight), mainPane)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.viewStatus())
}

func (m Model) viewSidebar() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Projects"))
	b.WriteString("\n\n")

	if len(m.ws.Projects) == 0 {
		b.WriteString(hintStyle.Render("No Git repositories\nfound in:\n" + m.root))
		return b.String()
	}

	inner := sidebarWidth - sidebarStyle.GetHorizontalFrameSize()
	for i, p := range m.ws.Projects {
		// ● running workspace, ○ workspace that Enter will restore, else blank.
		marker := "  "
		switch {
		case len(p.Terminals) > 0:
			marker = "● "
		case len(p.Saved) > 0:
			marker = "○ "
		}
		// Wrap rather than truncate: a project's name is how it is recognised,
		// and the sidebar is narrow enough that real names do not fit.
		wrapped := strings.Split(ansi.Wrap(p.Name, max(inner-2, 1), ""), "\n")
		for j, part := range wrapped {
			prefix := marker
			if j > 0 {
				prefix = "  " // continuation lines line up under the name
			}
			// Every variant paints a background of its own. A style that sets
			// only a foreground closes its span with a reset, which drops the
			// ground the sidebar is painted on, so the padding after a short
			// name would fall through to the user's own terminal colour.
			style := lipgloss.NewStyle().Background(ground)
			switch {
			case i == m.cursor && m.focus == focusSidebar:
				style = cursorStyle
			case i == m.selected:
				style = selectedStyle
			}
			b.WriteString(style.Width(inner).MaxWidth(inner).Render(prefix + part))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (m Model) viewMain() string {
	innerWidth := m.width - sidebarWidth - paneGutter - mainStyle.GetHorizontalFrameSize()
	if m.panel != nil {
		return m.panel.view(innerWidth)
	}
	if m.form != nil {
		return m.form.view(innerWidth)
	}
	p := m.selectedProject()
	if p == nil {
		return "\n" + hintStyle.Render(" Select a project and press enter to open a shell there.")
	}
	if t := p.ActiveTerminal(); t != nil && m.layouts[p] != nil {
		return m.viewPanes(p, t)
	}
	lines := []string{"", " " + titleStyle.Render(p.Name), " " + p.Path, ""}
	if n := len(p.Terminals); n > 0 { // running, but every pane was hidden
		lines = append(lines, " "+hintStyle.Render(fmt.Sprintf("%d terminal%s running, all hidden. Press m to show one.", n, plural(n))))
		return strings.Join(lines, "\n")
	}
	if p.Notice != "" {
		lines = append(lines, " "+hintStyle.Render(p.Notice))
	}
	if len(p.Saved) > 0 {
		lines = append(lines, " "+hintStyle.Render("Press enter to open this project's terminal, or m to pick one."))
	} else {
		lines = append(lines, " "+hintStyle.Render("Press enter or n to open a shell here."))
	}
	return strings.Join(lines, "\n")
}

// viewPanes renders the project's visible terminals in their layout.
// Each pane has a one-line header naming its terminal; the focused pane's
// header is highlighted and only its cursor is drawn.
func (m Model) viewPanes(p *workspace.Project, active *workspace.Terminal) string {
	area := m.mainArea()
	if m.zoom {
		return m.renderLeaf(p, active, area, m.focus == focusMain)
	}
	return m.renderPane(p, m.layouts[p], area, active)
}

func (m Model) renderPane(p *workspace.Project, n *pane, r rect, active *workspace.Terminal) string {
	if n == nil {
		return ""
	}
	if n.isLeaf() {
		return m.renderLeaf(p, n.term, r, n.term == active && m.focus == focusMain)
	}
	if n.dir == splitSide {
		wa := splitSize(r.w, n.ratio, minPaneCols)
		left := m.renderPane(p, n.a, rect{r.x, r.y, wa, r.h}, active)
		right := m.renderPane(p, n.b, rect{r.x + wa + 1, r.y, r.w - wa - 1, r.h}, active)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, gap(1, r.h), right)
	}
	ha := splitSize(r.h, n.ratio, minPaneRows)
	top := m.renderPane(p, n.a, rect{r.x, r.y, r.w, ha}, active)
	bottom := m.renderPane(p, n.b, rect{r.x, r.y + ha + 1, r.w, r.h - ha - 1}, active)
	return lipgloss.JoinVertical(lipgloss.Left, top, gap(r.w, 1), bottom)
}

// gap is the empty space between two cards, painted so the ground shows
// through rather than the host terminal's own background.
func gap(w, h int) string {
	return lipgloss.NewStyle().Background(ground).Width(w).Height(h).Render("")
}

// renderLeaf draws one terminal as its own card, filling exactly r.w x r.h
// cells including the card's border.
func (m Model) renderLeaf(p *workspace.Project, t *workspace.Terminal, r rect, focused bool) string {
	label, branch := m.paneHeaderParts(p, t)
	screen := t.Session.Render(focused)
	if off := m.scroll[t]; off > 0 {
		branch += fmt.Sprintf(" %s %d", scrollMark, off) // meta like the branch, not the label
		screen = t.Session.RenderScrolled(off)
	}
	innerW := max(r.w-cardStyle.GetHorizontalBorderSize(), 1)
	innerH := max(r.h-cardStyle.GetVerticalBorderSize(), 1)

	lines := []string{paneHeader(label, branch, innerW, focused, running(t))}
	for i, line := range strings.Split(screen, "\n") {
		if i >= innerH-paneHeaderHeight {
			break
		}
		lines = append(lines, ansi.Truncate(line, innerW, ""))
	}
	return framed(cardStyle, focused, r.w, r.h).Render(strings.Join(lines, "\n"))
}

// running reports whether t's process is still alive, without blocking: its
// Done() channel is only ever closed on exit, so a select with a default
// case tells the two apart in one step.
func running(t *workspace.Terminal) bool {
	select {
	case <-t.Session.Done():
		return false
	default:
		return true
	}
}

// statusDot marks a pane's header with its terminal's state at a glance.
const statusDot = "●"

// paneHeader is a pane's title row: a status dot, the terminal's label, and
// the branch it is on. It is no longer a filled bar — the card's border is
// the pane's edge now, so the header only has to be readable. The dot and
// the label are bright; the branch is dim — focus is carried by the dot's
// colour alone, so the label stays bright whether or not the pane has focus.
// The space after the dot is grounded like every other run: it follows the
// dot's own styled run, whose reset dropped the ground.
func paneHeader(label, branch string, width int, focused bool, running bool) string {
	dot := dotStyle(focused, running)
	line := " " + dot.Render(statusDot) + textStyle.Render(" ") + titleStyle.Render(label) + hintStyle.Render(branch)
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Background(ground).
		Render(ansi.Truncate(line, width, "…"))
}

// dotStyle picks the status dot's colour: accent while the pane has focus
// (focus is the strongest signal, and wins even over a since-exited process),
// green while its process still runs, dim once it has exited.
func dotStyle(focused, running bool) lipgloss.Style {
	switch {
	case focused:
		return dotFocused
	case running:
		return dotRunning
	default:
		return dotExited
	}
}

// paneTitle is the terminal's identity as shown everywhere: `Terminal 2 ·
// ⎇ feature/login`. The branch is the one checked out where the terminal
// runs; ⎇ marks a terminal with its own dedicated worktree. Whether the
// checkout is a worktree is otherwise not the user's concern, and custom
// names are not part of the identity.
func (m Model) paneTitle(p *workspace.Project, t *workspace.Terminal) string {
	return identity(terminalLabel(t), m.branches.get(t.Dir(p)), t.Worktree != nil)
}

// paneHeaderParts is paneTitle's label and branch suffix kept apart, so the
// header can render the label bright and the branch dim instead of joining
// them into one uniformly-styled string.
func (m Model) paneHeaderParts(p *workspace.Project, t *workspace.Terminal) (label, branch string) {
	return identityParts(terminalLabel(t), m.branches.get(t.Dir(p)), t.Worktree != nil)
}

// terminalLabel is a running terminal's display name wherever one is
// needed: its command when it has one (more use than a number for telling
// panes apart), else "Terminal N". Shared by the pane header, the Terminal
// Manager and the deletion prompt so the three never drift apart.
func terminalLabel(t *workspace.Terminal) string {
	if t.Command != "" {
		return stripControlChars(t.Command)
	}
	return fmt.Sprintf("Terminal %d", t.Num)
}

// stripControlChars removes control characters (C0, DEL and C1, including
// U+009B which xterm in UTF-8 mode still honours as CSI) from s. A stored
// command is free text that can contain raw ESC or other control bytes; left
// in a label, they can rewrite the surrounding terminal display (pane
// header, manager row, deletion prompt) instead of just being shown as
// text. The changes view renders repository bytes — code lines, paths,
// git's own output — straight into the frame and strips them here for the
// same reason. Everything else, including multi-byte UTF-8, is left unchanged.
func stripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// identity joins a terminal's label and its Git context: `label · ⎇ branch`,
// `label · branch`, or just label when there is no Git context.
func identity(label, branch string, worktree bool) string {
	l, b := identityParts(label, branch, worktree)
	return l + b
}

// identityParts is identity's label and Git-context suffix kept apart,
// for a caller that styles them differently — the pane header shows the
// label bright and the branch dim. The suffix carries its own leading
// " · " so a caller that has no branch can just concatenate the two.
func identityParts(label, branch string, worktree bool) (string, string) {
	if branch == "" {
		return label, ""
	}
	if worktree {
		return label, " · " + worktreeMark + branch
	}
	return label, " · " + branch
}

// worktreeMark is the one visible sign that a terminal has a dedicated
// worktree.
const worktreeMark = "⎇ "

// programOf is what runs in the terminal: the title its program set, else
// "shell".
func programOf(t *workspace.Terminal) string {
	if title := t.Session.Title(); title != "" {
		return title
	}
	return "shell"
}

func (m Model) viewStatus() string {
	var bindings []key.Binding
	var info, prompt string // prompt: a question awaiting y, shown like a notice
	switch {
	case m.help:
		bindings = []key.Binding{keys.HelpClose}
	case m.diff != nil:
		bindings = keys.diffHelp(m.diff.listFocus)
	case m.panel != nil:
		bindings = keys.managerHelp()
	case m.form != nil:
		bindings = keys.formHelp()
	case m.deleting != nil:
		bindings = []key.Binding{keys.Confirm, keys.Cancel}
		prompt = m.deletePrompt
	case m.focus == focusMain:
		bindings = keys.terminalHelp()
		if p, t := m.activeTerminal(); t != nil {
			info = p.Name + " · " + m.paneTitle(p, t)
			if m.zoom {
				info += " · zoom"
			}
		}
	default:
		bindings = keys.sidebarHelp()
	}
	hints := make([]string, 0, len(bindings))
	for _, b := range bindings {
		h := b.Help()
		hints = append(hints, textStyle.Render(h.Key+" ")+hintStyle.Render(h.Desc))
	}
	width := max(m.width-statusStyle.GetHorizontalPadding(), 1)
	if prompt != "" { // a question must never push its own answer keys off the line
		room := width - ansi.StringWidth(strings.Join(hints, "  •  ")) - 5
		prompt = ansi.Truncate(prompt, max(room, 12), "…")
	}
	parts := make([]string, 0, len(hints)+3)
	if m.notice != "" {
		parts = append(parts, errorStyle.Render(m.notice))
	}
	if prompt != "" {
		parts = append(parts, errorStyle.Render(prompt))
	}
	if info != "" {
		parts = append(parts, hintStyle.Render(info))
	}
	left := strings.Join(parts, textStyle.Render("  •  "))
	right := strings.Join(hints, hintStyle.Render(" │ "))
	space := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if space < 1 { // too narrow for both: the hints matter more than the label
		left = ansi.Truncate(left, max(width-ansi.StringWidth(right)-1, 0), "…")
		space = 1
	}
	line := left + textStyle.Render(strings.Repeat(" ", space)) + right
	// Backstop: if the hints alone are wider than the window, left was
	// already sacrificed down to nothing above but right is never shortened,
	// so the assembled line could still overflow width.
	line = ansi.Truncate(line, width, "…")
	return statusStyle.Width(m.width).Render(line)
}
