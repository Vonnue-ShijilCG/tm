# Branch-driven terminal location — Implementation Plan (Phase 1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The branch decides where a terminal opens, a branch already checked out anywhere reuses that location instead of failing, and one directory can hold as many terminals as you want.

**Architecture:** A new `Workspace.Location(p, branch)` resolves a branch name to exactly one destination — the project checkout, an existing worktree, or a new worktree — plus the terminals already there. The form stops asking whether to use a worktree and starts reporting where the terminal will go. Three artificial refusals are removed: `addWorktree`'s "directory already exists", `OpenSaved`'s one-terminal-per-worktree rule, and `remember`'s dedupe by worktree path. The guard that stops tm *removing* a worktree it did not create is untouched.

**Tech Stack:** Go (version in `go.mod`), Bubble Tea, Lip Gloss, the `git` CLI.

**Spec:** `docs/superpowers/specs/2026-09-07-branch-location-and-picker-design.md` (Phase 1 only — the branch picker is Phase 2 and is NOT in this plan)

## Global Constraints

- The Go toolchain is at `~/.local/go/bin` and is **not** on `PATH`. Every shell must start with `export PATH=$HOME/.local/go/bin:$PATH`.
- `gofmt` is mandatory: CI fails on any unformatted file.
- `make check` (gofmt, vet, test, test -race, build) must pass before a task is done.
- Tests require `git` on `PATH`. Never point a test at a real repository; build temp repos with `initRepo(t)` (`internal/git/git_test.go`) or `gitWorkspace(t)` (`internal/workspace/worktree_test.go`).
- Each package's `TestMain` sets `XDG_CONFIG_HOME` to an empty temp dir. Do not remove it.
- Commit messages carry **no** `Co-Authored-By` trailer.
- `internal/ui/keys.go` is the single source of truth for shortcuts.
- **Safety invariant, must survive every task:** tm removes only worktrees under `<root>/.terminal-worktrees/<project>/`. `RemoveWorktree`'s `ownedName` guard is NOT to be touched. Only the guard on *opening* is relaxed.

---

## How it must behave, by case

Every task below serves this table. An implementer should be able to check their work against it.

Setup for all examples: workspace `~/work`, project `app`, whose checkout is on `main`. A tm worktree exists at `~/work/.terminal-worktrees/app/feature-login` on `feature/login`. A foreign worktree exists at `~/work/app/.claude/worktrees/agent-x` on `feature/agent`. Branch `feature/old` exists but is checked out nowhere.

| # | User does | Location resolved | What tm shows | What happens |
| --- | --- | --- | --- | --- |
| 1 | Leaves Branch empty | Project checkout `~/work/app` | `project checkout · on main` plus terminals already there | A shell in the project checkout, no worktree |
| 2 | Types `main` (what the checkout has) | Project checkout `~/work/app` | `project checkout · on main` | Same as case 1 — no worktree is created for a branch the checkout already holds |
| 3 | Types `feature/login` (tm worktree exists) | `~/work/.terminal-worktrees/app/feature-login` | `worktree · .terminal-worktrees/app/feature-login` plus its terminals | A terminal in the existing worktree. No new worktree, no git error |
| 4 | Types `feature/agent` (foreign worktree) | `~/work/app/.claude/worktrees/agent-x` | `worktree · app/.claude/worktrees/agent-x (not created by tm)` and `no tm terminals here — may be in use elsewhere` | A terminal in that worktree. tm still refuses to *delete* it later |
| 5 | Types `feature/old` (exists, checked out nowhere) | New worktree `~/work/.terminal-worktrees/app/feature-old` | `new worktree · .terminal-worktrees/app/feature-old` | tm creates the worktree and checks the branch out into it |
| 6 | Types `feature/new` (does not exist) | New branch, new worktree | `new branch` on the Branch row, the `From` field appears, `new worktree · …/feature-new` | Branch created from the base, worktree created |
| 7 | Types `feature/login` again, with a second terminal | Same existing worktree | Location line lists the terminal already there | A second terminal in that worktree — allowed |
| 8 | Types `feature/login`, command `claude`, and `claude` already runs there | Same existing worktree | `claude is already running here. Press enter again to start another.` | Only on an exact command match in the same directory |
| 9 | Non-Git project | Project directory | `project directory (not a git repository)` | A shell there; no branch or base fields at all |

---

### Task 1: Resolve a branch to its checkout location

**Files:**
- Modify: `internal/git/git.go` (add `WorktreeFor`)
- Test: `internal/git/git_test.go`

**Interfaces:**
- Consumes: the existing `(*Repo).Worktrees() ([]Worktree, error)` and the `Worktree{Path, Branch, IsMain}` type.
- Produces: `(*Repo).WorktreeFor(branch string) (Worktree, bool)` — the checkout holding that branch and true, or the zero value and false when no checkout holds it. `IsMain` on the result distinguishes the project checkout from a linked worktree.

- [ ] **Step 1: Write the failing test**

Append to `internal/git/git_test.go`:

```go
func TestWorktreeForFindsWhereABranchIsCheckedOut(t *testing.T) {
	r := initRepo(t) // repo on main, plus a develop branch
	linked := filepath.Join(t.TempDir(), "linked")
	if err := r.AddWorktree(linked, "develop"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}

	main, ok := r.WorktreeFor("main")
	if !ok {
		t.Fatal(`WorktreeFor("main") = not found, want the project checkout`)
	}
	if !main.IsMain {
		t.Errorf("main is at %q with IsMain=false, want the primary checkout", main.Path)
	}

	dev, ok := r.WorktreeFor("develop")
	if !ok {
		t.Fatal(`WorktreeFor("develop") = not found, want the linked worktree`)
	}
	if dev.IsMain {
		t.Error("develop resolved to the primary checkout, want the linked worktree")
	}
	if dev.Path != linked {
		t.Errorf("develop is at %q, want %q", dev.Path, linked)
	}

	if _, ok := r.WorktreeFor("no/such/branch"); ok {
		t.Error(`WorktreeFor("no/such/branch") = found, want not found`)
	}
}
```

If `path/filepath` is not imported in that file, add it.

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/git -run TestWorktreeFor -v`
Expected: FAIL to build — `r.WorktreeFor undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/git/git.go`:

```go
// WorktreeFor returns the checkout that currently holds branch, and whether
// one does. Git allows a branch in at most one checkout, so the result is
// unique; IsMain distinguishes the project's own checkout from a linked
// worktree. A branch checked out nowhere returns false.
func (r *Repo) WorktreeFor(branch string) (Worktree, bool) {
	if branch == "" {
		return Worktree{}, false
	}
	list, err := r.Worktrees()
	if err != nil {
		return Worktree{}, false
	}
	for _, wt := range list {
		if wt.Branch == branch {
			return wt, true
		}
	}
	return Worktree{}, false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/git -v`
Expected: PASS, including every pre-existing test.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/git
git add internal/git
git commit -m "Resolve which checkout holds a branch"
```

---

### Task 2: Reuse an existing worktree instead of refusing

**Files:**
- Modify: `internal/workspace/worktree.go` (`addWorktree`)
- Test: `internal/workspace/worktree_test.go`

**Interfaces:**
- Consumes: `(*Repo).WorktreeFor(branch string) (Worktree, bool)` from Task 1.
- Produces: `addWorktree` returns an existing `*git.Worktree` when the branch already has one, creating nothing. Its signature is unchanged: `addWorktree(p *Project, opts Options) (*git.Worktree, error)`.

This is case 3, 4 and 5 in the behaviour table.

- [ ] **Step 1: Write the failing test**

Append to `internal/workspace/worktree_test.go`:

```go
func TestABranchWithAWorktreeReusesItRatherThanFailing(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]

	first, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/login", Base: ws.DefaultBranch(p)}, 80, 24)
	if err != nil {
		t.Fatalf("first terminal: %v", err)
	}
	path := first.Worktree.Path

	second, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/login"}, 80, 24)
	if err != nil {
		t.Fatalf("second terminal on the same branch should reuse the worktree, got: %v", err)
	}
	if second.Worktree == nil || second.Worktree.Path != path {
		t.Fatalf("second terminal is at %+v, want the same worktree %q", second.Worktree, path)
	}
	if len(p.Terminals) != 2 {
		t.Fatalf("project has %d terminals, want 2", len(p.Terminals))
	}
}

func TestReusingAWorktreeNeedsNoBase(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	if _, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/x", Base: ws.DefaultBranch(p)}, 80, 24); err != nil {
		t.Fatalf("first: %v", err)
	}
	// The branch now exists and has a worktree, so no base is needed or used.
	if _, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/x", Base: "no/such/base"}, 80, 24); err != nil {
		t.Fatalf("reuse should ignore the base entirely, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -run 'ReusesIt|NeedsNoBase' -v`
Expected: FAIL — the second create returns `worktree directory already exists: …`.

- [ ] **Step 3: Write minimal implementation**

In `internal/workspace/worktree.go`, insert the reuse check into `addWorktree` immediately after the repository is opened and **before** the new-branch/base validation, because a branch that already has a worktree needs no base at all:

```go
	if wt, ok := repo.WorktreeFor(opts.Branch); ok {
		// Git allows a branch in only one checkout, so the answer is never to
		// create a second: the terminal belongs in the one that exists. This
		// includes worktrees tm did not create; removal is still restricted to
		// tm's own directory by RemoveWorktree.
		return &git.Worktree{Path: wt.Path, Branch: opts.Branch}, nil
	}
```

Leave the rest of `addWorktree` exactly as it is, including the `os.Stat` guard — it now only fires when a directory exists that git does not know about, which is a genuine problem worth refusing.

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -v`
Expected: PASS. Pre-existing tests that assert a duplicate-branch creation fails must now be re-read: if one asserts `worktree directory already exists` for a branch git already has checked out, that assertion encodes the old contract — rewrite it to assert reuse and say so in your report. Do not weaken assertions about a directory existing that git does not know about.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/workspace
git add internal/workspace
git commit -m "Reuse the worktree a branch already has"
```

---

### Task 3: Allow many terminals in one directory

**Files:**
- Modify: `internal/workspace/persist.go` (`OpenSaved`'s refusal, `remember`'s dedupe)
- Test: `internal/workspace/worktree_test.go`, `internal/workspace/persist_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `OpenSaved` no longer refuses a worktree that already has a running terminal; `remember` no longer drops a second definition for the same worktree path.

This is case 7 in the behaviour table. Note there are THREE blockers and Task 2 removed only the first; both of the remaining two live here, and fixing only one produces a terminal that works until you restart.

- [ ] **Step 1: Write the failing tests**

Append to `internal/workspace/persist_test.go`:

```go
func TestTwoTerminalsInOneWorktreeBothSurviveARestart(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	for _, cmd := range []string{"claude", "npm run dev"} {
		if _, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/pair", Base: ws.DefaultBranch(p), Command: cmd}, 80, 24); err != nil {
			t.Fatalf("create %q: %v", cmd, err)
		}
	}
	if err := ws.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, _ := gitWorkspaceAt(t, ws.Root)
	if err := reloaded.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	saved := reloaded.Projects[0].Saved
	if len(saved) != 2 {
		t.Fatalf("restored %d definitions, want 2: %+v", len(saved), saved)
	}
	got := map[string]bool{}
	for _, s := range saved {
		got[s.Command] = true
	}
	if !got["claude"] || !got["npm run dev"] {
		t.Errorf("restored commands = %+v, want both claude and npm run dev", saved)
	}
}

func TestASecondSavedTerminalInOneWorktreeCanBeOpened(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	first, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/pair", Base: ws.DefaultBranch(p), Command: "claude"}, 80, 24)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	def := SavedTerminal{Command: "npm run dev", Branch: "feature/pair", Worktree: first.Worktree.Path}
	if _, err := ws.OpenSaved(p, def, 80, 24); err != nil {
		t.Fatalf("opening a second terminal in the same worktree: %v", err)
	}
	if len(p.Terminals) != 2 {
		t.Fatalf("project has %d terminals, want 2", len(p.Terminals))
	}
}
```

Also append this test, which covers the second gate — it is the one that catches
the foreign-worktree trap:

```go
func TestATerminalInAForeignWorktreeReopensAfterARestart(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]

	// A worktree created outside tm, the way another tool would make one.
	foreign := filepath.Join(t.TempDir(), "foreign")
	repo, err := git.Open(p.Path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := repo.AddWorktreeNewBranch(foreign, "feature/foreign", ws.DefaultBranch(p)); err != nil {
		t.Fatalf("creating a foreign worktree: %v", err)
	}

	def := SavedTerminal{Command: "claude", Branch: "feature/foreign", Worktree: foreign}
	if _, err := ws.OpenSaved(p, def, 80, 24); err != nil {
		t.Fatalf("opening a terminal in a worktree tm did not create: %v", err)
	}
	if got := p.Terminals[0].Dir(p); got != foreign {
		t.Errorf("terminal runs in %q, want the foreign worktree %q", got, foreign)
	}
}
```

Add `path/filepath` and the `git` package to that file's imports if absent.

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -run 'OneWorktree|ForeignWorktree' -v`
Expected: the OpenSaved test FAILs with `terminal 1 is already running in this worktree`; the restart test FAILs with `restored 1 definitions, want 2`, because `remember` dropped the second.

- [ ] **Step 3: Write minimal implementation**

In `internal/workspace/persist.go`, delete the refusal in `OpenSaved`:

```go
	if t := p.terminalUsing(s.Worktree); t != nil {
		return nil, fmt.Errorf("terminal %d is already running in this worktree", t.Num)
	}
```

**There is a SECOND gate immediately below it, and missing it is the trap in this
task.** `OpenSaved` also rejects any path it cannot find in `ws.Worktrees(p)`,
and that function skips every worktree `ownedName` rejects — so a worktree tm did
not create is never listed, and reopening one dies on `worktree no longer in git`
even though git knows about it perfectly well. A terminal in a Claude Code
worktree would work when created and fail after a restart, which is worse than
refusing up front.

Remove the ownership guard and widen the lookup to every worktree git reports,
not just tm's:

```go
	if ws.ownedName(p, s.Worktree) == "" {
		return nil, fmt.Errorf("refusing to open %s: not under %s", s.Worktree, WorktreesDir)
	}
	worktrees, err := ws.Worktrees(p)
```

becomes:

```go
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
```

The `listed` loop below it then works unchanged, because `git.Worktree` and
`WorktreeInfo` both expose `Path` and `Branch`. Confirm the field names line up
when you make the change; if the loop needs adjusting, adjust the loop, not the
guard.

and simplify `remember` so a worktree no longer limits how many definitions it holds. Definitions are still deduplicated by their full identity, which `Key()` already expresses:

```go
// remember keeps a definition that is not running. Definitions are distinct by
// name, command and worktree, so one worktree may hold several terminals — two
// shells in a directory are no more a conflict than two in the project checkout.
func (p *Project) remember(s SavedTerminal) {
	for _, cur := range p.Saved {
		if cur.key() == s.key() {
			return
		}
	}
	p.Saved = append(p.Saved, s)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -v` then `go test ./...`
Expected: PASS. Several pre-existing tests assert the old one-per-worktree rule — for example that reopening a worktree definition while a terminal uses it is refused. Each such assertion encodes the contract this task deliberately changes: rewrite it to the new behaviour and list every one you touched in your report. Do not delete a test to make it pass.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/workspace
git add internal/workspace
git commit -m "Allow more than one terminal in a worktree"
```

---

### Task 4: Resolve a location for the UI

**Files:**
- Create: `internal/workspace/location.go`
- Test: `internal/workspace/location_test.go`

**Interfaces:**
- Consumes: `(*Repo).WorktreeFor` (Task 1), the existing `(*Workspace).ownedName(p, path) string` and `(*Project).Terminals`.
- Produces:

```go
type LocationKind int

const (
    LocationCheckout LocationKind = iota // the project's own checkout
    LocationWorktree                     // a checkout that already exists
    LocationNewWorktree                  // one tm will create
    LocationPlainDir                     // a project that is not a git repository
)

type Location struct {
    Kind      LocationKind
    Path      string      // where the terminal will run
    Branch    string      // branch at that path, "" for a plain directory
    Foreign   bool        // an existing worktree tm did not create
    NewBranch bool        // the branch does not exist yet, so a base is required
    Terminals []*Terminal // tm's terminals already running at Path
}

// Locator gathers the repository state once; Locate then answers with no
// further git calls, so a caller may resolve on every keystroke.
func (ws *Workspace) Locator(p *Project) LocationSource
func (s LocationSource) Locate(branch string) Location
```

**Why a snapshot rather than `Locate(p, branch)` directly:** the form resolves a
location on every keystroke in the branch field. Reaching for git each time would
mean `git.Open`, `worktree list`, `HasBranch` and `CurrentBranch` per character —
over a hundred subprocesses while typing a 40-character branch name, and a
visible stall on a repository with 180 branches. The existing form already avoids
this by fetching branches once in `newForm`. Gather once, resolve in memory.

This produces every row of the behaviour table. Task 5 only renders it.

- [ ] **Step 1: Write the failing test**

Create `internal/workspace/location_test.go`:

```go
package workspace

import "testing"

func TestLocateAnEmptyBranchIsTheProjectCheckout(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]

	loc := ws.Locator(p).Locate("")

	if loc.Kind != LocationCheckout {
		t.Errorf("Kind = %v, want LocationCheckout", loc.Kind)
	}
	if loc.Path != p.Path {
		t.Errorf("Path = %q, want the project path %q", loc.Path, p.Path)
	}
}

func TestLocateTheCheckoutsOwnBranchIsTheCheckout(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	_, current, err := ws.Branches(p)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}

	loc := ws.Locator(p).Locate(current)

	if loc.Kind != LocationCheckout {
		t.Errorf("Kind = %v, want LocationCheckout for the checked-out branch", loc.Kind)
	}
	if loc.Path != p.Path {
		t.Errorf("Path = %q, want %q", loc.Path, p.Path)
	}
}

func TestLocateAnExistingWorktreeAndItsTerminals(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	term, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/login", Base: ws.DefaultBranch(p), Command: "claude"}, 80, 24)
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}

	loc := ws.Locator(p).Locate("feature/login")

	if loc.Kind != LocationWorktree {
		t.Errorf("Kind = %v, want LocationWorktree", loc.Kind)
	}
	if loc.Path != term.Worktree.Path {
		t.Errorf("Path = %q, want %q", loc.Path, term.Worktree.Path)
	}
	if loc.Foreign {
		t.Error("Foreign = true for a worktree tm created")
	}
	if len(loc.Terminals) != 1 || loc.Terminals[0] != term {
		t.Errorf("Terminals = %v, want the one terminal running there", loc.Terminals)
	}
}

func TestLocateAnUncheckedOutBranchIsANewWorktree(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	branches, _, err := ws.Branches(p)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	var free string
	_, current, _ := ws.Branches(p)
	for _, b := range branches {
		if b != current {
			free = b
			break
		}
	}
	if free == "" {
		t.Skip("fixture has only one branch")
	}

	loc := ws.Locator(p).Locate(free)

	if loc.Kind != LocationNewWorktree {
		t.Errorf("Kind = %v, want LocationNewWorktree for a branch checked out nowhere", loc.Kind)
	}
	if loc.NewBranch {
		t.Error("NewBranch = true for a branch that already exists")
	}
}

func TestLocateAnUnknownBranchNeedsABase(t *testing.T) {
	ws, _ := gitWorkspace(t)

	loc := ws.Locator(ws.Projects[0]).Locate("feature/does-not-exist")

	if loc.Kind != LocationNewWorktree {
		t.Errorf("Kind = %v, want LocationNewWorktree", loc.Kind)
	}
	if !loc.NewBranch {
		t.Error("NewBranch = false for a branch that does not exist, want true")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -run TestLocate -v`
Expected: FAIL to build — `ws.Locator undefined`, `LocationCheckout undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/workspace/location.go`:

```go
package workspace

import (
	"github.com/Vonnue-ShijilCG/tm/internal/git"
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
			return s.withTerminals(Location{Kind: LocationCheckout, Path: wt.Path, Branch: branch})
		}
		return s.withTerminals(Location{
			Kind:    LocationWorktree,
			Path:    wt.Path,
			Branch:  branch,
			Foreign: s.ws.ownedName(s.project, wt.Path) == "",
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

// withTerminals fills in the terminals tm is running at the location's path.
func (s LocationSource) withTerminals(loc Location) Location {
	for _, t := range s.project.Terminals {
		if t.Dir(s.project) == loc.Path {
			loc.Terminals = append(loc.Terminals, t)
		}
	}
	return loc
}
```

Note `Locator` uses `repo.Worktrees()` directly rather than
`ws.Worktrees(p)`, because the latter filters out every worktree tm did not
create — and a foreign worktree is exactly what case 4 must resolve.

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/workspace -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/workspace
git add internal/workspace
git commit -m "Resolve a branch to the one place a terminal can run"
```

---

### Task 5: Replace the worktree toggle with a Location line

**Files:**
- Modify: `internal/ui/form.go` (field set, submit, view)
- Modify: `internal/ui/fixtures_test.go` (`makeTerminal` no longer toggles)
- Test: `internal/ui/location_test.go` (create)

**Interfaces:**
- Consumes: `(*Workspace).Locate(p, branch) Location` and the `Location*` constants from Task 4.
- Produces: `newTermForm.loc Location`, refreshed whenever the branch text changes; `fieldWorktree` is removed.

This renders every row of the behaviour table.

- [ ] **Step 1: Write the failing test**

Create `internal/ui/location_test.go`:

```go
package ui

import (
	"strings"
	"testing"
)

func TestFormReportsTheProjectCheckoutWhenNoBranchIsTyped(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	if m.form == nil {
		t.Fatal("n should open the form")
	}

	view := m.form.view(80)

	if !strings.Contains(view, "project checkout") {
		t.Errorf("an empty branch should report the project checkout:\n%s", view)
	}
	if strings.Contains(view, "Worktree") {
		t.Errorf("the worktree toggle should be gone:\n%s", view)
	}
}

func TestFormReportsANewWorktreeForAnUnknownBranch(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	m = typeText(m, "feature/brand-new")

	view := m.form.view(80)

	if !strings.Contains(view, "new worktree") {
		t.Errorf("an unknown branch should report a new worktree:\n%s", view)
	}
	if !strings.Contains(view, "From") {
		t.Errorf("an unknown branch still needs a base:\n%s", view)
	}
}
```

`gitModel(t)` is the existing Git-backed helper in `internal/ui/form_test.go`. Its signature is `func gitModel(t *testing.T) (Model, *[]*fakeSession, string)` — a model, the spawned fake sessions, and the workspace root — so call it as `m, _, _ := gitModel(t)` when only the model is needed.

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run TestFormReports -v`
Expected: FAIL — the view still renders a `Worktree` row and no location text.

- [ ] **Step 3: Write minimal implementation**

In `internal/ui/form.go`:

Remove `fieldWorktree` from the constants and the `worktree bool` field from the struct. The field list becomes:

```go
const (
	fieldBranch = iota
	fieldBase
	fieldCommand
)

// fields returns the navigable fields in display order. A non-Git project has
// no branch or base to choose, so only the command remains.
func (f *newTermForm) fields() []int {
	if !f.canWorktree {
		return []int{fieldCommand}
	}
	if f.loc.NewBranch {
		return []int{fieldBranch, fieldBase, fieldCommand}
	}
	return []int{fieldBranch, fieldCommand}
}
```

Add the resolved location to the struct and refresh it whenever the branch text changes. In `update`, after the branch field consumes a key:

```go
	case f.field == fieldBranch && f.canWorktree:
		f.branch, _ = f.branch.Update(msg)
		f.loc = f.snap.Locate(strings.TrimSpace(f.branch.Value()))
```

**Do NOT call `ws.Locate` here.** It opens the repository and runs `worktree
list`, `HasBranch` and `CurrentBranch` — three or four git subprocesses. Calling
it per keystroke means well over a hundred subprocesses while typing a branch
name, and the form would visibly stall on a repo with 180 branches. The existing
form already avoids this by fetching branches **once** in `newForm`.

So `Locate` must resolve from data gathered once. Change Task 4's signature to
take a locator, and have the form hold it:

```go
// LocationSource is everything Locate needs, gathered once when the form opens.
type LocationSource struct {
	project   *Project
	isGit     bool
	current   string         // branch the project checkout has
	branches  map[string]bool
	worktrees []git.Worktree // every checkout git reports for this project
	owned     func(string) bool
	terminals func(string) []*Terminal
}

// Locator gathers the repository state a form needs, in one pass. It is NOT
// (*Workspace).Snapshot — that name is already taken by the state-file snapshot
// in persist.go, and Go has no method overloading.
func (ws *Workspace) Locator(p *Project) LocationSource

// Locate resolves a branch against the snapshot with no further git calls.
func (s LocationSource) Locate(branch string) Location
```

`newForm` calls `ws.Locator(p)` once and stores the result as `f.snap` (type
`workspace.LocationSource`), setting `f.loc = f.snap.Locate("")` for the initial
empty-branch state; every
keystroke calls `f.snap.Locate(...)`, which touches no subprocess. The snapshot
goes stale if the repository changes while the form is open, which is acceptable
— the form is short-lived, and creation still validates against live git.

`submit` no longer consults a toggle. The location decides:

```go
func (f *newTermForm) submit() (*workspace.Options, bool) {
	opts := &workspace.Options{Command: strings.TrimSpace(f.command.Value())}
	if strings.ContainsAny(opts.Command, "\n\r") {
		f.err = "a command must be a single line"
		f.setField(fieldCommand)
		return nil, false
	}
	switch f.loc.Kind {
	case workspace.LocationWorktree, workspace.LocationNewWorktree:
		opts.Worktree = true
		opts.Branch = strings.TrimSpace(f.branch.Value())
		if f.loc.NewBranch {
			opts.Base = strings.TrimSpace(f.base.Value())
			if opts.Base == "" {
				f.err = "a base is required for a new branch"
				f.setField(fieldBase)
				return nil, false
			}
		}
	}
	return opts, false
}
```

Render the location where the toggle used to be:

```go
	b.WriteString(f.row(-1, "Location", f.locationLine()))

// locationLine describes where the terminal will run, and what tm already has
// running there. tm sees only its own terminals, so an empty list is reported
// as "no tm terminals" rather than as an idle directory.
func (f *newTermForm) locationLine() string {
	var head string
	switch f.loc.Kind {
	case workspace.LocationPlainDir:
		return "project directory " + hintStyle.Render("(not a git repository)")
	case workspace.LocationCheckout:
		head = "project checkout"
		if f.loc.Branch != "" {
			head += " · on " + f.loc.Branch
		}
	case workspace.LocationWorktree:
		head = "worktree · " + f.loc.Path
		if f.loc.Foreign {
			head += hintStyle.Render("  (not created by tm)")
		}
	case workspace.LocationNewWorktree:
		return "new worktree · " + f.loc.Path
	}
	if len(f.loc.Terminals) == 0 {
		if f.loc.Foreign {
			return head + "\n" + strings.Repeat(" ", 14) + hintStyle.Render("no tm terminals here — may be in use elsewhere")
		}
		return head
	}
	var names []string
	for _, t := range f.loc.Terminals {
		names = append(names, terminalLabel(t))
	}
	return head + "\n" + strings.Repeat(" ", 14) + hintStyle.Render(fmt.Sprintf("%d terminal(s) here: %s", len(names), strings.Join(names, ", ")))
}
```

`f.row` currently highlights when `f.field == field`; passing `-1` means never highlighted, which is right for a read-only line.

- [ ] **Step 4: Update the test fixture and run the whole suite**

`makeTerminal` in `internal/ui/fixtures_test.go` presses `tab` twice then space to turn the worktree off. There is no toggle now: an empty branch already means the project checkout. Update its no-branch path to tab to Command and submit, without the space:

```go
	if branch == "" {
		m = press(m, "tab", "enter") // no branch means the project checkout
	} else {
		m = typeText(m, branch)
		m = press(m, "enter")
	}
```

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -v` then `go test ./...`
Expected: PASS. Many UI tests drive the form through `makeTerminal`, so a wrong key count breaks several at once — fix the fixture, not the individual tests. Tests that assert on the string `Worktree` in the form view are asserting the old contract: rewrite them against the Location line and report each one.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui
git commit -m "Report where a terminal will run instead of asking about worktrees"
```

---

### Task 6: Confirm a duplicate command in the same directory

**Files:**
- Modify: `internal/ui/form.go` (submit), `internal/ui/model.go` (the confirmation)
- Test: `internal/ui/location_test.go`

**Interfaces:**
- Consumes: `Location.Terminals` (Task 4), `terminalLabel(t)` in `internal/ui/model.go`.
- Produces: `newTermForm.confirmDup string` — the command awaiting confirmation, `""` when none.

This is case 8 in the behaviour table. Only an exact command match in the same directory asks; different commands never do.

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/location_test.go`:

```go
func TestADuplicateCommandInTheSameDirectoryAsksFirst(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	m = press(m, "tab") // to Command
	m = typeText(m, "claude")
	m = press(m, "enter")
	if m.form != nil {
		t.Fatalf("the first terminal should have been created: %s", m.notice)
	}

	m = press(m, "esc", "n")
	m = press(m, "tab")
	m = typeText(m, "claude")
	m = press(m, "enter")

	if m.form == nil {
		t.Fatal("a duplicate command should ask before creating a second terminal")
	}
	if !strings.Contains(m.form.view(80), "already running here") {
		t.Errorf("the form should say the command is already running:\n%s", m.form.view(80))
	}

	m = press(m, "enter")
	if m.form != nil {
		t.Fatalf("a second enter should confirm and create: %s", m.notice)
	}
}

func TestADifferentCommandInTheSameDirectoryDoesNotAsk(t *testing.T) {
	m, _, _ := gitModel(t)
	m = press(m, "n")
	m = press(m, "tab")
	m = typeText(m, "claude")
	m = press(m, "enter")

	m = press(m, "esc", "n")
	m = press(m, "tab")
	m = typeText(m, "npm run dev")
	m = press(m, "enter")

	if m.form != nil {
		t.Fatalf("a different command should create without asking: %s", m.form.view(80))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -run Duplicate -v`
Expected: the duplicate test FAILs because the second terminal is created without asking; the different-command test passes already.

- [ ] **Step 3: Write minimal implementation**

Add the pending confirmation to the form struct and check it in `submit`, before returning options:

```go
	if opts.Command != "" && f.confirmDup != opts.Command {
		for _, t := range f.loc.Terminals {
			if t.Command == opts.Command {
				f.confirmDup = opts.Command
				f.err = fmt.Sprintf("%s is already running here. Press enter again to start another.", opts.Command)
				return nil, false
			}
		}
	}
	f.confirmDup = ""
```

Confirmation is **pressing Enter again**, not a `y` key. A form is a text-entry
context: intercepting `y` would stop the user typing `y` as the first character
of anything after a warned submit. Submitting twice reads naturally — "I meant
it" — and needs no new binding.

`submit` already returns without options the first time and records
`f.confirmDup`. The second Enter reaches `submit` again, where the guard above
now passes because `f.confirmDup == opts.Command`, and the terminal is created.

Any other key clears the pending confirmation, since the command may have
changed underneath it:

```go
	if f.confirmDup != "" && !key.Matches(msg, keys.FormSubmit) {
		f.confirmDup, f.err = "", ""
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `export PATH=$HOME/.local/go/bin:$PATH && go test ./internal/ui -v` then `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
export PATH=$HOME/.local/go/bin:$PATH && gofmt -w internal/ui
git add internal/ui
git commit -m "Ask before starting a command already running in the same place"
```

---

### Task 7: Document and ship

**Files:**
- Modify: `README.md`, `CLAUDE.md`
- Test: none; documentation and release mechanics.

- [ ] **Step 1: Update the README**

Replace the `n` bullet in Usage with:

```markdown
- `n` adds another terminal. Give it a command to run, and a branch: the branch
  decides where the terminal opens. A branch already checked out — in your
  project or in a worktree any tool created — opens a terminal there; a branch
  checked out nowhere gets a worktree, and a new branch also asks what it starts
  from. Leave the branch empty for a shell in the project itself.
```

- [ ] **Step 2: Update CLAUDE.md**

In "Worktree safety", append:

```markdown
A branch is checked out in at most one place, so `Workspace.Locate` resolves a
branch to a single destination: the project checkout, an existing worktree, or
one to create. `addWorktree` reuses the worktree a branch already has rather than
creating a second, including worktrees tm did not create. Removal is unchanged
and still refuses anything outside `<root>/.terminal-worktrees/<project>/`.
A directory may hold any number of terminals; two shells in one folder are no
more a conflict than two in the project checkout.
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
git commit -m "Document branch-driven terminal location"
```

---

## Manual verification

In a scratch workspace only, never a real repository, walk the behaviour table:

1. `n`, leave Branch empty — Location says `project checkout · on <branch>`.
2. `n`, type the checkout's own branch — still `project checkout`, no worktree made.
3. `n`, type a branch with an existing tm worktree — Location names that worktree and lists the terminals already there.
4. `n`, type a branch whose worktree another tool made — Location says `(not created by tm)` and `may be in use elsewhere`.
5. `n`, type a branch that exists but is checked out nowhere — `new worktree · …`.
6. `n`, type a name that matches nothing — `new branch`, the `From` field appears.
7. Create two terminals on one branch — both open in the same worktree, both come back after `q` and `Enter`.
8. Create a terminal whose command already runs there — the form asks; a second `Enter` creates it.
