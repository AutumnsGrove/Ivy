package mailworld

import (
	"bytes"
	"io"
	"net"
	"sync"
	"time"
)

// faultListener wraps the IMAP listener so an armed DropConnection fault can
// close the next accepted connection part-way through.
type faultListener struct {
	net.Listener
	w *World
}

func (l *faultListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.w.hasFault(Unreachable{}) {
			_ = c.Close()
			continue
		}
		return l.wrap(c), nil
	}
}

func (l *faultListener) wrap(c net.Conn) net.Conn {
	if drop, ok := l.w.takeDropFault(); ok {
		return &dropConn{Conn: c, after: drop.After}
	}
	if delay, ok := l.w.latency(); ok {
		return newSlowConn(c, delay)
	}
	return c
}

// slowConn delays every read, modelling a server that is still backfilling so
// the UI's in-progress state lasts long enough to look at (DEV.md section 4).
type slowConn struct {
	net.Conn
	delay time.Duration

	closeOnce sync.Once
	closed    chan struct{}
}

func newSlowConn(c net.Conn, delay time.Duration) *slowConn {
	return &slowConn{Conn: c, delay: delay, closed: make(chan struct{})}
}

// Read waits out the delay but gives up the moment the connection is closed, so
// a stalled client that hangs up does not leave a sleeping server goroutine.
func (c *slowConn) Read(p []byte) (int, error) {
	timer := time.NewTimer(c.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-c.closed:
		return 0, net.ErrClosed
	}
	return c.Conn.Read(p)
}

func (c *slowConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// dropConn closes the connection once the client has sent After IMAP commands.
// Commands are counted by CRLF, which is enough to inject a mid-session drop
// without understanding literals.
type dropConn struct {
	net.Conn
	after int

	mu    sync.Mutex
	lines int
}

func (c *dropConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		c.lines += bytes.Count(p[:n], []byte("\r\n"))
		drop := c.lines >= c.after
		c.mu.Unlock()
		if drop {
			_ = c.Close()
			if err == nil {
				err = io.EOF
			}
		}
	}
	return n, err
}
