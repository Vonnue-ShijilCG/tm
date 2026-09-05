# tm

`tm` is a terminal-native workspace manager for Linux developers who work
across several Git repositories at once. Start it in the directory that holds
your repositories and you get one screen with every project in a sidebar and
as many independent terminals per project as you need, each in its own PTY,
each showing the branch it is on, arranged in split panes, and remembered
between sessions.

```text
Workspace
├── api            ● Terminal 1 · main
│                  ● Terminal 2 · ⎇ feature/login
├── web            ● Terminal 1 · main
└── docs           ○ (saved, not running)
```

## Features

- **Workspace discovery.** Every direct subdirectory that contains a `.git`
  entry becomes a project, listed in a sidebar with Vim-style navigation.
- **Multiple terminals per project.** Each terminal is an independent
  process in its own pseudo-terminal, driven by a terminal emulator, so
  editors, servers and interactive tools behave as they do in any terminal.
- **Terminal-first UI.** The terminal is the object you manage. Its identity
  is `Terminal 1 · main`: a number and the branch it is on.
- **Isolated worktrees when you need them.** A terminal can be given its own
  Git worktree on a branch of your choice, so a checkout there never
  disturbs the other terminals of the same project. Such a terminal shows
  `⎇` before its branch, and that symbol is the only place the worktree
  appears.
- **Branch visibility.** Headers read the checked-out branch straight from
  `.git/HEAD`, so a checkout you type shows up within a second.
- **Hidden background terminals.** Hide a pane without stopping what runs in
  it; bring it back later from the Terminal Manager.
- **Terminal Manager.** One place per project to see visible, hidden and
  saved terminals, show or hide them, restore them, and delete them.
- **Pane layouts.** Automatic balanced grids for one to six terminals, plus
  manual splits, divider resizing and zoom. Layouts are saved.
- **Focus and navigation** between panes with Alt chords; everything else
  you type reaches the program in the terminal.
- **Saved terminal restoration.** Terminal definitions and layouts survive a
  restart; processes do not, and nothing is launched until you ask.
- **Configurable terminal limit** per project.
- **Unused worktree cleanup.** Worktrees no terminal uses any more are listed
  apart and can be removed deliberately, never automatically.
- **Non-Git directories.** A project whose `.git` is not a usable repository
  still gets plain terminals.
- **Lightweight local state.** One small JSON file per workspace holding
  terminal definitions and layouts. No daemon, no database.

## Installation

Requires Linux and Go (the version in `go.mod`). With the Go toolchain on
your `PATH`:

```bash
go install github.com/Vonnue-ShijilCG/tm/cmd/tm@latest
```

This places `tm` in `$(go env GOPATH)/bin`. From a clone, the Makefile offers
the same steps:

```bash
git clone https://github.com/Vonnue-ShijilCG/tm.git
cd tm
make build      # bin/tm
make install    # go install ./cmd/tm
```

`git` must be on your `PATH` for branch names and worktrees. There are no
prebuilt releases yet.

## Usage

Point `tm` at a directory whose direct children are Git repositories:

```bash
mkdir -p ~/workspace
cd ~/workspace        # contains api/, web/, docs/ ...
tm
```

Each repository appears in the sidebar. Press `Enter` on one to open a shell
in it. Press `n` (or `Alt+n` from inside a terminal) for another terminal of
the same project: for a Git project a small dialog asks whether the new
terminal should run in its own worktree on a branch you name; leave that off
for a second shell in the same checkout, turn it on when the terminal should
work on another branch without touching the others, for instance a dev
server on `main` beside a feature branch you are editing. For a directory
that is not a usable Git repository, `Enter` in the dialog simply opens a
shell.

Terminals of a project are laid out automatically as a balanced grid. `Esc`
returns to the sidebar, `m` opens the Terminal Manager, `s` lists every
shortcut, `q` quits and closes all terminals.

## Keyboard shortcuts

Sidebar:

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | move between projects |
| `g` / `G` | first / last project |
| `Enter` | open the project: focus it if it is running, otherwise start one terminal |
| `n` | new terminal for the highlighted project |
| `v` / `h` | new terminal beside / below the focused pane |
| `Tab`, `l`, `→` | focus the project's terminals |
| `m` | Terminal Manager for the highlighted project |
| `z` | zoom the focused terminal (toggle) |
| `s` | help screen with every shortcut |
| `q`, `Ctrl+C` | quit and close all terminals |

Terminal (everything not listed here is forwarded to the program, including
`Tab`, `Shift+Tab`, `Ctrl+C`, `Ctrl+D`, `Ctrl+Z`, `Ctrl+\`, arrows, and Alt
chords `tm` does not use):

| Key | Action |
| --- | --- |
| `Esc` | back to the sidebar |
| `Alt+j` / `Alt+k` | next / previous **visible** terminal |
| `Alt+n` | new terminal, placed automatically |
| `Alt+m` | Terminal Manager for this terminal's project |
| `Alt+x` | **hide** the terminal: the pane goes, the process keeps running |
| `Alt+Delete` | **delete** the terminal permanently (asks first) |
| `Alt+v` / `Alt+h` | new terminal beside / below the focused pane |
| `Alt+←` `Alt+→` `Alt+↑` `Alt+↓` | focus the neighbouring pane |
| `Alt+=` / `Alt+-` | grow / shrink the focused pane |
| `Alt+z` | zoom the focused pane (toggle) |

Three things are worth stating plainly:

- `Alt+x` only hides. The process, the terminal definition and any worktree
  stay exactly as they are; a hidden build or server keeps running.
- `Alt+Delete` is the destructive one. It stops the process, forgets the
  definition, and removes the dedicated worktree the terminal owned (see
  below). The project's own checkout is never touched.
- `Alt+j` / `Alt+k` cycle through visible terminals only. Hidden and saved
  terminals come back through the Terminal Manager. `Tab` is not a terminal
  switcher because it is forwarded to the PTY for completion.

A lone `Esc` is the one key programs in a terminal do not receive; Alt
sequences still reach Vim, `less` and friends.

## Terminal Manager

`m` in the sidebar, or `Alt+m` inside a terminal, opens the manager for that
project (the focused terminal's project, whatever the sidebar cursor points
at). `Esc` returns to where you were.

```text
api
Manage terminals

 › ● Terminal 1 · main
       shell                        visible
   ○ Terminal 2 · ⎇ feature/login
       claude                       hidden
   ● Terminal 3 · ⎇ feature/tests
       vitest                       visible
   ○ Terminal · ⎇ feature/payment
                                    saved

 Unused worktrees (no terminal uses them)
   ○ old-login · feature/old-login  clean
```

Each terminal is one entry: its identity, then the program it runs and its
state. **visible** is in the layout; **hidden** is running but taken out of
the layout with `Alt+x`; **saved** is a definition from an earlier session
whose process is not running (no number until it runs again). A saved
terminal whose worktree is missing or unknown to Git is flagged and never
recreated silently.

| Key | Action |
| --- | --- |
| `j` / `k` | move |
| `Enter` | focus a visible terminal, show a hidden one, or start a saved one |
| `h` | hide the selected visible terminal (same as `Alt+x`) |
| `n` | new terminal |
| `a` | restore every saved terminal that can run, up to the limit, reapplying the saved layout |
| `d` | delete the selected terminal permanently, after confirmation |
| `Esc` | close |

Hidden terminals never reappear on their own; `Enter` here is how they come
back, and a shown terminal returns to the pane it left.

## Git worktrees

```text
Project
├── Terminal 1 · main
├── Terminal 2 · ⎇ feature/login
└── Terminal 3 · ⎇ feature/payment
```

Two terminals in the same checkout share one working directory, so a
`git checkout` in one changes what the other sees. A terminal created with
the worktree option gets its own worktree on the branch you name (created
from `HEAD` if it does not exist yet), under
`<workspace>/.terminal-worktrees/<project>/`. The `⎇` in its header is the
only sign; you keep thinking in terminals.

Deleting a worktree-backed terminal removes the worktree it owned along
with the definition. The confirmation names the worktree and warns when it
has uncommitted changes; cancelling keeps everything. A worktree that
another terminal still uses, or that lies outside `tm`'s worktree
directory, is never removed, and the notice says why. Worktrees left behind
by other means appear under *Unused worktrees* in the manager, where `d`
removes them with the same care.

## Persistence and restart behaviour

`tm` writes `<workspace>/.terminal-state.json`: per project, each terminal's
branch and workspace-relative worktree path, the pane layout, and which
terminal had focus. It is written atomically when terminals are created or
closed, when the layout changes, and on quit. Terminal output, environment
variables and secrets are never stored.

Processes do not survive `tm` exiting, so after a restart every terminal is
a saved definition and nothing is launched automatically. `Enter` on a
project starts exactly one: the terminal that had focus, or the first
restorable one. The others wait in the Terminal Manager, where `Enter`
starts one and `a` starts them all and reapplies the saved layout. A
terminal hidden with `Alt+x` is therefore simply "saved" after a restart.
Worktree-backed terminals keep their identity and their place in the saved
layout; plain terminals in the same checkout are interchangeable and may
swap cells when a layout is restored.

## Configuration

`tm` reads one optional file, never writes it:

```text
$XDG_CONFIG_HOME/tm/config.toml    # or ~/.config/tm/config.toml
```

```toml
# Maximum number of terminals allowed per project.
# Lowering this value does not delete existing terminals.
max_terminals = 6
```

The default is 6. The limit counts running terminals, hidden ones included;
saved definitions do not count. At the limit, creating or restoring another
terminal is refused with a message naming this file; the file is re-read on
every attempt, so an edit takes effect without a restart. Lowering the limit
below the number already running closes nothing. An invalid value falls back
to 6 and is reported in the status bar.

## Development

```bash
make build      # compile bin/tm
make install    # go install ./cmd/tm
make test       # go test ./...
make race       # go test -race ./...
make vet        # go vet ./...
make fmt        # gofmt -w .
make check      # what CI runs: formatting, vet, tests, race tests, build
```

CI runs the same steps as `make check` on every push and pull request with
the Go version from `go.mod`. Tests use temporary directories and temporary
Git repositories and never touch your workspaces or `~/.config/tm`.

## Design

```text
Workspace   the directory tm is started in
└── Project the direct children with a .git entry
    └── Terminal   a PTY-backed shell shown in a pane, identified by number and branch
        └── Worktree (optional)   a dedicated checkout under .terminal-worktrees/
```

The code follows the same shape. `internal/project` discovers repositories.
`internal/workspace` owns projects, terminals, saved definitions, worktree
operations, the terminal limit and the state file's contents. `internal/git`
wraps the `git` command and reads `.git/HEAD`. `internal/terminal` runs a
PTY with an emulated screen. `internal/state` loads and saves the JSON state
file. `internal/config` reads the configuration. `internal/ui` is the Bubble
Tea front end: sidebar, pane layout, Terminal Manager, form and help, with a
single key map that drives input, the status bar and the help screen. State
is a small local file; there is no daemon and no process persistence.

## Requirements and limitations

- Linux. Other Unix-like systems are untested; Windows is not supported.
- A terminal emulator that sends Alt chords as `Esc`-prefixed keys.
- `git` on `PATH`.
- Plain terminals in one checkout share a saved-layout identity and may swap
  places on restore. Worktree terminals are placed exactly.
- Hidden state is not remembered across a restart, since processes end with
  `tm`.

## License

MIT. See [LICENSE](LICENSE).
