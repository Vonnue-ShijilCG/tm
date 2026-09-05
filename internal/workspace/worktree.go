package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
)

// WorktreesDir is the directory under the workspace root that holds every
// worktree tm creates: <root>/.terminal-worktrees/<project>/<name>.
const WorktreesDir = ".terminal-worktrees"

// WorktreePath returns the deterministic worktree location for a terminal
// name inside a project. The name is sanitised to a single lowercase path
// segment so the result can never escape the project's worktree directory.
func WorktreePath(root, projectName, name string) (string, error) {
	segment, err := sanitizeName(name)
	if err != nil {
		return "", err
	}
	base := filepath.Join(root, WorktreesDir, projectName)
	path := filepath.Join(base, segment)
	if rel, err := filepath.Rel(base, path); err != nil || rel != segment {
		return "", fmt.Errorf("invalid worktree name %q", name)
	}
	return path, nil
}

// sanitizeName lowercases the name and replaces everything except letters,
// digits, '.', '_' and '-' with '-', collapsing runs and trimming the ends.
func sanitizeName(name string) (string, error) {
	var b strings.Builder
	lastDash := true // suppress a leading dash
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
			lastDash = r == '-'
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}
	s := strings.Trim(b.String(), "-.")
	if s == "" {
		return "", fmt.Errorf("invalid worktree name %q", name)
	}
	return s, nil
}

// Branches lists p's local branches and the currently checked-out one.
func (ws *Workspace) Branches(p *Project) (branches []string, current string, err error) {
	repo, err := git.Open(p.Path)
	if err != nil {
		return nil, "", err
	}
	if branches, err = repo.Branches(); err != nil {
		return nil, "", err
	}
	current, err = repo.CurrentBranch()
	return branches, current, err
}

// addWorktree creates the worktree for a terminal: an existing branch is
// checked out, an unknown one is created from HEAD. It fails rather than
// touching a path that already exists.
func (ws *Workspace) addWorktree(p *Project, opts Options) (*git.Worktree, error) {
	if opts.Branch == "" {
		return nil, errors.New("a branch is required to create a worktree")
	}
	repo, err := git.Open(p.Path)
	if err != nil {
		if errors.Is(err, git.ErrNotRepository) {
			return nil, fmt.Errorf("%s is not a git repository", p.Name)
		}
		return nil, err
	}

	name := opts.Name
	if name == "" {
		name = opts.Branch
	}
	path, err := WorktreePath(ws.Root, p.Name, name)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("worktree directory already exists: %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	if repo.HasBranch(opts.Branch) {
		err = repo.AddWorktree(path, opts.Branch)
	} else {
		err = repo.AddWorktreeNewBranch(path, opts.Branch)
	}
	if err != nil {
		return nil, err
	}
	return &git.Worktree{Path: path, Branch: opts.Branch}, nil
}

// WorktreeInfo describes one worktree tm owns for a project, as listed by
// git plus what tm knows about it.
type WorktreeInfo struct {
	Name     string    // directory name under <root>/.terminal-worktrees/<project>/
	Path     string    // absolute path
	Branch   string    // checked-out branch, "" when detached
	Dirty    bool      // uncommitted or untracked changes
	Missing  bool      // registered in git but the directory is gone
	Terminal *Terminal // running terminal using it, or nil
}

// ownedName returns the worktree's directory name when path is exactly one
// level below the project's worktree directory, else "".
func (ws *Workspace) ownedName(p *Project, path string) string {
	base := filepath.Join(ws.Root, WorktreesDir, p.Name)
	if real, err := filepath.EvalSymlinks(base); err == nil {
		base = real
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.ContainsRune(rel, filepath.Separator) {
		return ""
	}
	return rel
}

// Worktrees lists the worktrees under the project's tm directory, sorted by
// name. Worktrees elsewhere (including the main checkout) are ignored, so
// removal can only ever touch directories tm created.
func (ws *Workspace) Worktrees(p *Project) ([]WorktreeInfo, error) {
	repo, err := git.Open(p.Path)
	if err != nil {
		return nil, err
	}
	list, err := repo.Worktrees()
	if err != nil {
		return nil, err
	}
	var infos []WorktreeInfo
	for _, wt := range list {
		name := ws.ownedName(p, wt.Path)
		if wt.IsMain || name == "" {
			continue
		}
		info := WorktreeInfo{Name: name, Path: wt.Path, Branch: wt.Branch, Terminal: p.terminalUsing(wt.Path)}
		if _, err := os.Stat(wt.Path); err != nil {
			info.Missing = true
		} else if dirty, err := git.IsDirty(wt.Path); err == nil {
			info.Dirty = dirty
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}

// terminalUsing returns the terminal running in the worktree at path.
func (p *Project) terminalUsing(path string) *Terminal {
	for _, t := range p.Terminals {
		if t.Worktree != nil && t.Worktree.Path == path {
			return t
		}
	}
	return nil
}

// RemoveWorktree deletes a worktree tm owns through git. A worktree with a
// running terminal is refused; a dirty one needs force; a missing directory
// is pruned from git's list. The branch is always kept.
func (ws *Workspace) RemoveWorktree(p *Project, wt WorktreeInfo, force bool) error {
	if t := p.terminalUsing(wt.Path); t != nil {
		return fmt.Errorf("terminal %d is still using this worktree; close it first", t.Num)
	}
	if ws.ownedName(p, wt.Path) == "" {
		return fmt.Errorf("refusing to remove %s: not under %s", wt.Path, WorktreesDir)
	}
	repo, err := git.Open(p.Path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(wt.Path); err != nil {
		if err := repo.Prune(); err != nil {
			return err
		}
		p.forget("", wt.Path)
		return nil
	}
	if !force {
		dirty, err := git.IsDirty(wt.Path)
		if err != nil {
			return err
		}
		if dirty {
			return errors.New("worktree has uncommitted changes")
		}
	}
	if err := repo.RemoveWorktree(wt.Path, force); err != nil {
		return err
	}
	p.forget("", wt.Path)
	return nil
}
