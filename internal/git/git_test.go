package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// initRepo creates a repository with one commit on "main" and a "develop" branch.
func initRepo(t *testing.T) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"branch", "develop"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOpenDetectsRepositoryAndSubdirectories(t *testing.T) {
	r := initRepo(t)
	sub := filepath.Join(r.Dir, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	fromSub, err := Open(sub)
	if err != nil {
		t.Fatal(err)
	}
	if fromSub.Dir != r.Dir {
		t.Errorf("Open(subdir).Dir = %q, want %q", fromSub.Dir, r.Dir)
	}
	if !IsRepository(r.Dir) {
		t.Error("IsRepository should be true for a repo")
	}
}

func TestOpenRejectsNonRepository(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(dir)
	if !errors.Is(err, ErrNotRepository) {
		t.Fatalf("Open(non-repo) error = %v, want ErrNotRepository", err)
	}
	if IsRepository(dir) {
		t.Error("IsRepository should be false for a plain directory")
	}
	if _, err := Open(filepath.Join(dir, "missing")); !errors.Is(err, ErrNotRepository) {
		t.Errorf("Open(missing dir) error = %v, want ErrNotRepository", err)
	}
}

func TestCurrentBranchAndBranches(t *testing.T) {
	r := initRepo(t)
	cur, err := r.CurrentBranch()
	if err != nil || cur != "main" {
		t.Fatalf("CurrentBranch() = %q, %v; want main", cur, err)
	}
	branches, err := r.Branches()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"develop", "main"}; !reflect.DeepEqual(branches, want) {
		t.Errorf("Branches() = %v, want %v", branches, want)
	}
	if !r.HasBranch("develop") || r.HasBranch("nope") {
		t.Error("HasBranch gave wrong answers")
	}
}

func TestWorktreeFromExistingBranch(t *testing.T) {
	r := initRepo(t)
	path := filepath.Join(t.TempDir(), "dev")
	if err := r.AddWorktree(path, "develop"); err != nil {
		t.Fatal(err)
	}
	wt, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if cur, _ := wt.CurrentBranch(); cur != "develop" {
		t.Errorf("worktree branch = %q, want develop", cur)
	}

	list, err := r.Worktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].IsMain || list[0].Branch != "main" || list[1].IsMain || list[1].Branch != "develop" || list[1].Path != path {
		t.Errorf("Worktrees() = %+v", list)
	}
}

func TestWorktreeWithNewBranch(t *testing.T) {
	r := initRepo(t)
	path := filepath.Join(t.TempDir(), "pay")
	if err := r.AddWorktreeNewBranch(path, "feature/payment", ""); err != nil {
		t.Fatal(err)
	}
	if !r.HasBranch("feature/payment") {
		t.Error("new branch should exist in the repo")
	}
	wt, _ := Open(path)
	if cur, _ := wt.CurrentBranch(); cur != "feature/payment" {
		t.Errorf("worktree branch = %q, want feature/payment", cur)
	}
}

func TestWorktreeRejectsBranchCheckedOutElsewhere(t *testing.T) {
	r := initRepo(t)
	err := r.AddWorktree(filepath.Join(t.TempDir(), "again"), "main") // main is checked out in r.Dir
	var gitErr *Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("expected *Error, got %v", err)
	}
	if !strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "Preparing") {
		t.Errorf("error should carry git's fatal line only, got %q", err.Error())
	}
}

func TestWorktreeRejectsExistingPathAndUnknownBranch(t *testing.T) {
	r := initRepo(t)
	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.AddWorktree(occupied, "develop"); err == nil {
		t.Error("adding a worktree into a non-empty directory should fail")
	}
	if err := r.AddWorktree(filepath.Join(t.TempDir(), "x"), "does-not-exist"); err == nil {
		t.Error("adding a worktree for an unknown branch should fail")
	}
	if err := r.AddWorktreeNewBranch(filepath.Join(t.TempDir(), "y"), "develop", ""); err == nil {
		t.Error("creating a branch that already exists should fail")
	}
}

func TestRemoveWorktree(t *testing.T) {
	r := initRepo(t)
	path := filepath.Join(t.TempDir(), "dev")
	if err := r.AddWorktree(path, "develop"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveWorktree(path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("worktree directory should be gone")
	}
	if list, _ := r.Worktrees(); len(list) != 1 {
		t.Errorf("Worktrees() after remove = %+v", list)
	}
	if !r.HasBranch("develop") {
		t.Error("removing a worktree must keep its branch")
	}
	if err := r.RemoveWorktree(path, false); err == nil {
		t.Error("removing a missing worktree should fail")
	}
}

func TestIsDirtyAndForceRemove(t *testing.T) {
	r := initRepo(t)
	path := filepath.Join(t.TempDir(), "dev")
	if err := r.AddWorktree(path, "develop"); err != nil {
		t.Fatal(err)
	}
	if dirty, err := IsDirty(path); err != nil || dirty {
		t.Fatalf("fresh worktree: dirty=%v err=%v, want clean", dirty, err)
	}
	if err := os.WriteFile(filepath.Join(path, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, err := IsDirty(path); err != nil || !dirty {
		t.Fatalf("untracked file: dirty=%v err=%v, want dirty", dirty, err)
	}
	if err := r.RemoveWorktree(path, false); err == nil {
		t.Fatal("removing a dirty worktree without force should fail")
	}
	if err := r.RemoveWorktree(path, true); err != nil {
		t.Fatalf("force remove: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("worktree directory should be gone after force remove")
	}
	if _, err := IsDirty(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("IsDirty on a missing directory should fail")
	}
}

func TestWorktreeNewBranchStartsFromTheGivenBase(t *testing.T) {
	r := initRepo(t)
	head, err := run(r.Dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	// feature/login gets a commit of its own, made inside its worktree so the
	// project checkout is never touched.
	first := filepath.Join(t.TempDir(), "first")
	if err := r.AddWorktreeNewBranch(first, "feature/login", ""); err != nil {
		t.Fatalf("create feature/login: %v", err)
	}
	if _, err := run(first, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"commit", "-q", "--allow-empty", "-m", "work"); err != nil {
		t.Fatalf("commit on feature/login: %v", err)
	}
	tip, err := run(r.Dir, "rev-parse", "feature/login")
	if err != nil {
		t.Fatal(err)
	}
	if tip == head {
		t.Fatal("feature/login must be ahead of HEAD or this test proves nothing")
	}

	stacked := filepath.Join(t.TempDir(), "stacked")
	if err := r.AddWorktreeNewBranch(stacked, "feature/login-fix", "feature/login"); err != nil {
		t.Fatalf("create feature/login-fix: %v", err)
	}
	fix, err := run(r.Dir, "rev-parse", "feature/login-fix")
	if err != nil {
		t.Fatal(err)
	}
	if fix != tip {
		t.Errorf("feature/login-fix is at %s, want the tip of feature/login %s", fix, tip)
	}
	if fix == head {
		t.Error("feature/login-fix started from HEAD, ignoring its base")
	}
}

func TestDefaultBranchPrefersMainThenMaster(t *testing.T) {
	r := initRepo(t) // creates the repository on main
	if got := r.DefaultBranch(); got != "main" {
		t.Errorf("DefaultBranch() = %q, want main", got)
	}
	if _, err := run(r.Dir, "branch", "-m", "main", "master"); err != nil {
		t.Fatal(err)
	}
	if got := r.DefaultBranch(); got != "master" {
		t.Errorf("after renaming main, DefaultBranch() = %q, want master", got)
	}
	if _, err := run(r.Dir, "branch", "-m", "master", "trunk"); err != nil {
		t.Fatal(err)
	}
	if got := r.DefaultBranch(); got != "trunk" {
		t.Errorf("with neither main nor master, DefaultBranch() = %q, want the checked-out branch trunk", got)
	}
}

func TestHasRefAcceptsBranchesAndRejectsTypos(t *testing.T) {
	r := initRepo(t)
	if _, err := run(r.Dir, "update-ref", "refs/remotes/origin/main", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(r.Dir, "tag", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	head, err := run(r.Dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(head)
	for _, ref := range []string{
		"main", "develop", // local branches, both created by initRepo
		"origin/main", // remote-tracking branch
		"v1.0.0",      // tag
		commit,        // commit id
	} {
		if !r.HasRef(ref) {
			t.Errorf("HasRef(%q) = false, want true", ref)
		}
	}
	for _, ref := range []string{
		"no/such/branch",
		"--all",     // dash-prefixed: rev-parse must treat this as an option, not a ref, and reject it
		"--git-dir", // same
	} {
		if r.HasRef(ref) {
			t.Errorf("HasRef(%q) = true, want false", ref)
		}
	}
}

func TestPruneDropsStaleWorktree(t *testing.T) {
	r := initRepo(t)
	path := filepath.Join(t.TempDir(), "dev")
	if err := r.AddWorktree(path, "develop"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(path); err != nil { // simulate a directory deleted behind git's back
		t.Fatal(err)
	}
	if list, _ := r.Worktrees(); len(list) != 2 {
		t.Fatalf("stale worktree should still be listed before prune: %+v", list)
	}
	if err := r.Prune(); err != nil {
		t.Fatal(err)
	}
	if list, _ := r.Worktrees(); len(list) != 1 {
		t.Errorf("stale worktree should be gone after prune: %+v", list)
	}
}
