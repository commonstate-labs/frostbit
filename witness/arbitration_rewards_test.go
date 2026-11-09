package witness

import (
	"math/big"
	"reflect"
	"testing"

	"dex/pb"
)

func rewardVote(a string, v pb.WitnessVoteType) *pb.WitnessVote {
	return &pb.WitnessVote{WitnessAddress: a, VoteType: v}
}
func rewardAmount(t *testing.T, m map[string]*big.Int, a, want string) {
	t.Helper()
	got := "0"
	if m[a] != nil {
		got = m[a].String()
	}
	if got != want {
		t.Fatalf("%s got %s want %s", a, got, want)
	}
}

func TestArbitrationRewardsWrongOriginalCannotBeWashedByOrdinaryOrCourt(t *testing.T) {
	in := ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_FAIL, Original: []*pb.WitnessVote{rewardVote("wrong", pb.WitnessVoteType_VOTE_PASS)}, Ordinary: []*pb.WitnessVote{rewardVote("wrong", pb.WitnessVoteType_VOTE_FAIL), rewardVote("good", pb.WitnessVoteType_VOTE_FAIL)}, Court: []string{"wrong", "court"}, CourtVotes: []*pb.WitnessVote{rewardVote("wrong", pb.WitnessVoteType_VOTE_FAIL), rewardVote("court", pb.WitnessVoteType_VOTE_FAIL)}, Fee: big.NewInt(101), Fine: big.NewInt(103), Contributions: map[string]*big.Int{"wrong": big.NewInt(30), "good": big.NewInt(50), "court": big.NewInt(20)}, Challenged: true, ChallengeSuccess: true}
	p, err := BuildArbitrationRewardPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	rewardAmount(t, p.Fees, "wrong", "0")
	rewardAmount(t, p.Refunds, "wrong", "30")
	rewardAmount(t, p.Refunds, "court", "20")
	rewardAmount(t, p.ContributorRewards, "good", "47")
	rewardAmount(t, p.VoterRewards, "good", "46")
	rewardAmount(t, p.Fees, "court", "51")
	rewardAmount(t, p.Fees, "good", "50")
	if p.Burn.String() != "10" {
		t.Fatal(p.Burn)
	}
}

func TestArbitrationRewardsEmptyGroupBurnDoesNotTransfer(t *testing.T) {
	in := ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_PASS, Ordinary: []*pb.WitnessVote{rewardVote("good", pb.WitnessVoteType_VOTE_PASS)}, Court: []string{"court"}, Fee: big.NewInt(0), Fine: big.NewInt(100), Contributions: map[string]*big.Int{"court": big.NewInt(100)}, Challenged: true, ChallengeSuccess: true}
	p, err := BuildArbitrationRewardPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	rewardAmount(t, p.VoterRewards, "good", "45")
	if p.Burn.String() != "55" {
		t.Fatal(p.Burn)
	}
	in.Ordinary = nil
	p, err = BuildArbitrationRewardPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Burn.String() != "100" {
		t.Fatal(p.Burn)
	}
}

func TestArbitrationRewardsFailurePrincipalAndFineToCorrectNonCourt(t *testing.T) {
	in := ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_PASS, Original: []*pb.WitnessVote{rewardVote("a", pb.WitnessVoteType_VOTE_PASS), rewardVote("b", pb.WitnessVoteType_VOTE_PASS), rewardVote("court", pb.WitnessVoteType_VOTE_PASS)}, Court: []string{"court"}, Fee: big.NewInt(0), Fine: big.NewInt(103), Contributions: map[string]*big.Int{"challenger": big.NewInt(100)}, Challenged: true}
	p, err := BuildArbitrationRewardPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	rewardAmount(t, p.FailedAwards, "a", "102")
	rewardAmount(t, p.FailedAwards, "b", "101")
	rewardAmount(t, p.FailedAwards, "court", "0")
	if len(p.Refunds) != 0 {
		t.Fatal("failed principal refunded")
	}
}

func TestArbitrationRewardsFrozenCourtExcludedWithoutCourtVotes(t *testing.T) {
	in := ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_PASS, Ordinary: []*pb.WitnessVote{rewardVote("overlap", pb.WitnessVoteType_VOTE_PASS)}, Court: []string{"overlap", "silent"}, Fee: big.NewInt(7), Fine: big.NewInt(100), Challenged: true, ChallengeSuccess: true}
	p, err := BuildArbitrationRewardPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	rewardAmount(t, p.Fees, "overlap", "7")
	rewardAmount(t, p.Fees, "silent", "0")
	if p.Burn.String() != "100" {
		t.Fatal(p.Burn)
	}
}

func TestArbitrationRewardsIntegerQuotaAcrossSmallAmounts(t *testing.T) {
	for amount := int64(0); amount <= 103; amount++ {
		p, err := BuildArbitrationRewardPlan(ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_PASS, Ordinary: []*pb.WitnessVote{rewardVote("a", pb.WitnessVoteType_VOTE_PASS), rewardVote("b", pb.WitnessVoteType_VOTE_PASS)}, Fee: big.NewInt(amount), Fine: big.NewInt(amount), Contributions: map[string]*big.Int{"a": big.NewInt(1), "b": big.NewInt(2)}, Challenged: true, ChallengeSuccess: true})
		if err != nil {
			t.Fatal(err)
		}
		                                                                          
		for i, group := range []map[string]*big.Int{p.ContributorRewards, p.VoterRewards} {
			sum := new(big.Int)
			for _, n := range group {
				sum.Add(sum, n)
			}
			lower := amount * 45 / 100
			if sum.Int64() < lower || sum.Int64() > lower+1 {
				t.Fatalf("amount=%d group=%d quota=%s", amount, i, sum)
			}
		}
		diff := new(big.Int).Sub(p.Fees["a"], p.Fees["b"])
		if diff.Sign() < 0 || diff.Cmp(big.NewInt(1)) > 0 {
			t.Fatal("unequal fee split")
		}
	}
}

func TestArbitrationRewardsNoRecipientBurnFeeNoFine(t *testing.T) {
	p, err := BuildArbitrationRewardPlan(ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_FAIL, Fee: big.NewInt(101), Fine: new(big.Int)})
	if err != nil {
		t.Fatal(err)
	}
	if p.Burn.String() != "101" {
		t.Fatal(p.Burn)
	}
}

func TestArbitrationRewardsRejectInvalidInputs(t *testing.T) {
	base := ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_PASS, Fee: new(big.Int), Fine: new(big.Int)}
	bad := []ArbitrationRewardInput{base, base, base, base}
	bad[0].FinalVote = pb.WitnessVoteType_VOTE_ABSTAIN
	bad[1].Fine = big.NewInt(-1)
	bad[2].Original = []*pb.WitnessVote{rewardVote("x", pb.WitnessVoteType_VOTE_PASS), rewardVote("x", pb.WitnessVoteType_VOTE_FAIL)}
	bad[3].CourtVotes = []*pb.WitnessVote{rewardVote("outsider", pb.WitnessVoteType_VOTE_PASS)}
	for i, in := range bad {
		if _, err := BuildArbitrationRewardPlan(in); err == nil {
			t.Fatalf("invalid case %d accepted", i)
		}
	}
}

func TestArbitrationRewardsDeduplicateAndLeaveInputsUnchanged(t *testing.T) {
	fee := new(big.Int).Lsh(big.NewInt(1), 200)
	before := new(big.Int).Set(fee)
	in := ArbitrationRewardInput{FinalVote: pb.WitnessVoteType_VOTE_PASS, Fee: fee, Fine: new(big.Int), Original: []*pb.WitnessVote{rewardVote("a", pb.WitnessVoteType_VOTE_PASS)}, Ordinary: []*pb.WitnessVote{rewardVote("a", pb.WitnessVoteType_VOTE_PASS), rewardVote("b", pb.WitnessVoteType_VOTE_ABSTAIN)}}
	p, err := BuildArbitrationRewardPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Fees["a"], before) || fee.Cmp(before) != 0 || len(p.Fees) != 1 {
		t.Fatal("duplicate reward or input mutation")
	}
}
