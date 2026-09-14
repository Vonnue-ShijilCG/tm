// Command tm is a terminal-native manager for multi-repo workspaces.
//
// Run it inside a directory whose direct children are Git repositories.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
	"github.com/Vonnue-ShijilCG/tm/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tm:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}

	projects, err := project.Discover(root)
	if err != nil {
		return fmt.Errorf("discover projects in %s: %w", root, err)
	}
	if len(projects) == 0 {
		return explainNoProjects(root)
	}

	// Ask the terminal itself to use tm's ground while tm runs. Styling every
	// cell tm draws is not enough on its own: the emulator's own window
	// padding, and any cell tm has not painted, keep whatever background the
	// user's terminal was already using, which shows through as stripes and
	// edges in a colour tm never chose. Setting it here makes the whole
	// window one colour; the deferred reset hands the user's own back.
	if isTerminal(os.Stdout) {
		setBackground(terminal.PaneGround)
		defer resetBackground()
	}

	_, err = tea.NewProgram(ui.New(root, projects), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

// explainNoProjects is what tm says when it finds nothing to show. Every other
// developer tool is run inside a project, so running tm inside one is the
// first thing anybody tries; landing on an empty sidebar that only names the
// directory it looked in gives them nowhere to go. This says what tm expected
// and, when it can work it out, the command that would have worked.
func explainNoProjects(root string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "no Git repositories in %s\n", root)

	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		fmt.Fprintf(&b, "\n    %s is a repository, not a directory of them.", filepath.Base(root))
		if parent := filepath.Dir(root); parent != root {
			if near, err := project.Discover(parent); err == nil && len(near) > 0 {
				noun := "repositories"
				if len(near) == 1 {
					noun = "repository"
				}
				fmt.Fprintf(&b, "\n    The directory above holds %d %s:\n\n        cd .. && tm\n", len(near), noun)
				return errors.New(b.String())
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n    Run tm in the directory that holds your repositories:\n\n" +
		"        ~/code          <- run tm here\n" +
		"        ~/code/api      <- a repository\n" +
		"        ~/code/web      <- another\n")
	return errors.New(b.String())
}

// isTerminal reports whether w is a terminal rather than a pipe or a file.
// The colour sequences below mean nothing to anything else, and writing them
// anyway put escape bytes at the front of the output whenever tm was piped or
// run without a TTY — including in front of the error saying it could not
// start.
func isTerminal(w *os.File) bool {
	info, err := w.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// setBackground sets the terminal's default background colour (OSC 11). A
// terminal that does not support it ignores the sequence.
func setBackground(hex string) {
	fmt.Fprintf(os.Stdout, "\x1b]11;%s\x07", hex)
}

// resetBackground restores the colour the terminal had before tm started
// (OSC 111). It must run on every exit path, or tm leaves the user's terminal
// recoloured after it quits.
func resetBackground() {
	fmt.Fprint(os.Stdout, "\x1b]111\x07")
}
