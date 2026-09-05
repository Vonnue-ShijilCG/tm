// Package project discovers Git repositories inside a workspace directory.
package project

import (
	"os"
	"path/filepath"
	"sort"
)

// Project is a Git repository found in the workspace.
type Project struct {
	Name string // directory name, used as the display label
	Path string // absolute path to the repository root
}

// Discover returns every direct child of root that contains a .git entry.
// Both .git directories and .git files (worktrees, submodules) count.
// Results are sorted by name so the sidebar is stable between runs.
func Discover(root string) ([]Project, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var projects []Project
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			continue
		}
		projects = append(projects, Project{Name: entry.Name(), Path: dir})
	}

	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	return projects, nil
}
