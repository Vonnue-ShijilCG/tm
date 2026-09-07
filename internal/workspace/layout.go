package workspace

import (
	"github.com/Vonnue-ShijilCG/tm/internal/state"
)

// Layout is a project's saved arrangement of terminals, independent of the
// UI: a leaf names a terminal by its stable Key, a split holds two children.
// The UI converts this to and from its pane tree; the state file stores it
// with terminal indexes instead of keys.
type Layout struct {
	Key           string // leaf: Terminal.Key() / SavedTerminal.key()
	Side          bool   // split: children left | right (false: top / bottom)
	Ratio         float64
	First, Second *Layout
}

// Key identifies a terminal across restarts: its name, its command and its
// worktree path. Unnamed plain terminals with no command share a key and are
// matched in order; a command makes two shells in one checkout distinct.
func (t *Terminal) Key() string {
	worktree := ""
	if t.Worktree != nil {
		worktree = t.Worktree.Path
	}
	return t.Name + "\x00" + t.Command + "\x00" + worktree
}

func (s SavedTerminal) key() string { return s.Name + "\x00" + s.Command + "\x00" + s.Worktree }

// Leaves returns the keys in reading order.
func (l *Layout) Leaves() []string {
	if l == nil {
		return nil
	}
	if l.First == nil && l.Second == nil {
		return []string{l.Key}
	}
	return append(l.First.Leaves(), l.Second.Leaves()...)
}

// RecordLayout records what is on screen for the next Save. The saved
// layout stays authoritative while it still places terminals that are not
// on screen but could be — hidden running terminals, or saved definitions
// not yet reopened: the visible tree is then a partial view (one terminal
// after Enter, a pane hidden with alt+x) and only the focus is updated, so
// restoring the rest brings the saved arrangement back. Once everything the
// saved layout knows about is visible, or gone for good, the live tree
// becomes the saved layout.
func (ws *Workspace) RecordLayout(p *Project, live *Layout, focusKey string) {
	p.Focus = focusKey
	if p.Layout != nil && p.layoutPending(live) {
		return
	}
	p.Layout = live
}

// layoutPending reports whether p.Layout places a terminal that live does
// not show but that still exists (running or saved). Keys are counted, not
// just matched, because unnamed shells share one key.
func (p *Project) layoutPending(live *Layout) bool {
	shown, saved, existing := map[string]int{}, map[string]int{}, map[string]int{}
	for _, k := range live.Leaves() {
		shown[k]++
	}
	for _, k := range p.Layout.Leaves() {
		saved[k]++
	}
	for _, t := range p.Terminals {
		existing[t.Key()]++
	}
	for _, s := range p.Saved {
		existing[s.key()]++
	}
	for k, n := range saved {
		if n > shown[k] && existing[k] > shown[k] {
			return true // the layout has a place for a terminal that exists but is not on screen
		}
	}
	return false
}

// layoutToState converts a key-based tree to index references into the
// snapshot's terminal list. Each key resolves to the first unused terminal
// with that key; leaves without a match are dropped by the sanitiser.
func layoutToState(l *Layout, keys []string, used map[int]bool) *state.Layout {
	if l == nil {
		return nil
	}
	if l.First == nil && l.Second == nil {
		for i, k := range keys {
			if k == l.Key && !used[i] {
				used[i] = true
				idx := i
				return &state.Layout{Terminal: &idx}
			}
		}
		return &state.Layout{} // no such terminal: dropped on sanitisation
	}
	dir := state.DirStack
	if l.Side {
		dir = state.DirSide
	}
	return &state.Layout{Direction: dir, Ratio: l.Ratio, First: layoutToState(l.First, keys, used), Second: layoutToState(l.Second, keys, used)}
}

// layoutFromState converts index references back to keys. keys[i] is ""
// for terminals the workspace refused to restore; their leaves are dropped
// and the parent collapses.
func layoutFromState(l *state.Layout, keys []string) *Layout {
	if l == nil {
		return nil
	}
	if l.Terminal != nil {
		i := *l.Terminal
		if i < 0 || i >= len(keys) || keys[i] == "" {
			return nil
		}
		return &Layout{Key: keys[i]}
	}
	first, second := layoutFromState(l.First, keys), layoutFromState(l.Second, keys)
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return &Layout{Side: l.Direction == state.DirSide, Ratio: l.Ratio, First: first, Second: second}
}
