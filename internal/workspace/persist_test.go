package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
)

func rel(ws *Workspace, abs string) string { return ws.relPath(abs) }

func TestSnapshotIncludesRunningAndSavedTerminals(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	plain, _ := ws.CreateTerminal(app, Options{Name: "Plain"}, 80, 24)
	login, _ := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	pay, _ := ws.CreateTerminal(app, Options{Name: "Payment", Branch: "feature/payment", Worktree: true}, 80, 24)
	_ = pay.Session.Close()
	ws.Remove(pay.ID) // closed worktree terminal is remembered
	_ = plain.Session.Close()
	_, _ = login, plain

	snap := ws.Snapshot()
	if len(snap.Projects) != 1 || snap.Projects[0].Name != "app" {
		t.Fatalf("snapshot projects = %+v", snap.Projects)
	}
	want := []state.Terminal{
		{Name: "Plain"},
		{Name: "Login", Branch: "develop", Worktree: ".terminal-worktrees/app/login"},
		{Name: "Payment", Branch: "feature/payment", Worktree: ".terminal-worktrees/app/payment"},
	}
	if got := snap.Projects[0].Terminals; !reflect.DeepEqual(got, want) {
		t.Errorf("terminals = %+v\nwant %+v", got, want)
	}
	for _, st := range snap.Projects[0].Terminals {
		if filepath.IsAbs(st.Worktree) {
			t.Errorf("worktree path must be workspace-relative, got %q", st.Worktree)
		}
	}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws.Root, state.FileName)); err != nil {
		t.Error("Save should write the state file into the workspace root")
	}
}

func TestClosingAnyTerminalIsRememberedAndDeleteForgets(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	app := ws.Projects[0]
	plain, _ := ws.CreateTerminal(app, Options{Name: "Plain"}, 80, 24)
	ws.Remove(plain.ID) // alt+x or the shell exiting: definition stays
	if len(app.Saved) != 1 || app.Saved[0].Name != "Plain" {
		t.Errorf("a closed plain terminal must stay restorable, saved = %+v", app.Saved)
	}
	if snap := ws.Snapshot(); len(snap.Projects) != 1 || len(snap.Projects[0].Terminals) != 1 {
		t.Errorf("snapshot should keep the definition, got %+v", snap.Projects)
	}

	login, _ := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	p, gone := ws.detach(login.ID) // the detach half of DeleteTerminal: forgotten, nothing else touched
	if p != app || gone != login || len(app.Terminals) != 0 {
		t.Fatalf("detach should remove the terminal: p=%v gone=%v terminals=%d", p, gone, len(app.Terminals))
	}
	for _, s := range app.Saved {
		if s.Name == "Login" {
			t.Error("deleted terminal must not be remembered")
		}
	}
	if (*spawned)[1].closed {
		t.Error("detach itself must not close the session; the UI does that")
	}
	if list, _ := ws.Worktrees(app); len(list) != 1 || list[0].Name != "login" {
		t.Errorf("detach alone must not touch the worktree, got %+v", list)
	}
	if _, none := ws.detach(999); none != nil {
		t.Error("detach of an unknown id should return nil")
	}
}

func TestRestoreMatchesProjectsAndValidatesPaths(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	saved := state.File{Projects: []state.Project{
		{Name: "app", Terminals: []state.Terminal{
			{Name: "Plain"},
			{Name: "Login", Branch: "develop", Worktree: ".terminal-worktrees/app/login"},
			{Name: "Elsewhere", Branch: "x", Worktree: ".terminal-worktrees/other/login"}, // another project's dir
			{Name: "Nested", Branch: "x", Worktree: ".terminal-worktrees/app/a/b"},        // too deep
			{Name: "Top", Branch: "x", Worktree: "app"},                                   // the checkout itself
		}},
		{Name: "gone-project", Terminals: []state.Terminal{{Name: "x"}}},
	}}
	if err := state.Save(ws.Root, saved); err != nil {
		t.Fatal(err)
	}
	if err := ws.Restore(); err != nil {
		t.Fatal(err)
	}
	want := []SavedTerminal{
		{Name: "Plain"},
		{Name: "Login", Branch: "develop", Worktree: filepath.Join(ws.Root, WorktreesDir, "app", "login")},
	}
	if !reflect.DeepEqual(app.Saved, want) {
		t.Errorf("Saved = %+v\nwant %+v", app.Saved, want)
	}
	if len(ws.Projects[1].Saved) != 0 {
		t.Error("plain project should have nothing restored")
	}
}

func TestRestoreToleratesMissingAndCorruptFiles(t *testing.T) {
	ws, _ := gitWorkspace(t)
	if err := ws.Restore(); err != nil {
		t.Fatalf("missing state file should not be an error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root, state.FileName), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ws.Restore(); err == nil {
		t.Error("corrupt state should be reported")
	}
	if _, err := ws.CreateTerminal(ws.Projects[0], Options{Name: "still works"}, 80, 24); err != nil {
		t.Errorf("workspace must stay usable after a corrupt state file: %v", err)
	}
}

func TestOpenSavedReusesWorktreeAndPreventsDuplicates(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	app := ws.Projects[0]
	pay, _ := ws.CreateTerminal(app, Options{Name: "Payment", Branch: "feature/payment", Worktree: true}, 80, 24)
	_ = pay.Session.Close()
	ws.Remove(pay.ID)
	before, _ := ws.Worktrees(app)

	find := func(name string) SavedTerminal {
		t.Helper()
		for _, s := range app.Saved {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("no saved definition %q in %+v", name, app.Saved)
		return SavedTerminal{}
	}

	saved := find("Payment")
	reopened, err := ws.OpenSaved(app, saved, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Name != "Payment" || reopened.Worktree == nil || reopened.Worktree.Path != pay.Worktree.Path || reopened.Worktree.Branch != "feature/payment" {
		t.Errorf("reopened = %+v", reopened)
	}
	if (*spawned)[1].dir != pay.Worktree.Path {
		t.Errorf("shell should start in the existing worktree, got %s", (*spawned)[1].dir)
	}
	after, _ := ws.Worktrees(app)
	if len(after) != len(before) {
		t.Errorf("reopening must not create another git worktree: %d -> %d", len(before), len(after))
	}
	if len(app.Saved) != 0 {
		t.Errorf("reopened definition should leave Saved, got %+v", app.Saved)
	}
	if reopened.Num != 2 {
		t.Errorf("reopened terminal should get the next number, got %d", reopened.Num)
	}

	// Opening again while it runs is refused.
	if _, err := ws.OpenSaved(app, saved, 80, 24); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("duplicate open: err = %v", err)
	}
	if len(*spawned) != 2 {
		t.Error("refused open must not spawn")
	}

	// A saved plain terminal reopens in the project checkout.
	app.remember(SavedTerminal{Name: "Plain"})
	plain, err := ws.OpenSaved(app, find("Plain"), 80, 24)
	if err != nil || plain.Worktree != nil || (*spawned)[2].dir != app.Path || plain.Name != "Plain" {
		t.Errorf("plain reopen: term=%+v err=%v dir=%s", plain, err, (*spawned)[2].dir)
	}

	// Missing and stale worktrees are never created behind the user's back.
	missing := filepath.Join(ws.Root, WorktreesDir, "app", "missing")
	app.remember(SavedTerminal{Name: "Missing", Branch: "feature/missing", Worktree: missing})
	if _, err := ws.OpenSaved(app, find("Missing"), 80, 24); err == nil {
		t.Error("opening a stale/missing worktree must fail")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("open must not create the missing worktree")
	}
	ws.DeleteTerminal(app, find("Missing"), nil) // nothing on disk or in git to remove: only the definition goes
	if len(app.Saved) != 0 {
		t.Errorf("DeleteTerminal should drop the saved definition, got %+v", app.Saved)
	}
}

func TestRemoveWorktreeDropsSavedDefinition(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	pay, _ := ws.CreateTerminal(app, Options{Name: "Payment", Branch: "feature/payment", Worktree: true}, 80, 24)
	_ = pay.Session.Close()
	ws.Remove(pay.ID)
	if len(app.Saved) != 1 {
		t.Fatalf("closed worktree terminal should be saved, got %+v", app.Saved)
	}
	infos, _ := ws.Worktrees(app)
	if err := ws.RemoveWorktree(app, infos[0], false); err != nil {
		t.Fatal(err)
	}
	if len(app.Saved) != 0 {
		t.Errorf("removing the worktree should forget its saved terminal, got %+v", app.Saved)
	}
	if snap := ws.Snapshot(); len(snap.Projects) != 0 {
		t.Errorf("snapshot after removal should be empty, got %+v", snap.Projects)
	}
}

func TestRestartResumesPreviousTerminals(t *testing.T) {
	ws1, _ := gitWorkspace(t)
	app := ws1.Projects[0]
	if _, err := ws1.CreateTerminal(app, Options{Name: "Plain"}, 80, 24); err != nil {
		t.Fatal(err)
	}
	login, err := ws1.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws1.Save(); err != nil { // what the UI does on quit
		t.Fatal(err)
	}
	ws1.CloseAll()

	// Second run over the same workspace root and projects, fresh spawner.
	spawned := &[]*fakeSession{}
	var projects []project.Project
	for _, p := range ws1.Projects {
		projects = append(projects, p.Project)
	}
	ws2 := New(ws1.Root, projects, func(dir string, cols, rows int) (Session, error) {
		f := &fakeSession{dir: dir, cols: cols, rows: rows}
		*spawned = append(*spawned, f)
		return f, nil
	})
	if err := ws2.Restore(); err != nil {
		t.Fatal(err)
	}
	app2 := ws2.Projects[0]
	if len(app2.Terminals) != 0 {
		t.Fatal("restore must not start processes automatically")
	}
	if len(app2.Saved) != 2 {
		t.Fatalf("saved after restart = %+v, want Login worktree + Plain", app2.Saved)
	}
	for _, s := range append([]SavedTerminal(nil), app2.Saved...) {
		if _, err := ws2.OpenSaved(app2, s, 80, 24); err != nil {
			t.Errorf("reopen %s: %v", s.Name, err)
		}
	}
	dirs := []string{(*spawned)[0].dir, (*spawned)[1].dir}
	if !(contains(dirs, login.Worktree.Path) && contains(dirs, app2.Path)) {
		t.Errorf("reopened shells should run in %s and %s, got %v", login.Worktree.Path, app2.Path, dirs)
	}
	if len(app2.Saved) != 0 {
		t.Errorf("everything reopened, Saved should be empty: %+v", app2.Saved)
	}
	snap := ws2.Snapshot()
	if len(snap.Projects) != 1 || len(snap.Projects[0].Terminals) != 2 {
		t.Errorf("snapshot after resume = %+v", snap)
	}
	if list, _ := ws2.Worktrees(app2); len(list) != 1 {
		t.Errorf("resume must not create extra worktrees, got %d", len(list))
	}
}

func TestUnnamedPlainTerminalsArePersistedIndividually(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	if _, err := ws.CreateTerminal(app, Options{}, 80, 24); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.CreateTerminal(app, Options{}, 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := ws.Save(); err != nil {
		t.Fatal(err)
	}
	f, _ := state.Load(ws.Root)
	if len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 2 {
		t.Fatalf("two unnamed shells should both be saved, got %+v", f)
	}
	ws2 := New(ws.Root, []project.Project{app.Project}, nil)
	if err := ws2.Restore(); err != nil {
		t.Fatal(err)
	}
	if len(ws2.Projects[0].Saved) != 2 {
		t.Errorf("both shells should be restorable, got %+v", ws2.Projects[0].Saved)
	}
	ws2.DeleteTerminal(ws2.Projects[0], SavedTerminal{}, nil)
	if len(ws2.Projects[0].Saved) != 1 {
		t.Errorf("deleting an unnamed saved shell should drop exactly one, got %+v", ws2.Projects[0].Saved)
	}
}

func TestOpenAllReopensValidSavedTerminalsOnly(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	app := ws.Projects[0]
	running, _ := ws.CreateTerminal(app, Options{Name: "Running"}, 80, 24)
	login, _ := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	dirty, _ := ws.CreateTerminal(app, Options{Name: "Dirty", Branch: "feature/dirty", Worktree: true}, 80, 24)
	missing, _ := ws.CreateTerminal(app, Options{Name: "Missing", Branch: "feature/missing", Worktree: true}, 80, 24)
	for _, term := range []*Terminal{login, dirty, missing} {
		_ = term.Session.Close()
		ws.Remove(term.ID)
	}
	if err := os.WriteFile(filepath.Join(dirty.Worktree.Path, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(missing.Worktree.Path); err != nil {
		t.Fatal(err)
	}
	app.remember(SavedTerminal{Name: "Plain"})
	app.remember(SavedTerminal{Name: "Stale", Branch: "feature/stale", Worktree: filepath.Join(ws.Root, WorktreesDir, "app", "stale")})
	before, _ := ws.Worktrees(app)
	spawnedBefore := len(*spawned)

	res, err := ws.OpenAll(app, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	opened, skipped := res.Opened, res.Skipped
	names := map[string]*Terminal{}
	for _, o := range opened {
		names[o.Name] = o
	}
	if len(opened) != 3 || names["Login"] == nil || names["Dirty"] == nil || names["Plain"] == nil {
		t.Fatalf("opened = %v, want Login, Dirty, Plain", keysOf(names))
	}
	if names["Login"].Worktree == nil || names["Login"].Worktree.Path != login.Worktree.Path || names["Login"].Worktree.Branch != "develop" {
		t.Errorf("Login should reuse its worktree with the branch from git: %+v", names["Login"].Worktree)
	}
	if names["Plain"].Worktree != nil || names["Plain"].Session.(*fakeSession).dir != app.Path {
		t.Errorf("Plain should run in the project dir: %+v", names["Plain"])
	}
	if len(skipped) != 2 || !strings.Contains(strings.Join(skipped, ";"), "Missing (worktree directory missing)") || !strings.Contains(strings.Join(skipped, ";"), "Stale (worktree no longer in git)") {
		t.Errorf("skipped = %v", skipped)
	}
	if len(*spawned) != spawnedBefore+3 {
		t.Errorf("spawned %d new sessions, want 3", len(*spawned)-spawnedBefore)
	}
	if after, _ := ws.Worktrees(app); len(after) != len(before) {
		t.Errorf("OpenAll must not create worktrees: %d -> %d", len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(dirty.Worktree.Path, "wip.txt")); err != nil {
		t.Error("dirty worktree must remain untouched")
	}
	if _, err := os.Stat(missing.Worktree.Path); !os.IsNotExist(err) {
		t.Error("missing worktree must not be recreated")
	}
	if running.Session.(*fakeSession).closed || len(app.Terminals) != 4 {
		t.Errorf("running terminal must be untouched and not duplicated: %d terminals", len(app.Terminals))
	}
	if len(app.Saved) != 2 {
		t.Errorf("only the skipped definitions should remain saved: %+v", app.Saved)
	}

	// A second OpenAll has nothing left to open and changes nothing.
	res, _ = ws.OpenAll(app, 80, 24)
	opened, skipped = res.Opened, res.Skipped
	if len(opened) != 0 || len(skipped) != 2 || len(*spawned) != spawnedBefore+3 {
		t.Errorf("second OpenAll: opened=%d skipped=%d spawned=%d", len(opened), len(skipped), len(*spawned)-spawnedBefore)
	}

	// A non-git project still reopens its plain saved terminals.
	plain := ws.Projects[1]
	plain.remember(SavedTerminal{Name: "Shell"})
	res, err = ws.OpenAll(plain, 80, 24)
	if err != nil || len(res.Opened) != 1 || res.Opened[0].Name != "Shell" {
		t.Errorf("non-git project OpenAll: opened=%v err=%v", res.Opened, err)
	}
}

func keysOf(m map[string]*Terminal) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
