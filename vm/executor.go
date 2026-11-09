package vm

import (
	"bytes"
	"dex/blocktrace"
	dbpkg "dex/db"
	iface "dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/rent"
	statedb "dex/stateDB"
	"dex/witness"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
)

                        
type Executor struct {
	mu                         sync.RWMutex
	DB                         iface.DBManager
	Reg                        *HandlerRegistry
	Cache                      SpecExecCache
	KFn                        KindFn
	ReadFn                     ReadThroughFn
	BatchReadFn                BatchReadFn
	ScanFn                     ScanFn
	ScanWithLimitFn            ScanWithLimitFn
	ScanWithLimitReverseFn     ScanWithLimitReverseFn
	ScanWithLimitOrderedFn     ScanWithLimitOrderedFn
	ScanWithLimitAfterFn       ScanWithLimitOrderedAfterFn
	ScanKeysOrderedFn          ScanKeysWithLimitOrderedFn
	ScanKeysAfterFn            ScanKeysWithLimitOrderedAfterFn
	ScanFutureOrderbookFn      ScanFutureOrderbookFn
	ScanFutureOrderbookVisitFn ScanFutureOrderbookVisitFn
	HotState                   *HotStateCache
	WitnessService             *witness.Service                         
	LivenessTracker            iface.LivenessTracker                             
	CertChainTracker           iface.CertChainTracker                                 
	networkChainID             uint64                                                                           

	                                                                                        
	dumpCertEvidence bool

	                                                                                         
	stateRootByHeight       map[uint64]uint64
	stateRootHeightsMap     map[uint64][]uint64
	stateDebugKeysByHeight  map[uint64]map[string]string
	stateRootLatestH        uint64
	stateRootMismatchLogged map[string]struct{}
	stateRootMismatchOrder  []string

	                                                                                          
	                                                                              
	committedReceiptsByBlock map[string]map[string]*Receipt
	committedReceiptsOrder   []string
	commitTimingMu           sync.RWMutex
	commitTimingsByBlock     map[string]CommitTiming
	commitTimingsOrder       []string
}

type CommitTiming struct {
	Height        uint64
	BlockHash     string
	Total         time.Duration
	BuildSpec     time.Duration
	ApplyAndFlush time.Duration
	CacheHit      bool
	Error         string
}

                                                                                               
func (x *Executor) SetDumpCertEvidence(enabled bool) {
	x.dumpCertEvidence = enabled
}

const committedReceiptCacheLimit = 16
const commitTimingCacheLimit = 16

func (x *Executor) rememberCommitTiming(t CommitTiming) {
	if t.BlockHash == "" {
		return
	}
	x.commitTimingMu.Lock()
	defer x.commitTimingMu.Unlock()
	if x.commitTimingsByBlock == nil {
		x.commitTimingsByBlock = make(map[string]CommitTiming)
	}
	if _, exists := x.commitTimingsByBlock[t.BlockHash]; !exists {
		x.commitTimingsOrder = append(x.commitTimingsOrder, t.BlockHash)
	}
	x.commitTimingsByBlock[t.BlockHash] = t
	for len(x.commitTimingsOrder) > commitTimingCacheLimit {
		oldest := x.commitTimingsOrder[0]
		x.commitTimingsOrder = x.commitTimingsOrder[1:]
		delete(x.commitTimingsByBlock, oldest)
	}
}

func (x *Executor) GetCommitTiming(blockHash string) (CommitTiming, bool) {
	x.commitTimingMu.RLock()
	defer x.commitTimingMu.RUnlock()
	t, ok := x.commitTimingsByBlock[blockHash]
	return t, ok
}

func (x *Executor) rememberCommittedReceiptsLocked(blockID string, receipts []*Receipt) {
	if blockID == "" || len(receipts) == 0 {
		return
	}
	if x.committedReceiptsByBlock == nil {
		x.committedReceiptsByBlock = make(map[string]map[string]*Receipt)
	}
	byTx := make(map[string]*Receipt, len(receipts))
	for _, rc := range receipts {
		if rc == nil || rc.TxID == "" {
			continue
		}
		byTx[rc.TxID] = rc
	}
	if len(byTx) == 0 {
		return
	}
	if _, exists := x.committedReceiptsByBlock[blockID]; !exists {
		x.committedReceiptsOrder = append(x.committedReceiptsOrder, blockID)
	}
	x.committedReceiptsByBlock[blockID] = byTx
	for len(x.committedReceiptsOrder) > committedReceiptCacheLimit {
		oldest := x.committedReceiptsOrder[0]
		x.committedReceiptsOrder = x.committedReceiptsOrder[1:]
		delete(x.committedReceiptsByBlock, oldest)
	}
}

                                                                                  
                                                                                              
func (x *Executor) TakeCommittedReceipts(blockID string) map[string]*Receipt {
	if blockID == "" {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	receipts := x.committedReceiptsByBlock[blockID]
	if receipts == nil {
		return nil
	}
	delete(x.committedReceiptsByBlock, blockID)
	for i, id := range x.committedReceiptsOrder {
		if id == blockID {
			x.committedReceiptsOrder = append(x.committedReceiptsOrder[:i], x.committedReceiptsOrder[i+1:]...)
			break
		}
	}
	return receipts
}

                                                                            
                                                                
                                                         
func (x *Executor) LockForSnapshotSwitch()   { x.mu.Lock() }
func (x *Executor) UnlockForSnapshotSwitch() { x.mu.Unlock() }

type stateDBSyncer interface {
	SyncStateUpdates(height uint64, updates []interface{}) (int, error)
}

type finalizedStateDBSyncer interface {
	SyncFinalizedStateUpdates(height uint64, updates []interface{}, forceCheckpoint bool) (int, error)
}

type finalizedStateDBSyncerForBlock interface {
	SyncFinalizedStateUpdatesForBlock(height uint64, blockID string, updates []interface{}, forceCheckpoint bool) (int, error)
}

type finalizedStateBlockReader interface {
	FinalizedStateBlockID(height uint64) (string, bool, error)
}

type finalizedStateCommitRecoverer interface {
	RecoverFinalizedStateCommit(height uint64, blockID string, forceCheckpoint bool) error
}

type hotFlusher interface {
	ForceFlushHot() error
}

type tracedHotFlusher interface {
	ForceFlushHotTrace(height uint64, blockID string) error
}

func writeOpsAsInterfaces(diff []WriteOp) []interface{} {
	updates := make([]interface{}, 0, len(diff))
	for i := range diff {
		updates = append(updates, &diff[i])
	}
	return updates
}

func writeOpAlreadyInStateView(sv StateView, op WriteOp) bool {
	if sv == nil || op.Key == "" {
		return false
	}
	data, exists, err := sv.Get(op.Key)
	if err != nil {
		return false
	}
	if op.Del {
		return !exists
	}
	return exists && bytes.Equal(data, op.Value)
}

func (x *Executor) syncFinalizedState(height uint64, blockID string, diff []WriteOp, forceCheckpoint bool) (int, error) {
	updates := writeOpsAsInterfaces(diff)
	if syncer, ok := x.DB.(finalizedStateDBSyncerForBlock); ok {
		return syncer.SyncFinalizedStateUpdatesForBlock(height, blockID, updates, forceCheckpoint)
	}
	if syncer, ok := x.DB.(finalizedStateDBSyncer); ok {
		return syncer.SyncFinalizedStateUpdates(height, updates, forceCheckpoint)
	}
	if syncer, ok := x.DB.(stateDBSyncer); ok {
		return syncer.SyncStateUpdates(height, updates)
	}
	return 0, nil
}

func NewExecutor(db iface.DBManager, reg *HandlerRegistry, cache SpecExecCache) *Executor {
	return NewExecutorWithWitness(db, reg, cache, nil)
}

                                                                              
func NewExecutorWithWitnessService(db iface.DBManager, reg *HandlerRegistry, cache SpecExecCache, witnessSvc *witness.Service) *Executor {
	if reg == nil {
		reg = NewHandlerRegistry()
	}
	if cache == nil {
		cache = NewSpecExecLRU(64)
	}

	executor := &Executor{
		DB:                       db,
		Reg:                      reg,
		Cache:                    cache,
		KFn:                      DefaultKindFn,
		WitnessService:           witnessSvc,
		stateRootByHeight:        make(map[uint64]uint64),
		stateRootHeightsMap:      make(map[uint64][]uint64),
		stateDebugKeysByHeight:   make(map[uint64]map[string]string),
		stateRootMismatchLogged:  make(map[string]struct{}),
		committedReceiptsByBlock: make(map[string]map[string]*Receipt),
		HotState:                 NewHotStateCache(),
	}

	rawScan := func(prefix string) (map[string][]byte, error) {
		return db.Scan(prefix)
	}
	rawGet := func(key string) ([]byte, error) {
		return db.Get(key)
	}
	type batchGetter interface {
		GetKVs([]string) (map[string][]byte, error)
	}
	var rawBatchGet BatchReadFn
	if batch, ok := db.(batchGetter); ok {
		rawBatchGet = batch.GetKVs
	}
	rawScanLimit := func(prefix string, limit int) (map[string][]byte, error) {
		return db.ScanKVWithLimit(prefix, limit)
	}
	rawScanLimitReverse := func(prefix string, limit int) (map[string][]byte, error) {
		return db.ScanKVWithLimitReverse(prefix, limit)
	}
	type orderedScanner interface {
		ScanKVWithLimitOrdered(prefix string, limit int, reverse bool) ([]KVPair, error)
	}
	var rawScanOrdered ScanWithLimitOrderedFn
	if ordered, ok := db.(orderedScanner); ok {
		rawScanOrdered = ordered.ScanKVWithLimitOrdered
	}
	type orderedAfterScanner interface {
		ScanKVWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error)
	}
	var rawScanAfter ScanWithLimitOrderedAfterFn
	if orderedAfter, ok := db.(orderedAfterScanner); ok {
		rawScanAfter = orderedAfter.ScanKVWithLimitOrderedAfter
	}
	type orderedKeyScanner interface {
		ScanKeysWithLimitOrdered(prefix string, limit int, reverse bool) ([]string, error)
	}
	var rawScanKeysOrdered ScanKeysWithLimitOrderedFn
	if orderedKeys, ok := db.(orderedKeyScanner); ok {
		rawScanKeysOrdered = orderedKeys.ScanKeysWithLimitOrdered
	}
	type orderedKeyAfterScanner interface {
		ScanKeysWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]string, error)
	}
	var rawScanKeysAfter ScanKeysWithLimitOrderedAfterFn
	if orderedKeysAfter, ok := db.(orderedKeyAfterScanner); ok {
		rawScanKeysAfter = orderedKeysAfter.ScanKeysWithLimitOrderedAfter
	}
	scanOrderedBase := func(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
		if rawScanAfter != nil {
			return rawScanAfter(prefix, startAfter, limit, reverse)
		}
		if startAfter == "" && rawScanOrdered != nil {
			return rawScanOrdered(prefix, limit, reverse)
		}
		kvs, err := rawScan(prefix)
		if err != nil {
			return nil, err
		}
		pairs := kvMapToOrderedPairs(kvs, reverse)
		if startAfter != "" || limit > 0 {
			out := pairs[:0]
			for _, p := range pairs {
				if !orderedAfter(p.Key, startAfter, reverse) {
					continue
				}
				out = append(out, p)
				if limit > 0 && len(out) >= limit {
					break
				}
			}
			return out, nil
		}
		return pairs, nil
	}
	scanOrderedKeysBase := func(prefix, startAfter string, limit int, reverse bool) ([]string, error) {
		if startAfter != "" && rawScanKeysAfter != nil {
			return rawScanKeysAfter(prefix, startAfter, limit, reverse)
		}
		if startAfter == "" && rawScanKeysOrdered != nil {
			return rawScanKeysOrdered(prefix, limit, reverse)
		}
		pairs, err := scanOrderedBase(prefix, startAfter, limit, reverse)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(pairs))
		for _, p := range pairs {
			out = append(out, p.Key)
		}
		return out, nil
	}

	executor.ReadFn = func(key string) ([]byte, error) {
		if hot := executor.HotState; hot != nil {
			if val, ok, err := hot.Get(key, rawGet); ok || err != nil {
				return val, err
			}
		}
		return rawGet(key)
	}
	executor.BatchReadFn = rawBatchGet

	executor.ScanFn = func(prefix string) (map[string][]byte, error) {
		if hot := executor.HotState; hot != nil {
			if kvs, ok, err := hot.Scan(prefix, rawScan); ok || err != nil {
				return kvs, err
			}
		}
		return rawScan(prefix)
	}
	executor.ScanWithLimitFn = func(prefix string, limit int) (map[string][]byte, error) {
		if hot := executor.HotState; hot != nil {
			if pairs, ok, err := hot.ScanOrderedAfter(prefix, "", limit, false, rawScan); ok || err != nil {
				return orderedPairsToMap(pairs), err
			}
		}
		return rawScanLimit(prefix, limit)
	}
	executor.ScanWithLimitReverseFn = func(prefix string, limit int) (map[string][]byte, error) {
		if hot := executor.HotState; hot != nil {
			if pairs, ok, err := hot.ScanOrderedAfter(prefix, "", limit, true, rawScan); ok || err != nil {
				return orderedPairsToMap(pairs), err
			}
		}
		return rawScanLimitReverse(prefix, limit)
	}
	executor.ScanWithLimitOrderedFn = func(prefix string, limit int, reverse bool) ([]KVPair, error) {
		if hot := executor.HotState; hot != nil {
			if pairs, ok, err := hot.ScanOrderedAfter(prefix, "", limit, reverse, rawScan); ok || err != nil {
				return pairs, err
			}
		}
		return scanOrderedBase(prefix, "", limit, reverse)
	}
	executor.ScanWithLimitAfterFn = func(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
		if hot := executor.HotState; hot != nil {
			if pairs, ok, err := hot.ScanOrderedAfter(prefix, startAfter, limit, reverse, rawScan); ok || err != nil {
				return pairs, err
			}
		}
		return scanOrderedBase(prefix, startAfter, limit, reverse)
	}
	executor.ScanKeysOrderedFn = func(prefix string, limit int, reverse bool) ([]string, error) {
		if hot := executor.HotState; hot != nil {
			if keys, ok, err := hot.ScanKeysOrderedAfter(prefix, "", limit, reverse, rawScan); ok || err != nil {
				return keys, err
			}
		}
		return scanOrderedKeysBase(prefix, "", limit, reverse)
	}
	executor.ScanKeysAfterFn = func(prefix, startAfter string, limit int, reverse bool) ([]string, error) {
		if hot := executor.HotState; hot != nil {
			if keys, ok, err := hot.ScanKeysOrderedAfter(prefix, startAfter, limit, reverse, rawScan); ok || err != nil {
				return keys, err
			}
		}
		return scanOrderedKeysBase(prefix, startAfter, limit, reverse)
	}
	executor.ScanFutureOrderbookFn = func(prefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error) {
		if hot := executor.HotState; hot != nil {
			return hot.ScanFutureOrderbookSnapshots(prefix, startAfter, limit, reverse, rawScan)
		}
		return nil, false, nil
	}
	executor.ScanFutureOrderbookVisitFn = func(prefix, startAfter string, limit int, reverse bool, visit FutureOrderbookVisitFn) (bool, error) {
		if hot := executor.HotState; hot != nil {
			return hot.VisitFutureOrderbookSnapshots(prefix, startAfter, limit, reverse, rawScan, visit)
		}
		return false, nil
	}

	return executor
}

                                                              
func NewExecutorWithWitness(db iface.DBManager, reg *HandlerRegistry, cache SpecExecCache, witnessConfig *witness.Config) *Executor {
	                                 
	witnessSvc := witness.NewService(witnessConfig)
	_ = witnessSvc.Start()

	executor := NewExecutorWithWitnessService(db, reg, cache, witnessSvc)

	                           
	if err := executor.LoadWitnessState(); err != nil {
		logs.Warn("[VM] failed to load witness state: %v", err)
	}

	return executor
}

                                                                                                                     
func (x *Executor) LoadWitnessState() error {
	if x.WitnessService == nil {
		return nil
	}
	                                                                                            
	x.WitnessService.Reset()

	                                                               
	winfoMap, err := x.DB.Scan(keys.KeyWitnessInfoPrefix())
	if err == nil {
		count := 0
		for _, key := range sortedBytesMapKeys(winfoMap) {
			data := winfoMap[key]
			var info pb.WitnessInfo
			if err := unmarshalProtoCompat(data, &info); err == nil {
				x.WitnessService.LoadWitness(&info)
				count++
			}
		}
		if count > 0 {
			logs.Info("[VM] Loaded %d witnesses into memory", count)
		}
	}

	                                    
	requestMap, err := x.DB.Scan(keys.KeyRechargeRequestPrefix())
	if err == nil {
		count := 0
		for _, key := range sortedBytesMapKeys(requestMap) {
			data := requestMap[key]
			var req pb.RechargeRequest
			if err := unmarshalProtoCompat(data, &req); err == nil {
				                                                            
				if req.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED &&
					req.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED {
					x.WitnessService.LoadRequest(&req)
					count++
				}
			}
		}
		if count > 0 {
			logs.Info("[VM] Loaded %d active recharge requests into memory", count)
		}
	}

	                                    
	challengeMap, err := x.DB.Scan(keys.KeyChallengeRecordPrefix())
	if err == nil {
		count := 0
		for _, key := range sortedBytesMapKeys(challengeMap) {
			data := challengeMap[key]
			var record pb.ChallengeRecord
			if err := unmarshalProtoCompat(data, &record); err == nil {
				if !record.Finalized {
					x.WitnessService.LoadChallenge(&record)
					count++
				}
			}
		}
		if count > 0 {
			logs.Info("[VM] Loaded %d active challenges into memory", count)
		}
	}

	return nil
}

                                                                           
func (x *Executor) SetWitnessService(svc *witness.Service) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.WitnessService = svc
}

                                          
func (x *Executor) GetWitnessService() *witness.Service {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.WitnessService
}

func (x *Executor) syncWitnessServiceFromCommittedDiff(diff []WriteOp) {
	if x == nil || x.WitnessService == nil {
		return
	}

	                                                                        
	                                                                          
	                                                                         
	                            
	var (
		witnesses          []*pb.WitnessInfo
		hotRequests        []*pb.RechargeRequest
		archivedRequests   []*pb.RechargeRequest
		hotChallenges      []*pb.ChallengeRecord
		archivedChallenges []*pb.ChallengeRecord
	)
	for _, op := range diff {
		if op.Del || len(op.Value) == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(op.Key, keys.KeyWitnessInfoPrefix()):
			var info pb.WitnessInfo
			if unmarshalProtoCompat(op.Value, &info) == nil {
				witnesses = append(witnesses, &info)
			}
		case strings.HasPrefix(op.Key, keys.KeyRechargeRequestPrefix()):
			var req pb.RechargeRequest
			if unmarshalProtoCompat(op.Value, &req) == nil {
				hotRequests = append(hotRequests, &req)
			}
		case strings.HasPrefix(op.Key, keys.KeyArchiveRechargeRequest("")):
			var req pb.RechargeRequest
			if unmarshalProtoCompat(op.Value, &req) == nil {
				archivedRequests = append(archivedRequests, &req)
			}
		case strings.HasPrefix(op.Key, keys.KeyChallengeRecordPrefix()):
			var challenge pb.ChallengeRecord
			if unmarshalProtoCompat(op.Value, &challenge) == nil {
				hotChallenges = append(hotChallenges, &challenge)
			}
		case strings.HasPrefix(op.Key, keys.KeyArchiveChallengeRecord("")):
			var challenge pb.ChallengeRecord
			if unmarshalProtoCompat(op.Value, &challenge) == nil {
				archivedChallenges = append(archivedChallenges, &challenge)
			}
		}
	}

	for _, info := range witnesses {
		x.WitnessService.LoadWitness(info)
	}
	for _, req := range hotRequests {
		x.WitnessService.SyncCommittedRequest(req)
	}
	for _, challenge := range hotChallenges {
		x.WitnessService.SyncCommittedChallenge(challenge)
	}
	                                                                        
	                                                            
	for _, req := range archivedRequests {
		x.WitnessService.SyncCommittedRequest(req)
	}
	for _, challenge := range archivedChallenges {
		x.WitnessService.SyncCommittedChallenge(challenge)
	}
}

                                                                              
                                                             
func (x *Executor) BuildSpecResult(b *pb.Block) (*SpecResult, error) {
	if x == nil {
		return nil, fmt.Errorf("nil executor")
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.buildSpecResult(b, true)
}

func (x *Executor) buildSpecResult(b *pb.Block, useCache bool, captures ...specExecutionCapture) (res *SpecResult, err error) {
	startAt := time.Now()
	proposalExecution := false
	for _, capture := range captures {
		if capture != nil {
			proposalExecution = true
			break
		}
	}
	var (
		durRebuild       time.Duration
		durDryRun        time.Duration
		durApplyWS       time.Duration
		durWitness       time.Duration
		durWitnessHeight time.Duration
		durRechargeSel   time.Duration
		durArbSel        time.Duration
		durArbTimeout    time.Duration
		durSpotHooks     time.Duration
		durFutureHooks   time.Duration
		durRewards       time.Duration
		durDiff          time.Duration
		durSanity        time.Duration
		durAuditBal      time.Duration
		durAuditGlobal   time.Duration
		durTxLoop        time.Duration
		txSeen           int
		txExecuted       int
		wsOps            int
		wsReturnedOps    int
		dryRunErrors     int
		diffOps          int
		pairCount        int
		failedTxCount    int
		directWriteTx    int
		changedKeys      int
		futureOrderTx    int
		futureCloseTx    int
		futureTrades     int
		futureFees       int
		positionEvents   int
		auditStats       GlobalConservationAuditStats
		futureHookStats  futureBlockHookStats
	)
	failureReasons := make(map[string]int)
	failedSamples := make([]string, 0, 5)
	defer func() {
		                                                                         
		                                                                         
		if proposalExecution {
			return
		}
		total := time.Since(startAt)
		height := uint64(0)
		hash := ""
		valid := false
		reason := ""
		if b != nil {
			height = b.Header.Height
			hash = b.BlockHash
		}
		if res != nil {
			valid = res.Valid
			reason = res.Reason
			diffOps = len(res.Diff)
		}
		if err != nil {
			reason = err.Error()
		}

		vmProbe.preBlocks.Add(1)
		vmProbe.preTxSeen.Add(uint64(txSeen))
		vmProbe.preTxExecuted.Add(uint64(txExecuted))
		vmProbe.prePairs.Add(uint64(pairCount))
		vmProbe.preDiffOps.Add(uint64(diffOps))
		vmProbe.preWSOps.Add(uint64(wsOps))
		vmProbe.preDryRunErrors.Add(uint64(dryRunErrors))
		addProbeDuration(&vmProbe.preTotalNs, total)
		addProbeDuration(&vmProbe.preRebuildNs, durRebuild)
		addProbeDuration(&vmProbe.preDryRunNs, durDryRun)
		addProbeDuration(&vmProbe.preApplyWSNs, durApplyWS)
		addProbeDuration(&vmProbe.preWitnessNs, durWitness)
		addProbeDuration(&vmProbe.preRewardsNs, durRewards)
		addProbeDuration(&vmProbe.preDiffNs, durDiff)
		durTxLoopOther := durTxLoop - durDryRun - durApplyWS
		if durTxLoopOther < 0 {
			durTxLoopOther = 0
		}

		if total >= vmProbeSlowBuildSpec {
			logs.Warn(
				"[VM][Probe][SlowBuildSpec] height=%d hash=%s total=%s rebuild=%s txLoop=%s txLoopOther=%s dryrun=%s applyWS=%s witnessHeight=%s rechargeSel=%s arbitrationSel=%s arbitrationTimeouts=%s witness=%s spotHooks=%s futureHooks=%s futureFunding=%s futureFundingApply=%s futureLiquidation=%s rewards=%s diff=%s sanity=%s auditBalance=%s auditGlobal=%s auditOldRead=%s txSeen=%d txExec=%d pairs=%d wsOps=%d wsReturned=%d directWriteTx=%d changedKeys=%d futureOrderTx=%d futureCloseTx=%d futureTrades=%d futureFees=%d posEvents=%d diffOps=%d dryErr=%d valid=%v reason=%s",
				height, hash, total, durRebuild, durTxLoop, durTxLoopOther, durDryRun, durApplyWS, durWitnessHeight, durRechargeSel, durArbSel, durArbTimeout, durWitness, durSpotHooks, durFutureHooks, futureHookStats.FundingDuration, futureHookStats.FundingApplyDuration, futureHookStats.LiquidationDuration, durRewards, durDiff, durSanity, durAuditBal, durAuditGlobal, auditStats.OldValueReadDuration,
				txSeen, txExecuted, pairCount, wsOps, wsReturnedOps, directWriteTx, changedKeys, futureOrderTx, futureCloseTx, futureTrades, futureFees, positionEvents, diffOps, dryRunErrors, valid, reason,
			)

		}
		maybeLogVMProbeSummary(time.Now())
		if !useCache {
			blocktrace.Record(height, hash, "Executor.buildSpecResult", total,
				blocktrace.F("rebuild", durRebuild), blocktrace.F("txLoop", durTxLoop), blocktrace.F("txLoopOther", durTxLoopOther),
				blocktrace.F("dryrun", durDryRun), blocktrace.F("applyWS", durApplyWS),
				blocktrace.F("witnessHeight", durWitnessHeight),
				blocktrace.F("rechargeSel", durRechargeSel), blocktrace.F("arbitrationSel", durArbSel), blocktrace.F("arbitrationTimeouts", durArbTimeout),
				blocktrace.F("witness", durWitness), blocktrace.F("spotHooks", durSpotHooks), blocktrace.F("futureHooks", durFutureHooks),
				blocktrace.F("futureExpire", futureHookStats.ExpireDuration), blocktrace.F("futureDueScan", futureHookStats.DueScanDuration), blocktrace.F("futureFallbackScan", futureHookStats.FallbackScanDuration),
				blocktrace.F("futurePairLoop", futureHookStats.PairLoopDuration), blocktrace.F("futureEMA", futureHookStats.EMADuration),
				blocktrace.F("futureFunding", futureHookStats.FundingDuration), blocktrace.F("futureFundingRate", futureHookStats.FundingRateDuration), blocktrace.F("futureFundingApply", futureHookStats.FundingApplyDuration),
				blocktrace.F("futureLiquidation", futureHookStats.LiquidationDuration), blocktrace.F("futureLiqCollect", futureHookStats.LiquidationCollectDuration), blocktrace.F("futureLiqOrderbook", futureHookStats.LiquidationOrderbookDuration),
				blocktrace.F("futureLiqMark", futureHookStats.LiquidationMarkDuration), blocktrace.F("futureLiqADL", futureHookStats.LiquidationADLDuration),
				blocktrace.F("rewards", durRewards), blocktrace.F("diff", durDiff),
				blocktrace.F("sanity", durSanity), blocktrace.F("auditBalance", durAuditBal), blocktrace.F("auditGlobal", durAuditGlobal),
				blocktrace.F("txSeen", txSeen), blocktrace.F("txExec", txExecuted), blocktrace.F("pairs", pairCount),
				blocktrace.F("wsOps", wsOps), blocktrace.F("wsReturned", wsReturnedOps), blocktrace.F("directWriteTx", directWriteTx), blocktrace.F("changedKeys", changedKeys),
				blocktrace.F("futureOrderTx", futureOrderTx), blocktrace.F("futureCloseTx", futureCloseTx), blocktrace.F("futureTrades", futureTrades), blocktrace.F("futureFees", futureFees), blocktrace.F("posEvents", positionEvents),
				blocktrace.F("futureHookPairs", futureHookStats.Pairs), blocktrace.F("futureDuePairs", futureHookStats.DuePairs), blocktrace.F("futureFallbackPairs", futureHookStats.FallbackPairs),
				blocktrace.F("futurePriceChangedPairs", futureHookStats.PriceChangedPairs), blocktrace.F("futureFundingSettledPairs", futureHookStats.FundingSettledPairs),
				blocktrace.F("futureLiqCheckedPairs", futureHookStats.LiquidationCheckedPairs), blocktrace.F("futureLiqCandidates", futureHookStats.LiquidationCandidates), blocktrace.F("futureLiqAtRisk", futureHookStats.LiquidationAtRisk),
				blocktrace.F("diffOps", diffOps), blocktrace.F("dryErr", dryRunErrors),
				blocktrace.F("auditInOps", auditStats.InputOps), blocktrace.F("auditOps", auditStats.CoalescedOps),
				blocktrace.F("auditTokenOps", auditStats.TokenOps), blocktrace.F("auditBalanceOps", auditStats.BalanceOps), blocktrace.F("auditPositionOps", auditStats.PositionOps),
				blocktrace.F("auditInsuranceOps", auditStats.InsuranceOps), blocktrace.F("auditChallengeOps", auditStats.ChallengeOps), blocktrace.F("auditOtherOps", auditStats.OtherOps),
				blocktrace.F("auditOldInline", auditStats.OldValueInline), blocktrace.F("auditOldFallbacks", auditStats.OldValueFallbacks),
				blocktrace.F("auditOldRead", auditStats.OldValueReadDuration), blocktrace.F("auditOldReadOps", auditStats.OldValueReadOps), blocktrace.F("auditOldReadMisses", auditStats.OldValueReadMisses),
				blocktrace.F("auditNewBytes", auditStats.NewValueBytes), blocktrace.F("auditOldBytes", auditStats.OldValueBytes),
				blocktrace.F("auditCoalesce", auditStats.CoalesceDuration), blocktrace.F("auditLoop", auditStats.LoopDuration),
				blocktrace.F("valid", valid), blocktrace.F("reason", reason),
			)
		}
	}()

	if b == nil {
		return nil, ErrNilBlock
	}

	baseCommittedHeight, baseCommittedHash := x.resolveBaseCommittedState(b)

	               
	if useCache {
		if cached, ok := x.Cache.Get(b.BlockHash); ok {
			if x.isCachedSpecUsable(b, cached) {
				return cached, nil
			}
		}
	}

	                           
	sv := NewStateViewWithLimitScanOrderedBatchKeysAndFuture(x.ReadFn, x.BatchReadFn, x.ScanFn, x.ScanWithLimitFn, x.ScanWithLimitReverseFn, x.ScanWithLimitOrderedFn, x.ScanWithLimitAfterFn, x.ScanKeysOrderedFn, x.ScanKeysAfterFn, x.ScanFutureOrderbookFn, x.ScanFutureOrderbookVisitFn)
	sv.(*overlayStateView).BindNetworkChainID(x.networkChainID)
	if err := x.applyProtocolMigrations(sv, b.Header.Height); err != nil {
		return nil, fmt.Errorf("protocol migration: %w", err)
	}

	                                                                                                                   
	                                                                                                      
	if b.BlockHash != "" {
		sv.Set(keys.KeyBlockHashByHeight(b.Header.Height), []byte(b.BlockHash))
	}
	                                                                        
	                                                                           
	if err := ensureLegacyRechargeIndex(sv); err != nil {
		return nil, fmt.Errorf("migrate legacy recharge identities: %w", err)
	}
	if err := ensureGlobalRechargePendingIndex(sv); err != nil {
		return nil, fmt.Errorf("migrate global recharge pending index: %w", err)
	}
	if err := ensureFrostFundsLotSeqCleanupMigration(sv, b.Header.Height); err != nil {
		return nil, fmt.Errorf("migrate FROST funds-lot sequence counters: %w", err)
	}

	receipts := make([]*Receipt, 0, len(b.Body))
	seenTxIDs := make(map[string]struct{}, len(b.Body))

	                                                  
	pairs := collectPairsFromBlock(b)
	pairCount = len(pairs)
	priceFeed := newBlockPriceFeedCollector()
	rechargeIndexMix := uint64(1)
	arbitrationMaxShelveWait := uint64(0)
	if x.WitnessService != nil {
		rechargeIndexMix = x.WitnessService.BeaconMixBlocks()
		arbitrationMaxShelveWait = x.WitnessService.MaxShelveWaitBlocks()
	}

	                           
	txLoopAt := time.Now()
	for idx, tx := range b.Body {
		if tx == nil {
			continue
		}
		txSeen++

		txID := tx.GetTxId()
		if txID != "" {
			if _, exists := seenTxIDs[txID]; exists {
				return &SpecResult{
					BlockID:  b.BlockHash,
					ParentID: b.Header.PrevBlockHash,
					Height:   b.Header.Height,
					Valid:    false,
					Reason:   fmt.Sprintf("tx %d duplicate tx_id: %s", idx, txID),
				}, nil
			}
			seenTxIDs[txID] = struct{}{}

		}
		txExecuted++
		                     
		kind, err := x.KFn(tx)
		if err != nil {
			return &SpecResult{
				BlockID:  b.BlockHash,
				ParentID: b.Header.PrevBlockHash,
				Height:   b.Header.Height,
				Valid:    false,
				Reason:   fmt.Sprintf("tx %d has invalid structure: %v", idx, err),
			}, nil
		}

		                                                                               
		if kind == "future_order" {
			futureOrderTx++
			if ftx := tx.GetFutureOrderTx(); ftx != nil && ftx.IsClose {
				futureCloseTx++
			}
		}

		base := getBaseMessage(tx)
		                                                                       
		                                                                    
		if guardErr := pb.ValidateTxExecutionGuard(tx, b.Header.Height, keys.MinerAddress(b.Header.Miner)); guardErr != nil {
			return &SpecResult{
				BlockID:  b.BlockHash,
				ParentID: b.Header.PrevBlockHash,
				Height:   b.Header.Height,
				Valid:    false,
				Reason:   fmt.Sprintf("tx %d execution guard rejected: %v", idx, guardErr),
			}, nil
		}
		if base != nil {
			base.ExecutedHeight = b.Header.Height
			if txRequiresAccountNonce(tx) {
				if nonceErr := validateTxNonce(sv, base); nonceErr != nil {
					rc := &Receipt{
						TxID:                  base.TxId,
						Status:                "FAILED",
						Error:                 fmt.Sprintf("invalid nonce: %v", nonceErr),
						BlockHeight:           b.Header.Height,
						Timestamp:             b.Header.Timestamp,
						Fee:                   "0",
						skipGlobalPersistence: true,
					}
					if base.FromAddress != "" {
						if fbBal := GetBalance(sv, base.FromAddress, FeeToken); fbBal != nil {
							rc.FBBalanceAfter = balanceToJSON(fbBal)
						}
					}
					receipts = append(receipts, rc)
					failedTxCount++
					failureReasons["invalid_nonce"]++
					if len(failedSamples) < cap(failedSamples) {
						failedSamples = append(failedSamples, fmt.Sprintf("%s:%s", base.TxId, rc.Error))
					}
					if !proposalExecution {
						logs.Debug("[VM] Tx %s nonce rejected in block %d hash=%s: %v",
							base.TxId, b.Header.Height, b.BlockHash, nonceErr)
					}
					releaseStateViewHistory(sv)
					continue
				}
			}

			if base.FromAddress != "" {
				                                                                   
				                                                                    
				                                                                     
				                                                                 
				                                                               
				rentSnapshot := sv.Snapshot()
				settlement, rentErr := settleRentOwner(sv, base.FromAddress, b.Header.Height)
				if rentErr != nil {
					_ = sv.Revert(rentSnapshot)
					return &SpecResult{
						BlockID: b.BlockHash, ParentID: b.Header.PrevBlockHash, Height: b.Header.Height,
						Valid: false, Reason: fmt.Sprintf("tx %d rent settlement failed: %v", idx, rentErr),
					}, nil
				}
				if txRequiresAccountNonce(tx) && settlement != nil && settlement.Projection != nil &&
					(settlement.Projection.UnpaidRentFB != "0" || settlement.Projection.Mode == rent.RentModeSweeping) {
					if nonceErr := advanceAccountNonce(sv, base); nonceErr != nil {
						_ = sv.Revert(rentSnapshot)
						return &SpecResult{
							BlockID: b.BlockHash, ParentID: b.Header.PrevBlockHash, Height: b.Header.Height,
							Valid: false, Reason: fmt.Sprintf("tx %d nonce advance failed: %v", idx, nonceErr),
						}, nil
					}
					rentReason := fmt.Sprintf("unpaid account rent: %s FB units", settlement.Projection.UnpaidRentFB)
					if settlement.Projection.Mode == rent.RentModeSweeping {
						rentReason = "account rent cleanup is in progress"
					}
					rc := &Receipt{
						TxID: base.TxId, Status: "FAILED",
						Error:       rentReason,
						BlockHeight: b.Header.Height, Timestamp: b.Header.Timestamp, Fee: "0",
					}
					if fbBal := GetBalance(sv, base.FromAddress, FeeToken); fbBal != nil {
						rc.FBBalanceAfter = balanceToJSON(fbBal)
					}
					receipts = append(receipts, rc)
					failedTxCount++
					failureReasons["rent_unpaid"]++
					if len(failedSamples) < cap(failedSamples) {
						failedSamples = append(failedSamples, fmt.Sprintf("%s:%s", base.TxId, rc.Error))
					}
					releaseStateViewHistory(sv)
					continue
				}
			}
		}

		                         
		h, ok := x.Reg.Get(kind)
		if !ok {
			return &SpecResult{
				BlockID:  b.BlockHash,
				ParentID: b.Header.PrevBlockHash,
				Height:   b.Header.Height,
				Valid:    false,
				Reason:   fmt.Sprintf("no handler for tx %d (kind: %s)", idx, kind),
			}, nil
		}

		                                                                        
		if witnessAware, ok := h.(WitnessServiceAware); ok && x.WitnessService != nil {
			witnessAware.SetWitnessService(x.WitnessService)
		}
		if certAware, ok := h.(CertChainTrackerAware); ok && x.CertChainTracker != nil {
			certAware.SetCertChainTracker(x.CertChainTracker)
		}

		                                          
		snapshot := sv.Snapshot()

		                                                                      
		var feeActual string
		if base != nil && base.FromAddress != "" {
			var feeErr error
			_, feeActual, feeErr = DeductFee(sv, base.FromAddress, base.Fee)
			if feeErr != nil {
				                                                   
				sv.Revert(snapshot)
				                                                                                             
				if txRequiresAccountNonce(tx) {
					if nonceErr := advanceAccountNonce(sv, base); nonceErr != nil {
						sv.Revert(snapshot)
						return &SpecResult{
							BlockID:  b.BlockHash,
							ParentID: b.Header.PrevBlockHash,
							Height:   b.Header.Height,
							Valid:    false,
							Reason:   fmt.Sprintf("tx %d nonce advance failed: %v", idx, nonceErr),
						}, nil
					}
				}
				rc := &Receipt{
					TxID:        base.TxId,
					Status:      "FAILED",
					Error:       feeErr.Error(),
					BlockHeight: b.Header.Height,
					Timestamp:   b.Header.Timestamp,
					Fee:         "0",
				}
				fbBal := GetBalance(sv, base.FromAddress, FeeToken)
				if fbBal != nil {
					rc.FBBalanceAfter = balanceToJSON(fbBal)
				}
				receipts = append(receipts, rc)
				failedTxCount++
				failureReasons["fee_insufficient"]++
				if len(failedSamples) < cap(failedSamples) {
					failedSamples = append(failedSamples, fmt.Sprintf("%s:%s", base.TxId, feeErr.Error()))
				}
				if !proposalExecution {
					logs.Debug("[VM] Tx %s fee rejected in block %d hash=%s: %v",
						base.TxId, b.Header.Height, b.BlockHash, feeErr)
				}
				releaseStateViewHistory(sv)
				continue
			}
		}
		                                                                                    
		feeSnapshot := sv.Snapshot()

		               
		dryRunAt := time.Now()
		dryRunSnapshot := sv.Snapshot()
		ws, rc, err := h.DryRun(tx, sv)
		dryRunTouchedState := sv.Snapshot() != dryRunSnapshot
		durDryRun += time.Since(dryRunAt)
		wsReturnedOps += len(ws)
		if err != nil {
			dryRunErrors++
			                                                                                            
			                                                                              
			if rc != nil {
				                                              
				sv.Revert(feeSnapshot)

				                               
				rc.Status = "FAILED"
				rc.Fee = feeActual
				if rc.Error == "" {
					rc.Error = err.Error()
				}

				                                    
				                                                                     
				rc.BlockHeight = b.Header.Height
				rc.Timestamp = b.Header.Timestamp
				if base != nil && base.FromAddress != "" && txRequiresAccountNonce(tx) {
					if nonceErr := advanceAccountNonce(sv, base); nonceErr != nil {
						sv.Revert(snapshot)
						return &SpecResult{
							BlockID:  b.BlockHash,
							ParentID: b.Header.PrevBlockHash,
							Height:   b.Header.Height,
							Valid:    false,
							Reason:   fmt.Sprintf("tx %d nonce advance failed: %v", idx, nonceErr),
						}, nil
					}
				}
				                                                 
				if base != nil && base.FromAddress != "" {
					fbBal := GetBalance(sv, base.FromAddress, FeeToken)
					if fbBal != nil {
						rc.FBBalanceAfter = balanceToJSON(fbBal)
					}
				}
				receipts = append(receipts, rc)
				failedTxCount++
				failureReasons[classifyTxFailureReason(err)]++
				if len(failedSamples) < cap(failedSamples) {
					failedSamples = append(failedSamples, fmt.Sprintf("%s:%s", rc.TxID, err.Error()))
				}
				if !proposalExecution {
					logs.Debug("[VM] Tx %s mark as FAILED in block %d hash=%s: %v",
						rc.TxID, b.Header.Height, b.BlockHash, err)
				}
				releaseStateViewHistory(sv)
				continue
			}

			                                                                   
			sv.Revert(snapshot)
			return &SpecResult{
				BlockID:  b.BlockHash,
				ParentID: b.Header.PrevBlockHash,
				Height:   b.Header.Height,
				Valid:    false,
				Reason:   fmt.Sprintf("tx %d protocol error: %v", idx, err),
			}, nil
		}

		                                                                                               
		                                                                                             
		                                                
		applyWSAt := time.Now()
		changedKeySet := map[string]struct{}(nil)
		if dryRunTouchedState {
			directWriteTx++
			changedKeySet = stateViewChangedKeysSince(sv, dryRunSnapshot)
			changedKeys += len(changedKeySet)
		}
		for _, w := range ws {
			if dryRunTouchedState {
				if _, changed := changedKeySet[w.Key]; changed {
					continue
				}
				if stateViewHasPendingWrite(sv, w.Key) && writeOpAlreadyInStateView(sv, w) {
					continue
				}
			}
			wsOps++
			if err := applyWriteOpWithRechargeIndexes(sv, w, rechargeIndexMix, arbitrationMaxShelveWait, b.Header.Height); err != nil {
				_ = sv.Revert(snapshot)
				return &SpecResult{
					BlockID:  b.BlockHash,
					ParentID: b.Header.PrevBlockHash,
					Height:   b.Header.Height,
					Valid:    false,
					Reason:   fmt.Sprintf("tx %d apply write op failed: %v", idx, err),
				}, nil
			}
		}
		durApplyWS += time.Since(applyWSAt)

		feeConsumed := feeActual
		if shouldEscrowRuntimeFee(tx, rc) && feeActual != "" {
			if base == nil || base.FromAddress == "" {
				sv.Revert(snapshot)
				return &SpecResult{
					BlockID: b.BlockHash, ParentID: b.Header.PrevBlockHash, Height: b.Header.Height,
					Valid: false, Reason: fmt.Sprintf("tx %d delayed fee escrow has no payer", idx),
				}, nil
			}
			if escrowErr := openRuntimeFeeEscrow(sv, base.TxId, base.FromAddress, feeActual, runtimeFeeEscrowKindWitnessRequest); escrowErr != nil {
				sv.Revert(snapshot)
				return &SpecResult{
					BlockID: b.BlockHash, ParentID: b.Header.PrevBlockHash, Height: b.Header.Height,
					Valid: false, Reason: fmt.Sprintf("tx %d fee escrow failed: %v", idx, escrowErr),
				}, nil
			}
			                                                                            
			                                                                      
			feeConsumed = "0"
		} else if shouldRefundRuntimeFee(tx, rc) && feeActual != "" {
			if base != nil && base.FromAddress != "" {
				if _, _, refundErr := RefundFee(sv, base.FromAddress, feeActual); refundErr != nil {
					sv.Revert(snapshot)
					return &SpecResult{
						BlockID:  b.BlockHash,
						ParentID: b.Header.PrevBlockHash,
						Height:   b.Header.Height,
						Valid:    false,
						Reason:   fmt.Sprintf("tx %d fee refund failed: %v", idx, refundErr),
					}, nil
				}
				feeConsumed = "0"
				recordUserActivity(sv, base.FromAddress, &UserActivityRecord{
					Kind:   ActKindFeeRefund,
					Height: b.Header.Height,
					Amount: feeActual,
					Token:  FeeToken,
					Ref:    base.TxId,
				})
			}
		}

		                                                       
		if base != nil && base.FromAddress != "" && txRequiresAccountNonce(tx) {
			if nonceErr := advanceAccountNonce(sv, base); nonceErr != nil {
				sv.Revert(snapshot)
				return &SpecResult{
					BlockID:  b.BlockHash,
					ParentID: b.Header.PrevBlockHash,
					Height:   b.Header.Height,
					Valid:    false,
					Reason:   fmt.Sprintf("tx %d nonce advance failed: %v", idx, nonceErr),
				}, nil
			}
		}

		                           
		                                                                     
		if rc != nil {
			rc.BlockHeight = b.Header.Height
			rc.Timestamp = b.Header.Timestamp
			rc.Fee = feeConsumed

			                                                                                                                   
			if base != nil && base.FromAddress != "" {
				fbBal := GetBalance(sv, base.FromAddress, FeeToken)
				if fbBal != nil {
					rc.FBBalanceAfter = balanceToJSON(fbBal)
				}
			}
			if err := priceFeed.recordReceipt(sv, tx, rc); err != nil {
				_ = sv.Revert(snapshot)
				return nil, fmt.Errorf("record block price feed for tx %s: %w", rc.TxID, err)
			}
			if kind == "future_order" {
				for _, ev := range rc.Events {
					switch ev.Kind {
					case EventOrderMatched:
						futureTrades++
					case EventFeeCharged:
						futureFees++
					case EventPositionOpened, EventPositionReduced, EventPositionClosed:
						positionEvents++
					}
				}
			}
		}
		receipts = append(receipts, rc)
		for _, capture := range captures {
			stepCapture, ok := capture.(specExecutionStepCapture)
			if !ok || stepCapture == nil {
				continue
			}
			if err := stepCapture.capturePostTransaction(sv, b, idx, tx, rc); err != nil {
				return nil, fmt.Errorf("capture post-transaction step %d: %w", idx, err)
			}
		}
		releaseStateViewHistory(sv)
	}
	durTxLoop += time.Since(txLoopAt)
	if failedTxCount > 0 && !proposalExecution {
		summary := fmt.Sprintf(
			"[VM] failed tx summary: height=%d hash=%s failed=%d uniqueReasons=%d topReasons=%s samples=%s",
			b.Header.Height,
			b.BlockHash,
			failedTxCount,
			len(failureReasons),
			formatTopFailureReasons(failureReasons, 3),
			strings.Join(failedSamples, "; "),
		)
		if shouldLogVMFailedSummaryAtInfo(b.BlockHash) {
			logs.Info(summary)
		} else {
			logs.Debug(summary)
		}
	}

	                                                                         
	                                                                        
	                                                                         
	                                                         
	for _, capture := range captures {
		if capture == nil {
			continue
		}
		if err := capture.capturePostTransactions(sv, b, receipts); err != nil {
			return nil, fmt.Errorf("capture post-transaction state: %w", err)
		}
	}
	for _, capture := range captures {
		stopper, ok := capture.(interface{ stopAfterTransactions() bool })
		if !ok || !stopper.stopAfterTransactions() {
			continue
		}
		                                                                         
		                                                                     
		                                                                       
		                                                                       
		return &SpecResult{
			BlockID:             b.BlockHash,
			ParentID:            b.Header.PrevBlockHash,
			Height:              b.Header.Height,
			BaseCommittedHeight: baseCommittedHeight,
			BaseCommittedHash:   baseCommittedHash,
			Valid:               true,
			Receipts:            receipts,
		}, nil
	}

	                                                                                              
	                                                                                 
	if x.WitnessService != nil {
		witnessHeightAt := time.Now()
		x.WitnessService.SetCurrentHeight(b.Header.Height)
		durWitnessHeight += time.Since(witnessHeightAt)
	}

	                                                                                            
	rechargeSelAt := time.Now()
	if err := x.applyPendingRechargeSelections(sv, b.Header.Height); err != nil {
		return nil, err
	}
	durRechargeSel += time.Since(rechargeSelAt)
	arbSelAt := time.Now()
	if err := x.applyPendingArbitrationSelections(sv, b.Header.Height); err != nil {
		durArbSel += time.Since(arbSelAt)
		return nil, err
	}
	durArbSel += time.Since(arbSelAt)

	                                                                                                                       
	                                                                                       
	arbTimeoutAt := time.Now()
	if err := x.applyArbitrationTimeouts(sv, b.Header.Height); err != nil {
		durArbTimeout += time.Since(arbTimeoutAt)
		return nil, err
	}
	durArbTimeout += time.Since(arbTimeoutAt)

	witnessAt := time.Now()
	if err := x.applyRechargeChallengeFinalizations(sv, b.Header.Height, rechargeIndexMix); err != nil {
		durWitness += time.Since(witnessAt)
		return nil, err
	}
	if err := x.applyWitnessFinalizedEvents(sv, b.Header.Height); err != nil {
		durWitness += time.Since(witnessAt)
		return nil, err
	}
	if err := x.applyRechargeVoteGraceClosures(sv, b.Header.Height, rechargeIndexMix); err != nil {
		durWitness += time.Since(witnessAt)
		return nil, err
	}
	if err := cleanupFrostFundsLotSequencesForHeight(sv, b.Header.Height); err != nil {
		durWitness += time.Since(witnessAt)
		return nil, fmt.Errorf("cleanup FROST funds-lot sequence counters: %w", err)
	}
	durWitness += time.Since(witnessAt)

	                                                                            
	spotHooksAt := time.Now()
	processSpotBlockHooks(sv, b.Header.Height)
	durSpotHooks += time.Since(spotHooksAt)

	                                                                                                 
	                                                                                       
	                                                                                           
	futureHooksAt := time.Now()
	spotChanged, futureChanged := priceFeed.changedPairs()
	futureHookStats, err = processFutureBlockHooksWithStats(sv, b.Header.Height, spotChanged, futureChanged)
	durFutureHooks += time.Since(futureHooksAt)
	if err != nil {
		return nil, fmt.Errorf("process future block hooks: %w", err)
	}

	                                                       
	rewardAt := time.Now()
	if b.Header.Miner != "" {
		minerAddr := keys.MinerAddress(b.Header.Miner)                                         

		                                                
		blockReward := CalculateBlockReward(b.Header.Height, DefaultBlockRewardParams)

		                           
		totalFees := big.NewInt(0)
		for _, rc := range receipts {
			if rc.Fee != "" {
				fee, err := ParseBalance(rc.Fee)
				if err == nil {
					totalFees, _ = SafeAdd(totalFees, fee)
				}
			}
		}

		releasedFees, err := collectReleasedRuntimeFees(sv, b.Header.Height)
		if err != nil {
			return nil, err
		}
		totalFees, err = SafeAdd(totalFees, releasedFees)
		if err != nil {
			return nil, fmt.Errorf("sum released runtime fees: %w", err)
		}

		                     
		totalReward, _ := SafeAdd(blockReward, totalFees)

		if totalReward.Sign() > 0 {
			                                                           
			var otherMiners []string
			if x.LivenessTracker != nil {
				for _, addr := range x.LivenessTracker.GetActiveMiners() {
					if addr != minerAddr && x.LivenessTracker.IsActive(addr) {
						otherMiners = append(otherMiners, addr)
					}
				}
			}

			                                                                                    
			proposerReward, otherMinersTotal, perMiner := SplitBlockReward(totalReward, len(otherMiners), DefaultBlockRewardParams.OtherMinersRatio)
			if err := SpendRuntimeFeePool(sv, totalFees); err != nil {
				return nil, fmt.Errorf("spend runtime fee pool for block reward: %w", err)
			}
			if _, err := adjustTokenTotalSupplyForAudit(sv, FeeToken, blockReward); err != nil {
				return nil, fmt.Errorf("adjust block reward supply: %w", err)
			}

			                                                                       
			                                                                                                       
			const RewardToken = "FB"
			type credit struct {
				amount      *big.Int
				proposerHit bool
				hots        []string
			}
			byReward := make(map[string]*credit)
			order := make([]string, 0)
			addCredit := func(rewardAddr, hot string, amt *big.Int, isProposer bool) {
				if amt == nil || amt.Sign() <= 0 {
					return
				}
				c, ok := byReward[rewardAddr]
				if !ok {
					c = &credit{amount: new(big.Int)}
					byReward[rewardAddr] = c
					order = append(order, rewardAddr)
				}
				c.amount.Add(c.amount, amt)
				c.hots = append(c.hots, hot)
				if isProposer {
					c.proposerHit = true
				}
			}
			addCredit(RewardAddressOf(sv, minerAddr), minerAddr, proposerReward, true)
			if perMiner != nil && perMiner.Sign() > 0 {
				for _, addr := range otherMiners {
					addCredit(RewardAddressOf(sv, addr), addr, perMiner, false)
				}
			}
			for _, rewardAddr := range order {
				c := byReward[rewardAddr]
				if err := ensureAccountExists(sv, rewardAddr); err != nil {
					return nil, fmt.Errorf("ensure block reward account %s: %w", rewardAddr, err)
				}
				if err := addBalance(sv, rewardAddr, RewardToken, c.amount); err != nil {
					return nil, fmt.Errorf("credit block reward account %s: %w", rewardAddr, err)
				}
				kind := ActKindBlockRewardActive
				if c.proposerHit {
					kind = ActKindBlockRewardProposer
				}
				recordUserActivity(sv, rewardAddr, &UserActivityRecord{
					Kind: kind, Height: b.Header.Height,
					Amount: c.amount.String(), Token: RewardToken,
					Ref: fmt.Sprintf("hots=%v", c.hots),
				})
			}
			if !proposalExecution {
				logs.Info("[BlockReward][DEBUG] height=%d miner=%q proposerReward=%s otherMinersTotal=%s otherMinersCount=%d perMiner=%v",
					b.Header.Height, minerAddr, proposerReward, otherMinersTotal, len(otherMiners), perMiner)
			}

			                        
			rewardRecord := &pb.BlockReward{
				Height:            b.Header.Height,
				Miner:             minerAddr,
				MinerReward:       proposerReward.String(),
				OtherMinersReward: otherMinersTotal.String(),
				TotalFees:         totalFees.String(),
				BlockReward:       blockReward.String(),
				Timestamp:         b.Header.Timestamp,
			}
			rewardData, _ := MarshalProto(rewardRecord)
			sv.Set(keys.KeyBlockReward(b.Header.Height), rewardData)
		}
	}

	                     
	durRewards += time.Since(rewardAt)

	diffAt := time.Now()
	if err := prefetchStateViewOldValues(sv); err != nil {
		return nil, err
	}
	diff := sv.Diff()
	durDiff += time.Since(diffAt)
	auditDiff := conservationAuditDiff(diff)

	                                                  
	sanityAt := time.Now()
	SanityCheckBalanceDiff(auditDiff, b.Header.Height)
	durSanity += time.Since(sanityAt)

	                                                          
	auditBalanceAt := time.Now()
	AuditBalanceDiffConservation(auditDiff, b.Header.Height, x.ReadFn)
	durAuditBal += time.Since(auditBalanceAt)
	auditGlobalAt := time.Now()
	auditStats = AuditGlobalConservationDiffWithStats(auditDiff, b.Header.Height, x.ReadFn)
	durAuditGlobal += time.Since(auditGlobalAt)

	res = &SpecResult{
		BlockID:             b.BlockHash,
		ParentID:            b.Header.PrevBlockHash,
		Height:              b.Header.Height,
		BaseCommittedHeight: baseCommittedHeight,
		BaseCommittedHash:   baseCommittedHash,
		Valid:               true,
		Receipts:            receipts,
		Diff:                diff,
	}

	                                                                                                   
	                                                                 
	if useCache {
		x.Cache.Put(res)
	}
	return res, nil
}

                                                            
                                                                                                 
                                                                                                    
func (x *Executor) verifyBlockPreStateRoot(b *pb.Block) error {
	if b == nil || b.Header == nil {
		return fmt.Errorf("nil block/header when verifying stateRoot")
	}
	dbm, ok := x.DB.(*dbpkg.Manager)
	if !ok || dbm == nil {
		                                                                   
		return nil
	}
	if len(b.Header.StateRoot) < 8 {
		return fmt.Errorf(
			"missing/invalid block stateRoot at height=%d block=%s len=%d",
			b.Header.Height, b.BlockHash, len(b.Header.StateRoot),
		)
	}
	localStateRoot, localFP := statedb.ReadFingerprintFromProvider(dbm)
	if len(localStateRoot) < 8 {
		return fmt.Errorf(
			"local state fingerprint unavailable at height=%d block=%s len=%d",
			b.Header.Height, b.BlockHash, len(localStateRoot),
		)
	}
	_, headerFP, matched := statedb.CompareFingerprintBytes(localStateRoot, b.Header.StateRoot)
	if !matched {
		logs.Error(
			"[VM][StateRootMismatch] height=%d block=%s local=%016x header=%016x localLen=%d headerLen=%d",
			b.Header.Height, b.BlockHash, localFP, headerFP, len(localStateRoot), len(b.Header.StateRoot),
		)
		x.logStateRootMismatchDetails(dbm, b, localStateRoot)
		return fmt.Errorf("block pre-state root mismatch at height=%d block=%s: local=%016x header=%016x", b.Header.Height, b.BlockHash, localFP, headerFP)
	}
	return nil
}

func summarizeStateValueForLog(v []byte) string {
	if len(v) == 0 {
		return "len=0 xxh=0000000000000000 head="
	}
	head := v
	if len(head) > 8 {
		head = head[:8]
	}
	return fmt.Sprintf("len=%d xxh=%016x head=%x", len(v), xxhash.Sum64(v), head)
}

const stateRootHistoryLimit = 256
const stateRootMismatchDetailLimit = 64
const stateRootDebugKeyHistoryLimit = 256
const stateRootDebugKeyDiffSampleLimit = 8

type statePrefixDigestForLog struct {
	prefix     string
	keyCount   int
	xorHash    uint64
	valueBytes int64
	firstKey   string
	lastKey    string
	samples    []string
	allKeys    []string
	err        error
}

func xxHashKVForLog(key string, value []byte) uint64 {
	d := xxhash.New()
	_, _ = d.WriteString(key)
	_, _ = d.Write(value)
	return d.Sum64()
}

func shortKeyForLog(key string) string {
	const max = 96
	if len(key) <= max {
		return key
	}
	return key[:max] + "..."
}

func formatHeightsForLog(heights []uint64) string {
	if len(heights) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(heights))
	for _, h := range heights {
		parts = append(parts, fmt.Sprintf("%d", h))
	}
	return strings.Join(parts, ",")
}

func removeHeightFromSlice(heights []uint64, target uint64) []uint64 {
	if len(heights) == 0 {
		return heights
	}
	out := heights[:0]
	for _, h := range heights {
		if h != target {
			out = append(out, h)
		}
	}
	return out
}

type commitStageTraceSnapshot struct {
	stage        string
	fp           uint64
	keySummaries map[string]string
	queueLen     int
	queueCap     int
}

func stateRootDebugEnabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("DEX_DEBUG_STATE_ROOT")))
	switch v {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

func collectStateDebugKeySummaries(dbm *dbpkg.Manager, keysSorted []string) map[string]string {
	out := make(map[string]string, len(keysSorted))
	if dbm == nil || len(keysSorted) == 0 {
		return out
	}
	for _, k := range keysSorted {
		val, err := dbm.Get(k)
		if err != nil {
			out[k] = "err=" + err.Error()
			continue
		}
		out[k] = summarizeStateValueForLog(val)
	}
	return out
}

func cloneStateDebugKeySummaries(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func formatStateDebugKeyDiff(prev, cur map[string]string, limit int) (int, string) {
	seen := make(map[string]struct{}, len(prev)+len(cur))
	keysAll := make([]string, 0, len(prev)+len(cur))
	for k := range prev {
		seen[k] = struct{}{}
		keysAll = append(keysAll, k)
	}
	for k := range cur {
		if _, ok := seen[k]; ok {
			continue
		}
		keysAll = append(keysAll, k)
	}
	sort.Strings(keysAll)

	changed := make([]string, 0, len(keysAll))
	for _, k := range keysAll {
		oldV, oldOK := prev[k]
		newV, newOK := cur[k]
		if oldOK != newOK || oldV != newV {
			changed = append(changed, fmt.Sprintf("%s:%s=>%s", shortKeyForLog(k), oldV, newV))
		}
	}
	if len(changed) == 0 {
		return 0, "-"
	}
	if limit <= 0 || len(changed) <= limit {
		return len(changed), strings.Join(changed, " | ")
	}
	return len(changed), strings.Join(changed[:limit], " | ") + fmt.Sprintf(" | ...(%d more)", len(changed)-limit)
}

func (x *Executor) collectStateDebugKeysForBlock(b *pb.Block) []string {
	keySet := map[string]struct{}{
		keys.KeyFrostActiveMiners(): {},
		keys.KeyTotalMinerStake():   {},
		keys.KeyLivenessGlobal():    {},
		keys.KeyCertGlobal():        {},
		keys.KeyCertPending():       {},
	}
	addAddress := func(addr string) {
		if addr == "" {
			return
		}
		keySet[keys.KeyAccount(addr)] = struct{}{}
		keySet[keys.KeyBalance(addr, "FB")] = struct{}{}
	}
	if b != nil {
		if b.Header != nil {
			addAddress(keys.MinerAddress(b.Header.Miner))
		}
		for _, tx := range b.Body {
			if tx == nil {
				continue
			}
			mtx := tx.GetMinerTx()
			if mtx == nil || mtx.Base == nil {
				continue
			}
			addAddress(mtx.Base.FromAddress)
		}
	}
	if x.LivenessTracker != nil {
		active := x.LivenessTracker.GetActiveMiners()
		if len(active) > 6 {
			active = active[:6]
		}
		for _, addr := range active {
			addAddress(addr)
		}
	}
	keysSorted := make([]string, 0, len(keySet))
	for k := range keySet {
		keysSorted = append(keysSorted, k)
	}
	sort.Strings(keysSorted)
	return keysSorted
}

func (x *Executor) captureCommitStageTraceSnapshot(dbm *dbpkg.Manager, b *pb.Block, stage string) *commitStageTraceSnapshot {
	if dbm == nil {
		return nil
	}
	_, fp := statedb.ReadFingerprintFromProvider(dbm)
	stats := dbm.GetChannelStats()
	qLen, qCap := 0, 0
	if len(stats) > 0 {
		qLen = stats[0].Len
		qCap = stats[0].Cap
	}
	keysSorted := x.collectStateDebugKeysForBlock(b)
	return &commitStageTraceSnapshot{
		stage:        stage,
		fp:           fp,
		keySummaries: collectStateDebugKeySummaries(dbm, keysSorted),
		queueLen:     qLen,
		queueCap:     qCap,
	}
}

func (x *Executor) logCommitStageTrace(b *pb.Block, prev, cur *commitStageTraceSnapshot) {
	if b == nil || b.Header == nil || cur == nil {
		return
	}
	if prev == nil {
		logs.Info(
			"[VM][CommitStageTrace] height=%d block=%s stage=%s fp=%016x q=%d/%d keyChanged=%d keys=-",
			b.Header.Height, b.BlockHash, cur.stage, cur.fp, cur.queueLen, cur.queueCap, 0,
		)
		return
	}
	changed, detail := formatStateDebugKeyDiff(prev.keySummaries, cur.keySummaries, stateRootDebugKeyDiffSampleLimit)
	logs.Info(
		"[VM][CommitStageTrace] height=%d block=%s stage=%s prevStage=%s fp=%016x prevFp=%016x fpChanged=%v q=%d/%d keyChanged=%d keys=%s",
		b.Header.Height, b.BlockHash, cur.stage, prev.stage, cur.fp, prev.fp, cur.fp != prev.fp, cur.queueLen, cur.queueCap, changed, detail,
	)
}

                                                                                                                 
                         
func (x *Executor) shouldLogStateRootMismatchDetailsLocked(blockHash string) bool {
	if blockHash == "" {
		return true
	}
	if x.stateRootMismatchLogged == nil {
		x.stateRootMismatchLogged = make(map[string]struct{})
	}
	if _, exists := x.stateRootMismatchLogged[blockHash]; exists {
		return false
	}
	x.stateRootMismatchLogged[blockHash] = struct{}{}
	x.stateRootMismatchOrder = append(x.stateRootMismatchOrder, blockHash)
	if len(x.stateRootMismatchOrder) <= stateRootMismatchDetailLimit {
		return true
	}
	oldest := x.stateRootMismatchOrder[0]
	x.stateRootMismatchOrder = x.stateRootMismatchOrder[1:]
	delete(x.stateRootMismatchLogged, oldest)
	return true
}

                                                                            
                         
func (x *Executor) rememberStateRootLocked(height uint64, fp uint64) {
	if x.stateRootByHeight == nil {
		x.stateRootByHeight = make(map[uint64]uint64)
	}
	if x.stateRootHeightsMap == nil {
		x.stateRootHeightsMap = make(map[uint64][]uint64)
	}

	if oldFP, exists := x.stateRootByHeight[height]; exists {
		if oldFP == fp {
			return
		}
		oldHeights := removeHeightFromSlice(x.stateRootHeightsMap[oldFP], height)
		if len(oldHeights) == 0 {
			delete(x.stateRootHeightsMap, oldFP)
		} else {
			x.stateRootHeightsMap[oldFP] = oldHeights
		}
	}

	x.stateRootByHeight[height] = fp
	hs := x.stateRootHeightsMap[fp]
	if len(hs) == 0 || hs[len(hs)-1] != height {
		hs = append(hs, height)
	}
	x.stateRootHeightsMap[fp] = hs
	if height > x.stateRootLatestH {
		x.stateRootLatestH = height
	}

	if len(x.stateRootByHeight) <= stateRootHistoryLimit {
		return
	}

	var minH uint64
	first := true
	for h := range x.stateRootByHeight {
		if first || h < minH {
			minH = h
			first = false
		}
	}
	if first {
		return
	}
	oldFP := x.stateRootByHeight[minH]
	delete(x.stateRootByHeight, minH)
	trimmed := removeHeightFromSlice(x.stateRootHeightsMap[oldFP], minH)
	if len(trimmed) == 0 {
		delete(x.stateRootHeightsMap, oldFP)
	} else {
		x.stateRootHeightsMap[oldFP] = trimmed
	}
	if minH == x.stateRootLatestH {
		x.stateRootLatestH = 0
		for h := range x.stateRootByHeight {
			if h > x.stateRootLatestH {
				x.stateRootLatestH = h
			}
		}
	}
}

                                                                                                    
                         
func (x *Executor) rememberStateDebugKeysLocked(height uint64, keySummaries map[string]string) {
	if len(keySummaries) == 0 {
		return
	}
	if x.stateDebugKeysByHeight == nil {
		x.stateDebugKeysByHeight = make(map[uint64]map[string]string)
	}
	x.stateDebugKeysByHeight[height] = cloneStateDebugKeySummaries(keySummaries)
	if len(x.stateDebugKeysByHeight) <= stateRootDebugKeyHistoryLimit {
		return
	}
	var minH uint64
	first := true
	for h := range x.stateDebugKeysByHeight {
		if first || h < minH {
			minH = h
			first = false
		}
	}
	if !first {
		delete(x.stateDebugKeysByHeight, minH)
	}
}

                                                                  
                         
func (x *Executor) getStateDebugKeysLocked(height uint64) map[string]string {
	if x.stateDebugKeysByHeight == nil {
		return nil
	}
	return cloneStateDebugKeySummaries(x.stateDebugKeysByHeight[height])
}

                                                                                    
                         
func (x *Executor) findRecordedHeightsByStateRootLocked(fp uint64, limit int) []uint64 {
	hs := x.stateRootHeightsMap[fp]
	if len(hs) == 0 {
		return nil
	}
	if limit <= 0 || len(hs) <= limit {
		out := make([]uint64, len(hs))
		copy(out, hs)
		return out
	}
	out := make([]uint64, limit)
	copy(out, hs[len(hs)-limit:])
	return out
}

                                                          
                         
func (x *Executor) latestRecordedStateRootLocked() (uint64, uint64, bool) {
	if len(x.stateRootByHeight) == 0 {
		return 0, 0, false
	}
	if fp, ok := x.stateRootByHeight[x.stateRootLatestH]; ok {
		return x.stateRootLatestH, fp, true
	}
	var maxH uint64
	first := true
	for h := range x.stateRootByHeight {
		if first || h > maxH {
			first = false
			maxH = h
			x.stateRootLatestH = h
		}
	}
	if first {
		return 0, 0, false
	}
	return maxH, x.stateRootByHeight[maxH], true
}

func collectStatePrefixDigestForLog(prefix string, kv map[string][]byte, sampleLimit int, allKeysLimit int) statePrefixDigestForLog {
	out := statePrefixDigestForLog{
		prefix:  prefix,
		samples: make([]string, 0, sampleLimit),
	}
	if len(kv) == 0 {
		return out
	}
	keysSorted := make([]string, 0, len(kv))
	for k := range kv {
		keysSorted = append(keysSorted, k)
	}
	sort.Strings(keysSorted)
	out.keyCount = len(keysSorted)
	out.firstKey = shortKeyForLog(keysSorted[0])
	out.lastKey = shortKeyForLog(keysSorted[len(keysSorted)-1])
	if len(keysSorted) <= allKeysLimit {
		out.allKeys = make([]string, 0, len(keysSorted))
	}
	for i, k := range keysSorted {
		v := kv[k]
		h := xxHashKVForLog(k, v)
		out.xorHash ^= h
		out.valueBytes += int64(len(v))
		if i < sampleLimit {
			out.samples = append(out.samples, fmt.Sprintf("%s:%016x(len=%d)", shortKeyForLog(k), h, len(v)))
		}
		if out.allKeys != nil {
			out.allKeys = append(out.allKeys, shortKeyForLog(k))
		}
	}
	return out
}

func collectCommitStagePrefixTraceList() []string {
	specs := keys.StateDBBusinessKeySpecs()
	if len(specs) == 0 {
		return nil
	}
	out := make([]string, 0, len(specs))
	livenessPrefix := keys.KeyLivenessPrefix()
	certPrefix := keys.KeyCertPrefix()
	for _, spec := range specs {
		p := spec.Prefix
		if p == "" {
			continue
		}
		if keys.IsWitnessKey(p) || keys.IsFrostKey(p) || strings.HasPrefix(p, livenessPrefix) || strings.HasPrefix(p, certPrefix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (x *Executor) logCommitStagePrefixTrace(dbm *dbpkg.Manager, b *pb.Block, stage string) {
	if dbm == nil || b == nil || b.Header == nil {
		return
	}
	prefixes := collectCommitStagePrefixTraceList()
	if len(prefixes) == 0 {
		return
	}
	const sampleLimit = 3
	for _, prefix := range prefixes {
		kv, err := dbm.Scan(prefix)
		digest := collectStatePrefixDigestForLog(prefix, kv, sampleLimit, -1)
		if err != nil {
			logs.Info(
				"[VM][CommitStagePrefixTrace] height=%d block=%s stage=%s prefix=%s err=%v",
				b.Header.Height,
				b.BlockHash,
				stage,
				prefix,
				err,
			)
			continue
		}
		samples := "-"
		if len(digest.samples) > 0 {
			samples = strings.Join(digest.samples, ",")
		}
		logs.Info(
			"[VM][CommitStagePrefixTrace] height=%d block=%s stage=%s prefix=%s keys=%d xor=%016x valueBytes=%d first=%s last=%s samples=%s",
			b.Header.Height,
			b.BlockHash,
			stage,
			prefix,
			digest.keyCount,
			digest.xorHash,
			digest.valueBytes,
			digest.firstKey,
			digest.lastKey,
			samples,
		)
	}
}

func (x *Executor) logStateRootMismatchPrefixDetails(dbm *dbpkg.Manager, blockHash string) {
	specs := keys.StateDBBusinessKeySpecs()
	sort.Slice(specs, func(i, j int) bool { return specs[i].Prefix < specs[j].Prefix })

	const sampleLimit = 4
	const allKeysLimit = 24
	for _, spec := range specs {
		kv, err := dbm.Scan(spec.Prefix)
		digest := collectStatePrefixDigestForLog(spec.Prefix, kv, sampleLimit, allKeysLimit)
		digest.err = err
		if digest.err != nil {
			logs.Error(
				"[VM][StateRootMismatchPrefix] block=%s prefix=%s err=%v",
				blockHash,
				digest.prefix,
				digest.err,
			)
			continue
		}
		logs.Error(
			"[VM][StateRootMismatchPrefix] block=%s prefix=%s keys=%d xor=%016x valueBytes=%d first=%s last=%s samples=%s",
			blockHash,
			digest.prefix,
			digest.keyCount,
			digest.xorHash,
			digest.valueBytes,
			digest.firstKey,
			digest.lastKey,
			strings.Join(digest.samples, ","),
		)
		if len(digest.allKeys) > 0 {
			logs.Error(
				"[VM][StateRootMismatchPrefixKeys] block=%s prefix=%s keys=%s",
				blockHash,
				digest.prefix,
				strings.Join(digest.allKeys, ","),
			)
		}
	}
}

func (x *Executor) logStateRootMismatchDetails(dbm *dbpkg.Manager, b *pb.Block, localStateRoot []byte) {
	if dbm == nil || b == nil || b.Header == nil {
		return
	}
	if !x.shouldLogStateRootMismatchDetailsLocked(b.BlockHash) {
		logs.Warn("[VM][StateRootMismatchDetailSuppressed] block=%s reason=already_logged", b.BlockHash)
		return
	}

	prevCommitted := false
	prevHash := ""
	if b.Header.Height > 0 {
		prevCommitted, prevHash = x.IsBlockCommitted(b.Header.Height - 1)
	}
	latestHeight, latestErr := dbm.GetLatestBlockHeight()
	if latestErr != nil {
		logs.Error("[VM][StateRootMismatchDetail] block=%s latestHeightErr=%v", b.BlockHash, latestErr)
	}
	logs.Error(
		"[VM][StateRootMismatchDetail] height=%d block=%s parent=%s prevCommitted=%v prevHash=%s latestHeight=%d localState=%s headerState=%s",
		b.Header.Height,
		b.BlockHash,
		b.Header.PrevBlockHash,
		prevCommitted,
		prevHash,
		latestHeight,
		summarizeStateValueForLog(localStateRoot),
		summarizeStateValueForLog(b.Header.StateRoot),
	)
	                                                                                                 
	                                                                                                   
	                                                                                   
	if b.Header.Height > 0 {
		if path, err := dbm.DumpMismatchEvidence(b.Header.Height-1, b.BlockHash); err != nil {
			logs.Warn("[VM][StateRootMismatchDetail] block=%s dumpMismatchEvidence failed: %v", b.BlockHash, err)
		} else {
			logs.Error("[VM][StateRootMismatchDetail] block=%s mismatch evidence written to %s", b.BlockHash, path)
		}
	}
	localFP, headerFP, _ := statedb.CompareFingerprintBytes(localStateRoot, b.Header.StateRoot)
	localSeenAt := x.findRecordedHeightsByStateRootLocked(localFP, 6)
	headerSeenAt := x.findRecordedHeightsByStateRootLocked(headerFP, 6)
	lastH, lastFP, hasLast := x.latestRecordedStateRootLocked()
	if hasLast {
		logs.Error(
			"[VM][StateRootMismatchHistory] block=%s latestRecordedHeight=%d latestRecordedFP=%016x localSeenAt=%s headerSeenAt=%s",
			b.BlockHash,
			lastH,
			lastFP,
			formatHeightsForLog(localSeenAt),
			formatHeightsForLog(headerSeenAt),
		)
		if lastH+1 == b.Header.Height {
			prevKeys := x.getStateDebugKeysLocked(lastH)
			if len(prevKeys) == 0 {
				logs.Error("[VM][StateRootDriftKeys] block=%s fromHeight=%d changed=- reason=no_snapshot", b.BlockHash, lastH)
			} else {
				keysSorted := make([]string, 0, len(prevKeys))
				for k := range prevKeys {
					keysSorted = append(keysSorted, k)
				}
				sort.Strings(keysSorted)
				curKeys := collectStateDebugKeySummaries(dbm, keysSorted)
				changed, detail := formatStateDebugKeyDiff(prevKeys, curKeys, stateRootDebugKeyDiffSampleLimit)
				logs.Error(
					"[VM][StateRootDriftKeys] block=%s fromHeight=%d expectedPreFp=%016x actualPreFp=%016x changed=%d keys=%s",
					b.BlockHash, lastH, lastFP, localFP, changed, detail,
				)
			}
		}
		if lastH+1 == b.Header.Height && lastFP != localFP {
			logs.Error(
				"[VM][StateRootDriftAfterCommit] block=%s expectedPreStateFromLastCommit=%016x actualLocalPreState=%016x lastCommittedHeight=%d verifyHeight=%d",
				b.BlockHash,
				lastFP,
				localFP,
				lastH,
				b.Header.Height,
			)
		}
	}

	shardFPs := dbm.GetLiveShardFingerprints()
	if len(shardFPs) > 0 {
		nonZero := 0
		sample := make([]string, 0, 8)
		for _, fp := range shardFPs {
			if fp.KeyCount == 0 && fp.XorHash == 0 {
				continue
			}
			nonZero++
			if len(sample) < 8 {
				sample = append(sample, fmt.Sprintf("%s:%016x(k=%d)", fp.Shard, fp.XorHash, fp.KeyCount))
			}
		}
		logs.Error("[VM][StateRootMismatchShards] block=%s nonZero=%d sample=%s", b.BlockHash, nonZero, strings.Join(sample, ","))
	}

	debugKeys := []struct {
		name string
		key  string
	}{
		{name: "frost_active_miners", key: keys.KeyFrostActiveMiners()},
		{name: "total_miner_stake", key: keys.KeyTotalMinerStake()},
		{name: "liveness_global", key: keys.KeyLivenessGlobal()},
		{name: "cert_global", key: keys.KeyCertGlobal()},
		{name: "cert_pending", key: keys.KeyCertPending()},
	}

	for _, dk := range debugKeys {
		val, err := dbm.Get(dk.key)
		if err != nil {
			logs.Error("[VM][StateRootMismatchKey] block=%s name=%s key=%s err=%v", b.BlockHash, dk.name, dk.key, err)
			continue
		}
		logs.Error("[VM][StateRootMismatchKey] block=%s name=%s key=%s %s", b.BlockHash, dk.name, dk.key, summarizeStateValueForLog(val))
		if dk.key == keys.KeyFrostActiveMiners() && len(val) > 0 {
			var top pb.FrostActiveMiners
			if err := unmarshalProtoCompat(val, &top); err == nil {
				addrHead := ""
				if len(top.Addresses) > 0 {
					addrHead = top.Addresses[0]
				}
				logs.Error(
					"[VM][StateRootMismatchFrost] block=%s topHeight=%d updateHeight=%d miners=%d firstAddr=%s pubkeys=%d",
					b.BlockHash,
					top.Height,
					top.UpdateHeight,
					len(top.Addresses),
					addrHead,
					len(top.PublicKeys),
				)
			}
		}
	}

	                                                                                                  
	x.logStateRootMismatchPrefixDetails(dbm, b.BlockHash)
}

func (x *Executor) CommitFinalizedBlock(b *pb.Block) (err error) {
	startAt := time.Now()
	var (
		durBuildSpec time.Duration
		durApply     time.Duration
		usedCache    bool
	)
	defer func() {
		total := time.Since(startAt)
		height := uint64(0)
		hash := ""
		if b != nil {
			height = b.Header.Height
			hash = b.BlockHash
		}
		vmProbe.commitCalls.Add(1)
		addProbeDuration(&vmProbe.commitTotalNs, total)
		addProbeDuration(&vmProbe.commitBuildNs, durBuildSpec)
		addProbeDuration(&vmProbe.commitApplyNs, durApply)

		if total >= vmProbeSlowCommitBlock {
			logs.Warn(
				"[VM][Probe][SlowCommit] height=%d hash=%s total=%s buildSpec=%s apply=%s cacheHit=%v err=%v",
				height, hash, total, durBuildSpec, durApply, usedCache, err,
			)
		}
		maybeLogVMProbeSummary(time.Now())
		blocktrace.Record(height, hash, "Executor.CommitFinalizedBlock", total, blocktrace.F("buildSpec", durBuildSpec), blocktrace.F("applyAndFlush", durApply), blocktrace.F("cacheHit", usedCache), blocktrace.F("err", err))
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		x.rememberCommitTiming(CommitTiming{Height: height, BlockHash: hash, Total: total, BuildSpec: durBuildSpec, ApplyAndFlush: durApply, CacheHit: usedCache, Error: errText})
	}()

	if b == nil {
		return ErrNilBlock
	}

	commitLockAt := time.Now()
	x.mu.Lock()
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.commitLockWait", time.Since(commitLockAt))
	defer x.mu.Unlock()
	dbm, _ := x.DB.(*dbpkg.Manager)
	traceEnabled := dbm != nil && stateRootDebugEnabled()
	var tracePrev *commitStageTraceSnapshot
	if traceEnabled {
		tracePrev = x.captureCommitStageTraceSnapshot(dbm, b, "start")
		x.logCommitStageTrace(b, nil, tracePrev)
	}

	                                                                           
	                                                                         
	                                                                             
	if reader, ok := x.DB.(finalizedStateBlockReader); ok {
		blockHash, committed, readErr := reader.FinalizedStateBlockID(b.Header.Height)
		if readErr != nil {
			return fmt.Errorf("read finalized StateDB identity at height %d: %w", b.Header.Height, readErr)
		}
		if committed {
			if blockHash != b.BlockHash {
				return fmt.Errorf("block at height %d already committed with different hash: %s vs %s",
					b.Header.Height, blockHash, b.BlockHash)
			}
			runtimeCommitted, runtimeHash := x.isRuntimeBlockCommitted(b.Header.Height)
			if runtimeCommitted && runtimeHash != blockHash {
				return fmt.Errorf("runtime commit marker conflicts with StateDB at height %d: %s vs %s",
					b.Header.Height, runtimeHash, blockHash)
			}
			if !runtimeCommitted {
				if recoverer, ok := x.DB.(finalizedStateCommitRecoverer); ok {
					forceCheckpoint := x.CertChainTracker != nil && x.CertChainTracker.IsCheckpointHeight(b.Header.Height)
					if err := recoverer.RecoverFinalizedStateCommit(b.Header.Height, blockHash, forceCheckpoint); err != nil {
						return fmt.Errorf("recover finalized StateDB commit at height %d: %w", b.Header.Height, err)
					}
				}
			}
			return nil
		}
	}

	                                                                           
	                                                             
	if committed, blockHash := x.isRuntimeBlockCommitted(b.Header.Height); committed {
		if blockHash == b.BlockHash {
			return nil
		}
		return fmt.Errorf("block at height %d already committed with different hash: %s vs %s",
			b.Header.Height, blockHash, b.BlockHash)
	}

	                                                                                                      
	verifyAt := time.Now()
	if err := x.verifyBlockPreStateRoot(b); err != nil {
		blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.verifyPreStateRoot", time.Since(verifyAt), blocktrace.F("ok", false))
		if traceEnabled {
			cur := x.captureCommitStageTraceSnapshot(dbm, b, "verify_fail")
			x.logCommitStageTrace(b, tracePrev, cur)
		}
		return err
	}
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.verifyPreStateRoot", time.Since(verifyAt), blocktrace.F("ok", true))
	if traceEnabled {
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_verify")
		x.logCommitStageTrace(b, tracePrev, cur)
		tracePrev = cur
	}

	var res *SpecResult
	buildSpecAt := time.Now()
	res, err = x.buildSpecResult(b, false)
	durBuildSpec += time.Since(buildSpecAt)
	if err != nil {
		return fmt.Errorf("build spec result failed: %v", err)
	}

	if !res.Valid {
		return fmt.Errorf("block invalid: %s", res.Reason)
	}

	                                                                         
	                                                                         
	                           
	seedAt := time.Now()
	finalSV := NewStateViewWithLimitScanOrderedBatchKeysAndFuture(x.ReadFn, x.BatchReadFn, x.ScanFn, x.ScanWithLimitFn, x.ScanWithLimitReverseFn, x.ScanWithLimitOrderedFn, x.ScanWithLimitAfterFn, x.ScanKeysOrderedFn, x.ScanKeysAfterFn, x.ScanFutureOrderbookFn, x.ScanFutureOrderbookVisitFn)
	finalSV.(*overlayStateView).BindNetworkChainID(x.networkChainID)
	applyWriteOpsToStateView(finalSV, res.Diff)
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.seedFinalStateView", time.Since(seedAt), blocktrace.F("diffOps", len(res.Diff)))

	timeoutStart := time.Now()
	x.processFrostDkgTimeouts(b, finalSV)
	durTimeout := time.Since(timeoutStart)
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.hook.frostDkgTimeouts", durTimeout)
	if traceEnabled {
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_frost_timeout")
		x.logCommitStageTrace(b, tracePrev, cur)
		tracePrev = cur
	}

	                                                                      
	                                                         
	                                                                          
	                                                                   
	hookStart := time.Now()
	latestTop, minerParticipantsChanged, err := x.processLivenessHooks(b, finalSV)
	if err != nil {
		return fmt.Errorf("liveness hooks failed: %w", err)
	}
	durLiveness := time.Since(hookStart)
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.hook.liveness", durLiveness)
	if traceEnabled {
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_liveness")
		x.logCommitStageTrace(b, tracePrev, cur)
		tracePrev = cur
	}

	autoStart := time.Now()
	x.processFrostAutoTransitions(b, latestTop, finalSV)
	durAutoTrans := time.Since(autoStart)
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.hook.frostAutoTransitions", durAutoTrans)
	if traceEnabled {
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_frost_auto")
		x.logCommitStageTrace(b, tracePrev, cur)
		tracePrev = cur
	}

	migrationIntentStart := time.Now()
	if err := x.processFrostMigrationIntents(b, finalSV); err != nil {
		return fmt.Errorf("frost migration intent hook failed: %w", err)
	}
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.hook.frostMigrationIntents", time.Since(migrationIntentStart))

	exitStart := time.Now()
	if err := x.processMinerExitUnlocks(b, finalSV); err != nil {
		return fmt.Errorf("miner exit unlock hook failed: %w", err)
	}
	durExit := time.Since(exitStart)
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.hook.minerExitUnlocks", durExit)
	if traceEnabled {
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_miner_exit")
		x.logCommitStageTrace(b, tracePrev, cur)
		tracePrev = cur
	}

	rentStart := time.Now()
	rentOwners, rentStats, err := x.processRentHooks(b, finalSV, res.Diff)
	if err != nil {
		return fmt.Errorf("rent hooks failed: %w", err)
	}
	durRent := time.Since(rentStart)
	blocktrace.Record(
		b.Header.Height, b.BlockHash, "Executor.hook.rent", durRent,
		blocktrace.F("owners", rentOwners),
		blocktrace.F("dueCount", rentStats.DueOwners),
		blocktrace.F("settleOwners", rentStats.Owners),
		blocktrace.F("dueOwners", rentStats.DueOwnersDuration),
		blocktrace.F("prefetchStates", rentStats.PrefetchStatesDuration),
		blocktrace.F("prefetchAssets", rentStats.PrefetchAssetsDuration),
		blocktrace.F("prefetchMarkers", rentStats.PrefetchMarkersDuration),
		blocktrace.F("settleLoop", rentStats.SettleLoopDuration),
		blocktrace.F("nominalAsset", rentStats.NominalAssetDuration),
		blocktrace.F("claimScan", rentStats.ClaimScanDuration),
		blocktrace.F("sweep", rentStats.SweepDuration),
		blocktrace.F("sweepClaimScan", rentStats.SweepClaimScanDuration),
		blocktrace.F("sweepCleaner", rentStats.SweepCleanerDuration),
		blocktrace.F("sweepBalances", rentStats.SweepBalanceDuration),
		blocktrace.F("sweepClaims", rentStats.SweepClaimDuration),
		blocktrace.F("recycleFlush", rentStats.RecycleFlushDuration),
		blocktrace.F("prefetchAssetKeys", rentStats.PrefetchedAssetKeys),
		blocktrace.F("prefetchMarkerKeys", rentStats.PrefetchedMarkerKeys),
		blocktrace.F("balanceCacheHits", rentStats.BalanceCacheHits),
		blocktrace.F("claimMarkerCacheHits", rentStats.ClaimMarkerCacheHits),
		blocktrace.F("claimTotalCacheHits", rentStats.ClaimTotalCacheHits),
		blocktrace.F("claimScans", rentStats.ClaimScans),
		blocktrace.F("claimScanKeys", rentStats.ClaimScanKeys),
		blocktrace.F("swept", rentStats.SweptAccounts),
		blocktrace.F("sweepOwnerBudget", rentStats.SweepOwnerBudget),
		blocktrace.F("sweepKeyBudget", rentStats.SweepKeyBudget),
		blocktrace.F("sweepOwnersProcessed", rentStats.SweepOwnersProcessed),
		blocktrace.F("sweepKeysScanned", rentStats.SweepKeysScanned),
		blocktrace.F("sweepBudgetDeferrals", rentStats.SweepBudgetDeferrals),
		blocktrace.F("exempt", rentStats.ExemptAccounts),
		blocktrace.F("written", rentStats.WrittenStates),
		blocktrace.F("unchanged", rentStats.UnchangedStates),
		blocktrace.F("recycleFlushes", rentStats.RecycleFlushes),
	)
	if traceEnabled {
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_rent")
		x.logCommitStageTrace(b, tracePrev, cur)
		tracePrev = cur
	}

	prefetchAt := time.Now()
	if err := prefetchStateViewOldValues(finalSV); err != nil {
		blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.prefetchFinalOldValues", time.Since(prefetchAt), blocktrace.F("ok", false))
		return fmt.Errorf("finalized state prefetch old values failed: %w", err)
	}
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.prefetchFinalOldValues", time.Since(prefetchAt), blocktrace.F("ok", true))
	diffAt := time.Now()
	finalDiff := finalSV.Diff()
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.finalStateViewDiff", time.Since(diffAt), blocktrace.F("diffOps", len(finalDiff)))
	readModelAt := time.Now()
	readModel, err := buildBlockReadModel(b, finalDiff)
	if err != nil {
		return fmt.Errorf("build finalized read model failed: %w", err)
	}
	blocktrace.Record(
		b.Header.Height,
		b.BlockHash,
		"Executor.buildReadModel",
		time.Since(readModelAt),
		blocktrace.F("mutations", len(readModel.Mutations)),
	)
	nodeDiff := finalizedNodeDiff(finalDiff)
	auditDiff := conservationAuditDiff(nodeDiff)

	                                                                             
	                                                                     
	                                                                      
	                                                                            
	                    
	                                                                             
	                                                                   
	finalAuditAt := time.Now()
	SanityCheckBalanceDiff(auditDiff, b.Header.Height)
	AuditBalanceDiffConservation(auditDiff, b.Header.Height, x.ReadFn)
	finalAuditStats := AuditGlobalConservationDiffWithStats(auditDiff, b.Header.Height, x.ReadFn)
	blocktrace.Record(
		b.Header.Height,
		b.BlockHash,
		"Executor.auditFinalState",
		time.Since(finalAuditAt),
		blocktrace.F("diffOps", len(auditDiff)),
		blocktrace.F("readModelOps", len(readModel.Mutations)),
		blocktrace.F("auditOldInline", finalAuditStats.OldValueInline),
		blocktrace.F("auditOldFallbacks", finalAuditStats.OldValueFallbacks),
		blocktrace.F("auditOldRead", finalAuditStats.OldValueReadDuration),
		blocktrace.F("auditOldReadOps", finalAuditStats.OldValueReadOps),
		blocktrace.F("auditOldReadMisses", finalAuditStats.OldValueReadMisses),
		blocktrace.F("auditOtherOps", finalAuditStats.OtherOps),
	)

	forceCheckpoint := x.CertChainTracker != nil && x.CertChainTracker.IsCheckpointHeight(b.Header.Height)
	stateSyncAt := time.Now()
	stateOps, err := x.syncFinalizedState(b.Header.Height, b.BlockHash, nodeDiff, forceCheckpoint)
	durStateSync := time.Since(stateSyncAt)
	durApply += durStateSync
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.syncFinalizedState", durStateSync, blocktrace.F("diffOps", len(nodeDiff)), blocktrace.F("stateOps", stateOps), blocktrace.F("forceCheckpoint", forceCheckpoint))
	if err != nil {
		return fmt.Errorf("sync finalized state failed: %w", err)
	}
	                                                                            
	                                                                             
	                                                                           
	                                                                            
	                                                                
	if dbm != nil && minerParticipantsChanged {
		dbm.InvalidateMinerCache()
	}
	if traceEnabled {
		logs.Info("[VM][FinalizedState] height=%d block=%s diff=%d syncedState=%d forceCheckpoint=%t", b.Header.Height, b.BlockHash, len(nodeDiff), stateOps, forceCheckpoint)
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_state_sync")
		x.logCommitStageTrace(b, tracePrev, cur)
		x.logCommitStagePrefixTrace(dbm, b, "post_state_sync")
		tracePrev = cur
	}

	var postStateRoot []byte
	if dbm != nil {
		postStateRoot, _ = statedb.ReadFingerprintFromProvider(dbm)
	}

	certStart := time.Now()
	x.processCertChainHooks(b, finalSV, postStateRoot, "finalized_state")
	durCert := time.Since(certStart)
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.hook.certChain", durCert)
	if traceEnabled {
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_cert_chain")
		x.logCommitStageTrace(b, tracePrev, cur)
		x.logCommitStagePrefixTrace(dbm, b, "post_cert_chain")
		tracePrev = cur
	}

	durHooksTotal := time.Since(hookStart)
	if durHooksTotal >= 50*time.Millisecond {
		logs.Warn("[VM][Probe][SlowHooks] height=%d liveness=%s autoTrans=%s dkgTimeout=%s exit=%s rent=%s cert=%s total=%s",
			b.Header.Height, durLiveness, durAutoTrans, durTimeout, durExit, durRent, durCert, durHooksTotal)
	}

	applyAt := time.Now()
	err = x.applyResult(res, b, nodeDiff, readModel)
	durApplyResult := time.Since(applyAt)
	durApply += durApplyResult
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.applyResultCall", durApplyResult, blocktrace.F("diffOps", len(nodeDiff)), blocktrace.F("receipts", len(res.Receipts)))
	if err != nil {
		return err
	}
	flushAt := time.Now()
	if flusher, ok := x.DB.(tracedHotFlusher); ok {
		if err := flusher.ForceFlushHotTrace(b.Header.Height, b.BlockHash); err != nil {
			return fmt.Errorf("flush finalized hot metadata failed: %w", err)
		}
		blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.forceFlushHot", time.Since(flushAt))
	} else if flusher, ok := x.DB.(hotFlusher); ok {
		if err := flusher.ForceFlushHot(); err != nil {
			return fmt.Errorf("flush finalized hot metadata failed: %w", err)
		}
		blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.forceFlushHot", time.Since(flushAt))
	} else if err := x.DB.ForceFlush(); err != nil {
		return fmt.Errorf("flush finalized metadata failed: %w", err)
	} else {
		blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.forceFlushFull", time.Since(flushAt))
	}
	durApply += time.Since(flushAt)
	hotStateAt := time.Now()
	x.applyHotStateDiff(nodeDiff)
	blocktrace.Record(b.Header.Height, b.BlockHash, "Executor.applyHotStateDiff", time.Since(hotStateAt), blocktrace.F("diffOps", len(nodeDiff)))
	x.syncWitnessServiceFromCommittedDiff(nodeDiff)
	if traceEnabled {
		cur := x.captureCommitStageTraceSnapshot(dbm, b, "post_force_flush")
		x.logCommitStageTrace(b, tracePrev, cur)
		x.logCommitStagePrefixTrace(dbm, b, "post_force_flush")
		tracePrev = cur
	}
	if dbm != nil {
		postStateRoot, postFP := statedb.ReadFingerprintFromProvider(dbm)
		_, headerFP, _ := statedb.CompareFingerprintBytes(postStateRoot, b.Header.StateRoot)
		logs.Info(
			"[VM][StateRootTrace][CommitChain] height=%d block=%s pre=%016x post=%016x",
			b.Header.Height,
			b.BlockHash,
			headerFP,
			postFP,
		)
		logs.Info("[VM][StateRootTrace][PostCommit] height=%d block=%s fp=%016x len=%d", b.Header.Height, b.BlockHash, postFP, len(postStateRoot))
		x.rememberStateRootLocked(b.Header.Height, postFP)
		x.rememberStateDebugKeysLocked(b.Header.Height, collectStateDebugKeySummaries(dbm, x.collectStateDebugKeysForBlock(b)))
	}
	if res != nil {
		x.rememberCommittedReceiptsLocked(b.BlockHash, res.Receipts)
	}

	return nil
}
