package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFindsDirectChildGitRepos(t *testing.T) {
	root := t.TempDir()

	mustMkdir(t, filepath.Join(root, "backend", ".git"))
	mustMkdir(t, filepath.Join(root, "ecommerce", ".git"))
	mustMkdir(t, filepath.Join(root, "notes"))                                  // plain dir, no .git
	mustMkdir(t, filepath.Join(root, "nested", "deep", ".git"))                 // grandchild, must be ignored
	mustWrite(t, filepath.Join(root, "README.md"), "hello")                     // plain file
	mustWrite(t, filepath.Join(root, "worktree", ".git"), "gitdir: /elsewhere") // .git as file

	got, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}

	want := []Project{
		{Name: "backend", Path: filepath.Join(root, "backend")},
		{Name: "ecommerce", Path: filepath.Join(root, "ecommerce")},
		{Name: "worktree", Path: filepath.Join(root, "worktree")},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d projects %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("project %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDiscoverEmptyDir(t *testing.T) {
	got, err := Discover(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no projects, got %v", got)
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	if _, err := Discover(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected error for missing root")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
