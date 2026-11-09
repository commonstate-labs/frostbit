package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
)

func archiveWriteOp(key string, value []byte, category string) WriteOp {
	return WriteOp{
		Key:      key,
		Value:    append([]byte(nil), value...),
		Category: category,
	}
}

func deleteWriteOp(key string, category string) WriteOp {
	return WriteOp{
		Key:      key,
		Del:      true,
		Category: category,
	}
}

func compactRechargeRequestForHotState(req *pb.RechargeRequest) *pb.RechargeRequest {
	if req == nil {
		return nil
	}
	return &pb.RechargeRequest{
		RequestId:                  req.RequestId,
		NativeChain:                req.NativeChain,
		NativeTxHash:               req.NativeTxHash,
		TokenAddress:               req.TokenAddress,
		Amount:                     req.Amount,
		ReceiverAddress:            req.ReceiverAddress,
		RequesterAddress:           req.RequesterAddress,
		Status:                     req.Status,
		CreateHeight:               req.CreateHeight,
		DeadlineHeight:             req.DeadlineHeight,
		FinalizeHeight:             req.FinalizeHeight,
		Round:                      req.Round,
		PassCount:                  req.PassCount,
		FailCount:                  req.FailCount,
		AbstainCount:               req.AbstainCount,
		ChallengeId:                req.ChallengeId,
		RechargeFee:                req.RechargeFee,
		VaultId:                    req.VaultId,
		NativeVout:                 req.NativeVout,
		NativeContract:             req.NativeContract,
		DepositEventIndex:          req.DepositEventIndex,
		DepositProtocol:            req.DepositProtocol,
		BtcProtocolVersion:         req.BtcProtocolVersion,
		ShelveHeight:               req.ShelveHeight,
		SelectionHeight:            req.SelectionHeight,
		DecisionHeight:             req.DecisionHeight,
		VoteAcceptUntilHeight:      req.VoteAcceptUntilHeight,
		FeeEscrow:                  req.FeeEscrow,
		FeeSettled:                 req.FeeSettled,
		ArbitrationProtocolVersion: req.ArbitrationProtocolVersion,
		RequesterBondAmount:        req.RequesterBondAmount,
		RequesterBondStatus:        req.RequesterBondStatus,
		EconomicsPolicy:            req.EconomicsPolicy,
		GrossAmount:                req.GrossAmount,
		ReceiverNetAmount:          req.ReceiverNetAmount,
		RequesterRewardAmount:      req.RequesterRewardAmount,
		WitnessRewardAmount:        req.WitnessRewardAmount,
	}
}

func rechargeRequestTerminal(status pb.RechargeRequestStatus) bool {
	return status == pb.RechargeRequestStatus_RECHARGE_FINALIZED || status == pb.RechargeRequestStatus_RECHARGE_REJECTED
}

func rechargeRequestAcceptsWitnessVote(req *pb.RechargeRequest, height uint64) bool {
	if req == nil {
		return false
	}
	switch req.Status {
	case pb.RechargeRequestStatus_RECHARGE_VOTING:
		return req.DeadlineHeight == 0 || height <= req.DeadlineHeight
	case pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS,
		pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL:
		return req.VoteAcceptUntilHeight > 0 && height <= req.VoteAcceptUntilHeight
	default:
		return false
	}
}

func challengeRecordTerminal(status pb.ChallengeStatus, finalized bool) bool {
	return finalized || status == pb.ChallengeStatus_CHALLENGE_FINALIZED
}

func archiveTerminalRechargeRequest(sv StateView, req *pb.RechargeRequest) error {
	if sv == nil || req == nil || req.RequestId == "" || !rechargeRequestTerminal(req.Status) {
		return nil
	}
	archiveKey := keys.KeyArchiveRechargeRequest(req.RequestId)
	if _, exists, err := sv.Get(archiveKey); err != nil {
		return err
	} else if exists {
		return nil
	}
	data, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	sv.Set(archiveKey, data)
	return nil
}

func cleanupTerminalWitnessAssignments(sv StateView, req *pb.RechargeRequest, storedVoters []string) {
	if sv == nil || req == nil || req.RequestId == "" || !rechargeRequestTerminal(req.Status) {
		return
	}
	seen := make(map[string]struct{}, len(req.SelectedWitnesses)+len(req.Votes))
	for _, addr := range req.SelectedWitnesses {
		if addr != "" {
			seen[addr] = struct{}{}
		}
	}
	for _, vote := range req.Votes {
		if vote != nil && vote.WitnessAddress != "" {
			seen[vote.WitnessAddress] = struct{}{}
		}
	}
	for _, vote := range req.OriginalPositions {
		if vote != nil && vote.WitnessAddress != "" {
			seen[vote.WitnessAddress] = struct{}{}
		}
	}
	for _, entry := range req.CaseCollateral {
		if entry != nil && entry.Address != "" {
			seen[entry.Address] = struct{}{}
		}
	}
	for _, address := range storedVoters {
		seen[address] = struct{}{}
	}
	addresses := make([]string, 0, len(seen))
	for addr := range seen {
		addresses = append(addresses, addr)
	}
	sort.Strings(addresses)
	for _, addr := range addresses {
		sv.Del(keys.KeyWitnessAssigned(addr, req.RequestId))
		sv.Del(keys.KeyWitnessAssignedCurrent(addr, req.RequestId))
		sv.Del(keys.KeyLegacyWitnessAssigned(addr, req.RequestId))
	}
}

func archiveAndDeleteTerminalWitnessVotes(sv StateView, req *pb.RechargeRequest) ([]string, error) {
	if sv == nil || req == nil || req.RequestId == "" {
		return nil, nil
	}
	                                                                          
	                                                                             
	prefix := keys.KeyWitnessVotePrefix(req.RequestId)
	rows, err := sv.Scan(prefix)
	if err != nil {
		return nil, err
	}
	byAddress := make(map[string][]byte, len(rows)+len(req.Votes))
	for key, raw := range rows {
		var vote pb.WitnessVote
		if err := proto.Unmarshal(raw, &vote); err != nil {
			return nil, fmt.Errorf("decode terminal witness vote %s: %w", key, err)
		}
		                                                                       
		                                                          
		if vote.RequestId != "" && vote.RequestId != req.RequestId {
			continue
		}
		address := vote.WitnessAddress
		if address == "" {
			address = strings.TrimPrefix(key, prefix)
		}
		if address == "" || key != keys.KeyWitnessVote(req.RequestId, address) {
			continue
		}
		byAddress[address] = raw
	}
	for _, vote := range req.Votes {
		if vote == nil || vote.WitnessAddress == "" {
			continue
		}
		if _, exists := byAddress[vote.WitnessAddress]; exists {
			continue
		}
		fullData, err := proto.Marshal(vote)
		if err != nil {
			return nil, err
		}
		byAddress[vote.WitnessAddress] = fullData
	}
	addresses := make([]string, 0, len(byAddress))
	for address := range byAddress {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		sv.Set(keys.KeyArchiveWitnessVote(req.RequestId, address), byAddress[address])
		sv.Del(keys.KeyWitnessVote(req.RequestId, address))
	}
	return addresses, nil
}

func cleanupTerminalRechargeHotState(sv StateView, req *pb.RechargeRequest) (resultErr error) {
	if sv == nil || req == nil || !rechargeRequestTerminal(req.Status) {
		return nil
	}
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil {
			_ = sv.Revert(snap)
		}
	}()
	if err := archiveTerminalRechargeRequest(sv, req); err != nil {
		return err
	}
	voters, err := archiveAndDeleteTerminalWitnessVotes(sv, req)
	if err != nil {
		return err
	}
	cleanupTerminalWitnessAssignments(sv, req, voters)
	return nil
}

func compactChallengeRecordForHotState(ch *pb.ChallengeRecord) *pb.ChallengeRecord {
	if ch == nil {
		return nil
	}
	return &pb.ChallengeRecord{
		ChallengeId:             ch.ChallengeId,
		RequestId:               ch.RequestId,
		ChallengerAddress:       ch.ChallengerAddress,
		StakeAmount:             "0",
		CreateHeight:            ch.CreateHeight,
		DeadlineHeight:          ch.DeadlineHeight,
		PassCount:               ch.PassCount,
		FailCount:               ch.FailCount,
		ChallengeSuccess:        ch.ChallengeSuccess,
		Finalized:               ch.Finalized,
		Status:                  ch.Status,
		Settled:                 ch.Settled,
		FinalVote:               ch.FinalVote,
		FinalizeHeight:          ch.FinalizeHeight,
		BallotRound:             ch.BallotRound,
		ArbitrationStage:        ch.ArbitrationStage,
		ArbitrationPeriodBlocks: ch.ArbitrationPeriodBlocks,
	}
}

func cleanupTerminalChallengeHotState(sv StateView, ch *pb.ChallengeRecord) error {
	if sv == nil || ch == nil || ch.ChallengeId == "" || !challengeRecordTerminal(ch.Status, ch.Finalized) {
		return nil
	}
	archiveKey := keys.KeyArchiveChallengeRecord(ch.ChallengeId)
	if _, exists, err := sv.Get(archiveKey); err != nil {
		return err
	} else if !exists {
		data, err := proto.Marshal(ch)
		if err != nil {
			return err
		}
		sv.Set(archiveKey, data)
	}

	                                                                           
	                                                                        
	                                                                            
	                            
	votes, err := sv.Scan(keys.KeyArbitrationVotePrefix(ch.ChallengeId))
	if err != nil {
		return err
	}
	voteKeys := make([]string, 0, len(votes))
	for key := range votes {
		voteKeys = append(voteKeys, key)
	}
	sort.Strings(voteKeys)
	for _, key := range voteKeys {
		sv.Del(key)
	}
	return nil
}
