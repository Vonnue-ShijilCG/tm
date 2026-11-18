# Background server: terminals that outlive the window

Date: 2026-11-18. Status: design, awaiting approval.

## Why

Today tm is one process: the Bubble Tea UI owns every PTY, so closing the
terminal window (or an SSH drop) kills every shell, dev server, test run and
Claude Code session inside it. The user wants herdr-like behaviour: close the
window, run `tm` again, and find the same terminals still running, showing
what they printed in the meantime. This is for **all** processes, not only
Claude.

Restarting the machine is explicitly *not* in scope to improve: after a
reboot tm must behave exactly as it does today (layout and commands come back
from `.terminal-state.json`, processes start fresh). Nothing can keep a
process alive across a reboot.

## Goals

- Closing the window, losing the SSH connection, or pressing `q` leaves every
  terminal running.
- Running `tm` again in the same workspace folder reconnects to the same
  terminals, in the same layout, including hidden (`alt+x`) ones, with their
  current screens and scrollback.
- Running `tm` in a different workspace folder gets its own, independent
  server.
- Everything tm does today keeps working while attached: scrollback and the
  wheel, mouse forwarding, copy mode, the pane cache, unseen-output marks,
  worktrees, the Terminal Manager, the changes view.

## Non-goals

- Surviving a reboot or a crash of the server process.
- Two windows controlling the same workspace at once.
- Remote attach over SSH (herdr's `--remote`). Running tm on the remote
  machine already works.
- Resuming Claude Code conversations with `--resume`. Worth doing, separate
  change.

## Anti-goals (would make this a failure even if it works)

- A server bug that breaks plain tm. Server mode is opt-in until it has been
  used daily; with it off, tm behaves byte-for-byte as today.
- Typing or output noticeably slower than today.
- Losing a running terminal the user did not ask to kill.
- Leaving orphaned servers running with nothing in them.

## Decisions made with the user

1. **One server per workspace folder.** Same folder → reattach to the same
   server; different folder → a new one.
2. **Second window takes over.** Running `tm` while another window is
   attached to the same workspace attaches the new window and makes the old
   one exit with `tm: opened in another window`.
3. **Keys:**
   - `q` / `ctrl+c` (sidebar) and closing the window: leave; everything keeps
     running.
   - `Q` (shift+q, sidebar): stop everything — kill all terminals, save the
     state file, stop the server. Same as today's `q`.
   - `alt+delete` and the manager's `d`: unchanged, kill one terminal.
   - `alt+x`: unchanged, hide only.
4. **Server stops** on `tm stop` (CLI, from the workspace folder), on `Q`,
   and when its last terminal exits while no window is attached.
5. **Approach A:** tm's own server, each window keeps a copy of the screens
   (see Approaches).
6. **Rollout (my call, user said "do the best"):** opt-in with
   `server = true` in `config.toml`; default flips to on in a later change
   once proven. With it off, tm runs exactly as today except for one check
   at startup: if a server for this workspace answers on its socket, tm
   refuses to start a second copy of every terminal and says
   `a tm server is running here with N terminals: set server = true to
   reattach, or run tm stop`. `tm stop` works whatever the setting.

## Approaches considered

- **A. tm's own server; the window holds a copy of each screen. Chosen.**
  No new dependency; plugs in behind the existing `workspace.Session`
  interface, so scrollback, copy mode, mouse forwarding and the pane cache
  keep working. Most code of the three.
- **B. One holder process per terminal (dtach-style).** Crash isolation and
  no "last terminal" logic, but dozens of processes and sockets to discover
  and the same snapshot problem as A.
- **C. tmux underneath.** Least code, but requires tmux installed, and tm's
  scrollback, copy mode and wheel would have to drive tmux instead of reading
  the screen, with tmux's own keys and options in the way.
- Dropped: the window pulling every frame from the server. tm redraws on every
  output event; a round trip per pane per frame is too slow.

## Design

### Processes

```
tm (client: Bubble Tea UI, workspace tree, git, state file)
   │  unix socket, one connection
   ▼
tm server for <root> (owns PTYs + vt emulators, nothing else)
```

- The server is the same binary re-executed as `tm __server <root>`
  (hidden subcommand), started by the client when no server answers. It calls
  `setsid`, ignores `SIGHUP`, redirects stdio to a log file
  (`$XDG_STATE_HOME/tm/server-<id>.log`, default `~/.local/state`), and stops
  on `SIGTERM`, `tm stop` or `Q`.
- Socket: `$XDG_STATE_HOME/tm/<id>.sock` (default `~/.local/state/tm/`),
  directory mode 0700, where `<id>` is the first 16 hex chars of
  sha256(absolute, symlink-resolved root). A lock file beside it (`flock`)
  stops two `tm`s starting two servers at the same moment. Not
  `$XDG_RUNTIME_DIR`: systemd-logind deletes that directory when the user's
  last login session ends, which is exactly an SSH drop on a headless
  machine, and a server whose socket vanished would be unreachable and get a
  duplicate started beside it. If the socket path would exceed the 108-byte
  `sun_path` limit, fall back to `/tmp/tm-<uid>/` (mode 0700, owner checked).
  *Known limit:* on hosts configured with logind `KillUserProcesses=yes`, the
  server is killed with the session (setsid does not leave the cgroup); tm
  does not work around that.
- `tm stop` connects and asks the server to kill everything and exit; it
  prints how many terminals were stopped, or that none was running.

### What lives where

The server is deliberately dumb: it owns **sessions** (PTY + process +
`vt.Emulator`, i.e. today's `terminal.Session`) keyed by a server-assigned
session id, and nothing about projects, worktrees, layouts or git. It answers
the programs' terminal queries itself (today's `replyLoop`), so a program
asking for the cursor position or background colour gets an answer while no
window is attached — without this Claude Code would stall in the background.

Everything else stays in the client: `workspace.Workspace`, worktree
creation, the config limit, the state file, the UI.

The client also hands the server an **opaque UI-state blob** after every
change it already persists (layout, hidden terminals, terminal metadata:
project, Num, command, worktree, name, and the session id each maps to). The
server stores it in memory and returns it on attach. This is how a reattach
restores exactly what was on screen — including hidden terminals, which the
state file does not record — without the server learning the workspace
model. The `.terminal-state.json` file keeps being written as today, so a
reboot still restores the layout.

### Plugging in: `remoteSession`

`workspace.Session` stays the boundary. In server mode the model gets a
`Spawner` that asks the server for a new session and returns a
`remoteSession`, which implements the full interface:

- **Input** (`SendKey`, `SendText`, `SendWheel`, `Resize`, `Close`): sent as
  messages; the server applies them to its emulator, so key encoding still
  follows the program's modes (application cursor keys etc.) exactly as now.
  Keys are serialised as the `uv.KeyPressEvent` fields, not pre-encoded bytes.
- **Screen** (`Render`, `Version`, `IsAltScreen`, `MouseTracking`, `Title`):
  the server *pushes* a frame when a session's screen changes, throttled to
  the same 16 ms window `watchTerminal` uses: the rendered screen string
  (with the cursor drawn if that session is the one the client says is
  focused), its version, and the flags. `remoteSession` keeps the latest
  frame; `Render` returns it with no round trip, and `Output()` fires on
  arrival, so the UI's watcher, pane cache and render cadence are unchanged.
  The client reports focus changes to the server so the cursor lands on the
  right pane; the server re-renders and bumps the version of both the
  session losing focus and the one gaining it, so the pane cache never keeps
  a card with the cursor in the wrong pane.
- **Spawn carries the client's environment** (and working directory and
  size). Today every shell inherits the environment of the `tm` that started
  it; a long-lived server's own environment would be days old, so a shell
  opened after an SSH reconnect would get a stale `SSH_AUTH_SOCK`,
  `DISPLAY` and `PATH`. The server adds `TERM`/`COLORTERM` as
  `terminal.Start` does now. (This is my call over mirroring a second emulator in the
  client: one emulator is the only authority, so there is no snapshot to get
  subtly wrong; the cost is a few hundred KB/s over a local socket while a
  pane is busy.)
- **History** (`RenderScrolled`, `Lines`, `ScrollbackLen`): request/reply,
  cached in `remoteSession` by (version, offset). These are only used while
  the user is scrolling or in copy mode.
- **Exit** (`Done`, `Err`): the server sends an exit message with the error
  text; the session stays listed (as exited) until a client has received it,
  so a terminal that exits while detached is dropped on the next attach the
  same way it is dropped today.

### Attach flow

1. `tm` in server mode: connect to the socket; if nothing answers, start the
   server and connect.
2. Handshake: protocol version + client build id. On a mismatch the client
   does not attach; it says a server from another tm version is running, how
   many terminals it holds, and that `tm stop` will end them.
3. If another client is attached, the server tells it to exit
   (`tm: opened in another window`) and closes its connection.
4. The server returns the blob and the list of live sessions (with exited
   ones flagged). The client rebuilds `Workspace` and the pane trees from
   the blob, binding each terminal to its `remoteSession`, and sends the
   window size, which resizes the visible panes as `applyLayout` does today.
   Sessions in the blob that no longer exist are treated as exited; live
   sessions missing from the blob (blob write lost) are attached as hidden
   terminals so nothing running is ever unreachable.
5. No blob yet (fresh server): exactly today's startup — restore from the
   state file, spawning through the server.

### Server lifetime

- Exits when it has no live sessions **and** no client attached (so pressing
  `q` with nothing running leaves nothing behind, and an attached window with
  zero terminals keeps its server).
- **Exits while detached are forgotten, as today.** Today an exited terminal
  is removed from the state file at once, so a finished `npm test` or deploy
  is never rerun unasked. In server mode nobody is attached to do that, so:
  the state file records each terminal's server session id (new optional
  field; old files have none), and a server that exits because everything
  exited first writes `<id>.exited` beside its socket, listing the session
  ids that exited. The next `tm` that finds no server reads that file, drops
  those terminals from the restored state, and deletes it. A terminal that
  exits while detached but with the server still running is dropped on the
  next attach (attach flow, step 4).
- `Q`, `tm stop`, `SIGTERM`: kill all sessions (SIGHUP to each process group,
  like a closing terminal), then exit.
- If the server dies, the attached client shows a notice and falls back to
  quitting; the next `tm` starts fresh from the state file (today's
  behaviour).

### Unseen output

The client marks a terminal unseen on attach if its version moved while no
client was attached (server reports the version it had at detach), so the
sidebar's amber marker tells the user which terminals printed while the
window was closed.

## Testing

- The server takes a `Spawner` exactly as `workspace` does, so protocol and
  lifetime tests run server and client over `net.Pipe` / a temp socket with
  fake sessions and no PTY.
- `remoteSession` gets the `fakeSession` contract tests the UI relies on
  (render, version moves, exit, scrolled render, lines).
- An end-to-end test in the style of `tools/screenshot`: start tm in a PTY,
  start a long-running command, kill the client's PTY (window close), start
  tm again, assert the command is still running and its output is on screen.
- With `server` off, the existing suite must pass unchanged.

## Left to the builder

- Wire format (length-prefixed binary or JSON lines); throughput of pushed
  frames is the only thing that must be measured.
- Package layout (suggested: `internal/server` for the server and protocol,
  `remoteSession` beside it, `cmd/tm` for `__server` and `stop`).
- Exact log file contents, and whether the frame push sends a full screen or
  changed rows.
- Where `Q` and the take-over message appear in help and the status bar,
  following `ui/keys.go` being the single source of truth.
