package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// orphanWorktree adds a worktree under tm's directory for p that no
// terminal knows about, as a manual git command might leave behind.
func orphanWorktree(t *testing.T, m Model, p *workspace.Project, name, branch string) string {
	t.Helper()
	path := filepath.Join(m.root, workspace.WorktreesDir, p.Name, name)
	if out, err := exec.Command("git", "-C", p.Path, "worktree", "add", "-q", "-b", branch, path).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return path
}

func TestDeletingACheckoutTerminalNeverTouchesTheCheckout(t *testing.T) {
	m, spawned, _ := gitModel(t)
	m = press(m, "enter") // plain shell in the project checkout
	p := m.ws.Projects[0]
	m = press(m, "esc", "m", "d")
	if !strings.Contains(m.View(), "Delete Terminal 1?") || !strings.Contains(m.View(), "project checkout is untouched") {
		t.Fatalf("prompt:\n%s", m.View())
	}
	m = press(m, "y")
	if len(p.Terminals) != 0 || len(p.Saved) != 0 || !(*spawned)[0].closed {
		t.Error("the definition goes and the process stops")
	}
	if _, err := os.Stat(filepath.Join(p.Path, ".git")); err != nil {
		t.Fatal("the project checkout must never be removed")
	}
	if out, _ := exec.Command("git", "-C", p.Path, "branch", "--show-current").Output(); strings.TrimSpace(string(out)) != "main" {
		t.Errorf("checkout branch changed: %q", out)
	}
	if !strings.Contains(m.View(), "deleted Terminal 1") || strings.Contains(m.View(), "worktree") {
		t.Errorf("notice should not mention a worktree:\n%s", m.View())
	}
}

func TestAltDeleteAndManagerDShareDeletionSemantics(t *testing.T) {
	m, spawned, p := repoWithThree(t)
	login, payment := p.Terminals[1], p.Terminals[2]
	loginPath, paymentPath := login.Worktree.Path, payment.Worktree.Path

	for p.ActiveTerminal() != login { // alt+delete on the focused Login
		m = press(m, "alt+k")
	}
	m = press(m, "alt+delete")
	altPrompt := m.deletePrompt
	m = press(m, "y")
	altNotice := m.notice

	m = press(m, "esc", "m", "j", "d") // manager d on Payment (rows: shell, Payment)
	mgPrompt := m.panel.prompt
	m = press(m, "y")
	mgNotice := m.panel.notice

	for _, c := range []struct{ name, num, branch, prompt, notice string }{{"Login", "2", "develop", altPrompt, altNotice}, {"Payment", "3", "feature/payment", mgPrompt, mgNotice}} {
		if want := "Delete Terminal " + c.num + " and its worktree \"" + c.branch + "\"? The worktree is clean."; c.prompt != want {
			t.Errorf("%s prompt = %q, want %q", c.name, c.prompt, want)
		}
		if c.notice != "deleted Terminal "+c.num+" and its worktree" {
			t.Errorf("%s notice = %q", c.name, c.notice)
		}
	}
	for _, path := range []string{loginPath, paymentPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("worktree %s should be removed", path)
		}
	}
	if !(*spawned)[1].closed || !(*spawned)[2].closed || len(p.Terminals) != 1 || len(p.Saved) != 0 {
		t.Errorf("both processes stopped and definitions gone: running=%d saved=%d", len(p.Terminals), len(p.Saved))
	}
	if list, _ := m.ws.Worktrees(p); len(list) != 0 {
		t.Errorf("no worktree should remain: %+v", list)
	}
	if strings.Contains(m.View(), "Unused worktrees") {
		t.Error("a removed worktree never shows up as unused")
	}
	if _, err := os.Stat(filepath.Join(p.Path, ".git")); err != nil {
		t.Fatal("the project checkout survives")
	}
}

func TestDirtyWorktreeRequiresConfirmationAndCancelKeepsEverything(t *testing.T) {
	m, spawned, p := repoWithThree(t)
	login := p.Terminals[1]
	wip := filepath.Join(login.Worktree.Path, "wip.txt")
	if err := os.WriteFile(wip, []byte("unsaved work"), 0o644); err != nil {
		t.Fatal(err)
	}
	// From the terminal.
	for p.ActiveTerminal() != login {
		m = press(m, "alt+k")
	}
	m = press(m, "alt+delete")
	if !strings.Contains(m.deletePrompt, "Delete Terminal 2 and its worktree \"develop\"") || !strings.Contains(m.deletePrompt, "UNCOMMITTED CHANGES") {
		t.Fatalf("dirty prompt = %q", m.deletePrompt)
	}
	if !strings.Contains(m.View(), "UNCOMMITTED CHANGES") {
		t.Error("the warning must be on screen")
	}
	m = press(m, "esc") // cancel
	if m.deleting != nil || (*spawned)[1].closed || len(p.Terminals) != 3 {
		t.Fatal("cancel must keep the terminal running")
	}
	if data, err := os.ReadFile(wip); err != nil || string(data) != "unsaved work" {
		t.Fatal("cancel must keep the dirty worktree and its data")
	}
	// From the manager: same warning, cancel keeps everything, y removes.
	m = press(m, "esc", "m", "j", "d")
	if !strings.Contains(m.panel.prompt, "UNCOMMITTED CHANGES") {
		t.Fatalf("manager prompt = %q", m.panel.prompt)
	}
	m = press(m, "k")
	if _, err := os.Stat(wip); err != nil || len(p.Terminals) != 3 {
		t.Fatal("cancel from the manager keeps everything")
	}
	m = press(m, "d", "y")
	if _, err := os.Stat(login.Worktree.Path); !os.IsNotExist(err) {
		t.Error("confirmed: the dirty worktree is removed")
	}
	if len(p.Terminals) != 2 || !(*spawned)[1].closed {
		t.Error("confirmed: the terminal is gone")
	}
}

func TestDeletingATerminalWhoseWorktreeVanishedStillCleansUp(t *testing.T) {
	m, spawned, p := repoWithThree(t)
	login := p.Terminals[1]
	if err := os.RemoveAll(login.Worktree.Path); err != nil {
		t.Fatal(err)
	}
	for p.ActiveTerminal() != login {
		m = press(m, "alt+k")
	}
	m = press(m, "alt+delete")
	if !strings.Contains(m.deletePrompt, "worktree directory is already gone") {
		t.Fatalf("prompt = %q", m.deletePrompt)
	}
	m = press(m, "y")
	if len(p.Terminals) != 2 || !(*spawned)[1].closed || len(p.Saved) != 0 {
		t.Error("the definition and process go regardless")
	}
	if list, _ := m.ws.Worktrees(p); len(list) != 1 {
		t.Errorf("git's stale record should be pruned, leaving Payment's: %+v", list)
	}
	if _, err := os.Stat(login.Worktree.Path); !os.IsNotExist(err) {
		t.Error("nothing may be recreated")
	}
	if !strings.Contains(m.notice, "deleted Terminal 2") {
		t.Errorf("notice = %q", m.notice)
	}
}

func TestDeletingKeepsAWorktreeAnotherDefinitionUses(t *testing.T) {
	m, _, p := repoWithThree(t)
	login := p.Terminals[1]
	p.Saved = append(p.Saved, workspace.SavedTerminal{Name: "Twin", Branch: "develop", Worktree: login.Worktree.Path})
	m = press(m, "esc", "m", "j", "j", "j", "d", "y") // rows: shell, Login, Payment, Twin(saved)
	if len(p.Saved) != 0 {
		t.Errorf("the twin definition goes: %+v", p.Saved)
	}
	if _, err := os.Stat(login.Worktree.Path); err != nil {
		t.Fatal("the worktree Login still uses must survive")
	}
	if !strings.Contains(m.panel.notice, "worktree kept") || !strings.Contains(m.panel.notice, "terminal 2 uses it") {
		t.Errorf("notice = %q", m.panel.notice)
	}
}

func TestManagerRowsKeepIdentityAndBranchReadable(t *testing.T) {
	m, spawned, _ := gitModel(t)
	m = makeTerminal(t, m, "n", "", "feature/payment-gateway")
	(*spawned)[0].title = "node --inspect=0.0.0.0:9229 ./node_modules/.bin/vite --host 0.0.0.0 --port 5173 --strictPort"
	// Wide enough: the identity line carries number, mark and full branch;
	// the second line the program and the state, and only the program is cut.
	for _, width := range []int{120, 80} {
		m = press(m, "esc")
		m.width = width
		m = press(m, "m")
		view := m.View()
		for _, want := range []string{"Terminal 1 · ⎇ feature/payment-gateway", "node --inspect", "visible"} {
			if !strings.Contains(view, want) {
				t.Errorf("%d cols: %q must survive:\n%s", width, want, view)
			}
		}
		if strings.Contains(view, "--strictPort") {
			t.Errorf("%d cols: the program title is what gets truncated, from the right:\n%s", width, view)
		}
		for _, forbidden := range []string{".terminal-worktrees", "worktree"} {
			if strings.Contains(view, forbidden) {
				t.Errorf("%d cols: %q must not appear on a terminal row:\n%s", width, forbidden, view)
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if w := len([]rune(stripANSI(line))); w > width {
				t.Errorf("%d cols: line overflows (%d cells): %q", width, w, line)
			}
		}
	}
	// Too narrow for the whole branch: identity is cut with an ellipsis but
	// keeps its head, and the state stays whole.
	m = press(m, "esc")
	m.width = 64
	m = press(m, "m")
	view := m.View()
	if !strings.Contains(view, "Terminal 1 · ⎇ feature/pay") || !strings.Contains(view, "…") {
		t.Errorf("64 cols: identity keeps its head and shows an ellipsis:\n%s", view)
	}
	if !strings.Contains(view, "visible") || strings.Contains(view, "--strictPort") {
		t.Errorf("64 cols: state whole, program cut:\n%s", view)
	}
}
