package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/witness"
	"fmt"
	"math/big"

	"google.golang.org/protobuf/proto"
)

func (h *WitnessChallengeTxHandler) fundChallenge(tx *pb.WitnessChallengeTx, sv StateView) (ops []WriteOp, receipt *Receipt, err error) {
	fail := func(reason string, cause error) ([]WriteOp, *Receipt, error) {
		return nil, &Receipt{TxID: tx.Base.TxId, Status: "FAILED", Error: reason}, cause
	}
	snap := sv.Snapshot()
	defer func() {
		if receipt == nil || receipt.Status != "SUCCEED" {
			_ = sv.Revert(snap)
		}
	}()
	req := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(tx.RequestId))
	if req == nil {
		return fail("request not found", nil)
	}
	if e := requireWitnessArbitrationVersion(req); e != nil {
		return fail(e.Error(), nil)
	}
	caseID := req.ChallengeId
	if caseID == "" {
		caseID = tx.Base.TxId
	}
	contributionKey := keys.KeyChallengeContribution(caseID, tx.Base.TxId)
	if _, exists, e := sv.Get(contributionKey); e != nil {
		return fail(e.Error(), e)
	} else if exists {
		return nil, &Receipt{TxID: tx.Base.TxId, Status: "SUCCEED"}, nil
	}
	if req.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		return fail("request is not fundraising in its publicity period", nil)
	}
	if tx.Base.ExecutedHeight > req.DeadlineHeight {
		return fail("publicity period has expired", nil)
	}
	amount, e := ParseBalance(tx.StakeAmount)
	if e != nil || amount.Sign() <= 0 {
		return fail("challenge amount must be a positive integer", e)
	}
	if amount.Cmp(big.NewInt(protocol.MinChallengeContributionRaw)) < 0 {
		return fail("challenge contribution must be at least 10 FB", nil)
	}
	if _, exists, e := sv.Get(keys.KeyAccount(tx.Base.FromAddress)); e != nil {
		return fail(e.Error(), e)
	} else if !exists {
		return fail("challenger account not found", nil)
	}
	var ch *pb.ChallengeRecord
	if req.ChallengeId != "" {
		data, exists, e := sv.Get(keys.KeyChallengeRecord(caseID))
		if e != nil {
			return fail(e.Error(), e)
		}
		if !exists {
			return fail("challenge record missing", nil)
		}
		ch = &pb.ChallengeRecord{}
		if e := unmarshalProtoCompat(data, ch); e != nil {
			return fail(e.Error(), e)
		}
		if ch.Finalized || ch.SelectionHeight > 0 || ch.Status != pb.ChallengeStatus_CHALLENGE_FUNDING {
			return fail("challenge funding already closed", nil)
		}
	} else {
		quote, e := quoteChallengeStakeFromState(sv, h.witnessSvc, req)
		if e != nil {
			return fail(e.Error(), e)
		}
		ch = &pb.ChallengeRecord{ChallengeId: caseID, RequestId: req.RequestId, ChallengerAddress: tx.Base.FromAddress,
			StakeAmount: "0", RequiredStake: quote.RequiredChallengeStake, CreateHeight: tx.Base.ExecutedHeight,
			PublicityDeadlineHeight: req.DeadlineHeight, Status: pb.ChallengeStatus_CHALLENGE_FUNDING,
			Reason: tx.Reason, Evidence: tx.Evidence}
	}
	cfg := witness.DefaultConfig()
	if h.witnessSvc != nil {
		cfg = h.witnessSvc.ConfigSnapshot()
	}
	if e := witness.PrepareArbitrationRecord(ch, req, cfg); e != nil {
		return fail(e.Error(), e)
	}
	prior, e := ParseBalance(ch.StakeAmount)
	if e != nil {
		return fail(e.Error(), e)
	}
	total, e := SafeAdd(prior, amount)
	if e != nil {
		return fail(e.Error(), e)
	}
	required, e := ParseBalance(ch.RequiredStake)
	if e != nil || required.Sign() <= 0 {
		return fail("invalid challenge threshold snapshot", e)
	}
	if e := witnessSettlementMove(sv, tx.Base.FromAddress, amount, true, "challenge_principal_escrow", tx.Base.ExecutedHeight, caseID); e != nil {
		return fail(e.Error(), e)
	}

	ch.StakeAmount = total.String()
	if total.Cmp(required) >= 0 {
		ch.Status = pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION
		ch.SelectionHeight = addRechargeHeightSaturating(tx.Base.ExecutedHeight, cfg.SelectionBeaconDepth)
		req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGED
	}
	req.ChallengeId = caseID
	data, e := proto.Marshal(canonicalChallengeContribution(tx))
	if e != nil {
		return fail(e.Error(), e)
	}
	sv.Set(contributionKey, data)
	if e := syncChallengeContributionRentClaim(sv, tx, caseID, amount); e != nil {
		return fail(e.Error(), e)
	}
	if e := setChallengeRecordWithTimeoutIndexes(sv, ch, cfg.BeaconMixBlocks, 0, 0); e != nil {
		return fail(e.Error(), e)
	}
	if e := setRechargeRequestWithIndexes(sv, req, cfg.BeaconMixBlocks); e != nil {
		return fail(e.Error(), e)
	}
	return nil, &Receipt{TxID: tx.Base.TxId, Status: "SUCCEED"}, nil
}

func refundUnfundedRechargeChallenge(sv StateView, req *pb.RechargeRequest, height, mix uint64) error {
	ch, active, err := rechargeActivatedChallengeInState(sv, req)
	if err != nil {
		return err
	}
	if ch == nil || ch.Finalized || active {
		return nil
	}
	if height <= req.DeadlineHeight {
		return fmt.Errorf("challenge publicity has not expired")
	}
	if err := refundPendingChallengePrincipal(sv, ch, height); err != nil {
		return err
	}
	return setChallengeRecordWithTimeoutIndexes(sv, ch, mix, 0, 0)
}
