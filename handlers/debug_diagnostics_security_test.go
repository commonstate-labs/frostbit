package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"dex/config"
	"dex/consensus"
	"dex/db"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/stats"

	"google.golang.org/protobuf/proto"
)

func addDebugServiceCredential(request *http.Request, token string) {
	request.Header.Set(config.ExplorerServiceHeader, "explorer")
	request.Header.Set(config.ExplorerServiceTokenHeader, token)
}

func TestDebugDiagnosticsRoutesRequireServiceToken(t *testing.T) {
	const token = "diagnostics-test-secret"
	t.Setenv(config.ExplorerServiceTokenEnv, token)
	cfg := config.DefaultConfig()
	cfg.Server.EnableDebugAPI = true
	hm := &HandlerManager{cfg: cfg, Stats: stats.NewStats()}
	mux := http.NewServeMux()
	hm.RegisterRoutes(mux)

	paths := []string{
		"/logs",
		"/frost/metrics",
		"/orderbook/debug?pair=BTC_USDT",
	}
	for _, path := range paths {
		t.Run("rejects_"+strings.Trim(strings.ReplaceAll(path, "/", "_"), "_"), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("GET %s status=%d body=%q, want 403", path, recorder.Code, recorder.Body.String())
			}
		})
	}

	authorized := []struct {
		path       string
		wantStatus int
	}{
		{path: "/logs", wantStatus: http.StatusOK},
		{path: "/frost/metrics", wantStatus: http.StatusOK},
		{path: "/orderbook/debug?pair=BTC_USDT", wantStatus: http.StatusServiceUnavailable},
	}
	for _, test := range authorized {
		t.Run("accepts_"+strings.Trim(strings.ReplaceAll(test.path, "/", "_"), "_"), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			addDebugServiceCredential(request, token)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("GET %s status=%d body=%q, want %d", test.path, recorder.Code, recorder.Body.String(), test.wantStatus)
			}
		})
	}
}

func TestOperatorDiagnosticsAlwaysRequireServiceToken(t *testing.T) {
	const token = "operator-diagnostics-test-secret"
	t.Setenv(config.ExplorerServiceTokenEnv, token)
	cfg := config.DefaultConfig()
	cfg.Server.EnableDebugAPI = false
	hm := &HandlerManager{cfg: cfg, Stats: stats.NewStats()}
	mux := http.NewServeMux()
	hm.RegisterRoutes(mux)

	for _, path := range []string{"/system/resources", "/block/timing"} {
		t.Run("rejects_"+strings.TrimPrefix(path, "/"), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("GET %s status=%d body=%q, want 403", path, recorder.Code, recorder.Body.String())
			}
		})

		t.Run("accepts_"+strings.TrimPrefix(path, "/"), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			addDebugServiceCredential(request, token)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("GET %s status=%d body=%q, want 200", path, recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("GET %s Cache-Control=%q, want no-store", path, got)
			}
		})

		t.Run("method_"+strings.TrimPrefix(path, "/"), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, nil)
			addDebugServiceCredential(request, token)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
				t.Fatalf("POST %s status=%d allow=%q, want 405/GET", path, recorder.Code, recorder.Header().Get("Allow"))
			}
		})
	}
}

func TestHandlePauseOnlyAllowsGetAndPost(t *testing.T) {
	hm := &HandlerManager{Stats: stats.NewStats()}
	recorder := httptest.NewRecorder()
	hm.HandlePause(recorder, httptest.NewRequest(http.MethodPut, "/pause", strings.NewReader(`{"paused":true}`)))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, POST" {
		t.Fatalf("PUT /pause status=%d allow=%q, want 405/GET, POST", recorder.Code, recorder.Header().Get("Allow"))
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("PUT /pause Cache-Control=%q, want no-store", recorder.Header().Get("Cache-Control"))
	}
}

func TestPausedNodeStillServesAuthenticatedDebugDiagnostics(t *testing.T) {
	const token = "paused-diagnostics-test-secret"
	t.Setenv(config.ExplorerServiceTokenEnv, token)
	dbManager, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("paused-diagnostics-db", 0))
	if err != nil {
		t.Fatalf("new db manager: %v", err)
	}
	defer dbManager.Close()

	consensusManager := &consensus.ConsensusNodeManager{}
	consensusManager.SetPaused(true)
	cfg := config.DefaultConfig()
	cfg.Server.EnableDebugAPI = true
	hm := &HandlerManager{
		cfg:              cfg,
		Stats:            stats.NewStats(),
		dbManager:        dbManager,
		consensusManager: consensusManager,
	}
	mux := http.NewServeMux()
	hm.RegisterRoutes(mux)

	for _, path := range []string{"/logs", "/orderbook/debug?pair=BTC_USDT&limit=2"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		addDebugServiceCredential(request, token)
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("paused GET %s status=%d body=%q, want 200", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRemovedFrostRescanRouteRemainsTombstoned(t *testing.T) {
	const token = "rescan-tombstone-secret"
	t.Setenv(config.ExplorerServiceTokenEnv, token)
	for _, test := range []struct {
		name     string
		debug    bool
		operator bool
	}{
		{name: "all disabled"},
		{name: "debug enabled", debug: true},
		{name: "operator enabled", operator: true},
		{name: "both enabled", debug: true, operator: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Server.EnableDebugAPI = test.debug
			hm := &HandlerManager{cfg: cfg, Stats: stats.NewStats()}
			hm.SetOperatorControlAPI(test.operator)
			mux := http.NewServeMux()
			hm.RegisterRoutes(mux)

			request := httptest.NewRequest(http.MethodPost, "/frost/rescan", nil)
			addDebugServiceCredential(request, token)
			if _, pattern := mux.Handler(request); pattern != "" {
				t.Fatalf("removed /frost/rescan registered as %q", pattern)
			}
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("/frost/rescan status=%d body=%q, want 404", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestHandleLogsMethodsRequestBoundsAndNoStore(t *testing.T) {
	hm := &HandlerManager{Stats: stats.NewStats()}

	methodRecorder := httptest.NewRecorder()
	hm.HandleLogs(methodRecorder, httptest.NewRequest(http.MethodDelete, "/logs", nil))
	if methodRecorder.Code != http.StatusMethodNotAllowed || methodRecorder.Header().Get("Allow") != "GET, POST" {
		t.Fatalf("DELETE status=%d allow=%q", methodRecorder.Code, methodRecorder.Header().Get("Allow"))
	}
	if got := methodRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("DELETE Cache-Control=%q, want no-store", got)
	}

	tooLarge := httptest.NewRecorder()
	largeRequest := httptest.NewRequest(http.MethodPost, "/logs", bytes.NewReader(make([]byte, maxDebugLogRequestBytes+1)))
	hm.HandleLogs(tooLarge, largeRequest)
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large request status=%d, want 413", tooLarge.Code)
	}

	negativeBody, err := proto.Marshal(&pb.LogsRequest{MaxLines: -1})
	if err != nil {
		t.Fatal(err)
	}
	negative := httptest.NewRecorder()
	hm.HandleLogs(negative, httptest.NewRequest(http.MethodPost, "/logs", bytes.NewReader(negativeBody)))
	if negative.Code != http.StatusBadRequest {
		t.Fatalf("negative max_lines status=%d, want 400", negative.Code)
	}

	valid := httptest.NewRecorder()
	hm.HandleLogs(valid, httptest.NewRequest(http.MethodGet, "/logs", nil))
	if valid.Code != http.StatusOK || valid.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET status=%d cache=%q", valid.Code, valid.Header().Get("Cache-Control"))
	}
	var response pb.LogsResponse
	if err := proto.Unmarshal(valid.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode logs response: %v", err)
	}
}

func TestMarshalBoundedLogsCapsLinesMessageAndResponse(t *testing.T) {
	shortLines := make([]*pb.LogLine, maxDebugLogLines+25)
	for i := range shortLines {
		shortLines[i] = &pb.LogLine{Timestamp: "12:00:00.000", Level: "INFO", Message: fmt.Sprintf("line-%04d", i)}
	}
	data, err := marshalBoundedLogs(shortLines, maxDebugLogLines+500)
	if err != nil {
		t.Fatal(err)
	}
	var shortResponse pb.LogsResponse
	if err := proto.Unmarshal(data, &shortResponse); err != nil {
		t.Fatal(err)
	}
	if len(shortResponse.Logs) != maxDebugLogLines {
		t.Fatalf("line count=%d, want %d", len(shortResponse.Logs), maxDebugLogLines)
	}
	if got := shortResponse.Logs[0].Message; got != "line-0025" {
		t.Fatalf("oldest retained line=%q, want line-0025", got)
	}
	if got := shortResponse.Logs[len(shortResponse.Logs)-1].Message; got != "line-1024" {
		t.Fatalf("newest retained line=%q, want line-1024", got)
	}

	longMessage := strings.Repeat("界", maxDebugLogMessageBytes)
	longLines := make([]*pb.LogLine, 300)
	for i := range longLines {
		longLines[i] = &pb.LogLine{Timestamp: strings.Repeat("t", 100), Level: strings.Repeat("L", 100), Message: longMessage}
	}
	data, err = marshalBoundedLogs(longLines, len(longLines))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxDebugLogResponseBytes {
		t.Fatalf("encoded response=%d bytes, max=%d", len(data), maxDebugLogResponseBytes)
	}
	var longResponse pb.LogsResponse
	if err := proto.Unmarshal(data, &longResponse); err != nil {
		t.Fatal(err)
	}
	if len(longResponse.Logs) == 0 || len(longResponse.Logs) >= len(longLines) {
		t.Fatalf("response budget retained %d of %d lines", len(longResponse.Logs), len(longLines))
	}
	for _, line := range longResponse.Logs {
		if len(line.Message) > maxDebugLogMessageBytes || !utf8.ValidString(line.Message) {
			t.Fatalf("invalid bounded message bytes=%d valid=%t", len(line.Message), utf8.ValidString(line.Message))
		}
		if len(line.Timestamp) > maxDebugLogTimestampBytes || len(line.Level) > maxDebugLogLevelBytes {
			t.Fatalf("metadata not bounded: timestamp=%d level=%d", len(line.Timestamp), len(line.Level))
		}
	}
}

func TestMetricsCacheCoalescesConcurrentBuildsAndExpires(t *testing.T) {
	hm := &HandlerManager{}
	var builds atomic.Int32
	builder := func() ([]byte, error) {
		builds.Add(1)
		time.Sleep(10 * time.Millisecond)
		return []byte("metrics"), nil
	}
	now := time.Now()
	const callers = 16
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			payload, err := hm.cachedMetricsPayload(now, builder)
			if err != nil || string(payload) != "metrics" {
				t.Errorf("payload=%q err=%v", payload, err)
			}
		}()
	}
	wg.Wait()
	if got := builds.Load(); got != 1 {
		t.Fatalf("concurrent builds=%d, want 1", got)
	}
	if _, err := hm.cachedMetricsPayload(now.Add(debugMetricsCacheTTL+time.Nanosecond), builder); err != nil {
		t.Fatal(err)
	}
	if got := builds.Load(); got != 2 {
		t.Fatalf("builds after expiry=%d, want 2", got)
	}
}

func TestFrostMetricCountsUseExactRecordFamilies(t *testing.T) {
	job := &pb.FrostWithdrawJobState{JobId: "job-1"}
	jobRaw, err := proto.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	withdraw := &pb.FrostWithdrawState{WithdrawId: "withdraw-1"}
	withdrawRaw, err := proto.Marshal(withdraw)
	if err != nil {
		t.Fatal(err)
	}
	records := map[string][]byte{
		keys.KeyFrostWithdrawJob(job.JobId):            jobRaw,
		keys.KeyFrostWithdraw(withdraw.WithdrawId):     withdrawRaw,
		keys.KeyFrostWithdrawCompositePaid("noise-id"): withdrawRaw,
		keys.KeyFrostWithdrawJob("mismatched-key"):     jobRaw,
	}
	if got := countFrostWithdrawJobs(records); got != 1 {
		t.Fatalf("FROST job count=%d, want 1", got)
	}
	if got := countFrostWithdraws(records); got != 1 {
		t.Fatalf("FROST withdraw count=%d, want 1", got)
	}
}

func TestHandleMetricsGETOnlyAndNoStore(t *testing.T) {
	hm := &HandlerManager{Stats: stats.NewStats()}
	post := httptest.NewRecorder()
	hm.HandleGetMetrics(post, httptest.NewRequest(http.MethodPost, "/frost/metrics", nil))
	if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST status=%d allow=%q", post.Code, post.Header().Get("Allow"))
	}
	if post.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("POST Cache-Control=%q", post.Header().Get("Cache-Control"))
	}

	get := httptest.NewRecorder()
	hm.HandleGetMetrics(get, httptest.NewRequest(http.MethodGet, "/frost/metrics", nil))
	if get.Code != http.StatusOK || get.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("GET status=%d cache=%q", get.Code, get.Header().Get("Cache-Control"))
	}
	var response pb.MetricsResponse
	if err := proto.Unmarshal(get.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode metrics response: %v", err)
	}
}

func TestOrderBookDebugGETOnlyAndBoundedScan(t *testing.T) {
	methodRecorder := httptest.NewRecorder()
	(&HandlerManager{}).HandleOrderBookDebug(methodRecorder, httptest.NewRequest(http.MethodPost, "/orderbook/debug", nil))
	if methodRecorder.Code != http.StatusMethodNotAllowed || methodRecorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST status=%d allow=%q", methodRecorder.Code, methodRecorder.Header().Get("Allow"))
	}
	if methodRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("POST Cache-Control=%q", methodRecorder.Header().Get("Cache-Control"))
	}

	var scanCalls int
	var pointReads int
	scan := func(prefix string, limit int, reverse bool) ([]interfaces.KVPair, error) {
		scanCalls++
		if limit != 3 {
			t.Fatalf("scan limit=%d, want 3", limit)
		}
		if scanCalls == 1 && !reverse {
			t.Fatal("buy scan must be reverse")
		}
		if scanCalls == 2 && reverse {
			t.Fatal("sell scan must be forward")
		}
		rows := make([]interfaces.KVPair, limit)
		for i := range rows {
			rows[i].Key = fmt.Sprintf("%sprice:%067d|order_id:order-%d-%d", prefix, i+1, scanCalls, i)
		}
		return rows, nil
	}
	get := func(string) ([]byte, error) {
		pointReads++
		return proto.Marshal(&pb.OrderState{Price: "100", Amount: "10", FilledBase: "1"})
	}
	recorder := httptest.NewRecorder()
	handleOrderBookDebugWithStore(recorder, httptest.NewRequest(http.MethodGet, "/orderbook/debug?pair=BTC_USDT&limit=4", nil), scan, get)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if scanCalls != 2 || pointReads != 4 {
		t.Fatalf("scan calls=%d point reads=%d, want 2/4", scanCalls, pointReads)
	}
	var response struct {
		TotalCount int  `json:"total_count"`
		IndexCount int  `json:"index_count"`
		Limit      int  `json:"limit"`
		Truncated  bool `json:"truncated"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TotalCount != 4 || response.IndexCount != 4 || response.Limit != 4 || !response.Truncated {
		t.Fatalf("unexpected response: %+v", response)
	}

	for _, target := range []string{
		"/orderbook/debug?pair=BTC_USDT&limit=501",
		"/orderbook/debug?pair=BTC_USDT%7Cside:0&limit=1",
	} {
		bad := httptest.NewRecorder()
		handleOrderBookDebugWithStore(bad, httptest.NewRequest(http.MethodGet, target, nil), scan, get)
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("GET %s status=%d, want 400", target, bad.Code)
		}
	}
}

func TestOrderBookDebugRejectsOversizedResponse(t *testing.T) {
	largeOrderID := strings.Repeat("x", (maxOrderBookDebugResponseBytes/2)+1024)
	scan := func(prefix string, _ int, _ bool) ([]interfaces.KVPair, error) {
		if strings.Contains(prefix, fmt.Sprintf("side:%d", pb.OrderSide_SELL)) {
			return nil, nil
		}
		return []interfaces.KVPair{{Key: prefix + "price:1|order_id:" + largeOrderID}}, nil
	}
	get := func(string) ([]byte, error) { return nil, fmt.Errorf("not found") }
	recorder := httptest.NewRecorder()
	handleOrderBookDebugWithStore(recorder, httptest.NewRequest(http.MethodGet, "/orderbook/debug?pair=BTC_USDT&limit=2", nil), scan, get)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d response bytes=%d, want 413", recorder.Code, recorder.Body.Len())
	}
}

func TestOrderBookDebugPrefixesRemainScopedToPair(t *testing.T) {
	var prefixes []string
	scan := func(prefix string, _ int, _ bool) ([]interfaces.KVPair, error) {
		prefixes = append(prefixes, prefix)
		return nil, nil
	}
	recorder := httptest.NewRecorder()
	handleOrderBookDebugWithStore(recorder, httptest.NewRequest(http.MethodGet, "/orderbook/debug?pair=FB_ETH.USDT&limit=2", nil), scan, func(string) ([]byte, error) { return nil, nil })
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	want := []string{
		keys.KeyOrderPriceIndexPrefix("FB_ETH.USDT", pb.OrderSide_BUY, false),
		keys.KeyOrderPriceIndexPrefix("FB_ETH.USDT", pb.OrderSide_SELL, false),
	}
	if len(prefixes) != len(want) || prefixes[0] != want[0] || prefixes[1] != want[1] {
		t.Fatalf("prefixes=%q, want %q", prefixes, want)
	}
}
