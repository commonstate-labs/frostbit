package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/witness"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
)

func rechargeSelectionReadyHeight(req *pb.RechargeRequest, mix uint64) uint64 {
	if req == nil || req.SelectionHeight == 0 {
		return 0
	}
	if mix == 0 {
		mix = 1
	}
	return req.SelectionHeight + mix - 1
}

func rechargeSelectionDueKeyForRequest(req *pb.RechargeRequest, mix uint64) string {
	if req == nil || req.RequestId == "" || req.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
		return ""
	}
	readyHeight := rechargeSelectionReadyHeight(req, mix)
	if readyHeight == 0 {
		return ""
	}
	return keys.KeyRechargeSelectionDue(readyHeight, req.RequestId)
}

func rechargeVoteGraceDueKeyForRequest(req *pb.RechargeRequest) string {
	if req == nil || req.RequestId == "" || req.VoteAcceptUntilHeight == 0 {
		return ""
	}
	if req.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS &&
		req.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL {
		return ""
	}
	return keys.KeyRechargeVoteGraceDue(req.VoteAcceptUntilHeight, req.RequestId)
}

func rechargeChallengeFinalizeHeight(req *pb.RechargeRequest) uint64 {
	if req == nil || req.DeadlineHeight == ^uint64(0) {
		return 0
	}
	return req.DeadlineHeight + 1
}

func rechargeChallengeDueKeyForRequest(req *pb.RechargeRequest) string {
	if req == nil || req.RequestId == "" || req.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		return ""
	}
	finalizeHeight := rechargeChallengeFinalizeHeight(req)
	if finalizeHeight == 0 {
		return ""
	}
	return keys.KeyRechargeChallengeDue(finalizeHeight, req.RequestId)
}

func rechargeQueryOrderHeight(req *pb.RechargeRequest) uint64 {
	if req == nil {
		return 0
	}
	                                                                                         
	                                                                                             
	                                                                                  
	if rechargeRequestTerminal(req.Status) && req.FinalizeHeight != 0 {
		return req.FinalizeHeight
	}
	return req.CreateHeight
}

func rechargeQueryKeysForRequest(req *pb.RechargeRequest) []string {
	if req == nil || req.RequestId == "" {
		return nil
	}
	terminal := rechargeRequestTerminal(req.Status)
	height := rechargeQueryOrderHeight(req)
	return []string{
		keys.KeyRechargeQueryRecentGlobal(terminal, height, req.RequestId),
		keys.KeyRechargeQueryRecentChain(req.NativeChain, terminal, height, req.RequestId),
		keys.KeyRechargeQueryRecentVault(req.VaultId, terminal, height, req.RequestId),
		keys.KeyRechargeQueryRecentChainVault(req.NativeChain, req.VaultId, terminal, height, req.RequestId),
	}
}

func challengeSelectionReadyHeight(ch *pb.ChallengeRecord, mix uint64) uint64 {
	if ch == nil || ch.SelectionHeight == 0 {
		return 0
	}
	if mix == 0 {
		mix = 1
	}
	return ch.SelectionHeight + mix - 1
}

func challengeSelectionDueKeyForRecord(ch *pb.ChallengeRecord, mix uint64) string {
	if ch == nil || ch.ChallengeId == "" || ch.Finalized || ch.Status != pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION {
		return ""
	}
	readyHeight := challengeSelectionReadyHeight(ch, mix)
	if readyHeight == 0 {
		return ""
	}
	return keys.KeyChallengeSelectionDue(readyHeight, ch.ChallengeId)
}

func challengeActiveTimeoutHeight(ch *pb.ChallengeRecord) uint64 {
	if ch == nil || ch.Finalized || ch.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || ch.DeadlineHeight == ^uint64(0) {
		return 0
	}
	return ch.DeadlineHeight + 1
}

func challengeTimeoutDueHeight(ch *pb.ChallengeRecord, maxShelveWait, minHeight uint64) uint64 {
	if ch == nil || ch.ChallengeId == "" || ch.Finalized {
		return 0
	}
	var dueHeight uint64
	switch ch.Status {
	case pb.ChallengeStatus_CHALLENGE_ACTIVE:
		dueHeight = challengeActiveTimeoutHeight(ch)
	}
	if dueHeight != 0 && dueHeight < minHeight {
		return minHeight
	}
	return dueHeight
}

func challengeTimeoutDueKeyForRecord(ch *pb.ChallengeRecord, maxShelveWait, minHeight uint64) string {
	dueHeight := challengeTimeoutDueHeight(ch, maxShelveWait, minHeight)
	if dueHeight == 0 {
		return ""
	}
	return keys.KeyChallengeTimeoutDue(dueHeight, ch.ChallengeId)
}

func rechargeRequestIDFromStateKey(key string) (string, bool) {
	prefix := keys.KeyRechargeRequestPrefix()
	if !strings.HasPrefix(key, prefix) || len(key) == len(prefix) {
		return "", false
	}
	return strings.TrimPrefix(key, prefix), true
}

func challengeIDFromStateKey(key string) (string, bool) {
	prefix := keys.KeyChallengeRecordPrefix()
	if !strings.HasPrefix(key, prefix) || len(key) == len(prefix) {
		return "", false
	}
	return strings.TrimPrefix(key, prefix), true
}

func rechargeRequestFromData(requestID string, data []byte) (*pb.RechargeRequest, bool) {
	if len(data) == 0 {
		return nil, false
	}
	var req pb.RechargeRequest
	if err := unmarshalProtoCompat(data, &req); err != nil {
		return nil, false
	}
	if req.RequestId == "" {
		req.RequestId = requestID
	}
	if req.RequestId == "" {
		return nil, false
	}
	return &req, true
}

func challengeRecordFromData(challengeID string, data []byte) (*pb.ChallengeRecord, bool) {
	if len(data) == 0 {
		return nil, false
	}
	var ch pb.ChallengeRecord
	if err := unmarshalProtoCompat(data, &ch); err != nil {
		return nil, false
	}
	if ch.ChallengeId == "" {
		ch.ChallengeId = challengeID
	}
	if ch.ChallengeId == "" {
		return nil, false
	}
	return &ch, true
}

func loadRechargeRequestAtKey(sv StateView, requestKey string) *pb.RechargeRequest {
	requestID, ok := rechargeRequestIDFromStateKey(requestKey)
	if !ok {
		return nil
	}
	data, exists, err := sv.Get(requestKey)
	if err != nil || !exists {
		return nil
	}
	req, ok := rechargeRequestFromData(requestID, data)
	if !ok {
		return nil
	}
	return req
}

func loadChallengeRecordAtKey(sv StateView, challengeKey string) *pb.ChallengeRecord {
	challengeID, ok := challengeIDFromStateKey(challengeKey)
	if !ok {
		return nil
	}
	data, exists, err := sv.Get(challengeKey)
	if err != nil || !exists {
		return nil
	}
	ch, ok := challengeRecordFromData(challengeID, data)
	if !ok {
		return nil
	}
	return ch
}

func syncRechargeRequestIndexes(sv StateView, oldReq, newReq *pb.RechargeRequest, mix uint64) {
	oldAssignments := witness.RequestWitnessAssignmentKeys(oldReq)
	newAssignments := witness.RequestWitnessAssignmentKeys(newReq)
	oldAssignmentSet, newAssignmentSet := make(map[string]bool), make(map[string]bool)
	for _, key := range oldAssignments {
		oldAssignmentSet[key] = true
	}
	for _, key := range newAssignments {
		newAssignmentSet[key] = true
	}
	for _, key := range oldAssignments {
		if !newAssignmentSet[key] {
			sv.Del(key)
		}
	}
	for _, key := range newAssignments {
		if !oldAssignmentSet[key] {
			sv.Set(key, []byte(newReq.RequestId))
		}
	}
	oldQueryKeys := rechargeQueryKeysForRequest(oldReq)
	newQueryKeys := rechargeQueryKeysForRequest(newReq)
	newQuerySet := make(map[string]struct{}, len(newQueryKeys))
	for _, queryKey := range newQueryKeys {
		newQuerySet[queryKey] = struct{}{}
	}
	oldQuerySet := make(map[string]struct{}, len(oldQueryKeys))
	for _, queryKey := range oldQueryKeys {
		oldQuerySet[queryKey] = struct{}{}
		if _, unchanged := newQuerySet[queryKey]; !unchanged {
			sv.Del(queryKey)
		}
	}
	if oldReq != nil && oldReq.RequestId != "" {
		if oldDueKey := rechargeSelectionDueKeyForRequest(oldReq, mix); oldDueKey != "" {
			sv.Del(oldDueKey)
		}
		if oldDueKey := rechargeVoteGraceDueKeyForRequest(oldReq); oldDueKey != "" {
			sv.Del(oldDueKey)
		}
		if oldDueKey := rechargeChallengeDueKeyForRequest(oldReq); oldDueKey != "" {
			sv.Del(oldDueKey)
		}
	}
	if newReq == nil || newReq.RequestId == "" {
		return
	}
	canonicalKey := []byte(keys.KeyRechargeRequest(newReq.RequestId))
	for _, queryKey := range newQueryKeys {
		if _, unchanged := oldQuerySet[queryKey]; !unchanged {
			sv.Set(queryKey, canonicalKey)
		}
	}
	if newDueKey := rechargeSelectionDueKeyForRequest(newReq, mix); newDueKey != "" {
		sv.Set(newDueKey, []byte(newReq.RequestId))
	}
	if newDueKey := rechargeVoteGraceDueKeyForRequest(newReq); newDueKey != "" {
		sv.Set(newDueKey, []byte(newReq.RequestId))
	}
	if newDueKey := rechargeChallengeDueKeyForRequest(newReq); newDueKey != "" {
		sv.Set(newDueKey, []byte(newReq.RequestId))
	}
}

func syncChallengeRecordIndexes(sv StateView, oldCh, newCh *pb.ChallengeRecord, mix, maxShelveWait, minTimeoutHeight uint64) {
	if oldCh != nil && oldCh.ChallengeId != "" {
		for _, arbitrator := range oldCh.Arbitrators {
			if arbitrator != "" {
				sv.Del(keys.KeyArbitratorChallenge(arbitrator, oldCh.ChallengeId))
			}
		}
		if oldDueKey := challengeSelectionDueKeyForRecord(oldCh, mix); oldDueKey != "" {
			sv.Del(oldDueKey)
		}
		if oldDueKey := challengeTimeoutDueKeyForRecord(oldCh, maxShelveWait, 0); oldDueKey != "" {
			sv.Del(oldDueKey)
		}
		if minTimeoutHeight != 0 {
			if oldDueKey := challengeTimeoutDueKeyForRecord(oldCh, maxShelveWait, minTimeoutHeight); oldDueKey != "" {
				sv.Del(oldDueKey)
			}
		}
	}
	if newCh == nil || newCh.ChallengeId == "" {
		return
	}
	if !newCh.Finalized && newCh.Status == pb.ChallengeStatus_CHALLENGE_ACTIVE {
		voted := make(map[string]struct{}, len(newCh.ArbitrationVotes))
		for _, vote := range newCh.ArbitrationVotes {
			if vote != nil && vote.WitnessAddress != "" {
				voted[vote.WitnessAddress] = struct{}{}
			}
		}
		for _, arbitrator := range newCh.Arbitrators {
			if _, alreadyVoted := voted[arbitrator]; arbitrator != "" && !alreadyVoted {
				sv.Set(keys.KeyArbitratorChallenge(arbitrator, newCh.ChallengeId), []byte(newCh.ChallengeId))
			}
		}
	}
	if newDueKey := challengeSelectionDueKeyForRecord(newCh, mix); newDueKey != "" {
		sv.Set(newDueKey, []byte(newCh.ChallengeId))
	}
	if newDueKey := challengeTimeoutDueKeyForRecord(newCh, maxShelveWait, minTimeoutHeight); newDueKey != "" {
		sv.Set(newDueKey, []byte(newCh.ChallengeId))
	}
}

func setRechargeRequestWithIndexes(sv StateView, req *pb.RechargeRequest, mix uint64) error {
	if req == nil || req.RequestId == "" {
		return nil
	}
	rejectionSnapshot := -1
	if req.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED {
		rejectionSnapshot = sv.Snapshot()
	}
	abort := func(err error) error {
		if err != nil && rejectionSnapshot >= 0 {
			_ = sv.Revert(rejectionSnapshot)
		}
		return err
	}
	requestKey := keys.KeyRechargeRequest(req.RequestId)
	oldReq := loadRechargeRequestAtKey(sv, requestKey)
	if oldReq != nil && rechargeRequestTerminal(oldReq.Status) {
		if err := validateRechargeEconomicsPolicy(sv, oldReq); err != nil {
			return abort(err)
		}
		if oldReq.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED {
			if _, credited, err := sv.Get(keys.KeyRechargeCredited(req.RequestId)); err != nil {
				return abort(err)
			} else if !credited {
				return abort(fmt.Errorf("finalized recharge lacks credited marker: %s", req.RequestId))
			}
		}
		return nil
	}
	if oldReq != nil {
		req = proto.Clone(req).(*pb.RechargeRequest)
		if err := inheritRechargeEconomicsPolicy(req, oldReq); err != nil {
			return abort(err)
		}
		inheritRequesterBond(req, oldReq)
	}
	if req.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED {
		if _, credited, err := sv.Get(keys.KeyRechargeCredited(req.RequestId)); err != nil {
			return err
		} else if !credited {
			return fmt.Errorf("refusing to persist uncredited finalized recharge: request=%s chain=%s", req.RequestId, req.NativeChain)
		} else if oldReq == nil || oldReq.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED {
			return fmt.Errorf("refusing to use credited marker to finalize non-finalized recharge: request=%s chain=%s", req.RequestId, req.NativeChain)
		}
	}
	if req.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED && req.ArbitrationProtocolVersion != 0 && !req.FeeSettled {
		req = proto.Clone(req).(*pb.RechargeRequest)
		if err := settleWitnessRequestFeesAndCollateral(sv, req, pb.WitnessVoteType_VOTE_FAIL); err != nil {
			return abort(err)
		}
	}
	if req.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED {
		if oldReq == nil {
			return abort(fmt.Errorf("rejected recharge lacks canonical request: %s", req.RequestId))
		}
		if err := validateRechargeEconomicsPolicy(sv, oldReq); err != nil {
			return abort(err)
		}
		if err := settleRequesterBond(sv, req, false); err != nil {
			return abort(err)
		}
	}
	hotReq := req
	if rechargeRequestTerminal(req.Status) {
		if err := cleanupTerminalRechargeHotState(sv, req); err != nil {
			return abort(err)
		}
		hotReq = compactRechargeRequestForHotState(req)
	}
	data, err := proto.Marshal(hotReq)
	if err != nil {
		return abort(err)
	}
	sv.Set(requestKey, data)
	syncRechargeRequestIndexes(sv, oldReq, hotReq, mix)
	if req.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED {
		                                                                             
		                                                                           
		if err := settleRuntimeFeeEscrow(sv, req.RequestId, req.FinalizeHeight, false); err != nil {
			return abort(err)
		}
		releaseRechargeNativeLock(sv, req)
	}
	return nil
}

func setChallengeRecordWithIndexes(sv StateView, ch *pb.ChallengeRecord, mix uint64) error {
	return setChallengeRecordWithTimeoutIndexes(sv, ch, mix, 0, 0)
}

func setChallengeRecordWithTimeoutIndexes(sv StateView, ch *pb.ChallengeRecord, mix, maxShelveWait, minTimeoutHeight uint64) error {
	if ch == nil || ch.ChallengeId == "" {
		return nil
	}
	challengeKey := keys.KeyChallengeRecord(ch.ChallengeId)
	oldCh := loadChallengeRecordAtKey(sv, challengeKey)
	hotCh := ch
	if challengeRecordTerminal(ch.Status, ch.Finalized) {
		if len(ch.OrdinaryCommitteeSizes) > 0 && !ch.Settled {
			return fmt.Errorf("cannot compact an unsettled arbitration")
		}
		if err := cleanupTerminalChallengeHotState(sv, ch); err != nil {
			return err
		}
		hotCh = compactChallengeRecordForHotState(ch)
	}
	data, err := proto.Marshal(hotCh)
	if err != nil {
		return err
	}
	sv.Set(challengeKey, data)
	syncChallengeRecordIndexes(sv, oldCh, hotCh, mix, maxShelveWait, minTimeoutHeight)
	return nil
}

func applyWriteOpWithRechargeIndexes(sv StateView, op WriteOp, mix, maxShelveWait, minTimeoutHeight uint64) error {
	if op.Key == "" {
		return nil
	}
	if requestID, ok := rechargeRequestIDFromStateKey(op.Key); ok {
		oldReq := loadRechargeRequestAtKey(sv, op.Key)
		if op.Del {
			sv.Del(op.Key)
			syncRechargeRequestIndexes(sv, oldReq, nil, mix)
			return nil
		}
		if newReq, ok := rechargeRequestFromData(requestID, op.Value); ok {
			if newReq.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED {
				return applyRechargeFinalized(sv, newReq, minTimeoutHeight, mix)
			}
			return setRechargeRequestWithIndexes(sv, newReq, mix)
		}
		sv.Set(op.Key, op.Value)
		return nil
	}
	if challengeID, ok := challengeIDFromStateKey(op.Key); ok {
		oldCh := loadChallengeRecordAtKey(sv, op.Key)
		if op.Del {
			sv.Del(op.Key)
			syncChallengeRecordIndexes(sv, oldCh, nil, mix, maxShelveWait, minTimeoutHeight)
			return nil
		}
		if newCh, ok := challengeRecordFromData(challengeID, op.Value); ok {
			return setChallengeRecordWithTimeoutIndexes(sv, newCh, mix, maxShelveWait, minTimeoutHeight)
		}
		sv.Set(op.Key, op.Value)
		return nil
	}
	if op.Del {
		sv.Del(op.Key)
		return nil
	}
	sv.Set(op.Key, op.Value)
	return nil
}
