# Worktree branch base — design

Date: 2026-09-07
Status: approved for planning

## Problem

When a worktree terminal is created on a **new** branch, tm runs
`git worktree add -b <branch> <path>` with no start point, so git branches from
the `HEAD` of the project's own checkout. Three consequences:

- There is no way to start a branch from anywhere else — stacking a branch on
  the branch you are working in requires dropping to git by hand
  (`git branch <new> <base>`) and then creating the terminal, relying on the
  fact that tm checks out branches that already exist.
- The base is invisible. Nothing in the form says what the branch will start
  from.
- The base is not "main"; it is whatever the main checkout happens to have
  checked out. A stray checkout left in the project directory silently
  redirects every new branch, with no warning.

Creating a worktree on an **existing** branch is already correct and is not
changed by this design: `git worktree add <path> <branch>` checks that branch
out, so commits land on it and `git push` updates its existing pull request.

## Goal

Choose what a new branch starts from, default it to the repository's trunk by
name, show it, and refuse anything that does not exist.

## Decisions

| Decision | Choice | Why |
| --- | --- | --- |
| When the base is asked for | Only when the typed branch does not already exist | An existing branch has no base to choose; the form already distinguishes the two cases live, printing `(new branch)` or `(existing)`. |
| Default base | `main` if present, else `master`, else the project checkout's current branch | New work starts from trunk regardless of what the main checkout is on. Resolved by name, not by `HEAD`. |
| Unknown base | Refuse; create no worktree, no branch, no terminal | Silent fallback to `HEAD` is the trap being removed. |
| Accepted values | Anything `git rev-parse --verify` resolves: local branches, remote branches such as `origin/main`, tags, SHAs | Branching from a stale local `main` is a common mistake; accepting `origin/main` costs nothing extra. |

## Behaviour

Creating a worktree terminal:

1. The branch field behaves as today, with its live `(new branch)` / `(existing)`
   hint and branch autocompletion.
2. **Existing branch**: no base field is shown. The worktree checks that branch
   out. Unchanged from today.
3. **New branch**: a `From` field appears, pre-filled with the default branch and
   autocompleting against the branch list.
4. On submit, the base is resolved. If it does not resolve, the form shows an
   error and nothing is created.

The worktree toggle continues to default to on for Git projects.

## Code changes by package

- `git`
  - `AddWorktreeNewBranch(path, branch, base string)` gains the base and runs
    `git worktree add -b <branch> <path> <base>`.
  - New `DefaultBranch()`: `main`, else `master`, else the current branch.
  - New `HasRef(ref string) bool` using `rev-parse --verify --quiet`, alongside
    the existing `HasBranch`, so remote refs, tags and SHAs are accepted.
- `workspace`
  - `Options` gains `Base string`.
  - `addWorktree` validates the base **before** `os.MkdirAll`, so a rejected base
    leaves no directory behind, and passes it to `AddWorktreeNewBranch`.
  - The base is ignored when the branch already exists.
- `ui/form.go`
  - A `From` field, shown only while the typed branch does not exist, pre-filled
    from `DefaultBranch()`, autocompleted from the existing branch list, and
    reporting an unresolvable base as a form error.

The base is used once, at creation. It is not persisted: a saved definition
records the branch and worktree path, which is all that reopening needs.

## Edge cases

| Case | Behaviour |
| --- | --- |
| Base field cleared | Refused with an error; the base is explicit, never implied. |
| Base names the branch being created | git refuses; the error is surfaced in the form. |
| Existing branch typed | No base field, base ignored, current behaviour preserved. |
| Non-Git project | No worktree and therefore no base field. |
| Repo with neither `main` nor `master` | Pre-filled with the checkout's current branch. |
| Local `main` behind the remote | The user can type `origin/main`; tm does not fetch on their behalf. |

## Testing

- **git**: a worktree created with a base starts at that base's tip and not at
  `HEAD` (real temp repo, asserting the commit); `DefaultBranch` prefers `main`,
  falls back to `master`, then to the current branch; `HasRef` accepts a remote
  ref and rejects a typo.
- **workspace**: an unresolvable base refuses and leaves no worktree directory,
  no branch and no terminal; an existing branch ignores the base entirely.
- **ui**: the `From` field appears only for a new branch; submitting an unknown
  base shows an error and creates nothing; a valid base creates the terminal.

## Out of scope

- Fetching before branching. tm never touches the network on the user's behalf;
  `origin/main` uses whatever was last fetched.
- Changing a branch's base after creation.
- Choosing a base for an existing branch, which is meaningless.
