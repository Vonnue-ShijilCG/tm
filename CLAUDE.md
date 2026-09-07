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
- **`workspace`**: the runtime tree `Workspace → Project → Terminal`. Owns terminal creation (with the per-project limit), worktree creation/removal, hidden/saved bookkeeping and the state snapshot. It depends only on the `Session` interface and a `Spawner` func, so tests inject fakes and never open a PTY.
- **`state`**: the JSON file format (`Version = 2`). Stores only metadata: names, branches, workspace-relative worktree paths, an index-based layout tree. Malformed layout nodes decode to empty nodes and are sanitised away rather than failing the load.
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

A `workspace.Terminal` has three identifiers with different jobs: `ID` (unique across the workspace, routes messages), `Num` (1-based, stable per project, shown in headers as `Terminal N`), and `Key()` (name + worktree path, used to match terminals across restarts). Unnamed plain terminals share a key and are matched in order, so they may swap cells after a restore; worktree terminals keep their exact place. `Name` is legacy: no longer entered in the form, only kept for state compatibility, but tests still set it to address terminals.

### Hidden vs. saved vs. visible

- **Visible**: in `Model.layouts[p]` (the pane tree).
- **Hidden**: running, in `Project.Terminals`, but not in the pane tree (`Alt+x`). `Model.allVisible[p]` remembers the last tree that showed every running terminal so a hidden pane can return to its exact place. Hidden state is not persisted.
- **Saved**: `Project.Saved` — definitions from the state file (or from a closed worktree terminal) with no process.

### Worktree safety

All tm-created worktrees live under `<root>/.terminal-worktrees/<project>/<sanitised-name>`. `WorktreePath` sanitises to a single path segment and verifies it cannot escape that directory. tm only ever removes worktrees inside that directory that no other terminal uses; the project's own checkout is never touched. `Restore` also drops saved entries whose worktree path is not owned.

### Scrollback

The Bubble Tea program runs with mouse reporting on (`tea.WithMouseCellMotion()`), used only for the wheel. `ui/scroll.go` hit-tests the pointer against `paneScreenRects` (pane rects translated by the sidebar width and main-pane border) and keeps a per-terminal offset in `Model.scroll`. Rendering calls `Session.RenderScrolled` when the offset is non-zero; typing, `applyLayout` (resizes) and `dropPane` clear it. A program that enabled mouse tracking (Claude Code, htop, Vim with mouse) gets the wheel forwarded through `Session.SendWheel` in its own protocol; alt-screen programs without mouse tracking get arrow keys; everything else scrolls tm's own history view. Scroll positions are never persisted.

### Key handling policy

`ui/keys.go` is the single source of truth for shortcuts: input matching, the help screen and the status bar all read from it, so adding a binding there is enough. In terminal focus the app reserves only a lone `Esc` and a small set of `Alt` chords; everything else (letters, Tab, Ctrl+C, arrows) goes to the PTY. Do not add plain-letter or Ctrl shortcuts in terminal mode.

## Testing conventions

- Each package's `TestMain` sets `XDG_CONFIG_HOME` to an empty temp dir so the developer's own `config.toml` cannot affect the default limit.
- UI tests drive the real `Model.Update` with helpers in `ui/*_test.go`: `newTestModel(t)` (fake spawner, 100x30 window, real temp root so saves work), `press(m, "j", "enter", "alt+x", ...)`, `typeText`, and `makeTerminal(t, m, key, name, branch)` which goes through the form exactly as a user would. Add new `alt+` keys to the `press` switch when you introduce them.
- `fakeSession` implementations exist in both `workspace` and `ui` tests; `Render` returns `"SCREEN:<dir>:<title>"` so views can be asserted with string matching.
- Git-touching tests build throwaway repos with `initRepo(t)` (`git/git_test.go`) or `gitWorkspace(t)` (`workspace/worktree_test.go`); never point tests at a real repository.
