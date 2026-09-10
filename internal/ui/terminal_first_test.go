package ui

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Pane headers name the terminal and the branch it is on, whether it runs
// in the project checkout or in its own worktree; the worktree itself is
// not something the header talks about.
func TestPaneHeadersShowTerminalAndBranch(t *testing.T) {
	m, spawned, p := repoWithThree(t) // 1: checkout on main, 2: Login on develop, 3: Payment on feature/payment
	view := m.View()
	for _, want := range []string{" Terminal 1 · main ", " Terminal 2 · ⎇ develop ", " Terminal 3 · ⎇ feature/payment "} {
		if !strings.Contains(view, want) {
			t.Errorf("header %q missing:\n%s", want, view)
		}
	}
	for _, forbidden := range []string{"[develop]", ".terminal-worktrees", "worktree"} {
		if strings.Contains(m.viewPanes(p, p.ActiveTerminal()), forbidden) {
			t.Errorf("pane view must not expose %q", forbidden)
		}
	}
	_ = spawned
}

func TestPaneHeaderFollowsCheckoutsAndDetachedHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	old := branchTTL
	branchTTL = 0 // re-read HEAD on every render for this test
	defer func() { branchTTL = old }()
	m, _, p := repoWithThree(t)
	login := p.Terminals[1]
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("-C", login.Worktree.Path, "checkout", "-q", "-b", "experiment")
	if v := m.View(); !strings.Contains(v, "Terminal 2 · ⎇ experiment") || strings.Contains(v, "⎇ develop") {
		t.Errorf("header should follow a checkout made in the worktree:\n%s", v)
	}
	if v := m.View(); !strings.Contains(v, "Terminal 1 · main") {
		t.Errorf("the main checkout is untouched by the worktree's checkout:\n%s", v)
	}
	run("-C", p.Path, "checkout", "-q", "--detach")
	if v := m.View(); !strings.Contains(v, "Terminal 1 · detached") {
		t.Errorf("detached HEAD should read as detached:\n%s", v)
	}
	// The lookup is cached: with the normal TTL a fresh checkout is not re-read at once.
	branchTTL = time.Hour
	run("-C", p.Path, "checkout", "-q", "main")
	if v := m.View(); !strings.Contains(v, "Terminal 1 · detached") {
		t.Errorf("within the TTL the cached answer is reused:\n%s", v)
	}
}

func TestManagerIsOneRowPerTerminalWithBranchAndState(t *testing.T) {
	m, spawned, p := repoWithThree(t)
	login, payment := p.Terminals[1], p.Terminals[2]
	for p.ActiveTerminal() != login {
		m = press(m, "alt+k")
	}
	m = press(m, "alt+x") // hide Login: still running
	_ = payment.Session.(*fakeSession).Close()
	m = exit(m, payment.ID) // Payment's shell exits: saved, not running
	if len(p.Terminals) != 2 || len(p.Saved) != 1 || m.layouts[p].contains(login) {
		t.Fatalf("setup: running=%d saved=%d loginVisible=%v", len(p.Terminals), len(p.Saved), m.layouts[p].contains(login))
	}

	m = press(m, "alt+esc", "m")
	view := m.View()
	rows := 0
	for _, r := range m.panel.rows {
		if r.terminal != nil {
			rows++
		}
	}
	if rows != 3 {
		t.Errorf("one row per terminal (2 running + 1 saved), got %d", rows)
	}
	for _, want := range []string{
		"● Terminal 1 · main", "visible", // plain terminal on the checkout branch
		"○ Terminal 2 · ⎇ develop", "hidden", // running, hidden by alt+x
		"○ Terminal · ⎇ feature/payment", "saved", // definition without a process
	} {
		if !strings.Contains(view, want) {
			t.Errorf("manager missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Unused worktrees") || strings.Contains(view, ".terminal-worktrees") {
		t.Errorf("worktrees that belong to terminals are not listed separately and paths stay out of the terminal list:\n%s", view)
	}
	if !strings.Contains(view, "Terminal 1") || strings.Count(view, "Terminal 2") != 1 {
		t.Errorf("identity lines wrong:\n%s", view)
	}
	// Hidden terminals are not drawn in the main view.
	m = press(m, "esc")
	if v := m.viewPanes(p, p.ActiveTerminal()); strings.Contains(v, "Login") {
		t.Errorf("hidden terminal must not appear as a pane:\n%s", v)
	}
	if len(*spawned) != 3 || (*spawned)[1].closed {
		t.Error("nothing was spawned or stopped by looking")
	}
}

func TestUnusedWorktreesListOnlyOrphans(t *testing.T) {
	m, _, p := repoWithThree(t)
	m = press(m, "alt+esc", "m")
	if strings.Contains(m.View(), "Unused worktrees") {
		t.Fatal("every worktree belongs to a terminal: no unused section")
	}
	orphan := orphanWorktree(t, m, p, "old-login", "feature/old-login")
	m = press(m, "esc", "m")
	view := m.View()
	if !strings.Contains(view, "Unused worktrees") || !strings.Contains(view, "old-login · feature/old-login") || !strings.Contains(view, "clean") {
		t.Errorf("the orphaned worktree should be listed under Unused worktrees with its branch:\n%s", view)
	}
	// Worktrees that belong to terminals appear once, on their terminal's row.
	for _, branch := range []string{"develop", "feature/payment"} {
		if strings.Count(view, branch) != 1 {
			t.Errorf("%s should appear once, on its terminal row:\n%s", branch, view)
		}
	}
	// Only the orphan row reveals a path, and only while selected.
	if strings.Contains(view, "path  ") {
		t.Errorf("no path while a terminal is selected:\n%s", view)
	}
	m = press(m, "j", "j", "j")
	if r := m.panel.selected(); r == nil || r.worktree == nil || r.worktree.Path != orphan || !strings.Contains(m.View(), "path  ") {
		t.Errorf("selecting the unused worktree shows where it lives: %+v", r)
	}
}
