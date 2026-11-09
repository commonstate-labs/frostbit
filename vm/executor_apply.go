package vm

import (
	"dex/blocktrace"
	dbpkg "dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                          
                                                       
func (x *Executor) applyResult(res *SpecResult, b *pb.Block, finalDiff []WriteOp, readModel *pb.BlockReadModel) (err error) {
	startAt := time.Now()
	var (
		durDiffLoop  time.Duration
		durDiffPrep  time.Duration
		durDiffApply time.Duration
		durStake     time.Duration
		durStateSync time.Duration
		durFlush     time.Duration
		writeOps     int
		accountOps   int
		stateOps     int
		diffWorkers  int
	)
	defer func() {
		total := time.Since(startAt)
		height := uint64(0)
		hash := ""
		if b != nil {
			height = b.Header.Height
			hash = b.BlockHash
		}
		vmProbe.applyCalls.Add(1)
		vmProbe.applyWriteOps.Add(uint64(writeOps))
		vmProbe.applyAccountOps.Add(uint64(accountOps))
		vmProbe.applyStateOps.Add(uint64(stateOps))
		addProbeDuration(&vmProbe.applyTotalNs, total)
		addProbeDuration(&vmProbe.applyDiffNs, durDiffLoop)
		addProbeDuration(&vmProbe.applyStakeNs, durStake)
		addProbeDuration(&vmProbe.applySyncNs, durStateSync)
		addProbeDuration(&vmProbe.applyFlushNs, durFlush)

		if total >= vmProbeSlowApplyResult {
			logs.Warn(
				"[VM][Probe][SlowApply] height=%d hash=%s total=%s diffLoop=%s stake=%s sync=%s flush=%s writeOps=%d accountOps=%d stateOps=%d err=%v",
				height, hash, total, durDiffLoop, durStake, durStateSync, durFlush, writeOps, accountOps, stateOps, err,
			)
		}
		maybeLogVMProbeSummary(time.Now())
		receiptCount := 0
		if res != nil {
			receiptCount = len(res.Receipts)
		}
		blocktrace.Record(height, hash, "Executor.applyResult", total,
			blocktrace.F("diffLoop", durDiffLoop), blocktrace.F("diffPrep", durDiffPrep), blocktrace.F("diffApply", durDiffApply),
			blocktrace.F("diffWorkers", diffWorkers), blocktrace.F("stake", durStake), blocktrace.F("sync", durStateSync),
			blocktrace.F("flush", durFlush), blocktrace.F("writeOps", writeOps), blocktrace.F("accountOps", accountOps),
			blocktrace.F("stateOps", stateOps), blocktrace.F("receipts", receiptCount), blocktrace.F("err", err),
		)
	}()
	                                                    
	                                    
	                                                                       
	                                                                         
	                                                                             
	if committed, blockHash := x.isRuntimeBlockCommitted(b.Header.Height); committed {
		if blockHash == b.BlockHash {
			return nil
		}
		return fmt.Errorf("block at height %d already committed with different hash: %s vs %s",
			b.Header.Height, blockHash, b.BlockHash)
	}

	_, strictStateSeparation := x.DB.(*dbpkg.Manager)

	                                                                             
	writeOps = len(finalDiff)
	diffLoopAt := time.Now()
	prepAt := time.Now()
	tasks, preparedStateOps, workers := prepareApplyResultKVTasks(finalDiff, strictStateSeparation)
	durDiffPrep += time.Since(prepAt)
	stateOps += preparedStateOps
	diffWorkers = workers
	applyAt := time.Now()
	for _, task := range tasks {
		if task.del {
			x.DB.EnqueueDel(task.key)
		} else {
			x.DB.EnqueueSet(task.key, task.value)
		}
	}
	durDiffApply += time.Since(applyAt)

	durDiffLoop += time.Since(diffLoopAt)
	stakeAt := time.Now()
	accountOps = x.updateStakeIndexFromDiff(finalDiff)
	durStake += time.Since(stakeAt)

	                                                             
	for _, rc := range res.Receipts {
		if rc == nil || rc.skipGlobalPersistence {
			continue
		}
		                                  
		statusKey := keys.KeyVMAppliedTx(rc.TxID)
		x.DB.EnqueueSet(statusKey, rc.Status)

		                                             
		if rc.Error != "" {
			errorKey := keys.KeyVMTxError(rc.TxID)
			x.DB.EnqueueSet(errorKey, rc.Error)
		}

		                                    
		heightKey := keys.KeyVMTxHeight(rc.TxID)
		x.DB.EnqueueSet(heightKey, fmt.Sprintf("%d", b.Header.Height))

		if rc.Fee != "" {
			feeKey := keys.KeyVMTxFee(rc.TxID)
			x.DB.EnqueueSet(feeKey, rc.Fee)
		}

		                                           
		if rc.FBBalanceAfter != "" {
			fbKey := keys.KeyVMTxFBBalance(rc.TxID)
			x.DB.EnqueueSet(fbKey, rc.FBBalanceAfter)
		}
	}

	                                                                                  
	receiptMap := make(map[string]*Receipt, len(res.Receipts))
	for _, rc := range res.Receipts {
		if rc != nil && !rc.skipGlobalPersistence {
			receiptMap[rc.TxID] = rc
		}
	}
	if recorder, ok := x.DB.(queryPruneManifestRecorder); ok {
		recorder.EnqueueQueryPruneManifest(
			b.Header.Height,
			collectQueryPruneManifestKeys(finalDiff),
			collectQueryPruneReceiptTxIDs(res.Receipts),
			collectQueryPruneNonceReceiptTxIDs(b.Body, receiptMap),
		)
	}

	                                                                                    
	                                                                                                                      
	                                                                                          
	type txRawSaver interface {
		SaveTxRaw(tx *pb.AnyTx) error
	}
	if saver, ok := x.DB.(txRawSaver); ok {
		savedRawTxs := make(map[string]struct{}, len(receiptMap))
		for _, tx := range b.Body {
			if tx == nil {
				continue
			}
			txID := tx.GetTxId()
			if txID == "" {
				continue
			}
			if _, done := savedRawTxs[txID]; done {
				continue
			}
			if _, executedThisBlock := receiptMap[txID]; !executedThisBlock {
				continue
			}
			rc := receiptMap[txID]

			                                                                              
			txForSave := proto.Clone(tx).(*pb.AnyTx)
			if base := txForSave.GetBase(); base != nil {
				base.ExecutedHeight = b.Header.Height
				if rc.Status == "SUCCEED" || rc.Status == "" {
					base.Status = pb.Status_SUCCEED
				} else {
					base.Status = pb.Status_FAILED
				}
			}

			                                          
			if err := saver.SaveTxRaw(txForSave); err != nil {
				                                    
				logs.Warn("[VM] failed to save tx raw %s: %v", txID, err)
			}
			savedRawTxs[txID] = struct{}{}
		}
	}

	                                                                         
	                                                                          
	                                                                             
	                                                   
	if readModel != nil {
		if err := pb.VerifyBlockReadModel(readModel, b.Header.Height, b.BlockHash); err != nil {
			return fmt.Errorf("invalid finalized read model: %w", err)
		}
		data, marshalErr := (proto.MarshalOptions{Deterministic: true}).Marshal(readModel)
		if marshalErr != nil {
			return fmt.Errorf("marshal finalized read model: %w", marshalErr)
		}
		x.DB.EnqueueSet(keys.KeyBlockReadModel(b.BlockHash), string(data))
	}

	                                                             
	commitKey := keys.KeyVMCommitHeight(b.Header.Height)
	x.DB.EnqueueSet(commitKey, b.BlockHash)

	return nil
}

const (
	applyResultParallelMinOps  = 4096
	applyResultParallelMaxWork = 8
)

type applyResultKVTask struct {
	key   string
	value string
	del   bool
}

type queryPruneManifestRecorder interface {
	EnqueueQueryPruneManifest(height uint64, queryKeys, receiptTxIDs, nonceReceiptTxIDs []string)
}

func collectQueryPruneManifestKeys(diff []WriteOp) []string {
	keysOut := make([]string, 0)
	for i := range diff {
		if !diff[i].Del && (keys.IsPrunableQueryHistoryKey(diff[i].Key) || keys.PrunableArchiveHistoryPrefix(diff[i].Key) != "") {
			keysOut = append(keysOut, diff[i].Key)
		}
	}
	return keysOut
}

func collectQueryPruneReceiptTxIDs(receipts []*Receipt) []string {
	txIDs := make([]string, 0, len(receipts))
	for _, receipt := range receipts {
		if receipt != nil && receipt.TxID != "" && !receipt.skipGlobalPersistence {
			txIDs = append(txIDs, receipt.TxID)
		}
	}
	return txIDs
}

func collectQueryPruneNonceReceiptTxIDs(txs []*pb.AnyTx, receipts map[string]*Receipt) []string {
	txIDs := make([]string, 0, len(receipts))
	for _, tx := range txs {
		if tx == nil || !pb.TxRequiresAccountNonce(tx) {
			continue
		}
		txID := tx.GetTxId()
		if _, ok := receipts[txID]; ok {
			txIDs = append(txIDs, txID)
		}
	}
	return txIDs
}

func prepareApplyResultKVTasks(diff []WriteOp, strictStateSeparation bool) ([]applyResultKVTask, int, int) {
	workers := applyResultWorkerCount(len(diff))
	if workers <= 1 {
		tasks := make([]applyResultKVTask, 0, len(diff))
		stateOps := 0
		for i := range diff {
			task, stateful, keep := applyResultTaskFromWriteOp(&diff[i], strictStateSeparation)
			if stateful {
				stateOps++
			}
			if keep {
				tasks = append(tasks, task)
			}
		}
		return tasks, stateOps, 1
	}
	chunk := (len(diff) + workers - 1) / workers
	type partResult struct {
		tasks    []applyResultKVTask
		stateOps int
	}
	parts := make([]partResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := i * chunk
		if start >= len(diff) {
			parts = parts[:i]
			break
		}
		end := start + chunk
		if end > len(diff) {
			end = len(diff)
		}
		wg.Add(1)
		go func(idx, start, end int) {
			defer wg.Done()
			tasks := make([]applyResultKVTask, 0, end-start)
			stateOps := 0
			for j := start; j < end; j++ {
				task, stateful, keep := applyResultTaskFromWriteOp(&diff[j], strictStateSeparation)
				if stateful {
					stateOps++
				}
				if keep {
					tasks = append(tasks, task)
				}
			}
			parts[idx] = partResult{tasks: tasks, stateOps: stateOps}
		}(i, start, end)
	}
	wg.Wait()
	totalTasks, stateOps := 0, 0
	for _, part := range parts {
		totalTasks += len(part.tasks)
		stateOps += part.stateOps
	}
	tasks := make([]applyResultKVTask, 0, totalTasks)
	for _, part := range parts {
		tasks = append(tasks, part.tasks...)
	}
	return tasks, stateOps, len(parts)
}

func applyResultWorkerCount(items int) int {
	if items < applyResultParallelMinOps {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > applyResultParallelMaxWork {
		workers = applyResultParallelMaxWork
	}
	if workers < 2 || items < workers*2 {
		return 1
	}
	return workers
}

func applyResultTaskFromWriteOp(w *WriteOp, strictStateSeparation bool) (applyResultKVTask, bool, bool) {
	stateful := keys.IsStatefulKey(w.Key)
	if strictStateSeparation && stateful {
		return applyResultKVTask{}, stateful, false
	}
	if w.Del {
		return applyResultKVTask{key: w.Key, del: true}, stateful, true
	}
	return applyResultKVTask{key: w.Key, value: string(w.Value)}, stateful, true
}

func (x *Executor) updateStakeIndexFromDiff(diff []WriteOp) int {
	type StakeIndexUpdater interface {
		UpdateStakeIndex(oldStake, newStake decimal.Decimal, address string) error
	}
	updater, ok := x.DB.(StakeIndexUpdater)
	if !ok {
		return 0
	}
	byKey := make(map[string]WriteOp, len(diff))
	for _, w := range diff {
		if w.Key != "" {
			byKey[w.Key] = w
		}
	}
	updated := 0
	for _, w := range diff {
		if w.Del || !strings.HasPrefix(w.Key, "v1_account_") {
			continue
		}
		address := extractAddressFromAccountKey(w.Key)
		if address == "" {
			continue
		}
		balOp, ok := byKey[keys.KeyBalance(address, "FB")]
		if !ok || balOp.Del {
			continue
		}
		oldStake := decimal.Zero
		if balOp.HasOldValue && balOp.OldExists {
			oldStake = minerLockedStakeFromBalanceValue(balOp.OldValue)
		} else if fbBalData, err := x.DB.Get(balOp.Key); err == nil && fbBalData != nil {
			oldStake = minerLockedStakeFromBalanceValue(fbBalData)
		}
		newStake := minerLockedStakeFromBalanceValue(balOp.Value)
		if oldStake.Equal(newStake) {
			continue
		}
		if err := updater.UpdateStakeIndex(oldStake, newStake, address); err != nil {
			logs.Warn("[VM] failed to update stake index for %s: %v", address, err)
			continue
		}
		updated++
	}
	return updated
}

func minerLockedStakeFromBalanceValue(data []byte) decimal.Decimal {
	if len(data) == 0 {
		return decimal.Zero
	}
	var balRecord pb.TokenBalanceRecord
	if err := unmarshalProtoCompat(data, &balRecord); err != nil || balRecord.Balance == nil {
		return decimal.Zero
	}
	stake, err := decimal.NewFromString(balRecord.Balance.MinerLockedBalance)
	if err != nil {
		return decimal.Zero
	}
	return stake
}

type hookDiffTraceSummary struct {
	diffCount       int
	setCount        int
	delCount        int
	statefulCount   int
	statefulSkipped int
	enqueuedCount   int
	valueBytes      int64
	xorHash         uint64
	firstKey        string
	lastKey         string
	samples         []string
}

func summarizeHookDiffForLog(diff []WriteOp, strict bool, sampleLimit int) hookDiffTraceSummary {
	out := hookDiffTraceSummary{
		diffCount: len(diff),
		samples:   make([]string, 0, sampleLimit),
	}
	if len(diff) == 0 {
		return out
	}
	indices := make([]int, len(diff))
	for i := range diff {
		indices[i] = i
	}
	sort.Slice(indices, func(i, j int) bool {
		a := diff[indices[i]]
		b := diff[indices[j]]
		if a.Key == b.Key {
			if a.Del == b.Del {
				return len(a.Value) < len(b.Value)
			}
			return a.Del && !b.Del
		}
		return a.Key < b.Key
	})
	out.firstKey = shortKeyForLog(diff[indices[0]].Key)
	out.lastKey = shortKeyForLog(diff[indices[len(indices)-1]].Key)
	for _, idx := range indices {
		w := diff[idx]
		stateful := keys.IsStatefulKey(w.Key)
		if stateful {
			out.statefulCount++
		}
		enqueued := !(strict && stateful)
		if enqueued {
			out.enqueuedCount++
		} else {
			out.statefulSkipped++
		}
		mode := "set"
		payload := w.Value
		if w.Del {
			mode = "del"
			out.delCount++
			payload = nil
		} else {
			out.setCount++
			out.valueBytes += int64(len(w.Value))
		}
		h := xxHashKVForLog(mode+"|"+w.Key, payload)
		out.xorHash ^= h
		if len(out.samples) >= sampleLimit {
			continue
		}
		action := "queue"
		if !enqueued {
			action = "skip"
		}
		if w.Del {
			out.samples = append(out.samples, fmt.Sprintf("%s:%s:%s:%016x", action, mode, shortKeyForLog(w.Key), h))
			continue
		}
		out.samples = append(out.samples, fmt.Sprintf("%s:%s:%s:%016x(len=%d)", action, mode, shortKeyForLog(w.Key), h, len(w.Value)))
	}
	return out
}

                                                                                          
                                                                                  
func (x *Executor) flushHookDiff(sv StateView, b *pb.Block) ([]WriteOp, error) {
	if b == nil || b.Header == nil {
		return nil, fmt.Errorf("flushHookDiff: nil block/header")
	}
	if err := prefetchStateViewOldValues(sv); err != nil {
		return nil, fmt.Errorf("flushHookDiff: prefetch old values failed: %w", err)
	}
	diff := sv.Diff()
	if len(diff) == 0 {
		return nil, nil
	}
	_, strict := x.DB.(*dbpkg.Manager)
	debugEnabled := stateRootDebugEnabled()
	traceSummary := hookDiffTraceSummary{}
	if debugEnabled {
		traceSummary = summarizeHookDiffForLog(diff, strict, 8)
	}
	syncedStateOps := 0
	if syncer, ok := x.DB.(stateDBSyncer); ok {
		updates := make([]interface{}, 0, len(diff))
		for i := range diff {
			updates = append(updates, &diff[i])
		}
		n, err := syncer.SyncStateUpdates(b.Header.Height, updates)
		if err != nil {
			return nil, fmt.Errorf("flushHookDiff: sync stateDB failed: %w", err)
		}
		syncedStateOps = n
	}
	enqueued := 0
	for _, w := range diff {
		if strict && keys.IsStatefulKey(w.Key) {
			continue
		}
		if w.Del {
			x.DB.EnqueueDel(w.Key)
		} else {
			x.DB.EnqueueSet(w.Key, string(w.Value))
		}
		enqueued++
	}
	if debugEnabled {
		samples := "-"
		if len(traceSummary.samples) > 0 {
			samples = strings.Join(traceSummary.samples, " | ")
		}
		logs.Info(
			"[VM][HookDiffTrace] height=%d block=%s diff=%d set=%d del=%d stateful=%d skippedStateful=%d syncedState=%d enqueued=%d expectedEnqueued=%d xor=%016x valueBytes=%d first=%s last=%s samples=%s",
			b.Header.Height,
			b.BlockHash,
			traceSummary.diffCount,
			traceSummary.setCount,
			traceSummary.delCount,
			traceSummary.statefulCount,
			traceSummary.statefulSkipped,
			syncedStateOps,
			enqueued,
			traceSummary.enqueuedCount,
			traceSummary.xorHash,
			traceSummary.valueBytes,
			traceSummary.firstKey,
			traceSummary.lastKey,
			samples,
		)
	}
	return diff, nil
}

                                                             
func extractAddressFromAccountKey(key string) string {
	                                    
	prefixes := []string{"v1_account_", "account_"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return strings.TrimPrefix(key, prefix)
		}
	}
	return ""
}
