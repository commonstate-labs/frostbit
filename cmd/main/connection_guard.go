package main

import (
	"net"
	"sync"
)

                                                                            
                                                                              
                                                                             
type connectionGuard struct {
	mu         sync.Mutex
	maxTotal   int
	maxPerIP   int
	total      int
	reserve    int
	untrusted  int
	verifiedIP func(string) bool
	perIP      map[string]int
}

func newConnectionGuard(maxTotal, maxPerIP int, verifiedIP func(string) bool) *connectionGuard {
	if maxTotal < 1 {
		maxTotal = 1024
	}
	if maxPerIP < 1 || maxPerIP > maxTotal {
		maxPerIP = maxTotal
	}
	return &connectionGuard{
		maxTotal:   maxTotal,
		maxPerIP:   maxPerIP,
		perIP:      make(map[string]int),
		reserve:    maxTotal / 4,
		verifiedIP: verifiedIP,
	}
}

func (g *connectionGuard) acquire(addr net.Addr) (release func(), ok bool) {
	if g == nil {
		return func() {}, true
	}
	key := connectionIP(addr)
	trusted := g.verifiedIP != nil && g.verifiedIP(key)
	reserve := g.reserve
	if g.verifiedIP == nil {
		reserve = 0
	}

	g.mu.Lock()
	if g.total >= g.maxTotal || g.perIP[key] >= g.maxPerIP || (!trusted && g.untrusted >= g.maxTotal-reserve) {
		g.mu.Unlock()
		return nil, false
	}
	g.total++
	g.perIP[key]++
	if !trusted {
		g.untrusted++
	}
	g.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			if g.total > 0 {
				g.total--
			}
			if !trusted {
				g.untrusted--
			}
			if count := g.perIP[key]; count <= 1 {
				delete(g.perIP, key)
			} else {
				g.perIP[key] = count - 1
			}
			g.mu.Unlock()
		})
	}, true
}

func connectionIP(addr net.Addr) string {
	if addr == nil {
		return "_unknown"
	}
	if host, _, err := net.SplitHostPort(addr.String()); err == nil && host != "" {
		return host
	}
	if addr.String() == "" {
		return "_unknown"
	}
	return addr.String()
}

func shouldVerifyQUICSource(addr net.Addr) bool {
	ip := net.ParseIP(connectionIP(addr))
	return ip == nil || !ip.IsLoopback()
}

type guardedConn struct {
	net.Conn
	release func()
}

func (c *guardedConn) Close() error {
	err := c.Conn.Close()
	if c.release != nil {
		c.release()
	}
	return err
}

type guardedListener struct {
	net.Listener
	guard *connectionGuard
}

func (l *guardedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		release, ok := l.guard.acquire(conn.RemoteAddr())
		if !ok {
			_ = conn.Close()
			continue
		}
		return &guardedConn{Conn: conn, release: release}, nil
	}
}
