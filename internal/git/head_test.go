package git

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHeadReadsCheckoutsWorktreesAndDetachedState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", repo, "worktree", "add", "-q", "-b", "feature/login", filepath.Join(root, "login")},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if b, err := Head(repo); err != nil || b != "main" {
		t.Errorf("main checkout: %q, %v", b, err)
	}
	if b, err := Head(filepath.Join(root, "login")); err != nil || b != "feature/login" {
		t.Errorf("worktree: %q, %v", b, err)
	}
	if out, err := exec.Command("git", "-C", repo, "checkout", "-q", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("detach: %v\n%s", err, out)
	}
	if b, err := Head(repo); err != nil || b != "" {
		t.Errorf("detached HEAD should be \"\" with no error: %q, %v", b, err)
	}
	if _, err := Head(root); err != ErrNoRepository {
		t.Errorf("plain directory: err = %v, want ErrNoRepository", err)
	}
}
