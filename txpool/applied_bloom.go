package txpool

import (
	"dex/keys"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
)

const (
	defaultAppliedBloomEstimate    = 50000000
	defaultAppliedBloomFalseRate   = 0.01
	defaultAppliedBloomRebuildPage = 100000
)

type appliedTxBloomIndex struct {
	mu          sync.RWMutex
	filter      *bloom.BloomFilter
	ready       bool
	rebuilding  bool
	pendingAdds map[string]struct{}
	estimate    uint
	falseRate   float64
}

func newAppliedTxBloomIndex(estimate int, falseRate float64) *appliedTxBloomIndex {
	if estimate <= 0 {
		estimate = defaultAppliedBloomEstimate
	}
	if falseRate <= 0 || falseRate >= 1 {
		falseRate = defaultAppliedBloomFalseRate
	}
	return &appliedTxBloomIndex{
		filter:    bloom.NewWithEstimates(uint(estimate), falseRate),
		estimate:  uint(estimate),
		falseRate: falseRate,
	}
}

func (idx *appliedTxBloomIndex) newFilter() *bloom.BloomFilter {
	if idx == nil {
		return nil
	}
	return bloom.NewWithEstimates(idx.estimate, idx.falseRate)
}

func (idx *appliedTxBloomIndex) mightContain(txID string) (bool, bool) {
	if idx == nil || txID == "" {
		return false, false
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if !idx.ready || idx.filter == nil {
		return false, false
	}
	return true, idx.filter.TestString(txID)
}

func (idx *appliedTxBloomIndex) add(txID string) {
	if idx == nil || txID == "" {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.ready && idx.filter != nil {
		idx.filter.AddString(txID)
	}
	if idx.rebuilding {
		if idx.pendingAdds == nil {
			idx.pendingAdds = make(map[string]struct{})
		}
		idx.pendingAdds[txID] = struct{}{}
	}
}

func (idx *appliedTxBloomIndex) beginRebuild() bool {
	if idx == nil {
		return false
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.rebuilding {
		return false
	}
	idx.rebuilding = true
	idx.ready = false
	idx.pendingAdds = make(map[string]struct{})
	return true
}

func (idx *appliedTxBloomIndex) finishRebuild(filter *bloom.BloomFilter) int {
	if idx == nil || filter == nil {
		return 0
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	pending := len(idx.pendingAdds)
	for txID := range idx.pendingAdds {
		filter.AddString(txID)
	}
	idx.filter = filter
	idx.ready = true
	idx.rebuilding = false
	idx.pendingAdds = nil
	return pending
}

func (idx *appliedTxBloomIndex) abortRebuild() {
	if idx == nil {
		return
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.rebuilding = false
	idx.pendingAdds = nil
}

func appliedBloomRebuildBatchSize(n int) int {
	if n <= 0 {
		return defaultAppliedBloomRebuildPage
	}
	return n
}

func (tp *TxPool) startAppliedBloomRebuild() {
	if tp == nil || tp.appliedBloom == nil || tp.dbManager == nil {
		return
	}
	if !tp.appliedBloom.beginRebuild() {
		return
	}
	tp.wg.Add(1)
	go tp.rebuildAppliedBloom()
}

func (tp *TxPool) rebuildAppliedBloom() {
	defer tp.wg.Done()
	start := time.Now()
	filter := tp.appliedBloom.newFilter()
	prefix := keys.KeyVMAppliedTx("")
	batchSize := appliedBloomRebuildBatchSize(tp.cfg.TxPool.AppliedBloomRebuildBatchSize)
	startAfter := ""
	loaded := 0

	for {
		select {
		case <-tp.stopChan:
			tp.appliedBloom.abortRebuild()
			return
		default:
		}

		keysPage, err := tp.dbManager.ScanKeysWithLimitOrderedAfter(prefix, startAfter, batchSize, false)
		if err != nil {
			tp.appliedBloom.abortRebuild()
			tp.Logger.Warn("[TxPool][AppliedBloom] rebuild failed after %d keys: %v", loaded, err)
			return
		}
		if len(keysPage) == 0 {
			break
		}
		for _, key := range keysPage {
			txID := key[len(prefix):]
			if txID != "" {
				filter.AddString(txID)
				loaded++
			}
		}
		startAfter = keysPage[len(keysPage)-1]
		if len(keysPage) < batchSize {
			break
		}
	}

	pending := tp.appliedBloom.finishRebuild(filter)
	tp.Logger.Info("[TxPool][AppliedBloom] ready loaded=%d pending=%d estimate=%d fp=%.4f dur=%s", loaded, pending, tp.appliedBloom.estimate, tp.appliedBloom.falseRate, time.Since(start))
}
