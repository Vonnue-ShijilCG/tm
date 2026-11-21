# Click to focus

Date: 2026-11-21. Status: approved (short design).

## Why

The user moves between panes and the sidebar with the mouse as often as with
the keys, and today a click does nothing. A click should move the highlight
to what was clicked, exactly as the keys do. Nothing more.

## What a click does

- **A terminal pane** (anywhere on its card): that pane takes focus, as
  `alt+j`/`alt+k` and `alt+arrows` do — accent border, cursor, typing goes
  there, and the focus is saved as part of the layout like those keys save it.
- **The sidebar**: the sidebar takes focus, as `alt+s` does. On a project row
  (any line of a wrapped name) the highlight moves to that project, as `j`/`k`
  would; nothing is opened, started or rerun — Enter still does that. On
  empty sidebar space the highlight stays where it was.
- The `↻ refresh` / `? keys` buttons and drag-to-copy keep working; a press
  that starts a drag focuses its pane first.

## Unchanged (anti-goals)

- Every key binding.
- Clicks are not forwarded to programs (Claude Code etc.).
- With the help screen, form, Terminal Manager or changes view open, clicks
  are ignored as now.

## Approaches

One sensible way: hit-test the press in `handleMouse` with the geometry
already used for the wheel (`paneAt`) and the sidebar's rows
(`visibleEntries`, which may scroll the list). Forwarding clicks to programs
was dropped: the user wants focus, not program input.

## Left to the builder

How the sidebar row hit-test reuses `visibleEntries` so a scrolled list maps
rows correctly.
