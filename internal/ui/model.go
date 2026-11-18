// Package ui contains the Bubble Tea model for the tm terminal UI.
package ui

import (
	"fmt"
	"strings"
	"time"
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
	copy     *copyMode    // open copy-mode selection, nil otherwise
	help     bool         // help screen shown over everything
	notice   string       // one-line message in the status bar (state file problems)
	quitting bool         // set once quit began; later exit messages must not rewrite state

	zoom bool // render only the focused terminal at full size; the layout tree is untouched

	scroll map[*workspace.Terminal]int // lines scrolled back into history per pane; absent or 0 = live

	panes map[*workspace.Terminal]paneRender // last rendered card per terminal; see renderLeaf

	// unseen marks terminals that printed while off screen — hidden with
	// alt+x, zoomed out of view, or belonging to a project that is not the
	// one being shown. A pane you can see needs no marker: its output is
	// already in front of you, which is why renderLeaf clears the mark as it
	// draws.
	unseen map[*workspace.Terminal]bool

	deleting     *workspace.Terminal          // alt+delete pressed: waiting for y to delete this terminal for good
	deletePrompt string                       // the question shown while deleting is set
	layouts      map[*workspace.Project]*pane // visible panes per project; nil root = none (running terminals not in the tree are hidden)
	allVisible   map[*workspace.Project]*pane // last tree that showed every running terminal, so a hidden pane can return to its exact place
	branches     *branchLookup                // checked-out branch per directory, for pane headers and the manager
	pendingSplit splitDir                     // where the form's new terminal goes; 0 = auto
	srv          *serverLink                  // the tm server holding the terminals; nil when they run here (see attach.go)

	width, height int
}

// New builds a Model for the given workspace root and its projects, and
// restores saved terminal definitions from the workspace state file.
func New(root string, projects []project.Project) Model {
	return NewWith(root, projects, Options{})
}

// NewWith is New with options: attached to a tm server when o.Client is
// set (see attach.go), and with o.Forget's terminals dropped from the
// restored state either way.
func NewWith(root string, projects []project.Project, o Options) Model {
	spawn := spawnShell
	if o.Client != nil {
		spawn = noPlainSpawn
	}
	m := Model{root: root, ws: workspace.New(root, projects, spawn), selected: -1, layouts: map[*workspace.Project]*pane{}, allVisible: map[*workspace.Project]*pane{}, scroll: map[*workspace.Terminal]int{}, panes: map[*workspace.Terminal]paneRender{}, unseen: map[*workspace.Terminal]bool{}, branches: newBranchLookup()}
	var err error
	if o.Client != nil {
		err = m.attach(o)
	} else if err = m.ws.Restore(); err == nil && len(o.Forget) > 0 {
		m.ws.Forget(o.Forget)
		m.persist()
	}
	if err != nil {
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
	if m.released() {
		return // the server has a new owner, and it writes the state now
	}
	for _, p := range m.ws.Projects {
		if t := p.ActiveTerminal(); t != nil {
			m.ws.RecordLayout(p, layoutFromPane(m.layouts[p]), t.Key())
		}
	}
	m.sendBlob()
	if err := m.ws.Save(); err != nil {
		m.notice = "could not save state: " + err.Error()
		return
	}
	m.notice = ""
}

func (m Model) Init() tea.Cmd { return m.serverInit() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.arrangeAttached() // the first size lays out a reattached window
		for _, p := range m.ws.Projects {
			m.applyLayout(p)
		}
		if m.diff != nil {
			m.diff.scroll(0, m.diffRows()) // a taller window must not show blank rows under the file
		}

	case tea.MouseMsg:
		return m, m.handleMouse(msg)

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
		if m.copy != nil {
			return m.updateCopy(msg)
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

	case diffListMsg:
		// A result for a request the view has moved past, or for a view that
		// has since closed, is not what is on screen: dropping it is what
		// makes a held-down key safe.
		if m.diff != nil && msg.seq == m.diff.listSeq.Load() {
			return m, m.diff.applyList(msg)
		}

	case diffFileMsg:
		if m.diff != nil && msg.seq == m.diff.fileSeq.Load() {
			m.diff.applyFile(msg)
			m.diff.scroll(0, m.diffRows()) // a re-read file may have got shorter
		}

	case terminalOutputMsg:
		// Re-arm only if this terminal is still registered with that session.
		if p, t := m.ws.Find(msg.id); t != nil && t.Session == msg.s {
			if printed(t.Session) && !m.onScreen(p, t) {
				m.unseen[t] = true
			}
			return m, watchTerminal(t)
		}

	case serverGoneMsg:
		return m.serverGone(msg)

	case terminalExitedMsg:
		if m.quitting || m.lostWith(msg.err) {
			return m, nil // sessions closed by quit, or a lost server: the saved state already has them
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
		m.panel = newManager(m.ws, p, m.layouts[p], m.branches.get, m.printedOffScreen) // p is the focused terminal's project; focus is kept for esc
	case key.Matches(msg, keys.DiffTerm):
		return m, m.openDiff(p, t)
	case key.Matches(msg, keys.Copy):
		m.copy = newCopyMode(t, m.scroll[t])
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
	delete(m.panes, t) // an exiting terminal must not leave its card behind
	if m.copy != nil && m.copy.term == t {
		m.copy = nil // the pane the selection was in has gone
	}
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
		rebuilt := gridFor(remaining, chooseCols(remaining, m.mainArea())).tree()
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
// Up and down move the file list, always: the columns are scrolled with the
// wheel (see diffWheel), which leaves the keys one job each.
func (m Model) updateDiff(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	height := m.diffRows()
	d := m.diff
	switch {
	case key.Matches(msg, keys.DiffClose):
		m.diff = nil
	case key.Matches(msg, keys.DiffRefresh):
		return m, d.refresh()
	case key.Matches(msg, keys.DiffNextChange):
		d.jumpChange(1, height)
	case key.Matches(msg, keys.DiffPrevChange):
		d.jumpChange(-1, height)
	case key.Matches(msg, keys.DiffLeft):
		d.scrollX(-diffHScrollStep)
	case key.Matches(msg, keys.DiffRight):
		d.scrollX(diffHScrollStep)
	case key.Matches(msg, keys.DiffDown):
		return m, d.move(1)
	case key.Matches(msg, keys.DiffUp):
		return m, d.move(-1)
	}
	return m, nil
}

// updateCopy handles a key while copy mode is open. Like the help screen and
// the changes view it consumes every key, so nothing reaches the program
// underneath — which is what lets plain j and k mean something here while they
// are PTY input everywhere else in terminal focus.
func (m Model) updateCopy(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.copy
	switch {
	case key.Matches(msg, keys.CopyClose):
		m.copy = nil
	case key.Matches(msg, keys.CopyYank):
		if what := c.copy(); what != "" {
			m.notice = what
		}
		m.copy = nil
	case key.Matches(msg, keys.CopySelect):
		c.mark()
	case key.Matches(msg, keys.Down):
		c.move(1)
	case key.Matches(msg, keys.Up):
		c.move(-1)
	case key.Matches(msg, keys.Top):
		c.move(-len(c.lines))
	case key.Matches(msg, keys.Bottom):
		c.move(len(c.lines))
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
func (m *Model) openDiff(p *workspace.Project, t *workspace.Terminal) tea.Cmd {
	dir, title := p.Path, p.Name
	if t != nil {
		dir = t.Dir(p)
	}
	if branch := m.branches.get(dir); branch != "" {
		title += "  " + worktreeMark + branch
	}
	var cmd tea.Cmd
	m.diff, cmd = newDiffView(dir, title)
	return cmd
}

// updateSidebar handles navigation and program-level keys.
func (m Model) updateSidebar(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.ws.Projects) - 1
	switch {
	case key.Matches(msg, keys.Quit) && m.srv != nil:
		return m.leave()
	case key.Matches(msg, keys.StopAll):
		if m.srv != nil {
			return m.stopAll()
		}
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
			m.panel = newManager(m.ws, p, m.layouts[p], m.branches.get, m.printedOffScreen)
		}
	case key.Matches(msg, keys.Diff):
		if last >= 0 {
			return m, m.openDiff(m.ws.Projects[m.cursor], nil)
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

// outputWindow is how long watchTerminal keeps collecting after the first
// signal before it wakes the UI. Bubble Tea rebuilds the whole window on
// every message it delivers, while the renderer ships at most sixty frames a
// second, so a message per PTY read is work thrown away: a program printing
// steadily woke the UI about 350 times a second, and roughly five in six of
// those frames were built and discarded. One window is one frame's worth of
// output, so a burst of any size costs one rebuild.
//
// The wake is deliberately on the trailing edge. The cost is that an isolated
// keystroke's echo appears one window later, which is the renderer's own
// frame time and below what a typist can see; the gain is that the steady
// case cannot outrun the screen.
const outputWindow = 16 * time.Millisecond

// watchTerminal blocks until the terminal has new output or exits, then
// collects further output for one window so a burst wakes the UI once. A
// session whose output is coalesced upstream (Coalesced, a server's
// RemoteSession) wakes the UI at once instead.
func watchTerminal(t *workspace.Terminal) tea.Cmd {
	s := t.Session
	return func() tea.Msg {
		select {
		case <-s.Done():
			return terminalExitedMsg{id: t.ID, err: s.Err()}
		case <-s.Output():
		}
		if c, ok := s.(interface{ Coalesced() bool }); ok && c.Coalesced() {
			// A tm server's session: the server already waited out its
			// frameWindow before pushing this frame, and a second window
			// here would only delay the echo of every keystroke.
			return terminalOutputMsg{id: t.ID, s: s}
		}
		// The screen is read once, when the message lands, so ten signals
		// and one describe the same work. Exit still wins the moment it
		// happens: the pane goes away, and waiting out the window first
		// would only delay saying so.
		window := time.NewTimer(outputWindow)
		defer window.Stop()
		for {
			select {
			case <-s.Done():
				return terminalExitedMsg{id: t.ID, err: s.Err()}
			case <-s.Output():
			case <-window.C:
				return terminalOutputMsg{id: t.ID, s: s}
			}
		}
	}
}

// printed reports whether the output a terminalOutputMsg stands for was the
// program printing. A local session signals Output only for that. A server's
// session also signals it for frames sent to redraw after an attach, a
// resize or a focus change, and says which through TakePrinted; it is asked
// on every message, on screen or not, so a flag left from output seen on
// screen cannot mark a later redraw.
func printed(s workspace.Session) bool {
	if p, ok := s.(interface{ TakePrinted() bool }); ok {
		return p.TakePrinted()
	}
	return true
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
			viewHelpFor(helpSectionsFor(m.srv != nil), m.width-modalStyle.GetHorizontalFrameSize(), paneHeight-modalStyle.GetVerticalFrameSize()))
		return lipgloss.JoinVertical(lipgloss.Left, screen, m.viewStatus())
	}
	if m.diff != nil {
		screen := framed(modalStyle, true, m.width, paneHeight).Render(
			m.diff.view(m.width-modalStyle.GetHorizontalFrameSize(), paneHeight-modalStyle.GetVerticalFrameSize()))
		return lipgloss.JoinVertical(lipgloss.Left, screen, m.viewStatus())
	}
	sidebar := framed(sidebarStyle, m.focus == focusSidebar, sidebarWidth, paneHeight).Render(
		m.viewSidebar(paneHeight - sidebarStyle.GetVerticalFrameSize()))
	style, focused := mainAreaStyle, false
	if m.panel != nil || m.form != nil {
		style, focused = mainStyle, m.focus == focusMain
	}
	mainPane := framed(style, focused, m.width-sidebarWidth-paneGutter, paneHeight).Render(m.viewMain())

	body := lipgloss.JoinHorizontal(lipgloss.Top, sandwich(sidebar, gap(paneGutter, paneHeight), mainPane)...)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.viewStatus())
}

// viewSidebar lists the projects in at most height rows. A workspace with more
// repositories than the terminal has rows would otherwise draw past the bottom
// of the window — Lip Gloss pads a short block but never cuts a long one — and
// take the status bar and the selected project off the screen with it.
func (m Model) viewSidebar(height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Projects"))
	b.WriteString("\n\n")
	height -= 2 // the title and the blank line under it

	if len(m.ws.Projects) == 0 {
		b.WriteString(hintStyle.Render("No Git repositories\nfound in:\n" + m.root))
		return b.String()
	}

	inner := sidebarWidth - sidebarStyle.GetHorizontalFrameSize()
	entries := make([][]string, len(m.ws.Projects))
	for i, p := range m.ws.Projects {
		// ● running workspace, ○ workspace that Enter will restore, else blank.
		marker := "  "
		switch {
		case len(p.Terminals) > 0:
			marker = "● "
		case len(p.Saved) > 0:
			marker = "○ "
		}
		stirred := m.stirring(p)
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
			// The marker is drawn separately only when it carries the amber,
			// and then on the row's own background: a nested span that sets
			// just a foreground ends with a reset, which would drop the
			// sidebar's ground for the rest of the line.
			if stirred && j == 0 && marker != "  " {
				dot := lipgloss.NewStyle().Foreground(stir).Background(style.GetBackground()).Render(strings.TrimSpace(marker))
				entries[i] = append(entries[i], style.Width(inner).MaxWidth(inner).Render(dot+style.Render(" "+part)))
			} else {
				entries[i] = append(entries[i], style.Width(inner).MaxWidth(inner).Render(prefix+part))
			}
		}
	}
	b.WriteString(strings.Join(visibleEntries(entries, m.cursor, height), "\n"))
	return b.String()
}

func (m Model) viewMain() string {
	innerWidth := m.width - sidebarWidth - paneGutter - mainStyle.GetHorizontalFrameSize()
	if m.panel != nil {
		return m.panel.view(innerWidth, m.height-statusHeight-mainStyle.GetVerticalFrameSize())
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
		wa := splitSize(r.w, paneGap, n.ratio, minPaneCols)
		left := m.renderPane(p, n.a, rect{r.x, r.y, wa, r.h}, active)
		right := m.renderPane(p, n.b, rect{r.x + wa + paneGap, r.y, r.w - wa - paneGap, r.h}, active)
		return lipgloss.JoinHorizontal(lipgloss.Top, sandwich(left, gap(paneGap, r.h), right)...)
	}
	ha := splitSize(r.h, paneRowGap, n.ratio, minPaneRows)
	top := m.renderPane(p, n.a, rect{r.x, r.y, r.w, ha}, active)
	bottom := m.renderPane(p, n.b, rect{r.x, r.y + ha + paneRowGap, r.w, r.h - ha - paneRowGap}, active)
	return lipgloss.JoinVertical(lipgloss.Left, sandwich(top, gap(r.w, paneRowGap), bottom)...)
}

// gap is the empty space between two cards, painted so the ground shows
// through rather than the host terminal's own background. A gap of no cells
// is the empty string, never a blank block: Lip Gloss joins a block of no
// width as a column of its own and one of no height as a row, either of
// which would put back the space the gap was set to zero to remove.
func gap(w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Background(ground).Width(w).Height(h).Render("")
}

// sandwich is a and b with mid between them, or just a and b when mid is
// empty, ready to hand to one of Lip Gloss's Join functions.
func sandwich(a, mid, b string) []string {
	if mid == "" {
		return []string{a, b}
	}
	return []string{a, mid, b}
}

// renderLeaf draws one terminal as its own card, filling exactly r.w x r.h
// cells including the card's border.
func (m Model) renderLeaf(p *workspace.Project, t *workspace.Terminal, r rect, focused bool) string {
	label, branch := m.paneHeaderParts(p, t)
	delete(m.unseen, t) // drawing a pane is what "seen" means
	if c := m.copy; c != nil && c.term == t {
		// Copy mode is drawn straight, never cached: its cursor and selection
		// move on keys paneKey knows nothing about, and it shows frozen rows
		// rather than the live screen, so there is nothing for the cache to
		// save. The card is built the same way, so the pane keeps its shape.
		innerW := max(r.w-cardStyle.GetHorizontalBorderSize(), 1)
		innerH := max(r.h-cardStyle.GetVerticalBorderSize(), 1)
		rows := append([]string{paneHeader(label, branch+"  "+copyMark, innerW, focused, running(t))},
			c.view(innerW, innerH-paneHeaderHeight)...)
		return framed(cardStyle, focused, r.w, r.h).Render(strings.Join(rows, "\n"))
	}
	// The version is read before the screen, never after: a write landing
	// between the two leaves a card built from newer content filed under an
	// older version, which costs one extra repaint next frame. Read the other
	// way round it would file older content under the newer version and the
	// card would stay wrong until something else changed.
	key := paneKey{
		version: t.Session.Version(),
		w:       r.w,
		h:       r.h,
		focused: focused,
		scroll:  m.scroll[t],
		label:   label,
		branch:  branch,
		running: running(t),
	}
	if c, ok := m.panes[t]; ok && c.key == key {
		return c.card
	}
	screen := t.Session.Render(focused)
	if off := key.scroll; off > 0 {
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
	card := framed(cardStyle, focused, r.w, r.h).Render(strings.Join(lines, "\n"))
	if m.panes != nil { // a Model built without New caches nothing rather than panicking
		m.panes[t] = paneRender{key: key, card: card}
	}
	return card
}

// paneRender is one terminal's card and everything it was built from. Drawing
// a card costs about a third of a millisecond, and Bubble Tea rebuilds the
// whole window whenever any terminal speaks, so without this a single busy
// pane repainted every other pane on screen.
type paneRender struct {
	key  paneKey
	card string
}

// paneKey is every input renderLeaf reads. Two cards built from equal keys are
// the same string, so a key that misses an input would show a stale card:
// anything new that renderLeaf consults belongs here.
type paneKey struct {
	version       uint64 // the session's screen
	w, h          int
	focused       bool
	scroll        int
	label, branch string
	running       bool
}

// onScreen reports whether t has a pane in the arrangement being shown. Only
// then is its output already in front of the user; anything else — a hidden
// terminal, a pane zoomed out of view, another project's — prints where nobody
// is looking, and is worth saying so.
func (m Model) onScreen(p *workspace.Project, t *workspace.Terminal) bool {
	if p != m.selectedProject() {
		return false
	}
	if root := m.layouts[p]; root == nil || !root.contains(t) {
		return false
	}
	return !m.zoom || t == p.ActiveTerminal()
}

// printedOffScreen is what the Terminal Manager asks about each hidden row.
func (m Model) printedOffScreen(t *workspace.Terminal) bool { return m.unseen[t] }

// stirring reports whether any of p's terminals has printed out of sight.
func (m Model) stirring(p *workspace.Project) bool {
	for _, t := range p.Terminals {
		if m.unseen[t] {
			return true
		}
	}
	return false
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
// the branch it is on, drawn as a band across the top of the card on its own
// slightly lighter ground so it reads as a title rather than as the first
// row of output. The dot and the label are bright; the branch is dim — focus
// is carried by the dot's colour alone, so the label stays bright whether or
// not the pane has focus. Every run carries the band, the spaces included:
// each styled run's closing reset drops the background, so a space left
// unstyled after the dot would show as a notch of card ground in the strip.
// The outer style paints the padding out to the card's width.
func paneHeader(label, branch string, width int, focused bool, running bool) string {
	dot := dotStyle(focused, running)
	line := headerStyle.Render(" ") + dot.Render(statusDot) + headerStyle.Render(" ") +
		headerLabelStyle.Render(label) + headerBranchStyle.Render(branch)
	return headerStyle.Width(width).MaxWidth(width).
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
		bindings = keys.diffHelp()
	case m.copy != nil:
		bindings = keys.copyHelp()
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
	case m.srv != nil:
		bindings = keys.serverSidebarHelp()
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
