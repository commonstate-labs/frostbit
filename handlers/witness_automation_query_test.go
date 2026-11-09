package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

func TestActiveWitnessRequestsCursorIgnoresColdHistoryAndSurvivesTerminalCursor(t *testing.T) {
	hm, db := newFrostQueryTestHandler(t)
	defer db.Close()
	for i, id := range []string{"a5500", "b5501", "c5502"} {
		req := &pb.RechargeRequest{RequestId: id, CreateHeight: uint64(100 - i), Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD}
		enqueueProto(t, db, keys.KeyRechargeRequest(id), req)
		db.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(false, req.CreateHeight, id), keys.KeyRechargeRequest(id))
	}
	terminal := &pb.RechargeRequest{RequestId: "cold", CreateHeight: 999, FinalizeHeight: 1000, Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED}
	enqueueProto(t, db, keys.KeyRechargeRequest("cold"), terminal)
	db.EnqueueSet(keys.KeyRechargeQueryRecentGlobal(true, 1000, "cold"), keys.KeyRechargeRequest("cold"))
	if err := db.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	get := func(query string) []*pb.RechargeRequest {
		rec := httptest.NewRecorder()
		hm.HandleWitnessRequests(rec, httptest.NewRequest(http.MethodGet, query, nil))
		if rec.Code != 200 {
			t.Fatal(rec.Code, rec.Body.String())
		}
		var list pb.RechargeRequestList
		if err := proto.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		return list.Requests
	}
	first := get("/witness/requests?active=1&limit=2")
	if len(first) != 2 || first[0].RequestId != "a5500" || first[1].RequestId != "b5501" {
		t.Fatal(first)
	}
	                                                                             
	enqueueProto(t, db, keys.KeyRechargeRequest("b5501"), &pb.RechargeRequest{RequestId: "b5501", CreateHeight: 99, FinalizeHeight: 5000, Status: pb.RechargeRequestStatus_RECHARGE_FINALIZED})
	if err := db.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	last := get("/witness/requests?active=1&after=b5501&limit=2")
	if len(last) != 1 || last[0].RequestId != "c5502" {
		t.Fatal(last)
	}
}

func TestAssignedWitnessChallengesUsesAddressIndexAndStableCursor(t *testing.T) {
	hm, db := newFrostQueryTestHandler(t)
	defer db.Close()
	for _, id := range []string{"a", "b", "c"} {
		enqueueProto(t, db, keys.KeyChallengeRecord(id), &pb.ChallengeRecord{ChallengeId: id, RequestId: "r", Status: pb.ChallengeStatus_CHALLENGE_ACTIVE})
		db.EnqueueSet(keys.KeyArbitratorChallenge("operator", id), id)
	}
	enqueueProto(t, db, keys.KeyChallengeRecord("unassigned"), &pb.ChallengeRecord{ChallengeId: "unassigned"})
	if err := db.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	hm.HandleWitnessChallenges(rec, httptest.NewRequest(http.MethodGet, "/witness/challenges?arbitrator=operator&after=a&limit=2", nil))
	page := decodeChallengeList(t, rec)
	if len(page.Records) != 2 || page.Records[0].ChallengeId != "b" || page.Records[1].ChallengeId != "c" {
		t.Fatal(page)
	}
}
