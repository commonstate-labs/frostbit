package witness_test

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"

	"dex/pb"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                               
                                                                         
func lifecyclePolicy() *witness.Config {
	return &witness.Config{OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500}
}

func lifecycleVote(address string, direction pb.WitnessVoteType, round uint64) *pb.WitnessVote {
	return &pb.WitnessVote{RequestId: "lifecycle-request", WitnessAddress: address, VoteType: direction, BallotRound: round}
}

func lifecycleFixture(t *testing.T, court bool) (*pb.ChallengeRecord, *pb.RechargeRequest) {
	t.Helper()
	req := &pb.RechargeRequest{RequestId: "lifecycle-request", Status: pb.RechargeRequestStatus_RECHARGE_ARBITRATION, PassCount: 4}
	ch := &pb.ChallengeRecord{ChallengeId: "lifecycle-challenge", RequestId: req.RequestId}
	if err := witness.PrepareArbitrationRecord(ch, req, lifecyclePolicy()); err != nil {
		t.Fatal(err)
	}
	if court {
		ch.ArbitrationStage = pb.ArbitrationStage_ARBITRATION_FINAL_COURT
		ch.ArbitrationRound = 3
		ch.FinalCourt = []string{"a", "b", "c", "d"}
		ch.CourtSnapshotHeight = 50
	}
	started, err := witness.StartArbitrationRound(ch, []string{"a", "b", "c", "d"}, nil, 100, 10000)
	if err != nil {
		t.Fatal(err)
	}
	return started, req
}

func lifecycleAccept(t *testing.T, ch *pb.ChallengeRecord, req *pb.RechargeRequest, address string, direction pb.WitnessVoteType) *witness.ArbitrationVoteOutcome {
	t.Helper()
	out, err := witness.ComputeArbitrationBallotOutcome(ch, req, lifecycleVote(address, direction, ch.BallotRound), ch.BallotRound, ch.DeadlineHeight)
	if err != nil {
		t.Fatalf("accept %s %s: %v", address, direction, err)
	}
	return out
}

func lifecyclePositions(t *testing.T, votes []*pb.WitnessVote, want map[string]pb.WitnessVoteType) {
	t.Helper()
	got := make(map[string]pb.WitnessVoteType)
	for _, vote := range votes {
		if vote == nil {
			t.Fatal("nil frozen position")
		}
		if _, exists := got[vote.WitnessAddress]; exists {
			t.Fatalf("duplicate position for %s", vote.WitnessAddress)
		}
		got[vote.WitnessAddress] = vote.VoteType
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}
}

func TestArbitrationLifecyclePrepareSnapshotsPolicyAndOriginalStage(t *testing.T) {
	cfg := lifecyclePolicy()
	req := &pb.RechargeRequest{
		RequestId: "lifecycle-request",
		Votes:     []*pb.WitnessVote{lifecycleVote("a", pb.WitnessVoteType_VOTE_PASS, 0)},
		OriginalPositions: []*pb.WitnessVote{
			lifecycleVote("b", pb.WitnessVoteType_VOTE_PASS, 0),
			lifecycleVote("a", pb.WitnessVoteType_VOTE_FAIL, 0),
			lifecycleVote("c", pb.WitnessVoteType_VOTE_ABSTAIN, 0),
		},
	}
	ch := &pb.ChallengeRecord{RequestId: req.RequestId}
	if err := witness.PrepareArbitrationRecord(ch, req, cfg); err != nil {
		t.Fatal(err)
	}
	if ch.ArbitrationRound != 1 || ch.OrdinaryThresholdBps != 7500 || !reflect.DeepEqual(ch.OrdinaryCommitteeSizes, []uint32{4, 8, 16}) {
		t.Fatalf("unexpected prepared policy: %v", ch)
	}
	lifecyclePositions(t, ch.OriginalVotes, map[string]pb.WitnessVoteType{"a": pb.WitnessVoteType_VOTE_FAIL, "b": pb.WitnessVoteType_VOTE_PASS})
	snapshot := proto.Clone(ch).(*pb.ChallengeRecord)
	cfg.OrdinaryCommitteeSizes[0] = 5
	cfg.OrdinaryThresholdBps = 9000
	req.OriginalPositions[1].VoteType = pb.WitnessVoteType_VOTE_PASS
	req.OriginalPositions = append(req.OriginalPositions, lifecycleVote("new", pb.WitnessVoteType_VOTE_PASS, 0))
	if err := witness.PrepareArbitrationRecord(ch, req, cfg); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(ch, snapshot) {
		t.Fatal("repeated preparation or external mutations altered frozen policy/original positions")
	}
}

func TestArbitrationLifecycleOriginalFallbackKeepsLastDirection(t *testing.T) {
	req := &pb.RechargeRequest{Votes: []*pb.WitnessVote{
		lifecycleVote("b", pb.WitnessVoteType_VOTE_FAIL, 0),
		lifecycleVote("a", pb.WitnessVoteType_VOTE_FAIL, 0),
		lifecycleVote("a", pb.WitnessVoteType_VOTE_PASS, 0),
		lifecycleVote("b", pb.WitnessVoteType_VOTE_ABSTAIN, 0),
	}}
	got := witness.OriginalPositions(req)
	lifecyclePositions(t, got, map[string]pb.WitnessVoteType{"a": pb.WitnessVoteType_VOTE_PASS, "b": pb.WitnessVoteType_VOTE_FAIL})
	if got[0].WitnessAddress != "a" || got[1].WitnessAddress != "b" {
		t.Fatal("position order is not deterministic")
	}
	got[0].VoteType = pb.WitnessVoteType_VOTE_FAIL
	if req.Votes[2].VoteType != pb.WitnessVoteType_VOTE_PASS {
		t.Fatal("returned original positions alias request votes")
	}
}

func TestArbitrationLifecycleQuorumUsesPlannedCommittee(t *testing.T) {
	for i, want := range []uint32{3, 6, 12} {
		t.Run(fmt.Sprintf("round_%d", i+1), func(t *testing.T) {
			ch, req := lifecycleFixture(t, false)
			ch.ArbitrationRound = uint32(i + 1)
			ch.Arbitrators = []string{"a", "b"}
			got, err := witness.ArbitrationQuorum(ch)
			if err != nil || got != want {
				t.Fatalf("quorum = %d, %v; want %d", got, err, want)
			}
			ch = lifecycleAccept(t, ch, req, "a", pb.WitnessVoteType_VOTE_PASS).Challenge
			out := lifecycleAccept(t, ch, req, "b", pb.WitnessVoteType_VOTE_PASS)
			if out.Challenge.Finalized || out.Request != nil {
				t.Fatal("all available voters must not lower the planned quorum")
			}
		})
	}
	ch, _ := lifecycleFixture(t, false)
	ch.OrdinaryCommitteeSizes = []uint32{5, 9, 17}
	if q, err := witness.ArbitrationQuorum(ch); err != nil || q != 4 {
		t.Fatalf("fractional quorum must round up: %d, %v", q, err)
	}
	court, _ := lifecycleFixture(t, true)
	court.OrdinaryThresholdBps = 10000
	if q, err := witness.ArbitrationQuorum(court); err != nil || q != 3 {
		t.Fatalf("final court must retain its fixed 3-of-4 threshold: %d, %v", q, err)
	}
}

func TestArbitrationLifecycleRejectsInvalidPolicy(t *testing.T) {
	cases := []struct {
		name      string
		sizes     []uint32
		threshold uint32
	}{
		{"missing_round", []uint32{4, 8}, 7500},
		{"extra_round", []uint32{4, 8, 16, 32}, 7500},
		{"too_small", []uint32{3, 8, 16}, 7500},
		{"same_size", []uint32{4, 4, 16}, 7500},
		{"shrinking", []uint32{4, 16, 8}, 7500},
		{"not_strict_majority", []uint32{4, 8, 16}, 5000},
		{"over_unanimity", []uint32{4, 8, 16}, 10001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := &pb.ChallengeRecord{ArbitrationRound: 1, OrdinaryCommitteeSizes: tc.sizes, OrdinaryThresholdBps: tc.threshold}
			if _, err := witness.ArbitrationQuorum(ch); err == nil {
				t.Fatal("invalid policy accepted by quorum")
			}
			cfg := &witness.Config{OrdinaryCommitteeSizes: tc.sizes, OrdinaryThresholdBps: tc.threshold}
			if err := witness.PrepareArbitrationRecord(&pb.ChallengeRecord{}, &pb.RechargeRequest{}, cfg); err == nil {
				t.Fatal("invalid policy accepted by preparation")
			}
		})
	}
}

func TestArbitrationLifecycleOriginalWrongNotWashedByLaterRoles(t *testing.T) {
	for _, court := range []bool{false, true} {
		t.Run(fmt.Sprintf("court_%t", court), func(t *testing.T) {
			ch, req := lifecycleFixture(t, court)
			ch.OriginalVotes = []*pb.WitnessVote{lifecycleVote("a", pb.WitnessVoteType_VOTE_FAIL, 0), lifecycleVote("b", pb.WitnessVoteType_VOTE_PASS, 0)}
			original := proto.Clone(ch).(*pb.ChallengeRecord).OriginalVotes
			var out *witness.ArbitrationVoteOutcome
			for _, address := range []string{"a", "b", "c"} {
				out = lifecycleAccept(t, ch, req, address, pb.WitnessVoteType_VOTE_PASS)
				ch = out.Challenge
			}
			if !ch.Finalized || ch.FinalVote != pb.WitnessVoteType_VOTE_PASS {
				t.Fatal("expected PASS finalization")
			}
			if !reflect.DeepEqual(ch.OriginalVotes, original) {
				t.Fatal("later voting rewrote frozen original responsibility")
			}
			input := witness.ArbitrationRewardInput{FinalVote: ch.FinalVote, Original: ch.OriginalVotes, Ordinary: ch.OrdinaryVotes, Court: ch.FinalCourt}
			if court {
				input.CourtVotes = ch.ArbitrationVotes
			}
			got, err := witness.ArbitrationEligibility(input)
			if err != nil || !reflect.DeepEqual(got.Wrong, []string{"a"}) || !reflect.DeepEqual(got.FeeRecipients, []string{"b", "c"}) {
				t.Fatalf("original wrong was washed by a later correct role: %+v, %v", got, err)
			}
		})
	}
}

func TestArbitrationLifecycleOrdinaryCorrectionAndAbstention(t *testing.T) {
	ch, req := lifecycleFixture(t, false)
	ch = lifecycleAccept(t, ch, req, "a", pb.WitnessVoteType_VOTE_FAIL).Challenge
	ch.ArbitrationRound = 2
	var err error
	ch, err = witness.StartArbitrationRound(ch, []string{"a", "b", "c", "d", "e", "f", "g", "h"}, nil, ch.DeadlineHeight+1, 10000)
	if err != nil {
		t.Fatal(err)
	}
	ch = lifecycleAccept(t, ch, req, "a", pb.WitnessVoteType_VOTE_PASS).Challenge
	lifecyclePositions(t, ch.OrdinaryVotes, map[string]pb.WitnessVoteType{"a": pb.WitnessVoteType_VOTE_PASS})
	ch.ArbitrationRound = 3
	ch, err = witness.StartArbitrationRound(ch, []string{"a", "b", "c", "d"}, []string{"a", "b", "c", "d"}, ch.DeadlineHeight+1, 10000)
	if err != nil {
		t.Fatal(err)
	}
	ch = lifecycleAccept(t, ch, req, "a", pb.WitnessVoteType_VOTE_ABSTAIN).Challenge
	lifecyclePositions(t, ch.OrdinaryVotes, map[string]pb.WitnessVoteType{"a": pb.WitnessVoteType_VOTE_PASS})
	if ch.PassCount != 0 || ch.FailCount != 0 || len(ch.ArbitrationVotes) != 1 {
		t.Fatal("abstention must consume a ballot but add no directional count")
	}
	if _, err := witness.ComputeArbitrationBallotOutcome(ch, req, lifecycleVote("a", pb.WitnessVoteType_VOTE_FAIL, ch.BallotRound), ch.BallotRound, ch.DeadlineHeight); !errors.Is(err, witness.ErrDuplicateVote) {
		t.Fatalf("abstainer voted again in the same round: %v", err)
	}
	got, err := witness.ArbitrationEligibility(witness.ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_PASS, Ordinary: ch.OrdinaryVotes})
	if err != nil || len(got.Wrong) != 0 || !reflect.DeepEqual(got.FeeRecipients, []string{"a"}) || !reflect.DeepEqual(got.FineRecipients, []string{"a"}) {
		t.Fatalf("corrected ordinary position remains punished: %+v, %v", got, err)
	}
}

func TestArbitrationLifecycleCourtPreservesFrozenOrdinaryResponsibility(t *testing.T) {
	ch, req := lifecycleFixture(t, true)
	ch.OrdinaryVotes = []*pb.WitnessVote{lifecycleVote("a", pb.WitnessVoteType_VOTE_FAIL, ch.BallotRound-1)}
	for _, a := range []string{"a", "b", "c"} {
		ch = lifecycleAccept(t, ch, req, a, pb.WitnessVoteType_VOTE_PASS).Challenge
	}
	lifecyclePositions(t, ch.OrdinaryVotes, map[string]pb.WitnessVoteType{"a": pb.WitnessVoteType_VOTE_FAIL})
	got, err := witness.ArbitrationEligibility(witness.ArbitrationRewardInput{FinalVote: ch.FinalVote, Ordinary: ch.OrdinaryVotes, Court: ch.FinalCourt, CourtVotes: ch.ArbitrationVotes})
	if err != nil || !reflect.DeepEqual(got.Wrong, []string{"a"}) || !reflect.DeepEqual(got.FeeRecipients, []string{"b", "c"}) || len(got.FineRecipients) != 0 {
		t.Fatalf("court vote must not wash ordinary liability or earn a fine share: %+v, %v", got, err)
	}
}

func TestArbitrationLifecycleSelectCourtRanksStakeThenAddress(t *testing.T) {
	active := func(a, amount string) *pb.WitnessInfo {
		return &pb.WitnessInfo{Address: a, StakeAmount: amount, Status: pb.WitnessStatus_WITNESS_ACTIVE}
	}
	infos := []*pb.WitnessInfo{
		active("b", "80"), active("c", "32"), active("a", "80"), active("d", "50"),
		active("z", "100000000000000000000000000000"), active("a", "80"),
		active("under", "31"), active("zero", "0"), active("negative", "-100"), active("malformed", "100.5"), active("", "999999"), nil,
	}
	for _, status := range []pb.WitnessStatus{pb.WitnessStatus_WITNESS_CANDIDATE, pb.WitnessStatus_WITNESS_UNSTAKING, pb.WitnessStatus_WITNESS_EXITED, pb.WitnessStatus_WITNESS_SLASHED} {
		infos = append(infos, &pb.WitnessInfo{Address: status.String(), StakeAmount: "999999999999999999999999999999", Status: status})
	}
	for _, reverse := range []bool{false, true} {
		if reverse {
			for i, j := 0, len(infos)-1; i < j; i, j = i+1, j-1 {
				infos[i], infos[j] = infos[j], infos[i]
			}
		}
		got, err := witness.SelectFinalCourt(infos, "32")
		if err != nil || !reflect.DeepEqual(got, []string{"z", "a", "b", "d"}) {
			t.Fatalf("ranked selection = %v, %v", got, err)
		}
	}
}

func TestArbitrationLifecycleSelectCourtRequiresFourEligibleIdentities(t *testing.T) {
	infos := []*pb.WitnessInfo{
		{Address: "a", StakeAmount: "32", Status: pb.WitnessStatus_WITNESS_ACTIVE},
		{Address: "b", StakeAmount: "32", Status: pb.WitnessStatus_WITNESS_ACTIVE},
		{Address: "c", StakeAmount: "32", Status: pb.WitnessStatus_WITNESS_ACTIVE},
		{Address: "a", StakeAmount: "32", Status: pb.WitnessStatus_WITNESS_ACTIVE},
		{Address: "d", StakeAmount: "0", Status: pb.WitnessStatus_WITNESS_ACTIVE},
	}
	if _, err := witness.SelectFinalCourt(infos, "32"); !errors.Is(err, witness.ErrInsufficientFinalCourt) {
		t.Fatalf("duplicate identity or zero stake filled a fourth seat: %v", err)
	}
	for _, minimum := range []string{"", "0", "-1", "32.5", "invalid"} {
		if _, err := witness.SelectFinalCourt(infos, minimum); err == nil {
			t.Fatalf("invalid minimum %q accepted", minimum)
		}
	}
}

func TestArbitrationLifecycleFreezesCourtAtThirdRoundAndKeepsIt(t *testing.T) {
	ch, _ := lifecycleFixture(t, false)
	members := []string{"a", "b", "c", "d"}
	infos := []*pb.WitnessInfo{}
	for i, a := range []string{"a", "b", "c", "d", "e", "f"} {
		infos = append(infos, &pb.WitnessInfo{Address: a, StakeAmount: fmt.Sprint(100 - i*10), Status: pb.WitnessStatus_WITNESS_ACTIVE})
	}
	court, err := witness.SelectFinalCourt(infos, "32")
	if err != nil {
		t.Fatal(err)
	}
	ch.ArbitrationRound = 2
	ch, err = witness.StartArbitrationRound(ch, members, court, ch.DeadlineHeight+1, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.FinalCourt) != 0 || ch.CourtSnapshotHeight != 0 {
		t.Fatal("court was frozen before third round")
	}
	ch.ArbitrationRound = 3
	height := ch.DeadlineHeight + 1
	ch, err = witness.StartArbitrationRound(ch, members, court, height, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if ch.CourtSnapshotHeight != height || !reflect.DeepEqual(ch.FinalCourt, []string{"a", "b", "c", "d"}) {
		t.Fatalf("missing third-round court snapshot: %v", ch)
	}
	court[0] = "mutated-input"
	infos[0].StakeAmount = "0"
	infos[1].Status = pb.WitnessStatus_WITNESS_UNSTAKING
	newCourt, err := witness.SelectFinalCourt(infos, "32")
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(newCourt, ch.FinalCourt) {
		t.Fatal("test must change the current eligible ranking")
	}
	ch.ArbitrationStage = pb.ArbitrationStage_ARBITRATION_FINAL_COURT
	for retry := 0; retry < 3; retry++ {
		ch, err = witness.StartArbitrationRound(ch, newCourt, newCourt, ch.DeadlineHeight+1, 10000)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(ch.FinalCourt, []string{"a", "b", "c", "d"}) || !reflect.DeepEqual(ch.Arbitrators, ch.FinalCourt) || ch.CourtSnapshotHeight != height || !ch.OrdinaryVotesFrozen {
			t.Fatalf("court membership changed on entry/retry %d: %v", retry, ch)
		}
	}
}

func TestArbitrationLifecycleRoundStartClearsOnlyCurrentBallots(t *testing.T) {
	ch, req := lifecycleFixture(t, false)
	ch.OriginalVotes = []*pb.WitnessVote{lifecycleVote("original", pb.WitnessVoteType_VOTE_FAIL, 0)}
	ch = lifecycleAccept(t, ch, req, "a", pb.WitnessVoteType_VOTE_PASS).Challenge
	ch.ArbitrationRound = 2
	before := proto.Clone(ch).(*pb.ChallengeRecord)
	members := []string{"a", "b", "c", "d"}
	updated, err := witness.StartArbitrationRound(ch, members, nil, ch.DeadlineHeight+1, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(ch, before) {
		t.Fatal("round start mutated its input")
	}
	if updated.BallotRound != before.BallotRound+1 || updated.DeadlineHeight != before.DeadlineHeight+10001 || len(updated.ArbitrationVotes) != 0 || updated.PassCount != 0 || updated.FailCount != 0 {
		t.Fatalf("new round retained current vote state: %v", updated)
	}
	lifecyclePositions(t, updated.OriginalVotes, map[string]pb.WitnessVoteType{"original": pb.WitnessVoteType_VOTE_FAIL})
	lifecyclePositions(t, updated.OrdinaryVotes, map[string]pb.WitnessVoteType{"a": pb.WitnessVoteType_VOTE_PASS})
	members[0] = "changed"
	updated.OriginalVotes[0].WitnessAddress = "changed"
	updated.OrdinaryVotes[0].VoteType = pb.WitnessVoteType_VOTE_FAIL
	if updated.Arbitrators[0] != "a" || !proto.Equal(ch, before) {
		t.Fatal("round result aliases input members or stage positions")
	}
}

func TestArbitrationLifecycleRoundStartRejectsInvalidState(t *testing.T) {
	tests := []struct {
		name   string
		change func(*pb.ChallengeRecord, *[]string, *[]string, *uint64, *uint64)
	}{
		{"finalized", func(c *pb.ChallengeRecord, _, _ *[]string, _, _ *uint64) { c.Finalized = true }},
		{"round_overflow", func(c *pb.ChallengeRecord, _, _ *[]string, _, _ *uint64) { c.BallotRound = math.MaxUint64 }},
		{"zero_period", func(_ *pb.ChallengeRecord, _, _ *[]string, _, p *uint64) { *p = 0 }},
		{"height_overflow", func(_ *pb.ChallengeRecord, _, _ *[]string, h, _ *uint64) { *h = math.MaxUint64 - 10 }},
		{"no_timeout_successor", func(_ *pb.ChallengeRecord, _, _ *[]string, h, p *uint64) { *h, *p = math.MaxUint64-10, 10 }},
		{"duplicate_member", func(_ *pb.ChallengeRecord, m, _ *[]string, _, _ *uint64) { *m = []string{"a", "a"} }},
		{"empty_member", func(_ *pb.ChallengeRecord, m, _ *[]string, _, _ *uint64) { *m = []string{"a", ""} }},
		{"oversized_committee", func(_ *pb.ChallengeRecord, m, _ *[]string, _, _ *uint64) { *m = []string{"a", "b", "c", "d", "e"} }},
		{"round_zero", func(c *pb.ChallengeRecord, _, _ *[]string, _, _ *uint64) { c.ArbitrationRound = 0 }},
		{"ordinary_round_four", func(c *pb.ChallengeRecord, _, _ *[]string, _, _ *uint64) { c.ArbitrationRound = 4 }},
		{"missing_third_round_court", func(c *pb.ChallengeRecord, _, court *[]string, _, _ *uint64) {
			c.ArbitrationRound = 3
			*court = []string{"a", "b", "c"}
		}},
		{"duplicate_court", func(c *pb.ChallengeRecord, _, court *[]string, _, _ *uint64) {
			c.ArbitrationRound = 3
			*court = []string{"a", "b", "c", "a"}
		}},
		{"empty_court_member", func(c *pb.ChallengeRecord, _, court *[]string, _, _ *uint64) {
			c.ArbitrationRound = 3
			*court = []string{"a", "b", "c", ""}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch, _ := lifecycleFixture(t, false)
			members, court := []string{"a", "b", "c", "d"}, []string(nil)
			height, period := uint64(20000), uint64(10000)
			tc.change(ch, &members, &court, &height, &period)
			before := proto.Clone(ch)
			if _, err := witness.StartArbitrationRound(ch, members, court, height, period); err == nil {
				t.Fatal("invalid round start accepted")
			}
			if !proto.Equal(ch, before) {
				t.Fatal("rejected round start mutated input")
			}
		})
	}
	if _, err := witness.StartArbitrationRound(nil, nil, nil, 1, 10000); err == nil {
		t.Fatal("nil state accepted")
	}
}

func TestArbitrationLifecycleDeadlineInclusiveAndPureTransition(t *testing.T) {
	for _, court := range []bool{false, true} {
		t.Run(fmt.Sprintf("court_%t", court), func(t *testing.T) {
			ch, req := lifecycleFixture(t, court)
			vote := lifecycleVote("a", pb.WitnessVoteType_VOTE_PASS, ch.BallotRound)
			chBefore, reqBefore, voteBefore := proto.Clone(ch), proto.Clone(req), proto.Clone(vote)
			out, err := witness.ComputeArbitrationBallotOutcome(ch, req, vote, ch.BallotRound, ch.DeadlineHeight)
			if err != nil || out == nil || out.Challenge.PassCount != 1 {
				t.Fatalf("deadline ballot rejected: %v", err)
			}
			if !proto.Equal(ch, chBefore) || !proto.Equal(req, reqBefore) || !proto.Equal(vote, voteBefore) {
				t.Fatal("accepted transition mutated inputs")
			}
			out.Challenge.ArbitrationVotes[0].WitnessAddress = "changed"
			if !proto.Equal(vote, voteBefore) {
				t.Fatal("accepted ballot aliases caller vote")
			}
			if _, err := witness.ComputeArbitrationBallotOutcome(ch, req, vote, ch.BallotRound, ch.DeadlineHeight+1); !errors.Is(err, witness.ErrArbitrationExpired) {
				t.Fatalf("late ballot accepted: %v", err)
			}
			if !proto.Equal(ch, chBefore) || !proto.Equal(req, reqBefore) || !proto.Equal(vote, voteBefore) {
				t.Fatal("rejected transition mutated inputs")
			}
		})
	}
}

func TestArbitrationLifecycleRejectsInvalidIncomingBallots(t *testing.T) {
	tests := []struct {
		name   string
		change func(*pb.ChallengeRecord, *pb.RechargeRequest, *pb.WitnessVote, *uint64)
	}{
		{"zero_round", func(_ *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, r *uint64) { *r = 0 }},
		{"old_round", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) { c.BallotRound++ }},
		{"payload_round_mismatch", func(_ *pb.ChallengeRecord, _ *pb.RechargeRequest, v *pb.WitnessVote, r *uint64) {
			v.BallotRound = *r + 1
		}},
		{"nonmember", func(_ *pb.ChallengeRecord, _ *pb.RechargeRequest, v *pb.WitnessVote, _ *uint64) {
			v.WitnessAddress = "outsider"
		}},
		{"empty_identity", func(_ *pb.ChallengeRecord, _ *pb.RechargeRequest, v *pb.WitnessVote, _ *uint64) {
			v.WitnessAddress = ""
		}},
		{"negative_vote", func(_ *pb.ChallengeRecord, _ *pb.RechargeRequest, v *pb.WitnessVote, _ *uint64) {
			v.VoteType = pb.WitnessVoteType(-1)
		}},
		{"unknown_vote", func(_ *pb.ChallengeRecord, _ *pb.RechargeRequest, v *pb.WitnessVote, _ *uint64) {
			v.VoteType = pb.WitnessVoteType(3)
		}},
		{"other_request", func(_ *pb.ChallengeRecord, _ *pb.RechargeRequest, v *pb.WitnessVote, _ *uint64) {
			v.RequestId = "other"
		}},
		{"challenge_request_mismatch", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) {
			c.RequestId = "other"
		}},
		{"already_finalized", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) { c.Finalized = true }},
		{"already_settled", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) { c.Settled = true }},
		{"not_active", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) {
			c.Status = pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION
		}},
		{"request_terminal", func(_ *pb.ChallengeRecord, r *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) {
			r.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
		}},
		{"frozen_ordinary", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) {
			c.OrdinaryVotesFrozen = true
		}},
		{"duplicate_ballot", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, v *pb.WitnessVote, _ *uint64) {
			c.ArbitrationVotes = []*pb.WitnessVote{proto.Clone(v).(*pb.WitnessVote)}
		}},
		{"duplicate_committee", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) {
			c.Arbitrators = []string{"a", "b", "b", "d"}
		}},
		{"empty_committee_member", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) {
			c.Arbitrators = []string{"a", "b", "", "d"}
		}},
		{"oversized_committee", func(c *pb.ChallengeRecord, _ *pb.RechargeRequest, _ *pb.WitnessVote, _ *uint64) {
			c.Arbitrators = append(c.Arbitrators, "e")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch, req := lifecycleFixture(t, false)
			vote, round := lifecycleVote("a", pb.WitnessVoteType_VOTE_PASS, ch.BallotRound), ch.BallotRound
			tc.change(ch, req, vote, &round)
			before, reqBefore, voteBefore := proto.Clone(ch), proto.Clone(req), proto.Clone(vote)
			if _, err := witness.ComputeArbitrationBallotOutcome(ch, req, vote, round, ch.DeadlineHeight); err == nil {
				t.Fatal("invalid ballot/state accepted")
			}
			if !proto.Equal(ch, before) || !proto.Equal(req, reqBefore) || !proto.Equal(vote, voteBefore) {
				t.Fatal("rejection mutated caller state")
			}
		})
	}
}

func TestArbitrationLifecycleRejectsMalformedPersistedBallots(t *testing.T) {
	for _, court := range []bool{false, true} {
		for _, kind := range []string{"nil", "old_round", "nonmember", "empty_identity", "other_request", "invalid_vote", "duplicate"} {
			t.Run(fmt.Sprintf("court_%t/%s", court, kind), func(t *testing.T) {
				ch, req := lifecycleFixture(t, court)
				old := lifecycleVote("a", pb.WitnessVoteType_VOTE_PASS, ch.BallotRound)
				ch.ArbitrationVotes = []*pb.WitnessVote{old}
				switch kind {
				case "nil":
					ch.ArbitrationVotes = []*pb.WitnessVote{nil}
				case "old_round":
					old.BallotRound--
				case "nonmember":
					old.WitnessAddress = "outsider"
				case "empty_identity":
					old.WitnessAddress = ""
				case "other_request":
					old.RequestId = "other"
				case "invalid_vote":
					old.VoteType = pb.WitnessVoteType(99)
				case "duplicate":
					ch.ArbitrationVotes = append(ch.ArbitrationVotes, proto.Clone(old).(*pb.WitnessVote))
				}
				before := proto.Clone(ch)
				if _, err := witness.ComputeArbitrationBallotOutcome(ch, req, lifecycleVote("d", pb.WitnessVoteType_VOTE_PASS, ch.BallotRound), ch.BallotRound, ch.DeadlineHeight); err == nil {
					t.Fatal("malformed persisted votes influenced tally")
				}
				if !proto.Equal(ch, before) {
					t.Fatal("invalid persisted votes mutated on rejection")
				}
			})
		}
	}
}

func TestArbitrationLifecycleRejectsCourtMembershipMismatch(t *testing.T) {
	for _, kind := range []string{"replacement", "duplicate_frozen", "missing_frozen", "unfrozen_ordinary"} {
		t.Run(kind, func(t *testing.T) {
			ch, req := lifecycleFixture(t, true)
			switch kind {
			case "replacement":
				ch.Arbitrators[3] = "replacement"
			case "duplicate_frozen":
				ch.FinalCourt[3] = "a"
			case "missing_frozen":
				ch.FinalCourt = ch.FinalCourt[:3]
			case "unfrozen_ordinary":
				ch.OrdinaryVotesFrozen = false
			}
			if _, err := witness.ComputeArbitrationBallotOutcome(ch, req, lifecycleVote("a", pb.WitnessVoteType_VOTE_PASS, ch.BallotRound), ch.BallotRound, ch.DeadlineHeight); err == nil {
				t.Fatal("malformed court accepted a ballot")
			}
		})
	}
}

func TestArbitrationLifecycleCourtCannotPoolVotesAcrossRetries(t *testing.T) {
	ch, req := lifecycleFixture(t, true)
	ch = lifecycleAccept(t, ch, req, "a", pb.WitnessVoteType_VOTE_PASS).Challenge
	ch = lifecycleAccept(t, ch, req, "b", pb.WitnessVoteType_VOTE_PASS).Challenge
	oldRound := ch.BallotRound
	var err error
	ch, err = witness.StartArbitrationRound(ch, nil, nil, ch.DeadlineHeight+1, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Finalized || ch.PassCount != 0 || ch.FailCount != 0 || len(ch.ArbitrationVotes) != 0 {
		t.Fatal("timeout created a default verdict or retained old tally")
	}
	if _, err := witness.ComputeArbitrationBallotOutcome(ch, req, lifecycleVote("a", pb.WitnessVoteType_VOTE_PASS, oldRound), oldRound, ch.DeadlineHeight); err == nil {
		t.Fatal("old-round signed ballot replayed")
	}
	ch = lifecycleAccept(t, ch, req, "c", pb.WitnessVoteType_VOTE_PASS).Challenge
	if ch.Finalized || ch.PassCount != 1 {
		t.Fatal("old two PASS ballots combined with one new PASS ballot")
	}
	ch = lifecycleAccept(t, ch, req, "a", pb.WitnessVoteType_VOTE_PASS).Challenge
	if ch.Finalized {
		t.Fatal("two current ballots finalized")
	}
	ch = lifecycleAccept(t, ch, req, "b", pb.WitnessVoteType_VOTE_PASS).Challenge
	if !ch.Finalized || ch.PassCount != 3 {
		t.Fatal("three fresh court votes did not finalize")
	}
}

func TestArbitrationLifecycleTallyRecomputedFromBallots(t *testing.T) {
	ch, req := lifecycleFixture(t, false)
	ch.PassCount, ch.FailCount = 100, 200
	ch = lifecycleAccept(t, ch, req, "a", pb.WitnessVoteType_VOTE_PASS).Challenge
	if ch.Finalized || ch.PassCount != 1 || ch.FailCount != 0 {
		t.Fatal("cached counters authorized a verdict without corresponding ballots")
	}
}

func TestArbitrationLifecycleFiniteBallotReference(t *testing.T) {
	choices := []pb.WitnessVoteType{pb.WitnessVoteType_VOTE_PASS, pb.WitnessVoteType_VOTE_FAIL, pb.WitnessVoteType_VOTE_ABSTAIN}
	identities := []string{"a", "b", "c", "d"}
	for _, court := range []bool{false, true} {
		for sequence := 0; sequence < 81; sequence++ {
			t.Run(fmt.Sprintf("court_%t/sequence_%02d", court, sequence), func(t *testing.T) {
				ch, req := lifecycleFixture(t, court)
				code, passes, failures := sequence, uint32(0), uint32(0)
				decided := false
				for position, address := range identities {
					vote := choices[code%3]
					code /= 3
					out, err := witness.ComputeArbitrationBallotOutcome(ch, req, lifecycleVote(address, vote, ch.BallotRound), ch.BallotRound, 101+uint64(position))
					if decided {
						if err == nil {
							t.Fatal("terminal result accepted another ballot")
						}
						continue
					}
					if err != nil {
						t.Fatalf("valid prefix rejected at %d: %v", position, err)
					}
					if vote == pb.WitnessVoteType_VOTE_PASS {
						passes++
					}
					if vote == pb.WitnessVoteType_VOTE_FAIL {
						failures++
					}
					decided = passes >= 3 || failures >= 3
					ch = out.Challenge
					if ch.PassCount != passes || ch.FailCount != failures || ch.Finalized != decided {
						t.Fatalf("prefix %d: PASS/FAIL/final = %d/%d/%t, reference %d/%d/%t", position, ch.PassCount, ch.FailCount, ch.Finalized, passes, failures, decided)
					}
					if !decided {
						if out.Request != nil || ch.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || ch.FinalizeHeight != 0 {
							t.Fatal("nonquorum prefix produced terminal state")
						}
						continue
					}
					wantVote, wantStatus := pb.WitnessVoteType_VOTE_PASS, pb.RechargeRequestStatus_RECHARGE_FINALIZED
					if failures >= 3 {
						wantVote, wantStatus = pb.WitnessVoteType_VOTE_FAIL, pb.RechargeRequestStatus_RECHARGE_REJECTED
					}
					if out.Request == nil || out.Request.Status != wantStatus || ch.FinalVote != wantVote || ch.ChallengeSuccess != (wantVote == pb.WitnessVoteType_VOTE_FAIL) || !ch.OrdinaryVotesFrozen || ch.Status != pb.ChallengeStatus_CHALLENGE_FINALIZED || ch.FinalizeHeight != 101+uint64(position) || out.Request.FinalizeHeight != ch.FinalizeHeight {
						t.Fatalf("first-quorum verdict disagrees with reference: %+v", out)
					}
				}
			})
		}
	}
}

func TestArbitrationLifecycleChallengeSuccessUsesActualDirection(t *testing.T) {
	for _, originalPass := range []bool{false, true} {
		for _, finalPass := range []bool{false, true} {
			t.Run(fmt.Sprintf("original_pass_%t/final_pass_%t", originalPass, finalPass), func(t *testing.T) {
				ch, req := lifecycleFixture(t, true)
				if !originalPass {
					req.PassCount, req.FailCount = 0, 4
				}
				vote := pb.WitnessVoteType_VOTE_FAIL
				if finalPass {
					vote = pb.WitnessVoteType_VOTE_PASS
				}
				for _, a := range []string{"a", "b", "c"} {
					ch = lifecycleAccept(t, ch, req, a, vote).Challenge
				}
				if !ch.Finalized || ch.ChallengeSuccess != (originalPass != finalPass) {
					t.Fatalf("challenge success confused evidence direction with challenge side: %v", ch)
				}
			})
		}
	}
}
