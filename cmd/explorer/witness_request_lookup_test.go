package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dex/pb"
)

func TestWitnessRequestPointLookupProxyPreservesSelectedIDAndArray(t *testing.T) {
	for _, found := range []bool{false, true} {
		t.Run(fmt.Sprint(found), func(t *testing.T) {
			const id = "old+deposit/request"
			calls := 0
			srv := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path != "/witness/requests" || req.URL.Query().Get("request_id") != id || len(req.URL.Query()) != 1 {
					t.Fatalf("unexpected upstream query: %s", req.URL)
				}
				result := &pb.RechargeRequestList{}
				if found {
					result.Requests = []*pb.RechargeRequest{{RequestId: id, Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED, Amount: "100"}}
				}
				return frostWithdrawV2ProtoResponse(req, http.StatusOK, result)
			})
			recorder := httptest.NewRecorder()
			srv.handleWitnessRequests(recorder, httptest.NewRequest(http.MethodGet, "/api/witness/requests?node=node.example:443&request_id="+url.QueryEscape(id), nil))
			if recorder.Code != http.StatusOK || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body)
			}
			var items []WitnessRequestItem
			if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil || items == nil {
				t.Fatalf("expected JSON array: body=%s err=%v", recorder.Body, err)
			}
			if found {
				if len(items) != 1 || items[0].RequestID != id || items[0].Status != "RECHARGE_FINALIZED" || items[0].Amount != "100" {
					t.Fatalf("unexpected exact result: %+v", items)
				}
			} else if len(items) != 0 {
				t.Fatalf("missing request returned %+v", items)
			}
		})
	}
}

func TestWitnessRequestPointLookupProxyRejectsInvalidIDBeforeNetwork(t *testing.T) {
	srv := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
		t.Fatalf("invalid lookup reached upstream: %s", req.URL)
		return nil, fmt.Errorf("unexpected request")
	})
	for _, id := range []string{"", " ", strings.Repeat("a", 257)} {
		recorder := httptest.NewRecorder()
		srv.handleWitnessRequests(recorder, httptest.NewRequest(http.MethodGet, "/api/witness/requests?request_id="+url.QueryEscape(id), nil))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
		}
	}
}

func TestWitnessMyRequestsProxyIncludesHistoryAndShelveMetadata(t *testing.T) {
	const witnessAddr = "witness+history"
	srv := newFrostWithdrawV2TestServer([]string{"node.example:443"}, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/witness/my_requests" || req.URL.Query().Get("witness") != witnessAddr || req.URL.Query().Get("include_history") != "1" {
			t.Fatalf("unexpected upstream query: %s", req.URL)
		}
		return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.RechargeRequestList{
			Requests: []*pb.RechargeRequest{{
				RequestId:       "req-shelved",
				Status:          pb.RechargeRequestStatus_RECHARGE_SHELVED,
				SelectionHeight: 80,
				DeadlineHeight:  100,
				ShelveHeight:    100,
				Round:           1,
			}},
		})
	})
	recorder := httptest.NewRecorder()
	srv.handleWitnessMyRequests(recorder, httptest.NewRequest(http.MethodGet, "/api/witness/my_requests?node=node.example:443&witness="+url.QueryEscape(witnessAddr), nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	var items []WitnessRequestItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%v", items)
	}
	got := items[0]
	if got.SelectionHeight != 80 || got.LastVotingDeadlineHeight != 100 || got.ShelveHeight != 100 || got.ShelveReason != "NO_ELIGIBLE_REPLACEMENT_WITNESSES" {
		t.Fatalf("shelve metadata=%+v", got)
	}
}
