package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile puts content at path inside the repo, creating directories.
func writeFile(t *testing.T, r *Repo, path, content string) {
	t.Helper()
	full := filepath.Join(r.Dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitAll stages everything and commits it.
func commitAll(t *testing.T, r *Repo) {
	t.Helper()
	if _, err := run(r.Dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(r.Dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base"); err != nil {
		t.Fatal(err)
	}
}

func changesByPath(t *testing.T, r *Repo) map[string]FileChange {
	t.Helper()
	got, err := Changes(r.Dir)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	byPath := map[string]FileChange{}
	for _, c := range got {
		if _, dup := byPath[c.Path]; dup {
			t.Errorf("Changes lists %q twice", c.Path)
		}
		byPath[c.Path] = c
	}
	return byPath
}

func TestChangesSeesModifiedStagedAndUntrackedFiles(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "tracked.txt", "one\ntwo\nthree\n")
	commitAll(t, r)

	writeFile(t, r, "tracked.txt", "one\nTWO CHANGED\nthree\n") // unstaged edit
	writeFile(t, r, "staged.txt", "s1\ns2\ns3\n")
	if _, err := run(r.Dir, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, r, "brandnew.txt", "alpha\nbeta\n") // untracked, git diff never sees this

	byPath := changesByPath(t, r)

	tracked, ok := byPath["tracked.txt"]
	if !ok {
		t.Fatal("tracked.txt missing from Changes")
	}
	if tracked.Kind != Modified {
		t.Errorf("tracked.txt Kind = %v, want Modified", tracked.Kind)
	}
	if tracked.Added != 1 || tracked.Deleted != 1 {
		t.Errorf("tracked.txt counts = +%d -%d, want +1 -1", tracked.Added, tracked.Deleted)
	}

	staged, ok := byPath["staged.txt"]
	if !ok {
		t.Fatal("staged.txt missing from Changes")
	}
	if staged.Kind != Added {
		t.Errorf("staged.txt Kind = %v, want Added", staged.Kind)
	}
	if staged.Added != 3 || staged.Deleted != 0 {
		t.Errorf("staged.txt counts = +%d -%d, want +3 -0", staged.Added, staged.Deleted)
	}

	fresh, ok := byPath["brandnew.txt"]
	if !ok {
		t.Fatal("brandnew.txt missing — untracked files are invisible to git diff and must come from status")
	}
	if fresh.Kind != Added {
		t.Errorf("brandnew.txt Kind = %v, want Added", fresh.Kind)
	}
	if fresh.Added != 2 {
		t.Errorf("brandnew.txt Added = %d, want 2 lines", fresh.Added)
	}
	if fresh.Binary {
		t.Error("brandnew.txt reported as binary")
	}
}

// The sole status record here is " D gone.txt": its first column is a space,
// which trimmed output would eat, shifting the path to "one.txt".
func TestChangesReportsADeletedFile(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "gone.txt", "a\nb\n")
	commitAll(t, r)
	if err := os.Remove(filepath.Join(r.Dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	got, err := Changes(r.Dir)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(got) != 1 || got[0].Path != "gone.txt" || got[0].Kind != Deleted {
		t.Fatalf("Changes = %+v, want one Deleted gone.txt", got)
	}
	if got[0].Added != 0 || got[0].Deleted != 2 {
		t.Errorf("gone.txt counts = +%d -%d, want +0 -2", got[0].Added, got[0].Deleted)
	}
}

// A new directory is where an agent's new files most often land. Without
// --untracked-files=all git status collapses it to a single "dir/" entry and
// the files inside never appear.
func TestChangesListsFilesInsideANewDirectory(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "newdir/inner.txt", "l1\nl2\nl3\n")

	byPath := changesByPath(t, r)
	if _, ok := byPath["newdir/"]; ok {
		t.Error("Changes lists the directory newdir/ instead of the file in it")
	}
	inner, ok := byPath["newdir/inner.txt"]
	if !ok {
		t.Fatalf("newdir/inner.txt missing from Changes: %v", byPath)
	}
	if inner.Kind != Added || inner.Added != 3 {
		t.Errorf("newdir/inner.txt = %+v, want Added with 3 lines", inner)
	}
}

// With -z, numstat prints a rename as "add\tdel\t" followed by the source
// and destination paths in two further fields. status lists the destination,
// so the counts must be keyed by it or a renamed file shows no numbers. The
// source survives as OldPath so the viewer can show the moved file's original
// content rather than treating it as new.
func TestChangesReportsBothPathsOfARename(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "old.txt", "one\ntwo\nthree\nfour\n")
	writeFile(t, r, "other.txt", "x\n")
	commitAll(t, r)
	if _, err := run(r.Dir, "mv", "old.txt", "new.txt"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, r, "new.txt", "one\nTWO\nthree\nfour\n")
	if _, err := run(r.Dir, "add", "new.txt"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, r, "other.txt", "x\ny\n")

	byPath := changesByPath(t, r)
	// The source path follows the rename record; parsed as a record of its
	// own it would show up as a bogus ".txt" entry.
	if len(byPath) != 2 {
		t.Errorf("Changes has %d entries, want new.txt and other.txt only: %v", len(byPath), byPath)
	}
	renamed, ok := byPath["new.txt"]
	if !ok {
		t.Fatalf("new.txt missing from Changes: %v", byPath)
	}
	if renamed.OldPath != "old.txt" {
		t.Errorf("new.txt OldPath = %q, want old.txt", renamed.OldPath)
	}
	if renamed.Kind != Modified {
		t.Errorf("new.txt Kind = %v, want Modified", renamed.Kind)
	}
	// The counts describe the edit made after the move, not the whole file.
	if renamed.Added != 1 || renamed.Deleted != 1 {
		t.Errorf("new.txt counts = +%d -%d, want +1 -1", renamed.Added, renamed.Deleted)
	}
	// The record after the rename must still parse as its own file.
	if other := byPath["other.txt"]; other.Added != 1 || other.Deleted != 0 || other.OldPath != "" {
		t.Errorf("other.txt = %+v, want +1 -0 and no OldPath", other)
	}
}

// numstat counts an unterminated last line; the untracked count must agree
// so a file reports the same number before and after it is staged.
func TestChangesCountsAnUnterminatedLastLineLikeGit(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "untracked.txt", "a\nb")
	writeFile(t, r, "staged.txt", "a\nb")
	if _, err := run(r.Dir, "add", "staged.txt"); err != nil {
		t.Fatal(err)
	}

	byPath := changesByPath(t, r)
	if byPath["staged.txt"].Added != 2 {
		t.Fatalf("staged.txt Added = %d, want git's 2 (the test's premise)", byPath["staged.txt"].Added)
	}
	if byPath["untracked.txt"].Added != 2 {
		t.Errorf("untracked.txt Added = %d, want 2 like the staged copy", byPath["untracked.txt"].Added)
	}
}

func TestChangesFlagsBinaryFiles(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "tracked.bin", "\x00\x01\x02\n\n")
	commitAll(t, r)
	writeFile(t, r, "tracked.bin", "\x00\x03\n\n\n")
	writeFile(t, r, "fresh.bin", "\x00\x00\n\n")

	byPath := changesByPath(t, r)
	for _, path := range []string{"tracked.bin", "fresh.bin"} {
		c, ok := byPath[path]
		if !ok {
			t.Fatalf("%s missing from Changes", path)
		}
		if !c.Binary || c.Added != 0 || c.Deleted != 0 {
			t.Errorf("%s = %+v, want Binary with zero counts", path, c)
		}
	}
}

func TestShowHEADReturnsCommittedContentAndReportsNewFiles(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "f.txt", "committed\n")
	commitAll(t, r)
	writeFile(t, r, "f.txt", "changed\n")

	content, ok, err := ShowHEAD(r.Dir, "f.txt")
	if err != nil {
		t.Fatalf("ShowHEAD: %v", err)
	}
	if !ok || content != "committed\n" {
		t.Errorf("ShowHEAD = %q, %v; want the committed content", content, ok)
	}

	if _, ok, err := ShowHEAD(r.Dir, "never-committed.txt"); err != nil || ok {
		t.Errorf("ShowHEAD on a file HEAD does not have = ok %v, err %v; want false, nil", ok, err)
	}
}

// The viewer aligns lines from the @@ headers alone, which only works when
// the diff carries no context lines.
func TestDiffHEADHasNoContextLines(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "f.txt", "one\ntwo\nthree\n")
	commitAll(t, r)
	writeFile(t, r, "f.txt", "one\nTWO\nthree\n")

	diff, err := DiffHEAD(r.Dir, "f.txt", "")
	if err != nil {
		t.Fatalf("DiffHEAD: %v", err)
	}
	if !strings.Contains(diff, "@@ -2 +2 @@") {
		t.Errorf("DiffHEAD lacks the single-line hunk header:\n%s", diff)
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, " ") {
			t.Errorf("DiffHEAD includes context line %q:\n%s", line, diff)
		}
	}
}

// git pairs the two sides of a rename only when both are in the pathspec.
// With the destination alone the diff claims the whole file is new, which
// would misalign every renamed file against the original content ShowHEAD
// returns for it.
func TestDiffHEADDescribesTheEditInAMovedFile(t *testing.T) {
	r := initRepo(t)
	writeFile(t, r, "old.txt", "one\ntwo\nthree\n")
	commitAll(t, r)
	if _, err := run(r.Dir, "mv", "old.txt", "new.txt"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, r, "new.txt", "one\nTWO\nthree\n")
	if _, err := run(r.Dir, "add", "new.txt"); err != nil {
		t.Fatal(err)
	}

	diff, err := DiffHEAD(r.Dir, "new.txt", "old.txt")
	if err != nil {
		t.Fatalf("DiffHEAD: %v", err)
	}
	if strings.Contains(diff, "@@ -0,0 +1,3 @@") {
		t.Errorf("DiffHEAD reports the moved file as a whole-file addition:\n%s", diff)
	}
	if !strings.Contains(diff, "@@ -2 +2 @@") {
		t.Errorf("DiffHEAD lacks the hunk for the one edited line:\n%s", diff)
	}
}
