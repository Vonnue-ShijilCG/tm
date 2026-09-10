// Command tm is a terminal-native manager for multi-repo workspaces.
//
// Run it inside a directory whose direct children are Git repositories.
package main

import (
	"fmt"
	"os"

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

	// Ask the terminal itself to use tm's ground while tm runs. Styling every
	// cell tm draws is not enough on its own: the emulator's own window
	// padding, and any cell tm has not painted, keep whatever background the
	// user's terminal was already using, which shows through as stripes and
	// edges in a colour tm never chose. Setting it here makes the whole
	// window one colour; the deferred reset hands the user's own back.
	setBackground(terminal.PaneGround)
	defer resetBackground()

	_, err = tea.NewProgram(ui.New(root, projects), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
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
