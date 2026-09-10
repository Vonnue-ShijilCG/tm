package terminal

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const waitTimeout = 5 * time.Second

func TestDefaultShellPrefersSHELL(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	if got := DefaultShell(); got != "/bin/sh" {
		t.Fatalf("DefaultShell() = %q, want /bin/sh", got)
	}
	t.Setenv("SHELL", "/definitely/not/a/shell")
	if got := DefaultShell(); got != fallbackShell {
		t.Fatalf("DefaultShell() with bogus $SHELL = %q, want %q", got, fallbackShell)
	}
	t.Setenv("SHELL", "")
	if got := DefaultShell(); got != fallbackShell {
		t.Fatalf("DefaultShell() with empty $SHELL = %q, want %q", got, fallbackShell)
	}
}

func TestSessionRunsInDirAndRendersOutput(t *testing.T) {
	dir := t.TempDir()
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "pwd; echo hello-from-pty; exit 3"}, Dir: dir, Cols: 80, Rows: 10})

	waitDone(t, s)

	screen := s.Render(false)
	if !strings.Contains(screen, "hello-from-pty") {
		t.Errorf("screen missing command output:\n%s", screen)
	}
	if !strings.Contains(screen, dir) {
		t.Errorf("screen missing working dir %q:\n%s", dir, screen)
	}
	var exitErr *exec.ExitError
	if !errors.As(s.Err(), &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("Err() = %v, want exit status 3", s.Err())
	}
}

func TestSessionForwardsKeys(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "read line; echo got:$line"}, Dir: t.TempDir(), Cols: 80, Rows: 10})

	s.SendText("abc")
	s.SendKey(uv.KeyPressEvent{Code: uv.KeyEnter})

	waitFor(t, s, "got:abc")
}

func TestSessionResizePropagatesToChild(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "sleep 0.3; stty size"}, Dir: t.TempDir(), Cols: 80, Rows: 10})
	s.Resize(50, 12)

	waitFor(t, s, "12 50")
	if w, h := s.Size(); w != 50 || h != 12 {
		t.Errorf("Size() = %dx%d, want 50x12", w, h)
	}
}

func TestSessionCloseKillsProcess(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "sleep 30"}, Dir: t.TempDir(), Cols: 80, Rows: 10})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitDone(t, s)
}

func TestSessionRenderShowsCursor(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "printf 'ab'; sleep 30"}, Dir: t.TempDir(), Cols: 20, Rows: 3})
	waitFor(t, s, "ab")

	// With the cursor shown, the cell after "ab" is rendered reverse-video,
	// which introduces an escape sequence that is absent otherwise.
	plain, withCursor := s.Render(false), s.Render(true)
	if plain == withCursor {
		t.Fatalf("Render(true) should differ from Render(false):\n%q", withCursor)
	}
}

func TestSessionTracksTitle(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", `printf '\033]0;npm run dev\007ready'; sleep 30`}, Dir: t.TempDir(), Cols: 40, Rows: 5})
	waitFor(t, s, "ready")
	if got := s.Title(); got != "npm run dev" {
		t.Fatalf("Title() = %q, want %q", got, "npm run dev")
	}
}

func start(t *testing.T, cfg Config) *Session {
	t.Helper()
	if _, err := os.Stat(cfg.Command); err != nil {
		t.Skipf("%s not available: %v", cfg.Command, err)
	}
	s, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func waitDone(t *testing.T, s *Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(waitTimeout):
		t.Fatalf("process did not exit within %s", waitTimeout)
	}
}

// waitFor drains Output() notifications until the screen contains want.
func waitFor(t *testing.T, s *Session, want string) {
	t.Helper()
	deadline := time.After(waitTimeout)
	for {
		if strings.Contains(s.Render(false), want) {
			return
		}
		select {
		case <-s.Output():
		case <-s.Done():
			if strings.Contains(s.Render(false), want) {
				return
			}
			t.Fatalf("process exited before %q appeared:\n%s", want, s.Render(false))
		case <-deadline:
			t.Fatalf("timed out waiting for %q:\n%s", want, s.Render(false))
		}
	}
}

func TestRenderScrolledShowsHistoryAboveTheScreen(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "i=1; while [ $i -le 30 ]; do printf 'line-%s\\n' $i; i=$((i+1)); done; sleep 30"}, Dir: t.TempDir(), Cols: 20, Rows: 5})
	waitFor(t, s, "line-30")

	// 30 lines through a 5-row screen: rows hold line-27..line-30 plus the
	// cursor row, everything before is history.
	if n := s.ScrollbackLen(); n != 26 {
		t.Fatalf("ScrollbackLen() = %d, want 26", n)
	}

	got := strings.Split(s.RenderScrolled(3), "\n")
	if len(got) != 5 {
		t.Fatalf("RenderScrolled(3) has %d lines, want 5:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.Contains(got[0], "line-24") || !strings.Contains(got[2], "line-26") || !strings.Contains(got[3], "line-27") || !strings.Contains(got[4], "line-28") {
		t.Errorf("RenderScrolled(3) shows wrong lines:\n%s", strings.Join(got, "\n"))
	}

	top := strings.Split(s.RenderScrolled(1000), "\n") // clamps to the history length
	if !strings.Contains(ansi.Strip(top[0]), "line-1 ") && strings.TrimSpace(ansi.Strip(top[0])) != "line-1" {
		t.Errorf("RenderScrolled(1000) should start at the oldest line:\n%s", strings.Join(top, "\n"))
	}

	if live := s.RenderScrolled(0); live != s.Render(false) {
		t.Errorf("RenderScrolled(0) should equal the live screen\n got: %q\nwant: %q", live, s.Render(false))
	}
}

func TestIsAltScreenFollowsTheProgram(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "printf 'main\\n'; sleep 0.3; printf '\\033[?1049halt\\n'; sleep 30"}, Dir: t.TempDir(), Cols: 20, Rows: 5})
	waitFor(t, s, "main")
	if s.IsAltScreen() {
		t.Fatal("IsAltScreen() = true before the program switched screens")
	}
	waitFor(t, s, "alt")
	if !s.IsAltScreen() {
		t.Fatal("IsAltScreen() = false after \\e[?1049h")
	}
	if n := s.ScrollbackLen(); n != 0 {
		t.Errorf("ScrollbackLen() on the alternate screen = %d, want 0", n)
	}
}

func TestSendWheelReachesAProgramThatTracksTheMouse(t *testing.T) {
	// The program enables normal mouse tracking with SGR encoding, then echoes
	// whatever it reads with escapes made visible.
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "printf '\\033[?1000h\\033[?1006h'; cat -v"}, Dir: t.TempDir(), Cols: 40, Rows: 5})
	waitUntil(t, s.MouseTracking, "program to enable mouse tracking")

	s.SendWheel(true, 5, 3)

	waitFor(t, s, "^[[<64;6;4M") // SGR wheel-up at 1-based column 6, row 4
}

func TestMouseTrackingFollowsTheProgram(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "sleep 0.3; printf '\\033[?1002h'; sleep 0.5; printf '\\033[?1002l'; sleep 30"}, Dir: t.TempDir(), Cols: 20, Rows: 5})
	if s.MouseTracking() {
		t.Fatal("MouseTracking() = true before the program asked for it")
	}
	waitUntil(t, s.MouseTracking, "mouse tracking on")
	waitUntil(t, func() bool { return !s.MouseTracking() }, "mouse tracking off")
}

// waitUntil polls cond until it holds or the timeout passes.
func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPanesRenderOnTheirOwnBackground(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "printf 'hi\\n'; sleep 30"}, Dir: t.TempDir(), Cols: 12, Rows: 3})
	waitFor(t, s, "hi")

	lines := strings.Split(s.Render(false), "\n")
	if len(lines) != 3 {
		t.Fatalf("Render gave %d lines, want one per row: %q", len(lines), lines)
	}
	for i, line := range lines {
		if !strings.Contains(line, "\x1b[48;2;42;54;74m") {
			t.Errorf("row %d carries no pane background: %q", i, line)
		}
		// The pane is a rectangle: a blank row is as wide as a full one, so
		// nothing behind the pane shows through at a short line's end.
		if w := ansi.StringWidth(line); w != 12 {
			t.Errorf("row %d is %d cells wide, want 12: %q", i, w, line)
		}
	}
}

func TestScrolledHistoryIsOnTheSameBackground(t *testing.T) {
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "i=1; while [ $i -le 30 ]; do printf 'line-%s\\n' $i; i=$((i+1)); done; sleep 30"}, Dir: t.TempDir(), Cols: 20, Rows: 5})
	waitFor(t, s, "line-30")

	lines := strings.Split(s.RenderScrolled(3), "\n")
	if len(lines) != 5 {
		t.Fatalf("RenderScrolled gave %d lines, want 5", len(lines))
	}
	for i, line := range lines {
		if !strings.Contains(line, "\x1b[48;2;42;54;74m") {
			t.Errorf("scrolled row %d carries no pane background: %q", i, line)
		}
	}
	if !strings.Contains(lines[0], "line-24") {
		t.Errorf("scrolling still has to show the right history: %q", lines[0])
	}
}

func TestTheProgramIsToldWhatGroundItIsOn(t *testing.T) {
	// A program that themes itself to its terminal asks with OSC 11. It must
	// be told tm's ground, not the emulator library's default, or it will pick
	// colours for a black background it is not actually on.
	//
	// The reply arrives on the child's stdin and the pty echoes it back to the
	// screen, which is what we wait for. (It cannot be read with the shell's
	// own "read": the reply is BEL-terminated, and POSIX read only completes a
	// line on a newline, so it would block forever.)
	s := start(t, Config{Command: "/bin/sh", Args: []string{"-c", "printf '\\033]11;?\\033\\\\'; sleep 30"}, Dir: t.TempDir(), Cols: 60, Rows: 4})

	waitFor(t, s, "rgb:2a2a/3636/4a4a")
}
