// Package terminal runs a command inside a pseudo-terminal and keeps a
// virtual screen of its output, so a TUI can render it as text.
//
// It knows nothing about projects or Bubble Tea: callers feed it keys,
// tell it about resizes, and ask it to render the current screen.
package terminal

import (
	"errors"
	"image/color"
	"io"
	"os"
	"os/exec"
	"strconv"
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

// PaneGround is the ground every pane is drawn on, as a hex string. Panes
// share one colour whatever the host terminal uses, so each terminal reads as
// its own rectangle instead of blending into whatever is behind tm. It is a
// dark slate rather than true black: black against a bordered pane reads as a
// hole in the screen, and leaves nothing for a program's own dark colours to
// sit on.
//
// It is exported as the string form because the UI paints its own chrome to
// match, and Lip Gloss takes a hex string while the emulator takes a
// color.Color. One constant, parsed once, keeps the two from drifting.
const PaneGround = "#07112b"

// paneBackground is PaneGround in the form the emulator wants.
var paneBackground color.Color = mustParseHex(PaneGround)

// mustParseHex turns "#rrggbb" into a colour, panicking on a malformed
// constant — it only ever parses constants in this package, so a bad value is
// a programming error caught on the first run, not a condition to handle.
func mustParseHex(s string) color.RGBA {
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil {
		panic("terminal: bad colour constant " + s)
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// drawTarget adapts a uv.Buffer to the uv.Screen the emulator draws onto. A
// Buffer is not a Screen by itself: it has no WidthMethod, which the emulator
// needs to know how many cells a grapheme occupies.
type drawTarget struct {
	*uv.Buffer
	width uv.WidthMethod
}

func (d drawTarget) WidthMethod() uv.WidthMethod { return d.width }

// Session is a live process attached to a PTY plus the emulated screen
// that mirrors its output. All methods are safe for concurrent use.
type Session struct {
	cmd *exec.Cmd
	pty *os.File

	mu            sync.Mutex // guards emu, buf, cursorVisible, title and mouseModes
	emu           *vt.Emulator
	buf           *uv.Buffer // reused render target; see paint
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
		buf:           uv.NewBuffer(cfg.Cols, cfg.Rows),
		cursorVisible: true,
		mouseModes:    map[ansi.Mode]bool{},
		output:        make(chan struct{}, 1),
		done:          make(chan struct{}),
	}
	// SetBackgroundColor is load-bearing: it sets e.bgColor, which is what
	// Draw's per-cell top-up checks before filling a cell the program left at
	// its default, so this is what actually puts the ground under a pane's
	// own output. SetDefaultBackgroundColor matters because paneBackground is
	// not the library's default: a program's own reset restores the default
	// rather than the current colour, so without this a reset would drop the
	// pane to black instead of back onto the ground tm draws. It is also what
	// answers an OSC 11 query, so a program that themes itself to its
	// terminal picks colours that suit this ground.
	s.emu.SetDefaultBackgroundColor(paneBackground)
	s.emu.SetBackgroundColor(paneBackground)
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

// paint draws the emulated screen onto the reused buffer and renders that.
// Going through Draw rather than emu.Render is what puts the pane background
// on every cell: Render emits nothing for a cell the program left at its
// default, so those cells would show the host terminal's colour instead. Draw
// also pads every row to the full width, which makes the pane a rectangle.
//
// The caller must hold s.mu. The buffer is reused across frames so a pane that
// prints steadily does not allocate a full screen of cells per output event.
func (s *Session) paint() string {
	s.buf.Resize(s.emu.Width(), s.emu.Height())
	s.emu.Draw(drawTarget{s.buf, s.emu.WidthMethod()}, s.buf.Bounds())
	return s.buf.Render()
}

// Render returns the screen as styled text, one line per row. When
// showCursor is true the cursor cell is drawn in reverse video.
func (s *Session) Render(showCursor bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !showCursor || !s.cursorVisible {
		return s.paint()
	}

	pos := s.emu.CursorPosition()
	cell := s.emu.CellAt(pos.X, pos.Y)
	if cell == nil {
		return s.paint()
	}

	original := cell.Clone()
	highlighted := cell.Clone()
	if highlighted.Content == "" {
		highlighted.Content, highlighted.Width = " ", 1
	}
	highlighted.Style.Attrs ^= uv.AttrReverse
	s.emu.SetCell(pos.X, pos.Y, highlighted)
	out := s.paint()
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
	if offset <= 0 || s.emu.IsAltScreen() {
		return s.paint()
	}
	sb := s.emu.Scrollback()
	if sb == nil {
		return s.paint()
	}

	width, height := s.emu.Width(), s.emu.Height()
	offset = min(offset, sb.Len())
	rows := min(offset, height) // rows of history shown above the live screen

	s.buf.Resize(width, height)
	blank := uv.EmptyCell
	blank.Style.Bg = paneBackground
	s.buf.Fill(&blank)

	for y := 0; y < rows; y++ {
		line := sb.Line(sb.Len() - offset + y)
		for x := 0; x < len(line) && x < width; x++ {
			cell := line[x].Clone()
			if cell.Style.Bg == nil {
				cell.Style.Bg = paneBackground
			}
			s.buf.SetCell(x, y, cell)
		}
	}
	// The live screen fills the rows history did not, clipped to them, which
	// shows its top rows — the same lines the string-splicing version showed.
	s.emu.Draw(drawTarget{s.buf, s.emu.WidthMethod()}, uv.Rect(0, rows, width, height-rows))
	return s.buf.Render()
}
