package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/state"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// arranged builds Login | (Payment / Dev), grows Dev once, focuses Login and quits.
func arranged(t *testing.T) (Model, string) {
	t.Helper()
	m, _, root := gitModel(t)
	m = makeTerminal(t, m, "n", "Login", "develop")
	m = makeTerminal(t, m, "alt+v", "Payment", "feature/payment")
	m = makeTerminal(t, m, "alt+h", "Dev", "")
	m = press(m, "alt+=")    // grow Dev: the Payment/Dev stack ratio becomes 0.4
	m = press(m, "alt+left") // focus Login
	if got := names(m.layouts[m.ws.Projects[0]]); strings.Join(got, ",") != "Login,Payment,Dev" {
		t.Fatalf("setup leaves = %v", got)
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	return m, root
}

func names(n *pane) []string {
	var out []string
	for _, t := range n.leaves() {
		out = append(out, t.Name)
	}
	return out
}

func TestLayoutSurvivesRestartThroughOpenAll(t *testing.T) {
	m, root := arranged(t)

	f, _ := state.Load(root)
	p := f.Projects[0]
	if p.Layout == nil || p.Layout.Direction != state.DirSide || p.Layout.Ratio != 0.5 || p.Layout.Second == nil || p.Layout.Second.Direction != state.DirStack {
		t.Fatalf("saved layout = %s", dumpLayout(p.Layout))
	}
	if r := p.Layout.Second.Ratio; r < 0.39 || r > 0.41 {
		t.Errorf("saved stack ratio = %v, want 0.4 after growing Dev", r)
	}
	if p.Focused == nil || p.Terminals[*p.Focused].Name != "Login" {
		t.Errorf("saved focus = %v", p.Focused)
	}

	m2, spawned := restartModel(t, m)
	app := m2.ws.Projects[0]
	m2 = press(m2, "m", "a")
	if len(*spawned) != 3 {
		t.Fatalf("open all spawned %d, want 3", len(*spawned))
	}
	root2 := m2.layouts[app]
	if got := names(root2); strings.Join(got, ",") != "Login,Payment,Dev" {
		t.Errorf("restored leaves = %v", got)
	}
	if root2.dir != splitSide || root2.ratio != 0.5 || !root2.a.isLeaf() || root2.b.dir != splitStack {
		t.Errorf("restored structure differs: %+v", root2)
	}
	if r := root2.b.ratio; r < 0.39 || r > 0.41 {
		t.Errorf("restored stack ratio = %v, want 0.4", r)
	}
	if app.ActiveTerminal() == nil || app.ActiveTerminal().Name != "Login" {
		t.Errorf("restored focus = %v, want Login", app.ActiveTerminal())
	}
	if m2.zoom {
		t.Error("zoom is not persisted")
	}
	for term, r := range m2.paneRects(app) {
		s := term.Session.(*fakeSession)
		if s.cols != sessionCols(r) || s.rows != sessionRows(r) {
			t.Errorf("%s not resized to its pane", term.Name)
		}
	}
	if list, _ := m2.ws.Worktrees(app); len(list) != 2 {
		t.Errorf("restoring must not create worktrees, got %d", len(list))
	}
}

func TestRestoreCollapsesAroundMissingTerminal(t *testing.T) {
	m, root := arranged(t)
	// Payment's worktree disappears while tm is not running: its leaf must go
	// and Login | Dev remain, with Dev taking the whole right column.
	if err := os.RemoveAll(filepath.Join(root, workspace.WorktreesDir, "app", "feature-payment")); err != nil {
		t.Fatal(err)
	}
	m2, spawned := restartModel(t, m)
	app := m2.ws.Projects[0]
	m2 = press(m2, "m", "a")
	if len(*spawned) != 2 || !strings.Contains(m2.notice, "Payment") {
		t.Fatalf("Payment should be skipped: spawned=%d notice=%q", len(*spawned), m2.notice)
	}
	r := m2.layouts[app]
	if got := names(r); strings.Join(got, ",") != "Login,Dev" || r.dir != splitSide || !r.b.isLeaf() {
		t.Errorf("collapsed tree wrong: leaves=%v root=%+v", got, r)
	}
	area := m2.mainArea()
	if rd := m2.paneRects(app)[r.b.term]; rd.h != area.h {
		t.Errorf("Dev should fill the right column, got %+v", rd)
	}
	if app.ActiveTerminal().Name != "Login" {
		t.Error("saved focus (Login) should still apply")
	}
	if _, err := os.Stat(filepath.Join(root, workspace.WorktreesDir, "app", "feature-payment")); !os.IsNotExist(err) {
		t.Error("missing worktree must not be recreated")
	}
}

func TestRestoreWithOneTerminalLeftIsSinglePane(t *testing.T) {
	m, root := arranged(t)
	f, _ := state.Load(root)
	// Drop Payment and Dev from the saved terminals; the layout only references Login now.
	var kept []state.Terminal
	for _, st := range f.Projects[0].Terminals {
		if st.Name == "Login" {
			kept = append(kept, st)
		}
	}
	f.Projects[0].Terminals = kept
	if err := state.Save(root, f); err != nil { // sanitiser rewrites the layout with the remaining index
		t.Fatal(err)
	}
	m2, spawned := restartModel(t, m)
	app := m2.ws.Projects[0]
	m2 = press(m2, "m", "a")
	if len(*spawned) != 1 || !m2.layouts[app].isLeaf() {
		t.Errorf("single remaining terminal should be a single pane: spawned=%d root=%+v", len(*spawned), m2.layouts[app])
	}
	if r := m2.paneRects(app)[app.Terminals[0]]; r != m2.mainArea() {
		t.Errorf("single pane should fill the area: %+v", r)
	}
}

func TestExtraSavedTerminalIsPlacedByBalancedLogic(t *testing.T) {
	m, root := arranged(t)
	f, _ := state.Load(root)
	f.Projects[0].Terminals = append(f.Projects[0].Terminals, state.Terminal{Name: "Extra"})
	if err := state.Save(root, f); err != nil {
		t.Fatal(err)
	}
	m2, spawned := restartModel(t, m)
	app := m2.ws.Projects[0]
	m2 = press(m2, "m", "a")
	if len(*spawned) != 4 {
		t.Fatalf("spawned %d, want 4", len(*spawned))
	}
	r := m2.layouts[app]
	got := names(r)
	if len(got) != 4 || !strings.Contains(strings.Join(got, ","), "Extra") {
		t.Fatalf("Extra should be added: %v", got)
	}
	// The saved structure survives: Payment/Dev still stacked on the right;
	// Extra was inserted by splitting the largest pane (Login's column).
	if r.dir != splitSide || r.b.dir != splitStack || strings.Join(names(r.b), ",") != "Payment,Dev" || !r.a.contains(app.Terminals[0]) {
		t.Errorf("saved part not intact: %+v (leaves %v)", r, got)
	}
	for term, rc := range m2.paneRects(app) {
		if rc.w < minPaneCols || rc.h < minPaneRows {
			t.Errorf("%s pane too small %+v", term.Name, rc)
		}
	}
}

func TestOldStateWithoutLayoutUsesAutomaticArrangement(t *testing.T) {
	m, root := arranged(t)
	f, _ := state.Load(root)
	f.Projects[0].Layout, f.Projects[0].Focused = nil, nil
	f.Version = 1
	data, _ := json.Marshal(f)
	if err := os.WriteFile(filepath.Join(root, state.FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m2, spawned := restartModel(t, m)
	app := m2.ws.Projects[0]
	if app.Layout != nil {
		t.Fatal("no layout expected from a v1 file")
	}
	m2 = press(m2, "m", "a")
	if len(*spawned) != 3 || len(m2.layouts[app].leaves()) != 3 {
		t.Errorf("old state should still open all with the automatic layout: spawned=%d leaves=%d", len(*spawned), len(m2.layouts[app].leaves()))
	}
	if m2.notice != "" {
		t.Errorf("unexpected notice %q", m2.notice)
	}
}

func TestMalformedLayoutDoesNotBreakStartupOrOpenAll(t *testing.T) {
	m, root := arranged(t)
	raw, _ := os.ReadFile(filepath.Join(root, state.FileName))
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// Valid JSON document, nonsense layout: wrong types and an impossible tree.
	doc["projects"].([]any)[0].(map[string]any)["layout"] = map[string]any{"direction": 42, "first": "x", "second": map[string]any{"terminal": "zero"}}
	data, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(root, state.FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := state.Load(root)
	if err != nil || f.Projects[0].Layout != nil || len(f.Projects[0].Terminals) != 3 {
		t.Fatalf("bad layout should be dropped and terminals kept: err=%v layout=%v", err, f.Projects[0].Layout)
	}
	m2, spawned := restartModel(t, m)
	if m2.notice != "" {
		t.Errorf("unexpected startup notice %q", m2.notice)
	}
	m2 = press(m2, "m", "a")
	app := m2.ws.Projects[0]
	if len(*spawned) != 3 || len(m2.layouts[app].leaves()) != 3 {
		t.Errorf("open all should fall back to the automatic layout: spawned=%d leaves=%d", len(*spawned), len(m2.layouts[app].leaves()))
	}
}

func TestLayoutIsPerProject(t *testing.T) {
	m, spawned := newTestModel(t)                      // three fake projects, plain terminals
	m = press(m, "enter", "alt+v", "enter", "alt+esc") // backend: two panes
	m = press(m, "j", "enter", "alt+esc")              // ecommerce: one terminal
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if len(*spawned) != 3 {
		t.Fatalf("setup spawned %d", len(*spawned))
	}
	m2, spawned2 := restartModel(t, m)
	backend, ecommerce := m2.ws.Projects[0], m2.ws.Projects[1]
	if backend.Layout == nil || len(backend.Layout.Leaves()) != 2 || ecommerce.Layout == nil || len(ecommerce.Layout.Leaves()) != 1 {
		t.Fatalf("per-project layouts not restored: backend=%+v ecommerce=%+v", backend.Layout, ecommerce.Layout)
	}
	m2 = press(m2, "m", "a")                 // backend
	m2 = press(m2, "alt+esc", "j", "m", "a") // ecommerce
	if len(*spawned2) != 3 {
		t.Fatalf("spawned %d, want 3", len(*spawned2))
	}
	for _, term := range m2.layouts[backend].leaves() {
		if term.Session.(*fakeSession).dir != "/ws/backend" {
			t.Error("backend layout contains a foreign terminal")
		}
	}
	if len(m2.layouts[backend].leaves()) != 2 || len(m2.layouts[ecommerce].leaves()) != 1 {
		t.Errorf("layouts mixed up: backend=%d ecommerce=%d", len(m2.layouts[backend].leaves()), len(m2.layouts[ecommerce].leaves()))
	}
}

func TestRestoredLayoutRespectsMinimumsAtAllSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {200, 60}} {
		m, _ := arranged(t)
		m2, spawned := restartModel(t, m)
		next, _ := m2.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m2 = next.(Model)
		app := m2.ws.Projects[0]
		m2 = press(m2, "m", "a")
		if len(*spawned) != 3 {
			t.Fatalf("%dx%d: spawned %d", size[0], size[1], len(*spawned))
		}
		rs := m2.paneRects(app)
		for term, r := range rs {
			if r.w < minPaneCols || r.h < minPaneRows {
				t.Errorf("%dx%d: %s pane too small %+v", size[0], size[1], term.Name, r)
			}
		}
		if !m2.layouts[app].contains(app.ActiveTerminal()) {
			t.Errorf("%dx%d: focused terminal must be visible", size[0], size[1])
		}
		if lines := strings.Count(m2.View(), "\n") + 1; lines != size[1] {
			t.Errorf("%dx%d: view has %d lines", size[0], size[1], lines)
		}
	}
}

func TestFocusAndResizeChangesArePersisted(t *testing.T) {
	m, spawned, root := gitModel(t)
	m = makeTerminal(t, m, "n", "A", "")
	m = makeTerminal(t, m, "alt+v", "B", "")
	_ = spawned
	f, _ := state.Load(root)
	if f.Projects[0].Focused == nil || *f.Projects[0].Focused != 1 {
		t.Fatalf("focus after create = %v, want 1", f.Projects[0].Focused)
	}
	m = press(m, "alt+left")
	f, _ = state.Load(root)
	if *f.Projects[0].Focused != 0 {
		t.Errorf("focus change should be saved, got %v", *f.Projects[0].Focused)
	}
	m = press(m, "alt+=")
	f, _ = state.Load(root)
	if r := f.Projects[0].Layout.Ratio; r < 0.59 || r > 0.61 {
		t.Errorf("resize should be saved, ratio = %v", r)
	}
	m = press(m, "alt+z") // zoom is not persisted: state unchanged
	f2, _ := state.Load(root)
	if dumpLayout(f2.Projects[0].Layout) != dumpLayout(f.Projects[0].Layout) {
		t.Error("zoom must not change the saved layout")
	}
}

func dumpLayout(l *state.Layout) string { b, _ := json.Marshal(l); return string(b) }
