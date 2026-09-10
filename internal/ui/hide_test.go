package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/state"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// repoWithThree opens a plain shell plus two worktree terminals (Login on
// develop, Payment on feature/payment) in the git fixture and returns them.
func repoWithThree(t *testing.T) (Model, *[]*fakeSession, *workspace.Project) {
	t.Helper()
	m, spawned, _ := gitModel(t)
	m = press(m, "enter") // terminal 1: plain shell in the checkout
	m = makeTerminal(t, m, "alt+n", "Login", "develop")
	m = makeTerminal(t, m, "alt+n", "Payment", "feature/payment")
	p := m.ws.Projects[0]
	if len(*spawned) != 3 || len(m.layouts[p].leaves()) != 3 {
		t.Fatalf("setup: spawned=%d visible=%d", len(*spawned), len(m.layouts[p].leaves()))
	}
	return m, spawned, p
}

func TestAltXHidesWorktreeTerminalAndManagerRestoresItReusingTheWorktree(t *testing.T) {
	m, spawned, p := repoWithThree(t)
	login := p.Terminals[1]
	wtPath := login.Worktree.Path
	before, _ := m.ws.Worktrees(p)
	for p.ActiveTerminal() != login {
		m = press(m, "alt+k")
	}
	m = press(m, "alt+x")

	if (*spawned)[1].closed {
		t.Fatal("alt+x must keep the PTY running")
	}
	if !m.layouts[p].contains(p.Terminals[0]) || !m.layouts[p].contains(p.Terminals[2]) || m.layouts[p].contains(login) {
		t.Errorf("Login's pane should be gone and the others kept: %v", names(m.layouts[p]))
	}
	if len(p.Terminals) != 3 || len(p.Saved) != 0 {
		t.Errorf("hidden is neither closed nor saved: running=%d saved=%d", len(p.Terminals), len(p.Saved))
	}
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		t.Error("hiding must not touch the worktree")
	}
	if after, _ := m.ws.Worktrees(p); len(after) != len(before) {
		t.Errorf("worktrees changed on hide: %d -> %d", len(before), len(after))
	}
	m = press(m, "alt+j", "alt+k", "alt+j")
	if m.layouts[p].contains(login) {
		t.Error("navigation must not reveal the hidden terminal")
	}

	m = press(m, "alt+s", "m")
	view := m.View()
	for _, want := range []string{"Manage terminals", "● Terminal 1 · main", "○ Terminal 2 · ⎇ develop", "hidden", "● Terminal 3 · ⎇ feature/payment", "visible"} {
		if !strings.Contains(view, want) {
			t.Errorf("manager missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Unused worktrees") || strings.Contains(view, "saved") {
		t.Errorf("no saved rows and no unused worktrees expected:\n%s", view)
	}
	m = press(m, "j", "enter") // select Login, show it
	if len(*spawned) != 3 || !m.layouts[p].contains(login) || p.ActiveTerminal() != login || m.focus != focusMain || m.panel != nil {
		t.Errorf("show should bring back only Login without spawning: spawned=%d visible=%v", len(*spawned), names(m.layouts[p]))
	}
	if len(m.layouts[p].leaves()) != 3 {
		t.Errorf("all three visible again: %v", names(m.layouts[p]))
	}
	if after, _ := m.ws.Worktrees(p); len(after) != len(before) {
		t.Errorf("show must reuse the worktree, not create one: %d -> %d", len(before), len(after))
	}
	if login.Worktree.Path != wtPath || (*spawned)[1].dir != wtPath {
		t.Error("the same session in the same worktree")
	}
	checkTiling(t, m, p)
}

func TestManagerHideAndShowReturnPaneToItsSavedPlace(t *testing.T) {
	m, _, p := gridModel(t, 4, 100, 30) // 2x2: 1 2 / 3 4
	rs := m.paneRects(p)
	second := p.Terminals[1]
	was := rs[second]
	m = press(m, "alt+s", "m", "j", "h") // hide terminal 2 from the manager
	if m.panel == nil || m.layouts[p].contains(second) || len(m.layouts[p].leaves()) != 3 || second.Session.(*fakeSession).closed {
		t.Fatalf("h should hide terminal 2 and keep it running: visible=%d", len(m.layouts[p].leaves()))
	}
	if !strings.Contains(m.View(), "hidden; it keeps running") {
		t.Errorf("manager should confirm the hide:\n%s", m.View())
	}
	checkTiling(t, m, p)
	m = press(m, "h") // already hidden
	if !strings.Contains(m.View(), "already hidden") {
		t.Errorf("hiding twice should just say so:\n%s", m.View())
	}
	m = press(m, "enter") // show it again
	if !m.layouts[p].contains(second) || len(m.layouts[p].leaves()) != 4 || m.panel != nil {
		t.Fatalf("enter should show terminal 2: visible=%d", len(m.layouts[p].leaves()))
	}
	if got := m.paneRects(p)[second]; got != was {
		t.Errorf("terminal 2 should return to its saved place %+v, got %+v", was, got)
	}
	if shape(m.layouts[p]) != "2,2" {
		t.Errorf("2x2 restored, got %s", shape(m.layouts[p]))
	}
}

func TestManagerEnterRestoresOnlyTheSelectedSavedTerminal(t *testing.T) {
	m, _, root := savedProject(t, false) // Login, Payment, Dev saved after a restart
	app := m.ws.Projects[0]
	m = press(m, "m")
	view := m.View()
	for _, want := range []string{"○ Terminal · ⎇ develop", "○ Terminal · ⎇ feature/payment", "○ Terminal · main", "saved"} {
		if !strings.Contains(view, want) {
			t.Errorf("manager missing %q:\n%s", want, view)
		}
	}
	m = press(m, "j", "enter") // Payment
	if len(app.Terminals) != 1 || app.Terminals[0].Name != "Payment" || len(app.Saved) != 2 {
		t.Fatalf("only Payment should start: running=%+v saved=%d", app.Terminals, len(app.Saved))
	}
	if app.Terminals[0].Worktree == nil || app.Terminals[0].Worktree.Path != filepath.Join(root, workspace.WorktreesDir, "app", "feature-payment") {
		t.Errorf("Payment should reuse its worktree: %+v", app.Terminals[0].Worktree)
	}
	if list, _ := m.ws.Worktrees(app); len(list) != 2 {
		t.Errorf("no worktree may be created: %d", len(list))
	}
	if m.focus != focusMain || m.panel != nil || !m.layouts[app].isLeaf() {
		t.Error("the restored terminal is focused as the only pane")
	}
	m = press(m, "alt+s", "m", "j", "j", "enter") // rows: Payment (running), Login, Dev -> Dev
	if len(app.Terminals) != 2 || app.Terminals[1].Name != "Dev" || len(app.Saved) != 1 || app.Saved[0].Name != "Login" {
		t.Errorf("Dev should be added alone: running=%+v saved=%+v", app.Terminals, app.Saved)
	}
}

func TestManagerIsScopedToOneProject(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "alt+n", "enter", "alt+s") // backend: 2
	m = press(m, "j", "enter", "alt+s")              // ecommerce: 1
	m = press(m, "k", "m")
	if v := m.View(); m.panel.project != m.ws.Projects[0] || !strings.Contains(v, "Terminal 1") || !strings.Contains(v, "Terminal 2") {
		t.Errorf("backend manager wrong:\n%s", v)
	}
	if len(m.panel.rows) != 2 {
		t.Errorf("backend has 2 rows, got %d", len(m.panel.rows))
	}
	m = press(m, "esc", "j", "m")
	if v := m.View(); m.panel.project != m.ws.Projects[1] || len(m.panel.rows) != 1 || strings.Contains(v, "Terminal 2") {
		t.Errorf("ecommerce manager wrong (%d rows):\n%s", len(m.panel.rows), v)
	}
}

func TestManagerNewUsesTheFormAndRespectsTheLimit(t *testing.T) {
	write := configFile(t)
	write("max_terminals = 2\n")
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+s", "m", "n")
	if m.form == nil || m.panel != nil {
		t.Fatal("n in the manager should open the new-terminal form")
	}
	m = press(m, "enter")
	if len(*spawned) != 2 {
		t.Fatalf("form should create the second terminal, spawned=%d", len(*spawned))
	}
	m = press(m, "alt+s", "m", "n")
	if m.form != nil || m.panel == nil || !strings.Contains(m.panel.notice, "maximum terminals reached (2)") {
		t.Errorf("at the limit the manager stays open and explains: form=%v panel=%+v", m.form != nil, m.panel)
	}
	// A hidden running terminal still counts.
	m = press(m, "esc", "tab", "alt+x")
	p := m.ws.Projects[0]
	if len(p.Terminals) != 2 || len(m.layouts[p].leaves()) != 1 {
		t.Fatalf("setup: running=%d visible=%d", len(p.Terminals), len(m.layouts[p].leaves()))
	}
	m = press(m, "alt+n")
	if m.form != nil || !strings.Contains(m.notice, "maximum terminals reached (2)") {
		t.Errorf("hidden terminals count toward the limit: %q", m.notice)
	}
	// A saved (exited) one does not.
	hidden := p.Terminals[0]
	if m.layouts[p].contains(hidden) {
		hidden = p.Terminals[1]
	}
	_ = hidden.Session.(*fakeSession).Close()
	m = exit(m, hidden.ID)
	m = press(m, "alt+n", "enter")
	if len(*spawned) != 3 || len(p.Terminals) != 2 {
		t.Errorf("after the hidden shell exits capacity is free: spawned=%d running=%d", len(*spawned), len(p.Terminals))
	}
}

func TestManagerDeleteIsSeparateFromHide(t *testing.T) {
	m, spawned, p := repoWithThree(t)
	login := p.Terminals[1]
	wtPath := login.Worktree.Path
	m = press(m, "alt+s", "m", "j", "d")
	if !m.panel.confirm || !strings.Contains(m.View(), "Delete Terminal 2 and its worktree \"develop\"") || !strings.Contains(m.View(), "worktree is clean") {
		t.Fatalf("d should ask first and say the worktree goes too:\n%s", m.View())
	}
	m = press(m, "n") // anything but y cancels
	if m.panel.confirm || (*spawned)[1].closed || len(p.Terminals) != 3 {
		t.Fatal("cancel must change nothing")
	}
	if _, err := os.Stat(filepath.Join(wtPath, ".git")); err != nil {
		t.Fatal("cancel must keep the worktree")
	}
	m = press(m, "d", "y")
	if !(*spawned)[1].closed || len(p.Terminals) != 2 || len(p.Saved) != 0 || m.layouts[p].contains(login) {
		t.Errorf("delete stops the process and forgets the definition: running=%d saved=%d", len(p.Terminals), len(p.Saved))
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("the dedicated worktree is removed with the terminal")
	}
	if list, _ := m.ws.Worktrees(p); len(list) != 1 || list[0].Path != p.Terminals[1].Worktree.Path {
		t.Errorf("only Payment's worktree should remain: %+v", list)
	}
	if v := m.View(); strings.Contains(v, "Unused worktrees") || len(m.panel.rows) != 2 {
		t.Errorf("nothing is left behind to list:\n%s", v)
	}
	if !strings.Contains(m.View(), "deleted Terminal 2 and its worktree") {
		t.Errorf("manager should report the deletion:\n%s", m.View())
	}
	checkTiling(t, m, p)
}

func TestUnusedWorktreeCleanupFromManager(t *testing.T) {
	m, _, p := repoWithThree(t)
	wtPath := orphanWorktree(t, m, p, "old-login", "feature/old-login")
	m = press(m, "alt+s", "m")
	if len(m.panel.rows) != 4 || m.panel.rows[3].worktree == nil || m.panel.rows[3].worktree.Path != wtPath {
		t.Fatalf("expected three terminals then one unused worktree: %+v", m.panel.rows)
	}
	m = press(m, "j", "j", "j", "enter") // enter on a worktree does nothing
	if m.panel == nil || len(p.Terminals) != 3 || !strings.Contains(m.View(), "not a terminal") {
		t.Errorf("enter on an unused worktree must not open anything:\n%s", m.View())
	}
	if err := os.WriteFile(filepath.Join(wtPath, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = press(m, "esc", "m", "j", "j", "j")
	if !strings.Contains(m.View(), "dirty") {
		t.Errorf("dirty worktree should be flagged:\n%s", m.View())
	}
	m = press(m, "d")
	if !strings.Contains(m.View(), "UNCOMMITTED CHANGES") {
		t.Errorf("dirty removal warns:\n%s", m.View())
	}
	m = press(m, "esc")
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatal("cancelled removal keeps the worktree")
	}
	m = press(m, "d", "y")
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("confirmed removal deletes the worktree")
	}
	if len(m.panel.rows) != 3 || strings.Contains(m.View(), "Unused worktrees") {
		t.Errorf("unused section disappears when empty:\n%s", m.View())
	}
	branches, _, _ := m.ws.Branches(p)
	if !strings.Contains(strings.Join(branches, ","), "feature/old-login") {
		t.Error("the branch survives")
	}
}

func TestHidingTheLastVisibleTerminalIsAllowed(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter")
	p := m.ws.Projects[0]
	m = press(m, "alt+x")
	if (*spawned)[0].closed || len(p.Terminals) != 1 || m.layouts[p] != nil {
		t.Fatalf("hidden, still running: closed=%v running=%d", (*spawned)[0].closed, len(p.Terminals))
	}
	if m.focus != focusSidebar || m.zoom {
		t.Error("focus returns to the sidebar")
	}
	if v := m.View(); !strings.Contains(v, "1 terminal running, all hidden") || !strings.Contains(v, "Press m to show one") || !strings.Contains(v, "● backend") {
		t.Errorf("main pane should explain, sidebar keeps the running marker:\n%s", v)
	}
	m = press(m, "tab") // nothing visible to focus
	if m.focus != focusSidebar {
		t.Error("tab must not focus an all-hidden workspace")
	}
	m = press(m, "enter")
	if len(*spawned) != 1 || !m.layouts[p].contains(p.Terminals[0]) || m.focus != focusMain || len(m.layouts[p].leaves()) != 1 {
		t.Errorf("enter shows the running terminal as one pane, creates nothing: spawned=%d", len(*spawned))
	}
}

func TestSavedLayoutRestoresEntirelyOnEnter(t *testing.T) {
	m, _, _ := gridModel(t, 4, 100, 30)
	root := m.root
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	f0, _ := state.Load(root)
	if stateLeaves(f0.Projects[0].Layout) != 4 {
		t.Fatalf("setup layout leaves = %d", stateLeaves(f0.Projects[0].Layout))
	}
	m2, spawned := restartModel(t, m)
	p := m2.ws.Projects[0]
	m2 = press(m2, "enter") // everything comes back at once, in the saved 2x2 grid
	if len(*spawned) != 4 || shape(m2.layouts[p]) != "2,2" {
		t.Fatalf("enter should restore the 2x2 grid: spawned=%d shape=%s", len(*spawned), shape(m2.layouts[p]))
	}
	// Now everything the layout knows is visible: a resize is the user's new layout.
	m2 = press(m2, "alt+=")
	f2, _ := state.Load(root)
	if dumpLayout(f2.Projects[0].Layout) == dumpLayout(f0.Projects[0].Layout) {
		t.Error("with everything visible, layout changes are saved again")
	}
}

func TestWorktreeTerminalsAreIsolatedFromTheCheckout(t *testing.T) {
	m, spawned, p := repoWithThree(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	main := (*spawned)[0].dir
	loginDir := (*spawned)[1].dir
	if main != p.Path || loginDir == main || (*spawned)[2].dir == loginDir {
		t.Fatalf("each terminal has its own directory: %q %q %q", main, loginDir, (*spawned)[2].dir)
	}
	branch := func(dir string) string {
		out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if branch(main) != "main" || branch(loginDir) != "develop" {
		t.Fatalf("branches: main=%q login=%q", branch(main), branch(loginDir))
	}
	// What "terminal 2" does to its checkout stays there.
	if out, err := exec.Command("git", "-C", loginDir, "checkout", "-q", "-b", "experiment").CombinedOutput(); err != nil {
		t.Fatalf("checkout in worktree: %v\n%s", err, out)
	}
	if branch(main) != "main" || branch(loginDir) != "experiment" {
		t.Errorf("checkout in the worktree must not touch the main checkout: main=%q login=%q", branch(main), branch(loginDir))
	}
	// Hiding and showing keeps the same worktree (and its branch) attached.
	login := p.Terminals[1]
	for p.ActiveTerminal() != login {
		m = press(m, "alt+k")
	}
	m = press(m, "alt+x", "alt+s", "m", "j", "enter")
	if login.Worktree.Path != loginDir || (*spawned)[1].dir != loginDir || len(*spawned) != 3 {
		t.Error("show must reuse the very same worktree")
	}
	if list, _ := m.ws.Worktrees(p); len(list) != 2 {
		t.Errorf("no duplicate worktree: %d", len(list))
	}
}
