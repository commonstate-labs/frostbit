package witness

import (
	"fmt"
	"math/big"
	"sort"

	"dex/pb"
)

                                                                               
                                                                                   
type ArbitrationRewardInput struct {
	FinalVote                      pb.WitnessVoteType
	Original, Ordinary, CourtVotes []*pb.WitnessVote
	Court                          []string
	Fee, Fine                      *big.Int
	Contributions                  map[string]*big.Int
	Challenged, ChallengeSuccess   bool
}

type ArbitrationRewardEligibility struct {
	Wrong, FeeRecipients, FineRecipients []string
}

type ArbitrationRewardPlan struct {
	Fees, Refunds, ContributorRewards, VoterRewards, FailedAwards map[string]*big.Int
	Burn                                                          *big.Int
}

                                                                            
                                                                                
func ArbitrationEligibility(in ArbitrationRewardInput) (ArbitrationRewardEligibility, error) {
	var out ArbitrationRewardEligibility
	if !rewardDirection(in.FinalVote) {
		return out, fmt.Errorf("settlement requires PASS or FAIL")
	}
	wrong, correct, court := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range in.Court {
		if a == "" {
			return out, fmt.Errorf("empty court address")
		}
		court[a] = true
	}
	for _, stage := range [][]*pb.WitnessVote{in.Original, in.Ordinary} {
		seen := map[string]pb.WitnessVoteType{}
		for _, v := range stage {
			if v == nil || !rewardDirection(v.VoteType) {
				continue
			}
			if v.WitnessAddress == "" {
				return out, fmt.Errorf("empty position address")
			}
			if old, ok := seen[v.WitnessAddress]; ok && old != v.VoteType {
				return out, fmt.Errorf("conflicting frozen positions for %s", v.WitnessAddress)
			}
			seen[v.WitnessAddress] = v.VoteType
			if v.VoteType == in.FinalVote {
				correct[v.WitnessAddress] = true
			} else {
				wrong[v.WitnessAddress] = true
			}
		}
	}
	fees, fines := map[string]bool{}, map[string]bool{}
	for a := range correct {
		if !wrong[a] {
			fees[a] = true
			if !court[a] {
				fines[a] = true
			}
		}
	}
	for _, v := range in.CourtVotes {
		if v == nil || !rewardDirection(v.VoteType) {
			continue
		}
		if !court[v.WitnessAddress] {
			return out, fmt.Errorf("court vote outside frozen court: %s", v.WitnessAddress)
		}
		if v.VoteType == in.FinalVote && !wrong[v.WitnessAddress] {
			fees[v.WitnessAddress] = true
		}
	}
	out.Wrong, out.FeeRecipients, out.FineRecipients = rewardSortedSet(wrong), rewardSortedSet(fees), rewardSortedSet(fines)
	return out, nil
}

                                                                             
                                                                              
func BuildArbitrationRewardPlan(in ArbitrationRewardInput) (*ArbitrationRewardPlan, error) {
	e, err := ArbitrationEligibility(in)
	if err != nil {
		return nil, err
	}
	for _, n := range []*big.Int{in.Fee, in.Fine} {
		if n == nil || n.Sign() < 0 {
			return nil, fmt.Errorf("invalid settlement money")
		}
	}
	principal := new(big.Int)
	for a, n := range in.Contributions {
		if a == "" || n == nil || n.Sign() <= 0 {
			return nil, fmt.Errorf("invalid contribution")
		}
		principal.Add(principal, n)
	}
	if !in.Challenged && (principal.Sign() != 0 || in.Fine.Sign() != 0) {
		return nil, fmt.Errorf("unchallenged fee settlement cannot consume principal or fine")
	}
	p := &ArbitrationRewardPlan{Fees: map[string]*big.Int{}, Refunds: map[string]*big.Int{}, ContributorRewards: map[string]*big.Int{}, VoterRewards: map[string]*big.Int{}, FailedAwards: map[string]*big.Int{}, Burn: new(big.Int)}
	p.Fees = rewardEqual(in.Fee, e.FeeRecipients)
	if len(e.FeeRecipients) == 0 {
		p.Burn.Add(p.Burn, in.Fee)
	}
	if in.Challenged && !in.ChallengeSuccess {
		pool := new(big.Int).Add(principal, in.Fine)
		p.FailedAwards = rewardEqual(pool, e.FineRecipients)
		if len(e.FineRecipients) == 0 {
			p.Burn.Add(p.Burn, pool)
		}
	} else if in.Challenged {
		for a, n := range in.Contributions {
			p.Refunds[a] = new(big.Int).Set(n)
		}
		                                                               
		pools := rewardWeighted(in.Fine, map[string]*big.Int{"0": big.NewInt(10), "1": big.NewInt(45), "2": big.NewInt(45)})
		p.Burn.Add(p.Burn, pools["0"])
		excluded := map[string]bool{}
		for _, a := range e.Wrong {
			excluded[a] = true
		}
		for _, a := range in.Court {
			excluded[a] = true
		}
		weights := map[string]*big.Int{}
		for a, n := range in.Contributions {
			if !excluded[a] {
				weights[a] = n
			}
		}
		p.ContributorRewards = rewardWeighted(pools["1"], weights)
		if len(weights) == 0 {
			p.Burn.Add(p.Burn, pools["1"])
		}
		p.VoterRewards = rewardEqual(pools["2"], e.FineRecipients)
		if len(e.FineRecipients) == 0 {
			p.Burn.Add(p.Burn, pools["2"])
		}
	}
	sources := new(big.Int).Add(in.Fee, in.Fine)
	sources.Add(sources, principal)
	used := new(big.Int).Set(p.Burn)
	for _, group := range []map[string]*big.Int{p.Fees, p.Refunds, p.ContributorRewards, p.VoterRewards, p.FailedAwards} {
		for _, n := range group {
			used.Add(used, n)
		}
	}
	if sources.Cmp(used) != 0 {
		return nil, fmt.Errorf("unbalanced settlement: sources=%s uses=%s", sources, used)
	}
	return p, nil
}

func rewardDirection(v pb.WitnessVoteType) bool {
	return v == pb.WitnessVoteType_VOTE_PASS || v == pb.WitnessVoteType_VOTE_FAIL
}
func rewardSortedSet(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for a := range s {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
func rewardEqual(amount *big.Int, recipients []string) map[string]*big.Int {
	w := make(map[string]*big.Int, len(recipients))
	for _, a := range recipients {
		w[a] = big.NewInt(1)
	}
	return rewardWeighted(amount, w)
}

                                                                            
func rewardWeighted(amount *big.Int, weights map[string]*big.Int) map[string]*big.Int {
	out := make(map[string]*big.Int, len(weights))
	total := new(big.Int)
	addresses := make([]string, 0, len(weights))
	for a, w := range weights {
		addresses = append(addresses, a)
		total.Add(total, w)
	}
	if len(addresses) == 0 {
		return out
	}
	sort.Strings(addresses)
	remainders := map[string]*big.Int{}
	spent := new(big.Int)
	for _, a := range addresses {
		n := new(big.Int).Mul(amount, weights[a])
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(n, total, r)
		out[a], remainders[a] = q, r
		spent.Add(spent, q)
	}
	sort.SliceStable(addresses, func(i, j int) bool { return remainders[addresses[i]].Cmp(remainders[addresses[j]]) > 0 })
	left := new(big.Int).Sub(amount, spent).Int64()                                      
	for i := int64(0); i < left; i++ {
		out[addresses[i]].Add(out[addresses[i]], big.NewInt(1))
	}
	return out
}
