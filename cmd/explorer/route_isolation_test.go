package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrderBookDebugRouteIsAdminOnly(t *testing.T) {
	srv := &server{}
	publicMux := http.NewServeMux()
	adminMux := http.NewServeMux()
	registerExplorerPublicOrderBookRoutes(publicMux, srv)
	registerExplorerAdminOrderBookRoutes(adminMux, srv)

	debugRequest := httptest.NewRequest(http.MethodGet, "/api/orderbook/debug", nil)
	if _, pattern := publicMux.Handler(debugRequest); pattern != "" {
		t.Fatalf("debug route unexpectedly registered on public mux as %q", pattern)
	}
	if _, pattern := adminMux.Handler(debugRequest); pattern != "/api/orderbook/debug" {
		t.Fatalf("debug route admin pattern=%q", pattern)
	}
	orderbookRequest := httptest.NewRequest(http.MethodGet, "/api/orderbook", nil)
	if _, pattern := publicMux.Handler(orderbookRequest); pattern != "/api/orderbook" {
		t.Fatalf("public orderbook route pattern=%q", pattern)
	}
}
