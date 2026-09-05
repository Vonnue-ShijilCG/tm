package ui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the single source of truth for every shortcut the TUI handles.
// Input handling matches against these bindings and the help screen and
// status bar render their WithHelp text, so adding a binding here is enough
// for it to show up everywhere.
//
// Conflict policy for terminal mode (see also the README): everything the
// user types reaches the program in the PTY — letters, digits, Tab and
// Shift+Tab (completion), Ctrl+C/D/Z/\, plain arrows, function keys. The
// application reserves exactly two kinds of key while a terminal has focus:
// a lone Esc (back to the sidebar) and a small set of Alt chords. Alt chords
// were chosen because bash, zsh and fish leave them unbound by default; the
// known costs are documented in the README (Emacs' M-x/M-v, readline's M-n
// and M--, a lone Esc in Vim/less/Claude Code). Plain letters such as s, m
// and z are application keys only in the sidebar and always PTY input in a
// terminal.
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
	FormToggle key.Binding
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

	// Help screen.
	HelpClose key.Binding
}

var keys = keyMap{
	Up:         key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k / ↑", "prev project")),
	Down:       key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j / ↓", "next project")),
	Top:        key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "first project")),
	Bottom:     key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "last project")),
	Select:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open/resume")),
	New:        key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new terminal")),
	SplitSide:  key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "split beside")),
	SplitStack: key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "split below")),
	Focus:      key.NewBinding(key.WithKeys("tab", "l", "right"), key.WithHelp("tab", "to terminal")),
	Manage:     key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "manage terminals")),
	Help:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "help")),
	Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q/ctrl+c", "quit")),

	Sidebar:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "to sidebar")),
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

	FormNext:   key.NewBinding(key.WithKeys("tab", "down"), key.WithHelp("tab", "next field")),
	FormPrev:   key.NewBinding(key.WithKeys("shift+tab", "up"), key.WithHelp("shift+tab", "prev field")),
	FormToggle: key.NewBinding(key.WithKeys(" ", "left", "right"), key.WithHelp("space", "worktree y/n")),
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

	HelpClose: key.NewBinding(key.WithKeys("esc", "s", "q"), key.WithHelp("esc", "close help")),
}

// helpSection groups bindings by the context in which they apply.
type helpSection struct {
	Title    string
	Bindings []key.Binding
}

// helpSections lists every binding for the help screen, grouped by context.
func helpSections() []helpSection {
	k := keys
	return []helpSection{
		{"SIDEBAR", []key.Binding{k.Down, k.Up, k.Top, k.Bottom, k.Select, k.New, k.SplitSide, k.SplitStack, k.Focus, k.Zoom, k.Manage, k.Help, k.Quit}},
		{"TERMINAL", []key.Binding{k.Sidebar, k.NextTerm, k.PrevTerm, k.NewTerm, k.ManageTerm, k.HideTerm, k.DeleteTerm}},
		{"PANES", []key.Binding{k.PaneSide, k.PaneStack, k.PaneLeft, k.PaneRight, k.PaneUp, k.PaneDown, k.PaneGrow, k.PaneShrink, k.PaneZoom}},
		{"NEW TERMINAL", []key.Binding{k.FormNext, k.FormPrev, k.FormToggle, k.FormSubmit, k.FormCancel}},
		{"MANAGE TERMINALS", []key.Binding{k.MgDown, k.MgUp, k.MgShow, k.MgHide, k.MgNew, k.MgOpenAll, k.MgDelete, k.MgClose}},
		{"CONFIRM", []key.Binding{k.Confirm, k.Cancel}},
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

func (k keyMap) formHelp() []key.Binding {
	return []key.Binding{k.FormSubmit, k.FormNext, k.FormToggle, k.FormCancel}
}

func (k keyMap) managerHelp() []key.Binding {
	return []key.Binding{k.MgShow, k.MgHide, k.MgNew, k.MgOpenAll, k.MgDelete, k.MgClose}
}
