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
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if drop, ok := l.w.takeDropFault(); ok {
		return &dropConn{Conn: c, after: drop.After}, nil
	}
	if delay, ok := l.w.latency(); ok {
		return &slowConn{Conn: c, delay: delay}, nil
	}
	return c, nil
}

// slowConn delays every read, modelling a server that is still backfilling so
// the UI's in-progress state lasts long enough to look at (DEV.md section 4).
type slowConn struct {
	net.Conn
	delay time.Duration
}

func (c *slowConn) Read(p []byte) (int, error) {
	time.Sleep(c.delay)
	return c.Conn.Read(p)
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
