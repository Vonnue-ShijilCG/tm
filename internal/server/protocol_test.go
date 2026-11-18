package server

import (
	"net"
	"reflect"
	"sync"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// pipe returns the two ends of an in-memory connection, closed when the test
// ends.
func pipe(t *testing.T) (*Conn, *Conn) {
	t.Helper()
	a, b := net.Pipe()
	ca, cb := NewConn(a), NewConn(b)
	t.Cleanup(func() {
		ca.Close()
		cb.Close()
	})
	return ca, cb
}

func TestConnRoundTripsEveryMessage(t *testing.T) {
	msgs := []Msg{
		{Hello: &Hello{Protocol: ProtocolVersion, Attach: true}},
		{Hello: &Hello{Protocol: ProtocolVersion}},
		{Welcome: &Welcome{
			Protocol: ProtocolVersion,
			Blob:     []byte("layout"),
			Sessions: []SessionInfo{
				{Ref: "0123456789abcdef", Meta: []byte("m1"), Changed: true},
				{Ref: "fedcba9876543210", Meta: []byte("m2"), Exited: true, Err: "exit status 1"},
			},
			Live: 1,
		}},
		{Spawn: &Spawn{Req: 7, Dir: "/tmp/x", Env: []string{"A=1", "B=2"}, Cols: 80, Rows: 24, Meta: []byte("meta")}},
		{Spawned: &Spawned{Req: 7, Ref: "0123456789abcdef"}},
		{Spawned: &Spawned{Req: 8, Err: "no such directory"}},
		{Input: &Input{Ref: "0123456789abcdef", Key: &uv.KeyPressEvent{Code: 'a', Mod: uv.ModCtrl}}},
		{Input: &Input{Ref: "0123456789abcdef", Text: "pasted text"}},
		{Input: &Input{Ref: "0123456789abcdef", Wheel: &Wheel{Up: true, X: 3, Y: 4}}},
		{Resize: &Resize{Ref: "0123456789abcdef", Cols: 120, Rows: 40}},
		{Focus: &Focus{Ref: "0123456789abcdef", Cursor: true}},
		{Kill: &Kill{Ref: "0123456789abcdef"}},
		{Frame: &Frame{Ref: "0123456789abcdef", Version: 42, Screen: "\x1b[1mhi\x1b[m", Title: "zsh", Alt: true, Mouse: true, Scrollback: 300}},
		{Exit: &Exit{Ref: "0123456789abcdef", Err: "signal: killed"}},
		{Query: &Query{Req: 9, Ref: "0123456789abcdef", Offset: 5}},
		{Query: &Query{Req: 10, Ref: "0123456789abcdef", Lines: true}},
		{Answer: &Answer{Req: 9, Screen: "scrolled"}},
		{Answer: &Answer{Req: 10, Lines: []string{"one", "two"}}},
		{SetBlob: &SetBlob{Blob: []byte("new layout")}},
		{Evict: &Evict{Reason: "opened in another window"}},
		{Stop: &Stop{}},
		{Status: &Status{Live: 3}},
		{Status: &Status{}},
	}

	a, b := pipe(t)
	go func() {
		for _, m := range msgs {
			if err := a.Send(m); err != nil {
				t.Errorf("send: %v", err)
				return
			}
		}
	}()
	for i, want := range msgs {
		got, err := b.Recv()
		if err != nil {
			t.Fatalf("recv %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("message %d:\n got %#v\nwant %#v", i, got, want)
		}
	}
}

func TestConnSendIsSafeConcurrently(t *testing.T) {
	const senders, each = 8, 100
	a, b := pipe(t)

	var wg sync.WaitGroup
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				m := Msg{Frame: &Frame{Ref: "0123456789abcdef", Version: uint64(s*each + i), Screen: "screen"}}
				if err := a.Send(m); err != nil {
					t.Errorf("send: %v", err)
					return
				}
			}
		}(s)
	}

	seen := make(map[uint64]bool)
	for len(seen) < senders*each {
		m, err := b.Recv()
		if err != nil {
			t.Fatalf("recv after %d: %v", len(seen), err)
		}
		if m.Frame == nil {
			t.Fatalf("got %#v, want a frame", m)
		}
		if seen[m.Frame.Version] {
			t.Fatalf("frame %d received twice", m.Frame.Version)
		}
		seen[m.Frame.Version] = true
	}
	wg.Wait()
}

func TestConnRecvFailsAfterPeerCloses(t *testing.T) {
	a, b := pipe(t)
	a.Close()
	if _, err := b.Recv(); err == nil {
		t.Fatal("Recv succeeded on a closed connection")
	}
}
