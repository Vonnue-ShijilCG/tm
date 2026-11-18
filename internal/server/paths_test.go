package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsForIsStableAndPrivate(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	root := t.TempDir()
	a, err := PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("same root gave %+v then %+v", a, b)
	}

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	c, err := PathsFor(link)
	if err != nil {
		t.Fatal(err)
	}
	if c.Socket != a.Socket {
		t.Fatalf("symlink gave socket %q, want %q", c.Socket, a.Socket)
	}

	if a.Dir != filepath.Join(state, "tm") {
		t.Fatalf("Dir = %q, want under XDG_STATE_HOME", a.Dir)
	}
	assertPrivateDir(t, a.Dir)
	stem := strings.TrimSuffix(filepath.Base(a.Socket), ".sock")
	if len(stem) != 16 {
		t.Fatalf("stem %q is not 16 hex chars", stem)
	}
	want := Paths{
		Dir:    a.Dir,
		Socket: filepath.Join(a.Dir, stem+".sock"),
		Lock:   filepath.Join(a.Dir, stem+".lock"),
		Exited: filepath.Join(a.Dir, stem+".exited"),
		Log:    filepath.Join(a.Dir, stem+".log"),
	}
	if a != want {
		t.Fatalf("paths = %+v, want %+v", a, want)
	}

	other, err := PathsFor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other.Socket == a.Socket {
		t.Fatal("different roots share a socket")
	}

	// A state dir deep enough that the socket would not fit in sun_path.
	long := filepath.Join(state, strings.Repeat("d", 100))
	t.Setenv("XDG_STATE_HOME", long)
	f, err := PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := fmt.Sprintf("/tmp/tm-%d", os.Getuid())
	if f.Dir != wantDir {
		t.Fatalf("long path Dir = %q, want %q", f.Dir, wantDir)
	}
	if len(f.Socket) > 107 {
		t.Fatalf("fallback socket %q still too long", f.Socket)
	}
	if filepath.Base(f.Socket) != filepath.Base(a.Socket) {
		t.Fatalf("fallback changed the stem: %q vs %q", f.Socket, a.Socket)
	}
	assertPrivateDir(t, f.Dir)
	if _, err := os.Stat(long); err == nil {
		t.Fatal("the too-long state dir was created anyway")
	}
}

func TestPathsForRefusesASharedFallbackDir(t *testing.T) {
	base := t.TempDir()
	old := fallbackBase
	fallbackBase = base
	t.Cleanup(func() { fallbackBase = old })
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), strings.Repeat("d", 100)))

	dir := filepath.Join(base, fmt.Sprintf("tm-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PathsFor(t.TempDir()); err == nil {
		t.Fatal("a 0755 fallback dir was accepted")
	}

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := PathsFor(t.TempDir()); err == nil {
		t.Fatal("a symlinked fallback dir was accepted")
	}
}

func assertPrivateDir(t *testing.T, dir string) {
	t.Helper()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("%s mode = %v, want drwx------", dir, info.Mode())
	}
}
