package diff

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAlignReproducesBothFilesAgainstRealGit is a property check, not an
// example: for many random edits it asks real git for the hunks, aligns, and
// requires that reading each column top to bottom gives back exactly the file
// it came from, and that every unchanged row shows the same text on both
// sides. Those two properties are the whole contract of the view.
func TestAlignReproducesBothFilesAgainstRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	rng := rand.New(rand.NewSource(7))

	for round := 0; round < 200; round++ {
		dir := t.TempDir()
		run := func(args ...string) string {
			t.Helper()
			cmd := exec.Command("git", append([]string{"-C", dir,
				"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
			out, err := cmd.CombinedOutput()
			if err != nil && args[0] != "diff" {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
			return string(out)
		}
		run("init", "-q")

		n := 1 + rng.Intn(12)
		var old []string
		for i := 1; i <= n; i++ {
			old = append(old, fmt.Sprintf("line %d", i))
		}
		write := func(lines []string) {
			body := ""
			if len(lines) > 0 {
				body = strings.Join(lines, "\n") + "\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write(old)
		run("add", "-A")
		run("commit", "-q", "-m", "base")

		// A handful of random edits: replace, insert, delete.
		fresh := append([]string(nil), old...)
		for e := 0; e < 1+rng.Intn(4); e++ {
			if len(fresh) == 0 {
				fresh = append(fresh, "inserted")
				continue
			}
			at := rng.Intn(len(fresh))
			switch rng.Intn(3) {
			case 0:
				fresh[at] = "CHANGED " + fresh[at]
			case 1:
				fresh = append(fresh[:at], append([]string{"NEW"}, fresh[at:]...)...)
			case 2:
				fresh = append(fresh[:at], fresh[at+1:]...)
			}
		}
		write(fresh)

		unified := run("diff", "HEAD", "-U0", "--", "f.txt")
		rows := Align(old, fresh, ParseHunks(unified))

		var gotOld, gotNew []string
		for _, r := range rows {
			if r.OldNum != 0 {
				gotOld = append(gotOld, r.OldText)
			}
			if r.NewNum != 0 {
				gotNew = append(gotNew, r.NewText)
			}
			if !r.Changed && r.OldText != r.NewText {
				t.Fatalf("round %d: unchanged row shows different text: %q vs %q\nhunks: %s",
					round, r.OldText, r.NewText, unified)
			}
		}
		if strings.Join(gotOld, "\n") != strings.Join(old, "\n") {
			t.Fatalf("round %d: left column is not the committed file\n got: %q\nwant: %q\nhunks: %s",
				round, gotOld, old, unified)
		}
		if strings.Join(gotNew, "\n") != strings.Join(fresh, "\n") {
			t.Fatalf("round %d: right column is not the working file\n got: %q\nwant: %q\nhunks: %s",
				round, gotNew, fresh, unified)
		}
	}
}
