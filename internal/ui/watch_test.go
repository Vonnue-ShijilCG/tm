package ui

import (
	"testing"
	"time"

	"github.com/Vonnue-ShijilCG/tm/internal/workspace"
)

// A program printing steadily signals far faster than the screen can be
// redrawn, and Bubble Tea rebuilds the whole window for every message it
// delivers. One wake must therefore cover a whole burst: before the window
// a chatty program woke the UI about 350 times a second, and five in six of
// those frames were built and thrown away by a renderer that ships sixty.
func TestAnOutputBurstWakesTheUIOnce(t *testing.T) {
	f := newFakeSession("/ws", 80, 24)
	term := &workspace.Terminal{ID: 7, Session: f}

	stop, signalled := make(chan struct{}), make(chan int, 1)
	go func() { // a program printing as fast as it can
		n := 0
		for {
			select {
			case <-stop:
				signalled <- n
				return
			case f.output <- struct{}{}:
				n++
			}
		}
	}()

	start := time.Now()
	msg := watchTerminal(term)()
	elapsed := time.Since(start)
	close(stop)
	n := <-signalled

	out, ok := msg.(terminalOutputMsg)
	if !ok {
		t.Fatalf("msg = %T, want terminalOutputMsg", msg)
	}
	if out.id != term.ID {
		t.Errorf("id = %d, want %d", out.id, term.ID)
	}
	if n < 2 {
		t.Fatalf("the producer signalled %d times: there was no burst to collapse", n)
	}
	if elapsed < outputWindow {
		t.Errorf("woke after %v, before the %v window closed: %d signals each woke the UI",
			elapsed, outputWindow, n)
	}
	t.Logf("%d signals collapsed into one wake", n)
}

// An exit must not wait out the window. The pane goes away when it lands, so
// holding it back only postpones saying the terminal is gone — and a program
// that prints and then exits would sit on screen for a window longer.
func TestAnExitDoesNotWaitOutTheOutputWindow(t *testing.T) {
	f := newFakeSession("/ws", 80, 24)
	term := &workspace.Terminal{ID: 3, Session: f}
	f.output <- struct{}{} // a burst has started, so the window is open
	go func() {
		time.Sleep(outputWindow / 4)
		close(f.done)
	}()

	start := time.Now()
	msg := watchTerminal(term)()
	elapsed := time.Since(start)

	if _, ok := msg.(terminalExitedMsg); !ok {
		t.Fatalf("msg = %T, want terminalExitedMsg", msg)
	}
	if elapsed >= outputWindow {
		t.Errorf("the exit was reported after %v, a full window: it should not wait for output to settle", elapsed)
	}
}

// An exit with nothing pending is reported straight away, as it was before
// the window existed.
func TestAnIdleTerminalsExitIsReportedAtOnce(t *testing.T) {
	f := newFakeSession("/ws", 80, 24)
	term := &workspace.Terminal{ID: 4, Session: f}
	close(f.done)

	start := time.Now()
	msg := watchTerminal(term)()

	if _, ok := msg.(terminalExitedMsg); !ok {
		t.Fatalf("msg = %T, want terminalExitedMsg", msg)
	}
	if elapsed := time.Since(start); elapsed >= outputWindow {
		t.Errorf("an idle exit took %v, want well under the %v window", elapsed, outputWindow)
	}
}

// coalescedSession is a session whose Output is already throttled to one
// signal per frame upstream, as a tm server's is.
type coalescedSession struct{ *fakeSession }

func (coalescedSession) Coalesced() bool { return true }

// A server already waits out a frame window before it pushes a frame, so
// waiting out another here would put two windows between a keystroke and
// its echo. A coalesced session's signal wakes the UI at once; the server's
// window still holds a steady printer to one wake per frame.
func TestACoalescedSessionWakesTheUIAtOnce(t *testing.T) {
	f := coalescedSession{newFakeSession("/ws", 80, 24)}
	term := &workspace.Terminal{ID: 5, Session: f}

	fastest := time.Hour
	for range 10 {
		f.output <- struct{}{}
		start := time.Now()
		msg := watchTerminal(term)()
		fastest = min(fastest, time.Since(start))
		if _, ok := msg.(terminalOutputMsg); !ok {
			t.Fatalf("msg = %T, want terminalOutputMsg", msg)
		}
	}
	// Every one of ten waiting the full window is what a second throttle
	// looks like; a scheduler hiccup cannot fake all ten.
	if fastest >= outputWindow {
		t.Errorf("a coalesced session's output never woke the UI in under %v (fastest %v): it waited out a second window", outputWindow, fastest)
	}
}
