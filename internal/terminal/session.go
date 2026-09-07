// Package terminal runs a command inside a pseudo-terminal and keeps a
// virtual screen of its output, so a TUI can render it as text.
//
// It knows nothing about projects or Bubble Tea: callers feed it keys,
// tell it about resizes, and ask it to render the current screen.
package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

const fallbackShell = "/bin/bash"

// DefaultShell returns $SHELL when it points at an executable, otherwise
// the fallback shell.
func DefaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		if info, err := os.Stat(shell); err == nil && !info.IsDir() {
			return shell
		}
	}
	return fallbackShell
}

// Config describes the command to run inside the PTY.
type Config struct {
	Command string   // executable, e.g. the result of DefaultShell
	Args    []string // arguments passed to Command
	Dir     string   // working directory for the process
	Cols    int      // initial terminal width in cells
	Rows    int      // initial terminal height in cells
}

// Session is a live process attached to a PTY plus the emulated screen
// that mirrors its output. All methods are safe for concurrent use.
type Session struct {
	cmd *exec.Cmd
	pty *os.File

	mu            sync.Mutex // guards emu, cursorVisible, title and mouseModes
	emu           *vt.Emulator
	cursorVisible bool
	title         string             // last title set by the program via OSC 0/2
	mouseModes    map[ansi.Mode]bool // mouse tracking modes the program has enabled

	output chan struct{} // signalled (buffered, coalescing) after new output
	done   chan struct{} // closed once the process has exited
	err    error         // exit error, valid after done is closed
}

// Start launches cfg.Command inside a new PTY of the given size.
func Start(cfg Config) (*Session, error) {
	if cfg.Cols <= 0 || cfg.Rows <= 0 {
		return nil, errors.New("terminal: size must be positive")
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cfg.Cols), Rows: uint16(cfg.Rows)})
	if err != nil {
		return nil, err
	}

	s := &Session{
		cmd:           cmd,
		pty:           ptmx,
		emu:           vt.NewEmulator(cfg.Cols, cfg.Rows),
		cursorVisible: true,
		mouseModes:    map[ansi.Mode]bool{},
		output:        make(chan struct{}, 1),
		done:          make(chan struct{}),
	}
	// Called from emu.Write, i.e. while s.mu is held by readLoop.
	s.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { s.cursorVisible = visible },
		Title:            func(title string) { s.title = title },
		EnableMode: func(mode ansi.Mode) {
			if isMouseTracking(mode) {
				s.mouseModes[mode] = true
			}
		},
		DisableMode: func(mode ansi.Mode) {
			if isMouseTracking(mode) {
				delete(s.mouseModes, mode)
			}
		},
	})
	go s.readLoop()
	go s.replyLoop()
	return s, nil
}

// readLoop copies PTY output into the emulator until the process exits.
func (s *Session) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.mu.Lock()
			_, _ = s.emu.Write(buf[:n])
			s.mu.Unlock()
			s.notify()
		}
		if err != nil { // io.EOF or EIO once the child closes its side
			break
		}
	}
	s.err = s.cmd.Wait()
	_ = s.pty.Close()
	s.closeInputPipe()
	close(s.done)
	s.notify()
}

// closeInputPipe closes the emulator's reply pipe so replyLoop sees EOF and
// later SendKey/SendText calls fail fast instead of blocking.
//
// The emulator's own Close would do this too, but it flips an unsynchronised
// flag that replyLoop reads concurrently, which the race detector rejects.
func (s *Session) closeInputPipe() {
	if pw, ok := s.emu.InputPipe().(interface{ CloseWithError(error) error }); ok {
		_ = pw.CloseWithError(io.EOF)
	}
}

// replyLoop forwards bytes the emulator wants to send to the process:
// key sequences from SendKey/SendText and answers to terminal queries
// (cursor position reports and the like). It keeps draining even if the
// PTY write fails, so an emulator write never blocks on a full pipe.
func (s *Session) replyLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.emu.Read(buf)
		if n > 0 {
			_, _ = s.pty.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) notify() {
	select {
	case s.output <- struct{}{}:
	default: // a notification is already pending; it covers this update too
	}
}

// Output is signalled whenever the screen may have changed.
func (s *Session) Output() <-chan struct{} { return s.output }

// Done is closed when the process has exited.
func (s *Session) Done() <-chan struct{} { return s.done }

// Err returns the process exit error. Only meaningful after Done is closed.
func (s *Session) Err() error {
	select {
	case <-s.done:
		return s.err
	default:
		return nil
	}
}

// SendKey encodes a key press the way the running program expects
// (honouring application cursor mode etc.) and writes it to the PTY.
func (s *Session) SendKey(k uv.KeyPressEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emu.SendKey(k)
}

// isMouseTracking reports whether mode makes the terminal report mouse
// events at all (as opposed to the encoding modes such as SGR 1006).
func isMouseTracking(mode ansi.Mode) bool {
	switch mode {
	case ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight, ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent:
		return true
	}
	return false
}

// MouseTracking reports whether the running program has asked for mouse
// events. When it has, wheel events belong to it (see SendWheel) rather
// than to the scrollback view.
func (s *Session) MouseTracking() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.mouseModes) > 0
}

// SendWheel reports one wheel notch at screen cell (x, y), encoded in the
// mouse protocol the program enabled. It writes nothing when the program
// has not enabled mouse tracking.
func (s *Session) SendWheel(up bool, x, y int) {
	button := uv.MouseWheelDown
	if up {
		button = uv.MouseWheelUp
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emu.SendMouse(uv.MouseWheelEvent{X: x, Y: y, Button: button})
}

// SendText writes literal text to the PTY.
func (s *Session) SendText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emu.SendText(text)
}

// Resize changes both the PTY window size (so the child gets SIGWINCH)
// and the emulated screen.
func (s *Session) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emu.Width() == cols && s.emu.Height() == rows {
		return
	}
	_ = pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	s.emu.Resize(cols, rows)
}

// Title returns the window title the running program last set, or "".
func (s *Session) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// Size returns the current screen size in cells.
func (s *Session) Size() (cols, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.Width(), s.emu.Height()
}

// Render returns the screen as styled text, one line per row. When
// showCursor is true the cursor cell is drawn in reverse video.
func (s *Session) Render(showCursor bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !showCursor || !s.cursorVisible {
		return s.emu.Render()
	}

	pos := s.emu.CursorPosition()
	cell := s.emu.CellAt(pos.X, pos.Y)
	if cell == nil {
		return s.emu.Render()
	}

	original := cell.Clone()
	highlighted := cell.Clone()
	if highlighted.Content == "" {
		highlighted.Content, highlighted.Width = " ", 1
	}
	highlighted.Style.Attrs ^= uv.AttrReverse
	s.emu.SetCell(pos.X, pos.Y, highlighted)
	out := s.emu.Render()
	s.emu.SetCell(pos.X, pos.Y, original)
	return out
}

// Close terminates the process if it is still running and releases the PTY.
// It returns once the process has exited.
func (s *Session) Close() error {
	select {
	case <-s.done:
		return nil
	default:
	}
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	<-s.done
	return nil
}

// ScrollbackLen returns how many lines have scrolled off the top of the main
// screen and can be viewed with RenderScrolled. It is 0 while the program
// uses the alternate screen, which keeps no history.
func (s *Session) ScrollbackLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.emu.IsAltScreen() {
		return 0
	}
	return s.emu.ScrollbackLen()
}

// IsAltScreen reports whether the program has switched to the alternate
// screen (full-screen programs such as editors and pagers).
func (s *Session) IsAltScreen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.IsAltScreen()
}

// RenderScrolled returns the screen as it looks scrolled back by offset
// lines: the last offset lines of history on top, then the upper part of
// the live screen, always as many lines as the screen has rows. offset is
// clamped to the history length; 0 renders the live screen without cursor.
func (s *Session) RenderScrolled(offset int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := s.emu.Render()
	if offset <= 0 || s.emu.IsAltScreen() {
		return live
	}
	sb := s.emu.Scrollback()
	if sb == nil {
		return live
	}
	offset = min(offset, sb.Len())
	height := s.emu.Height()
	lines := make([]string, 0, height)
	for i := sb.Len() - offset; i < sb.Len() && len(lines) < height; i++ {
		lines = append(lines, sb.Line(i).Render())
	}
	screen := strings.Split(live, "\n")
	lines = append(lines, screen[:max(height-len(lines), 0)]...)
	return strings.Join(lines, "\n")
}
