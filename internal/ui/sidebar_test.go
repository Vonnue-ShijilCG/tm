package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTheSelectedProjectRowFillsTheSidebar(t *testing.T) {
	m, _ := newTestModel(t)

	rows := strings.Split(m.viewSidebar(m.height-statusHeight-sidebarStyle.GetVerticalFrameSize()), "\n")
	found := false
	for _, row := range rows {
		if !strings.Contains(row, m.ws.Projects[m.cursor].Name) {
			continue
		}
		found = true
		if w := ansi.StringWidth(row); w != sidebarWidth-sidebarStyle.GetHorizontalFrameSize() {
			t.Errorf("selected row is %d cells wide, want the sidebar's full inner width %d",
				w, sidebarWidth-sidebarStyle.GetHorizontalFrameSize())
		}
	}
	if !found {
		t.Fatal("the cursor's project was not rendered at all")
	}
}

func TestALongProjectNameWrapsRatherThanRunningOff(t *testing.T) {
	m, _ := newTestModel(t)
	long := "WEB-HB-Component-Library-With-A-Very-Long-Name"
	m.ws.Projects[0].Name = long

	out := m.viewSidebar(m.height - statusHeight - sidebarStyle.GetVerticalFrameSize())

	for _, row := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(row); w > sidebarWidth-sidebarStyle.GetHorizontalFrameSize() {
			t.Errorf("row %q is %d cells, wider than the sidebar", row, w)
		}
	}
	// The name survives the wrap, even though it is split across rows and
	// padded out to the sidebar's width: a project name has no internal
	// whitespace, so squashing it out on both sides is an exact comparison,
	// not a loose one.
	squash := func(s string) string { return strings.Join(strings.Fields(ansi.Strip(s)), "") }
	if !strings.Contains(squash(out), squash(long)) {
		t.Errorf("a wrapped name should still read back as one name:\n%s", out)
	}
}

// TestASelectedWrappedProjectIsHighlightedOnEveryLine guards the property the
// previous (wrong) fix for the test above silently dropped: a cursor-selected
// row must read as one solid bar down every one of its wrapped lines, not
// just the last — otherwise the first lines of a long, selected name would
// carry no highlight at all.
func TestASelectedWrappedProjectIsHighlightedOnEveryLine(t *testing.T) {
	m, _ := newTestModel(t)
	long := "WEB-HB-Component-Library-With-A-Very-Long-Name"
	m.ws.Projects[0].Name = long // project 0 is the cursor row by default

	inner := sidebarWidth - sidebarStyle.GetHorizontalFrameSize()
	wantLines := len(strings.Split(ansi.Wrap(long, max(inner-2, 1), ""), "\n"))
	if wantLines < 2 {
		t.Fatalf("setup: the name did not wrap at all (%d lines)", wantLines)
	}

	rows := strings.Split(m.viewSidebar(m.height-statusHeight-sidebarStyle.GetVerticalFrameSize()), "\n")
	nameRows := rows[2 : 2+wantLines] // "Projects", a blank line, then the wrapped project

	for _, row := range nameRows {
		if w := ansi.StringWidth(row); w != inner {
			t.Errorf("wrapped row %q is %d cells, want the sidebar's full width %d on every line, not just the last", row, w, inner)
		}
	}
}
