package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
)

// gitWorkspace builds <root>/app (a repo with one commit on main) and
// <root>/plain (a directory with an empty .git, i.e. not a usable repo).
func gitWorkspace(t *testing.T) (*Workspace, *[]*fakeSession) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	app := filepath.Join(root, "app")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", app},
		{"-C", app, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", app, "branch", "develop"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(filepath.Join(plain, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	spawned := &[]*fakeSession{}
	ws := New(root, []project.Project{{Name: "app", Path: app}, {Name: "plain", Path: plain}},
		func(dir string, cols, rows int) (Session, error) {
			f := &fakeSession{dir: dir, cols: cols, rows: rows}
			*spawned = append(*spawned, f)
			return f, nil
		})
	return ws, spawned
}

func TestWorktreePathIsSafeAndDeterministic(t *testing.T) {
	cases := map[string]string{
		"Payment":         "payment",
		"feature/payment": "feature-payment",
		"  Hello World! ": "hello-world",
		"../../etc":       "etc",
		"a//b\\c":         "a-b-c",
		"v1.2_rc":         "v1.2_rc",
	}
	for in, want := range cases {
		got, err := WorktreePath("/root", "app", in)
		if err != nil {
			t.Errorf("WorktreePath(%q) error: %v", in, err)
			continue
		}
		if want := filepath.Join("/root", WorktreesDir, "app", want); got != want {
			t.Errorf("WorktreePath(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "   ", "..", "...", "/", "!!!"} {
		if p, err := WorktreePath("/root", "app", bad); err == nil {
			t.Errorf("WorktreePath(%q) should fail, got %q", bad, p)
		}
	}
}

func TestTerminalWithoutWorktreeRunsInProjectDir(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	p := ws.Projects[0]
	term, err := ws.CreateTerminal(p, Options{Name: "Plain"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if term.Worktree != nil || term.Dir(p) != p.Path || (*spawned)[0].dir != p.Path {
		t.Errorf("plain terminal should run in %s, got dir=%s worktree=%v", p.Path, (*spawned)[0].dir, term.Worktree)
	}
	if term.Name != "Plain" || term.Num != 1 || term.ID != 1 {
		t.Errorf("terminal fields wrong: %+v", term)
	}
}

func TestTerminalsWithDifferentWorktrees(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	p := ws.Projects[0]

	login, err := ws.CreateTerminal(p, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24) // existing branch
	if err != nil {
		t.Fatal(err)
	}
	payment, err := ws.CreateTerminal(p, Options{Name: "Payment", Branch: "feature/payment", Worktree: true}, 80, 24) // new branch
	if err != nil {
		t.Fatal(err)
	}

	wantLogin := filepath.Join(ws.Root, WorktreesDir, "app", "login")
	wantPayment := filepath.Join(ws.Root, WorktreesDir, "app", "payment")
	if login.Worktree == nil || login.Worktree.Path != wantLogin || login.Worktree.Branch != "develop" {
		t.Errorf("login worktree = %+v, want path %s", login.Worktree, wantLogin)
	}
	if payment.Worktree == nil || payment.Worktree.Path != wantPayment || payment.Worktree.Branch != "feature/payment" {
		t.Errorf("payment worktree = %+v, want path %s", payment.Worktree, wantPayment)
	}
	if (*spawned)[0].dir != wantLogin || (*spawned)[1].dir != wantPayment {
		t.Errorf("shells should start in their worktrees, got %s and %s", (*spawned)[0].dir, (*spawned)[1].dir)
	}
	for _, dir := range []string{wantLogin, wantPayment} {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			t.Errorf("%s should be a git worktree: %v", dir, err)
		}
	}
	branches, _, err := ws.Branches(p)
	if err != nil || !contains(branches, "feature/payment") {
		t.Errorf("new branch should exist; branches=%v err=%v", branches, err)
	}
	if login.Num != 1 || payment.Num != 2 || login.ID+1 != payment.ID {
		t.Errorf("numbering changed: %+v %+v", login, payment)
	}
}

func TestWorktreeCreationErrors(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	app, plain := ws.Projects[0], ws.Projects[1]

	if _, err := ws.CreateTerminal(app, Options{Name: "Dup", Branch: "develop", Worktree: true}, 80, 24); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		p    *Project
		opts Options
		want string
	}{
		{"same name twice", app, Options{Name: "Dup", Branch: "other", Worktree: true}, "already exists"},
		{"branch checked out elsewhere", app, Options{Name: "Main2", Branch: "main", Worktree: true}, "already"},
		{"missing branch", app, Options{Name: "X", Worktree: true}, "branch is required"},
		{"bad name", app, Options{Name: "..", Branch: "b", Worktree: true}, "invalid worktree name"},
		{"non-git project", plain, Options{Name: "P", Branch: "b", Worktree: true}, "not a git repository"},
	}
	for _, tc := range cases {
		_, err := ws.CreateTerminal(tc.p, tc.opts, 80, 24)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want containing %q", tc.name, err, tc.want)
		}
	}
	if len(*spawned) != 1 || len(app.Terminals) != 1 || len(plain.Terminals) != 0 {
		t.Errorf("failed creations must not spawn or register terminals: spawned=%d app=%d plain=%d", len(*spawned), len(app.Terminals), len(plain.Terminals))
	}

	// A non-git project still gets ordinary terminals.
	if _, err := ws.CreateTerminal(plain, Options{Name: "Shell"}, 80, 24); err != nil {
		t.Errorf("plain terminal in non-git project failed: %v", err)
	}
}

func TestClosingTerminalKeepsWorktree(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	p := ws.Projects[0]
	term, err := ws.CreateTerminal(p, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := ws.CreateTerminal(p, Options{}, 80, 24)

	_ = term.Session.Close()
	ws.Remove(term.ID)

	if _, err := os.Stat(filepath.Join(term.Worktree.Path, ".git")); err != nil {
		t.Errorf("worktree must survive closing its terminal: %v", err)
	}
	if !(*spawned)[0].closed || (*spawned)[1].closed {
		t.Error("only the closed terminal's session should be closed")
	}
	if p.ActiveTerminal() != other {
		t.Error("remaining terminal should stay active")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestWorktreesListsOnlyOwnedWithState(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]

	login, err := ws.CreateTerminal(p, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	payment, err := ws.CreateTerminal(p, Options{Name: "Payment", Branch: "feature/payment", Worktree: true}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := ws.CreateTerminal(p, Options{Name: "Stale", Branch: "feature/stale", Worktree: true}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	// payment: terminal closed, directory dirty. stale: terminal closed, directory deleted.
	ws.Remove(payment.ID)
	ws.Remove(stale.ID)
	if err := os.WriteFile(filepath.Join(payment.Worktree.Path, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(stale.Worktree.Path); err != nil {
		t.Fatal(err)
	}
	// A worktree outside .terminal-worktrees must never show up.
	outside := filepath.Join(t.TempDir(), "outside")
	if out, err := exec.Command("git", "-C", p.Path, "worktree", "add", "-q", "-b", "feature/outside", outside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	infos, err := ws.Worktrees(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 {
		t.Fatalf("Worktrees() = %+v, want 3 owned entries", infos)
	}
	byName := map[string]WorktreeInfo{}
	for _, i := range infos {
		byName[i.Name] = i
	}
	if i := byName["login"]; i.Branch != "develop" || i.Dirty || i.Missing || i.Terminal != login || i.Path != login.Worktree.Path {
		t.Errorf("login = %+v", i)
	}
	if i := byName["payment"]; i.Branch != "feature/payment" || !i.Dirty || i.Missing || i.Terminal != nil {
		t.Errorf("payment = %+v", i)
	}
	if i := byName["stale"]; i.Branch != "feature/stale" || !i.Missing || i.Terminal != nil {
		t.Errorf("stale = %+v", i)
	}
	if infos[0].Name != "login" || infos[1].Name != "payment" || infos[2].Name != "stale" {
		t.Errorf("expected sorted by name, got %v %v %v", infos[0].Name, infos[1].Name, infos[2].Name)
	}

	if _, err := ws.Worktrees(ws.Projects[1]); err == nil {
		t.Error("Worktrees on a non-git project should fail")
	}
}

func TestRemoveWorktreeRules(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	mk := func(name, branch string) *Terminal {
		t.Helper()
		term, err := ws.CreateTerminal(p, Options{Name: name, Branch: branch, Worktree: true}, 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		return term
	}
	mk("InUse", "develop") // keeps its terminal running
	dirty := mk("Dirty", "feature/dirty")
	clean := mk("Clean", "feature/clean")
	stale := mk("Stale", "feature/stale")
	for _, term := range []*Terminal{dirty, clean, stale} {
		_ = term.Session.Close()
		ws.Remove(term.ID)
	}
	if err := os.WriteFile(filepath.Join(dirty.Worktree.Path, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(stale.Worktree.Path); err != nil {
		t.Fatal(err)
	}
	find := func(name string) WorktreeInfo {
		t.Helper()
		infos, err := ws.Worktrees(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range infos {
			if i.Name == name {
				return i
			}
		}
		t.Fatalf("worktree %q not listed in %+v", name, infos)
		return WorktreeInfo{}
	}
	exists := func(path string) bool { _, err := os.Stat(path); return err == nil }

	if err := ws.RemoveWorktree(p, find("inuse"), true); err == nil || !strings.Contains(err.Error(), "terminal 1 is still using") {
		t.Errorf("in-use worktree: err = %v", err)
	}
	if err := ws.RemoveWorktree(p, find("dirty"), false); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("dirty without force: err = %v", err)
	}
	if !exists(dirty.Worktree.Path) {
		t.Fatal("refused removal must not delete anything")
	}
	if err := ws.RemoveWorktree(p, find("dirty"), true); err != nil || exists(dirty.Worktree.Path) {
		t.Errorf("dirty with force: err = %v, exists = %v", err, exists(dirty.Worktree.Path))
	}
	if err := ws.RemoveWorktree(p, find("clean"), false); err != nil || exists(clean.Worktree.Path) {
		t.Errorf("clean: err = %v, exists = %v", err, exists(clean.Worktree.Path))
	}
	if err := ws.RemoveWorktree(p, find("stale"), false); err != nil {
		t.Errorf("stale: err = %v", err)
	}
	infos, _ := ws.Worktrees(p)
	if len(infos) != 1 || infos[0].Name != "inuse" {
		t.Errorf("after removals only the in-use worktree should remain, got %+v", infos)
	}

	// Branches survive worktree removal.
	branches, _, _ := ws.Branches(p)
	for _, b := range []string{"feature/dirty", "feature/clean", "feature/stale"} {
		if !contains(branches, b) {
			t.Errorf("branch %s should be kept", b)
		}
	}

	// Paths outside the owned directory are refused even if handed in directly.
	bogus := WorktreeInfo{Name: "x", Path: p.Path}
	if err := ws.RemoveWorktree(p, bogus, true); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("unowned path: err = %v", err)
	}
	if !exists(p.Path) {
		t.Fatal("project checkout must never be removed")
	}
}
