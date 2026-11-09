package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestWitnessRequestPointLookupFindsOldArchivedRequest(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	const oldID = "old-deposit"
	marker := &pb.RechargeRequest{RequestId: oldID, Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED, FinalizeHeight: 1}
	archived := &pb.RechargeRequest{
		RequestId: oldID, Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED, FinalizeHeight: 1,
		ReceiverAddress: "fbc1qreceiver", Amount: "12345", NativeChain: "eth", DepositProtocol: 1, DepositEventIndex: 7,
	}
	enqueueProto(t, dbMgr, keys.KeyRechargeRequest(oldID), marker)
	enqueueProto(t, dbMgr, keys.KeyArchiveRechargeRequest(oldID), archived)
	                                                                             
	for i := 0; i < 105; i++ {
		id := fmt.Sprintf("new-%03d", i)
		request := &pb.RechargeRequest{RequestId: id, Status: pb.RechargeRequestStatus_RECHARGE_VOTING, CreateHeight: uint64(i + 2)}
		key := keys.KeyRechargeRequest(id)
		enqueueProto(t, dbMgr, key, request)
		dbMgr.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, request.CreateHeight, id), key)
	}
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	pageRecorder := httptest.NewRecorder()
	hm.HandleWitnessRequests(pageRecorder, httptest.NewRequest(http.MethodGet, "/witness/requests", nil))
	var page pb.RechargeRequestList
	if err := proto.Unmarshal(pageRecorder.Body.Bytes(), &page); err != nil || len(page.Requests) != 100 {
		t.Fatalf("recent page=%+v err=%v", &page, err)
	}
	for _, req := range page.Requests {
		if req.RequestId == oldID {
			t.Fatal("fixture unexpectedly exposes saved deposit in recent page")
		}
	}
	recorder := httptest.NewRecorder()
	hm.HandleWitnessRequests(recorder, httptest.NewRequest(http.MethodGet, "/witness/requests?request_id="+oldID+"&offset=5000&limit=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	var result pb.RechargeRequestList
	if err := proto.Unmarshal(recorder.Body.Bytes(), &result); err != nil || len(result.Requests) != 1 || !proto.Equal(result.Requests[0], archived) {
		t.Fatalf("exact result=%+v err=%v", &result, err)
	}
}

func TestWitnessRequestPointLookupMissingAndInvalidRecords(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	dbMgr.EnqueueSet(keys.KeyRechargeRequest("corrupt"), "\xff")
	enqueueProto(t, dbMgr, keys.KeyRechargeRequest("wrong-id"), &pb.RechargeRequest{RequestId: "another-request"})
	enqueueProto(t, dbMgr, keys.KeyRechargeRequest("rejected"), &pb.RechargeRequest{RequestId: "rejected", Status: pb.RechargeRequestStatus_RECHARGE_REJECTED})
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id     string
		status int
		count  int
	}{
		{"missing", http.StatusOK, 0},
		{"rejected", http.StatusOK, 1},
		{"", http.StatusBadRequest, 0},
		{"   ", http.StatusBadRequest, 0},
		{strings.Repeat("a", 257), http.StatusBadRequest, 0},
		{"corrupt", http.StatusInternalServerError, 0},
		{"wrong-id", http.StatusInternalServerError, 0},
	} {
		t.Run(tc.id, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			hm.HandleWitnessRequests(recorder, httptest.NewRequest(http.MethodGet, "/witness/requests?request_id="+url.QueryEscape(tc.id), nil))
			if recorder.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.status, recorder.Body)
			}
			if tc.status == http.StatusOK {
				var result pb.RechargeRequestList
				if err := proto.Unmarshal(recorder.Body.Bytes(), &result); err != nil || len(result.Requests) != tc.count {
					t.Fatalf("result=%+v err=%v", &result, err)
				}
			}
		})
	}
}

func TestWitnessRequestEventLookupUsesCanonicalGlobalIndexes(t *testing.T) {
	hm, dbMgr := newFrostQueryTestHandler(t)
	defer dbMgr.Close()
	accountHash := "0x" + strings.Repeat("ab", 32)
	account := &pb.RechargeRequest{
		RequestId: "account-event", NativeChain: "eth", NativeTxHash: accountHash,
		DepositEventIndex: 7, Status: pb.RechargeRequestStatus_RECHARGE_VOTING,
	}
	btcHash := strings.Repeat("cd", 32)
	btc := &pb.RechargeRequest{
		RequestId: "btc-outpoint", NativeChain: "btc", NativeTxHash: btcHash, NativeVout: 3,
		Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED,
	}
	enqueueProto(t, dbMgr, keys.KeyRechargeRequest(account.RequestId), account)
	enqueueProto(t, dbMgr, keys.KeyRechargeRequest(btc.RequestId), btc)
	dbMgr.EnqueueSet(keys.KeyRechargeEventPending("eth", accountHash, 7), account.RequestId)
	dbMgr.EnqueueSet(keys.KeyRechargeRequestByNativeTx("btc", btcHash+":3"), btc.RequestId)
	if err := dbMgr.ForceFlush(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		query string
		want  string
		code  int
	}{
		{"account", "?chain=eth&native_tx_hash=" + url.QueryEscape(accountHash) + "&deposit_event_index=7", account.RequestId, http.StatusOK},
		{"btc", "?chain=btc&native_tx_hash=" + btcHash + "&native_vout=3", btc.RequestId, http.StatusOK},
		{"missing", "?chain=eth&native_tx_hash=" + url.QueryEscape(accountHash) + "&deposit_event_index=8", "", http.StatusOK},
		{"missing-account-index", "?chain=eth&native_tx_hash=" + url.QueryEscape(accountHash), "", http.StatusBadRequest},
		{"missing-btc-vout", "?chain=btc&native_tx_hash=" + btcHash, "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			hm.HandleWitnessRequests(recorder, httptest.NewRequest(http.MethodGet, "/witness/requests"+tc.query, nil))
			if recorder.Code != tc.code {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.code, recorder.Body)
			}
			if tc.code != http.StatusOK {
				return
			}
			var result pb.RechargeRequestList
			if err := proto.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(result.Requests) != 0 {
					t.Fatalf("unexpected result: %+v", &result)
				}
				return
			}
			if len(result.Requests) != 1 || result.Requests[0].RequestId != tc.want {
				t.Fatalf("result=%+v", &result)
			}
		})
	}
}
