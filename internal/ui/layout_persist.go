package ui

import "github.com/Vonnue-ShijilCG/tm/internal/workspace"

// layoutFromPane converts the live pane tree into the workspace's key-based
// layout for persistence.
func layoutFromPane(n *pane) *workspace.Layout {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return &workspace.Layout{Key: n.term.Key()}
	}
	return &workspace.Layout{Side: n.dir == splitSide, Ratio: n.ratio, First: layoutFromPane(n.a), Second: layoutFromPane(n.b)}
}

// paneFromLayout rebuilds a pane tree from a saved layout using the given
// running terminals. Each leaf takes the first unused terminal with its
// key; leaves without one are dropped and their parent collapses, so the
// result only ever shows terminals that exist. used is filled with the
// terminals placed.
func paneFromLayout(l *workspace.Layout, terms []*workspace.Terminal, used map[*workspace.Terminal]bool) *pane {
	if l == nil {
		return nil
	}
	if l.First == nil && l.Second == nil {
		for _, t := range terms {
			if !used[t] && t.Key() == l.Key {
				used[t] = true
				return leaf(t)
			}
		}
		return nil
	}
	a, b := paneFromLayout(l.First, terms, used), paneFromLayout(l.Second, terms, used)
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	dir := splitStack
	if l.Side {
		dir = splitSide
	}
	return &pane{dir: dir, ratio: max(minRatio, min(maxRatio, l.Ratio)), a: a, b: b}
}

// enforceMinimums hides panes that a restored tree squeezes below the
// minimum size, smallest first, until every visible pane is usable. Hidden
// terminals keep running and can be shown from the manager.
func (m *Model) enforceMinimums(p *workspace.Project) {
	for {
		root := m.layouts[p]
		leaves := root.leaves()
		if len(leaves) < 2 {
			return
		}
		rs := m.paneRects(p)
		var victim *workspace.Terminal
		victimArea := -1
		for _, t := range leaves {
			r := rs[t]
			if r.w < minPaneCols || r.h < minPaneRows {
				if area := r.w * r.h; victim == nil || area < victimArea {
					victim, victimArea = t, area
				}
			}
		}
		if victim == nil {
			return
		}
		m.layouts[p] = root.remove(victim)
	}
}
