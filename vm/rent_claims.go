package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

const (
	rentClaimFuturePositionMargin = "future_position_margin"
	rentClaimChallengeStake       = "challenge_contribution"
)

func syncFuturePositionRentClaim(sv StateView, pos *future.Position) error {
	if sv == nil || pos == nil || strings.TrimSpace(pos.Owner) == "" || strings.TrimSpace(pos.ID) == "" {
		return nil
	}
	token, err := futureMarketQuoteToken(sv, pos.Pair)
	if err != nil {
		return fmt.Errorf("resolve future position rent token: %w", err)
	}
	amount := big.NewInt(0)
	if pos.Status == future.StatusOpen && pos.Margin != nil && pos.Margin.Sign() > 0 {
		amount = new(big.Int).Set(pos.Margin)
	}
	return rent.SetClaim(sv, pos.Owner, token, rentClaimFuturePositionMargin, pos.ID, amount)
}

func clearFuturePositionRentClaim(sv StateView, pos *future.Position) error {
	if sv == nil || pos == nil || strings.TrimSpace(pos.Owner) == "" || strings.TrimSpace(pos.ID) == "" {
		return nil
	}
	token, err := futureMarketQuoteToken(sv, pos.Pair)
	if err != nil {
		return fmt.Errorf("resolve future position rent token: %w", err)
	}
	return rent.SetClaim(sv, pos.Owner, token, rentClaimFuturePositionMargin, pos.ID, big.NewInt(0))
}

func rentChallengeContributionSourceID(challengeID, txID string) string {
	return hex.EncodeToString([]byte(challengeID + "\n" + txID))
}

func parseRentChallengeContributionSourceID(sourceID string) (string, string, error) {
	raw, err := hex.DecodeString(sourceID)
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(string(raw), "\n", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid challenge contribution source id")
	}
	return parts[0], parts[1], nil
}

func syncChallengeContributionRentClaim(sv StateView, challenge *pb.WitnessChallengeTx, caseID string, amount *big.Int) error {
	if sv == nil || challenge == nil || challenge.Base == nil || caseID == "" || challenge.Base.TxId == "" || challenge.Base.FromAddress == "" {
		return nil
	}
	return rent.SetClaim(
		sv,
		challenge.Base.FromAddress,
		FeeToken,
		rentClaimChallengeStake,
		rentChallengeContributionSourceID(caseID, challenge.Base.TxId),
		amount,
	)
}

func clearFinalizedChallengeContributionState(sv StateView, challengeID string) []WriteOp {
	if sv == nil || challengeID == "" {
		return nil
	}
	kvs, err := sv.Scan(keys.KeyChallengeContributionPrefix(challengeID))
	if err != nil || len(kvs) == 0 {
		return nil
	}
	var ops []WriteOp
	for _, k := range sortedRentKeys(kvs) {
		var tx pb.WitnessChallengeTx
		if err := unmarshalProtoCompat(kvs[k], &tx); err != nil || tx.Base == nil || tx.Base.FromAddress == "" || tx.Base.TxId == "" {
			continue
		}
		if err := rent.SetClaim(sv, tx.Base.FromAddress, FeeToken, rentClaimChallengeStake, rentChallengeContributionSourceID(challengeID, tx.Base.TxId), big.NewInt(0)); err != nil {
			continue
		}
		archiveKey := keys.KeyArchiveChallengeContribution(challengeID, tx.Base.TxId)
		sv.Set(archiveKey, kvs[k])
		ops = append(ops, archiveWriteOp(archiveKey, kvs[k], "archive_challenge_contribution"))
		sv.Del(k)
		ops = append(ops, WriteOp{Key: k, Del: true, Category: "challenge_contribution"})
	}
	return ops
}

func addRentRecycleBalance(sv StateView, token string, amount *big.Int) error {
	if sv == nil || strings.TrimSpace(token) == "" || amount == nil || amount.Sign() <= 0 {
		return nil
	}
	bal := GetBalance(sv, rent.RecycleFundAccount, token)
	cur, err := parseBalanceStrict("recycle balance", bal.Balance)
	if err != nil {
		return err
	}
	next, err := SafeAdd(cur, amount)
	if err != nil {
		return err
	}
	bal.Balance = next.String()
	SetBalance(sv, rent.RecycleFundAccount, token, bal)
	return nil
}
