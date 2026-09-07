# Branch-driven terminal location, and a real branch picker — design

Date: 2026-09-07
Status: draft for review

## Problem

Three separate frustrations, all rooted in the same wrong model.

**The form asks a question the branch has already answered.** Creating a terminal
asks for a branch and, separately, whether to use a worktree. But a branch can be
checked out in exactly one place — git enforces this — so once the branch is
known, the location follows. The toggle can only agree with reality or contradict
it.

**Branches already in use are a dead end.** In `WEB-VMS-WebApp` eight branches are
currently checked out: one in the project checkout, one in a tm worktree, six in
worktrees Claude Code created. Choosing any of them fills the whole form and then
fails with git's `fatal: 'X' is already used by worktree at ...`. tm knows this
before the user types a character and does not say so.

**A worktree can hold only one terminal.** `OpenSaved` refuses a second with
"terminal N is already running in this worktree". Nothing in git requires that —
two shells share a directory perfectly well, which is already true of the project
checkout. The limit blocks the ordinary case of running a dev server in one pane
and working in another, on the same branch.

Underneath all three: with 180 local branches, the form's one-line `branches:`
hint is sorted alphabetically, so it opens on `66, 99, 999, COMBINE-EVENT` and
truncates long before reaching anything recent.

## What is actually shared between worktrees

Established by experiment, and the basis for the design:

| | Shared? |
| --- | --- |
| Working files | **No.** Editing in a worktree leaves every other directory untouched. |
| Commits, branches, tags, stash | **Yes.** One repository; a commit in a worktree is immediately visible everywhere. |
| A checked-out branch | **Exclusive.** At most one directory per branch, enforced by git. |
| Files between two terminals in the *same* directory | **Yes.** They are two shells in one folder. |

So the only place two things can contend over files is two terminals in one
directory — which is exactly what the user wants when one runs a server and the
other is a prompt. tm's job is to make the location unmistakable, not to police it.

## Decisions

| Decision | Choice | Why |
| --- | --- | --- |
| What determines location | The selected branch | A branch lives in exactly one place; asking separately invites contradiction. |
| A branch already checked out | The terminal opens **there** | Turns a dead end into the thing the user wanted. Includes worktrees tm did not create. |
| A branch with no checkout | A new worktree under `.terminal-worktrees/` | Only way to have a terminal on it without disturbing the project checkout. |
| Worktrees per branch | Exactly one, ever | Prevents "which copy am I in?". tm reuses rather than creating a second. |
| Terminals per directory | Unlimited | Git never forbade it; the dev-server-plus-shell case needs it. |
| Duplicate commands | Confirm once | The one mistake worth interrupting for. |
| Deleting worktrees | **Unchanged** | tm still removes only worktrees under its own directory. |

## Behaviour

### Location resolution

When the user selects or types a branch, tm resolves one of four locations and
shows it plainly, along with which of tm's terminals are already there:

1. **The project checkout** — the branch is what the checkout has.
2. **An existing worktree** — the branch is checked out in one. tm states the path
   and whether tm created it.
3. **A new worktree** — the branch exists but is checked out nowhere.
4. **A new branch and a new worktree** — the name matches nothing, so the `From`
   field appears as it does today.

With no branch entered, the location is the project checkout — today's
"worktree: no", reached by leaving the field empty rather than toggling.

The yes/no Worktree toggle is replaced by a read-only Location line. It never
asks; it reports.

### What tm can and cannot claim about "in use"

tm knows only about terminals it started. A worktree created by another tool may
hold a live session tm cannot see. The Location line must therefore distinguish
"no terminals here" from "no terminals I know about":

- For a worktree tm created: `2 tm terminals here: claude --continue, npm run dev`
- For a foreign worktree: `no tm terminals here — this worktree may be in use elsewhere`

Claiming a Claude Code worktree is idle would be a lie the user would act on.

### Many terminals in one directory

The refusal in `OpenSaved` is removed, and `remember` stops deduplicating saved
definitions by worktree path. Terminals in one directory are told apart by their
command label, which already replaces `Terminal N` when a command is set.

Creating a terminal whose command exactly matches one already running in the same
target directory asks for confirmation once, naming it. Different commands in one
directory are normal and ask nothing.

### The picker

Both the Branch and the From field get the same list, opening beneath the field
and filtering as the user types:

- Ordered by last commit, most recent first, so live work is at the top.
- Each row: branch name, relative age, and where it is checked out if anywhere.
- The Branch field shows location; the From field does not, because a base has no
  availability constraint — branching *from* an in-use branch is legal.
- `Tab` / `Shift+Tab` keep moving between fields. `Up` / `Down` create and move a
  highlight in the list. `Enter` with a highlight takes that branch into the
  field; `Enter` with no highlight submits, so typing a new branch name and
  pressing Enter is unchanged.
- `Esc` clears a highlight if there is one, otherwise cancels the form.

## Code changes by package

- **`git`**: `Branches()` returns `[]Branch{Name, Updated, Worktree}` instead of
  `[]string`, built from one `for-each-ref --sort=-committerdate` plus one
  `worktree list --porcelain`. New `WorktreeFor(branch)` resolves a branch to its
  checkout path.
- **`workspace`**: `Options` gains no field; `addWorktree` reuses an existing
  worktree for the branch instead of asking git to create a second one, and drops
  the `ownedName` guard on *opening* while `RemoveWorktree` keeps it. `OpenSaved`
  loses the one-terminal-per-worktree refusal; `remember` stops deduplicating by
  worktree path. New `Location(p, branch)` returns the resolved destination and
  the terminals already there.
- **`ui`**: new `picker.go` — a list with items, filter, cursor and scroll window,
  unit-testable without Bubble Tea. `form.go` replaces the Worktree toggle with
  the Location line, holds a picker per text field, and confirms duplicate
  commands. The byte-slice truncation in `matchingBranches` is replaced by the
  rune-aware truncation used elsewhere.

## Invariants preserved

- tm creates worktrees only under `<root>/.terminal-worktrees/<project>/`, always
  running git from the project checkout, so every worktree is a flat sibling.
- tm removes only worktrees under that directory that no terminal uses.
- The project's own checkout is never modified.

## Edge cases

| Case | Behaviour |
| --- | --- |
| Branch checked out in the project checkout | Terminal opens in the checkout; no worktree made. |
| Branch in a foreign worktree | Terminal opens there; tm notes it did not create it. |
| Branch with a worktree whose directory is missing | Reported; tm offers to recreate it rather than silently making a second. |
| Empty branch field | Project checkout. |
| New branch, empty base | Refused, as today. |
| Duplicate command in the same directory | Confirmed once, then allowed. |
| Two terminals editing the same files | Permitted and not policed; the Location line makes the sharing visible. |

## Testing

- **git**: branches come back ordered by recency with occupancy resolved against
  real worktrees; `WorktreeFor` finds the checkout of an in-use branch and returns
  empty for a free one.
- **workspace**: a branch already in a worktree reuses it rather than creating a
  second; a second terminal in one worktree succeeds; two saved definitions for
  one worktree survive a save/restore round trip; `RemoveWorktree` still refuses
  a foreign path.
- **picker**: case-insensitive filtering, recency ordering, cursor clamping,
  window scrolling, `... N more` on overflow.
- **ui**: the Location line reports each of the four cases; a foreign worktree is
  labelled as such; `Down` highlights and `Enter` fills; `Enter` without a
  highlight still submits; a duplicate command asks once.

## Out of scope

- **Detecting processes tm did not start.** Scanning `/proc` for working
  directories inside a worktree would reveal foreign sessions, but it inspects
  processes tm has no relationship with. The honest "may be in use elsewhere" is
  enough.
- **Preventing two terminals from editing the same files.** That is the user's
  call, and identical to opening two editors.
- **Creating worktrees from inside a worktree.** tm always runs git from the
  project checkout; worktrees are flat siblings and stay that way.
