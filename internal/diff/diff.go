// Package diff aligns the committed and working versions of a file so both can
// be shown in full, side by side, with each line facing its counterpart.
//
// It works from the @@ headers of a diff produced with -U0, which carry the
// whole alignment on their own: the diff body is never read.
package diff

import (
	"strconv"
	"strings"
)

// Hunk is one changed region. OldCount lines starting at OldStart in the
// committed file became NewCount lines starting at NewStart in the working
// file. When a count is zero its start names the line the change sits AFTER,
// not a line that changed: "@@ -3,0 +4 @@" inserts after old line 3.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
}

// Row is one line of the aligned view. A side is padding — present only to
// keep the two columns in step — when its number is 0.
type Row struct {
	OldNum  int
	OldText string
	NewNum  int
	NewText string
	Changed bool
}

// ParseHunks reads the @@ headers of a unified diff and ignores everything
// else, so it does not care whether the body has context lines.
func ParseHunks(unified string) []Hunk {
	var hunks []Hunk
	for _, line := range strings.Split(unified, "\n") {
		if !strings.HasPrefix(line, "@@ ") {
			continue
		}
		end := strings.Index(line[3:], " @@")
		if end < 0 {
			continue
		}
		parts := strings.Fields(line[3 : 3+end])
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "-") || !strings.HasPrefix(parts[1], "+") {
			continue
		}
		oldStart, oldCount := parseRange(parts[0][1:])
		newStart, newCount := parseRange(parts[1][1:])
		hunks = append(hunks, Hunk{oldStart, oldCount, newStart, newCount})
	}
	return hunks
}

// parseRange reads "start" or "start,count"; a missing count means one line.
func parseRange(s string) (start, count int) {
	count = 1
	if comma := strings.IndexByte(s, ','); comma >= 0 {
		count, _ = strconv.Atoi(s[comma+1:])
		s = s[:comma]
	}
	start, _ = strconv.Atoi(s)
	return start, count
}

// Align walks both complete files, emitting unchanged lines as pairs and each
// hunk as its own rows, padding whichever side is shorter so line numbers on
// the two sides never drift apart.
func Align(old, fresh []string, hunks []Hunk) []Row {
	var rows []Row
	o, n := 0, 0 // 0-based cursors into old and fresh

	for _, h := range hunks {
		// Where the unchanged run before this hunk ends. A zero count means the
		// start names the line before the change, so that line is unchanged.
		stop := h.OldStart - 1
		if h.OldCount == 0 {
			stop = h.OldStart
		}
		for o < stop && o < len(old) && n < len(fresh) {
			rows = append(rows, Row{OldNum: o + 1, OldText: old[o], NewNum: n + 1, NewText: fresh[n]})
			o++
			n++
		}
		for i := 0; i < max(h.OldCount, h.NewCount); i++ {
			r := Row{Changed: true}
			if i < h.OldCount && o < len(old) {
				r.OldNum, r.OldText = o+1, old[o]
				o++
			}
			if i < h.NewCount && n < len(fresh) {
				r.NewNum, r.NewText = n+1, fresh[n]
				n++
			}
			// Both sides ran out: the file changed after the diff was taken and the
			// hunk names lines that no longer exist. A row empty on both sides
			// would be padding for nothing, so stop rather than draw one.
			if r.OldNum == 0 && r.NewNum == 0 {
				break
			}
			rows = append(rows, r)
		}
	}
	for o < len(old) && n < len(fresh) {
		rows = append(rows, Row{OldNum: o + 1, OldText: old[o], NewNum: n + 1, NewText: fresh[n]})
		o++
		n++
	}
	return rows
}
