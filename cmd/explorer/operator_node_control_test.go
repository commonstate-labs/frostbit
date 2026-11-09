package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dex/config"
	"dex/pb"
	"dex/utils"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"google.golang.org/protobuf/proto"
)

func newOperatorNodeControlTestAuth(t *testing.T) (*simulatorOperatorAuth, *simulatorOperatorSession) {
	t.Helper()
	privateKey := secp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{9}, 32))
	address, err := utils.DeriveDexAddress(privateKey)
	if err != nil {
		t.Fatalf("derive operator address: %v", err)
	}
	auth, err := newSimulatorOperatorAuth(address, "http://127.0.0.1:8080", 7, true, nil)
	if err != nil {
		t.Fatalf("new operator auth: %v", err)
	}
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }
	session, err := auth.createSession(address, now)
	if err != nil {
		t.Fatalf("create operator session: %v", err)
	}
	return auth, session
}

func addOperatorSession(req *http.Request, auth *simulatorOperatorAuth, session *simulatorOperatorSession, withCSRF bool) {
	req.AddCookie(&http.Cookie{Name: auth.cookieName(), Value: session.id})
	req.Header.Set("Origin", auth.publicOrigin)
	if withCSRF {
		req.Header.Set(simulatorCSRFHeader, session.csrf)
	}
}

func TestOperatorNodeDiagnosticsRequiresSessionAndUsesServiceBoundary(t *testing.T) {
	const serviceToken = "operator-diagnostics-service-secret"
	t.Setenv(config.ExplorerServiceTokenEnv, serviceToken)
	auth, session := newOperatorNodeControlTestAuth(t)
	var outbound atomic.Int32
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		outbound.Add(1)
		if got := req.Header.Get(config.ExplorerServiceHeader); got != "explorer" {
			t.Errorf("service header=%q", got)
		}
		if got := req.Header.Get(config.ExplorerServiceTokenHeader); got != serviceToken {
			t.Errorf("service token=%q", got)
		}
		status := http.StatusOK
		contentType := "application/json"
		body := []byte(`{}`)
		switch req.URL.Path {
		case "/system/resources":
			body = []byte(`{"process_memory_bytes":1234,"go_routines":7}`)
		case "/block/timing":
			body = []byte(`{"available":true,"timing":{"height":9,"total_ms":12}}`)
		case "/pause":
			body = []byte(`{"paused":true}`)
		case "/frost/metrics":
			contentType = "application/x-protobuf"
			body, _ = proto.Marshal(&pb.MetricsResponse{HeapAlloc: 55, NumGoroutine: 8, ApiCallStats: map[string]uint64{"/status": 4}})
		case "/logs":
			contentType = "application/x-protobuf"
			body, _ = proto.Marshal(&pb.LogsResponse{Logs: []*pb.LogLine{{Level: "INFO", Message: "ready"}}})
		default:
			status = http.StatusNotFound
			body = []byte(`not found`)
		}
		return &http.Response{
			StatusCode:    status,
			ContentLength: int64(len(body)),
			Body:          io.NopCloser(bytes.NewReader(body)),
			Header:        http.Header{"Content-Type": []string{contentType}},
			Request:       req,
		}, nil
	})}
	srv := &server{
		gateway: newNodeGateway(client, []string{"node-a.example:6000"}, 5),
		timeout: time.Second,
	}
	mux := http.NewServeMux()
	registerSimulatorOperatorRoutes(mux, srv, auth)
	path := "/api/operator/node/diagnostics?address=node-a.example%3A6000"

	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthorized.Code != http.StatusUnauthorized || outbound.Load() != 0 {
		t.Fatalf("unauthorized status=%d outbound=%d body=%s", unauthorized.Code, outbound.Load(), unauthorized.Body.String())
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, path, nil)
	addOperatorSession(authorizedRequest, auth, session, false)
	authorized := httptest.NewRecorder()
	mux.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK || outbound.Load() != 4 {
		t.Fatalf("authorized status=%d outbound=%d body=%s", authorized.Code, outbound.Load(), authorized.Body.String())
	}
	if authorized.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control=%q", authorized.Header().Get("Cache-Control"))
	}
	var diagnostics nodeDiagnostics
	if err := json.Unmarshal(authorized.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if diagnostics.Address != "node-a.example:6000" || diagnostics.Resources == nil || diagnostics.Resources.ProcessMemoryBytes != 1234 || diagnostics.BlockTiming == nil || diagnostics.BlockTiming.Height != 9 || diagnostics.FrostMetrics == nil || diagnostics.FrostMetrics.ApiCallStats["/status"] != 4 || len(diagnostics.Logs) != 0 || diagnostics.IsPaused == nil || !*diagnostics.IsPaused || len(diagnostics.Errors) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}

	withLogsRequest := httptest.NewRequest(http.MethodGet, path+"&include_logs=true", nil)
	addOperatorSession(withLogsRequest, auth, session, false)
	withLogs := httptest.NewRecorder()
	mux.ServeHTTP(withLogs, withLogsRequest)
	if withLogs.Code != http.StatusOK || outbound.Load() != 9 {
		t.Fatalf("with logs status=%d outbound=%d body=%s", withLogs.Code, outbound.Load(), withLogs.Body.String())
	}
	if err := json.Unmarshal(withLogs.Body.Bytes(), &diagnostics); err != nil {
		t.Fatalf("decode diagnostics with logs: %v", err)
	}
	if len(diagnostics.Logs) != 1 || diagnostics.Logs[0].Message != "ready" {
		t.Fatalf("diagnostics logs=%+v", diagnostics.Logs)
	}

	disallowedRequest := httptest.NewRequest(http.MethodGet, "/api/operator/node/diagnostics?address=attacker.example%3A443", nil)
	addOperatorSession(disallowedRequest, auth, session, false)
	disallowed := httptest.NewRecorder()
	mux.ServeHTTP(disallowed, disallowedRequest)
	if disallowed.Code != http.StatusBadRequest || outbound.Load() != 9 {
		t.Fatalf("disallowed status=%d outbound=%d body=%s", disallowed.Code, outbound.Load(), disallowed.Body.String())
	}
}

func TestOperatorNodePauseRouteRequiresSessionOriginAndCSRF(t *testing.T) {
	auth, session := newOperatorNodeControlTestAuth(t)
	var outbound atomic.Int32
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		outbound.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"paused":true}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	srv := &server{gateway: newNodeGateway(client, []string{"node-a.example:6000"}, 2)}
	mux := http.NewServeMux()
	registerSimulatorOperatorRoutes(mux, srv, auth)

	methodRequest := httptest.NewRequest(http.MethodGet, "/api/node/pause", nil)
	methodRecorder := httptest.NewRecorder()
	mux.ServeHTTP(methodRecorder, methodRequest)
	if methodRecorder.Code != http.StatusMethodNotAllowed || methodRecorder.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET status=%d allow=%q", methodRecorder.Code, methodRecorder.Header().Get("Allow"))
	}

	body := `{"nodes":["node-a.example:6000"],"paused":true}`
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/node/pause", strings.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("without session status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	withoutCSRFRequest := httptest.NewRequest(http.MethodPost, "/api/node/pause", strings.NewReader(body))
	addOperatorSession(withoutCSRFRequest, auth, session, false)
	withoutCSRF := httptest.NewRecorder()
	mux.ServeHTTP(withoutCSRF, withoutCSRFRequest)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("without CSRF status=%d body=%s", withoutCSRF.Code, withoutCSRF.Body.String())
	}

	wrongOriginRequest := httptest.NewRequest(http.MethodPost, "/api/node/pause", strings.NewReader(body))
	addOperatorSession(wrongOriginRequest, auth, session, true)
	wrongOriginRequest.Header.Set("Origin", "https://attacker.example")
	wrongOrigin := httptest.NewRecorder()
	mux.ServeHTTP(wrongOrigin, wrongOriginRequest)
	if wrongOrigin.Code != http.StatusForbidden {
		t.Fatalf("wrong origin status=%d body=%s", wrongOrigin.Code, wrongOrigin.Body.String())
	}

	authorizedRequest := httptest.NewRequest(http.MethodPost, "/api/node/pause", strings.NewReader(body))
	addOperatorSession(authorizedRequest, auth, session, true)
	authorized := httptest.NewRecorder()
	mux.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK || outbound.Load() != 1 {
		t.Fatalf("authorized status=%d outbound=%d body=%s", authorized.Code, outbound.Load(), authorized.Body.String())
	}
}

func TestOperatorMinerToggleRouteRequiresPOSTSessionOriginAndCSRF(t *testing.T) {
	auth, session := newOperatorNodeControlTestAuth(t)
	var outbound atomic.Int32
	var outboundMethod, outboundHost, outboundPath, outboundQuery string
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		outbound.Add(1)
		outboundMethod = req.Method
		outboundHost = req.URL.Host
		outboundPath = req.URL.Path
		outboundQuery = req.URL.RawQuery
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"tx_id":"miner-tx-1","op":"ADD"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	srv := &server{gateway: newNodeGateway(client, []string{"node-a.example:6000"}, 2)}
	mux := http.NewServeMux()
	registerSimulatorOperatorRoutes(mux, srv, auth)
	path := "/api/debug/toggle_miner?node=node-a.example%3A6000&op=join"

	methodRecorder := httptest.NewRecorder()
	mux.ServeHTTP(methodRecorder, httptest.NewRequest(http.MethodGet, path, nil))
	if methodRecorder.Code != http.StatusMethodNotAllowed || methodRecorder.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET status=%d allow=%q", methodRecorder.Code, methodRecorder.Header().Get("Allow"))
	}

	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("without session status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	withoutCSRFRequest := httptest.NewRequest(http.MethodPost, path, nil)
	addOperatorSession(withoutCSRFRequest, auth, session, false)
	withoutCSRF := httptest.NewRecorder()
	mux.ServeHTTP(withoutCSRF, withoutCSRFRequest)
	if withoutCSRF.Code != http.StatusForbidden {
		t.Fatalf("without CSRF status=%d body=%s", withoutCSRF.Code, withoutCSRF.Body.String())
	}

	wrongOriginRequest := httptest.NewRequest(http.MethodPost, path, nil)
	addOperatorSession(wrongOriginRequest, auth, session, true)
	wrongOriginRequest.Header.Set("Origin", "https://attacker.example")
	wrongOrigin := httptest.NewRecorder()
	mux.ServeHTTP(wrongOrigin, wrongOriginRequest)
	if wrongOrigin.Code != http.StatusForbidden {
		t.Fatalf("wrong origin status=%d body=%s", wrongOrigin.Code, wrongOrigin.Body.String())
	}

	invalidOperationRequest := httptest.NewRequest(http.MethodPost, "/api/debug/toggle_miner?node=node-a.example%3A6000&op=invalid&amount=999", nil)
	addOperatorSession(invalidOperationRequest, auth, session, true)
	invalidOperation := httptest.NewRecorder()
	mux.ServeHTTP(invalidOperation, invalidOperationRequest)
	if invalidOperation.Code != http.StatusBadRequest || outbound.Load() != 0 {
		t.Fatalf("invalid operation status=%d outbound=%d body=%s", invalidOperation.Code, outbound.Load(), invalidOperation.Body.String())
	}

	authorizedRequest := httptest.NewRequest(http.MethodPost, path, nil)
	addOperatorSession(authorizedRequest, auth, session, true)
	authorized := httptest.NewRecorder()
	mux.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK || outbound.Load() != 1 {
		t.Fatalf("authorized status=%d outbound=%d body=%s", authorized.Code, outbound.Load(), authorized.Body.String())
	}
	if contentType := authorized.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("authorized content type=%q", contentType)
	}
	if outboundMethod != http.MethodPost || outboundHost != "node-a.example:6000" || outboundPath != "/debug/toggle_miner" {
		t.Fatalf("outbound method=%q host=%q path=%q", outboundMethod, outboundHost, outboundPath)
	}
	query, err := url.ParseQuery(outboundQuery)
	if err != nil {
		t.Fatalf("parse outbound query: %v", err)
	}
	if query.Get("op") != "join" || query.Has("node") {
		t.Fatalf("outbound query=%q", outboundQuery)
	}
	var response map[string]any
	if err := json.Unmarshal(authorized.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode authorized response: %v", err)
	}
	if ok, _ := response["ok"].(bool); !ok || response["tx_id"] != "miner-tx-1" {
		t.Fatalf("authorized response=%v", response)
	}
}

func TestOperatorNodePauseBatchValidatesTargetsAndReportsPartialFailure(t *testing.T) {
	auth, session := newOperatorNodeControlTestAuth(t)
	var mu sync.Mutex
	requests := make(map[string]string)
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		mu.Lock()
		requests[req.URL.Host] = string(body)
		mu.Unlock()
		status := http.StatusOK
		responseBody := `{"ok":true,"paused":true}`
		if req.URL.Host == "node-b.example:6000" {
			status = http.StatusServiceUnavailable
			responseBody = `temporarily unavailable`
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(responseBody)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	srv := &server{gateway: newNodeGateway(client, []string{"node-a.example:6000", "node-b.example:6000"}, 4)}
	mux := http.NewServeMux()
	registerSimulatorOperatorRoutes(mux, srv, auth)

	request := httptest.NewRequest(http.MethodPost, "/api/node/pause", strings.NewReader(`{"nodes":["node-a.example:6000","node-b.example:6000","node-a.example:6000"],"paused":true}`))
	addOperatorSession(request, auth, session, true)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("batch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response operatorNodePauseResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	if response.OK || len(response.Results) != 2 || !response.Results[0].OK || response.Results[1].OK {
		t.Fatalf("unexpected batch response: %+v", response)
	}
	if response.Results[0].Node != "node-a.example:6000" || response.Results[1].Node != "node-b.example:6000" {
		t.Fatalf("result order=%+v", response.Results)
	}
	mu.Lock()
	if len(requests) != 2 || requests["node-a.example:6000"] != `{"paused":true}` || requests["node-b.example:6000"] != `{"paused":true}` {
		mu.Unlock()
		t.Fatalf("outbound requests=%v", requests)
	}
	mu.Unlock()

	for name, body := range map[string]string{
		"empty":         `{"nodes":[],"paused":true}`,
		"blank":         `{"nodes":[""],"paused":true}`,
		"not-allowed":   `{"nodes":["attacker.example:443"],"paused":true}`,
		"missing-state": `{"nodes":["node-a.example:6000"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			invalidRequest := httptest.NewRequest(http.MethodPost, "/api/node/pause", strings.NewReader(body))
			addOperatorSession(invalidRequest, auth, session, true)
			invalidRecorder := httptest.NewRecorder()
			mux.ServeHTTP(invalidRecorder, invalidRequest)
			if invalidRecorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", invalidRecorder.Code, invalidRecorder.Body.String())
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("invalid requests reached nodes: %v", requests)
	}
}

func TestOperatorNodePauseUsesDedicatedRequestAndResponseLimits(t *testing.T) {
	auth, session := newOperatorNodeControlTestAuth(t)
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", int(operatorNodePauseResponseBodyLimit)+1))),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	srv := &server{gateway: newNodeGateway(client, []string{"node-a.example:6000"}, 1)}
	mux := http.NewServeMux()
	registerSimulatorOperatorRoutes(mux, srv, auth)

	oversizedRequest := httptest.NewRequest(http.MethodPost, "/api/node/pause", strings.NewReader(strings.Repeat("x", int(explorerAdminBodyLimit)+1)))
	addOperatorSession(oversizedRequest, auth, session, true)
	oversizedRecorder := httptest.NewRecorder()
	newResourceGuard(4).Wrap(mux).ServeHTTP(oversizedRecorder, oversizedRequest)
	if oversizedRecorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status=%d body=%s", oversizedRecorder.Code, oversizedRecorder.Body.String())
	}

	responseRequest := httptest.NewRequest(http.MethodPost, "/api/node/pause", strings.NewReader(`{"nodes":["node-a.example:6000"],"paused":true}`))
	addOperatorSession(responseRequest, auth, session, true)
	responseRecorder := httptest.NewRecorder()
	mux.ServeHTTP(responseRecorder, responseRequest)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("oversized response status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var response operatorNodePauseResponse
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode oversized response result: %v", err)
	}
	if response.OK || len(response.Results) != 1 || response.Results[0].OK || response.Results[0].Error != errOperatorNodePauseResponseTooLarge.Error() {
		t.Fatalf("unexpected oversized response result: %+v", response)
	}
}

func TestOperatorNodePauseRequiresExplicitUpstreamState(t *testing.T) {
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	srv := &server{gateway: newNodeGateway(client, []string{"node-a.example:6000"}, 1)}
	result := srv.pauseConfiguredNode(context.Background(), "node-a.example:6000", false)
	if result.OK || result.Error != "node returned an unexpected pause state" {
		t.Fatalf("missing upstream pause state was accepted: %+v", result)
	}
}

func TestPrivateNodePauseRejectsMissingNodeAndReportsGatewayErrors(t *testing.T) {
	srv := &server{}
	missing := httptest.NewRecorder()
	srv.handleNodePause(missing, httptest.NewRequest(http.MethodGet, "/api/node/pause", nil))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing node status=%d body=%s", missing.Code, missing.Body.String())
	}

	unavailable := httptest.NewRecorder()
	srv.handleNodePause(unavailable, httptest.NewRequest(http.MethodGet, "/api/node/pause?node=node-a.example:6000", nil))
	if unavailable.Code != http.StatusBadGateway {
		t.Fatalf("unavailable node status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
}
