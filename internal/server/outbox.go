package server

import "sync"

// client is the attached connection. Messages are queued and written by
// run in order, so a slow client never blocks a session or the server.
// Only the latest frame per session is kept while it waits.
type client struct {
	conn *Conn
	wake chan struct{}
	done chan struct{} // run has returned and the connection is closed

	mu      sync.Mutex
	q       []*Msg
	frames  map[string]*Msg // ref -> its queued, not yet written frame
	closing bool            // write what is queued, then close
	lost    []string        // refs whose Exit was never written
}

func newClient(conn *Conn) *client {
	return &client{
		conn:   conn,
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		frames: map[string]*Msg{},
	}
}

func (c *client) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *client) push(m Msg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		c.dropped(&m)
		return
	}
	c.q = append(c.q, &m)
	c.signal()
}

func (c *client) pushFrame(f *Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return
	}
	if queued := c.frames[f.Ref]; queued != nil {
		// The output the replaced frame carried still happened.
		f.Printed = f.Printed || queued.Frame.Printed
		queued.Frame = f
		return
	}
	m := &Msg{Frame: f}
	c.frames[f.Ref] = m
	c.q = append(c.q, m)
	c.signal()
}

// evict drops whatever is queued, sends Evict and closes the connection.
func (c *client) evict(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return
	}
	c.drop()
	c.q = []*Msg{{Evict: &Evict{Reason: reason}}}
	c.frames = map[string]*Msg{}
	c.closing = true
	c.signal()
}

// shutdown stops run without writing anything more.
func (c *client) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drop()
	c.closing = true
	c.signal()
}

// drop discards the queue, keeping the refs of the Exits in it. Called
// with mu held.
func (c *client) drop() {
	for _, m := range c.q {
		c.dropped(m)
	}
	c.q = nil
}

// dropped notes a message that will never be written. An Exit that never
// reached a window must still be reported, or a terminal whose end nobody
// saw would be started again. Called with mu held.
func (c *client) dropped(m *Msg) {
	if m != nil && m.Exit != nil {
		c.lost = append(c.lost, m.Exit.Ref)
	}
}

// takeLost returns the refs of Exits that were never written. It is only
// complete once run has returned.
func (c *client) takeLost() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lost := c.lost
	c.lost = nil
	return lost
}

func (c *client) run() {
	defer close(c.done)
	defer c.conn.Close()
	for {
		c.mu.Lock()
		if len(c.q) == 0 {
			closing := c.closing
			c.mu.Unlock()
			if closing {
				return
			}
			<-c.wake
			continue
		}
		m := c.q[0]
		c.q[0] = nil
		c.q = c.q[1:]
		if m.Frame != nil && c.frames[m.Frame.Ref] == m {
			delete(c.frames, m.Frame.Ref)
		}
		c.mu.Unlock()
		if sendNow(c.conn, *m) != nil {
			c.mu.Lock()
			c.dropped(m)
			c.mu.Unlock()
			c.shutdown()
			return
		}
	}
}
