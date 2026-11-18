package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrNotRunning is StopServer's answer when no server is listening.
var ErrNotRunning = errors.New("no tm server is running here")

const (
	// dialTimeout and replyTimeout bound how long a probe can hang on a
	// server that accepted the connection but never answers.
	dialTimeout  = time.Second
	replyTimeout = 2 * time.Second
	// startTimeout is how long Ensure waits for a new server to answer.
	startTimeout = 3 * time.Second
	// stopTimeout is how long StopServer waits for the server to go.
	stopTimeout = 5 * time.Second
	// pollInterval is how often Ensure checks whether the server is up.
	pollInterval = 20 * time.Millisecond
)

// status asks the server at p for its live session count with
// Hello{Attach: false}, which by ruling R1 leaves any attached client alone.
// It returns the open connection so StopServer can follow with Stop. timeout
// bounds the wait for the reply.
func status(p Paths, timeout time.Duration) (*Conn, int, error) {
	c, err := net.DialTimeout("unix", p.Socket, min(dialTimeout, timeout))
	if err != nil {
		return nil, 0, err
	}
	conn := NewConn(c)
	c.SetDeadline(time.Now().Add(timeout))
	if err := conn.Send(Msg{Hello: &Hello{Protocol: ProtocolVersion}}); err != nil {
		conn.Close()
		return nil, 0, err
	}
	m, err := conn.Recv()
	if err != nil {
		conn.Close()
		return nil, 0, err
	}
	c.SetDeadline(time.Time{})
	switch {
	case m.Status != nil:
		return conn, m.Status.Live, nil
	case m.Welcome != nil && m.Welcome.Protocol != ProtocolVersion:
		// A server of another version answers with only its protocol and
		// closes (ruling R2). It is running; it just will not talk to us.
		conn.Close()
		return nil, 0, errOtherVersion
	default:
		conn.Close()
		return nil, 0, errors.New("tm server sent an unexpected reply")
	}
}

var errOtherVersion = errors.New("a tm server from another tm version is running here")

// isDialError reports whether err means nothing is listening: no socket
// file, or a stale one left by a server that died. Any other failure came
// from a connection a server accepted, which proves one is alive.
func isDialError(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// probe is Probe with the handshake's error: running with a non-nil err is
// a server that accepted the connection but did not answer properly. A
// server of another protocol version is running with no error; telling the
// user about the mismatch is the attaching client's job.
func probe(p Paths, timeout time.Duration) (live int, running bool, err error) {
	conn, n, err := status(p, timeout)
	switch {
	case err == nil:
		conn.Close()
		return n, true, nil
	case errors.Is(err, errOtherVersion):
		return 0, true, nil
	case isDialError(err):
		return 0, false, err
	default:
		return 0, true, err
	}
}

// Probe reports whether a server is on p's socket and how many live
// sessions it holds. Only a dial error reads as not running. A server that
// accepted the connection but was slow or answered oddly, or one of another
// protocol version, is running with an unknown count of 0.
func Probe(p Paths) (live int, running bool) {
	live, running, _ = probe(p, replyTimeout)
	return live, running
}

// Ensure makes sure a server is running for the workspace at root, starting
// `exe __server root` if none answers. The lock is held across probing,
// starting and waiting for the new server to answer, so concurrent callers
// serialise: the first starts the server, the rest find it running.
//
// A server that accepts the connection but does not answer is an error, not
// a reason to start another: a second server would take over the socket and
// orphan the first one's sessions.
//
// The child is a session leader (setsid) in / with stdin on /dev/null and
// stdout and stderr appended to p.Log, so it outlives the terminal that
// started it and pins no directory. Ensure does not wait for it to exit.
func Ensure(p Paths, exe, root string) error {
	lock, err := os.OpenFile(p.Lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close() // releases the flock
	if err := flock(lock); err != nil {
		return fmt.Errorf("locking %s: %w", p.Lock, err)
	}

	if _, running, err := probe(p, replyTimeout); running {
		if err != nil {
			return fmt.Errorf("a tm server is running here but not answering (%v); see %s", err, p.Log)
		}
		return nil
	}

	exited, err := start(p, exe, root)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(startTimeout)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return fmt.Errorf("tm server did not start within %v; see %s", startTimeout, p.Log)
		}
		if _, running, err := probe(p, left); running && err == nil {
			return nil
		}
		select {
		case err := <-exited:
			how := "exited"
			if err != nil {
				how = err.Error()
			}
			return fmt.Errorf("tm server stopped before it was ready (%s); see %s", how, p.Log)
		case <-time.After(min(pollInterval, max(time.Until(deadline), 0))):
		}
	}
}

// start launches the server detached. The returned channel yields the
// child's exit, which is reaped in the background so a server that stops
// while this tm is still open does not linger as a zombie.
func start(p Paths, exe, root string) (<-chan error, error) {
	log, err := os.OpenFile(p.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer null.Close()

	// Both paths are resolved before the child moves to /, where a relative
	// one would name something else.
	if abs, err := filepath.Abs(exe); err == nil && strings.ContainsRune(exe, '/') {
		exe = abs
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	cmd := exec.Command(exe, "__server", root)
	// In /, so the server does not pin the directory tm was started from:
	// it may run for days, and that directory may be one to unmount or
	// delete.
	cmd.Dir = "/"
	cmd.Stdin = null
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the tm server: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}

func flock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			return err
		}
	}
}

// StopServer asks the server at p to end every session and exit, and waits
// for it to close the connection. It returns the number of live sessions the
// server held before stopping, or ErrNotRunning when none is listening.
func StopServer(p Paths) (stopped int, err error) {
	conn, n, err := status(p, replyTimeout)
	if err != nil {
		if isDialError(err) {
			return 0, ErrNotRunning
		}
		return 0, err
	}
	defer conn.Close()
	if err := conn.Send(Msg{Stop: &Stop{}}); err != nil {
		return n, err
	}
	conn.c.SetDeadline(time.Now().Add(stopTimeout))
	for {
		if _, err := conn.Recv(); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return n, fmt.Errorf("tm server did not stop within %v", stopTimeout)
			}
			return n, nil // closed: the server is gone
		}
	}
}

// TakeExited returns the refs of sessions that ended while no client was
// attached, one per line in p.Exited, and removes the file so they are
// reported once. The file is renamed aside before it is read, so two tm
// started together cannot both take the same refs. It returns nil when
// there is no file.
func TakeExited(p Paths) []string {
	taken := p.Exited + ".taken"
	if err := os.Rename(p.Exited, taken); err != nil {
		return nil
	}
	b, err := os.ReadFile(taken)
	os.Remove(taken)
	if err != nil {
		return nil
	}
	var refs []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			refs = append(refs, line)
		}
	}
	return refs
}
