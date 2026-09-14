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
- **`terminal`**: `Start(Config)` runs a command in a PTY and mirrors output into a `vt.Emulator`. Exposes `Output()`/`Done()` channels; the UI's per-terminal watcher command turns those into Bubble Tea messages. Output is coalesced twice over: `Session.notify` drops a signal when one is already pending, and `watchTerminal` then collects for `outputWindow` before waking the UI (see Render cadence).
- **`workspace`**: the runtime tree `Workspace → Project → Terminal`. Owns terminal creation (with the per-project limit), worktree creation/removal, hidden/saved bookkeeping and the state snapshot. A terminal's `Command` is written into its shell by `startTerminal`, the single choke point every start path goes through, so `terminal.Spawner` never learns about commands. It depends only on the `Session` interface and a `Spawner` func, so tests inject fakes and never open a PTY.
- **`state`**: the JSON file format (`Version = 3`). Stores only metadata: names, branches, commands, workspace-relative worktree paths, an index-based layout tree. Paths are relative to the
root by design, which is why the form refuses a worktree outside the workspace rather than
accepting one and dropping the terminal on save. Malformed layout nodes decode to empty nodes and are sanitised away rather than failing the load.
- **`config`**: `$XDG_CONFIG_HOME/tm/config.toml` with `max_terminals`. Re-read on every creation attempt (`workspace.LimitSource`), so edits apply without restart; invalid values fall back to the default and carry a `Notice`.
- **`ui`**: the Bubble Tea `Model` (`ui/model.go`), the new-terminal form, the per-project Terminal Manager (`ui/manager.go`), the uncommitted-changes view (`ui/diffview.go`), copy mode (`ui/copymode.go`), and the pane tree.

### Lists longer than the window

Lip Gloss pads a short block up to its height but never cuts a long one, so a
list that ignores the height does not clip — it draws past the bottom of the
window and pushes the status bar off the screen, taking the selected row with
it. A workspace with 24 repositories rendered 30 lines in a 24-row terminal.

`visibleEntries` (`ui/scrolllist.go`) is the one answer, shared by the sidebar,
the Terminal Manager and the changes view's file list. An entry is a group of
lines that belong together — a wrapped project name, a terminal's two rows, an
unused-worktree heading and the row it introduces — and groups are never split
across the edge. Entries are dropped from the top only when the selected one
would otherwise not fit, so the selection is always on screen.

Two rules for anything that draws into a frame:

- **Budget what sits below the list first.** The manager's worktree path,
  confirmation and notice are about the row the cursor is on; a question with
  its answer scrolled off is worse than a shorter list.
- **Truncate every line whose length you do not control.** `manager.fit` cuts
  git's messages to one line and to the pane — `backend is not a git
  repository: terminals here run in the project directory` is 77 cells, and
  wrapped it cost the list its last row. The changes view does the same for
  the same reason.

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

### Render cadence

Bubble Tea calls `Model.View` after **every** message it delivers; the 60 fps
cap belongs to the renderer's write to the terminal, not to `View`. `View`
rebuilds the whole window — measured at ~1.9 ms and ~400 KB for four panes in
a 160x48 window, before the panes' own `Session.Render` (~0.5 ms each at
120x40) — so one message per PTY read is work built and thrown away.

`watchTerminal` waits out `outputWindow` (16 ms, one frame) after
the first signal, collecting whatever else arrives, and wakes the UI once. A
program printing steadily fell from ~350 wakes a second to ~55, at the
renderer's own ceiling. The wake is on the trailing edge, so an isolated
keystroke's echo appears one window later — the renderer's own frame time,
and below what a typist can see. An exit is the exception and never waits:
the pane goes away when it lands, so holding it back would only postpone
saying the terminal is gone.

A frame then repaints only the panes that changed. `renderLeaf` caches each
terminal's finished card in `Model.panes` against a `paneKey` — every input it
reads, including `Session.Version()`, which the session bumps on output and on
resize. With six panes and one of them talking, a frame costs one screen paint
instead of six. Two rules keep it honest: anything new that `renderLeaf`
consults must join `paneKey`, or a stale card is served; and the version is
read *before* the screen, never after, so a write landing between the two
costs one extra repaint next frame rather than filing older content under a
newer version. `dropPane` evicts, so a terminal that exits leaves no card
behind.

What is left in a frame is tm's own chrome: `framed` on the main area and the
`JoinHorizontal` of sidebar and panes together account for most of it, and the
sidebar is usually identical between frames. `diffview.body` shows the
technique that would fix it — assembling rows by hand rather than asking Lip
Gloss to re-pad a whole block.

### Appearance

Every terminal is drawn as its own rounded card on a shared ground
(`terminal.PaneGround`, a hex string both the emulator and Lip Gloss read, so
the two cannot drift). Cards are never separated by divider glyphs, and there
is no frame around the pane area — each card carries its own border, and two
cards meeting draw two lines with nothing between them.

`paneGap` and `paneRowGap` (`ui/styles.go`) are the blank cells between
siblings, side by side and stacked. **They are zero, and they are a pair
because the two cannot be chosen independently.** A character cell is about
twice as tall as it is wide, so the only non-zero pair that looks even on
screen is two columns to one row; 1 and 1 measures the same in cells and
looks lopsided, which is the complaint the pair was introduced to answer.
Zero is the only other even pair. `(*pane).rects` reserves them and
`renderPane` draws them, both from the constants, so the geometry the wheel
is hit-tested against cannot drift from what is painted — and `gap` returns
the empty string rather than a blank block at zero, because Lip Gloss joins
a block of no width as a column of its own and one of no height as a row,
which would put back the space the zero removed. The layout tests read the
constants too: changing the pair is a one-line edit, not a test rewrite.

The card border grows inward from the rect, so pane arithmetic and mouse
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

### Output you cannot see

`Model.unseen` marks terminals that printed while off screen — hidden with
`alt+x`, zoomed out of view, or belonging to a project that is not the one
being shown. `onScreen` is the test, and `renderLeaf` clears the mark as it
draws, so "seen" means "drawn" and needs no key of its own.

It is deliberately not shown on the pane itself: a pane you can see has its
output in front of you, and marking every visible pane would say nothing. The
two places it appears are the two places you look for a terminal that is not
on screen — the sidebar's project marker, which turns amber (`stir`), and the
Terminal Manager's row, which reads `hidden · new output`. The sidebar keeps
the colour rather than adding a glyph because the marker is one cell wide and
a project's row must not change width as its terminals come and go.

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

**git runs off the event loop.** This is the only part of tm that would
otherwise block a key handler: `git status` plus `numstat` measured 16 ms on a
mid-sized repository, and `git show` plus `git diff` 12 ms **on every `j`**.
`readCmd` and `loadCmd` are `tea.Cmd`s; the view opens saying `Reading …` and
fills in when `diffListMsg` and `diffFileMsg` land. `readFile` is a pure
function of a directory and a `git.FileChange` precisely so it can run there.

Two counters, `listSeq` and `fileSeq`, decide whether an answer is still
wanted; they are atomic because the commands run while `Update` writes the
view. A command carries the value it was issued with and its result is
dropped when the counter has moved on — which is what makes a held-down key
safe, and what lets a read outlive the view that asked for it. `loadCmd` also
checks the counter *before* running git, after waiting `diffLoadDelay`
(60 ms), so a key repeat spawns no process for the files it passes over; the
check after git is the one that matters for correctness, since the read a
landed file list asks for has no delay to be superseded during.

A refresh spans both reads, so what it wants restored lives in `keep`: the
selected path, matched again in the new list, and the scroll position that
comes back with it. `keep` is cleared when the file lands, and also when the
new list is empty and no file will land — otherwise a refresh's plan would
move whatever loaded next.

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

### Copy mode

`alt+c` freezes the focused pane and lets a selection be made in it
(`ui/copymode.go`). It exists because panes are drawn side by side in one
terminal buffer: a row of that buffer spans every pane, so the host terminal's
own mouse selection drags in the card borders and the neighbouring pane's text.
The rows here come from the emulator's grid through `Session.Lines`, so a copy
is that pane's text and nothing else.

**Selection is by whole lines.** A line is the unit an error message, a command
and a path all arrive in, and it sidesteps the column arithmetic that wide
runes and grapheme clusters would demand of every cursor move.

**The rows are frozen when the mode opens**, so a program that keeps printing
cannot move text out from under the cursor between choosing a line and copying
it; the pane header says `COPY` so a screen that has stopped is explained.
The pane is drawn straight rather than through the pane cache: its cursor and
selection move on keys `paneKey` knows nothing about.

**The clipboard is written with OSC 52** (`ansi.SetSystemClipboard` to
`clipboard`, a variable so tests can read it). tm asks the terminal to do it
rather than calling a clipboard tool, because that is the only thing that
works over SSH — the escape travels back down the connection and lands in the
clipboard of the machine in front of the user. Terminals may refuse it, many
ship with it disabled, and there is no reply, so tm cannot report failure.

Moving the cursor reuses `keys.Up`/`Down`/`Top`/`Bottom` rather than adding
bindings: the same keys meaning the same thing, and the help screen has no row
to spare for repeating them.

### Key handling policy

`ui/keys.go` is the single source of truth for shortcuts: input matching, the help screen and the status bar all read from it, so adding a binding there is enough. In terminal focus the app reserves **only `Alt` chords**; everything else — letters, Tab, Ctrl+C, arrows and a lone `Esc` — goes to the PTY, which is what lets Esc interrupt Claude Code, leave Vim's insert mode and quit `less`. Do not add plain-letter or Ctrl shortcuts in terminal mode.

`alt+s`, not `alt+esc`, returns to the sidebar: GNOME binds Alt+Esc to "switch windows directly", so the window manager takes it before tm ever sees it. Check what the desktop already claims before binding an Alt chord.

## The first minute

Two things decide whether somebody ever sees any of the above.

**`cmd/tm` explains an empty workspace.** Every other developer tool is run
inside a project, so running tm inside one is what everybody tries first;
`explainNoProjects` says what tm expected instead and, when the directory
above holds repositories, that `cd .. && tm` would have worked. Colour
sequences are only written when stdout is a terminal, so a piped tm does not
put escape bytes in front of its own error.

**The README's pictures are generated.** `tools/screenshot` drives a real tm
in a PTY against a throwaway workspace of invented repositories and branches,
feeds the output through the same `vt.Emulator` the panes use, and writes the
cell grid as SVG; `make screenshots` regenerates both. Three things it has to
get right, each learned by getting it wrong:

- **Drain the emulator.** `emu.Write` of a query blocks until the answer it
  wants to send has been read, so the capture copies `emu` back into the PTY.
  That also means tm's startup questions are answered by a real terminal
  emulator rather than a hand-written table of replies.
- **`COLORTERM=truecolor`**, or Lip Gloss quantises tm's exact hex to 256
  slots and the image is not the colours tm chose.
- **`textLength` on every run**, or the text is laid out at whatever advance
  the reader's monospace font has and drifts off the backgrounds drawn under
  it.

**Releases carry binaries.** `.github/workflows/release.yml` builds static
linux/amd64 and linux/arm64 on a tag push. The asset names carry no version,
which is what lets `/releases/latest/download/tm_linux_amd64.tar.gz` — the
one-line install in the README — resolve.

## Testing conventions

- The `workspace` and `ui` packages have a `TestMain` that sets `XDG_CONFIG_HOME` to an empty temp dir so the developer's own `config.toml` cannot affect the default limit; the packages that never read config have none.
- UI tests drive the real `Model.Update` with helpers in `ui/*_test.go`: `newTestModel(t)` (fake spawner, 100x30 window, real temp root so saves work), `press(m, "j", "enter", "alt+x", ...)`, `typeText`, and `makeTerminal(t, m, key, name, branch)` which goes through the form exactly as a user would. Add new `alt+` keys to the `press` switch when you introduce them.
- `fakeSession` implementations exist in both `workspace` and `ui` tests; `Render` returns `"SCREEN:<dir>:<title>"` so views can be asserted with string matching.
- Git-touching tests build throwaway repos with `initRepo(t)` (`git/git_test.go`) or `gitWorkspace(t)` (`workspace/worktree_test.go`); never point tests at a real repository.
