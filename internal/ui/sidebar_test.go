package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTheSelectedProjectRowFillsTheSidebar(t *testing.T) {
	m, _ := newTestModel(t)

	rows := strings.Split(m.viewSidebar(), "\n")
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

	out := m.viewSidebar()

	for _, row := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(row); w > sidebarWidth-sidebarStyle.GetHorizontalFrameSize() {
			t.Errorf("row %q is %d cells, wider than the sidebar", row, w)
		}
	}
	// The name survives the wrap, even though it is split across rows.
	if !strings.Contains(strings.ReplaceAll(ansi.Strip(out), "\n", ""), "WEB-HB-Component-Library") {
		t.Errorf("a wrapped name should still be readable:\n%s", out)
	}
}
