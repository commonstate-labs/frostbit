package main

import (
	"net"
	"testing"
	"time"
)

type acceptedConnection struct {
	conn net.Conn
	err  error
}

func TestConnectionLimitListenerReleasesClosedConnections(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newConnectionLimitListener(base, 1).(*connectionLimitListener)
	defer limited.Close()

	accepted := make(chan acceptedConnection, 1)
	go func() {
		conn, err := limited.Accept()
		accepted <- acceptedConnection{conn: conn, err: err}
	}()
	client, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	first := <-accepted
	if first.err != nil {
		t.Fatalf("first accept: %v", first.err)
	}
	if err := first.conn.Close(); err != nil {
		t.Fatalf("close first accepted connection: %v", err)
	}

	go func() {
		conn, err := limited.Accept()
		accepted <- acceptedConnection{conn: conn, err: err}
	}()
	secondClient, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer secondClient.Close()
	select {
	case second := <-accepted:
		if second.err != nil {
			t.Fatalf("second accept: %v", second.err)
		}
		_ = second.conn.Close()
	case <-time.After(time.Second):
		t.Fatal("slot was not released after first connection closed")
	}
}

func TestConnectionLimitListenerCloseUnblocksFullAccept(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limited := newConnectionLimitListener(base, 1).(*connectionLimitListener)

	accepted := make(chan acceptedConnection, 1)
	go func() {
		conn, err := limited.Accept()
		accepted <- acceptedConnection{conn: conn, err: err}
	}()
	client, err := net.DialTimeout("tcp", base.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	first := <-accepted
	if first.err != nil {
		t.Fatalf("first accept: %v", first.err)
	}
	defer first.conn.Close()

	blocked := make(chan acceptedConnection, 1)
	go func() {
		conn, err := limited.Accept()
		blocked <- acceptedConnection{conn: conn, err: err}
	}()
	if err := limited.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	select {
	case result := <-blocked:
		if result.conn != nil {
			_ = result.conn.Close()
		}
		if result.err == nil {
			t.Fatal("full accept returned nil error after listener closed")
		}
	case <-time.After(time.Second):
		t.Fatal("full accept did not unblock after listener closed")
	}
}
