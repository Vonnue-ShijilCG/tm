package ui

import "github.com/Vonnue-ShijilCG/tm/internal/workspace"

// Panes are views onto existing terminal sessions. A project's visible
// terminals are arranged in a small binary split tree; terminals not in the
// tree keep running hidden and take over the focused pane when cycled to.

type splitDir int

const (
	splitSide  splitDir = iota + 1 // children left | right
	splitStack                     // children top / bottom
)

// pane is a node of the tree: a leaf holds a terminal, a split holds two
// children and the fraction of space given to the first one.
type pane struct {
	term  *workspace.Terminal
	dir   splitDir
	ratio float64
	a, b  *pane
}

const (
	// A pane must fit its card border, its header, and two rows of output.
	// Below that a split produces a box with nothing readable inside it.
	// The 2s are cardStyle's border, which is not a constant expression.
	minPaneCols = 2 + 12
	minPaneRows = 2 + 1 + 2
	minRatio    = 0.15
	maxRatio    = 0.85
	resizeStep  = 0.1
)

type rect struct{ x, y, w, h int }

func leaf(t *workspace.Terminal) *pane { return &pane{term: t} }

func (n *pane) isLeaf() bool { return n != nil && n.term != nil }

// clone copies the tree; remove and resize mutate nodes in place.
func (n *pane) clone() *pane {
	if n == nil {
		return nil
	}
	return &pane{term: n.term, dir: n.dir, ratio: n.ratio, a: n.a.clone(), b: n.b.clone()}
}

// leaves returns the visible terminals in reading order.
func (n *pane) leaves() []*workspace.Terminal {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return []*workspace.Terminal{n.term}
	}
	return append(n.a.leaves(), n.b.leaves()...)
}

func (n *pane) contains(t *workspace.Terminal) bool {
	for _, cur := range n.leaves() {
		if cur == t {
			return true
		}
	}
	return false
}

// insert splits the leaf holding at so that nt appears beside (splitSide)
// or below (splitStack) it, and returns the new root. With a nil root the
// new terminal becomes the only pane; an unknown at falls back to the root.
func (n *pane) insert(at, nt *workspace.Terminal, dir splitDir) *pane {
	if n == nil {
		return leaf(nt)
	}
	if n.isLeaf() {
		if n.term != at {
			return n
		}
		return &pane{dir: dir, ratio: 0.5, a: leaf(at), b: leaf(nt)}
	}
	n.a = n.a.insert(at, nt, dir)
	n.b = n.b.insert(at, nt, dir)
	return n
}

// remove drops the leaf holding t; its sibling takes the parent's place.
func (n *pane) remove(t *workspace.Terminal) *pane {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		if n.term == t {
			return nil
		}
		return n
	}
	n.a, n.b = n.a.remove(t), n.b.remove(t)
	switch {
	case n.a == nil:
		return n.b
	case n.b == nil:
		return n.a
	}
	return n
}

// swap shows nt in the pane that currently shows old.
func (n *pane) swap(old, nt *workspace.Terminal) {
	if n == nil {
		return
	}
	if n.isLeaf() {
		if n.term == old {
			n.term = nt
		}
		return
	}
	n.a.swap(old, nt)
	n.b.swap(old, nt)
}

// rects lays the tree out inside r. A gap between siblings is reserved —
// paneGap columns side by side, paneRowGap stacked; each child gets at least
// the minimum size when r allows. Sizes never go negative, even in an area
// too small for a gap.
func (n *pane) rects(r rect, out map[*workspace.Terminal]rect) {
	if n == nil {
		return
	}
	if n.isLeaf() {
		out[n.term] = r
		return
	}
	if n.dir == splitSide {
		wa := splitSize(r.w, paneGap, n.ratio, minPaneCols)
		n.a.rects(rect{r.x, r.y, wa, r.h}, out)
		n.b.rects(rect{r.x + wa + paneGap, r.y, max(r.w-wa-paneGap, 0), r.h}, out)
		return
	}
	ha := splitSize(r.h, paneRowGap, n.ratio, minPaneRows)
	n.a.rects(rect{r.x, r.y, r.w, ha}, out)
	n.b.rects(rect{r.x, r.y + ha + paneRowGap, r.w, max(r.h-ha-paneRowGap, 0)}, out)
}

// splitSize returns the first child's extent along an axis of total cells,
// leaving gap cells between the children and honouring the minimum where
// possible.
func splitSize(total, gap int, ratio float64, minimum int) int {
	avail := total - gap
	first := int(float64(avail)*ratio + 0.5)
	if avail >= 2*minimum {
		first = max(minimum, min(first, avail-minimum))
	} else {
		first = max(1, min(first, avail-1))
	}
	return first
}

// canSplit reports whether a pane of size r can be divided in dir while
// keeping both halves usable.
func canSplit(r rect, dir splitDir) bool {
	if dir == splitSide {
		return r.w >= 2*minPaneCols+paneGap
	}
	return r.h >= 2*minPaneRows+paneRowGap
}

// autoDir picks the split direction that leaves the roomier panes. Cells
// are about twice as tall as they are wide, so a pane looks wider than tall
// once its width exceeds twice its row count.
func autoDir(r rect) splitDir {
	if r.w >= 2*r.h {
		return splitSide
	}
	return splitStack
}

// resize moves the divider of the innermost split around t by delta in
// t's favour (positive grows t). It reports whether anything changed.
func (n *pane) resize(t *workspace.Terminal, delta float64) bool {
	if n == nil || n.isLeaf() {
		return false
	}
	if n.a.resize(t, delta) || n.b.resize(t, delta) {
		return true
	}
	var ratio float64
	switch {
	case n.a.contains(t):
		ratio = n.ratio + delta
	case n.b.contains(t):
		ratio = n.ratio - delta
	default:
		return false
	}
	ratio = max(minRatio, min(maxRatio, ratio))
	changed := ratio != n.ratio
	n.ratio = ratio
	return changed
}

type direction int

const (
	dirLeft direction = iota
	dirRight
	dirUp
	dirDown
)

// neighbor finds the pane next to from in the given direction: the closest
// pane on that side whose span overlaps from's span on the other axis.
func neighbor(rs map[*workspace.Terminal]rect, from *workspace.Terminal, d direction) *workspace.Terminal {
	src, ok := rs[from]
	if !ok {
		return nil
	}
	var best *workspace.Terminal
	bestDist := -1
	for t, r := range rs {
		if t == from {
			continue
		}
		var dist int
		var overlaps bool
		switch d {
		case dirLeft:
			dist, overlaps = src.x-(r.x+r.w), spans(src.y, src.h, r.y, r.h)
		case dirRight:
			dist, overlaps = r.x-(src.x+src.w), spans(src.y, src.h, r.y, r.h)
		case dirUp:
			dist, overlaps = src.y-(r.y+r.h), spans(src.x, src.w, r.x, r.w)
		case dirDown:
			dist, overlaps = r.y-(src.y+src.h), spans(src.x, src.w, r.x, r.w)
		}
		if dist < 0 || !overlaps {
			continue
		}
		if best == nil || dist < bestDist || (dist == bestDist && before(r, rs[best])) {
			best, bestDist = t, dist
		}
	}
	return best
}

func spans(a, alen, b, blen int) bool { return a < b+blen && b < a+alen }

// before orders equally distant candidates top-left first, deterministically.
func before(r, s rect) bool {
	if r.y != s.y {
		return r.y < s.y
	}
	return r.x < s.x
}
