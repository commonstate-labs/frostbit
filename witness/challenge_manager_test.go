                                                                                                         
package witness_test

import (
	"dex/pb"
	"dex/witness"
	"errors"
	"fmt"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func newCMWithChallenge(t *testing.T, arbitrators []string) (*witness.ChallengeManager, string) {
	t.Helper()
	cfg := witness.DefaultConfig()
	cfg.OrdinaryCommitteeSizes, cfg.OrdinaryThresholdBps = []uint32{4, 8, 16}, 7500
	cm := witness.NewChallengeManager(cfg)
	const id = "challenge_unit_001"
	if _, err := cm.CreateChallenge(id, "req_unit_001", "challenger", "1000000000", "unit", 100, arbitrators); err != nil {
		t.Fatal(err)
	}
	return cm, id
}
func cmVote(t *testing.T, cm *witness.ChallengeManager, id, address string, direction pb.WitnessVoteType) *pb.WitnessVote {
	t.Helper()
	ch, err := cm.GetChallenge(id)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.WitnessVote{RequestId: ch.RequestId, WitnessAddress: address, VoteType: direction, BallotRound: ch.BallotRound}
}
func addVote(t *testing.T, cm *witness.ChallengeManager, id, address string, direction pb.WitnessVoteType) {
	t.Helper()
	ch, err := cm.GetChallenge(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.AddArbitrationVoteAtHeight(id, cmVote(t, cm, id, address, direction), ch.DeadlineHeight); err != nil {
		t.Fatal(err)
	}
}
func committee(prefix string, size int) []string {
	result := make([]string, size)
	for i := range result {
		result[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return result
}

func TestChallengeManagerConsensusUsesFixedDirectionalQuorum(t *testing.T) {
	for _, direction := range []pb.WitnessVoteType{pb.WitnessVoteType_VOTE_PASS, pb.WitnessVoteType_VOTE_FAIL} {
		t.Run(direction.String(), func(t *testing.T) {
			members := []string{"A", "B", "C", "D"}
			cm, id := newCMWithChallenge(t, members)
			addVote(t, cm, id, "A", direction)
			addVote(t, cm, id, "B", direction)
			for _, expired := range []bool{false, true} {
				if cm.CheckArbitrationConsensus(id, expired) != witness.ConsensusNone {
					t.Fatal("two votes formed a verdict")
				}
			}
			if _, err := cm.FinalizeChallenge(id, direction == pb.WitnessVoteType_VOTE_PASS, 200); err == nil {
				t.Fatal("caller selected verdict without quorum")
			}
			addVote(t, cm, id, "C", direction)
			want := witness.ConsensusPass
			if direction == pb.WitnessVoteType_VOTE_FAIL {
				want = witness.ConsensusFail
			}
			if got := cm.CheckArbitrationConsensus(id, true); got != want {
				t.Fatalf("got %v want %v", got, want)
			}
		})
	}
}

func TestChallengeManagerDeadlineDoesNotDecideSplitOrAbstention(t *testing.T) {
	cases := [][]pb.WitnessVoteType{
		{pb.WitnessVoteType_VOTE_PASS, pb.WitnessVoteType_VOTE_PASS, pb.WitnessVoteType_VOTE_FAIL, pb.WitnessVoteType_VOTE_FAIL},
		{pb.WitnessVoteType_VOTE_ABSTAIN, pb.WitnessVoteType_VOTE_ABSTAIN, pb.WitnessVoteType_VOTE_ABSTAIN, pb.WitnessVoteType_VOTE_ABSTAIN},
	}
	for _, votes := range cases {
		members := []string{"A", "B", "C", "D"}
		cm, id := newCMWithChallenge(t, members)
		for i, vote := range votes {
			addVote(t, cm, id, members[i], vote)
		}
		if got := cm.CheckArbitrationConsensus(id, true); got != witness.ConsensusNone {
			t.Fatalf("deadline invented result: %v", got)
		}
		for _, success := range []bool{false, true} {
			if _, err := cm.FinalizeChallenge(id, success, 20000); err == nil {
				t.Fatal("split/abstention finalized")
			}
		}
	}
}

func TestChallengeManagerRejectsInvalidVoteWithoutConsumingSlot(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*pb.WitnessVote)
		want   error
	}{
		{"outsider", func(v *pb.WitnessVote) { v.WitnessAddress = "X" }, witness.ErrNotArbitrator},
		{"wrong_request", func(v *pb.WitnessVote) { v.RequestId = "another" }, nil},
		{"zero_round", func(v *pb.WitnessVote) { v.BallotRound = 0 }, nil},
		{"future_round", func(v *pb.WitnessVote) { v.BallotRound++ }, nil},
		{"unknown_direction", func(v *pb.WitnessVote) { v.VoteType = pb.WitnessVoteType(123) }, witness.ErrInvalidVote},
	} {
		t.Run(test.name, func(t *testing.T) {
			cm, id := newCMWithChallenge(t, []string{"A", "B", "C", "D"})
			before, _ := cm.GetChallenge(id)
			vote := cmVote(t, cm, id, "A", pb.WitnessVoteType_VOTE_PASS)
			test.mutate(vote)
			err := cm.AddArbitrationVoteAtHeight(id, vote, 200)
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("invalid vote error: %v", err)
			}
			after, _ := cm.GetChallenge(id)
			if !proto.Equal(before, after) {
				t.Fatal("rejected vote mutated record")
			}
			addVote(t, cm, id, "A", pb.WitnessVoteType_VOTE_PASS)
		})
	}
}

func TestChallengeManagerRequiresExecutionHeightAndRejectsExpiredVotes(t *testing.T) {
	cm, id := newCMWithChallenge(t, []string{"A", "B", "C", "D"})
	record, _ := cm.GetChallenge(id)
	vote := cmVote(t, cm, id, "A", pb.WitnessVoteType_VOTE_PASS)
	if err := cm.AddArbitrationVote(id, vote); !errors.Is(err, witness.ErrArbitrationHeightRequired) {
		t.Fatalf("heightless vote accepted: %v", err)
	}
	if err := cm.AddArbitrationVoteAtHeight(id, vote, record.DeadlineHeight+1); !errors.Is(err, witness.ErrArbitrationExpired) {
		t.Fatal(err)
	}
	if err := cm.AddArbitrationVoteAtHeight(id, vote, record.DeadlineHeight); err != nil {
		t.Fatal(err)
	}
	if err := cm.AddArbitrationVoteAtHeight(id, vote, record.DeadlineHeight); !errors.Is(err, witness.ErrDuplicateVote) {
		t.Fatalf("duplicate: %v", err)
	}
	got, _ := cm.GetChallenge(id)
	if got.PassCount != 1 || len(got.ArbitrationVotes) != 1 {
		t.Fatal("rejected votes changed counts")
	}
	if err := cm.AddArbitrationVoteAtHeight("missing", vote, 100); !errors.Is(err, witness.ErrChallengeNotFound) {
		t.Fatalf("missing challenge: %v", err)
	}
}

func TestChallengeManagerExpansionKeepsHistoryAndNeverLowersQuorum(t *testing.T) {
	cm, id := newCMWithChallenge(t, committee("A", 4))
	addVote(t, cm, id, "A0", pb.WitnessVoteType_VOTE_FAIL)
	first, _ := cm.GetChallenge(id)
	oldVote := cmVote(t, cm, id, "A1", pb.WitnessVoteType_VOTE_PASS)
	if err := cm.ExpandArbitrationScope(id, committee("A", 8), first.DeadlineHeight); err == nil {
		t.Fatal("expanded before inclusive deadline ended")
	}
	if err := cm.ExpandArbitrationScope(id, committee("A", 8), first.DeadlineHeight+1); err != nil {
		t.Fatal(err)
	}
	second, _ := cm.GetChallenge(id)
	if second.ArbitrationRound != 2 || second.BallotRound != first.BallotRound+1 || second.PassCount != 0 || second.FailCount != 0 || len(second.ArbitrationVotes) != 0 {
		t.Fatal("round did not reset")
	}
	if len(second.OrdinaryVotes) != 1 || second.OrdinaryVotes[0].VoteType != pb.WitnessVoteType_VOTE_FAIL {
		t.Fatal("ordinary liability history erased")
	}
	if err := cm.AddArbitrationVoteAtHeight(id, oldVote, second.DeadlineHeight); err == nil {
		t.Fatal("old round vote accepted")
	}
	for _, member := range second.Arbitrators[:5] {
		addVote(t, cm, id, member, pb.WitnessVoteType_VOTE_PASS)
	}
	if cm.CheckArbitrationConsensus(id, true) != witness.ConsensusNone {
		t.Fatal("five votes below frozen six-vote threshold finalized")
	}
	addVote(t, cm, id, second.Arbitrators[5], pb.WitnessVoteType_VOTE_PASS)
	if cm.CheckArbitrationConsensus(id, true) != witness.ConsensusPass {
		t.Fatal("six same-round votes did not decide")
	}
	if err := cm.ExpandArbitrationScope(id, committee("A", 16), second.DeadlineHeight+1); err == nil {
		t.Fatal("decided round expanded")
	}
	got, _ := cm.GetChallenge(id)
	if got.OrdinaryVotes[0].VoteType != pb.WitnessVoteType_VOTE_PASS {
		t.Fatal("ordinary correction was not recorded")
	}
}

func TestChallengeManagerThirdRoundRequiresFrozenCourtAndCannotBecomeFourthOrdinary(t *testing.T) {
	cm, id := newCMWithChallenge(t, committee("A", 4))
	first, _ := cm.GetChallenge(id)
	if err := cm.ExpandArbitrationScope(id, committee("B", 8), first.DeadlineHeight+1); err != nil {
		t.Fatal(err)
	}
	second, _ := cm.GetChallenge(id)
	if err := cm.ExpandArbitrationScope(id, committee("C", 16), second.DeadlineHeight+1); err == nil {
		t.Fatal("third round admitted without court snapshot")
	}
	second.FinalCourt = committee("court", 4)
	second.CourtSnapshotHeight = second.DeadlineHeight + 1
	cm.LoadChallenge(second)
	if err := cm.ExpandArbitrationScope(id, committee("C", 16), second.DeadlineHeight+1); err != nil {
		t.Fatal(err)
	}
	third, _ := cm.GetChallenge(id)
	quorum, err := witness.ArbitrationQuorum(third)
	if err != nil || quorum != 12 {
		t.Fatalf("third threshold %d %v", quorum, err)
	}
	if err := cm.ExpandArbitrationScope(id, committee("D", 20), third.DeadlineHeight+1); err == nil {
		t.Fatal("fourth ordinary round admitted")
	}
	if third.FinalCourt[0] != "court0" || third.CourtSnapshotHeight != second.CourtSnapshotHeight {
		t.Fatal("frozen court changed")
	}
}

func TestChallengeManagerFinalizeChecksDirectionAndIsIdempotent(t *testing.T) {
	cm, id := newCMWithChallenge(t, []string{"A", "B", "C", "D"})
	ch, _ := cm.GetChallenge(id)
	ch.OriginalVotes = []*pb.WitnessVote{{RequestId: ch.RequestId, WitnessAddress: "original", VoteType: pb.WitnessVoteType_VOTE_FAIL}}
	cm.LoadChallenge(ch)
	for _, address := range []string{"A", "B", "C"} {
		addVote(t, cm, id, address, pb.WitnessVoteType_VOTE_PASS)
	}
	if _, err := cm.FinalizeChallenge(id, false, 200); err == nil {
		t.Fatal("caller inverted known success")
	}
	final, err := cm.FinalizeChallenge(id, true, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Finalized || final.FinalVote != pb.WitnessVoteType_VOTE_PASS || !final.ChallengeSuccess || final.FinalizeHeight != 200 || !final.OrdinaryVotesFrozen {
		t.Fatal("incorrect pure verdict")
	}
	again, err := cm.FinalizeChallenge(id, true, 300)
	if err != nil || !proto.Equal(final, again) {
		t.Fatal("second finalization changed result")
	}
	if err := cm.AddArbitrationVoteAtHeight(id, cmVote(t, cm, id, "D", pb.WitnessVoteType_VOTE_PASS), 201); err == nil {
		t.Fatal("finalized challenge accepted vote")
	}
	if len(cm.GetActiveChallenges()) != 0 {
		t.Fatal("terminal retained in active index")
	}
	if _, err := cm.FinalizeChallenge(id, false, 300); err == nil {
		t.Fatal("terminal verdict overwritten")
	}
}

func TestChallengeManagerDoesNotTrustCountersOrMalformedPersistedVotes(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt func(*pb.ChallengeRecord)
	}{
		{"counters_only", func(ch *pb.ChallengeRecord) { ch.ArbitrationVotes = nil; ch.PassCount = 999 }},
		{"old_round", func(ch *pb.ChallengeRecord) { ch.ArbitrationVotes[0].BallotRound++ }},
		{"outsider", func(ch *pb.ChallengeRecord) { ch.ArbitrationVotes[0].WitnessAddress = "outsider" }},
		{"wrong_request", func(ch *pb.ChallengeRecord) { ch.ArbitrationVotes[0].RequestId = "wrong" }},
		{"duplicate", func(ch *pb.ChallengeRecord) {
			ch.ArbitrationVotes[1] = proto.Clone(ch.ArbitrationVotes[0]).(*pb.WitnessVote)
		}},
		{"member_duplicate", func(ch *pb.ChallengeRecord) { ch.Arbitrators[1] = ch.Arbitrators[0] }},
		{"unknown_direction", func(ch *pb.ChallengeRecord) { ch.ArbitrationVotes[0].VoteType = pb.WitnessVoteType(99) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			cm, id := newCMWithChallenge(t, committee("A", 4))
			for _, address := range []string{"A0", "A1", "A2"} {
				addVote(t, cm, id, address, pb.WitnessVoteType_VOTE_PASS)
			}
			ch, _ := cm.GetChallenge(id)
			test.corrupt(ch)
			cm.LoadChallenge(ch)
			if cm.CheckArbitrationConsensus(id, true) != witness.ConsensusNone {
				t.Fatal("corrupt snapshot finalized")
			}
			if _, err := cm.FinalizeChallenge(id, true, 20000); err == nil {
				t.Fatal("legacy finalizer bypassed snapshot validation")
			}
		})
	}
}

func TestChallengeManagerCourtRequiresThreeFrozenMembersDespiteTimeout(t *testing.T) {
	cm, id := newCMWithChallenge(t, committee("A", 4))
	ch, _ := cm.GetChallenge(id)
	ch.ArbitrationStage = pb.ArbitrationStage_ARBITRATION_FINAL_COURT
	ch.FinalCourt = committee("court", 4)
	ch.Arbitrators = append([]string(nil), ch.FinalCourt...)
	ch.OrdinaryVotesFrozen = true
	cm.LoadChallenge(ch)
	addVote(t, cm, id, "court0", pb.WitnessVoteType_VOTE_PASS)
	addVote(t, cm, id, "court1", pb.WitnessVoteType_VOTE_PASS)
	if cm.CheckArbitrationConsensus(id, true) != witness.ConsensusNone {
		t.Fatal("two court votes formed verdict")
	}
	if err := cm.ExpandArbitrationScope(id, committee("replacement", 4), ch.DeadlineHeight+1); err == nil {
		t.Fatal("legacy expansion replaced frozen court")
	}
	addVote(t, cm, id, "court2", pb.WitnessVoteType_VOTE_PASS)
	if cm.CheckArbitrationConsensus(id, true) != witness.ConsensusPass {
		t.Fatal("three court votes failed")
	}
	corrupt, _ := cm.GetChallenge(id)
	corrupt.FinalCourt[3] = "replacement"
	cm.LoadChallenge(corrupt)
	if cm.CheckArbitrationConsensus(id, true) != witness.ConsensusNone {
		t.Fatal("mismatched frozen court authorized verdict")
	}
}

func TestChallengeManagerSnapshotsDoNotAliasAndReloadClearsOldBallots(t *testing.T) {
	cm, id := newCMWithChallenge(t, committee("A", 4))
	addVote(t, cm, id, "A0", pb.WitnessVoteType_VOTE_FAIL)
	ch, _ := cm.GetChallenge(id)
	before := proto.Clone(ch).(*pb.ChallengeRecord)
	ch.Arbitrators[0] = "tampered"
	ch.ArbitrationVotes[0].VoteType = pb.WitnessVoteType_VOTE_PASS
	again, _ := cm.GetChallenge(id)
	if !proto.Equal(before, again) {
		t.Fatal("GetChallenge leaked cache pointer")
	}
	active := cm.GetActiveChallenges()
	active[0].Arbitrators[0] = "tampered"
	byRequest, _ := cm.GetChallengeByRequest(before.RequestId)
	byRequest.Arbitrators[0] = "tampered"
	reloaded := proto.Clone(before).(*pb.ChallengeRecord)
	reloaded.BallotRound++
	reloaded.ArbitrationVotes = nil
	reloaded.PassCount = 0
	reloaded.FailCount = 0
	cm.LoadChallenge(reloaded)
	reloaded.Arbitrators[0] = "tampered"
	addVote(t, cm, id, "A0", pb.WitnessVoteType_VOTE_PASS)
	got, _ := cm.GetChallenge(id)
	if got.PassCount != 1 || got.FailCount != 0 || len(got.ArbitrationVotes) != 1 {
		t.Fatal("old ballots survived reload")
	}
}

func TestServiceRuntimeVoteDoesNotSettleFundsOrReleaseOldDuties(t *testing.T) {
	svc := witness.NewService(witness.DefaultConfig())
	defer svc.Stop()
	req := &pb.RechargeRequest{RequestId: "runtime-case", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED, PassCount: 3, RechargeFee: "99999999999999"}
	ch := &pb.ChallengeRecord{ChallengeId: "runtime-challenge", RequestId: req.RequestId, Status: pb.ChallengeStatus_CHALLENGE_ACTIVE, Arbitrators: committee("A", 4), ArbitrationRound: 1, BallotRound: 1, DeadlineHeight: 10000, OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500}
	svc.LoadRequest(req)
	svc.LoadChallenge(ch)
	for _, address := range append(committee("A", 4), "original") {
		svc.LoadWitness(&pb.WitnessInfo{Address: address, StakeAmount: "3200000000", ReservedStakeAmount: "1000000000", ActiveCaseCount: 1, PendingTasks: []string{ch.ChallengeId}, PendingReward: "0", Status: pb.WitnessStatus_WITNESS_ACTIVE})
	}
	svc.SetCurrentHeight(200)
	var outcome *witness.ArbitrationVoteOutcome
	for i, address := range ch.Arbitrators[:3] {
		var err error
		outcome, err = svc.ProcessArbitrationVote(ch.ChallengeId, &pb.WitnessVote{RequestId: req.RequestId, WitnessAddress: address, VoteType: pb.WitnessVoteType_VOTE_FAIL, BallotRound: 1})
		if err != nil {
			t.Fatal(err)
		}
		if i < 2 && outcome.Request != nil {
			t.Fatal("early verdict")
		}
	}
	if outcome.Request == nil || outcome.Request.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED || !outcome.Challenge.ChallengeSuccess {
		t.Fatal("wrong pure runtime verdict")
	}
	assertNoTimeoutMoney(t, outcome)
	current, err := svc.GetRequest(req.RequestId)
	if err != nil || current.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED {
		t.Fatal("runtime vote finalized request before VM commit")
	}
	for _, address := range append(committee("A", 4), "original") {
		info, err := svc.GetWitnessInfo(address)
		if err != nil || info.StakeAmount != "3200000000" || info.PendingReward != "0" || info.SlashedCount != 0 || info.ActiveCaseCount != 1 || len(info.PendingTasks) != 1 {
			t.Fatalf("memory economics/duty changed for %s: %+v", address, info)
		}
	}
}

func TestServiceChallengeResolvedEventCannotMintOrFinalizeUncommittedRequest(t *testing.T) {
	svc := witness.NewService(witness.DefaultConfig())
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()
	req := &pb.RechargeRequest{RequestId: "uncommitted-case", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED, PassCount: 1, RechargeFee: "999999999999999", Votes: []*pb.WitnessVote{{WitnessAddress: "original", VoteType: pb.WitnessVoteType_VOTE_PASS}}}
	svc.LoadRequest(req)
	svc.LoadWitness(&pb.WitnessInfo{Address: "original", StakeAmount: "3200000000", PendingReward: "0", Status: pb.WitnessStatus_WITNESS_ACTIVE})
	ch, err := svc.CreateChallenge("uncommitted-challenge", req.RequestId, "challenger", "1000000000", "test", 100, committee("A", 4))
	if err != nil {
		t.Fatal(err)
	}
	ch.OriginalVotes = req.Votes
	for _, address := range ch.Arbitrators[:3] {
		ch.ArbitrationVotes = append(ch.ArbitrationVotes, &pb.WitnessVote{RequestId: req.RequestId, WitnessAddress: address, VoteType: pb.WitnessVoteType_VOTE_FAIL, BallotRound: ch.BallotRound})
	}
	ch.FailCount = 3
	svc.LoadChallenge(ch)
	if _, err := svc.FinalizeChallenge(ch.ChallengeId, true, 200); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-svc.Events():
			if event.Type != witness.EventChallengeResolved {
				continue
			}
			current, err := svc.GetRequest(req.RequestId)
			if err != nil || current.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED {
				t.Fatal("uncommitted event changed recharge")
			}
			info, _ := svc.GetWitnessInfo("original")
			if info.StakeAmount != "3200000000" || info.PendingReward != "0" || info.SlashedCount != 0 {
				t.Fatal("event changed money")
			}
			return
		case <-timer.C:
			t.Fatal("challenge event not observed")
		}
	}
}

func TestServiceLegacySelectionRequiresStateBackedReservation(t *testing.T) {
	svc := witness.NewService(witness.DefaultConfig())
	defer svc.Stop()
	for _, address := range committee("A", 20) {
		svc.LoadWitness(&pb.WitnessInfo{Address: address, StakeAmount: "9999999999999", Status: pb.WitnessStatus_WITNESS_ACTIVE})
	}
	if _, _, _, err := svc.PrepareArbitration("case", 100, nil, []byte("beacon")); !errors.Is(err, witness.ErrStateBackedArbitrationSelectionRequired) {
		t.Fatalf("cache selection bypass: %v", err)
	}
	ch := &pb.ChallengeRecord{ChallengeId: "case", RequestId: "req", ArbitrationRound: 1, OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500}
	before := proto.Clone(ch)
	if _, err := svc.ApplyArbitrationSelection(ch, nil, []byte("beacon"), 100); !errors.Is(err, witness.ErrStateBackedArbitrationSelectionRequired) {
		t.Fatalf("cache selection bypass: %v", err)
	}
	if !proto.Equal(before, ch) {
		t.Fatal("rejected selection mutated input")
	}
}
