package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Vonnue-ShijilCG/tm/internal/terminal"
)

// fakeSession is a Session whose output and exit the test drives.
type fakeSession struct {
	cfg     terminal.Config
	output  chan struct{}
	done    chan struct{}
	version atomic.Uint64
	closed  atomic.Int32
	once    sync.Once

	mu    sync.Mutex
	text  []string
	keys  []uv.KeyPressEvent
	size  [2]int
	wheel []bool
}

func newFakeSession(cfg terminal.Config) *fakeSession {
	return &fakeSession{cfg: cfg, output: make(chan struct{}, 1), done: make(chan struct{})}
}

// emit is one burst of output, signalled the way terminal.Session does.
func (f *fakeSession) emit() {
	f.version.Add(1)
	select {
	case f.output <- struct{}{}:
	default:
	}
}

func (f *fakeSession) exit() { f.once.Do(func() { close(f.done) }) }

func (f *fakeSession) SendKey(k uv.KeyPressEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, k)
}
func (f *fakeSession) SendText(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text = append(f.text, s)
}
func (f *fakeSession) Resize(cols, rows int) {
	f.mu.Lock()
	f.size = [2]int{cols, rows}
	f.mu.Unlock()
	f.version.Add(1)
}
func (f *fakeSession) Render(cursor bool) string {
	return fmt.Sprintf("SCREEN:%s:v%d:cursor=%v", f.cfg.Dir, f.version.Load(), cursor)
}
func (f *fakeSession) RenderScrolled(offset int) string {
	if offset >= 1000 { // big enough to fill a socket buffer
		return strings.Repeat("x", offset)
	}
	return fmt.Sprintf("SCROLLED:%d", offset)
}
func (f *fakeSession) Version() uint64 { return f.version.Load() }
func (f *fakeSession) Lines(offset int) []string {
	return []string{fmt.Sprintf("LINE:%d", offset), "second"}
}
func (f *fakeSession) ScrollbackLen() int  { return 7 }
func (f *fakeSession) IsAltScreen() bool   { return false }
func (f *fakeSession) MouseTracking() bool { return false }
func (f *fakeSession) SendWheel(up bool, x, y int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wheel = append(f.wheel, up)
}
func (f *fakeSession) Title() string           { return "title" }
func (f *fakeSession) Output() <-chan struct{} { return f.output }
func (f *fakeSession) Done() <-chan struct{}   { return f.done }
func (f *fakeSession) Err() error              { return nil }
func (f *fakeSession) Close() error            { f.closed.Add(1); f.exit(); return nil }
func (f *fakeSession) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.text...)
}
func (f *fakeSession) lastSize() [2]int { f.mu.Lock(); defer f.mu.Unlock(); return f.size }
func (f *fakeSession) wheels() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.wheel...)
}
func (f *fakeSession) keyPresses() []uv.KeyPressEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uv.KeyPressEvent(nil), f.keys...)
}

// fakes is a Spawner that records every session it starts.
type fakes struct {
	mu   sync.Mutex
	all  []*fakeSession
	fail bool
}

func (fs *fakes) spawn(cfg terminal.Config) (Session, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.fail {
		return nil, errors.New("spawn refused")
	}
	f := newFakeSession(cfg)
	fs.all = append(fs.all, f)
	return f, nil
}

func (fs *fakes) get(t *testing.T, i int) *fakeSession {
	t.Helper()
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if i >= len(fs.all) {
		t.Fatalf("only %d sessions spawned, want #%d", len(fs.all), i)
	}
	return fs.all[i]
}

type running struct {
	srv    *Server
	sock   string
	exited string
	served chan error
}

func startServer(t *testing.T, spawn Spawner) *running {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	r := &running{srv: New(spawn, filepath.Join(dir, "exited")), sock: sock, exited: filepath.Join(dir, "exited"), served: make(chan error, 1)}
	go func() { r.served <- r.srv.Serve(ln) }()
	t.Cleanup(func() {
		r.srv.stop()
		select {
		case <-r.served:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after stop")
		}
	})
	return r
}

func (r *running) waitServed(t *testing.T, within time.Duration) error {
	t.Helper()
	select {
	case err := <-r.served:
		r.served <- err // let Cleanup see it too
		return err
	case <-time.After(within):
		t.Fatalf("Serve did not return within %v", within)
		return nil
	}
}

func (r *running) servedYet() bool {
	select {
	case err := <-r.served:
		r.served <- err
		return true
	default:
		return false
	}
}

func dial(t *testing.T, r *running) *Conn {
	t.Helper()
	c, err := net.Dial("unix", r.sock)
	if err != nil {
		t.Fatal(err)
	}
	conn := NewConn(c)
	t.Cleanup(func() { conn.Close() })
	return conn
}

// attach dials and sends Hello{Attach:true}, returning the Welcome.
func attach(t *testing.T, r *running) (*Conn, *Welcome) {
	t.Helper()
	conn := dial(t, r)
	send(t, conn, Msg{Hello: &Hello{Protocol: ProtocolVersion, Attach: true}})
	m := expect(t, conn, "Welcome", func(m Msg) bool { return m.Welcome != nil })
	return conn, m.Welcome
}

func send(t *testing.T, c *Conn, m Msg) {
	t.Helper()
	if err := c.Send(m); err != nil {
		t.Fatal(err)
	}
}

// expect reads until a message satisfies want, skipping others, for up to 2s.
func expect(t *testing.T, c *Conn, what string, want func(Msg) bool) Msg {
	t.Helper()
	c.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	defer c.c.SetReadDeadline(time.Time{})
	for {
		m, err := c.Recv()
		if err != nil {
			t.Fatalf("waiting for %s: %v", what, err)
		}
		if want(m) {
			return m
		}
	}
}

// collect returns every message received within d.
func collect(c *Conn, d time.Duration) []Msg {
	c.c.SetReadDeadline(time.Now().Add(d))
	defer c.c.SetReadDeadline(time.Time{})
	var ms []Msg
	for {
		m, err := c.Recv()
		if err != nil {
			return ms
		}
		ms = append(ms, m)
	}
}

func spawnOne(t *testing.T, c *Conn, req uint64, dir string) string {
	t.Helper()
	send(t, c, Msg{Spawn: &Spawn{Req: req, Dir: dir, Env: []string{"A=1"}, Cols: 80, Rows: 24, Meta: []byte("meta-" + dir)}})
	m := expect(t, c, "Spawned", func(m Msg) bool { return m.Spawned != nil && m.Spawned.Req == req })
	if m.Spawned.Err != "" {
		t.Fatalf("spawn failed: %s", m.Spawned.Err)
	}
	return m.Spawned.Ref
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var refPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestSpawnInputAndFrame(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, w := attach(t, r)
	if w.Protocol != ProtocolVersion || len(w.Sessions) != 0 {
		t.Fatalf("fresh Welcome = %+v", w)
	}
	ref := spawnOne(t, c, 1, "/a")
	if !refPattern.MatchString(ref) {
		t.Fatalf("ref %q is not 16 lowercase hex", ref)
	}
	f := fs.get(t, 0)
	if f.cfg.Dir != "/a" || f.cfg.Cols != 80 || f.cfg.Rows != 24 || f.cfg.Command != terminal.DefaultShell() {
		t.Fatalf("spawn config = %+v", f.cfg)
	}
	if len(f.cfg.Env) != 1 || f.cfg.Env[0] != "A=1" {
		t.Fatalf("spawn env = %q, want the client's", f.cfg.Env)
	}

	send(t, c, Msg{Input: &Input{Ref: ref, Text: "ls"}})
	key := uv.KeyPressEvent{Code: 'x', Text: "x"}
	send(t, c, Msg{Input: &Input{Ref: ref, Key: &key}})
	send(t, c, Msg{Input: &Input{Ref: ref, Wheel: &Wheel{Up: true, X: 1, Y: 2}}})
	eventually(t, "input to reach the session", func() bool {
		return len(f.texts()) == 1 && f.texts()[0] == "ls" && len(f.keyPresses()) == 1 && len(f.wheels()) == 1
	})

	// Frames arrive right after Spawned only on output.
	f.emit()
	m := expect(t, c, "Frame", func(m Msg) bool { return m.Frame != nil })
	if m.Frame.Ref != ref || m.Frame.Screen != f.Render(false) || m.Frame.Title != "title" || m.Frame.Scrollback != 7 {
		t.Fatalf("frame = %+v, want screen %q", m.Frame, f.Render(false))
	}

	// A refused spawn says so.
	fs.mu.Lock()
	fs.fail = true
	fs.mu.Unlock()
	send(t, c, Msg{Spawn: &Spawn{Req: 9, Dir: "/b", Cols: 80, Rows: 24}})
	m = expect(t, c, "Spawned", func(m Msg) bool { return m.Spawned != nil })
	if m.Spawned.Req != 9 || m.Spawned.Err == "" || m.Spawned.Ref != "" {
		t.Fatalf("refused spawn = %+v", m.Spawned)
	}
}

func TestFramesAreThrottled(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attach(t, r)
	ref := spawnOne(t, c, 1, "/a")
	f := fs.get(t, 0)
	start := time.Now()
	for i := 0; i < 100; i++ {
		f.emit()
		time.Sleep(50 * time.Microsecond)
		if time.Since(start) > 5*time.Millisecond {
			break
		}
	}
	frames := 0
	for _, m := range collect(c, 40*time.Millisecond) {
		if m.Frame != nil && m.Frame.Ref == ref {
			frames++
		}
	}
	if frames == 0 || frames > 2 {
		t.Fatalf("got %d frames for a 5ms burst in 40ms, want 1 or 2", frames)
	}
}

func TestFocusRedrawsWithCursor(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attach(t, r)
	ref := spawnOne(t, c, 1, "/a")
	f := fs.get(t, 0)
	f.emit()
	first := expect(t, c, "Frame", func(m Msg) bool { return m.Frame != nil }).Frame

	send(t, c, Msg{Focus: &Focus{Ref: ref, Cursor: true}})
	m := expect(t, c, "focused Frame", func(m Msg) bool { return m.Frame != nil })
	if m.Frame.Screen != f.Render(true) {
		t.Fatalf("focused frame screen = %q, want %q", m.Frame.Screen, f.Render(true))
	}
	if m.Frame.Version <= first.Version {
		t.Fatalf("frame version %d did not rise above %d", m.Frame.Version, first.Version)
	}

	// The same focus again changes nothing, so nothing is sent.
	send(t, c, Msg{Focus: &Focus{Ref: ref, Cursor: true}})
	for _, m := range collect(c, 60*time.Millisecond) {
		if m.Frame != nil {
			t.Fatalf("unchanged focus sent a frame %+v", m.Frame)
		}
	}

	// Resize redraws too.
	send(t, c, Msg{Resize: &Resize{Ref: ref, Cols: 100, Rows: 30}})
	m2 := expect(t, c, "resized Frame", func(m Msg) bool { return m.Frame != nil })
	if f.lastSize() != [2]int{100, 30} || m2.Frame.Version <= m.Frame.Version {
		t.Fatalf("resize: size %v, frame %+v", f.lastSize(), m2.Frame)
	}
}

func TestSecondClientEvictsFirst(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	refA := spawnOne(t, a, 1, "/a")
	refB := spawnOne(t, a, 2, "/b")
	send(t, a, Msg{SetBlob: &SetBlob{Blob: []byte("layout")}})
	fs.get(t, 1).emit() // output while A is still attached is not "changed"
	expect(t, a, "Frame", func(m Msg) bool { return m.Frame != nil })

	b := dial(t, r)
	send(t, b, Msg{Hello: &Hello{Protocol: ProtocolVersion, Attach: true}})
	m := expect(t, a, "Evict", func(m Msg) bool { return m.Evict != nil })
	if m.Evict.Reason != "opened in another window" {
		t.Fatalf("evict reason %q", m.Evict.Reason)
	}
	w := expect(t, b, "Welcome", func(m Msg) bool { return m.Welcome != nil }).Welcome
	if string(w.Blob) != "layout" || w.Live != 2 || len(w.Sessions) != 2 {
		t.Fatalf("B's Welcome = %+v", w)
	}
	if w.Sessions[0].Ref != refA || w.Sessions[1].Ref != refB || string(w.Sessions[1].Meta) != "meta-/b" {
		t.Fatalf("B's sessions = %+v", w.Sessions)
	}
	for _, s := range w.Sessions {
		if s.Changed || s.Exited {
			t.Fatalf("session %+v marked changed or exited", s)
		}
	}
	// B gets a frame per live session at once.
	got := map[string]bool{}
	for len(got) < 2 {
		f := expect(t, b, "initial Frame", func(m Msg) bool { return m.Frame != nil }).Frame
		got[f.Ref] = true
	}
	// A's connection is closed.
	a.c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, err := a.Recv(); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatal("A's connection stayed open")
			}
			break
		}
	}
	// A blob replaced by B is what the next client gets.
	send(t, b, Msg{SetBlob: &SetBlob{Blob: []byte("B's")}})
	send(t, b, Msg{}) // an empty message is ignored, not fatal
	_, w2 := attach(t, r)
	if string(w2.Blob) != "B's" {
		t.Fatalf("third Welcome blob %q", w2.Blob)
	}
}

func TestChangedReportsOutputWhileDetached(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	spawnOne(t, a, 1, "/a")
	spawnOne(t, a, 2, "/b")
	a.Close()
	time.Sleep(50 * time.Millisecond) // let the server see the detach
	fs.get(t, 1).emit()
	_, w := attach(t, r)
	if len(w.Sessions) != 2 || w.Sessions[0].Changed || !w.Sessions[1].Changed {
		t.Fatalf("sessions = %+v, want only the second changed", w.Sessions)
	}
}

func TestServerSurvivesClientVanishingMidFrame(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	ref := spawnOne(t, a, 1, "/a")
	f := fs.get(t, 0)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				f.emit()
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()
	time.Sleep(50 * time.Millisecond)
	a.Close()
	time.Sleep(50 * time.Millisecond)
	b, w := attach(t, r)
	close(stop)
	wg.Wait()
	if len(w.Sessions) != 1 || w.Sessions[0].Ref != ref || w.Sessions[0].Exited || w.Live != 1 {
		t.Fatalf("Welcome after vanish = %+v", w)
	}
	f.emit()
	expect(t, b, "Frame", func(m Msg) bool { return m.Frame != nil && m.Frame.Ref == ref })
}

func TestExitedWhileDetachedIsReportedOnce(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	gone := spawnOne(t, a, 1, "/a")
	kept := spawnOne(t, a, 2, "/b")
	a.Close()
	time.Sleep(50 * time.Millisecond)
	fs.get(t, 0).exit()
	time.Sleep(50 * time.Millisecond)

	b, w := attach(t, r)
	if len(w.Sessions) != 2 || w.Sessions[0].Ref != gone || !w.Sessions[0].Exited || w.Sessions[1].Exited || w.Live != 1 {
		t.Fatalf("first Welcome = %+v", w)
	}
	b.Close()
	_, w = attach(t, r)
	if len(w.Sessions) != 1 || w.Sessions[0].Ref != kept {
		t.Fatalf("second Welcome = %+v, want only %s", w.Sessions, kept)
	}
}

func TestExitWhileAttachedIsSent(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	ref := spawnOne(t, a, 1, "/a")
	spawnOne(t, a, 2, "/b")
	send(t, a, Msg{Kill: &Kill{Ref: ref}})
	m := expect(t, a, "Exit", func(m Msg) bool { return m.Exit != nil })
	if m.Exit.Ref != ref || fs.get(t, 0).closed.Load() == 0 {
		t.Fatalf("exit = %+v, closed %d", m.Exit, fs.get(t, 0).closed.Load())
	}
	_, w := attach(t, r)
	if len(w.Sessions) != 1 || w.Sessions[0].Ref == ref {
		t.Fatalf("Welcome after a delivered Exit = %+v", w.Sessions)
	}
}

func TestServeReturnsWhenEmptyAndDetached(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	ref := spawnOne(t, a, 1, "/a")
	a.Close()
	time.Sleep(50 * time.Millisecond)
	if r.servedYet() {
		t.Fatal("Serve returned with a live session")
	}
	fs.get(t, 0).exit()
	if err := r.waitServed(t, time.Second); err != nil {
		t.Fatalf("Serve = %v, want nil", err)
	}
	b, err := os.ReadFile(r.exited)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != ref {
		t.Fatalf("exited file %q, want %s", b, ref)
	}

	// With a client attached and nothing running it keeps going.
	r2 := startServer(t, fs.spawn)
	c, _ := attach(t, r2)
	ref2 := spawnOne(t, c, 1, "/b")
	send(t, c, Msg{Kill: &Kill{Ref: ref2}})
	expect(t, c, "Exit", func(m Msg) bool { return m.Exit != nil })
	time.Sleep(100 * time.Millisecond)
	if r2.servedYet() {
		t.Fatal("Serve returned with a client attached")
	}
	c.Close()
	if err := r2.waitServed(t, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r2.exited); !os.IsNotExist(err) {
		t.Fatalf("an exit the client saw was written to the exited file: %v", err)
	}
}

// An Exit that was queued for a client that went before reading it was
// never seen by any window, so it must still reach the exited file.
func TestExitLostWithTheClientIsRecorded(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	ref := spawnOne(t, a, 1, "/a")
	// Answers this client never reads fill the socket, so the server's
	// writer stalls and the Exit below waits in its queue.
	for i := 0; i < 100; i++ {
		send(t, a, Msg{Query: &Query{Req: uint64(i), Ref: ref, Offset: 100000}})
	}
	send(t, a, Msg{Kill: &Kill{Ref: ref}})
	eventually(t, "the session to be killed", func() bool { return fs.get(t, 0).closed.Load() > 0 })
	time.Sleep(50 * time.Millisecond) // let the server queue the Exit
	a.Close()
	if err := r.waitServed(t, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(r.exited)
	if err != nil || strings.TrimSpace(string(b)) != ref {
		t.Fatalf("exited file %q (%v), want %s", b, err, ref)
	}
}

func TestSizesAreClamped(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attach(t, r)
	send(t, c, Msg{Spawn: &Spawn{Req: 1, Cols: 5000, Rows: 1 << 30}})
	ref := expect(t, c, "Spawned", func(m Msg) bool { return m.Spawned != nil }).Spawned.Ref
	if f := fs.get(t, 0); f.cfg.Cols != maxSize || f.cfg.Rows != maxSize {
		t.Fatalf("spawn size %dx%d, want %d each", f.cfg.Cols, f.cfg.Rows, maxSize)
	}
	send(t, c, Msg{Resize: &Resize{Ref: ref, Cols: 1 << 20, Rows: 2000}})
	expect(t, c, "Frame", func(m Msg) bool { return m.Frame != nil })
	if got := fs.get(t, 0).lastSize(); got != [2]int{maxSize, maxSize} {
		t.Fatalf("resize to %v, want %d each", got, maxSize)
	}
}

func TestListenerClosedFromOutsideStopsEverything(t *testing.T) {
	fs := &fakes{}
	dir := t.TempDir()
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(fs.spawn, filepath.Join(dir, "exited"))
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	r := &running{srv: srv, sock: filepath.Join(dir, "s")}
	a, _ := attach(t, r)
	spawnOne(t, a, 1, "/a")
	ln.Close()
	m := expect(t, a, "Evict", func(m Msg) bool { return m.Evict != nil })
	if m.Evict.Reason != "stopped by tm stop" {
		t.Fatalf("evict %q", m.Evict.Reason)
	}
	select {
	case err := <-served:
		if err == nil {
			t.Fatal("Serve = nil after its listener failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return")
	}
	if fs.get(t, 0).closed.Load() == 0 {
		t.Fatal("session left running")
	}
}

func TestExitedFileIsAppended(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	if err := os.WriteFile(r.exited, []byte("0000000000000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := attach(t, r)
	ref := spawnOne(t, a, 1, "/a")
	a.Close()
	time.Sleep(50 * time.Millisecond)
	fs.get(t, 0).exit()
	r.waitServed(t, time.Second)
	b, _ := os.ReadFile(r.exited)
	if string(b) != "0000000000000000\n"+ref+"\n" {
		t.Fatalf("exited file %q", b)
	}
}

func TestStopKillsEverything(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	spawnOne(t, a, 1, "/a")
	spawnOne(t, a, 2, "/b")

	// tm stop from another process: probe, then Stop.
	p := dial(t, r)
	send(t, p, Msg{Hello: &Hello{Protocol: ProtocolVersion}})
	st := expect(t, p, "Status", func(m Msg) bool { return m.Status != nil })
	if st.Status.Live != 2 {
		t.Fatalf("Status = %+v", st.Status)
	}
	send(t, p, Msg{Stop: &Stop{}})
	m := expect(t, a, "Evict", func(m Msg) bool { return m.Evict != nil })
	if m.Evict.Reason != "stopped by tm stop" {
		t.Fatalf("evict reason %q", m.Evict.Reason)
	}
	for i := 0; i < 2; i++ {
		if fs.get(t, i).closed.Load() == 0 {
			t.Fatalf("session %d not closed", i)
		}
	}
	if err := r.waitServed(t, time.Second); err != nil {
		t.Fatal(err)
	}
	// The stopper's connection is closed once the server is gone.
	p.c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := p.Recv(); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("stopper's connection: %v", err)
	}
}

func TestStopFromAttachedClient(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	spawnOne(t, a, 1, "/a")
	send(t, a, Msg{Stop: &Stop{}})
	r.waitServed(t, time.Second)
	if fs.get(t, 0).closed.Load() == 0 {
		t.Fatal("session not closed")
	}
}

func TestProbeLeavesTheClientAlone(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	a, _ := attach(t, r)
	ref := spawnOne(t, a, 1, "/a")

	// A probe of any protocol version gets Status (ruling R2b).
	for _, proto := range []int{ProtocolVersion, ProtocolVersion + 41} {
		p := dial(t, r)
		send(t, p, Msg{Hello: &Hello{Protocol: proto}})
		if st := expect(t, p, "Status", func(m Msg) bool { return m.Status != nil }); st.Status.Live != 1 {
			t.Fatalf("Status = %+v", st.Status)
		}
		p.Close()
	}
	// An attach from another version gets the protocol alone and is closed.
	o := dial(t, r)
	send(t, o, Msg{Hello: &Hello{Protocol: ProtocolVersion + 1, Attach: true}})
	ms := collect(o, 300*time.Millisecond)
	if len(ms) != 1 || ms[0].Welcome == nil || ms[0].Welcome.Protocol != ProtocolVersion ||
		ms[0].Welcome.Sessions != nil || ms[0].Welcome.Blob != nil || ms[0].Welcome.Live != 0 {
		t.Fatalf("mismatched attach got %+v", ms)
	}
	o.c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := o.Recv(); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("mismatched attach's connection left open: %v", err)
	}

	// The attached client noticed nothing.
	fs.get(t, 0).emit()
	got := expect(t, a, "Frame", func(m Msg) bool { return m.Frame != nil || m.Evict != nil })
	if got.Frame == nil || got.Frame.Ref != ref {
		t.Fatalf("attached client got %+v", got)
	}
}

func TestQueryAnswersScrolledAndLines(t *testing.T) {
	fs := &fakes{}
	r := startServer(t, fs.spawn)
	c, _ := attach(t, r)
	ref := spawnOne(t, c, 1, "/a")
	send(t, c, Msg{Query: &Query{Req: 5, Ref: ref, Offset: 3}})
	m := expect(t, c, "Answer", func(m Msg) bool { return m.Answer != nil })
	if m.Answer.Req != 5 || m.Answer.Screen != "SCROLLED:3" {
		t.Fatalf("answer = %+v", m.Answer)
	}
	send(t, c, Msg{Query: &Query{Req: 6, Ref: ref, Offset: 3, Lines: true}})
	m = expect(t, c, "Answer", func(m Msg) bool { return m.Answer != nil })
	if m.Answer.Req != 6 || len(m.Answer.Lines) != 2 || m.Answer.Lines[0] != "LINE:3" {
		t.Fatalf("answer = %+v", m.Answer)
	}
	// An unknown ref still gets an answer, so a client never waits forever.
	send(t, c, Msg{Query: &Query{Req: 7, Ref: "nope"}})
	m = expect(t, c, "Answer", func(m Msg) bool { return m.Answer != nil })
	if m.Answer.Req != 7 {
		t.Fatalf("answer = %+v", m.Answer)
	}
	// Input for unknown refs, odd sizes and empty messages are ignored.
	send(t, c, Msg{Input: &Input{Ref: "nope", Text: "x"}})
	send(t, c, Msg{Resize: &Resize{Ref: ref, Cols: -1, Rows: 0}})
	send(t, c, Msg{Kill: &Kill{Ref: "nope"}})
	send(t, c, Msg{Status: &Status{}})
	send(t, c, Msg{})
	send(t, c, Msg{Query: &Query{Req: 8, Ref: ref}})
	if m := expect(t, c, "Answer", func(m Msg) bool { return m.Answer != nil }); m.Answer.Req != 8 {
		t.Fatalf("answer = %+v", m.Answer)
	}
}

func TestServerAnswersQueriesWithNoClient(t *testing.T) {
	// The shell asks for the cursor position and waits for the reply,
	// which only the server's emulator can give while no client is
	// attached (the sleep makes the query land after the client has
	// gone). The reply has no newline, so the tty is put in
	// non-canonical mode to read it; the shell then lingers so the session
	// is still there to query.
	script := `sleep 0.3; printf "\033[6n"; stty -icanon -echo min 1; dd bs=1 count=6 >/dev/null 2>&1; echo got; exec sleep 30`
	spawn := func(c terminal.Config) (Session, error) {
		c.Command = "/bin/sh"
		c.Args = []string{"-c", script}
		return DefaultSpawner(c)
	}
	r := startServer(t, spawn)
	a, _ := attach(t, r)
	ref := spawnOne(t, a, 1, t.TempDir())
	a.Close()
	time.Sleep(500 * time.Millisecond)

	b, w := attach(t, r)
	if len(w.Sessions) != 1 || w.Sessions[0].Exited {
		t.Fatalf("Welcome = %+v", w)
	}
	eventually(t, "the shell to print got", func() bool {
		send(t, b, Msg{Query: &Query{Req: 1, Ref: ref, Lines: true}})
		m := expect(t, b, "Answer", func(m Msg) bool { return m.Answer != nil })
		return strings.Contains(strings.Join(m.Answer.Lines, "\n"), "got")
	})
}

func TestDefaultSpawnerUsesTheShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	s, err := DefaultSpawner(terminal.Config{Dir: t.TempDir(), Cols: 20, Rows: 5})
	if err != nil {
		t.Fatal(err)
	}
	s.SendText("exit\n")
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		s.Close()
		t.Fatal("shell did not exit")
	}
	if _, err := DefaultSpawner(terminal.Config{Command: "/bin/sh", Cols: 0, Rows: 0}); err == nil {
		t.Fatal("a zero size spawned")
	}
}

func TestListenReplacesOnlyAStaleSocket(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, err := PathsFor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Nothing there.
	ln, err := Listen(p)
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()

	// A socket file left by a server that died.
	stale, err := net.Listen("unix", p.Socket)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	stale.Close()
	ln, err = Listen(p)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	defer ln.Close()

	// A live listener's socket is never removed.
	if l2, err := Listen(p); err == nil {
		l2.Close()
		t.Fatal("Listen took over a live server's socket")
	}
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	c, err := net.Dial("unix", p.Socket)
	if err != nil {
		t.Fatalf("live socket gone after a refused Listen: %v", err)
	}
	c.Close()
}
