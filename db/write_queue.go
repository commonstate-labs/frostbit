package db

import (
	"dex/blocktrace"
	"dex/config"
	"dex/keys"
	"dex/logs"
	statedb "dex/stateDB"
	"dex/stats"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/pebble"
)

const (
	defaultWriteQueueMaxPebbleBatchBytes = 32 << 20
	defaultWriteQueueMaxStateApplyBytes  = 32 << 20
	writeQueueRetryInitialDelay          = 100 * time.Millisecond
	writeQueueRetryMaxDelay              = 5 * time.Second
)

                                                                            
                                                                               
                                                                          
                                             
type writeQueueRetryTimer struct {
	timer   *time.Timer
	channel <-chan time.Time
	delay   time.Duration
}

func newWriteQueueRetryTimer() *writeQueueRetryTimer {
	return &writeQueueRetryTimer{delay: writeQueueRetryInitialDelay}
}

func (r *writeQueueRetryTimer) Pending() bool {
	return r != nil && r.channel != nil
}

func (r *writeQueueRetryTimer) C() <-chan time.Time {
	if r == nil {
		return nil
	}
	return r.channel
}

func (r *writeQueueRetryTimer) Schedule() time.Duration {
	if r == nil {
		return 0
	}
	delay := r.delay
	if delay <= 0 {
		delay = writeQueueRetryInitialDelay
	}
	r.stopPending()
	if r.timer == nil {
		r.timer = time.NewTimer(delay)
	} else {
		r.timer.Reset(delay)
	}
	r.channel = r.timer.C
	if delay < writeQueueRetryMaxDelay {
		r.delay = delay * 2
		if r.delay > writeQueueRetryMaxDelay {
			r.delay = writeQueueRetryMaxDelay
		}
	}
	return delay
}

func (r *writeQueueRetryTimer) Fired() {
	if r != nil {
		r.channel = nil
	}
}

                                                                             
                                                                              
func (r *writeQueueRetryTimer) CancelPending() {
	if r != nil {
		r.stopPending()
	}
}

func (r *writeQueueRetryTimer) Reset() {
	if r == nil {
		return
	}
	r.stopPending()
	r.delay = writeQueueRetryInitialDelay
}

func (r *writeQueueRetryTimer) Stop() {
	if r != nil {
		r.stopPending()
	}
}

func (r *writeQueueRetryTimer) stopPending() {
	if r.timer != nil && !r.timer.Stop() {
		select {
		case <-r.timer.C:
		default:
		}
	}
	r.channel = nil
}

type flushRequest struct {
	done    chan error
	height  uint64
	blockID string
	name    string
}

type writeQueueMetricsSnapshot struct {
	enqueueTotal         uint64
	enqueueSetTotal      uint64
	enqueueDeleteTotal   uint64
	enqueueBlockedCount  uint64
	enqueueBlockedNs     uint64
	dequeuedTotal        uint64
	flushBatchTotal      uint64
	flushedTaskTotal     uint64
	flushErrTotal        uint64
	flushDurationNsTotal uint64
	forceFlushTotal      uint64
	maxDepth             uint64
	keyCounters          map[string]uint64
	blockedKeyCounters   map[string]uint64
}

func (manager *Manager) InitWriteQueue(maxBatchSize int, flushInterval time.Duration) {
	if maxBatchSize <= 0 {
		maxBatchSize = 1
	}
	cfg := manager.cfg
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	manager.maxBatchSize = maxBatchSize
	manager.flushInterval = flushInterval
	manager.maxPebbleBatchBytes = defaultWriteQueueMaxPebbleBatchBytes
	manager.maxStateApplyBytes = defaultWriteQueueMaxStateApplyBytes
	manager.resetWriteQueueMetrics()
	manager.writeQueueChan = make(chan WriteTask, cfg.Database.WriteQueueSize)
	manager.coldWriteQueueChan = make(chan WriteTask, cfg.Database.WriteQueueSize)
	manager.forceFlushChan = make(chan flushRequest, 1)
	manager.coldForceFlushChan = make(chan flushRequest, 1)
	manager.stopChan = make(chan struct{})
	manager.wg.Add(3)
	go manager.runWriteQueue()
	go manager.runColdWriteQueue()
	go manager.runWriteQueueWatchdog()
}

func (manager *Manager) resetWriteQueueMetrics() {
	manager.writeQueueEnqueueTotal = 0
	manager.writeQueueEnqueueSetTotal = 0
	manager.writeQueueEnqueueDeleteTotal = 0
	manager.writeQueueEnqueueBlockedCount = 0
	manager.writeQueueEnqueueBlockedNs = 0
	manager.writeQueueDequeuedTotal = 0
	manager.writeQueueFlushBatchTotal = 0
	manager.writeQueueFlushedTaskTotal = 0
	manager.writeQueueFlushErrTotal = 0
	manager.writeQueueFlushDurationNsTotal = 0
	manager.writeQueueForceFlushTotal = 0
	manager.writeQueueMaxDepth = 0
	manager.writeQueueFlushInFlightSince = 0
	manager.clearCounterMap(&manager.writeQueueKeyCounters)
	manager.clearCounterMap(&manager.writeQueueBlockedKeyCounters)
}

func (manager *Manager) observeQueueDepth() {
	q := len(manager.writeQueueChan)
	for {
		old := atomic.LoadUint64(&manager.writeQueueMaxDepth)
		if uint64(q) <= old {
			return
		}
		if atomic.CompareAndSwapUint64(&manager.writeQueueMaxDepth, old, uint64(q)) {
			return
		}
	}
}

func normalizeWriteQueueKeyBucket(key string) string {
	if key == "" {
		return "empty"
	}
	keyNoVer := key
	if idx := strings.IndexByte(key, '_'); idx > 1 && idx <= 4 && key[0] == 'v' {
		allDigits := true
		for i := 1; i < idx; i++ {
			if key[i] < '0' || key[i] > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			keyNoVer = key[idx+1:]
		}
	}
	switch {
	case strings.HasPrefix(keyNoVer, "snapshot_"):
		return "snapshot"
	case strings.HasPrefix(keyNoVer, "finalization_chits_"):
		return "finalization_chits"
	case strings.HasPrefix(keyNoVer, "pending_anytx_"):
		return "pending_anytx"
	case strings.HasPrefix(keyNoVer, "orderstate_"):
		return "orderstate"
	case strings.HasPrefix(keyNoVer, "order_expiry_"):
		return "order_expiry"
	case strings.HasPrefix(keyNoVer, "blockdata_"):
		return "blockdata"
	case strings.HasPrefix(keyNoVer, "height_"):
		return "height"
	case strings.HasPrefix(keyNoVer, "blockid_"):
		return "blockid"
	case strings.HasPrefix(keyNoVer, "account_"):
		return "account"
	case strings.HasPrefix(keyNoVer, "balance_"):
		return "balance"
	case strings.HasPrefix(keyNoVer, "acc_order_"):
		return "acc_order"
	case strings.HasPrefix(keyNoVer, "frost_"):
		return "frost"
	case strings.HasPrefix(keyNoVer, "pair:"):
		return "pair_index"
	case strings.HasPrefix(keyNoVer, "trade_"):
		return "trade"
	case strings.HasPrefix(keyNoVer, "txraw_"):
		return "txraw"
	case strings.HasPrefix(keyNoVer, "vmtx_fbbal_"):
		return "vmtx_fbbal"
	case strings.HasPrefix(keyNoVer, "vm_tx_"):
		return "vm_tx"
	case strings.HasPrefix(keyNoVer, "vm_applied_"):
		return "vm_applied"
	}

	                                                                                                                 
	if idx := strings.IndexAny(keyNoVer, ":|"); idx > 0 {
		return keyNoVer[:idx]
	}

	                                                                  
	parts := strings.Split(keyNoVer, "_")
	if len(parts) > 0 && len(parts[0]) > 0 {
		if len(parts[0]) > 48 {
			return parts[0][:48]
		}
		return parts[0]
	}
	if len(keyNoVer) > 48 {
		return keyNoVer[:48] + "..."
	}
	return keyNoVer
}

func (manager *Manager) addCounter(counterMap *sync.Map, key string) {
	if counterMap == nil || key == "" {
		return
	}
	val, _ := counterMap.LoadOrStore(key, &atomic.Uint64{})
	if c, ok := val.(*atomic.Uint64); ok && c != nil {
		c.Add(1)
	}
}

func (manager *Manager) clearCounterMap(counterMap *sync.Map) {
	if counterMap == nil {
		return
	}
	counterMap.Range(func(k, _ any) bool {
		counterMap.Delete(k)
		return true
	})
}

func (manager *Manager) snapshotCounterMapDelta(counterMap *sync.Map) map[string]uint64 {
	snapshot := make(map[string]uint64)
	if counterMap == nil {
		return snapshot
	}
	counterMap.Range(func(k, v any) bool {
		key, ok := k.(string)
		if !ok || key == "" {
			return true
		}
		c, ok := v.(*atomic.Uint64)
		if !ok || c == nil {
			return true
		}
		delta := c.Swap(0)
		if delta == 0 {
			return true
		}
		snapshot[key] = delta
		return true
	})
	return snapshot
}

type counterValue struct {
	Key   string
	Value uint64
}

func topCounterValues(cur map[string]uint64, topN int) []string {
	if topN <= 0 || len(cur) == 0 {
		return nil
	}
	values := make([]counterValue, 0, len(cur))
	for k, v := range cur {
		values = append(values, counterValue{Key: k, Value: v})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Value != values[j].Value {
			return values[i].Value > values[j].Value
		}
		return values[i].Key < values[j].Key
	})
	if len(values) > topN {
		values = values[:topN]
	}
	result := make([]string, 0, len(values))
	for _, d := range values {
		result = append(result, fmt.Sprintf("%s=%d", d.Key, d.Value))
	}
	return result
}

func (manager *Manager) snapshotWriteQueueMetrics() writeQueueMetricsSnapshot {
	return writeQueueMetricsSnapshot{
		enqueueTotal:         atomic.LoadUint64(&manager.writeQueueEnqueueTotal),
		enqueueSetTotal:      atomic.LoadUint64(&manager.writeQueueEnqueueSetTotal),
		enqueueDeleteTotal:   atomic.LoadUint64(&manager.writeQueueEnqueueDeleteTotal),
		enqueueBlockedCount:  atomic.LoadUint64(&manager.writeQueueEnqueueBlockedCount),
		enqueueBlockedNs:     atomic.LoadUint64(&manager.writeQueueEnqueueBlockedNs),
		dequeuedTotal:        atomic.LoadUint64(&manager.writeQueueDequeuedTotal),
		flushBatchTotal:      atomic.LoadUint64(&manager.writeQueueFlushBatchTotal),
		flushedTaskTotal:     atomic.LoadUint64(&manager.writeQueueFlushedTaskTotal),
		flushErrTotal:        atomic.LoadUint64(&manager.writeQueueFlushErrTotal),
		flushDurationNsTotal: atomic.LoadUint64(&manager.writeQueueFlushDurationNsTotal),
		forceFlushTotal:      atomic.LoadUint64(&manager.writeQueueForceFlushTotal),
		maxDepth:             atomic.LoadUint64(&manager.writeQueueMaxDepth),
		keyCounters:          manager.snapshotCounterMapDelta(&manager.writeQueueKeyCounters),
		blockedKeyCounters:   manager.snapshotCounterMapDelta(&manager.writeQueueBlockedKeyCounters),
	}
}

func (manager *Manager) logWriteQueueStats(prev writeQueueMetricsSnapshot, interval time.Duration) writeQueueMetricsSnapshot {
	cur := manager.snapshotWriteQueueMetrics()
	seconds := interval.Seconds()
	if seconds <= 0 {
		seconds = 1
	}

	enqDelta := cur.enqueueTotal - prev.enqueueTotal
	setDelta := cur.enqueueSetTotal - prev.enqueueSetTotal
	delDelta := cur.enqueueDeleteTotal - prev.enqueueDeleteTotal
	deqDelta := cur.dequeuedTotal - prev.dequeuedTotal
	flushBatchDelta := cur.flushBatchTotal - prev.flushBatchTotal
	flushedTaskDelta := cur.flushedTaskTotal - prev.flushedTaskTotal
	flushErrDelta := cur.flushErrTotal - prev.flushErrTotal
	flushDurationDeltaNs := cur.flushDurationNsTotal - prev.flushDurationNsTotal
	forceFlushDelta := cur.forceFlushTotal - prev.forceFlushTotal
	blockedDelta := cur.enqueueBlockedCount - prev.enqueueBlockedCount
	blockedNsDelta := cur.enqueueBlockedNs - prev.enqueueBlockedNs

	avgBatch := 0.0
	avgFlushMs := 0.0
	if flushBatchDelta > 0 {
		avgBatch = float64(flushedTaskDelta) / float64(flushBatchDelta)
		avgFlushMs = float64(flushDurationDeltaNs) / float64(flushBatchDelta) / float64(time.Millisecond)
	}

	avgBlockMs := 0.0
	if blockedDelta > 0 {
		avgBlockMs = float64(blockedNsDelta) / float64(blockedDelta) / float64(time.Millisecond)
	}

	qLen := len(manager.writeQueueChan)
	qCap := cap(manager.writeQueueChan)
	msg := fmt.Sprintf(
		"[DBQueue] 10s stats q=%d/%d max=%d enq=%d(%.1f/s,set=%d,del=%d) deq=%d(%.1f/s) flushTasks=%d batches=%d avgBatch=%.1f avgFlush=%.2fms flushErr=%d forceFlush=%d blocked=%d avgBlock=%.2fms",
		qLen, qCap, cur.maxDepth,
		enqDelta, float64(enqDelta)/seconds, setDelta, delDelta,
		deqDelta, float64(deqDelta)/seconds,
		flushedTaskDelta, flushBatchDelta, avgBatch, avgFlushMs,
		flushErrDelta, forceFlushDelta, blockedDelta, avgBlockMs,
	)
	topKeys := topCounterValues(cur.keyCounters, 6)
	if len(topKeys) > 0 {
		msg += " topWriteKeys=" + strings.Join(topKeys, ",")
	}
	topBlockedKeys := topCounterValues(cur.blockedKeyCounters, 6)
	if len(topBlockedKeys) > 0 {
		msg += " topBlockedKeys=" + strings.Join(topBlockedKeys, ",")
	}
	if manager.Logger != nil {
		manager.Logger.Info(msg)
	} else {
		logs.Info(msg)
	}
	return cur
}

                                      
func (manager *Manager) runWriteQueue() {
	defer manager.wg.Done()
	var batch []WriteTask
	batchCap := manager.writeQueueBatchOpLimit()
	if batchCap > 1024 {
		batchCap = 1024
	}
	batch = make([]WriteTask, 0, batchCap)
	ticker := time.NewTicker(manager.flushInterval)
	defer ticker.Stop()
	metricsTicker := time.NewTicker(10 * time.Second)
	defer metricsTicker.Stop()
	lastMetricsAt := time.Now()
	metricsPrev := manager.snapshotWriteQueueMetrics()
	retry := newWriteQueueRetryTimer()
	defer retry.Stop()

	flushCurrentBatch := func(req *flushRequest) error {
		if len(batch) == 0 {
			return nil
		}
		count := len(batch)
		start := time.Now()
		atomic.StoreUint64(&manager.writeQueueFlushInFlightSince, uint64(start.UnixNano()))
		err := manager.flushBatchWithTrace(batch, req)
		atomic.StoreUint64(&manager.writeQueueFlushInFlightSince, 0)
		duration := time.Since(start)
		atomic.AddUint64(&manager.writeQueueFlushBatchTotal, 1)
		atomic.AddUint64(&manager.writeQueueFlushDurationNsTotal, uint64(duration))
		if err != nil {
			atomic.AddUint64(&manager.writeQueueFlushErrTotal, 1)
			retryIn := retry.Schedule()
			logs.Warn("[runWriteQueue] retaining failed batch tasks=%d retryIn=%s: %v", count, retryIn, err)
		} else {
			atomic.AddUint64(&manager.writeQueueFlushedTaskTotal, uint64(count))
		}
		if duration >= 2*time.Second {
			logs.Warn("[DBQueue] slow flush batch=%d took=%s q=%d/%d", count, duration, len(manager.writeQueueChan), cap(manager.writeQueueChan))
		}
		if err != nil {
			return err
		}
		batch = batch[:0]
		retry.Reset()
		return err
	}
	flushDrainedBatches := func(req *flushRequest) error {
		for {
			if len(batch) == 0 {
				batch = manager.drainWriteQueue(batch)
			}
			if len(batch) == 0 {
				return nil
			}
			if err := flushCurrentBatch(req); err != nil {
				return err
			}
			if len(manager.writeQueueChan) == 0 {
				return nil
			}
		}
	}

	for {
		taskCh := manager.writeQueueChan
		if retry.Pending() {
			taskCh = nil
		}
		select {
		case <-manager.stopChan:
			retry.CancelPending()
			err := flushDrainedBatches(nil)
			manager.resolvePendingForceFlush(err)
			return
		case task := <-taskCh:
			atomic.AddUint64(&manager.writeQueueDequeuedTotal, 1)
			batch = append(batch, task)
			if len(batch) >= manager.maxBatchSize {
				if err := flushCurrentBatch(nil); err != nil {
					logs.Error("[runWriteQueue] flush by size failed: %v", err)
				}
			}
		case <-ticker.C:
			if retry.Pending() {
				continue
			}
			if err := flushDrainedBatches(nil); err != nil {
				logs.Error("[runWriteQueue] flush by ticker failed: %v", err)
			}
		case <-retry.C():
			retry.Fired()
			if err := flushDrainedBatches(nil); err != nil {
				logs.Error("[runWriteQueue] retry flush failed: %v", err)
			}
		case <-metricsTicker.C:
			metricsPrev = manager.logWriteQueueStats(metricsPrev, time.Since(lastMetricsAt))
			lastMetricsAt = time.Now()
		case req := <-manager.forceFlushChan:
			atomic.AddUint64(&manager.writeQueueForceFlushTotal, 1)
			retry.CancelPending()
			err := flushDrainedBatches(&req)
			manager.finishForceFlush(req, err)
			for {
				select {
				case req = <-manager.forceFlushChan:
					atomic.AddUint64(&manager.writeQueueForceFlushTotal, 1)
					err = flushDrainedBatches(&req)
					manager.finishForceFlush(req, err)
				default:
					goto doneForceFlush
				}
			}
		doneForceFlush:
		}
	}
}

func (manager *Manager) runColdWriteQueue() {
	defer manager.wg.Done()
	var batch []WriteTask
	batchCap := manager.writeQueueBatchOpLimit()
	if batchCap > 1024 {
		batchCap = 1024
	}
	batch = make([]WriteTask, 0, batchCap)
	ticker := time.NewTicker(manager.flushInterval)
	defer ticker.Stop()
	retry := newWriteQueueRetryTimer()
	defer retry.Stop()

	flushCurrentBatch := func(req *flushRequest) error {
		if len(batch) == 0 {
			return nil
		}
		err := manager.flushBatchWithTrace(batch, req)
		if err != nil {
			retryIn := retry.Schedule()
			logs.Warn("[runColdWriteQueue] retaining failed batch tasks=%d retryIn=%s: %v", len(batch), retryIn, err)
			return err
		}
		batch = batch[:0]
		retry.Reset()
		return nil
	}
	flushDrainedBatches := func(req *flushRequest) error {
		for {
			if len(batch) == 0 {
				batch = manager.drainColdWriteQueue(batch)
			}
			if len(batch) == 0 {
				return nil
			}
			if err := flushCurrentBatch(req); err != nil {
				return err
			}
			if len(manager.coldWriteQueueChan) == 0 {
				return nil
			}
		}
	}

	for {
		taskCh := manager.coldWriteQueueChan
		if retry.Pending() {
			taskCh = nil
		}
		select {
		case <-manager.stopChan:
			retry.CancelPending()
			err := flushDrainedBatches(nil)
			manager.resolvePendingForceFlushOn(manager.coldForceFlushChan, err)
			return
		case task := <-taskCh:
			batch = append(batch, task)
			if len(batch) >= manager.maxBatchSize {
				if err := flushCurrentBatch(nil); err != nil {
					logs.Error("[runColdWriteQueue] flush by size failed: %v", err)
				}
			}
		case <-ticker.C:
			if retry.Pending() {
				continue
			}
			if err := flushDrainedBatches(nil); err != nil {
				logs.Error("[runColdWriteQueue] flush by ticker failed: %v", err)
			}
		case <-retry.C():
			retry.Fired()
			if err := flushDrainedBatches(nil); err != nil {
				logs.Error("[runColdWriteQueue] retry flush failed: %v", err)
			}
		case req := <-manager.coldForceFlushChan:
			retry.CancelPending()
			err := flushDrainedBatches(&req)
			manager.finishForceFlush(req, err)
			for {
				select {
				case req = <-manager.coldForceFlushChan:
					err = flushDrainedBatches(&req)
					manager.finishForceFlush(req, err)
				default:
					goto doneForceFlush
				}
			}
		doneForceFlush:
		}
	}
}

func (manager *Manager) runWriteQueueWatchdog() {
	defer manager.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-manager.stopChan:
			return
		case <-ticker.C:
			sinceNs := atomic.LoadUint64(&manager.writeQueueFlushInFlightSince)
			if sinceNs == 0 {
				continue
			}
			elapsed := time.Since(time.Unix(0, int64(sinceNs)))
			if elapsed < 5*time.Second {
				continue
			}
			logs.Warn("[DBQueue] flush appears stuck elapsed=%s q=%d/%d", elapsed.Truncate(time.Millisecond), len(manager.writeQueueChan), cap(manager.writeQueueChan))
		}
	}
}

                                          
func (manager *Manager) ForceFlush() error {
	return manager.ForceFlushTrace(0, "")
}

func (manager *Manager) ForceFlushTrace(height uint64, blockID string) error {
	if err := manager.ForceFlushHotTrace(height, blockID); err != nil {
		return err
	}
	return manager.forceFlushOnTrace(manager.coldForceFlushChan, height, blockID, "DB.ForceFlushCold")
}

                                                                                 
                                                                           
                 
func (manager *Manager) ForceFlushHot() error {
	return manager.ForceFlushHotTrace(0, "")
}

func (manager *Manager) forceFlushOn(ch chan flushRequest) error {
	return manager.forceFlushOnTrace(ch, 0, "", "DB.ForceFlush")
}

func (manager *Manager) ForceFlushHotTrace(height uint64, blockID string) error {
	return manager.forceFlushOnTrace(manager.forceFlushChan, height, blockID, "DB.ForceFlushHot")
}

func (manager *Manager) forceFlushOnTrace(ch chan flushRequest, height uint64, blockID, name string) error {
	if ch == nil {
		return nil
	}
	startAt := time.Now()
	req := flushRequest{done: make(chan error, 1), height: height, blockID: blockID, name: name}
	var err error
	defer func() {
		if height > 0 {
			blocktrace.Record(height, blockID, name, time.Since(startAt), blocktrace.F("err", err))
		}
	}()
	if manager.stopChan != nil {
		select {
		case ch <- req:
		case <-manager.stopChan:
			err = fmt.Errorf("write queue already stopped")
			return err
		}
	} else {
		ch <- req
	}
	if manager.stopChan != nil {
		select {
		case err = <-req.done:
			return err
		case <-manager.stopChan:
			select {
			case err = <-req.done:
				return err
			default:
			}
			err = fmt.Errorf("write queue stopped before flush completed")
			return err
		}
	}
	err = <-req.done
	return err
}

func (manager *Manager) drainWriteQueue(batch []WriteTask) []WriteTask {
	return manager.drainWriteQueueFrom(manager.writeQueueChan, batch)
}

func (manager *Manager) drainColdWriteQueue(batch []WriteTask) []WriteTask {
	return manager.drainWriteQueueFrom(manager.coldWriteQueueChan, batch)
}

func (manager *Manager) drainWriteQueueFrom(ch chan WriteTask, batch []WriteTask) []WriteTask {
	limit := manager.maxBatchSize
	if limit <= 0 {
		limit = 1
	}
	for len(batch) < limit {
		select {
		case task := <-ch:
			atomic.AddUint64(&manager.writeQueueDequeuedTotal, 1)
			batch = append(batch, task)
		default:
			return batch
		}
	}
	return batch
}

func (manager *Manager) finishForceFlush(req flushRequest, err error) {
	req.done <- err
	close(req.done)
}

func (manager *Manager) resolvePendingForceFlush(err error) {
	manager.resolvePendingForceFlushOn(manager.forceFlushChan, err)
}

func (manager *Manager) resolvePendingForceFlushOn(ch chan flushRequest, err error) {
	for {
		select {
		case req := <-ch:
			manager.finishForceFlush(req, err)
		default:
			return
		}
	}
}

                                                             
func (manager *Manager) EnqueueDel(key string) {
	manager.EnqueueDelete(key)
}

func (manager *Manager) writeQueueBatchOpLimit() int {
	if manager.maxBatchSize > 0 {
		return manager.maxBatchSize
	}
	return 1
}

func (manager *Manager) pebbleBatchByteLimit() int {
	if manager.maxPebbleBatchBytes > 0 {
		return manager.maxPebbleBatchBytes
	}
	return defaultWriteQueueMaxPebbleBatchBytes
}

func (manager *Manager) stateApplyByteLimit() int {
	if manager.maxStateApplyBytes > 0 {
		return manager.maxStateApplyBytes
	}
	return defaultWriteQueueMaxStateApplyBytes
}

func writeTaskBatchBytes(task WriteTask) int {
	return len(task.Key) + len(task.Value) + 32
}

func stateUpdateBatchBytes(update statedb.KVUpdate) int {
	return len(update.Key) + len(update.Value) + 32
}

func (manager *Manager) currentStateStore() (*statedb.DB, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, fmt.Errorf("stateDB is not initialized")
	}
	return stateStore, nil
}

func (manager *Manager) flushBatch(batch []WriteTask) error {
	return manager.flushBatchWithTrace(batch, nil)
}

func (manager *Manager) flushBatchWithTrace(batch []WriteTask, req *flushRequest) error {
	if len(batch) == 0 {
		return nil
	}
	startAt := time.Now()
	opLimit := manager.writeQueueBatchOpLimit()
	pebbleByteLimit := manager.pebbleBatchByteLimit()
	stateByteLimit := manager.stateApplyByteLimit()
	stateCap := opLimit
	if stateCap > 1024 {
		stateCap = 1024
	}
	stateUpdates := make([]statedb.KVUpdate, 0, stateCap)
	stateBytes, stateOpsTotal, stateApplies := 0, 0, 0
	var stateApplyDur time.Duration
	var stateStore *statedb.DB
	flushState := func() error {
		if len(stateUpdates) == 0 {
			return nil
		}
		if stateStore == nil {
			var err error
			stateStore, err = manager.currentStateStore()
			if err != nil {
				return err
			}
		}
		                                                                               
		                                        
		applyAt := time.Now()
		if err := stateStore.ApplyAccountUpdate(0, stateUpdates...); err != nil {
			stateApplyDur += time.Since(applyAt)
			logs.Error("[flushBatch] stateDB apply error: %v", err)
			return err
		}
		stateApplyDur += time.Since(applyAt)
		for i := range stateUpdates {
			stateUpdates[i] = statedb.KVUpdate{}
		}
		stateUpdates = stateUpdates[:0]
		stateBytes = 0
		stateApplies++
		return nil
	}

	kvTasks := make(map[keys.StorageClass][]WriteTask, 3)
	for _, task := range batch {
		key := string(task.Key)
		if keys.IsStatefulKey(key) {
			continue
		}
		class := keys.StorageClassForKey(key)
		kvTasks[class] = append(kvTasks[class], task)
	}
	kvOpsTotal, kvCommits := 0, 0
	classes := []keys.StorageClass{keys.StorageRuntime, keys.StorageQuery, keys.StorageArchive}
	for _, class := range classes {
		ops := kvTasks[class]
		if len(ops) == 0 {
			continue
		}
		classAt := time.Now()
		commits, err := manager.flushPebbleTasks(class, ops, opLimit, pebbleByteLimit)
		classDur := time.Since(classAt)
		recordFlushStep(req, "DB.flushBatch."+class.String(), classDur, blocktrace.F("ops", len(ops)), blocktrace.F("commits", commits))
		if err != nil {
			return err
		}
		kvOpsTotal += len(ops)
		kvCommits += commits
	}

	for _, task := range batch {
		key := string(task.Key)
		if !keys.IsStatefulKey(key) {
			continue
		}
		update := statedb.KVUpdate{
			Key:     key,
			Deleted: task.Op == OpDelete,
		}
		if task.Op == OpSet {
			valCopy := make([]byte, len(task.Value))
			copy(valCopy, task.Value)
			update.Value = valCopy
		}
		updateBytes := stateUpdateBatchBytes(update)
		if len(stateUpdates) > 0 && (len(stateUpdates) >= opLimit || stateBytes+updateBytes > stateByteLimit) {
			if err := flushState(); err != nil {
				return err
			}
		}
		stateUpdates = append(stateUpdates, update)
		stateBytes += updateBytes
		stateOpsTotal++
	}
	if err := flushState(); err != nil {
		return err
	}
	recordFlushStep(req, "DB.flushBatch.stateDB", stateApplyDur, blocktrace.F("ops", stateOpsTotal), blocktrace.F("applies", stateApplies))
	recordFlushStep(req, "DB.flushBatch.total", time.Since(startAt),
		blocktrace.F("tasks", len(batch)), blocktrace.F("kvOps", kvOpsTotal), blocktrace.F("kvCommits", kvCommits),
		blocktrace.F("stateOps", stateOpsTotal), blocktrace.F("stateApplies", stateApplies),
	)
	if kvCommits > 1 || stateApplies > 1 {
		logs.Info("[flushBatch] chunked flush tasks=%d kvOps=%d kvCommits=%d stateOps=%d stateApplies=%d", len(batch), kvOpsTotal, kvCommits, stateOpsTotal, stateApplies)
	}
	return nil
}

func recordFlushStep(req *flushRequest, name string, dur time.Duration, fields ...blocktrace.Field) {
	if req == nil || req.height == 0 {
		return
	}
	all := make([]blocktrace.Field, 0, len(fields)+1)
	all = append(all, fields...)
	all = append(all, blocktrace.F("request", req.name))
	blocktrace.Record(req.height, req.blockID, name, dur, all...)
}

func (manager *Manager) flushPebbleTasks(class keys.StorageClass, tasks []WriteTask, opLimit, byteLimit int) (int, error) {
	db, err := manager.dbForStorageClass(class)
	if err != nil {
		return 0, err
	}
	var batch *pebble.Batch
	ops, bytesInBatch, commits := 0, 0, 0
	defer func() {
		if batch != nil {
			_ = batch.Close()
		}
	}()
	flush := func() error {
		if ops == 0 {
			return nil
		}
		if batch == nil {
			return fmt.Errorf("%s pebble batch is not initialized", class.String())
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			logs.Error("[flushBatch] %s commit error: %v", class.String(), err)
			return err
		}
		_ = batch.Close()
		batch = nil
		ops, bytesInBatch = 0, 0
		commits++
		return nil
	}
	ensureBatch := func() {
		if batch == nil {
			batch = db.NewBatch()
		}
	}
	for _, task := range tasks {
		taskBytes := writeTaskBatchBytes(task)
		if ops > 0 && (ops >= opLimit || bytesInBatch+taskBytes > byteLimit) {
			if err := flush(); err != nil {
				return commits, err
			}
		}
		ensureBatch()
		switch task.Op {
		case OpSet:
			err = batch.Set(task.Key, task.Value, nil)
		case OpDelete:
			err = batch.Delete(task.Key, nil)
		default:
			err = fmt.Errorf("unknown write op %d for key %q", task.Op, string(task.Key))
		}
		if err != nil {
			logs.Error("[flushBatch] %s set/delete error: %v", class.String(), err)
			return commits, err
		}
		ops++
		bytesInBatch += taskBytes
	}
	if err := flush(); err != nil {
		return commits, err
	}
	return commits, nil
}

                                                                         
func (manager *Manager) EnqueueSet(key, value string) {
	bucket := normalizeWriteQueueKeyBucket(key)
	start := time.Now()
	ch := manager.queueForKey(key)
	ch <- WriteTask{Key: []byte(key), Value: []byte(value), Op: OpSet}
	atomic.AddUint64(&manager.writeQueueEnqueueTotal, 1)
	atomic.AddUint64(&manager.writeQueueEnqueueSetTotal, 1)
	manager.addCounter(&manager.writeQueueKeyCounters, bucket)
	blocked := time.Since(start)
	if blocked > 100*time.Microsecond {
		atomic.AddUint64(&manager.writeQueueEnqueueBlockedCount, 1)
		atomic.AddUint64(&manager.writeQueueEnqueueBlockedNs, uint64(blocked))
		manager.addCounter(&manager.writeQueueBlockedKeyCounters, bucket)
	}
	manager.observeQueueDepth()
}

func (manager *Manager) EnqueueDelete(key string) {
	bucket := normalizeWriteQueueKeyBucket(key)
	start := time.Now()
	ch := manager.queueForKey(key)
	ch <- WriteTask{Key: []byte(key), Op: OpDelete}
	atomic.AddUint64(&manager.writeQueueEnqueueTotal, 1)
	atomic.AddUint64(&manager.writeQueueEnqueueDeleteTotal, 1)
	manager.addCounter(&manager.writeQueueKeyCounters, bucket)
	blocked := time.Since(start)
	if blocked > 100*time.Microsecond {
		atomic.AddUint64(&manager.writeQueueEnqueueBlockedCount, 1)
		atomic.AddUint64(&manager.writeQueueEnqueueBlockedNs, uint64(blocked))
		manager.addCounter(&manager.writeQueueBlockedKeyCounters, bucket)
	}
	manager.observeQueueDepth()
}

func (manager *Manager) queueForKey(key string) chan WriteTask {
	if !keys.IsStatefulKey(key) {
		class := keys.StorageClassForKey(key)
		if (class == keys.StorageQuery || class == keys.StorageArchive) && manager.coldWriteQueueChan != nil {
			return manager.coldWriteQueueChan
		}
	}
	if manager.writeQueueChan != nil {
		return manager.writeQueueChan
	}
	return manager.coldWriteQueueChan
}

                                                       
func (m *Manager) GetChannelStats() []stats.ChannelStat {
	if m.writeQueueChan == nil {
		return nil
	}
	return []stats.ChannelStat{
		stats.NewChannelStat("writeQueueChan", "DB", len(m.writeQueueChan), cap(m.writeQueueChan)),
	}
}
