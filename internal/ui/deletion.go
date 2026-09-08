package ui

import (
	"fmt"
	"path/filepath"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// Deleting a terminal is one operation with two entry points, alt+delete on
// the focused terminal and d in the manager, and this file words it for
// both. The work itself is Workspace.DeleteTerminal.

// deletionPrompt is the question asked before a terminal is deleted for
// good. It says what goes: the definition, the process when running, and
// the dedicated worktree — with its state spelled out, so a dirty worktree
// is never removed without the user having read that it is dirty.
func deletionPrompt(ws *workspace.Workspace, p *workspace.Project, def workspace.SavedTerminal, running *workspace.Terminal) string {
	who := "this saved terminal"
	if running != nil {
		who = terminalLabel(running)
	}
	if def.Worktree == "" {
		if running != nil {
			return fmt.Sprintf("Delete %s? Its process stops; the project checkout is untouched.", who)
		}
		return fmt.Sprintf("Delete %s? Nothing on disk is touched.", who)
	}
	info, listed := ws.WorktreeStatus(p, def.Worktree)
	switch {
	case !listed:
		return fmt.Sprintf("Delete %s? Its worktree is no longer known to git.", who)
	case info.Foreign:
		return fmt.Sprintf("Delete %s? Its worktree was not created by tm, so tm will leave it as is.", who)
	case info.Missing:
		return fmt.Sprintf("Delete %s? Its worktree directory is already gone.", who)
	case info.Dirty:
		return fmt.Sprintf("Delete %s and its worktree %q? It has UNCOMMITTED CHANGES.", who, worktreeLabel(def, info))
	default:
		return fmt.Sprintf("Delete %s and its worktree %q? The worktree is clean.", who, worktreeLabel(def, info))
	}
}

// deletionNotice reports what a deletion did. note is Workspace.DeleteTerminal's
// explanation when the worktree was kept.
func deletionNotice(def workspace.SavedTerminal, running *workspace.Terminal, note string) string {
	s := "deleted saved terminal"
	if running != nil {
		s = "deleted " + terminalLabel(running)
	}
	switch {
	case note != "":
		s += " (" + note + ")"
	case def.Worktree != "":
		s += " and its worktree"
	}
	return s
}

// worktreeLabel names a worktree the way the user knows it: by its branch,
// else by its directory name.
func worktreeLabel(def workspace.SavedTerminal, info workspace.WorktreeInfo) string {
	switch {
	case info.Branch != "":
		return info.Branch
	case def.Branch != "":
		return def.Branch
	default:
		return filepath.Base(def.Worktree)
	}
}
