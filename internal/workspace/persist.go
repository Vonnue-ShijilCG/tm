package workspace

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
)

// SavedTerminal is a terminal definition remembered from a previous run (or
// from a closed worktree terminal) that is not currently running. Worktree
// is an absolute path, "" for a terminal in the project checkout. Ref is
// the tm server session the terminal last ran in ("" without a server); it
// is what lets a reattaching client adopt that session instead of starting
// a new one, and it is deliberately not part of key(): a terminal is the
// same terminal whichever session runs it.
type SavedTerminal struct {
	Name     string
	Command  string
	Branch   string
	Worktree string
	Ref      string
}

// Restore loads the state file and attaches saved terminal definitions to
// the matching projects. Unknown projects are skipped. A saved worktree
// path is kept when EITHER git currently reports it as a checkout of this
// project (covers a worktree another tool made, which tm's own directory
// obviously does not list), OR it is a path tm itself could only ever have
// created (ownedName - the same check addWorktree and RemoveWorktree use),
// which also covers a tm worktree git has since forgotten: that
// definition must survive so the manager can show it as stale and let the
// user forget it deliberately, rather than have it silently vanish here.
// Anything satisfying neither is refused, so a corrupted state file can
// never point a terminal at an arbitrary directory. A malformed file is
// reported but leaves the workspace usable.
func (ws *Workspace) Restore() error {
	f, err := state.Load(ws.Root)
	ws.RestoreFrom(f)
	return err
}

// RestoreFrom is Restore for a state file already in hand - the one a tm
// server keeps for its client - with exactly the same checks.
func (ws *Workspace) RestoreFrom(f state.File) {
	for _, sp := range f.Projects {
		p := ws.projectByName(sp.Name)
		if p == nil {
			continue // project no longer exists in this workspace
		}
		var worktrees map[string]bool             // resolved lazily, once per project
		keys := make([]string, len(sp.Terminals)) // "" marks a terminal we refused
		for i, st := range sp.Terminals {
			saved := SavedTerminal{Name: st.Name, Command: st.Command, Branch: st.Branch, Ref: st.Ref}
			if st.Worktree != "" {
				abs := filepath.Join(ws.Root, filepath.FromSlash(st.Worktree))
				if worktrees == nil {
					worktrees = ws.projectWorktreePaths(p)
				}
				if !worktrees[abs] && ws.ownedName(p, abs) == "" {
					continue // neither a checkout git knows about, nor a path tm could have created
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
}

// Forget drops the saved definitions whose server session is listed in
// refs - terminals that exited while no client was attached, which must not
// be rerun unasked - together with their places in the saved layout. A ref
// no definition carries is ignored, so a list may be applied twice.
func (ws *Workspace) Forget(refs []string) {
	gone := map[string]bool{}
	for _, r := range refs {
		if r != "" {
			gone[r] = true
		}
	}
	if len(gone) == 0 {
		return
	}
	for _, p := range ws.Projects {
		// A leaf names a key, and the n-th leaf with a key belongs to the
		// n-th terminal with it, running ones first (see layoutToState), so
		// the occurrence number is what picks out the right leaf.
		seen := map[string]int{}
		for _, t := range p.Terminals {
			seen[t.Key()]++
		}
		drop := map[string]map[int]bool{}
		kept := p.Saved[:0]
		for _, s := range p.Saved {
			k := s.key()
			n := seen[k]
			seen[k]++
			if !gone[s.Ref] {
				kept = append(kept, s)
				continue
			}
			if drop[k] == nil {
				drop[k] = map[int]bool{}
			}
			drop[k][n] = true
		}
		if len(drop) == 0 {
			continue
		}
		p.Saved = kept
		p.Layout = dropLeaves(p.Layout, drop, map[string]int{})
		if p.Focus != "" && !p.hasKey(p.Focus) {
			p.Focus = ""
		}
	}
}

// dropLeaves removes the leaves whose occurrence of their key (counted in
// reading order) is listed in drop, collapsing a split left with one child.
func dropLeaves(l *Layout, drop map[string]map[int]bool, seen map[string]int) *Layout {
	if l == nil {
		return nil
	}
	if l.First == nil && l.Second == nil {
		n := seen[l.Key]
		seen[l.Key]++
		if drop[l.Key][n] {
			return nil
		}
		return l
	}
	first, second := dropLeaves(l.First, drop, seen), dropLeaves(l.Second, drop, seen)
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return &Layout{Side: l.Side, Ratio: l.Ratio, First: first, Second: second}
}

// hasKey reports whether a running terminal or saved definition has key k.
func (p *Project) hasKey(k string) bool {
	for _, t := range p.Terminals {
		if t.Key() == k {
			return true
		}
	}
	for _, s := range p.Saved {
		if s.key() == k {
			return true
		}
	}
	return false
}

// projectCheckouts is every checkout git currently reports for p - the
// main checkout and every worktree, whether or not tm created it - keyed
// by path. Restore and Rows both need to recognise a real checkout git
// knows about even when tm did not make it; ws.Worktrees deliberately
// stays narrower (tm-owned only), since removal and the "unused
// worktrees" section must never touch, or offer to touch, anything else.
// A project that is not a usable Git repository yields an empty map.
func (ws *Workspace) projectCheckouts(p *Project) map[string]git.Worktree {
	m := map[string]git.Worktree{}
	repo, err := git.Open(p.Path)
	if err != nil {
		return m
	}
	list, err := repo.Worktrees()
	if err != nil {
		return m
	}
	for _, wt := range list {
		m[wt.Path] = wt
	}
	return m
}

// projectWorktreePaths is the set of linked-worktree checkout paths git
// currently reports for p, asked for once per project (rather than once
// per saved definition) since Restore runs at startup and a project may
// have several saved worktree terminals. The main checkout is deliberately
// excluded: a terminal there is never recorded with a worktree path (see
// SavedTerminal), so a saved definition naming it can only be a corrupted
// or hand-edited entry.
func (ws *Workspace) projectWorktreePaths(p *Project) map[string]bool {
	checkouts := ws.projectCheckouts(p)
	set := make(map[string]bool, len(checkouts))
	for path, wt := range checkouts {
		if wt.IsMain {
			continue
		}
		set[path] = true
	}
	return set
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
			st := state.Terminal{Name: t.Name, Command: t.Command, Ref: sessionRef(t.Session)}
			if t.Worktree != nil {
				st.Branch, st.Worktree = t.Worktree.Branch, ws.relPath(t.Worktree.Path)
			}
			sp.Terminals = append(sp.Terminals, st)
			keys = append(keys, t.Key())
		}
		for _, s := range p.Saved {
			sp.Terminals = append(sp.Terminals, state.Terminal{Name: s.Name, Command: s.Command, Branch: s.Branch, Worktree: ws.relPath(s.Worktree), Ref: s.Ref})
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

// sessionRef is the server session ref of s, or "" for a session that runs
// in this process. The workspace learns it only through this method, so it
// never depends on the server package.
func sessionRef(s Session) string {
	if r, ok := s.(interface{ Ref() string }); ok {
		return r.Ref()
	}
	return ""
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

// remember keeps a definition that is not running. A worktree, like the
// project checkout, may hold several definitions, including more than one
// with the exact same name, command and worktree path (the form's own
// confirmation flow exists precisely to let a user create two terminals
// running the same command in one directory), so nothing here deduplicates.
// Terminals that share a Key() — most commonly unnamed plain shells with no
// command — are matched positionally on restore, which is exactly why both
// must stay in Saved for that matching to have two entries to work with.
func (p *Project) remember(s SavedTerminal) {
	p.Saved = append(p.Saved, s)
}

// forget drops the saved definition matching s's full identity - name,
// command and worktree path, exactly what Key() expresses. Since a worktree
// may now hold several definitions, this is the only safe way to remove
// one: matching on the worktree path alone (as before this task) could drop
// a different definition that happens to share the path. A definition equal
// in every field, Ref included, is preferred, so opening one of two
// same-key definitions by its session leaves the other's ref in place.
func (p *Project) forget(s SavedTerminal) {
	for i, cur := range p.Saved {
		if cur == s {
			p.Saved = append(p.Saved[:i], p.Saved[i+1:]...)
			return
		}
	}
	for i, cur := range p.Saved {
		if cur.key() == s.key() {
			p.Saved = append(p.Saved[:i], p.Saved[i+1:]...)
			return
		}
	}
}

// forgetPath drops every saved definition at worktree, regardless of name
// or command. Used only when the whole worktree is gone (removed or
// pruned), so nothing that pointed there can still be opened; callers that
// mean one specific definition must use forget instead.
func (p *Project) forgetPath(worktree string) {
	kept := p.Saved[:0]
	for _, cur := range p.Saved {
		if cur.Worktree != worktree {
			kept = append(kept, cur)
		}
	}
	p.Saved = kept
}

// OpenSaved starts exactly one saved terminal definition: a plain shell in
// the project checkout, or a shell in the definition's existing worktree,
// which is reused and never recreated. Missing or unlisted worktrees are
// refused with an explanation; nothing is created, deleted or checked out.
// On success the definition leaves p.Saved (it is running now).
func (ws *Workspace) OpenSaved(p *Project, s SavedTerminal, cols, rows int) (*Terminal, error) {
	if s.Worktree == "" {
		t, err := ws.startTerminal(p, s.Name, s.Command, s.Ref, nil, cols, rows)
		if err == nil {
			p.forget(s)
		}
		return t, err
	}
	// A terminal may live in any checkout of this project, including worktrees
	// another tool created. Removal stays restricted to tm's own directory by
	// RemoveWorktree, which is the guard that actually protects the user.
	repo, err := git.Open(p.Path)
	if err != nil {
		return nil, err
	}
	worktrees, err := repo.Worktrees()
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
	t, err := ws.startTerminal(p, s.Name, s.Command, s.Ref, &git.Worktree{Path: s.Worktree, Branch: branch}, cols, rows)
	if err == nil {
		p.forget(s)
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

// OpenLive is OpenAll restricted to the definitions whose server session
// is still running (their Ref is in live), which Adopt then hands back
// instead of a new spawn. Every other definition stays in p.Saved
// untouched. Skipped and NotOpened are counted as OpenAll counts them.
func (ws *Workspace) OpenLive(p *Project, live map[string]bool, cols, rows int) RestoreResult {
	var defs []SavedTerminal
	for _, s := range p.Saved {
		if s.Ref != "" && live[s.Ref] {
			defs = append(defs, s)
		}
	}
	return ws.open(p, defs, false, cols, rows)
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
			name = s.Command
		}
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
	worktrees, err := ws.Worktrees(p) // tm-owned only: the unused section must never touch anything else
	listed := map[string]WorktreeInfo{}
	for _, wt := range worktrees {
		listed[wt.Path] = wt
	}
	// Stale must mean what its comment says - git no longer lists the
	// worktree - not merely "tm did not create it", so a saved definition
	// in a worktree another tool made is checked against every checkout
	// git reports, not just tm's own.
	checkouts := ws.projectCheckouts(p)
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
			} else if wt, ok := checkouts[s.Worktree]; ok {
				if _, err := os.Stat(s.Worktree); err != nil {
					r.Missing = true
				}
				if wt.Branch != "" {
					r.Definition.Branch = wt.Branch
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
