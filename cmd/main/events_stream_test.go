package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dex/config"

	"github.com/gorilla/websocket"
)

const eventsStreamTestToken = "events-stream-test-secret"

func newEventsStreamTestServer(t *testing.T, hub *eventsHub) (*httptest.Server, string) {
	t.Helper()
	t.Setenv(config.ExplorerServiceTokenEnv, eventsStreamTestToken)
	server := httptest.NewServer(http.HandlerFunc(hub.handleWS))
	t.Cleanup(func() {
		hub.closeAll()
		server.Close()
	})
	return server, "ws" + strings.TrimPrefix(server.URL, "http")
}

func eventsStreamAuthHeader() http.Header {
	header := make(http.Header)
	header.Set(config.ExplorerServiceHeader, "explorer")
	header.Set(config.ExplorerServiceTokenHeader, eventsStreamTestToken)
	return header
}

func currentEventsClient(hub *eventsHub) *eventsClient {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.client
}

func waitForEventsClient(t *testing.T, hub *eventsHub, match func(*eventsClient) bool) *eventsClient {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		client := currentEventsClient(hub)
		if match(client) {
			return client
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for events client state")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestEventsStreamRejectsMissingExplorerToken(t *testing.T) {
	hub := newEventsHub()
	_, wsURL := newEventsStreamTestServer(t, hub)

	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("unauthenticated websocket connection unexpectedly succeeded")
	}
	if err == nil {
		t.Fatal("unauthenticated websocket dial unexpectedly returned no error")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		if response == nil {
			t.Fatal("unauthenticated websocket response is nil")
		}
		t.Fatalf("unauthenticated websocket status=%d, want %d", response.StatusCode, http.StatusForbidden)
	}
	_ = response.Body.Close()
	if got := currentEventsClient(hub); got != nil {
		t.Fatal("unauthenticated request installed an events client")
	}
}

func TestEventsStreamSuccessfulUpgradeReplacesSingleClient(t *testing.T) {
	hub := newEventsHub()
	_, wsURL := newEventsStreamTestServer(t, hub)

	firstConn, _, err := websocket.DefaultDialer.Dial(wsURL, eventsStreamAuthHeader())
	if err != nil {
		t.Fatalf("dial first events stream: %v", err)
	}
	defer firstConn.Close()
	first := waitForEventsClient(t, hub, func(client *eventsClient) bool { return client != nil })

	secondConn, _, err := websocket.DefaultDialer.Dial(wsURL, eventsStreamAuthHeader())
	if err != nil {
		t.Fatalf("dial replacement events stream: %v", err)
	}
	defer secondConn.Close()
	second := waitForEventsClient(t, hub, func(client *eventsClient) bool { return client != nil && client != first })

	_ = firstConn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := firstConn.ReadMessage(); err == nil {
		t.Fatal("replaced events connection remained open")
	}
	if got := currentEventsClient(hub); got != second {
		t.Fatal("closing the replaced connection removed the active client")
	}
}

func TestEventsStreamFailedUpgradeKeepsExistingClient(t *testing.T) {
	hub := newEventsHub()
	server, wsURL := newEventsStreamTestServer(t, hub)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, eventsStreamAuthHeader())
	if err != nil {
		t.Fatalf("dial events stream: %v", err)
	}
	defer conn.Close()
	active := waitForEventsClient(t, hub, func(client *eventsClient) bool { return client != nil })

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("build failed-upgrade request: %v", err)
	}
	for key, values := range eventsStreamAuthHeader() {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send failed-upgrade request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("failed-upgrade status=%d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if got := currentEventsClient(hub); got != active {
		t.Fatal("failed websocket upgrade evicted the active client")
	}
}

func TestEventsStreamStaleDisconnectDoesNotRemoveReplacement(t *testing.T) {
	hub := newEventsHub()
	_, wsURL := newEventsStreamTestServer(t, hub)

	firstConn, _, err := websocket.DefaultDialer.Dial(wsURL, eventsStreamAuthHeader())
	if err != nil {
		t.Fatalf("dial first events stream: %v", err)
	}
	defer firstConn.Close()
	stale := waitForEventsClient(t, hub, func(client *eventsClient) bool { return client != nil })

	secondConn, _, err := websocket.DefaultDialer.Dial(wsURL, eventsStreamAuthHeader())
	if err != nil {
		t.Fatalf("dial replacement events stream: %v", err)
	}
	defer secondConn.Close()
	current := waitForEventsClient(t, hub, func(client *eventsClient) bool { return client != nil && client != stale })

	                                                                        
	                                          
	hub.unregister(stale)
	if got := currentEventsClient(hub); got != current {
		t.Fatal("stale disconnect removed the replacement events client")
	}
}

func TestEventsStreamCloseAllRejectsLaterConnections(t *testing.T) {
	hub := newEventsHub()
	_, wsURL := newEventsStreamTestServer(t, hub)

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, eventsStreamAuthHeader())
	if err != nil {
		t.Fatalf("dial events stream: %v", err)
	}
	defer conn.Close()
	waitForEventsClient(t, hub, func(client *eventsClient) bool { return client != nil })

	hub.closeAll()
	if got := currentEventsClient(hub); got != nil {
		t.Fatal("closeAll retained the active events client")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("closeAll left the active websocket open")
	}

	replacement, _, err := websocket.DefaultDialer.Dial(wsURL, eventsStreamAuthHeader())
	if err != nil {
		                                                                         
		return
	}
	defer replacement.Close()
	_ = replacement.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := replacement.ReadMessage(); err == nil {
		t.Fatal("events stream accepted a live connection after closeAll")
	}
	if got := currentEventsClient(hub); got != nil {
		t.Fatal("post-closeAll connection was retained")
	}
}
