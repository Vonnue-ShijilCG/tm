# Terminal startup commands and worktree branch bases — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A terminal can remember a command that tm re-runs whenever it starts, and a worktree on a new branch can choose what that branch starts from.

**Architecture:** Both features add a field that flows the same route: `state.Terminal` on disk → `workspace.SavedTerminal`/`Options`/`Terminal` in memory → the new-terminal form in the UI. The command is delivered by writing to the existing session rather than by spawning a different process, so `terminal.Spawner` is untouched. `startTerminal` is the single choke point through which every path that starts a process passes, so one change there covers creation, `OpenSaved` and `OpenAll`.

**Tech Stack:** Go (version in `go.mod`), Bubble Tea, Lip Gloss, `charmbracelet/x/vt`, the `git` CLI.

**Specs:**
- `docs/superpowers/specs/2026-09-07-terminal-startup-command-design.md` (Tasks 1–5)
- `docs/superpowers/specs/2026-09-07-worktree-branch-base-design.md` (Tasks 6–8)

## Global Constraints

- The Go toolchain is at `~/.local/go/bin` and is **not** on `PATH`. Every shell must start with `export PATH=$HOME/.local/go/bin:$PATH`.
- `gofmt` is mandatory: CI fails on any unformatted file. Run `gofmt -w .` before every commit.
- `make check` (gofmt, vet, test, test -race, build) must pass before a task is done.
- Tests require `git` on `PATH`. Never point a test at a real repository; build temp repos with `initRepo(t)` (`internal/git/git_test.go`) or `gitWorkspace(t)` (`internal/workspace/worktree_test.go`).
- Each package's `TestMain` sets `XDG_CONFIG_HOME` to an empty temp dir. Do not remove it.
- Commit messages carry **no** `Co-Authored-By` trailer. The user's git identity is the only author.
- `internal/ui/keys.go` is the single source of truth for shortcuts. Do not add plain-letter or Ctrl bindings usable in terminal focus.
- After changing UI behaviour, rebuild both binaries the user runs: `go build -o ~/.local/bin/tm ./cmd/tm && go build -o tm ./cmd/tm`.

---

### Task 1: Persist a command in the state file

**Files:**
- Modify: `internal/state/state.go` (the `Terminal` struct, the `Version` constant)
- Test: `internal/state/state_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `state.Terminal.Command string` (JSON key `command`, omitted when empty); `state.Version == 3`.

- [ ] **Step 1: Write the failing test**

Append to `internal/state/state_test.go`:

```go
func TestTerminalCommandSurvivesARoundTrip(t *testing.T) {
	root := t.TempDir()
	in := File{Version: Version, Projects: []Project{{
		Name:      "api",
		Terminals: []Terminal{{Command: "npm run dev"}, {Name: "shell"}},
	}}}
	if err := Save(root, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := out.Projects[0].Terminals[0].Command; got != "npm run dev" {
		t.Errorf("command = %q, want %q", got, "npm run dev")
	}
	if got := out.Projects[0].Terminals[1].Command; got != "" {
		t.Errorf("a terminal with no command loaded %q, want empty", got)
	}
}

func TestVersion2FileLoadsWithNoCommand(t *testing.T) {
	root := t.TempDir()
	body := `{"version":2,"projects":[{"name":"api","terminals":[{"name":"old"}]}]}`
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := out.Projects[0].Terminals[0].Command; got != "" {
		t.Errorf("command = %q, want empty for a v2 file", got)
	}
}
```

If `os` or `path/filepath` are not already imported in that file, add them.

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/state -run 'Command|Version2' -v`
Expected: FAIL to build — `unknown field Command in struct literal of type Terminal`.

- [ ] **Step 3: Write minimal implementation**

In `internal/state/state.go`, add the field to `Terminal`:

```go
// Terminal is one saved terminal definition. Worktree is relative to the
// workspace root and empty for terminals running in the project checkout.
// Command is run in the terminal's shell each time the terminal is started.
type Terminal struct {
	Name     string `json:"name,omitempty"`
	Command  string `json:"command,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Worktree string `json:"worktree,omitempty"`
}
```

And bump the version constant, keeping its comment truthful:

```go
// Version is written to new files so the format can evolve. Version 3 added
// the optional per-terminal command; versions 1 and 2 load unchanged, since
// a missing command simply means none.
const Version = 3
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/state -v`
Expected: PASS, including the pre-existing tests.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/state
git add internal/state/state.go internal/state/state_test.go
git commit -m "Persist an optional per-terminal command in the state file"
```

---

### Task 2: Run the command when a terminal starts

**Files:**
- Modify: `internal/workspace/workspace.go` (`Options`, `Terminal`, `saved`, `startTerminal`, `CreateTerminal`, `forget` call site at line ~284)
- Modify: `internal/workspace/layout.go` (`Terminal.Key`, `SavedTerminal.key`)
- Modify: `internal/workspace/persist.go` (`SavedTerminal`, `forget`, `OpenSaved` call sites)
- Modify: `internal/workspace/worktree.go` (two `p.forget` call sites)
- Test: `internal/workspace/command_test.go` (create)

**Interfaces:**
- Consumes: `state.Terminal.Command` from Task 1.
- Produces: `workspace.Options.Command`, `workspace.SavedTerminal.Command`, `workspace.Terminal.Command`, all `string`. `startTerminal(p *Project, name, command string, wt *git.Worktree, cols, rows int) (*Terminal, error)`. `(*Project).forget(name, command, worktree string)`.

- [ ] **Step 1: Write the failing test**

Create `internal/workspace/command_test.go`:

```go
package workspace

import "testing"

func TestCreatingATerminalWithACommandRunsIt(t *testing.T) {
	ws, spawned := newTestWorkspace()
	p := ws.Projects[0]

	if _, err := ws.CreateTerminal(p, Options{Command: "npm run dev"}, 80, 24); err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}

	s := (*spawned)[0]
	if len(s.text) != 1 || s.text[0] != "npm run dev\n" {
		t.Errorf("session received %q, want [\"npm run dev\\n\"]", s.text)
	}
}

func TestATerminalWithoutACommandIsSentNothing(t *testing.T) {
	ws, spawned := newTestWorkspace()

	if _, err := ws.NewTerminal(ws.Projects[0], 80, 24); err != nil {
		t.Fatalf("NewTerminal: %v", err)
	}

	if got := (*spawned)[0].text; len(got) != 0 {
		t.Errorf("session received %q, want nothing", got)
	}
}

func TestReopeningASavedDefinitionRunsItsCommandAgain(t *testing.T) {
	ws, spawned := newTestWorkspace()
	p := ws.Projects[0]

	if _, err := ws.OpenSaved(p, SavedTerminal{Command: "go test ./..."}, 80, 24); err != nil {
		t.Fatalf("OpenSaved: %v", err)
	}

	s := (*spawned)[0]
	if len(s.text) != 1 || s.text[0] != "go test ./...\n" {
		t.Errorf("session received %q, want the saved command", s.text)
	}
}

func TestDefinitionsAreDistinguishedByCommand(t *testing.T) {
	p := &Project{}
	p.remember(SavedTerminal{Command: "npm run dev"})
	p.remember(SavedTerminal{Command: "go test ./..."})

	p.forget("", "npm run dev", "")

	if len(p.Saved) != 1 || p.Saved[0].Command != "go test ./..." {
		t.Fatalf("saved = %+v, want only the go test definition", p.Saved)
	}
	if a, b := (SavedTerminal{Command: "a"}).key(), (SavedTerminal{Command: "b"}).key(); a == b {
		t.Error("definitions differing only by command must have different keys")
	}
}
```

The `fakeSession` in `internal/workspace/workspace_test.go` currently discards `SendText`. Change it to record:

```go
func (f *fakeSession) SendText(s string) { f.text = append(f.text, s) }
```

and add `text []string` to the `fakeSession` struct.

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -run 'Command|Definitions' -v`
Expected: FAIL to build — `unknown field Command in struct literal of type Options`.

- [ ] **Step 3: Write minimal implementation**

In `internal/workspace/workspace.go`, add `Command` to `Options` and `Terminal`:

```go
type Options struct {
	Name     string // kept for saved-state compatibility; no longer shown or set by the form
	Command  string // run in the terminal's shell when it starts; empty for a plain shell
	Branch   string // branch to check out when Worktree is set
	Worktree bool   // create (or check out) Branch in its own worktree
}
```

```go
type Terminal struct {
	ID       int           // unique across the workspace; used to route messages
	Num      int           // 1-based, stable for the life of the project; shown in the pane header
	Name     string        // optional label chosen by the user
	Command  string        // run in the shell when this terminal starts; "" for a plain shell
	Worktree *git.Worktree // nil when the terminal runs in the project checkout
	Session  Session
}
```

Carry it into the definition:

```go
func (t *Terminal) saved() SavedTerminal {
	s := SavedTerminal{Name: t.Name, Command: t.Command}
	if t.Worktree != nil {
		s.Branch, s.Worktree = t.Worktree.Branch, t.Worktree.Path
	}
	return s
}
```

Take the command in `startTerminal` and send it after the terminal is registered:

```go
func (ws *Workspace) startTerminal(p *Project, name, command string, wt *git.Worktree, cols, rows int) (*Terminal, error) {
	if err := ws.CanCreateTerminal(p); err != nil {
		return nil, err
	}
	t := &Terminal{Name: name, Command: command, Worktree: wt}
	s, err := ws.spawn(t.Dir(p), cols, rows)
	if err != nil {
		return nil, err
	}
	ws.nextID++
	p.nextNum++
	t.ID, t.Num, t.Session = ws.nextID, p.nextNum, s
	p.Terminals = append(p.Terminals, t)
	p.active = len(p.Terminals) - 1
	p.Notice = ""
	if command != "" { // typed into the shell, so the pane is a usable prompt when it ends
		s.SendText(command + "\n")
	}
	return t, nil
}
```

Update its one caller in `CreateTerminal`:

```go
	return ws.startTerminal(p, opts.Name, opts.Command, wt, cols, rows)
```

In `internal/workspace/persist.go`, add the field to `SavedTerminal` and widen `forget`:

```go
type SavedTerminal struct {
	Name     string
	Command  string
	Branch   string
	Worktree string
}
```

```go
func (p *Project) forget(name, command, worktree string) {
	for i, s := range p.Saved {
		if s.Worktree == worktree && (worktree != "" || (s.Name == name && s.Command == command)) {
			p.Saved = append(p.Saved[:i], p.Saved[i+1:]...)
			return
		}
	}
}
```

In `internal/workspace/layout.go`, include the command in both keys:

```go
// Key identifies a terminal across restarts: its name, its command and its
// worktree path. Unnamed plain terminals with no command share a key and are
// matched in order; a command makes two shells in one checkout distinct.
func (t *Terminal) Key() string {
	worktree := ""
	if t.Worktree != nil {
		worktree = t.Worktree.Path
	}
	return t.Name + "\x00" + t.Command + "\x00" + worktree
}

func (s SavedTerminal) key() string { return s.Name + "\x00" + s.Command + "\x00" + s.Worktree }
```

Update the remaining call sites so the package compiles:

- `internal/workspace/workspace.go` line ~284: `p.forget(def.Name, def.Command, def.Worktree)`
- `internal/workspace/persist.go` line ~150: `p.forget(s.Name, s.Command, "")`
- `internal/workspace/persist.go` line ~182: `p.forget("", "", s.Worktree)`
- `internal/workspace/worktree.go` lines ~194 and ~209: `p.forget("", "", wt.Path)`

In `internal/workspace/persist.go`, both `OpenSaved` calls to `startTerminal` must pass the command:

```go
		t, err := ws.startTerminal(p, s.Name, s.Command, nil, cols, rows)
```

and, in the worktree branch of the same function, the call that passes a `*git.Worktree` becomes:

```go
	return ws.startTerminal(p, s.Name, s.Command, wt, cols, rows)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -v`
Expected: PASS, including every pre-existing workspace test.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/workspace
git add internal/workspace
git commit -m "Run a terminal's command in its shell when the terminal starts"
```

---

### Task 3: Carry the command through save and restore

**Files:**
- Modify: `internal/workspace/persist.go` (`Snapshot`, `Restore`)
- Test: `internal/workspace/persist_test.go`

**Interfaces:**
- Consumes: `state.Terminal.Command` (Task 1), `workspace.SavedTerminal.Command` (Task 2).
- Produces: nothing new; the mapping is internal.

- [ ] **Step 1: Write the failing test**

Append to `internal/workspace/persist_test.go`:

```go
func TestCommandsAreSavedAndRestored(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	if _, err := ws.CreateTerminal(p, Options{Command: "npm run dev"}, 80, 24); err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	if err := ws.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, _ := gitWorkspaceAt(t, ws.Root)
	if err := reloaded.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	saved := reloaded.Projects[0].Saved
	if len(saved) != 1 || saved[0].Command != "npm run dev" {
		t.Fatalf("restored definitions = %+v, want one with the command", saved)
	}
}
```

`gitWorkspaceAt` may not exist. If `gitWorkspace(t)` does not already accept a root, add this helper next to it in `internal/workspace/worktree_test.go`, mirroring how `gitWorkspace` builds its workspace but taking an existing root:

```go
// gitWorkspaceAt builds a second workspace over an existing root, so a save
// from one can be restored by the other.
func gitWorkspaceAt(t *testing.T, root string) (*Workspace, *[]*fakeSession) {
	t.Helper()
	spawned := &[]*fakeSession{}
	projects, err := project.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	ws := New(root, projects, func(dir string, cols, rows int) (Session, error) {
		f := &fakeSession{dir: dir, cols: cols, rows: rows}
		*spawned = append(*spawned, f)
		return f, nil
	})
	return ws, spawned
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -run TestCommandsAreSavedAndRestored -v`
Expected: FAIL with `restored definitions = [...]`, the command empty, because `Snapshot` and `Restore` do not map it.

- [ ] **Step 3: Write minimal implementation**

In `Snapshot`, carry the command for both running terminals and saved definitions:

```go
		for _, t := range p.Terminals {
			st := state.Terminal{Name: t.Name, Command: t.Command}
			if t.Worktree != nil {
				st.Branch, st.Worktree = t.Worktree.Branch, ws.relPath(t.Worktree.Path)
			}
			sp.Terminals = append(sp.Terminals, st)
			keys = append(keys, t.Key())
		}
		for _, s := range p.Saved {
			sp.Terminals = append(sp.Terminals, state.Terminal{Name: s.Name, Command: s.Command, Branch: s.Branch, Worktree: ws.relPath(s.Worktree)})
			keys = append(keys, s.key())
		}
```

In `Restore`, read it back:

```go
			saved := SavedTerminal{Name: st.Name, Command: st.Command, Branch: st.Branch}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/workspace
git add internal/workspace
git commit -m "Save and restore a terminal's command"
```

---

### Task 4: Label panes and manager rows by their command

**Files:**
- Modify: `internal/ui/model.go` (`paneTitle`)
- Modify: `internal/ui/manager.go` (row label, around line 255)
- Test: `internal/ui/command_test.go` (create)

**Interfaces:**
- Consumes: `workspace.Terminal.Command`, `workspace.SavedTerminal.Command` (Task 2).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Create `internal/ui/command_test.go`:

```go
package ui

import (
	"strings"
	"testing"
)

func TestAPaneIsLabelledByItsCommand(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()
	term.Command = "npm run dev"

	panes := m.viewPanes(p, term)
	if !strings.Contains(panes, "npm run dev") {
		t.Errorf("pane header should show the command:\n%s", panes)
	}
	if strings.Contains(panes, "Terminal 1") {
		t.Errorf("the command should replace the terminal number:\n%s", panes)
	}
}

func TestAPaneWithoutACommandKeepsItsNumber(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "j", "enter")
	p, term := m.activeTerminal()

	if panes := m.viewPanes(p, term); !strings.Contains(panes, "Terminal 1") {
		t.Errorf("a terminal with no command should still show its number:\n%s", panes)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run 'LabelledByItsCommand|KeepsItsNumber' -v`
Expected: `TestAPaneIsLabelledByItsCommand` FAILs with "pane header should show the command"; the second test passes already.

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/model.go`, teach `paneTitle` about the command:

```go
func (m Model) paneTitle(p *workspace.Project, t *workspace.Terminal) string {
	label := fmt.Sprintf("Terminal %d", t.Num)
	if t.Command != "" { // a command is more use than a number for telling panes apart
		label = t.Command
	}
	return identity(label, m.branches.get(t.Dir(p)), t.Worktree != nil)
}
```

In `internal/ui/manager.go`, replace the label assignment inside the row builder so both saved and running rows prefer the command:

```go
	label, program, state := "Terminal", "", "saved"
	if cmd := r.terminal.Definition.Command; cmd != "" {
		label = cmd
	}
	if t := r.terminal.Terminal; t != nil {
		if t.Command == "" {
			label = fmt.Sprintf("Terminal %d", t.Num)
		}
		program, state = programOf(t), "hidden"
		if r.visible {
			mark, state = "●", "visible"
		}
	} else {
		switch {
		case r.terminal.Missing:
			state = "saved · worktree missing"
		case r.terminal.Stale:
			state = "saved · worktree not in git"
		}
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -v`
Expected: PASS. If a pre-existing test asserts on `"Terminal 1"` for a terminal that now has a command, that test's terminal has no command and is unaffected; investigate any failure rather than editing the assertion away.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui
git commit -m "Label panes and manager rows by their command"
```

---

### Task 5: Type a command in the new-terminal form

**Files:**
- Modify: `internal/ui/form.go` (field set, navigation, submit, view)
- Modify: `internal/ui/fixtures_test.go` (`makeTerminal` must skip the new field)
- Test: `internal/ui/command_test.go` (extend)

**Interfaces:**
- Consumes: `workspace.Options.Command` (Task 2).
- Produces: `newTermForm.command textinput.Model`; `fieldCommand` constant; `(*newTermForm).fields() []int`; `(*newTermForm).moveField(delta int)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/command_test.go`:

```go
func TestFormCreatesATerminalWithTheTypedCommand(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter", "esc")

	m = press(m, "n")           // open the form, focus on Branch
	m = press(m, "tab")         // → Command
	m = typeText(m, "npm run dev")
	m = press(m, "tab", " ")    // → Worktree, turn it off
	m = press(m, "enter")

	if m.form != nil {
		t.Fatalf("the form should have closed: %s", m.notice)
	}
	p := m.ws.Projects[1]
	last := p.Terminals[len(p.Terminals)-1]
	if last.Command != "npm run dev" {
		t.Fatalf("terminal command = %q, want %q", last.Command, "npm run dev")
	}
	s := (*spawned)[len(*spawned)-1]
	if len(s.text) == 0 || s.text[len(s.text)-1] != "npm run dev\n" {
		t.Errorf("shell received %q, want the command", s.text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run TestFormCreatesATerminalWithTheTypedCommand -v`
Expected: FAIL — the typed text lands in the Branch field, so `last.Command` is empty.

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/form.go`, add the input and the field id. Replace the `field` constants and `fieldCount` with an explicit, order-carrying field list, because the Base field added in Task 8 appears only conditionally and modular arithmetic over a fixed count cannot express that:

```go
const (
	fieldBranch = iota
	fieldCommand
	fieldWorktree
)
```

Add to the struct:

```go
	command  textinput.Model
```

Build it in `newForm`:

```go
func newForm(ws *workspace.Workspace, p *workspace.Project, back focus) *newTermForm {
	f := &newTermForm{
		project: p,
		back:    back,
		branch:  newInput("e.g. feature/payment"),
		command: newInput("e.g. npm run dev (optional)"),
	}
	if branches, current, err := ws.Branches(p); err == nil {
		f.canWorktree, f.branches, f.current, f.worktree = true, branches, current, true
	}
	f.setField(f.fields()[0])
	return f
}
```

Replace `fieldCount` with the field list and movement:

```go
// fields returns the navigable fields in display order. A non-Git project has
// no branch to choose; the worktree row is always present, disabled when it
// cannot apply, so the form always has something to land on.
func (f *newTermForm) fields() []int {
	if !f.canWorktree {
		return []int{fieldCommand, fieldWorktree}
	}
	return []int{fieldBranch, fieldCommand, fieldWorktree}
}

// moveField steps through fields() by delta, wrapping.
func (f *newTermForm) moveField(delta int) {
	fs := f.fields()
	cur := 0
	for i, id := range fs {
		if id == f.field {
			cur = i
		}
	}
	f.setField(fs[((cur+delta)%len(fs)+len(fs))%len(fs)])
}

// normalizeField moves focus onto a field that currently exists, in case the
// set changed while the user was typing.
func (f *newTermForm) normalizeField() {
	for _, id := range f.fields() {
		if id == f.field {
			return
		}
	}
	f.setField(f.fields()[0])
}
```

Route keys, replacing the body of `update`:

```go
func (f *newTermForm) update(msg tea.KeyMsg) (opts *workspace.Options, cancelled bool) {
	f.normalizeField()
	switch {
	case key.Matches(msg, keys.FormCancel):
		return nil, true
	case key.Matches(msg, keys.FormSubmit):
		return f.submit()
	case key.Matches(msg, keys.FormNext):
		f.moveField(1)
	case key.Matches(msg, keys.FormPrev):
		f.moveField(-1)
	case f.field == fieldWorktree:
		if key.Matches(msg, keys.FormToggle) && f.canWorktree {
			f.worktree = !f.worktree
		}
	case f.field == fieldBranch && f.canWorktree:
		f.branch, _ = f.branch.Update(msg)
	case f.field == fieldCommand:
		f.command, _ = f.command.Update(msg)
	}
	return nil, false
}
```

Focus the right input:

```go
func (f *newTermForm) setField(i int) {
	f.field = i
	f.branch.Blur()
	f.command.Blur()
	switch i {
	case fieldBranch:
		f.branch.Focus()
	case fieldCommand:
		f.command.Focus()
	}
}
```

Collect it on submit, rejecting a multi-line command:

```go
func (f *newTermForm) submit() (*workspace.Options, bool) {
	opts := &workspace.Options{Command: strings.TrimSpace(f.command.Value())}
	if strings.ContainsAny(opts.Command, "\n\r") {
		f.err = "a command must be a single line"
		f.setField(fieldCommand)
		return nil, false
	}
	if f.canWorktree && f.worktree {
		opts.Worktree = true
		opts.Branch = strings.TrimSpace(f.branch.Value())
		if opts.Branch == "" {
			f.err = "a branch is required to create a worktree"
			f.setField(fieldBranch)
			return nil, false
		}
	}
	return opts, false
}
```

Render the row. In `view`, add it inside the `canWorktree` branch after the Branch row, and inside the `else` branch before the Worktree row, so it appears for every project:

```go
		b.WriteString(f.row(fieldCommand, "Command", f.command.View()))
```

- [ ] **Step 4: Fix the test fixture, then run the whole UI suite**

`makeTerminal` in `internal/ui/fixtures_test.go` presses `tab` once to reach Worktree. There is now one more field between them. Update its no-branch path:

```go
	if branch == "" {
		m = press(m, "tab", "tab", " ", "enter") // skip Command, worktree off (a no-op on non-git projects)
	} else {
		m = typeText(m, branch)
		m = press(m, "enter")
	}
```

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -v`
Expected: PASS, including every pre-existing UI test.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui
git commit -m "Add a command field to the new-terminal form"
```

---

### Task 6: Let git create a branch from a chosen base

**Files:**
- Modify: `internal/git/git.go` (`AddWorktreeNewBranch`, new `DefaultBranch`, new `HasRef`)
- Modify: `internal/workspace/worktree.go` (the one `AddWorktreeNewBranch` call site)
- Test: `internal/git/git_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `(*Repo).AddWorktreeNewBranch(path, branch, base string) error`; `(*Repo).DefaultBranch() string`; `(*Repo).HasRef(ref string) bool`.

- [ ] **Step 1: Write the failing test**

Append to `internal/git/git_test.go`:

```go
func TestWorktreeNewBranchStartsFromTheGivenBase(t *testing.T) {
	r := initRepo(t)
	if err := r.AddWorktreeNewBranch(filepath.Join(t.TempDir(), "wt"), "feature/login", ""); err != nil {
		t.Fatalf("create feature/login: %v", err)
	}
	tip, err := run(r.Dir, "rev-parse", "feature/login")
	if err != nil {
		t.Fatal(err)
	}
	head, err := run(r.Dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if tip != head {
		t.Fatalf("an empty base should branch from HEAD: %s vs %s", tip, head)
	}

	// A commit on feature/login moves it ahead of HEAD; a branch based on it
	// must start there, not at HEAD.
	if _, err := run(r.Dir, "commit", "--allow-empty", "-m", "work", "--", ); err != nil {
		_ = err // committing on a checked-out branch happens in the worktree below
	}

	stacked := filepath.Join(t.TempDir(), "stacked")
	if err := r.AddWorktreeNewBranch(stacked, "feature/login-fix", "feature/login"); err != nil {
		t.Fatalf("create feature/login-fix: %v", err)
	}
	fix, err := run(r.Dir, "rev-parse", "feature/login-fix")
	if err != nil {
		t.Fatal(err)
	}
	base, err := run(r.Dir, "rev-parse", "feature/login")
	if err != nil {
		t.Fatal(err)
	}
	if fix != base {
		t.Errorf("feature/login-fix is at %s, want the tip of feature/login %s", fix, base)
	}
}

func TestDefaultBranchPrefersMain(t *testing.T) {
	r := initRepo(t)
	current, err := r.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(r.Dir, "branch", "main"); err != nil && current != "main" {
		t.Fatal(err)
	}
	if got := r.DefaultBranch(); got != "main" {
		t.Errorf("DefaultBranch() = %q, want main", got)
	}
}

func TestHasRefAcceptsBranchesAndRejectsTypos(t *testing.T) {
	r := initRepo(t)
	current, err := r.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if !r.HasRef(current) {
		t.Errorf("HasRef(%q) = false, want true", current)
	}
	if r.HasRef("no/such/branch") {
		t.Error("HasRef on a missing ref = true, want false")
	}
}
```

If `path/filepath` is not imported in `git_test.go`, add it. Remove the stray empty-commit block if `initRepo` already leaves a usable HEAD — the assertions that matter are the two `rev-parse` comparisons.

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/git -run 'Base|DefaultBranch|HasRef' -v`
Expected: FAIL to build — `too many arguments in call to r.AddWorktreeNewBranch`, `r.DefaultBranch undefined`, `r.HasRef undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/git/git.go`:

```go
// AddWorktreeNewBranch creates branch at base and checks it out into a new
// worktree at path. An empty base means the repository's current HEAD.
func (r *Repo) AddWorktreeNewBranch(path, branch, base string) error {
	args := []string{"worktree", "add", "-b", branch, path}
	if base != "" {
		args = append(args, base)
	}
	_, err := run(r.Dir, args...)
	return err
}

// DefaultBranch is the branch new work should start from: main, else master,
// else whatever the repository currently has checked out.
func (r *Repo) DefaultBranch() string {
	for _, name := range []string{"main", "master"} {
		if r.HasBranch(name) {
			return name
		}
	}
	branch, err := r.CurrentBranch()
	if err != nil {
		return ""
	}
	return branch
}

// HasRef reports whether ref resolves in this repository. Unlike HasBranch it
// also accepts remote branches such as origin/main, tags and commit ids.
func (r *Repo) HasRef(ref string) bool {
	_, err := run(r.Dir, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}
```

Update the single call site in `internal/workspace/worktree.go` so the package still builds; it passes no base for now, preserving today's behaviour until Task 7:

```go
		err = repo.AddWorktreeNewBranch(path, opts.Branch, "")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/git ./internal/workspace -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/git internal/workspace
git add internal/git internal/workspace
git commit -m "Let a new worktree branch start from a chosen base"
```

---

### Task 7: Require and validate a base for new branches

**Files:**
- Modify: `internal/workspace/workspace.go` (`Options`)
- Modify: `internal/workspace/worktree.go` (`addWorktree`), plus a new `DefaultBranch` accessor
- Test: `internal/workspace/worktree_test.go`

**Interfaces:**
- Consumes: `(*Repo).HasRef`, `(*Repo).DefaultBranch`, `AddWorktreeNewBranch(path, branch, base)` (Task 6).
- Produces: `workspace.Options.Base string`; `(*Workspace).DefaultBranch(p *Project) string`.

- [ ] **Step 1: Write the failing test**

Append to `internal/workspace/worktree_test.go`:

```go
func TestANewBranchWithAnUnknownBaseIsRefused(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	p := ws.Projects[0]

	_, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/x", Base: "no/such/branch"}, 80, 24)

	if err == nil {
		t.Fatal("creating a branch from a missing base should fail")
	}
	if !strings.Contains(err.Error(), "no/such/branch") {
		t.Errorf("error = %v, want it to name the missing base", err)
	}
	if len(p.Terminals) != 0 || len(*spawned) != 0 {
		t.Error("a refused base must leave no terminal behind")
	}
	dir := filepath.Join(ws.Root, WorktreesDir, p.Name, "feature-x")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a refused base must leave no worktree directory: %s", dir)
	}
}

func TestANewBranchStartsFromItsBase(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	if _, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/one", Base: ws.DefaultBranch(p)}, 80, 24); err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	if _, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/two", Base: "feature/one"}, 80, 24); err != nil {
		t.Fatalf("stacked branch: %v", err)
	}
}

func TestAnExistingBranchIgnoresTheBase(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	if _, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/first", Base: ws.DefaultBranch(p)}, 80, 24); err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}
	// Delete the terminal so the branch is free, then reopen it by name with a
	// base that does not exist: an existing branch needs no base at all.
	ws.DeleteTerminal(p, p.Terminals[0].Definition(), p.Terminals[0])
	if _, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/first", Base: "no/such/branch"}, 80, 24); err != nil {
		t.Fatalf("an existing branch should ignore the base: %v", err)
	}
}
```

Add `os`, `strings` and `path/filepath` to that file's imports if absent.

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -run 'Base' -v`
Expected: FAIL to build — `unknown field Base in struct literal of type Options`.

- [ ] **Step 3: Write minimal implementation**

Add the field to `Options` in `internal/workspace/workspace.go`:

```go
type Options struct {
	Name     string // kept for saved-state compatibility; no longer shown or set by the form
	Command  string // run in the terminal's shell when it starts; empty for a plain shell
	Branch   string // branch to check out when Worktree is set
	Base     string // what a new Branch starts from; ignored when Branch exists
	Worktree bool   // create (or check out) Branch in its own worktree
}
```

In `internal/workspace/worktree.go`, add the accessor the UI will use:

```go
// DefaultBranch is what a new branch in this project should start from.
// It is "" when the project is not a Git repository.
func (ws *Workspace) DefaultBranch(p *Project) string {
	repo, err := git.Open(p.Path)
	if err != nil {
		return ""
	}
	return repo.DefaultBranch()
}
```

Validate before anything is created, inside `addWorktree`, immediately after the repository is opened and **before** `WorktreePath`/`MkdirAll`, so a refusal leaves no directory:

```go
	newBranch := !repo.HasBranch(opts.Branch)
	if newBranch {
		if opts.Base == "" {
			return nil, errors.New("a base branch is required to create a new branch")
		}
		if !repo.HasRef(opts.Base) {
			return nil, fmt.Errorf("base branch %q does not exist", opts.Base)
		}
	}
```

Then use the already-computed `newBranch` and pass the base:

```go
	if newBranch {
		err = repo.AddWorktreeNewBranch(path, opts.Branch, opts.Base)
	} else {
		err = repo.AddWorktree(path, opts.Branch)
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -v`
Expected: PASS. Pre-existing worktree tests that create a new branch without a `Base` will now fail with "a base branch is required" — that is the intended new contract, so give those tests a `Base: ws.DefaultBranch(p)`.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/workspace
git add internal/workspace
git commit -m "Require an existing base when creating a new worktree branch"
```

---

### Task 8: Choose the base in the new-terminal form

**Files:**
- Modify: `internal/ui/form.go` (Base field, conditional field list, submit, view)
- Test: `internal/ui/base_test.go` (create)

**Interfaces:**
- Consumes: `workspace.Options.Base`, `(*Workspace).DefaultBranch` (Task 7); `fields()`, `moveField`, `normalizeField` (Task 5).
- Produces: `fieldBase` constant; `newTermForm.base textinput.Model`; `(*newTermForm).branchIsNew() bool`.

- [ ] **Step 1: Write the failing test**

Create `internal/ui/base_test.go`:

```go
package ui

import (
	"strings"
	"testing"
)

func TestBaseFieldAppearsOnlyForANewBranch(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "n") // form for a project the fake workspace treats as non-Git
	if m.form == nil {
		t.Fatal("n should open the form")
	}
	f := m.form
	f.canWorktree, f.branches = true, []string{"main", "feature/login"}

	f.branch.SetValue("feature/login") // existing
	if f.branchIsNew() {
		t.Error("an existing branch must not be treated as new")
	}
	if strings.Contains(f.view(80), "From") {
		t.Error("no base is chosen for an existing branch")
	}

	f.branch.SetValue("feature/new") // does not exist
	if !f.branchIsNew() {
		t.Error("an unknown branch must be treated as new")
	}
	if !strings.Contains(f.view(80), "From") {
		t.Error("a new branch must offer a base")
	}
}

func TestFormPassesTheBaseForANewBranch(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "n")
	f := m.form
	f.canWorktree, f.branches, f.worktree = true, []string{"main"}, true
	f.branch.SetValue("feature/new")
	f.base.SetValue("main")

	opts, cancelled := f.submit()
	if cancelled || opts == nil {
		t.Fatalf("submit returned opts=%v cancelled=%v (%s)", opts, cancelled, f.err)
	}
	if opts.Base != "main" {
		t.Errorf("opts.Base = %q, want main", opts.Base)
	}
}

func TestFormRefusesAnEmptyBase(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "n")
	f := m.form
	f.canWorktree, f.branches, f.worktree = true, []string{"main"}, true
	f.branch.SetValue("feature/new")
	f.base.SetValue("")

	opts, _ := f.submit()
	if opts != nil {
		t.Fatal("an empty base must not create a terminal")
	}
	if !strings.Contains(f.err, "base") {
		t.Errorf("error = %q, want it to mention the base", f.err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run 'Base' -v`
Expected: FAIL to build — `f.branchIsNew undefined`, `f.base undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/form.go`, add the constant, keeping display order:

```go
const (
	fieldBranch = iota
	fieldBase
	fieldCommand
	fieldWorktree
)
```

Add the input to the struct:

```go
	base     textinput.Model
```

Build and pre-fill it in `newForm`:

```go
	f := &newTermForm{
		project: p,
		back:    back,
		branch:  newInput("e.g. feature/payment"),
		base:    newInput("e.g. main"),
		command: newInput("e.g. npm run dev (optional)"),
	}
	if branches, current, err := ws.Branches(p); err == nil {
		f.canWorktree, f.branches, f.current, f.worktree = true, branches, current, true
		f.base.SetValue(ws.DefaultBranch(p))
	}
	f.setField(f.fields()[0])
```

Add the predicate and include the field only when it applies:

```go
// branchIsNew reports whether the typed branch does not exist yet, which is
// the only case where a base has to be chosen.
func (f *newTermForm) branchIsNew() bool {
	v := strings.TrimSpace(f.branch.Value())
	if v == "" {
		return false
	}
	for _, br := range f.branches {
		if br == v {
			return false
		}
	}
	return true
}

func (f *newTermForm) fields() []int {
	if !f.canWorktree {
		return []int{fieldCommand, fieldWorktree}
	}
	if f.worktree && f.branchIsNew() {
		return []int{fieldBranch, fieldBase, fieldCommand, fieldWorktree}
	}
	return []int{fieldBranch, fieldCommand, fieldWorktree}
}
```

Route typing and focus to it, extending the two switches from Task 5:

```go
	case f.field == fieldBase:
		f.base, _ = f.base.Update(msg)
```

```go
func (f *newTermForm) setField(i int) {
	f.field = i
	f.branch.Blur()
	f.base.Blur()
	f.command.Blur()
	switch i {
	case fieldBranch:
		f.branch.Focus()
	case fieldBase:
		f.base.Focus()
	case fieldCommand:
		f.command.Focus()
	}
}
```

Require it on submit, inside the `f.worktree` block after the branch check:

```go
		if f.branchIsNew() {
			opts.Base = strings.TrimSpace(f.base.Value())
			if opts.Base == "" {
				f.err = "a base is required for a new branch"
				f.setField(fieldBase)
				return nil, false
			}
		}
```

Render the row in `view`, immediately after the Branch row and only when it applies:

```go
		if f.worktree && f.branchIsNew() {
			b.WriteString(f.row(fieldBase, "From", f.base.View()))
		}
```

- [ ] **Step 4: Run the whole suite**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./... -v 2>&1 | tail -30`
Expected: PASS. `makeTerminal` creates worktree terminals by typing a branch and pressing enter; with a new branch the Base field is now in play but pre-filled, so submission still succeeds. If a UI test that creates a *new* branch fails with "a base is required", the fake workspace has no Git repository behind it and therefore no default branch — set `m.form.base.SetValue("main")` in that test rather than weakening the check.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui
git commit -m "Choose what a new worktree branch starts from"
```

---

### Task 9: Document both features and ship

**Files:**
- Modify: `README.md` (the feature list and the Usage section)
- Modify: `CLAUDE.md` (Architecture, Terminal identity)
- Test: none; this task is documentation and release mechanics.

**Interfaces:**
- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: Update the README**

In the bullet list, after the worktree bullet, add:

```markdown
- A terminal can remember a command, so reopening a workspace brings back your
  dev server or agent session instead of an empty shell.
```

In the Usage list, replace the `n` bullet with:

```markdown
- `n` adds another terminal. You can give it a command to run, and for a Git
  project a branch with its own worktree; a new branch also asks what it starts
  from, defaulting to `main`.
```

- [ ] **Step 2: Update CLAUDE.md**

In the Architecture section, extend the `workspace` bullet:

```markdown
- **`workspace`**: the runtime tree `Workspace → Project → Terminal`. Owns terminal creation (with the per-project limit), worktree creation/removal, hidden/saved bookkeeping and the state snapshot. A terminal's `Command` is written into its shell by `startTerminal`, the single choke point every start path goes through, so `terminal.Spawner` never learns about commands. It depends only on the `Session` interface and a `Spawner` func, so tests inject fakes and never open a PTY.
```

In "Terminal identity", replace the `Key()` sentence:

```markdown
A `workspace.Terminal` has three identifiers with different jobs: `ID` (unique across the workspace, routes messages), `Num` (1-based, stable per project, shown in headers when the terminal has no command), and `Key()` (name + command + worktree path, used to match terminals across restarts). Unnamed plain terminals with no command share a key and are matched in order, so they may swap cells after a restore; a command or a worktree makes a terminal keep its exact place.
```

In the `state` bullet, change `Version = 2` to `Version = 3` and mention the command.

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
git commit -m "Document terminal commands and worktree branch bases"
```

---

## Manual verification

After Task 9, confirm the two user journeys by hand in a scratch workspace
(never a real repository):

1. `tm` in a directory of Git repos. Press `n`, type a command, tab to Worktree,
   turn it off, Enter. The pane header shows the command and the command runs.
2. Quit with `q`, start `tm` again, press `Enter` on the project and `a` in the
   manager. The terminal comes back with its command running.
3. Press `n`, type a branch name that does not exist. A `From` field appears
   pre-filled with `main`. Type a nonsense base and press Enter: the form
   refuses and no worktree directory is created under
   `.terminal-worktrees/`.
4. Type an existing branch instead: no `From` field appears, and the worktree
   checks that branch out.
