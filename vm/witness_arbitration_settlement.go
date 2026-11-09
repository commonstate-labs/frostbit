package vm

import (
	"fmt"
	"math/big"
	"sort"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/rent"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

const WitnessFeePoolAccount = "__witness_fee_pool__"

                                                                              
                                                                                
func collectWitnessServiceFee(sv StateView, req *pb.RechargeRequest, minFeeActive bool) (err error) {
	if sv == nil || req == nil || req.RequestId == "" || req.RequesterAddress == "" {
		return fmt.Errorf("missing witness fee payer/request")
	}
	fee, err := ParseBalance(req.RechargeFee)
	if err != nil {
		return fmt.Errorf("witness fee: %w", err)
	}
	if data, ok, e := sv.Get(keys.KeyRechargeRequest(req.RequestId)); e != nil {
		return e
	} else if ok {
		var stored pb.RechargeRequest
		if e = unmarshalProtoCompat(data, &stored); e != nil {
			return e
		}
		if stored.ArbitrationProtocolVersion == protocol.ArbitrationProtocolVersion {
			if stored.RequesterAddress != req.RequesterAddress || stored.RechargeFee != req.RechargeFee {
				return fmt.Errorf("witness fee request mismatch")
			}
			req.FeeEscrow, req.FeeSettled, req.ArbitrationProtocolVersion = stored.FeeEscrow, stored.FeeSettled, stored.ArbitrationProtocolVersion
			return nil
		}
	}
	if minFeeActive && fee.Cmp(big.NewInt(protocol.MinWitnessRechargeFeeRaw)) < 0 {
		return fmt.Errorf("witness fee must be at least 0.01 FB (%d base units)", protocol.MinWitnessRechargeFeeRaw)
	}
	snap := sv.Snapshot()
	defer func() {
		if err != nil {
			_ = sv.Revert(snap)
		}
	}()
	if err = witnessSettlementMove(sv, req.RequesterAddress, fee, true, "witness_fee_prepay", req.CreateHeight, req.RequestId); err != nil {
		return err
	}
	if err = witnessSettlementMove(sv, WitnessFeePoolAccount, fee, false, "witness_fee_escrow", req.CreateHeight, req.RequestId); err != nil {
		return err
	}
	updated := proto.Clone(req).(*pb.RechargeRequest)
	updated.FeeEscrow, updated.FeeSettled, updated.ArbitrationProtocolVersion = fee.String(), false, protocol.ArbitrationProtocolVersion
	if err = persistWitnessFinancialRequest(sv, updated); err != nil {
		return err
	}
	proto.Reset(req)
	proto.Merge(req, updated)
	return nil
}

func validateWitnessRechargeMinFee(raw string) error {
	fee, err := ParseBalance(raw)
	if err != nil {
		return fmt.Errorf("witness fee: %w", err)
	}
	if fee.Cmp(big.NewInt(protocol.MinWitnessRechargeFeeRaw)) < 0 {
		return fmt.Errorf("witness fee must be at least 0.01 FB (%d base units)", protocol.MinWitnessRechargeFeeRaw)
	}
	return nil
}

                                                                               
                                                                           
func settleWitnessRequestFeesAndCollateral(sv StateView, req *pb.RechargeRequest, finalVote pb.WitnessVoteType) (err error) {
	if sv == nil || req == nil {
		return fmt.Errorf("missing witness settlement request")
	}
	if req.ArbitrationProtocolVersion != protocol.ArbitrationProtocolVersion {
		if len(req.CaseCollateral) != 0 {
			return fmt.Errorf("legacy request with case collateral requires explicit migration")
		}
		return nil
	}
	stored, err := readWitnessSettlementRequest(sv, req.RequestId)
	if err != nil {
		return err
	}
	if stored.FeeSettled {
		req.FeeEscrow, req.FeeSettled, req.CaseCollateral = stored.FeeEscrow, true, stored.CaseCollateral
		return nil
	}
	if req.ChallengeId != "" {
		if raw, exists, e := sv.Get(keys.KeyChallengeRecord(req.ChallengeId)); e != nil {
			return e
		} else if exists {
			var ch pb.ChallengeRecord
			if e = unmarshalProtoCompat(raw, &ch); e != nil {
				return e
			}
			if !ch.Settled {
				return fmt.Errorf("active challenge requires arbitration settlement")
			}
		}
	}
	updated := proto.Clone(req).(*pb.RechargeRequest)
	                                                            
	updated.FeeEscrow = stored.FeeEscrow
	updated.CaseCollateral = proto.Clone(stored).(*pb.RechargeRequest).CaseCollateral
	fee, err := ParseBalance(updated.FeeEscrow)
	if err != nil {
		return err
	}
	positions := updated.OriginalPositions
	if len(positions) == 0 {
		positions = updated.Votes
	}
	plan, err := witness.BuildArbitrationRewardPlan(witness.ArbitrationRewardInput{FinalVote: finalVote, Original: positions, Fee: fee, Fine: new(big.Int)})
	if err != nil {
		return err
	}
	snap := sv.Snapshot()
	defer func() {
		if err != nil {
			_ = sv.Revert(snap)
		}
	}()
	if err = applyWitnessRewardPlan(sv, updated, fee, plan); err != nil {
		return err
	}
	if err = releaseWitnessCaseCollateral(sv, updated); err != nil {
		return err
	}
	updated.FeeEscrow, updated.FeeSettled = "0", true
	if err = persistWitnessFinancialRequest(sv, updated); err != nil {
		return err
	}
	proto.Reset(req)
	proto.Merge(req, updated)
	return nil
}

                                                                                
                                                                            
func settleWitnessArbitration(sv StateView, req *pb.RechargeRequest, ch *pb.ChallengeRecord, frozenFeeRecipients ...*[]string) (err error) {
	if req == nil || ch == nil || req.RequestId != ch.RequestId || ch.ChallengeId == "" {
		return fmt.Errorf("arbitration settlement identity mismatch")
	}
	if !ch.Finalized || !ch.OrdinaryVotesFrozen {
		return fmt.Errorf("arbitration positions are not frozen")
	}
	if ch.Settled {
		return nil
	}
	stored, err := readWitnessSettlementRequest(sv, req.RequestId)
	if err != nil {
		return err
	}
	if stored.FeeSettled {
		return fmt.Errorf("arbitration request already economically settled")
	}
	if stored.ArbitrationProtocolVersion != protocol.ArbitrationProtocolVersion {
		return fmt.Errorf("arbitration settlement requires migrated collateral and fee escrow")
	}
	rawChallenge, exists, err := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("canonical challenge escrow missing")
	}
	var escrow pb.ChallengeRecord
	if err = unmarshalProtoCompat(rawChallenge, &escrow); err != nil {
		return err
	}
	if escrow.RequestId != req.RequestId || escrow.StakeAmount != ch.StakeAmount || escrow.Settled || escrow.Finalized {
		return fmt.Errorf("canonical challenge escrow does not match unsettled outcome")
	}
	updated, settled := proto.Clone(req).(*pb.RechargeRequest), proto.Clone(ch).(*pb.ChallengeRecord)
	updated.FeeEscrow, updated.CaseCollateral = stored.FeeEscrow, proto.Clone(stored).(*pb.RechargeRequest).CaseCollateral
	fee, err := ParseBalance(updated.FeeEscrow)
	if err != nil {
		return err
	}
	contribs, total, rows, err := readChallengeContributionsStrict(sv, ch.ChallengeId)
	if err != nil {
		return err
	}
	stake, err := ParseBalance(ch.StakeAmount)
	if err != nil {
		return err
	}
	if total.Cmp(stake) != 0 {
		return fmt.Errorf("challenge escrow mismatch: held=%s contributions=%s", stake, total)
	}
	in := witness.ArbitrationRewardInput{FinalVote: ch.FinalVote, Original: ch.OriginalVotes, Ordinary: ch.OrdinaryVotes, Court: ch.FinalCourt, Fee: fee, Fine: new(big.Int), Contributions: contribs, Challenged: true, ChallengeSuccess: ch.ChallengeSuccess}
	if len(in.Original) == 0 {
		in.Original = updated.OriginalPositions
	}
	if ch.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_FINAL_COURT {
		in.CourtVotes = ch.ArbitrationVotes
	}
	eligibility, err := witness.ArbitrationEligibility(in)
	if err != nil {
		return err
	}
	snap := sv.Snapshot()
	defer func() {
		if err != nil {
			_ = sv.Revert(snap)
		}
	}()
	actual, err := slashWitnessCaseCollateral(sv, updated, eligibility.Wrong)
	if err != nil {
		return err
	}
	for _, addr := range sortedBigIntMapKeys(actual) {
		if actual[addr] == nil || actual[addr].Sign() < 0 {
			return fmt.Errorf("invalid actual collateral debit")
		}
		in.Fine.Add(in.Fine, actual[addr])
	}
	plan, err := witness.BuildArbitrationRewardPlan(in)
	if err != nil {
		return err
	}
	if err = applyWitnessRewardPlan(sv, updated, fee, plan); err != nil {
		return err
	}
	if err = clearWitnessSettlementContributions(sv, ch.ChallengeId, rows); err != nil {
		return err
	}
	if err = releaseWitnessCaseCollateral(sv, updated); err != nil {
		return err
	}
	updated.FeeEscrow, updated.FeeSettled = "0", true
	settled.StakeAmount, settled.Settled = "0", true
	if err = persistWitnessFinancialRequest(sv, updated); err != nil {
		return err
	}
	proto.Reset(req)
	proto.Merge(req, updated)
	proto.Reset(ch)
	proto.Merge(ch, settled)
	if len(frozenFeeRecipients) > 0 && frozenFeeRecipients[0] != nil {
		*frozenFeeRecipients[0] = append([]string(nil), eligibility.FeeRecipients...)
	}
	return nil
}

                                                                             
                                                                            
                                                                               
func refundPendingChallengePrincipal(sv StateView, ch *pb.ChallengeRecord, height uint64) (err error) {
	if ch == nil || ch.ChallengeId == "" {
		return fmt.Errorf("missing funding challenge")
	}
	if ch.Settled {
		return nil
	}
	if ch.Status != pb.ChallengeStatus_CHALLENGE_FUNDING {
		return fmt.Errorf("cannot refund an activated challenge as funding expiry")
	}
	contribs, total, rows, err := readChallengeContributionsStrict(sv, ch.ChallengeId)
	if err != nil {
		return err
	}
	stake, err := ParseBalance(ch.StakeAmount)
	if err != nil {
		return err
	}
	if stake.Cmp(total) != 0 {
		return fmt.Errorf("funding escrow mismatch")
	}
	snap := sv.Snapshot()
	defer func() {
		if err != nil {
			_ = sv.Revert(snap)
		}
	}()
	for _, a := range sortedBigIntMapKeys(contribs) {
		if err = witnessSettlementMove(sv, a, contribs[a], false, "witness_challenge_funding_refund", height, ch.ChallengeId); err != nil {
			return err
		}
	}
	if err = clearWitnessSettlementContributions(sv, ch.ChallengeId, rows); err != nil {
		return err
	}
	ch.StakeAmount, ch.Settled, ch.Finalized, ch.Status, ch.FinalizeHeight = "0", true, true, pb.ChallengeStatus_CHALLENGE_FINALIZED, height
	return nil
}

func applyWitnessRewardPlan(sv StateView, req *pb.RechargeRequest, fee *big.Int, plan *witness.ArbitrationRewardPlan) error {
	if err := witnessSettlementMove(sv, WitnessFeePoolAccount, fee, true, "witness_fee_settle", req.FinalizeHeight, req.RequestId); err != nil {
		return err
	}
	groups := []struct {
		reason  string
		amounts map[string]*big.Int
	}{{"witness_service_fee", plan.Fees}, {"witness_challenge_principal_refund", plan.Refunds}, {"witness_challenge_contributor_reward", plan.ContributorRewards}, {"witness_correct_vote_reward", plan.VoterRewards}, {"witness_failed_challenge_award", plan.FailedAwards}}
	for _, group := range groups {
		for _, addr := range sortedBigIntMapKeys(group.amounts) {
			if err := witnessSettlementMove(sv, addr, group.amounts[addr], false, group.reason, req.FinalizeHeight, req.RequestId); err != nil {
				return err
			}
		}
	}
	if plan.Burn.Sign() > 0 {
		_, err := adjustTokenTotalSupplyForAudit(sv, FeeToken, new(big.Int).Neg(plan.Burn))
		return err
	}
	return nil
}

func witnessSettlementMove(sv StateView, addr string, amount *big.Int, debit bool, reason string, height uint64, id string) error {
	if amount == nil || amount.Sign() < 0 {
		return fmt.Errorf("invalid witness transfer")
	}
	if amount.Sign() == 0 {
		return nil
	}
	                                                                          
	                                                                             
	bal, err := getBalanceStrict(sv, addr, FeeToken)
	if err != nil {
		return err
	}
	current, err := ParseBalance(bal.Balance)
	if err != nil {
		return err
	}
	from, to := BucketExternal, BucketAvailable
	if debit {
		if current.Cmp(amount) < 0 {
			return fmt.Errorf("insufficient witness settlement source %s", addr)
		}
		from, to = BucketAvailable, BucketExternal
	} else if _, err = SafeAdd(current, amount); err != nil {
		return err
	}
	return NewLedger(sv).Move(MoveRequest{Account: addr, Token: FeeToken, Amount: amount, From: from, To: to, Reason: reason, Height: height, TxID: id, Ref: id})
}

func readWitnessSettlementRequest(sv StateView, id string) (*pb.RechargeRequest, error) {
	data, exists, err := sv.Get(keys.KeyRechargeRequest(id))
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("canonical witness request missing: %s", id)
	}
	var req pb.RechargeRequest
	if err = unmarshalProtoCompat(data, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

                                                                                
func persistWitnessFinancialRequest(sv StateView, req *pb.RechargeRequest) error {
	updated := proto.Clone(req).(*pb.RechargeRequest)
	if data, ok, err := sv.Get(keys.KeyRechargeRequest(req.RequestId)); err != nil {
		return err
	} else if ok {
		var stored pb.RechargeRequest
		if err = unmarshalProtoCompat(data, &stored); err != nil {
			return err
		}
		if rechargeRequestTerminal(stored.Status) {
			return nil
		}
		updated.Status = stored.Status
		inheritRequesterBond(updated, &stored)
		if err := inheritRechargeEconomicsPolicy(updated, &stored); err != nil {
			return err
		}
	}
	data, err := proto.Marshal(updated)
	if err != nil {
		return err
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), data)
	return nil
}

func readChallengeContributionsStrict(sv StateView, id string) (map[string]*big.Int, *big.Int, map[string][]byte, error) {
	rows, err := sv.Scan(keys.KeyChallengeContributionPrefix(id))
	if err != nil {
		return nil, nil, nil, err
	}
	out, total := map[string]*big.Int{}, new(big.Int)
	for _, key := range sortedSettlementRowKeys(rows) {
		var tx pb.WitnessChallengeTx
		if err = unmarshalProtoCompat(rows[key], &tx); err != nil {
			return nil, nil, nil, err
		}
		if tx.Base == nil || tx.Base.FromAddress == "" || tx.Base.TxId == "" || key != keys.KeyChallengeContribution(id, tx.Base.TxId) {
			return nil, nil, nil, fmt.Errorf("invalid challenge contribution identity")
		}
		amount, e := ParseBalance(tx.StakeAmount)
		if e != nil || amount.Sign() <= 0 {
			return nil, nil, nil, fmt.Errorf("invalid challenge contribution amount %q", tx.StakeAmount)
		}
		if out[tx.Base.FromAddress] == nil {
			out[tx.Base.FromAddress] = new(big.Int)
		}
		out[tx.Base.FromAddress].Add(out[tx.Base.FromAddress], amount)
		total.Add(total, amount)
	}
	return out, total, rows, nil
}

func clearWitnessSettlementContributions(sv StateView, id string, rows map[string][]byte) error {
	for _, key := range sortedSettlementRowKeys(rows) {
		var tx pb.WitnessChallengeTx
		if err := unmarshalProtoCompat(rows[key], &tx); err != nil {
			return err
		}
		if tx.Base == nil {
			return fmt.Errorf("missing contribution identity")
		}
		if err := rent.SetClaim(sv, tx.Base.FromAddress, FeeToken, rentClaimChallengeStake, rentChallengeContributionSourceID(id, tx.Base.TxId), new(big.Int)); err != nil {
			return err
		}
		sv.Set(keys.KeyArchiveChallengeContribution(id, tx.Base.TxId), rows[key])
		sv.Del(key)
	}
	return nil
}

func sortedSettlementRowKeys(rows map[string][]byte) []string {
	out := make([]string, 0, len(rows))
	for key := range rows {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
