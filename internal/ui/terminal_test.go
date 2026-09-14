package ui

import (
	"fmt"
	"hash/fnv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// fakeSession records what the UI sends to it instead of talking to a PTY.
type fakeSession struct {
	dir        string
	cols, rows int
	keys       []uv.KeyPressEvent
	text       []string
	title      string
	history    int  // lines of scrollback the fake claims to have
	alt        bool // the fake program is on the alternate screen
	mouse      bool // the fake program has enabled mouse tracking
	wheels     []wheelCall
	closed     bool
	renders    int  // times Render was called, for the pane-cache tests
	grounded   bool // Render paints its ground the way a real Session does, for tests that read every cell
	output     chan struct{}
	done       chan struct{}
	err        error
}

func newFakeSession(dir string, cols, rows int) *fakeSession {
	return &fakeSession{dir: dir, cols: cols, rows: rows, output: make(chan struct{}, 1), done: make(chan struct{})}
}

func (f *fakeSession) SendKey(k uv.KeyPressEvent) { f.keys = append(f.keys, k) }
func (f *fakeSession) SendText(t string)          { f.text = append(f.text, t) }
func (f *fakeSession) Resize(c, r int)            { f.cols, f.rows = c, r }
func (f *fakeSession) Render(bool) string {
	f.renders++
	screen := "SCREEN:" + f.dir + ":" + f.title
	if f.grounded {
		return textStyle.Render(screen)
	}
	return screen
}
func (f *fakeSession) RenderScrolled(off int) string {
	return fmt.Sprintf("SCROLLED:%d:%s", off, f.dir)
}

// Version must move whenever a test changes what Render would return, or the
// pane cache serves the old card and the test reads a stale screen. Hashing
// the fields Render reads keeps that true without every test remembering to
// bump a counter.
func (f *fakeSession) Version() uint64 {
	h := fnv.New64a()
	fmt.Fprint(h, f.dir, f.title, f.grounded, f.history, f.alt, f.closed)
	return h.Sum64()
}
func (f *fakeSession) ScrollbackLen() int  { return f.history }
func (f *fakeSession) IsAltScreen() bool   { return f.alt }
func (f *fakeSession) MouseTracking() bool { return f.mouse }
func (f *fakeSession) SendWheel(up bool, x, y int) {
	f.wheels = append(f.wheels, wheelCall{up, x, y})
}

// wheelCall is one SendWheel the UI made on a fake session.
type wheelCall struct {
	up   bool
	x, y int
}

func (f *fakeSession) Title() string           { return f.title }
func (f *fakeSession) Output() <-chan struct{} { return f.output }
func (f *fakeSession) Done() <-chan struct{}   { return f.done }
func (f *fakeSession) Err() error              { return f.err }
func (f *fakeSession) Close() error            { f.closed = true; close(f.done); return nil }

// newTestModel wires a Model to fake sessions and reports every spawn.
func newTestModel(t *testing.T) (Model, *[]*fakeSession) {
	t.Helper()
	spawned := &[]*fakeSession{}
	root := t.TempDir() // real directory so state saves succeed; project paths stay fake
	m := New(root, testProjects())
	m.ws = workspace.New(root, testProjects(), func(dir string, cols, rows int) (workspace.Session, error) {
		f := newFakeSession(dir, cols, rows)
		f.title = "t" + string(rune('0'+len(*spawned)+1))
		*spawned = append(*spawned, f)
		return f, nil
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model), spawned
}

func TestEnterStartsShellInProjectDirAndFocusesTerminal(t *testing.T) {
	m, spawned := newTestModel(t)

	m = press(m, "j", "enter")

	if len(*spawned) != 1 {
		t.Fatalf("spawned %d sessions, want 1", len(*spawned))
	}
	s := (*spawned)[0]
	if s.dir != "/ws/ecommerce" {
		t.Errorf("shell dir = %q, want /ws/ecommerce", s.dir)
	}
	area := m.mainArea() // a single pane fills the main area, minus its card border and header row
	if s.cols != sessionCols(area) || s.rows != sessionRows(area) || area.w <= 0 || area.h <= 1 {
		t.Errorf("session size %dx%d, want %dx%d", s.cols, s.rows, sessionCols(area), sessionRows(area))
	}
	if m.focus != focusMain {
		t.Error("terminal should be focused after enter")
	}
	if !strings.Contains(m.View(), "SCREEN:/ws/ecommerce") {
		t.Errorf("view should render the session screen:\n%s", m.View())
	}
}

func TestEnterReturnsToExistingTerminal(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+s", "enter")
	if len(*spawned) != 1 {
		t.Fatalf("re-entering a project spawned %d sessions, want 1", len(*spawned))
	}
	if m.focus != focusMain {
		t.Error("enter on a project with a terminal should focus it")
	}
	m = press(m, "alt+s", "j", "enter")
	if len(*spawned) != 2 || (*spawned)[1].dir != "/ws/ecommerce" {
		t.Fatalf("second project should get its own session, got %d", len(*spawned))
	}
}

func TestNewTerminalFromSidebarAndFromAltN(t *testing.T) {
	m, spawned := newTestModel(t)

	m = press(m, "j", "n", "enter") // sidebar: new terminal for highlighted project
	m = press(m, "alt+n", "enter")  // terminal focused: alt+n adds to the same project
	m = press(m, "alt+n", "enter")

	if len(*spawned) != 3 {
		t.Fatalf("spawned %d sessions, want 3", len(*spawned))
	}
	p := m.ws.Projects[1]
	if len(p.Terminals) != 3 {
		t.Fatalf("ecommerce has %d terminals, want 3", len(p.Terminals))
	}
	for i, s := range *spawned {
		if s.dir != "/ws/ecommerce" {
			t.Errorf("terminal %d dir = %q, want /ws/ecommerce", i, s.dir)
		}
		for j, other := range *spawned {
			if i != j && s == other {
				t.Errorf("terminals %d and %d share a session", i, j)
			}
		}
	}
	if p.ActiveTerminal() != p.Terminals[2] {
		t.Error("the newest terminal should be active")
	}
	if len(m.ws.Projects[0].Terminals) != 0 {
		t.Error("other projects must not gain terminals")
	}
}

func TestAltJKSwitchTerminalsAndWrap(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter", "alt+n", "enter") // 3 terminals, active = 3
	p := m.ws.Projects[0]

	m = press(m, "alt+j") // wraps from 3 to 1
	if p.ActiveIndex() != 0 {
		t.Errorf("after alt+j active = %d, want 0", p.ActiveIndex())
	}
	m = press(m, "alt+j", "alt+j")
	if p.ActiveIndex() != 2 {
		t.Errorf("after two more alt+j active = %d, want 2", p.ActiveIndex())
	}
	m = press(m, "alt+k", "alt+k", "alt+k") // 3 -> 2 -> 1 -> wraps to 3
	if p.ActiveIndex() != 2 {
		t.Errorf("alt+k should wrap backwards, active = %d, want 2", p.ActiveIndex())
	}
	m = press(m, "alt+k")
	if p.ActiveIndex() != 1 {
		t.Errorf("after alt+k active = %d, want 1", p.ActiveIndex())
	}
	if m.focus != focusMain {
		t.Error("switching terminals must keep the terminal focused")
	}

	// Typing goes only to the active terminal.
	m = press(m, "z")
	if got := strings.Join((*spawned)[1].text, ""); got != "z" {
		t.Errorf("active terminal received %q, want \"z\"", got)
	}
	for _, i := range []int{0, 2} {
		if len((*spawned)[i].text) != 0 {
			t.Errorf("inactive terminal %d received %q", i, (*spawned)[i].text)
		}
	}
	if !strings.Contains(m.View(), "SCREEN:/ws/backend:t2") {
		t.Errorf("view should show the active terminal's screen:\n%s", m.View())
	}
}

func TestSwitchingWithOneTerminalKeepsFocus(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "alt+j", "alt+k")
	p := m.ws.Projects[0]
	if m.focus != focusMain || p.ActiveIndex() != 0 {
		t.Errorf("with one terminal switching should be a no-op (focus=%v active=%d)", m.focus, p.ActiveIndex())
	}
}

func TestTabAndShiftTabAreForwardedToTerminal(t *testing.T) {
	// Tab is the shell's completion key and Shift+Tab is bound by zsh, so
	// inside a terminal both must reach the program instead of switching.
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter")
	p := m.ws.Projects[0]
	active := (*spawned)[1]

	m = press(m, "tab", "shift+tab")
	if p.ActiveIndex() != 1 {
		t.Errorf("tab must not switch terminals, active = %d", p.ActiveIndex())
	}
	want := []uv.KeyPressEvent{{Code: uv.KeyTab}, {Code: uv.KeyTab, Mod: uv.ModShift}}
	if len(active.keys) != 2 || active.keys[0] != want[0] || active.keys[1] != want[1] {
		t.Errorf("terminal should receive tab and shift+tab, got %+v", active.keys)
	}
}

func TestOldPrefixIsGone(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter")
	s := (*spawned)[0]

	m = press(m, "ctrl+\\", "n", "ctrl+\\", "x", "ctrl+\\", "s")
	if len(*spawned) != 1 || s.closed || m.focus != focusMain {
		t.Errorf("ctrl+\\ sequences must be plain input: spawned=%d closed=%v focus=%v", len(*spawned), s.closed, m.focus)
	}
	if got := strings.Join(s.text, ""); got != "nxs" {
		t.Errorf("shell received text %q, want \"nxs\"", got)
	}
	ctrlBackslash := uv.KeyPressEvent{Code: '\\', Mod: uv.ModCtrl}
	if len(s.keys) != 3 || s.keys[0] != ctrlBackslash {
		t.Errorf("each ctrl+\\ should reach the shell literally, got %+v", s.keys)
	}
}

func TestKeysGoToTerminalWhenFocused(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter")
	s := (*spawned)[0]

	// Sidebar keys must not be interpreted while the terminal has focus.
	m = press(m, "j", "k", "g", "G", "q", "n", "x", "1", "enter")
	if m.cursor != 0 || len(*spawned) != 1 || s.closed {
		t.Errorf("sidebar keys leaked: cursor=%d spawned=%d closed=%v", m.cursor, len(*spawned), s.closed)
	}
	if got := strings.Join(s.text, ""); got != "jkgGqnx1" {
		t.Errorf("shell received text %q, want \"jkgGqnx1\"", got)
	}
	if len(s.keys) != 1 || s.keys[0].Code != uv.KeyEnter {
		t.Errorf("shell should have received an enter key, got %+v", s.keys)
	}

	// Control keys and arrows are passed through rather than handled here.
	for _, tc := range []struct {
		msg  tea.KeyMsg
		want uv.KeyPressEvent
	}{
		{tea.KeyMsg{Type: tea.KeyCtrlC}, uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl}},
		{tea.KeyMsg{Type: tea.KeyCtrlD}, uv.KeyPressEvent{Code: 'd', Mod: uv.ModCtrl}},
		{tea.KeyMsg{Type: tea.KeyCtrlZ}, uv.KeyPressEvent{Code: 'z', Mod: uv.ModCtrl}},
		{tea.KeyMsg{Type: tea.KeyUp}, uv.KeyPressEvent{Code: uv.KeyUp}},
		{tea.KeyMsg{Type: tea.KeyLeft}, uv.KeyPressEvent{Code: uv.KeyLeft}},
	} {
		next, cmd := m.Update(tc.msg)
		m = next.(Model)
		if cmd != nil {
			t.Errorf("%s in the terminal must not produce a command (e.g. quit)", tc.msg)
		}
		if last := s.keys[len(s.keys)-1]; last != tc.want {
			t.Errorf("shell should receive %+v for %s, got %+v", tc.want, tc.msg, last)
		}
	}
	if m.focus != focusMain {
		t.Error("normal input must not change focus")
	}
}

func TestAltSReturnsToSidebarAndTabComesBack(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+s")
	if m.focus != focusSidebar {
		t.Fatal("alt+s should return focus to the sidebar")
	}
	if len((*spawned)[0].keys) != 0 {
		t.Errorf("alt+s must not be forwarded to the shell, got %+v", (*spawned)[0].keys)
	}
	m = press(m, "j")
	if m.cursor != 1 {
		t.Fatal("sidebar navigation should work again after leaving the terminal")
	}
	m = press(m, "k", "tab")
	if m.focus != focusMain {
		t.Fatal("tab from the sidebar should focus the terminal")
	}
}

func TestResizePropagatesToAllTerminals(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter", "alt+s", "j", "enter")

	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	if len(*spawned) != 3 {
		t.Fatalf("expected 3 terminals, got %d", len(*spawned))
	}
	// Every visible terminal is sized to its own pane (minus the header row).
	for _, p := range m.ws.Projects {
		for term, r := range m.paneRects(p) {
			s := term.Session.(*fakeSession)
			if s.cols != sessionCols(r) || s.rows != sessionRows(r) {
				t.Errorf("terminal %d size %dx%d, want pane %dx%d", term.Num, s.cols, s.rows, sessionCols(r), sessionRows(r))
			}
		}
	}
	// The selected project's single pane fills the whole area.
	area := m.mainArea()
	if s := (*spawned)[2]; s.cols != sessionCols(area) || s.rows != sessionRows(area) {
		t.Errorf("lone pane size %dx%d, want %dx%d", s.cols, s.rows, sessionCols(area), sessionRows(area))
	}
}

func TestAltXHidesOnlyActiveTerminalAndKeepsItRunning(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter", "alt+n", "enter") // active = terminal 3
	m = press(m, "alt+k")                                     // back to terminal 2
	m = press(m, "alt+x")
	p := m.ws.Projects[0]

	if (*spawned)[1].closed || (*spawned)[0].closed || (*spawned)[2].closed {
		t.Error("alt+x must not close any session")
	}
	if len(p.Terminals) != 3 || len(p.Saved) != 0 {
		t.Errorf("hidden terminal stays running (not saved): running=%d saved=%d", len(p.Terminals), len(p.Saved))
	}
	if m.layouts[p].contains(p.Terminals[1]) || !m.layouts[p].contains(p.Terminals[0]) || !m.layouts[p].contains(p.Terminals[2]) {
		t.Errorf("only terminal 2's pane should be gone: %v", m.layouts[p].leaves())
	}
	if m.focus != focusMain || p.ActiveTerminal() != p.Terminals[2] {
		t.Errorf("focus moves to the next visible terminal (3), got %d", p.ActiveTerminal().Num)
	}
	if v := m.View(); strings.Contains(v, "Terminal 2") || !strings.Contains(v, "Terminal 1") || !strings.Contains(v, "Terminal 3") {
		t.Errorf("only visible panes are drawn; the hidden terminal is reachable from the manager:\n%s", v)
	}

	// If the hidden shell later exits, it leaves the running list but stays restorable.
	_ = (*spawned)[1].Close()
	next, _ := m.Update(terminalExitedMsg{id: p.Terminals[1].ID})
	m = next.(Model)
	if len(p.Terminals) != 2 || p.Terminals[0].Num != 1 || p.Terminals[1].Num != 3 || len(p.Saved) != 1 {
		t.Errorf("remaining terminals wrong: %+v saved=%d", p.Terminals, len(p.Saved))
	}
	if m.focus != focusMain || p.ActiveTerminal() == nil {
		t.Error("focus should stay on the remaining terminals")
	}
}

func TestLastTerminalExitReturnsToSidebarWithNotice(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter")
	id := m.ws.Projects[0].Terminals[0].ID

	next, _ := m.Update(terminalExitedMsg{id: id, err: nil})
	m = next.(Model)

	if m.focus != focusSidebar {
		t.Error("focus should return to the sidebar when the last terminal exits")
	}
	if !strings.Contains(m.View(), "terminal 1 exited") {
		t.Errorf("view should mention the exit:\n%s", m.View())
	}
	m = press(m, "enter")
	if len(*spawned) != 2 || m.ws.Projects[0].Terminals[0].Num != 2 {
		t.Error("enter after exit should start a fresh terminal numbered 2")
	}
}

func TestQuitClosesAllTerminals(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter", "alt+s", "j", "enter", "alt+s")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q in sidebar should quit")
	}
	for i, s := range *spawned {
		if !s.closed {
			t.Errorf("quitting should close terminal %d", i)
		}
	}
}

func TestViewShowsTabsAndCounts(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter", "alt+n", "enter")
	view := m.View()
	for _, want := range []string{"Terminal 1", "Terminal 2", "Terminal 3", "● backend", "backend · Terminal 3", "alt+x"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestKeyTranslation(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
		text string            // expected SendText payload, "" if none
		key  *uv.KeyPressEvent // expected SendKey payload, nil if none
	}{
		{"rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}, "x", nil},
		{"paste", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ls -la"), Paste: true}, "ls -la", nil},
		{"alt+rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true}, "\x1bf", nil},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, " ", nil},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, "", &uv.KeyPressEvent{Code: uv.KeyEnter}},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, "", &uv.KeyPressEvent{Code: uv.KeyBackspace}},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, "", &uv.KeyPressEvent{Code: uv.KeyUp}},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, "", &uv.KeyPressEvent{Code: uv.KeyEscape}},
		{"ctrl+a", tea.KeyMsg{Type: tea.KeyCtrlA}, "", &uv.KeyPressEvent{Code: 'a', Mod: uv.ModCtrl}},
		{"ctrl+z", tea.KeyMsg{Type: tea.KeyCtrlZ}, "", &uv.KeyPressEvent{Code: 'z', Mod: uv.ModCtrl}},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, "", &uv.KeyPressEvent{Code: uv.KeyTab}},
		{"shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab}, "", &uv.KeyPressEvent{Code: uv.KeyTab, Mod: uv.ModShift}},
		{"f5", tea.KeyMsg{Type: tea.KeyF5}, "", &uv.KeyPressEvent{Code: uv.KeyF5}},
		{"delete", tea.KeyMsg{Type: tea.KeyDelete}, "", &uv.KeyPressEvent{Code: uv.KeyDelete}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeSession("", 1, 1)
			forwardKey(f, tc.msg)
			if got := strings.Join(f.text, ""); got != tc.text {
				t.Errorf("text = %q, want %q", got, tc.text)
			}
			switch {
			case tc.key == nil && len(f.keys) != 0:
				t.Errorf("unexpected keys %+v", f.keys)
			case tc.key != nil && (len(f.keys) != 1 || f.keys[0] != *tc.key):
				t.Errorf("keys = %+v, want %+v", f.keys, *tc.key)
			}
		})
	}
}

func TestEscReachesTheProgramAndAltSReturnsToTheSidebar(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter") // focus a terminal

	m = press(m, "esc")

	if m.focus != focusMain {
		t.Error("esc must not leave the terminal; the program needs it")
	}
	s := (*spawned)[0]
	if len(s.keys) != 1 || s.keys[0].Code != uv.KeyEscape {
		t.Fatalf("program received %v, want a single Escape", s.keys)
	}

	m = press(m, "alt+s")

	if m.focus != focusSidebar {
		t.Error("alt+s should return to the sidebar")
	}
}
