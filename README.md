# tm

A terminal-native workspace manager for Linux. Start `tm` in a directory of
Git repositories and get every project in a sidebar, as many terminals per
project as you need, each showing its branch, arranged in panes, and
remembered between sessions.

```text
Workspace
├── api      ● Terminal 1 · main
│            ● Terminal 2 · ⎇ feature/login
└── web      ● Terminal 1 · main
```

- Discovers every direct subdirectory with a `.git` entry as a project.
- Multiple independent PTY-backed terminals per project, in automatic
  balanced grids or manual splits, with zoom.
- A terminal is identified by number and branch: `Terminal 1 · main`. The
  branch is read live from `.git/HEAD`.
- Optional isolated Git worktree per terminal, shown only as `⎇`, so a
  checkout in one terminal never disturbs another.
- Hide a running terminal and bring it back later; a Terminal Manager per
  project lists visible, hidden and saved terminals.
- Terminal definitions and layouts survive restarts; processes do not.
- Configurable per-project terminal limit; safe worktree cleanup; one small
  JSON state file per workspace, no daemon.

## Install

```bash
go install github.com/Vonnue-ShijilCG/tm/cmd/tm@latest
```

Needs Linux, Go (version in `go.mod`) and `git` on `PATH`. From a clone:
`make build` (into `bin/`) or `make install`.

## Usage

```bash
cd ~/workspace   # api/, web/, ... are Git repositories
tm
```

`Enter` on a project opens a shell in it. `n` (or `Alt+n` inside a terminal)
adds another terminal; for a Git project a small dialog asks whether it
should run in its own worktree on a branch you name. Leave that off for a
second shell in the same checkout; turn it on to work on another branch
without touching the others. `Esc` returns to the sidebar, `s` shows every
shortcut, `q` quits and closes all terminals.

## Keys

Sidebar:

| Key | Action |
| --- | --- |
| `j` / `k`, `↓` / `↑` | move between projects |
| `g` / `G` | first / last project |
| `Enter` | open the project: focus it if running, otherwise start one terminal |
| `n`, `v`, `h` | new terminal: automatic, beside, below |
| `Tab`, `l`, `→` | focus the project's terminals |
| `m` | Terminal Manager |
| `z` | zoom the focused terminal |
| `s` | help |
| `q`, `Ctrl+C` | quit |

Terminal (everything else, including `Tab`, `Shift+Tab` and `Ctrl+C`, goes
to the program):

| Key | Action |
| --- | --- |
| `Esc` | back to the sidebar |
| `Alt+j` / `Alt+k` | next / previous **visible** terminal |
| `Alt+n`, `Alt+v`, `Alt+h` | new terminal: automatic, beside, below |
| `Alt+m` | Terminal Manager for this terminal's project |
| `Alt+x` | **hide**: the pane goes, the process keeps running |
| `Alt+Delete` | **delete** permanently (asks first) |
| `Alt+←` `Alt+→` `Alt+↑` `Alt+↓` | focus the neighbouring pane |
| `Alt+=` / `Alt+-` | grow / shrink the pane |
| `Alt+z` | zoom the pane |

`Alt+x` never stops anything. `Alt+Delete` stops the process, forgets the
terminal and removes the worktree it owned; the project's own checkout is
never touched. `Alt+j`/`Alt+k` skip hidden terminals; they come back through
the manager. `Tab` is not a switcher because the shell needs it.

## Terminal Manager

`m` in the sidebar or `Alt+m` in a terminal, scoped to that project; `Esc`
returns to where you were.

```text
api
Manage terminals

 › ● Terminal 1 · main
       shell                        visible
   ○ Terminal 2 · ⎇ feature/login
       claude                       hidden
   ○ Terminal · ⎇ feature/payment
                                    saved

 Unused worktrees (no terminal uses them)
   ○ old-login · feature/old-login  clean
```

**visible** is in the layout, **hidden** is running but taken out with
`Alt+x`, **saved** is a definition whose process is not running. `Enter`
focuses, shows or starts the selected terminal; `h` hides; `n` creates; `a`
restores every saved terminal that can run and reapplies the saved layout;
`d` deletes after confirmation. Hidden terminals never reappear on their
own.

## Worktrees

```text
Project
├── Terminal 1 · main
├── Terminal 2 · ⎇ feature/login
└── Terminal 3 · ⎇ feature/payment
```

A worktree-backed terminal has its own checkout under
`<workspace>/.terminal-worktrees/<project>/` on the branch you named
(created from `HEAD` if new). Deleting the terminal removes that worktree
too, after a confirmation that warns about uncommitted changes. Worktrees
another terminal still uses, or outside that directory, are never removed.
Worktrees left behind by other means appear under *Unused worktrees* for
explicit removal.

## Persistence

`tm` writes `<workspace>/.terminal-state.json` atomically: terminal
definitions, layout and focus per project. No output, environment or
secrets. After a restart nothing runs; `Enter` on a project starts the
terminal that had focus (or the first restorable one), and the manager's
`a` brings the rest back with the saved layout. Worktree terminals keep
their exact place; plain terminals in one checkout are interchangeable and
may swap cells.

## Configuration

`$XDG_CONFIG_HOME/tm/config.toml`, or `~/.config/tm/config.toml`:

```toml
# Maximum number of terminals allowed per project (running, hidden included).
# Lowering this value does not delete existing terminals.
max_terminals = 6
```

Re-read on every attempt, so edits apply without a restart. Invalid values
fall back to 6 with a notice.

## Development

`make check` runs what CI runs: `gofmt`, `go vet`, `go test`, `go test
-race`, `go build`. Also `make build`, `make install`, `make test`, `make
race`, `make vet`, `make fmt`. Tests use temporary repositories only.

Packages: `project` (discovery), `workspace` (projects, terminals,
worktrees, limits, state contents), `git`, `terminal` (PTY and emulator),
`state` (JSON file), `config`, `ui` (Bubble Tea front end).

Limitations: Linux only (other Unix systems untested); Alt chords must reach
the terminal as `Esc`-prefixed keys; hidden state is not remembered across a
restart.

## License

MIT. See [LICENSE](LICENSE).
