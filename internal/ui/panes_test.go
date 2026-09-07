package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// twoPanes opens a terminal and splits it side by side with a second one.
func twoPanes(t *testing.T) (Model, *[]*fakeSession, *workspace.Project) {
	t.Helper()
	m, spawned := newTestModel(t)
	m = press(m, "enter", "alt+v", "enter")
	p := m.ws.Projects[0]
	if len(*spawned) != 2 || len(m.layouts[p].leaves()) != 2 {
		t.Fatalf("setup: spawned=%d leaves=%d", len(*spawned), len(m.layouts[p].leaves()))
	}
	return m, spawned, p
}

func TestAltVCreatesSideBySidePanes(t *testing.T) {
	m, spawned, p := twoPanes(t)
	rs := m.paneRects(p)
	a, b := rs[p.Terminals[0]], rs[p.Terminals[1]]
	area := m.mainArea()
	if a.y != b.y || a.h != area.h || b.h != area.h || a.w+b.w+1 != area.w || b.x != a.x+a.w+1 {
		t.Errorf("panes should sit side by side across the area %+v: a=%+v b=%+v", area, a, b)
	}
	for i, s := range *spawned {
		r := rs[p.Terminals[i]]
		if s.cols != r.w || s.rows != r.h-paneHeaderHeight {
			t.Errorf("terminal %d session %dx%d, want %dx%d", i+1, s.cols, s.rows, r.w, r.h-paneHeaderHeight)
		}
	}
	if p.ActiveTerminal() != p.Terminals[1] || m.focus != focusMain {
		t.Error("the new terminal should be focused")
	}
	view := m.View()
	for _, want := range []string{"SCREEN:/ws/backend:t1", "SCREEN:/ws/backend:t2", verticalDivider} {
		if !strings.Contains(view, want) {
			t.Errorf("both terminals should be visible with a divider, missing %q:\n%s", want, view)
		}
	}
	if lines := strings.Count(view, "\n") + 1; lines > m.height {
		t.Errorf("view has %d lines for a %d-row window", lines, m.height)
	}
}

func TestAltHCreatesStackedPanes(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "alt+h", "enter")
	p := m.ws.Projects[0]
	rs := m.paneRects(p)
	a, b := rs[p.Terminals[0]], rs[p.Terminals[1]]
	area := m.mainArea()
	if a.x != b.x || a.w != area.w || b.w != area.w || a.h+b.h+1 != area.h || b.y != a.y+a.h+1 {
		t.Errorf("panes should be stacked over the area %+v: a=%+v b=%+v", area, a, b)
	}
	if !strings.Contains(m.View(), horizontalDivider) {
		t.Error("stacked panes should show a horizontal divider")
	}
}

func TestSidebarVHOpenFormWithSplitDirection(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "esc", "v", "enter")
	p := m.ws.Projects[0]
	if root := m.layouts[p]; root == nil || root.dir != splitSide {
		t.Fatalf("v in the sidebar should split beside, got %+v", root)
	}
	m = press(m, "esc", "h", "enter")
	rs := m.paneRects(p)
	if len(rs) != 3 {
		t.Fatalf("expected three panes, got %d", len(rs))
	}
}

func TestAltNGrowsTheAutomaticGrid(t *testing.T) {
	m, _ := newTestModel(t) // 100x30: area is wide -> two terminals sit side by side
	m = press(m, "enter", "alt+n", "enter")
	p := m.ws.Projects[0]
	if m.layouts[p].dir != splitSide {
		t.Errorf("wide area should put two terminals side by side, got %v", m.layouts[p].dir)
	}
	m = press(m, "alt+n", "enter") // third: balanced 2+1, terminal 2 keeps the whole right column
	rs := m.paneRects(p)
	first, second, third := rs[p.Terminals[0]], rs[p.Terminals[1]], rs[p.Terminals[2]]
	if first.x != third.x || third.y <= first.y || second.h != m.mainArea().h {
		t.Errorf("expected 1 over 3 beside a full-height 2: %+v %+v %+v", first, second, third)
	}
}

func TestFocusMovesBetweenPanesWithAltArrows(t *testing.T) {
	m, spawned, p := twoPanes(t)
	m = press(m, "alt+left")
	if p.ActiveTerminal() != p.Terminals[0] {
		t.Fatal("alt+left should focus the left pane")
	}
	m = press(m, "alt+left") // nothing further left: stays
	if p.ActiveTerminal() != p.Terminals[0] {
		t.Error("focus should stay when there is no pane in that direction")
	}
	m = press(m, "alt+right")
	if p.ActiveTerminal() != p.Terminals[1] {
		t.Fatal("alt+right should focus the right pane")
	}
	m = press(m, "alt+h", "enter") // stack a third under the right pane
	m = press(m, "alt+up")
	if p.ActiveTerminal() != p.Terminals[1] {
		t.Error("alt+up should focus the pane above")
	}
	m = press(m, "alt+down")
	if p.ActiveTerminal() != p.Terminals[2] {
		t.Error("alt+down should focus the pane below")
	}
	if len(*spawned) != 3 {
		t.Error("focus movement must not spawn anything")
	}
	view := m.View()
	if !strings.Contains(view, "Terminal 3") || strings.Count(view, "SCREEN:") != 3 {
		t.Errorf("all three panes should be rendered:\n%s", view)
	}
}

func TestInputGoesToFocusedPaneOnly(t *testing.T) {
	m, spawned, _ := twoPanes(t)
	m = press(m, "x")
	m = press(m, "alt+left", "y")
	if got := strings.Join((*spawned)[1].text, ""); got != "x" {
		t.Errorf("right pane received %q, want \"x\"", got)
	}
	if got := strings.Join((*spawned)[0].text, ""); got != "y" {
		t.Errorf("left pane received %q, want \"y\"", got)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	if last := (*spawned)[0].keys[len((*spawned)[0].keys)-1]; last != (uv.KeyPressEvent{Code: 'c', Mod: uv.ModCtrl}) {
		t.Errorf("ctrl+c should reach the focused pane, got %+v", last)
	}
	if len((*spawned)[1].keys) != 0 {
		t.Error("unfocused pane must not receive keys")
	}
	// Tab still goes to the PTY, never to the layout.
	m = press(m, "tab")
	if last := (*spawned)[0].keys[len((*spawned)[0].keys)-1]; last.Code != uv.KeyTab {
		t.Error("tab must be forwarded to the terminal")
	}
}

func TestRenderingDoesNotSpawnOrDuplicateSessions(t *testing.T) {
	m, spawned, p := twoPanes(t)
	for i := 0; i < 5; i++ {
		_ = m.View()
	}
	if len(*spawned) != 2 {
		t.Fatalf("rendering spawned sessions: %d", len(*spawned))
	}
	seen := map[workspace.Session]bool{}
	for _, term := range m.layouts[p].leaves() {
		if seen[term.Session] {
			t.Error("two panes share one session")
		}
		seen[term.Session] = true
	}
	if len(seen) != len(p.Terminals) {
		t.Error("each pane must show exactly one existing terminal session")
	}
}

func TestClosingFocusedPaneLeavesOthersRunning(t *testing.T) {
	m, spawned, p := twoPanes(t)
	_ = (*spawned)[1].Close() // the focused (right) shell exits
	next, _ := m.Update(terminalExitedMsg{id: p.Terminals[1].ID})
	m = next.(Model)

	if !(*spawned)[1].closed || (*spawned)[0].closed {
		t.Fatal("only the exited terminal should be closed")
	}
	if len(m.layouts[p].leaves()) != 1 || m.layouts[p].leaves()[0] != p.Terminals[0] {
		t.Errorf("pane should be removed, leaving the other: %v", m.layouts[p].leaves())
	}
	area := m.mainArea()
	if s := (*spawned)[0]; s.cols != area.w || s.rows != area.h-paneHeaderHeight {
		t.Errorf("remaining pane should fill the area again, got %dx%d", s.cols, s.rows)
	}
	if m.focus != focusMain || p.ActiveTerminal() != p.Terminals[0] {
		t.Error("focus should move to the remaining terminal")
	}
	m = press(m, "z")
	if strings.Join((*spawned)[0].text, "") != "z" {
		t.Error("remaining terminal should still accept input")
	}
}

func TestResizeGrowsAndShrinksFocusedPane(t *testing.T) {
	m, spawned, p := twoPanes(t)
	before := m.paneRects(p)[p.Terminals[1]].w
	m = press(m, "alt+=")
	after := m.paneRects(p)[p.Terminals[1]].w
	if after <= before {
		t.Fatalf("alt+= should widen the focused pane: %d -> %d", before, after)
	}
	if s := (*spawned)[1]; s.cols != after {
		t.Errorf("session should be resized with its pane: %d vs %d", s.cols, after)
	}
	if s := (*spawned)[0]; s.cols != m.mainArea().w-after-1 {
		t.Errorf("the other session should shrink to %d, got %d", m.mainArea().w-after-1, s.cols)
	}
	m = press(m, "alt+-", "alt+-")
	if w := m.paneRects(p)[p.Terminals[1]].w; w >= before {
		t.Errorf("alt+- should shrink the pane below %d, got %d", before, w)
	}
	for i := 0; i < 20; i++ {
		m = press(m, "alt+-")
	}
	if w := m.paneRects(p)[p.Terminals[1]].w; w < minPaneCols {
		t.Errorf("pane must not shrink below %d cols, got %d", minPaneCols, w)
	}
}

func TestWindowResizeRecomputesPanes(t *testing.T) {
	m, spawned, p := twoPanes(t)
	for _, size := range [][2]int{{80, 24}, {120, 30}, {200, 60}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		area := m.mainArea()
		rs := m.paneRects(p)
		total := 0
		for i, s := range *spawned {
			r := rs[p.Terminals[i]]
			if s.cols != r.w || s.rows != r.h-paneHeaderHeight {
				t.Errorf("%dx%d: terminal %d session %dx%d, want %dx%d", size[0], size[1], i+1, s.cols, s.rows, r.w, r.h-paneHeaderHeight)
			}
			if r.x+r.w > area.w || r.y+r.h > area.h {
				t.Errorf("%dx%d: pane %+v exceeds area %+v", size[0], size[1], r, area)
			}
			total += r.w
		}
		if total+1 != area.w {
			t.Errorf("%dx%d: panes + divider should tile the width: %d vs %d", size[0], size[1], total+1, area.w)
		}
		view := m.View()
		if lines := strings.Count(view, "\n") + 1; lines != size[1] {
			t.Errorf("%dx%d: view has %d lines", size[0], size[1], lines)
		}
		for _, line := range strings.Split(view, "\n") {
			if w := len([]rune(stripANSI(line))); w > size[0] {
				t.Errorf("%dx%d: a line is %d cells wide", size[0], size[1], w)
				break
			}
		}
	}
}

func TestAltJKCycleVisibleTerminalsOnly(t *testing.T) {
	m, spawned, p := twoPanes(t)
	// A third terminal squeezed into a pane too small to split takes over that pane.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 9}) // area 30x6: too small to split either way
	m = next.(Model)
	m = press(m, "alt+n", "enter")
	if len(m.layouts[p].leaves()) != 2 || !m.layouts[p].contains(p.Terminals[2]) || m.layouts[p].contains(p.Terminals[1]) {
		t.Fatalf("new terminal should replace the focused pane when nothing can be split: %v", m.layouts[p].leaves())
	}
	if (*spawned)[1].closed {
		t.Fatal("hidden terminal must keep running")
	}
	m = press(m, "alt+k") // back among the visible panes: 3 -> 1; terminal 2 stays hidden
	if p.ActiveTerminal() != p.Terminals[0] || m.layouts[p].contains(p.Terminals[1]) {
		t.Errorf("alt+k must move to the other visible pane, not reveal the hidden one: active=%d visible=%v", p.ActiveTerminal().Num, m.layouts[p].leaves())
	}
	m = press(m, "alt+j") // 1 -> 3
	if p.ActiveTerminal() != p.Terminals[2] || m.layouts[p].contains(p.Terminals[1]) {
		t.Errorf("alt+j cycles visible terminals only: active=%d", p.ActiveTerminal().Num)
	}
	m = press(m, "alt+j", "alt+k", "alt+k") // 3 -> 1 -> 3 -> 1
	if p.ActiveTerminal() != p.Terminals[0] || len(m.layouts[p].leaves()) != 2 || m.layouts[p].contains(p.Terminals[1]) {
		t.Errorf("hidden terminal must never be swapped in by navigation: %v", m.layouts[p].leaves())
	}
	if !strings.Contains(m.View(), " Terminal 1 ") { // panes are ~14 cols wide here: check the header
		t.Errorf("focused visible terminal should be rendered:\n%s", m.View())
	}
	// The manager is the explicit way back.
	m = press(m, "esc", "m", "j", "enter")
	if p.ActiveTerminal() != p.Terminals[1] || !m.layouts[p].contains(p.Terminals[1]) || len(*spawned) != 3 {
		t.Errorf("manager enter should show terminal 2: active=%d visible=%v", p.ActiveTerminal().Num, m.layouts[p].leaves())
	}
}

func TestPaneHeadersShowNameAndBranch(t *testing.T) {
	m, _, _ := gitModel(t)
	m = makeTerminal(t, m, "n", "Login", "develop")
	m = makeTerminal(t, m, "alt+v", "Plain", "")
	view := m.View()
	for _, want := range []string{"Terminal 1 · ⎇ develop", "Terminal 2 · main"} {
		if !strings.Contains(view, want) {
			t.Errorf("pane header missing %q:\n%s", want, view)
		}
	}
	if len(m.ws.Projects[0].Terminals) != 2 || m.ws.Projects[0].Terminals[0].Worktree == nil {
		t.Error("worktree terminal creation must keep working in split mode")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestFramesFillTheWindowExactly(t *testing.T) {
	m, _, _ := twoPanes(t)
	for _, size := range [][2]int{{80, 24}, {120, 30}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		for _, view := range []string{m.View(), press(m, "esc", "s").View()} {
			lines := strings.Split(view, "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
			}
			for i, line := range lines[:len(lines)-1] { // every body line spans the full width
				if w := len([]rune(stripANSI(line))); w != size[0] {
					t.Errorf("%dx%d: line %d is %d cells wide: %q", size[0], size[1], i, w, stripANSI(line))
					break
				}
			}
		}
	}
}
