package server

import (
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

func attachClient(t *testing.T, r *running) (*Client, Welcome) {
	t.Helper()
	c, w, err := Attach(Paths{Socket: r.sock})
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, w
}

func spawnRemote(t *testing.T, c *Client, dir string) *RemoteSession {
	t.Helper()
	rs, err := c.Spawn(dir, 80, 24, []byte("meta-"+dir))
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	return rs
}

func waitOutput(t *testing.T, rs *RemoteSession) {
	t.Helper()
	select {
	case <-rs.Output():
	case <-time.After(2 * time.Second):
		t.Fatal("Output() did not fire")
	}
}

func waitClosed(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not close", what)
	}
}

func waitGone(t *testing.T, c *Client) string {
	t.Helper()
	select {
	case reason := <-c.Gone():
		return reason
	case <-time.After(3 * time.Second):
		t.Fatal("Gone() did not fire")
		return ""
	}
}

func TestRemoteSessionMirrorsFrames(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attachClient(t, r)
	rs := spawnRemote(t, c, "/a")
	if rs.Ref() == "" {
		t.Fatal("Ref() is empty")
	}
	f := fs.get(t, 0)
	if got := f.cfg.Env; len(got) == 0 {
		t.Error("Spawn sent no environment")
	}

	f.emit()
	f.emit()
	eventually(t, "frame for the latest output", func() bool {
		return rs.Render(false) == f.Render(false)
	})
	if rs.Version() == 0 {
		t.Error("Version() still 0 after a frame")
	}
	if rs.Title() != "title" || rs.IsAltScreen() || rs.MouseTracking() || rs.ScrollbackLen() != 7 {
		t.Errorf("flags: title=%q alt=%v mouse=%v scrollback=%d", rs.Title(), rs.IsAltScreen(), rs.MouseTracking(), rs.ScrollbackLen())
	}

	before := rs.Version()
	for len(rs.Output()) > 0 {
		<-rs.Output()
	}
	f.emit()
	waitOutput(t, rs)
	eventually(t, "a newer version", func() bool { return rs.Version() > before })
}

func TestRemoteSessionHistoryQueries(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attachClient(t, r)
	rs := spawnRemote(t, c, "/a")
	if got := rs.RenderScrolled(3); got != "SCROLLED:3" {
		t.Errorf("RenderScrolled(3) = %q", got)
	}
	if got := rs.Lines(2); !reflect.DeepEqual(got, []string{"LINE:2", "second"}) {
		t.Errorf("Lines(2) = %q", got)
	}
	// Cached: the same question at the same version asks nothing.
	if got := rs.RenderScrolled(3); got != "SCROLLED:3" {
		t.Errorf("cached RenderScrolled(3) = %q", got)
	}
	if n := fs.get(t, 0).scrolls.Load(); n != 1 {
		t.Errorf("server rendered the scrolled screen %d times, want 1 (cached)", n)
	}
}

func TestStuckProgramDoesNotStallOtherSessions(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attachClient(t, r)
	stuck := spawnRemote(t, c, "/stuck")
	other := spawnRemote(t, c, "/other")
	fs.get(t, 0).stuck.Store(true)

	stuck.SendText("a large paste nobody reads")
	stuck.SendText("more")
	other.SendText("hi")
	f := fs.get(t, 1)
	deadline := time.Now().Add(time.Second)
	for len(f.texts()) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("input to another session stalled behind a stuck one")
		}
		time.Sleep(2 * time.Millisecond)
	}
	start := time.Now()
	if got := other.RenderScrolled(5); got != "SCROLLED:5" || time.Since(start) > time.Second {
		t.Fatalf("Query = %q after %v", got, time.Since(start))
	}
	select {
	case reason := <-c.Gone():
		t.Fatalf("client gone: %q", reason)
	default:
	}
}

func TestRemoteSessionCursorFollowsFocus(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attachClient(t, r)
	rs := spawnRemote(t, c, "/a")
	f := fs.get(t, 0)
	f.emit()
	eventually(t, "first frame", func() bool { return rs.Version() > 0 })
	before := rs.Version()

	rs.Render(true)
	deadline := time.Now().Add(100 * time.Millisecond)
	for !strings.Contains(rs.Render(true), "cursor=true") || rs.Version() <= before {
		if time.Now().After(deadline) {
			t.Fatalf("no cursor frame within 100ms: %q v%d (was v%d)", rs.Render(true), rs.Version(), before)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRemoteSessionKeysAndClose(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attachClient(t, r)
	rs := spawnRemote(t, c, "/a")
	f := fs.get(t, 0)

	key := uv.KeyPressEvent{Code: 'x', Mod: uv.ModAlt, Text: "x"}
	rs.SendKey(key)
	rs.SendText("hello")
	rs.SendWheel(true, 3, 4)
	rs.Resize(100, 40)
	eventually(t, "inputs reach the session", func() bool {
		return len(f.keyPresses()) == 1 && len(f.texts()) == 1 && len(f.wheels()) == 1 && f.lastSize() == [2]int{100, 40}
	})
	if got := f.keyPresses()[0]; !reflect.DeepEqual(got, key) {
		t.Errorf("key = %+v, want %+v", got, key)
	}
	if got := f.texts()[0]; got != "hello" {
		t.Errorf("text = %q", got)
	}
	if got := f.wheels()[0]; !got {
		t.Error("wheel up became down")
	}

	if err := rs.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "session closed", func() bool { return f.closed.Load() > 0 })
	waitClosed(t, "Done()", rs.Done())
	if err := rs.Err(); err != nil {
		t.Errorf("Err() = %v after a clean exit", err)
	}
}

func TestSpawnReportsServerError(t *testing.T) {
	fs := &fakes{fail: true}
	r := startServer(t, fs.spawn)
	c, _ := attachClient(t, r)
	if _, err := c.Spawn("/a", 80, 24, nil); err == nil || !strings.Contains(err.Error(), "spawn refused") {
		t.Fatalf("Spawn err = %v, want the server's refusal", err)
	}
}

func TestAdoptStartsWithTheCurrentScreen(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c1, _ := attachClient(t, r)
	rs1 := spawnRemote(t, c1, "/a")
	f := fs.get(t, 0)
	f.emit()
	eventually(t, "first frame", func() bool { return rs1.Version() > 0 })
	c1.Close()
	eventually(t, "the server sees the detach", func() bool {
		r.srv.mu.Lock()
		defer r.srv.mu.Unlock()
		return r.srv.client == nil
	})

	f.emit() // printed while detached
	c2, w := attachClient(t, r)
	if len(w.Sessions) != 1 || w.Sessions[0].Ref != rs1.Ref() || string(w.Sessions[0].Meta) != "meta-/a" {
		t.Fatalf("Welcome sessions = %+v", w.Sessions)
	}
	if !w.Sessions[0].Changed {
		t.Error("Welcome does not say the session printed while detached")
	}
	eventually(t, "the attach frame is kept before Adopt", func() bool {
		c2.mu.Lock()
		e := c2.refs[rs1.Ref()]
		c2.mu.Unlock()
		return e != nil && e.Version() > 0
	})
	rs2 := c2.Adopt(w.Sessions[0])
	if got, want := rs2.Render(false), f.Render(false); got != want {
		t.Errorf("adopted Render = %q, want %q", got, want)
	}
	rs2.SendText("again")
	eventually(t, "input from the adopting client", func() bool { return len(f.texts()) == 1 })
}

func TestGoneOnEvictAndOnServerExit(t *testing.T) {
	t.Run("evicted", func(t *testing.T) {
		fs := &fakes{}
		r := startServer(t, fs.spawn)
		c1, _ := attachClient(t, r)
		rs := spawnRemote(t, c1, "/a")
		attachClient(t, r)
		if got := waitGone(t, c1); got != "opened in another window" {
			t.Errorf("Gone = %q", got)
		}
		waitClosed(t, "Done() after Gone", rs.Done())
		if !errors.Is(rs.Err(), ErrGone) {
			t.Errorf("Err() = %v, want ErrGone", rs.Err())
		}
		// Every send is now a no-op that returns at once.
		rs.SendText("x")
		rs.SendKey(uv.KeyPressEvent{Code: 'a'})
		rs.Resize(10, 10)
		rs.Render(true)
		rs.Close()
		c1.SetBlob([]byte("b"))
		c1.Stop()
		if got := rs.RenderScrolled(1); got != "" {
			t.Errorf("RenderScrolled after Gone = %q", got)
		}
		if _, err := c1.Spawn("/b", 80, 24, nil); err == nil {
			t.Error("Spawn after Gone succeeded")
		}
		if len(fs.get(t, 0).texts()) != 0 {
			t.Error("input after Gone reached the session")
		}
	})
	t.Run("stopped", func(t *testing.T) {
		fs := &fakes{}
		r := startServer(t, fs.spawn)
		c, _ := attachClient(t, r)
		spawnRemote(t, c, "/a")
		c.Stop()
		if got := waitGone(t, c); got != "stopped by tm stop" {
			t.Errorf("Gone = %q", got)
		}
		r.waitServed(t, 5*time.Second)
	})
	t.Run("server died", func(t *testing.T) {
		fs := &fakes{}
		r := startServer(t, fs.spawn)
		c, _ := attachClient(t, r)
		rs := spawnRemote(t, c, "/a")
		// The server's end of the connection drops with no Evict.
		r.srv.mu.Lock()
		for conn := range r.srv.conns {
			conn.Close()
		}
		r.srv.mu.Unlock()
		if got := waitGone(t, c); got != "the tm server stopped" {
			t.Errorf("Gone = %q", got)
		}
		waitClosed(t, "Done() after Gone", rs.Done())
		select {
		case reason, ok := <-c.Gone():
			if ok {
				t.Errorf("Gone fired twice: %q", reason)
			}
		case <-time.After(50 * time.Millisecond):
			t.Error("Gone() not closed after firing")
		}
	})
}

func TestCloseIsNotGone(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attachClient(t, r)
	rs := spawnRemote(t, c, "/a")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-c.Gone():
		t.Fatalf("Gone fired on Close: %q", reason)
	case <-time.After(100 * time.Millisecond):
	}
	rs.SendText("x") // a no-op, not a panic
	select {
	case <-rs.Done():
		t.Error("Close made a running session read as exited")
	default:
	}
	if f := fs.get(t, 0); f.closed.Load() != 0 {
		t.Error("detaching closed the session")
	}
}

// rawServer accepts one connection on a fresh socket and hands it to serve.
func rawServer(t *testing.T, serve func(*Conn)) Paths {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		conn := NewConn(c)
		defer conn.Close()
		serve(conn)
	}()
	return Paths{Socket: sock}
}

func TestAttachRejectsOtherProtocol(t *testing.T) {
	p := rawServer(t, func(c *Conn) {
		c.Recv()
		c.Send(Msg{Welcome: &Welcome{Protocol: 99}})
	})
	if _, _, err := Attach(p); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Attach err = %v, want ErrProtocol", err)
	}
}

func TestAttachTreatsUndecodableReplyAsProtocol(t *testing.T) {
	p := rawServer(t, func(c *Conn) {
		c.Recv()
		c.c.Write([]byte("\x05garbage that is not gob\n"))
		time.Sleep(100 * time.Millisecond)
	})
	if _, _, err := Attach(p); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Attach err = %v, want ErrProtocol", err)
	}
}

func TestAttachDialErrorIsNotProtocol(t *testing.T) {
	_, _, err := Attach(Paths{Socket: filepath.Join(t.TempDir(), "none")})
	if err == nil || errors.Is(err, ErrProtocol) {
		t.Fatalf("Attach err = %v, want a plain dial error", err)
	}
}

func TestClientIgnoresStrayMessagesAndReportsExitErr(t *testing.T) {
	p := rawServer(t, func(c *Conn) {
		c.Recv()
		c.Send(Msg{Welcome: &Welcome{Protocol: ProtocolVersion, Sessions: []SessionInfo{{Ref: "r1"}}}})
		c.Send(Msg{})                                     // all nil
		c.Send(Msg{Status: &Status{Live: 3}})             // not for a client
		c.Send(Msg{Spawn: &Spawn{Req: 1}})                // not for a client
		c.Send(Msg{Answer: &Answer{Req: 42}})             // answers nothing asked
		c.Send(Msg{Spawned: &Spawned{Req: 9}})            // answers nothing asked
		c.Send(Msg{Frame: &Frame{Ref: "zz", Version: 1}}) // unknown ref
		c.Send(Msg{Frame: &Frame{Ref: "r1", Version: 2, Screen: "two"}})
		c.Send(Msg{Frame: &Frame{Ref: "r1", Version: 1, Screen: "stale"}})
		c.Send(Msg{Exit: &Exit{Ref: "r1", Err: "exit status 3"}})
		c.Recv() // until the client goes
	})
	c, w, err := Attach(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rs := c.Adopt(w.Sessions[0])
	waitClosed(t, "Done()", rs.Done())
	if got := rs.Render(false); got != "two" {
		t.Errorf("Render = %q, want the newest frame", got)
	}
	if err := rs.Err(); err == nil || err.Error() != "exit status 3" {
		t.Errorf("Err() = %v", err)
	}
	select {
	case reason := <-c.Gone():
		t.Fatalf("Gone fired: %q", reason)
	default:
	}
}

// TakePrinted reports, once, whether any frame since the last call came
// from output rather than a redraw.
func TestRemoteSessionTakePrinted(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attachClient(t, r)
	rs := spawnRemote(t, c, "/a")
	f := fs.get(t, 0)

	f.emit()
	waitOutput(t, rs)
	if !rs.TakePrinted() {
		t.Fatal("output did not read as printed")
	}
	if rs.TakePrinted() {
		t.Fatal("TakePrinted did not clear")
	}
	rs.Resize(100, 30)
	waitOutput(t, rs)
	if rs.TakePrinted() {
		t.Fatal("a resize read as printed")
	}
	if !rs.Coalesced() {
		t.Fatal("a RemoteSession's frames are coalesced by the server")
	}
}
