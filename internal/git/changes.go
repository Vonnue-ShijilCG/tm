package git

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ChangeKind is how a path differs from HEAD.
type ChangeKind int

const (
	Modified ChangeKind = iota
	Added               // new in the working tree, tracked or not
	Deleted
)

// FileChange is one path that differs from HEAD, whether the difference is
// staged, unstaged or untracked. Added and Deleted are line counts, both zero
// for a binary file.
type FileChange struct {
	Path    string // relative to the repository root
	OldPath string // where a rename came from; empty otherwise
	Kind    ChangeKind
	Added   int
	Deleted int
	Binary  bool
}

// Changes lists every path in dir that differs from HEAD.
//
// It consults status as well as diff because git diff — in every form,
// including --cached and HEAD — ignores untracked files, so a file just
// created would otherwise not appear at all. --untracked-files=all is what
// makes files inside a new directory appear individually; without it status
// collapses the directory to a single "dir/" entry.
func Changes(dir string) ([]FileChange, error) {
	out, err := output(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	counts, binary := diffCounts(dir)

	var changes []FileChange
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if len(rec) < 4 { // "XY path"
			continue
		}
		x, y, path := rec[0], rec[1], rec[3:]
		var oldPath string
		if strings.ContainsAny(rec[:2], "RC") && i+1 < len(fields) {
			i++ // a rename or copy is followed by its source path
			oldPath = fields[i]
		}
		c := FileChange{Path: path, OldPath: oldPath, Kind: Modified}
		switch {
		case x == '?' && y == '?':
			c.Kind = Added
			c.Added, c.Binary = untrackedLines(filepath.Join(dir, path))
		case x == 'D' || y == 'D':
			c.Kind = Deleted
		case x == 'A':
			c.Kind = Added
		}
		if n, ok := counts[path]; ok {
			c.Added, c.Deleted = n[0], n[1]
		}
		if binary[path] {
			c.Binary, c.Added, c.Deleted = true, 0, 0
		}
		changes = append(changes, c)
	}
	return changes, nil
}

// diffCounts reads per-path line counts from numstat. A binary file is
// reported by git as "-\t-", which is recorded separately rather than as zero
// counts, so a binary file is distinguishable from an unchanged one.
func diffCounts(dir string) (map[string][2]int, map[string]bool) {
	counts, binary := map[string][2]int{}, map[string]bool{}
	out, err := output(dir, "diff", "HEAD", "--numstat", "-z")
	if err != nil {
		return counts, binary // no HEAD yet: nothing is tracked
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) < 3 {
			continue
		}
		add, del, path := parts[0], parts[1], parts[2]
		if path == "" && i+2 < len(fields) {
			// -z prints a rename as "add\tdel\t" with the source and
			// destination paths in the two following fields. status lists
			// the destination, so that is the key.
			path = fields[i+2]
			i += 2
		}
		if add == "-" || del == "-" {
			binary[path] = true
			continue
		}
		// numstat prints only digits or "-" here, and "-" was handled above,
		// so a parse failure cannot happen and is not worth a code path.
		a, _ := strconv.Atoi(add)
		d, _ := strconv.Atoi(del)
		counts[path] = [2]int{a, d}
	}
	return counts, binary
}

// untrackedLines counts the lines of a file git does not track yet, and
// reports whether it looks binary. git's own heuristic is a NUL byte near the
// start, and this matches it.
func untrackedLines(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	head := data
	if len(head) > 8000 {
		head = head[:8000]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return 0, true
	}
	n := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++ // git counts an unterminated last line too
	}
	return n, false
}

// ShowHEAD returns the committed content of path, byte for byte, and whether
// HEAD has it at all. A file created since the last commit reports false with
// no error. The content is not trimmed: the viewer aligns it line by line
// with the working file, so a trailing newline must survive on both sides.
func ShowHEAD(dir, path string) (string, bool, error) {
	out, err := output(dir, "show", "HEAD:"+path)
	if err != nil {
		var gitErr *Error
		if errors.As(err, &gitErr) {
			return "", false, nil // not in HEAD: a new file
		}
		return "", false, err
	}
	return out, true, nil
}

// DiffHEAD returns the unified diff of one path against HEAD with no context
// lines. With -U0 the @@ headers alone carry the whole alignment, so the body
// never has to be parsed.
func DiffHEAD(dir, path string) (string, error) {
	return output(dir, "diff", "HEAD", "-U0", "--", path)
}
