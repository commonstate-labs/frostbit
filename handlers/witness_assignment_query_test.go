package handlers

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestWitnessAssignmentsSkipStaleRowsBeforeLimitAndValidateHistoricalMembership(t *testing.T) {
	hm, manager := newFrostQueryTestHandler(t)
	defer manager.Close()
	for i := 0; i < 130; i++ {
		id := fmt.Sprintf("closed-%03d", i)
		enqueueProto(t, manager, keys.KeyRechargeRequest(id), &pb.RechargeRequest{RequestId: id, Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED})
		manager.EnqueueSet(keys.KeyWitnessAssigned("w", id), id)
		manager.EnqueueSet(keys.KeyWitnessAssignedCurrent("w", id), id)
	}
	for _, id := range []string{"z-current", "z-former"} {
		req := &pb.RechargeRequest{RequestId: id, Status: pb.RechargeRequestStatus_RECHARGE_VOTING, SelectedWitnesses: []string{"w"}}
		if id == "z-former" {
			req.SelectedWitnesses = []string{"replacement"}
			req.CaseCollateral = []*pb.WitnessCaseCollateral{{Address: "w", RoleMask: 1}}
		}
		enqueueProto(t, manager, keys.KeyRechargeRequest(id), req)
		manager.EnqueueSet(keys.KeyWitnessAssigned("w", id), id)
		if id == "z-current" {
			manager.EnqueueSet(keys.KeyWitnessAssignedCurrent("w", id), id)
		}
	}
	                                                                
	enqueueProto(t, manager, keys.KeyRechargeRequest("z-unrelated"), &pb.RechargeRequest{RequestId: "z-unrelated", Status: pb.RechargeRequestStatus_RECHARGE_VOTING, SelectedWitnesses: []string{"other"}})
	manager.EnqueueSet(keys.KeyWitnessAssigned("w", "z-unrelated"), "z-unrelated")
	require.NoError(t, manager.ForceFlush())
	for _, history := range []bool{false, true} {
		r := httptest.NewRecorder()
		hm.HandleWitnessMyRequests(r, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/witness/my_requests?witness=w&limit=2&include_history=%t", history), nil))
		require.Equal(t, http.StatusOK, r.Code, r.Body.String())
		var result pb.RechargeRequestList
		require.NoError(t, proto.Unmarshal(r.Body.Bytes(), &result))
		want := 1
		if history {
			want = 2
		}
		require.Len(t, result.Requests, want)
		require.Equal(t, "z-current", result.Requests[0].RequestId)
	}
	manager.EnqueueSet(keys.KeyRechargeRequest("bad"), string([]byte{0xff}))
	require.NoError(t, manager.ForceFlush())
	require.Error(t, manager.RebuildWitnessAssignmentIndex())
	r := httptest.NewRecorder()
	hm.HandleWitnessMyRequests(r, httptest.NewRequest(http.MethodGet, "/witness/my_requests?witness=w", nil))
	require.Equal(t, http.StatusServiceUnavailable, r.Code)
}

func TestWitnessCurrentAssignmentsNotBlockedByLargeLegitimateCaseHistory(t *testing.T) {
	hm, manager := newFrostQueryTestHandler(t)
	defer manager.Close()
	for i := 0; i < 700; i++ {
		id := fmt.Sprintf("a-old-%04d", i)
		enqueueProto(t, manager, keys.KeyRechargeRequest(id), &pb.RechargeRequest{
			RequestId: id, Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION,
			CaseCollateral: []*pb.WitnessCaseCollateral{{Address: "w", RoleMask: 1}},
		})
	}
	enqueueProto(t, manager, keys.KeyRechargeRequest("z-current"), &pb.RechargeRequest{
		RequestId: "z-current", Status: pb.RechargeRequestStatus_RECHARGE_VOTING, SelectedWitnesses: []string{"w"},
	})
	require.NoError(t, manager.RebuildWitnessAssignmentIndex())
	r := httptest.NewRecorder()
	hm.HandleWitnessMyRequests(r, httptest.NewRequest(http.MethodGet, "/witness/my_requests?witness=w", nil))
	require.Equal(t, http.StatusOK, r.Code, r.Body.String())
	var result pb.RechargeRequestList
	require.NoError(t, proto.Unmarshal(r.Body.Bytes(), &result))
	require.Len(t, result.Requests, 1)
	require.Equal(t, "z-current", result.Requests[0].RequestId)
}
