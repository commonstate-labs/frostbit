package main

import (
	"net"
	"testing"
)

type testNetAddr string

func (a testNetAddr) Network() string { return "tcp" }
func (a testNetAddr) String() string  { return string(a) }

func TestConnectionGuardEnforcesTotalAndPerIP(t *testing.T) {
	guard := newConnectionGuard(3, 2, nil)
	release1, ok := guard.acquire(testNetAddr("192.0.2.1:1001"))
	if !ok {
		t.Fatal("first connection rejected")
	}
	release2, ok := guard.acquire(testNetAddr("192.0.2.1:1002"))
	if !ok {
		t.Fatal("second connection rejected")
	}
	if _, ok := guard.acquire(testNetAddr("192.0.2.1:1003")); ok {
		t.Fatal("per-IP limit was not enforced")
	}
	release3, ok := guard.acquire(testNetAddr("192.0.2.2:1001"))
	if !ok {
		t.Fatal("third total connection rejected")
	}
	if _, ok := guard.acquire(testNetAddr("192.0.2.3:1001")); ok {
		t.Fatal("total limit was not enforced")
	}

	release1()
	release1()                         
	if release4, ok := guard.acquire(testNetAddr("192.0.2.1:1004")); !ok {
		t.Fatal("released capacity was not reusable")
	} else {
		release4()
	}
	release2()
	release3()

	guard.mu.Lock()
	defer guard.mu.Unlock()
	if guard.total != 0 || len(guard.perIP) != 0 {
		t.Fatalf("connection state leaked: total=%d perIP=%v", guard.total, guard.perIP)
	}
}

func TestConnectionIP(t *testing.T) {
	tests := []struct {
		addr net.Addr
		want string
	}{
		{testNetAddr("192.0.2.10:6000"), "192.0.2.10"},
		{testNetAddr("[2001:db8::1]:6000"), "2001:db8::1"},
		{nil, "_unknown"},
	}
	for _, tt := range tests {
		if got := connectionIP(tt.addr); got != tt.want {
			t.Fatalf("connectionIP(%v) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}

func TestShouldVerifyQUICSource(t *testing.T) {
	if shouldVerifyQUICSource(testNetAddr("127.0.0.1:6000")) {
		t.Fatal("loopback QUIC should not pay a Retry round trip")
	}
	if !shouldVerifyQUICSource(testNetAddr("198.51.100.1:6000")) {
		t.Fatal("remote QUIC source must be address-verified")
	}
}

func TestConnectionGuardReservesCapacityForVerifiedSources(t *testing.T) {
	verified := map[string]bool{"192.0.2.99": true}
	guard := newConnectionGuard(4, 4, func(ip string) bool { return verified[ip] })
	var releases []func()
	for i := 0; i < 3; i++ {
		release, ok := guard.acquire(testNetAddr("192.0.2.1:1000"))
		if !ok {
			t.Fatal("untrusted budget rejected before capacity")
		}
		releases = append(releases, release)
	}
	if _, ok := guard.acquire(testNetAddr("192.0.2.2:1000")); ok {
		t.Fatal("unknown IP rotation consumed verified connection reserve")
	}
	release, ok := guard.acquire(testNetAddr("192.0.2.99:1000"))
	if !ok {
		t.Fatal("verified source could not connect under anonymous saturation")
	}
	release()
	delete(verified, "192.0.2.99")
	if _, ok := guard.acquire(testNetAddr("192.0.2.99:1000")); ok {
		t.Fatal("expired source still consumed verified connection reserve")
	}
	for _, release := range releases {
		release()
		release()
	}
	if guard.total != 0 || guard.untrusted != 0 || len(guard.perIP) != 0 {
		t.Fatalf("connection counters leaked: %+v", guard)
	}
}
