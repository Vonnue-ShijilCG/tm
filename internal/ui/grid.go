package ui

import (
	"math"
	"slices"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// Automatic layouts are balanced grids. A grid is a row of columns, each a
// stack of terminals, filled row by row in terminal order:
//
//	1 │ 2 │ 3
//	──┼───┼──
//	4 │ 5 │
//
// so terminal 3 has the whole right column to itself. As a pane tree a grid
// is a chain of side splits (one per column, ratio 1/columns-left) whose
// children are chains of stack splits (ratio 1/terminals-left). That
// canonical shape is what makes an automatic layout recognisable: a tree
// tm built and nobody touched is exactly such a chain with exactly those
// ratios. Any alt+v / alt+h split or divider resize breaks the pattern and
// the layout becomes the user's; from then on new terminals are inserted
// next to the focused pane instead of the grid being rebuilt.
type grid [][]*workspace.Terminal

// ratioTolerance absorbs float round-trips through the state file.
const ratioTolerance = 0.01

// allowedCols lists the column counts that keep a grid of n terminals
// compact: the integers on either side of sqrt(n). 4 → 2×2 only; 5 and 6
// → two or three columns, chosen by the window's shape.
func allowedCols(n int) []int {
	if n <= 1 {
		return []int{1}
	}
	lo := int(math.Sqrt(float64(n)))
	if lo*lo == n {
		return []int{lo}
	}
	return []int{lo, lo + 1}
}

// chooseCols picks the allowed column count for n terminals in r whose
// cells come closest to square (a character cell is about twice as tall as
// wide), preferring counts that keep every pane at the minimum size. It
// only decides orientation when a grid is generated; a window resize keeps
// whatever was chosen.
func chooseCols(n int, r rect) int {
	options := allowedCols(n)
	best, bestScore, bestFits := options[0], math.Inf(1), false
	for _, cols := range options {
		w, h := cellSize(n, cols, r)
		fits := w >= minPaneCols && h >= minPaneRows
		score := math.Abs(math.Log(float64(max(w, 1)) / 2 / float64(max(h, 1))))
		if (fits && !bestFits) || (fits == bestFits && score < bestScore) {
			best, bestScore, bestFits = cols, score, fits
		}
	}
	return best
}

// cellSize is the size of one pane in an n-terminal grid with cols columns
// filling r, dividers taken out.
func cellSize(n, cols int, r rect) (w, h int) {
	rows := (n + cols - 1) / cols
	return (r.w - (cols - 1)) / cols, (r.h - (rows - 1)) / rows
}

// gridFor fills cols columns with ts row by row: ts[i] goes to column i%cols.
func gridFor(ts []*workspace.Terminal, cols int) grid {
	g := make(grid, cols)
	for i, t := range ts {
		g[i%cols] = append(g[i%cols], t)
	}
	return slices.DeleteFunc(g, func(col []*workspace.Terminal) bool { return len(col) == 0 })
}

// tree builds the canonical pane tree for g.
func (g grid) tree() *pane {
	cols := make([]*pane, len(g))
	for i, col := range g {
		cells := make([]*pane, len(col))
		for j, t := range col {
			cells[j] = leaf(t)
		}
		cols[i] = chain(cells, splitStack)
	}
	return chain(cols, splitSide)
}

// chain nests nodes to the right, giving the first 1/len of the space.
func chain(nodes []*pane, dir splitDir) *pane {
	if len(nodes) == 1 {
		return nodes[0]
	}
	return &pane{dir: dir, ratio: 1 / float64(len(nodes)), a: nodes[0], b: chain(nodes[1:], dir)}
}

// gridOf recognises a canonical grid tree and returns its columns. Trees
// nested the other way, with other ratios, or mixing directions are not
// grids.
func gridOf(n *pane) (grid, bool) {
	if n == nil {
		return nil, false
	}
	cols, ok := unchain(n, splitSide)
	if !ok {
		return nil, false
	}
	g := make(grid, 0, len(cols))
	for _, c := range cols {
		cells, ok := unchain(c, splitStack)
		if !ok {
			return nil, false
		}
		col := make([]*workspace.Terminal, len(cells))
		for i, cell := range cells {
			if !cell.isLeaf() {
				return nil, false
			}
			col[i] = cell.term
		}
		g = append(g, col)
	}
	return g, true
}

// unchain is the inverse of chain: it unrolls a right-nested chain of dir
// splits and checks every ratio is 1/nodes-left. A node that is not a dir
// split is a chain of one.
func unchain(n *pane, dir splitDir) ([]*pane, bool) {
	var nodes []*pane
	var ratios []float64
	for n != nil && !n.isLeaf() && n.dir == dir {
		nodes, ratios = append(nodes, n.a), append(ratios, n.ratio)
		n = n.b
	}
	nodes = append(nodes, n)
	for i, r := range ratios {
		if math.Abs(r-1/float64(len(nodes)-i)) > ratioTolerance {
			return nil, false
		}
	}
	return nodes, true
}

// balanced reports whether g is an arrangement tm would generate for its
// terminals: an allowed column count and column heights within one of each
// other (in any order, so closing a terminal keeps a grid a grid).
func (g grid) balanced() bool {
	n := 0
	lo, hi := math.MaxInt, 0
	for _, col := range g {
		n += len(col)
		lo, hi = min(lo, len(col)), max(hi, len(col))
	}
	return n > 0 && slices.Contains(allowedCols(n), len(g)) && hi-lo <= 1
}

// automatic reports whether root is an untouched tm-generated layout.
func automatic(root *pane) bool {
	g, ok := gridOf(root)
	return ok && g.balanced()
}

// rowMajor lists g's terminals row by row, the order they were filled in.
func (g grid) rowMajor() []*workspace.Terminal {
	var out []*workspace.Terminal
	for row := 0; ; row++ {
		before := len(out)
		for _, col := range g {
			if row < len(col) {
				out = append(out, col[row])
			}
		}
		if len(out) == before {
			return out
		}
	}
}

// without returns g minus t, dropping a column that becomes empty.
func (g grid) without(t *workspace.Terminal) grid {
	out := make(grid, 0, len(g))
	for _, col := range g {
		col = slices.DeleteFunc(slices.Clone(col), func(x *workspace.Terminal) bool { return x == t })
		if len(col) > 0 {
			out = append(out, col)
		}
	}
	return out
}

// grow adds nt to g for an area of size r: into the shortest column when
// the column count stays right for the new total (nothing else moves),
// otherwise the grid is refilled row by row with nt last.
func (g grid) grow(nt *workspace.Terminal, r rect) grid {
	ts := append(g.rowMajor(), nt)
	cols := chooseCols(len(ts), r)
	if cols != len(g) {
		return gridFor(ts, cols)
	}
	out := make(grid, len(g))
	shortest := 0
	for i, col := range g {
		out[i] = slices.Clone(col)
		if len(col) < len(g[shortest]) {
			shortest = i
		}
	}
	out[shortest] = append(out[shortest], nt)
	return out
}

// fits reports whether every pane of root has the minimum size inside r.
func (n *pane) fits(r rect) bool {
	rs := map[*workspace.Terminal]rect{}
	n.rects(r, rs)
	for _, pr := range rs {
		if pr.w < minPaneCols || pr.h < minPaneRows {
			return false
		}
	}
	return true
}
