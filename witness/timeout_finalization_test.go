package witness_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"dex/pb"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

func timeoutRecord(pass, fail int, round uint32) *pb.ChallengeRecord {
	n := []int{4, 8, 16}[round-1]
	ch := &pb.ChallengeRecord{ChallengeId: "timeout-case", RequestId: "timeout-request", Status: pb.ChallengeStatus_CHALLENGE_ACTIVE,
		ArbitrationRound: round, BallotRound: 7, DeadlineHeight: 100, OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500}
	for i := 0; i < n; i++ {
		a := fmt.Sprintf("arb%d", i+1)
		ch.Arbitrators = append(ch.Arbitrators, a)
		if i < pass+fail {
			direction := pb.WitnessVoteType_VOTE_PASS
			if i >= pass {
				direction = pb.WitnessVoteType_VOTE_FAIL
			}
			ch.ArbitrationVotes = append(ch.ArbitrationVotes, &pb.WitnessVote{RequestId: ch.RequestId, WitnessAddress: a, VoteType: direction, BallotRound: 7})
		}
	}
	ch.PassCount, ch.FailCount = uint32(pass), uint32(fail)
	ch.OrdinaryVotes = witness.LastDirectionalPositions(ch.ArbitrationVotes, nil)
	return ch
}

func TestCanForceFinalizeUsesFrozenQuorumAndCurrentBallots(t *testing.T) {
	s := witness.NewService(witness.DefaultConfig())
	defer s.Stop()
	for _, tc := range []struct {
		name       string
		pass, fail int
		round      uint32
		want       bool
	}{
		{"no votes", 0, 0, 1, false}, {"one vote", 1, 0, 1, false}, {"two votes", 2, 0, 1, false},
		{"two two split", 2, 2, 1, false}, {"three pass", 3, 0, 1, true}, {"three fail", 0, 3, 1, true},
		{"larger round five insufficient", 5, 0, 2, false}, {"larger round six", 6, 1, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.CanForceFinalize(timeoutRecord(tc.pass, tc.fail, tc.round)); got != tc.want {
				t.Fatalf("finalizable=%v want=%v", got, tc.want)
			}
		})
	}
	cachedOnly := timeoutRecord(0, 0, 1)
	cachedOnly.PassCount = 99
	if s.CanForceFinalize(cachedOnly) {
		t.Fatal("cached count without signed current ballots authorized finality")
	}
	stale := timeoutRecord(3, 0, 1)
	stale.ArbitrationVotes[0].BallotRound = 6
	if s.CanForceFinalize(stale) {
		t.Fatal("old round contributed to current quorum")
	}
	smaller := timeoutRecord(4, 0, 2)
	smaller.Arbitrators = smaller.Arbitrators[:4]
	if s.CanForceFinalize(smaller) {
		t.Fatal("actual attendance lowered planned eight-member quorum")
	}
}

func makeTimeoutFixture(success bool) (*pb.ChallengeRecord, *pb.RechargeRequest, map[string]*pb.WitnessInfo) {
	ch := timeoutRecord(3, 0, 1)
	if success {
		ch = timeoutRecord(0, 3, 1)
	}
	ch.ChallengerAddress = "challenger"
	ch.StakeAmount = "1000000000"
	req := &pb.RechargeRequest{RequestId: ch.RequestId, Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED, RechargeFee: "10000", PassCount: 2,
		Votes: []*pb.WitnessVote{{RequestId: ch.RequestId, WitnessAddress: "w1", VoteType: pb.WitnessVoteType_VOTE_PASS}, {RequestId: ch.RequestId, WitnessAddress: "w2", VoteType: pb.WitnessVoteType_VOTE_PASS}}}
	req.OriginalPositions = witness.OriginalPositions(req)
	ch.OriginalVotes = witness.OriginalPositions(req)
	infos := map[string]*pb.WitnessInfo{}
	for _, a := range append([]string{"w1", "w2"}, ch.Arbitrators...) {
		infos[a] = &pb.WitnessInfo{Address: a, StakeAmount: "3200000000", Status: pb.WitnessStatus_WITNESS_ACTIVE, PendingReward: "0"}
	}
	return ch, req, infos
}

func assertNoTimeoutMoney(t *testing.T, o *witness.ArbitrationVoteOutcome) {
	t.Helper()
	if len(o.SlashedWitnesses) != 0 || len(o.RewardedWitnesses) != 0 || len(o.SlashAmounts) != 0 || len(o.ChallengerRefunds) != 0 || o.ChallengerRefund != "" || o.ChallengerAddr != "" {
		t.Fatalf("pure verdict precomputed money or punished silence: %+v", o)
	}
}

func TestComputeTimeoutFinalizationRequiresSameRoundDirectionalQuorum(t *testing.T) {
	s := witness.NewService(witness.DefaultConfig())
	defer s.Stop()
	ch, req, infos := makeTimeoutFixture(false)
	ch.ArbitrationVotes = ch.ArbitrationVotes[:2]
	ch.PassCount = 2
	if o := s.ComputeTimeoutFinalization(ch, req, infos, 200); o != nil {
		t.Fatalf("two votes force-finalized: %+v", o)
	}
	if o := s.ComputeConservativeFinalization(ch, req, infos, 200); o != nil {
		t.Fatalf("silence created a conservative final verdict: %+v", o)
	}
}

func TestComputeTimeoutFinalizationRejectsWrongBoundaryAndCorruptSnapshot(t *testing.T) {
	s := witness.NewService(witness.DefaultConfig())
	defer s.Stop()
	ch, req, infos := makeTimeoutFixture(false)
	if s.ComputeTimeoutFinalization(ch, req, infos, ch.DeadlineHeight) != nil {
		t.Fatal("timeout ran before inclusive voting deadline ended")
	}
	if s.ComputeTimeoutFinalization(ch, req, infos, ch.DeadlineHeight+1) == nil {
		t.Fatal("valid quorum did not resolve after deadline")
	}
	for _, corrupt := range []func(*pb.ChallengeRecord){
		func(c *pb.ChallengeRecord) { c.ArbitrationVotes[0].RequestId = "different-case" },
		func(c *pb.ChallengeRecord) { c.ArbitrationVotes[0].WitnessAddress = "outsider" },
		func(c *pb.ChallengeRecord) { c.ArbitrationVotes[0].BallotRound-- },
		func(c *pb.ChallengeRecord) {
			c.ArbitrationVotes[1] = proto.Clone(c.ArbitrationVotes[0]).(*pb.WitnessVote)
		},
		func(c *pb.ChallengeRecord) { c.ArbitrationVotes[0].VoteType = pb.WitnessVoteType(88) },
	} {
		broken := proto.Clone(ch).(*pb.ChallengeRecord)
		corrupt(broken)
		if s.ComputeTimeoutFinalization(broken, req, infos, 200) != nil {
			t.Fatal("timeout accepted malformed persisted vote")
		}
		if s.ComputeConservativeFinalization(broken, req, infos, 2000000) != nil {
			t.Fatal("long wait bypassed ballot validation")
		}
	}
}

func TestComputeTimeoutFinalizationProducesPureVerdictWithoutEconomics(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.SilenceSlashRatio = 1
	s := witness.NewService(cfg)
	defer s.Stop()
	for _, success := range []bool{false, true} {
		ch, req, infos := makeTimeoutFixture(success)
		beforeCh, beforeReq := proto.Clone(ch), proto.Clone(req)
		beforeInfos := map[string]*pb.WitnessInfo{}
		for a, v := range infos {
			beforeInfos[a] = proto.Clone(v).(*pb.WitnessInfo)
		}
		o := s.ComputeTimeoutFinalization(ch, req, infos, 200)
		if o == nil || o.Challenge == nil || !o.Challenge.Finalized || o.Challenge.ChallengeSuccess != success || !o.Challenge.OrdinaryVotesFrozen {
			t.Fatalf("wrong final verdict: %+v", o)
		}
		want := pb.RechargeRequestStatus_RECHARGE_FINALIZED
		if success {
			want = pb.RechargeRequestStatus_RECHARGE_REJECTED
		}
		if o.Request == nil || o.Request.Status != want || o.Request.FinalizeHeight != 200 || o.Challenge.FinalizeHeight != 200 {
			t.Fatalf("wrong request/deadline: %+v", o)
		}
		assertNoTimeoutMoney(t, o)
		if !proto.Equal(ch, beforeCh) || !proto.Equal(req, beforeReq) {
			t.Fatal("pure timeout mutated input record")
		}
		for a, v := range infos {
			if !proto.Equal(v, beforeInfos[a]) {
				t.Fatalf("timeout changed witness %s", a)
			}
		}
	}
}

func TestComputeTimeoutFinalizationIdempotentAndDeterministic(t *testing.T) {
	s := witness.NewService(witness.DefaultConfig())
	defer s.Stop()
	ch, req, infos := makeTimeoutFixture(false)
	first, second := s.ComputeTimeoutFinalization(ch, req, infos, 200), s.ComputeTimeoutFinalization(ch, req, infos, 200)
	if first == nil || second == nil || !reflect.DeepEqual(first, second) {
		t.Fatal("identical timeout inputs produced different outcomes")
	}
	if o := s.ComputeTimeoutFinalization(first.Challenge, first.Request, infos, 201); o != nil {
		t.Fatalf("terminal timeout repeated: %+v", o)
	}
	ch.ChallengerAddress = "system"
	system := s.ComputeTimeoutFinalization(ch, req, infos, 200)
	if system == nil {
		t.Fatal("system case lost valid final verdict")
	}
	assertNoTimeoutMoney(t, system)
}

                                                                     

func makeCandidates(addrs ...string) []*pb.WitnessInfo {
	out := make([]*pb.WitnessInfo, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, &pb.WitnessInfo{Address: a, StakeAmount: witness.DefaultConfig().MinStakeAmount, Status: pb.WitnessStatus_WITNESS_ACTIVE})
	}
	return out
}

func TestApplyRechargeSelection_MinSelectedWitnessGate(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.InitialWitnessCount = 4
	cfg.MinSelectedWitnessCount = 4
	s := witness.NewService(cfg)
	defer s.Stop()

	for _, info := range makeCandidates("w1", "w2", "w3") {
		s.LoadWitness(info)
	}

	req := &pb.RechargeRequest{
		RequestId:       "req_min_selected_001",
		Status:          pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		NativeChain:     "ETH",
		NativeTxHash:    "0xmin",
		SelectionHeight: 10,
	}
	updated, err := s.ApplyRechargeSelection(req, []byte("beacon"), 15)
	if !errors.Is(err, witness.ErrInsufficientSelectedWitnesses) {
		t.Fatalf("expected insufficient selected witnesses error, got updated=%v err=%v", updated, err)
	}
	if updated != nil {
		t.Fatalf("request must stay pending selection when selected witnesses are insufficient: %+v", updated)
	}
}

func TestApplyRechargeSelection_DeterministicSameBeacon(t *testing.T) {
	s := witness.NewService(witness.DefaultConfig())
	defer s.Stop()
	for _, info := range makeCandidates("w1", "w2", "w3", "w4", "w5", "w6", "w7") {
		s.LoadWitness(info)
	}

	req := &pb.RechargeRequest{
		RequestId:       "req_beacon_001",
		Status:          pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		NativeChain:     "ETH",
		NativeTxHash:    "0xdead",
		SelectionHeight: 10,
	}
	beacon := []byte{0x01, 0x02, 0x03, 0x04}

	a, err := s.ApplyRechargeSelection(req, beacon, 15)
	if err != nil {
		t.Fatalf("first ApplyRechargeSelection: %v", err)
	}
	b, err := s.ApplyRechargeSelection(req, beacon, 15)
	if err != nil {
		t.Fatalf("second ApplyRechargeSelection: %v", err)
	}
	if len(a.SelectedWitnesses) == 0 || len(a.SelectedWitnesses) != len(b.SelectedWitnesses) {
		t.Fatalf("selections differ in length: a=%d b=%d", len(a.SelectedWitnesses), len(b.SelectedWitnesses))
	}
	for i := range a.SelectedWitnesses {
		if a.SelectedWitnesses[i] != b.SelectedWitnesses[i] {
			t.Errorf("selection[%d] a=%s b=%s", i, a.SelectedWitnesses[i], b.SelectedWitnesses[i])
		}
	}
	if a.Status != pb.RechargeRequestStatus_RECHARGE_VOTING {
		t.Errorf("expected VOTING, got %v", a.Status)
	}
}

func TestApplyRechargeSelection_DifferentBeaconYieldsDifferentSelection(t *testing.T) {
	s := witness.NewService(witness.DefaultConfig())
	defer s.Stop()
	                                                                    
	for _, info := range makeCandidates("w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8", "w9", "w10") {
		s.LoadWitness(info)
	}
	req := &pb.RechargeRequest{
		RequestId:       "req_beacon_002",
		Status:          pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION,
		NativeChain:     "ETH",
		NativeTxHash:    "0xbeef",
		SelectionHeight: 10,
	}
	a, err := s.ApplyRechargeSelection(req, []byte("beacon-A"), 15)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ApplyRechargeSelection(req, []byte("beacon-B"), 15)
	if err != nil {
		t.Fatal(err)
	}
	                                                                                 
	sameSet := true
	aset := map[string]bool{}
	for _, addr := range a.SelectedWitnesses {
		aset[addr] = true
	}
	for _, addr := range b.SelectedWitnesses {
		if !aset[addr] {
			sameSet = false
			break
		}
	}
	if sameSet && len(a.SelectedWitnesses) == len(b.SelectedWitnesses) {
		t.Errorf("different beacons produced identical selection: %v vs %v", a.SelectedWitnesses, b.SelectedWitnesses)
	}
}

                                                                           
                                                                                
func TestComputeArbitrationVoteOutcomeDoesNotSlashSilentMember(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.SilenceSlashRatio = 1
	s := witness.NewService(cfg)
	defer s.Stop()
	ch, req, infos := makeTimeoutFixture(false)
	deciding := proto.Clone(ch.ArbitrationVotes[2]).(*pb.WitnessVote)
	ch.ArbitrationVotes = ch.ArbitrationVotes[:2]
	ch.PassCount = 2
	o, err := s.ComputeArbitrationVoteOutcome(ch, req, deciding, infos, 100)
	if err != nil || o == nil || !o.Challenge.Finalized {
		t.Fatalf("deciding vote failed: outcome=%+v err=%v", o, err)
	}
	assertNoTimeoutMoney(t, o)
	if infos["arb4"].StakeAmount != "3200000000" || infos["arb4"].SlashedCount != 0 {
		t.Fatal("silent member was punished")
	}
}
