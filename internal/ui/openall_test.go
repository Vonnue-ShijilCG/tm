package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/state"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// savedProject builds a restarted model whose only project has saved
// terminals: two worktree terminals (Login on develop, Payment on a new
// branch), a plain "Dev" terminal and, when withMissing is set, a worktree
// terminal whose directory was deleted.
func savedProject(t *testing.T, withMissing bool) (Model, *[]*fakeSession, string) {
	t.Helper()
	m, _, root := gitModel(t)
	create := func(name, branch string) {
		m = press(m, "n")
		m = typeText(m, name)
		if branch == "" {
			m = press(m, "tab", "tab", " ", "enter")
			return
		}
		m = press(m, "tab")
		m = typeText(m, branch)
		m = press(m, "enter")
	}
	m = press(m, "esc")
	create("Login", "develop")
	m = press(m, "esc")
	create("Payment", "feature/payment")
	m = press(m, "esc")
	create("Dev", "")
	if withMissing {
		m = press(m, "esc")
		create("Gone", "feature/gone")
	}
	m = press(m, "esc")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if withMissing {
		if err := os.RemoveAll(filepath.Join(root, workspace.WorktreesDir, "app", "gone")); err != nil {
			t.Fatal(err)
		}
	}
	m2, spawned := restartModel(t, m)
	return m2, spawned, root
}

func TestOpenAllReopensSavedTerminalsIntoPanes(t *testing.T) {
	m, spawned, root := savedProject(t, false)
	app := m.ws.Projects[0]
	before, _ := m.ws.Worktrees(app)

	m = press(m, "m", "a")

	if len(*spawned) != 3 || len(app.Terminals) != 3 {
		t.Fatalf("open all should start 3 terminals, spawned=%d terminals=%d", len(*spawned), len(app.Terminals))
	}
	names := map[string]string{}
	for _, term := range app.Terminals {
		names[term.Name] = term.Session.(*fakeSession).dir
	}
	if names["Login"] != filepath.Join(root, workspace.WorktreesDir, "app", "login") || names["Payment"] != filepath.Join(root, workspace.WorktreesDir, "app", "payment") || names["Dev"] != app.Path {
		t.Errorf("terminals opened in wrong directories: %v", names)
	}
	if after, _ := m.ws.Worktrees(app); len(after) != len(before) {
		t.Errorf("open all must reuse worktrees: %d -> %d", len(before), len(after))
	}
	if len(app.Saved) != 0 {
		t.Errorf("all definitions reopened, Saved = %+v", app.Saved)
	}
	if m.panel != nil || m.focus != focusMain || m.selected != 0 {
		t.Error("open all should close the panel and focus the terminals")
	}
	leaves := m.layouts[app].leaves()
	if len(leaves) != 3 {
		t.Fatalf("all three terminals should be visible at 120x30, got %d panes", len(leaves))
	}
	rs := m.paneRects(app)
	for _, term := range leaves {
		r := rs[term]
		if r.w < minPaneCols || r.h < minPaneRows {
			t.Errorf("pane for %s too small: %+v", term.Name, r)
		}
		if s := term.Session.(*fakeSession); s.cols != r.w || s.rows != r.h-paneHeaderHeight {
			t.Errorf("%s session %dx%d, want %dx%d", term.Name, s.cols, s.rows, r.w, r.h-paneHeaderHeight)
		}
	}
	if !m.layouts[app].contains(app.ActiveTerminal()) {
		t.Error("focused terminal must be visible")
	}
	view := m.View()
	for _, want := range []string{"Login [develop]", "Payment [feature/payment]", "Dev"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	if m.notice != "" {
		t.Errorf("nothing was skipped, notice = %q", m.notice)
	}

	// Doing it again is a no-op: nothing to open, nothing duplicated.
	m = press(m, "esc", "m", "a")
	if len(*spawned) != 3 || m.panel == nil || !strings.Contains(m.panel.notice, "nothing to restore") {
		t.Errorf("second open all should do nothing: spawned=%d panel=%v", len(*spawned), m.panel)
	}
	f, _ := state.Load(root)
	if len(f.Projects[0].Terminals) != 3 {
		t.Errorf("state after open all = %+v", f.Projects[0].Terminals)
	}
}

func TestOpenAllSkipsMissingWorktreeAndKeepsRunning(t *testing.T) {
	m, spawned, root := savedProject(t, true)
	app := m.ws.Projects[0]

	// One saved terminal already running: it must not be duplicated.
	m = press(m, "m", "enter") // Login is the first saved row
	if len(*spawned) != 1 {
		t.Fatalf("setup: enter should open one terminal, got %d", len(*spawned))
	}
	m = press(m, "esc", "m", "a")

	if len(*spawned) != 3 || len(app.Terminals) != 3 {
		t.Fatalf("open all should add Payment and Dev only: spawned=%d terminals=%d", len(*spawned), len(app.Terminals))
	}
	if !strings.Contains(m.notice, "Gone") || !strings.Contains(m.notice, "missing") {
		t.Errorf("missing worktree should be reported: %q", m.notice)
	}
	if _, err := os.Stat(filepath.Join(root, workspace.WorktreesDir, "app", "gone")); !os.IsNotExist(err) {
		t.Error("missing worktree must not be recreated")
	}
	if len(app.Saved) != 1 || app.Saved[0].Name != "Gone" {
		t.Errorf("only the skipped definition should remain saved: %+v", app.Saved)
	}
	count := map[string]int{}
	for _, term := range app.Terminals {
		count[term.Name]++
	}
	if count["Login"] != 1 || count["Payment"] != 1 || count["Dev"] != 1 {
		t.Errorf("duplicate or missing terminals: %v", count)
	}
}

func TestOpenAllOnSmallWindowHidesRatherThanShrinks(t *testing.T) {
	m, spawned, _ := savedProject(t, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 9}) // area 30x6: too small for three panes
	m = next.(Model)
	m = press(m, "m", "a")
	app := m.ws.Projects[0]
	if len(*spawned) != 3 {
		t.Fatalf("all three should still start, spawned=%d", len(*spawned))
	}
	leaves := m.layouts[app].leaves()
	if len(leaves) != 2 { // 30 cols split once into 14/15; neither half can be split again
		t.Errorf("two panes fit 30x6, got %d", len(leaves))
	}
	for _, term := range leaves {
		r := m.paneRects(app)[term]
		if r.w < minPaneCols || r.h < minPaneRows {
			t.Errorf("invalid pane size %+v", r)
		}
	}
	if !m.layouts[app].contains(app.ActiveTerminal()) {
		t.Error("the focused terminal must be the visible one")
	}
	var hidden *workspace.Terminal
	for _, term := range app.Terminals {
		if !m.layouts[app].contains(term) {
			hidden = term
		}
	}
	m = press(m, "alt+j", "alt+j", "alt+k") // navigation stays among the visible panes
	if m.layouts[app].contains(hidden) || !m.layouts[app].contains(app.ActiveTerminal()) {
		t.Error("alt+j/k must not reveal a hidden terminal")
	}
	m = press(m, "esc", "m")
	if !strings.Contains(m.View(), "hidden") {
		t.Errorf("manager should list the hidden terminal:\n%s", m.View())
	}
	for _, term := range app.Terminals { // rows follow creation order: move to the hidden one
		if term == hidden {
			break
		}
		m = press(m, "j")
	}
	m = press(m, "enter")
	if !m.layouts[app].contains(hidden) || app.ActiveTerminal() != hidden || len(*spawned) != 3 || hidden.Session.(*fakeSession).closed {
		t.Errorf("the manager shows the hidden terminal explicitly (it takes over a pane, nothing spawns): visible=%v", m.layouts[app].leaves())
	}
	if len(m.layouts[app].leaves()) != 2 {
		t.Errorf("no room for a third pane: %d visible", len(m.layouts[app].leaves()))
	}
}

func TestOpenAllAt80x24StaysUsable(t *testing.T) {
	m, _, _ := savedProject(t, false)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	m = press(m, "m", "a")
	app := m.ws.Projects[0]
	rs := m.paneRects(app)
	for term, r := range rs {
		if r.w < minPaneCols || r.h < minPaneRows {
			t.Errorf("pane %s too small at 80x24: %+v", term.Name, r)
		}
	}
	if len(rs) < 2 {
		t.Errorf("80x24 should still show at least two panes, got %d", len(rs))
	}
	view := m.View()
	if lines := strings.Count(view, "\n") + 1; lines != 24 {
		t.Errorf("view has %d lines at 80x24", lines)
	}
}

func TestOpenAllOnProjectWithNothingSaved(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "m", "a")
	if len(*spawned) != 0 || m.panel == nil || !strings.Contains(m.panel.notice, "nothing to restore") {
		t.Errorf("open all with nothing saved should just say so: spawned=%d panel=%v", len(*spawned), m.panel)
	}
}
