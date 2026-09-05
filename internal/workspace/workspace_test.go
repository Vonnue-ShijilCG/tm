package workspace

import (
	"errors"
	"os"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
)

type fakeSession struct {
	dir        string
	cols, rows int
	closed     bool
}

func (f *fakeSession) SendKey(uv.KeyPressEvent) {}
func (f *fakeSession) SendText(string)          {}
func (f *fakeSession) Resize(c, r int)          { f.cols, f.rows = c, r }
func (f *fakeSession) Render(bool) string       { return "" }
func (f *fakeSession) Title() string            { return "" }
func (f *fakeSession) Output() <-chan struct{}  { return nil }
func (f *fakeSession) Done() <-chan struct{}    { return nil }
func (f *fakeSession) Err() error               { return nil }
func (f *fakeSession) Close() error             { f.closed = true; return nil }

// TestMain points the configuration at an empty directory so every test
// sees the default limit regardless of the developer's own config file.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tm-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func newTestWorkspace() (*Workspace, *[]*fakeSession) {
	spawned := &[]*fakeSession{}
	ws := New("/ws", []project.Project{
		{Name: "backend", Path: "/ws/backend"},
		{Name: "ecommerce", Path: "/ws/ecommerce"},
	}, func(dir string, cols, rows int) (Session, error) {
		f := &fakeSession{dir: dir, cols: cols, rows: rows}
		*spawned = append(*spawned, f)
		return f, nil
	})
	return ws, spawned
}

func TestNewTerminalBelongsToProjectAndBecomesActive(t *testing.T) {
	ws, spawned := newTestWorkspace()
	p := ws.Projects[1]

	t1, err := ws.NewTerminal(p, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	t2, _ := ws.NewTerminal(p, 80, 24)

	if (*spawned)[0].dir != "/ws/ecommerce" || (*spawned)[1].dir != "/ws/ecommerce" {
		t.Errorf("terminals should start in the project dir, got %q %q", (*spawned)[0].dir, (*spawned)[1].dir)
	}
	if (*spawned)[0].cols != 80 || (*spawned)[0].rows != 24 {
		t.Errorf("spawn size = %dx%d, want 80x24", (*spawned)[0].cols, (*spawned)[0].rows)
	}
	if t1.Num != 1 || t2.Num != 2 {
		t.Errorf("terminal numbers = %d, %d, want 1, 2", t1.Num, t2.Num)
	}
	if t1.ID == t2.ID {
		t.Error("terminal IDs must be unique")
	}
	if len(p.Terminals) != 2 || p.ActiveTerminal() != t2 {
		t.Errorf("new terminal should become active; terminals=%d active=%v", len(p.Terminals), p.ActiveTerminal())
	}
	if len(ws.Projects[0].Terminals) != 0 {
		t.Error("other project must be unaffected")
	}
}

func TestNewTerminalSpawnError(t *testing.T) {
	ws := New("/", []project.Project{{Name: "a", Path: "/a"}}, func(string, int, int) (Session, error) {
		return nil, errors.New("boom")
	})
	if _, err := ws.NewTerminal(ws.Projects[0], 80, 24); err == nil {
		t.Fatal("expected spawn error")
	}
	if len(ws.Projects[0].Terminals) != 0 || ws.Projects[0].ActiveTerminal() != nil {
		t.Error("failed spawn must not register a terminal")
	}
}

func TestSelect(t *testing.T) {
	ws, _ := newTestWorkspace()
	p := ws.Projects[0]
	if p.ActiveTerminal() != nil {
		t.Fatal("no active terminal expected on an empty project")
	}
	ws.NewTerminal(p, 80, 24)
	b, _ := ws.NewTerminal(p, 80, 24)
	if c, _ := ws.NewTerminal(p, 80, 24); p.ActiveTerminal() != c {
		t.Errorf("the newest terminal becomes active, got %v", p.ActiveTerminal())
	}
	if !p.Select(1) || p.ActiveTerminal() != b {
		t.Errorf("Select(1) should activate b, got %v", p.ActiveTerminal())
	}
	if p.Select(7) {
		t.Error("Select out of range should return false")
	}
	if p.ActiveTerminal() != b {
		t.Error("failed Select must not change the active terminal")
	}
}

func TestRemoveKeepsOthersAndFixesActive(t *testing.T) {
	ws, spawned := newTestWorkspace()
	p := ws.Projects[0]
	a, _ := ws.NewTerminal(p, 80, 24)
	b, _ := ws.NewTerminal(p, 80, 24)
	c, _ := ws.NewTerminal(p, 80, 24)

	// Remove the active (last) terminal: active moves to the new last.
	gotP, gotT := ws.Remove(c.ID)
	if gotP != p || gotT != c {
		t.Fatalf("Remove returned %v %v", gotP, gotT)
	}
	if len(p.Terminals) != 2 || p.ActiveTerminal() != b {
		t.Errorf("after removing c: terminals=%d active=%v", len(p.Terminals), p.ActiveTerminal())
	}
	// Remove a terminal before the active one: active index shifts down.
	ws.Remove(a.ID)
	if len(p.Terminals) != 1 || p.ActiveTerminal() != b {
		t.Errorf("after removing a: terminals=%d active=%v", len(p.Terminals), p.ActiveTerminal())
	}
	for i, s := range *spawned {
		if s.closed {
			t.Errorf("Remove must not close sessions (session %d closed)", i)
		}
	}
	ws.Remove(b.ID)
	if p.ActiveTerminal() != nil {
		t.Error("no active terminal expected after removing all")
	}
	if p, t2 := ws.Remove(999); p != nil || t2 != nil {
		t.Error("removing an unknown id should return nils")
	}

	// Numbering keeps counting instead of reusing freed numbers.
	d, _ := ws.NewTerminal(p, 80, 24)
	if d.Num != 4 {
		t.Errorf("new terminal Num = %d, want 4", d.Num)
	}
}

func TestFindAndCloseAll(t *testing.T) {
	ws, spawned := newTestWorkspace()
	a, _ := ws.NewTerminal(ws.Projects[0], 80, 24)
	b, _ := ws.NewTerminal(ws.Projects[1], 80, 24)

	if p, term := ws.Find(b.ID); p != ws.Projects[1] || term != b {
		t.Errorf("Find(b) = %v %v", p, term)
	}
	if p, term := ws.Find(a.ID + b.ID + 1); p != nil || term != nil {
		t.Error("Find of unknown id should return nils")
	}

	ws.CloseAll()
	for i, s := range *spawned {
		if !s.closed {
			t.Errorf("session %d not closed", i)
		}
	}
}
