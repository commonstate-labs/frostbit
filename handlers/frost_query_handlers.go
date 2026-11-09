                                   
                         

package handlers

import (
	"dex/config"
	"dex/frost/artifact"
	frostchain "dex/frost/chain"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/witness"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	maxFrostPlanningLogRows        = 128
	maxFrostPlanningLogsPerReply   = 256
	defaultFrostWithdrawQueryLimit = 100
	maxFrostWithdrawQueryLimit     = 500
	maxFrostWithdrawQueryOffset    = 5000
	maxFrostWithdrawFilterReads    = 6000
	defaultFrostVaultListLimit     = 100
	maxFrostVaultListLimit         = 500
	defaultFrostVaultFundsLimit    = 1024
	maxFrostVaultFundsLimit        = 4096
	maxFrostMigrationBundleRaw     = 64 << 20
	defaultFrostDkgCommitLimit     = 100
	maxFrostDkgCommitLimit         = 500
	maxFrostDkgProgressCommittee   = 100
)

type frostDkgParticipantProgress struct {
	Address         string `json:"address"`
	Committed       bool   `json:"committed"`
	SharesSubmitted int    `json:"shares_submitted"`
	SharesExpected  int    `json:"shares_expected"`
	ValidationAck   bool   `json:"validation_ack"`
}

type frostDkgProgressResponse struct {
	Chain                  string                        `json:"chain"`
	VaultID                uint32                        `json:"vault_id"`
	EpochID                uint64                        `json:"epoch_id"`
	SessionID              string                        `json:"session_id"`
	DkgStatus              string                        `json:"dkg_status"`
	ValidationStatus       string                        `json:"validation_status"`
	TranscriptAvailable    bool                          `json:"transcript_available"`
	CommitCount            int                           `json:"commit_count"`
	CommitExpected         int                           `json:"commit_expected"`
	ShareCount             int                           `json:"share_count"`
	ShareExpected          int                           `json:"share_expected"`
	ComplaintCount         int                           `json:"complaint_count"`
	PendingComplaints      int                           `json:"pending_complaints"`
	ResolvedComplaints     int                           `json:"resolved_complaints"`
	DisqualifiedComplaints int                           `json:"disqualified_complaints"`
	ValidationAckCount     int                           `json:"validation_ack_count"`
	ValidationAckExpected  int                           `json:"validation_ack_expected"`
	Participants           []frostDkgParticipantProgress `json:"participants"`
}

func (hm *HandlerManager) hydrateArchivedRechargeRequest(req *pb.RechargeRequest) *pb.RechargeRequest {
	if req == nil || req.RequestId == "" ||
		(req.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED && req.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED) {
		return req
	}
	raw, err := hm.dbManager.Get(keys.KeyArchiveRechargeRequest(req.RequestId))
	if err != nil || len(raw) == 0 {
		return req
	}
	var archived pb.RechargeRequest
	if err := proto.Unmarshal(raw, &archived); err != nil || archived.RequestId != req.RequestId {
		return req
	}
	return &archived
}

func (hm *HandlerManager) hydrateArchivedChallengeRecord(rec *pb.ChallengeRecord) *pb.ChallengeRecord {
	if rec == nil || rec.ChallengeId == "" || (!rec.Finalized && rec.Status != pb.ChallengeStatus_CHALLENGE_FINALIZED) {
		return rec
	}
	raw, err := hm.dbManager.Get(keys.KeyArchiveChallengeRecord(rec.ChallengeId))
	if err != nil || len(raw) == 0 {
		return rec
	}
	var archived pb.ChallengeRecord
	if err := proto.Unmarshal(raw, &archived); err != nil || archived.ChallengeId != rec.ChallengeId {
		return rec
	}
	return &archived
}

func (hm *HandlerManager) hydrateArchivedFrostWithdrawState(state *pb.FrostWithdrawState) *pb.FrostWithdrawState {
	if state == nil || state.WithdrawId == "" || !isFrostWithdrawTerminal(state.Status) {
		return state
	}
	raw, _, err := hm.frostPermanentOrLegacy(keys.KeyFrostPermanentWithdraw(state.WithdrawId), keys.KeyArchiveFrostWithdraw(state.WithdrawId))
	if err != nil || len(raw) == 0 {
		return state
	}
	var archived pb.FrostWithdrawState
	if err := proto.Unmarshal(raw, &archived); err != nil || archived.WithdrawId != state.WithdrawId {
		return state
	}
	return &archived
}

func isFrostWithdrawTerminal(status string) bool {
	return strings.EqualFold(status, "SIGNED") || strings.EqualFold(status, "CANCELLED")
}

func clonePlanningLog(log *pb.FrostPlanningLog) *pb.FrostPlanningLog {
	if log == nil {
		return nil
	}
	return &pb.FrostPlanningLog{
		Step:      log.Step,
		Status:    log.Status,
		Message:   log.Message,
		Timestamp: log.Timestamp,
	}
}

func mergeAndSortPlanningLogs(base, extra []*pb.FrostPlanningLog) []*pb.FrostPlanningLog {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	baseStart := max(0, len(base)-maxFrostPlanningLogsPerReply)
	extraStart := max(0, len(extra)-maxFrostPlanningLogsPerReply)
	merged := make([]*pb.FrostPlanningLog, 0, min(maxFrostPlanningLogsPerReply*2, len(base)-baseStart+len(extra)-extraStart))
	for _, log := range base[baseStart:] {
		if cloned := clonePlanningLog(log); cloned != nil {
			merged = append(merged, cloned)
		}
	}
	for _, log := range extra[extraStart:] {
		if cloned := clonePlanningLog(log); cloned != nil {
			merged = append(merged, cloned)
		}
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Timestamp == merged[j].Timestamp {
			if merged[i].Step == merged[j].Step {
				if merged[i].Status == merged[j].Status {
					return merged[i].Message < merged[j].Message
				}
				return merged[i].Status < merged[j].Status
			}
			return merged[i].Step < merged[j].Step
		}
		return merged[i].Timestamp < merged[j].Timestamp
	})
	if len(merged) > maxFrostPlanningLogsPerReply {
		merged = merged[len(merged)-maxFrostPlanningLogsPerReply:]
	}
	return merged
}

func (hm *HandlerManager) collectLocalPlanningLogs(withdrawID string) []*pb.FrostPlanningLog {
	if withdrawID == "" {
		return nil
	}
	prefix := fmt.Sprintf("v1_frost_planning_log_%s_", withdrawID)
	rows, err := hm.dbManager.ScanKVWithLimitOrdered(prefix, maxFrostPlanningLogRows, true)
	if err != nil || len(rows) == 0 {
		return nil
	}

	merged := make([]*pb.FrostPlanningLog, 0)
	for i := len(rows) - 1; i >= 0; i-- {
		var tx pb.FrostWithdrawPlanningLogTx
		if err := proto.Unmarshal(rows[i].Value, &tx); err != nil {
			continue
		}
		for _, log := range tx.Logs {
			if cloned := clonePlanningLog(log); cloned != nil {
				merged = append(merged, cloned)
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Timestamp == merged[j].Timestamp {
			if merged[i].Step == merged[j].Step {
				if merged[i].Status == merged[j].Status {
					return merged[i].Message < merged[j].Message
				}
				return merged[i].Status < merged[j].Status
			}
			return merged[i].Step < merged[j].Step
		}
		return merged[i].Timestamp < merged[j].Timestamp
	})
	return merged
}

func boundedPositiveInt(raw string, defaultValue, maxValue int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return defaultValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func (hm *HandlerManager) loadFrostWithdrawForQuery(withdrawID string) (*pb.FrostWithdrawState, bool) {
	withdrawID = strings.TrimSpace(withdrawID)
	if withdrawID == "" {
		return nil, false
	}
	if data, found, err := hm.frostResultGet(keys.KeyFrostPermanentWithdraw(withdrawID)); err != nil {
		return nil, false
	} else if found {
		var state pb.FrostWithdrawState
		if len(data) == 0 || proto.Unmarshal(data, &state) != nil || state.WithdrawId != withdrawID {
			return nil, false
		}
		return &state, true
	}
	data, err := hm.dbManager.Get(keys.KeyFrostWithdraw(withdrawID))
	if err != nil || len(data) == 0 {
		data, err = hm.dbManager.Get(keys.KeyArchiveFrostWithdraw(withdrawID))
	}
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var state pb.FrostWithdrawState
	if proto.Unmarshal(data, &state) != nil || state.WithdrawId != withdrawID {
		return nil, false
	}
	return hm.hydrateArchivedFrostWithdrawState(&state), true
}

                                                                              
                                                                              
                                                                          
func (hm *HandlerManager) scanFrostWithdrawStates(
	prefix string,
	reverse bool,
	offset int,
	limit int,
	readBudget int,
	accept func(*pb.FrostWithdrawState) bool,
) ([]*pb.FrostWithdrawState, error) {
	if limit <= 0 || prefix == "" {
		return nil, nil
	}
	if offset < 0 {
		offset = 0
	}
	                                                                         
	                                                                       
	minimumBudget := offset + limit + 512
	if readBudget < minimumBudget {
		readBudget = minimumBudget
	}
	if readBudget > maxFrostWithdrawFilterReads {
		readBudget = maxFrostWithdrawFilterReads
	}

	const batchSize = 256
	states := make([]*pb.FrostWithdrawState, 0, limit)
	seen := make(map[string]struct{}, min(readBudget, 1024))
	cursor := ""
	matched := 0
	scanned := 0
	for scanned < readBudget && len(states) < limit {
		pageLimit := min(batchSize, readBudget-scanned)
		rows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(prefix, cursor, pageLimit, reverse)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			cursor = row.Key
			scanned++
			withdrawID := strings.TrimSpace(string(row.Value))
			if _, duplicate := seen[withdrawID]; duplicate {
				continue
			}
			state, ok := hm.loadFrostWithdrawForQuery(withdrawID)
			if !ok || !frostWithdrawQueryRowMatches(row.Key, state) || (accept != nil && !accept(state)) {
				continue
			}
			seen[withdrawID] = struct{}{}
			if matched < offset {
				matched++
				continue
			}
			states = append(states, state)
			matched++
			if len(states) == limit {
				break
			}
		}
		if len(rows) < pageLimit {
			break
		}
	}
	return states, nil
}

func frostWithdrawQueryRowMatches(key string, state *pb.FrostWithdrawState) bool {
	terminal := isFrostWithdrawTerminal(state.Status)
	for _, expected := range []string{
		keys.KeyFrostWithdrawQueryGlobalState(terminal, state.RequestHeight, state.Seq, state.WithdrawId),
		keys.KeyFrostWithdrawQueryChainState(state.Chain, terminal, state.RequestHeight, state.Seq, state.WithdrawId),
		keys.KeyFrostWithdrawQueryChainVaultState(state.Chain, state.VaultId, terminal, state.RequestHeight, state.Seq, state.WithdrawId),
		                                                                     
		keys.KeyFrostWithdrawQueryGlobal(state.RequestHeight, state.Seq, state.WithdrawId),
		keys.KeyFrostWithdrawQueryChain(state.Chain, state.RequestHeight, state.Seq, state.WithdrawId),
		keys.KeyFrostWithdrawQueryChainVault(state.Chain, state.VaultId, state.RequestHeight, state.Seq, state.WithdrawId),
	} {
		if key == expected {
			return true
		}
	}
	return false
}

                                           
func (hm *HandlerManager) HandleGetFrostConfig(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("GetFrostConfig")

	                               
	var frostCfg config.FrostConfig
	cfgKey := keys.KeyFrostConfig()
	data, err := hm.dbManager.Get(cfgKey)
	if err != nil || data == nil {
		                                       
		frostCfg = config.DefaultFrostConfig()
	} else {
		                     
		if err := json.Unmarshal(data, &frostCfg); err != nil {
			frostCfg = config.DefaultFrostConfig()
		}
	}

	                           
	supportedChains := make([]string, 0, len(frostCfg.Chains))
	chainCfgsPb := make(map[string]*pb.ChainCfg)
	for chainName, chainCfg := range frostCfg.Chains {
		supportedChains = append(supportedChains, chainName)
		chainCfgsPb[chainName] = &pb.ChainCfg{
			SignAlgo:           chainCfg.SignAlgo,
			FrostVariant:       chainCfg.FrostVariant,
			VaultsPerChain:     int32(chainCfg.VaultsPerChain),
			BtcFeeSatsPerVbyte: chainCfg.FeeSatsPerVByte,
		}
	}

	                         
	totalVaults := 0
	for _, c := range frostCfg.Chains {
		totalVaults += c.VaultsPerChain
	}

	resp := &pb.GetFrostConfigResponse{
		Enabled:          frostCfg.Enabled,
		SupportedChains:  supportedChains,
		DefaultThreshold: frostCfg.Vault.ThresholdRatio,
		VaultCount:       int32(totalVaults),
		CommitteeSize:    int32(frostCfg.Vault.DefaultK),
		MinWithdraw:      "0.001",                         
		Chains:           chainCfgsPb,
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                             
func (hm *HandlerManager) HandleGetWithdrawStatus(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("GetWithdrawStatus")

	withdrawID := r.URL.Query().Get("withdraw_id")
	if withdrawID == "" {
		                     
		withdrawID = r.URL.Query().Get("lot_id")
	}
	if withdrawID == "" {
		http.Error(w, "missing withdraw_id", http.StatusBadRequest)
		return
	}

	statePtr, found := hm.loadFrostWithdrawForQuery(withdrawID)
	if !found {
		http.Error(w, "withdraw not found", http.StatusNotFound)
		return
	}

	resp := &pb.GetWithdrawStatusResponse{
		LotId:        statePtr.WithdrawId,
		Chain:        statePtr.Chain,
		Status:       statePtr.Status,
		Amount:       statePtr.Amount,
		Receiver:     statePtr.To,
		Height:       statePtr.RequestHeight,
		TxHash:       statePtr.JobId,                              
		PlanningLogs: mergeAndSortPlanningLogs(statePtr.PlanningLogs, hm.collectLocalPlanningLogs(statePtr.WithdrawId)),
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                   
func (hm *HandlerManager) HandleListWithdraws(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("ListWithdraws")

	chain := strings.TrimSpace(r.URL.Query().Get("chain"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit := boundedPositiveInt(r.URL.Query().Get("limit"), 20, 100)
	prefix := keys.KeyFrostWithdrawQueryGlobalPrefix()
	if chain != "" {
		prefix = keys.KeyFrostWithdrawQueryChainPrefix(chain)
	}
	readBudget := limit
	if status != "" {
		readBudget = maxFrostWithdrawFilterReads
	}
	states, err := hm.scanFrostWithdrawStates(prefix, false, 0, limit, readBudget, func(state *pb.FrostWithdrawState) bool {
		return (chain == "" || strings.EqualFold(state.Chain, chain)) &&
			(status == "" || strings.EqualFold(state.Status, status))
	})
	if err != nil {
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}

	withdraws := make([]*pb.GetWithdrawStatusResponse, 0, len(states))
	for _, state := range states {
		withdraws = append(withdraws, &pb.GetWithdrawStatusResponse{
			LotId:        state.WithdrawId,
			Chain:        state.Chain,
			Status:       state.Status,
			Amount:       state.Amount,
			Receiver:     state.To,
			Height:       state.RequestHeight,
			PlanningLogs: mergeAndSortPlanningLogs(state.PlanningLogs, hm.collectLocalPlanningLogs(state.WithdrawId)),
		})
	}

	resp := &pb.ListWithdrawsResponse{
		Withdraws: withdraws,
		Total:     int32(len(withdraws)),
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

const (
	defaultFrostWithdrawMonitorLimit          = 500
	defaultFrostWithdrawMonitorCompletedLimit = 100
	maxFrostWithdrawMonitorLimit              = 1000
	maxFrostWithdrawMonitorCompletedLimit     = 200
	maxFrostWithdrawMonitorScan               = 5000
)

func frostWithdrawMonitorQueueQuota(total, queueCount, queueIndex int) int {
	if total <= 0 || queueCount <= 0 || queueIndex < 0 || queueIndex >= queueCount {
		return 0
	}
	quota := total / queueCount
	if queueIndex < total%queueCount {
		quota++
	}
	return quota
}

func frostWithdrawMonitorCandidateScanLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	scanLimit := limit * 4
	if scanLimit < 64 {
		scanLimit = 64
	}
	if scanLimit > maxFrostWithdrawMonitorScan {
		scanLimit = maxFrostWithdrawMonitorScan
	}
	return scanLimit
}

type frostWithdrawQueueRef struct {
	chain string
	asset string
}

func parseFrostWithdrawMonitorLimit(raw string) int {
	if raw == "" {
		return defaultFrostWithdrawMonitorLimit
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return defaultFrostWithdrawMonitorLimit
	}
	if v > maxFrostWithdrawMonitorLimit {
		return maxFrostWithdrawMonitorLimit
	}
	return v
}

func parseFrostWithdrawMonitorCompletedLimit(raw string) int {
	if raw == "" {
		return defaultFrostWithdrawMonitorCompletedLimit
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return defaultFrostWithdrawMonitorCompletedLimit
	}
	if v > maxFrostWithdrawMonitorCompletedLimit {
		return maxFrostWithdrawMonitorCompletedLimit
	}
	return v
}

func parseFrostWithdrawFIFOSeqKey(key string) (frostWithdrawQueueRef, bool) {
	prefix := keys.KeyFrostWithdrawFIFOSeqPrefix()
	if !strings.HasPrefix(key, prefix) {
		return frostWithdrawQueueRef{}, false
	}
	rest := strings.TrimPrefix(key, prefix)
	parts := strings.SplitN(rest, "_", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return frostWithdrawQueueRef{}, false
	}
	return frostWithdrawQueueRef{chain: parts[0], asset: parts[1]}, true
}

func (hm *HandlerManager) frostWithdrawMonitorQueues(chain, asset string) ([]frostWithdrawQueueRef, error) {
	if chain != "" && asset != "" {
		return []frostWithdrawQueueRef{{chain: chain, asset: asset}}, nil
	}
	seqPrefix := keys.KeyFrostWithdrawFIFOSeqPrefix()
	if chain != "" {
		seqPrefix = keys.KeyFrostWithdrawFIFOSeq(chain, "")
	}
	seqKeys, err := hm.dbManager.ScanKeysWithLimitOrdered(seqPrefix, 0, false)
	if err != nil {
		return nil, err
	}
	refs := make([]frostWithdrawQueueRef, 0, len(seqKeys))
	seen := make(map[string]struct{}, len(seqKeys))
	for _, seqKey := range seqKeys {
		ref, ok := parseFrostWithdrawFIFOSeqKey(seqKey)
		if !ok {
			continue
		}
		if chain != "" && !strings.EqualFold(ref.chain, chain) {
			continue
		}
		if asset != "" && !strings.EqualFold(ref.asset, asset) {
			continue
		}
		k := strings.ToLower(ref.chain) + "\x00" + strings.ToLower(ref.asset)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		refs = append(refs, ref)
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if strings.EqualFold(refs[i].chain, refs[j].chain) {
			return refs[i].asset < refs[j].asset
		}
		return refs[i].chain < refs[j].chain
	})
	return refs, nil
}

func (hm *HandlerManager) readFrostWithdrawUint64(key string, fallback uint64) (uint64, error) {
	raw, exists, err := hm.dbManager.GetState(key)
	if err != nil {
		return fallback, err
	}
	if !exists || len(raw) == 0 {
		return fallback, nil
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || v == 0 {
		return fallback, nil
	}
	return v, nil
}

func (hm *HandlerManager) scanFrostWithdrawMonitorQueue(ref frostWithdrawQueueRef, limit, scanLimit int) ([]*pb.FrostWithdrawState, int, error) {
	if limit <= 0 || scanLimit <= 0 {
		return nil, 0, nil
	}
	head, err := hm.readFrostWithdrawUint64(keys.KeyFrostWithdrawFIFOHead(ref.chain, ref.asset), 1)
	if err != nil {
		return nil, 0, err
	}
	maxSeq, err := hm.readFrostWithdrawUint64(keys.KeyFrostWithdrawFIFOSeq(ref.chain, ref.asset), 0)
	if err != nil {
		return nil, 0, err
	}
	if maxSeq == 0 || head > maxSeq {
		return nil, 0, nil
	}
	states := make([]*pb.FrostWithdrawState, 0, limit)
	scanned := 0
	for seq := head; seq <= maxSeq && len(states) < limit && scanned < scanLimit; seq++ {
		scanned++
		withdrawIDRaw, exists, err := hm.dbManager.GetState(keys.KeyFrostWithdrawFIFOIndex(ref.chain, ref.asset, seq))
		if err != nil {
			return nil, scanned, err
		}
		if !exists || len(withdrawIDRaw) == 0 {
			continue
		}
		withdrawID := string(withdrawIDRaw)
		withdrawRaw, exists, err := hm.dbManager.GetState(keys.KeyFrostWithdraw(withdrawID))
		if err != nil {
			return nil, scanned, err
		}
		if !exists || len(withdrawRaw) == 0 {
			continue
		}
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(withdrawRaw, &state); err != nil || state.WithdrawId == "" {
			continue
		}
		if isFrostWithdrawTerminal(state.Status) {
			continue
		}
		if !strings.EqualFold(state.Chain, ref.chain) || !strings.EqualFold(state.Asset, ref.asset) {
			continue
		}
		state.PlanningLogs = mergeAndSortPlanningLogs(state.PlanningLogs, hm.collectLocalPlanningLogs(state.WithdrawId))
		states = append(states, proto.Clone(&state).(*pb.FrostWithdrawState))
	}
	return states, scanned, nil
}

func frostWithdrawIDFromDoneIndex(key string, value []byte) string {
	if id := strings.TrimSpace(string(value)); id != "" {
		return id
	}
	idx := strings.LastIndex(key, "_")
	if idx < 0 || idx == len(key)-1 {
		return ""
	}
	return strings.TrimSpace(key[idx+1:])
}

func (hm *HandlerManager) scanFrostWithdrawMonitorCompletedQueue(ref frostWithdrawQueueRef, limit, scanLimit int) ([]*pb.FrostWithdrawState, int, error) {
	if limit <= 0 || scanLimit <= 0 {
		return nil, 0, nil
	}
	states := make([]*pb.FrostWithdrawState, 0, limit)
	seen := make(map[string]struct{}, limit)
	addState := func(state *pb.FrostWithdrawState) {
		if state == nil || len(states) >= limit || strings.TrimSpace(state.WithdrawId) == "" {
			return
		}
		if _, ok := seen[state.WithdrawId]; ok {
			return
		}
		if !isFrostWithdrawTerminal(state.Status) {
			return
		}
		if !strings.EqualFold(state.Chain, ref.chain) || !strings.EqualFold(state.Asset, ref.asset) {
			return
		}
		seen[state.WithdrawId] = struct{}{}
		statePtr := hm.hydrateArchivedFrostWithdrawState(state)
		cloned := proto.Clone(statePtr).(*pb.FrostWithdrawState)
		cloned.PlanningLogs = mergeAndSortPlanningLogs(cloned.PlanningLogs, hm.collectLocalPlanningLogs(cloned.WithdrawId))
		states = append(states, cloned)
	}
	loadArchived := func(withdrawID string) {
		if strings.TrimSpace(withdrawID) == "" {
			return
		}
		withdrawRaw, err := hm.dbManager.Get(keys.KeyArchiveFrostWithdraw(withdrawID))
		if err != nil || len(withdrawRaw) == 0 {
			return
		}
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(withdrawRaw, &state); err != nil {
			return
		}
		addState(&state)
	}

	                                                                             
	                                                                           
	pairs, err := hm.dbManager.ScanKVWithLimitOrdered(keys.KeyArchiveFrostWithdrawDonePrefix(ref.chain, ref.asset), scanLimit, true)
	if err != nil {
		return nil, 0, err
	}
	for _, pair := range pairs {
		if len(states) >= limit {
			break
		}
		loadArchived(frostWithdrawIDFromDoneIndex(pair.Key, pair.Value))
	}
	if len(states) >= limit {
		return states, len(pairs), nil
	}

	remainingScan := scanLimit - len(pairs)
	if remainingScan <= 0 {
		return states, len(pairs), nil
	}
	pairs, err = hm.dbManager.ScanKVWithLimitOrdered(keys.KeyArchiveFrostWithdraw(""), remainingScan, true)
	if err != nil {
		return nil, scanLimit - remainingScan, err
	}
	for _, pair := range pairs {
		if len(states) >= limit {
			break
		}
		var state pb.FrostWithdrawState
		if err := proto.Unmarshal(pair.Value, &state); err != nil {
			continue
		}
		addState(&state)
	}
	return states, scanLimit - remainingScan + len(pairs), nil
}

                                                                        
                                                                
                                            
func (hm *HandlerManager) HandleFrostWithdrawMonitor(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("FrostWithdrawMonitor")

	q := r.URL.Query()
	chain := frostchain.NormalizeChain(q.Get("chain"))
	asset := strings.TrimSpace(q.Get("asset"))
	limit := parseFrostWithdrawMonitorLimit(strings.TrimSpace(q.Get("limit")))
	completedLimit := parseFrostWithdrawMonitorCompletedLimit(strings.TrimSpace(q.Get("completed_limit")))

	refs, err := hm.frostWithdrawMonitorQueues(chain, asset)
	if err != nil {
		http.Error(w, "scan queues failed", http.StatusInternalServerError)
		return
	}
	                                                                           
	                                                                         
	                                                                             
	                                                                              
	states := make([]*pb.FrostWithdrawState, 0, limit)
	activeScanBudget := maxFrostWithdrawMonitorScan
	for i, ref := range refs {
		if activeScanBudget <= 0 {
			break
		}
		quota := frostWithdrawMonitorQueueQuota(limit, len(refs), i)
		if quota <= 0 {
			continue
		}
		scanLimit := min(activeScanBudget, frostWithdrawMonitorCandidateScanLimit(quota))
		part, scanned, err := hm.scanFrostWithdrawMonitorQueue(ref, quota, scanLimit)
		activeScanBudget -= scanned
		if err != nil {
			http.Error(w, "scan queue failed", http.StatusInternalServerError)
			return
		}
		states = append(states, part...)
	}
	sort.SliceStable(states, func(i, j int) bool {
		if states[i].RequestHeight == states[j].RequestHeight {
			return states[i].WithdrawId < states[j].WithdrawId
		}
		return states[i].RequestHeight < states[j].RequestHeight
	})
	if len(states) > limit {
		states = states[:limit]
	}
	completed := make([]*pb.FrostWithdrawState, 0, completedLimit)
	completedScanBudget := maxFrostWithdrawMonitorScan
	for i, ref := range refs {
		if completedScanBudget <= 0 {
			break
		}
		quota := frostWithdrawMonitorQueueQuota(completedLimit, len(refs), i)
		if quota <= 0 {
			continue
		}
		scanLimit := min(completedScanBudget, frostWithdrawMonitorCandidateScanLimit(quota))
		part, scanned, err := hm.scanFrostWithdrawMonitorCompletedQueue(ref, quota, scanLimit)
		completedScanBudget -= scanned
		if err != nil {
			http.Error(w, "scan completed queue failed", http.StatusInternalServerError)
			return
		}
		completed = append(completed, part...)
	}
	sort.SliceStable(completed, func(i, j int) bool {
		if completed[i].RequestHeight == completed[j].RequestHeight {
			return completed[i].WithdrawId > completed[j].WithdrawId
		}
		return completed[i].RequestHeight > completed[j].RequestHeight
	})
	if len(completed) > completedLimit {
		completed = completed[:completedLimit]
	}
	states = append(states, completed...)

	respData, err := proto.Marshal(&pb.FrostWithdrawStateList{States: states})
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                          
func (hm *HandlerManager) HandleGetVaultGroupPubKey(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("GetVaultGroupPubKey")

	chain := r.URL.Query().Get("chain")
	vaultIDStr := r.URL.Query().Get("vault_id")

	if chain == "" {
		http.Error(w, "missing chain parameter", http.StatusBadRequest)
		return
	}

	vaultID, err := strconv.ParseUint(vaultIDStr, 10, 32)
	if err != nil {
		http.Error(w, "invalid vault_id", http.StatusBadRequest)
		return
	}

	                    
	vaultKey := keys.KeyFrostVaultState(chain, uint32(vaultID))
	data, err := hm.dbManager.Get(vaultKey)
	if err != nil || data == nil {
		http.Error(w, "vault not found", http.StatusNotFound)
		return
	}

	var vaultState pb.FrostVaultState
	if err := proto.Unmarshal(data, &vaultState); err != nil {
		http.Error(w, "invalid vault state data", http.StatusInternalServerError)
		return
	}

	                                           
	groupPubkeyHex := ""
	if len(vaultState.GroupPubkey) > 0 {
		groupPubkeyHex = hex.EncodeToString(vaultState.GroupPubkey)
	}

	resp := &pb.GetVaultGroupPubKeyResponse{
		Chain:              vaultState.Chain,
		VaultId:            vaultState.VaultId,
		KeyEpoch:           vaultState.KeyEpoch,
		GroupPubkey:        groupPubkeyHex,
		SignAlgo:           vaultState.SignAlgo.String(),
		Status:             vaultState.Status,
		DepositKeyEpoch:    vaultState.KeyEpoch,
		DepositGroupPubkey: groupPubkeyHex,
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                                           
func (hm *HandlerManager) HandleGetVaultConfig(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("GetVaultConfig")

	chain := strings.TrimSpace(r.URL.Query().Get("chain"))
	if chain == "" {
		http.Error(w, "missing chain parameter", http.StatusBadRequest)
		return
	}

	cfgKey := keys.KeyFrostVaultConfig(chain, 0)
	data, err := hm.dbManager.Get(cfgKey)
	if err != nil || data == nil {
		http.Error(w, "vault config not found", http.StatusNotFound)
		return
	}

	cfg := &pb.FrostVaultConfig{}
	if err := proto.Unmarshal(data, cfg); err != nil {
		http.Error(w, "invalid vault config data", http.StatusInternalServerError)
		return
	}

	respData, err := proto.Marshal(cfg)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                
func (hm *HandlerManager) HandleGetVaultTransitionStatus(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("GetVaultTransitionStatus")

	chain := r.URL.Query().Get("chain")
	vaultIDStr := r.URL.Query().Get("vault_id")
	epochIDStr := r.URL.Query().Get("epoch_id")

	if chain == "" {
		http.Error(w, "missing chain parameter", http.StatusBadRequest)
		return
	}

	vaultID, err := strconv.ParseUint(vaultIDStr, 10, 32)
	if err != nil {
		http.Error(w, "invalid vault_id", http.StatusBadRequest)
		return
	}

	                                                            
	var epochID uint64
	if epochIDStr != "" {
		epochID, err = strconv.ParseUint(epochIDStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid epoch_id", http.StatusBadRequest)
			return
		}
	} else {
		                                                                                    
		                                                                                
		                                                                                   
		vaultKey := keys.KeyFrostVaultState(chain, uint32(vaultID))
		data, err := hm.dbManager.Get(vaultKey)
		if err == nil && data != nil {
			var vaultState pb.FrostVaultState
			if err := proto.Unmarshal(data, &vaultState); err == nil {
				epochID = vaultState.KeyEpoch
				nextEpoch := vaultState.KeyEpoch + 1
				nextKey := keys.KeyFrostVaultTransition(chain, uint32(vaultID), nextEpoch)
				if nextData, nextErr := hm.dbManager.Get(nextKey); nextErr == nil && nextData != nil {
					epochID = nextEpoch
				}
			}
		}
		                                     
		if epochID == 0 {
			epochID = 1
		}
	}

	                         
	transitionKey := keys.KeyFrostVaultTransition(chain, uint32(vaultID), epochID)
	data, err := hm.dbManager.Get(transitionKey)
	if err != nil || data == nil {
		http.Error(w, "transition not found", http.StatusNotFound)
		return
	}

	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(data, &transition); err != nil {
		http.Error(w, "invalid transition state data", http.StatusInternalServerError)
		return
	}

	                              
	oldGroupPubkeyHex := ""
	if len(transition.OldGroupPubkey) > 0 {
		oldGroupPubkeyHex = hex.EncodeToString(transition.OldGroupPubkey)
	}
	newGroupPubkeyHex := ""
	if len(transition.NewGroupPubkey) > 0 {
		newGroupPubkeyHex = hex.EncodeToString(transition.NewGroupPubkey)
	}

	resp := &pb.GetVaultTransitionStatusResponse{
		Chain:                 transition.Chain,
		VaultId:               transition.VaultId,
		EpochId:               transition.EpochId,
		SignAlgo:              transition.SignAlgo.String(),
		TriggerHeight:         transition.TriggerHeight,
		OldCommitteeMembers:   transition.OldCommitteeMembers,
		NewCommitteeMembers:   transition.NewCommitteeMembers,
		DkgStatus:             transition.DkgStatus,
		DkgSessionId:          transition.DkgSessionId,
		DkgThresholdT:         transition.DkgThresholdT,
		DkgN:                  transition.DkgN,
		DkgCommitDeadline:     transition.DkgCommitDeadline,
		DkgDisputeDeadline:    transition.DkgDisputeDeadline,
		OldGroupPubkey:        oldGroupPubkeyHex,
		NewGroupPubkey:        newGroupPubkeyHex,
		ValidationStatus:      transition.ValidationStatus,
		Lifecycle:             transition.Lifecycle,
		MigrationJobId:        transition.MigrationJobId,
		MigrationStatus:       transition.MigrationStatus,
		MigrationTemplateHash: hex.EncodeToString(transition.MigrationTemplateHash),
		MigrationIntentHash:   hex.EncodeToString(transition.MigrationIntentHash),
		MigrationError:        transition.MigrationError,
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                             
                                                                         
func (hm *HandlerManager) HandleGetVaultMigration(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("GetVaultMigration")
	migrationID := strings.TrimSpace(r.URL.Query().Get("migration_id"))
	if migrationID == "" {
		http.Error(w, "missing migration_id", http.StatusBadRequest)
		return
	}
	data, found, err := hm.frostPermanentOrLegacy(keys.KeyFrostPermanentMigration(migrationID), keys.KeyArchiveFrostMigration(migrationID))
	if err != nil {
		http.Error(w, "migration record unavailable", http.StatusServiceUnavailable)
		return
	}
	if !found {
		                                                                          
		                                                                   
		stateRef, refErr := hm.dbManager.Get(keys.KeyFrostMigrationStateRef(migrationID))
		if refErr != nil || len(stateRef) == 0 {
			http.Error(w, "migration not found", http.StatusNotFound)
			return
		}
		stateData, stateErr := hm.dbManager.Get(strings.TrimSpace(string(stateRef)))
		if stateErr != nil || len(stateData) == 0 {
			http.Error(w, "migration state not found", http.StatusNotFound)
			return
		}
		var state pb.FrostVaultMigrationState
		if proto.Unmarshal(stateData, &state) != nil || state.MigrationId != migrationID {
			http.Error(w, "invalid migration state", http.StatusInternalServerError)
			return
		}
		data, err = proto.Marshal(&pb.FrostVaultMigrationArchive{State: &state})
		if err != nil {
			http.Error(w, "failed to marshal migration state", http.StatusInternalServerError)
			return
		}
	}
	var archive pb.FrostVaultMigrationArchive
	if err := proto.Unmarshal(data, &archive); err != nil || archive.State == nil || archive.State.MigrationId != migrationID {
		http.Error(w, "invalid migration archive", http.StatusInternalServerError)
		return
	}
	if len(archive.RawTx) == 0 {
		if artifactIDRaw, readErr := hm.dbManager.Get(keys.KeyFrostMigrationArtifact(migrationID)); readErr == nil {
			artifactID := strings.TrimSpace(string(artifactIDRaw))
			record, recordErr := artifact.ReadRecord(hm.frostResultGet, artifactID, maxFrostMigrationBundleRaw*2)
			if recordErr == nil {
				archive.RawTx = append([]byte(nil), record.RawTx...)
			} else if !errors.Is(recordErr, artifact.ErrRecordUnavailable) {
				http.Error(w, "invalid migration artifact", http.StatusInternalServerError)
				return
			}
		}
		data, _ = proto.Marshal(&archive)
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(data)
}

func (hm *HandlerManager) HandleFrostArchiveStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := hm.dbManager.FrostArchiveStatus()
	if err != nil {
		http.Error(w, "failed to read archive status", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

                                                                            
                                                                               
                                        
func (hm *HandlerManager) HandleDownloadVaultMigration(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("DownloadVaultMigration")
	migrationID := strings.TrimSpace(r.URL.Query().Get("migration_id"))
	if migrationID == "" {
		http.Error(w, "missing migration_id", http.StatusBadRequest)
		return
	}
	rootRaw, err := hm.dbManager.Get(keys.KeyFrostMigrationArtifact(migrationID))
	if err != nil || len(rootRaw) == 0 {
		http.Error(w, "signed migration artifact not found", http.StatusNotFound)
		return
	}
	rootID := strings.TrimSpace(string(rootRaw))
	ordered := make([]artifact.Record, 0, 8)
	totalRawBytes := 0
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("artifact dependency cycle at %s", id)
		}
		if err := r.Context().Err(); err != nil {
			return err
		}
		record, err := artifact.ReadRecord(hm.frostResultGet, id, maxFrostMigrationBundleRaw*2)
		if err != nil {
			return fmt.Errorf("artifact %s: %w", id, err)
		}
		visiting[id] = true
		for _, parentID := range record.Metadata.ParentIDs {
			if err := visit(parentID); err != nil {
				return err
			}
		}
		delete(visiting, id)
		if len(record.RawTx) > maxFrostMigrationBundleRaw-totalRawBytes {
			return fmt.Errorf("migration artifact bundle exceeds %d bytes", maxFrostMigrationBundleRaw)
		}
		totalRawBytes += len(record.RawTx)
		ordered = append(ordered, *record)
		visited[id] = true
		return nil
	}
	if err := visit(rootID); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, artifact.ErrRecordUnavailable) {
			status = http.StatusGone                                            
		} else if errors.Is(err, artifact.ErrRecordTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	bundle := &artifact.Bundle{Version: artifact.Version, MigrationID: migrationID, Artifacts: ordered}
	payload, err := artifact.MarshalBundle(bundle)
	if err != nil {
		http.Error(w, "failed to build migration bundle", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "frost-migration-"+migrationID+".json"))
	_, _ = w.Write(payload)
}

                                                            
func (hm *HandlerManager) HandleGetVaultDkgCommitments(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("GetVaultDkgCommitments")

	chain := r.URL.Query().Get("chain")
	vaultIDStr := r.URL.Query().Get("vault_id")
	epochIDStr := r.URL.Query().Get("epoch_id")

	if chain == "" {
		http.Error(w, "missing chain parameter", http.StatusBadRequest)
		return
	}

	vaultID, err := strconv.ParseUint(vaultIDStr, 10, 32)
	if err != nil {
		http.Error(w, "invalid vault_id", http.StatusBadRequest)
		return
	}

	epochID, err := strconv.ParseUint(epochIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid epoch_id", http.StatusBadRequest)
		return
	}
	vaultIDU32 := uint32(vaultID)

	                                                                                          
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		transitionKey := keys.KeyFrostVaultTransition(chain, vaultIDU32, epochID)
		if transitionData, err := hm.dbManager.Get(transitionKey); err == nil && len(transitionData) > 0 {
			var transition pb.VaultTransitionState
			if err := proto.Unmarshal(transitionData, &transition); err == nil {
				sessionID = strings.TrimSpace(transition.DkgSessionId)
			}
		}
	}

	if sessionID == "" {
		http.Error(w, "DKG session not found", http.StatusNotFound)
		return
	}
	if len(sessionID) > 256 {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}
	limit := boundedPositiveInt(r.URL.Query().Get("limit"), defaultFrostDkgCommitLimit, maxFrostDkgCommitLimit)
	prefix := keys.KeyFrostVaultDkgCommitPrefix(chain, vaultIDU32, epochID, sessionID)
	rows, err := hm.dbManager.ScanKVWithLimitOrdered(prefix, limit, false)
	if err != nil {
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}

	commitments := make([]*pb.DkgCommitmentInfo, 0, len(rows))
	for _, row := range rows {
		var commit pb.FrostVaultDkgCommitment
		if err := proto.Unmarshal(row.Value, &commit); err != nil {
			continue
		}

		                                                   
		pointsHex := make([]string, len(commit.CommitmentPoints))
		for i, point := range commit.CommitmentPoints {
			pointsHex[i] = hex.EncodeToString(point)
		}

		commitments = append(commitments, &pb.DkgCommitmentInfo{
			Participant:      commit.MinerAddress,
			CommitmentPoints: pointsHex,
			CommitHeight:     commit.CommitHeight,
		})
	}

	resp := &pb.GetVaultDkgCommitmentsResponse{
		Chain:       chain,
		VaultId:     vaultIDU32,
		EpochId:     epochID,
		Commitments: commitments,
		Total:       int32(len(commitments)),
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                             
                                                                               
                                                                            
func (hm *HandlerManager) HandleFrostDkgProgress(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("FrostDkgProgress")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	chain := frostchain.NormalizeChain(r.URL.Query().Get("chain"))
	if !frostchain.IsValidChain(chain) {
		http.Error(w, "invalid chain", http.StatusBadRequest)
		return
	}
	vaultID64, err := strconv.ParseUint(r.URL.Query().Get("vault_id"), 10, 32)
	if err != nil {
		http.Error(w, "invalid vault_id", http.StatusBadRequest)
		return
	}
	epochID, err := strconv.ParseUint(r.URL.Query().Get("epoch_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid epoch_id", http.StatusBadRequest)
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" || len(sessionID) > 256 {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}

	vaultID := uint32(vaultID64)
	raw, err := hm.dbManager.Get(keys.KeyFrostVaultTransition(chain, vaultID, epochID))
	if err != nil {
		http.Error(w, "read transition failed", http.StatusInternalServerError)
		return
	}
	if len(raw) == 0 {
		http.Error(w, "transition not found", http.StatusNotFound)
		return
	}
	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(raw, &transition); err != nil {
		http.Error(w, "decode transition failed", http.StatusInternalServerError)
		return
	}
	if transition.DkgSessionId != sessionID {
		http.Error(w, "session_id does not match current attempt", http.StatusConflict)
		return
	}
	n := len(transition.NewCommitteeMembers)
	if n > maxFrostDkgProgressCommittee {
		http.Error(w, "committee exceeds progress query limit", http.StatusUnprocessableEntity)
		return
	}
	shareExpected := n * (n - 1)
	if shareExpected < 0 {
		shareExpected = 0
	}
	response := frostDkgProgressResponse{
		Chain: chain, VaultID: vaultID, EpochID: epochID, SessionID: sessionID,
		DkgStatus: transition.DkgStatus, ValidationStatus: transition.ValidationStatus,
		CommitExpected: n, ShareExpected: shareExpected, ValidationAckExpected: n,
		Participants: make([]frostDkgParticipantProgress, 0, n),
	}
	for _, member := range transition.NewCommitteeMembers {
		response.Participants = append(response.Participants, frostDkgParticipantProgress{
			Address: member, SharesExpected: max(0, n-1),
		})
	}

	                                                                            
	                                                              
	response.TranscriptAvailable = transition.DkgStatus == "NOT_STARTED" ||
		transition.DkgStatus == "COMMITTING" || transition.DkgStatus == "SHARING" ||
		transition.DkgStatus == "RESOLVING" || transition.ValidationStatus == "SIGNING"
	if response.TranscriptAvailable {
		participantIndex := make(map[string]int, n)
		for i := range response.Participants {
			participantIndex[response.Participants[i].Address] = i
		}
		for member, i := range participantIndex {
			commit, readErr := hm.dbManager.Get(keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, member))
			if readErr == nil && len(commit) > 0 {
				response.Participants[i].Committed = true
				response.CommitCount++
			}
			ack, readErr := hm.dbManager.Get(keys.KeyFrostVaultDkgValidationAck(chain, vaultID, epochID, sessionID, member))
			if readErr == nil && len(ack) > 0 {
				response.Participants[i].ValidationAck = true
				response.ValidationAckCount++
			}
		}

		shareRows, scanErr := hm.dbManager.ScanKVWithLimitOrdered(
			keys.KeyFrostVaultDkgSharePrefix(chain, vaultID, epochID, sessionID), shareExpected+1, false,
		)
		if scanErr != nil {
			http.Error(w, "scan DKG shares failed", http.StatusInternalServerError)
			return
		}
		for _, row := range shareRows {
			var share pb.FrostVaultDkgShare
			if proto.Unmarshal(row.Value, &share) != nil || share.DealerId == share.ReceiverId {
				continue
			}
			i, dealerKnown := participantIndex[share.DealerId]
			_, receiverKnown := participantIndex[share.ReceiverId]
			if !dealerKnown || !receiverKnown {
				continue
			}
			response.ShareCount++
			response.Participants[i].SharesSubmitted++
		}

		complaintRows, scanErr := hm.dbManager.ScanKVWithLimitOrdered(
			keys.KeyFrostVaultDkgComplaintPrefix(chain, vaultID, epochID, sessionID), max(1, shareExpected+1), false,
		)
		if scanErr != nil {
			http.Error(w, "scan DKG complaints failed", http.StatusInternalServerError)
			return
		}
		for _, row := range complaintRows {
			var complaint pb.FrostVaultDkgComplaint
			if proto.Unmarshal(row.Value, &complaint) != nil {
				continue
			}
			response.ComplaintCount++
			switch complaint.Status {
			case "PENDING":
				response.PendingComplaints++
			case "RESOLVED":
				response.ResolvedComplaints++
			case "DISQUALIFIED":
				response.DisqualifiedComplaints++
			}
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		return
	}
}

                                                                      
                               
func (hm *HandlerManager) HandleFrostWithdrawList(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("FrostWithdrawList")

	q := r.URL.Query()
	chain := strings.TrimSpace(q.Get("chain"))
	asset := strings.TrimSpace(q.Get("asset"))
	var vaultID uint32
	filterVault := false
	if raw := strings.TrimSpace(q.Get("vault_id")); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			http.Error(w, "invalid vault_id", http.StatusBadRequest)
			return
		}
		vaultID = uint32(v)
		filterVault = true
	}
	if filterVault && chain == "" {
		http.Error(w, "chain is required with vault_id", http.StatusBadRequest)
		return
	}
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			offset = min(v, maxFrostWithdrawQueryOffset)
		}
	}
	limit := boundedPositiveInt(q.Get("limit"), defaultFrostWithdrawQueryLimit, maxFrostWithdrawQueryLimit)
	reverse := strings.EqualFold(strings.TrimSpace(q.Get("order")), "asc")

	prefix := keys.KeyFrostWithdrawQueryGlobalPrefix()
	if chain != "" {
		prefix = keys.KeyFrostWithdrawQueryChainPrefix(chain)
	}
	if filterVault {
		prefix = keys.KeyFrostWithdrawQueryChainVaultPrefix(chain, vaultID)
	}
	readBudget := offset + limit
	if asset != "" {
		readBudget = maxFrostWithdrawFilterReads
	}
	states, err := hm.scanFrostWithdrawStates(prefix, reverse, offset, limit, readBudget, func(state *pb.FrostWithdrawState) bool {
		return (chain == "" || strings.EqualFold(state.Chain, chain)) &&
			(asset == "" || strings.EqualFold(state.Asset, asset)) &&
			(!filterVault || state.VaultId == vaultID)
	})
	if err != nil {
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}
	for _, state := range states {
		state.PlanningLogs = mergeAndSortPlanningLogs(state.PlanningLogs, hm.collectLocalPlanningLogs(state.WithdrawId))
	}

	resp := &pb.FrostWithdrawStateList{
		States: states,
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

type rechargeQueryRef struct {
	indexKey string
	orderKey string
	request  *pb.RechargeRequest
}

const rechargeQueryRepairReadAllowance = 512

                                                                            
                                                                              
                                                                               
func (hm *HandlerManager) scanRechargeQueryRefs(chain string, filterVault bool, vaultID uint32, terminal bool, after string, want int, reverse bool) ([]rechargeQueryRef, error) {
	prefix := rechargeQueryPrefix(chain, filterVault, vaultID, terminal)
	refs := make([]rechargeQueryRef, 0, want)
	seen := make(map[string]struct{}, want)
	budget := want + rechargeQueryRepairReadAllowance
	for remaining := budget; remaining > 0 && len(refs) < want; {
		batchSize := min(128, remaining)
		rows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(prefix, after, batchSize, reverse)
		if err != nil {
			return nil, err
		}
		remaining -= len(rows)
		for _, row := range rows {
			after = row.Key
			canonical := string(row.Value)
			if !strings.HasPrefix(canonical, keys.KeyRechargeRequestPrefix()) {
				continue
			}
			raw, exists, err := hm.frostResultGet(canonical)
			if err != nil {
				return nil, err
			}
			var req pb.RechargeRequest
			if !exists || proto.Unmarshal(raw, &req) != nil || req.RequestId == "" || canonical != keys.KeyRechargeRequest(req.RequestId) {
				continue
			}
			isTerminal := req.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED || req.Status == pb.RechargeRequestStatus_RECHARGE_REJECTED
			if isTerminal != terminal || (chain != "" && !strings.EqualFold(strings.TrimSpace(req.NativeChain), chain)) || (filterVault && req.VaultId != vaultID) {
				continue
			}
			height := req.CreateHeight
			if terminal && req.FinalizeHeight != 0 {
				height = req.FinalizeHeight
			}
			expected := keys.KeyRechargeQueryRecentGlobal(terminal, height, req.RequestId)
			switch {
			case chain != "" && filterVault:
				expected = keys.KeyRechargeQueryRecentChainVault(req.NativeChain, req.VaultId, terminal, height, req.RequestId)
			case chain != "":
				expected = keys.KeyRechargeQueryRecentChain(req.NativeChain, terminal, height, req.RequestId)
			case filterVault:
				expected = keys.KeyRechargeQueryRecentVault(req.VaultId, terminal, height, req.RequestId)
			}
			if row.Key != expected {
				continue
			}
			if _, duplicate := seen[req.RequestId]; duplicate {
				continue
			}
			seen[req.RequestId] = struct{}{}
			_, orderKey, _ := strings.Cut(row.Key, "|height:")
			refs = append(refs, rechargeQueryRef{indexKey: row.Key, orderKey: orderKey, request: &req})
			if len(refs) == want {
				return refs, nil
			}
		}
		if len(rows) < batchSize {
			return refs, nil
		}
	}
	return nil, fmt.Errorf("recharge query index repair required: bounded scan exhausted after %d rows", budget)
}

func rechargeQueryPrefix(chain string, filterVault bool, vaultID uint32, terminal bool) string {
	switch {
	case chain != "" && filterVault:
		return keys.KeyRechargeQueryRecentChainVaultPrefix(chain, vaultID, terminal)
	case chain != "":
		return keys.KeyRechargeQueryRecentChainPrefix(chain, terminal)
	case filterVault:
		return keys.KeyRechargeQueryRecentVaultPrefix(vaultID, terminal)
	default:
		return keys.KeyRechargeQueryRecentGlobalPrefix(terminal)
	}
}

func (hm *HandlerManager) writeRechargeRequestList(w http.ResponseWriter, requests ...*pb.RechargeRequest) {
	resp := &pb.RechargeRequestList{Requests: requests}
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(respData)
}

func (hm *HandlerManager) handleRechargeEventLookup(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if !q.Has("native_tx_hash") {
		return false
	}
	if q.Has("request_id") {
		http.Error(w, "request_id and native_tx_hash are mutually exclusive", http.StatusBadRequest)
		return true
	}
	chain := frostchain.NormalizeChain(strings.TrimSpace(q.Get("chain")))
	if !frostchain.IsValidChain(chain) {
		http.Error(w, "invalid chain", http.StatusBadRequest)
		return true
	}
	txHash, err := protocol.CanonicalExternalTxHash(chain, q.Get("native_tx_hash"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}

	var referenceKeys []string
	var eventIndex, nativeVout uint64
	if chain == frostchain.ChainBTC {
		if !q.Has("native_vout") {
			http.Error(w, "native_vout is required for BTC", http.StatusBadRequest)
			return true
		}
		nativeVout, err = strconv.ParseUint(strings.TrimSpace(q.Get("native_vout")), 10, 32)
		if err != nil {
			http.Error(w, "invalid native_vout", http.StatusBadRequest)
			return true
		}
		referenceKeys = []string{keys.KeyRechargeRequestByNativeTx(chain, txHash+":"+strconv.FormatUint(nativeVout, 10))}
	} else {
		if !q.Has("deposit_event_index") {
			http.Error(w, "deposit_event_index is required for account chains", http.StatusBadRequest)
			return true
		}
		eventIndex, err = strconv.ParseUint(strings.TrimSpace(q.Get("deposit_event_index")), 10, 32)
		if err != nil {
			http.Error(w, "invalid deposit_event_index", http.StatusBadRequest)
			return true
		}
		referenceKeys = []string{
			keys.KeyRechargeEvent(chain, txHash, uint32(eventIndex)),
			keys.KeyRechargeEventPending(chain, txHash, uint32(eventIndex)),
		}
	}

	requestID := ""
	for _, key := range referenceKeys {
		raw, exists, getErr := hm.frostResultGet(key)
		if getErr != nil {
			http.Error(w, "failed to read recharge event index", http.StatusInternalServerError)
			return true
		}
		if exists && len(raw) > 0 {
			requestID = string(raw)
			break
		}
	}
	if requestID == "" {
		hm.writeRechargeRequestList(w)
		return true
	}
	raw, exists, err := hm.frostResultGet(keys.KeyRechargeRequest(requestID))
	if err != nil {
		http.Error(w, "failed to read request", http.StatusInternalServerError)
		return true
	}
	if !exists {
		http.Error(w, "recharge event index points to a missing request", http.StatusInternalServerError)
		return true
	}
	var req pb.RechargeRequest
	if err := proto.Unmarshal(raw, &req); err != nil || req.RequestId != requestID ||
		!strings.EqualFold(req.NativeChain, chain) || req.NativeTxHash != txHash ||
		(chain == frostchain.ChainBTC && uint64(req.NativeVout) != nativeVout) ||
		(chain != frostchain.ChainBTC && uint64(req.DepositEventIndex) != eventIndex) {
		http.Error(w, "recharge event index is inconsistent", http.StatusInternalServerError)
		return true
	}
	hm.writeRechargeRequestList(w, hm.hydrateArchivedRechargeRequest(&req))
	return true
}

                                                                                   
                                                                              
                                                                                
func (hm *HandlerManager) HandleWitnessRequests(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("WitnessRequests")
	if r.URL.Query().Get("active") == "1" {
		hm.handleActiveWitnessRequests(w, r)
		return
	}

	q := r.URL.Query()
	if hm.handleRechargeEventLookup(w, r) {
		return
	}
	if q.Has("request_id") {
		requestID := strings.TrimSpace(q.Get("request_id"))
		if requestID == "" || len(requestID) > 256 {
			http.Error(w, "invalid request_id", http.StatusBadRequest)
			return
		}
		                                                                     
		                                                                        
		raw, exists, err := hm.frostResultGet(keys.KeyRechargeRequest(requestID))
		if err != nil {
			http.Error(w, "failed to read request", http.StatusInternalServerError)
			return
		}
		resp := &pb.RechargeRequestList{}
		if exists {
			var req pb.RechargeRequest
			if err := proto.Unmarshal(raw, &req); err != nil || req.RequestId != requestID {
				http.Error(w, "failed to parse request", http.StatusInternalServerError)
				return
			}
			resp.Requests = []*pb.RechargeRequest{hm.hydrateArchivedRechargeRequest(&req)}
		}
		hm.writeRechargeRequestList(w, resp.Requests...)
		return
	}
	chain := strings.TrimSpace(q.Get("chain"))
	var vaultID uint32
	filterVault := false
	if raw := strings.TrimSpace(q.Get("vault_id")); raw != "" {
		if v, err := strconv.ParseUint(raw, 10, 32); err == nil {
			vaultID = uint32(v)
			filterVault = true
		}
	}
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			if v > 5_000 {
				v = 5_000
			}
			offset = v
		}
	}
	orderDesc := !strings.EqualFold(strings.TrimSpace(q.Get("order")), "asc")
	limitStr := q.Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			if l > 500 {
				l = 500
			}
			limit = l
		}
	}

	scanBudget := offset + limit
	refs := make([]rechargeQueryRef, 0, scanBudget*2)
	for _, terminal := range []bool{false, true} {
		rows, err := hm.scanRechargeQueryRefs(chain, filterVault, vaultID, terminal, "", scanBudget, !orderDesc)
		if err != nil {
			http.Error(w, "recharge query unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		refs = append(refs, rows...)
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].orderKey != refs[j].orderKey {
			if orderDesc {
				return refs[i].orderKey < refs[j].orderKey
			}
			return refs[i].orderKey > refs[j].orderKey
		}
		if orderDesc {
			return refs[i].indexKey > refs[j].indexKey
		}
		return refs[i].indexKey < refs[j].indexKey
	})
	                                                                         
	                                                                        
	unique := refs[:0]
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, duplicate := seen[ref.request.RequestId]; duplicate {
			continue
		}
		seen[ref.request.RequestId] = struct{}{}
		unique = append(unique, ref)
	}
	refs = unique
	if offset > len(refs) {
		offset = len(refs)
	}
	pageRefs := refs[offset:]
	if len(pageRefs) > limit {
		pageRefs = pageRefs[:limit]
	}
	requests := make([]*pb.RechargeRequest, 0, len(pageRefs))
	for _, ref := range pageRefs {
		requests = append(requests, hm.hydrateArchivedRechargeRequest(ref.request))
	}

	resp := &pb.RechargeRequestList{
		Requests: requests,
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                                                
                                                                
            
                                                                              
                                                                                                     
  
                                                                                                       
                                                                                                   
func (hm *HandlerManager) HandleWitnessMyRequests(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("WitnessMyRequests")

	witnessAddr := r.URL.Query().Get("witness")
	if witnessAddr == "" {
		http.Error(w, "witness parameter required", http.StatusBadRequest)
		return
	}
	includeHistoryRaw := strings.TrimSpace(r.URL.Query().Get("include_history"))
	includeHistory := includeHistoryRaw == "1" || strings.EqualFold(includeHistoryRaw, "true")
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	seen := make(map[string]struct{})
	requests := make([]*pb.RechargeRequest, 0, limit)

	                                                                     
	                                                                           
	assigned, err := hm.assignedWitnessRequests(witnessAddr, includeHistory, limit)
	if err != nil {
		http.Error(w, "witness assignment query unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	for _, req := range assigned {
		seen[req.RequestId] = struct{}{}
		requests = append(requests, req)
	}

	                                                                                                              
	remaining := limit - len(requests)
	if remaining > 0 {
		challengeRefs, err := hm.dbManager.ScanKVWithLimit(keys.KeyArbitratorChallengePrefix(witnessAddr), remaining)
		if err == nil {
			for _, challengeIDBytes := range challengeRefs {
				data, getErr := hm.dbManager.Get(keys.KeyChallengeRecord(string(challengeIDBytes)))
				if getErr != nil || len(data) == 0 {
					continue
				}
				var rec pb.ChallengeRecord
				if err := proto.Unmarshal(data, &rec); err != nil {
					continue
				}
				if rec.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || rec.Finalized {
					continue
				}
				isArb := false
				for _, a := range rec.Arbitrators {
					if a == witnessAddr {
						isArb = true
						break
					}
				}
				if !isArb {
					continue
				}
				                     
				if v, _ := hm.dbManager.Get(keys.KeyArbitrationVote(rec.ChallengeId, witnessAddr)); len(v) > 0 {
					continue
				}
				if _, dup := seen[rec.RequestId]; dup {
					continue
				}
				reqData, err := hm.dbManager.Get(keys.KeyRechargeRequest(rec.RequestId))
				if err != nil || reqData == nil {
					continue
				}
				var req pb.RechargeRequest
				if err := proto.Unmarshal(reqData, &req); err != nil {
					continue
				}
				seen[req.RequestId] = struct{}{}
				requests = append(requests, proto.Clone(&req).(*pb.RechargeRequest))
				if len(requests) >= limit {
					break
				}
			}
		}
	}

	respData, err := proto.Marshal(&pb.RechargeRequestList{Requests: requests})
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                      
                          
func (hm *HandlerManager) HandleFrostDkgList(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("FrostDkgList")

	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > 500 {
				parsed = 500
			}
			limit = parsed
		}
	}
	rows, err := hm.dbManager.ScanKVWithLimitOrdered(keys.KeyFrostVaultTransitionPrefix(), limit, false)
	if err != nil {
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}

	states := make([]*pb.VaultTransitionState, 0, len(rows))
	for _, row := range rows {
		var state pb.VaultTransitionState
		if err := proto.Unmarshal(row.Value, &state); err != nil {
			continue
		}

		states = append(states, &state)
	}

	                                               
	for _, state := range states {
		if state.DkgStatus == "KEY_READY" || state.DkgStatus == "" {
			                                                             
			                                                                                 
			searchPrefix := fmt.Sprintf("v1_frost_signed_pkg_migration_%s_%d_%d_", state.Chain, state.VaultId, state.EpochId)
			res, err := hm.dbManager.ScanKVWithLimitOrdered(searchPrefix, 1, false)
			if err == nil {
				for _, row := range res {
					rk := row.Key
					if strings.HasPrefix(rk, "v1_frost_signed_pkg_") {
						parts := strings.Split(rk, "_")
						if len(parts) >= 10 {
							                                       
							jobParts := parts[4 : len(parts)-1]
							state.MigrationJobId = strings.Join(jobParts, "_")
							break
						}
					}
				}
			}
		}
	}

	resp := &pb.VaultTransitionStateList{
		States: states,
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                                      
                            
func (hm *HandlerManager) HandleFrostVaultList(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("FrostVaultList")

	limit := boundedPositiveInt(r.URL.Query().Get("limit"), defaultFrostVaultListLimit, maxFrostVaultListLimit)
	rows, err := hm.dbManager.ScanKVWithLimitOrdered(keys.KeyFrostVaultStatePrefix(), limit, false)
	if err != nil {
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}

	states := make([]*pb.FrostVaultState, 0, len(rows))
	for _, row := range rows {
		var state pb.FrostVaultState
		if err := proto.Unmarshal(row.Value, &state); err != nil {
			continue
		}
		states = append(states, &state)
	}

	respData, err := proto.Marshal(&pb.FrostVaultStateList{States: states})
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

type FrostVaultFundItem struct {
	Chain            string `json:"chain"`
	Asset            string `json:"asset"`
	VaultID          uint32 `json:"vault_id"`
	TotalBalance     string `json:"total_balance,omitempty"`
	AvailableBalance string `json:"available_balance"`
}

type FrostVaultFundsResponse struct {
	Funds []FrostVaultFundItem `json:"funds"`
}

func parseFrostVaultAvailableKey(key, chainFilter string) (string, string, uint32, bool) {
	chain := strings.ToLower(strings.TrimSpace(chainFilter))
	prefix := keys.KeyFrostVaultAvailableAllPrefix()
	if chain != "" {
		prefix = keys.KeyFrostVaultAvailablePrefix(chain)
	}
	if !strings.HasPrefix(key, prefix) {
		return "", "", 0, false
	}
	rest := strings.TrimPrefix(key, prefix)
	if chain == "" {
		idx := strings.Index(rest, "_")
		if idx <= 0 {
			return "", "", 0, false
		}
		chain = rest[:idx]
		rest = rest[idx+1:]
	}
	idx := strings.LastIndex(rest, "_")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", 0, false
	}
	vaultID, err := strconv.ParseUint(rest[idx+1:], 10, 32)
	if err != nil {
		return "", "", 0, false
	}
	return chain, rest[:idx], uint32(vaultID), true
}

func parseFrostVaultFundsLedgerKey(key, chainFilter string) (string, string, uint32, bool) {
	chain := strings.ToLower(strings.TrimSpace(chainFilter))
	prefix := keys.KeyFrostVaultFundsLedgerAllPrefix()
	if chain != "" {
		prefix = keys.KeyFrostVaultFundsLedgerPrefix(chain)
	}
	if !strings.HasPrefix(key, prefix) {
		return "", "", 0, false
	}
	rest := strings.TrimPrefix(key, prefix)
	if chain == "" {
		idx := strings.Index(rest, "_")
		if idx <= 0 {
			return "", "", 0, false
		}
		chain = rest[:idx]
		rest = rest[idx+1:]
	}
	idx := strings.LastIndex(rest, "_")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", 0, false
	}
	vaultID, err := strconv.ParseUint(rest[idx+1:], 10, 32)
	if err != nil {
		return "", "", 0, false
	}
	return chain, rest[:idx], uint32(vaultID), true
}

func dkgVaultFundKey(chain string, vaultID uint32, asset string) string {
	return fmt.Sprintf("%s:%d:%s", strings.ToUpper(strings.TrimSpace(chain)), vaultID, asset)
}

                                                                 
                              
func (hm *HandlerManager) HandleFrostVaultFunds(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("FrostVaultFunds")

	chain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	limit := boundedPositiveInt(r.URL.Query().Get("limit"), defaultFrostVaultFundsLimit, maxFrostVaultFundsLimit)
	availablePrefix := keys.KeyFrostVaultAvailableAllPrefix()
	ledgerPrefix := keys.KeyFrostVaultFundsLedgerAllPrefix()
	if chain != "" {
		availablePrefix = keys.KeyFrostVaultAvailablePrefix(chain)
		ledgerPrefix = keys.KeyFrostVaultFundsLedgerPrefix(chain)
	}
	availableRows, err := hm.dbManager.ScanKVWithLimitOrdered(availablePrefix, limit, false)
	if err != nil {
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}
	ledgerRows, err := hm.dbManager.ScanKVWithLimitOrdered(ledgerPrefix, limit, false)
	if err != nil {
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}

	byFundKey := make(map[string]FrostVaultFundItem, len(availableRows)+len(ledgerRows))
	for _, row := range availableRows {
		parsedChain, asset, vaultID, ok := parseFrostVaultAvailableKey(row.Key, chain)
		if !ok {
			continue
		}
		key := dkgVaultFundKey(parsedChain, vaultID, asset)
		item := byFundKey[key]
		item.Chain = parsedChain
		item.Asset = asset
		item.VaultID = vaultID
		item.AvailableBalance = strings.TrimSpace(string(row.Value))
		byFundKey[key] = item
	}
	for _, row := range ledgerRows {
		parsedChain, asset, vaultID, ok := parseFrostVaultFundsLedgerKey(row.Key, chain)
		if !ok {
			continue
		}
		key := dkgVaultFundKey(parsedChain, vaultID, asset)
		item := byFundKey[key]
		item.Chain = parsedChain
		item.Asset = asset
		item.VaultID = vaultID
		item.TotalBalance = strings.TrimSpace(string(row.Value))
		byFundKey[key] = item
	}

	orderedKeys := make([]string, 0, len(byFundKey))
	for key := range byFundKey {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	if len(orderedKeys) > limit {
		orderedKeys = orderedKeys[:limit]
	}
	funds := make([]FrostVaultFundItem, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		item := byFundKey[key]
		if item.AvailableBalance == "" {
			item.AvailableBalance = "0"
		}
		funds = append(funds, item)
	}

	                                                                           
	                                                                             
	                                                                           

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FrostVaultFundsResponse{Funds: funds})
}

                                              
func (hm *HandlerManager) HandleDownloadSignedPackage(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("DownloadSignedPackage")

	jobID := r.URL.Query().Get("job_id")
	idxStr := r.URL.Query().Get("idx")

	if jobID == "" {
		http.Error(w, "missing job_id parameter", http.StatusBadRequest)
		return
	}

	idx := uint64(0)
	if idxStr != "" {
		var err error
		idx, err = strconv.ParseUint(idxStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid idx", http.StatusBadRequest)
			return
		}
	}

	                                                                        
	                                                                     
	data, permanent, err := hm.frostResultGet(keys.KeyFrostPermanentSignedPackage(jobID, idx))
	if err != nil {
		http.Error(w, "signed package unavailable", http.StatusServiceUnavailable)
		return
	}
	found := permanent
	if !permanent {
		data, found, err = hm.frostPermanentOrLegacy(keys.KeyFrostSignedPackage(jobID, idx), keys.KeyArchiveFrostSignedPackage(jobID, idx))
		if err != nil {
			http.Error(w, "signed package unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	if !found {
		http.Error(w, "signed package not found", http.StatusNotFound)
		return
	}

	signedPkg := new(pb.FrostSignedPackage)
	if len(data) == 0 || proto.Unmarshal(data, signedPkg) != nil || signedPkg.JobId != jobID || signedPkg.Idx != idx {
		http.Error(w, "invalid signed package data", http.StatusInternalServerError)
		return
	}

	                                                                        
	if len(signedPkg.SignedPackageBytes) == 0 && !permanent {
		archivedData, exists, err := hm.frostResultGet(keys.KeyArchiveFrostSignedPackage(jobID, idx))
		if err != nil {
			http.Error(w, "signed package archive unavailable", http.StatusServiceUnavailable)
			return
		}
		if exists {
			var archived pb.FrostSignedPackage
			if len(archivedData) == 0 || proto.Unmarshal(archivedData, &archived) != nil || archived.JobId != jobID || archived.Idx != idx {
				http.Error(w, "invalid signed package archive", http.StatusInternalServerError)
				return
			}
			signedPkg = &archived
		}
	}
	if len(signedPkg.SignedPackageBytes) == 0 {
		status := http.StatusGone
		if permanent {
			status = http.StatusInternalServerError
		}
		http.Error(w, "signed package bytes unavailable", status)
		return
	}

	resp := &pb.DownloadSignedPackageResponse{
		JobId:              signedPkg.JobId,
		Idx:                signedPkg.Idx,
		SignedPackageBytes: hex.EncodeToString(signedPkg.SignedPackageBytes),
		SubmitHeight:       signedPkg.SubmitHeight,
		Submitter:          signedPkg.Submitter,
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                         
                        
func (hm *HandlerManager) HandleWitnessList(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("WitnessList")
	limit := 500
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > 500 {
				parsed = 500
			}
			limit = parsed
		}
	}

	                              
	                         
	prefix := keys.KeyWitnessInfoPrefix()

	startAfter := ""
	if after := strings.TrimSpace(r.URL.Query().Get("after")); after != "" {
		startAfter = keys.KeyWitnessInfo(after)
	}
	rows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(prefix, startAfter, limit, false)
	if err != nil {
		http.Error(w, "scan failed", http.StatusInternalServerError)
		return
	}

	                                                                                                      
	witnesses := make([]*pb.WitnessInfo, 0, len(rows))
	for _, row := range rows {
		var info pb.WitnessInfo
		if err := proto.Unmarshal(row.Value, &info); err != nil {
			continue
		}

		                                     
		witnesses = append(witnesses, &info)
	}

	resp := &pb.WitnessInfoList{
		Witnesses: witnesses,
	}

	                        
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}

                                                                                                   
                          
func (hm *HandlerManager) HandleWitnessConfig(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("WitnessConfig")
	if hm.cfg == nil {
		http.Error(w, "config unavailable", http.StatusInternalServerError)
		return
	}
	wc := hm.cfg.Witness
	resp := map[string]any{
		"unstake_lock_blocks":             wc.UnstakeLockBlocks,
		"arbitration_period_blocks":       wc.ArbitrationPeriodBlocks,
		"arbitration_timing_profile":      wc.ArbitrationTimingProfile,
		"retry_interval_blocks":           wc.RetryIntervalBlocks,
		"vote_grace_period_blocks":        wc.VoteGracePeriodBlocks,
		"max_shelve_wait_blocks":          wc.MaxShelveWaitBlocks,
		"min_stake_amount":                wc.MinStakeAmount,
		"challenge_stake_amount":          wc.ChallengeStakeAmount,
		"challenge_stake_ratio_bps":       wc.ChallengeStakeRatioBps,
		"challenge_arbitration_base_cost": wc.ChallengeArbitrationBaseCost,
		"max_challenge_stake_amount":      wc.MaxChallengeStakeAmount,
		"initial_witness_count":           wc.InitialWitnessCount,
		"min_selected_witness_count":      wc.MinSelectedWitnessCount,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

                                                                            
                              
func (hm *HandlerManager) HandleWitnessChallengeQuote(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("WitnessChallengeQuote")
	if hm.cfg == nil {
		http.Error(w, "config unavailable", http.StatusInternalServerError)
		return
	}
	requestID := r.URL.Query().Get("request_id")
	if requestID == "" {
		requestID = r.URL.Query().Get("requestId")
	}
	if requestID == "" {
		http.Error(w, "request_id required", http.StatusBadRequest)
		return
	}

	rawReq, err := hm.dbManager.Get(keys.KeyRechargeRequest(requestID))
	if err != nil || len(rawReq) == 0 {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}
	var req pb.RechargeRequest
	if err := proto.Unmarshal(rawReq, &req); err != nil {
		http.Error(w, "failed to parse request", http.StatusInternalServerError)
		return
	}
	reqPtr := hm.hydrateArchivedRechargeRequest(&req)

	stakes := make([]string, 0, len(reqPtr.CaseCollateral))
	seen := make(map[string]bool)
	for _, bond := range reqPtr.CaseCollateral {
		if bond != nil && bond.RoleMask&1 != 0 && !seen[bond.Address] {
			seen[bond.Address] = true
			stakes = append(stakes, bond.OriginalAmount)
		}
	}
	if len(reqPtr.CaseCollateral) == 0 {
		for _, addr := range reqPtr.SelectedWitnesses {
			if addr != "" && !seen[addr] {
				seen[addr] = true
				stakes = append(stakes, strconv.FormatInt(protocol.WitnessCaseBondRaw, 10))
			}
		}
	}

	quote, err := witness.CalculateChallengeStakeRequirement(handlerWitnessConfig(hm.cfg.Witness), stakes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	currentStake := big.NewInt(0)
	if reqPtr.ChallengeId != "" {
		if rawChallenge, err := hm.dbManager.Get(keys.KeyChallengeRecord(reqPtr.ChallengeId)); err == nil && len(rawChallenge) > 0 {
			var rec pb.ChallengeRecord
			if err := proto.Unmarshal(rawChallenge, &rec); err == nil {
				recPtr := hm.hydrateArchivedChallengeRecord(&rec)
				if rec.RequiredStake != "" {
					quote.RequiredChallengeStake = rec.RequiredStake
				}
				if v, ok := new(big.Int).SetString(rec.StakeAmount, 10); ok && v.Sign() > 0 {
					currentStake = v
				} else if v, ok := new(big.Int).SetString(recPtr.StakeAmount, 10); ok && v.Sign() > 0 && !recPtr.Finalized && recPtr.Status != pb.ChallengeStatus_CHALLENGE_FINALIZED {
					currentStake = v
				}
			}
		}
	}
	required, _ := new(big.Int).SetString(quote.RequiredChallengeStake, 10)
	if required == nil {
		required = big.NewInt(0)
	}
	remaining := new(big.Int).Sub(required, currentStake)
	if remaining.Sign() < 0 {
		remaining.SetInt64(0)
	}
	minContribution, _ := new(big.Int).SetString(quote.MinChallengeContribution, 10)
	suggested := new(big.Int).Set(remaining)
	if suggested.Cmp(minContribution) < 0 {
		suggested.Set(minContribution)
	}
	resp := map[string]any{
		"request_id":                 requestID,
		"challenge_id":               reqPtr.ChallengeId,
		"current_challenge_stake":    currentStake.String(),
		"remaining_challenge_stake":  remaining.String(),
		"min_challenge_contribution": minContribution.String(),
		"suggested_challenge_stake":  suggested.String(),
		"quote":                      quote,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func handlerWitnessConfig(wc config.WitnessConfig) *witness.Config {
	return &witness.Config{
		ConsensusThreshold:           wc.ConsensusThreshold,
		AbstainThreshold:             wc.AbstainThreshold,
		VotingPeriodBlocks:           wc.VotingPeriodBlocks,
		VoteGracePeriodBlocks:        wc.VoteGracePeriodBlocks,
		ChallengePeriodBlocks:        wc.ChallengePeriodBlocks,
		ArbitrationPeriodBlocks:      wc.ArbitrationPeriodBlocks,
		UnstakeLockBlocks:            wc.UnstakeLockBlocks,
		RetryIntervalBlocks:          wc.RetryIntervalBlocks,
		MaxShelveWaitBlocks:          wc.MaxShelveWaitBlocks,
		MinStakeAmount:               wc.MinStakeAmount,
		ChallengeStakeAmount:         wc.ChallengeStakeAmount,
		ChallengeStakeRatioBps:       wc.ChallengeStakeRatioBps,
		ChallengeArbitrationBaseCost: wc.ChallengeArbitrationBaseCost,
		MaxChallengeStakeAmount:      wc.MaxChallengeStakeAmount,
		InitialWitnessCount:          wc.InitialWitnessCount,
		MinSelectedWitnessCount:      wc.MinSelectedWitnessCount,
		ExpandMultiplier:             wc.ExpandMultiplier,
		OrdinaryCommitteeSizes:       append([]uint32(nil), wc.OrdinaryCommitteeSizes...),
		OrdinaryThresholdBps:         wc.OrdinaryThresholdBps,
		SelectionBeaconDepth:         wc.SelectionBeaconDepth,
		BeaconMixBlocks:              wc.BeaconMixBlocks,
		SlashRatio:                   wc.SlashRatio,
		ChallengerReward:             wc.ChallengerReward,
		SilenceSlashRatio:            wc.SilenceSlashRatio,
	}
}

func (hm *HandlerManager) HandleWitnessChallenges(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("WitnessChallenges")
	if r.URL.Query().Has("arbitrator") {
		hm.handleAssignedWitnessChallenges(w, r)
		return
	}

	q := r.URL.Query()
	limit := 100
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > 500 {
				parsed = 500
			}
			limit = parsed
		}
	}

	records := make([]*pb.ChallengeRecord, 0, limit)
	appendRecord := func(data []byte) {
		var rec pb.ChallengeRecord
		if err := proto.Unmarshal(data, &rec); err != nil || rec.ChallengeId == "" {
			return
		}
		records = append(records, proto.Clone(hm.hydrateArchivedChallengeRecord(&rec)).(*pb.ChallengeRecord))
	}

	                                                                        
	                                                                       
	if rawIDs := strings.TrimSpace(q.Get("challenge_ids")); rawIDs != "" {
		parts := strings.Split(rawIDs, ",")
		if len(parts) > 100 {
			http.Error(w, "challenge_ids supports at most 100 IDs", http.StatusBadRequest)
			return
		}
		seen := make(map[string]struct{}, len(parts))
		for _, rawID := range parts {
			challengeID := strings.TrimSpace(rawID)
			if challengeID == "" {
				continue
			}
			if _, duplicate := seen[challengeID]; duplicate {
				continue
			}
			seen[challengeID] = struct{}{}
			data, err := hm.dbManager.Get(keys.KeyChallengeRecord(challengeID))
			if err == nil && len(data) > 0 {
				appendRecord(data)
			}
		}
	} else {
		prefix := keys.KeyChallengeRecordPrefix()
		startAfter := ""
		if after := strings.TrimSpace(q.Get("after")); after != "" {
			startAfter = keys.KeyChallengeRecord(after)
		}
		rows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(prefix, startAfter, limit, false)
		if err != nil {
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			appendRecord(row.Value)
		}
	}

	resp := &pb.ChallengeRecordList{Records: records}
	respData, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(respData)
}
