package diff

import "testing"

func TestParseHunksReadsTheHeaders(t *testing.T) {
	// Produced by: git diff HEAD -U0 on a,b,c,d,e -> a,B,c,NEW,d
	in := "@@ -2 +2 @@ a\n@@ -3,0 +4 @@ c\n@@ -5 +5,0 @@ d\n"

	got := ParseHunks(in)

	want := []Hunk{
		{OldStart: 2, OldCount: 1, NewStart: 2, NewCount: 1},
		{OldStart: 3, OldCount: 0, NewStart: 4, NewCount: 1},
		{OldStart: 5, OldCount: 1, NewStart: 5, NewCount: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d hunks, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hunk %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseHunksIgnoresEverythingElse(t *testing.T) {
	in := "diff --git a/f.txt b/f.txt\nindex 1..2 100644\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-old\n+new\n"

	got := ParseHunks(in)

	if len(got) != 1 || got[0].OldStart != 1 || got[0].NewCount != 1 {
		t.Fatalf("got %+v, want the single @@ header", got)
	}
}

func TestAlignKeepsBothSidesInStep(t *testing.T) {
	old := []string{"a", "b", "c", "d", "e"}
	fresh := []string{"a", "B CHANGED", "c", "NEW LINE", "d"}
	hunks := []Hunk{
		{OldStart: 2, OldCount: 1, NewStart: 2, NewCount: 1},
		{OldStart: 3, OldCount: 0, NewStart: 4, NewCount: 1},
		{OldStart: 5, OldCount: 1, NewStart: 5, NewCount: 0},
	}

	rows := Align(old, fresh, hunks)

	want := []Row{
		{OldNum: 1, OldText: "a", NewNum: 1, NewText: "a"},
		{OldNum: 2, OldText: "b", NewNum: 2, NewText: "B CHANGED", Changed: true},
		{OldNum: 3, OldText: "c", NewNum: 3, NewText: "c"},
		{OldNum: 0, OldText: "", NewNum: 4, NewText: "NEW LINE", Changed: true},
		{OldNum: 4, OldText: "d", NewNum: 5, NewText: "d"},
		{OldNum: 5, OldText: "e", NewNum: 0, NewText: "", Changed: true},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d:\n%+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v\n      want %+v", i, rows[i], want[i])
		}
	}
}

func TestAlignWithNoHunksIsTheFileUnchanged(t *testing.T) {
	lines := []string{"x", "y"}

	rows := Align(lines, lines, nil)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for i, r := range rows {
		if r.Changed || r.OldNum != i+1 || r.NewNum != i+1 {
			t.Errorf("row %d = %+v, want an unchanged pair", i, r)
		}
	}
}

func TestAlignANewFileHasNoLeftSide(t *testing.T) {
	fresh := []string{"one", "two"}
	hunks := []Hunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 2}}

	rows := Align(nil, fresh, hunks)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}
	for i, r := range rows {
		if r.OldNum != 0 || r.OldText != "" || !r.Changed || r.NewNum != i+1 {
			t.Errorf("row %d = %+v, want a right-only added row", i, r)
		}
	}
}
