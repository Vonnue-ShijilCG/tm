package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/Vonnue-ShijilCG/tm/internal/server"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// TestRemoteSessionSatisfiesSession: a server session plugs in wherever a
// local one does.
func TestRemoteSessionSatisfiesSession(t *testing.T) {
	var _ workspace.Session = (*server.RemoteSession)(nil)
}

// serverFake is a session held by a real tm server in these tests. The
// server calls it from its own goroutines, so unlike fakeSession it is safe
// for concurrent use.
type serverFake struct {
	dir     string
	output  chan struct{}
	done    chan struct{}
	once    sync.Once
	version atomic.Uint64
	closed  atomic.Int32

	mu   sync.Mutex
	size [2]int
}

func (f *serverFake) emit() {
	f.version.Add(1)
	select {
	case f.output <- struct{}{}:
	default:
	}
}
func (f *serverFake) exit()                    { f.once.Do(func() { close(f.done) }) }
func (f *serverFake) SendKey(uv.KeyPressEvent) {}
func (f *serverFake) SendText(string)          {}
func (f *serverFake) Render(bool) string {
	return fmt.Sprintf("SCREEN:%s:v%d", f.dir, f.version.Load())
}
func (f *serverFake) RenderScrolled(int) string { return "" }
func (f *serverFake) Version() uint64           { return f.version.Load() }
func (f *serverFake) Lines(int) []string        { return nil }
func (f *serverFake) ScrollbackLen() int        { return 0 }
func (f *serverFake) IsAltScreen() bool         { return false }
func (f *serverFake) MouseTracking() bool       { return false }
func (f *serverFake) SendWheel(bool, int, int)  {}
func (f *serverFake) Title() string             { return "" }
func (f *serverFake) Output() <-chan struct{}   { return f.output }
func (f *serverFake) Done() <-chan struct{}     { return f.done }
func (f *serverFake) Err() error                { return nil }
func (f *serverFake) Close() error              { f.closed.Add(1); f.exit(); return nil }
func (f *serverFake) Resize(cols, rows int) {
	f.mu.Lock()
	f.size = [2]int{cols, rows}
	f.mu.Unlock()
	f.version.Add(1)
}
func (f *serverFake) lastSize() [2]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.size
}

// testServer is a real server.Server on a temp socket whose sessions are
// serverFakes.
type testServer struct {
	paths  server.Paths
	served chan error

	mu    sync.Mutex
	fakes []*serverFake
}

func startTestServer(t *testing.T) *testServer {
	t.Helper()
	dir := t.TempDir()
	ts := &testServer{
		paths:  server.Paths{Dir: dir, Socket: filepath.Join(dir, "s"), Exited: filepath.Join(dir, "exited")},
		served: make(chan error, 1),
	}
	ln, err := net.Listen("unix", ts.paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(ts.spawn, ts.paths.Exited)
	go func() { ts.served <- srv.Serve(ln) }()
	t.Cleanup(func() {
		server.StopServer(ts.paths)
		select {
		case <-ts.served:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return ts
}

func (ts *testServer) spawn(cfg terminal.Config) (server.Session, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	f := &serverFake{dir: cfg.Dir, output: make(chan struct{}, 1), done: make(chan struct{})}
	f.size = [2]int{cfg.Cols, cfg.Rows}
	ts.fakes = append(ts.fakes, f)
	return f, nil
}

func (ts *testServer) spawned() []*serverFake {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]*serverFake(nil), ts.fakes...)
}

// stopped reports whether Serve has returned, waiting up to within.
func (ts *testServer) stopped(within time.Duration) bool {
	select {
	case err := <-ts.served:
		ts.served <- err // for Cleanup
		return true
	case <-time.After(within):
		return false
	}
}

// attachModel attaches a new client to ts and builds a model on it, with no
// window size yet.
func attachModel(t *testing.T, ts *testServer, root string) Model {
	t.Helper()
	c, w, err := server.Attach(ts.paths)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return NewWith(root, testProjects(), Options{Client: c, Welcome: w})
}

// newAttachedModel is newTestModel for server mode: attached, at 100x30.
func newAttachedModel(t *testing.T, ts *testServer, root string) Model {
	t.Helper()
	return resize(attachModel(t, ts, root), 100, 30)
}

func resize(m Model, w, h int) Model {
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

// quitWith presses key in the sidebar and requires the program to end.
func quitWith(t *testing.T, m Model, k string) Model {
	t.Helper()
	m = press(m, "alt+s")
	if m.focus != focusSidebar {
		t.Fatalf("alt+s did not reach the sidebar")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	if cmd == nil {
		t.Fatalf("%s returned no command", k)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("%s should quit", k)
	}
	return next.(Model)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func refOf(t *workspace.Terminal) string {
	if r, ok := t.Session.(interface{ Ref() string }); ok {
		return r.Ref()
	}
	return ""
}

func refsOf(ts []*workspace.Terminal) []string {
	var out []string
	for _, t := range ts {
		out = append(out, refOf(t))
	}
	return out
}

// awaitMsg runs cmd - a batch included - and returns the first message for
// which want is true.
func awaitMsg(t *testing.T, cmd tea.Cmd, want func(tea.Msg) bool) tea.Msg {
	t.Helper()
	got := make(chan tea.Msg, 64)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			msg := c()
			if b, ok := msg.(tea.BatchMsg); ok {
				for _, sub := range b {
					run(sub)
				}
				return
			}
			got <- msg
		}()
	}
	run(cmd)
	timeout := time.After(3 * time.Second)
	for {
		select {
		case msg := <-got:
			if want(msg) {
				return msg
			}
		case <-timeout:
			t.Fatal("the expected message never came")
			return nil
		}
	}
}

func savedFile(t *testing.T, root string) state.File {
	t.Helper()
	f, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestQuitLeavesTerminalsRunning(t *testing.T) {
	ts := startTestServer(t)
	m := newAttachedModel(t, ts, t.TempDir())
	m = press(m, "enter")
	if n := len(ts.spawned()); n != 1 {
		t.Fatalf("spawned %d sessions on the server, want 1", n)
	}
	quitWith(t, m, "q")
	f := ts.spawned()[0]

	c, w, err := server.Attach(ts.paths)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if f.closed.Load() != 0 {
		t.Error("q closed the terminal")
	}
	if w.Live != 1 || len(w.Sessions) != 1 || w.Sessions[0].Exited {
		t.Errorf("welcome after q = %+v, want the terminal still live", w)
	}
}

func TestStopAllKillsEverything(t *testing.T) {
	ts := startTestServer(t)
	m := newAttachedModel(t, ts, t.TempDir())
	m = press(m, "enter")
	quitWith(t, m, "Q")
	if !ts.stopped(5 * time.Second) {
		t.Fatal("Q did not stop the server")
	}
	if f := ts.spawned()[0]; f.closed.Load() == 0 {
		t.Error("Q left the terminal running")
	}
}

func TestReattachRestoresLayoutAndHidden(t *testing.T) {
	ts := startTestServer(t)
	root := t.TempDir()
	m := newAttachedModel(t, ts, root)
	m = press(m, "enter", "alt+n", "enter", "alt+n", "enter", "alt+x")
	p := m.ws.Projects[0]
	visible := refsOf(m.layouts[p].leaves())
	if len(p.Terminals) != 3 || len(visible) != 2 {
		t.Fatalf("setup: %d running, %d visible", len(p.Terminals), len(visible))
	}
	var hidden string
	for _, tm := range p.Terminals {
		if !m.layouts[p].contains(tm) {
			hidden = refOf(tm)
		}
	}
	quitWith(t, m, "q")

	m2 := newAttachedModel(t, ts, root)
	p2 := m2.ws.Projects[0]
	if got := refsOf(m2.layouts[p2].leaves()); fmt.Sprint(got) != fmt.Sprint(visible) {
		t.Errorf("visible after reattach = %v, want %v", got, visible)
	}
	if len(p2.Terminals) != 3 || len(p2.Saved) != 0 {
		t.Fatalf("after reattach: %d running, %d saved; want 3 running", len(p2.Terminals), len(p2.Saved))
	}
	for _, tm := range p2.Terminals {
		if refOf(tm) == hidden && m2.layouts[p2].contains(tm) {
			t.Error("the hidden terminal came back visible")
		}
	}
	if n := len(ts.spawned()); n != 3 {
		t.Errorf("reattach spawned: %d sessions, want the 3 from before", n)
	}
	if m2.selected != 0 || m2.focus != focusMain {
		t.Errorf("selected %d focus %v: the project shown before should be shown again", m2.selected, m2.focus)
	}
}

func TestAttachDropsTerminalsThatExitedWhileAway(t *testing.T) {
	ts := startTestServer(t)
	root := t.TempDir()
	m := newAttachedModel(t, ts, root)
	m = press(m, "enter", "alt+n", "enter")
	quitWith(t, m, "q")
	keep, gone := ts.spawned()[0], ts.spawned()[1]
	gone.exit()
	waitFor(t, "the server to see the exit", func() bool {
		live, _ := server.Probe(ts.paths)
		return live == 1
	})
	_ = keep

	m2 := newAttachedModel(t, ts, root)
	p := m2.ws.Projects[0]
	if len(p.Terminals) != 1 || len(p.Saved) != 0 {
		t.Fatalf("after reattach: %d running, %d saved; want the one still live and nothing saved", len(p.Terminals), len(p.Saved))
	}
	f := savedFile(t, root)
	if len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 1 || f.Projects[0].Terminals[0].Ref != refOf(p.Terminals[0]) {
		t.Errorf("state file = %+v, want only the live terminal", f.Projects)
	}
}

// Without a server, the refs tm stop or an exiting server recorded are
// forgotten from the state file the same way.
func TestForgetWithoutClient(t *testing.T) {
	root := t.TempDir()
	body := `{"version":3,"projects":[{"name":"backend","terminals":[{"command":"a","ref":"00000000000000aa"},{"command":"b","ref":"00000000000000bb"}]}]}`
	if err := os.WriteFile(filepath.Join(root, state.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewWith(root, testProjects(), Options{Forget: []string{"00000000000000aa"}})
	if s := m.ws.Projects[0].Saved; len(s) != 1 || s[0].Command != "b" {
		t.Errorf("saved = %+v, want only b", s)
	}
	if f := savedFile(t, root); len(f.Projects[0].Terminals) != 1 {
		t.Errorf("state file still lists %d terminals", len(f.Projects[0].Terminals))
	}
}

func TestAttachMarksOutputWhileAway(t *testing.T) {
	ts := startTestServer(t)
	root := t.TempDir()
	m := newAttachedModel(t, ts, root)
	m = press(m, "enter", "alt+s", "j", "enter") // one in backend, one in ecommerce
	quitWith(t, m, "q")
	time.Sleep(50 * time.Millisecond) // let the server see the detach, as its own test does
	ts.spawned()[0].emit()            // backend prints while nobody is attached

	m2 := newAttachedModel(t, ts, root)
	backend, ecommerce := m2.ws.Projects[0], m2.ws.Projects[1]
	if len(backend.Terminals) != 1 || !m2.unseen[backend.Terminals[0]] || !m2.stirring(backend) {
		t.Error("output while away should mark backend's terminal")
	}
	if len(ecommerce.Terminals) != 1 || m2.stirring(ecommerce) {
		t.Error("ecommerce printed nothing and must not be marked")
	}
}

func TestAttachResizesBeforeFirstFrame(t *testing.T) {
	ts := startTestServer(t)
	root := t.TempDir()
	m := resize(attachModel(t, ts, root), 160, 48)
	m = press(m, "enter")
	quitWith(t, m, "q")
	f := ts.spawned()[0]
	big := f.lastSize()

	m2 := attachModel(t, ts, root)
	if v := m2.View(); v != "loading..." {
		t.Fatalf("before a size, the view should hold no frame:\n%s", v)
	}
	m2 = resize(m2, 80, 24)
	p := m2.ws.Projects[0]
	r, ok := m2.paneRects(p)[p.Terminals[0]]
	if !ok {
		t.Fatal("the adopted terminal has no pane")
	}
	want := [2]int{sessionCols(r), sessionRows(r)}
	if want == big {
		t.Fatalf("test needs the two windows to give different sizes, both %v", want)
	}
	waitFor(t, fmt.Sprintf("the session to be resized to %v (is %v)", want, f.lastSize()), func() bool { return f.lastSize() == want })
}

func TestEvictedModelQuitsWithReason(t *testing.T) {
	ts := startTestServer(t)
	root := t.TempDir()
	m := newAttachedModel(t, ts, root)
	m = press(m, "enter")
	before, err := os.ReadFile(filepath.Join(root, state.FileName))
	if err != nil {
		t.Fatal(err)
	}
	init := m.Init()

	c, _, err := server.Attach(ts.paths) // another window
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	gone := awaitMsg(t, init, func(msg tea.Msg) bool { _, ok := msg.(serverGoneMsg); return ok })

	// The sessions read as done once the connection is lost; that is not
	// their processes exiting and must not forget them.
	p := m.ws.Projects[0]
	tm := p.Terminals[0]
	waitFor(t, "the session to read as done", func() bool { return !running(tm) })
	if !errors.Is(tm.Session.Err(), server.ErrGone) {
		t.Fatalf("Err = %v, want ErrGone", tm.Session.Err())
	}
	next, _ := m.Update(terminalExitedMsg{id: tm.ID, err: tm.Session.Err()})
	m = next.(Model)
	if len(p.Terminals) != 1 {
		t.Error("a lost connection removed the terminal")
	}

	next, cmd := m.Update(gone)
	m = next.(Model)
	if cmd == nil {
		t.Fatal("eviction should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("eviction should quit")
	}
	if got := m.ExitMessage(); got != "tm: opened in another window" {
		t.Errorf("ExitMessage = %q", got)
	}
	after, _ := os.ReadFile(filepath.Join(root, state.FileName))
	if string(after) != string(before) {
		t.Errorf("state written after the server let go:\n%s\nwas\n%s", after, before)
	}
}

func TestQuitWithoutClientIsUnchanged(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+s")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Q")})
	m = next.(Model)
	if cmd != nil {
		t.Error("Q without a server should do nothing")
	}
	if (*spawned)[0].closed || m.help || m.form != nil || m.panel != nil {
		t.Error("Q without a server changed something")
	}
	if m.ExitMessage() != "" {
		t.Errorf("ExitMessage = %q without a server", m.ExitMessage())
	}
}

// Spawns record their project and definition with the server, so a session
// the blob does not mention can still be put back where it belongs.
func TestSessionMissingFromBlobIsAttachedHidden(t *testing.T) {
	ts := startTestServer(t)
	c, _, err := server.Attach(ts.paths)
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(spawnMeta{Project: "ecommerce", Terminal: workspace.SavedTerminal{Command: "npm run dev"}})
	if _, err := c.Spawn("/ws/ecommerce", 80, 24, meta); err != nil {
		t.Fatal(err)
	}
	c.Close() // never sent a blob

	m := newAttachedModel(t, ts, t.TempDir())
	p := m.ws.Projects[1]
	if len(p.Terminals) != 1 || p.Terminals[0].Command != "npm run dev" {
		t.Fatalf("ecommerce terminals = %+v, want the orphan", p.Terminals)
	}
	if m.layouts[p].contains(p.Terminals[0]) {
		t.Error("an orphan should come back hidden")
	}
	if n := len(ts.spawned()); n != 1 {
		t.Errorf("spawned %d", n)
	}

	// And spawning through the model records the same.
	m = press(m, "enter") // backend's first shell
	c2, w, err := server.Attach(ts.paths)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	var got spawnMeta
	for _, s := range w.Sessions {
		if s.Ref == refOf(m.ws.Projects[0].Terminals[0]) {
			json.Unmarshal(s.Meta, &got)
		}
	}
	if got.Project != "backend" {
		t.Errorf("meta = %+v, want backend's", got)
	}
}

// A ref in the server's own blob that the server no longer lists is a
// session that ended unseen - its Exit went to a client on its way out - and
// is dropped like one reported as exited. A ref in the state file, read
// because the server has no blob yet, belongs to an earlier server, and its
// definition is kept to be reopened as usual.
func TestUnknownRefsDependOnWhereTheyCameFrom(t *testing.T) {
	const ended = "00000000000000ee"
	file := state.File{Version: state.Version, Projects: []state.Project{{
		Name: "backend", Terminals: []state.Terminal{{Command: "make watch", Ref: ended}},
	}}}

	t.Run("blob", func(t *testing.T) {
		ts := startTestServer(t)
		c, _, err := server.Attach(ts.paths)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Spawn("/ws/mobile", 80, 24, nil); err != nil { // keeps the server up
			t.Fatal(err)
		}
		b, _ := json.Marshal(uiBlob{State: file})
		c.SetBlob(b)
		c.Close()

		m := newAttachedModel(t, ts, t.TempDir())
		if s := m.ws.Projects[0].Saved; len(s) != 0 {
			t.Errorf("saved = %+v, want the ended terminal forgotten", s)
		}
	})

	t.Run("file", func(t *testing.T) {
		ts := startTestServer(t)
		root := t.TempDir()
		if err := state.Save(root, file); err != nil {
			t.Fatal(err)
		}
		m := newAttachedModel(t, ts, root)
		if s := m.ws.Projects[0].Saved; len(s) != 1 || s[0].Command != "make watch" || s[0].Ref != "" {
			t.Errorf("saved = %+v, want the definition kept, naming no session", s)
		}
	})
}

// R11: with a server, q does not quit everything, and the status bar must
// not say it does. The help screen has room for the long form.
func TestStatusBarSaysLeaveWithServer(t *testing.T) {
	ts := startTestServer(t)
	m := newAttachedModel(t, ts, t.TempDir())
	status := ansi.Strip(m.viewStatus())
	if !strings.Contains(status, "q/ctrl+c leave") || strings.Contains(status, "quit") || strings.Contains(status, "keep running") {
		t.Errorf("status bar with a server = %q, want the short leave hint", status)
	}
	plain, _ := newTestModel(t)
	if status := ansi.Strip(plain.viewStatus()); !strings.Contains(status, "q/ctrl+c quit") {
		t.Errorf("status bar without a server = %q, want quit", status)
	}
}

// Q is answered by an eviction ("stopped by tm stop") that may be read
// before the program has finished quitting; the user asked for the stop and
// must not be told about it as if it happened to them.
func TestStopAllPrintsNothing(t *testing.T) {
	ts := startTestServer(t)
	m := quitWith(t, press(newAttachedModel(t, ts, t.TempDir()), "enter"), "Q")
	next, cmd := m.Update(serverGoneMsg{reason: "stopped by tm stop"})
	if cmd != nil {
		t.Error("a gone message after Q should do nothing")
	}
	if got := next.(Model).ExitMessage(); got != "" {
		t.Errorf("ExitMessage after Q = %q, want none", got)
	}
}

// Every server spawn goes through SpawnFor, which records the terminal; the
// plain Spawner is never meant to run and says so rather than spawning a
// session with nothing recorded.
func TestServerModeSpawnerWithoutMetaRefuses(t *testing.T) {
	ts := startTestServer(t)
	m := newAttachedModel(t, ts, t.TempDir())
	m.ws.SpawnFor = nil
	if _, err := m.ws.NewTerminal(m.ws.Projects[0], 80, 24); err == nil {
		t.Error("the plain spawner should refuse in server mode")
	}
	if n := len(ts.spawned()); n != 0 {
		t.Errorf("spawned %d", n)
	}
}

// Before the first window size nothing is laid out, so a blob written then
// would call every terminal hidden.
func TestNoBlobBeforeTheFirstLayout(t *testing.T) {
	ts := startTestServer(t)
	m := attachModel(t, ts, t.TempDir())
	m.persist()
	c, w, err := server.Attach(ts.paths)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(w.Blob) != 0 {
		t.Errorf("blob sent before the first layout: %s", w.Blob)
	}
}

func TestSessionsForMissingProjectsAreReported(t *testing.T) {
	ts := startTestServer(t)
	c, _, err := server.Attach(ts.paths)
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(spawnMeta{Project: "removed-repo"})
	if _, err := c.Spawn("/ws/removed-repo", 80, 24, meta); err != nil {
		t.Fatal(err)
	}
	c.Close()

	m := newAttachedModel(t, ts, t.TempDir())
	if want := "1 terminal still running for projects no longer here: Q or tm stop ends them"; m.notice != want {
		t.Errorf("notice = %q, want %q", m.notice, want)
	}
}
