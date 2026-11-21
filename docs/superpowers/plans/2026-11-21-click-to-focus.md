# Click to Focus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A left click on a pane focuses it, and a left click on the sidebar focuses the sidebar and moves its highlight to the clicked project — the mouse doing what the keys already do.

**Architecture:** One new step in `handleMouse` (`internal/ui/scroll.go`), before drag selection: hit-test the press against the sidebar (with the same row arithmetic `viewSidebarList` draws) and the panes (`paneAt`), then reuse `selectTerminal` / `m.focus` exactly as the key handlers do. No new state.

**Tech Stack:** Go, Bubble Tea v1 (`tea.MouseMsg`), Lip Gloss.

**Spec:** `docs/superpowers/specs/2026-11-21-click-to-focus-design.md`

## Global Constraints

- No key binding changes.
- Clicks are never forwarded to programs.
- With help, form, Terminal Manager or changes view open, clicks are ignored (already true in `handleMouse`).
- A click never opens, starts or reruns a terminal.
- Go is at `~/.local/go/bin`; run `gofmt`; commits carry no co-author trailer.

## Review Focus

1. A wrapped project name (two sidebar lines): a click on either line selects that project.
2. A sidebar list scrolled because the cursor is near the end of a long list: a row maps to the project drawn there, not to index = row.
3. A drag that starts in an unfocused pane still copies only that pane's text, and leaves that pane focused.
4. A click on the sidebar's button row or the blank row above it does not move the highlight.
5. A click in zoom mode lands on the one visible pane only.

---

### Task 1: Click a pane to focus it

**Files:**
- Create: `internal/ui/clickfocus.go`
- Modify: `internal/ui/scroll.go` (`handleMouse`, after the sidebar-button switch, before `handleSelect`)
- Test: `internal/ui/clickfocus_test.go`

**Interfaces:**
- Produces: `func (m *Model) clickFocus(msg tea.MouseMsg)` — acts only on a left-button press; Task 2 extends it.
- Consumes: `paneAt(p, x, y)`, `selectTerminal(p, t)`, `applyLayout(p)`, `persist()`, `mouse`/`drag`/`find`/`selectPanes` test helpers from `mouseselect_test.go`.

- [ ] **Step 1: Write the failing tests** in `clickfocus_test.go`:
  - `TestClickingAPaneFocusesIt`: `selectPanes(t, {"LEFT"}, {"RIGHT"})` (right pane focused after the split), `alt+s` to the sidebar, click on `find("LEFT")`; assert `m.focus == focusMain`, `p.ActiveTerminal() == p.Terminals[0]`, and typing `x` reaches session 0's `text`.
  - `TestDragFromAnUnfocusedPaneFocusesAndCopiesIt`: right pane focused; drag over `"LEFT"`; assert terminal 0 focused and clipboard holds `"LEFT"` only.
- [ ] **Step 2: Run** `go test ./internal/ui -run 'Click|DragFrom'` — expect FAIL (focus unchanged).
- [ ] **Step 3: Implement `clickFocus`** for panes: on a left press over a pane, `selectTerminal`, `m.focus = focusMain`, `applyLayout(p)`, and `persist()` only if the active terminal changed; call it from `handleMouse` before `handleSelect`.
- [ ] **Step 4: Run** the same command — expect PASS; then `go test ./internal/ui`.
- [ ] **Step 5: Commit** `Focus a pane when it is clicked`.

### Task 2: Click the sidebar to focus it and move its highlight

**Files:**
- Modify: `internal/ui/clickfocus.go`, `internal/ui/model.go` (`viewSidebarList`), `internal/ui/scrolllist.go`
- Test: `internal/ui/clickfocus_test.go`

**Interfaces:**
- Produces: `func visibleStart(entries [][]string, selected, height int) int` in `scrolllist.go` (the first entry `visibleEntries` draws; `visibleEntries` uses it), and `func (m Model) sidebarProjectAt(y int) int` (project index drawn on screen row y, or -1).
- Consumes: Task 1's `clickFocus`.

- [ ] **Step 1: Write the failing tests:**
  - `TestClickingTheSidebarFocusesItAndMovesTheHighlight`: from terminal focus, click on `find("mobile")`; assert `m.focus == focusSidebar`, `m.ws.Projects[m.cursor].Name == "mobile"`, no new session spawned.
  - `TestClickingEmptySidebarSpaceKeepsTheHighlight`: click a blank sidebar row below the list and the blank row above the buttons; focus is the sidebar, cursor unchanged.
  - `TestClickingEitherLineOfAWrappedNameSelectsIt`: a project whose name wraps (longer than 22 cells); click its second line; cursor on it.
  - `TestClickingAScrolledSidebarPicksTheProjectDrawnThere`: 40 discovered projects in a 100x12 window, cursor at the last; click the row of a visible project; cursor on that project.
- [ ] **Step 2: Run** `go test ./internal/ui -run 'Sidebar'` — expect FAIL.
- [ ] **Step 3: Implement:** extract the per-project wrapped lines from `viewSidebarList` into a helper both use; `visibleStart` out of `visibleEntries`; `sidebarProjectAt` maps a screen row (sidebar border + title + blank = first list row) through `visibleStart` and the line counts, within the list height `viewSidebar` gives it; in `clickFocus`, a left press with `x < sidebarWidth` sets `m.focus = focusSidebar` and, when a project is hit, `m.cursor`.
- [ ] **Step 4: Run** `go test ./internal/ui`, then `make check` — expect all PASS.
- [ ] **Step 5: Update docs** — CLAUDE.md "Scrollback" says the mouse is used only for the wheel: add clicks-to-focus there, and a line in README's key list. **Commit** `Focus the sidebar and move its highlight when it is clicked`.

### Task 3: Real-binary check and local install

- [ ] **Step 1:** Extend the scratch probe (`scratchpad/manual/select_probe.py`) with: click the unfocused left pane, type `echo CLICKED`, assert it appears; click the sidebar's `web` row and press `j`/Enter-free checks via the status bar (`enter open/resume` shown means sidebar focus). Run with server off and on — expect PASS.
- [ ] **Step 2:** `make build`, install to `~/.local/bin/tm`. Do not push.
