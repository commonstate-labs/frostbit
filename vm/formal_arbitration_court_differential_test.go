package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"dex/formal/itftrace"
	"dex/keys"
	"dex/pb"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

func courtModelInt(t *testing.T, s itftrace.State, field string) int64 {
	t.Helper()
	n, err := s.Int(field)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestFormalArbitrationCourtITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_ARBITRATION_COURT")
	if path == "" {
		t.Skip("set by formal/run-arbitration-implementation.ps1")
	}
	trace, err := itftrace.Load(path)
	if err != nil || len(trace.States) != 13 {
		t.Fatalf("court trace: %v %+v", err, trace)
	}
	sv := newMemStateView()
	svc := witness.NewService(witness.DefaultConfig())
	defer svc.Stop()
	x := &Executor{WitnessService: svc}
	members := []string{"w01", "w02", "w03", "w04"}
	req := &pb.RechargeRequest{RequestId: "court-refinement", RequesterAddress: "payer", RechargeFee: "0", Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION, PassCount: 1, ChallengeId: "court-case"}
	if err := collectWitnessServiceFee(sv, req, false); err != nil {
		t.Fatal(err)
	}
	var duties []WitnessCaseAssignment
	for _, a := range members {
		settlementPut(t, sv, keys.KeyWitnessInfo(a), &pb.WitnessInfo{Address: a, StakeAmount: "3200000000", Status: pb.WitnessStatus_WITNESS_ACTIVE})
		SetBalance(sv, a, FeeToken, &pb.TokenBalance{WitnessLockedBalance: "3200000000"})
		duties = append(duties, WitnessCaseAssignment{Address: a, Role: WitnessCaseRoleCourt, Amount: "0"})
	}
	if err := reserveWitnessCaseCollateral(sv, req, duties, "3200000000"); err != nil {
		t.Fatal(err)
	}
	if err := setRechargeRequestWithIndexes(sv, req, 3); err != nil {
		t.Fatal(err)
	}
	ch := &pb.ChallengeRecord{ChallengeId: req.ChallengeId, RequestId: req.RequestId, StakeAmount: "0", ChallengerAddress: "system", Status: pb.ChallengeStatus_CHALLENGE_ACTIVE, ArbitrationStage: pb.ArbitrationStage_ARBITRATION_FINAL_COURT, ArbitrationRound: 3, BallotRound: 4, FinalCourt: members, Arbitrators: members, OrdinaryVotesFrozen: true, CourtSnapshotHeight: 20002, DeadlineHeight: 40003, OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500}
	if err := setChallengeRecordWithTimeoutIndexes(sv, ch, 3, 0, 0); err != nil {
		t.Fatal(err)
	}
	assertCourtRefinement(t, sv, trace.States[0], ch.ChallengeId, 0)
	for step := 0; step < 12; step++ {
		before, after := trace.States[step], trace.States[step+1]
		ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
		height := uint64(courtModelInt(t, before, "height"))
		switch step {
		case 0, 1, 2, 3:
			dir := pb.WitnessVoteType_VOTE_PASS
			if step >= 2 {
				dir = pb.WitnessVoteType_VOTE_FAIL
			}
			lifecycleBallot(t, sv, x, ch, members[step], dir, height, true)
		case 4:
			if err := x.applyArbitrationTimeouts(sv, uint64(courtModelInt(t, after, "height"))); err != nil {
				t.Fatal(err)
			}
		case 5:
			if err := x.applyArbitrationTimeouts(sv, uint64(courtModelInt(t, after, "height"))); err != nil {
				t.Fatal(err)
			}
		case 6:
			old := proto.Clone(ch).(*pb.ChallengeRecord)
			old.BallotRound = 4
			lifecycleBallot(t, sv, x, old, members[0], pb.WitnessVoteType_VOTE_PASS, height, false)
		case 7:
			                                                                    
			settlementPut(t, sv, keys.KeyWitnessInfo("w05"), &pb.WitnessInfo{Address: "w05", StakeAmount: "100000000000", Status: pb.WitnessStatus_WITNESS_ACTIVE})
			SetBalance(sv, "w05", FeeToken, &pb.TokenBalance{WitnessLockedBalance: "100000000000"})
		case 8:
			var info pb.WitnessInfo
			raw, _, _ := sv.Get(keys.KeyWitnessInfo(members[0]))
			if err := proto.Unmarshal(raw, &info); err != nil {
				t.Fatal(err)
			}
			info.Status = pb.WitnessStatus_WITNESS_UNSTAKING
			settlementPut(t, sv, keys.KeyWitnessInfo(members[0]), &info)
		case 9, 10, 11:
			lifecycleBallot(t, sv, x, ch, members[step-9], pb.WitnessVoteType_VOTE_FAIL, height, true)
		}
		assertCourtRefinement(t, sv, after, ch.ChallengeId, step+1)
	}
}

func assertCourtRefinement(t *testing.T, sv StateView, model itftrace.State, id string, step int) {
	t.Helper()
	ch := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(id))
	if ch == nil {
		t.Fatal("missing case")
	}
	if ch.Finalized {
		raw, ok, err := sv.Get(keys.KeyArchiveChallengeRecord(id))
		if err != nil || !ok {
			t.Fatalf("archive %v", err)
		}
		if err := proto.Unmarshal(raw, ch); err != nil {
			t.Fatal(err)
		}
	}
	court, err := model.SetInts("courtMembers")
	if err != nil {
		t.Fatal(err)
	}
	gotCourt := map[int64]struct{}{}
	for _, a := range ch.FinalCourt {
		var n int64
		if _, err := fmt.Sscanf(a, "w%02d", &n); err != nil {
			t.Fatal(err)
		}
		gotCourt[n] = struct{}{}
	}
	if !reflect.DeepEqual(gotCourt, court) || int64(ch.BallotRound)-3 != courtModelInt(t, model, "courtRound") || int64(ch.DeadlineHeight) != courtModelInt(t, model, "deadline") {
		t.Fatalf("court/round/deadline relation failed at step %d: %+v", step, ch)
	}
	phase, err := model.Tag("phase")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Finalized != (phase == "Decided") {
		t.Fatalf("finality relation failed step %d", step)
	}
	if ch.Finalized {
		direction := int64(-1)
		if ch.FinalVote == pb.WitnessVoteType_VOTE_PASS {
			direction = 1
		}
		if direction != courtModelInt(t, model, "result") {
			t.Fatal("verdict mismatch")
		}
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(model.Values["ballots"], &values); err != nil {
		t.Fatal(err)
	}
	ballots := itftrace.State{Values: values}
	got := map[string]int64{}
	for _, v := range ch.ArbitrationVotes {
		d := int64(2)
		if v.VoteType == pb.WitnessVoteType_VOTE_PASS {
			d = 1
		} else if v.VoteType == pb.WitnessVoteType_VOTE_FAIL {
			d = -1
		}
		got[v.WitnessAddress] = d
	}
	for i, field := range []string{"a", "b", "c", "d", "e", "f"} {
		if got[fmt.Sprintf("w%02d", i+1)] != courtModelInt(t, ballots, field) {
			t.Fatalf("ballot mismatch step %d actor %s", step, field)
		}
	}
	if !ch.OrdinaryVotesFrozen || len(ch.OrdinaryVotes) != 0 || len(ch.OriginalVotes) != 0 {
		t.Fatal("frozen positions changed")
	}
}
