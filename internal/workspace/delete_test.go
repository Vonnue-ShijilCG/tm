package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteTerminalInCheckoutOnlyDropsTheDefinition(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	app := ws.Projects[0]
	plain, _ := ws.CreateTerminal(app, Options{Name: "Plain"}, 80, 24)
	before, _ := ws.Worktrees(app)
	if note := ws.DeleteTerminal(app, plain.Definition(), plain); note != "" {
		t.Errorf("note = %q", note)
	}
	if len(app.Terminals) != 0 || len(app.Saved) != 0 {
		t.Errorf("definition should be gone: running=%d saved=%d", len(app.Terminals), len(app.Saved))
	}
	if _, err := os.Stat(filepath.Join(app.Path, ".git")); err != nil {
		t.Fatal("the project checkout must never be touched")
	}
	if after, _ := ws.Worktrees(app); len(after) != len(before) {
		t.Error("no worktree may change")
	}
	_ = spawned
	// A saved plain definition likewise.
	app.remember(SavedTerminal{Name: "Old"})
	ws.DeleteTerminal(app, SavedTerminal{Name: "Old"}, nil)
	if len(app.Saved) != 0 {
		t.Errorf("saved plain definition should be gone: %+v", app.Saved)
	}
}

func TestDeleteTerminalRemovesItsDedicatedWorktree(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	login, _ := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	path := login.Worktree.Path
	if err := os.WriteFile(filepath.Join(path, "wip.txt"), []byte("x"), 0o644); err != nil { // dirty: the UI has confirmed
		t.Fatal(err)
	}
	if info, ok := ws.WorktreeStatus(app, path); !ok || !info.Dirty || info.Branch != "develop" {
		t.Fatalf("status before delete = %+v, %v", info, ok)
	}
	if note := ws.DeleteTerminal(app, login.Definition(), login); note != "" {
		t.Errorf("note = %q", note)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the dedicated worktree should be removed")
	}
	if list, _ := ws.Worktrees(app); len(list) != 0 {
		t.Errorf("git should no longer list it: %+v", list)
	}
	branches, _, _ := ws.Branches(app)
	if !strings.Contains(strings.Join(branches, ","), "develop") {
		t.Error("the branch is kept")
	}
	if len(app.Terminals) != 0 || len(app.Saved) != 0 {
		t.Error("definition gone")
	}
	if _, ok := ws.WorktreeStatus(app, path); ok {
		t.Error("status of a removed worktree is not found")
	}

	// A saved definition whose worktree directory vanished: the definition
	// goes and git's stale record is pruned, without failing.
	pay, _ := ws.CreateTerminal(app, Options{Name: "Pay", Branch: "feature/pay", Base: ws.DefaultBranch(app), Worktree: true}, 80, 24)
	ws.Remove(pay.ID)
	os.RemoveAll(pay.Worktree.Path)
	if info, ok := ws.WorktreeStatus(app, pay.Worktree.Path); !ok || !info.Missing {
		t.Fatalf("missing status = %+v, %v", info, ok)
	}
	if note := ws.DeleteTerminal(app, app.Saved[0], nil); note != "" {
		t.Errorf("missing worktree: note = %q", note)
	}
	if list, _ := ws.Worktrees(app); len(list) != 0 || len(app.Saved) != 0 {
		t.Errorf("record pruned and definition gone: %+v saved=%+v", list, app.Saved)
	}
}

func TestDeleteTerminalKeepsAWorktreeAnotherTerminalUses(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	login, _ := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	path := login.Worktree.Path
	twin := SavedTerminal{Name: "Twin", Branch: "develop", Worktree: path}
	app.Saved = append(app.Saved, twin) // a second definition pointing at the same worktree
	note := ws.DeleteTerminal(app, twin, nil)
	if !strings.Contains(note, "worktree kept") || !strings.Contains(note, "terminal 1") {
		t.Errorf("note = %q", note)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a worktree in use by another terminal must survive")
	}
	if len(app.Saved) != 0 || len(app.Terminals) != 1 {
		t.Errorf("only the twin definition goes: saved=%+v running=%d", app.Saved, len(app.Terminals))
	}
	// The other way round: the running one is deleted while a saved twin remains.
	app.Saved = append(app.Saved, twin)
	note = ws.DeleteTerminal(app, login.Definition(), login)
	if !strings.Contains(note, "worktree kept") || len(app.Terminals) != 0 {
		t.Errorf("note = %q running=%d", note, len(app.Terminals))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("worktree still referenced by a saved definition must survive")
	}
	// A path outside tm's worktree directory is refused, not removed.
	outside := SavedTerminal{Name: "X", Worktree: app.Path}
	if note := ws.DeleteTerminal(app, outside, nil); !strings.Contains(note, "worktree kept") {
		t.Errorf("foreign path: note = %q", note)
	}
	if _, err := os.Stat(filepath.Join(app.Path, ".git")); err != nil {
		t.Fatal("the project checkout must never be removed")
	}
}
