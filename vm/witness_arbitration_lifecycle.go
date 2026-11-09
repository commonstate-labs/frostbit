package vm

import (
	"crypto/sha256"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/witness"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"

	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                              
                                                                                   
func requireWitnessArbitrationVersion(req *pb.RechargeRequest) error {
	if req == nil || req.ArbitrationProtocolVersion != protocol.ArbitrationProtocolVersion {
		return fmt.Errorf("active legacy witness request requires explicit arbitration migration before using the new protocol")
	}
	return nil
}

func witnessCaseInfos(sv StateView) ([]*pb.WitnessInfo, error) {
	rows, err := sv.Scan(keys.KeyWitnessInfoPrefix())
	if err != nil {
		return nil, err
	}
	result := make([]*pb.WitnessInfo, 0, len(rows))
	for _, key := range sortedStateKeys(rows) {
		var info pb.WitnessInfo
		if err := unmarshalProtoCompat(rows[key], &info); err != nil {
			return nil, err
		}
		result = append(result, &info)
	}
	return result, nil
}

func sortedStateKeys(rows map[string][]byte) []string {
	result := make([]string, 0, len(rows))
	for key := range rows {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func selectWitnessCaseMembers(sv StateView, req *pb.RechargeRequest, infos []*pb.WitnessInfo, role uint32, count uint32, beacon []byte, round uint64, minimum string) ([]string, error) {
	type ranked struct {
		address string
		score   *big.Int
	}
	all := make([]ranked, 0, len(infos))
	for _, info := range infos {
		if info == nil {
			continue
		}
		allowed, err := canReserveWitnessCaseAssignment(sv, req, WitnessCaseAssignment{Address: info.Address, Role: role, Amount: strconv.FormatInt(protocol.WitnessCaseBondRaw, 10)}, minimum)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		stake, err := parseWitnessCollateralAmount("registered witness stake", info.StakeAmount)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		h.Write(beacon)
		h.Write([]byte("\x00" + req.RequestId + "\x00" + strconv.FormatUint(round, 10) + "\x00" + info.Address))
		score := new(big.Int).SetBytes(h.Sum(nil))
		score.Mul(score, stake)
		all = append(all, ranked{info.Address, score})
	}
	sort.Slice(all, func(i, j int) bool {
		if cmp := all[i].score.Cmp(all[j].score); cmp != 0 {
			return cmp > 0
		}
		return all[i].address < all[j].address
	})
	if uint64(count) > uint64(len(all)) {
		count = uint32(len(all))
	}
	addresses := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		addresses = append(addresses, all[i].address)
	}
	return addresses, nil
}

func (x *Executor) selectOriginalWitnessesFromState(sv StateView, in *pb.RechargeRequest, beacon []byte, height uint64) (*pb.RechargeRequest, error) {
	if err := requireWitnessArbitrationVersion(in); err != nil {
		return nil, err
	}
	cfg := x.WitnessService.ConfigSnapshot()
	infos, err := witnessCaseInfos(sv)
	if err != nil {
		return nil, err
	}
	count := uint64(cfg.InitialWitnessCount)
	for i := uint32(0); i < in.Round && count < uint64(len(infos)); i++ {
		mult := uint64(cfg.ExpandMultiplier)
		if mult < 2 {
			mult = 2
		}
		count *= mult
	}
	if count > uint64(len(infos)) {
		count = uint64(len(infos))
	}
	members, err := selectWitnessCaseMembers(sv, in, infos, WitnessCaseRoleOriginal, uint32(count), beacon, uint64(in.Round), cfg.MinStakeAmount)
	if err != nil {
		return nil, err
	}
	if len(members) < 4 || uint32(len(members)) < x.WitnessService.MinSelectedWitnessCount() {
		return nil, witness.ErrInsufficientSelectedWitnesses
	}
	req := proto.Clone(in).(*pb.RechargeRequest)
	assignments := make([]WitnessCaseAssignment, 0, len(members))
	for _, member := range members {
		assignments = append(assignments, WitnessCaseAssignment{Address: member, Role: WitnessCaseRoleOriginal, Amount: strconv.FormatInt(protocol.WitnessCaseBondRaw, 10)})
	}
	if err := reserveWitnessCaseCollateral(sv, req, assignments, cfg.MinStakeAmount); err != nil {
		return nil, err
	}
	req.Status = pb.RechargeRequestStatus_RECHARGE_VOTING
	req.SelectedWitnesses = members
	req.DeadlineHeight = addRechargeHeightSaturating(height, cfg.VotingPeriodBlocks)
	req.DecisionHeight = 0
	req.VoteAcceptUntilHeight = 0
	req.ShelveHeight = 0
	req.SelectionBeacon = append([]byte(nil), beacon...)
	return req, nil
}

func (x *Executor) startArbitrationRoundFromState(sv StateView, in *pb.ChallengeRecord, req *pb.RechargeRequest, height uint64) (*pb.ChallengeRecord, error) {
	if err := requireWitnessArbitrationVersion(req); err != nil {
		return nil, err
	}
	cfg := x.WitnessService.ConfigSnapshot()
	ch := proto.Clone(in).(*pb.ChallengeRecord)
	if err := witness.PrepareArbitrationRecord(ch, req, cfg); err != nil {
		return nil, err
	}
	beacon := append([]byte(nil), ch.SelectionBeacon...)
	if len(beacon) == 0 {
		beacon = deriveForwardBeacon(sv, ch.SelectionHeight, x.WitnessService.BeaconMixBlocks(), req.NativeChain, req.NativeTxHash+"|"+ch.ChallengeId)
	}
	var members, court []string
	assignments := []WitnessCaseAssignment{}
	if ch.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_FINAL_COURT {
		                                                                         
		members = append([]string(nil), ch.FinalCourt...)
	} else {
		infos, err := witnessCaseInfos(sv)
		if err != nil {
			return nil, err
		}
		if ch.ArbitrationRound == 3 && len(ch.FinalCourt) == 0 {
			court, err = witness.SelectFinalCourt(infos, cfg.MinStakeAmount)
			if err != nil {
				return nil, err
			}
			for _, address := range court {
				assignments = append(assignments, WitnessCaseAssignment{Address: address, Role: WitnessCaseRoleCourt, Amount: "0"})
			}
		}
		members, err = selectWitnessCaseMembers(sv, req, infos, WitnessCaseRoleOrdinary, ch.OrdinaryCommitteeSizes[ch.ArbitrationRound-1], beacon, ch.BallotRound+1, cfg.MinStakeAmount)
		if err != nil {
			return nil, err
		}
		for _, address := range members {
			assignments = append(assignments, WitnessCaseAssignment{Address: address, Role: WitnessCaseRoleOrdinary, Amount: strconv.FormatInt(protocol.WitnessCaseBondRaw, 10)})
		}
	}
	updated, err := witness.StartArbitrationRound(ch, members, court, height, x.WitnessService.ArbitrationPeriodBlocks())
	if err != nil {
		return nil, err
	}
	updated.SelectionBeacon = beacon
	copyReq := proto.Clone(req).(*pb.RechargeRequest)
	snap := sv.Snapshot()
	committed := false
	defer func() {
		if !committed {
			_ = sv.Revert(snap)
		}
	}()
	if len(assignments) > 0 {
		if err := reserveWitnessCaseCollateral(sv, copyReq, assignments, cfg.MinStakeAmount); err != nil {
			return nil, err
		}
	}
	copyReq.Status = pb.RechargeRequestStatus_RECHARGE_ARBITRATION
	copyReq.ChallengeId = ch.ChallengeId
	if err := setRechargeRequestWithIndexes(sv, copyReq, x.WitnessService.BeaconMixBlocks()); err != nil {
		_ = sv.Revert(snap)
		return nil, err
	}
	if err := setChallengeRecordWithTimeoutIndexes(sv, updated, x.WitnessService.BeaconMixBlocks(), 0, 0); err != nil {
		return nil, err
	}
	proto.Reset(req)
	proto.Merge(req, copyReq)
	committed = true
	return updated, nil
}

func clearCurrentArbitrationVoteKeys(sv StateView, ch *pb.ChallengeRecord) {
	for _, vote := range ch.ArbitrationVotes {
		if vote != nil {
			sv.Del(keys.KeyArbitrationVoteRound(ch.ChallengeId, ch.BallotRound, vote.WitnessAddress))
			sv.Del(keys.KeyArbitrationVote(ch.ChallengeId, vote.WitnessAddress))
		}
	}
}

func (x *Executor) retryArbitrationSelection(sv StateView, ch *pb.ChallengeRecord, height uint64) error {
	delay := x.WitnessService.RetryIntervalBlocks()
	if delay == 0 {
		delay = 1
	}
	ch.Status = pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION
	ch.SelectionHeight = addRechargeHeightSaturating(height, delay)
	                                                                                
	return setChallengeRecordWithTimeoutIndexes(sv, ch, x.WitnessService.BeaconMixBlocks(), 0, 0)
}

func (x *Executor) advanceArbitrationFromState(sv StateView, ch *pb.ChallengeRecord, req *pb.RechargeRequest, height uint64) error {
	if height <= ch.DeadlineHeight {
		return fmt.Errorf("arbitration round has not expired")
	}
	snap := sv.Snapshot()
	committed := false
	defer func() {
		if !committed {
			_ = sv.Revert(snap)
		}
	}()
	next := proto.Clone(ch).(*pb.ChallengeRecord)
	clearCurrentArbitrationVoteKeys(sv, ch)
	if next.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_ORDINARY {
		if next.ArbitrationRound < 3 {
			next.ArbitrationRound++
		} else {
			next.ArbitrationStage = pb.ArbitrationStage_ARBITRATION_FINAL_COURT
			next.OrdinaryVotesFrozen = true
		}
	}
	next.ArbitrationVotes = nil
	next.PassCount = 0
	next.FailCount = 0
	updated, err := x.startArbitrationRoundFromState(sv, next, req, height)
	if err != nil {
		if !errors.Is(err, witness.ErrInsufficientFinalCourt) && !errors.Is(err, errWitnessCaseUnavailable) {
			return err
		}
		if err := x.retryArbitrationSelection(sv, next, height); err != nil {
			return err
		}
	} else if err := setChallengeRecordWithTimeoutIndexes(sv, updated, x.WitnessService.BeaconMixBlocks(), 0, 0); err != nil {
		return err
	}
	committed = true
	return nil
}
