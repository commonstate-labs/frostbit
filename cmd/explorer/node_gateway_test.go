package main

import (
	"dex/config"
	"io"
	"net/http"
	"strings"
	"testing"
)

type explorerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f explorerRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNodeGatewayAuthenticatesExplorerServiceRequests(t *testing.T) {
	const token = "test-explorer-service-secret"
	t.Setenv(config.ExplorerServiceTokenEnv, token)

	var gotService, gotToken string
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotService = req.Header.Get(config.ExplorerServiceHeader)
		gotToken = req.Header.Get(config.ExplorerServiceTokenHeader)
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: 2,
			Body:          io.NopCloser(strings.NewReader("ok")),
			Header:        make(http.Header),
			Request:       req,
		}, nil
	})}
	gateway := newNodeGateway(client, []string{"node.example:443"}, 1)
	req, err := http.NewRequest(http.MethodGet, "https://node.example:443/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := gateway.Do(req)
	if err != nil {
		t.Fatalf("gateway.Do: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close response: %v", err)
	}
	if gotService != "explorer" || gotToken != token {
		t.Fatalf("service headers=%q/%q, want explorer and configured token", gotService, gotToken)
	}
}
