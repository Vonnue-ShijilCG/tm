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
- Optional isolated Git worktree per terminal, so a checkout in one terminal
  never disturbs another.
- Hide a running terminal and bring it back later.
- Mouse wheel scrolls a terminal's history.
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

- `Enter` on a project opens a shell in it.
- `n` adds another terminal. For a Git project you can give it a branch and
  its own worktree.
- `Esc` returns to the sidebar, `s` shows every shortcut, `q` quits.
- Inside a terminal everything you type reaches the program. Only `Esc` and a
  few `Alt` chords belong to `tm`; press `s` in the sidebar to see them.

## Where things live

| What | Where |
| --- | --- |
| Saved terminals and layouts | `<workspace>/.terminal-state.json` |
| Worktrees created by `tm` | `<workspace>/.terminal-worktrees/<project>/` |
| Configuration | `~/.config/tm/config.toml` (`max_terminals = 6`) |

`tm` only removes worktrees it created and that no terminal still uses. The
project's own checkout is never touched.

## Development

`make check` runs what CI runs: `gofmt`, `go vet`, `go test`, `go test
-race`, `go build`.

Limitations: Linux only. Inside a terminal `tm` reserves `Esc` and its
`Alt` chords, so programs that bind those keys (Emacs `M-x`, readline
`M-n`) lose them. While `tm` runs it owns the mouse, so selecting text in
the host terminal needs `Shift` held.

## License

MIT. See [LICENSE](LICENSE).
