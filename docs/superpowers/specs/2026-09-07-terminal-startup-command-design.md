# Terminal startup command — design

Date: 2026-09-07
Status: approved for planning

## Problem

tm restores the *shape* of a workspace but not its *work*. After a restart the
saved definitions, worktrees and layout come back, yet every pane is an empty
shell: the dev server, the test watcher and the agent session are gone, and the
user restarts each one by hand. The value of persistence is therefore only
partly realised.

## Goal

A terminal can carry a command that tm runs whenever it starts that terminal, so
reopening a workspace brings back what was running in it, not just where it sat.

## Decisions

| Decision | Choice | Why |
| --- | --- | --- |
| How the command runs | tm starts the user's shell as today, then feeds the command in as input | When the command ends the pane is a usable prompt in the right directory, with the command in shell history and `Up` ready to re-run it. Quitting an agent or stopping a server leaves somewhere useful instead of a dead pane. |
| Pane label | The command replaces `Terminal N` when set | Self-describing panes; gives the vestigial `Name` field a purpose; lets tm tell two shells in one checkout apart. |
| Where the command comes from | Typed by the user in the new-terminal form | Predictable and small. tm cannot judge whether re-running an arbitrary captured command is safe. |

## Behaviour

A terminal's command runs when tm **starts a process** for it:

- when the terminal is created through the form;
- when a saved definition is opened, whether singly (manager `enter`) or in bulk
  (manager `a`).

It does **not** run when a hidden terminal is shown again, because that process
is still alive.

An empty command reproduces today's behaviour exactly: a plain shell, no input
sent. The command is echoed in the pane as though typed, which is intentional —
it is visible, and it enters shell history.

## Data model and persistence

- `state.Terminal` gains an optional `Command string` field, serialised as `command` and omitted when empty.
- `state.Version` goes 2 → 3. The bump is documentary: `Load` does not reject
  unknown versions and a missing field means no command, so v2 files load
  unchanged and v3 files remain readable by older builds. An older build that
  rewrites the file drops commands; this is accepted, not defended against.
- `workspace.Options`, `workspace.SavedTerminal` and `workspace.Terminal` each
  gain a `Command` field; `Terminal.saved()` carries it into the definition.
- `Terminal.Key()` includes the command, so two plain shells in one checkout are
  distinguishable and keep their exact pane positions across a restart instead of
  possibly swapping cells. `Project.forget` must likewise match on the full
  definition (name, command, worktree) rather than name and worktree alone.

Layout files are unaffected: `state.Layout` references terminals by index, and
keys are derived at load time.

## UI

- **Form** (`ui/form.go`): a Command text field, offered for every project
  including non-Git ones. Tab order: Branch, Command, Worktree. Newlines are
  rejected so one terminal means one command.
- **Pane header** (`ui/model.go`, `paneTitle`): `npm run dev · main` with a
  command, `Terminal 2 · main` without. The existing header truncation applies.
- **Terminal Manager** (`ui/manager.go`): rows label a terminal by its command
  when it has one, falling back to `Terminal N`.

## Code changes by package

- `state`: new field, version constant.
- `workspace`: `Command` on `Options`, `SavedTerminal`, `Terminal`; `saved()`,
  `Key()` and `forget` updated; `startTerminal` takes the command and sends it
  after a successful spawn. `startTerminal` is the single choke point for every
  path that starts a process, so creation, `OpenSaved` and `OpenAll` are all
  covered by that one change. `persist.go` maps the field to and from state.
- `ui`: form field, pane title, manager row.
- `terminal`: **unchanged**. `Spawner` keeps its signature, because the command
  is delivered as session input rather than as a different process.

## Edge cases

| Case | Behaviour |
| --- | --- |
| Empty command | Plain shell, nothing sent — identical to today. |
| Newline in the command | Rejected by the form with an error. |
| Command fails or exits at once | The shell survives; the pane shows the error and a prompt. |
| Hidden then shown | No re-run; the process was never stopped. |
| Terminal limit reached | Unchanged; the limit is enforced in `startTerminal` before spawning. |
| Worktree terminals | Orthogonal; a command may accompany a worktree or not. |
| Deletion | Unchanged. |

## Testing

- **state**: a command survives a save/load round trip; a v2 file with no
  `command` field loads with an empty command.
- **workspace**: creating a terminal with a command sends exactly that command to
  the session; reopening a saved definition sends it again; an empty command
  sends nothing; two definitions differing only by command are remembered and
  forgotten independently.
- **ui**: the form creates a terminal with the typed command; the pane header
  shows the command in place of `Terminal N`; the manager labels saved rows by
  command; two shells with different commands keep their panes across save and
  restore.

Existing UI tests drive the form through `makeTerminal`, which must be updated
for the added field.

## Out of scope

- **Capturing what is already running.** Deferred deliberately: tm can read a
  pane's foreground process and argv, so a "remember what this terminal is
  running" action in the manager is feasible later, but it is a separate feature
  with its own confirmation flow.
- **Automatic capture and replay.** Rejected: tm cannot distinguish a dev server
  from a destructive command, and silently repeating the latter on every startup
  is unacceptable.
- **Editing a command after creation.** Not in this change; delete and recreate.
- **Attention indicators.** Explored and dropped in the same session; the bell is
  unused by Claude Code and timing-based detection proved unreliable across
  programs.
