package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"dex/pb"
)

func TestWitnessTxEnrichmentUsesOnlyExactRequestLookup(t *testing.T) {
	const node = "node.example:443"
	const requestID = "old+deposit/request&limit=500"
	for _, found := range []bool{false, true} {
		name := "missing"
		if found {
			name = "archived"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Path != "/witness/requests" || req.URL.Query().Get("request_id") != requestID || len(req.URL.Query()) != 1 {
					t.Fatalf("single transaction must use an exact request lookup without a list scan: %s", req.URL)
				}
				result := &pb.RechargeRequestList{}
				if found {
					result.Requests = []*pb.RechargeRequest{{
						RequestId: requestID, Status: pb.RechargeRequestStatus_RECHARGE_REJECTED,
						SelectedWitnesses: []string{"original-witness"}, PassCount: 3,
						Votes: []*pb.WitnessVote{{WitnessAddress: "original-witness", VoteType: pb.WitnessVoteType_VOTE_PASS, Reason: "original vote"}},
					}}
				}
				return frostWithdrawV2ProtoResponse(req, http.StatusOK, result)
			})
			info := &txInfo{TxID: requestID, Details: map[string]interface{}{}}
			srv.enrichWitnessRequestInfo(context.Background(), node, info)
			if calls != 1 {
				t.Fatalf("enrichment calls=%d want exactly one; no fallback list scan is allowed", calls)
			}
			if !found {
				if len(info.Details) != 0 {
					t.Fatalf("missing archive invented enrichment: %+v", info.Details)
				}
				return
			}
			if info.Details["recharge_status"] != "RECHARGE_REJECTED" || info.Details["pass_count"] != uint32(3) {
				t.Fatalf("exact archived request was not enriched: %+v", info.Details)
			}
			witnesses, ok := info.Details["selected_witnesses"].([]string)
			if !ok || len(witnesses) != 1 || witnesses[0] != "original-witness" {
				t.Fatalf("selected witnesses lost: %+v", info.Details)
			}
			votes, ok := info.Details["witness_votes"].([]map[string]interface{})
			if !ok || len(votes) != 1 || votes[0]["vote_type"] != "VOTE_PASS" {
				t.Fatalf("original vote enrichment lost: %+v", info.Details)
			}
		})
	}
}

func TestWitnessViewsPreserveArchivedEvidenceWithoutExtraReads(t *testing.T) {
	const node = "node.example:443"
	const requestID = "archived+case/request"
	const ballot = uint64(9007199254740993)
	request := &pb.RechargeRequest{
		RequestId: requestID, ChallengeId: "archived-challenge", Status: pb.RechargeRequestStatus_RECHARGE_REJECTED,
		CreateHeight: 100, SelectionHeight: 102, DecisionHeight: 110, VoteAcceptUntilHeight: 111, FinalizeHeight: 130,
		OriginalPositions: []*pb.WitnessVote{{RequestId: requestID, WitnessAddress: "request-position", VoteType: pb.WitnessVoteType_VOTE_PASS, TxId: "request-original-tx", Timestamp: 110}},
	}
	challenge := &pb.ChallengeRecord{
		ChallengeId: "archived-challenge", RequestId: requestID, ChallengerAddress: "challenger",
		BallotRound:  ballot,
		CreateHeight: 115, SelectionHeight: 117, FinalizeHeight: 130, FinalVote: pb.WitnessVoteType_VOTE_FAIL,
		Status: pb.ChallengeStatus_CHALLENGE_FINALIZED, Finalized: true, ChallengeSuccess: true, Settled: true,
		OriginalVotes:       []*pb.WitnessVote{{RequestId: requestID, WitnessAddress: "frozen-original", VoteType: pb.WitnessVoteType_VOTE_PASS, TxId: "challenge-original-tx", Timestamp: 110}},
		OrdinaryVotes:       []*pb.WitnessVote{{RequestId: requestID, WitnessAddress: "ordinary-judge", VoteType: pb.WitnessVoteType_VOTE_FAIL, BallotRound: ballot, Round: 2, TxId: "ordinary-tx", Timestamp: 125}},
		OrdinaryVotesFrozen: true,
	}
	for _, personal := range []bool{false, true} {
		name := "public"
		if personal {
			name = "personal"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
				calls++
				switch req.URL.Path {
				case "/witness/requests":
					if personal || req.URL.Query().Get("request_id") != requestID || len(req.URL.Query()) != 1 {
						t.Fatalf("unexpected public evidence lookup: %s", req.URL)
					}
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.RechargeRequestList{Requests: []*pb.RechargeRequest{request}})
				case "/witness/my_requests":
					if !personal || req.URL.Query().Get("include_history") != "1" {
						t.Fatalf("personal evidence must include archived history: %s", req.URL)
					}
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.RechargeRequestList{Requests: []*pb.RechargeRequest{request}})
				case "/witness/challenges":
					if req.URL.Query().Get("challenge_ids") != challenge.ChallengeId || len(req.URL.Query()) != 1 {
						t.Fatalf("unexpected challenge lookup: %s", req.URL)
					}
					                                                                 
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.ChallengeRecordList{Records: []*pb.ChallengeRecord{challenge}})
				default:
					t.Fatalf("evidence mapping caused an extra read: %s", req.URL)
					return nil, nil
				}
			})
			recorder := httptest.NewRecorder()
			httpReq := httptest.NewRequest(http.MethodGet, "/api/witness/requests?node="+node+"&request_id="+url.QueryEscape(requestID)+"&witness=judge", nil)
			if personal {
				srv.handleWitnessMyRequests(recorder, httpReq)
			} else {
				srv.handleWitnessRequests(recorder, httpReq)
			}
			if recorder.Code != http.StatusOK || calls != 2 {
				t.Fatalf("status=%d reads=%d want two existing bounded reads: %s", recorder.Code, calls, recorder.Body)
			}
			var items []WitnessRequestItem
			if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil || len(items) != 1 {
				t.Fatalf("items=%+v err=%v", items, err)
			}
			got := items[0]
			if got.DecisionHeight != 110 || got.VoteAcceptUntilHeight != 111 || got.ChallengeCreateHeight != 115 || got.ChallengeSelectionHeight != 117 || got.ChallengeFinalizeHeight != 130 || got.FinalizeHeight != 130 {
				t.Fatalf("request/challenge trajectory heights lost: %+v", got)
			}
			if got.ChallengeFinalVote != "VOTE_FAIL" || got.ChallengerAddress != "challenger" || !got.OrdinaryVotesFrozen || !got.ChallengeFinalized || !got.ChallengeSuccess {
				t.Fatalf("archived resolution evidence lost: %+v", got)
			}
			if len(got.OriginalPositions) != 1 || got.OriginalPositions[0].WitnessAddress != "request-position" || got.OriginalPositions[0].TxId != "request-original-tx" || len(got.OriginalVotes) != 1 || got.OriginalVotes[0].WitnessAddress != "frozen-original" || got.OriginalVotes[0].TxId != "challenge-original-tx" {
				t.Fatalf("request positions and challenge snapshot were conflated: %+v", got)
			}
			if len(got.OrdinaryVotes) != 1 || got.OrdinaryVotes[0].BallotRound != "9007199254740993" || got.OrdinaryVotes[0].Round != 2 || got.OrdinaryVotes[0].VoteType != int32(pb.WitnessVoteType_VOTE_FAIL) || got.OrdinaryVotes[0].TxId != "ordinary-tx" {
				t.Fatalf("ordinary ballot evidence lost: %+v", got)
			}
			var raw []map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"decision_height", "vote_accept_until_height", "original_positions", "challenge_create_height", "challenge_selection_height", "challenge_finalize_height", "challenge_final_vote", "challenger_address", "original_votes", "ordinary_votes", "ordinary_votes_frozen"} {
				if _, present := raw[0][field]; !present {
					t.Fatalf("evidence JSON field %q absent: %s", field, recorder.Body)
				}
			}
		})
	}
}

func TestWitnessReadModelDoesNotInventUnfinishedFinalVote(t *testing.T) {
	var got WitnessRequestItem
	applyWitnessChallengeFields(&got, &pb.ChallengeRecord{Status: pb.ChallengeStatus_CHALLENGE_ACTIVE})
	if got.ChallengeFinalVote != "" {
		t.Fatalf("unfinished case must not expose protobuf default PASS as a verdict: %+v", got)
	}
	applyWitnessChallengeFields(&got, &pb.ChallengeRecord{Status: pb.ChallengeStatus_CHALLENGE_FINALIZED, Finalized: true, Settled: true})
	if got.ChallengeFinalVote != "" {
		t.Fatalf("refunded funding without an arbitration ballot must not invent a PASS verdict: %+v", got)
	}
	for _, verdict := range []pb.WitnessVoteType{pb.WitnessVoteType_VOTE_PASS, pb.WitnessVoteType_VOTE_FAIL} {
		applyWitnessChallengeFields(&got, &pb.ChallengeRecord{Status: pb.ChallengeStatus_CHALLENGE_FINALIZED, Finalized: true, BallotRound: 1, FinalVote: verdict})
		if got.ChallengeFinalVote != verdict.String() {
			t.Fatalf("final verdict %s lost: %+v", verdict, got)
		}
	}
}

func TestWitnessViewsExposeFrozenCourtAndExactBallotIdentity(t *testing.T) {
	const node = "node.example:443"
	const ballot = uint64(9007199254740993)
	request := &pb.RechargeRequest{RequestId: "case", ChallengeId: "challenge", Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION}
	challenge := &pb.ChallengeRecord{ChallengeId: "challenge", RequestId: "case", Status: pb.ChallengeStatus_CHALLENGE_ACTIVE,
		ArbitrationStage: pb.ArbitrationStage_ARBITRATION_FINAL_COURT, ArbitrationRound: 3, BallotRound: ballot, DeadlineHeight: 99999,
		ArbitrationPeriodBlocks: 20,
		OrdinaryCommitteeSizes:  []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500, CourtSnapshotHeight: 1234,
		FinalCourt: []string{"a", "b", "c", "d"}, Arbitrators: []string{"a", "b", "c", "d"}, RequiredStake: "1000000000", StakeAmount: "1000000000",
		ArbitrationVotes: []*pb.WitnessVote{{RequestId: "case", WitnessAddress: "a", BallotRound: ballot, VoteType: pb.WitnessVoteType_VOTE_FAIL}}, FailCount: 1}
	for _, personal := range []bool{false, true} {
		srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/witness/requests", "/witness/my_requests":
				return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.RechargeRequestList{Requests: []*pb.RechargeRequest{request}})
			case "/witness/challenges":
				if req.URL.Query().Get("challenge_ids") != "challenge" {
					t.Fatal("request page lost bounded challenge batch")
				}
				return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.ChallengeRecordList{Records: []*pb.ChallengeRecord{challenge}})
			default:
				t.Fatalf("unexpected node path %s", req.URL.Path)
				return nil, nil
			}
		})
		recorder := httptest.NewRecorder()
		httpReq := httptest.NewRequest(http.MethodGet, "/api/witness/requests?node="+node+"&witness=a", nil)
		if personal {
			srv.handleWitnessMyRequests(recorder, httpReq)
		} else {
			srv.handleWitnessRequests(recorder, httpReq)
		}
		if recorder.Code != http.StatusOK {
			t.Fatalf("personal=%v status=%d: %s", personal, recorder.Code, recorder.Body.String())
		}
		var items []WitnessRequestItem
		if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil || len(items) != 1 {
			t.Fatalf("items=%+v err=%v", items, err)
		}
		got := items[0]
		if got.ArbitrationPeriodBlocks != 20 {
			t.Fatalf("case period missing from API: %+v", got)
		}
		if got.BallotRound != "9007199254740993" || got.ArbitrationRequiredVotes != 3 || got.ArbitrationRound != 3 || got.ArbitrationStage != "ARBITRATION_FINAL_COURT" || got.CourtSnapshotHeight != 1234 || len(got.FinalCourt) != 4 {
			t.Fatalf("court snapshot lost: %+v", got)
		}
		if len(got.ArbitrationVotes) != 1 || got.ArbitrationVotes[0].BallotRound != got.BallotRound || got.ArbitrationVotes[0].VoteType != 1 || got.OrdinaryThresholdBps != 7500 || len(got.OrdinaryCommitteeSizes) != 3 || got.ChallengeRequiredStake != "1000000000" {
			t.Fatalf("ballot/policy mapping lost: %+v", got)
		}
	}
}

func TestWitnessCasePeriodLegacyFallback(t *testing.T) {
	var got WitnessRequestItem
	applyWitnessChallengeFields(&got, &pb.ChallengeRecord{})
	if got.ArbitrationPeriodBlocks != 10000 {
		t.Fatalf("old case period=%d want=10000", got.ArbitrationPeriodBlocks)
	}
}

func TestWitnessReadModelDoesNotInferQuorumFromActualAttendance(t *testing.T) {
	rec := &pb.ChallengeRecord{ArbitrationRound: 2, OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500, Arbitrators: []string{"a", "b", "c", "d"}}
	var got WitnessRequestItem
	applyWitnessChallengeFields(&got, rec)
	if got.ArbitrationRequiredVotes != 6 {
		t.Fatalf("actual four-member attendance lowered planned quorum: %+v", got)
	}
	rec.OrdinaryCommitteeSizes = nil
	got = WitnessRequestItem{}
	applyWitnessChallengeFields(&got, rec)
	if got.ArbitrationRequiredVotes != 0 {
		t.Fatal("missing policy was filled with an invented threshold")
	}
}

func TestWitnessListExposesOldCaseDutyEvenWithoutReservedBond(t *testing.T) {
	const node = "node.example:443"
	srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/witness/list" {
			t.Fatalf("unexpected node path %s", req.URL.Path)
		}
		return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.WitnessInfoList{Witnesses: []*pb.WitnessInfo{{Address: "judge", Status: pb.WitnessStatus_WITNESS_UNSTAKING, StakeAmount: "3200000000", ReservedStakeAmount: "0", ActiveCaseCount: 1}}})
	})
	recorder := httptest.NewRecorder()
	srv.handleWitnessList(recorder, httptest.NewRequest(http.MethodGet, "/api/witness/list?node="+node, nil))
	var items []WitnessInfoItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil || len(items) != 1 || items[0].ActiveCaseCount != 1 || items[0].ReservedStakeAmount != "0" {
		t.Fatalf("court duty disappeared: items=%+v err=%v", items, err)
	}
}
