package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/Vonnue-ShijilCG/tm/internal/server"
	"github.com/Vonnue-ShijilCG/tm/internal/state"
)

// The whole point of the server, tested the way a user meets it: a real tm
// binary in a real PTY, a command that keeps printing, the window closed
// under it, and a second tm that finds the command still running. Every
// unit test of the server runs over a pipe with fake sessions; this is the
// one place the pieces — Ensure starting `tm __server`, the PTYs living in
// another process, the blob bringing the layout back — are seen together.
//
// The driver is tools/screenshot's: tm's output goes through the same vt
// emulator tm uses for its panes, and the emulator's reply side is drained
// back into the PTY, or tm's startup queries block it.
func TestServerKeepsTerminalsAcrossWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("starts real tm processes in PTYs")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newE2E(t)

	// 1. A terminal running a command that prints forever.
	a := e.open(t, "A")
	a.waitScreen(t, "the sidebar", 5*time.Second, contains("api"))
	// Enter on a project with nothing in it opens its first shell.
	a.send(t, "\r")
	a.waitScreen(t, "the shell prompt", 3*time.Second, contains("$"))
	a.send(t, "i=0; while :; do i=$((i+1)); echo tick$i; sleep 0.2; done")
	a.send(t, "\r")
	a.waitScreen(t, "the command printing", 5*time.Second, func(s string) bool { return lastTick(s) > 0 })
	time.Sleep(time.Second)
	seen := lastTick(a.screen())
	// The server catches SIGHUP rather than ignoring it, because an ignored
	// signal is inherited: the shell would ignore the hangup that ends its
	// foreground job when the terminal is killed.
	if ignored := hangupIgnoredBy(t, "/bin/sh", filepath.Join(e.root, "api")); ignored {
		t.Error("the shell the server started ignores SIGHUP")
	}

	// 2. The window closes. The terminal must not go with it.
	a.closeWindow(t)
	if live, running := server.Probe(e.paths); !running || live != 1 {
		t.Fatalf("after the window closed: running %v with %d live, want running with 1", running, live)
	}

	// 3. A new window finds it still running, and printing.
	time.Sleep(time.Second)
	b := e.open(t, "B")
	b.waitScreen(t, "ticks past "+strconv.Itoa(seen), 3*time.Second, func(s string) bool {
		return lastTick(s) > seen
	})

	// Take-over: a third window attaches and the second must go on its own.
	seen = lastTick(b.screen())
	c := e.open(t, "C")
	b.waitExit(t, 3*time.Second)
	if !strings.Contains(b.output(), "tm: opened in another window") {
		t.Errorf("the window that was taken over did not say why; its output ends:\n%s", tail(b.output()))
	}
	c.waitScreen(t, "ticks past "+strconv.Itoa(seen)+" in the new window", 3*time.Second, func(s string) bool {
		return lastTick(s) > seen
	})

	// 4. Q stops everything, and the server with it.
	c.send(t, "\x1bs")
	time.Sleep(100 * time.Millisecond)
	c.send(t, "Q")
	waitUntil(t, "the server to stop after Q", 2*time.Second, func() bool {
		_, running := server.Probe(e.paths)
		_, err := os.Stat(e.paths.Socket)
		return !running && os.IsNotExist(err)
	})
	c.waitExit(t, 2*time.Second)
	// Q saves the state file as quitting always has, so a reboot still
	// brings the terminal back.
	if f := e.state(t, e.root); len(f.Projects) != 1 || len(f.Projects[0].Terminals) != 1 {
		t.Errorf("after Q the state file should keep the terminal, got %+v", f.Projects)
	}

	// 5. A terminal that exits while no window is attached is forgotten, as
	// it would have been had the window stayed open. A workspace of its own,
	// so the terminal Q saved is not restored beside it and kept alive.
	root := e.workspace(t, "solo")
	paths, err := server.PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}
	e.track(paths)
	d := e.openIn(t, "D", root)
	d.waitScreen(t, "the sidebar", 5*time.Second, contains("solo"))
	// Enter on a project with nothing in it opens its first shell.
	d.send(t, "\r")
	d.waitScreen(t, "the shell prompt", 3*time.Second, contains("$"))
	// Long enough that the window is certainly gone before it ends; the
	// .exited check below proves it was.
	d.send(t, "sleep 2; exit")
	d.send(t, "\r")
	waitUntil(t, "the terminal to be recorded with its server ref", 3*time.Second, func() bool {
		f := e.state(t, root)
		return len(f.Projects) == 1 && len(f.Projects[0].Terminals) == 1 && f.Projects[0].Terminals[0].Ref != ""
	})
	before := d.screen()
	d.closeWindow(t)
	for deadline := time.Now().Add(5 * time.Second); ; {
		live, running := server.Probe(paths)
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server is still running with %d live after its last terminal should have exited; the window showed:\n%s", live, before)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(paths.Exited); err != nil {
		t.Fatalf("the server ended without recording the terminal that exited while detached: %v", err)
	}

	e.config(t, false)
	off := e.openIn(t, "E", root)
	off.waitScreen(t, "the sidebar", 5*time.Second, contains("solo"))
	off.send(t, "q")
	off.waitExit(t, 3*time.Second)
	if f := e.state(t, root); len(f.Projects) > 0 && len(f.Projects[0].Terminals) > 0 {
		t.Errorf("the terminal that exited while detached came back: %+v", f.Projects[0].Terminals)
	}
	if _, err := os.Stat(paths.Exited); !os.IsNotExist(err) {
		t.Errorf("%s should be gone once read: %v", paths.Exited, err)
	}
}

// e2e is one run's binary and private directories, and everything it
// started, so cleanup can end all of it however the test stops.
type e2e struct {
	bin, home, cfg string
	root           string
	paths          server.Paths

	mu      sync.Mutex
	windows []*window
	servers []server.Paths
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		gobin = filepath.Join(runtime.GOROOT(), "bin", "go")
	}
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "tm")
	if out, err := exec.Command(gobin, "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build tm: %v\n%s", err, out)
	}

	// Short, so the socket path fits in sun_path without the /tmp fallback.
	home, err := os.MkdirTemp("", "tme2e")
	if err != nil {
		t.Fatal(err)
	}
	e := &e2e{bin: bin, home: home, cfg: filepath.Join(home, "config")}
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", e.cfg)

	// Registered first, so it runs last: after every window and server is gone.
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Cleanup(func() { e.cleanup(t) })

	e.config(t, true)
	e.root = e.workspace(t, "api")
	if e.paths, err = server.PathsFor(e.root); err != nil {
		t.Fatal(err)
	}
	e.track(e.paths)
	return e
}

// workspace makes a workspace holding one committed repository named repo.
func (e *e2e) workspace(t *testing.T, repo string) string {
	t.Helper()
	root := filepath.Join(e.home, "ws-"+repo)
	dir := filepath.Join(root, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte(repo+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=tm", "-c", "user.email=tm@example.com", "add", "."},
		{"-c", "user.name=tm", "-c", "user.email=tm@example.com", "commit", "-q", "-m", "initial"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

func (e *e2e) config(t *testing.T, on bool) {
	t.Helper()
	dir := filepath.Join(e.cfg, "tm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "server = " + strconv.FormatBool(on) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *e2e) state(t *testing.T, root string) state.File {
	t.Helper()
	f, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (e *e2e) track(p server.Paths) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.servers = append(e.servers, p)
}

// cleanup kills every window, stops every server, and then kills whatever
// `tm __server` of this binary is still alive, so a failure halfway leaves
// nothing running. On failure it also logs what the servers wrote.
func (e *e2e) cleanup(t *testing.T) {
	e.mu.Lock()
	windows, servers := e.windows, e.servers
	e.mu.Unlock()
	for _, w := range windows {
		w.kill()
	}
	for _, p := range servers {
		server.StopServer(p)
		if t.Failed() {
			if log, err := os.ReadFile(p.Log); err == nil && len(log) > 0 {
				t.Logf("server log %s:\n%s", p.Log, log)
			}
		}
	}
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, path := range procs {
		cmdline, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		args := strings.Split(string(cmdline), "\x00")
		if len(args) >= 2 && args[0] == e.bin && args[1] == "__server" {
			pid, err := strconv.Atoi(filepath.Base(filepath.Dir(path)))
			if err == nil {
				t.Logf("killing a server still running at cleanup: pid %d", pid)
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

// window is one tm process in a PTY, seen through a vt emulator.
type window struct {
	name string
	cmd  *exec.Cmd
	tty  *os.File
	done chan struct{} // closed when the process has exited

	mu  sync.Mutex // the emulator and raw output are written by the reader
	emu *vt.Emulator
	raw bytes.Buffer
}

const winCols, winRows = 100, 30

func (e *e2e) open(t *testing.T, name string) *window { return e.openIn(t, name, e.root) }

func (e *e2e) openIn(t *testing.T, name, root string) *window {
	t.Helper()
	cmd := exec.Command(e.bin)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"HOME="+e.home, // no .bashrc or .profile to set a title or prompt
		"XDG_CONFIG_HOME="+e.cfg,
		"XDG_STATE_HOME="+filepath.Join(e.home, "state"),
		"SHELL=/bin/sh",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"PS1=$ ",
	)
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: winCols, Rows: winRows})
	if err != nil {
		t.Fatal(err)
	}
	w := &window{name: name, cmd: cmd, tty: tty, done: make(chan struct{}), emu: vt.NewEmulator(winCols, winRows)}
	e.mu.Lock()
	e.windows = append(e.windows, w)
	e.mu.Unlock()

	go func() { cmd.Wait(); close(w.done) }()
	// The emulator answers tm's terminal queries on its reply side; draining
	// it back into the PTY is what lets tm get past startup.
	go func() { io.Copy(tty, w.emu) }()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				w.mu.Lock()
				w.raw.Write(buf[:n])
				w.emu.Write(buf[:n])
				w.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return w
}

func (w *window) screen() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.emu.String()
}

func (w *window) output() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.raw.String()
}

func (w *window) send(t *testing.T, s string) {
	t.Helper()
	if _, err := w.tty.WriteString(s); err != nil {
		t.Fatalf("window %s: %v", w.name, err)
	}
}

func (w *window) waitScreen(t *testing.T, what string, timeout time.Duration, ok func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok(w.screen()) {
		if time.Now().After(deadline) {
			t.Fatalf("window %s: timed out after %v waiting for %s; screen:\n%s", w.name, timeout, what, w.screen())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// closeWindow is the terminal window going away: the PTY master closes,
// the kernel hangs up tm's session, and tm must exit.
func (w *window) closeWindow(t *testing.T) {
	t.Helper()
	w.tty.Close()
	select {
	case <-w.done:
	case <-time.After(3 * time.Second):
		t.Fatalf("window %s: tm did not exit when its window closed", w.name)
	}
}

// waitExit waits for tm to exit on its own. One that does not gets a
// SIGQUIT, so the goroutine dump of whatever held it lands in the log.
func (w *window) waitExit(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-w.done:
		return
	case <-time.After(timeout):
	}
	w.cmd.Process.Signal(syscall.SIGQUIT)
	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
	}
	t.Fatalf("BLOCKED: window %s did not exit within %v; output after SIGQUIT:\n%s", w.name, timeout, tail(w.output()))
}

func (w *window) kill() {
	select {
	case <-w.done:
	default:
		w.cmd.Process.Kill()
		<-w.done
	}
	w.tty.Close()
}

func waitUntil(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func contains(s string) func(string) bool {
	return func(screen string) bool { return strings.Contains(screen, s) }
}

var tickRE = regexp.MustCompile(`tick(\d+)`)

// lastTick is the largest tick number on screen, 0 when there is none.
func lastTick(screen string) int {
	n := 0
	for _, m := range tickRE.FindAllStringSubmatch(screen, -1) {
		if v, _ := strconv.Atoi(m[1]); v > n {
			n = v
		}
	}
	return n
}

// tail is the end of a window's output, which is where an exit message or
// a goroutine dump is, without the frames before it.
func tail(s string) string {
	const max = 64 * 1024
	if len(s) > max {
		return s[len(s)-max:]
	}
	return s
}

// hangupIgnoredBy reports whether a process running prog in dir ignores
// SIGHUP, from the SigIgn mask in /proc. It fails the test when there is
// no such process.
func hangupIgnoredBy(t *testing.T, prog, dir string) bool {
	t.Helper()
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, p := range procs {
		if cwd, _ := os.Readlink(filepath.Join(p, "cwd")); cwd != dir {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join(p, "cmdline"))
		if strings.Split(string(cmdline), "\x00")[0] != prog {
			continue
		}
		status, _ := os.ReadFile(filepath.Join(p, "status"))
		for _, line := range strings.Split(string(status), "\n") {
			if mask, ok := strings.CutPrefix(line, "SigIgn:"); ok {
				bits, err := strconv.ParseUint(strings.TrimSpace(mask), 16, 64)
				if err != nil {
					t.Fatalf("%s: %v", line, err)
				}
				return bits&(1<<(syscall.SIGHUP-1)) != 0
			}
		}
	}
	t.Fatalf("no %s running in %s", prog, dir)
	return false
}
