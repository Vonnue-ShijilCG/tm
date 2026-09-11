# tm

A terminal workspace manager for Linux. Run `tm` in a directory that holds
your Git repositories and get every project in a sidebar, as many terminals
per project as you need, each labelled with its branch, arranged in panes,
and remembered between sessions.

```text
Workspace
├── api      ● Terminal 1 · main
│            ● Terminal 2 · ⎇ feature/login
└── web      ● Terminal 1 · main
```

- Several independent terminals per project, in split panes, with zoom.
- The branch you pick decides where a terminal opens: your checkout, the
  worktree that branch already lives in, or a new one. A branch checked out
  somewhere is reused, never duplicated.
- Any number of terminals in one directory, so a dev server and a prompt can
  share a branch.
- A terminal can remember a command, so reopening a workspace brings back your
  dev server or agent session instead of an empty shell.
- Hide a running terminal and bring it back later.
- Mouse wheel scrolls a terminal's history, and the diff in the changes view.
- Every terminal is drawn as its own card on one dark ground, so a four-way
  split reads as four panes rather than one wall of text.
- See what changed without leaving tm: `d` shows a project's uncommitted
  changes, each file as it was committed beside how it is now, line for line.
- Terminal definitions and layouts survive restarts. Processes do not.
- One small JSON state file per workspace, no daemon.

## Install

Needs Linux, Go (version in `go.mod`) and `git` on `PATH`.

```bash
go install github.com/Vonnue-ShijilCG/tm/cmd/tm@latest
```

Make sure `$(go env GOPATH)/bin` (usually `~/go/bin`) is on your `PATH`.
From a clone, `make build` puts the binary in `bin/` and `make install`
installs it.

## Usage

```bash
cd ~/workspace   # api/, web/, ... are Git repositories
tm
```

- `Enter` on a project brings back every terminal it had, in the layout you
  left them, each running its command again. A project with nothing saved gets
  one fresh shell.
- `n` adds another terminal. Give it a command to run, and a branch: the branch
  decides where the terminal opens. A branch already checked out — in your
  project or in a worktree any tool created — opens a terminal there; a branch
  checked out nowhere gets a worktree, and a new branch also asks what it starts
  from. Leave the branch empty for a shell in the project itself.
- `d` on a project, or `alt+d` inside a terminal, shows the uncommitted
  changes in that directory: the changed files on the left — new, untracked
  ones included — and the selected file shown twice in full, committed
  against current, aligned line for line. The wheel scrolls: over the columns
  it moves both of them together, over the list it picks the file. `j`/`k`
  and the arrows move the file list, `←`/`→` scroll both columns sideways,
  `n`/`N` jump between changes, `r` re-reads from git, `Esc` closes. Long
  lines are cut rather than wrapped so the two sides stay in step.
- `alt+s` returns to the sidebar, `s` shows every shortcut, `q` quits. Plain
  `Esc` goes to the program, so it interrupts Claude Code and leaves Vim's
  insert mode as usual.
- Inside a terminal everything you type reaches the program. Only a few
  `Alt` chords belong to `tm`; press `s` in the sidebar to see them.

## Where things live

| What | Where |
| --- | --- |
| Saved terminals and layouts | `<workspace>/.terminal-state.json` |
| Worktrees created by `tm` | `<workspace>/.terminal-worktrees/<project>/` |
| Configuration | `~/.config/tm/config.toml` (`max_terminals = 6`) |

`tm` only removes worktrees it created and that no terminal still uses. The
project's own checkout is never touched.

The state file records the command each terminal runs, and `tm` runs it when
that terminal starts. Treat it like a `Makefile` or a `.envrc`: only run `tm`
in a workspace whose `.terminal-state.json` you trust, since opening a project
there runs the commands it contains.

## Development

`make check` runs what CI runs: `gofmt`, `go vet`, `go test`, `go test
-race`, `go build`.

Limitations: Linux only. Inside a terminal `tm` reserves its `Alt` chords,
so programs that bind those keys (Emacs `M-x`, readline `M-n`) lose them.
While `tm` runs it owns the mouse, so selecting text in the host terminal
needs `Shift` held.

## License

MIT. See [LICENSE](LICENSE).
