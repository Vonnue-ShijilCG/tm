package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// restartModel builds a fresh Model over the same workspace as m, as if tm
// had been started again, with a new fake spawner.
func restartModel(t *testing.T, m Model) (Model, *[]*fakeSession) {
	t.Helper()
	spawned := &[]*fakeSession{}
	var projects []project.Project
	for _, p := range m.ws.Projects {
		projects = append(projects, p.Project)
	}
	fresh := New(m.root, projects) // New restores state via the real spawner...
	fresh.ws = workspace.New(m.root, projects, func(dir string, cols, rows int) (workspace.Session, error) {
		f := newFakeSession(dir, cols, rows)
		*spawned = append(*spawned, f)
		return f, nil
	})
	if err := fresh.ws.Restore(); err != nil { // ...so restore again onto the fake-backed workspace
		t.Fatal(err)
	}
	next, _ := fresh.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return next.(Model), spawned
}

func TestStateIsSavedOnCreateCloseAndQuit(t *testing.T) {
	m, spawned, root := gitModel(t)
	stateFile := filepath.Join(root, state.FileName)

	m = makeTerminal(t, m, "n", "Login", "develop")
	f, err := state.Load(root)
	if err != nil || len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 1 || f.Projects[0].Terminals[0].Worktree != ".terminal-worktrees/app/develop" {
		t.Fatalf("state after create = %+v, %v", f, err)
	}

	m = press(m, "alt+n", "enter") // empty branch is rejected and the form moves to the branch field...
	if m.form == nil {
		t.Fatal("form should stay open after the branch error")
	}
	m = press(m, "tab", "tab", " ", "enter") // ...so two tabs (skipping Command) reach the worktree toggle; off -> plain terminal
	f, _ = state.Load(root)
	if len(f.Projects[0].Terminals) != 2 {
		t.Fatalf("state after second create = %+v", f.Projects[0].Terminals)
	}

	// Either terminal exiting keeps its definition restorable.
	next, _ := m.Update(terminalExitedMsg{id: m.ws.Projects[0].Terminals[1].ID})
	m = next.(Model)
	f, _ = state.Load(root)
	if len(f.Projects[0].Terminals) != 2 {
		t.Fatalf("state after closing plain terminal = %+v (definition must stay)", f.Projects[0].Terminals)
	}
	next, _ = m.Update(terminalExitedMsg{id: m.ws.Projects[0].Terminals[0].ID})
	m = next.(Model)
	f, _ = state.Load(root)
	if len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 2 {
		t.Fatalf("closed terminals should stay saved, state = %+v", f)
	}
	if _, err := os.Stat(filepath.Join(root, workspace.WorktreesDir, "app", "develop", ".git")); err != nil {
		t.Error("closing must not delete the worktree")
	}

	// Enter on the project restores both saved terminals at once; quit
	// saves them as running.
	m = press(m, "enter")
	hasLogin := false
	for _, term := range m.ws.Projects[0].Terminals {
		if term.Name == "Login" {
			hasLogin = true
		}
	}
	if len(*spawned) != 4 || len(m.ws.Projects[0].Terminals) != 2 || !hasLogin {
		t.Fatalf("enter should restore both saved terminals, spawned=%d terminals=%+v (notice=%q)", len(*spawned), m.ws.Projects[0].Terminals, m.notice)
	}
	m = press(m, "esc")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q should quit")
	}
	f, _ = state.Load(root)
	if len(f.Projects[0].Terminals) != 2 {
		t.Errorf("state after quit = %+v", f.Projects[0].Terminals)
	}
	if _, err := os.Stat(stateFile); err != nil {
		t.Error("state file should exist")
	}
}

func TestRestartOffersSavedTerminalsAndReopensThem(t *testing.T) {
	m, _, root := gitModel(t)
	m = makeTerminal(t, m, "n", "Login", "develop")
	m = makeTerminal(t, m, "alt+n", "Plain", "")
	if len(m.ws.Projects[0].Terminals) != 2 {
		t.Fatalf("setup: expected 2 terminals, got %d", len(m.ws.Projects[0].Terminals))
	}
	m = press(m, "esc")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}) // saves

	m2, spawned := restartModel(t, m)
	app := m2.ws.Projects[0]
	if len(app.Terminals) != 0 || len(*spawned) != 0 {
		t.Fatal("restart must not spawn processes automatically")
	}
	if len(app.Saved) != 2 {
		t.Fatalf("restart should restore 2 saved definitions, got %+v", app.Saved)
	}
	view := m2.View()
	if !strings.Contains(view, "○ app") || strings.Contains(view, "saved)") {
		t.Errorf("sidebar should mark a restorable project without exposing counts:\n%s", view)
	}

	m2 = press(m2, "m")
	view = m2.View()
	for _, want := range []string{"Manage terminals", "○ Terminal · ⎇ develop", "○ Terminal · main", "saved"} {
		if !strings.Contains(view, want) {
			t.Errorf("manager missing %q:\n%s", want, view)
		}
	}
	// Reopen the worktree terminal: reuses the existing worktree.
	m2 = press(m2, "enter")
	if len(*spawned) != 1 || (*spawned)[0].dir != filepath.Join(root, workspace.WorktreesDir, "app", "develop") {
		t.Fatalf("enter should reopen Login in its worktree, spawned=%+v", *spawned)
	}
	if m2.panel != nil || m2.focus != focusMain || app.Terminals[0].Name != "Login" || app.Terminals[0].Worktree == nil {
		t.Errorf("reopened terminal should be focused with its name and worktree: panel=%v focus=%v term=%+v", m2.panel, m2.focus, app.Terminals[0])
	}
	if list, _ := m2.ws.Worktrees(app); len(list) != 1 {
		t.Errorf("reopening must not create another worktree, got %d", len(list))
	}
	if !strings.Contains(m2.View(), "Terminal 1 · ⎇ develop") {
		t.Errorf("tab should show the restored name and branch:\n%s", m2.View())
	}

	// Reopen the plain one; the panel then shows both as running.
	m2 = press(m2, "esc", "m", "j", "enter")
	if len(*spawned) != 2 || (*spawned)[1].dir != app.Path {
		t.Fatalf("enter on the saved plain terminal should start it in the project dir, spawned=%+v", *spawned)
	}
	if len(app.Saved) != 0 {
		t.Errorf("Saved should be empty after reopening everything: %+v", app.Saved)
	}
	m2 = press(m2, "esc", "m")
	if strings.Contains(m2.View(), "saved") || !strings.Contains(m2.View(), "Terminal 1 · ⎇ develop") || !strings.Contains(m2.View(), "visible") {
		t.Errorf("manager should show both as visible running terminals:\n%s", m2.View())
	}

	// Enter on a row with a running terminal focuses it instead of opening another.
	m2 = press(m2, "enter")
	if len(*spawned) != 2 || m2.focus != focusMain || m2.panel != nil {
		t.Error("enter on a running row must focus, not spawn")
	}
	f, _ := state.Load(root)
	if len(f.Projects[0].Terminals) != 2 {
		t.Errorf("state after resume = %+v", f.Projects[0].Terminals)
	}
}

func TestRestartWithMissingWorktreeIsStaleNotRecreated(t *testing.T) {
	m, _, root := gitModel(t)
	m = makeTerminal(t, m, "n", "Login", "develop")
	m = press(m, "esc")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	wtPath := filepath.Join(root, workspace.WorktreesDir, "app", "develop")

	// Worktree deleted while tm was not running.
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatal(err)
	}
	m2, spawned := restartModel(t, m)
	m2 = press(m2, "m")
	if !strings.Contains(m2.View(), "missing") {
		t.Fatalf("deleted worktree still known to git should show as missing:\n%s", m2.View())
	}
	m2 = press(m2, "enter")
	if len(*spawned) != 0 || !strings.Contains(m2.View(), "missing") {
		t.Errorf("enter must not recreate a missing worktree:\n%s", m2.View())
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("missing worktree must not be recreated")
	}
	m2 = press(m2, "d")
	if !strings.Contains(m2.View(), "worktree directory is already gone") {
		t.Errorf("the prompt should explain the missing directory:\n%s", m2.View())
	}
	m2 = press(m2, "y") // delete the definition; git's stale record is pruned with it
	f, _ := state.Load(root)
	if len(f.Projects) != 0 {
		t.Errorf("state should be empty after deleting, got %+v", f.Projects)
	}
	if len(m2.panel.rows) != 0 || strings.Contains(m2.View(), "Unused worktrees") || !strings.Contains(m2.View(), "deleted saved terminal") {
		t.Errorf("nothing should remain, not even an unused entry:\n%s", m2.View())
	}
	if list, _ := m2.ws.Worktrees(m2.ws.Projects[0]); len(list) != 0 {
		t.Errorf("git's record should be pruned: %+v", list)
	}

	// Worktree removed from git entirely (git no longer lists it): stale row, forgettable.
	m3, _, root3 := gitModel(t)
	m3 = makeTerminal(t, m3, "n", "Gone", "develop")
	m3 = press(m3, "esc")
	m3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	gonePath := filepath.Join(root3, workspace.WorktreesDir, "app", "develop")
	if err := os.RemoveAll(gonePath); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.New(root3, nil, nil), error(nil); err != nil {
		t.Fatal(err)
	}
	m4, spawned4 := restartModel(t, m3)
	// prune git's record so only tm's saved definition remains
	infos, _ := m4.ws.Worktrees(m4.ws.Projects[0])
	for _, wt := range infos {
		_ = m4.ws.RemoveWorktree(m4.ws.Projects[0], wt, true)
	}
	m4.ws.Projects[0].Saved = nil
	if err := m4.ws.Restore(); err != nil {
		t.Fatal(err)
	}
	m4 = press(m4, "m")
	if !strings.Contains(m4.View(), "not in git") {
		t.Fatalf("saved worktree unknown to git should show as not in git:\n%s", m4.View())
	}
	m4 = press(m4, "enter")
	if len(*spawned4) != 0 || !strings.Contains(m4.View(), "no longer in git") {
		t.Errorf("enter on a stale row must not open or create anything:\n%s", m4.View())
	}
	m4 = press(m4, "d")
	if !strings.Contains(m4.View(), "Delete this saved terminal") {
		t.Errorf("d on a stale row should offer to delete the definition:\n%s", m4.View())
	}
	m4 = press(m4, "y")
	if len(m4.panel.rows) != 0 || !strings.Contains(m4.View(), "deleted saved terminal") {
		t.Errorf("stale definition should be deleted:\n%s", m4.View())
	}
}

func TestRestartIgnoresRemovedProjectsAndCorruptState(t *testing.T) {
	m, _, root := gitModel(t)
	m = press(m, "enter", "esc") // plain terminal in app
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	// Project disappeared: state mentions "app" but the workspace has no such project.
	fresh := New(root, []project.Project{{Name: "other", Path: filepath.Join(root, "other")}})
	if fresh.notice != "" || len(fresh.ws.Projects[0].Saved) != 0 {
		t.Errorf("unknown project must be ignored silently: notice=%q saved=%+v", fresh.notice, fresh.ws.Projects[0].Saved)
	}

	// Corrupt file: tm starts, shows a notice, keeps working.
	if err := os.WriteFile(filepath.Join(root, state.FileName), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := New(root, []project.Project{{Name: "app", Path: filepath.Join(root, "app")}})
	if !strings.Contains(broken.notice, "could not read saved state") {
		t.Errorf("corrupt state should produce a notice, got %q", broken.notice)
	}
	next, _ := broken.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !strings.Contains(next.(Model).View(), "could not read saved state") {
		t.Error("notice should be visible in the status bar")
	}
}

func TestExitMessagesAfterQuitDoNotRewriteState(t *testing.T) {
	m, spawned, root := gitModel(t)
	m = press(m, "enter", "esc") // one running plain terminal
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd == nil || !(*spawned)[0].closed {
		t.Fatal("q should quit and close sessions")
	}
	f, _ := state.Load(root)
	if len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 1 {
		t.Fatalf("state at quit should hold the running terminal, got %+v", f)
	}
	// The closed session's exit message arrives while Bubble Tea is shutting down.
	next, _ = m.Update(terminalExitedMsg{id: m.ws.Projects[0].Terminals[0].ID})
	m = next.(Model)
	f, _ = state.Load(root)
	if len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 1 {
		t.Errorf("exit messages after quit must not rewrite the state, got %+v", f)
	}
}

func TestManagerListsRunningPlainTerminals(t *testing.T) {
	m, spawned := newTestModel(t)
	m = press(m, "enter", "esc", "m") // one running plain terminal in backend
	view := m.View()
	if !strings.Contains(view, "Terminal 1") || !strings.Contains(view, "visible") || !strings.Contains(view, "t1") { // unnamed: the program's title stands in
		t.Fatalf("manager should list the running plain terminal:\n%s", view)
	}
	m = press(m, "d")
	if !m.panel.confirm || !strings.Contains(m.View(), "Delete Terminal 1") {
		t.Errorf("d on a running terminal asks before deleting for good:\n%s", m.View())
	}
	m = press(m, "esc") // cancel
	if m.panel == nil || m.panel.confirm || (*spawned)[0].closed {
		t.Error("cancelling must keep the terminal and the manager")
	}
	m = press(m, "enter")
	if len(*spawned) != 1 || m.focus != focusMain || m.panel != nil {
		t.Error("enter on a running row should focus it without spawning")
	}
}
