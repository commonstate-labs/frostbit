package main

import (
	"context"
	"crypto/sha256"
	"dex/config"
	"dex/db"
	bridgeasset "dex/frost/asset"
	frostchain "dex/frost/chain"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dex/keys"
	"dex/pb"
	"dex/utils"
	witnessprotocol "dex/witness"

	"github.com/btcsuite/btcd/btcutil/base58"
	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

                                         
type FrostWithdrawQueueItem struct {
	WithdrawID          string                 `json:"withdraw_id"`
	Requester           string                 `json:"requester,omitempty"`
	Chain               string                 `json:"chain"`
	Asset               string                 `json:"asset"`
	To                  string                 `json:"to"`
	Amount              string                 `json:"amount"`
	Status              string                 `json:"status"`
	TxID                string                 `json:"tx_id,omitempty"`
	RequestTxID         string                 `json:"request_tx_id,omitempty"`
	JobID               string                 `json:"job_id,omitempty"`
	VaultID             *uint32                `json:"vault_id,omitempty"`
	RequestHeight       uint64                 `json:"request_height,omitempty"`
	PlanningLogs        []*pb.FrostPlanningLog `json:"planning_logs,omitempty"`
	DerivedStatus       string                 `json:"derived_status,omitempty"`
	DerivedReason       string                 `json:"derived_reason,omitempty"`
	LastPlanningStep    string                 `json:"last_planning_step,omitempty"`
	LastPlanningStatus  string                 `json:"last_planning_status,omitempty"`
	LastPlanningMessage string                 `json:"last_planning_message,omitempty"`
	LastPlanningAt      uint64                 `json:"last_planning_at,omitempty"`
	FailureReason       string                 `json:"failure_reason,omitempty"`
	FailureAt           uint64                 `json:"failure_at,omitempty"`
}

type FrostWithdrawJobItem struct {
	JobID               string   `json:"job_id"`
	Chain               string   `json:"chain"`
	Asset               string   `json:"asset"`
	VaultID             uint32   `json:"vault_id"`
	Status              string   `json:"status"`
	WithdrawCount       int      `json:"withdraw_count"`
	WithdrawIDs         []string `json:"withdraw_ids"`
	TotalAmount         string   `json:"total_amount"`
	LastPlanningStep    string   `json:"last_planning_step,omitempty"`
	LastPlanningStatus  string   `json:"last_planning_status,omitempty"`
	LastPlanningMessage string   `json:"last_planning_message,omitempty"`
	LastPlanningAt      uint64   `json:"last_planning_at,omitempty"`
	FailureReason       string   `json:"failure_reason,omitempty"`
	FailureAt           uint64   `json:"failure_at,omitempty"`
	DerivedStatus       string   `json:"derived_status,omitempty"`
	DerivedReason       string   `json:"derived_reason,omitempty"`
	Synthetic           bool     `json:"synthetic,omitempty"`
}

type planningSummary struct {
	LastStep    string
	LastStatus  string
	LastMessage string
	LastAt      uint64

	FailureReason string
	FailureAt     uint64
}

func summarizePlanningLogs(logs []*pb.FrostPlanningLog) planningSummary {
	var out planningSummary
	for _, log := range logs {
		if log == nil {
			continue
		}
		if log.Timestamp >= out.LastAt {
			out.LastAt = log.Timestamp
			out.LastStep = strings.TrimSpace(log.Step)
			out.LastStatus = strings.TrimSpace(log.Status)
			out.LastMessage = strings.TrimSpace(log.Message)
		}
		if strings.EqualFold(strings.TrimSpace(log.Status), "FAILED") && strings.TrimSpace(log.Message) != "" {
			if log.Timestamp >= out.FailureAt {
				out.FailureAt = log.Timestamp
				out.FailureReason = strings.TrimSpace(log.Message)
			}
		}
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	if s == "" {
		return false
	}
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func deriveWithdrawDebugStatus(chainStatus string, logs []*pb.FrostPlanningLog) (string, string) {
	chainStatus = strings.ToUpper(strings.TrimSpace(chainStatus))
	if chainStatus == "CANCELLED" {
		return "CANCELLED", ""
	}
	if chainStatus == "SIGNED" {
		return "SIGNED", ""
	}

	summary := summarizePlanningLogs(logs)
	if summary.LastStep == "" {
		if chainStatus == "SIGNING" {
			return "SIGNING", ""
		}
		return "QUEUED", ""
	}

	step := strings.ToLower(summary.LastStep)
	status := strings.ToLower(summary.LastStatus)
	msgLower := strings.ToLower(summary.LastMessage)

	switch {
	case step == "signing" && status == "failed":
		return "SIGN_FAILED", summary.LastMessage
	case step == "signing" && status == "ok":
		return "SIGN_DONE", summary.LastMessage
	case step == "signing" && status == "waiting":
		return "SIGNING", summary.LastMessage
	case step == "startsigning" && status == "ok":
		return "SIGNING", summary.LastMessage
	case step == "planbtcjob" && status == "failed":
		if containsAny(msgLower, "insufficient utxo", "no available utxo", "utxo") {
			return "WAIT_UTXO", summary.LastMessage
		}
		if containsAny(msgLower, "insufficient", "balance", "available") {
			return "WAIT_VAULT_BALANCE", summary.LastMessage
		}
		if containsAny(msgLower, "youngest treasury address unavailable", "no active vault") {
			return "WAIT_ACTIVE_VAULT", summary.LastMessage
		}
		return "PLAN_FAILED", summary.LastMessage
	case step == "selectvault" && status == "failed":
		if containsAny(msgLower, "insufficient", "balance", "available") {
			return "WAIT_VAULT_BALANCE", summary.LastMessage
		}
		if containsAny(msgLower, "no active vault", "active vault", "status=active") {
			return "WAIT_ACTIVE_VAULT", summary.LastMessage
		}
		return "WAIT_SELECT_VAULT", summary.LastMessage
	case step == "compositeplan" && status == "waiting":
		return "WAIT_VAULT_BALANCE", summary.LastMessage
	case status == "waiting":
		return "PLANNING_WAITING", summary.LastMessage
	case status == "failed":
		return "PLANNING_FAILED", summary.LastMessage
	case chainStatus == "SIGNING":
		return "SIGNING", summary.LastMessage
	case step == "finishplanning" && status == "ok":
		return "PLANNED", summary.LastMessage
	case step == "buildtemplate" && status == "ok":
		return "PLANNED", summary.LastMessage
	default:
		return "QUEUED", summary.LastMessage
	}
}

func isExplorerWithdrawTerminalStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SIGNED", "CANCELLED":
		return true
	default:
		return false
	}
}

func explorerWithdrawRequester(st *pb.FrostWithdrawState) string {
	if st == nil {
		return ""
	}
	return strings.TrimSpace(st.Requester)
}

func isPlanningBlockerStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "WAIT_UTXO", "WAIT_ACTIVE_VAULT", "WAIT_VAULT_BALANCE", "WAIT_SELECT_VAULT", "PLAN_FAILED", "PLANNING_FAILED", "PLANNING_WAITING":
		return true
	default:
		return false
	}
}

func withdrawVaultIDForExplorer(st *pb.FrostWithdrawState) *uint32 {
	if st == nil {
		return nil
	}
	status := strings.ToUpper(strings.TrimSpace(st.Status))
	if status == "SIGNING" || status == "SIGNED" || strings.TrimSpace(st.JobId) != "" || strings.TrimSpace(st.SignedTxId) != "" {
		v := st.VaultId
		return &v
	}
	return nil
}

func ensureExplorerPlanningLog(logs []*pb.FrostPlanningLog, step, status, message string, ts *uint64) []*pb.FrostPlanningLog {
	for _, log := range logs {
		if log == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(log.Step), step) && strings.EqualFold(strings.TrimSpace(log.Status), status) && strings.TrimSpace(log.Message) == message {
			return logs
		}
	}
	timestamp := uint64(0)
	if *ts > 0 {
		*ts = *ts + 1
		timestamp = *ts
	}
	return append(logs, &pb.FrostPlanningLog{Step: step, Status: status, Message: message, Timestamp: timestamp})
}

func augmentWithdrawPlanningLogs(st *pb.FrostWithdrawState) []*pb.FrostPlanningLog {
	if st == nil {
		return nil
	}
	logs := mergeExplorerPlanningLogs(nil, st.PlanningLogs)
	summary := summarizePlanningLogs(logs)
	ts := summary.LastAt
	jobID := strings.TrimSpace(st.JobId)
	status := strings.ToUpper(strings.TrimSpace(st.Status))
	if jobID != "" {
		logs = ensureExplorerPlanningLog(logs, "Claim", "OK", fmt.Sprintf("Vault %d assigned to job %s", st.VaultId, jobID), &ts)
	}
	if status == "SIGNING" || status == "SIGNED" {
		if jobID != "" {
			logs = ensureExplorerPlanningLog(logs, "StartSigning", "OK", fmt.Sprintf("Signing session %s started", jobID), &ts)
		} else {
			logs = ensureExplorerPlanningLog(logs, "StartSigning", "OK", "Signing session started", &ts)
		}
	}
	if status == "SIGNED" || strings.TrimSpace(st.SignedTxId) != "" {
		msg := "Signed tx recorded"
		if txID := strings.TrimSpace(st.SignedTxId); txID != "" {
			msg = "Signed tx recorded: " + txID
		}
		logs = ensureExplorerPlanningLog(logs, "Signing", "OK", msg, &ts)
	}
	return logs
}

func cloneExplorerPlanningLog(log *pb.FrostPlanningLog) *pb.FrostPlanningLog {
	if log == nil {
		return nil
	}
	return &pb.FrostPlanningLog{Step: log.Step, Status: log.Status, Message: log.Message, Timestamp: log.Timestamp}
}

func mergeExplorerPlanningLogs(base, extra []*pb.FrostPlanningLog) []*pb.FrostPlanningLog {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := make([]*pb.FrostPlanningLog, 0, len(base)+len(extra))
	for _, log := range base {
		if cloned := cloneExplorerPlanningLog(log); cloned != nil {
			merged = append(merged, cloned)
		}
	}
	for _, log := range extra {
		if cloned := cloneExplorerPlanningLog(log); cloned != nil {
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
	return merged
}

const (
	defaultExplorerWithdrawMonitorLimit          = 500
	defaultExplorerWithdrawMonitorCompletedLimit = 100
	maxExplorerWithdrawMonitorLimit              = 1000
	maxExplorerWithdrawMonitorCompletedLimit     = 200
	                                                                           
	                                                                          
	                                         
	maxExplorerWithdrawMonitorScan      = 5000
	maxExplorerWithdrawMonitorQueueRefs = 64
)

func explorerWithdrawMonitorQueueQuota(total, queueCount, queueIndex int) int {
	if total <= 0 || queueCount <= 0 || queueIndex < 0 || queueIndex >= queueCount {
		return 0
	}
	quota := total / queueCount
	if queueIndex < total%queueCount {
		quota++
	}
	return quota
}

func explorerWithdrawMonitorCandidateScanLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	scanLimit := limit * 4
	if scanLimit < 64 {
		scanLimit = 64
	}
	if scanLimit > maxExplorerWithdrawMonitorScan {
		scanLimit = maxExplorerWithdrawMonitorScan
	}
	return scanLimit
}

func clampExplorerWithdrawMonitorLimit(limit int) int {
	if limit <= 0 {
		return defaultExplorerWithdrawMonitorLimit
	}
	if limit > maxExplorerWithdrawMonitorLimit {
		return maxExplorerWithdrawMonitorLimit
	}
	return limit
}

func parseExplorerWithdrawMonitorLimit(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return defaultExplorerWithdrawMonitorLimit
	}
	return clampExplorerWithdrawMonitorLimit(v)
}

func clampExplorerWithdrawMonitorCompletedLimit(limit int) int {
	if limit < 0 {
		return defaultExplorerWithdrawMonitorCompletedLimit
	}
	if limit > maxExplorerWithdrawMonitorCompletedLimit {
		return maxExplorerWithdrawMonitorCompletedLimit
	}
	return limit
}

func parseExplorerWithdrawMonitorCompletedLimit(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultExplorerWithdrawMonitorCompletedLimit
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return defaultExplorerWithdrawMonitorCompletedLimit
	}
	return clampExplorerWithdrawMonitorCompletedLimit(v)
}

func frostWithdrawMonitorPath(chain, asset string, limit, completedLimit int) string {
	v := url.Values{}
	if chain != "" {
		v.Set("chain", chain)
	}
	if asset != "" {
		v.Set("asset", asset)
	}
	v.Set("limit", strconv.Itoa(clampExplorerWithdrawMonitorLimit(limit)))
	v.Set("completed_limit", strconv.Itoa(clampExplorerWithdrawMonitorCompletedLimit(completedLimit)))
	return "/frost/withdraw/monitor?" + v.Encode()
}

func frostWithdrawListPathWithOptions(chain, asset string, vaultID *uint32, limit, offset int, order string) string {
	v := url.Values{}
	if chain != "" {
		v.Set("chain", chain)
	}
	if asset != "" {
		v.Set("asset", asset)
	}
	if vaultID != nil {
		v.Set("vault_id", strconv.FormatUint(uint64(*vaultID), 10))
	}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		v.Set("offset", strconv.Itoa(offset))
	}
	if strings.TrimSpace(order) != "" {
		v.Set("order", strings.TrimSpace(order))
	}
	if qs := v.Encode(); qs != "" {
		return "/frost/withdraw/list?" + qs
	}
	return "/frost/withdraw/list"
}

const (
	explorerMaxFrostMonitorNodes = 16
	explorerFrostMonitorWorkers  = 4
	explorerFrostMonitorCacheTTL = 90 * time.Second
	explorerFrostDetailCacheTTL  = 30 * time.Second
	explorerFrostPollInterval    = 30 * time.Second
	explorerFrostPageDefault     = 25
	explorerFrostPageMax         = 100
	explorerFrostDetailLogMax    = 256
)

type frostWithdrawMonitorSnapshot struct {
	Revision        string
	States          []*pb.FrostWithdrawState
	Pending         []FrostWithdrawQueueItem
	Jobs            []FrostWithdrawJobItem
	Completed       []FrostWithdrawQueueItem
	UnassignedTasks []FrostWithdrawQueueItem
}

func (s *server) ensureFrostWithdrawCaches() {
	if s == nil {
		return
	}
	s.withdrawCacheOnce.Do(func() {
		if s.withdrawMonitorCache == nil {
			s.withdrawMonitorCache = newBoundedTTLCache[*frostWithdrawMonitorSnapshot](128, 32<<20, 4)
		}
		if s.withdrawDetailCache == nil {
			s.withdrawDetailCache = newBoundedTTLCache[[]byte](128, 16<<20, 4)
		}
	})
}

func (s *server) markFrostWithdrawMonitorHeight(height uint64) {
	if s == nil || height == 0 {
		return
	}
	for {
		current := s.withdrawMonitorHeight.Load()
		if height <= current || s.withdrawMonitorHeight.CompareAndSwap(current, height) {
			return
		}
	}
}

func (s *server) forceFrostWithdrawMonitorRefresh() {
	if s != nil {
		s.withdrawRefreshEpoch.Add(1)
	}
}

func (s *server) frostWithdrawMonitorRevision() string {
	if s == nil {
		return "0:0:0"
	}
	pollSeconds := int64(explorerFrostPollInterval / time.Second)
	return fmt.Sprintf("%d:%d:%d", s.withdrawMonitorHeight.Load(), s.withdrawRefreshEpoch.Load(), time.Now().Unix()/pollSeconds)
}

func (s *server) frostWithdrawLogNodes(primary string) []string {
	seen := make(map[string]struct{})
	capacity := len(s.defaultNodes) + 1
	if capacity > explorerMaxFrostMonitorNodes {
		capacity = explorerMaxFrostMonitorNodes
	}
	nodes := make([]string, 0, capacity)
	add := func(node string) {
		node = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(node, "https://"), "http://"), "/"))
		if node == "" {
			return
		}
		if _, ok := seen[node]; ok {
			return
		}
		seen[node] = struct{}{}
		nodes = append(nodes, node)
	}
	add(primary)
	for _, node := range s.defaultNodes {
		if len(nodes) >= explorerMaxFrostMonitorNodes {
			break
		}
		add(node)
	}
	return nodes
}

type explorerWithdrawQueueRef struct {
	chain string
	asset string
}

func parseExplorerFrostWithdrawFIFOSeqKey(key string) (explorerWithdrawQueueRef, bool) {
	prefix := keys.KeyFrostWithdrawFIFOSeqPrefix()
	if !strings.HasPrefix(key, prefix) {
		return explorerWithdrawQueueRef{}, false
	}
	rest := strings.TrimPrefix(key, prefix)
	parts := strings.SplitN(rest, "_", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return explorerWithdrawQueueRef{}, false
	}
	return explorerWithdrawQueueRef{chain: parts[0], asset: parts[1]}, true
}

func explorerWithdrawMonitorQueues(nodeDB *db.Manager, chain, asset string) ([]explorerWithdrawQueueRef, error) {
	if chain != "" && asset != "" {
		return []explorerWithdrawQueueRef{{chain: chain, asset: asset}}, nil
	}
	seqPrefix := keys.KeyFrostWithdrawFIFOSeqPrefix()
	if chain != "" {
		seqPrefix = keys.KeyFrostWithdrawFIFOSeq(chain, "")
	}
	seqKeys, err := nodeDB.ScanKeysWithLimitOrdered(seqPrefix, maxExplorerWithdrawMonitorQueueRefs, false)
	if err != nil {
		return nil, err
	}
	refs := make([]explorerWithdrawQueueRef, 0, len(seqKeys))
	seen := make(map[string]struct{}, len(seqKeys))
	for _, seqKey := range seqKeys {
		ref, ok := parseExplorerFrostWithdrawFIFOSeqKey(seqKey)
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

func readExplorerWithdrawUint64(nodeDB *db.Manager, key string, fallback uint64) (uint64, error) {
	raw, exists, err := nodeDB.GetState(key)
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

func fetchLocalFrostWithdrawPlanningLogs(nodeDB *db.Manager, withdrawID string) []*pb.FrostPlanningLog {
	if nodeDB == nil || withdrawID == "" {
		return nil
	}
	                                                                             
	                                                                              
	                              
	const maxPlanningLogRows = 8
	const maxPlanningLogItems = 64
	rows, err := nodeDB.ScanKVWithLimitOrdered(fmt.Sprintf("v1_frost_planning_log_%s_", withdrawID), maxPlanningLogRows, false)
	if err != nil || len(rows) == 0 {
		return nil
	}
	logs := make([]*pb.FrostPlanningLog, 0)
	for _, row := range rows {
		var tx pb.FrostWithdrawPlanningLogTx
		if err := proto.Unmarshal(row.Value, &tx); err != nil {
			continue
		}
		for _, log := range tx.Logs {
			if cloned := cloneExplorerPlanningLog(log); cloned != nil {
				logs = append(logs, cloned)
				if len(logs) >= maxPlanningLogItems {
					return mergeExplorerPlanningLogs(nil, logs)
				}
			}
		}
	}
	return mergeExplorerPlanningLogs(nil, logs)
}

func hydrateLocalArchivedFrostWithdrawState(nodeDB *db.Manager, st *pb.FrostWithdrawState) *pb.FrostWithdrawState {
	if nodeDB == nil || st == nil || st.WithdrawId == "" || !isExplorerWithdrawTerminalStatus(st.Status) {
		return st
	}
	raw, err := nodeDB.Get(keys.KeyArchiveFrostWithdraw(st.WithdrawId))
	if err != nil || len(raw) == 0 {
		return st
	}
	var archived pb.FrostWithdrawState
	if err := proto.Unmarshal(raw, &archived); err != nil || archived.WithdrawId != st.WithdrawId {
		return st
	}
	return &archived
}

func scanLocalFrostWithdrawMonitorQueue(nodeDB *db.Manager, ref explorerWithdrawQueueRef, limit, scanBudget int) ([]*pb.FrostWithdrawState, int, error) {
	if nodeDB == nil || limit <= 0 || scanBudget <= 0 {
		return nil, 0, nil
	}
	head, err := readExplorerWithdrawUint64(nodeDB, keys.KeyFrostWithdrawFIFOHead(ref.chain, ref.asset), 1)
	if err != nil {
		return nil, 0, err
	}
	maxSeq, err := readExplorerWithdrawUint64(nodeDB, keys.KeyFrostWithdrawFIFOSeq(ref.chain, ref.asset), 0)
	if err != nil {
		return nil, 0, err
	}
	if maxSeq == 0 || head > maxSeq {
		return nil, 0, nil
	}
	states := make([]*pb.FrostWithdrawState, 0, limit)
	scanned := 0
	for seq := head; seq <= maxSeq && len(states) < limit && scanned < scanBudget; seq++ {
		scanned++
		withdrawIDRaw, exists, err := nodeDB.GetState(keys.KeyFrostWithdrawFIFOIndex(ref.chain, ref.asset, seq))
		if err != nil {
			return nil, scanned, err
		}
		if !exists || len(withdrawIDRaw) == 0 {
			continue
		}
		withdrawRaw, exists, err := nodeDB.GetState(keys.KeyFrostWithdraw(string(withdrawIDRaw)))
		if err != nil {
			return nil, scanned, err
		}
		if !exists || len(withdrawRaw) == 0 {
			continue
		}
		var st pb.FrostWithdrawState
		if err := proto.Unmarshal(withdrawRaw, &st); err != nil || st.WithdrawId == "" {
			continue
		}
		if isExplorerWithdrawTerminalStatus(st.Status) {
			continue
		}
		if !strings.EqualFold(st.Chain, ref.chain) || !strings.EqualFold(st.Asset, ref.asset) {
			continue
		}
		st.PlanningLogs = mergeExplorerPlanningLogs(st.PlanningLogs, fetchLocalFrostWithdrawPlanningLogs(nodeDB, st.WithdrawId))
		states = append(states, proto.Clone(&st).(*pb.FrostWithdrawState))
	}
	return states, scanned, nil
}

func explorerWithdrawIDFromDoneIndex(key string, value []byte) string {
	if id := strings.TrimSpace(string(value)); id != "" {
		return id
	}
	idx := strings.LastIndex(key, "_")
	if idx < 0 || idx == len(key)-1 {
		return ""
	}
	return strings.TrimSpace(key[idx+1:])
}

func scanLocalFrostWithdrawMonitorCompletedQueue(nodeDB *db.Manager, ref explorerWithdrawQueueRef, limit, scanBudget int) ([]*pb.FrostWithdrawState, int, error) {
	if nodeDB == nil || limit <= 0 || scanBudget <= 0 {
		return nil, 0, nil
	}
	states := make([]*pb.FrostWithdrawState, 0, limit)
	seen := make(map[string]struct{}, limit)
	addState := func(st *pb.FrostWithdrawState) {
		if st == nil || len(states) >= limit || strings.TrimSpace(st.WithdrawId) == "" {
			return
		}
		if _, ok := seen[st.WithdrawId]; ok {
			return
		}
		if !isExplorerWithdrawTerminalStatus(st.Status) {
			return
		}
		if !strings.EqualFold(st.Chain, ref.chain) || !strings.EqualFold(st.Asset, ref.asset) {
			return
		}
		seen[st.WithdrawId] = struct{}{}
		statePtr := hydrateLocalArchivedFrostWithdrawState(nodeDB, st)
		cloned := proto.Clone(statePtr).(*pb.FrostWithdrawState)
		cloned.PlanningLogs = mergeExplorerPlanningLogs(cloned.PlanningLogs, fetchLocalFrostWithdrawPlanningLogs(nodeDB, cloned.WithdrawId))
		states = append(states, cloned)
	}
	loadArchived := func(withdrawID string) {
		if strings.TrimSpace(withdrawID) == "" {
			return
		}
		withdrawRaw, err := nodeDB.Get(keys.KeyArchiveFrostWithdraw(withdrawID))
		if err != nil || len(withdrawRaw) == 0 {
			return
		}
		var st pb.FrostWithdrawState
		if err := proto.Unmarshal(withdrawRaw, &st); err != nil {
			return
		}
		addState(&st)
	}

	pairs, err := nodeDB.ScanKVWithLimitOrdered(keys.KeyArchiveFrostWithdrawDonePrefix(ref.chain, ref.asset), scanBudget, true)
	if err != nil {
		return nil, 0, err
	}
	scanned := len(pairs)
	for _, pair := range pairs {
		if len(states) >= limit {
			break
		}
		loadArchived(explorerWithdrawIDFromDoneIndex(pair.Key, pair.Value))
	}
	if len(states) >= limit {
		return states, scanned, nil
	}

	remaining := scanBudget - scanned
	if remaining <= 0 {
		return states, scanned, nil
	}
	pairs, err = nodeDB.ScanKVWithLimitOrdered(keys.KeyArchiveFrostWithdraw(""), remaining, true)
	if err != nil {
		return nil, scanned, err
	}
	scanned += len(pairs)
	for _, pair := range pairs {
		if len(states) >= limit {
			break
		}
		var st pb.FrostWithdrawState
		if err := proto.Unmarshal(pair.Value, &st); err != nil {
			continue
		}
		addState(&st)
	}
	return states, scanned, nil
}

func (s *server) fetchLocalFrostWithdrawMonitorStates(chain, asset string, limit, completedLimit int) []*pb.FrostWithdrawState {
	chain = frostchain.NormalizeChain(chain)
	limit = clampExplorerWithdrawMonitorLimit(limit)
	completedLimit = clampExplorerWithdrawMonitorCompletedLimit(completedLimit)
	nodeDBs := s.nodeDBs
	if len(nodeDBs) == 0 && s.nodeDB != nil {
		nodeDBs = append(nodeDBs, s.nodeDB)
	}
	if len(nodeDBs) == 0 {
		return nil
	}
	byID := make(map[string]*pb.FrostWithdrawState)
	order := make([]string, 0)
	                                                                          
	                                                                          
	activeScanBudget := maxExplorerWithdrawMonitorScan
	for _, nodeDB := range nodeDBs {
		if nodeDB == nil || len(byID) >= limit || activeScanBudget <= 0 {
			continue
		}
		refs, err := explorerWithdrawMonitorQueues(nodeDB, chain, asset)
		if err != nil {
			continue
		}
		for i, ref := range refs {
			if len(byID) >= limit || activeScanBudget <= 0 {
				break
			}
			quota := explorerWithdrawMonitorQueueQuota(limit, len(refs), i)
			if quota <= 0 {
				continue
			}
			scanBudget := min(activeScanBudget, explorerWithdrawMonitorCandidateScanLimit(quota))
			states, scanned, err := scanLocalFrostWithdrawMonitorQueue(nodeDB, ref, quota, scanBudget)
			activeScanBudget -= scanned
			if err != nil {
				continue
			}
			for _, st := range states {
				if len(byID) >= limit {
					break
				}
				if st == nil || strings.TrimSpace(st.WithdrawId) == "" {
					continue
				}
				if existing, ok := byID[st.WithdrawId]; ok {
					existing.PlanningLogs = mergeExplorerPlanningLogs(existing.PlanningLogs, st.PlanningLogs)
					continue
				}
				byID[st.WithdrawId] = proto.Clone(st).(*pb.FrostWithdrawState)
				order = append(order, st.WithdrawId)
			}
		}
	}
	states := make([]*pb.FrostWithdrawState, 0, len(order))
	for _, withdrawID := range order {
		states = append(states, byID[withdrawID])
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
	completedSeen := make(map[string]struct{}, completedLimit)
	completedScanBudget := maxExplorerWithdrawMonitorScan
	for _, nodeDB := range nodeDBs {
		if nodeDB == nil || len(completed) >= completedLimit || completedScanBudget <= 0 {
			continue
		}
		refs, err := explorerWithdrawMonitorQueues(nodeDB, chain, asset)
		if err != nil {
			continue
		}
		for i, ref := range refs {
			if len(completed) >= completedLimit || completedScanBudget <= 0 {
				break
			}
			quota := explorerWithdrawMonitorQueueQuota(completedLimit, len(refs), i)
			if quota <= 0 {
				continue
			}
			scanBudget := min(completedScanBudget, explorerWithdrawMonitorCandidateScanLimit(quota))
			part, scanned, err := scanLocalFrostWithdrawMonitorCompletedQueue(nodeDB, ref, quota, scanBudget)
			completedScanBudget -= scanned
			if err != nil {
				continue
			}
			for _, st := range part {
				if st == nil || strings.TrimSpace(st.WithdrawId) == "" {
					continue
				}
				if _, ok := completedSeen[st.WithdrawId]; ok {
					continue
				}
				completedSeen[st.WithdrawId] = struct{}{}
				completed = append(completed, st)
				if len(completed) >= completedLimit {
					break
				}
			}
		}
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
	return states
}

func buildFrostWithdrawMonitorSnapshot(revision string, states []*pb.FrostWithdrawState) *frostWithdrawMonitorSnapshot {
	queue := buildFrostWithdrawQueueItems(states)
	allJobs := buildFrostWithdrawJobItems(states)
	activeJobs := make([]FrostWithdrawJobItem, 0, len(allJobs))
	activeJobWithdrawIDs := make(map[string]struct{})
	for _, job := range allJobs {
		if isExplorerWithdrawTerminalStatus(job.Status) || isExplorerWithdrawTerminalStatus(job.DerivedStatus) {
			continue
		}
		activeJobs = append(activeJobs, job)
		for _, withdrawID := range job.WithdrawIDs {
			activeJobWithdrawIDs[withdrawID] = struct{}{}
		}
	}

	pending := make([]FrostWithdrawQueueItem, 0, len(queue))
	completed := make([]FrostWithdrawQueueItem, 0, len(queue))
	unassigned := make([]FrostWithdrawQueueItem, 0)
	for _, source := range queue {
		item := source
		                                                                       
		                                                                  
		item.PlanningLogs = nil
		if isExplorerWithdrawTerminalStatus(item.Status) {
			completed = append(completed, item)
			continue
		}
		_, belongsToActiveJob := activeJobWithdrawIDs[item.WithdrawID]
		runtimeStatus := strings.TrimSpace(item.DerivedStatus)
		if runtimeStatus == "" {
			runtimeStatus = item.Status
		}
		isTask := belongsToActiveJob || !strings.EqualFold(runtimeStatus, "QUEUED") || strings.TrimSpace(item.JobID) != ""
		if !isTask {
			pending = append(pending, item)
			continue
		}
		if !belongsToActiveJob {
			unassigned = append(unassigned, item)
		}
	}

	return &frostWithdrawMonitorSnapshot{
		Revision:        revision,
		States:          states,
		Pending:         pending,
		Jobs:            activeJobs,
		Completed:       completed,
		UnassignedTasks: unassigned,
	}
}

func frostWithdrawMonitorCacheKey(revision, node, chain, asset string, limit, completedLimit int) string {
	return strings.Join([]string{
		revision,
		normalizeExplorerNode(node),
		frostchain.NormalizeChain(chain),
		strings.ToUpper(strings.TrimSpace(asset)),
		strconv.Itoa(clampExplorerWithdrawMonitorLimit(limit)),
		strconv.Itoa(clampExplorerWithdrawMonitorCompletedLimit(completedLimit)),
	}, "\x00")
}

func (s *server) fetchFrostWithdrawMonitorSnapshot(ctx context.Context, node, chain, asset string, limit, completedLimit int, force bool) (*frostWithdrawMonitorSnapshot, error) {
	if s == nil {
		return nil, errors.New("explorer server is not initialized")
	}
	s.ensureFrostWithdrawCaches()
	if force {
		s.forceFrostWithdrawMonitorRefresh()
	}
	chain = frostchain.NormalizeChain(chain)
	asset = strings.ToUpper(strings.TrimSpace(asset))
	limit = clampExplorerWithdrawMonitorLimit(limit)
	completedLimit = clampExplorerWithdrawMonitorCompletedLimit(completedLimit)
	primary := s.pickNode(node)
	revision := s.frostWithdrawMonitorRevision()
	key := frostWithdrawMonitorCacheKey(revision, primary, chain, asset, limit, completedLimit)

	return s.withdrawMonitorCache.GetOrLoad(ctx, key, explorerFrostMonitorCacheTTL, func(context.Context) (*frostWithdrawMonitorSnapshot, int64, error) {
		timeout := s.timeout
		if timeout <= 0 {
			timeout = 8 * time.Second
		}
		loadCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		var states []*pb.FrostWithdrawState
		if primary == "" {
			states = s.fetchLocalFrostWithdrawMonitorStates(chain, asset, limit, completedLimit)
			if len(states) == 0 {
				return nil, 0, errors.New("no node configured")
			}
		} else {
			var resp pb.FrostWithdrawStateList
			path := frostWithdrawMonitorPath(chain, asset, limit, completedLimit)
			if err := s.fetchProto(loadCtx, primary, path, nil, &resp); err != nil {
				return nil, 0, err
			}
			states = resp.States
		}

		owned := make([]*pb.FrostWithdrawState, 0, len(states))
		for _, state := range states {
			if state != nil {
				owned = append(owned, proto.Clone(state).(*pb.FrostWithdrawState))
			}
		}
		snapshot := buildFrostWithdrawMonitorSnapshot(revision, owned)
		pageBytes, _ := json.Marshal(snapshot)
		return snapshot, int64(len(pageBytes)), nil
	})
}

                                                                              
                                                                               
                                                                                
                                  
func (s *server) fetchMergedFrostWithdrawMonitorStates(ctx context.Context, node, chain, asset string, limit, completedLimit int) (*pb.FrostWithdrawStateList, error) {
	snapshot, err := s.fetchFrostWithdrawMonitorSnapshot(ctx, node, chain, asset, limit, completedLimit, false)
	if err != nil {
		return nil, err
	}
	states := make([]*pb.FrostWithdrawState, 0, len(snapshot.States))
	for _, state := range snapshot.States {
		if state != nil {
			states = append(states, proto.Clone(state).(*pb.FrostWithdrawState))
		}
	}
	return &pb.FrostWithdrawStateList{States: states}, nil
}

                                                       
type WitnessVoteItem struct {
	BallotRound    string `json:"ballot_round,omitempty"`
	Round          uint32 `json:"round,omitempty"`
	RequestId      string `json:"request_id"`
	WitnessAddress string `json:"witness_address"`
	VoteType       int32  `json:"vote_type"`
	Status         string `json:"status"`                                                           
	Reason         string `json:"reason,omitempty"`
	Timestamp      uint64 `json:"timestamp"`
	TxId           string `json:"tx_id"`
}

func buildFrostWithdrawQueueItems(states []*pb.FrostWithdrawState) []FrostWithdrawQueueItem {
	items := make([]FrostWithdrawQueueItem, 0, len(states))
	for _, st := range states {
		if st == nil {
			continue
		}
		planningLogs := augmentWithdrawPlanningLogs(st)
		summary := summarizePlanningLogs(planningLogs)
		derivedStatus, derivedReason := deriveWithdrawDebugStatus(st.Status, planningLogs)
		items = append(items, FrostWithdrawQueueItem{
			WithdrawID:          st.WithdrawId,
			Requester:           explorerWithdrawRequester(st),
			Chain:               st.Chain,
			Asset:               st.Asset,
			To:                  st.To,
			Amount:              st.Amount,
			Status:              st.Status,
			TxID:                st.SignedTxId,
			RequestTxID:         st.TxId,
			JobID:               st.JobId,
			VaultID:             withdrawVaultIDForExplorer(st),
			RequestHeight:       st.RequestHeight,
			PlanningLogs:        planningLogs,
			DerivedStatus:       derivedStatus,
			DerivedReason:       derivedReason,
			LastPlanningStep:    summary.LastStep,
			LastPlanningStatus:  summary.LastStatus,
			LastPlanningMessage: summary.LastMessage,
			LastPlanningAt:      summary.LastAt,
			FailureReason:       summary.FailureReason,
			FailureAt:           summary.FailureAt,
		})
	}
	return items
}

func (s *server) handleFrostWithdrawQueue(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)

	chain := r.URL.Query().Get("chain")
	asset := r.URL.Query().Get("asset")
	limit := parseExplorerWithdrawMonitorLimit(r.URL.Query().Get("limit"))
	completedLimit := parseExplorerWithdrawMonitorCompletedLimit(r.URL.Query().Get("completed_limit"))

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	resp, err := s.fetchMergedFrostWithdrawMonitorStates(ctx, node, chain, asset, limit, completedLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, buildFrostWithdrawQueueItems(resp.States))
}

func inferJobIDFromPlanningLogs(logs []*pb.FrostPlanningLog) string {
	for _, log := range logs {
		if log == nil {
			continue
		}
		msg := strings.TrimSpace(log.Message)
		if !strings.HasPrefix(msg, "Job ") || !strings.HasSuffix(msg, " created") {
			if strings.HasPrefix(msg, "Signing session ") && strings.HasSuffix(msg, " started") {
				rest := strings.TrimPrefix(msg, "Signing session ")
				parts := strings.Fields(rest)
				if len(parts) > 0 {
					jobID := strings.TrimSpace(parts[0])
					if jobID != "" {
						return jobID
					}
				}
			}
			continue
		}
		jobID := strings.TrimSuffix(strings.TrimPrefix(msg, "Job "), " created")
		jobID = strings.TrimSpace(jobID)
		if jobID != "" {
			return jobID
		}
	}
	return ""
}

func resolveJobStatus(statuses map[string]struct{}, logs []*pb.FrostPlanningLog) string {
	if _, ok := statuses["SIGNED"]; ok {
		return "SIGNED"
	}
	if _, ok := statuses["CANCELLED"]; ok {
		return "CANCELLED"
	}
	if derivedStatus, _ := deriveWithdrawDebugStatus("", logs); isPlanningBlockerStatus(derivedStatus) {
		return derivedStatus
	}
	logHas := func(step, status string) bool {
		for _, log := range logs {
			if log == nil {
				continue
			}
			if log.Step == step && log.Status == status {
				return true
			}
		}
		return false
	}
	if logHas("Signing", "FAILED") {
		return "FAILED"
	}
	if logHas("Signing", "OK") {
		return "SIGN_DONE"
	}
	if logHas("StartSigning", "OK") || logHas("Signing", "WAITING") {
		return "SIGNING"
	}
	if _, ok := statuses["SIGNING"]; ok {
		return "SIGNING"
	}
	if logHas("FinishPlanning", "OK") {
		return "PLANNED"
	}
	return "QUEUED"
}

func buildFrostWithdrawJobItems(states []*pb.FrostWithdrawState) []FrostWithdrawJobItem {
	type agg struct {
		item      FrostWithdrawJobItem
		statuses  map[string]struct{}
		total     uint64
		allLogs   []*pb.FrostPlanningLog
		bestLogAt uint64
		failLogAt uint64
	}
	byID := make(map[string]*agg)
	syntheticByID := make(map[string]FrostWithdrawJobItem)

	for _, st := range states {
		if st == nil {
			continue
		}
		if strings.EqualFold(st.Status, "CANCELLED") {
			continue
		}
		planningLogs := augmentWithdrawPlanningLogs(st)
		summary := summarizePlanningLogs(planningLogs)
		derivedStatus, derivedReason := deriveWithdrawDebugStatus(st.Status, planningLogs)

		jobID := strings.TrimSpace(st.JobId)
		if jobID == "" {
			jobID = inferJobIDFromPlanningLogs(st.PlanningLogs)
		}
		if jobID == "" {
			if derivedStatus == "QUEUED" && summary.LastStep == "" {
				continue
			}
			withdrawID := strings.TrimSpace(st.WithdrawId)
			if withdrawID == "" {
				continue
			}
			synthID := "planning:" + withdrawID
			synth := FrostWithdrawJobItem{
				JobID:              synthID,
				Chain:              st.Chain,
				Asset:              st.Asset,
				VaultID:            st.VaultId,
				Status:             derivedStatus,
				WithdrawCount:      1,
				WithdrawIDs:        []string{withdrawID},
				TotalAmount:        st.Amount,
				LastPlanningStep:   summary.LastStep,
				LastPlanningStatus: summary.LastStatus,
				LastPlanningAt:     summary.LastAt,
				FailureReason:      summary.FailureReason,
				FailureAt:          summary.FailureAt,
				DerivedStatus:      derivedStatus,
				DerivedReason:      derivedReason,
				Synthetic:          true,
			}
			if synth.FailureReason == "" && strings.HasSuffix(strings.ToUpper(derivedStatus), "FAILED") {
				synth.FailureReason = derivedReason
				synth.FailureAt = summary.LastAt
			}
			syntheticByID[synthID] = synth
			continue
		}

		cur, ok := byID[jobID]
		if !ok {
			cur = &agg{
				item: FrostWithdrawJobItem{
					JobID:       jobID,
					Chain:       st.Chain,
					Asset:       st.Asset,
					VaultID:     st.VaultId,
					WithdrawIDs: make([]string, 0),
				},
				statuses: make(map[string]struct{}),
				allLogs:  make([]*pb.FrostPlanningLog, 0),
			}
			byID[jobID] = cur
		}
		if cur.item.Chain == "" {
			cur.item.Chain = st.Chain
		}
		if cur.item.Asset == "" {
			cur.item.Asset = st.Asset
		}
		if cur.item.VaultID == 0 {
			cur.item.VaultID = st.VaultId
		}
		if cur.item.DerivedStatus == "" {
			cur.item.DerivedStatus = derivedStatus
			cur.item.DerivedReason = derivedReason
		}

		if status := strings.ToUpper(strings.TrimSpace(st.Status)); status != "" {
			cur.statuses[status] = struct{}{}
		}
		cur.item.WithdrawIDs = append(cur.item.WithdrawIDs, st.WithdrawId)

		if amount, err := strconv.ParseUint(st.Amount, 10, 64); err == nil {
			cur.total += amount
		}

		for _, log := range planningLogs {
			if log == nil {
				continue
			}
			cur.allLogs = append(cur.allLogs, log)
			if strings.EqualFold(strings.TrimSpace(log.Status), "FAILED") && strings.TrimSpace(log.Message) != "" {
				if log.Timestamp >= cur.failLogAt {
					cur.failLogAt = log.Timestamp
					cur.item.FailureReason = strings.TrimSpace(log.Message)
					cur.item.FailureAt = log.Timestamp
				}
			}
			if log.Timestamp >= cur.bestLogAt {
				cur.bestLogAt = log.Timestamp
				cur.item.LastPlanningStep = log.Step
				cur.item.LastPlanningStatus = log.Status
				cur.item.LastPlanningMessage = log.Message
				cur.item.LastPlanningAt = log.Timestamp
			}
		}
	}

	items := make([]FrostWithdrawJobItem, 0, len(byID))
	for _, cur := range byID {
		sort.Strings(cur.item.WithdrawIDs)
		cur.item.WithdrawCount = len(cur.item.WithdrawIDs)
		cur.item.TotalAmount = strconv.FormatUint(cur.total, 10)
		cur.item.Status = resolveJobStatus(cur.statuses, cur.allLogs)
		derivedStatus, derivedReason := deriveWithdrawDebugStatus(cur.item.Status, cur.allLogs)
		cur.item.DerivedStatus = derivedStatus
		if cur.item.DerivedReason == "" {
			cur.item.DerivedReason = derivedReason
		}
		if cur.item.Status == "SIGNED" || cur.item.DerivedStatus == "SIGNED" || cur.item.DerivedStatus == "SIGN_DONE" {
			cur.item.FailureReason = ""
			cur.item.FailureAt = 0
		}
		items = append(items, cur.item)
	}
	for _, synth := range syntheticByID {
		items = append(items, synth)
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].LastPlanningAt == items[j].LastPlanningAt {
			return items[i].JobID < items[j].JobID
		}
		return items[i].LastPlanningAt > items[j].LastPlanningAt
	})

	return items
}

func (s *server) handleFrostWithdrawJobs(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)

	chain := r.URL.Query().Get("chain")
	asset := r.URL.Query().Get("asset")
	limit := parseExplorerWithdrawMonitorLimit(r.URL.Query().Get("limit"))
	completedLimit := parseExplorerWithdrawMonitorCompletedLimit(r.URL.Query().Get("completed_limit"))

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	resp, err := s.fetchMergedFrostWithdrawMonitorStates(ctx, node, chain, asset, limit, completedLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, buildFrostWithdrawJobItems(resp.States))
}

type FrostWithdrawQueuePage struct {
	Items      []FrostWithdrawQueueItem `json:"items"`
	NextCursor string                   `json:"next_cursor,omitempty"`
	HasMore    bool                     `json:"has_more"`
	Total      int                      `json:"total"`
}

type FrostWithdrawJobPage struct {
	Items      []FrostWithdrawJobItem `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
	HasMore    bool                   `json:"has_more"`
	Total      int                    `json:"total"`
}

type FrostWithdrawMonitorPageResponse struct {
	Section         string                   `json:"section"`
	Revision        string                   `json:"revision"`
	Pending         *FrostWithdrawQueuePage  `json:"pending,omitempty"`
	Jobs            *FrostWithdrawJobPage    `json:"jobs,omitempty"`
	Completed       *FrostWithdrawQueuePage  `json:"completed,omitempty"`
	UnassignedTasks []FrostWithdrawQueueItem `json:"unassigned_tasks,omitempty"`
}

type frostWithdrawPageCursor struct {
	Version  int    `json:"v"`
	Section  string `json:"s"`
	Revision string `json:"r"`
	Filter   string `json:"f"`
	Offset   int    `json:"o"`
}

var (
	errFrostWithdrawCursorInvalid = errors.New("invalid withdraw monitor cursor")
	errFrostWithdrawCursorStale   = errors.New("withdraw monitor changed; restart pagination")
	errFrostWithdrawDetailMissing = errors.New("withdraw not found on selected node")
)

func parseFrostWithdrawPageLimit(raw string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return explorerFrostPageDefault
	}
	if limit > explorerFrostPageMax {
		return explorerFrostPageMax
	}
	return limit
}

func frostWithdrawPageFilter(node, chain, asset string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		normalizeExplorerNode(node),
		frostchain.NormalizeChain(chain),
		strings.ToUpper(strings.TrimSpace(asset)),
	}, "\x00")))
	return fmt.Sprintf("%x", sum[:12])
}

func encodeFrostWithdrawPageCursor(section, revision, filter string, offset int) string {
	encoded, _ := json.Marshal(frostWithdrawPageCursor{
		Version: 1, Section: section, Revision: revision, Filter: filter, Offset: offset,
	})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func parseFrostWithdrawPageCursor(raw string) (frostWithdrawPageCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return frostWithdrawPageCursor{}, nil
	}
	if len(raw) > 1024 {
		return frostWithdrawPageCursor{}, errFrostWithdrawCursorInvalid
	}
	encoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(encoded) > 768 {
		return frostWithdrawPageCursor{}, errFrostWithdrawCursorInvalid
	}
	var cursor frostWithdrawPageCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil {
		return frostWithdrawPageCursor{}, errFrostWithdrawCursorInvalid
	}
	return cursor, nil
}

func decodeFrostWithdrawPageCursor(raw, section, revision, filter string, total int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	cursor, err := parseFrostWithdrawPageCursor(raw)
	if err != nil {
		return 0, err
	}
	if cursor.Version != 1 || cursor.Section != section || cursor.Filter != filter || cursor.Offset < 0 {
		return 0, errFrostWithdrawCursorInvalid
	}
	if cursor.Revision != revision {
		return 0, errFrostWithdrawCursorStale
	}
	if cursor.Offset > total {
		return 0, errFrostWithdrawCursorInvalid
	}
	return cursor.Offset, nil
}

func pageFrostWithdrawQueueItems(items []FrostWithdrawQueueItem, section, revision, filter, cursor string, limit int) (*FrostWithdrawQueuePage, error) {
	offset, err := decodeFrostWithdrawPageCursor(cursor, section, revision, filter, len(items))
	if err != nil {
		return nil, err
	}
	end := min(len(items), offset+limit)
	pageItems := make([]FrostWithdrawQueueItem, end-offset)
	copy(pageItems, items[offset:end])
	page := &FrostWithdrawQueuePage{Items: pageItems, Total: len(items), HasMore: end < len(items)}
	if page.HasMore {
		page.NextCursor = encodeFrostWithdrawPageCursor(section, revision, filter, end)
	}
	return page, nil
}

func pageFrostWithdrawJobItems(items []FrostWithdrawJobItem, revision, filter, cursor string, limit int) (*FrostWithdrawJobPage, error) {
	offset, err := decodeFrostWithdrawPageCursor(cursor, "jobs", revision, filter, len(items))
	if err != nil {
		return nil, err
	}
	end := min(len(items), offset+limit)
	pageItems := make([]FrostWithdrawJobItem, end-offset)
	copy(pageItems, items[offset:end])
	page := &FrostWithdrawJobPage{Items: pageItems, Total: len(items), HasMore: end < len(items)}
	if page.HasMore {
		page.NextCursor = encodeFrostWithdrawPageCursor("jobs", revision, filter, end)
	}
	return page, nil
}

func writeFrostWithdrawPageError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "WITHDRAW_CURSOR_INVALID"
	if errors.Is(err, errFrostWithdrawCursorStale) {
		status = http.StatusConflict
		code = "WITHDRAW_CURSOR_STALE"
	}
	writeJSONStatus(w, status, APIError{Error: err.Error(), Code: code})
}

func (s *server) handleFrostWithdrawMonitorPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed", Code: "METHOD_NOT_ALLOWED"})
		return
	}
	q := r.URL.Query()
	section := strings.ToLower(strings.TrimSpace(q.Get("section")))
	if section == "" {
		section = "all"
	}
	if section != "all" && section != "pending" && section != "jobs" && section != "completed" {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "invalid section", Code: "INVALID_PARAMS"})
		return
	}
	cursor := strings.TrimSpace(q.Get("cursor"))
	if section == "all" && cursor != "" {
		writeJSONStatus(w, http.StatusBadRequest, APIError{Error: "all section does not accept a cursor", Code: "INVALID_PARAMS"})
		return
	}
	node := s.pickNode(q.Get("node"))
	chain := frostchain.NormalizeChain(q.Get("chain"))
	asset := strings.ToUpper(strings.TrimSpace(q.Get("asset")))
	limit := parseFrostWithdrawPageLimit(q.Get("limit"))

	filter := frostWithdrawPageFilter(node, chain, asset)
	var snapshot *frostWithdrawMonitorSnapshot
	var err error
	if cursor != "" {
		parsed, parseErr := parseFrostWithdrawPageCursor(cursor)
		if parseErr != nil || parsed.Version != 1 || parsed.Section != section || parsed.Filter != filter {
			writeFrostWithdrawPageError(w, errFrostWithdrawCursorInvalid)
			return
		}
		s.ensureFrostWithdrawCaches()
		cacheKey := frostWithdrawMonitorCacheKey(parsed.Revision, node, chain, asset, defaultExplorerWithdrawMonitorLimit, defaultExplorerWithdrawMonitorCompletedLimit)
		var ok bool
		snapshot, ok = s.withdrawMonitorCache.Get(cacheKey)
		if !ok {
			writeFrostWithdrawPageError(w, errFrostWithdrawCursorStale)
			return
		}
	} else {
		timeout := s.timeout
		if timeout <= 0 {
			timeout = 8 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		snapshot, err = s.fetchFrostWithdrawMonitorSnapshot(ctx, node, chain, asset, defaultExplorerWithdrawMonitorLimit, defaultExplorerWithdrawMonitorCompletedLimit, false)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, errExplorerCacheBusy) {
				status = http.StatusServiceUnavailable
			}
			writeJSONStatus(w, status, APIError{Error: err.Error(), Code: "WITHDRAW_MONITOR_UNAVAILABLE"})
			return
		}
	}
	response := FrostWithdrawMonitorPageResponse{Section: section, Revision: snapshot.Revision}

	if section == "all" || section == "pending" {
		response.Pending, err = pageFrostWithdrawQueueItems(snapshot.Pending, "pending", snapshot.Revision, filter, cursor, limit)
	}
	if err == nil && (section == "all" || section == "jobs") {
		jobCursor := cursor
		if section == "all" {
			jobCursor = ""
		}
		response.Jobs, err = pageFrostWithdrawJobItems(snapshot.Jobs, snapshot.Revision, filter, jobCursor, limit)
		response.UnassignedTasks = append([]FrostWithdrawQueueItem(nil), snapshot.UnassignedTasks[:min(len(snapshot.UnassignedTasks), limit)]...)
	}
	if err == nil && (section == "all" || section == "completed") {
		completedCursor := cursor
		if section == "all" {
			completedCursor = ""
		}
		response.Completed, err = pageFrostWithdrawQueueItems(snapshot.Completed, "completed", snapshot.Revision, filter, completedCursor, limit)
	}
	if err != nil {
		writeFrostWithdrawPageError(w, err)
		return
	}
	writeJSON(w, response)
}

type FrostWithdrawDetailResponse struct {
	WithdrawID     string                 `json:"withdraw_id"`
	Chain          string                 `json:"chain"`
	Status         string                 `json:"status"`
	Amount         string                 `json:"amount"`
	Receiver       string                 `json:"receiver"`
	Height         uint64                 `json:"height"`
	JobID          string                 `json:"job_id,omitempty"`
	PlanningLogs   []*pb.FrostPlanningLog `json:"planning_logs"`
	LogSourceCount int                    `json:"log_source_count"`
	QueriedNodes   int                    `json:"queried_nodes"`
	Partial        bool                   `json:"partial"`
}

func mergeExplorerPlanningLogsCapped(groups ...[]*pb.FrostPlanningLog) []*pb.FrostPlanningLog {
	type logKey struct {
		timestamp uint64
		step      string
		status    string
		message   string
	}
	seen := make(map[logKey]struct{})
	logs := make([]*pb.FrostPlanningLog, 0)
	for _, group := range groups {
		for _, source := range group {
			if source == nil {
				continue
			}
			key := logKey{source.Timestamp, source.Step, source.Status, source.Message}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			logs = append(logs, cloneExplorerPlanningLog(source))
		}
	}
	sort.SliceStable(logs, func(i, j int) bool {
		if logs[i].Timestamp != logs[j].Timestamp {
			return logs[i].Timestamp < logs[j].Timestamp
		}
		if logs[i].Step != logs[j].Step {
			return logs[i].Step < logs[j].Step
		}
		if logs[i].Status != logs[j].Status {
			return logs[i].Status < logs[j].Status
		}
		return logs[i].Message < logs[j].Message
	})
	if len(logs) > explorerFrostDetailLogMax {
		logs = logs[len(logs)-explorerFrostDetailLogMax:]
	}
	return logs
}

func (s *server) fetchFrostWithdrawDetailJSON(ctx context.Context, node, withdrawID string) ([]byte, error) {
	if s == nil {
		return nil, errors.New("explorer server is not initialized")
	}
	s.ensureFrostWithdrawCaches()
	primary := s.pickNode(node)
	withdrawID = strings.TrimSpace(withdrawID)
	if primary == "" {
		return nil, errors.New("no node configured")
	}
	if withdrawID == "" || len(withdrawID) > 256 {
		return nil, errFrostWithdrawDetailMissing
	}
	cacheKey := strings.Join([]string{s.frostWithdrawMonitorRevision(), primary, withdrawID}, "\x00")
	return s.withdrawDetailCache.GetOrLoad(ctx, cacheKey, explorerFrostDetailCacheTTL, func(context.Context) ([]byte, int64, error) {
		timeout := s.timeout
		if timeout <= 0 {
			timeout = 8 * time.Second
		}
		loadCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		path := "/frost/withdraw/status?withdraw_id=" + url.QueryEscape(withdrawID)

		var canonical pb.GetWithdrawStatusResponse
		if err := s.fetchProto(loadCtx, primary, path, nil, &canonical); err != nil {
			if strings.Contains(err.Error(), "status 404") {
				return nil, 0, errFrostWithdrawDetailMissing
			}
			return nil, 0, err
		}

		nodes := s.frostWithdrawLogNodes(primary)
		secondaries := nodes
		if len(secondaries) > 0 {
			secondaries = secondaries[1:]
		}
		type detailResult struct {
			logs    []*pb.FrostPlanningLog
			lotID   string
			chain   string
			queried bool
			err     error
		}
		results := make([]detailResult, len(secondaries))
		workerCount := min(explorerFrostMonitorWorkers, len(secondaries))
		if workerCount > 0 {
			jobs := make(chan int)
			var wg sync.WaitGroup
			for worker := 0; worker < workerCount; worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for index := range jobs {
						var response pb.GetWithdrawStatusResponse
						err := s.fetchProto(loadCtx, secondaries[index], path, nil, &response)
						results[index] = detailResult{
							logs: response.PlanningLogs, lotID: response.LotId, chain: response.Chain, queried: true, err: err,
						}
					}
				}()
			}
		dispatch:
			for index := range secondaries {
				select {
				case jobs <- index:
				case <-loadCtx.Done():
					break dispatch
				}
			}
			close(jobs)
			wg.Wait()
		}

		groups := make([][]*pb.FrostPlanningLog, 0, len(results)+1)
		groups = append(groups, canonical.PlanningLogs)
		sourceCount := 1
		failedSources := 0
		for _, result := range results {
			if !result.queried || result.err != nil || result.lotID != canonical.LotId || !strings.EqualFold(result.chain, canonical.Chain) {
				failedSources++
				continue
			}
			sourceCount++
			groups = append(groups, result.logs)
		}
		response := FrostWithdrawDetailResponse{
			WithdrawID:     canonical.LotId,
			Chain:          canonical.Chain,
			Status:         canonical.Status,
			Amount:         canonical.Amount,
			Receiver:       canonical.Receiver,
			Height:         canonical.Height,
			JobID:          canonical.TxHash,
			PlanningLogs:   mergeExplorerPlanningLogsCapped(groups...),
			LogSourceCount: sourceCount,
			QueriedNodes:   len(nodes),
			Partial:        failedSources > 0,
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			return nil, 0, err
		}
		return encoded, int64(len(encoded)), nil
	})
}

func (s *server) handleFrostWithdrawDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, APIError{Error: "method not allowed", Code: "METHOD_NOT_ALLOWED"})
		return
	}
	timeout := s.timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	data, err := s.fetchFrostWithdrawDetailJSON(ctx, r.URL.Query().Get("node"), r.URL.Query().Get("withdraw_id"))
	if err != nil {
		status := http.StatusBadGateway
		code := "WITHDRAW_DETAIL_UNAVAILABLE"
		if errors.Is(err, errFrostWithdrawDetailMissing) {
			status = http.StatusNotFound
			code = "WITHDRAW_NOT_FOUND"
		} else if errors.Is(err, errExplorerCacheBusy) {
			status = http.StatusServiceUnavailable
		}
		writeJSONStatus(w, status, APIError{Error: err.Error(), Code: code})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

                                                                          
type WitnessRequestItem struct {
	ArbitrationStage            string            `json:"arbitration_stage,omitempty"`
	BallotRound                 string            `json:"ballot_round,omitempty"`
	ArbitrationPeriodBlocks     uint64            `json:"arbitration_period_blocks,omitempty"`
	ArbitrationRequiredVotes    uint32            `json:"arbitration_required_votes,omitempty"`
	OrdinaryCommitteeSizes      []uint32          `json:"ordinary_committee_sizes,omitempty"`
	OrdinaryThresholdBps        uint32            `json:"ordinary_threshold_bps,omitempty"`
	FinalCourt                  []string          `json:"final_court,omitempty"`
	CourtSnapshotHeight         uint64            `json:"court_snapshot_height,omitempty"`
	ChallengeSettled            bool              `json:"challenge_settled,omitempty"`
	ChallengeRequiredStake      string            `json:"challenge_required_stake,omitempty"`
	ChallengeStakeAmount        string            `json:"challenge_stake_amount,omitempty"`
	ChallengePublicityDeadline  uint64            `json:"challenge_publicity_deadline_height,omitempty"`
	RequestID                   string            `json:"request_id"`
	NativeChain                 string            `json:"native_chain"`
	NativeTxHash                string            `json:"native_tx_hash"`
	NativeContract              string            `json:"native_contract,omitempty"`
	TokenAddress                string            `json:"token_address"`
	Amount                      string            `json:"amount"`
	EconomicsPolicy             uint32            `json:"economics_policy,omitempty"`
	GrossAmount                 string            `json:"gross_amount,omitempty"`
	ReceiverNetAmount           string            `json:"receiver_net_amount,omitempty"`
	RequesterRewardAmount       string            `json:"requester_reward_amount,omitempty"`
	WitnessRewardAmount         string            `json:"witness_reward_amount,omitempty"`
	ReceiverAddress             string            `json:"receiver_address"`
	RequesterAddress            string            `json:"requester_address"`
	Status                      string            `json:"status"`
	CreateHeight                uint64            `json:"create_height"`
	SelectionHeight             uint64            `json:"selection_height,omitempty"`
	DecisionHeight              uint64            `json:"decision_height,omitempty"`
	VoteAcceptUntilHeight       uint64            `json:"vote_accept_until_height,omitempty"`
	DeadlineHeight              uint64            `json:"deadline_height"`
	LastVotingDeadlineHeight    uint64            `json:"last_voting_deadline_height,omitempty"`
	ShelveHeight                uint64            `json:"shelve_height,omitempty"`
	ShelveReason                string            `json:"shelve_reason,omitempty"`
	FinalizeHeight              uint64            `json:"finalize_height"`
	Round                       uint32            `json:"round"`
	PassCount                   uint32            `json:"pass_count"`
	FailCount                   uint32            `json:"fail_count"`
	AbstainCount                uint32            `json:"abstain_count"`
	VaultID                     uint32            `json:"vault_id"`
	SelectedWitnesses           []string          `json:"selected_witnesses"`
	Votes                       []WitnessVoteItem `json:"votes"`
	OriginalPositions           []WitnessVoteItem `json:"original_positions,omitempty"`
	ChallengeID                 string            `json:"challenge_id,omitempty"`
	ChallengeStatus             string            `json:"challenge_status,omitempty"`
	ChallengeFinalized          bool              `json:"challenge_finalized,omitempty"`
	ChallengeSuccess            bool              `json:"challenge_success,omitempty"`
	ChallengeCreateHeight       uint64            `json:"challenge_create_height,omitempty"`
	ChallengeSelectionHeight    uint64            `json:"challenge_selection_height,omitempty"`
	ChallengeFinalizeHeight     uint64            `json:"challenge_finalize_height,omitempty"`
	ChallengeFinalVote          string            `json:"challenge_final_vote,omitempty"`
	ChallengerAddress           string            `json:"challenger_address,omitempty"`
	OriginalVotes               []WitnessVoteItem `json:"original_votes,omitempty"`
	OrdinaryVotes               []WitnessVoteItem `json:"ordinary_votes,omitempty"`
	OrdinaryVotesFrozen         bool              `json:"ordinary_votes_frozen,omitempty"`
	ChallengeDeadlineHeight     uint64            `json:"challenge_deadline_height,omitempty"`
	ArbitrationRound            uint32            `json:"arbitration_round,omitempty"`
	ArbitrationPassCount        uint32            `json:"arbitration_pass_count,omitempty"`
	ArbitrationFailCount        uint32            `json:"arbitration_fail_count,omitempty"`
	ChallengeConsensusThreshold uint32            `json:"challenge_consensus_threshold,omitempty"`
	Arbitrators                 []string          `json:"arbitrators,omitempty"`
	ArbitrationVotes            []WitnessVoteItem `json:"arbitration_votes,omitempty"`
}

                                                                                              
func applyWitnessChallengeFields(item *WitnessRequestItem, rec *pb.ChallengeRecord) {
	if item == nil || rec == nil {
		return
	}
	item.ChallengeStatus = rec.Status.String()
	item.ChallengeFinalized, item.ChallengeSuccess = rec.Finalized, rec.ChallengeSuccess
	item.ChallengeCreateHeight, item.ChallengeSelectionHeight = rec.CreateHeight, rec.SelectionHeight
	item.ChallengeFinalizeHeight = rec.FinalizeHeight
	                                                                       
	                                                                            
	item.ChallengeFinalVote = ""
	if rec.Finalized && rec.BallotRound > 0 {
		item.ChallengeFinalVote = rec.FinalVote.String()
	}
	item.ChallengerAddress = rec.ChallengerAddress
	item.OriginalVotes, item.OrdinaryVotes = convertVotes(rec.OriginalVotes), convertVotes(rec.OrdinaryVotes)
	item.OrdinaryVotesFrozen = rec.OrdinaryVotesFrozen
	item.ChallengeDeadlineHeight = rec.DeadlineHeight
	item.ArbitrationRound, item.ArbitrationStage = rec.ArbitrationRound, rec.ArbitrationStage.String()
	item.BallotRound = strconv.FormatUint(rec.BallotRound, 10)
	item.ArbitrationPeriodBlocks = witnessprotocol.ArbitrationRoundPeriod(rec)
	item.ArbitrationPassCount, item.ArbitrationFailCount = rec.PassCount, rec.FailCount
	item.OrdinaryCommitteeSizes = append([]uint32(nil), rec.OrdinaryCommitteeSizes...)
	item.OrdinaryThresholdBps = rec.OrdinaryThresholdBps
	item.FinalCourt = append([]string(nil), rec.FinalCourt...)
	item.CourtSnapshotHeight = rec.CourtSnapshotHeight
	item.ChallengeSettled = rec.Settled
	item.ChallengeRequiredStake, item.ChallengeStakeAmount = rec.RequiredStake, rec.StakeAmount
	item.ChallengePublicityDeadline = rec.PublicityDeadlineHeight
	                                                                                     
	if quorum, err := witnessprotocol.ArbitrationQuorum(rec); err == nil {
		item.ArbitrationRequiredVotes = quorum
	}
}

func rechargeShelveFields(req *pb.RechargeRequest) (uint64, string) {
	if req == nil || req.Status != pb.RechargeRequestStatus_RECHARGE_SHELVED {
		return 0, ""
	}
	return req.DeadlineHeight, "NO_ELIGIBLE_REPLACEMENT_WITNESSES"
}

                                                                         
                                                                               
                                           
func witnessRequestAsset(req *pb.RechargeRequest) string {
	if req == nil {
		return ""
	}
	if asset := strings.TrimSpace(req.TokenAddress); asset != "" {
		return asset
	}
	identity, err := bridgeasset.Resolve(req.NativeChain, req.NativeContract)
	if err != nil {
		return ""
	}
	return identity.ID
}

func witnessChallengeBatchPath(requests []*pb.RechargeRequest) string {
	seen := make(map[string]struct{}, len(requests))
	ids := make([]string, 0, len(requests))
	for _, req := range requests {
		if req == nil || req.ChallengeId == "" {
			continue
		}
		if _, duplicate := seen[req.ChallengeId]; duplicate {
			continue
		}
		seen[req.ChallengeId] = struct{}{}
		ids = append(ids, req.ChallengeId)
		if len(ids) == 100 {
			break
		}
	}
	if len(ids) == 0 {
		return ""
	}
	q := url.Values{}
	q.Set("challenge_ids", strings.Join(ids, ","))
	return "/witness/challenges?" + q.Encode()
}

func (s *server) handleWitnessRequests(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	query := url.Values{"limit": {"100"}}
	if r.URL.Query().Has("request_id") {
		requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
		if requestID == "" || len(requestID) > 256 {
			http.Error(w, "invalid request_id", http.StatusBadRequest)
			return
		}
		query = url.Values{"request_id": {requestID}}
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	                                                                                     
	var resp pb.RechargeRequestList
	if err := s.fetchProto(ctx, node, "/witness/requests?"+query.Encode(), nil, &resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	                                                                     
	arbitratorsByReqID := make(map[string][]string)
	arbitrationVotesByReqID := make(map[string][]WitnessVoteItem)
	challengeByReqID := make(map[string]*pb.ChallengeRecord)
	var challengeResp pb.ChallengeRecordList
	challengePath := witnessChallengeBatchPath(resp.Requests)
	if challengePath != "" {
		if err := s.fetchProto(ctx, node, challengePath, nil, &challengeResp); err == nil {
			for _, rec := range challengeResp.Records {
				if rec.RequestId != "" {
					challengeByReqID[rec.RequestId] = rec
					arbitratorsByReqID[rec.RequestId] = rec.Arbitrators
					if len(rec.ArbitrationVotes) > 0 {
						arbitrationVotesByReqID[rec.RequestId] = convertVotes(rec.ArbitrationVotes)
					}
				}
			}
		}
	}

	                                        
	items := make([]WitnessRequestItem, 0, len(resp.Requests))
	for _, req := range resp.Requests {
		lastVotingDeadline, shelveReason := rechargeShelveFields(req)
		item := WitnessRequestItem{
			RequestID:                req.RequestId,
			NativeChain:              req.NativeChain,
			NativeTxHash:             req.NativeTxHash,
			NativeContract:           req.NativeContract,
			TokenAddress:             witnessRequestAsset(req),
			Amount:                   req.Amount,
			EconomicsPolicy:          req.EconomicsPolicy,
			GrossAmount:              req.GrossAmount,
			ReceiverNetAmount:        req.ReceiverNetAmount,
			RequesterRewardAmount:    req.RequesterRewardAmount,
			WitnessRewardAmount:      req.WitnessRewardAmount,
			ReceiverAddress:          req.ReceiverAddress,
			RequesterAddress:         req.RequesterAddress,
			Status:                   req.Status.String(),
			CreateHeight:             req.CreateHeight,
			SelectionHeight:          req.SelectionHeight,
			DecisionHeight:           req.DecisionHeight,
			VoteAcceptUntilHeight:    req.VoteAcceptUntilHeight,
			DeadlineHeight:           req.DeadlineHeight,
			LastVotingDeadlineHeight: lastVotingDeadline,
			ShelveHeight:             req.ShelveHeight,
			ShelveReason:             shelveReason,
			FinalizeHeight:           req.FinalizeHeight,
			Round:                    req.Round,
			PassCount:                req.PassCount,
			FailCount:                req.FailCount,
			AbstainCount:             req.AbstainCount,
			VaultID:                  req.VaultId,
			SelectedWitnesses:        req.SelectedWitnesses,
			Votes:                    convertVotes(req.Votes),
			OriginalPositions:        convertVotes(req.OriginalPositions),
			ChallengeID:              req.ChallengeId,
			Arbitrators:              arbitratorsByReqID[req.RequestId],
			ArbitrationVotes:         arbitrationVotesByReqID[req.RequestId],
		}
		if rec := challengeByReqID[req.RequestId]; rec != nil {
			applyWitnessChallengeFields(&item, rec)
		}
		items = append(items, item)
	}

	writeJSON(w, items)
}

                                                                                                       
func (s *server) handleWitnessMyRequests(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)
	witness := r.URL.Query().Get("witness")
	if witness == "" {
		writeJSON(w, []WitnessRequestItem{})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	var resp pb.RechargeRequestList
	path := fmt.Sprintf("/witness/my_requests?witness=%s&limit=100&include_history=1", url.QueryEscape(witness))
	if err := s.fetchProto(ctx, node, path, nil, &resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	challengeByID := make(map[string]*pb.ChallengeRecord)
	challengeByReqID := make(map[string]*pb.ChallengeRecord)
	var challengeResp pb.ChallengeRecordList
	challengePath := witnessChallengeBatchPath(resp.Requests)
	if challengePath != "" {
		if err := s.fetchProto(ctx, node, challengePath, nil, &challengeResp); err == nil {
			for _, rec := range challengeResp.Records {
				if rec.ChallengeId != "" {
					challengeByID[rec.ChallengeId] = rec
				}
				if rec.RequestId != "" {
					challengeByReqID[rec.RequestId] = rec
				}
			}
		}
	}

	items := make([]WitnessRequestItem, 0, len(resp.Requests))
	for _, req := range resp.Requests {
		lastVotingDeadline, shelveReason := rechargeShelveFields(req)
		item := WitnessRequestItem{
			RequestID:                req.RequestId,
			NativeChain:              req.NativeChain,
			NativeTxHash:             req.NativeTxHash,
			NativeContract:           req.NativeContract,
			TokenAddress:             witnessRequestAsset(req),
			Amount:                   req.Amount,
			EconomicsPolicy:          req.EconomicsPolicy,
			GrossAmount:              req.GrossAmount,
			ReceiverNetAmount:        req.ReceiverNetAmount,
			RequesterRewardAmount:    req.RequesterRewardAmount,
			WitnessRewardAmount:      req.WitnessRewardAmount,
			ReceiverAddress:          req.ReceiverAddress,
			RequesterAddress:         req.RequesterAddress,
			Status:                   req.Status.String(),
			CreateHeight:             req.CreateHeight,
			SelectionHeight:          req.SelectionHeight,
			DecisionHeight:           req.DecisionHeight,
			VoteAcceptUntilHeight:    req.VoteAcceptUntilHeight,
			DeadlineHeight:           req.DeadlineHeight,
			LastVotingDeadlineHeight: lastVotingDeadline,
			ShelveHeight:             req.ShelveHeight,
			ShelveReason:             shelveReason,
			FinalizeHeight:           req.FinalizeHeight,
			Round:                    req.Round,
			PassCount:                req.PassCount,
			FailCount:                req.FailCount,
			AbstainCount:             req.AbstainCount,
			VaultID:                  req.VaultId,
			SelectedWitnesses:        req.SelectedWitnesses,
			Votes:                    convertVotes(req.Votes),
			OriginalPositions:        convertVotes(req.OriginalPositions),
			ChallengeID:              req.ChallengeId,
		}
		rec := challengeByReqID[req.RequestId]
		if rec == nil && req.ChallengeId != "" {
			rec = challengeByID[req.ChallengeId]
		}
		if rec != nil {
			applyWitnessChallengeFields(&item, rec)
			item.Arbitrators = rec.Arbitrators
			item.ArbitrationVotes = convertVotes(rec.ArbitrationVotes)
		}
		items = append(items, item)
	}
	writeJSON(w, items)
}

func convertVotes(protoVotes []*pb.WitnessVote) []WitnessVoteItem {
	if protoVotes == nil {
		return nil
	}
	votes := make([]WitnessVoteItem, 0, len(protoVotes))
	for _, v := range protoVotes {
		if v == nil {
			continue
		}
		votes = append(votes, WitnessVoteItem{
			BallotRound:    strconv.FormatUint(v.BallotRound, 10),
			Round:          v.Round,
			RequestId:      v.RequestId,
			WitnessAddress: v.WitnessAddress,
			VoteType:       int32(v.VoteType),
			Status:         v.VoteType.String(),
			Reason:         v.Reason,
			Timestamp:      v.Timestamp,
			TxId:           v.TxId,
		})
	}
	return votes
}

                                                                     
type DKGSessionItem struct {
	Chain               string             `json:"chain"`
	VaultID             uint32             `json:"vault_id"`
	EpochID             uint64             `json:"epoch_id"`
	SignAlgo            string             `json:"sign_algo"`
	TriggerHeight       uint64             `json:"trigger_height"`
	OldCommitteeMembers []string           `json:"old_committee_members"`
	NewCommitteeMembers []string           `json:"new_committee_members"`
	DkgStatus           string             `json:"dkg_status"`
	DkgSessionID        string             `json:"dkg_session_id"`
	DkgThresholdT       uint32             `json:"dkg_threshold_t"`
	DkgN                uint32             `json:"dkg_n"`
	DkgCommitDeadline   uint64             `json:"dkg_commit_deadline"`
	DkgDisputeDeadline  uint64             `json:"dkg_dispute_deadline"`
	DkgSharingDeadline  uint64             `json:"dkg_sharing_deadline"`
	DkgAttempt          uint32             `json:"dkg_attempt"`
	DkgDisqualified     []string           `json:"dkg_disqualified_members,omitempty"`
	ValidationStatus    string             `json:"validation_status"`
	Lifecycle           string             `json:"lifecycle"`
	VaultStatus         string             `json:"vault_status"`
	NewGroupPubkey      string             `json:"new_group_pubkey,omitempty"`
	OldGroupPubkey      string             `json:"old_group_pubkey,omitempty"`
	ChainAddresses      map[string]string  `json:"chain_addresses,omitempty"`
	MigrationJobId      string             `json:"migration_job_id,omitempty"`
	MigrationStatus     string             `json:"migration_status,omitempty"`
	MigrationError      string             `json:"migration_error,omitempty"`
	CurrentHeight       uint64             `json:"current_height,omitempty"`
	EstimatedBlockSecs  uint64             `json:"estimated_block_seconds,omitempty"`
	ManagedAssets       []string           `json:"managed_assets,omitempty"`
	VaultFunds          []DKGVaultFundItem `json:"vault_funds,omitempty"`
}

type FrostWithdrawAvailabilityResponse struct {
	CurrentHeight         uint64                           `json:"current_height"`
	EstimatedBlockSeconds uint64                           `json:"estimated_block_seconds"`
	Chains                []FrostWithdrawChainAvailability `json:"chains"`
}

type FrostWithdrawChainAvailability struct {
	Chain                 string   `json:"chain"`
	Status                string   `json:"status"`
	WithdrawEnabled       bool     `json:"withdraw_enabled"`
	CurrentHeight         uint64   `json:"current_height"`
	TriggerHeight         uint64   `json:"trigger_height,omitempty"`
	EstimatedResumeHeight uint64   `json:"estimated_resume_height,omitempty"`
	RemainingBlocks       uint64   `json:"remaining_blocks,omitempty"`
	EstimatedSeconds      uint64   `json:"estimated_seconds,omitempty"`
	Phase                 string   `json:"phase,omitempty"`
	Reason                string   `json:"reason"`
	VaultIDs              []uint32 `json:"vault_ids,omitempty"`
}

func frostWithdrawEstimatedBlockSeconds() uint64 {
	interval := config.DefaultConfig().Consensus.ProposalInterval
	if interval <= 0 {
		return 5
	}
	seconds := uint64((interval + time.Second - 1) / time.Second)
	if seconds == 0 {
		return 1
	}
	return seconds
}

func frostWithdrawTransitionPhase(transition *pb.VaultTransitionState) string {
	if transition == nil {
		return ""
	}
	if transition.DkgStatus == "KEY_READY" && transition.MigrationStatus != "" {
		return transition.MigrationStatus
	}
	if transition.DkgStatus != "" {
		return transition.DkgStatus
	}
	return transition.MigrationStatus
}

func frostWithdrawEstimatedResumeHeight(currentHeight uint64, transition *pb.VaultTransitionState) uint64 {
	if transition == nil || transition.Lifecycle != "DRAINING" || transition.DkgStatus == "FAILED" || transition.DkgStatus == "KEY_READY" {
		return 0
	}
	if transition.DkgDisputeDeadline > currentHeight {
		return transition.DkgDisputeDeadline
	}
	return 0
}

func buildFrostWithdrawAvailability(currentHeight, blockSeconds uint64, transitions []*pb.VaultTransitionState, vaults []*pb.FrostVaultState) FrostWithdrawAvailabilityResponse {
	if blockSeconds == 0 {
		blockSeconds = 1
	}
	transitionsByEpoch := make(map[string]*pb.VaultTransitionState, len(transitions))
	for _, transition := range transitions {
		if transition == nil {
			continue
		}
		chainName := frostchain.NormalizeChain(transition.Chain)
		if chainName == "" {
			continue
		}
		key := fmt.Sprintf("%s/%d/%d", chainName, transition.VaultId, transition.EpochId)
		transitionsByEpoch[key] = transition
	}

	vaultsByChain := make(map[string][]*pb.FrostVaultState)
	for _, vault := range vaults {
		if vault == nil {
			continue
		}
		chainName := frostchain.NormalizeChain(vault.Chain)
		if chainName == "" {
			continue
		}
		vaultsByChain[chainName] = append(vaultsByChain[chainName], vault)
	}

	chains := make([]string, 0, len(vaultsByChain))
	for chainName := range vaultsByChain {
		chains = append(chains, chainName)
	}
	sort.Strings(chains)

	response := FrostWithdrawAvailabilityResponse{
		CurrentHeight: currentHeight, EstimatedBlockSeconds: blockSeconds,
		Chains: make([]FrostWithdrawChainAvailability, 0, len(chains)),
	}
	for _, chainName := range chains {
		chainVaults := vaultsByChain[chainName]
		sort.SliceStable(chainVaults, func(i, j int) bool { return chainVaults[i].VaultId < chainVaults[j].VaultId })
		item := FrostWithdrawChainAvailability{
			Chain: chainName, CurrentHeight: currentHeight, Status: "UNAVAILABLE", Reason: "no_active_vault",
		}
		activeVaults := 0
		usableVaults := 0
		blockers := make([]*pb.VaultTransitionState, 0, len(chainVaults))
		for _, vault := range chainVaults {
			if vault.Status != "ACTIVE" {
				continue
			}
			activeVaults++
			key := fmt.Sprintf("%s/%d/%d", chainName, vault.VaultId, vault.KeyEpoch+1)
			transition := transitionsByEpoch[key]
			if transition != nil && transition.Lifecycle == "DRAINING" {
				blockers = append(blockers, transition)
				continue
			}
			usableVaults++
		}

		if activeVaults == 0 {
			response.Chains = append(response.Chains, item)
			continue
		}
		if len(blockers) == 0 {
			item.Status = "AVAILABLE"
			item.WithdrawEnabled = true
			item.Reason = "active_vault_available"
			response.Chains = append(response.Chains, item)
			continue
		}

		item.WithdrawEnabled = usableVaults > 0
		item.Status = "PLANNED"
		item.Reason = "vault_handover_planned"
		allPlanned := true
		failed := false
		var estimatedResume uint64
		for _, transition := range blockers {
			item.VaultIDs = append(item.VaultIDs, transition.VaultId)
			if item.TriggerHeight == 0 || (transition.TriggerHeight > 0 && transition.TriggerHeight < item.TriggerHeight) {
				item.TriggerHeight = transition.TriggerHeight
			}
			if transition.TriggerHeight == 0 || transition.TriggerHeight <= currentHeight {
				allPlanned = false
			}
			if transition.DkgStatus == "FAILED" {
				failed = true
			}
			candidate := frostWithdrawEstimatedResumeHeight(currentHeight, transition)
			if candidate > 0 && (estimatedResume == 0 || candidate < estimatedResume) {
				estimatedResume = candidate
			}
			if item.Phase == "" || transition.TriggerHeight <= currentHeight {
				item.Phase = frostWithdrawTransitionPhase(transition)
			}
		}
		if failed {
			item.Status = "FAILED"
			item.Reason = "vault_handover_failed"
		} else if !allPlanned {
			item.Status = "ROTATING"
			item.Reason = "vault_handover_in_progress"
		}
		if item.WithdrawEnabled {
			item.Reason = "alternate_active_vault_available"
		}
		item.EstimatedResumeHeight = estimatedResume
		if estimatedResume > currentHeight {
			item.RemainingBlocks = estimatedResume - currentHeight
			item.EstimatedSeconds = item.RemainingBlocks * blockSeconds
		}
		response.Chains = append(response.Chains, item)
	}
	return response
}

                                                                             
                                                                          
                                                                         
type FrostVaultOverviewItem struct {
	Chain              string             `json:"chain"`
	VaultID            uint32             `json:"vault_id"`
	KeyEpoch           uint64             `json:"key_epoch"`
	GroupPubkey        string             `json:"group_pubkey,omitempty"`
	AuthorityProtocol  uint32             `json:"authority_protocol"`
	GuardianPubkey     string             `json:"guardian_pubkey,omitempty"`
	SignAlgo           string             `json:"sign_algo"`
	Status             string             `json:"status"`
	ActiveSinceHeight  uint64             `json:"active_since_height"`
	CommitteeMembers   []string           `json:"committee_members"`
	CommitteeThreshold uint32             `json:"committee_threshold"`
	VaultRef           string             `json:"vault_ref,omitempty"`
	ChainID            string             `json:"chain_id,omitempty"`
	ManagedAssets      []string           `json:"managed_assets,omitempty"`
	VaultFunds         []DKGVaultFundItem `json:"vault_funds,omitempty"`
}

type DKGVaultFundItem struct {
	Chain            string `json:"chain"`
	Asset            string `json:"asset"`
	VaultID          uint32 `json:"vault_id"`
	TotalBalance     string `json:"total_balance,omitempty"`
	AvailableBalance string `json:"available_balance"`
}

type VaultActivityItem struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Chain       string `json:"chain"`
	Asset       string `json:"asset,omitempty"`
	Amount      string `json:"amount,omitempty"`
	Status      string `json:"status"`
	Height      uint64 `json:"height,omitempty"`
	TxID        string `json:"tx_id,omitempty"`
	RequestTxID string `json:"request_tx_id,omitempty"`
	Target      string `json:"target,omitempty"`
	JobID       string `json:"job_id,omitempty"`
}

type VaultActivityResponse struct {
	Items    []VaultActivityItem `json:"items"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
	HasMore  bool                `json:"has_more"`
}

type dkgVaultFundsResponse struct {
	Funds []DKGVaultFundItem `json:"funds"`
}

func dkgVaultKey(chain string, vaultID uint32) string {
	return fmt.Sprintf("%s:%d", strings.ToUpper(strings.TrimSpace(chain)), vaultID)
}

func normalizeAsset(asset string) string {
	a := strings.TrimSpace(asset)
	if a == "" {
		return "native"
	}
	return a
}

func parseExplorerVaultAvailableKey(key, chainFilter string) (string, string, uint32, bool) {
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

func (s *server) fetchFrostVaultFunds(ctx context.Context, node, chain string) []DKGVaultFundItem {
	if funds, err := s.fetchNodeFrostVaultFunds(ctx, node, chain); err == nil {
		return funds
	}
	return s.fetchLocalFrostVaultFunds(chain)
}

func (s *server) fetchNodeFrostVaultFunds(ctx context.Context, node, chain string) ([]DKGVaultFundItem, error) {
	node = s.pickNode(node)
	if node == "" {
		return nil, fmt.Errorf("node is not allowed")
	}
	path := "/frost/vault/funds"
	if strings.TrimSpace(chain) != "" {
		path += "?chain=" + url.QueryEscape(strings.ToLower(strings.TrimSpace(chain)))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s%s", node, path), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Node-Address", "explorer-api")

	resp, err := s.nodeDo(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := readResponseBody(resp)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	data, err := readResponseBody(resp)
	if err != nil {
		return nil, err
	}
	var out dkgVaultFundsResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out.Funds, func(i, j int) bool {
		if out.Funds[i].Chain != out.Funds[j].Chain {
			return out.Funds[i].Chain < out.Funds[j].Chain
		}
		if out.Funds[i].VaultID != out.Funds[j].VaultID {
			return out.Funds[i].VaultID < out.Funds[j].VaultID
		}
		return out.Funds[i].Asset < out.Funds[j].Asset
	})
	return out.Funds, nil
}

const maxExplorerLocalVaultFundRows = 4096

func (s *server) fetchLocalFrostVaultFunds(chain string) []DKGVaultFundItem {
	nodeDBs := s.nodeDBs
	if len(nodeDBs) == 0 && s.nodeDB != nil {
		nodeDBs = append(nodeDBs, s.nodeDB)
	}
	if len(nodeDBs) == 0 {
		return nil
	}

	chain = strings.ToLower(strings.TrimSpace(chain))
	prefix := keys.KeyFrostVaultAvailableAllPrefix()
	if chain != "" {
		prefix = keys.KeyFrostVaultAvailablePrefix(chain)
	}
	byKey := make(map[string]DKGVaultFundItem)
	order := make([]string, 0)
	for _, nodeDB := range nodeDBs {
		if nodeDB == nil {
			continue
		}
		rows, err := nodeDB.ScanKVWithLimitOrdered(prefix, maxExplorerLocalVaultFundRows, false)
		if err != nil {
			continue
		}
		for _, row := range rows {
			parsedChain, asset, vaultID, ok := parseExplorerVaultAvailableKey(row.Key, chain)
			if !ok {
				continue
			}
			fundKey := dkgVaultKey(parsedChain, vaultID) + ":" + asset
			if _, exists := byKey[fundKey]; !exists {
				order = append(order, fundKey)
			}
			byKey[fundKey] = DKGVaultFundItem{
				Chain:            parsedChain,
				Asset:            asset,
				VaultID:          vaultID,
				AvailableBalance: string(row.Value),
			}
		}
	}
	sort.Strings(order)
	funds := make([]DKGVaultFundItem, 0, len(order))
	for _, k := range order {
		funds = append(funds, byKey[k])
	}
	return funds
}

func mergeDKGAssets(assets []string, funds []DKGVaultFundItem) []string {
	if len(assets) == 0 && len(funds) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(assets)+len(funds))
	for _, asset := range assets {
		if asset = strings.TrimSpace(asset); asset != "" {
			set[asset] = struct{}{}
		}
	}
	for _, fund := range funds {
		if asset := normalizeAsset(fund.Asset); asset != "" {
			set[asset] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for asset := range set {
		out = append(out, asset)
	}
	sort.Strings(out)
	return out
}

const maxExplorerVaultActivityOffset = 5000

func parseVaultActivityPage(r *http.Request) (page, pageSize, offset int) {
	page = 1
	pageSize = 6
	q := r.URL.Query()
	if raw := strings.TrimSpace(q.Get("page")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			page = v
		}
	}
	if raw := strings.TrimSpace(q.Get("page_size")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			pageSize = v
		}
	}
	if pageSize > 50 {
		pageSize = 50
	}
	maxPage := maxExplorerVaultActivityOffset/pageSize + 1
	if page > maxPage {
		page = maxPage
	}
	offset = (page - 1) * pageSize
	return page, pageSize, offset
}

func rechargeRequestActivityHeight(req *pb.RechargeRequest) uint64 {
	if req == nil {
		return 0
	}
	if req.FinalizeHeight > 0 {
		return req.FinalizeHeight
	}
	return req.CreateHeight
}

func frostWithdrawActivityHeight(st *pb.FrostWithdrawState) uint64 {
	if st == nil {
		return 0
	}
	return st.RequestHeight
}

func (s *server) fetchVaultWitnessActivity(ctx context.Context, node, chain string, vaultID uint32, limit, offset int) ([]VaultActivityItem, error) {
	v := url.Values{}
	v.Set("limit", strconv.Itoa(limit))
	v.Set("offset", strconv.Itoa(offset))
	v.Set("order", "desc")
	v.Set("chain", chain)
	v.Set("vault_id", strconv.FormatUint(uint64(vaultID), 10))
	var resp pb.RechargeRequestList
	if err := s.fetchProto(ctx, node, "/witness/requests?"+v.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	items := make([]VaultActivityItem, 0, len(resp.Requests))
	for _, req := range resp.Requests {
		if req == nil || !strings.EqualFold(req.NativeChain, chain) || req.VaultId != vaultID {
			continue
		}
		txID := strings.TrimSpace(req.NativeTxHash)
		items = append(items, VaultActivityItem{
			Kind:        "witness",
			ID:          req.RequestId,
			Chain:       req.NativeChain,
			Asset:       witnessRequestAsset(req),
			Amount:      req.Amount,
			Status:      req.Status.String(),
			Height:      rechargeRequestActivityHeight(req),
			TxID:        txID,
			RequestTxID: req.RequestId,
			Target:      req.ReceiverAddress,
		})
	}
	return items, nil
}

func (s *server) fetchVaultWithdrawActivity(ctx context.Context, node, chain string, vaultID uint32, limit, offset int) ([]VaultActivityItem, error) {
	path := frostWithdrawListPathWithOptions(chain, "", &vaultID, limit, offset, "desc")
	var resp pb.FrostWithdrawStateList
	if err := s.fetchProto(ctx, node, path, nil, &resp); err != nil {
		                                                                     
		                                                                        
		                                                          
		return nil, err
	}
	items := make([]VaultActivityItem, 0, len(resp.States))
	for _, st := range resp.States {
		if st == nil || st.VaultId != vaultID || !strings.EqualFold(st.Chain, chain) {
			continue
		}
		status := st.Status
		if derived, _ := deriveWithdrawDebugStatus(st.Status, augmentWithdrawPlanningLogs(st)); derived != "" {
			status = derived
		}
		txID := strings.TrimSpace(st.SignedTxId)
		if txID == "" {
			txID = strings.TrimSpace(st.TxId)
		}
		items = append(items, VaultActivityItem{
			Kind:        "withdraw",
			ID:          st.WithdrawId,
			Chain:       st.Chain,
			Asset:       normalizeAsset(st.Asset),
			Amount:      st.Amount,
			Status:      status,
			Height:      st.RequestHeight,
			TxID:        txID,
			RequestTxID: st.TxId,
			Target:      st.To,
			JobID:       st.JobId,
		})
	}
	return items, nil
}

func (s *server) handleFrostVaultActivity(w http.ResponseWriter, r *http.Request) {
	node := s.pickNode(r.URL.Query().Get("node"))
	chain := strings.TrimSpace(r.URL.Query().Get("chain"))
	if chain == "" {
		http.Error(w, "chain is required", http.StatusBadRequest)
		return
	}
	vaultID64, err := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get("vault_id")), 10, 32)
	if err != nil {
		http.Error(w, "vault_id is required", http.StatusBadRequest)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))
	if kind == "" {
		kind = "witness"
	}
	page, pageSize, offset := parseVaultActivityPage(r)
	limit := pageSize + 1

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	var items []VaultActivityItem
	switch kind {
	case "witness":
		items, err = s.fetchVaultWitnessActivity(ctx, node, chain, uint32(vaultID64), limit, offset)
	case "withdraw":
		items, err = s.fetchVaultWithdrawActivity(ctx, node, chain, uint32(vaultID64), limit, offset)
	default:
		http.Error(w, "kind must be witness or withdraw", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hasMore := len(items) > pageSize
	if hasMore {
		items = items[:pageSize]
	}
	writeJSON(w, VaultActivityResponse{Items: items, Page: page, PageSize: pageSize, HasMore: hasMore})
}

func (s *server) handleFrostDKGSessions(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	                                     
	var resp pb.VaultTransitionStateList
	if err := s.fetchProto(ctx, node, "/frost/dkg/list", nil, &resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	currentHeight := uint64(0)
	if height, heightErr := s.fetchHeight(ctx, node); heightErr == nil && height != nil {
		currentHeight = height.LastAcceptedHeight
		if currentHeight == 0 {
			currentHeight = height.CurrentHeight
		}
	}
	estimatedBlockSeconds := frostWithdrawEstimatedBlockSeconds()

	                                                                                                               
	vaultStatuses := make(map[string]string)
	var vaultList pb.FrostVaultStateList
	if err := s.fetchProto(ctx, node, "/frost/vault/list", nil, &vaultList); err == nil {
		for _, v := range vaultList.States {
			vaultStatuses[dkgVaultKey(v.Chain, v.VaultId)] = v.Status
		}
	}

	vaultFundsByVault := make(map[string][]DKGVaultFundItem)
	for _, fund := range s.fetchFrostVaultFunds(ctx, node, "") {
		vKey := dkgVaultKey(fund.Chain, fund.VaultID)
		vaultFundsByVault[vKey] = append(vaultFundsByVault[vKey], fund)
	}
	for vKey, funds := range vaultFundsByVault {
		sort.SliceStable(funds, func(i, j int) bool {
			return funds[i].Asset < funds[j].Asset
		})
		vaultFundsByVault[vKey] = funds
	}

	                                        
	for vKey, funds := range vaultFundsByVault {
		sort.SliceStable(funds, func(i, j int) bool {
			return funds[i].Asset < funds[j].Asset
		})
		vaultFundsByVault[vKey] = funds
	}

	items := make([]DKGSessionItem, 0, len(resp.States))
	for _, st := range resp.States {
		vKey := dkgVaultKey(st.Chain, st.VaultId)
		item := DKGSessionItem{
			Chain:               st.Chain,
			VaultID:             st.VaultId,
			EpochID:             st.EpochId,
			SignAlgo:            st.SignAlgo.String(),
			TriggerHeight:       st.TriggerHeight,
			OldCommitteeMembers: st.OldCommitteeMembers,
			NewCommitteeMembers: st.NewCommitteeMembers,
			DkgStatus:           st.DkgStatus,
			DkgSessionID:        st.DkgSessionId,
			DkgThresholdT:       st.DkgThresholdT,
			DkgN:                st.DkgN,
			DkgCommitDeadline:   st.DkgCommitDeadline,
			DkgDisputeDeadline:  st.DkgDisputeDeadline,
			DkgSharingDeadline:  st.DkgSharingDeadline,
			DkgAttempt:          st.DkgAttempt,
			DkgDisqualified:     append([]string(nil), st.DkgDisqualifiedMembers...),
			ValidationStatus:    st.ValidationStatus,
			Lifecycle:           st.Lifecycle,
			VaultStatus:         vaultStatuses[vKey],
			MigrationJobId:      st.MigrationJobId,
			MigrationStatus:     st.MigrationStatus,
			MigrationError:      st.MigrationError,
			CurrentHeight:       currentHeight,
			EstimatedBlockSecs:  estimatedBlockSeconds,
		}
		                                      
		if len(st.NewGroupPubkey) > 0 {
			pkHex := hex.EncodeToString(st.NewGroupPubkey)
			item.NewGroupPubkey = pkHex
			item.ChainAddresses = s.deriveChainAddresses(ctx, node, st.Chain, st.NewGroupPubkey)
		}
		if len(st.OldGroupPubkey) > 0 {
			item.OldGroupPubkey = hex.EncodeToString(st.OldGroupPubkey)
		}

		if funds := vaultFundsByVault[vKey]; len(funds) > 0 {
			item.VaultFunds = append([]DKGVaultFundItem(nil), funds...)
			item.ManagedAssets = mergeDKGAssets(item.ManagedAssets, funds)
		}

		items = append(items, item)
	}

	writeJSON(w, items)
}

func (s *server) handleFrostWithdrawAvailability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	node := s.pickNode(r.URL.Query().Get("node"))
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	height, err := s.fetchHeight(ctx, node)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	currentHeight := height.LastAcceptedHeight
	if currentHeight == 0 {
		currentHeight = height.CurrentHeight
	}

	var transitions pb.VaultTransitionStateList
	if err := s.fetchProto(ctx, node, "/frost/dkg/list?limit=500", nil, &transitions); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	var vaults pb.FrostVaultStateList
	if err := s.fetchProto(ctx, node, "/frost/vault/list", nil, &vaults); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, buildFrostWithdrawAvailability(
		currentHeight,
		frostWithdrawEstimatedBlockSeconds(),
		transitions.States,
		vaults.States,
	))
}

func (s *server) handleFrostVaults(w http.ResponseWriter, r *http.Request) {
	node := s.pickNode(r.URL.Query().Get("node"))
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	var vaultList pb.FrostVaultStateList
	if err := s.fetchProto(ctx, node, "/frost/vault/list", nil, &vaultList); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	configs := s.fetchFrostVaultConfigs(ctx, node, vaultList.States)
	vaultFundsByVault := make(map[string][]DKGVaultFundItem)
	for _, fund := range s.fetchFrostVaultFunds(ctx, node, "") {
		vKey := dkgVaultKey(fund.Chain, fund.VaultID)
		vaultFundsByVault[vKey] = append(vaultFundsByVault[vKey], fund)
	}
	for vKey, funds := range vaultFundsByVault {
		sort.SliceStable(funds, func(i, j int) bool { return funds[i].Asset < funds[j].Asset })
		vaultFundsByVault[vKey] = funds
	}

	items := make([]FrostVaultOverviewItem, 0, len(vaultList.States))
	for _, state := range vaultList.States {
		if state == nil {
			continue
		}
		chainName := strings.ToLower(strings.TrimSpace(state.Chain))
		funds := vaultFundsByVault[dkgVaultKey(chainName, state.VaultId)]
		item := FrostVaultOverviewItem{
			Chain:             chainName,
			VaultID:           state.VaultId,
			KeyEpoch:          state.KeyEpoch,
			GroupPubkey:       hex.EncodeToString(state.GroupPubkey),
			SignAlgo:          state.SignAlgo.String(),
			Status:            state.Status,
			ActiveSinceHeight: state.ActiveSinceHeight,
			CommitteeMembers:  append([]string(nil), state.CommitteeMembers...),
			ManagedAssets:     mergeDKGAssets(nil, funds),
			VaultFunds:        append([]DKGVaultFundItem(nil), funds...),
		}
		if state.CommitteeThreshold > 0 {
			item.CommitteeThreshold = state.CommitteeThreshold
		} else if threshold, err := config.ComputeVaultCommitteeThreshold(len(state.CommitteeMembers)); err == nil {
			item.CommitteeThreshold = uint32(threshold)
		}
		if cfg := configs[chainName]; cfg != nil {
			item.AuthorityProtocol = cfg.AuthorityProtocol
			item.GuardianPubkey = hex.EncodeToString(cfg.GuardianPubkey)
			if cfg.EvmChainId != 0 {
				item.ChainID = strconv.FormatUint(cfg.EvmChainId, 10)
			}
			if int(state.VaultId) < len(cfg.VaultRefs) {
				item.VaultRef = explorerVaultRef(chainName, cfg.VaultRefs[state.VaultId])
			}
		}
		if item.VaultRef == "" && strings.EqualFold(chainName, "btc") {
			item.VaultRef = s.deriveChainAddresses(ctx, node, chainName, state.GroupPubkey)["btc"]
		}
		items = append(items, item)
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Chain != items[j].Chain {
			return items[i].Chain < items[j].Chain
		}
		return items[i].VaultID < items[j].VaultID
	})
	writeJSON(w, items)
}

func explorerVaultRef(chainName, raw string) string {
	ref := strings.TrimSpace(raw)
	if !strings.EqualFold(chainName, "trx") || ref == "" {
		return ref
	}
	if decoded, version, err := base58.CheckDecode(ref); err == nil && version == 0x41 && len(decoded) == 20 {
		return ref
	}
	hexRef := strings.TrimPrefix(strings.TrimPrefix(ref, "0x"), "0X")
	if len(hexRef) == 42 && strings.HasPrefix(strings.ToLower(hexRef), "41") {
		hexRef = hexRef[2:]
	}
	if len(hexRef) != 40 {
		return ref
	}
	payload, err := hex.DecodeString(hexRef)
	if err != nil {
		return ref
	}
	return base58.CheckEncode(payload, 0x41)
}

func (s *server) fetchFrostVaultConfigs(ctx context.Context, node string, states []*pb.FrostVaultState) map[string]*pb.FrostVaultConfig {
	chains := make(map[string]struct{})
	for _, state := range states {
		if state != nil {
			if chainName := strings.ToLower(strings.TrimSpace(state.Chain)); chainName != "" {
				chains[chainName] = struct{}{}
			}
		}
	}

	configs := make(map[string]*pb.FrostVaultConfig, len(chains))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for chainName := range chains {
		chainName := chainName
		wg.Add(1)
		go func() {
			defer wg.Done()
			var cfg pb.FrostVaultConfig
			path := "/frost/vault/config?chain=" + url.QueryEscape(chainName)
			if err := s.fetchProto(ctx, node, path, nil, &cfg); err != nil {
				log.Printf("[FrostVaults] load config for %s failed: %v", chainName, err)
				return
			}
			mu.Lock()
			configs[chainName] = &cfg
			mu.Unlock()
		}()
	}
	wg.Wait()
	return configs
}

                                                                          
type WitnessInfoItem struct {
	ReservedStakeAmount string   `json:"reserved_stake_amount"`
	ActiveCaseCount     uint64   `json:"active_case_count"`
	Address             string   `json:"address"`
	StakeAmount         string   `json:"stake_amount"`
	PendingReward       string   `json:"pending_reward"`
	Status              string   `json:"status"`
	UnstakeHeight       uint64   `json:"unstake_height"`
	TotalWitnessCount   uint64   `json:"total_witness_count"`
	PassCount           uint64   `json:"pass_count"`
	FailCount           uint64   `json:"fail_count"`
	AbstainCount        uint64   `json:"abstain_count"`
	SlashedCount        uint64   `json:"slashed_count"`
	PendingTasks        []string `json:"pending_tasks"`
	TotalReward         string   `json:"total_reward"`
}

func (s *server) handleWitnessList(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	node = s.pickNode(node)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	                                 
	var resp pb.WitnessInfoList
	if err := s.fetchProto(ctx, node, "/witness/list", nil, &resp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	                                        
	items := make([]WitnessInfoItem, 0, len(resp.Witnesses))
	for _, info := range resp.Witnesses {
		items = append(items, WitnessInfoItem{
			ReservedStakeAmount: info.ReservedStakeAmount,
			ActiveCaseCount:     info.ActiveCaseCount,
			Address:             info.Address,
			StakeAmount:         info.StakeAmount,
			PendingReward:       info.PendingReward,
			Status:              info.Status.String(),
			UnstakeHeight:       info.UnstakeHeight,
			TotalWitnessCount:   info.TotalWitnessCount,
			PassCount:           info.PassCount,
			FailCount:           info.FailCount,
			AbstainCount:        info.AbstainCount,
			SlashedCount:        info.SlashedCount,
			PendingTasks:        info.PendingTasks,
			TotalReward:         info.TotalReward,
		})
	}

	writeJSON(w, items)
}

                                                                                
func (s *server) deriveChainAddresses(ctx context.Context, node, chain string, groupPubkey []byte) map[string]string {
	if len(groupPubkey) == 0 {
		return nil
	}
	addrs := make(map[string]string)
	ch := strings.ToUpper(strings.TrimSpace(chain))
	switch ch {
	case "BTC":
		netParams, err := btcNetParams(s.btcNetworkForChain(ctx, node, chain))
		if err != nil {
			log.Printf("[FrostDKG] Resolve BTC network failed: %v", err)
			break
		}
		var cfg pb.FrostVaultConfig
		if err := s.fetchProto(ctx, node, "/frost/vault/config?chain="+url.QueryEscape(chain), nil, &cfg); err != nil {
			break
		}
		addr, err := utils.DualCustodyTaprootAddress(groupPubkey, cfg.GuardianPubkey, netParams)
		if err == nil {
			addrs["btc"] = addr
		} else {
			log.Printf("[FrostDKG] Derive BTC address failed: %v", err)
		}
	case "ETH", "BNB", "BSC", "TRX", "BASE", "ARB":
		                                                                                                                              
		hash := sha3.NewLegacyKeccak256()
		hash.Write(groupPubkey)
		digest := hash.Sum(nil)

		                                         
		addr := fmt.Sprintf("0x%s", hex.EncodeToString(digest[12:]))
		addrs[strings.ToLower(ch)] = addr
	}
	return addrs
}
