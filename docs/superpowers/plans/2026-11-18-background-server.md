# Background Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** With `server = true` in `config.toml`, terminals keep running when the tm window closes, and the next `tm` in the same folder reattaches to them.

**Architecture:** A per-workspace server process (`tm __server <root>`) owns every PTY and `vt.Emulator` and pushes rendered frames over a Unix socket. The client keeps the whole workspace model and plugs a `RemoteSession` in behind the existing `workspace.Session` interface, so the UI barely changes. An opaque blob (the client's state plus session refs and hidden terminals) is parked in the server so a reattach restores exactly what was on screen.

**Tech Stack:** Go (version in `go.mod`), `encoding/gob` over `net.UnixConn`, `golang.org/x/sys/unix` only if `syscall` lacks something (it should not: `syscall.Flock`, `syscall.Setsid` via `SysProcAttr`), existing `creack/pty` and `charmbracelet/x/vt`.

**Spec:** `docs/superpowers/specs/2026-11-18-background-server-design.md`

## Global Constraints

- Go toolchain: `export PATH="$HOME/.local/go/bin:$PATH"` before every Go command on this machine.
- `make check` must pass after every task (gofmt, vet, test, race, build). CI fails on unformatted files.
- With `server` unset or false, behaviour is unchanged except the startup check in Task 8; the existing test suite passes untouched.
- Socket dir: `$XDG_STATE_HOME/tm/` (default `~/.local/state/tm/`), mode 0700. File stem `<id>` = first 16 hex chars of sha256(abs, symlink-resolved root). Files: `<id>.sock`, `<id>.lock`, `<id>.exited`, `<id>.log`. If the socket path exceeds 107 bytes, use `/tmp/tm-<uid>/` (0700, owner verified) for all four.
- Never `$XDG_RUNTIME_DIR` (logind deletes it on last logout).
- Frame push throttle: 16 ms (reuse the value of `ui.outputWindow`; define `server.frameWindow = 16 * time.Millisecond`).
- Session refs are random 64-bit values rendered as 16 lowercase hex chars, never sequential.
- Server env for each spawn = the client's `os.Environ()` sent in the spawn request, plus `TERM=xterm-256color`, `COLORTERM=truecolor` (as `terminal.Start` does today).
- Messages, exact copy: evicted client prints `tm: opened in another window`; stopped client prints `tm: stopped by tm stop`; off-mode refusal prints `tm: a tm server is running here with N terminal(s): set server = true in <config path> to reattach, or run tm stop`; protocol mismatch prints `tm: a tm server from another tm version is running here with N terminal(s); run tm stop to end them, then start tm again`.
- No `Co-Authored-By` trailer in commits (repo rule). Work on branch `background-server`.
- Key handling policy holds: no new plain-letter or Ctrl bindings in terminal focus. `Q` is a sidebar key.

## Review Focus

1. **Client killed by SIGHUP mid-write** (window closed while a frame is in flight): the server must treat any read/write error as a detach, never crash, and keep every session running. → Task 4 `TestServerSurvivesClientVanishingMidFrame`.
2. **Program queries the terminal while detached** (Claude Code asking for cursor position): the server's emulator must answer with no client attached. → Task 4 `TestServerAnswersQueriesWithNoClient` (uses a real `terminal.Start` running `printf '\033[6n'; read -r x; echo got`).
3. **Two `tm` started at the same instant in a fresh folder**: exactly one server. → Task 3 `TestEnsureServerStartsOnlyOne`.
4. **Terminal exits while detached, then the server exits because nothing is left**: the next `tm` must not rerun its command. → Task 6 `TestRestoreDropsTerminalsListedAsExited` and Task 9 end-to-end step.
5. **Window much smaller on reattach than when detached**: panes are resized by the server before the first frame is drawn, so no frame wider than the pane reaches the card. → Task 7 `TestAttachResizesBeforeFirstFrame`.

---

### Task 1: Config flag and spawn environment

**Files:**
- Modify: `internal/config/config.go` (add `Server bool`, key `server`)
- Modify: `internal/terminal/session.go` (`Config.Env`)
- Test: `internal/config/config_test.go`, `internal/terminal/session_test.go`

**Interfaces:**
- Produces: `config.Config.Server bool`; `terminal.Config.Env []string` — when non-nil it replaces `os.Environ()` as the base, and `TERM`/`COLORTERM` are still appended.

- [ ] **Step 1: Failing tests**
  - `TestServerFlagParses`: file `server = true` → `Server == true`; `server = false` and missing → false; `server = yes` → false plus a `Notice` naming the line, like `max_terminals`.
  - `TestStartUsesGivenEnv`: `Start(Config{Command: "/bin/sh", Args: []string{"-c", "echo $TM_PROBE"}, Env: []string{"TM_PROBE=hello"}, ...})`; wait on `Done()`; `strings.Join(Lines(0), "\n")` contains `hello`.
- [ ] **Step 2:** `go test ./internal/config ./internal/terminal` → both FAIL.
- [ ] **Step 3:** Implement. Accept `true`/`false` only.
- [ ] **Step 4:** Tests pass; `make check`.
- [ ] **Step 5:** Commit `Read a server flag and let a terminal take its environment`.

---

### Task 2: Wire protocol

**Files:**
- Create: `internal/server/protocol.go`
- Test: `internal/server/protocol_test.go`

**Interfaces:**
- Produces:
  ```go
  const ProtocolVersion = 1
  type Msg struct { // one envelope, gob-encoded; exactly one pointer field set
      Hello *Hello; Welcome *Welcome; Spawn *Spawn; Spawned *Spawned
      Input *Input; Resize *Resize; Focus *Focus; Kill *Kill
      Frame *Frame; Exit *Exit; Query *Query; Answer *Answer
      SetBlob *SetBlob; Evict *Evict; Stop *Stop; Status *Status
  }
  type Hello   struct{ Protocol int; Attach bool } // Attach=false: tm stop / probe
  type Welcome struct{ Protocol int; Blob []byte; Sessions []SessionInfo; Live int }
  type SessionInfo struct{ Ref string; Meta []byte; Exited bool; Err string; Changed bool } // Changed: output while detached
  type Spawn   struct{ Req uint64; Dir string; Env []string; Cols, Rows int; Meta []byte }
  type Spawned struct{ Req uint64; Ref string; Err string }
  type Input   struct{ Ref string; Key *uv.KeyPressEvent; Text string; Wheel *Wheel }
  type Wheel   struct{ Up bool; X, Y int }
  type Resize  struct{ Ref string; Cols, Rows int }
  type Focus   struct{ Ref string; Cursor bool }
  type Kill    struct{ Ref string }
  type Frame   struct{ Ref string; Version uint64; Screen, Title string; Alt, Mouse bool; Scrollback int }
  type Exit    struct{ Ref string; Err string }
  type Query   struct{ Req uint64; Ref string; Offset int; Lines bool } // Lines=false: RenderScrolled
  type Answer  struct{ Req uint64; Screen string; Lines []string }
  type SetBlob struct{ Blob []byte }
  type Evict   struct{ Reason string } // "opened in another window" | "stopped by tm stop"
  type Stop    struct{}
  type Status  struct{ Live int } // reply to Hello{Attach:false}
  type Conn struct{ ... }          // wraps net.Conn with gob encoder/decoder, Send is mutex-guarded
  func NewConn(c net.Conn) *Conn
  func (c *Conn) Send(m Msg) error
  func (c *Conn) Recv() (Msg, error)
  func (c *Conn) Close() error
  ```
- [ ] **Step 1: Failing test** `TestConnRoundTripsEveryMessage`: over `net.Pipe`, send one `Msg` of each kind (Input with a `uv.KeyPressEvent{Code: 'a', Mod: uv.ModCtrl}`), receive, `reflect.DeepEqual`. `TestConnSendIsSafeConcurrently`: 8 goroutines × 100 Frames, receiver counts 800, under `-race`.
- [ ] **Step 2:** FAIL (package missing).
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test -race ./internal/server`.
- [ ] **Step 5:** Commit `Define the tm server protocol`.

---

### Task 3: Paths, lock, starting and stopping the server process

**Files:**
- Create: `internal/server/paths.go`, `internal/server/daemon.go`
- Test: `internal/server/paths_test.go`, `internal/server/daemon_test.go`

**Interfaces:**
- Consumes: Task 2 `Conn`, `Hello`, `Status`, `Stop`.
- Produces:
  ```go
  type Paths struct{ Dir, Socket, Lock, Exited, Log string }
  func PathsFor(root string) (Paths, error)          // creates Dir 0700
  func Probe(p Paths) (live int, running bool)       // Hello{Attach:false}; not running on dial error
  func Ensure(p Paths, exe, root string) error       // flock Lock; if Probe fails, start exe "__server" root detached, wait ≤3s for socket
  func StopServer(p Paths) (stopped int, err error)  // Hello{Attach:false} then Stop; returns Live before stop
  func TakeExited(p Paths) []string                  // read + delete Exited (one ref per line); nil if absent
  ```
  `Ensure` starts the child with `SysProcAttr{Setsid: true}`, stdin `/dev/null`, stdout/stderr the `Log` file (append), and does not wait for it.
- [ ] **Step 1: Failing tests**
  - `TestPathsForIsStableAndPrivate`: same root twice (and via a symlink) → same Socket; Dir mode 0700; different roots differ; a root whose path makes Socket > 107 bytes → Dir under `/tmp/tm-<uid>`.
  - `TestEnsureServerStartsOnlyOne`: build a tiny helper binary in `TestMain` (or re-exec the test binary with `TM_TEST_SERVER=1`) that runs `Serve` from Task 4 on the socket; call `Ensure` from 5 goroutines; `Probe` succeeds; count helper processes started (helper appends its pid to a file) == 1.
  - `TestTakeExitedReadsOnce`: write two refs; first call returns both, second nil.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement. (The `TestEnsureServerStartsOnlyOne` test may be finished after Task 4 lands; mark it `t.Skip` until then and remove the skip in Task 4 Step 4.)
- [ ] **Step 4:** Tests pass; `make check`.
- [ ] **Step 5:** Commit `Find, start and stop a workspace's tm server`.

---

### Task 4: The server

**Files:**
- Create: `internal/server/server.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: Task 2 protocol; `terminal.Config`, `terminal.Start`.
- Produces:
  ```go
  type Session interface { // structurally identical to workspace.Session
      SendKey(uv.KeyPressEvent); SendText(string); Resize(cols, rows int)
      Render(showCursor bool) string; RenderScrolled(offset int) string
      Version() uint64; Lines(offset int) []string; ScrollbackLen() int
      IsAltScreen() bool; MouseTracking() bool; SendWheel(up bool, x, y int)
      Title() string; Output() <-chan struct{}; Done() <-chan struct{}; Err() error; Close() error
  }
  type Spawner func(terminal.Config) (Session, error)
  func DefaultSpawner(c terminal.Config) (Session, error) // shell = terminal.DefaultShell()
  type Server struct{ ... }
  func New(spawn Spawner, exitedPath string) *Server
  func (s *Server) Serve(l net.Listener) error // returns nil when the server decides to exit
  ```
  Behaviour (each line is a test below):
  - One attached client at a time; a new `Hello{Attach:true}` sends `Evict{"opened in another window"}` to the old one, closes it, then `Welcome`s the new one with the blob and every session (`Changed` true if the session's `Version()` moved since the last detach).
  - Frames only go to an attached client, at most one per session per `frameWindow`, on output, on `Resize`, and on a `Focus` that changes the cursor flag; `Frame.Version` is a server counter per session that increases on every frame sent.
  - `Exit` is sent when a session ends; an exited session stays in the list (`Exited: true`) until a `Welcome` has carried it, then is dropped.
  - Lifetime: `Serve` returns when there are no live sessions and no client. Before returning it appends to `exitedPath` the refs of sessions that exited since the last client detached (one per line). `Stop` closes every session, sends `Evict{"stopped by tm stop"}` to an attached client, and returns.
  - Any connection error = detach. Nothing a client does may panic the server.
- [ ] **Step 1: Failing tests** (fake `Session` in `server_test.go` with controllable Output/Done; server over `net.Listen("unix", t.TempDir()+"/s")`):
  - `TestSpawnInputAndFrame`: spawn → `Spawned` with 16-hex ref; `Input{Text:"ls"}` reaches fake `SendText`; fake output → one `Frame` with fake's `Render(false)`.
  - `TestFramesAreThrottled`: 100 output signals in 5 ms → ≤ 2 frames for that session in 40 ms.
  - `TestFocusRedrawsWithCursor`: `Focus{Ref, Cursor:true}` → a Frame whose Screen equals fake `Render(true)` and whose Version is higher.
  - `TestSecondClientEvictsFirst`: client A gets `Evict{"opened in another window"}`; B's `Welcome` lists A's sessions and the blob A set.
  - `TestServerSurvivesClientVanishingMidFrame`: client closes its conn while the fake floods output; a new client attaches and sees the session live.
  - `TestExitedWhileDetachedIsReportedOnce`: detach, fake exits, attach → `Exited:true`; attach again → absent.
  - `TestServeReturnsWhenEmptyAndDetached`: last session exits with no client → `Serve` returns nil within 1 s and `exitedPath` contains its ref; with a client attached and zero sessions it keeps running.
  - `TestStopKillsEverything`: `Stop` → every fake `Close` called, attached client gets `Evict{"stopped by tm stop"}`, `Serve` returns.
  - `TestServerAnswersQueriesWithNoClient`: `DefaultSpawner` running `/bin/sh -c 'printf "\033[6n"; read -r x; echo got'` with no client attached; attach after 500 ms; `Query{Lines:true}` answer contains `got`.
  - `TestQueryAnswersScrolledAndLines`: `Query` → `Answer` with fake's `RenderScrolled(3)` / `Lines(3)`.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement. One goroutine per session watching `Output()`/`Done()`; all session-map and client state under one mutex; sends via `Conn.Send` outside the mutex.
- [ ] **Step 4:** `go test -race ./internal/server` (also un-skip Task 3's `TestEnsureServerStartsOnlyOne`); `make check`.
- [ ] **Step 5:** Commit `Add the tm server that owns the terminals`.

---

### Task 5: Client and RemoteSession

**Files:**
- Create: `internal/server/client.go`
- Test: `internal/server/client_test.go`

**Interfaces:**
- Consumes: Tasks 2 and 4.
- Produces:
  ```go
  type Client struct{ ... }
  func Attach(p Paths) (*Client, Welcome, error)       // error mentions protocol mismatch distinctly: ErrProtocol
  var ErrProtocol = errors.New("protocol mismatch")
  func (c *Client) Spawn(dir string, cols, rows int, meta []byte) (*RemoteSession, error) // sends os.Environ()
  func (c *Client) Adopt(info SessionInfo) *RemoteSession // a live session from Welcome
  func (c *Client) SetBlob(b []byte)
  func (c *Client) Stop()                                 // Stop; used by Q
  func (c *Client) Gone() <-chan string                    // closed-with-reason: Evict reason, or "the tm server stopped" on EOF
  func (c *Client) Close() error                           // detach
  type RemoteSession struct{ ... } // implements workspace.Session (compile-time assertion in a _test file in package ui, Task 7, to keep server free of workspace)
  func (r *RemoteSession) Ref() string
  ```
  `RemoteSession`: `Render(showCursor)` returns the latest frame's Screen and, if `showCursor` differs from the last flag sent for this ref, sends `Focus`. `Version()` = latest frame's Version. `Output()` signalled (coalescing, buffer 1) per frame. `RenderScrolled`/`Lines` send `Query` and wait ≤ 2 s for the `Answer` (empty result on timeout), cached by (Version, offset, kind). `Done()` closes on `Exit` or when the client is gone; `Err()` returns `errors.New(Exit.Err)` when non-empty. `Close()` sends `Kill`.
- [ ] **Step 1: Failing tests** (real `Server` from Task 4 with the fake Session, over a temp socket):
  - `TestRemoteSessionMirrorsFrames`: Spawn; fake prints → `Output()` fires; `Render(false)` equals fake Render; `IsAltScreen`/`MouseTracking`/`Title`/`ScrollbackLen` follow the frame.
  - `TestRemoteSessionCursorFollowsFocus`: `Render(true)` once → within 100 ms a new frame with the cursor variant and a higher `Version()`.
  - `TestRemoteSessionKeysAndClose`: `SendKey`, `SendText`, `SendWheel`, `Resize` reach the fake with the same arguments; `Close()` → fake closed → `Done()` closes.
  - `TestGoneOnEvictAndOnServerExit`: second `Attach` → first client's `Gone()` yields `opened in another window`; server `Stop` → `stopped by tm stop`; killing the listener → `the tm server stopped`.
  - `TestAttachRejectsOtherProtocol`: server answering `Welcome{Protocol: 99}` → `errors.Is(err, ErrProtocol)`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement (one reader goroutine dispatching by message). **Step 4:** `go test -race ./internal/server`; `make check`.
- [ ] **Step 5:** Commit `Add the client side: RemoteSession behind the Session interface`.

---

### Task 6: Session refs in the workspace and state file

**Files:**
- Modify: `internal/state/state.go` (`Terminal.Ref string \`json:"ref,omitempty"\``; `Version` stays 3)
- Modify: `internal/workspace/persist.go`, `internal/workspace/workspace.go`
- Test: `internal/workspace/persist_test.go`, `internal/state/state_test.go`

**Interfaces:**
- Produces:
  - `SavedTerminal.Ref string` (round-trips through Restore/Snapshot; not part of `key()`).
  - `Snapshot` writes `Ref` for a running terminal whose `Session` implements `interface{ Ref() string }`.
  - `func (ws *Workspace) RestoreFrom(f state.File)` — today's `Restore` body; `Restore` = `state.Load` + `RestoreFrom`.
  - `func (ws *Workspace) Forget(refs []string)` — drops saved definitions with those refs (and their layout leaves, as `forget` does).
  - `Workspace.Adopt func(ref string) Session` — when set and it returns non-nil, `startTerminal` uses that session instead of spawning **and does not type the command**.
  - `func (ws *Workspace) OpenLive(p *Project, live map[string]bool, cols, rows int) RestoreResult` — like `OpenAll` but only definitions whose `Ref` is in `live`.
- [ ] **Step 1: Failing tests**
  - `TestRefRoundTripsThroughState`: fake session with `Ref() == "00ff00ff00ff00ff"` → `Save` → new workspace `Restore` → `p.Saved[0].Ref` matches; a file without refs still loads.
  - `TestRestoreDropsTerminalsListedAsExited`: two saved with refs a, b; `Forget([]string{"a"})` → only b left, layout has one leaf.
  - `TestAdoptSkipsSpawnAndCommand`: `Adopt` returns a fake for ref "a"; `OpenLive(p, {"a":true})` → spawner never called, fake got no `SendText`, terminal running; def with ref "b" not in live stays saved.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** `go test ./internal/state ./internal/workspace`; `make check`.
- [ ] **Step 5:** Commit `Remember which server session each terminal runs in`.

---

### Task 7: UI in server mode

**Files:**
- Create: `internal/ui/attach.go`
- Modify: `internal/ui/model.go` (`New` signature via options, `persist`, quit keys), `internal/ui/keys.go` (`StopAll`)
- Test: `internal/ui/attach_test.go`, `internal/ui/keys_test.go`

**Interfaces:**
- Consumes: Tasks 5 and 6.
- Produces:
  ```go
  type Options struct {
      Client  *server.Client  // nil: today's behaviour exactly
      Welcome server.Welcome
      Forget  []string        // refs from server.TakeExited, applied after Restore
  }
  func NewWith(root string, projects []project.Project, o Options) Model // New(root, projects) = NewWith(root, projects, Options{})
  func (m Model) ExitMessage() string // "" normally; Gone() reason when evicted/stopped/lost
  ```
  - Blob (`attach.go`): `type uiBlob struct{ State state.File; Hidden []string }` JSON. `persist()` in server mode also sends `client.SetBlob` with the snapshot and the refs of hidden terminals.
  - Spawner in server mode: `client.Spawn(dir, cols, rows, meta)`; meta = JSON `{project name, SavedTerminal}`.
  - Attach: if `Welcome.Blob` non-empty, `ws.RestoreFrom(blob.State)` instead of the file; `Forget` refs of `Exited` sessions; `ws.Adopt` = lookup in Welcome's live sessions → `client.Adopt`; for every project with a live ref, run the same path as `restore(p)` but via `OpenLive`; then `hide` each terminal whose ref is in `blob.Hidden`; live sessions not in the blob are added from their meta to their project as hidden; sessions with `Changed` are marked `unseen`. Empty blob → today's startup (`Restore` from file, then `Forget(o.Forget)`).
  - Keys: `StopAll: key.NewBinding(key.WithKeys("Q"), key.WithHelp("Q", "stop all"))`, in the sidebar help next to Quit only when a client is set. `q`/`ctrl+c` with a client: `persist`, `client.Close()`, `tea.Quit` — no `CloseAll`. `Q` with a client: `persist`, `CloseAll`, `client.Stop()`, `tea.Quit`. Without a client, `Q` does nothing and `q` is unchanged.
  - `Init` returns a cmd waiting on `client.Gone()` → `serverGoneMsg{reason}` → set exit message, `tea.Quit` (no persist: the new owner writes state).
- [ ] **Step 1: Failing tests** (real `Server` with fake sessions on a temp socket; `newTestModel`-style helper `newAttachedModel(t, srv)`):
  - `TestQuitLeavesTerminalsRunning`: open a terminal, `q` → fake not closed; `Welcome` of a new client lists it live.
  - `TestStopAllKillsEverything`: `Q` → fake closed, server `Serve` returned.
  - `TestReattachRestoresLayoutAndHidden`: two terminals side by side plus one hidden via `alt+x`; quit; new attached model → same two leaves in the same order, the third running and hidden, nothing spawned.
  - `TestAttachDropsTerminalsThatExitedWhileAway`: quit; fake exits; reattach → terminal gone from `Terminals` and `Saved`, state file no longer lists it.
  - `TestAttachMarksOutputWhileAway`: quit; fake prints; reattach → project marker amber (`m.unseen[t]`).
  - `TestAttachResizesBeforeFirstFrame`: quit at 160x48; reattach with `tea.WindowSizeMsg{80,24}` → fake `Resize` called with pane size before any `View` contains a frame.
  - `TestEvictedModelQuitsWithReason`: second client attaches → first model's `ExitMessage() == "tm: opened in another window"`.
  - `TestRemoteSessionSatisfiesSession`: `var _ workspace.Session = (*server.RemoteSession)(nil)`.
  - `TestQuitWithoutClientIsUnchanged`: existing quit test still passes; `Q` without a client is a no-op.
  - `TestHelpScreenExplainsServerKeys` (in `help_test.go`): with a client, the help screen (`s`) SIDEBAR section lists `Q  stop all` and Quit's text reads `q/ctrl+c  leave (terminals keep running)`; without a client, Quit reads `quit` and there is no `Q` row. The help screen must still fit the window (`help_test.go` already checks height; keep it passing at 100x30). The user asked for the help to be updated explicitly.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. Add `Q` to the `press` helper switch if it needs special-casing. **Step 4:** `go test -race ./internal/ui`; `make check`.
- [ ] **Step 5:** Commit `Attach the UI to a tm server`.

---

### Task 8: Command line: server mode, `__server`, `stop`, off-mode guard

**Files:**
- Modify: `cmd/tm/main.go`
- Test: `cmd/tm/main_test.go`

**Interfaces:**
- Consumes: Tasks 3, 4, 7.
- Produces: `tm` (normal), `tm stop`, `tm __server <root>` (hidden; ignores SIGHUP via `signal.Ignore(syscall.SIGHUP)`, stops on SIGTERM, listens on `Paths.Socket` after removing a stale socket file whose `Probe` fails).
  Startup: `cfg := config.Load(config.Path())`; `paths := server.PathsFor(root)`; `forget := server.TakeExited(paths)`.
  - `cfg.Server`: `server.Ensure(paths, os.Executable(), root)`, `server.Attach(paths)` (on `ErrProtocol` print the mismatch message using `Probe`'s count and exit 1), run `ui.NewWith(root, projects, Options{Client, Welcome, Forget: forget})`, then print `m.ExitMessage()` to stderr if non-empty.
  - Not `cfg.Server`: if `Probe` reports running → print the refusal message, exit 1. Else today's path with `Options{Forget: forget}`.
  - `tm stop`: `StopServer`; print `stopped N terminal(s)` or `no tm server running here`.
- [ ] **Step 1: Failing tests** `TestStopWithNoServer` (prints `no tm server running here`, exit 0) and `TestOffModeRefusesWhenServerRuns` (start a `Server` on the workspace's socket with one fake session; `run()` with `server` unset returns an error equal to the refusal message with `1 terminal`).
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** `go test ./cmd/tm`; `make check`.
- [ ] **Step 5:** Commit `Run tm against its server: tm stop and the hidden __server command`.

---

### Task 9: End-to-end test and docs

**Files:**
- Create: `cmd/tm/server_e2e_test.go`
- Modify: `CLAUDE.md` (new "Background server" section; drop "No daemon" from the opening paragraph, stating server mode is opt-in), `README.md` (how to enable `server = true`, what `q` vs `Q` vs `alt+delete` do in server mode, `tm stop`, and the key table if the README has one), and `tm --help`/usage text in `cmd/tm` if present (list `tm stop`)

- [ ] **Step 1: Write `TestServerKeepsTerminalsAcrossWindows`** (skipped under `-short`): build `./cmd/tm` to a temp dir; temp workspace with one git repo (`git init`, one commit); fake `HOME`, `XDG_CONFIG_HOME` (with `server = true`), `XDG_STATE_HOME`; `SHELL=/bin/sh`. Drive tm in a PTY with a draining `vt.Emulator` exactly as `tools/screenshot/main.go` `capture` does. Steps and assertions:
  1. `enter`, `tab`, type `i=0; while :; do i=$((i+1)); echo tick$i; sleep 0.2; done` + `\r`; wait 1 s; screen contains `tick`.
  2. Close the PTY master and wait for the tm process to exit (window closed). `server.Probe(paths)` → running, live == 1.
  3. Wait 1 s, start tm again in a new PTY; within 3 s the screen contains a `tick` number larger than the last one seen in 1.
  4. Press `alt+s`, then `Q`; within 2 s `Probe` reports not running and the socket file is gone.
  5. Exit-while-detached: start tm, open a terminal running `sleep 1; exit`, close the PTY, wait 3 s (server exits on its own), start tm with `server` **off**: state file lists no terminal, and `<id>.exited` is gone.
- [ ] **Step 2:** `go test ./cmd/tm -run TestServerKeepsTerminalsAcrossWindows -v` → PASS (it exercises Tasks 1–8; if it fails, fix the owning task's code, not the test).
- [ ] **Step 3:** Docs. **Step 4:** `make check`.
- [ ] **Step 5:** Commit `Test the server end to end and document it`.

---

### Task 10: Manual verification before merge (user asked for this)

- [ ] Build `make build`; in a scratch workspace (not EagleEye) with `server = true` in a scratch `XDG_CONFIG_HOME`, using a real terminal window:
  - open three terminals (one `claude` if available, one `npm`/long-running loop, one hidden with `alt+x`), close the window, reopen: all three running, hidden one still hidden, amber marker on projects that printed.
  - open tm in a second window → first window exits with `tm: opened in another window`.
  - `alt+delete` one terminal, `q`, reopen: it stays gone.
  - `Q`: everything stops; `tm stop` afterwards prints `no tm server running here`.
  - `server` off with a server running → refusal message.
  - Reboot-equivalent: `tm stop`, then `tm` → layout restored from the state file as today.
- [ ] Report results to the user with what was observed; merge only after they confirm.
