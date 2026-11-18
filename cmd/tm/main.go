// Command tm is a terminal-native manager for multi-repo workspaces.
//
// Run it inside a directory whose direct children are Git repositories.
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Vonnue-ShijilCG/tm/internal/config"
	"github.com/Vonnue-ShijilCG/tm/internal/project"
	"github.com/Vonnue-ShijilCG/tm/internal/server"
	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
	"github.com/Vonnue-ShijilCG/tm/internal/ui"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "tm:", err)
		os.Exit(1)
	}
}

// run is tm with its arguments. `stop` and the hidden `__server <root>` are
// the only ones it knows; anything else is ignored, as it always was.
func run(args []string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "__server" {
		if len(args) != 2 {
			return errors.New("usage: tm __server <root>")
		}
		return serve(args[1])
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "stop" {
		return stop(root, stdout)
	}

	projects, err := project.Discover(root)
	if err != nil {
		return fmt.Errorf("discover projects in %s: %w", root, err)
	}
	if len(projects) == 0 {
		return explainNoProjects(root)
	}

	// A config file with mistakes still yields usable values, and the UI
	// reports the mistake itself in the status bar.
	cfg, _ := config.Load(config.Path())
	if cfg.Server {
		return runAttached(root, projects)
	}
	return runHere(root, projects)
}

// runHere is tm with its terminals in this process.
func runHere(root string, projects []project.Project) error {
	forget, err := offModeGuard(root)
	if err != nil {
		return err
	}
	_, err = runUI(ui.NewWith(root, projects, ui.Options{Forget: forget}))
	return err
}

// offModeGuard refuses to start while a server holds this workspace's
// terminals: tm would start a second copy of every one of them beside the
// first. Otherwise it returns the refs of terminals that ended in a server
// since tm last ran.
func offModeGuard(root string) ([]string, error) {
	// Without a state directory there can be no server to find, and tm runs
	// exactly as it did before servers existed.
	paths, err := server.PathsFor(root)
	if err != nil {
		return nil, nil
	}
	if live, running := server.Probe(paths); running {
		return nil, fmt.Errorf("a tm server is running here with %s: set server = true in %s to reattach, or run tm stop",
			terminals(live), config.Display(config.Path()))
	}
	return server.TakeExited(paths), nil
}

// runAttached is tm with its terminals in the workspace's server.
func runAttached(root string, projects []project.Project) error {
	client, welcome, forget, err := connect(root)
	if err != nil {
		return err
	}
	defer client.Close()

	final, err := runUI(ui.NewWith(root, projects, ui.Options{Client: client, Welcome: welcome, Forget: forget}))
	// Printed after the alternate screen is gone, or it would vanish with it.
	if m, ok := final.(ui.Model); ok {
		if msg := m.ExitMessage(); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
	}
	return err
}

// connect attaches to the workspace's server, starting one when none is
// running, and returns the refs of terminals that ended while no window
// was attached.
func connect(root string) (*server.Client, server.Welcome, []string, error) {
	paths, err := server.PathsFor(root)
	if err != nil {
		return nil, server.Welcome{}, nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, server.Welcome{}, nil, err
	}
	if err := server.Ensure(paths, exe, root); err != nil {
		return nil, server.Welcome{}, nil, err
	}
	client, welcome, err := server.Attach(paths)
	if errors.Is(err, server.ErrProtocol) {
		live, _ := server.Probe(paths)
		return nil, server.Welcome{}, nil, fmt.Errorf("a tm server from another tm version is running here with %s; run tm stop to end them, then start tm again", terminals(live))
	}
	if err != nil {
		return nil, server.Welcome{}, nil, err
	}
	// Taken only once attached: a start that fails leaves them for the next.
	return client, welcome, server.TakeExited(paths), nil
}

// runUI runs the Bubble Tea program and returns its final model.
func runUI(m ui.Model) (tea.Model, error) {
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

	return tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
}

// stop is `tm stop`: end the workspace's server and every terminal in it.
// Finding none running is an answer, not a failure.
func stop(root string, stdout io.Writer) error {
	paths, err := server.PathsFor(root)
	if err != nil {
		return err
	}
	n, err := server.StopServer(paths)
	switch {
	case errors.Is(err, server.ErrNotRunning):
		fmt.Fprintln(stdout, "no tm server running here")
		return nil
	case err != nil:
		return err
	}
	fmt.Fprintf(stdout, "stopped %s\n", terminals(n))
	return nil
}

// serve is the hidden `tm __server <root>`, which Ensure starts detached.
// It outlives the terminal that started it, so a SIGHUP is caught and
// dropped; SIGTERM stops it as `tm stop` would.
//
// Caught, not signal.Ignore'd: an ignored signal stays ignored across
// exec, so every shell the server started, and everything run in it,
// ignored SIGHUP too. Killing a shell hangs up its PTY, and the hangup is
// what ends the job in its foreground — with it ignored, a dev server
// would outlive the terminal it was started in. A caught signal is reset to
// its default in the child.
func serve(root string) error {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	// Registered before the socket exists, so a SIGTERM sent to a server
	// that answers is always caught rather than killing it mid-stop.
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	defer signal.Stop(term)

	paths, err := server.PathsFor(root)
	if err != nil {
		return err
	}
	l, err := server.Listen(paths)
	if err != nil {
		return err
	}
	served := make(chan struct{})
	defer close(served)
	var terminated atomic.Bool
	go func() {
		select {
		case <-term:
			// Closing the listener is the server's own stop path: nothing
			// can reach the sessions any more, so it ends them.
			terminated.Store(true)
			l.Close()
		case <-served:
		}
	}()

	err = server.New(server.DefaultSpawner, paths.Exited).Serve(l)
	if terminated.Load() && errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// terminals is a count of terminals that reads properly at one.
func terminals(n int) string {
	if n == 1 {
		return "1 terminal"
	}
	return fmt.Sprintf("%d terminals", n)
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
