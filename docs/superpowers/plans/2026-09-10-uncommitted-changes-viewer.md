# Uncommitted changes viewer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A full-screen view, one keystroke away, showing the uncommitted changes in the directory you are looking at — files on the left, the selected file rendered twice in full and aligned line for line, red for removals and green for additions.

**Architecture:** `git diff HEAD -U0` emits hunk headers that carry the complete alignment, so no diff-body parser is needed — only the `@@` lines. A new `internal/diff` package turns those headers plus both complete file contents into a slice of aligned rows, as pure functions with no git and no UI. The UI renders those rows as a modal in the same shape as the help screen, which takes the full terminal width rather than the narrower main pane.

**Tech Stack:** Go (version in `go.mod`), Bubble Tea, Lip Gloss, the `git` CLI.

**Spec:** `docs/superpowers/specs/2026-09-10-uncommitted-changes-viewer-design.md`

## Global Constraints

- The Go toolchain is at `~/.local/go/bin` and is **not** on `PATH`. Every shell must start with `export PATH=$HOME/.local/go/bin:$PATH`.
- `gofmt` is mandatory: CI fails on any unformatted file.
- `make check` (gofmt, vet, test, test -race, build) must pass before a task is done.
- Tests require `git` on `PATH`. Never point a test at a real repository; build temp repos with `initRepo(t)` (`internal/git/git_test.go`) or `gitWorkspace(t)` (`internal/workspace/worktree_test.go`).
- Each package's `TestMain` sets `XDG_CONFIG_HOME` to an empty temp dir. Do not remove it.
- `internal/ui/keys.go` is the single source of truth for shortcuts: input matching, the help screen and the status bar all read from it.
- In terminal focus tm reserves only `Alt` chords after Task 1. Never add a plain-letter or `Ctrl` shortcut that applies while a terminal has focus.
- Tests that need a git identity must pass `-c user.name=t -c user.email=t@example.com`; the machine has no guaranteed global identity.

---

### Task 1: Let Esc reach the running program

**Files:**
- Modify: `internal/ui/keys.go` (the `Sidebar` binding)
- Modify: `README.md` (the Usage bullet naming `Esc`)
- Test: `internal/ui/terminal_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `keys.Sidebar` bound to `alt+esc` instead of `esc`. Nothing else in this plan depends on it; it is first because it is independent and immediately useful.

Today `updateTerminal` matches `keys.Sidebar` (bound to `esc`) before falling through to `forwardKey`, so pressing Esc in a pane returns to the sidebar and never reaches the program. That makes Claude Code uninterruptible from inside tm, stops Vim leaving insert mode, and stops `less` quitting. After this task tm reserves only `Alt` chords in terminal focus, which is a simpler rule than the one it has now.

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/terminal_test.go`:

```go
func TestEscReachesTheProgramAndAltEscReturnsToTheSidebar(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter") // focus a terminal

	m = press(m, "esc")

	if m.focus != focusMain {
		t.Error("esc must not leave the terminal; the program needs it")
	}
	s := (*spawned)[0]
	if len(s.keys) != 1 || s.keys[0].Code != uv.KeyEscape {
		t.Fatalf("program received %v, want a single Escape", s.keys)
	}

	m = press(m, "alt+esc")

	if m.focus != focusSidebar {
		t.Error("alt+esc should return to the sidebar")
	}
}
```

`press` builds a `tea.KeyMsg` from a string; check its switch handles `"alt+esc"` and add a case if not — the existing `alt+delete` case shows the shape (`tea.KeyMsg{Type: tea.KeyEsc, Alt: true}`).

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run TestEscReachesTheProgram -v`
Expected: FAIL — `esc must not leave the terminal`, because `keys.Sidebar` still matches it.

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/keys.go`:

```go
	Sidebar:    key.NewBinding(key.WithKeys("alt+esc"), key.WithHelp("alt+esc", "to sidebar")),
```

Update the key-policy comment at the top of the file: tm now reserves only `Alt` chords while a terminal has focus, so the sentence naming a lone `Esc` as reserved, and the note about its cost to Vim, `less` and Claude Code, are both wrong and should go.

- [ ] **Step 4: Run the whole UI suite**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -v` then `go test ./...`
Expected: PASS. Tests that press `"esc"` expecting to reach the sidebar from a terminal encode the old contract — change them to `"alt+esc"` and list each in your report. Do NOT change a test that presses `esc` to close the help screen, the form or the manager: modals intercept every key, so `esc` still closes them and that is correct.

- [ ] **Step 5: Update the README and commit**

In `README.md`, the Usage bullet reads "`Esc` returns to the sidebar, `s` shows every shortcut, `q` quits." Replace with:

```markdown
- `alt+esc` returns to the sidebar, `s` shows every shortcut, `q` quits. Plain
  `Esc` goes to the program, so it interrupts Claude Code and leaves Vim's
  insert mode as usual.
```

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui README.md
git commit -m "Let Esc reach the running program"
```

---

### Task 2: Ask git what changed

**Files:**
- Create: `internal/git/changes.go`
- Test: `internal/git/changes_test.go`

**Interfaces:**
- Consumes: the package-level `run(dir string, args ...string) (string, error)` helper in `git.go`.
- Produces:

```go
type ChangeKind int

const (
    Modified ChangeKind = iota
    Added                     // new in the working tree, tracked or not
    Deleted
)

type FileChange struct {
    Path    string // relative to the repository root
    Kind    ChangeKind
    Added   int    // lines added; 0 for a binary file
    Deleted int
    Binary  bool
}

func Changes(dir string) ([]FileChange, error)
func ShowHEAD(dir, path string) (string, bool, error) // content, whether HEAD has it
func DiffHEAD(dir, path string) (string, error)       // unified diff with -U0
```

**The trap this task exists to avoid:** `git diff`, `git diff --cached` and `git diff HEAD` all ignore untracked files. A file an agent just created is invisible to every one of them. `Changes` must consult `git status` as well, or newly created files will silently not appear.

- [ ] **Step 1: Write the failing test**

Create `internal/git/changes_test.go`:

```go
package git

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile puts content at path inside the repo.
func writeFile(t *testing.T, r *Repo, path, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Dir, path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestChangesSeesModifiedStagedAndUntrackedFiles(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "tracked.txt", "one\ntwo\nthree\n")
	if _, err := run(r.Dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(r.Dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base"); err != nil {
		t.Fatal(err)
	}

	writeFile(t, r, "tracked.txt", "one\nTWO CHANGED\nthree\n") // unstaged edit
	writeFile(t, r, "brandnew.txt", "alpha\nbeta\n")            // untracked, git diff never sees this

	got, err := Changes(r.Dir)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	byPath := map[string]FileChange{}
	for _, c := range got {
		byPath[c.Path] = c
	}

	tracked, ok := byPath["tracked.txt"]
	if !ok {
		t.Fatal("tracked.txt missing from Changes")
	}
	if tracked.Kind != Modified {
		t.Errorf("tracked.txt Kind = %v, want Modified", tracked.Kind)
	}
	if tracked.Added != 1 || tracked.Deleted != 1 {
		t.Errorf("tracked.txt counts = +%d -%d, want +1 -1", tracked.Added, tracked.Deleted)
	}

	fresh, ok := byPath["brandnew.txt"]
	if !ok {
		t.Fatal("brandnew.txt missing — untracked files are invisible to git diff and must come from status")
	}
	if fresh.Kind != Added {
		t.Errorf("brandnew.txt Kind = %v, want Added", fresh.Kind)
	}
	if fresh.Added != 2 {
		t.Errorf("brandnew.txt Added = %d, want 2 lines", fresh.Added)
	}
}

func TestChangesReportsADeletedFile(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "gone.txt", "a\nb\n")
	if _, err := run(r.Dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(r.Dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(r.Dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	got, err := Changes(r.Dir)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(got) != 1 || got[0].Path != "gone.txt" || got[0].Kind != Deleted {
		t.Fatalf("Changes = %+v, want one Deleted gone.txt", got)
	}
}

func TestShowHEADReturnsCommittedContentAndReportsNewFiles(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "f.txt", "committed\n")
	if _, err := run(r.Dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(r.Dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, r, "f.txt", "changed\n")

	content, ok, err := ShowHEAD(r.Dir, "f.txt")
	if err != nil {
		t.Fatalf("ShowHEAD: %v", err)
	}
	if !ok || content != "committed\n" {
		t.Errorf("ShowHEAD = %q, %v; want the committed content", content, ok)
	}

	if _, ok, err := ShowHEAD(r.Dir, "never-committed.txt"); err != nil || ok {
		t.Errorf("ShowHEAD on a file HEAD does not have = ok %v, err %v; want false, nil", ok, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/git -run 'TestChanges|TestShowHEAD' -v`
Expected: FAIL to build — `undefined: Changes`, `undefined: ShowHEAD`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/git/changes.go`:

```go
package git

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ChangeKind is how a path differs from HEAD.
type ChangeKind int

const (
	Modified ChangeKind = iota
	Added                // new in the working tree, tracked or not
	Deleted
)

// FileChange is one path that differs from HEAD, whether the difference is
// staged, unstaged or untracked. Added and Deleted are line counts, both zero
// for a binary file.
type FileChange struct {
	Path    string // relative to the repository root
	Kind    ChangeKind
	Added   int
	Deleted int
	Binary  bool
}

// Changes lists every path in dir that differs from HEAD.
//
// It consults status as well as diff because git diff — in every form,
// including --cached and HEAD — ignores untracked files, so a file just
// created would otherwise not appear at all.
func Changes(dir string) ([]FileChange, error) {
	out, err := run(dir, "status", "--porcelain=v1", "-z")
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
		if x == 'R' || x == 'C' {
			i++ // a rename or copy is followed by its source path; skip it
		}
		c := FileChange{Path: path, Kind: Modified}
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
	out, err := run(dir, "diff", "HEAD", "--numstat", "-z")
	if err != nil || out == "" {
		return counts, binary // no HEAD yet, or nothing tracked has changed
	}
	fields := strings.Split(out, "\x00")
	for i := 0; i+2 < len(fields)+1 && i < len(fields); i++ {
		parts := strings.Split(fields[i], "\t")
		if len(parts) < 3 {
			continue
		}
		add, del, path := parts[0], parts[1], parts[2]
		if path == "" && i+1 < len(fields) {
			path = fields[i+1] // -z puts a renamed path in the following field
			i++
		}
		if add == "-" || del == "-" {
			binary[path] = true
			continue
		}
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
	return bytes.Count(data, []byte("\n")), false
}

// ShowHEAD returns the committed content of path and whether HEAD has it at
// all. A file created since the last commit reports false with no error.
func ShowHEAD(dir, path string) (string, bool, error) {
	out, err := run(dir, "show", "HEAD:"+path)
	if err != nil {
		var gitErr *Error
		if errorsAs(err, &gitErr) {
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
	return run(dir, "diff", "HEAD", "-U0", "--", path)
}
```

`errorsAs` is a placeholder name for the standard `errors.As`: import `errors` and call `errors.As(err, &gitErr)`. The package already defines `*Error` for a git command that exited non-zero.

Note that `run` trims trailing whitespace from its output, so `ShowHEAD` loses a file's final newline. That is acceptable here — the viewer splits into lines and never writes the content back.

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/git -v`
Expected: PASS, including every pre-existing test.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/git
git add internal/git
git commit -m "Report the files that differ from HEAD, untracked ones included"
```

---

### Task 3: Align two versions of a file

**Files:**
- Create: `internal/diff/diff.go`
- Test: `internal/diff/diff_test.go`

**Interfaces:**
- Consumes: nothing. This package must import neither `git` nor any UI package — it is pure functions over strings, which is what makes the only real algorithm here testable on its own.
- Produces:

```go
type Hunk struct {
    OldStart, OldCount int
    NewStart, NewCount int
}

type Row struct {
    OldNum  int    // 1-based; 0 when the left side is padding
    OldText string
    NewNum  int    // 0 when the right side is padding
    NewText string
    Changed bool
}

func ParseHunks(unified string) []Hunk
func Align(old, new []string, hunks []Hunk) []Row
```

**Reading a `-U0` hunk header.** `@@ -a,b +c,d @@` means: `b` lines starting at old line `a` became `d` lines starting at new line `c`. A missing count means 1, so `@@ -2 +2 @@` is one line for one. When a count is **zero** the start names the line the change sits *after*, not a line that changed: `@@ -3,0 +4 @@` inserts one line after old line 3.

- [ ] **Step 1: Write the failing test**

Create `internal/diff/diff_test.go`:

```go
package diff

import "testing"

func TestParseHunksReadsTheHeaders(t *testing.T) {
	// Produced by: git diff HEAD -U0 on a,b,c,d,e -> a,B,c,NEW,d
	in := "@@ -2 +2 @@ a\n@@ -3,0 +4 @@ c\n@@ -5 +5,0 @@ d\n"

	got := ParseHunks(in)

	want := []Hunk{
		{OldStart: 2, OldCount: 1, NewStart: 2, NewCount: 1},
		{OldStart: 3, OldCount: 0, NewStart: 4, NewCount: 1},
		{OldStart: 5, OldCount: 1, NewStart: 5, NewCount: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d hunks, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hunk %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseHunksIgnoresEverythingElse(t *testing.T) {
	in := "diff --git a/f.txt b/f.txt\nindex 1..2 100644\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-old\n+new\n"

	got := ParseHunks(in)

	if len(got) != 1 || got[0].OldStart != 1 || got[0].NewCount != 1 {
		t.Fatalf("got %+v, want the single @@ header", got)
	}
}

func TestAlignKeepsBothSidesInStep(t *testing.T) {
	old := []string{"a", "b", "c", "d", "e"}
	fresh := []string{"a", "B CHANGED", "c", "NEW LINE", "d"}
	hunks := []Hunk{
		{OldStart: 2, OldCount: 1, NewStart: 2, NewCount: 1},
		{OldStart: 3, OldCount: 0, NewStart: 4, NewCount: 1},
		{OldStart: 5, OldCount: 1, NewStart: 5, NewCount: 0},
	}

	rows := Align(old, fresh, hunks)

	want := []Row{
		{OldNum: 1, OldText: "a", NewNum: 1, NewText: "a"},
		{OldNum: 2, OldText: "b", NewNum: 2, NewText: "B CHANGED", Changed: true},
		{OldNum: 3, OldText: "c", NewNum: 3, NewText: "c"},
		{OldNum: 0, OldText: "", NewNum: 4, NewText: "NEW LINE", Changed: true},
		{OldNum: 4, OldText: "d", NewNum: 5, NewText: "d"},
		{OldNum: 5, OldText: "e", NewNum: 0, NewText: "", Changed: true},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d:\n%+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v\n      want %+v", i, rows[i], want[i])
		}
	}
}

func TestAlignWithNoHunksIsTheFileUnchanged(t *testing.T) {
	lines := []string{"x", "y"}

	rows := Align(lines, lines, nil)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for i, r := range rows {
		if r.Changed || r.OldNum != i+1 || r.NewNum != i+1 {
			t.Errorf("row %d = %+v, want an unchanged pair", i, r)
		}
	}
}

func TestAlignANewFileHasNoLeftSide(t *testing.T) {
	fresh := []string{"one", "two"}
	hunks := []Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 2}}

	rows := Align(nil, fresh, hunks)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	for i, r := range rows {
		if r.OldNum != 0 || r.OldText != "" || !r.Changed || r.NewNum != i+1 {
			t.Errorf("row %d = %+v, want a right-only added row", i, r)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/diff -v`
Expected: FAIL to build — `undefined: ParseHunks`, `undefined: Align`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/diff/diff.go`:

```go
// Package diff aligns the committed and working versions of a file so both can
// be shown in full, side by side, with each line facing its counterpart.
//
// It works from the @@ headers of a diff produced with -U0, which carry the
// whole alignment on their own: the diff body is never read.
package diff

import (
	"strconv"
	"strings"
)

// Hunk is one changed region. OldCount lines starting at OldStart in the
// committed file became NewCount lines starting at NewStart in the working
// file. When a count is zero its start names the line the change sits AFTER,
// not a line that changed: "@@ -3,0 +4 @@" inserts after old line 3.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
}

// Row is one line of the aligned view. A side is padding — present only to
// keep the two columns in step — when its number is 0.
type Row struct {
	OldNum  int
	OldText string
	NewNum  int
	NewText string
	Changed bool
}

// ParseHunks reads the @@ headers of a unified diff and ignores everything
// else, so it does not care whether the body has context lines.
func ParseHunks(unified string) []Hunk {
	var hunks []Hunk
	for _, line := range strings.Split(unified, "\n") {
		if !strings.HasPrefix(line, "@@ ") {
			continue
		}
		end := strings.Index(line[3:], " @@")
		if end < 0 {
			continue
		}
		parts := strings.Fields(line[3 : 3+end])
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "-") || !strings.HasPrefix(parts[1], "+") {
			continue
		}
		oldStart, oldCount := parseRange(parts[0][1:])
		newStart, newCount := parseRange(parts[1][1:])
		hunks = append(hunks, Hunk{oldStart, oldCount, newStart, newCount})
	}
	return hunks
}

// parseRange reads "start" or "start,count"; a missing count means one line.
func parseRange(s string) (start, count int) {
	count = 1
	if comma := strings.IndexByte(s, ','); comma >= 0 {
		count, _ = strconv.Atoi(s[comma+1:])
		s = s[:comma]
	}
	start, _ = strconv.Atoi(s)
	return start, count
}

// Align walks both complete files, emitting unchanged lines as pairs and each
// hunk as its own rows, padding whichever side is shorter so line numbers on
// the two sides never drift apart.
func Align(old, fresh []string, hunks []Hunk) []Row {
	var rows []Row
	o, n := 0, 0 // 0-based cursors into old and fresh

	for _, h := range hunks {
		// Where the unchanged run before this hunk ends. A zero count means the
		// start names the line before the change, so that line is unchanged.
		stop := h.OldStart - 1
		if h.OldCount == 0 {
			stop = h.OldStart
		}
		for o < stop && o < len(old) && n < len(fresh) {
			rows = append(rows, Row{OldNum: o + 1, OldText: old[o], NewNum: n + 1, NewText: fresh[n]})
			o++
			n++
		}
		for i := 0; i < max(h.OldCount, h.NewCount); i++ {
			r := Row{Changed: true}
			if i < h.OldCount && o < len(old) {
				r.OldNum, r.OldText = o+1, old[o]
				o++
			}
			if i < h.NewCount && n < len(fresh) {
				r.NewNum, r.NewText = n+1, fresh[n]
				n++
			}
			rows = append(rows, r)
		}
	}
	for o < len(old) && n < len(fresh) {
		rows = append(rows, Row{OldNum: o + 1, OldText: old[o], NewNum: n + 1, NewText: fresh[n]})
		o++
		n++
	}
	return rows
}
```

`max` is a builtin in the Go version this module targets; the UI package already relies on it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/diff -v`
Expected: PASS, all five.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/diff
git add internal/diff
git commit -m "Align a file's committed and working versions"
```

---

### Task 4: Open the viewer and list the changed files

**Files:**
- Create: `internal/ui/diffview.go`
- Modify: `internal/ui/keys.go`, `internal/ui/model.go`
- Test: `internal/ui/diffview_test.go`

**Interfaces:**
- Consumes: `git.Changes(dir)`, `git.FileChange` (Task 2); `(*Terminal).Dir(p)` and `workspace.Project`.
- Produces:

```go
type diffView struct {
    dir    string            // the directory whose changes these are
    title  string            // project name, and branch when there is one
    files  []git.FileChange
    cursor int               // selected file
    err    string            // git problem, e.g. not a repository
}

func newDiffView(dir, title string) *diffView
func (d *diffView) view(width, height int) string
```

plus `Model.diff *diffView`, `keys.Diff` (`d`), `keys.DiffTerm` (`alt+d`), `keys.DiffClose` (`esc`), `keys.DiffDown`/`DiffUp` (`j`/`k` and the arrows).

This task delivers the modal and the file list only. The two code columns are Task 5.

- [ ] **Step 1: Write the failing test**

Create `internal/ui/diffview_test.go`:

```go
package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDOpensTheChangesViewAndListsChangedFiles(t *testing.T) {
	m, _, root := gitModel(t)
	if err := os.WriteFile(filepath.Join(root, "app", "fresh.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	if m.diff == nil {
		t.Fatal("d should open the changes view")
	}
	view := m.View()
	if !strings.Contains(view, "Uncommitted changes") {
		t.Errorf("the view should name itself:\n%s", view)
	}
	if !strings.Contains(view, "fresh.txt") {
		t.Errorf("an untracked file must be listed:\n%s", view)
	}

	m = press(m, "esc")
	if m.diff != nil {
		t.Error("esc should close the changes view")
	}
}

func TestTheChangesViewSaysWhenNothingChanged(t *testing.T) {
	m, _, _ := gitModel(t)

	m = press(m, "d")

	if !strings.Contains(m.View(), "No uncommitted changes") {
		t.Errorf("a clean checkout should say so:\n%s", m.View())
	}
}
```

`gitModel(t)` returns `(Model, *[]*fakeSession, string)` — a model over a real temp git repo, the spawned fakes, and the workspace root. The project inside it is named `app`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run TestTheChangesView -v` and `-run TestDOpensTheChangesView -v`
Expected: FAIL to build — `m.diff undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/ui/diffview.go`:

```go
package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
)

// diffView shows the uncommitted changes of one directory: the files that
// differ from HEAD, and the selected file rendered as it was committed beside
// how it is now. tm reads this when the view opens; it does not follow an
// agent's edits live, so the content cannot shift while it is being read.
type diffView struct {
	dir    string
	title  string
	files  []git.FileChange
	cursor int
	err    string
}

const diffListWidth = 32

// newDiffView reads the changes in dir. A directory that is not a git
// repository, or a git that fails, is reported in the view rather than
// preventing it from opening.
func newDiffView(dir, title string) *diffView {
	d := &diffView{dir: dir, title: title}
	files, err := git.Changes(dir)
	if err != nil {
		d.err = err.Error()
		return d
	}
	d.files = files
	return d
}

// move changes the selected file, clamping at both ends.
func (d *diffView) move(delta int) {
	if len(d.files) == 0 {
		return
	}
	d.cursor = min(max(d.cursor+delta, 0), len(d.files)-1)
}

func (d *diffView) view(width, height int) string {
	var b strings.Builder
	b.WriteString(d.header(width) + "\n\n")
	if d.err != "" {
		b.WriteString(" " + errorStyle.Render(d.err) + "\n")
		return b.String()
	}
	if len(d.files) == 0 {
		b.WriteString(" " + hintStyle.Render("No uncommitted changes in "+d.dir) + "\n")
		return b.String()
	}
	b.WriteString(d.list(diffListWidth))
	return b.String()
}

// header names what is being shown and totals the changes.
func (d *diffView) header(width int) string {
	added, deleted := 0, 0
	for _, f := range d.files {
		added += f.Added
		deleted += f.Deleted
	}
	left := titleStyle.Render("Uncommitted changes") + "  " + d.title
	right := hintStyle.Render(fmt.Sprintf("%d file%s  +%d  -%d", len(d.files), plural(len(d.files)), added, deleted))
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return " " + left + strings.Repeat(" ", gap) + right
}

// list renders the changed files, basename first with the directory beneath,
// so a long path is never truncated mid-word.
func (d *diffView) list(width int) string {
	var b strings.Builder
	for i, f := range d.files {
		marker, style := "  ", lipgloss.NewStyle()
		if i == d.cursor {
			marker, style = "› ", selectedStyle
		}
		b.WriteString(marker + style.Render(ansi.Truncate(filepath.Base(f.Path), width-3, "…")) + "\n")
		if dir := filepath.Dir(f.Path); dir != "." {
			b.WriteString("    " + hintStyle.Render(ansi.Truncate(dir, width-5, "…")) + "\n")
		}
		b.WriteString("    " + d.counts(f) + "\n")
	}
	return b.String()
}

// counts is one file's summary: line counts, or what makes it special.
func (d *diffView) counts(f git.FileChange) string {
	switch {
	case f.Binary:
		return hintStyle.Render("binary")
	case f.Kind == git.Deleted:
		return delStyle.Render("deleted")
	case f.Kind == git.Added:
		return addStyle.Render(fmt.Sprintf("+%d", f.Added)) + hintStyle.Render("  new file")
	default:
		return addStyle.Render(fmt.Sprintf("+%d", f.Added)) + " " + delStyle.Render(fmt.Sprintf("-%d", f.Deleted))
	}
}
```

Add the two colours to `internal/ui/styles.go`, beside the existing ones:

```go
	added   = lipgloss.Color("108") // additions: green, legible on a dark ground
	removed = lipgloss.Color("174") // removals: red
)

var (
	addStyle = lipgloss.NewStyle().Foreground(added)
	delStyle = lipgloss.NewStyle().Foreground(removed)
```

In `internal/ui/keys.go`, add the bindings and a help section:

```go
	// Uncommitted changes view.
	Diff      key.Binding
	DiffTerm  key.Binding
	DiffDown  key.Binding
	DiffUp    key.Binding
	DiffClose key.Binding
```

```go
	Diff:      key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "uncommitted changes")),
	DiffTerm:  key.NewBinding(key.WithKeys("alt+d"), key.WithHelp("alt+d", "uncommitted changes")),
	DiffDown:  key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j / ↓", "next file")),
	DiffUp:    key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k / ↑", "prev file")),
	DiffClose: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
```

and in `helpSections()`, before the HELP section:

```go
		{"UNCOMMITTED CHANGES", []key.Binding{k.DiffDown, k.DiffUp, k.DiffClose}},
```

In `internal/ui/model.go`: add `diff *diffView` to `Model`, beside `panel`. Dispatch it in `Update`'s key branch **before** the manager and form, since it is a full-screen modal like help:

```go
		if m.diff != nil {
			return m.updateDiff(msg)
		}
```

```go
// updateDiff handles a key while the changes view is open. Like the help
// screen it consumes every key, so nothing reaches a terminal underneath.
func (m Model) updateDiff(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.DiffClose):
		m.diff = nil
	case key.Matches(msg, keys.DiffDown):
		m.diff.move(1)
	case key.Matches(msg, keys.DiffUp):
		m.diff.move(-1)
	}
	return m, nil
}

// openDiff shows the uncommitted changes of wherever the user's attention is:
// the focused terminal's directory, else the selected project's.
func (m *Model) openDiff() {
	p := m.selectedProject()
	if p == nil {
		return
	}
	dir, title := p.Path, p.Name
	if t := p.ActiveTerminal(); t != nil && m.focus == focusMain {
		dir = t.Dir(p)
	}
	if branch := m.branches.get(dir); branch != "" {
		title += "  " + worktreeMark + branch
	}
	m.diff = newDiffView(dir, title)
}
```

Wire the keys: `case key.Matches(msg, keys.Diff): m.openDiff()` in `updateSidebar`, and `case key.Matches(msg, keys.DiffTerm): m.openDiff()` in `updateTerminal`.

Render it in `View()`, in the same shape as help and before the sidebar split:

```go
	if m.diff != nil {
		style := mainStyle.Padding(0, 1)
		screen := framed(style, true, m.width, paneHeight).Render(
			m.diff.view(m.width-style.GetHorizontalFrameSize(), paneHeight-style.GetVerticalFrameSize()))
		return lipgloss.JoinVertical(lipgloss.Left, screen, m.viewStatus())
	}
```

And give it a status-bar case in `viewStatus`, before `m.panel != nil`:

```go
	case m.diff != nil:
		bindings = []key.Binding{keys.DiffDown, keys.DiffUp, keys.DiffClose}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -v` then `go test ./...`
Expected: PASS. `TestEveryBindingIsInHelp` walks the keyMap by reflection, so every new binding must appear in `helpSections()` or it fails — that is the check working, not a problem to route around.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui
git commit -m "Open a view of a directory's uncommitted changes"
```

---

### Task 5: Show the selected file, committed beside current

**Files:**
- Modify: `internal/ui/diffview.go`
- Test: `internal/ui/diffview_test.go`

**Interfaces:**
- Consumes: `git.ShowHEAD`, `git.DiffHEAD` (Task 2); `diff.ParseHunks`, `diff.Align`, `diff.Row` (Task 3).
- Produces: `diffView.rows []diff.Row`, `diffView.load()`, and the two-column rendering.

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/diffview_test.go`:

```go
func TestTheSelectedFileShowsBothVersionsAligned(t *testing.T) {
	m, _, root := gitModel(t)
	app := filepath.Join(root, "app")
	if err := os.WriteFile(filepath.Join(app, "f.txt"), []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, app, "add", "-A")
	gitRun(t, app, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(app, "f.txt"), []byte("a\nB CHANGED\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")
	view := m.View()

	if !strings.Contains(view, "b") || !strings.Contains(view, "B CHANGED") {
		t.Errorf("both the committed line and the current one should show:\n%s", view)
	}
	if !strings.Contains(view, "HEAD") || !strings.Contains(view, "WORKING TREE") {
		t.Errorf("the two columns should be labelled:\n%s", view)
	}
	if !strings.Contains(view, "a") {
		t.Errorf("unchanged lines are shown too - this is a full-file view:\n%s", view)
	}
}

func TestANewFileHasNoCommittedSide(t *testing.T) {
	m, _, root := gitModel(t)
	if err := os.WriteFile(filepath.Join(root, "app", "fresh.txt"), []byte("only\nnew\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")

	d := m.diff
	if len(d.rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(d.rows), d.rows)
	}
	for i, r := range d.rows {
		if r.OldNum != 0 || r.OldText != "" {
			t.Errorf("row %d has a committed side %+v; a new file has none", i, r)
		}
		if !r.Changed || r.NewText == "" {
			t.Errorf("row %d = %+v, want an added line", i, r)
		}
	}
}
```

Add this helper to the same file if the package does not already have one:

```go
// gitRun runs a git command in dir and fails the test if it errors.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run 'BothVersionsAligned|NoCommittedSide' -v`
Expected: FAIL — `d.rows undefined`, and the view shows only the file list.

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/diffview.go`, add the row state and loading:

```go
	rows   []diff.Row // the selected file, aligned; nil until loaded
	loadErr string    // problem reading the selected file
```

```go
// load reads the selected file's committed and working versions and aligns
// them. A file with no committed version — one just created — aligns against
// an empty left side, which renders as all additions.
func (d *diffView) load() {
	d.rows, d.loadErr = nil, ""
	if len(d.files) == 0 {
		return
	}
	f := d.files[d.cursor]
	if f.Binary {
		d.loadErr = "binary file"
		return
	}
	old, _, err := git.ShowHEAD(d.dir, f.Path)
	if err != nil {
		d.loadErr = err.Error()
		return
	}
	fresh := ""
	if f.Kind != git.Deleted {
		data, err := os.ReadFile(filepath.Join(d.dir, f.Path))
		if err != nil {
			d.loadErr = err.Error()
			return
		}
		fresh = string(data)
	}
	unified, err := git.DiffHEAD(d.dir, f.Path)
	if err != nil {
		d.loadErr = err.Error()
		return
	}
	d.rows = diff.Align(splitLines(old), splitLines(fresh), diff.ParseHunks(unified))
}

// splitLines splits file content into lines without a trailing empty element.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
```

Call `d.load()` at the end of `newDiffView` and at the end of `move`, so the rows always match the cursor.

An untracked file has no diff at all — `git diff HEAD` ignores it — so `ParseHunks` returns nothing and `Align` would render it as unchanged. Handle that case explicitly by synthesising a single hunk covering the whole file:

```go
	hunks := diff.ParseHunks(unified)
	if f.Kind == git.Added && len(hunks) == 0 {
		lines := splitLines(fresh)
		hunks = []diff.Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: len(lines)}}
	}
	d.rows = diff.Align(splitLines(old), splitLines(fresh), hunks)
```

Render the columns. Replace `view`'s body after the list with a horizontal join:

```go
func (d *diffView) view(width, height int) string {
	var b strings.Builder
	b.WriteString(d.header(width) + "\n\n")
	if d.err != "" {
		return b.String() + " " + errorStyle.Render(d.err) + "\n"
	}
	if len(d.files) == 0 {
		return b.String() + " " + hintStyle.Render("No uncommitted changes in "+d.dir) + "\n"
	}
	body := max(height-3, 1)
	list := lipgloss.NewStyle().Width(diffListWidth).Height(body).Render(d.list(diffListWidth))
	divider := dividerStyle.Render(strings.TrimRight(strings.Repeat(verticalDivider+"\n", body), "\n"))
	pane := d.panes(max(width-diffListWidth-2, 20), body)
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, list, divider, pane))
	return b.String()
}

// panes renders the committed and current versions side by side. Both columns
// are the same width and scroll together, so a line always faces its
// counterpart.
func (d *diffView) panes(width, height int) string {
	if d.loadErr != "" {
		return " " + hintStyle.Render(d.loadErr)
	}
	half := (width - 1) / 2
	var b strings.Builder
	b.WriteString(" " + delStyle.Render("HEAD") + hintStyle.Render("  committed") +
		strings.Repeat(" ", max(half-20, 1)) +
		addStyle.Render("WORKING TREE") + hintStyle.Render("  yours now") + "\n")
	for i := 0; i < height-1; i++ {
		if i >= len(d.rows) {
			b.WriteString("\n")
			continue
		}
		r := d.rows[i]
		b.WriteString(d.side(r.OldNum, r.OldText, r.Changed && r.OldNum != 0, delStyle, half) + " " +
			d.side(r.NewNum, r.NewText, r.Changed && r.NewNum != 0, addStyle, half) + "\n")
	}
	return b.String()
}

// side renders one column of one row: a line number gutter, then the text,
// coloured when the line is part of a change and blank when it is padding.
func (d *diffView) side(num int, text string, changed bool, style lipgloss.Style, width int) string {
	gutter := "    "
	if num > 0 {
		gutter = fmt.Sprintf("%4d", num)
	}
	body := ansi.Truncate(text, max(width-6, 1), "")
	if changed {
		return hintStyle.Render(gutter) + " " + style.Render(ansi.Truncate(body, max(width-6, 1), ""))
	}
	return hintStyle.Render(gutter) + " " + body
}
```

Import `os`, `path/filepath` and `github.com/Vonnue-ShijilCG/tm/internal/diff` in `diffview.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -v` then `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui
git commit -m "Show a file's committed and current versions side by side"
```

---

### Task 6: Scroll, switch focus, and jump between changes

**Files:**
- Modify: `internal/ui/diffview.go`, `internal/ui/keys.go`, `internal/ui/model.go`
- Test: `internal/ui/diffview_test.go`

**Interfaces:**
- Consumes: `diffView.rows` (Task 5).
- Produces: `diffView.top`, `diffView.left`, `diffView.listFocus bool`; `keys.DiffTab`, `DiffLeft`, `DiffRight`, `DiffNextChange`, `DiffPrevChange`, `DiffRefresh`.

**Why `Tab` rather than splitting j/k from the arrows:** everywhere else in tm `j`/`k` and the arrow keys are synonyms — the Terminal Manager binds both to "move a row". Making them diverge here would be the only place in the application where they mean different things. `Tab` moves between the file list and the diff, exactly as it moves between the sidebar and the main pane, and `j`/`k`/arrows then move within whichever has focus.

**Why long lines scroll rather than wrap:** a wrapped line takes two rows on one side and one on the other, which pushes everything below out of alignment — and the alignment is the whole point of the view.

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/diffview_test.go`:

```go
func TestTabMovesFocusAndScrollingIsLockedAcrossColumns(t *testing.T) {
	m, _, root := gitModel(t)
	app := filepath.Join(root, "app")
	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(filepath.Join(app, "big.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, app, "add", "-A")
	gitRun(t, app, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base")
	lines[150] = "line 151 CHANGED"
	if err := os.WriteFile(filepath.Join(app, "big.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d")
	if !m.diff.listFocus {
		t.Fatal("the file list should have focus when the view opens")
	}

	m = press(m, "tab")
	if m.diff.listFocus {
		t.Fatal("tab should move focus to the diff")
	}

	m = press(m, "down", "down", "down")
	if m.diff.top != 3 {
		t.Errorf("top = %d, want 3 after three downs", m.diff.top)
	}

	m = press(m, "right", "right")
	if m.diff.left != 2*diffHScrollStep {
		t.Errorf("left = %d, want %d; both columns share one horizontal offset",
			m.diff.left, 2*diffHScrollStep)
	}
}

func TestNextChangeJumpsPastUnchangedLines(t *testing.T) {
	m, _, root := gitModel(t)
	app := filepath.Join(root, "app")
	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	if err := os.WriteFile(filepath.Join(app, "big.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, app, "add", "-A")
	gitRun(t, app, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base")
	lines[150] = "line 151 CHANGED"
	if err := os.WriteFile(filepath.Join(app, "big.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m = press(m, "d", "tab", "n")

	if m.diff.top < 140 {
		t.Errorf("top = %d; n should jump to the change around row 150, not scroll one line", m.diff.top)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run 'TabMovesFocus|NextChangeJumps' -v`
Expected: FAIL to build — `m.diff.listFocus undefined`, `diffHScrollStep undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/diffview.go`:

```go
// diffHScrollStep is how far one left/right press moves. Whole words rather
// than single columns, because a line of code is scanned in words.
const diffHScrollStep = 8
```

Add to the struct:

```go
	top       int  // first visible row of the diff
	left      int  // horizontal offset, shared by both columns
	listFocus bool // true when j/k move the file list rather than the diff
```

Set `listFocus: true` in `newDiffView`, and reset `top`, `left` in `load()` so switching file starts at the top.

```go
// scroll moves the diff vertically, clamping so the view cannot run past
// either end of the file.
func (d *diffView) scroll(delta, height int) {
	d.top = min(max(d.top+delta, 0), max(len(d.rows)-height, 0))
}

// scrollX moves both columns sideways together; there is one offset, so they
// stay showing the same columns of the same lines.
func (d *diffView) scrollX(delta int) {
	d.left = max(d.left+delta, 0)
}

// jumpChange moves to the next or previous run of changed rows. Without it a
// single edit in a long file would have to be found by scrolling, which is
// what makes a full-file view practical.
func (d *diffView) jumpChange(dir, height int) {
	i := d.top
	for i += dir; i > 0 && i < len(d.rows); i += dir {
		if d.rows[i].Changed && !d.rows[i-1].Changed {
			d.top = min(max(i-2, 0), max(len(d.rows)-height, 0))
			return
		}
	}
}
```

Apply `d.left` when rendering: in `side`, take the visible slice of the text before truncating.

```go
	runes := []rune(text)
	if d.left < len(runes) {
		text = string(runes[d.left:])
	} else {
		text = ""
	}
```

In `keys.go`:

```go
	DiffTab:        key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "list / diff")),
	DiffLeft:       key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "scroll left")),
	DiffRight:      key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "scroll right")),
	DiffNextChange: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next change")),
	DiffPrevChange: key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "prev change")),
	DiffRefresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "re-read")),
```

Add all six to the UNCOMMITTED CHANGES help section.

In `updateDiff`, route by focus and pass the body height so scrolling can clamp:

```go
func (m Model) updateDiff(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	height := max(m.height-statusHeight-mainStyle.GetVerticalFrameSize()-3, 1)
	switch {
	case key.Matches(msg, keys.DiffClose):
		m.diff = nil
	case key.Matches(msg, keys.DiffTab):
		m.diff.listFocus = !m.diff.listFocus
	case key.Matches(msg, keys.DiffRefresh):
		m.openDiff() // re-read from git; an agent may have written since
	case key.Matches(msg, keys.DiffNextChange):
		m.diff.jumpChange(1, height)
	case key.Matches(msg, keys.DiffPrevChange):
		m.diff.jumpChange(-1, height)
	case key.Matches(msg, keys.DiffLeft):
		m.diff.scrollX(-diffHScrollStep)
	case key.Matches(msg, keys.DiffRight):
		m.diff.scrollX(diffHScrollStep)
	case key.Matches(msg, keys.DiffDown):
		if m.diff.listFocus {
			m.diff.move(1)
		} else {
			m.diff.scroll(1, height)
		}
	case key.Matches(msg, keys.DiffUp):
		if m.diff.listFocus {
			m.diff.move(-1)
		} else {
			m.diff.scroll(-1, height)
		}
	}
	return m, nil
}
```

`panes` must render from `d.top` rather than row 0: index `d.rows[d.top+i]`.

Show which half has focus by rendering the focused one's heading in `selectedStyle` — the file list heading when `listFocus`, the column headings otherwise.

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -v` then `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui
git commit -m "Scroll the changes view and jump between changes"
```

---

### Task 7: Document it

**Files:**
- Modify: `README.md`, `CLAUDE.md`
- Test: none; documentation.

- [ ] **Step 1: Update the README**

Add to the feature list, after the mouse-wheel bullet:

```markdown
- See what changed without leaving tm: `d` shows the uncommitted changes in the
  directory you are looking at, each file as it was committed beside how it is
  now.
```

Add to Usage, after the `n` bullet:

```markdown
- `d` opens the uncommitted changes of the focused terminal's directory: the
  changed files on the left, the selected one shown twice in full, committed
  against current. `tab` moves between the list and the diff, `n` jumps to the
  next change, `r` re-reads from git.
```

- [ ] **Step 2: Update CLAUDE.md**

Add a section after "Scrollback":

```markdown
### Uncommitted changes view

`d` (sidebar) and `alt+d` (terminal) open `ui/diffview.go`, a full-screen modal
in the same shape as the help screen, so it takes `m.width` rather than the
narrower main pane. It shows the changes in the focused terminal's directory,
or the selected project's when focus is in the sidebar.

`git.Changes` unions `git status` with `git diff HEAD --numstat`, because every
form of `git diff` ignores untracked files — a file an agent just created is
invisible to `git diff`, `--cached` and `HEAD` alike.

Alignment comes from `git diff HEAD -U0`: with no context lines the `@@`
headers carry the whole mapping, so `internal/diff` parses only those and never
reads the diff body. `diff.Align` walks both complete files and pads whichever
side is shorter, which is why the two columns' line numbers legitimately
diverge. That package imports neither git nor Bubble Tea, so the one real
algorithm here is testable on its own.

Long lines scroll horizontally rather than wrapping: a wrapped line would take
two rows on one side and one on the other, breaking the alignment the view
exists to provide. The view is read on open and on `r`, never on a timer.
```

- [ ] **Step 3: Run the full check**

Run: `export PATH=$HOME/.local/go/bin:$PATH && make check`
Expected: every package `ok`, build succeeds, no gofmt output.

- [ ] **Step 4: Rebuild the binaries the user runs**

```bash
export PATH=$HOME/.local/go/bin:$PATH
go build -o ~/.local/bin/tm ./cmd/tm && go build -o tm ./cmd/tm
```

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "Document the uncommitted changes view"
```

---

## Manual verification

In a scratch workspace only, never a real repository:

1. Edit a tracked file and create a new one. Press `d` — both appear, the new one marked `new file`.
2. Select the edited file. The committed version is on the left, yours on the right, changed lines coloured, unchanged lines shown on both sides.
3. `tab` into the diff, scroll to the bottom of a long file, and confirm the two sides stay in step.
4. Press `→` a few times: both columns move together.
5. Press `n` in a long file with one change far down — it jumps there.
6. Delete a tracked file, reopen: it shows a committed left side and an empty right.
7. Press `d` in a project with no changes — it says so rather than opening an empty split.
8. In a Claude Code pane, press `Esc` — Claude Code interrupts, and tm does not steal it. `alt+esc` returns to the sidebar.
