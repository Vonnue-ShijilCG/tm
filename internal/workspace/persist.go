package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
)

// SavedTerminal is a terminal definition remembered from a previous run (or
// from a closed worktree terminal) that is not currently running. Worktree
// is an absolute path, "" for a terminal in the project checkout.
type SavedTerminal struct {
	Name     string
	Command  string
	Branch   string
	Worktree string
}

// Restore loads the state file and attaches saved terminal definitions to
// the matching projects. Unknown projects and unsafe paths are skipped.
// A malformed file is reported but leaves the workspace usable.
func (ws *Workspace) Restore() error {
	f, err := state.Load(ws.Root)
	for _, sp := range f.Projects {
		p := ws.projectByName(sp.Name)
		if p == nil {
			continue // project no longer exists in this workspace
		}
		keys := make([]string, len(sp.Terminals)) // "" marks a terminal we refused
		for i, st := range sp.Terminals {
			saved := SavedTerminal{Name: st.Name, Branch: st.Branch}
			if st.Worktree != "" {
				abs := filepath.Join(ws.Root, filepath.FromSlash(st.Worktree))
				if ws.ownedName(p, abs) == "" {
					continue // not one of ours
				}
				saved.Worktree = abs
			}
			keys[i] = saved.key()
			p.remember(saved)
		}
		p.Layout = layoutFromState(sp.Layout, keys)
		if sp.Focused != nil && *sp.Focused >= 0 && *sp.Focused < len(keys) {
			p.Focus = keys[*sp.Focused]
		}
	}
	return err
}

// Save writes the current configuration: every running terminal plus the
// remembered definitions that are not running.
func (ws *Workspace) Save() error {
	return state.Save(ws.Root, ws.Snapshot())
}

// Snapshot converts the workspace into its persisted form.
func (ws *Workspace) Snapshot() state.File {
	f := state.File{Version: state.Version}
	for _, p := range ws.Projects {
		sp := state.Project{Name: p.Name}
		var keys []string
		for _, t := range p.Terminals {
			st := state.Terminal{Name: t.Name}
			if t.Worktree != nil {
				st.Branch, st.Worktree = t.Worktree.Branch, ws.relPath(t.Worktree.Path)
			}
			sp.Terminals = append(sp.Terminals, st)
			keys = append(keys, t.Key())
		}
		for _, s := range p.Saved {
			sp.Terminals = append(sp.Terminals, state.Terminal{Name: s.Name, Branch: s.Branch, Worktree: ws.relPath(s.Worktree)})
			keys = append(keys, s.key())
		}
		if len(sp.Terminals) == 0 {
			continue
		}
		used := map[int]bool{}
		sp.Layout = layoutToState(p.Layout, keys, used)
		for i, k := range keys {
			if p.Focus != "" && k == p.Focus {
				idx := i
				sp.Focused = &idx
				break
			}
		}
		f.Projects = append(f.Projects, sp)
	}
	return f
}

func (ws *Workspace) relPath(abs string) string {
	if abs == "" {
		return ""
	}
	rel, err := filepath.Rel(ws.Root, abs)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

func (ws *Workspace) projectByName(name string) *Project {
	for _, p := range ws.Projects {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// remember adds a saved definition. A worktree is remembered once and not
// while a running terminal uses it; plain terminals may repeat.
func (p *Project) remember(s SavedTerminal) {
	if s.Worktree != "" {
		if p.terminalUsing(s.Worktree) != nil {
			return
		}
		for _, cur := range p.Saved {
			if cur.Worktree == s.Worktree {
				return
			}
		}
	}
	p.Saved = append(p.Saved, s)
}

// forget drops the saved definition for a worktree path, or the first
// plain terminal with that name and command when worktree is "".
func (p *Project) forget(name, command, worktree string) {
	for i, s := range p.Saved {
		if s.Worktree == worktree && (worktree != "" || (s.Name == name && s.Command == command)) {
			p.Saved = append(p.Saved[:i], p.Saved[i+1:]...)
			return
		}
	}
}

// OpenSaved starts exactly one saved terminal definition: a plain shell in
// the project checkout, or a shell in the definition's existing worktree,
// which is reused and never recreated. Missing or unlisted worktrees are
// refused with an explanation; nothing is created, deleted or checked out.
// On success the definition leaves p.Saved (it is running now).
func (ws *Workspace) OpenSaved(p *Project, s SavedTerminal, cols, rows int) (*Terminal, error) {
	if s.Worktree == "" {
		t, err := ws.startTerminal(p, s.Name, s.Command, nil, cols, rows)
		if err == nil {
			p.forget(s.Name, s.Command, "")
		}
		return t, err
	}
	if t := p.terminalUsing(s.Worktree); t != nil {
		return nil, fmt.Errorf("terminal %d is already running in this worktree", t.Num)
	}
	if ws.ownedName(p, s.Worktree) == "" {
		return nil, fmt.Errorf("refusing to open %s: not under %s", s.Worktree, WorktreesDir)
	}
	worktrees, err := ws.Worktrees(p)
	if err != nil {
		return nil, err
	}
	branch := s.Branch
	listed := false
	for _, wt := range worktrees {
		if wt.Path == s.Worktree {
			listed = true
			if wt.Branch != "" {
				branch = wt.Branch // git knows best what is checked out there now
			}
		}
	}
	if !listed {
		return nil, errors.New("worktree no longer in git")
	}
	if _, err := os.Stat(s.Worktree); err != nil {
		return nil, errors.New("worktree directory missing")
	}
	t, err := ws.startTerminal(p, s.Name, s.Command, &git.Worktree{Path: s.Worktree, Branch: branch}, cols, rows)
	if err == nil {
		p.forget("", "", s.Worktree)
	}
	return t, err
}

// RestoreResult describes what OpenAll or RestoreOne did.
type RestoreResult struct {
	Opened    []*Terminal
	Skipped   []string // unavailable definitions: "name (reason)"
	NotOpened int      // definitions left closed because the terminal limit was reached
	Limit     Limit    // the limit in force during the run
}

// OpenAll starts every saved terminal of p that can run, in saved order,
// reusing existing worktrees. Missing or unlisted worktrees are skipped and
// reported. Once the project runs as many terminals as the limit allows the
// remaining definitions are counted in NotOpened and stay saved, ready for
// a higher limit. Nothing is created, deleted or checked out.
func (ws *Workspace) OpenAll(p *Project, cols, rows int) (RestoreResult, error) {
	res := ws.open(p, append([]SavedTerminal(nil), p.Saved...), false, cols, rows)
	return res, nil
}

// RestoreOne starts a single saved terminal of p: the one that had focus
// when the layout was saved if it can run, otherwise the first restorable
// definition in saved order. Definitions found unavailable along the way
// are reported in Skipped; nothing else is started.
func (ws *Workspace) RestoreOne(p *Project, cols, rows int) RestoreResult {
	order := make([]SavedTerminal, 0, len(p.Saved))
	for i, s := range p.Saved {
		if p.Focus != "" && s.key() == p.Focus { // the focused definition is tried first
			order = append(order, s)
			order = append(order, p.Saved[:i]...)
			return ws.open(p, append(order, p.Saved[i+1:]...), true, cols, rows)
		}
	}
	return ws.open(p, append(order, p.Saved...), true, cols, rows)
}

// open starts the given definitions in order, stopping after the first
// success when one is set.
func (ws *Workspace) open(p *Project, defs []SavedTerminal, one bool, cols, rows int) RestoreResult {
	var res RestoreResult
	for _, s := range defs {
		name := s.Name
		if name == "" {
			name = "shell"
		}
		if err := ws.CanCreateTerminal(p); err != nil {
			res.NotOpened++
			continue
		}
		t, err := ws.OpenSaved(p, s, cols, rows)
		if err != nil {
			res.Skipped = append(res.Skipped, name+" ("+err.Error()+")")
			continue
		}
		res.Opened = append(res.Opened, t)
		if one {
			break
		}
	}
	res.Limit = ws.limit()
	return res
}

// Row is one terminal of a project as the manager lists it: running
// (Terminal set) or saved (Terminal nil). Definition is the terminal's saved
// form either way — name, branch and worktree path ("" for the project
// checkout) — so a saved row can be handed to OpenSaved or DeleteTerminal.
// Missing and Stale flag a saved worktree that cannot be opened.
type Row struct {
	Terminal   *Terminal
	Definition SavedTerminal
	Missing    bool // worktree directory is gone
	Stale      bool // git no longer lists the worktree
}

// Rows lists p's terminals, terminal-first: running terminals in creation
// order, then saved definitions in saved order. Worktrees tm owns that no
// terminal or definition uses are returned separately as unused, for the
// manager's cleanup section; they are never terminals. err reports a git
// problem listing worktrees (rows are still returned).
func (ws *Workspace) Rows(p *Project) (rows []Row, unused []WorktreeInfo, err error) {
	worktrees, err := ws.Worktrees(p)
	listed := map[string]WorktreeInfo{}
	for _, wt := range worktrees {
		listed[wt.Path] = wt
	}
	for _, t := range p.Terminals {
		rows = append(rows, Row{Terminal: t, Definition: t.saved()})
	}
	used := map[string]bool{}
	for _, s := range p.Saved {
		r := Row{Definition: s}
		if s.Worktree != "" {
			used[s.Worktree] = true
			if wt, ok := listed[s.Worktree]; ok {
				r.Missing = wt.Missing
				if wt.Branch != "" {
					r.Definition.Branch = wt.Branch // git knows what is checked out there now
				}
			} else {
				r.Stale = true
			}
		}
		rows = append(rows, r)
	}
	for _, wt := range worktrees {
		if wt.Terminal == nil && !used[wt.Path] {
			unused = append(unused, wt)
		}
	}
	return rows, unused, err
}
