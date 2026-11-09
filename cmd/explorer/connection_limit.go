package main

import (
	"net"
	"net/http"
	"sync"
)

                                                                           
                                                                              
                                                                            
                         
type connectionLimitListener struct {
	net.Listener
	slots chan struct{}
	done  chan struct{}
	once  sync.Once
}

func newConnectionLimitListener(listener net.Listener, maxConnections int) net.Listener {
	if maxConnections <= 0 {
		maxConnections = 1
	}
	return &connectionLimitListener{
		Listener: listener,
		slots:    make(chan struct{}, maxConnections),
		done:     make(chan struct{}),
	}
}

func (l *connectionLimitListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	conn, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &connectionLimitConn{Conn: conn, release: func() { <-l.slots }}, nil
}

func (l *connectionLimitListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

type connectionLimitConn struct {
	net.Conn
	release func()
	once    sync.Once
}

func (c *connectionLimitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

func serveExplorerWithConnectionLimit(server *http.Server, maxConnections int) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	return server.Serve(newConnectionLimitListener(listener, maxConnections))
}
