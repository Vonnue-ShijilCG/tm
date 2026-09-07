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

func TestEnterOnEmptyProjectOpensFirstShell(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "j", "enter")
	if len(*spawned) != 1 || (*spawned)[0].dir != "/ws/ecommerce" || m.focus != focusMain {
		t.Errorf("first use: enter should open one shell in the project: spawned=%+v focus=%v", *spawned, m.focus)
	}
}

// stateLeaves counts the terminal references in a saved layout.
func stateLeaves(l *state.Layout) int {
	if l == nil {
		return 0
	}
	if l.Terminal != nil {
		return 1
	}
	return stateLeaves(l.First) + stateLeaves(l.Second)
}

func TestEnterRestoresEveryTerminalAndKeepsTheLayout(t *testing.T) {
	m, root := arranged(t) // Login | (Payment / Dev), stack ratio 0.4, Login focused, then quit
	m2, spawned := restartModel(t, m)
	app := m2.ws.Projects[0]
	view := m2.View()
	if strings.Contains(view, "saved") || !strings.Contains(view, "○ app") {
		t.Errorf("sidebar must not expose saved counts:\n%s", view)
	}

	m2 = press(m2, "enter") // enter should restore every saved terminal, not just the focused one
	if len(*spawned) != 3 || len(app.Terminals) != 3 {
		t.Fatalf("enter should restore all saved terminals, spawned=%d terminals=%+v", len(*spawned), app.Terminals)
	}
	r := m2.layouts[app]
	if got := strings.Join(names(r), ","); got != "Login,Payment,Dev" || r.dir != splitSide || r.b.dir != splitStack {
		t.Errorf("saved layout not rebuilt: %s %+v", got, r)
	}
	if ratio := r.b.ratio; ratio < 0.39 || ratio > 0.41 {
		t.Errorf("ratio not restored: %v", ratio)
	}
	if app.ActiveTerminal().Name != "Login" || m2.focus != focusMain || m2.notice != "" {
		t.Errorf("saved focus honoured, no notice: active=%v focus=%v notice=%q", app.ActiveTerminal(), m2.focus, m2.notice)
	}
	if len(app.Saved) != 0 {
		t.Errorf("nothing should be left saved once everything is restored: saved=%d", len(app.Saved))
	}
	if list, _ := m2.ws.Worktrees(app); len(list) != 2 {
		t.Errorf("worktrees must be reused, got %d", len(list))
	}
	dirs := map[string]bool{}
	for _, s := range *spawned {
		dirs[s.dir] = true
	}
	for _, want := range []string{filepath.Join(root, workspace.WorktreesDir, "app", "develop"), filepath.Join(root, workspace.WorktreesDir, "app", "feature-payment"), app.Path} {
		if !dirs[want] {
			t.Errorf("no shell started in %s", want)
		}
	}
}

func TestEnterOnRunningProjectFocusesAndCreatesNothing(t *testing.T) {
	m, spawned, p := twoPanes(t)
	m = press(m, "esc", "enter")
	if len(*spawned) != 2 || len(p.Terminals) != 2 || m.focus != focusMain || p.ActiveTerminal() != p.Terminals[1] {
		t.Errorf("enter on a running project must just focus it: spawned=%d focus=%v", len(*spawned), m.focus)
	}
	// Hidden active terminal is revealed rather than duplicated.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 9})
	m = next.(Model)
	m = press(m, "alt+n", "enter") // third terminal takes over a pane; one is hidden
	m = press(m, "alt+j")          // cycle to the hidden one, then leave and come back
	m = press(m, "esc", "enter")
	if len(*spawned) != 3 || !m.layouts[p].contains(p.ActiveTerminal()) {
		t.Errorf("enter should reveal the active terminal without spawning: spawned=%d", len(*spawned))
	}
}

func TestEnterPartialRestoreReportsUnavailable(t *testing.T) {
	m, root := arranged(t)
	if err := os.RemoveAll(filepath.Join(root, workspace.WorktreesDir, "app", "feature-payment")); err != nil {
		t.Fatal(err)
	}
	m2, spawned := restartModel(t, m)
	m2 = press(m2, "enter")
	app := m2.ws.Projects[0]
	if len(*spawned) != 2 || len(app.Terminals) != 2 {
		t.Fatalf("enter should restore everything it can (Login and Dev) and skip Payment, spawned=%d", len(*spawned))
	}
	if !strings.Contains(m2.notice, "restored 2 terminals;") || !strings.Contains(m2.notice, "1 unavailable") || !strings.Contains(m2.notice, "Payment") {
		t.Errorf("notice = %q", m2.notice)
	}
	if _, err := os.Stat(filepath.Join(root, workspace.WorktreesDir, "app", "feature-payment")); !os.IsNotExist(err) {
		t.Error("missing worktree must not be recreated")
	}
	if got := strings.Join(names(m2.layouts[app]), ","); got != "Login,Dev" {
		t.Errorf("layout should collapse around the missing terminal: %s", got)
	}
	if app.ActiveTerminal().Name != "Login" || m2.focus != focusMain {
		t.Errorf("saved focus (Login) is still available so it should win: %v", app.ActiveTerminal())
	}
}

func TestEnterFallsBackWhenTheSavedFocusIsUnavailable(t *testing.T) {
	m, root := arranged(t) // Login focused
	if err := os.RemoveAll(filepath.Join(root, workspace.WorktreesDir, "app", "develop")); err != nil {
		t.Fatal(err)
	}
	m2, spawned := restartModel(t, m)
	m2 = press(m2, "enter")
	app := m2.ws.Projects[0]
	if len(*spawned) != 2 || len(app.Terminals) != 2 {
		t.Fatalf("Payment and Dev are restorable even though Login (the saved focus) is not: spawned=%d %+v", len(*spawned), app.Terminals)
	}
	if !strings.Contains(m2.notice, "restored 2 terminals;") || !strings.Contains(m2.notice, "Login") {
		t.Errorf("notice should explain the fallback: %q", m2.notice)
	}
	// Login (unavailable) drops out of the saved side|stack layout, leaving
	// just the Payment/Dev stack; arrange places Dev last, so Dev is where
	// focus falls back to.
	if app.ActiveTerminal().Name != "Dev" {
		t.Errorf("the unavailable saved focus cannot win, want fallback to Dev: %v", app.ActiveTerminal())
	}
	if _, err := os.Stat(filepath.Join(root, workspace.WorktreesDir, "app", "develop")); !os.IsNotExist(err) {
		t.Error("missing worktree must not be recreated")
	}
}

func TestEnterWhenNothingIsRestorableOpensFreshShell(t *testing.T) {
	m, _, root := gitModel(t)
	m = makeTerminal(t, m, "n", "Gone", "develop")
	m = press(m, "esc")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if err := os.RemoveAll(filepath.Join(root, workspace.WorktreesDir, "app", "develop")); err != nil { // Gone lives on develop
		t.Fatal(err)
	}
	m2, spawned := restartModel(t, m)
	m2 = press(m2, "enter")
	app := m2.ws.Projects[0]
	if len(*spawned) != 1 || (*spawned)[0].dir != app.Path || app.Terminals[0].Worktree != nil || m2.focus != focusMain {
		t.Errorf("a fresh shell should open in the project dir: spawned=%+v", *spawned)
	}
	if !strings.Contains(m2.notice, "restored 0 terminals") || !strings.Contains(m2.notice, "Gone") {
		t.Errorf("notice should explain what was unavailable: %q", m2.notice)
	}
	if _, err := os.Stat(filepath.Join(root, workspace.WorktreesDir, "app", "develop")); !os.IsNotExist(err) {
		t.Error("missing worktree must not be recreated")
	}
}

func TestEnterKeepsProjectsSeparate(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+v", "enter", "esc") // backend: 2 panes
	m = press(m, "j", "enter", "esc")              // ecommerce: 1
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if len(*spawned) != 3 {
		t.Fatalf("setup spawned %d", len(*spawned))
	}
	m2, spawned2 := restartModel(t, m)
	backend, ecommerce := m2.ws.Projects[0], m2.ws.Projects[1]
	m2 = press(m2, "enter")             // backend: both of its saved shells come back
	m2 = press(m2, "esc", "j", "enter") // ecommerce
	m2 = press(m2, "esc", "k", "enter") // back to backend: running, so nothing new
	if len(*spawned2) != 3 {
		t.Fatalf("spawned %d, want 3 (backend's two plus ecommerce's one, no duplicates on re-enter)", len(*spawned2))
	}
	if len(m2.layouts[backend].leaves()) != 2 || len(m2.layouts[ecommerce].leaves()) != 1 || len(backend.Saved) != 0 {
		t.Errorf("layouts mixed: backend=%d ecommerce=%d backend saved=%d", len(m2.layouts[backend].leaves()), len(m2.layouts[ecommerce].leaves()), len(backend.Saved))
	}
	for _, term := range m2.layouts[backend].leaves() {
		if term.Session.(*fakeSession).dir != "/ws/backend" {
			t.Error("foreign terminal in backend layout")
		}
	}
	if m2.selected != 0 || m2.focus != focusMain {
		t.Error("re-entering backend should show its workspace")
	}
}

func TestHelpAndPanelWording(t *testing.T) {
	m, _ := newTestModel(t)
	view := press(m, "s").View()
	for _, want := range []string{"open/resume", "manage terminals", "hide terminal", "delete forever", "restore all", "show / restore"} {
		if !strings.Contains(view, want) {
			t.Errorf("help missing %q", want)
		}
	}
	if strings.Contains(view, "saved") {
		t.Error("help should not talk about saved terminals")
	}
	status := m.viewStatus()
	if !strings.Contains(status, "open/resume") || strings.Contains(status, "saved") {
		t.Errorf("sidebar hints should read open/resume without persistence jargon: %s", status)
	}
	m = press(m, "m")
	if !strings.Contains(m.View(), "Manage terminals") || !strings.Contains(m.viewStatus(), "restore all") {
		t.Errorf("m should open the terminal manager with restore all:\n%s", m.View())
	}
}
