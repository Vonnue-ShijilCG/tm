package workspace

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
)

func TestSnapshotAndRestoreCarryLayoutAndFocus(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	login, _ := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	dev, _ := ws.CreateTerminal(app, Options{Name: "Dev"}, 80, 24)
	shell, _ := ws.CreateTerminal(app, Options{}, 80, 24) // unnamed
	layout := &Layout{Side: true, Ratio: 0.6, First: &Layout{Key: login.Key()},
		Second: &Layout{Side: false, Ratio: 0.3, First: &Layout{Key: dev.Key()}, Second: &Layout{Key: shell.Key()}}}
	app.Layout, app.Focus = layout, dev.Key()

	snap := ws.Snapshot()
	sp := snap.Projects[0]
	i0, i1, i2 := 0, 1, 2
	wantState := &state.Layout{Direction: state.DirSide, Ratio: 0.6, First: &state.Layout{Terminal: &i0},
		Second: &state.Layout{Direction: state.DirStack, Ratio: 0.3, First: &state.Layout{Terminal: &i1}, Second: &state.Layout{Terminal: &i2}}}
	if !reflect.DeepEqual(sp.Layout, wantState) {
		t.Errorf("snapshot layout = %+v", sp.Layout)
	}
	if sp.Focused == nil || *sp.Focused != 1 {
		t.Errorf("snapshot focused = %v, want 1", sp.Focused)
	}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}

	// A fresh workspace over the same root restores keys and focus.
	ws2 := New(ws.Root, []project.Project{app.Project}, nil)
	if err := ws2.Restore(); err != nil {
		t.Fatal(err)
	}
	app2 := ws2.Projects[0]
	wantKeys := []string{login.Key(), dev.Key(), shell.Key()}
	if got := app2.Layout.Leaves(); !reflect.DeepEqual(got, wantKeys) {
		t.Errorf("restored leaves = %q, want %q", got, wantKeys)
	}
	if !app2.Layout.Side || app2.Layout.Ratio != 0.6 || app2.Layout.Second.Side || app2.Layout.Second.Ratio != 0.3 {
		t.Errorf("restored structure = %+v", app2.Layout)
	}
	if app2.Focus != dev.Key() {
		t.Errorf("restored focus = %q, want %q", app2.Focus, dev.Key())
	}

	// Saving again from a workspace with nothing running keeps the layout.
	if err := ws2.Save(); err != nil {
		t.Fatal(err)
	}
	ws3 := New(ws.Root, []project.Project{app.Project}, nil)
	if err := ws3.Restore(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ws3.Projects[0].Layout, app2.Layout) || ws3.Projects[0].Focus != app2.Focus {
		t.Error("a no-op session must not lose the saved layout")
	}
}

func TestRestoreDropsLayoutLeavesForRefusedTerminals(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	i0, i1, i2 := 0, 1, 2
	f := state.File{Projects: []state.Project{{Name: "app",
		Terminals: []state.Terminal{{Name: "A"}, {Name: "Elsewhere", Branch: "x", Worktree: ".terminal-worktrees/other/x"}, {Name: "C"}},
		Layout: &state.Layout{Direction: state.DirSide, Ratio: 0.5, First: &state.Layout{Terminal: &i0},
			Second: &state.Layout{Direction: state.DirStack, Ratio: 0.5, First: &state.Layout{Terminal: &i1}, Second: &state.Layout{Terminal: &i2}}},
		Focused: &i1}}}
	if err := state.Save(ws.Root, f); err != nil {
		t.Fatal(err)
	}
	if err := ws.Restore(); err != nil {
		t.Fatal(err)
	}
	// "Elsewhere" belongs to another project's worktree dir and is refused; its leaf is
	// gone and the stack collapsed to C.
	if got := app.Layout.Leaves(); !reflect.DeepEqual(got, []string{"A\x00\x00", "C\x00\x00"}) {
		t.Errorf("leaves = %q", got)
	}
	if app.Layout.Second == nil || app.Layout.Second.Key != "C\x00\x00" {
		t.Errorf("stack should collapse to C: %+v", app.Layout)
	}
	if app.Focus != "" {
		t.Errorf("focus on a refused terminal should be dropped, got %q", app.Focus)
	}
	_ = filepath.Join
}
