package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
)

// Session is what the server needs from a running terminal. It is
// structurally identical to workspace.Session; *terminal.Session satisfies
// it and tests use fakes.
type Session interface {
	SendKey(uv.KeyPressEvent)
	SendText(string)
	Resize(cols, rows int)
	Render(showCursor bool) string
	RenderScrolled(offset int) string
	Version() uint64
	Lines(offset int) []string
	ScrollbackLen() int
	IsAltScreen() bool
	MouseTracking() bool
	SendWheel(up bool, x, y int)
	Title() string
	Output() <-chan struct{}
	Done() <-chan struct{}
	Err() error
	Close() error
}

var _ Session = (*terminal.Session)(nil)

// Spawner starts a session.
type Spawner func(terminal.Config) (Session, error)

// DefaultSpawner runs c in a PTY, with the user's shell when c names no
// command.
func DefaultSpawner(c terminal.Config) (Session, error) {
	if c.Command == "" {
		c.Command = terminal.DefaultShell()
	}
	s, err := terminal.Start(c)
	if err != nil {
		return nil, err
	}
	return s, nil
}

const (
	// frameWindow is the throttle on pushed frames, the same window
	// ui.watchTerminal waits out before waking the UI.
	frameWindow = 16 * time.Millisecond
	// sendTimeout bounds one write to a client; a client that stops
	// reading for this long is detached.
	sendTimeout = 10 * time.Second
	// acceptBackoff doubles up to maxAcceptBackoff while Accept keeps
	// failing.
	acceptBackoff    = 10 * time.Millisecond
	maxAcceptBackoff = time.Second
	// maxSize caps the columns and rows a client may ask a session for.
	maxSize = 1000
	// evictWait is how long Serve lets a stopped client's Evict drain
	// before closing every connection.
	evictWait = time.Second
)

// startGrace is how long a server that has never had a client waits for
// one. Without it a tm that started a server and then died before attaching
// would leave the server running with nothing in it.
var startGrace = 10 * time.Second

// Server owns a workspace's sessions and serves one attached client at a
// time. Everything below mu is guarded by it; Session methods that may
// block and every write to a connection happen outside it. Version is
// called under it: it is an atomic read.
type Server struct {
	spawn      Spawner
	exitedPath string

	mu         sync.Mutex
	sessions   map[string]*session
	order      []*session // spawn order, for Welcome
	client     *client
	blob       []byte
	conns      map[*Conn]struct{}
	stopping   bool     // Serve is about to return
	exitRefs   []string // written to exitedPath when Serve returns
	lostExits  []string // Exits queued for a client that left unwritten
	stopClient *client  // evicted by Stop; Serve lets its Evict drain

	done     chan struct{} // closed once the server decides to exit
	doneOnce sync.Once
	closed   chan struct{} // closed when Serve has finished its cleanup
	wg       sync.WaitGroup
}

type session struct {
	ref    string
	s      Session
	meta   []byte
	redraw chan struct{} // Resize or Focus: push a frame

	// guarded by Server.mu
	cursor    bool
	exited    bool
	err       string
	detachVer uint64 // Version() when the last client detached

	render   sync.Mutex // serialises frames, so versions follow content
	frameVer uint64     // guarded by render
}

// New returns a server that starts sessions with spawn and, when it exits
// on its own, appends to exitedPath the refs of sessions no client was
// told had exited.
func New(spawn Spawner, exitedPath string) *Server {
	return &Server{
		spawn:      spawn,
		exitedPath: exitedPath,
		sessions:   map[string]*session{},
		conns:      map[*Conn]struct{}{},
		done:       make(chan struct{}),
		closed:     make(chan struct{}),
	}
}

// Serve accepts connections on l until the server decides to exit — no
// live sessions and no client, or Stop — and then returns nil. It returns
// the error if l fails for another reason.
func (s *Server) Serve(l net.Listener) error {
	grace := time.AfterFunc(startGrace, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.maybeFinish()
	})
	defer grace.Stop()
	go func() {
		<-s.done
		l.Close()
	}()

	var serveErr error
	backoff := acceptBackoff
	for {
		c, err := l.Accept()
		if err != nil {
			select {
			case <-s.done:
			default:
				if !errors.Is(err, net.ErrClosed) {
					// e.g. out of file descriptors: wait, longer each time.
					time.Sleep(backoff)
					backoff = min(2*backoff, maxAcceptBackoff)
					continue
				}
				// Closed by someone else: nothing can reach these sessions
				// any more, so end them as Stop would.
				serveErr = err
				s.stop()
			}
			break
		}
		backoff = acceptBackoff
		conn := NewConn(c)
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go s.handle(conn)
	}

	s.mu.Lock()
	refs, evicted := s.exitRefs, s.stopClient
	s.mu.Unlock()
	if len(refs) > 0 {
		appendExited(s.exitedPath, refs)
	}
	if evicted != nil {
		select {
		case <-evicted.done:
		case <-time.After(evictWait):
		}
	}
	close(s.closed)
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return serveErr
}

// appendExited opens the file for each write rather than holding it:
// TakeExited renames it aside to read it.
func appendExited(path string, refs []string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tm server: recording exited sessions: %v\n", err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(refs, "\n") + "\n"); err != nil {
		fmt.Fprintf(os.Stderr, "tm server: recording exited sessions: %v\n", err)
	}
}

// finish makes Serve return. The caller has set exitRefs if it wants them.
func (s *Server) finish() {
	s.doneOnce.Do(func() { close(s.done) })
}

// maybeFinish ends the server when nothing is left for it to do. Called
// with mu held.
func (s *Server) maybeFinish() {
	if s.stopping || s.client != nil || s.live() > 0 {
		return
	}
	s.stopping = true
	s.exitRefs = s.exitedRefs()
	s.finish()
}

func (s *Server) live() int {
	n := 0
	for _, ss := range s.order {
		if !ss.exited {
			n++
		}
	}
	return n
}

func (s *Server) exitedRefs() []string {
	var refs []string
	for _, ss := range s.order {
		if ss.exited {
			refs = append(refs, ss.ref)
		}
	}
	return append(refs, s.lostExits...)
}

// remove drops ss from the list. Called with mu held.
func (s *Server) remove(ss *session) {
	delete(s.sessions, ss.ref)
	for i, o := range s.order {
		if o == ss {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// recordDetach notes each session's version as a client leaves, so the
// next Welcome can say which printed meanwhile. Called with mu held.
func (s *Server) recordDetach() {
	for _, ss := range s.order {
		ss.detachVer = ss.s.Version()
	}
}

// stop closes every session, evicts the client and makes Serve return.
func (s *Server) stop() {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	// Sessions that ended on their own before the stop are reported like
	// any other; the ones the stop kills are not, as with Q today.
	s.exitRefs = s.exitedRefs()
	var live []*session
	for _, ss := range s.order {
		if !ss.exited {
			live = append(live, ss)
		}
	}
	c := s.client
	s.client = nil
	s.stopClient = c
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, ss := range live {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ss.s.Close()
		}()
	}
	wg.Wait()
	if c != nil {
		c.evict("stopped by tm stop")
	}
	s.finish()
}

// handle serves one connection. Its first Hello decides what it is: a
// probe (Attach false), a client of another version, or the new client.
func (s *Server) handle(conn *Conn) {
	defer s.wg.Done()
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()

	var hello *Hello
	for hello == nil {
		m, err := conn.Recv()
		if err != nil {
			return
		}
		hello = m.Hello
	}
	switch {
	case !hello.Attach:
		// Hello, Status and Stop are frozen (ruling R2b): answered whatever
		// the protocol, so any tm can count and stop any server.
		s.mu.Lock()
		live := s.live()
		s.mu.Unlock()
		if sendNow(conn, Msg{Status: &Status{Live: live}}) != nil {
			return
		}
		for {
			m, err := conn.Recv()
			if err != nil {
				return
			}
			if m.Stop != nil {
				s.stop()
				<-s.closed
				return
			}
		}
	case hello.Protocol != ProtocolVersion:
		sendNow(conn, Msg{Welcome: &Welcome{Protocol: ProtocolVersion}})
		return
	}

	c := s.attach(conn)
	if c == nil {
		return
	}
	defer s.detach(c)
	for {
		m, err := conn.Recv()
		if err != nil {
			return
		}
		if m.Stop != nil {
			s.stop()
			<-s.closed
			return
		}
		s.dispatch(c, m)
	}
}

// sendNow writes to a connection that has no client writer.
func sendNow(conn *Conn, m Msg) error {
	conn.c.SetWriteDeadline(time.Now().Add(sendTimeout))
	return conn.Send(m)
}

// attach makes conn the client, evicting any other, and queues its
// Welcome followed by a frame per live session. It returns nil when the
// server is stopping.
func (s *Server) attach(conn *Conn) *client {
	c := newClient(conn)
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil
	}
	if old := s.client; old != nil {
		old.evict("opened in another window")
		s.recordDetach()
	}
	s.client = c
	w := &Welcome{Protocol: ProtocolVersion, Blob: s.blob, Live: s.live()}
	var live []*session
	for _, ss := range s.order {
		w.Sessions = append(w.Sessions, SessionInfo{
			Ref:     ss.ref,
			Meta:    ss.meta,
			Exited:  ss.exited,
			Err:     ss.err,
			Changed: !ss.exited && ss.s.Version() != ss.detachVer,
		})
		if !ss.exited {
			live = append(live, ss)
		}
	}
	// A Welcome carries an exited session once; after it, it is gone.
	for _, ss := range s.order {
		if ss.exited {
			delete(s.sessions, ss.ref)
		}
	}
	s.order = live
	// Queued under mu, so nothing reaches this client before its Welcome.
	c.push(Msg{Welcome: w})
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		c.run()
	}()
	for _, ss := range live {
		s.pushFrame(ss)
	}
	return c
}

// detach forgets c if it is still the client.
func (s *Server) detach(c *client) {
	c.shutdown()
	c.conn.Close() // so a write stalled on a full socket fails now
	<-c.done       // run has dropped what it will never write
	s.mu.Lock()
	defer s.mu.Unlock()
	// Taken under mu with the check below, so an Exit queued after this
	// went to a client that is no longer s.client and stayed listed.
	s.lostExits = append(s.lostExits, c.takeLost()...)
	if s.client != c {
		return
	}
	s.client = nil
	s.recordDetach()
	s.maybeFinish()
}

// lookup returns the session ref names if c is still the client.
func (s *Server) lookup(c *client, ref string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != c {
		return nil
	}
	return s.sessions[ref]
}

// dispatch applies one message from the attached client. Anything a
// server should not receive, or a message naming no session it holds, is
// ignored.
func (s *Server) dispatch(c *client, m Msg) {
	switch {
	case m.Spawn != nil:
		s.spawnFor(c, m.Spawn)
	case m.Input != nil:
		ss := s.lookup(c, m.Input.Ref)
		if ss == nil {
			return
		}
		if m.Input.Key != nil {
			ss.s.SendKey(*m.Input.Key)
		}
		if m.Input.Text != "" {
			ss.s.SendText(m.Input.Text)
		}
		if w := m.Input.Wheel; w != nil {
			ss.s.SendWheel(w.Up, w.X, w.Y)
		}
	case m.Resize != nil:
		if ss := s.lookup(c, m.Resize.Ref); ss != nil && m.Resize.Cols > 0 && m.Resize.Rows > 0 {
			ss.s.Resize(min(m.Resize.Cols, maxSize), min(m.Resize.Rows, maxSize))
			ss.poke()
		}
	case m.Focus != nil:
		s.mu.Lock()
		ss := s.sessions[m.Focus.Ref]
		changed := s.client == c && ss != nil && ss.cursor != m.Focus.Cursor
		if changed {
			ss.cursor = m.Focus.Cursor
		}
		s.mu.Unlock()
		if changed {
			ss.poke()
		}
	case m.Kill != nil:
		if ss := s.lookup(c, m.Kill.Ref); ss != nil {
			go ss.s.Close() // the watcher reports the exit
		}
	case m.Query != nil:
		q := m.Query
		a := &Answer{Req: q.Req}
		if ss := s.lookup(c, q.Ref); ss != nil {
			if q.Lines {
				a.Lines = ss.s.Lines(max(q.Offset, 0))
			} else {
				a.Screen = ss.s.RenderScrolled(max(q.Offset, 0))
			}
		}
		c.push(Msg{Answer: a})
	case m.SetBlob != nil:
		s.mu.Lock()
		if s.client == c {
			s.blob = append([]byte(nil), m.SetBlob.Blob...)
		}
		s.mu.Unlock()
	}
}

func (s *Server) spawnFor(c *client, sp *Spawn) {
	sess, err := s.spawn(terminal.Config{
		Command: terminal.DefaultShell(),
		Dir:     sp.Dir,
		Env:     sp.Env,
		Cols:    min(sp.Cols, maxSize),
		Rows:    min(sp.Rows, maxSize),
	})
	if err != nil {
		c.push(Msg{Spawned: &Spawned{Req: sp.Req, Err: err.Error()}})
		return
	}
	ss := &session{s: sess, meta: sp.Meta, redraw: make(chan struct{}, 1)}
	s.mu.Lock()
	// A client evicted while this spawn ran would leave a session that the
	// client now attached was never told about.
	if s.stopping || s.client != c {
		s.mu.Unlock()
		sess.Close()
		return
	}
	ss.ref = s.newRef()
	ss.detachVer = sess.Version()
	s.sessions[ss.ref] = ss
	s.order = append(s.order, ss)
	c.push(Msg{Spawned: &Spawned{Req: sp.Req, Ref: ss.ref}})
	s.mu.Unlock()
	go s.watch(ss)
}

// newRef returns a random 64-bit ref no session holds. Called with mu held.
func (s *Server) newRef() string {
	for {
		var b [8]byte
		rand.Read(b[:])
		if ref := hex.EncodeToString(b[:]); s.sessions[ref] == nil {
			return ref
		}
	}
}

func (ss *session) poke() {
	select {
	case ss.redraw <- struct{}{}:
	default:
	}
}

// watch pushes ss's frames until it exits. After the first signal it waits
// out frameWindow, collecting whatever else arrives, then sends one frame:
// at most one per window. An exit never waits.
func (s *Server) watch(ss *session) {
	out, done := ss.s.Output(), ss.s.Done()
	for {
		select {
		case <-out:
		case <-ss.redraw:
		case <-done:
			s.ended(ss)
			return
		}
		t := time.NewTimer(frameWindow)
	collect:
		for {
			select {
			case <-out:
			case <-ss.redraw:
			case <-t.C:
				break collect
			case <-done:
				t.Stop()
				s.ended(ss)
				return
			}
		}
		s.pushFrame(ss)
	}
}

// ended records that ss exited and tells the client, if there is one.
// Otherwise it stays listed until a Welcome carries it.
func (s *Server) ended(ss *session) {
	s.pushFrame(ss) // whatever it printed last
	var msg string
	if err := ss.s.Err(); err != nil {
		msg = err.Error()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss.exited, ss.err = true, msg
	if s.stopping {
		return
	}
	if c := s.client; c != nil {
		c.push(Msg{Exit: &Exit{Ref: ss.ref, Err: msg}})
		s.remove(ss)
	}
	s.maybeFinish()
}

// pushFrame renders ss and queues it for the client, if there is one.
func (s *Server) pushFrame(ss *session) {
	ss.render.Lock()
	defer ss.render.Unlock()
	s.mu.Lock()
	c, cursor := s.client, ss.cursor
	s.mu.Unlock()
	if c == nil {
		return
	}
	f := &Frame{
		Ref:        ss.ref,
		Screen:     ss.s.Render(cursor),
		Title:      ss.s.Title(),
		Alt:        ss.s.IsAltScreen(),
		Mouse:      ss.s.MouseTracking(),
		Scrollback: ss.s.ScrollbackLen(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != c || ss.exited || s.sessions[ss.ref] != ss {
		return
	}
	ss.frameVer++
	f.Version = ss.frameVer
	c.pushFrame(f)
}
