package oidclogin

import (
	"net"
	"sync"
	"sync/atomic"
)

// Only eight accepted connections can retain HTTP server state. Excess local
// connections are closed rather than queued with unbounded per-client tasks.
type callbackListener struct {
	net.Listener
	active atomic.Int32
}
type callbackConn struct {
	net.Conn
	parent *callbackListener
	once   sync.Once
}

func (l *callbackListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		if l.active.Add(1) > 8 {
			l.active.Add(-1)
			c.Close()
			continue
		}
		return &callbackConn{Conn: c, parent: l}, nil
	}
}
func (c *callbackConn) Close() error {
	e := c.Conn.Close()
	c.once.Do(func() { c.parent.active.Add(-1) })
	return e
}
