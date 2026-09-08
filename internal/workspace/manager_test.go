package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
)

func rowNames(rows []Row) string {
	var out []string
	for _, r := range rows {
		s := r.Definition.Name
		if r.Terminal != nil {
			s = "run:" + s
		} else {
			s = "saved:" + s
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

func TestRowsAreTerminalFirstAndListUnusedWorktreesApart(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	ws.NewTerminal(app, 80, 24)
	login, _ := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	orphan, _ := ws.CreateTerminal(app, Options{Name: "Old", Branch: "feature/old", Base: ws.DefaultBranch(app), Worktree: true}, 80, 24)
	pay, _ := ws.CreateTerminal(app, Options{Name: "Payment", Branch: "feature/pay", Base: ws.DefaultBranch(app), Worktree: true}, 80, 24)
	ws.Remove(pay.ID)    // exits: saved
	ws.detach(orphan.ID) // gone without cleanup, as an older tm left it: its worktree is now unused

	rows, unused, err := ws.Rows(app)
	if err != nil {
		t.Fatal(err)
	}
	if got := rowNames(rows); got != "run:,run:Login,saved:Payment" {
		t.Errorf("rows = %s", got)
	}
	if rows[1].Definition.Worktree != login.Worktree.Path || rows[1].Definition.Branch != "develop" || rows[0].Definition.Worktree != "" {
		t.Errorf("worktree metadata wrong: %+v %+v", rows[0], rows[1])
	}
	if rows[2].Terminal != nil || rows[2].Definition.Worktree != pay.Worktree.Path || rows[2].Missing || rows[2].Stale {
		t.Errorf("saved row wrong: %+v", rows[2])
	}
	if len(unused) != 1 || unused[0].Path != orphan.Worktree.Path || unused[0].Branch != "feature/old" {
		t.Errorf("unused worktrees = %+v", unused)
	}
	// A saved worktree whose directory vanished, or that git forgot, is flagged.
	os.RemoveAll(pay.Worktree.Path)
	rows, _, _ = ws.Rows(app)
	if !rows[2].Missing || rows[2].Stale {
		t.Errorf("missing directory should be flagged: %+v", rows[2])
	}
	app.remember(SavedTerminal{Name: "Ghost", Branch: "x", Worktree: filepath.Join(ws.Root, WorktreesDir, "app", "ghost")})
	rows, _, _ = ws.Rows(app)
	if !rows[3].Stale {
		t.Errorf("unlisted worktree should be stale: %+v", rows[3])
	}
	if _, err := ws.OpenSaved(app, rows[2].Definition, 80, 24); err == nil || !strings.Contains(err.Error(), "directory missing") {
		t.Errorf("open missing = %v", err)
	}
	if _, err := ws.OpenSaved(app, rows[3].Definition, 80, 24); err == nil || !strings.Contains(err.Error(), "no longer in git") {
		t.Errorf("open stale = %v", err)
	}
	if _, err := os.Stat(pay.Worktree.Path); !os.IsNotExist(err) {
		t.Error("a refused open must not recreate the worktree")
	}

	// A project that is not a git repository still lists its terminals.
	plain := ws.Projects[1]
	plain.remember(SavedTerminal{Name: "Shell"})
	rows, unused, err = ws.Rows(plain)
	if !errors.Is(err, git.ErrNotRepository) || len(rows) != 1 || rows[0].Definition.Name != "Shell" || len(unused) != 0 {
		t.Errorf("non-git project: rows=%+v unused=%+v err=%v", rows, unused, err)
	}
}

// TestRowsDoNotFlagAForeignWorktreeAsStaleOrMissing guards against a defect
// exposed by the Restore fix above: git lists a worktree another tool
// created perfectly well, so a saved definition pointing at one must not be
// reported Stale (that means "git no longer lists the worktree", which
// would be false) or appear as an unused worktree tm might offer to remove.
func TestRowsDoNotFlagAForeignWorktreeAsStaleOrMissing(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]

	foreign := filepath.Join(t.TempDir(), "foreign")
	repo, err := git.Open(app.Path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := repo.AddWorktreeNewBranch(foreign, "feature/foreign", ws.DefaultBranch(app)); err != nil {
		t.Fatalf("creating a foreign worktree: %v", err)
	}
	app.remember(SavedTerminal{Command: "claude", Branch: "feature/foreign", Worktree: foreign})

	rows, unused, err := ws.Rows(app)
	if err != nil {
		t.Fatal(err)
	}
	var row Row
	found := false
	for _, r := range rows {
		if r.Definition.Worktree == foreign {
			row, found = r, true
		}
	}
	if !found {
		t.Fatalf("no row for the foreign worktree: %+v", rows)
	}
	if row.Stale {
		t.Error("a worktree git knows about must not be reported stale")
	}
	if row.Missing {
		t.Error("a worktree whose directory exists must not be reported missing")
	}
	if row.Definition.Branch != "feature/foreign" {
		t.Errorf("branch should come from git, got %+v", row)
	}
	for _, u := range unused {
		if u.Path == foreign {
			t.Errorf("a foreign worktree must never be offered in the unused/cleanup section: %+v", unused)
		}
	}
}

func TestOpenSavedStartsExactlyOneAndReusesTheWorktree(t *testing.T) {
	ws, spawned := gitWorkspace(t)
	app := ws.Projects[0]
	login, _ := ws.CreateTerminal(app, Options{Name: "Login", Branch: "develop", Worktree: true}, 80, 24)
	plain, _ := ws.CreateTerminal(app, Options{Name: "Plain"}, 80, 24)
	ws.Remove(login.ID)
	ws.Remove(plain.ID)
	before, _ := ws.Worktrees(app)
	n := len(*spawned)

	t2, err := ws.OpenSaved(app, app.Saved[0], 80, 24)
	if err != nil || t2.Name != "Login" || t2.Worktree == nil || t2.Worktree.Path != login.Worktree.Path {
		t.Fatalf("open Login: %+v %v", t2, err)
	}
	if len(*spawned) != n+1 || len(app.Terminals) != 1 || len(app.Saved) != 1 || app.Saved[0].Name != "Plain" {
		t.Errorf("exactly one started: spawned=%d running=%d saved=%+v", len(*spawned)-n, len(app.Terminals), app.Saved)
	}
	if after, _ := ws.Worktrees(app); len(after) != len(before) {
		t.Errorf("worktree must be reused: %d -> %d", len(before), len(after))
	}
	// A second definition for the same worktree now opens rather than being
	// refused: two shells may share a worktree just as they can share the
	// project checkout.
	t3, err := ws.OpenSaved(app, SavedTerminal{Name: "Login2", Command: "npm run dev", Branch: "develop", Worktree: login.Worktree.Path}, 80, 24)
	if err != nil || t3.Worktree == nil || t3.Worktree.Path != login.Worktree.Path {
		t.Fatalf("a second terminal in the same worktree should be allowed: %+v %v", t3, err)
	}
	if len(app.Terminals) != 2 {
		t.Errorf("both terminals sharing the worktree should be running, got %d", len(app.Terminals))
	}
}

func TestRestoreOnePrefersTheFocusedThenTheFirstRestorable(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	a, _ := ws.CreateTerminal(app, Options{Name: "A"}, 80, 24)
	b, _ := ws.CreateTerminal(app, Options{Name: "B", Branch: "develop", Worktree: true}, 80, 24)
	c, _ := ws.CreateTerminal(app, Options{Name: "C"}, 80, 24)
	app.Focus = b.Key() // as a saved layout would record it
	for _, term := range []*Terminal{a, b, c} {
		ws.Remove(term.ID)
	}
	res := ws.RestoreOne(app, 80, 24)
	if len(res.Opened) != 1 || res.Opened[0].Name != "B" || len(res.Skipped) != 0 || len(app.Saved) != 2 {
		t.Fatalf("focused B first: %+v saved=%+v", res, app.Saved)
	}
	ws.Remove(res.Opened[0].ID)

	// Focused unavailable: first restorable in saved order (A), with B reported.
	os.RemoveAll(b.Worktree.Path)
	res = ws.RestoreOne(app, 80, 24)
	if len(res.Opened) != 1 || res.Opened[0].Name != "A" || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "B (worktree directory missing)") {
		t.Errorf("fallback: %+v", res)
	}
	if len(app.Saved) != 2 || len(app.Terminals) != 1 {
		t.Errorf("only A running: running=%d saved=%+v", len(app.Terminals), app.Saved)
	}
	// Nothing restorable: empty result, definitions kept.
	ws.Remove(res.Opened[0].ID)
	app.Saved = []SavedTerminal{app.Saved[1]} // only B left (missing)
	app.Focus = ""
	res = ws.RestoreOne(app, 80, 24)
	if len(res.Opened) != 0 || len(res.Skipped) != 1 || len(app.Saved) != 1 {
		t.Errorf("nothing restorable: %+v saved=%+v", res, app.Saved)
	}
	// The limit applies.
	max := 0
	ws.Limits = fixedLimit(&max)
	app.remember(SavedTerminal{Name: "D"})
	ws.NewTerminal(app, 80, 24) // (max 0 -> default 6 applies) one running
	max = 1
	res = ws.RestoreOne(app, 80, 24)
	if len(res.Opened) != 0 || res.NotOpened == 0 {
		t.Errorf("restore one at the limit: %+v", res)
	}
}

func TestRestoreSkippedLabelPrefersCommandOverName(t *testing.T) {
	ws, _ := gitWorkspace(t)
	app := ws.Projects[0]
	// The new form never sets Name, so a skipped definition must be labelled
	// by its Command rather than falling straight through to "shell".
	deploy, err := ws.CreateTerminal(app, Options{Command: "deploy", Branch: "develop", Worktree: true}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	ws.Remove(deploy.ID)
	os.RemoveAll(deploy.Worktree.Path)

	res := ws.RestoreOne(app, 80, 24)
	if len(res.Opened) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "deploy (worktree directory missing)") {
		t.Errorf("skipped label should use Command, not \"shell\": %+v", res.Skipped)
	}

	// A definition with neither Name nor Command still falls back to "shell".
	app.Saved = nil
	plain, err := ws.CreateTerminal(app, Options{Branch: "feature/plain", Base: ws.DefaultBranch(app), Worktree: true}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	ws.Remove(plain.ID)
	os.RemoveAll(plain.Worktree.Path)
	res = ws.RestoreOne(app, 80, 24)
	if len(res.Opened) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "shell (worktree directory missing)") {
		t.Errorf("fallback to \"shell\" when Name and Command are both empty: %+v", res.Skipped)
	}
}

func TestRecordLayoutKeepsSavedLayoutWhileTerminalsArePending(t *testing.T) {
	ws, _ := newTestWorkspace()
	p := ws.Projects[0]
	a, _ := ws.CreateTerminal(p, Options{Name: "A"}, 80, 24)
	b, _ := ws.CreateTerminal(p, Options{Name: "B"}, 80, 24)
	c, _ := ws.CreateTerminal(p, Options{Name: "C"}, 80, 24)
	full := &Layout{Side: true, Ratio: 0.3, First: &Layout{Key: a.Key()}, Second: &Layout{Ratio: 0.5, First: &Layout{Key: b.Key()}, Second: &Layout{Key: c.Key()}}}
	ws.RecordLayout(p, full, a.Key())
	if p.Layout != full || p.Focus != a.Key() {
		t.Fatal("first record is taken as is")
	}

	// B hidden (still running): the live tree shows A|C; the saved layout is kept, focus updated.
	live := &Layout{Side: true, Ratio: 0.3, First: &Layout{Key: a.Key()}, Second: &Layout{Key: c.Key()}}
	ws.RecordLayout(p, live, c.Key())
	if p.Layout != full || p.Focus != c.Key() {
		t.Error("hiding must not overwrite the saved layout")
	}
	// B exits (saved): still pending.
	ws.Remove(b.ID)
	ws.RecordLayout(p, live, c.Key())
	if p.Layout != full {
		t.Error("a saved-but-not-running terminal keeps the layout pending")
	}
	// Everything hidden: live is nil, layout kept.
	ws.RecordLayout(p, nil, a.Key())
	if p.Layout != full {
		t.Error("an empty screen must not erase the layout")
	}
	// A new terminal joins while B is still pending: layout still kept.
	d, _ := ws.CreateTerminal(p, Options{Name: "D"}, 80, 24)
	withD := &Layout{Side: true, Ratio: 0.5, First: live, Second: &Layout{Key: d.Key()}}
	ws.RecordLayout(p, withD, d.Key())
	if p.Layout != full {
		t.Error("new terminals do not throw the saved layout away while B can still come back")
	}
	// B deleted for good: nothing pending, the live tree becomes the layout.
	p.forget(SavedTerminal{Name: "B"})
	ws.RecordLayout(p, withD, d.Key())
	if p.Layout != withD {
		t.Error("with nothing pending the live tree is recorded")
	}
	// Same-key terminals: two unnamed shells, one hidden, still pending.
	ws2, _ := newTestWorkspace()
	q := ws2.Projects[0]
	s1, _ := ws2.NewTerminal(q, 80, 24)
	s2, _ := ws2.NewTerminal(q, 80, 24)
	pair := &Layout{Side: true, Ratio: 0.5, First: &Layout{Key: s1.Key()}, Second: &Layout{Key: s2.Key()}}
	ws2.RecordLayout(q, pair, s1.Key())
	ws2.RecordLayout(q, &Layout{Key: s1.Key()}, s1.Key())
	if q.Layout != pair {
		t.Error("a hidden unnamed shell keeps the pair layout pending")
	}
	ws2.DeleteTerminal(q, s2.Definition(), s2)
	ws2.RecordLayout(q, &Layout{Key: s1.Key()}, s1.Key())
	if q.Layout == pair {
		t.Error("once the second shell is gone the single pane is the layout")
	}
}
