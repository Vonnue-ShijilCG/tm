package workspace

import (
	"path/filepath"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
)

// A branch is checked out in at most one place, so the branch a user picks
// determines where their terminal goes. Location is that answer, together with
// what tm already has running there.
type LocationKind int

const (
	LocationCheckout    LocationKind = iota // the project's own checkout
	LocationWorktree                        // a checkout that already exists
	LocationNewWorktree                     // one tm will create
	LocationPlainDir                        // the project is not a git repository
)

// Location is where a terminal for a branch will run.
type Location struct {
	Kind      LocationKind
	Path      string      // where the terminal will run
	Branch    string      // branch at that path, "" for a plain directory
	Foreign   bool        // an existing worktree tm did not create
	OutOfRoot bool        // an existing worktree outside the workspace root; tm can use it this session but can never persist or remove it, so opening a new terminal there is refused
	NewBranch bool        // the branch does not exist yet, so a base is required
	Terminals []*Terminal // tm's terminals already running at Path
}

// LocationSource is the repository state Locate needs, gathered once so that a
// caller resolving on every keystroke costs no git subprocesses.
type LocationSource struct {
	ws        *Workspace
	project   *Project
	isGit     bool
	current   string          // branch the project checkout has
	branches  map[string]bool // every local branch
	worktrees []git.Worktree  // every checkout git reports, tm's or not
}

// Locator gathers the repository state a form needs, in one pass. It is NOT
// (*Workspace).Snapshot — that name is already taken by the state-file snapshot
// in persist.go, and Go has no method overloading.
func (ws *Workspace) Locator(p *Project) LocationSource {
	src := LocationSource{ws: ws, project: p, branches: map[string]bool{}}
	repo, err := git.Open(p.Path)
	if err != nil {
		return src
	}
	src.isGit = true
	src.current, _ = repo.CurrentBranch()
	if names, err := repo.Branches(); err == nil {
		for _, b := range names {
			src.branches[b] = true
		}
	}
	src.worktrees, _ = repo.Worktrees()
	return src
}

// Locate resolves a branch to the one place a terminal for it can run, using
// only the snapshot. An empty branch means the project checkout. tm can report
// only on terminals it started: an empty Terminals list does not prove the
// directory is idle.
func (s LocationSource) Locate(branch string) Location {
	if !s.isGit {
		return s.withTerminals(Location{Kind: LocationPlainDir, Path: s.project.Path})
	}
	if branch == "" {
		return s.withTerminals(Location{Kind: LocationCheckout, Path: s.project.Path, Branch: s.current})
	}
	for _, wt := range s.worktrees {
		if wt.Branch != branch {
			continue
		}
		if wt.IsMain {
			// Use the project's own path, not wt.Path (git's resolved toplevel),
			// so the main checkout names the same Path regardless of whether the
			// branch was typed - ownedName already calls EvalSymlinks for the
			// identical reason: under a symlinked root the two can otherwise
			// differ, changing both the Location line and its Terminals list.
			return s.withTerminals(Location{Kind: LocationCheckout, Path: s.project.Path, Branch: branch})
		}
		return s.withTerminals(Location{
			Kind:      LocationWorktree,
			Path:      wt.Path,
			Branch:    branch,
			Foreign:   s.ws.ownedName(s.project, wt.Path) == "",
			OutOfRoot: !insideRoot(s.ws.Root, wt.Path),
		})
	}
	path, err := WorktreePath(s.ws.Root, s.project.Name, branch)
	if err != nil {
		path = ""
	}
	return s.withTerminals(Location{
		Kind:      LocationNewWorktree,
		Path:      path,
		Branch:    branch,
		NewBranch: !s.branches[branch],
	})
}

// insideRoot reports whether path can be expressed as a workspace-relative
// path that would survive state.ValidRelPath - i.e. whether it lies on or
// under root. The state file only ever stores workspace-relative worktree
// paths (see relPath/Snapshot), so a worktree outside the root can be used
// for the session it was opened in but never saved: sanitized() would drop
// the whole terminal entry and its layout leaf on the next load, silently
// losing the pane. Locate uses this to refuse opening a new terminal there
// instead of letting that happen.
func insideRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return state.ValidRelPath(filepath.ToSlash(rel))
}

// withTerminals fills in the terminals tm is running at the location's path.
func (s LocationSource) withTerminals(loc Location) Location {
	for _, t := range s.project.Terminals {
		if t.Dir(s.project) == loc.Path {
			loc.Terminals = append(loc.Terminals, t)
		}
	}
	return loc
}
