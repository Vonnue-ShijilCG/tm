package ui

import (
	"reflect"
	"testing"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

func terms(n int) []*workspace.Terminal {
	out := make([]*workspace.Terminal, n)
	for i := range out {
		out[i] = &workspace.Terminal{ID: i + 1, Num: i + 1}
	}
	return out
}

func layoutOf(root *pane, w, h int) map[*workspace.Terminal]rect {
	rs := map[*workspace.Terminal]rect{}
	root.rects(rect{0, 0, w, h}, rs)
	return rs
}

func TestInsertSideAndStackProduceAdjacentRects(t *testing.T) {
	ts := terms(3)
	var root *pane
	root = root.insert(nil, ts[0], splitSide) // first pane
	root = root.insert(ts[0], ts[1], splitSide)
	rs := layoutOf(root, 101, 20)
	a, b := rs[ts[0]], rs[ts[1]]
	if a.w+b.w+1 != 101 || a.h != 20 || b.h != 20 || b.x != a.x+a.w+1 {
		t.Errorf("side split rects wrong: a=%+v b=%+v", a, b)
	}
	if a.w != 50 || b.w != 50 {
		t.Errorf("even split expected 50/50, got %d/%d", a.w, b.w)
	}

	root = root.insert(ts[1], ts[2], splitStack) // stack inside the right pane
	rs = layoutOf(root, 101, 20)
	b, c := rs[ts[1]], rs[ts[2]]
	if b.x != c.x || b.w != c.w || b.h+c.h+1 != 20 || c.y != b.y+b.h+1 {
		t.Errorf("stack split rects wrong: b=%+v c=%+v", b, c)
	}
	if got := root.leaves(); !reflect.DeepEqual(got, ts) {
		t.Errorf("leaves order = %v", got)
	}
	// Every rect stays inside the area and none overlap.
	for x, r := range rs {
		if r.x < 0 || r.y < 0 || r.x+r.w > 101 || r.y+r.h > 20 {
			t.Errorf("rect out of bounds for %d: %+v", x.Num, r)
		}
		for y, s := range rs {
			if x != y && spans(r.x, r.w, s.x, s.w) && spans(r.y, r.h, s.y, s.h) {
				t.Errorf("rects overlap: %+v %+v", r, s)
			}
		}
	}
}

func TestRemoveCollapsesSplitAndSwapReplacesLeaf(t *testing.T) {
	ts := terms(3)
	var root *pane
	root = root.insert(nil, ts[0], splitSide).insert(ts[0], ts[1], splitSide).insert(ts[1], ts[2], splitStack)

	root = root.remove(ts[1])
	if got := root.leaves(); !reflect.DeepEqual(got, []*workspace.Terminal{ts[0], ts[2]}) {
		t.Fatalf("after remove leaves = %v", got)
	}
	rs := layoutOf(root, 101, 20)
	if rs[ts[2]].h != 20 { // the stacked sibling now fills the right half
		t.Errorf("sibling should take the removed pane's space: %+v", rs[ts[2]])
	}
	root.swap(ts[2], ts[1])
	if !root.contains(ts[1]) || root.contains(ts[2]) {
		t.Error("swap should replace the leaf's terminal")
	}
	root = root.remove(ts[0]).remove(ts[1])
	if root != nil {
		t.Error("removing every pane should leave a nil root")
	}
	if (*pane)(nil).remove(ts[0]) != nil || len((*pane)(nil).leaves()) != 0 {
		t.Error("nil root operations must be safe")
	}
}

func TestResizeMovesDividerWithinBounds(t *testing.T) {
	ts := terms(2)
	root := leaf(ts[0]).insert(ts[0], ts[1], splitSide)
	if !root.resize(ts[0], resizeStep) {
		t.Fatal("resize should report a change")
	}
	rs := layoutOf(root, 101, 20)
	if rs[ts[0]].w != 60 || rs[ts[1]].w != 40 {
		t.Errorf("after growing a: %d/%d, want 60/40", rs[ts[0]].w, rs[ts[1]].w)
	}
	root.resize(ts[1], resizeStep) // shrinking a back
	rs = layoutOf(root, 101, 20)
	if rs[ts[0]].w != 50 {
		t.Errorf("growing b should shrink a back to 50, got %d", rs[ts[0]].w)
	}
	for i := 0; i < 20; i++ {
		root.resize(ts[0], resizeStep)
	}
	if root.ratio != maxRatio {
		t.Errorf("ratio should clamp at %v, got %v", maxRatio, root.ratio)
	}
	if root.resize(ts[0], resizeStep) {
		t.Error("resize at the clamp should report no change")
	}
	if leaf(ts[0]).resize(ts[0], resizeStep) {
		t.Error("a single pane cannot be resized")
	}
}

func TestMinimumSizesAndSplitGuards(t *testing.T) {
	ts := terms(2)
	root := leaf(ts[0]).insert(ts[0], ts[1], splitSide)
	root.ratio = 0.05
	rs := layoutOf(root, 60, 10)
	if rs[ts[0]].w < minPaneCols || rs[ts[1]].w < minPaneCols {
		t.Errorf("panes should keep the minimum width: %+v %+v", rs[ts[0]], rs[ts[1]])
	}
	// Thresholds are 2*minPaneCols+1 and 2*minPaneRows+1: 29 and 11 with the
	// card-aware minimums.
	if canSplit(rect{0, 0, 28, 10}, splitSide) || !canSplit(rect{0, 0, 29, 10}, splitSide) {
		t.Error("canSplit side thresholds wrong")
	}
	if canSplit(rect{0, 0, 80, 10}, splitStack) || !canSplit(rect{0, 0, 80, 11}, splitStack) {
		t.Error("canSplit stack thresholds wrong")
	}
	if autoDir(rect{0, 0, 100, 20}) != splitSide || autoDir(rect{0, 0, 39, 20}) != splitStack {
		t.Error("autoDir should split wide panes side by side and narrow ones stacked")
	}
}

func TestNeighborFindsAdjacentPanes(t *testing.T) {
	ts := terms(3)
	// [0 | 1]
	//     [2]   (2 stacked under 1)
	root := leaf(ts[0]).insert(ts[0], ts[1], splitSide).insert(ts[1], ts[2], splitStack)
	rs := layoutOf(root, 101, 21)
	cases := []struct {
		from *workspace.Terminal
		d    direction
		want *workspace.Terminal
	}{
		{ts[0], dirRight, ts[1]}, // tie between 1 and 2 resolves to the top one
		{ts[1], dirLeft, ts[0]},
		{ts[2], dirLeft, ts[0]},
		{ts[1], dirDown, ts[2]},
		{ts[2], dirUp, ts[1]},
		{ts[0], dirLeft, nil},
		{ts[0], dirUp, nil},
		{ts[2], dirDown, nil},
	}
	for _, tc := range cases {
		if got := neighbor(rs, tc.from, tc.d); got != tc.want {
			t.Errorf("neighbor(%d, %v) = %v, want %v", tc.from.Num, tc.d, got, tc.want)
		}
	}
	if neighbor(rs, &workspace.Terminal{}, dirLeft) != nil {
		t.Error("unknown terminal has no neighbours")
	}
}
