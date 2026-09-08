package workspace

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLocateAnEmptyBranchIsTheProjectCheckout(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]

	loc := ws.Locator(p).Locate("")

	if loc.Kind != LocationCheckout {
		t.Errorf("Kind = %v, want LocationCheckout", loc.Kind)
	}
	if loc.Path != p.Path {
		t.Errorf("Path = %q, want the project path %q", loc.Path, p.Path)
	}
}

func TestLocateTheCheckoutsOwnBranchIsTheCheckout(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	_, current, err := ws.Branches(p)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}

	loc := ws.Locator(p).Locate(current)

	if loc.Kind != LocationCheckout {
		t.Errorf("Kind = %v, want LocationCheckout for the checked-out branch", loc.Kind)
	}
	if loc.Path != p.Path {
		t.Errorf("Path = %q, want %q", loc.Path, p.Path)
	}
}

func TestLocateAnExistingWorktreeAndItsTerminals(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	term, err := ws.CreateTerminal(p, Options{Worktree: true, Branch: "feature/login", Base: ws.DefaultBranch(p), Command: "claude"}, 80, 24)
	if err != nil {
		t.Fatalf("CreateTerminal: %v", err)
	}

	loc := ws.Locator(p).Locate("feature/login")

	if loc.Kind != LocationWorktree {
		t.Errorf("Kind = %v, want LocationWorktree", loc.Kind)
	}
	if loc.Path != term.Worktree.Path {
		t.Errorf("Path = %q, want %q", loc.Path, term.Worktree.Path)
	}
	if loc.Foreign {
		t.Error("Foreign = true for a worktree tm created")
	}
	if len(loc.Terminals) != 1 || loc.Terminals[0] != term {
		t.Errorf("Terminals = %v, want the one terminal running there", loc.Terminals)
	}
}

func TestLocateAnUncheckedOutBranchIsANewWorktree(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	branches, _, err := ws.Branches(p)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	var free string
	_, current, _ := ws.Branches(p)
	for _, b := range branches {
		if b != current {
			free = b
			break
		}
	}
	if free == "" {
		t.Skip("fixture has only one branch")
	}

	loc := ws.Locator(p).Locate(free)

	if loc.Kind != LocationNewWorktree {
		t.Errorf("Kind = %v, want LocationNewWorktree for a branch checked out nowhere", loc.Kind)
	}
	if loc.NewBranch {
		t.Error("NewBranch = true for a branch that already exists")
	}
}

// TestLocateAWorktreeOutsideTheRootIsMarkedOutOfRoot covers a worktree made
// with `git worktree add` entirely outside the workspace root (e.g. a
// person's own ~/wt-foo). The state file only ever stores workspace-relative
// worktree paths, so such a location can never be persisted or reopened
// after a restart; Locate must flag it so callers (the form) can refuse to
// use it instead of silently losing the pane later.
func TestLocateAWorktreeOutsideTheRootIsMarkedOutOfRoot(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]

	outside := filepath.Join(t.TempDir(), "wt-foo")
	if out, err := exec.Command("git", "-C", p.Path, "worktree", "add", "-q", "-b", "feature/outside", outside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	loc := ws.Locator(p).Locate("feature/outside")

	if loc.Kind != LocationWorktree {
		t.Errorf("Kind = %v, want LocationWorktree", loc.Kind)
	}
	if !loc.OutOfRoot {
		t.Error("OutOfRoot = false for a worktree outside the workspace root, want true")
	}
	if loc.Path != outside {
		t.Errorf("Path = %q, want %q", loc.Path, outside)
	}
}

// TestLocateAWorktreeInsideTheRootIsNotOutOfRoot is the control for the test
// above: a foreign worktree that still lives under the workspace root can be
// persisted (its path round-trips through the state file), so it must not be
// flagged.
func TestLocateAWorktreeInsideTheRootIsNotOutOfRoot(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]

	inside := filepath.Join(ws.Root, "external", "manual-checkout")
	if out, err := exec.Command("git", "-C", p.Path, "worktree", "add", "-q", "-b", "feature/manual", inside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}

	loc := ws.Locator(p).Locate("feature/manual")

	if loc.OutOfRoot {
		t.Error("OutOfRoot = true for a worktree inside the workspace root, want false")
	}
	if !loc.Foreign {
		t.Error("Foreign = false for a worktree tm did not create, want true")
	}
}

// TestLocateNamesTheCheckoutConsistently covers Fix 5: an empty branch and
// the checkout's own branch must resolve Path to the identical value
// (s.project.Path), not sometimes to git's resolved toplevel (wt.Path),
// which can differ under a symlinked root.
func TestLocateNamesTheCheckoutConsistently(t *testing.T) {
	ws, _ := gitWorkspace(t)
	p := ws.Projects[0]
	_, current, err := ws.Branches(p)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}

	empty := ws.Locator(p).Locate("")
	named := ws.Locator(p).Locate(current)

	if empty.Path != named.Path {
		t.Errorf("Path differs: Locate(\"\") = %q, Locate(%q) = %q", empty.Path, current, named.Path)
	}
	if named.Path != p.Path {
		t.Errorf("Path = %q, want the project path %q", named.Path, p.Path)
	}
}

func TestLocateAnUnknownBranchNeedsABase(t *testing.T) {
	ws, _ := gitWorkspace(t)

	loc := ws.Locator(ws.Projects[0]).Locate("feature/does-not-exist")

	if loc.Kind != LocationNewWorktree {
		t.Errorf("Kind = %v, want LocationNewWorktree", loc.Kind)
	}
	if !loc.NewBranch {
		t.Error("NewBranch = false for a branch that does not exist, want true")
	}
}
