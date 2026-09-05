package terminal

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
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
