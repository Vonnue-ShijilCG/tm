package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/config"
	"github.com/Vonnue-ShijilCG/tm/internal/server"
	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
)

// Running tm inside a project is the first thing anybody tries, because every
// other developer tool is run there. The message has to say what tm wanted
// instead, and — since it can see them — that the repositories are one
// directory up.
func TestInsideARepositoryTheMessagePointsAtTheDirectoryAbove(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for _, name := range []string{"api", "web"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	err := explainNoProjects(filepath.Join(root, "api"))

	if err == nil {
		t.Fatal("a directory with no repositories in it should be explained")
	}
	msg := err.Error()
	for _, want := range []string{"api is a repository", "holds 2", "cd .. && tm"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

// A directory that is neither a repository nor holds any gets the shape tm
// expects, since there is nothing better to point at.
func TestAnEmptyDirectoryGetsTheLayoutTmExpects(t *testing.T) {
	err := explainNoProjects(t.TempDir())

	if err == nil {
		t.Fatal("an empty directory should be explained")
	}
	msg := err.Error()
	for _, want := range []string{"no Git repositories in", "run tm here", "a repository"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "cd ..") {
		t.Errorf("there are no repositories above to suggest:\n%s", msg)
	}
}

// A lone repository still has somewhere to point: its own directory holds one
// repository — itself — and tm there shows a sidebar of one. The count is
// written out, so it has to read properly at one as well as at six.
func TestTheCountInTheSuggestionReadsProperlyAtOne(t *testing.T) {
	root := t.TempDir()
	lone := filepath.Join(root, "lone")
	if err := os.MkdirAll(filepath.Join(lone, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	msg := explainNoProjects(lone).Error()

	if !strings.Contains(msg, "holds 1 repository:") {
		t.Errorf("want \"holds 1 repository\", not a plural:\n%s", msg)
	}
	if !strings.Contains(msg, "cd .. && tm") {
		t.Errorf("going up would show that one repository, so it should be suggested:\n%s", msg)
	}
}

// idleSession is a server.Session that does nothing until it is closed.
type idleSession struct {
	output chan struct{}
	done   chan struct{}
	once   sync.Once
}

func spawnIdle(terminal.Config) (server.Session, error) {
	return &idleSession{output: make(chan struct{}), done: make(chan struct{})}, nil
}

func (s *idleSession) SendKey(uv.KeyPressEvent)  {}
func (s *idleSession) SendText(string)           {}
func (s *idleSession) Resize(int, int)           {}
func (s *idleSession) Render(bool) string        { return "" }
func (s *idleSession) RenderScrolled(int) string { return "" }
func (s *idleSession) Version() uint64           { return 0 }
func (s *idleSession) Lines(int) []string        { return nil }
func (s *idleSession) ScrollbackLen() int        { return 0 }
func (s *idleSession) IsAltScreen() bool         { return false }
func (s *idleSession) MouseTracking() bool       { return false }
func (s *idleSession) SendWheel(bool, int, int)  {}
func (s *idleSession) Title() string             { return "" }
func (s *idleSession) Output() <-chan struct{}   { return s.output }
func (s *idleSession) Done() <-chan struct{}     { return s.done }
func (s *idleSession) Err() error                { return nil }
func (s *idleSession) Close() error              { s.once.Do(func() { close(s.done) }); return nil }

// workspace makes a directory holding one repository, makes it the working
// directory, and gives tm a private config and state directory. The state
// directory is kept short so the socket path fits in sun_path.
func workspace(t *testing.T) string {
	t.Helper()
	state, err := os.MkdirTemp("", "tmst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(state) })
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "api", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

// serveWith starts a server on root's socket holding n sessions, attached
// to by a client that stays connected. The server is stopped at cleanup.
func serveWith(t *testing.T, root string, n int) server.Paths {
	t.Helper()
	paths, err := server.PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}
	l, err := server.Listen(paths)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(spawnIdle, paths.Exited)
	served := make(chan struct{})
	go func() { srv.Serve(l); close(served) }()
	t.Cleanup(func() {
		server.StopServer(paths)
		<-served
	})

	client, _, err := server.Attach(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	for range n {
		if _, err := client.Spawn(root, 80, 24, nil); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func TestStopWithNoServer(t *testing.T) {
	workspace(t)
	var out bytes.Buffer

	if err := run([]string{"stop"}, &out); err != nil {
		t.Fatalf("tm stop with nothing running is not a failure: %v", err)
	}
	if got, want := out.String(), "no tm server running here\n"; got != want {
		t.Errorf("tm stop printed %q, want %q", got, want)
	}
}

func TestStopEndsTheServerAndCountsItsTerminals(t *testing.T) {
	root := workspace(t)
	paths := serveWith(t, root, 2)
	var out bytes.Buffer

	if err := run([]string{"stop"}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "stopped 2 terminals\n"; got != want {
		t.Errorf("tm stop printed %q, want %q", got, want)
	}
	if _, running := server.Probe(paths); running {
		t.Error("the server is still running after tm stop")
	}
}

// With the server setting off, tm must not start a second copy of every
// terminal a running server already holds.
func TestOffModeRefusesWhenServerRuns(t *testing.T) {
	root := workspace(t)
	serveWith(t, root, 1)

	// The guard itself, not run: a guard that regressed would have run
	// start a real Bubble Tea program and hang instead of failing.
	_, err := offModeGuard(root)

	want := "a tm server is running here with 1 terminal: set server = true in " +
		config.Display(config.Path()) + " to reattach, or run tm stop"
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

func TestTerminalsReadsProperlyAtOneAndMany(t *testing.T) {
	for n, want := range map[int]string{0: "0 terminals", 1: "1 terminal", 2: "2 terminals"} {
		if got := terminals(n); got != want {
			t.Errorf("terminals(%d) = %q, want %q", n, got, want)
		}
	}
}

// The hidden server outlives the terminal that started it, so a hangup must
// not end it; SIGTERM must, and through the same stop as tm stop.
func TestServerSurvivesHangupAndStopsOnTerm(t *testing.T) {
	root := workspace(t)
	paths, err := server.PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- run([]string{"__server", root}, io.Discard) }()
	waitFor(t, func() bool { _, running := server.Probe(paths); return running })

	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, running := server.Probe(paths); !running {
		t.Fatal("SIGHUP stopped the server")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("a server stopped by SIGTERM returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not stop the server")
	}
	if _, err := os.Stat(paths.Socket); !os.IsNotExist(err) {
		t.Errorf("the socket is still there after the server stopped: %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// serveOtherVersion answers like a server of another protocol version: a
// probe or tm stop gets its count, an attach gets only its protocol number.
func serveOtherVersion(t *testing.T, root string, live int) server.Paths {
	t.Helper()
	paths, err := server.PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}
	l, err := server.Listen(paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	const other = 99
	if other == server.ProtocolVersion {
		t.Fatal("pick another protocol number for the other version")
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			conn := server.NewConn(c)
			if m, err := conn.Recv(); err == nil && m.Hello != nil {
				if m.Hello.Attach {
					conn.Send(server.Msg{Welcome: &server.Welcome{Protocol: other}})
				} else {
					conn.Send(server.Msg{Status: &server.Status{Live: live}})
				}
			}
			conn.Close()
		}
	}()
	return paths
}

func TestAServerOfAnotherVersionIsNamedWithItsCount(t *testing.T) {
	root := workspace(t)
	serveOtherVersion(t, root, 2)

	_, _, _, err := connect(root)

	want := "a tm server from another tm version is running here with 2 terminals; run tm stop to end them, then start tm again"
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

// Terminals that ended while no window was attached are reported by the
// attach that succeeds; a start that fails must leave them for the next.
func TestAFailedAttachLeavesTheExitedTerminalsForTheNextStart(t *testing.T) {
	root := workspace(t)
	paths := serveOtherVersion(t, root, 0)
	if err := os.WriteFile(paths.Exited, []byte("0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := connect(root); err == nil {
		t.Fatal("attaching to another version should fail")
	}

	if got := server.TakeExited(paths); len(got) != 1 || got[0] != "0123456789abcdef" {
		t.Errorf("exited refs after a failed attach = %q, want the one written", got)
	}
}

// A server that is exiting on its own when tm starts still answers Ensure's
// probe, then closes the attach. tm waits for it to go and starts a fresh
// one, rather than failing with "tm server did not answer".
func TestAttachRetriesPastAServerThatWasExiting(t *testing.T) {
	root := workspace(t)
	paths, err := server.PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}
	l, err := server.Listen(paths)
	if err != nil {
		t.Fatal(err)
	}
	var gone atomic.Bool
	go func() { // the dying server: counts, refuses attaches, then goes
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			conn := server.NewConn(c)
			if m, err := conn.Recv(); err == nil && m.Hello != nil && !m.Hello.Attach {
				conn.Send(server.Msg{Status: &server.Status{}})
			} else {
				time.AfterFunc(100*time.Millisecond, func() { gone.Store(true); l.Close() })
			}
			conn.Close()
		}
	}()

	ensures := 0
	ensure := func() error {
		ensures++
		if ensures == 1 {
			return nil // found the dying one running
		}
		if !gone.Load() {
			t.Error("started a server before the dying one had gone")
		}
		nl, err := server.Listen(paths)
		if err != nil {
			return err
		}
		srv := server.New(spawnIdle, paths.Exited)
		served := make(chan struct{})
		go func() { srv.Serve(nl); close(served) }()
		t.Cleanup(func() { server.StopServer(paths); <-served })
		return nil
	}

	client, _, err := attachFresh(paths, ensure)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	client.Close()
	if ensures != 2 {
		t.Errorf("Ensure ran %d times, want 2", ensures)
	}
}

// Another version is not a server on its way out: no retry.
func TestAttachDoesNotRetryAnotherVersion(t *testing.T) {
	root := workspace(t)
	paths := serveOtherVersion(t, root, 1)
	ensures := 0
	_, _, err := attachFresh(paths, func() error { ensures++; return nil })
	if !errors.Is(err, server.ErrProtocol) || ensures != 1 {
		t.Errorf("err %v after %d Ensures, want ErrProtocol after 1", err, ensures)
	}
}
