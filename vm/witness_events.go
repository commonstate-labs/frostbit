package vm

import (
	"crypto/sha256"
	bridgeasset "dex/frost/asset"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"dex/witness"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

func (x *Executor) applyWitnessFinalizedEvents(sv StateView, fallbackHeight uint64) error {
	if x.WitnessService == nil {
		return nil
	}
	mix := x.WitnessService.BeaconMixBlocks()

	events := make([]*witness.Event, 0, 16)
	for {
		select {
		case evt := <-x.WitnessService.Events():
			if evt == nil {
				continue
			}
			events = append(events, evt)
		default:
			goto apply
		}
	}

apply:
	if len(events) == 0 {
		return nil
	}
	sort.SliceStable(events, func(i, j int) bool {
		return witnessEventSortKey(events[i]) < witnessEventSortKey(events[j])
	})
	debugEnabled := stateRootDebugEnabled()
	if debugEnabled {
		sampleLimit := 8
		samples := make([]string, 0, sampleLimit)
		var xor uint64
		for i, evt := range events {
			item := witnessEventDebugSummary(evt)
			xor ^= xxHashKVForLog(item, nil)
			if i < sampleLimit {
				samples = append(samples, item)
			}
		}
		first := witnessEventDebugSummary(events[0])
		last := witnessEventDebugSummary(events[len(events)-1])
		sampleText := "-"
		if len(samples) > 0 {
			sampleText = strings.Join(samples, " | ")
		}
		logs.Info(
			"[VM][WitnessEventsBatch] height=%d count=%d xor=%016x first=%s last=%s samples=%s",
			fallbackHeight,
			len(events),
			xor,
			first,
			last,
			sampleText,
		)
	}

	applied := 0
	heightSkew := 0
	typeCounts := make(map[string]int)
	for _, evt := range events {
		reqID := witnessEventRequestID(evt)
		typeKey := string(evt.Type)
		typeCounts[typeKey]++
		if evt.Height > 0 && evt.Height != fallbackHeight {
			heightSkew++
			logs.Warn(
				"[VM][WitnessEventHeightSkew] blockHeight=%d evtType=%s evtHeight=%d request=%s",
				fallbackHeight,
				evt.Type,
				evt.Height,
				reqID,
			)
		}
		preDiff := map[string]string(nil)
		if debugEnabled {
			preDiff = summarizeWriteOpsForWitnessEventLog(sv.Diff())
		}
		switch evt.Type {
		case witness.EventChallengeInitiated:
			ch, ok := evt.Data.(*pb.ChallengeRecord)
			if !ok || ch == nil {
				continue
			}
			if _, exists, err := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId)); err != nil {
				return err
			} else if !exists {
				if err := setChallengeRecordWithTimeoutIndexes(sv, ch, mix, 0, 0); err != nil {
					return err
				}
			}
			applied++
		case witness.EventRechargeFinalized:
			req, ok := evt.Data.(*pb.RechargeRequest)
			if !ok || req == nil {
				continue
			}
			if err := applyRechargeFinalized(sv, req, fallbackHeight, mix); err != nil {
				return fmt.Errorf("witness event apply failed type=%s request=%s evtHeight=%d: %w", evt.Type, reqID, evt.Height, err)
			}
			applied++
		case witness.EventRechargeRejected:
			req, ok := evt.Data.(*pb.RechargeRequest)
			if !ok || req == nil {
				continue
			}
			if req.FinalizeHeight == 0 {
				req = proto.Clone(req).(*pb.RechargeRequest)
				req.FinalizeHeight = fallbackHeight
			}
			if err := applyRechargeRejected(sv, req, mix); err != nil {
				return fmt.Errorf("witness event apply failed type=%s request=%s evtHeight=%d: %w", evt.Type, reqID, evt.Height, err)
			}
			applied++
		case witness.EventConsensusReached, witness.EventConsensusFailed,
			witness.EventChallengePeriodStart, witness.EventScopeExpanded,
			witness.EventRechargeRequested, witness.EventRechargeShelved,
			witness.EventRechargeReselectionScheduled:
			req, ok := evt.Data.(*pb.RechargeRequest)
			if !ok || req == nil {
				continue
			}
			if err := persistRechargeRequest(sv, req, mix); err != nil {
				return fmt.Errorf("witness event apply failed type=%s request=%s evtHeight=%d: %w", evt.Type, reqID, evt.Height, err)
			}
			applied++
		default:
			continue
		}
		if debugEnabled {
			postDiff := summarizeWriteOpsForWitnessEventLog(sv.Diff())
			changed, detail := formatStateDebugKeyDiff(preDiff, postDiff, stateRootDebugKeyDiffSampleLimit)
			logs.Info(
				"[VM][WitnessEventDiff] blockHeight=%d evtType=%s evtHeight=%d request=%s changed=%d keys=%s",
				fallbackHeight,
				evt.Type,
				evt.Height,
				reqID,
				changed,
				detail,
			)
		}
	}
	if applied > 0 || heightSkew > 0 {
		logs.Info(
			"[VM][WitnessEventsApplied] height=%d applied=%d skew=%d types=%s",
			fallbackHeight,
			applied,
			heightSkew,
			formatWitnessEventTypeCounts(typeCounts),
		)
	}
	return nil
}

func witnessEventSortRank(t witness.EventType) int {
	switch t {
	case witness.EventRechargeRequested:
		return 10
	case witness.EventConsensusReached, witness.EventConsensusFailed:
		return 15
	case witness.EventScopeExpanded, witness.EventChallengePeriodStart, witness.EventRechargeShelved,
		witness.EventRechargeReselectionScheduled:
		return 20
	case witness.EventRechargeFinalized, witness.EventRechargeRejected:
		return 30
	default:
		return 99
	}
}

func witnessEventSortKey(evt *witness.Event) string {
	if evt == nil {
		return ""
	}
	payloadKey := ""
	if req, ok := evt.Data.(*pb.RechargeRequest); ok && req != nil {
		payloadKey = fmt.Sprintf(
			"%03d|%010d|%020d|%020d|%s",
			req.Status,
			req.Round,
			req.FinalizeHeight,
			req.DeadlineHeight,
			req.RequestId,
		)
	}
	return fmt.Sprintf(
		"%020d|%s|%03d|%s|%s",
		evt.Height,
		evt.RequestID,
		witnessEventSortRank(evt.Type),
		evt.Type,
		payloadKey,
	)
}

func formatWitnessEventTypeCounts(typeCounts map[string]int) string {
	if len(typeCounts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(typeCounts))
	for k := range typeCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, typeCounts[k]))
	}
	return strings.Join(parts, ",")
}

func witnessEventRequestID(evt *witness.Event) string {
	if evt == nil {
		return ""
	}
	if evt.RequestID != "" {
		return evt.RequestID
	}
	if req, ok := evt.Data.(*pb.RechargeRequest); ok && req != nil {
		return req.RequestId
	}
	return ""
}

func witnessEventDebugSummary(evt *witness.Event) string {
	if evt == nil {
		return "nil"
	}
	reqID := witnessEventRequestID(evt)
	var status pb.RechargeRequestStatus
	var round, finalizeHeight, deadlineHeight uint64
	if req, ok := evt.Data.(*pb.RechargeRequest); ok && req != nil {
		status = req.Status
		round = uint64(req.Round)
		finalizeHeight = req.FinalizeHeight
		deadlineHeight = req.DeadlineHeight
	}
	return fmt.Sprintf(
		"type=%s,req=%s,evtHeight=%d,status=%d,round=%d,finalize=%d,deadline=%d",
		evt.Type,
		reqID,
		evt.Height,
		status,
		round,
		finalizeHeight,
		deadlineHeight,
	)
}

func summarizeWriteOpsForWitnessEventLog(diff []WriteOp) map[string]string {
	out := make(map[string]string, len(diff))
	for _, w := range diff {
		if w.Del {
			out[w.Key] = "del"
			continue
		}
		out[w.Key] = "set:" + summarizeStateValueForLog(w.Value)
	}
	return out
}

func applyRechargeRejected(sv StateView, req *pb.RechargeRequest, mix uint64) error {
	if req == nil || req.RequestId == "" {
		return nil
	}

	requestKey := keys.KeyRechargeRequest(req.RequestId)
	requestData, exists, err := sv.Get(requestKey)
	if err != nil || !exists {
		return nil
	}

	var stored pb.RechargeRequest
	if err := unmarshalProtoCompat(requestData, &stored); err != nil {
		return err
	}
	if err := validateRechargeEconomicsPolicy(sv, &stored); err != nil {
		return err
	}
	                                                                          
	                                                        
	if stored.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED {
		return nil
	}
	snap := sv.Snapshot()
	abort := func(err error) error {
		if err != nil {
			_ = sv.Revert(snap)
		}
		return err
	}
	oldStored := proto.Clone(&stored).(*pb.RechargeRequest)
	if err := settleWitnessRequestFeesAndCollateral(sv, &stored, pb.WitnessVoteType_VOTE_FAIL); err != nil {
		return abort(err)
	}

	if stored.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED {
		if req.FinalizeHeight != 0 {
			stored.FinalizeHeight = req.FinalizeHeight
		}
		stored.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
		if err := settleRequesterBond(sv, &stored, false); err != nil {
			return abort(err)
		}
		if err := cleanupTerminalRechargeHotState(sv, &stored); err != nil {
			return abort(err)
		}
		updatedRequestData, err := proto.Marshal(compactRechargeRequestForHotState(&stored))
		if err != nil {
			return abort(err)
		}
		sv.Set(requestKey, updatedRequestData)
		if stored.ReceiverAddress != "" {
			recordUserActivity(sv, stored.ReceiverAddress, &UserActivityRecord{
				Kind: ActKindRechargeRejected, Height: stored.FinalizeHeight,
				Amount: stored.Amount, Token: stored.TokenAddress,
				Ref: stored.RequestId,
			})
		}
	}
	if err := settleRuntimeFeeEscrow(sv, stored.RequestId, stored.FinalizeHeight, false); err != nil {
		return abort(err)
	}
	syncRechargeRequestIndexes(sv, oldStored, &stored, mix)

	releaseRechargeNativeLock(sv, &stored)
	return nil
}

func releaseRechargeNativeLock(sv StateView, req *pb.RechargeRequest) {
	if sv == nil || req == nil || req.NativeChain == "" || req.NativeTxHash == "" || req.RequestId == "" {
		return
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		return
	}
	if !isBTCChainName(req.NativeChain) && protocol.IsBoundDepositProtocol(req.DepositProtocol) {
		key := accountChainRechargePendingKey(req)
		if raw, exists, _ := sv.Get(key); exists && string(raw) == req.RequestId {
			sv.Del(key)
		}
		return
	}
	if !isBTCChainName(req.NativeChain) {
		                                                                                  
		key := keys.KeyRechargeRequestByNativeTx(req.NativeChain, req.NativeTxHash)
		if raw, exists, _ := sv.Get(key); exists && (string(raw) == req.RequestId || len(raw) == 0 || (len(raw) == 1 && raw[0] == 1)) {
			sv.Del(key)
		}
		return
	}
	_, key, err := rechargeNativeLockIdentity(req.NativeChain, req.NativeTxHash, req.NativeVout)
	if err != nil {
		return
	}
	raw, exists, _ := sv.Get(key)
	if !exists {
		return
	}
	if len(raw) == 0 || string(raw) == req.RequestId || (len(raw) == 1 && raw[0] == 1) {
		sv.Del(key)
	}
}

func readUintSeq(sv StateView, key string) uint64 {
	data, exists, err := sv.Get(key)
	if err != nil || !exists || len(data) == 0 {
		return 0
	}
	if n, err := strconv.ParseUint(string(data), 10, 64); err == nil {
		return n
	}
	return 0
}

                                                                                                  
func (x *Executor) applyArbitrationTimeouts(sv StateView, height uint64) (resultErr error) {
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil {
			_ = sv.Revert(snap)
		}
	}()
	if x.WitnessService == nil {
		return nil
	}
	maxShelveWait := x.WitnessService.MaxShelveWaitBlocks()
	if err := x.ensureChallengeTimeoutDueIndex(sv, height, maxShelveWait); err != nil {
		return err
	}
	scanned, err := sv.Scan(keys.KeyChallengeTimeoutDuePrefix(height))
	if err != nil {
		return err
	}
	if len(scanned) == 0 {
		return nil
	}

	ids := make([]string, 0, len(scanned))
	for k := range scanned {
		ids = append(ids, k)
	}
	sort.Strings(ids)

	for _, indexKey := range ids {
		challengeID := string(scanned[indexKey])
		if challengeID == "" {
			challengeID = strings.TrimPrefix(indexKey, keys.KeyChallengeTimeoutDuePrefix(height))
		}
		if challengeID == "" {
			sv.Del(indexKey)
			continue
		}
		key := keys.KeyChallengeRecord(challengeID)
		raw, exists, err := sv.Get(key)
		if err != nil {
			return err
		}
		if !exists || len(raw) == 0 {
			sv.Del(indexKey)
			continue
		}
		var ch pb.ChallengeRecord
		if err := unmarshalProtoCompat(raw, &ch); err != nil {
			return err
		}
		if ch.ChallengeId == "" {
			ch.ChallengeId = challengeID
		}
		if ch.Finalized {
			sv.Del(indexKey)
			continue
		}

		if ch.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE {
			sv.Del(indexKey)
			continue
		}

		reqData, reqOk, err := sv.Get(keys.KeyRechargeRequest(ch.RequestId))
		if err != nil {
			return err
		}
		if !reqOk || len(reqData) == 0 {
			return fmt.Errorf("active arbitration request missing: %s", ch.RequestId)
		}
		var req pb.RechargeRequest
		if err := unmarshalProtoCompat(reqData, &req); err != nil {
			return err
		}

		                                                                                  
		if height <= ch.DeadlineHeight {
			sv.Del(indexKey)
			if dueHeight := challengeActiveTimeoutHeight(&ch); dueHeight != 0 {
				sv.Set(keys.KeyChallengeTimeoutDue(dueHeight, ch.ChallengeId), []byte(ch.ChallengeId))
			}
			continue
		}

		if err := x.advanceArbitrationFromState(sv, &ch, &req, height); err != nil {
			return err
		}
		sv.Del(indexKey)

	}
	return nil
}

func applyArbitrationOutcomeToState(sv StateView, outcome *witness.ArbitrationVoteOutcome, mix, maxShelveWait, height uint64) error {
	if outcome == nil {
		return nil
	}
	                                                                         
	                                                                      
	working := *outcome
	if outcome.Request != nil {
		working.Request = proto.Clone(outcome.Request).(*pb.RechargeRequest)
	}
	if outcome.Challenge != nil {
		working.Challenge = proto.Clone(outcome.Challenge).(*pb.ChallengeRecord)
		if data, exists, err := sv.Get(keys.KeyChallengeRecord(working.Challenge.ChallengeId)); err != nil {
			return err
		} else if exists {
			var stored pb.ChallengeRecord
			if err := unmarshalProtoCompat(data, &stored); err != nil {
				return err
			}
			if stored.Settled {
				return nil
			}
		}
	}
	snap := sv.Snapshot()
	committed := false
	defer func() {
		if !committed {
			_ = sv.Revert(snap)
		}
	}()
	newProtocol := working.Request != nil && working.Request.ArbitrationProtocolVersion != 0
	var frozenFeeRecipients []string
	arbitrationSettled := false
	if newProtocol && working.Challenge != nil && working.Challenge.Finalized {
		working.Request.FinalizeHeight = height
		working.Challenge.FinalizeHeight = height
		if err := settleWitnessArbitration(sv, working.Request, working.Challenge, &frozenFeeRecipients); err != nil {
			return err
		}
		arbitrationSettled = true
	}
	if working.Request != nil {
		request := working.Request
		if request.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED && request.FinalizeHeight == 0 {
			request = proto.Clone(request).(*pb.RechargeRequest)
			request.FinalizeHeight = height
		}
		if request.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED {
			if arbitrationSettled {
				if err := applyRechargeFinalized(sv, request, height, mix, frozenFeeRecipients); err != nil {
					return err
				}
			} else if err := applyRechargeFinalized(sv, request, height, mix); err != nil {
				return err
			}
		} else if err := setRechargeRequestWithIndexes(sv, request, mix); err != nil {
			return err
		}
	}
	if working.Challenge != nil {
		if err := setChallengeRecordWithTimeoutIndexes(sv, working.Challenge, mix, maxShelveWait, height); err != nil {
			return err
		}
	}
	if !newProtocol {
		for _, info := range working.RewardedWitnesses {
			if data, mErr := proto.Marshal(info); mErr == nil {
				sv.Set(keys.KeyWitnessInfo(info.Address), data)
			}
		}
		attachChallengeContributorRefunds(sv, &working)
		if _, err := applySlashAndRefundToState(sv, &working); err != nil {
			return err
		}
	}
	committed = true
	outcome.Request, outcome.Challenge = working.Request, working.Challenge
	return nil
}

func collectArbitrationWitnesses(sv StateView, ch *pb.ChallengeRecord, req *pb.RechargeRequest) map[string]*pb.WitnessInfo {
	addrSet := make(map[string]bool)
	if req != nil {
		for _, v := range req.Votes {
			if v != nil && v.WitnessAddress != "" {
				addrSet[v.WitnessAddress] = true
			}
		}
	}
	if ch != nil {
		for _, v := range ch.ArbitrationVotes {
			if v != nil && v.WitnessAddress != "" {
				addrSet[v.WitnessAddress] = true
			}
		}
		for _, addr := range ch.Arbitrators {
			if addr != "" {
				addrSet[addr] = true
			}
		}
	}
	witnesses := make(map[string]*pb.WitnessInfo, len(addrSet))
	addrs := make([]string, 0, len(addrSet))
	for addr := range addrSet {
		addrs = append(addrs, addr)
	}
	sort.Strings(addrs)
	for _, addr := range addrs {
		infoData, infoOk, _ := sv.Get(keys.KeyWitnessInfo(addr))
		if !infoOk || len(infoData) == 0 {
			continue
		}
		var info pb.WitnessInfo
		if err := unmarshalProtoCompat(infoData, &info); err != nil {
			continue
		}
		witnesses[addr] = &info
	}
	return witnesses
}

func (x *Executor) ensureChallengeTimeoutDueIndex(sv StateView, height, maxShelveWait uint64) error {
	if _, exists, err := sv.Get(keys.KeyChallengeTimeoutIndexBackfillDone()); err != nil || exists {
		return err
	}
	scanned, err := sv.Scan(keys.KeyChallengeRecordPrefix())
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(scanned))
	for k := range scanned {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, key := range ids {
		challengeID, ok := challengeIDFromStateKey(key)
		if !ok {
			continue
		}
		ch, ok := challengeRecordFromData(challengeID, scanned[key])
		if !ok {
			continue
		}
		if dueKey := challengeTimeoutDueKeyForRecord(ch, maxShelveWait, 0); dueKey != "" {
			sv.Set(dueKey, []byte(ch.ChallengeId))
		}
	}
	sv.Set(keys.KeyChallengeTimeoutIndexBackfillDone(), []byte("1"))
	return nil
}

                                                                            
                                                                                                            
                                                                                                                      
func deriveForwardBeacon(sv StateView, selectionHeight, mix uint64, nativeChain, nativeTxHash string) []byte {
	if mix == 0 {
		mix = 1
	}
	h := sha256.New()
	for i := uint64(0); i < mix; i++ {
		bh, _, _ := sv.Get(keys.KeyBlockHashByHeight(selectionHeight + i))
		h.Write(bh)
		h.Write([]byte{0})
	}
	h.Write([]byte(nativeChain))
	h.Write([]byte{0})
	h.Write([]byte(nativeTxHash))
	return h.Sum(nil)
}

                                                                                                                 
                                                                                                                   
                                                       
func (x *Executor) applyPendingRechargeSelections(sv StateView, height uint64) (resultErr error) {
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil {
			_ = sv.Revert(snap)
		}
	}()
	if err := migrateLegacyWitnessAssignments(sv); err != nil {
		return err
	}
	if x.WitnessService == nil {
		return nil
	}
	mix := x.WitnessService.BeaconMixBlocks()
	scanned, err := sv.Scan(keys.KeyRechargeSelectionDuePrefix(height))
	if err != nil || len(scanned) == 0 {
		return err
	}

	ids := make([]string, 0, len(scanned))
	for k := range scanned {
		ids = append(ids, k)
	}
	sort.Strings(ids)

	for _, indexKey := range ids {
		requestID := string(scanned[indexKey])
		if requestID == "" {
			sv.Del(indexKey)
			continue
		}
		requestKey := keys.KeyRechargeRequest(requestID)
		data, exists, err := sv.Get(requestKey)
		if err != nil {
			return err
		}
		if !exists || len(data) == 0 {
			sv.Del(indexKey)
			continue
		}
		var req pb.RechargeRequest
		if err := unmarshalProtoCompat(data, &req); err != nil {
			return err
		}
		if req.RequestId == "" {
			req.RequestId = requestID
		}
		if req.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
			sv.Del(indexKey)
			continue
		}
		readyHeight := rechargeSelectionReadyHeight(&req, mix)
		if readyHeight == 0 {
			sv.Del(indexKey)
			continue
		}
		if readyHeight > height {
			sv.Del(indexKey)
			sv.Set(keys.KeyRechargeSelectionDue(readyHeight, req.RequestId), []byte(req.RequestId))
			continue
		}
		beacon := deriveForwardBeacon(sv, req.SelectionHeight, mix, req.NativeChain, req.NativeTxHash)
		updated, err := x.selectOriginalWitnessesFromState(sv, &req, beacon, height)
		if err != nil || updated == nil {
			sv.Del(indexKey)
			if errors.Is(err, witness.ErrInsufficientSelectedWitnesses) {
				retry := proto.Clone(&req).(*pb.RechargeRequest)
				maxWait := x.WitnessService.MaxShelveWaitBlocks()
				if maxWait > 0 && retry.ShelveHeight > 0 && height > addRechargeHeightSaturating(retry.ShelveHeight, maxWait) {
					retry.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
					retry.FinalizeHeight = height
					if err := applyRechargeRejected(sv, retry, mix); err != nil {
						return err
					}
					continue
				}
				delay := x.WitnessService.RetryIntervalBlocks()
				if depth := x.WitnessService.SelectionBeaconDepth(); delay < depth {
					delay = depth
				}
				if delay == 0 {
					delay = 1
				}
				retry.SelectionHeight = addRechargeHeightSaturating(height, delay)
				retry.SelectionBeacon = nil
				if err := setRechargeRequestWithIndexes(sv, retry, mix); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			return fmt.Errorf("empty witness selection")
		}
		sv.Del(indexKey)
		if err := setRechargeRequestWithIndexes(sv, updated, mix); err != nil {
			return err
		}
	}
	return nil
}

                                                                                               
                                                                                              
func (x *Executor) applyPendingArbitrationSelections(sv StateView, height uint64) (resultErr error) {
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil {
			_ = sv.Revert(snap)
		}
	}()
	if x.WitnessService == nil {
		return nil
	}
	scanned, err := sv.Scan(keys.KeyChallengeSelectionDuePrefix(height))
	if err != nil || len(scanned) == 0 {
		return err
	}
	mix := x.WitnessService.BeaconMixBlocks()
	maxShelveWait := x.WitnessService.MaxShelveWaitBlocks()

	ids := make([]string, 0, len(scanned))
	for k := range scanned {
		ids = append(ids, k)
	}
	sort.Strings(ids)

	for _, indexKey := range ids {
		challengeID := string(scanned[indexKey])
		if challengeID == "" {
			challengeID = strings.TrimPrefix(indexKey, keys.KeyChallengeSelectionDuePrefix(height))
		}
		if challengeID == "" {
			sv.Del(indexKey)
			continue
		}
		key := keys.KeyChallengeRecord(challengeID)
		raw, exists, err := sv.Get(key)
		if err != nil {
			return err
		}
		if !exists || len(raw) == 0 {
			sv.Del(indexKey)
			continue
		}
		var ch pb.ChallengeRecord
		if err := unmarshalProtoCompat(raw, &ch); err != nil {
			return err
		}
		if ch.ChallengeId == "" {
			ch.ChallengeId = challengeID
		}
		if ch.Finalized || ch.Status != pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION {
			sv.Del(indexKey)
			continue
		}
		readyHeight := challengeSelectionReadyHeight(&ch, mix)
		if readyHeight == 0 {
			sv.Del(indexKey)
			continue
		}
		if height < readyHeight {
			sv.Del(indexKey)
			sv.Set(keys.KeyChallengeSelectionDue(readyHeight, ch.ChallengeId), []byte(ch.ChallengeId))
			continue
		}

		req := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(ch.RequestId))
		if req == nil {
			return fmt.Errorf("arbitration request missing: %s", ch.RequestId)
		}
		updated, err := x.startArbitrationRoundFromState(sv, &ch, req, height)
		if err != nil {
			if !errors.Is(err, witness.ErrInsufficientFinalCourt) && !errors.Is(err, errWitnessCaseUnavailable) {
				return err
			}
			if err := x.retryArbitrationSelection(sv, &ch, height); err != nil {
				return err
			}
			sv.Del(indexKey)
			continue
		}
		if err := setChallengeRecordWithTimeoutIndexes(sv, updated, mix, maxShelveWait, 0); err != nil {
			return err
		}
		sv.Del(indexKey)

	}
	return nil
}

                                                                                                    
                        
                                                                             
                                                                                
  
            
                                                                                               
                                                                                   
                                                                              
func applySlashAndRefundToState(sv StateView, o *witness.ArbitrationVoteOutcome) ([]WriteOp, error) {
	if o == nil {
		return nil, nil
	}
	if o.Request != nil && o.Request.ArbitrationProtocolVersion != 0 {
		return nil, fmt.Errorf("new arbitration settlement requires applyArbitrationOutcomeToState")
	}
	snap := sv.Snapshot()
	committed := false
	defer func() {
		if !committed {
			_ = sv.Revert(snap)
		}
	}()
	var ws []WriteOp
	refundTotal := new(big.Int)

	for _, info := range o.SlashedWitnesses {
		if info == nil {
			continue
		}
		if data, err := proto.Marshal(info); err == nil {
			key := keys.KeyWitnessInfo(info.Address)
			sv.Set(key, data)
			ws = append(ws, WriteOp{Key: key, Value: data, Category: "witness"})
		}
	}

	actualSlashTotal := big.NewInt(0)
	slashAddrs := make([]string, 0, len(o.SlashAmounts))
	for addr := range o.SlashAmounts {
		slashAddrs = append(slashAddrs, addr)
	}
	sort.Strings(slashAddrs)
	for _, addr := range slashAddrs {
		amtStr := o.SlashAmounts[addr]
		if addr == "" || amtStr == "" || amtStr == "0" {
			continue
		}
		amt, err := ParseBalance(amtStr)
		if err != nil || amt.Sign() <= 0 {
			continue
		}
		bal := GetBalance(sv, addr, "FB")
		locked, err := parseBalanceStrict("witness locked balance", bal.WitnessLockedBalance)
		if err != nil {
			continue
		}
		actual := new(big.Int).Set(amt)
		if locked.Cmp(actual) < 0 {
			actual.Set(locked)
		}
		if actual.Sign() <= 0 {
			continue
		}
		newLocked, err := SafeSub(locked, actual)
		if err != nil {
			continue
		}
		bal.WitnessLockedBalance = newLocked.String()
		SetBalance(sv, addr, "FB", bal)
		ws = append(ws, balanceWriteOp(sv, addr, "FB"))
		actualSlashTotal.Add(actualSlashTotal, actual)
	}

	if len(o.ChallengerRefunds) > 0 {
		addrs := make([]string, 0, len(o.ChallengerRefunds))
		for addr := range o.ChallengerRefunds {
			addrs = append(addrs, addr)
		}
		sort.Strings(addrs)
		for _, addr := range addrs {
			refund, err := ParseBalance(o.ChallengerRefunds[addr])
			if addr == "" || addr == "system" || err != nil || refund.Sign() <= 0 {
				continue
			}
			bal := GetBalance(sv, addr, "FB")
			cur, err := parseBalanceStrict("balance", bal.Balance)
			if err != nil {
				continue
			}
			if newBal, aErr := SafeAdd(cur, refund); aErr == nil {
				bal.Balance = newBal.String()
				SetBalance(sv, addr, "FB", bal)
				ws = append(ws, balanceWriteOp(sv, addr, "FB"))
				refundTotal.Add(refundTotal, refund)
			}
		}
	} else if o.ChallengerAddr != "" && o.ChallengerAddr != "system" &&
		o.ChallengerRefund != "" && o.ChallengerRefund != "0" {
		refund, err := ParseBalance(o.ChallengerRefund)
		if err == nil && refund.Sign() > 0 {
			bal := GetBalance(sv, o.ChallengerAddr, "FB")
			cur, err := parseBalanceStrict("balance", bal.Balance)
			if err == nil {
				if newBal, aErr := SafeAdd(cur, refund); aErr == nil {
					bal.Balance = newBal.String()
					SetBalance(sv, o.ChallengerAddr, "FB", bal)
					ws = append(ws, balanceWriteOp(sv, o.ChallengerAddr, "FB"))
					refundTotal.Add(refundTotal, refund)
				}
			}
		}
	}

	tokenOp, err := applyChallengeSettlementSupplyDelta(sv, o, refundTotal, actualSlashTotal)
	if err != nil {
		return ws, err
	}
	if tokenOp.Key != "" {
		ws = append(ws, tokenOp)
	}
	if o.Challenge != nil && (o.Challenge.Finalized || o.Challenge.Status == pb.ChallengeStatus_CHALLENGE_FINALIZED) {
		ws = append(ws, clearFinalizedChallengeContributionState(sv, o.Challenge.ChallengeId)...)
	}
	committed = true
	return ws, nil
}

func applyChallengeSettlementSupplyDelta(sv StateView, o *witness.ArbitrationVoteOutcome, refundTotal, actualSlashTotal *big.Int) (WriteOp, error) {
	if sv == nil || o == nil || o.Challenge == nil {
		return WriteOp{}, nil
	}
	if !o.Challenge.Finalized && o.Challenge.Status != pb.ChallengeStatus_CHALLENGE_FINALIZED {
		return WriteOp{}, nil
	}
	stake, err := parseBalanceStrict("challenge stake amount", o.Challenge.StakeAmount)
	if err != nil {
		return WriteOp{}, err
	}
	if refundTotal == nil {
		refundTotal = new(big.Int)
	}
	if actualSlashTotal == nil {
		actualSlashTotal = new(big.Int)
	}
	delta := new(big.Int).Sub(refundTotal, stake) // safe-math-ok: 守恒审计需要有符号的退款与原质押差额，正增发随后被拒绝
	delta.Sub(delta, actualSlashTotal)
	if delta.Sign() == 0 {
		return WriteOp{}, nil
	}
	if delta.Sign() > 0 {
		return WriteOp{}, fmt.Errorf("legacy challenge settlement cannot mint missing funds: %s", delta)
	}
	return adjustTokenTotalSupplyForAudit(sv, FeeToken, delta)
}

func adjustTokenTotalSupplyForAudit(sv StateView, tokenAddr string, delta *big.Int) (WriteOp, error) {
	if sv == nil || tokenAddr == "" || delta == nil || delta.Sign() == 0 {
		return WriteOp{}, nil
	}
	tokenKey := keys.KeyToken(tokenAddr)
	tokenData, exists, err := sv.Get(tokenKey)
	if err != nil {
		return WriteOp{}, err
	}
	if !exists || len(tokenData) == 0 {
		return WriteOp{}, fmt.Errorf("token %s not found for supply delta %s", tokenAddr, delta.String())
	}
	var token pb.Token
	if err := unmarshalProtoCompat(tokenData, &token); err != nil {
		return WriteOp{}, err
	}
	supply, ok := new(big.Int).SetString(token.TotalSupply, 10)
	if !ok || supply == nil {
		return WriteOp{}, fmt.Errorf("invalid token total supply: token=%s supply=%q", tokenAddr, token.TotalSupply)
	}
	newSupply := new(big.Int).Add(supply, delta) // safe-math-ok: 供应变动使用有符号 delta，写回前显式拒绝负供应
	if newSupply.Sign() < 0 {
		return WriteOp{}, fmt.Errorf("token total supply underflow: token=%s cur=%s delta=%s", tokenAddr, supply.String(), delta.String())
	}
	token.TotalSupply = newSupply.String()
	updated, err := proto.Marshal(&token)
	if err != nil {
		return WriteOp{}, err
	}
	sv.Set(tokenKey, updated)
	return WriteOp{Key: tokenKey, Value: updated, Category: "token"}, nil
}

func persistRechargeRequest(sv StateView, req *pb.RechargeRequest, mix uint64) error {
	if req == nil || req.RequestId == "" {
		return nil
	}
	existing := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if existing != nil {
		if rechargeRequestTerminal(existing.Status) {
			return nil
		}
		if req.Round < existing.Round {
			return nil
		}
		if req.Status == pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION && existing.Status != pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION {
			allowed := (existing.Status == pb.RechargeRequestStatus_RECHARGE_VOTING || existing.Status == pb.RechargeRequestStatus_RECHARGE_SHELVED) && req.Round == existing.Round+1 && req.SelectionHeight > existing.SelectionHeight
			if !allowed {
				return nil
			}
		}
		                                                                                                       
		if existing.Status == pb.RechargeRequestStatus_RECHARGE_ARBITRATION || existing.Status == pb.RechargeRequestStatus_RECHARGE_CHALLENGED {
			return nil
		}
		req = proto.Clone(req).(*pb.RechargeRequest)
		if existing.ChallengeId != "" {
			req.ChallengeId = existing.ChallengeId
		}
		req.CaseCollateral = existing.CaseCollateral
		req.FeeEscrow, req.FeeSettled = existing.FeeEscrow, existing.FeeSettled
		req.ArbitrationProtocolVersion = existing.ArbitrationProtocolVersion
		inheritRequesterBond(req, existing)
		if err := inheritRechargeEconomicsPolicy(req, existing); err != nil {
			return err
		}
		if len(existing.OriginalPositions) > len(req.OriginalPositions) {
			req.OriginalPositions = existing.OriginalPositions
		}
	}
	return setRechargeRequestWithIndexes(sv, req, mix)
}

                                                                                
                                                                             
                                                                                
func resolveOrRegisterBridgedToken(sv StateView, stored *pb.RechargeRequest) error {
	if stored == nil {
		return fmt.Errorf("nil recharge request")
	}
	identity, err := bridgeasset.Resolve(stored.NativeChain, stored.NativeContract)
	if err != nil {
		return err
	}
	stored.NativeChain = identity.Chain
	stored.NativeContract = identity.Contract
	if stored.TokenAddress != "" && stored.TokenAddress != identity.ID {
		return fmt.Errorf("recharge AssetID mismatch: stored=%s canonical=%s", stored.TokenAddress, identity.ID)
	}

	idxKey := keys.KeyNativeTokenIndex(identity.Chain, identity.Contract)
	if idxData, hit, err := sv.Get(idxKey); err != nil {
		return err
	} else if hit && string(idxData) != identity.ID {
		return fmt.Errorf("native asset index conflict: indexed=%s canonical=%s", string(idxData), identity.ID)
	}

	tokenKey := keys.KeyToken(identity.ID)
	if tokenData, exists, err := sv.Get(tokenKey); err != nil {
		return err
	} else if exists {
		var tok pb.Token
		if err := unmarshalProtoCompat(tokenData, &tok); err != nil {
			return err
		}
		if tok.Type != pb.TokenType_TOKEN_BRIDGED || tok.Address != identity.ID {
			return fmt.Errorf("canonical AssetID is occupied by a non-bridged asset")
		}
		registered, err := bridgeasset.Resolve(tok.NativeChain, tok.NativeContract)
		if err != nil || registered != identity {
			return fmt.Errorf("registered asset identity mismatch")
		}
	} else {
		tok := &pb.Token{
			Address:        identity.ID,
			Owner:          "system",
			TotalSupply:    "0",
			Type:           pb.TokenType_TOKEN_BRIDGED,
			CreateHeight:   stored.FinalizeHeight,
			NativeChain:    identity.Chain,
			NativeContract: identity.Contract,
		}
		data, err := proto.Marshal(tok)
		if err != nil {
			return err
		}
		sv.Set(tokenKey, data)
	}
	sv.Set(idxKey, []byte(identity.ID))
	stored.TokenAddress = identity.ID
	return nil
}

                                                                                                       
                                                                                 
func adjustBridgedTotalSupply(sv StateView, tokenAddr string, delta *big.Int) error {
	if delta == nil || delta.Sign() == 0 || tokenAddr == "" {
		return nil
	}
	tokenKey := keys.KeyToken(tokenAddr)
	data, exists, err := sv.Get(tokenKey)
	if err != nil {
		return fmt.Errorf("read bridged total supply: %w", err)
	}
	if !exists {
		return nil
	}
	var tok pb.Token
	if err := unmarshalProtoCompat(data, &tok); err != nil {
		return err
	}
	if tok.Type != pb.TokenType_TOKEN_BRIDGED {
		return nil
	}
	supply, ok := new(big.Int).SetString(tok.TotalSupply, 10)
	if !ok || supply == nil {
		supply = big.NewInt(0)
	}
	newSupply := new(big.Int).Add(supply, delta) // safe-math-ok: 供应变动使用有符号 delta，写回前显式拒绝负供应
	if newSupply.Sign() < 0 {
		return fmt.Errorf("bridged total supply underflow: token=%s cur=%s delta=%s", tokenAddr, supply, delta)
	}
	tok.TotalSupply = newSupply.String()
	updated, err := proto.Marshal(&tok)
	if err != nil {
		return err
	}
	sv.Set(tokenKey, updated)
	return nil
}
