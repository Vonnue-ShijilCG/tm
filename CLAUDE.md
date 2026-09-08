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

Tests need `git` on `PATH` (git/workspace/ui worktree tests create temp repos). Requires the Go version in `go.mod`. CI (`.github/workflows/ci.yml`) fails on any unformatted file, so run `gofmt` before committing.

## Architecture

Dependency direction (lower layers know nothing about higher ones):

```
cmd/tm  →  ui  →  workspace  →  { project, git, config, state }
                ↘  terminal (PTY + vt emulator; independent of everything above)
```

- **`project`**: `Discover(root)` — every direct subdirectory with a `.git` entry.
- **`git`**: thin wrapper over the `git` CLI (branches, worktrees, dirty check). `git.Head(dir)` reads `.git/HEAD` directly without spawning a process; the UI uses it via a 1s TTL cache (`ui/branches.go`) because rendering happens on every output event.
- **`terminal`**: `Start(Config)` runs a command in a PTY and mirrors output into a `vt.Emulator`. Exposes `Output()`/`Done()` channels; the UI's per-terminal watcher command turns those into Bubble Tea messages.
- **`workspace`**: the runtime tree `Workspace → Project → Terminal`. Owns terminal creation (with the per-project limit), worktree creation/removal, hidden/saved bookkeeping and the state snapshot. A terminal's `Command` is written into its shell by `startTerminal`, the single choke point every start path goes through, so `terminal.Spawner` never learns about commands. It depends only on the `Session` interface and a `Spawner` func, so tests inject fakes and never open a PTY.
- **`state`**: the JSON file format (`Version = 3`). Stores only metadata: names, branches, commands, workspace-relative worktree paths, an index-based layout tree. Malformed layout nodes decode to empty nodes and are sanitised away rather than failing the load.
- **`config`**: `$XDG_CONFIG_HOME/tm/config.toml` with `max_terminals`. Re-read on every creation attempt (`workspace.LimitSource`), so edits apply without restart; invalid values fall back to the default and carry a `Notice`.
- **`ui`**: the Bubble Tea `Model` (`ui/model.go`), the new-terminal form, the per-project Terminal Manager (`ui/manager.go`), and the pane tree.

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

### Key handling policy

`ui/keys.go` is the single source of truth for shortcuts: input matching, the help screen and the status bar all read from it, so adding a binding there is enough. In terminal focus the app reserves only a lone `Esc` and a small set of `Alt` chords; everything else (letters, Tab, Ctrl+C, arrows) goes to the PTY. Do not add plain-letter or Ctrl shortcuts in terminal mode.

## Testing conventions

- Each package's `TestMain` sets `XDG_CONFIG_HOME` to an empty temp dir so the developer's own `config.toml` cannot affect the default limit.
- UI tests drive the real `Model.Update` with helpers in `ui/*_test.go`: `newTestModel(t)` (fake spawner, 100x30 window, real temp root so saves work), `press(m, "j", "enter", "alt+x", ...)`, `typeText`, and `makeTerminal(t, m, key, name, branch)` which goes through the form exactly as a user would. Add new `alt+` keys to the `press` switch when you introduce them.
- `fakeSession` implementations exist in both `workspace` and `ui` tests; `Render` returns `"SCREEN:<dir>:<title>"` so views can be asserted with string matching.
- Git-touching tests build throwaway repos with `initRepo(t)` (`git/git_test.go`) or `gitWorkspace(t)` (`workspace/worktree_test.go`); never point tests at a real repository.
