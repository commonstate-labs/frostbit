package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type frostWithdrawV2RoundTripFunc func(*http.Request) (*http.Response, error)

func (f frostWithdrawV2RoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newFrostWithdrawV2TestServer(nodes []string, roundTrip frostWithdrawV2RoundTripFunc) *server {
	client := &http.Client{Transport: roundTrip}
	gateway := newNodeGateway(client, nodes, 16)
	return &server{
		client:               client,
		gateway:              gateway,
		defaultNodes:         append([]string(nil), nodes...),
		timeout:              2 * time.Second,
		withdrawMonitorCache: newBoundedTTLCache[*frostWithdrawMonitorSnapshot](32, 8<<20, 4),
		withdrawDetailCache:  newBoundedTTLCache[[]byte](32, 8<<20, 4),
	}
}

func frostWithdrawV2ProtoResponse(req *http.Request, status int, message proto.Message) (*http.Response, error) {
	data, err := proto.Marshal(message)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode:    status,
		ContentLength: int64(len(data)),
		Header:        http.Header{"Content-Type": []string{"application/x-protobuf"}},
		Body:          io.NopCloser(bytes.NewReader(data)),
		Request:       req,
	}, nil
}

func TestFrostWithdrawMonitorSnapshotSingleflightUsesOnlySelectedNode(t *testing.T) {
	nodes := []string{"primary.example:443", "secondary.example:443"}
	var primaryCalls atomic.Int32
	var secondaryCalls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once

	srv := newFrostWithdrawV2TestServer(nodes, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case nodes[0]:
			primaryCalls.Add(1)
			startedOnce.Do(func() { close(started) })
			select {
			case <-release:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostWithdrawStateList{States: []*pb.FrostWithdrawState{{
				WithdrawId: "primary-withdraw", Chain: "btc", Asset: "BTC", Status: "QUEUED", RequestHeight: 10,
			}}})
		case nodes[1]:
			secondaryCalls.Add(1)
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostWithdrawStateList{States: []*pb.FrostWithdrawState{{
				WithdrawId: "secondary-withdraw", Chain: "btc", Asset: "BTC", Status: "SIGNED", RequestHeight: 99,
			}}})
		default:
			return nil, fmt.Errorf("unexpected node %q", req.URL.Host)
		}
	})

	const callers = 12
	start := make(chan struct{})
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func() {
			<-start
			snapshot, err := srv.fetchFrostWithdrawMonitorSnapshot(context.Background(), "node-0", "BTC", "BTC", 20, 20, false)
			if err == nil && (snapshot == nil || len(snapshot.States) != 1 || snapshot.States[0].WithdrawId != "primary-withdraw") {
				err = fmt.Errorf("unexpected snapshot: %+v", snapshot)
			}
			results <- err
		}()
	}
	close(start)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("selected-node cache fill did not start")
	}
	                                                                           
	                                                                            
	time.Sleep(25 * time.Millisecond)
	close(release)
	for i := 0; i < callers; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}

	                                                                        
	if _, err := srv.fetchFrostWithdrawMonitorSnapshot(context.Background(), nodes[0], "btc", "BTC", 20, 20, false); err != nil {
		t.Fatal(err)
	}
	if got := primaryCalls.Load(); got != 1 {
		t.Fatalf("primary monitor calls = %d, want one shared fill", got)
	}
	if got := secondaryCalls.Load(); got != 0 {
		t.Fatalf("secondary monitor calls = %d, want zero for formal list state", got)
	}
}

func TestFrostWithdrawMonitorPageAllCursorBoundsForeignAndStale(t *testing.T) {
	node := "primary.example:443"
	var calls atomic.Int32
	states := frostWithdrawV2PageStates()
	srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/frost/withdraw/monitor" {
			return nil, fmt.Errorf("unexpected path %q", req.URL.Path)
		}
		calls.Add(1)
		return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostWithdrawStateList{States: states})
	})

	all := requestFrostWithdrawV2Page(t, srv, "/api/frost/withdraw/page?node=node-0&chain=BTC&section=all&limit=2", http.StatusOK)
	if all.Section != "all" || all.Revision == "" || all.Pending == nil || all.Jobs == nil || all.Completed == nil {
		t.Fatalf("incomplete all page: %+v", all)
	}
	if len(all.Pending.Items) != 2 || !all.Pending.HasMore || all.Pending.NextCursor == "" || all.Pending.Total != 3 {
		t.Fatalf("pending first page = %+v", all.Pending)
	}
	if len(all.Jobs.Items) != 2 || !all.Jobs.HasMore || all.Jobs.NextCursor == "" || all.Jobs.Total != 3 {
		t.Fatalf("jobs first page = %+v", all.Jobs)
	}
	if len(all.Completed.Items) != 2 || !all.Completed.HasMore || all.Completed.NextCursor == "" || all.Completed.Total != 3 {
		t.Fatalf("completed first page = %+v", all.Completed)
	}

	secondPath := "/api/frost/withdraw/page?" + url.Values{
		"node": {"node-0"}, "chain": {"BTC"}, "section": {"pending"}, "limit": {"2"}, "cursor": {all.Pending.NextCursor},
	}.Encode()
	second := requestFrostWithdrawV2Page(t, srv, secondPath, http.StatusOK)
	if second.Pending == nil || len(second.Pending.Items) != 1 || second.Pending.HasMore || second.Pending.NextCursor != "" {
		t.Fatalf("pending second page = %+v", second.Pending)
	}
	if second.Pending.Items[0].WithdrawID == all.Pending.Items[0].WithdrawID || second.Pending.Items[0].WithdrawID == all.Pending.Items[1].WithdrawID {
		t.Fatalf("cursor repeated a first-page item: %+v", second.Pending.Items)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("first and second pages made %d upstream calls, want one cached snapshot", got)
	}

	foreignSectionPath := "/api/frost/withdraw/page?" + url.Values{
		"node": {"node-0"}, "chain": {"BTC"}, "section": {"completed"}, "cursor": {all.Pending.NextCursor},
	}.Encode()
	requestFrostWithdrawV2Error(t, srv, foreignSectionPath, http.StatusBadRequest, "WITHDRAW_CURSOR_INVALID")

	foreignFilterPath := "/api/frost/withdraw/page?" + url.Values{
		"node": {"node-0"}, "chain": {"ETH"}, "section": {"pending"}, "cursor": {all.Pending.NextCursor},
	}.Encode()
	requestFrostWithdrawV2Error(t, srv, foreignFilterPath, http.StatusBadRequest, "WITHDRAW_CURSOR_INVALID")

	requestFrostWithdrawV2Error(t, srv, "/api/frost/withdraw/page?node=node-0&chain=BTC&section=pending&cursor=not-base64!", http.StatusBadRequest, "WITHDRAW_CURSOR_INVALID")

	                                                                           
	                                                                      
	srv.markFrostWithdrawMonitorHeight(123)
	if got := srv.withdrawMonitorHeight.Load(); got != 123 {
		t.Fatalf("monitor height = %d after mark, want 123", got)
	}
	continued := requestFrostWithdrawV2Page(t, srv, secondPath, http.StatusOK)
	if continued.Revision != all.Revision || continued.Pending == nil || len(continued.Pending.Items) != 1 {
		t.Fatalf("old cursor did not continue its immutable snapshot: %+v", continued)
	}

	staleCursor := encodeFrostWithdrawPageCursor("pending", "evicted-revision", frostWithdrawPageFilter(node, "btc", ""), 2)
	stalePath := "/api/frost/withdraw/page?" + url.Values{
		"node": {"node-0"}, "chain": {"BTC"}, "section": {"pending"}, "cursor": {staleCursor},
	}.Encode()
	requestFrostWithdrawV2Error(t, srv, stalePath, http.StatusConflict, "WITHDRAW_CURSOR_STALE")

	for raw, want := range map[string]int{"": explorerFrostPageDefault, "0": explorerFrostPageDefault, "bad": explorerFrostPageDefault, "1": 1, "9999": explorerFrostPageMax} {
		if got := parseFrostWithdrawPageLimit(raw); got != want {
			t.Fatalf("parseFrostWithdrawPageLimit(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestWithdrawMonitorV2WebSocketIsInvalidationOnly(t *testing.T) {
	var transportCalls atomic.Int32
	srv := newFrostWithdrawV2TestServer([]string{"primary.example:443"}, func(req *http.Request) (*http.Response, error) {
		transportCalls.Add(1)
		return nil, fmt.Errorf("unexpected transport request to %s", req.URL)
	})
	hub := &wsHub{srv: srv}
	payload := hub.fetchTopicData("withdraw_monitor_v2", []byte(`{"node":"node-0"}`))
	if len(payload.data) == 0 {
		t.Fatal("withdraw_monitor_v2 returned no invalidation payload")
	}
	var got struct {
		Invalidated bool   `json:"invalidated"`
		Revision    string `json:"revision"`
	}
	if err := json.Unmarshal(payload.data, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Invalidated || got.Revision == "" {
		t.Fatalf("unexpected invalidation payload: %s", payload.data)
	}
	if calls := transportCalls.Load(); calls != 0 {
		t.Fatalf("withdraw_monitor_v2 made %d transport calls, want zero", calls)
	}
}

func TestFrostWithdrawDetailAloneFansOutAndKeepsPrimaryCanonicalFields(t *testing.T) {
	nodes := []string{"primary.example:443", "secondary-a.example:443", "secondary-b.example:443"}
	var calls [3]atomic.Int32
	srv := newFrostWithdrawV2TestServer(nodes, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/frost/withdraw/status" || req.URL.Query().Get("withdraw_id") != "withdraw-1" {
			return nil, fmt.Errorf("unexpected detail request %s", req.URL)
		}
		var response *pb.GetWithdrawStatusResponse
		switch req.URL.Host {
		case nodes[0]:
			calls[0].Add(1)
			response = &pb.GetWithdrawStatusResponse{
				LotId: "withdraw-1", Chain: "btc", Status: "SIGNED", Amount: "100", Receiver: "bc1-primary", Height: 123, TxHash: "primary-job",
				PlanningLogs: frostWithdrawV2Logs(0, 200),
			}
		case nodes[1]:
			calls[1].Add(1)
			response = &pb.GetWithdrawStatusResponse{
				LotId: "withdraw-1", Chain: "BTC", Status: "QUEUED", Amount: "999", Receiver: "secondary", Height: 999, TxHash: "secondary-job",
				PlanningLogs: frostWithdrawV2Logs(100, 300),
			}
		case nodes[2]:
			calls[2].Add(1)
			response = &pb.GetWithdrawStatusResponse{
				LotId: "withdraw-1", Chain: "btc", Status: "FAILED", PlanningLogs: frostWithdrawV2Logs(150, 250),
			}
		default:
			return nil, fmt.Errorf("unexpected node %q", req.URL.Host)
		}
		return frostWithdrawV2ProtoResponse(req, http.StatusOK, response)
	})

	data, err := srv.fetchFrostWithdrawDetailJSON(context.Background(), "node-0", "withdraw-1")
	if err != nil {
		t.Fatal(err)
	}
	var detail FrostWithdrawDetailResponse
	if err := json.Unmarshal(data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.WithdrawID != "withdraw-1" || detail.Chain != "btc" || detail.Status != "SIGNED" || detail.Amount != "100" ||
		detail.Receiver != "bc1-primary" || detail.Height != 123 || detail.JobID != "primary-job" {
		t.Fatalf("secondary node overwrote canonical detail fields: %+v", detail)
	}
	if detail.QueriedNodes != len(nodes) || detail.LogSourceCount != len(nodes) || detail.Partial {
		t.Fatalf("unexpected detail source metadata: %+v", detail)
	}
	if len(detail.PlanningLogs) != explorerFrostDetailLogMax {
		t.Fatalf("planning logs = %d, want capped %d", len(detail.PlanningLogs), explorerFrostDetailLogMax)
	}
	if first, last := detail.PlanningLogs[0].Timestamp, detail.PlanningLogs[len(detail.PlanningLogs)-1].Timestamp; first != 44 || last != 299 {
		t.Fatalf("deduplicated/capped log range = %d..%d, want 44..299", first, last)
	}
	for i := range calls {
		if got := calls[i].Load(); got != 1 {
			t.Fatalf("detail calls to node %d = %d, want 1", i, got)
		}
	}

	                                                                
	if _, err := srv.fetchFrostWithdrawDetailJSON(context.Background(), nodes[0], "withdraw-1"); err != nil {
		t.Fatal(err)
	}
	for i := range calls {
		if got := calls[i].Load(); got != 1 {
			t.Fatalf("cached detail calls to node %d = %d, want 1", i, got)
		}
	}
}

func frostWithdrawV2PageStates() []*pb.FrostWithdrawState {
	states := make([]*pb.FrostWithdrawState, 0, 9)
	for i := 1; i <= 3; i++ {
		states = append(states, &pb.FrostWithdrawState{
			WithdrawId: fmt.Sprintf("pending-%d", i), Chain: "btc", Asset: "BTC", Status: "QUEUED", RequestHeight: uint64(i),
		})
	}
	for i := 1; i <= 3; i++ {
		states = append(states, &pb.FrostWithdrawState{
			WithdrawId: fmt.Sprintf("job-withdraw-%d", i), Chain: "btc", Asset: "BTC", Status: "SIGNING", JobId: fmt.Sprintf("job-%d", i), RequestHeight: uint64(10 + i),
			PlanningLogs: []*pb.FrostPlanningLog{{Step: "StartSigning", Status: "OK", Message: fmt.Sprintf("Signing session job-%d started", i), Timestamp: uint64(100 + i)}},
		})
	}
	for i := 1; i <= 3; i++ {
		states = append(states, &pb.FrostWithdrawState{
			WithdrawId: fmt.Sprintf("completed-%d", i), Chain: "btc", Asset: "BTC", Status: "SIGNED", SignedTxId: fmt.Sprintf("signed-%d", i), RequestHeight: uint64(20 + i),
		})
	}
	return states
}

func frostWithdrawV2Logs(from, to int) []*pb.FrostPlanningLog {
	logs := make([]*pb.FrostPlanningLog, 0, to-from)
	for i := from; i < to; i++ {
		logs = append(logs, &pb.FrostPlanningLog{
			Timestamp: uint64(i), Step: "Signing", Status: "OK", Message: fmt.Sprintf("log-%03d", i),
		})
	}
	return logs
}

func requestFrostWithdrawV2Page(t *testing.T, srv *server, path string, wantStatus int) FrostWithdrawMonitorPageResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	srv.handleFrostWithdrawMonitorPage(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != wantStatus {
		t.Fatalf("GET %s status=%d body=%s, want %d", path, recorder.Code, recorder.Body.String(), wantStatus)
	}
	var response FrostWithdrawMonitorPageResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode GET %s: %v", path, err)
	}
	return response
}

func requestFrostWithdrawV2Error(t *testing.T, srv *server, path string, wantStatus int, wantCode string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	srv.handleFrostWithdrawMonitorPage(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != wantStatus {
		t.Fatalf("GET %s status=%d body=%s, want %d", path, recorder.Code, recorder.Body.String(), wantStatus)
	}
	var response APIError
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode GET %s error: %v", path, err)
	}
	if response.Code != wantCode {
		t.Fatalf("GET %s code=%q body=%s, want %q", path, response.Code, recorder.Body.String(), wantCode)
	}
}

func TestFrostWithdrawV2CursorRejectsOversizedPayload(t *testing.T) {
	filter := frostWithdrawPageFilter("primary.example:443", "btc", "BTC")
	if _, err := decodeFrostWithdrawPageCursor(strings.Repeat("a", 1025), "pending", "revision", filter, 10); !errors.Is(err, errFrostWithdrawCursorInvalid) {
		t.Fatalf("oversized cursor error = %v, want invalid cursor", err)
	}
}
