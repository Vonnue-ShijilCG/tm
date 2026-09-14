package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// gridModel opens n plain terminals in the first project with enter and
// alt+n, the automatic paths, at the given window size.
func gridModel(t *testing.T, n, width, height int) (Model, *[]*fakeSession, *workspace.Project) {
	t.Helper()
	m, spawned := newTestModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	m = press(m, "enter")
	for i := 1; i < n; i++ {
		m = press(m, "alt+n", "enter")
	}
	p := m.ws.Projects[0]
	if len(*spawned) != n || len(p.Terminals) != n {
		t.Fatalf("setup: spawned=%d running=%d want %d", len(*spawned), len(p.Terminals), n)
	}
	return m, spawned, p
}

// checkTiling verifies the visible panes of p are valid, at least the
// minimum size, inside the area, non-overlapping and free of duplicates.
func checkTiling(t *testing.T, m Model, p *workspace.Project) {
	t.Helper()
	area := m.mainArea()
	leaves := m.layouts[p].leaves()
	seen := map[*workspace.Terminal]bool{}
	for _, l := range leaves {
		if seen[l] {
			t.Errorf("terminal %d appears twice in the tree", l.Num)
		}
		seen[l] = true
	}
	rs := m.paneRects(p)
	for term, r := range rs {
		if r.w < minPaneCols || r.h < minPaneRows {
			t.Errorf("pane %d below minimum: %+v", term.Num, r)
		}
		if r.x < 0 || r.y < 0 || r.x+r.w > area.w || r.y+r.h > area.h {
			t.Errorf("pane %d outside area %+v: %+v", term.Num, area, r)
		}
		for other, s := range rs {
			if term != other && spans(r.x, r.w, s.x, s.w) && spans(r.y, r.h, s.y, s.h) {
				t.Errorf("panes %d and %d overlap: %+v %+v", term.Num, other.Num, r, s)
			}
		}
	}
	for _, term := range p.Terminals { // nothing disappears: visible, or hidden but running
		if _, ok := rs[term]; !ok && term.Session.(*fakeSession).closed {
			t.Errorf("terminal %d is neither visible nor running", term.Num)
		}
	}
}

// shape describes the grid as column heights, e.g. "2,2,1".
func shape(root *pane) string {
	g, ok := gridOf(root)
	if !ok {
		return "not a grid"
	}
	var parts []string
	for _, col := range g {
		parts = append(parts, fmt.Sprint(len(col)))
	}
	return strings.Join(parts, ",")
}

func TestAutomaticGridGeometryOneToSix(t *testing.T) {
	want := map[int]string{1: "1", 2: "1,1", 3: "2,1", 4: "2,2", 5: "2,2,1", 6: "2,2,2"}
	for n := 1; n <= 6; n++ {
		m, _, p := gridModel(t, n, 100, 30) // area 70x26, a typical wide-ish window
		root := m.layouts[p]
		if got := shape(root); got != want[n] {
			t.Errorf("%d terminals: shape %s, want %s", n, got, want[n])
		}
		if !automatic(root) {
			t.Errorf("%d terminals: layout built by tm must be recognised as automatic", n)
		}
		if len(root.leaves()) != n {
			t.Errorf("%d terminals: %d visible", n, len(root.leaves()))
		}
		checkTiling(t, m, p)
		area := m.mainArea()
		rs := m.paneRects(p)
		g, _ := gridOf(root)
		// Columns share the width within two cells: one systematic column
		// from the chain's share — each split rounds its first child's share
		// of the width left after a single gap, so the first column is
		// usually the widest and the last pays for it (k+1, k, k-1 with
		// three columns) — plus one of rounding. Each column's panes share
		// its height evenly; every column spans the full height.
		colW := rs[g[0][0]].w
		for _, col := range g {
			top, bottom := rs[col[0]], rs[col[len(col)-1]]
			if top.y != 0 || bottom.y+bottom.h != area.h {
				t.Errorf("%d terminals: column does not span the height: %+v .. %+v", n, top, bottom)
			}
			if d := rs[col[0]].w - colW; d < -2 || d > 2 {
				t.Errorf("%d terminals: column widths differ: %d vs %d", n, rs[col[0]].w, colW)
			}
			for _, term := range col {
				if d := rs[term].h*len(col) - area.h; d < -len(col) || d > len(col) {
					t.Errorf("%d terminals: pane %d height %d not ~1/%d of %d", n, term.Num, rs[term].h, len(col), area.h)
				}
				if rs[term].w != rs[col[0]].w {
					t.Errorf("%d terminals: panes in one column differ in width", n)
				}
			}
		}
		switch n {
		case 1:
			if rs[p.Terminals[0]] != area {
				t.Errorf("one terminal should fill the area: %+v vs %+v", rs[p.Terminals[0]], area)
			}
		case 2:
			a, b := rs[p.Terminals[0]], rs[p.Terminals[1]]
			if a.h != area.h || b.h != area.h || a.w+b.w+paneGap != area.w || a.w-b.w > 1 || b.w-a.w > 1 {
				t.Errorf("two terminals should split the width evenly: %+v %+v", a, b)
			}
		case 3:
			if rs[p.Terminals[1]].h != area.h || rs[p.Terminals[0]].x != rs[p.Terminals[2]].x {
				t.Errorf("2+1: terminal 2 keeps the right column, 3 goes under 1: %+v", rs)
			}
		case 4:
			if rs[p.Terminals[0]].y != rs[p.Terminals[1]].y || rs[p.Terminals[2]].y != rs[p.Terminals[3]].y || rs[p.Terminals[0]].x != rs[p.Terminals[2]].x {
				t.Errorf("2x2 rows wrong: %+v", rs)
			}
		case 5:
			if rs[p.Terminals[2]].h != area.h || rs[p.Terminals[3]].x != rs[p.Terminals[0]].x || rs[p.Terminals[4]].x != rs[p.Terminals[1]].x {
				t.Errorf("3+2: terminal 3 takes the right column, 4 and 5 sit under 1 and 2: %+v", rs)
			}
		case 6:
			for i := 0; i < 3; i++ {
				if rs[p.Terminals[i]].x != rs[p.Terminals[i+3]].x || rs[p.Terminals[i]].y != 0 || rs[p.Terminals[i+3]].y == 0 {
					t.Errorf("3x2: %d over %d expected: %+v %+v", i+1, i+4, rs[p.Terminals[i]], rs[p.Terminals[i+3]])
				}
			}
		}
	}
}

func TestGridOrientationFollowsWindowShape(t *testing.T) {
	cases := []struct {
		name          string
		n, w, h       int
		shape         string
		minCols, rows int
	}{
		{"tall: two stack", 2, 60, 60, "2", 1, 2},
		{"tall: four is still 2x2", 4, 60, 60, "2,2", 2, 2},
		{"tall: six goes 2 columns x 3 rows", 6, 60, 60, "3,3", 2, 3},
		{"wide and short: six goes 3 columns x 2 rows", 6, 200, 14, "2,2,2", 3, 2},
		{"wide and short: five", 5, 200, 14, "2,2,1", 3, 2},
		{"wide: three", 3, 200, 30, "2,1", 2, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _, p := gridModel(t, c.n, c.w, c.h)
			if got := shape(m.layouts[p]); got != c.shape {
				t.Errorf("shape %s, want %s (area %+v)", got, c.shape, m.mainArea())
			}
			checkTiling(t, m, p)
			if len(m.layouts[p].leaves()) != c.n {
				t.Errorf("%d visible of %d", len(m.layouts[p].leaves()), c.n)
			}
		})
	}
}

// TestChooseColsCountsTheGapBetweenColumns: chooseCols judges whether a
// column count fits by what rects will actually give the grid, gaps and
// the chain's uneven share included. One column short of two cards and the
// gap, two columns cannot hold cards even though their shape would score
// better than one tall column's; one column more and they can. Three
// columns are checked either side of the width where they start to fit.
// That width moves with the gap — a non-zero gap makes the chain divide
// unevenly, which costs the narrowest column a cell — so the test finds it
// rather than baking it in. A height of 17 lets two columns fit either way,
// so the choice is between fitting counts and not a tie of unfit ones.
func TestChooseColsCountsTheGapBetweenColumns(t *testing.T) {
	if got := chooseCols(terms(2), rect{0, 0, 2*minPaneCols + paneGap - 1, 11}); got != 1 {
		t.Errorf("one column short of two cards and a gap: chooseCols = %d, want 1", got)
	}
	if got := chooseCols(terms(2), rect{0, 0, 2*minPaneCols + paneGap, 11}); got != 2 {
		t.Errorf("exactly two cards and a gap: chooseCols = %d, want 2", got)
	}
	fit := 0 // the narrowest width whose three columns all clear the minimum
	for w := 2 * minPaneCols; w < 4*minPaneCols && fit == 0; w++ {
		if gridFor(terms(5), 3).tree().fits(rect{0, 0, w, 17}) {
			fit = w
		}
	}
	if fit == 0 {
		t.Fatal("three columns never fit: the search bounds are wrong")
	}
	if got := chooseCols(terms(5), rect{0, 0, fit - 1, 17}); got == 3 {
		t.Errorf("at %d the chain leaves a column under the minimum: chooseCols must not pick 3", fit-1)
	}
	if got := chooseCols(terms(5), rect{0, 0, fit, 17}); got != 3 {
		t.Errorf("at %d three columns fit and are the squarer shape: chooseCols = %d, want 3", fit, got)
	}
}

func TestGridOnTinyWindowFallsBackWithoutLosingTerminals(t *testing.T) {
	// height raised from 30 (area 20x27) to 40 (area 20x37): with cards'
	// card-aware minPaneRows, 30 no longer leaves room to split every pane
	// without swapping one out, which starved alt+j of a terminal it should
	// still reach; 40 keeps the "too narrow for two columns" intent (width
	// is still 20, well under 2*minPaneCols+paneGap) without also being too short.
	m, spawned, p := gridModel(t, 4, 50, 40) // area 20x37: too narrow for two columns
	checkTiling(t, m, p)
	if len(p.Terminals) != 4 {
		t.Fatalf("all four must keep running: %d", len(p.Terminals))
	}
	visible := len(m.layouts[p].leaves())
	if visible == 0 || visible > 4 {
		t.Fatalf("visible = %d", visible)
	}
	if !m.layouts[p].contains(p.ActiveTerminal()) {
		t.Error("the focused terminal must be visible")
	}
	// Hidden terminals stay reachable, and every step keeps a valid tiling.
	seen := map[*workspace.Terminal]bool{}
	for i := 0; i < 4; i++ {
		seen[p.ActiveTerminal()] = true
		m = press(m, "alt+j")
		checkTiling(t, m, p)
	}
	if len(seen) != 4 {
		t.Errorf("alt+j should reach every terminal, reached %d", len(seen))
	}
	// A 10x5 window (a one-cell area) must not crash or go negative.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 10, Height: 5})
	m = next.(Model)
	m = press(m, "alt+n", "enter")
	for _, r := range m.paneRects(p) {
		if r.w < 0 || r.h < 0 {
			t.Errorf("negative rect %+v", r)
		}
	}
	_ = m.View()
	for _, s := range *spawned {
		if s.cols < 1 || s.rows < 1 {
			t.Errorf("session resized to %dx%d", s.cols, s.rows)
		}
	}
}

func TestAutomaticDetectionIsStructural(t *testing.T) {
	ts := terms(8)
	area := rect{0, 0, 70, 26}
	for n := 1; n <= 8; n++ {
		root := gridFor(ts[:n], chooseCols(ts[:n], area)).tree()
		if !automatic(root) {
			t.Errorf("generated grid for %d must be automatic", n)
		}
		g, _ := gridOf(root)
		if got := g.rowMajor(); len(got) != n || got[0] != ts[0] || got[n-1] != ts[n-1] {
			t.Errorf("row-major order of %d lost: %v", n, got)
		}
	}
	// Same picture, different tree: a left-nested pair of splits is not canonical.
	left := &pane{dir: splitSide, ratio: 2.0 / 3, a: &pane{dir: splitSide, ratio: 0.5, a: leaf(ts[0]), b: leaf(ts[1])}, b: leaf(ts[2])}
	if automatic(left) {
		t.Error("left-nested chain must not count as automatic")
	}
	// A canonical shape with a moved divider is manual.
	moved := gridFor(ts[:2], 2).tree()
	moved.resize(ts[0], resizeStep)
	if automatic(moved) {
		t.Error("resized grid must be manual")
	}
	// An extra split beside the second pane: side(1, side(2, 3)) has ratios
	// 0.5/0.5 where a three-column grid needs 1/3 then 1/2.
	split := gridFor(ts[:2], 2).tree().insert(ts[1], ts[2], splitSide)
	if automatic(split) {
		t.Error("alt+v inside a grid must make it manual")
	}
	// Four columns for four terminals is not an allowed grid (2x2 is).
	if automatic(gridFor(ts[:4], 4).tree()) {
		t.Error("1x4 strip is not a balanced grid for four terminals")
	}
	// Ragged in either order is fine (closing a terminal leaves these).
	if !automatic((grid{{ts[0]}, {ts[1], ts[2]}}).tree()) || !automatic((grid{{ts[0], ts[1]}, {ts[2]}}).tree()) {
		t.Error("columns within one of each other are balanced")
	}
	if automatic((grid{{ts[0], ts[1], ts[2]}, {ts[3]}}).tree()) {
		t.Error("columns differing by two are not balanced")
	}
	if automatic(nil) {
		t.Error("nil is not a layout")
	}
	// Float noise from the state file is tolerated.
	noisy := gridFor(ts[:3], 2).tree()
	noisy.ratio, noisy.a.ratio = 0.5000001, 0.4999999
	if !automatic(noisy) {
		t.Error("tiny ratio noise must not break detection")
	}
}

func TestManualSplitMakesLayoutManualAndAltNSplitsFocusedPane(t *testing.T) {
	m, _, p := gridModel(t, 2, 100, 30)
	m = press(m, "alt+v", "enter") // explicit split beside terminal 2: manual from now on
	root := m.layouts[p]
	if automatic(root) {
		t.Fatal("alt+v must make the layout manual")
	}
	if root.dir != splitSide || !root.a.isLeaf() || root.b.dir != splitSide {
		t.Fatalf("expected 1 | (2 | 3), got %+v", root)
	}
	m = press(m, "alt+n", "enter") // automatic key in a manual layout: split the focused pane (3), keep the rest
	root = m.layouts[p]
	if root.dir != splitSide || !root.a.isLeaf() || root.a.term != p.Terminals[0] || root.b.dir != splitSide || !root.b.a.isLeaf() || root.b.a.term != p.Terminals[1] {
		t.Errorf("manual structure must be kept, got %+v", root)
	}
	if inner := root.b.b; inner.isLeaf() || !inner.contains(p.Terminals[2]) || !inner.contains(p.Terminals[3]) {
		t.Errorf("terminal 4 should split terminal 3's pane, got %+v", inner)
	}
	checkTiling(t, m, p)
	if len(root.leaves()) != 4 {
		t.Errorf("%d visible", len(root.leaves()))
	}
}

func TestSidebarSplitKeysAreManualToo(t *testing.T) {
	m, _, p := gridModel(t, 2, 100, 30)
	m = press(m, "alt+s", "v", "enter") // sidebar v: beside the focused pane -> 1 | (2 | 3)
	if automatic(m.layouts[p]) {
		t.Error("sidebar v is a manual split")
	}
	checkTiling(t, m, p)

	m, _, p = gridModel(t, 4, 100, 30)
	m = press(m, "alt+s", "h", "enter") // sidebar h below terminal 4 -> a column 2 / (4 / 5) with 0.5 ratios
	if automatic(m.layouts[p]) {
		t.Error("sidebar h is a manual split")
	}
	checkTiling(t, m, p)
	if len(p.Terminals) != 5 {
		t.Errorf("running %d", len(p.Terminals))
	}
	// alt+h on a two-pane grid gives 1 / 3 beside 2, which is exactly the
	// balanced three-pane grid: indistinguishable, and treated as automatic.
	m, _, p = gridModel(t, 2, 100, 30)
	m = press(m, "alt+left", "alt+h", "enter")
	if !automatic(m.layouts[p]) || shape(m.layouts[p]) != "2,1" {
		t.Errorf("a manual split that equals the grid counts as automatic: %s", shape(m.layouts[p]))
	}
}

func TestDividerResizeMakesLayoutManual(t *testing.T) {
	m, _, p := gridModel(t, 2, 100, 30)
	m = press(m, "alt+=") // terminal 2 grows: ratio 0.4
	root := m.layouts[p]
	if automatic(root) || root.ratio != 0.4 {
		t.Fatalf("resized layout must be manual: %+v", root)
	}
	m = press(m, "alt+n", "enter")
	root = m.layouts[p]
	if root.ratio != 0.4 || !root.a.isLeaf() {
		t.Errorf("the user's divider must survive a new terminal: %+v", root)
	}
	if !root.b.contains(p.Terminals[2]) {
		t.Error("new terminal should split the focused (second) pane")
	}
	checkTiling(t, m, p)
}

func TestWindowResizeRescalesWithoutChangingOrientation(t *testing.T) {
	m, spawned, p := gridModel(t, 6, 100, 30) // 3 columns x 2 rows
	if shape(m.layouts[p]) != "2,2,2" {
		t.Fatalf("setup shape %s", shape(m.layouts[p]))
	}
	before := m.layouts[p]
	for _, size := range [][2]int{{200, 14}, {60, 60}, {160, 50}, {100, 30}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = next.(Model)
		if m.layouts[p] != before || shape(m.layouts[p]) != "2,2,2" {
			t.Errorf("%dx%d: resize must keep the tree (3 columns), got %s", size[0], size[1], shape(m.layouts[p]))
		}
		rs := m.paneRects(p)
		area := m.mainArea()
		for i, s := range *spawned {
			r := rs[p.Terminals[i]]
			if s.cols != sessionCols(r) || s.rows != sessionRows(r) {
				t.Errorf("%dx%d: session %d not resized to its pane", size[0], size[1], i+1)
			}
			if r.x+r.w > area.w || r.y+r.h > area.h {
				t.Errorf("%dx%d: pane %+v outside %+v", size[0], size[1], r, area)
			}
		}
	}
	// A tall window chooses a different orientation only for a *new* grid.
	m2, _, p2 := gridModel(t, 6, 60, 60)
	if shape(m2.layouts[p2]) != "3,3" {
		t.Errorf("fresh grid on a tall window should be 2 columns, got %s", shape(m2.layouts[p2]))
	}
}

func TestSavedGridIsRecognisedAndGrowsAfterRestart(t *testing.T) {
	m, _, _ := gridModel(t, 4, 100, 30)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m2, spawned := restartModel(t, m) // 120x30: area 90x26
	p := m2.ws.Projects[0]
	m2 = press(m2, "m", "a") // restore all: the saved grid comes back as saved
	if len(*spawned) != 4 || shape(m2.layouts[p]) != "2,2" {
		t.Fatalf("restore: spawned=%d shape=%s", len(*spawned), shape(m2.layouts[p]))
	}
	if !automatic(m2.layouts[p]) {
		t.Fatal("a restored balanced grid must still count as automatic")
	}
	m2 = press(m2, "alt+n", "enter") // grows to a balanced five-pane grid, not a split cell
	if got := shape(m2.layouts[p]); got != "2,2,1" {
		t.Errorf("after restart + alt+n: shape %s, want 2,2,1", got)
	}
	m2 = press(m2, "alt+n", "enter")
	if got := shape(m2.layouts[p]); got != "2,2,2" {
		t.Errorf("six: shape %s", got)
	}
	checkTiling(t, m2, p)
}

func TestAsymmetricSavedLayoutRestoresExactly(t *testing.T) {
	m, _ := newTestModel(t)
	m = press(m, "enter", "alt+v", "enter") // 1 | 2 at 50/50
	p := m.ws.Projects[0]
	m = press(m, "alt+=", "alt+=") // terminal 2 grows twice: 30% / 70%
	if r := m.layouts[p].ratio; r < 0.29 || r > 0.31 {
		t.Fatalf("setup ratio %v", r)
	}
	saved := m.layouts[p].ratio
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	m2, spawned := restartModel(t, m)
	p2 := m2.ws.Projects[0]
	m2 = press(m2, "enter") // both come back at once, in the saved arrangement
	root := m2.layouts[p2]
	if len(*spawned) != 2 || root == nil || root.isLeaf() {
		t.Fatalf("restore: spawned=%d root=%+v", len(*spawned), root)
	}
	if root.ratio != saved || root.dir != splitSide {
		t.Errorf("30/70 must come back exactly, got ratio %v dir %v", root.ratio, root.dir)
	}
	area := m2.mainArea()
	rs := m2.paneRects(p2)
	a, b := rs[p2.Terminals[0]], rs[p2.Terminals[1]]
	if want := splitSize(area.w, paneGap, saved, minPaneCols); a.w != want || b.w != area.w-want-paneGap {
		t.Errorf("30/70 geometry wrong: %+v %+v (area %+v)", a, b, area)
	}
	if automatic(root) {
		t.Error("an asymmetric layout is the user's, never automatic")
	}
	m2 = press(m2, "alt+n", "enter") // still manual: the divider stays where it was
	if m2.layouts[p2].ratio != saved {
		t.Errorf("new terminal must not rebalance a saved manual layout: %v", m2.layouts[p2].ratio)
	}
}

func TestClosingTerminalsKeepsAutomaticLayoutBalanced(t *testing.T) {
	// Closing a middle terminal of a 3x2 grid collapses cleanly into a ragged
	// grid; closing from a column of three needs the grid rebuilt.
	m, _, p := gridModel(t, 6, 100, 30)
	second := p.Terminals[1]
	m = press(m, "alt+k", "alt+k", "alt+k", "alt+k") // focus terminal 2 (6 -> 5 -> 4 -> 3 -> 2)
	if p.ActiveTerminal() != second {
		t.Fatalf("focus setup: %d", p.ActiveTerminal().Num)
	}
	m = exit(m, second.ID)
	root := m.layouts[p]
	if len(p.Terminals) != 5 || len(root.leaves()) != 5 || !automatic(root) {
		t.Errorf("after closing 2: running=%d visible=%d automatic=%v shape=%s", len(p.Terminals), len(root.leaves()), automatic(root), shape(root))
	}
	checkTiling(t, m, p)
	m = press(m, "alt+n", "enter") // grows back to 3x2 without nesting
	if got := shape(m.layouts[p]); got != "2,2,2" {
		t.Errorf("regrown shape %s", got)
	}

	// Tall window: 6 terminals in two columns of three; close the middle of a column.
	m, _, p = gridModel(t, 6, 60, 60)
	if shape(m.layouts[p]) != "3,3" {
		t.Fatalf("tall setup %s", shape(m.layouts[p]))
	}
	third := p.Terminals[2] // second row, first column
	for p.ActiveTerminal() != third {
		m = press(m, "alt+k")
	}
	m = exit(m, third.ID)
	root = m.layouts[p]
	if !automatic(root) || len(root.leaves()) != 5 {
		t.Errorf("closing the middle of a column should leave a balanced grid, got %s (visible %d)", shape(root), len(root.leaves()))
	}
	checkTiling(t, m, p)

	// A manual layout is only collapsed, never rebuilt.
	m, _, p = gridModel(t, 3, 100, 30)
	m = press(m, "alt+=") // manual
	ratio := m.layouts[p].ratio
	first := p.Terminals[0]
	for p.ActiveTerminal() != first {
		m = press(m, "alt+k")
	}
	m = exit(m, first.ID)
	root = m.layouts[p]
	if root.dir != splitSide || root.ratio != ratio || !root.a.isLeaf() || root.a.term != p.Terminals[1] {
		t.Errorf("manual layout must just collapse: %+v", root)
	}
}

func TestOpenAllWithoutSavedLayoutBuildsAGrid(t *testing.T) {
	m, _, p := gridModel(t, 4, 100, 30)
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m2, spawned := restartModel(t, m)
	p2 := m2.ws.Projects[0]
	p2.Layout, p2.Focus = nil, "" // as if the file had no layout (v1)
	m2 = press(m2, "m", "a")
	if len(*spawned) != 4 || shape(m2.layouts[p2]) != "2,2" {
		t.Errorf("no saved layout: spawned=%d shape=%s", len(*spawned), shape(m2.layouts[p2]))
	}
	checkTiling(t, m2, p2)
	_ = p
}
