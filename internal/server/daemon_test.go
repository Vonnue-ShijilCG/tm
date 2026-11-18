package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for `tm __server <root>`: with
// TM_TEST_SERVER=1 it records its pid and runs the real server on the
// root's socket until killed. Ensure is then exercised against a real,
// detached child process.
func TestMain(m *testing.M) {
	if os.Getenv("TM_TEST_SERVER") == "1" && len(os.Args) == 3 && os.Args[1] == "__server" {
		os.Exit(testServer(os.Args[2]))
	}
	os.Exit(m.Run())
}

func testServer(root string) int {
	p, err := PathsFor(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	f, err := os.OpenFile(os.Getenv("TM_TEST_PIDFILE"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintln(f, os.Getpid())
	f.Close()
	// Linger before listening so concurrent Ensure callers really do have
	// to wait for the socket rather than finding it at once.
	time.Sleep(100 * time.Millisecond)
	ln, err := Listen(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// A safety net in case a test dies before its cleanup kills us.
	time.AfterFunc(30*time.Second, func() { os.Exit(2) })
	if err := New(DefaultSpawner, p.Exited).Serve(ln); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// fakeServer speaks ruling R1: Hello{Attach:false} is answered with
// Status{Live}, and a Stop that follows on the same connection is recorded
// and ends the server.
type fakeServer struct {
	live    int
	stopped atomic.Int32
	hellos  atomic.Int32
	ln      net.Listener
}

func (f *fakeServer) serve(ln net.Listener) {
	f.ln = ln
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go f.handle(NewConn(c))
	}
}

func (f *fakeServer) handle(c *Conn) {
	defer c.Close()
	m, err := c.Recv()
	if err != nil || m.Hello == nil || m.Hello.Attach {
		return
	}
	f.hellos.Add(1)
	if err := c.Send(Msg{Status: &Status{Live: f.live}}); err != nil {
		return
	}
	m, err = c.Recv()
	if err != nil || m.Stop == nil {
		return
	}
	f.stopped.Add(1)
	f.ln.Close()
}

func startFake(t *testing.T, live int) (Paths, *fakeServer) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := PathsFor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeServer{live: live}
	go f.serve(ln)
	return p, f
}

func TestProbeReportsLiveSessions(t *testing.T) {
	p, f := startFake(t, 3)
	live, running := Probe(p)
	if !running || live != 3 {
		t.Fatalf("Probe = %d, %v; want 3, true", live, running)
	}
	if f.stopped.Load() != 0 {
		t.Fatal("Probe stopped the server")
	}
}

func TestProbeWithNothingListening(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := PathsFor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, running := Probe(p); running {
		t.Fatal("no socket file read as running")
	}

	// A socket file left behind by a server that died.
	ln, err := net.Listen("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err := os.Stat(p.Socket); err != nil {
		t.Fatalf("stale socket not left behind: %v", err)
	}
	if _, running := Probe(p); running {
		t.Fatal("a stale socket read as running")
	}
}

func TestStopServerReturnsLiveAndStops(t *testing.T) {
	p, f := startFake(t, 2)
	n, err := StopServer(p)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("StopServer = %d, want 2", n)
	}
	if f.stopped.Load() != 1 {
		t.Fatalf("server saw %d stops, want 1", f.stopped.Load())
	}
	if _, running := Probe(p); running {
		t.Fatal("server still running after StopServer")
	}
}

func TestStopServerWithNothingRunning(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := PathsFor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := StopServer(p); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("StopServer err = %v, want ErrNotRunning", err)
	}
}

func TestEnsureLeavesARunningServerAlone(t *testing.T) {
	p, f := startFake(t, 1)
	// exe would fail if it were ever run.
	if err := Ensure(p, "/nonexistent/tm", "/nowhere"); err != nil {
		t.Fatal(err)
	}
	if f.hellos.Load() == 0 {
		t.Fatal("Ensure did not probe")
	}
}

func TestEnsureReportsAServerThatNeverListens(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := PathsFor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = Ensure(p, "/bin/true", "/nowhere")
	if err == nil {
		t.Fatal("Ensure succeeded with a server that exits at once")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("Ensure waited %v for a child that had already exited", time.Since(start))
	}
	if !strings.Contains(err.Error(), p.Log) {
		t.Fatalf("error %q does not name the log", err)
	}
	if strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("error %q prints a nil exit error", err)
	}
}

// startSilent listens on a fresh workspace's socket and accepts connections
// but never answers: a server that is alive but wedged or busy.
func startSilent(t *testing.T) Paths {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := PathsFor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	return p
}

func TestProbeCountsASilentServerAsRunning(t *testing.T) {
	p := startSilent(t)
	if live, running := Probe(p); !running || live != 0 {
		t.Fatalf("Probe = %d, %v; want 0, true", live, running)
	}
}

func TestEnsureDoesNotStartASecondServerBesideASilentOne(t *testing.T) {
	p := startSilent(t)
	pidFile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("TM_TEST_SERVER", "1")
	t.Setenv("TM_TEST_PIDFILE", pidFile)
	t.Cleanup(func() {
		for _, pid := range readPids(t, pidFile) {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	err := Ensure(p, os.Args[0], t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "running here but not answering") {
		t.Fatalf("Ensure err = %v, want a not-answering error", err)
	}
	if !strings.Contains(err.Error(), p.Log) {
		t.Fatalf("error %q does not name the log", err)
	}
	if pids := readPids(t, pidFile); len(pids) != 0 {
		t.Fatalf("Ensure started %d servers beside a running one", len(pids))
	}
}

func TestEnsureServerStartsOnlyOne(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pidFile := filepath.Join(t.TempDir(), "pids")
	t.Setenv("TM_TEST_SERVER", "1")
	t.Setenv("TM_TEST_PIDFILE", pidFile)
	t.Cleanup(func() {
		for _, pid := range readPids(t, pidFile) {
			syscall.Kill(pid, syscall.SIGKILL)
		}
	})

	root := t.TempDir()
	p, err := PathsFor(root)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = Ensure(p, os.Args[0], root)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Ensure %d: %v", i, err)
		}
	}
	if _, running := Probe(p); !running {
		t.Fatal("no server running after Ensure")
	}
	if pids := readPids(t, pidFile); len(pids) != 1 {
		t.Fatalf("started %d servers (%v), want 1", len(pids), pids)
	}

	// The child is its own session leader, so closing this terminal or
	// killing the test's process group does not take it down.
	pid := readPids(t, pidFile)[0]
	sid, err := getsid(pid)
	if err != nil {
		t.Fatal(err)
	}
	if sid != pid {
		t.Fatalf("server session id = %d, want its own pid %d", sid, pid)
	}
}

func getsid(pid int) (int, error) {
	sid, _, errno := syscall.RawSyscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
	if errno != 0 {
		return 0, errno
	}
	return int(sid), nil
}

func readPids(t *testing.T, path string) []int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("pid file holds %q", f)
		}
		pids = append(pids, n)
	}
	return pids
}

func TestTakeExitedReadsOnce(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := PathsFor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := TakeExited(p); got != nil {
		t.Fatalf("absent file gave %v", got)
	}
	if err := os.WriteFile(p.Exited, []byte("0123456789abcdef\nfedcba9876543210\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := TakeExited(p)
	want := []string{"0123456789abcdef", "fedcba9876543210"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TakeExited = %q, want %q", got, want)
	}
	if again := TakeExited(p); again != nil {
		t.Fatalf("second TakeExited = %q, want nil", again)
	}
	if _, err := os.Stat(p.Exited); !os.IsNotExist(err) {
		t.Fatalf("exited file still there: %v", err)
	}
}
