package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dex/pb"
	"dex/protocol"
)

func TestWitnessViewsPassCanonicalRechargeEconomics(t *testing.T) {
	const node = "node.example:443"
	req := &pb.RechargeRequest{
		RequestId: "request-1", Amount: "10000", GrossAmount: "10000",
		EconomicsPolicy: protocol.RechargeEconomicsLaunchV1, ReceiverNetAmount: "9998", RequesterRewardAmount: "1", WitnessRewardAmount: "1",
	}
	srv := newFrostWithdrawV2TestServer([]string{node}, func(request *http.Request) (*http.Response, error) {
		return frostWithdrawV2ProtoResponse(request, http.StatusOK, &pb.RechargeRequestList{Requests: []*pb.RechargeRequest{req}})
	})
	for _, personal := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/witness/requests?node="+node+"&witness=owner", nil)
		if personal {
			srv.handleWitnessMyRequests(recorder, request)
		} else {
			srv.handleWitnessRequests(recorder, request)
		}
		if recorder.Code != 200 {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
		var items []WitnessRequestItem
		if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil || len(items) != 1 {
			t.Fatalf("items=%+v err=%v", items, err)
		}
		got := items[0]
		if got.EconomicsPolicy != protocol.RechargeEconomicsLaunchV1 || got.GrossAmount != "10000" || got.ReceiverNetAmount != "9998" || got.RequesterRewardAmount != "1" || got.WitnessRewardAmount != "1" {
			t.Fatalf("personal=%v missing economics: %+v", personal, got)
		}
	}
}

func TestRechargeRewardExplorerProxyKeepsBatchAndNodeError(t *testing.T) {
	const node = "node.example:443"
	calls := 0
	srv := newFrostWithdrawV2TestServer([]string{node}, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Path != "/witness/rewards" || request.URL.Query().Get("owner") != "fbc1owner" ||
			len(request.URL.Query()["request_id"]) != 2 || request.URL.Query().Has("node") {
			t.Fatalf("proxy changed reward query: %s", request.URL.String())
		}
		return &http.Response{StatusCode: 500, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader("cold KV failed")), Request: request}, nil
	})
	query := url.Values{"node": {node}, "owner": {"fbc1owner"}, "request_id": {"a", "b"}}
	recorder := httptest.NewRecorder()
	srv.handleFutureProxy("/witness/rewards")(recorder, httptest.NewRequest(http.MethodGet, "/api/witness/rewards?"+query.Encode(), nil))
	if calls != 1 || recorder.Code != 500 || !strings.Contains(recorder.Body.String(), "cold KV failed") {
		t.Fatalf("calls=%d response=%d %s", calls, recorder.Code, recorder.Body.String())
	}
}
