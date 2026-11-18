// Package server keeps a workspace's terminals running in a background
// process so they outlive the window that started them. This file is the wire
// protocol between that process and a tm client: one gob-encoded envelope per
// message over a Unix socket.
package server

import (
	"encoding/gob"
	"net"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
)

// ProtocolVersion is bumped whenever any message below changes shape, which
// under the rule on Hello means only by adding fields. A client and server
// that disagree refuse to talk (see Hello).
const ProtocolVersion = 1

// Msg is the one envelope both directions use. Exactly one pointer field is
// set; the receiver switches on which.
type Msg struct {
	Hello   *Hello
	Welcome *Welcome
	Spawn   *Spawn
	Spawned *Spawned
	Input   *Input
	Resize  *Resize
	Focus   *Focus
	Kill    *Kill
	Frame   *Frame
	Exit    *Exit
	Query   *Query
	Answer  *Answer
	SetBlob *SetBlob
	Evict   *Evict
	Stop    *Stop
	Status  *Status
}

// Hello is the first message a client sends. Attach=false is tm stop or a
// probe: the server answers with Status and leaves any attached client alone.
//
// Hello, Welcome.Protocol and Status are frozen across protocol versions:
// they are what a client and server of different versions still exchange to
// discover the mismatch. A server receiving a Hello with a different Protocol
// replies Welcome{Protocol: ProtocolVersion} with nothing else set and closes.
//
// Freezing those three is not enough under gob, which checks the type of
// every field it is sent whose name matches a local field, set or not, across
// the whole of Msg. So the rule for every version is: a field name anywhere
// under Msg must never change type. A version may only add fields, or retire
// a name and add a new one in its place; changing, say, SessionInfo.Ref or
// Spawn.Dir would make an older tm fail to decode even the mismatch reply.
type Hello struct {
	Protocol int
	Attach   bool
}

// Welcome answers Hello{Attach: true}: the client's saved blob and every
// session the server holds. Protocol is frozen (see Hello); the other fields
// may change with the version.
type Welcome struct {
	Protocol int
	Blob     []byte
	Sessions []SessionInfo
	Live     int
}

// SessionInfo describes one session held by the server. Changed reports
// output while no client was attached.
type SessionInfo struct {
	Ref     string
	Meta    []byte
	Exited  bool
	Err     string
	Changed bool
}

// Spawn asks the server to start a shell. Req pairs it with its Spawned.
type Spawn struct {
	Req        uint64
	Dir        string
	Env        []string
	Cols, Rows int
	Meta       []byte
}

// Spawned answers Spawn with the new session's Ref, or Err.
type Spawned struct {
	Req uint64
	Ref string
	Err string
}

// Input carries one of a key, pasted text or a wheel event to a session.
type Input struct {
	Ref   string
	Key   *uv.KeyPressEvent
	Text  string
	Wheel *Wheel
}

// Wheel is one wheel notch at a cell of the session's screen.
type Wheel struct {
	Up   bool
	X, Y int
}

// Resize changes a session's size.
type Resize struct {
	Ref        string
	Cols, Rows int
}

// Focus tells a session whether its cursor is shown.
type Focus struct {
	Ref    string
	Cursor bool
}

// Kill ends a session.
type Kill struct {
	Ref string
}

// Frame is a session's rendered screen, pushed by the server.
type Frame struct {
	Ref        string
	Version    uint64
	Screen     string
	Title      string
	Alt        bool
	Mouse      bool
	Scrollback int
}

// Exit reports that a session's process ended.
type Exit struct {
	Ref string
	Err string
}

// Query asks for a session's scrolled screen at Offset, or, with Lines, its
// lines. Req pairs it with its Answer.
type Query struct {
	Req    uint64
	Ref    string
	Offset int
	Lines  bool // false: RenderScrolled
}

// Answer replies to Query.
type Answer struct {
	Req    uint64
	Screen string
	Lines  []string
}

// SetBlob replaces the client's saved blob held by the server.
type SetBlob struct {
	Blob []byte
}

// Evict tells an attached client it has been detached, and why:
// "opened in another window" or "stopped by tm stop".
type Evict struct {
	Reason string
}

// Stop asks the server to end every session and exit.
type Stop struct{}

// Status answers Hello{Attach: false} with the number of live sessions. It is
// frozen across protocol versions (see Hello).
type Status struct {
	Live int
}

// Conn carries Msgs over a connection. Send may be called from any number of
// goroutines; Recv from one at a time.
type Conn struct {
	c   net.Conn
	mu  sync.Mutex
	enc *gob.Encoder
	dec *gob.Decoder
}

// NewConn wraps c.
func NewConn(c net.Conn) *Conn {
	return &Conn{c: c, enc: gob.NewEncoder(c), dec: gob.NewDecoder(c)}
}

// Send writes one message.
func (c *Conn) Send(m Msg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(m)
}

// Recv reads one message.
func (c *Conn) Recv() (Msg, error) {
	var m Msg
	err := c.dec.Decode(&m)
	return m, err
}

// Close closes the underlying connection.
func (c *Conn) Close() error {
	return c.c.Close()
}
