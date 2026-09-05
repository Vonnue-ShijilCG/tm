package ui

import (
	"time"

	"github.com/Vonnue-ShijilCG/tm/internal/git"
)

// branchTTL is how long a branch answer is reused before .git/HEAD is read
// again. Rendering happens on every terminal output event, so the lookup
// must not hit the filesystem each time; a checkout typed in a terminal
// shows up in the header within this delay.
var branchTTL = time.Second

// branchLookup answers "which branch is checked out in this directory" for
// pane headers and manager rows. It reads .git/HEAD (no git process) and
// caches per directory. The pointer is shared by every copy of the model.
type branchLookup struct {
	cache map[string]branchEntry
}

type branchEntry struct {
	label string
	at    time.Time
}

func newBranchLookup() *branchLookup { return &branchLookup{cache: map[string]branchEntry{}} }

// get returns the branch checked out in dir, "detached" for a detached
// HEAD, or "" when dir is not a Git checkout.
func (b *branchLookup) get(dir string) string {
	if e, ok := b.cache[dir]; ok && time.Since(e.at) < branchTTL {
		return e.label
	}
	label := ""
	switch branch, err := git.Head(dir); {
	case err != nil:
		label = ""
	case branch == "":
		label = "detached"
	default:
		label = branch
	}
	b.cache[dir] = branchEntry{label: label, at: time.Now()}
	return label
}
