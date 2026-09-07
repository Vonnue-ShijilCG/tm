// Command tm is a terminal-native manager for multi-repo workspaces.
//
// Run it inside a directory whose direct children are Git repositories.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/project"
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

	_, err = tea.NewProgram(ui.New(root, projects), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}
