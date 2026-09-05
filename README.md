# tm

`tm` is a terminal-native workspace manager for developers: one screen for
all the Git repositories in a directory, with as many independent terminals
per repository as you need.

## Features

- Multiple Git projects in one workspace, listed in a sidebar and navigated
  with Vim-style keys.
- Multiple terminals per project, each an independent process in its own
  PTY with a full terminal emulator behind it.
- Optional Git worktree isolation: a terminal can run in its own checkout
  of the same repository, so branch changes in one terminal never affect
  another.
- Split layouts: automatic balanced grids for one to six terminals, plus
  manual splits, divider resizing, and zoom.
- Hide terminals without stopping them, and bring them back later.
- A Terminal Manager per project for visible, hidden, and saved terminals.
- Persistent terminal definitions and layouts across restarts, without
  storing terminal output or environment.
- A configurable per-project limit on running terminals.
- Keyboard-first workflow; every shortcut is listed on the built-in help
  screen.

## How it works

```text
Workspace
├── Project
│   ├── Terminal 1
│   ├── Terminal 2
│   └── Terminal 3
├── Project
│   └── Terminal 1
└── Project
    └── Terminal 1
```

- **Workspace** is the directory you start `tm` in. Its direct
  subdirectories are scanned for Git repositories.
- **Project** is one of those repositories. It appears in the sidebar and
  owns a set of terminals.
- **Terminal** is the thing you work with: a shell (your `$SHELL`, falling
  back to `/bin/bash`) started in the project directory, shown in a pane
  with a header carrying its number, name, and branch. Terminals are
  created, hidden, shown, restored, and deleted as terminals.
- **Worktree** is an optional property of a terminal. When enabled, the
  terminal runs in its own Git worktree of the repository, on a branch you
  choose, so what you check out there never touches the other terminals of
  the same project. Worktrees live under
  `<workspace>/.terminal-worktrees/<project>/<name>/`.

You create terminals. Worktrees are only something to think about when two
terminals of the same repository need different checkouts at the same
time.

## Installation

With the Go toolchain installed:

```bash
go install github.com/Vonnue-ShijilCG/tm/cmd/tm@latest
```

This places `tm` in `$(go env GOPATH)/bin`; make sure that directory is on
your `PATH`. To build from a clone instead:

```bash
git clone https://github.com/Vonnue-ShijilCG/tm.git
cd tm
go install ./cmd/tm            # or: go build -o bin/tm ./cmd/tm
```

There are no prebuilt releases yet.

## Quick start

Point `tm` at a directory whose direct children are Git repositories:

```text
~/workspace/
├── project-a/
├── project-b/
└── project-c/
```

```bash
cd ~/workspace
tm
```

`tm` discovers the repositories and lists them in the sidebar. Move with
`j`/`k`, press `Enter` on a project to open a shell in it, and type. To add
another terminal to the same project press `n` (or `Alt+n` from inside a
terminal): a small dialog asks for an optional name and, for Git projects,
whether the terminal should run in its own worktree on a given branch.
Leave the worktree off for a plain second shell in the same checkout; turn
it on when that terminal should work on another branch without disturbing
the others, for example a long-running dev server on `main` next to a
feature branch you are editing.

Terminals of a project are arranged automatically as a balanced grid. `Esc`
returns to the sidebar, `m` opens the Terminal Manager for the highlighted
project, `s` shows every shortcut, and `q` quits (closing every terminal).

## Keyboard shortcuts

In the sidebar:

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | move between projects |
| `g` / `G` | first / last project |
| `Enter` | open the project: focus it if it is running, otherwise start one terminal |
| `n` | new terminal for the highlighted project |
| `v` / `h` | new terminal beside / below the focused pane |
| `Tab` (`l`, `→`) | focus the project's terminals |
| `m` | Terminal Manager for the highlighted project |
| `z` | zoom the focused terminal (toggle) |
| `s` | help screen with every shortcut |
| `q`, `Ctrl+C` | quit and close all terminals |

While a terminal has focus, everything you type goes to the program in it,
including `Tab`, `Shift+Tab`, `Ctrl+C`, `Ctrl+D`, `Ctrl+Z`, `Ctrl+\`, and
Alt chords `tm` does not use. `tm` reserves only these:

| Key | Action |
| --- | --- |
| `Esc` | back to the sidebar |
| `Alt+j` / `Alt+k` | next / previous **visible** terminal |
| `Alt+n` | new terminal (placed automatically) |
| `Alt+v` / `Alt+h` | new terminal beside / below the focused pane |
| `Alt+←` `Alt+→` `Alt+↑` `Alt+↓` | focus the neighbouring pane |
| `Alt+=` / `Alt+-` | grow / shrink the focused pane |
| `Alt+z` | zoom the focused pane (toggle) |
| `Alt+x` | **hide** the terminal: its pane goes away, the process keeps running |
| `Alt+Delete` | **delete** the terminal permanently: stops the process and forgets the definition (asks first; a worktree is kept) |

Two points are worth stating plainly. `Alt+x` never stops anything: a
build, a server, or an editor in a hidden terminal keeps running, and `m`
brings the pane back. `Alt+j`/`Alt+k` cycle through visible terminals
only; hidden and saved terminals are reached through the Terminal Manager.

A lone `Esc` is the one key that programs in the terminal do not receive.
Programs that use `Esc` heavily (Vim, `less`) still get `Alt+key`
sequences normally.

## Terminal Manager

`m` lists the highlighted project's terminals, one row each:

```text
Manage terminals · project-a

 › ● 1 shell        visible   project checkout
   ○ 2 Login        hidden    feature/login · worktree
   ● 3 Tests        visible   feature/tests · worktree
   ○   Payment      saved     feature/payment · worktree

 Unused worktrees
   ○ old-login      clean     feature/old-login · worktree
```

- **visible**: running and shown in the layout.
- **hidden**: running but not shown (after `Alt+x`, or because the window
  was too small for its pane).
- **saved**: a definition from an earlier session that is not running.
  Terminals whose worktree is missing or no longer known to Git are
  flagged and never recreated silently.

Keys: `j`/`k` move; `Enter` acts on the selected row only, focusing a
visible terminal, showing a hidden one, or starting a saved one (reusing
its worktree); `h` hides the selected visible terminal, the same as
`Alt+x`; `n` opens the new-terminal dialog; `a` restores every saved
terminal that can run, up to the configured limit, and reapplies the saved
layout; `d` deletes the selected terminal for good after confirmation,
stopping its process and keeping its worktree; `Esc` closes.

Worktrees under `.terminal-worktrees/<project>/` that no terminal or saved
definition uses any more are listed separately under *Unused worktrees*.
They are never terminals and never removed automatically; `d` there
removes one, with a warning if it has uncommitted changes.

## Worktrees

Two terminals in the same normal checkout share one working directory: a
`git checkout` in one changes the files the other sees. When a terminal is
created with the worktree option, it gets its own Git worktree on the
branch you name (created from `HEAD` if the branch does not exist yet), so
each terminal can be on a different branch at the same time.

Worktrees are optional. Hiding, deleting, or losing a terminal never
removes its worktree; restoring a terminal reuses the existing worktree
and never creates a second one. Cleanup is explicit, through the Terminal
Manager's *Unused worktrees* section.

## Configuration

`tm` reads one optional file:

```text
$XDG_CONFIG_HOME/tm/config.toml
```

falling back to

```text
~/.config/tm/config.toml
```

It is never written by `tm`. The one setting is the maximum number of
running terminals per project:

```toml
# Maximum number of terminals allowed per project.
# Lowering this value does not delete existing terminals.
max_terminals = 6
```

The default is 6. The limit counts running terminals, including hidden
ones; saved definitions do not count. At the limit, creating or restoring
another terminal is refused with a message naming this file. The file is
re-read on every attempt, so an edit takes effect without restarting.
Lowering the limit below the number of terminals already running closes
nothing; it only prevents further creation. An invalid value falls back to
6 and is reported in the status bar.

## Requirements

- Linux. `tm` is developed and tested on Linux; it uses Unix
  pseudo-terminals and is not supported on Windows. Other Unix-like
  systems are untested.
- A terminal emulator that sends Alt chords as `Esc`-prefixed keys (most
  do).
- `git` on your `PATH` for repository discovery, branches, and worktrees.
- Go (the version in `go.mod`) to build.

## Building from source

```bash
go build ./...                 # compile every package
go build -o bin/tm ./cmd/tm    # build the CLI binary
./bin/tm                       # run it inside a workspace directory
```

`make build` does the same, and `make install` runs `go install ./cmd/tm`.

## Development

Validate a change with the same steps CI runs:

```bash
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
go build ./...
```

`make check` runs all of them. The tests use temporary directories and
temporary Git repositories; they never touch your real workspaces or
`~/.config/tm`.

## State and data

`tm` remembers terminal *definitions* per workspace in
`<workspace>/.terminal-state.json`: each terminal's name, branch, and
workspace-relative worktree path, the pane layout, and which terminal had
focus. It is written atomically when terminals are created or closed, when
the layout changes, and on quit. Terminal output, environment variables,
command history, and secrets are never stored, and processes do not
survive `tm` exiting.

On the next start nothing is launched automatically. `Enter` on a project
starts one of its saved terminals; the Terminal Manager starts the others
individually or all at once.

## Limitations and known issues

- Hidden terminals are not remembered as hidden across a restart: running
  processes do not survive `tm` exiting, so after a restart every
  terminal is simply a saved definition.
- Terminals without a name share a layout identity across restarts, so two
  unnamed shells may swap places when a saved layout is restored. Named
  terminals are placed exactly.
- Projects that are not Git repositories get plain terminals only; the
  worktree option and the Unused worktrees section do not apply.

## Roadmap

`tm` is early-stage software. Likely future work includes prebuilt
releases and broader installation options, and verifying other Unix-like
platforms. There is no plan to add process persistence such as tmux.

## License

MIT. See [LICENSE](LICENSE).
