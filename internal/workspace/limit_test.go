package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixedLimit is a LimitSource whose value tests can change between calls.
func fixedLimit(max *int) LimitSource {
	return func() Limit { return Limit{Max: *max, Source: "test.toml"} }
}

func TestDefaultLimitIsSixWithoutConfiguration(t *testing.T) {
	ws, _ := newTestWorkspace()
	p := ws.Projects[0]
	for i := 0; i < 6; i++ {
		if _, err := ws.NewTerminal(p, 80, 24); err != nil {
			t.Fatalf("terminal %d: %v", i+1, err)
		}
	}
	_, err := ws.NewTerminal(p, 80, 24)
	var le *LimitError
	if !errors.As(err, &le) || le.Max != 6 || le.Running != 6 {
		t.Fatalf("7th terminal: err = %v, want LimitError max 6", err)
	}
	if !strings.Contains(err.Error(), "maximum terminals reached (6)") || !strings.Contains(err.Error(), "max_terminals") || !strings.Contains(err.Error(), "config.toml") {
		t.Errorf("message should say how to change the limit: %v", err)
	}
	if len(p.Terminals) != 6 {
		t.Errorf("refused creation must not change the project: %d terminals", len(p.Terminals))
	}
}

func TestLimitIsPerProjectAndCountsRunningOnly(t *testing.T) {
	ws, spawned := newTestWorkspace()
	max := 2
	ws.Limits = fixedLimit(&max)
	a, b := ws.Projects[0], ws.Projects[1]
	ws.NewTerminal(a, 80, 24)
	ta, _ := ws.NewTerminal(a, 80, 24)
	if err := ws.CanCreateTerminal(a); err == nil {
		t.Fatal("a is full")
	}
	if err := ws.CanCreateTerminal(b); err != nil {
		t.Fatalf("b is independent: %v", err)
	}
	// Closing one (definition kept, saved) frees capacity: saved does not count.
	ws.Remove(ta.ID)
	if len(a.Saved) != 1 {
		t.Fatalf("closed terminal should be saved: %+v", a.Saved)
	}
	if err := ws.CanCreateTerminal(a); err != nil {
		t.Fatalf("saved definitions must not consume capacity: %v", err)
	}
	if _, err := ws.NewTerminal(a, 80, 24); err != nil {
		t.Fatal(err)
	}
	if len(*spawned) != 3 {
		t.Errorf("spawned %d", len(*spawned))
	}
}

func TestLimitIsReadOnEveryAttempt(t *testing.T) {
	ws, _ := newTestWorkspace()
	max := 1
	ws.Limits = fixedLimit(&max)
	p := ws.Projects[0]
	ws.NewTerminal(p, 80, 24)
	if _, err := ws.NewTerminal(p, 80, 24); err == nil {
		t.Fatal("second terminal should be refused at max 1")
	}
	max = 3 // "edit the config"
	if _, err := ws.NewTerminal(p, 80, 24); err != nil {
		t.Fatalf("raised limit must apply to the next attempt: %v", err)
	}
	max = 1 // lowered below the running count
	if len(p.Terminals) != 2 || p.Terminals[0].Session.(*fakeSession).closed || p.Terminals[1].Session.(*fakeSession).closed {
		t.Fatal("lowering the limit must not close or drop running terminals")
	}
	if err := ws.CanCreateTerminal(p); err == nil {
		t.Fatal("no more creation while over the lowered limit")
	}
	snap := ws.Snapshot()
	if len(snap.Projects) != 1 || len(snap.Projects[0].Terminals) != 2 {
		t.Errorf("saved state must still hold both terminals: %+v", snap)
	}
}

func TestInvalidLimitSourceFallsBackToDefault(t *testing.T) {
	ws, _ := newTestWorkspace()
	ws.Limits = func() Limit { return Limit{Max: 0, Notice: "config.toml: line 1: bad"} }
	p := ws.Projects[0]
	if _, err := ws.NewTerminal(p, 80, 24); err != nil {
		t.Fatalf("an invalid limit must not block creation: %v", err)
	}
	if ws.LimitNotice() != "config.toml: line 1: bad" {
		t.Errorf("notice = %q", ws.LimitNotice())
	}
	ws.Limits = nil
	for i := 0; i < 5; i++ {
		if _, err := ws.NewTerminal(p, 80, 24); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ws.NewTerminal(p, 80, 24); err == nil {
		t.Fatal("nil source should still enforce the default of 6")
	}
}

func TestConfigLimitReadsTheUsersFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if l := ConfigLimit(); l.Max != 6 || l.Notice != "" || !strings.HasSuffix(l.Source, filepath.Join("tm", "config.toml")) {
		t.Fatalf("no file: %+v", l)
	}
	os.MkdirAll(filepath.Join(dir, "tm"), 0o755)
	os.WriteFile(filepath.Join(dir, "tm", "config.toml"), []byte("max_terminals = 9\n"), 0o644)
	if l := ConfigLimit(); l.Max != 9 || l.Notice != "" {
		t.Fatalf("valid file: %+v", l)
	}
	os.WriteFile(filepath.Join(dir, "tm", "config.toml"), []byte("max_terminals = 0\n"), 0o644)
	if l := ConfigLimit(); l.Max != 6 || !strings.Contains(l.Notice, "config.toml") || !strings.Contains(l.Notice, "positive integer") {
		t.Fatalf("invalid file: %+v", l)
	}
}

func TestCreateTerminalRefusedAtLimitCreatesNoWorktree(t *testing.T) {
	ws, _ := gitWorkspace(t)
	max := 1
	ws.Limits = fixedLimit(&max)
	app := ws.Projects[0]
	if _, err := ws.NewTerminal(app, 80, 24); err != nil {
		t.Fatal(err)
	}
	before, _ := ws.Worktrees(app)
	_, err := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v", err)
	}
	if after, _ := ws.Worktrees(app); len(after) != len(before) {
		t.Errorf("refused terminal must not create a worktree: %d -> %d", len(before), len(after))
	}
}

func TestOpenAllStopsAtTheLimitAndKeepsTheRestSaved(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	max := 10
	ws.Limits = fixedLimit(&max)
	app := ws.Projects[0]
	var made []*Terminal
	for _, name := range []string{"One", "Two", "Three", "Four", "Five", "Six"} {
		opts := Options{Name: name}
		if name == "Two" || name == "Five" {
			opts = Options{Name: name, Branch: "wt/" + strings.ToLower(name), Worktree: true}
		}
		term, err := ws.CreateTerminal(app, opts, 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, term)
	}
	for _, term := range made {
		ws.Remove(term.ID)
	}
	if len(app.Saved) != 6 {
		t.Fatalf("saved = %d", len(app.Saved))
	}
	worktreesBefore, _ := ws.Worktrees(app)
	spawnedBefore := len(*spawned)

	max = 4
	res, err := ws.OpenAll(app, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range res.Opened {
		names = append(names, o.Name)
	}
	if strings.Join(names, ",") != "One,Two,Three,Four" {
		t.Errorf("should open the first four in saved order, got %v", names)
	}
	if res.NotOpened != 2 || len(res.Skipped) != 0 || res.Limit.Max != 4 {
		t.Errorf("result = %+v", res)
	}
	if len(*spawned) != spawnedBefore+4 || len(app.Terminals) != 4 {
		t.Errorf("spawned %d, running %d", len(*spawned)-spawnedBefore, len(app.Terminals))
	}
	if len(app.Saved) != 2 || app.Saved[0].Name != "Five" || app.Saved[1].Name != "Six" {
		t.Errorf("the rest must stay saved: %+v", app.Saved)
	}
	if after, _ := ws.Worktrees(app); len(after) != len(worktreesBefore) {
		t.Errorf("worktrees changed: %d -> %d", len(worktreesBefore), len(after))
	}
	snap := ws.Snapshot()
	if len(snap.Projects[0].Terminals) != 6 {
		t.Errorf("state must still list all six definitions: %+v", snap.Projects[0].Terminals)
	}

	// Raising the limit later lets the rest come back.
	max = 6
	res, _ = ws.OpenAll(app, 80, 24)
	names = names[:0]
	for _, o := range res.Opened {
		names = append(names, o.Name)
	}
	if strings.Join(names, ",") != "Five,Six" || res.NotOpened != 0 || len(app.Saved) != 0 {
		t.Errorf("after raising: opened %v, result %+v, saved %+v", names, res, app.Saved)
	}
	if res.Opened[0].Worktree == nil || res.Opened[0].Worktree.Path != made[4].Worktree.Path {
		t.Errorf("Five should reuse its worktree: %+v", res.Opened[0].Worktree)
	}
}
