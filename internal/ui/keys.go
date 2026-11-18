package ui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the single source of truth for every shortcut the TUI handles.
// Input handling matches against these bindings and the help screen and
// status bar render their WithHelp text, so adding a binding here is enough
// for it to show up everywhere.
//
// Conflict policy for terminal mode (summarised under Limitations in the
// README): everything the user types reaches the program in the PTY —
// letters, digits, Tab and Shift+Tab (completion), Ctrl+C/D/Z/\, plain
// arrows, function keys, and a lone Esc. The application reserves only Alt
// chords while a terminal has focus. Alt chords were chosen because bash,
// zsh and fish leave them unbound by default; the known costs are Emacs'
// M-x/M-v and readline's M-n and M--. Plain letters such as s, m and z are
// application keys only in the sidebar and always PTY input in a terminal.
type keyMap struct {
	// Sidebar.
	Up         key.Binding
	Down       key.Binding
	Top        key.Binding
	Bottom     key.Binding
	Select     key.Binding
	New        key.Binding
	SplitSide  key.Binding
	SplitStack key.Binding
	Focus      key.Binding
	Manage     key.Binding
	Help       key.Binding
	Quit       key.Binding
	// With a tm server, q and ctrl+c leave instead (Leave is Quit's help
	// text then; input still matches Quit) and Q ends every terminal.
	Leave   key.Binding
	StopAll key.Binding

	// Terminal.
	Sidebar    key.Binding
	NextTerm   key.Binding
	PrevTerm   key.Binding
	NewTerm    key.Binding
	ManageTerm key.Binding
	HideTerm   key.Binding
	DeleteTerm key.Binding

	// Panes (terminal mode).
	PaneSide   key.Binding
	PaneStack  key.Binding
	PaneLeft   key.Binding
	PaneRight  key.Binding
	PaneUp     key.Binding
	PaneDown   key.Binding
	PaneGrow   key.Binding
	PaneShrink key.Binding
	PaneZoom   key.Binding
	Zoom       key.Binding // sidebar counterpart

	// New-terminal form.
	FormNext   key.Binding
	FormPrev   key.Binding
	FormSubmit key.Binding
	FormCancel key.Binding

	// Terminal manager.
	MgDown    key.Binding
	MgUp      key.Binding
	MgShow    key.Binding
	MgHide    key.Binding
	MgNew     key.Binding
	MgOpenAll key.Binding
	MgDelete  key.Binding
	MgClose   key.Binding

	// Confirmations (permanent deletion, worktree removal).
	Confirm key.Binding
	Cancel  key.Binding

	// Uncommitted changes view. Diff opens it from the sidebar, DiffTerm from
	// a terminal (an Alt chord, like every other terminal-mode key). Inside
	// the view the keys only ever move the file list: the two code columns
	// are scrolled with the wheel (DiffScroll), which is why there is no key
	// that moves them a row at a time and no focus to move between halves.
	// DiffNextChange and DiffPrevChange remain, because a wheel cannot find
	// the one edit in a thousand-line file.
	Diff           key.Binding
	DiffTerm       key.Binding
	DiffDown       key.Binding
	DiffUp         key.Binding
	DiffLeft       key.Binding
	DiffRight      key.Binding
	DiffNextChange key.Binding
	DiffPrevChange key.Binding
	DiffRefresh    key.Binding
	DiffClose      key.Binding
	DiffScroll     key.Binding // not a key: the wheel, listed for the help screen

	// Copy mode. Copy opens it on the focused terminal — an Alt chord, since
	// terminal focus reserves nothing else — and inside it the keys are plain,
	// because copy mode consumes every key like the other modal views. Moving
	// the cursor reuses Up and Down rather than adding bindings of its own:
	// they are the same keys meaning the same thing, and the help screen has
	// no room for a line that would only repeat "j / ↓  next line".
	Copy       key.Binding
	CopySelect key.Binding
	CopyYank   key.Binding
	CopyClose  key.Binding

	// Help screen.
	HelpClose key.Binding

	// Mouse. Not a key: never matched against input, listed on the help
	// screen only (see handleMouse).
	Scroll key.Binding
}

var keys = keyMap{
	Up:         key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k / ↑", "prev project")),
	Down:       key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j / ↓", "next project")),
	Top:        key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g / home", "first project")),
	Bottom:     key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G / end", "last project")),
	Select:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open/resume")),
	New:        key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new terminal")),
	SplitSide:  key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "split beside")),
	SplitStack: key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "split below")),
	Focus:      key.NewBinding(key.WithKeys("tab", "l", "right"), key.WithHelp("tab / l", "to terminal")),
	Manage:     key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "manage terminals")),
	Help:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "help")),
	Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q/ctrl+c", "quit")),
	Leave:      key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q/ctrl+c", "leave (terminals keep running)")),
	StopAll:    key.NewBinding(key.WithKeys("Q"), key.WithHelp("Q", "stop all")),

	Sidebar:    key.NewBinding(key.WithKeys("alt+s"), key.WithHelp("alt+s", "to sidebar")),
	NextTerm:   key.NewBinding(key.WithKeys("alt+j"), key.WithHelp("alt+j", "next terminal")),
	PrevTerm:   key.NewBinding(key.WithKeys("alt+k"), key.WithHelp("alt+k", "prev terminal")),
	NewTerm:    key.NewBinding(key.WithKeys("alt+n"), key.WithHelp("alt+n", "new terminal")),
	ManageTerm: key.NewBinding(key.WithKeys("alt+m"), key.WithHelp("alt+m", "manage terminals")),
	HideTerm:   key.NewBinding(key.WithKeys("alt+x"), key.WithHelp("alt+x", "hide terminal")),
	DeleteTerm: key.NewBinding(key.WithKeys("alt+delete"), key.WithHelp("alt+del", "delete forever")),

	PaneSide:   key.NewBinding(key.WithKeys("alt+v"), key.WithHelp("alt+v", "split beside")),
	PaneStack:  key.NewBinding(key.WithKeys("alt+h"), key.WithHelp("alt+h", "split below")),
	PaneLeft:   key.NewBinding(key.WithKeys("alt+left"), key.WithHelp("alt+←", "pane left")),
	PaneRight:  key.NewBinding(key.WithKeys("alt+right"), key.WithHelp("alt+→", "pane right")),
	PaneUp:     key.NewBinding(key.WithKeys("alt+up"), key.WithHelp("alt+↑", "pane up")),
	PaneDown:   key.NewBinding(key.WithKeys("alt+down"), key.WithHelp("alt+↓", "pane down")),
	PaneGrow:   key.NewBinding(key.WithKeys("alt+="), key.WithHelp("alt+=", "grow pane")),
	PaneShrink: key.NewBinding(key.WithKeys("alt+-"), key.WithHelp("alt+-", "shrink pane")),
	PaneZoom:   key.NewBinding(key.WithKeys("alt+z"), key.WithHelp("alt+z", "zoom pane")),
	Zoom:       key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "zoom terminal")),

	FormNext:   key.NewBinding(key.WithKeys("tab", "down"), key.WithHelp("tab / ↓", "next field")),
	FormPrev:   key.NewBinding(key.WithKeys("shift+tab", "up"), key.WithHelp("shift+tab", "prev field")),
	FormSubmit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "create")),
	FormCancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),

	MgDown:    key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j / ↓", "next row")),
	MgUp:      key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k / ↑", "prev row")),
	MgShow:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "show / restore")),
	MgHide:    key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "hide")),
	MgNew:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new terminal")),
	MgOpenAll: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "restore all")),
	MgDelete:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete forever")),
	MgClose:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),

	Confirm: key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm")),
	Cancel:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc/other", "cancel")),

	Diff:           key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "from sidebar")),
	DiffTerm:       key.NewBinding(key.WithKeys("alt+d"), key.WithHelp("alt+d", "from terminal")),
	DiffDown:       key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j / ↓", "next file")),
	DiffUp:         key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k / ↑", "prev file")),
	DiffLeft:       key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "scroll left")),
	DiffRight:      key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "scroll right")),
	DiffNextChange: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next change")),
	DiffPrevChange: key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "prev change")),
	DiffRefresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "re-read")),
	DiffClose:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close")),
	DiffScroll:     key.NewBinding(key.WithHelp("wheel", "scroll diff")),

	Copy:       key.NewBinding(key.WithKeys("alt+c"), key.WithHelp("alt+c", "copy mode")),
	CopySelect: key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "select lines")),
	CopyYank:   key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("y", "copy, close")),
	CopyClose:  key.NewBinding(key.WithKeys("esc", "q"), key.WithHelp("esc", "close")),

	HelpClose: key.NewBinding(key.WithKeys("esc", "s", "q"), key.WithHelp("esc / q", "close help")),

	Scroll: key.NewBinding(key.WithHelp("wheel", "scroll back")),
}

// helpSection groups bindings by the context in which they apply.
type helpSection struct {
	Title    string
	Bindings []key.Binding
}

// helpSections lists every binding for the help screen, grouped by context.
// The order decides how viewHelp packs them into columns, and the screen has
// no slack: CHANGES sits between PANES and NEW TERMINAL, and COPY between
// CHANGES and NEW TERMINAL, because those are the places their rows fit
// without squeezing a column narrow enough to truncate another section's
// labels. Moving a section, or adding one, means checking the screen at 100x30
// again.
//
// helpSections is every binding, whichever screen shows it; helpSectionsFor
// is the screen itself, which says what q does and lists Q only when a tm
// server holds the terminals.
func helpSections() []helpSection {
	s := helpSectionsFor(false)
	s[0].Bindings = append(s[0].Bindings, keys.Leave, keys.StopAll)
	return s
}

func helpSectionsFor(server bool) []helpSection {
	k := keys
	quit := []key.Binding{k.Quit}
	if server {
		quit = []key.Binding{k.Leave, k.StopAll}
	}
	return []helpSection{
		{"SIDEBAR", append([]key.Binding{k.Down, k.Up, k.Top, k.Bottom, k.Select, k.New, k.SplitSide, k.SplitStack, k.Focus, k.Zoom, k.Manage, k.Help}, quit...)},
		{"TERMINAL", []key.Binding{k.Sidebar, k.NextTerm, k.PrevTerm, k.NewTerm, k.ManageTerm, k.HideTerm, k.DeleteTerm}},
		{"PANES", []key.Binding{k.PaneSide, k.PaneStack, k.PaneLeft, k.PaneRight, k.PaneUp, k.PaneDown, k.PaneGrow, k.PaneShrink, k.PaneZoom}},
		{"CHANGES", []key.Binding{k.Diff, k.DiffTerm, k.DiffScroll, k.DiffDown, k.DiffUp, k.DiffLeft, k.DiffRight, k.DiffNextChange, k.DiffPrevChange, k.DiffRefresh, k.DiffClose}},
		{"COPY", []key.Binding{k.Copy, k.CopySelect, k.CopyYank, k.CopyClose}},
		{"NEW TERMINAL", []key.Binding{k.FormNext, k.FormPrev, k.FormSubmit, k.FormCancel}},
		{"MANAGE TERMINALS", []key.Binding{k.MgDown, k.MgUp, k.MgShow, k.MgHide, k.MgNew, k.MgOpenAll, k.MgDelete, k.MgClose}},
		{"CONFIRM", []key.Binding{k.Confirm, k.Cancel}},
		{"MOUSE", []key.Binding{k.Scroll}},
		{"HELP", []key.Binding{k.HelpClose}},
	}
}

// Status-bar subsets: short enough to fit on one line.
func (k keyMap) sidebarHelp() []key.Binding {
	return []key.Binding{k.Select, k.New, k.Manage, k.Help, k.Quit}
}

func (k keyMap) terminalHelp() []key.Binding {
	return []key.Binding{k.Sidebar, k.NextTerm, k.HideTerm}
}

func (k keyMap) copyHelp() []key.Binding {
	return []key.Binding{k.Down, k.Up, k.CopySelect, k.CopyYank, k.CopyClose}
}

func (k keyMap) formHelp() []key.Binding {
	return []key.Binding{k.FormSubmit, k.FormNext, k.FormCancel}
}

func (k keyMap) managerHelp() []key.Binding {
	return []key.Binding{k.MgShow, k.MgHide, k.MgNew, k.MgOpenAll, k.MgDelete, k.MgClose}
}

// diffHelp leads with the wheel: it is the only way to scroll the columns a
// row at a time, and unlike a key it cannot be discovered by pressing things.
func (k keyMap) diffHelp() []key.Binding {
	return []key.Binding{k.DiffScroll, k.DiffDown, k.DiffUp, k.DiffNextChange, k.DiffRefresh, k.DiffClose}
}
