package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

// Listen listens on p's socket. A socket file is removed first only when
// dialing it proves nothing is there (ECONNREFUSED: a server died and left
// it; ENOENT: there is none); a timeout or EAGAIN may be a busy live server,
// whose socket must never be taken.
func Listen(p Paths) (net.Listener, error) {
	c, err := net.DialTimeout("unix", p.Socket, dialTimeout)
	switch {
	case err == nil:
		c.Close()
		return nil, fmt.Errorf("a tm server is already listening on %s", p.Socket)
	case errors.Is(err, syscall.ECONNREFUSED):
		if err := os.Remove(p.Socket); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	case errors.Is(err, syscall.ENOENT):
	default:
		return nil, fmt.Errorf("checking %s: %w", p.Socket, err)
	}
	return net.Listen("unix", p.Socket)
}
