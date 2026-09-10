package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/state"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// exit simulates a terminal's process ending (the watcher reports it).
func exit(m Model, id int) Model {
	next, _ := m.Update(terminalExitedMsg{id: id})
	return next.(Model)
}

func savedNames(root string) []string {
	f, _ := state.Load(root)
	var out []string
	for _, p := range f.Projects {
		for _, t := range p.Terminals {
			out = append(out, t.Name)
		}
	}
	return out
}

func TestAltXHidesWithoutStoppingAndEnterShowsItAgain(t *testing.T) {
	m, spawned, root := gitModel(t)
	m = makeTerminal(t, m, "n", "API", "")
	p := m.ws.Projects[0]
	api := p.Terminals[0]

	m = press(m, "alt+x")
	if (*spawned)[0].closed {
		t.Fatal("alt+x must not close the PTY")
	}
	if len(p.Terminals) != 1 || m.layouts[p] != nil {
		t.Errorf("hidden terminal keeps running but leaves the layout: running=%d layout=%v", len(p.Terminals), m.layouts[p])
	}
	if len(p.Saved) != 0 {
		t.Errorf("a hidden terminal is running, not saved: %+v", p.Saved)
	}
	if m.focus != focusSidebar || !strings.Contains(m.View(), "1 terminal running, all hidden") {
		t.Errorf("with nothing visible the sidebar takes focus and the main pane says so:\n%s", m.View())
	}
	if got := savedNames(root); len(got) != 1 || got[0] != "API" {
		t.Fatalf("definition must stay in the state file, got %v", got)
	}
	m = press(m, "m")
	if v := m.View(); !strings.Contains(v, "○ Terminal 1 · main") || !strings.Contains(v, "hidden") || strings.Contains(v, "saved") {
		t.Errorf("manager should list API as hidden:\n%s", v)
	}
	m = press(m, "esc", "enter") // Enter on the project shows its terminal again
	if len(*spawned) != 1 || !m.layouts[p].contains(api) || p.ActiveTerminal() != api || m.focus != focusMain {
		t.Errorf("enter should show the hidden terminal without spawning: spawned=%d", len(*spawned))
	}
}

func TestShellExitingBehavesLikeAltX(t *testing.T) {
	m, spawned, root := gitModel(t)
	m = makeTerminal(t, m, "n", "Dev", "")
	m = exit(m, m.ws.Projects[0].Terminals[0].ID) // the user typed exit
	if got := savedNames(root); len(got) != 1 || got[0] != "Dev" {
		t.Fatalf("an exited shell must stay restorable, state = %v", got)
	}
	m = press(m, "enter")
	if len(*spawned) != 2 || m.ws.Projects[0].Terminals[0].Name != "Dev" {
		t.Error("enter should recreate the exited terminal")
	}
}

func TestAltDeleteAsksThenForgetsForGood(t *testing.T) {
	m, spawned, root := gitModel(t)
	m = makeTerminal(t, m, "n", "Login", "develop")
	app := m.ws.Projects[0]
	wt := app.Terminals[0].Worktree.Path

	m = press(m, "alt+delete")
	if m.deleting == nil || (*spawned)[0].closed || len(app.Terminals) != 1 {
		t.Fatal("alt+delete must ask before doing anything")
	}
	view := m.View()
	for _, want := range []string{`Delete Terminal 1 and its worktree "develop"?`, "The worktree is clean", "y confirm"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirmation missing %q:\n%s", want, view)
		}
	}
	m = press(m, "y")
	if m.deleting != nil || !(*spawned)[0].closed || len(app.Terminals) != 0 || m.layouts[app] != nil {
		t.Fatalf("confirm should close and remove the terminal: deleting=%v closed=%v terminals=%d", m.deleting, (*spawned)[0].closed, len(app.Terminals))
	}
	m = exit(m, 1) // the late exit message of the killed session is ignored
	if len(app.Saved) != 0 || len(savedNames(root)) != 0 {
		t.Errorf("definition must be gone from memory and disk: saved=%+v state=%v", app.Saved, savedNames(root))
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("the dedicated worktree is removed with its terminal")
	}
	if list, _ := m.ws.Worktrees(app); len(list) != 0 {
		t.Errorf("git should no longer list the worktree: %+v", list)
	}
	if !strings.Contains(m.notice, `deleted Terminal 1 and its worktree`) {
		t.Errorf("notice = %q", m.notice)
	}
	if m.focus != focusSidebar {
		t.Error("no terminal left: focus returns to the sidebar")
	}
	m = press(m, "enter") // Enter does not bring it back: the project is empty, so a fresh shell opens
	if len(*spawned) != 2 || app.Terminals[0].Name != "" {
		t.Errorf("deleted terminal must not return; got %+v", app.Terminals)
	}
	m = press(m, "alt+s", "m")
	if v := m.View(); strings.Contains(v, "Login") || strings.Contains(v, "Unused worktrees") { // neither the terminal nor an orphan remains
		t.Errorf("manager should list neither a Login terminal nor an unused worktree:\n%s", v)
	}
}

func TestAltDeleteCancelChangesNothing(t *testing.T) {
	m, spawned, root := gitModel(t)
	m = makeTerminal(t, m, "n", "Keep", "")
	app := m.ws.Projects[0]
	before, _ := state.Load(root)
	for _, cancel := range []string{"esc", "n", "z"} {
		m = press(m, "alt+delete")
		if m.deleting == nil {
			t.Fatal("expected confirmation")
		}
		m = press(m, cancel)
		if m.deleting != nil || (*spawned)[0].closed || len(app.Terminals) != 1 || len(app.Saved) != 0 || m.focus != focusMain {
			t.Errorf("%s should cancel without changes", cancel)
		}
	}
	after, _ := state.Load(root)
	if dumpLayout(before.Projects[0].Layout) != dumpLayout(after.Projects[0].Layout) || len(after.Projects[0].Terminals) != 1 {
		t.Error("cancelled delete must not touch the state file")
	}
	if strings.Join((*spawned)[0].text, "") != "" {
		t.Error("the cancelling key must not leak into the terminal")
	}
}

// threePanes builds Login | (Payment / Dev) with named plain terminals.
func threePanes(t *testing.T) (Model, *[]*fakeSession, string) {
	t.Helper()
	m, spawned, root := gitModel(t)
	for i, step := range []struct{ key, name string }{{"n", "Login"}, {"alt+v", "Payment"}, {"alt+h", "Dev"}} {
		m = makeTerminal(t, m, step.key, step.name, "")
		if len(*spawned) != i+1 {
			t.Fatalf("setup step %d spawned %d", i, len(*spawned))
		}
	}
	return m, spawned, root
}

func TestAltDeleteRepairsLayoutAndFocus(t *testing.T) {
	// Focused leaf in a nested split: Dev (under Payment) goes, Payment fills the column.
	m, spawned, root := threePanes(t)
	app := m.ws.Projects[0]
	m = press(m, "alt+delete", "y")
	if got := strings.Join(names(m.layouts[app]), ","); got != "Login,Payment" {
		t.Errorf("leaves after deleting Dev = %s", got)
	}
	r := m.layouts[app]
	if r.dir != splitSide || !r.b.isLeaf() || m.paneRects(app)[r.b.term].h != m.mainArea().h {
		t.Errorf("stack should collapse to Payment filling the right column: %+v", r)
	}
	if app.ActiveTerminal() == nil || app.ActiveTerminal().Name != "Payment" || m.focus != focusMain {
		t.Errorf("focus should move to a remaining terminal, got %v", app.ActiveTerminal())
	}
	if got := savedNames(root); strings.Join(got, ",") != "Login,Payment" {
		t.Errorf("state after delete = %v", got)
	}
	f, _ := state.Load(root)
	if l := f.Projects[0].Layout; l == nil || l.Direction != state.DirSide || l.First.Terminal == nil || l.Second.Terminal == nil {
		t.Errorf("persisted layout should be the collapsed two-leaf tree: %s", dumpLayout(f.Projects[0].Layout))
	}
	if (*spawned)[0].closed || (*spawned)[1].closed || !(*spawned)[2].closed {
		t.Error("only Dev's session should be closed")
	}

	// Non-nested root leaf: delete Login, Payment becomes the only pane.
	m = press(m, "alt+left", "alt+delete", "y")
	if !m.layouts[app].isLeaf() || m.layouts[app].term.Name != "Payment" || m.paneRects(app)[app.Terminals[0]] != m.mainArea() {
		t.Errorf("single remaining pane should fill the area: %+v", m.layouts[app])
	}
	// Last one: delete Payment, nothing left.
	m = press(m, "alt+delete", "y")
	if len(app.Terminals) != 0 || m.layouts[app] != nil || m.focus != focusSidebar || len(savedNames(root)) != 0 {
		t.Errorf("deleting the last terminal should empty the project: terminals=%d state=%v", len(app.Terminals), savedNames(root))
	}
	f, _ = state.Load(root)
	if len(f.Projects) != 0 {
		t.Errorf("no dangling project/layout expected, got %+v", f.Projects)
	}
}

func TestAltDeleteOfSavedOnlyDefinitionViaPanelStillWorks(t *testing.T) {
	// A closed (saved) terminal cannot receive alt+delete; the panel's d forgets it.
	m, _, root := gitModel(t)
	m = makeTerminal(t, m, "n", "Old", "")
	m = exit(m, m.ws.Projects[0].Terminals[0].ID) // the shell exits: Old is saved, not running
	m = press(m, "m", "d", "y")
	if len(savedNames(root)) != 0 || len(m.ws.Projects[0].Saved) != 0 {
		t.Error("d in the panel should forget the saved definition")
	}
}

func TestDeletingRunningTerminalLeavesNoDanglingLayoutReferences(t *testing.T) {
	m, _, root := threePanes(t)
	m = press(m, "alt+left", "alt+delete", "y") // delete Login (root.a)
	f, _ := state.Load(root)
	p := f.Projects[0]
	seen := map[int]bool{}
	var walk func(l *state.Layout)
	walk = func(l *state.Layout) {
		if l == nil {
			return
		}
		if l.Terminal != nil {
			if *l.Terminal < 0 || *l.Terminal >= len(p.Terminals) || seen[*l.Terminal] {
				t.Errorf("dangling or duplicate layout reference %d", *l.Terminal)
			}
			seen[*l.Terminal] = true
		}
		walk(l.First)
		walk(l.Second)
	}
	walk(p.Layout)
	if len(seen) != 2 || len(p.Terminals) != 2 {
		t.Errorf("expected two referenced terminals, got refs=%d terminals=%d", len(seen), len(p.Terminals))
	}
	if p.Focused == nil || p.Terminals[*p.Focused].Name == "Login" {
		t.Errorf("focus must not point at the deleted terminal: %v", p.Focused)
	}
	_ = workspace.WorktreesDir
}
