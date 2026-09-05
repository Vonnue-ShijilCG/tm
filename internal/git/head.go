package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoRepository is returned by Head when dir is not inside a Git checkout
// or worktree.
var ErrNoRepository = errors.New("not a git checkout")

// Head reports the branch checked out in dir by reading .git/HEAD directly,
// without running git, so it is cheap enough to ask on every screen
// refresh. dir may be a main checkout (.git is a directory) or a linked
// worktree (.git is a file pointing at the repository's worktree metadata).
// A detached HEAD yields "" with a nil error.
func Head(dir string) (string, error) {
	gitDir := filepath.Join(dir, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return "", ErrNoRepository
	}
	if !info.IsDir() { // worktree: "gitdir: <path>"
		data, err := os.ReadFile(gitDir)
		if err != nil {
			return "", ErrNoRepository
		}
		line := strings.TrimSpace(string(data))
		target, ok := strings.CutPrefix(line, "gitdir:")
		if !ok {
			return "", ErrNoRepository
		}
		gitDir = strings.TrimSpace(target)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(dir, gitDir)
		}
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", ErrNoRepository
	}
	head := strings.TrimSpace(string(data))
	if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		return ref, nil
	}
	return "", nil // detached
}
