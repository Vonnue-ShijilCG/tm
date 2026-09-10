# Uncommitted changes viewer — design

Date: 2026-09-10
Status: draft for review

## Problem

An agent working in a tm pane changes files, and there is no way to see what it
changed without leaving what you are doing. `git diff` in a pane works, but it
shows a unified diff of the changed hunks only, it disturbs the pane you were
using, and it cannot show the old and new versions beside each other.

The gap is sharpest in the workflow tm now supports: several worktrees, each
with an agent editing files, each with its own uncommitted state.

## Goal

A full-screen view, one keystroke away, showing the uncommitted changes in the
directory you are already looking at: the files that changed on the left, and
the selected file rendered twice in full — as committed, and as it is now —
aligned line for line, additions green and removals red.

## What "uncommitted" means

Everything not in `HEAD`, which is three distinct things git reports separately:

| Change | Where it comes from |
| --- | --- |
| Unstaged edits | `git diff HEAD` |
| Staged edits | `git diff HEAD` (it covers both) |
| New, untracked files | **`git status --porcelain`** — invisible to every `git diff` |

That third row is the trap. `git diff`, `git diff --cached` and `git diff HEAD`
all ignore untracked files, so a file an agent just created shows nothing at
all. Verified by experiment. The viewer must union the diff with status and
present a new file as an all-additions right side with an empty left.

## Which directory

The one your attention is already on: the focused terminal's working directory,
or the selected project's path when focus is in the sidebar. `Terminal.Dir(p)`
already answers this. It is the only rule that works with worktrees — six
agents in six worktrees have six different sets of uncommitted changes, and the
project checkout's diff describes none of them.

## Layout

A full-screen modal, rendered the way the help screen is: it takes `m.width`
rather than the narrower main pane, so both code columns get the whole terminal.

```
 Uncommitted changes  WEB-VMS-WebApp  ⎇ EEPD-131800        3 files  +42 −11

 CHANGED FILES        │ HEAD  committed          WORKING TREE  yours now
                      │─────────────────────────────────────────────────────
 › Timeline.vue       │ 196   const props = ...  196   const props = ...
     src/components   │ 197 − ..camera: Camera   197 + ..camera: Camera | null
     +28 −4           │ 198   ..autoplay: bool   198   ..autoplay: bool
   usePlayer.ts       │                          199 + ..retryOnStall?: bool
     src/composables  │ 199   }>()               200   }>()
     +9 −7           │
```

The file list is permanent. Each entry shows the basename, its directory dimmed
beneath, and its `+`/`−` counts, so no path is truncated mid-word.

## Full file, not hunks

Both columns show the complete file, not just changed regions. The left comes
from `git show HEAD:<path>`, the right from the file on disk. The diff is used
only to learn which lines changed and where to insert blank rows so the two
sides stay in step: a line added on the right pads the left, a line removed on
the left pads the right.

Line numbers therefore diverge between the columns, and that is correct — each
side is numbered as its own version of the file is.

## Scrolling

Vertical and horizontal, both locked across the two columns, so you are always
comparing the same lines and the same columns of those lines.

Long lines scroll sideways rather than wrapping. Wrapping is incompatible with
this view: a line that wraps to two rows on one side but fits on one row on the
other pushes everything below it out of alignment, and the alignment is the
entire value of the layout.

`n` and `N` jump to the next and previous change. Without them the full-file
view is impractical — a single edit in a 2000-line file would have to be found
by scrolling. tm already knows every changed line's position from the diff.

## Keys

| Key | Action |
| --- | --- |
| `d` (sidebar) / `alt+d` (terminal) | open |
| `j` / `k` | previous / next file |
| `↑` / `↓` | scroll the file |
| `←` / `→` | scroll long lines |
| `n` / `N` | next / previous change |
| `r` | re-read from git |
| `esc` | close |

`alt+d` rather than a plain letter in terminal focus, because tm reserves only
`Esc` and `Alt` chords there — a plain letter belongs to the running program.
This mirrors `m` / `alt+m` for the Terminal Manager. All bindings go in
`ui/keys.go`, which feeds input matching, the help screen and the status bar.

## Refresh

Read on open and on `r`. Deliberately not live: an agent editing files
underneath would make the view shift while being read, and polling git for
every open modal costs work with no clear benefit.

## Read-only

No staging, no discarding, no editing. Each would need its own confirmation and
error handling, and all are one keystroke away in a real terminal.

## Edge cases

| Case | Behaviour |
| --- | --- |
| New (untracked) file | Empty left column; right shows the whole file as additions |
| Deleted file | Left shows the committed file; right column empty |
| Binary file | Listed with its status, content replaced by a note |
| Non-Git project | The view reports that the directory is not a git repository |
| No uncommitted changes | The view says so rather than opening an empty split |
| Very large file | Rendered; only the visible window is drawn, as panes already do |
| File deleted from disk but tracked | Treated as a deleted file |

## Code shape

- **`git`**: new `Status(dir)` returning changed paths with their staged and
  untracked state; `DiffHEAD(dir, path)` returning the unified diff for one
  file; `ShowHEAD(dir, path)` returning the committed content. The package has
  no diff support today.
- **New `internal/diff` package**: parses a unified diff into changed line
  ranges, and pairs a committed file and a working file into aligned rows. Pure
  functions over strings, no git and no UI, so it is testable on its own. This
  is the part with real logic and it earns its own package.
- **`ui`**: a `diffview.go` holding the modal's state — selected file, both
  scroll offsets, the aligned rows — rendered like the help screen. `keys.go`
  gains the bindings above.

## Out of scope

- **Word-level highlighting inside a changed line.** VS Code dims the unchanged
  parts of a changed line. It is a second alignment algorithm on top of the
  first, and the line-level colouring already answers "what changed".
- **Committed history.** This shows uncommitted work only. Browsing past
  commits is a different feature with different navigation.
- **Cross-worktree overview.** "What did all six agents change" is a summary,
  not a diff viewer, and deserves its own design.
