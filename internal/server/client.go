package server

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// ErrProtocol is Attach's error when the server speaks another protocol
// version, or answers Hello with something this tm cannot decode.
var ErrProtocol = errors.New("protocol mismatch")

// ErrGone is what a RemoteSession's Err wraps when its Done closed because
// the connection to the server was lost, not because its process exited.
// The terminal may well still be running; it must not be forgotten as an
// exited one.
var ErrGone = errors.New("tm server connection lost")

const (
	// spawnTimeout bounds the wait for Spawned.
	spawnTimeout = 5 * time.Second
	// queryTimeout bounds the wait for an Answer; a late one gets an
	// empty result.
	queryTimeout = 2 * time.Second
	// goneStopped is Gone's reason when the connection ends without Evict.
	goneStopped = "the tm server stopped"
)

// Client is an attached connection to a workspace's server. One reader
// goroutine receives every message and dispatches it: frames and exits to
// the session they name, replies to whoever is waiting for them.
type Client struct {
	conn *Conn

	gone     chan string   // the reason, once; closed after it
	goneOnce sync.Once     // guards gone and the closing of every session
	dead     chan struct{} // the reader has stopped, for any reason
	read     chan struct{} // closed when the reader goroutine has returned

	deadOnce sync.Once // guards dead

	mu      sync.Mutex
	closing bool // Close was called: the end is not reported by Gone
	refs    map[string]*RemoteSession
	spawns  map[uint64]chan spawnReply
	queries map[uint64]chan *Answer
	nextReq uint64
}

// spawnReply is a Spawned with the session the reader registered for it.
type spawnReply struct {
	sp *Spawned
	rs *RemoteSession
}

// Attach connects to the server at p and attaches as its client, which
// evicts any other. A server of another protocol version, or a reply that
// does not decode, is ErrProtocol; failing to connect is a plain error.
//
// Every session in the Welcome is known from the start, and the frames the
// server sends right after it are kept, so Adopt starts with the current
// screen whenever it is called.
func Attach(p Paths) (*Client, Welcome, error) {
	nc, err := net.DialTimeout("unix", p.Socket, dialTimeout)
	if err != nil {
		return nil, Welcome{}, err
	}
	conn := NewConn(nc)
	nc.SetDeadline(time.Now().Add(replyTimeout))
	if err := conn.Send(Msg{Hello: &Hello{Protocol: ProtocolVersion, Attach: true}}); err != nil {
		conn.Close()
		return nil, Welcome{}, err
	}
	m, err := conn.Recv()
	if err != nil {
		conn.Close()
		var ne net.Error
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &ne) {
			return nil, Welcome{}, fmt.Errorf("tm server did not answer: %w", err)
		}
		// Anything else came from gob: the reply is not ours to read.
		return nil, Welcome{}, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	switch {
	case m.Welcome == nil:
		conn.Close()
		return nil, Welcome{}, fmt.Errorf("%w: unexpected reply to Hello", ErrProtocol)
	case m.Welcome.Protocol != ProtocolVersion:
		conn.Close()
		return nil, Welcome{}, fmt.Errorf("%w: server speaks version %d, this tm %d", ErrProtocol, m.Welcome.Protocol, ProtocolVersion)
	}
	nc.SetDeadline(time.Time{})

	c := &Client{
		conn:    conn,
		gone:    make(chan string, 1),
		dead:    make(chan struct{}),
		read:    make(chan struct{}),
		refs:    map[string]*RemoteSession{},
		spawns:  map[uint64]chan spawnReply{},
		queries: map[uint64]chan *Answer{},
	}
	w := *m.Welcome
	for _, info := range w.Sessions {
		// The server's cursor flag is whatever the last client left it as.
		rs := c.newSession(info.Ref, false)
		if info.Exited {
			rs.finish(exitErr(info.Err))
		}
		c.refs[info.Ref] = rs
	}
	go c.readLoop()
	return c, w, nil
}

func exitErr(msg string) error {
	if msg == "" {
		return nil
	}
	return errors.New(msg)
}

func (c *Client) newSession(ref string, cursorKnown bool) *RemoteSession {
	return &RemoteSession{
		c:           c,
		ref:         ref,
		output:      make(chan struct{}, 1),
		done:        make(chan struct{}),
		cursorKnown: cursorKnown,
	}
}

// readLoop is the one reader. It ends when the connection does.
func (c *Client) readLoop() {
	defer close(c.read)
	for {
		m, err := c.conn.Recv()
		if err != nil {
			c.lost(goneStopped)
			return
		}
		switch {
		case m.Frame != nil:
			c.onFrame(m.Frame)
		case m.Exit != nil:
			c.onExit(m.Exit)
		case m.Spawned != nil:
			c.onSpawned(m.Spawned)
		case m.Answer != nil:
			c.mu.Lock()
			ch := c.queries[m.Answer.Req]
			delete(c.queries, m.Answer.Req)
			c.mu.Unlock()
			if ch != nil {
				ch <- m.Answer // buffered 1, and only one Answer per Req is taken
			}
		case m.Evict != nil:
			c.lost(m.Evict.Reason)
			return
		}
		// Anything else is not for a client, or from a newer version.
	}
}

func (c *Client) onFrame(f *Frame) {
	c.mu.Lock()
	rs := c.refs[f.Ref]
	c.mu.Unlock()
	if rs == nil {
		return // a session nobody told us about
	}
	rs.mu.Lock()
	newer := f.Version > rs.frame.Version
	if newer {
		rs.frame = *f
	}
	rs.mu.Unlock()
	if newer {
		select {
		case rs.output <- struct{}{}:
		default:
		}
	}
}

func (c *Client) onExit(e *Exit) {
	c.mu.Lock()
	rs := c.refs[e.Ref]
	// A session handed out is held by its owner from now on. One not yet
	// adopted stays, so Adopt returns it already done.
	if rs != nil && rs.handed {
		delete(c.refs, e.Ref)
	}
	c.mu.Unlock()
	if rs != nil {
		rs.finish(exitErr(e.Err))
	}
}

func (c *Client) onSpawned(sp *Spawned) {
	c.mu.Lock()
	ch := c.spawns[sp.Req]
	delete(c.spawns, sp.Req)
	var rs *RemoteSession
	if sp.Err == "" && sp.Ref != "" {
		// Registered here, before any frame of it can be read, so none is
		// missed. The server starts every session with no cursor.
		rs = c.newSession(sp.Ref, true)
		rs.handed = ch != nil
		c.refs[sp.Ref] = rs
	}
	c.mu.Unlock()
	switch {
	case ch != nil:
		ch <- spawnReply{sp, rs} // buffered 1
	case rs != nil:
		// Its Spawn gave up waiting: nobody will ever show it.
		c.mu.Lock()
		delete(c.refs, sp.Ref)
		c.mu.Unlock()
		c.send(Msg{Kill: &Kill{Ref: sp.Ref}})
	}
}

// lost ends the client after its reader stopped. Every session's Done is
// closed before Gone fires, so whoever reads Gone finds them all done. A
// self-initiated Close reports nothing and leaves the sessions open.
func (c *Client) lost(reason string) {
	c.deadOnce.Do(func() { close(c.dead) })
	c.conn.Close()
	c.mu.Lock()
	closing := c.closing
	var all []*RemoteSession
	if !closing {
		for _, rs := range c.refs {
			all = append(all, rs)
		}
	}
	c.mu.Unlock()
	if closing {
		return
	}
	c.goneOnce.Do(func() {
		err := fmt.Errorf("%w: %s", ErrGone, reason)
		for _, rs := range all {
			rs.finish(err)
		}
		c.gone <- reason
		close(c.gone)
	})
}

// isDead reports whether the connection has ended.
func (c *Client) isDead() bool {
	select {
	case <-c.dead:
		return true
	default:
		return false
	}
}

// send writes m unless the connection has ended. A write that fails ends
// it, which the reader then reports.
func (c *Client) send(m Msg) {
	if c.isDead() {
		return
	}
	c.conn.c.SetWriteDeadline(time.Now().Add(sendTimeout))
	if c.conn.Send(m) != nil {
		c.conn.Close()
	}
}

func (c *Client) req() uint64 {
	c.nextReq++
	return c.nextReq
}

// Spawn asks the server for a shell in dir, started with this process's
// environment, and waits up to 5 s for it.
func (c *Client) Spawn(dir string, cols, rows int, meta []byte) (*RemoteSession, error) {
	ch := make(chan spawnReply, 1)
	c.mu.Lock()
	if c.isDead() {
		c.mu.Unlock()
		return nil, errors.New("not connected to the tm server")
	}
	req := c.req()
	c.spawns[req] = ch
	c.mu.Unlock()
	c.send(Msg{Spawn: &Spawn{Req: req, Dir: dir, Env: os.Environ(), Cols: cols, Rows: rows, Meta: meta}})

	timer := time.NewTimer(spawnTimeout)
	defer timer.Stop()
	select {
	case reply := <-ch:
		if reply.sp.Err != "" {
			return nil, errors.New(reply.sp.Err)
		}
		if reply.rs == nil {
			return nil, errors.New("tm server started a terminal with no name")
		}
		return reply.rs, nil
	case <-c.dead:
		return nil, errors.New("tm server connection lost while starting a terminal")
	case <-timer.C:
		c.mu.Lock()
		_, waiting := c.spawns[req]
		delete(c.spawns, req)
		c.mu.Unlock()
		if !waiting {
			// The reply landed just now; take it rather than leak it.
			if reply := <-ch; reply.rs != nil {
				reply.rs.Close()
			}
		}
		return nil, fmt.Errorf("tm server did not start a terminal within %v", spawnTimeout)
	}
}

// Adopt returns the session info names, one listed in the Welcome. Its
// screen is the newest frame received so far.
func (c *Client) Adopt(info SessionInfo) *RemoteSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	rs := c.refs[info.Ref]
	if rs == nil {
		// Not in the Welcome: there is nothing on the server behind it.
		rs = c.newSession(info.Ref, false)
		rs.finish(exitErr(info.Err))
		return rs
	}
	rs.handed = true
	select {
	case <-rs.done:
		delete(c.refs, info.Ref) // its Exit has already come
	default:
	}
	return rs
}

// SetBlob replaces the UI state the server keeps for the next attach.
func (c *Client) SetBlob(b []byte) {
	c.send(Msg{SetBlob: &SetBlob{Blob: b}})
}

// Stop asks the server to end every session and exit. Gone then reports
// "stopped by tm stop".
func (c *Client) Stop() {
	c.send(Msg{Stop: &Stop{}})
}

// Gone yields, once, why the server let this client go: the Evict reason
// ("opened in another window", "stopped by tm stop") or "the tm server
// stopped" when the connection ended without one. The channel is closed
// after the reason. Close does not signal it: a detach the client asked for
// is not news.
func (c *Client) Gone() <-chan string {
	return c.gone
}

// Close detaches, leaving every session running on the server. Gone is not
// signalled and the sessions' Done channels stay open, so nothing reads as
// exited; sends on them become no-ops.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	err := c.conn.Close()
	<-c.read
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

// RemoteSession is a session held by the server, seen through its pushed
// frames. It has workspace.Session's method set.
type RemoteSession struct {
	c      *Client
	ref    string
	output chan struct{}
	done   chan struct{}
	once   sync.Once

	handed bool // given out by Spawn or Adopt; guarded by Client.mu

	mu          sync.Mutex
	frame       Frame
	err         error
	cursorKnown bool // whether cursor is what the server has
	cursor      bool
	cache       queryCache
}

// queryCache holds answers for one frame version.
type queryCache struct {
	version uint64
	screens map[int]string
	lines   map[int][]string
}

func (r *RemoteSession) finish(err error) {
	r.once.Do(func() {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
		close(r.done)
	})
}

// Ref is the server's name for the session.
func (r *RemoteSession) Ref() string { return r.ref }

func (r *RemoteSession) SendKey(k uv.KeyPressEvent) {
	r.c.send(Msg{Input: &Input{Ref: r.ref, Key: &k}})
}

func (r *RemoteSession) SendText(s string) {
	if s == "" {
		return
	}
	r.c.send(Msg{Input: &Input{Ref: r.ref, Text: s}})
}

func (r *RemoteSession) SendWheel(up bool, x, y int) {
	r.c.send(Msg{Input: &Input{Ref: r.ref, Wheel: &Wheel{Up: up, X: x, Y: y}}})
}

func (r *RemoteSession) Resize(cols, rows int) {
	r.c.send(Msg{Resize: &Resize{Ref: r.ref, Cols: cols, Rows: rows}})
}

// Render returns the newest frame. When showCursor differs from what the
// server was last told, it says so; the frame with the cursor follows.
func (r *RemoteSession) Render(showCursor bool) string {
	r.mu.Lock()
	screen := r.frame.Screen
	tell := !r.cursorKnown || r.cursor != showCursor
	r.cursorKnown, r.cursor = true, showCursor
	r.mu.Unlock()
	if tell {
		r.c.send(Msg{Focus: &Focus{Ref: r.ref, Cursor: showCursor}})
	}
	return screen
}

func (r *RemoteSession) Version() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frame.Version
}

func (r *RemoteSession) Title() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frame.Title
}

func (r *RemoteSession) IsAltScreen() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frame.Alt
}

func (r *RemoteSession) MouseTracking() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frame.Mouse
}

func (r *RemoteSession) ScrollbackLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frame.Scrollback
}

// RenderScrolled asks the server for the screen offset lines back, cached
// for the current frame. It is "" when no answer comes within 2 s.
func (r *RemoteSession) RenderScrolled(offset int) string {
	r.mu.Lock()
	r.cache.reset(r.frame.Version)
	s, ok := r.cache.screens[offset]
	v := r.frame.Version
	r.mu.Unlock()
	if ok {
		return s
	}
	a := r.query(offset, false)
	if a == nil {
		return ""
	}
	r.mu.Lock()
	if r.cache.version == v {
		r.cache.screens[offset] = a.Screen
	}
	r.mu.Unlock()
	return a.Screen
}

// Lines asks the server for the rows copy mode reads, cached like
// RenderScrolled. It is nil when no answer comes within 2 s.
func (r *RemoteSession) Lines(offset int) []string {
	r.mu.Lock()
	r.cache.reset(r.frame.Version)
	l, ok := r.cache.lines[offset]
	v := r.frame.Version
	r.mu.Unlock()
	if ok {
		return l
	}
	a := r.query(offset, true)
	if a == nil {
		return nil
	}
	r.mu.Lock()
	if r.cache.version == v {
		r.cache.lines[offset] = a.Lines
	}
	r.mu.Unlock()
	return a.Lines
}

// reset empties the cache when the frame has moved on. Called with the
// session's mu held.
func (q *queryCache) reset(version uint64) {
	if q.screens == nil || q.version != version {
		*q = queryCache{version: version, screens: map[int]string{}, lines: map[int][]string{}}
	}
}

// query sends a Query and waits for its Answer, or nil.
func (r *RemoteSession) query(offset int, lines bool) *Answer {
	c := r.c
	ch := make(chan *Answer, 1)
	c.mu.Lock()
	if c.isDead() {
		c.mu.Unlock()
		return nil
	}
	req := c.req()
	c.queries[req] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.queries, req)
		c.mu.Unlock()
	}()
	c.send(Msg{Query: &Query{Req: req, Ref: r.ref, Offset: offset, Lines: lines}})
	timer := time.NewTimer(queryTimeout)
	defer timer.Stop()
	select {
	case a := <-ch:
		return a
	case <-c.dead:
		return nil
	case <-timer.C:
		return nil
	}
}

func (r *RemoteSession) Output() <-chan struct{} { return r.output }
func (r *RemoteSession) Done() <-chan struct{}   { return r.done }

// Err is the process's exit error, or one wrapping ErrGone when the
// connection to the server was lost.
func (r *RemoteSession) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close ends the session on the server; Done closes when its Exit lands.
func (r *RemoteSession) Close() error {
	r.c.send(Msg{Kill: &Kill{Ref: r.ref}})
	return nil
}
