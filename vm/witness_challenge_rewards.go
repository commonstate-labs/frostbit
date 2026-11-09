package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/witness"
	"math/big"
	"sort"
)

func attachChallengeContributorRefunds(sv StateView, o *witness.ArbitrationVoteOutcome) {
	if o == nil || o.Challenge == nil || o.Challenge.ChallengeId == "" {
		return
	}
	if o.Request != nil && o.Request.ArbitrationProtocolVersion != 0 {
		return                                                                      
	}
	contribs, total := readChallengeContributions(sv, o.Challenge.ChallengeId)
	if len(contribs) == 0 || total.Sign() <= 0 {
		return
	}
	o.ChallengerAddr = ""
	o.ChallengerRefund = ""
	if !o.Challenge.ChallengeSuccess {
		o.ChallengerRefunds = nil
		return
	}

	slashPool := actualSlashTotalFromState(sv, o.SlashAmounts)

	addrs := sortedBigIntMapKeys(contribs)
	refunds := make(map[string]string, len(addrs))
	distributed := big.NewInt(0)
	for _, addr := range addrs {
		principal := contribs[addr]
		reward := new(big.Int).Mul(slashPool, principal) // safe-math-ok: 按实际罚没池与出资权重分配的分子，随后除以 total
		reward.Div(reward, total)
		refund, err := SafeAdd(principal, reward)
		if err != nil {
			continue
		}
		refunds[addr] = refund.String()
		distributed.Add(distributed, reward)
	}

	remainder := new(big.Int).Sub(slashPool, distributed) // safe-math-ok: 每笔奖励向下取整，已分配总额不超罚没池
	for _, addr := range addrs {
		if remainder.Sign() <= 0 {
			break
		}
		cur, err := parseBalanceStrict("challenger refund", refunds[addr])
		if err != nil {
			continue
		}
		cur.Add(cur, big.NewInt(1))
		refunds[addr] = cur.String()
		remainder.Sub(remainder, big.NewInt(1))
	}
	o.ChallengerRefunds = refunds
}

func actualSlashTotalFromState(sv StateView, slashAmounts map[string]string) *big.Int {
	total := big.NewInt(0)
	addrs := make([]string, 0, len(slashAmounts))
	for addr := range slashAmounts {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		amt, err := ParseBalance(slashAmounts[addr])
		if err != nil || amt.Sign() <= 0 {
			continue
		}
		total.Add(total, actualSlashAmountFromState(sv, addr, amt))
	}
	return total
}

func actualSlashAmountFromState(sv StateView, addr string, amount *big.Int) *big.Int {
	if sv == nil || addr == "" || amount == nil || amount.Sign() <= 0 {
		return big.NewInt(0)
	}
	bal := GetBalance(sv, addr, FeeToken)
	locked, err := ParseBalance(bal.WitnessLockedBalance)
	if err != nil || locked.Sign() <= 0 {
		return big.NewInt(0)
	}
	if locked.Cmp(amount) < 0 {
		return new(big.Int).Set(locked)
	}
	return new(big.Int).Set(amount)
}

func readChallengeContributions(sv StateView, challengeID string) (map[string]*big.Int, *big.Int) {
	scanned, err := sv.Scan(keys.KeyChallengeContributionPrefix(challengeID))
	if err != nil || len(scanned) == 0 {
		return nil, big.NewInt(0)
	}
	keysSorted := make([]string, 0, len(scanned))
	for key := range scanned {
		keysSorted = append(keysSorted, key)
	}
	sort.Strings(keysSorted)

	contribs := make(map[string]*big.Int)
	total := big.NewInt(0)
	for _, key := range keysSorted {
		var tx pb.WitnessChallengeTx
		if err := unmarshalProtoCompat(scanned[key], &tx); err != nil || tx.Base == nil {
			continue
		}
		amount, err := ParseBalance(tx.StakeAmount)
		if err != nil || amount.Sign() <= 0 || tx.Base.FromAddress == "" {
			continue
		}
		if contribs[tx.Base.FromAddress] == nil {
			contribs[tx.Base.FromAddress] = big.NewInt(0)
		}
		contribs[tx.Base.FromAddress].Add(contribs[tx.Base.FromAddress], amount)
		total.Add(total, amount)
	}
	return contribs, total
}

func sortedBigIntMapKeys(values map[string]*big.Int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
