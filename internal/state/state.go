// Package state persists the workspace's terminal configuration in a small
// JSON file inside the workspace. It stores only metadata (names, branches,
// workspace-relative worktree paths) — never output, environment or
// processes — and knows nothing about the UI or Git.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileName is the state file, relative to the workspace root.
const FileName = ".terminal-state.json"

// Version is written to new files so the format can evolve. Version 3 added
// the optional per-terminal command; versions 1 and 2 load unchanged, since
// a missing command simply means none.
const Version = 3

// File is the persisted workspace state.
type File struct {
	Version  int       `json:"version"`
	Projects []Project `json:"projects"`
}

// Project groups the saved terminals of one project, identified by its
// directory name under the workspace root. Layout and Focused are optional;
// leaves reference terminals by index into Terminals.
type Project struct {
	Name      string     `json:"name"`
	Terminals []Terminal `json:"terminals"`
	Layout    *Layout    `json:"layout,omitempty"`
	Focused   *int       `json:"focused,omitempty"`
}

// Layout is a node of the split tree: either a leaf (Terminal set) or a
// split (Direction, Ratio, First, Second set).
type Layout struct {
	Terminal  *int    `json:"terminal,omitempty"`
	Direction string  `json:"direction,omitempty"` // DirSide or DirStack
	Ratio     float64 `json:"ratio,omitempty"`
	First     *Layout `json:"first,omitempty"`
	Second    *Layout `json:"second,omitempty"`
}

// Split directions.
const (
	DirSide  = "side"  // children left | right
	DirStack = "stack" // children top / bottom
)

// MaxLayoutDepth bounds the tree so malformed files cannot recurse forever.
const MaxLayoutDepth = 16

// UnmarshalJSON tolerates malformed layout nodes: anything that does not
// decode becomes an empty node, which sanitisation then drops, so a broken
// layout never prevents the rest of the file from loading.
func (l *Layout) UnmarshalJSON(b []byte) error {
	type plain Layout
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		*l = Layout{}
		return nil
	}
	*l = Layout(p)
	return nil
}

// sanitizeLayout validates a tree against the (remapped) terminal indexes:
// leaves must reference an existing terminal at most once, splits need a
// known direction and two valid children (a split with one child collapses
// to it), ratios are clamped, and depth is bounded.
func sanitizeLayout(l *Layout, remap map[int]int, seen map[int]bool, depth int) *Layout {
	if l == nil || depth > MaxLayoutDepth {
		return nil
	}
	if l.Terminal != nil {
		idx, ok := remap[*l.Terminal]
		if !ok || seen[idx] {
			return nil
		}
		seen[idx] = true
		return &Layout{Terminal: &idx}
	}
	if l.Direction != DirSide && l.Direction != DirStack {
		return nil
	}
	first := sanitizeLayout(l.First, remap, seen, depth+1)
	second := sanitizeLayout(l.Second, remap, seen, depth+1)
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	ratio := l.Ratio
	if !(ratio > 0 && ratio < 1) {
		ratio = 0.5
	}
	return &Layout{Direction: l.Direction, Ratio: ratio, First: first, Second: second}
}

// Terminal is one saved terminal definition. Worktree is relative to the
// workspace root and empty for terminals running in the project checkout.
// Command is run in the terminal's shell each time the terminal is started.
type Terminal struct {
	Name     string `json:"name,omitempty"`
	Command  string `json:"command,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Worktree string `json:"worktree,omitempty"`
}

// Load reads the state file under root. A missing file yields an empty
// File and no error. Unknown JSON fields are ignored; malformed JSON is
// reported as an error together with an empty File so callers can carry on.
func Load(root string) (File, error) {
	data, err := os.ReadFile(filepath.Join(root, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return File{Version: Version}, nil
	}
	if err != nil {
		return File{Version: Version}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return File{Version: Version}, nil
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{Version: Version}, fmt.Errorf("%s: %w", FileName, err)
	}
	if f.Version == 0 {
		f.Version = Version
	}
	return f.sanitized(), nil
}

// Save writes f atomically: to a temporary file in the same directory,
// then renamed over the state file, so a failed write never leaves a
// truncated or corrupt file behind.
func Save(root string, f File) error {
	f.Version = Version
	f = f.sanitized()
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(root, FileName+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(root, FileName)); err != nil {
		cleanup()
		return err
	}
	return nil
}

// sanitized drops entries that could not have been written by tm: project
// names that are not a single path segment and worktree paths that are
// absolute or escape the workspace. A worktree may be shared by several
// terminals, distinguished by name and command; only an exact duplicate
// (same name, command and worktree) collapses to one. Plain terminals may
// repeat too (a project can have several shells).
func (f File) sanitized() File {
	out := File{Version: f.Version, Projects: []Project{}}
	for _, p := range f.Projects {
		if !ValidName(p.Name) {
			continue
		}
		var terms []Terminal
		seen := map[string]bool{}
		remap := map[int]int{} // original index -> index after filtering
		for i, t := range p.Terminals {
			t.Command = stripNewlines(t.Command)
			t.Worktree = filepath.Clean(filepath.ToSlash(t.Worktree))
			if t.Worktree == "." {
				t.Worktree = ""
			}
			if t.Worktree != "" && !ValidRelPath(t.Worktree) {
				continue
			}
			if t.Worktree != "" {
				key := t.Name + "\x00" + t.Command + "\x00" + t.Worktree
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			remap[i] = len(terms)
			terms = append(terms, t)
		}
		sp := Project{Name: p.Name, Terminals: terms}
		sp.Layout = sanitizeLayout(p.Layout, remap, map[int]bool{}, 0)
		if p.Focused != nil {
			if idx, ok := remap[*p.Focused]; ok {
				sp.Focused = &idx
			}
		}
		out.Projects = append(out.Projects, sp)
	}
	return out
}

// stripNewlines removes newlines from a command loaded from disk so that one
// stored definition types exactly one line into the shell; a command with an
// embedded newline would otherwise appear to the terminal as two lines,
// which is confusing and can corrupt the pane. This is a consistency
// measure, not a security boundary: a single line can still chain commands
// via ";", "&&", "||", "|", "&", "$(...)" or backticks, so removing
// newlines does not prevent a stored command from doing anything a plain
// command already could. Replacing with a space (rather than dropping the
// character outright) avoids silently welding two commands into one runnable
// line, e.g. "npm run devrm -rf build".
func stripNewlines(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}

// ValidName reports whether name is a plain directory name.
func ValidName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// ValidRelPath reports whether p is a relative path that stays inside the
// directory it is resolved against.
func ValidRelPath(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.IsLocal(p)
}
