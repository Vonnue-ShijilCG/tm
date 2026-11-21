// Package workspace holds the runtime tree of the application:
//
//	Workspace
//	├── Project A
//	│   ├── Terminal A1
//	│   └── Terminal A2
//	└── Project B
//	    └── Terminal B1
//
// It only depends on the discovered project list and on the Session
// interface, so terminal backends and the UI can change independently.
package workspace

import (
	"fmt"
	"os"
	"sort"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/config"
	"github.com/Vonnue-ShijilCG/tm/internal/git"
	"github.com/Vonnue-ShijilCG/tm/internal/project"
)

// Session is what the workspace and UI need from a running terminal.
// *terminal.Session satisfies it; tests use fakes.
type Session interface {
	SendKey(uv.KeyPressEvent)
	SendText(string)
	Resize(cols, rows int)
	Render(showCursor bool) string
	RenderScrolled(offset int) string // the screen scrolled back offset lines into history
	Version() uint64                  // changes whenever Render might return something different
	Lines(offset int) []string        // the rows Render draws, as plain text, for a mouse selection
	ScrollbackLen() int               // lines of history available to RenderScrolled
	IsAltScreen() bool                // the program runs full-screen and keeps no history
	MouseTracking() bool              // the program asked for mouse events and should get the wheel
	SendWheel(up bool, x, y int)      // one wheel notch at screen-relative cell (x, y), encoded as the program expects
	Title() string
	Output() <-chan struct{}
	Done() <-chan struct{}
	Err() error
	Close() error
}

// Spawner starts a new session in dir with the given screen size.
type Spawner func(dir string, cols, rows int) (Session, error)

// Terminal is one independent process attached to a project. It runs in
// the project checkout unless a Worktree was created for it.
type Terminal struct {
	ID       int           // unique across the workspace; used to route messages
	Num      int           // 1-based, stable for the life of the project; shown in the pane header
	Name     string        // optional label chosen by the user
	Command  string        // run in the shell when this terminal starts; "" for a plain shell
	Worktree *git.Worktree // nil when the terminal runs in the project checkout
	Session  Session
}

// Dir is the terminal's working directory.
func (t *Terminal) Dir(p *Project) string {
	if t.Worktree != nil {
		return t.Worktree.Path
	}
	return p.Path
}

// Options describes how to create a terminal. The zero value is a plain
// shell in the project directory.
type Options struct {
	Name     string // kept for saved-state compatibility; no longer shown or set by the form
	Command  string // run in the terminal's shell when it starts; empty for a plain shell
	Branch   string // branch to check out when Worktree is set
	Base     string // what a new Branch starts from; ignored when Branch exists
	Worktree bool   // create (or check out) Branch in its own worktree
}

// Project is a discovered repository together with its terminals.
type Project struct {
	project.Project
	Terminals []*Terminal
	Saved     []SavedTerminal // remembered definitions that are not running
	Layout    *Layout         // last known arrangement (live, or restored from the state file)
	Focus     string          // key of the focused terminal in Layout, "" if unknown
	Notice    string          // shown when no terminal is running, e.g. the last exit status

	active  int // index into Terminals, -1 when empty
	nextNum int
}

// ActiveTerminal returns the focused terminal of the project, or nil.
func (p *Project) ActiveTerminal() *Terminal {
	if p.active < 0 || p.active >= len(p.Terminals) {
		return nil
	}
	return p.Terminals[p.active]
}

// ActiveIndex returns the index of the active terminal, or -1.
func (p *Project) ActiveIndex() int {
	if p.ActiveTerminal() == nil {
		return -1
	}
	return p.active
}

// Select makes the terminal at index i active. It reports whether i was valid.
func (p *Project) Select(i int) bool {
	if i < 0 || i >= len(p.Terminals) {
		return false
	}
	p.active = i
	return true
}

// Limit is the per-project cap on running terminals. It is read through a
// LimitSource every time a terminal is about to be created, so a changed
// configuration applies to the next attempt without restarting.
type Limit struct {
	Max    int    // running terminals allowed per project
	Source string // where the setting lives, for messages
	Notice string // problem found while reading the setting, "" when fine
}

// LimitSource returns the current limit.
type LimitSource func() Limit

// ConfigLimit is the default LimitSource: max_terminals from the user's
// configuration file, falling back to the default when it is missing or
// invalid (the problem is carried in Notice).
func ConfigLimit() Limit {
	path := config.Path()
	c, err := config.Load(path)
	l := Limit{Max: c.MaxTerminals, Source: config.Display(path)}
	if err != nil {
		l.Notice = err.Error()
	}
	return l
}

// LimitError is returned when a project already runs as many terminals as
// the limit allows. Nothing is created, closed or forgotten.
type LimitError struct {
	Limit
	Running int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("maximum terminals reached (%d): change max_terminals in %s", e.Max, e.Source)
}

// Workspace is the root of the tree. Root is the directory the projects
// were discovered in; worktrees live under it (see WorktreesDir).
type Workspace struct {
	Root     string
	Projects []*Project
	Limits   LimitSource // per-project terminal limit; ConfigLimit unless replaced

	// Adopt, when set, is asked for an already-running session before a
	// saved definition with a Ref is started; a non-nil answer is used
	// instead of spawning, and its command is not typed again (it is
	// already running there). Nil means spawn as usual.
	Adopt func(ref string) Session

	// SpawnFor, when set, starts every new session instead of the Spawner,
	// and is told which project and definition the session is for, which a
	// tm server records beside the session. Nil means use the Spawner.
	SpawnFor func(p *Project, def SavedTerminal, cols, rows int) (Session, error)

	spawn       Spawner
	nextID      int
	limitNotice string
}

// New wraps discovered projects; each starts with no terminals.
func New(root string, projects []project.Project, spawn Spawner) *Workspace {
	ws := &Workspace{Root: root, spawn: spawn, Limits: ConfigLimit}
	for _, p := range projects {
		ws.Projects = append(ws.Projects, &Project{Project: p, active: -1})
	}
	return ws
}

// Rediscover merges a fresh project.Discover of the root into the workspace,
// for repositories cloned or removed while tm runs. A repository not seen
// before is added with no terminals, in name order like Discover's. One whose
// directory is gone is dropped only when nothing runs or is saved in it:
// forgetting a terminal is not something a rescan may do. It returns the
// names added, removed, and kept although their directory is gone.
func (ws *Workspace) Rediscover(found []project.Project) (added, removed, kept []string) {
	seen := map[string]bool{}
	for _, f := range found {
		seen[f.Path] = true
	}
	known := map[string]bool{}
	next := ws.Projects[:0:0]
	for _, p := range ws.Projects {
		known[p.Path] = true
		if !seen[p.Path] {
			if len(p.Terminals) == 0 && len(p.Saved) == 0 {
				removed = append(removed, p.Name)
				continue
			}
			kept = append(kept, p.Name)
		}
		next = append(next, p)
	}
	for _, f := range found {
		if !known[f.Path] {
			next = append(next, &Project{Project: f, active: -1})
			added = append(added, f.Name)
		}
	}
	sort.SliceStable(next, func(i, j int) bool { return next[i].Name < next[j].Name })
	ws.Projects = next
	return added, removed, kept
}

// NewTerminal spawns a plain shell in p's directory. See CreateTerminal.
func (ws *Workspace) NewTerminal(p *Project, cols, rows int) (*Terminal, error) {
	return ws.CreateTerminal(p, Options{}, cols, rows)
}

// CreateTerminal spawns an independent session for p, appends it to the
// project and makes it active. With opts.Worktree the shell starts inside a
// worktree for opts.Branch; the worktree is kept when the terminal closes.
func (ws *Workspace) CreateTerminal(p *Project, opts Options, cols, rows int) (*Terminal, error) {
	if err := ws.CanCreateTerminal(p); err != nil {
		return nil, err // before touching git: a refused terminal leaves no worktree behind
	}
	var wt *git.Worktree
	if opts.Worktree {
		var err error
		if wt, err = ws.addWorktree(p, opts); err != nil {
			return nil, err
		}
	}
	return ws.startTerminal(p, opts.Name, opts.Command, "", wt, cols, rows) // a new terminal never adopts
}

// CanCreateTerminal is the single rule every creation path goes through:
// p may start another terminal while it runs fewer than the configured
// maximum. Saved definitions do not count; only running terminals do. The
// error, a *LimitError, says how to change the limit.
func (ws *Workspace) CanCreateTerminal(p *Project) error {
	l := ws.limit()
	if len(p.Terminals) >= l.Max {
		return &LimitError{Limit: l, Running: len(p.Terminals)}
	}
	return nil
}

// limit reads the current limit and remembers any problem with it.
func (ws *Workspace) limit() Limit {
	var l Limit
	if ws.Limits != nil {
		l = ws.Limits()
	}
	if l.Max < 1 {
		l.Max = config.DefaultMaxTerminals
	}
	if l.Source == "" {
		l.Source = "the tm config"
	}
	ws.limitNotice = l.Notice
	return l
}

// LimitNotice reports the problem found the last time the limit was read
// (an invalid configuration value, for instance), or "".
func (ws *Workspace) LimitNotice() string { return ws.limitNotice }

// CurrentLimit reads the limit now, e.g. to report a configuration problem
// at startup.
func (ws *Workspace) CurrentLimit() Limit { return ws.limit() }

// startTerminal spawns the session and registers the terminal. It enforces
// the limit as the final guard for every path, including reopening saved
// definitions. ref is the server session the definition last ran in; when
// Adopt hands that session back it is used as it is, without a spawn and
// without typing the command into it again - and without the limit, which
// is about starting processes: one already running is not a new terminal,
// and refusing it would leave it with no pane to reach it through.
func (ws *Workspace) startTerminal(p *Project, name, command, ref string, wt *git.Worktree, cols, rows int) (*Terminal, error) {
	t := &Terminal{Name: name, Command: command, Worktree: wt}
	var s Session
	if ref != "" && ws.Adopt != nil {
		s = ws.Adopt(ref)
	}
	if s != nil {
		ws.register(p, t, s)
		return t, nil
	}
	if err := ws.CanCreateTerminal(p); err != nil {
		return nil, err
	}
	var err error
	if ws.SpawnFor != nil {
		s, err = ws.SpawnFor(p, t.saved(), cols, rows)
	} else {
		s, err = ws.spawn(t.Dir(p), cols, rows)
	}
	if err != nil {
		return nil, err
	}
	ws.register(p, t, s)
	if command != "" { // typed into the shell, so the pane is a usable prompt when it ends
		s.SendText(command + "\n")
	}
	return t, nil
}

// AttachRunning registers s, a session that is already running, as a
// terminal of p described by def, and returns it. Nothing is checked: not
// the limit, and not whether def's worktree still exists, because the
// process is there either way and must stay reachable. A saved definition
// equal to def, Ref included, is no longer saved.
func (ws *Workspace) AttachRunning(p *Project, def SavedTerminal, s Session) *Terminal {
	t := &Terminal{Name: def.Name, Command: def.Command}
	if def.Worktree != "" {
		t.Worktree = &git.Worktree{Path: def.Worktree, Branch: def.Branch}
	}
	for i, cur := range p.Saved {
		if cur == def {
			p.Saved = append(p.Saved[:i], p.Saved[i+1:]...)
			break
		}
	}
	ws.register(p, t, s)
	return t
}

// register gives t its identifiers and session and makes it p's active
// terminal.
func (ws *Workspace) register(p *Project, t *Terminal, s Session) {
	ws.nextID++
	p.nextNum++
	t.ID, t.Num, t.Session = ws.nextID, p.nextNum, s
	p.Terminals = append(p.Terminals, t)
	p.active = len(p.Terminals) - 1
	p.Notice = ""
}

// Find returns the project and terminal with the given ID, or nils.
func (ws *Workspace) Find(id int) (*Project, *Terminal) {
	for _, p := range ws.Projects {
		for _, t := range p.Terminals {
			if t.ID == id {
				return p, t
			}
		}
	}
	return nil, nil
}

// Remove detaches the terminal with the given ID from its project once its
// process has ended, keeping the project's active terminal sensible. It
// does not close the session. The terminal's definition is remembered so
// the workspace can recreate it later — a process ending never forgets a
// terminal. Use DeleteTerminal to forget one for good.
func (ws *Workspace) Remove(id int) (*Project, *Terminal) {
	p, t := ws.detach(id)
	if t != nil {
		p.remember(t.saved())
	}
	return p, t
}

// DeleteTerminal is the one way a terminal ceases to exist. Running (t set)
// or saved (t nil), its definition goes, and the dedicated worktree it
// owned goes with it: removed through git, or pruned when its directory is
// already gone. A terminal in the project checkout only loses its
// definition; the checkout is never touched. The worktree is kept, with the
// reason in note, when another terminal still uses it or git cannot remove
// it. The session is not closed here; callers do that first. Callers are
// expected to have confirmed with the user, so a dirty worktree is removed.
func (ws *Workspace) DeleteTerminal(p *Project, def SavedTerminal, t *Terminal) (note string) {
	if t != nil {
		ws.detach(t.ID)
	} else {
		p.forget(def)
	}
	if def.Worktree == "" {
		return ""
	}
	if other := p.terminalUsing(def.Worktree); other != nil {
		return fmt.Sprintf("worktree kept: terminal %d uses it", other.Num)
	}
	for _, s := range p.Saved {
		if s.Worktree == def.Worktree {
			return "worktree kept: another saved terminal uses it"
		}
	}
	if ws.ownedName(p, def.Worktree) == "" {
		// A worktree git knows about (or once did) that tm did not create -
		// e.g. one another tool made. tm never removes those; see
		// RemoveWorktree's ownedName guard. Say so instead of advising the
		// user to delete a directory that may still be in active use.
		return "worktree kept: not created by tm, so tm will not remove it"
	}
	if _, listed := ws.WorktreeStatus(p, def.Worktree); !listed { // git has forgotten it already
		if _, err := os.Stat(def.Worktree); err != nil {
			return "" // and the directory is gone too: nothing left to clean up
		}
		return "worktree kept: git no longer lists it; remove the directory by hand"
	}
	if err := ws.RemoveWorktree(p, WorktreeInfo{Path: def.Worktree, Branch: def.Branch}, true); err != nil {
		return "worktree kept: " + err.Error()
	}
	return ""
}

// WorktreeStatus reports what git knows about the worktree at path: its
// branch, whether it is dirty or its directory missing, and whether tm owns
// it. ok is false only when git has no record of the checkout at all -
// never merely because tm did not create it. A worktree another tool made
// (or one under .terminal-worktrees git has since forgotten) still needs an
// accurate report, not one that says git has never heard of it; see Rows,
// which consults projectCheckouts for the identical reason.
func (ws *Workspace) WorktreeStatus(p *Project, path string) (info WorktreeInfo, ok bool) {
	if worktrees, err := ws.Worktrees(p); err == nil { // tm-owned: carries Name too
		for _, wt := range worktrees {
			if wt.Path == path {
				return wt, true
			}
		}
	}
	wt, known := ws.projectCheckouts(p)[path]
	if !known {
		return WorktreeInfo{}, false
	}
	info = WorktreeInfo{Path: path, Branch: wt.Branch, Foreign: true, Terminal: p.terminalUsing(path)}
	if _, err := os.Stat(path); err != nil {
		info.Missing = true
	} else if dirty, err := git.IsDirty(path); err == nil {
		info.Dirty = dirty
	}
	return info, true
}

// Definition is the terminal's persistent form: name, branch and worktree
// path. It is what survives when the process does not.
func (t *Terminal) Definition() SavedTerminal { return t.saved() }

// saved is the terminal's persistent definition.
func (t *Terminal) saved() SavedTerminal {
	s := SavedTerminal{Name: t.Name, Command: t.Command}
	if t.Worktree != nil {
		s.Branch, s.Worktree = t.Worktree.Branch, t.Worktree.Path
	}
	return s
}

func (ws *Workspace) detach(id int) (*Project, *Terminal) {
	p, t := ws.Find(id)
	if t == nil {
		return nil, nil
	}
	idx := 0
	for i, cur := range p.Terminals {
		if cur == t {
			idx = i
			break
		}
	}
	p.Terminals = append(p.Terminals[:idx], p.Terminals[idx+1:]...)
	switch {
	case len(p.Terminals) == 0:
		p.active = -1
	case idx < p.active:
		p.active--
	case p.active >= len(p.Terminals):
		p.active = len(p.Terminals) - 1
	}
	return p, t
}

// CloseAll terminates every session.
func (ws *Workspace) CloseAll() {
	for _, p := range ws.Projects {
		for _, t := range p.Terminals {
			_ = t.Session.Close()
		}
	}
}
