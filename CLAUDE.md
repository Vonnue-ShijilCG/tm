# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`tm` is a Linux-only, terminal-native workspace manager written in Go (Bubble Tea TUI). Run inside a directory whose direct children are Git repositories; it shows each repo in a sidebar and hosts multiple PTY-backed terminals per project in split panes, each optionally in its own Git worktree. Terminal definitions and layouts persist in `<workspace>/.terminal-state.json`; processes do not. No daemon.

## Commands

```bash
make check                 # what CI runs: gofmt check, go vet, go test, go test -race, go build
make build                 # binary into bin/tm
make fmt                   # gofmt -w .
go test ./...              # all tests
go test -race ./...
go test ./internal/ui -run 'TestManagerHideAndShowReturnPaneToItsSavedPlace'   # one test
go test ./internal/ui -run 'TestManager' -v                # by prefix
```

Tests need `git` on `PATH` (git/workspace/ui worktree and changes tests create temp repos; the `diff` property test skips without it). Requires the Go version in `go.mod`. CI (`.github/workflows/ci.yml`) fails on any unformatted file, so run `gofmt` before committing.

## Architecture

Dependency direction (lower layers know nothing about higher ones):

```
cmd/tm  →  ui  →  workspace  →  { project, git, config, state }
                ↘  terminal (PTY + vt emulator; independent of everything above)
                ↘  diff (hunk headers → aligned rows; imports neither git nor Bubble Tea)
```

- **`project`**: `Discover(root)` — every direct subdirectory with a `.git` entry.
- **`git`**: thin wrapper over the `git` CLI (branches, worktrees, dirty check, and in `git/changes.go` the uncommitted-changes queries `Changes`, `ShowHEAD`, `DiffHEAD`). `git.Head(dir)` reads `.git/HEAD` directly without spawning a process; the UI uses it via a 1s TTL cache (`ui/branches.go`) because rendering happens on every output event.
- **`diff`**: `ParseHunks` and `Align`, pure functions over strings — the one real algorithm behind the changes view, kept free of git and Bubble Tea so it can be tested on its own (see Uncommitted changes view).
- **`terminal`**: `Start(Config)` runs a command in a PTY and mirrors output into a `vt.Emulator`. Exposes `Output()`/`Done()` channels; the UI's per-terminal watcher command turns those into Bubble Tea messages.
- **`workspace`**: the runtime tree `Workspace → Project → Terminal`. Owns terminal creation (with the per-project limit), worktree creation/removal, hidden/saved bookkeeping and the state snapshot. A terminal's `Command` is written into its shell by `startTerminal`, the single choke point every start path goes through, so `terminal.Spawner` never learns about commands. It depends only on the `Session` interface and a `Spawner` func, so tests inject fakes and never open a PTY.
- **`state`**: the JSON file format (`Version = 3`). Stores only metadata: names, branches, commands, workspace-relative worktree paths, an index-based layout tree. Paths are relative to the
root by design, which is why the form refuses a worktree outside the workspace rather than
accepting one and dropping the terminal on save. Malformed layout nodes decode to empty nodes and are sanitised away rather than failing the load.
- **`config`**: `$XDG_CONFIG_HOME/tm/config.toml` with `max_terminals`. Re-read on every creation attempt (`workspace.LimitSource`), so edits apply without restart; invalid values fall back to the default and carry a `Notice`.
- **`ui`**: the Bubble Tea `Model` (`ui/model.go`), the new-terminal form, the per-project Terminal Manager (`ui/manager.go`), the uncommitted-changes view (`ui/diffview.go`), and the pane tree.

### Three layout representations

The same arrangement exists in three shapes; keep conversions in sync when touching any of them:

| Type | Where | Leaf identity | Purpose |
| --- | --- | --- | --- |
| `ui.pane` | `ui/layout.go`, `ui/grid.go` | `*workspace.Terminal` | live binary split tree actually rendered |
| `workspace.Layout` | `workspace/layout.go` | `Terminal.Key()` string | UI-independent saved arrangement on `Project.Layout` |
| `state.Layout` | `state/state.go` | index into `Terminals` | JSON on disk |

`ui/layout_persist.go` converts pane ↔ `workspace.Layout`; `workspace/persist.go` converts `workspace.Layout` ↔ `state.Layout`.

`Workspace.RecordLayout` has deliberate semantics: the saved layout stays authoritative while it still places terminals that are not on screen but could be (hidden running ones, or saved definitions not yet reopened). Only once everything it knows about is visible or gone does the live tree replace it. This is what lets `a` in the manager restore the old arrangement after a restart or after `Alt+x`.

### Terminal identity

A `workspace.Terminal` has three identifiers with different jobs: `ID` (unique across the workspace, routes messages), `Num` (1-based, stable per project, shown in headers when the terminal has no command), and `Key()` (name + command + worktree path, used to match terminals across restarts). Terminals that share a `Key()` are matched positionally on restore, so they may swap cells; this is not just unnamed plain shells — two terminals with the same command in the same worktree (or both in the project checkout) share a key too, since duplicates are never collapsed (see Worktree safety below). A distinct command or worktree makes a terminal keep its exact place. `Name` is legacy: no longer entered in the form, only kept for state compatibility, but tests still set it to address terminals.

### Hidden vs. saved vs. visible

- **Visible**: in `Model.layouts[p]` (the pane tree).
- **Hidden**: running, in `Project.Terminals`, but not in the pane tree (`Alt+x`). `Model.allVisible[p]` remembers the last tree that showed every running terminal so a hidden pane can return to its exact place. Hidden state is not persisted.
- **Saved**: `Project.Saved` — definitions from the state file (or from a closed worktree terminal) with no process.

### Worktree safety

All tm-created worktrees live under `<root>/.terminal-worktrees/<project>/<sanitised-name>`. `WorktreePath` sanitises to a single path segment and verifies it cannot escape that directory. tm only ever removes worktrees inside that directory that no other terminal uses; the project's own checkout is never touched. `Restore` keeps a saved entry whenever either `projectCheckouts` still lists that path as a checkout of this project (tm's or another tool's) or `ownedName` says the path is one tm's own worktree directory could have created; it drops an entry only when neither is true. That second check is what keeps a stale tm worktree — one git has since forgotten — showing up in the Terminal Manager as removable instead of vanishing silently.

A branch is checked out in at most one place, so `LocationSource.Locate` (from `Workspace.Locator`) resolves a branch to a single destination: the project checkout, an existing worktree, or one to create. `addWorktree` reuses the worktree a branch already has rather than creating a second, including worktrees tm did not create. A directory may hold any number of terminals; two shells in one folder are no more a conflict than two in the project checkout.

Creating a worktree on a branch that does not already exist requires `Options.Base`; `addWorktree` validates it before any filesystem work, ahead of `WorktreePath` and `os.MkdirAll`, so a refused base leaves no directory, no branch and no terminal behind. The existing-vs-new decision uses `HasBranch`, which checks local branches only; the base itself is validated with `HasRef`, which also resolves remote branches such as `origin/main`, tags and commit ids. Keep the two distinct: using `HasRef` for the existing-branch check would make a remote-only name look local. An existing branch ignores the base entirely. `Workspace.DefaultBranch(p)` is what the UI pre-fills the form's From field with: `main`, else `master`, else the project checkout's current branch, and `""` for a non-Git project.

### Scrollback

The Bubble Tea program runs with mouse reporting on (`tea.WithMouseCellMotion()`), used only for the wheel. `ui/scroll.go` hit-tests the pointer against `paneScreenRects` (pane rects translated by the sidebar width and main-pane border) and keeps a per-terminal offset in `Model.scroll`. Rendering calls `Session.RenderScrolled` when the offset is non-zero; typing, `applyLayout` (resizes) and `dropPane` clear it. A program that enabled mouse tracking (Claude Code, htop, Vim with mouse) gets the wheel forwarded through `Session.SendWheel` in its own protocol; alt-screen programs without mouse tracking get arrow keys; everything else scrolls tm's own history view. Scroll positions are never persisted.

### Appearance

Every terminal is drawn as its own rounded card on a shared ground
(`terminal.PaneGround`, a hex string both the emulator and Lip Gloss read, so
the two cannot drift). Cards are separated by blank gaps, not divider glyphs,
and there is no frame around the pane area — each card carries its own border.
`(*pane).rects` reserves one cell between split children, which is that gap;
the card border grows inward from the rect, so pane arithmetic and mouse
hit-testing are unaffected by the card. `minPaneCols`/`minPaneRows` are written
as visible arithmetic (`2 + 12`, `2 + 1 + 2`) because the leading 2 is the card
border — a pane smaller than that renders a box with nothing inside it.

A pane's cells come from `Emulator.Draw` onto a `uv.Buffer` the session
reuses (`Session.paint`), not from `Emulator.Render`: `Render` emits nothing
for a cell the program left at its default background, so those cells would
show the host terminal's colour through the card, while `Draw` fills the area
and pads every row to the full width, which is what makes a card a solid
rectangle. `RenderScrolled` composes history and live screen into that same
buffer rather than splicing strings, so scrolled rows carry the ground too.
The emulator's current *and* default background are both `PaneGround`, for
different reasons: the current colour is what `Draw` fills a default cell
with; the default is what a program's own reset returns to (without it a reset
would drop the pane to black) and what answers an OSC 11 query, so a program
that themes itself to its terminal picks colours that suit the ground.

Colour rules, both learned the hard way:

1. **Never use an ANSI-256 index.** An index is a slot number, not a colour;
   the user's terminal theme decides what it paints. tm's borders and hints
   came out maroon on a user's machine for exactly this reason. Every colour in
   `ui/styles.go` is an exact hex value.
2. **Paint every cell tm owns**, including border glyphs (`BorderBackground`)
   and any style that would otherwise set only a foreground — a nested span
   that sets no background ends with a reset that drops the ground around it.
   On top of that, `cmd/tm` sets the terminal's own background with OSC 11 on
   start and restores it with OSC 111 on exit, which is the only thing that
   reaches the emulator's window padding. The two layers are deliberate
   belt-and-braces: tmux filters OSC 11 unless `allow-passthrough` is on.

### Uncommitted changes view

`d` (sidebar) and `alt+d` (terminal focus) open `ui/diffview.go`, a
full-screen modal framed like the help screen (`modalStyle`, sized to
`m.width` rather than the narrower main pane). It shows the changes in the
focused terminal's directory, or the selected project's checkout when opened
from the sidebar, and like the help screen it consumes every key.

**The wheel scrolls the columns; the keys move the file list.** There is no
focus to move between halves and no key that scrolls the diff a row at a
time: `handleMouse` hands every wheel event to `diffWheel` while the view is
open, and the pointer's column decides what moves — over the file list a
notch changes the selected file (one file, not `wheelStep`: an entry is two
or three rows tall and each step re-reads a file from git), over the columns
it scrolls them by `wheelStep`, both at once along the one shared `top`.
Sideways notches move the shared `left`, like `←`/`→`. `j`/`k` and the arrows
move the file list, always; `n`/`N` jump between runs of changed rows, which
a wheel cannot do in a thousand-line file; `r` re-reads; `esc` closes. The
wheel is the one binding a user cannot discover by pressing things, so
`diffHelp` leads with it and `DiffScroll` is a keyless binding carrying its
help text, like `Scroll` for the panes.
Nothing is read on a timer: the list on open and on `r`, a file when it is
selected, so content cannot shift while it is being read. `diffRows` and
`View` must measure the same frame, which is why `modalStyle` has a name.

**The untracked-file trap.** Every form of `git diff` — plain, `--cached`,
`HEAD` — ignores untracked files, so a file an agent just created is invisible
to all of them. `git.Changes` therefore consults `git status --porcelain=v1 -z
--untracked-files=all` as well as `git diff HEAD --numstat -z`;
`--untracked-files=all` is what lists the files inside a new directory
individually instead of as one `dir/` entry. tm counts an untracked file's
lines itself, with git's unterminated-last-line rule, and `load` synthesises
one whole-file hunk for it because `git diff HEAD -U0` yields none.

**Rename detection comes from numstat, not status.** status reports from the
index — a `git mv` is always `R` to it — while diff decides by content
similarity, and the two disagree below git's 50% threshold. The hunks
`DiffHEAD` produces follow numstat's pairing, so taking the pairing from the
same command that produces the hunks is what stops the file list disagreeing
with the alignment: a move status calls a rename but numstat did not pair is
listed as an addition and a deletion. `DiffHEAD` is handed both paths of a
rename because git pairs the two sides only when both are inside the
pathspec; given the destination alone it reports a whole-file addition.

**Alignment.** `git diff HEAD -U0` hunk headers carry the whole mapping, so
`diff.ParseHunks` reads only the `@@` lines and the diff body is never
parsed. A zero count means the start names the line the change sits *after*
(`@@ -3,0 +4 @@` inserts after old line 3), which `Align` honours when it
decides where the unchanged run before a hunk ends. `Align` walks both
complete files and pads whichever side is shorter, so the two columns' line
numbers legitimately diverge; a hunk naming lines beyond both files (the file
changed between the diff and the read) is dropped rather than drawn as
padding for nothing. `internal/diff` imports neither git nor Bubble Tea: that
isolation is what makes the one real algorithm here testable on its own,
including `TestAlignReproducesBothFilesAgainstRealGit`, which asks real git
for the hunks of random edits and checks that each column reads back as its
file.

**Long lines scroll, never wrap.** A wrapped line takes two rows on one side
and one on the other, destroying the alignment the view exists for. `side`
cuts each line with `ansi.TruncateLeft`/`ansi.Truncate` at the shared offset,
and `scrollX` stops at the widest line because past it there is nothing to see.

**File-derived text is stripped of control characters** — code lines
(`plainText`), paths, rename sources, load errors and the line git itself
prints when the listing fails. This is the first tm
view that renders repository bytes straight into the frame; the terminal
panes pass everything through a vt emulator that interprets it first. A lone
`\r` from a CRLF file counts as zero cells to both `ansi.Truncate` and
`lipgloss.Width`, so it slips past every width guard, and drawn it returns
the cursor to column 0 so the right column lands on the file list. Tabs are
expanded to four spaces *before* stripping: Truncate counts a tab as nothing
while the terminal draws it wide, so a line cut with tabs in it shoves the
other column along. `d.title` and `d.dir` are the deliberate exemptions: they
name where tm runs, not what is inside the repository.

### Key handling policy

`ui/keys.go` is the single source of truth for shortcuts: input matching, the help screen and the status bar all read from it, so adding a binding there is enough. In terminal focus the app reserves **only `Alt` chords**; everything else — letters, Tab, Ctrl+C, arrows and a lone `Esc` — goes to the PTY, which is what lets Esc interrupt Claude Code, leave Vim's insert mode and quit `less`. Do not add plain-letter or Ctrl shortcuts in terminal mode.

`alt+s`, not `alt+esc`, returns to the sidebar: GNOME binds Alt+Esc to "switch windows directly", so the window manager takes it before tm ever sees it. Check what the desktop already claims before binding an Alt chord.

## Testing conventions

- The `workspace` and `ui` packages have a `TestMain` that sets `XDG_CONFIG_HOME` to an empty temp dir so the developer's own `config.toml` cannot affect the default limit; the packages that never read config have none.
- UI tests drive the real `Model.Update` with helpers in `ui/*_test.go`: `newTestModel(t)` (fake spawner, 100x30 window, real temp root so saves work), `press(m, "j", "enter", "alt+x", ...)`, `typeText`, and `makeTerminal(t, m, key, name, branch)` which goes through the form exactly as a user would. Add new `alt+` keys to the `press` switch when you introduce them.
- `fakeSession` implementations exist in both `workspace` and `ui` tests; `Render` returns `"SCREEN:<dir>:<title>"` so views can be asserted with string matching.
- Git-touching tests build throwaway repos with `initRepo(t)` (`git/git_test.go`) or `gitWorkspace(t)` (`workspace/worktree_test.go`); never point tests at a real repository.
