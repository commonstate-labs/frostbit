package sender

import (
	"dex/config"
	"dex/logs"
	"dex/pb"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

type txGossipItem struct {
	tx      *pb.AnyTx
	targets []string
}

type txGossipBatcher struct {
	sm                *SenderManager
	in                chan txGossipItem
	stopCh            chan struct{}
	doneCh            chan struct{}
	stopOnce          sync.Once
	backpressureUntil sync.Map                       
	batchSize         int
	flushEvery        time.Duration
	maxPending        int
}

const txGossipBackpressureCooldown = time.Second

func newTxGossipBatcher(sm *SenderManager) *txGossipBatcher {
	if sm == nil {
		return nil
	}
	cfg := config.DefaultConfig().Sender
	if sm.cfg != nil {
		cfg = sm.cfg.Sender
	}
	capacity := cfg.TxGossipInputCapacity
	if capacity <= 0 {
		capacity = 8192
	}
	b := &txGossipBatcher{
		sm:         sm,
		in:         make(chan txGossipItem, capacity),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		batchSize:  cfg.TxGossipBatchSize,
		flushEvery: cfg.TxGossipFlushInterval,
		maxPending: cfg.TxGossipMaxPendingPerPeer,
	}
	if b.batchSize <= 0 {
		b.batchSize = 256
	}
	if b.flushEvery <= 0 {
		b.flushEvery = 50 * time.Millisecond
	}
	if b.maxPending <= 0 {
		b.maxPending = 4096
	}
	go b.run()
	return b
}

func (b *txGossipBatcher) Enqueue(tx *pb.AnyTx, targets []string) bool {
	if b == nil || tx == nil {
		return false
	}
	select {
	case b.in <- txGossipItem{tx: tx, targets: targets}:
		return true
	default:
		logs.Debug("[TxGossipBatcher] input queue full, dropping gossip tx=%s targets=%d", tx.GetTxId(), len(targets))
		return true
	}
}

func (b *txGossipBatcher) Stop() {
	if b == nil {
		return
	}
	select {
	case <-b.doneCh:
		return
	default:
	}
	b.stopOnce.Do(func() { close(b.stopCh) })
	<-b.doneCh
}

func (b *txGossipBatcher) run() {
	defer close(b.doneCh)
	ticker := time.NewTicker(b.flushEvery)
	defer ticker.Stop()

	pending := make(map[string][]*pb.AnyTx)
	pendingIDs := make(map[string]map[string]struct{})
	var global []*pb.AnyTx
	globalIDs := make(map[string]struct{})

	flushTarget := func(target string) {
		txs := pending[target]
		if len(txs) == 0 {
			return
		}
		n := b.batchSize
		if len(txs) < n {
			n = len(txs)
		}
		b.sendBatch(target, txs[:n])
		if n == len(txs) {
			delete(pending, target)
			delete(pendingIDs, target)
			return
		}
		rest := txs[n:]
		pending[target] = rest
		ids := make(map[string]struct{}, len(rest))
		for _, tx := range rest {
			if txID := tx.GetTxId(); txID != "" {
				ids[txID] = struct{}{}
			}
		}
		pendingIDs[target] = ids
	}

	flushGlobal := func() {
		for len(global) > 0 {
			n := b.batchSize
			if len(global) < n {
				n = len(global)
			}
			if !b.sendBatchToRandomPeers(global[:n]) {
				return
			}
			for _, tx := range global[:n] {
				if txID := tx.GetTxId(); txID != "" {
					delete(globalIDs, txID)
				}
			}
			global = global[n:]
		}
	}

	flushAll := func() {
		flushGlobal()
		for target := range pending {
			for len(pending[target]) > 0 {
				flushTarget(target)
			}
		}
	}

	for {
		select {
		case item := <-b.in:
			txID := item.tx.GetTxId()
			if len(item.targets) == 0 {
				if txID != "" {
					if _, exists := globalIDs[txID]; exists {
						continue
					}
				}
				if len(global) >= b.maxPending {
					logs.Debug("[TxGossipBatcher] global backlog full, dropping gossip tx=%s", txID)
					continue
				}
				global = append(global, item.tx)
				if txID != "" {
					globalIDs[txID] = struct{}{}
				}
				if len(global) >= b.batchSize {
					flushGlobal()
				}
				continue
			}
			for _, target := range item.targets {
				if target == "" {
					continue
				}
				if pendingIDs[target] == nil {
					pendingIDs[target] = make(map[string]struct{})
				}
				if txID != "" {
					if _, exists := pendingIDs[target][txID]; exists {
						continue
					}
				}
				if len(pending[target]) >= b.maxPending {
					logs.Debug("[TxGossipBatcher] target backlog full, dropping gossip tx=%s target=%s", txID, target)
					continue
				}
				pending[target] = append(pending[target], item.tx)
				if txID != "" {
					pendingIDs[target][txID] = struct{}{}
				}
				if len(pending[target]) >= b.batchSize {
					flushTarget(target)
				}
			}
		case <-ticker.C:
			flushAll()
		case <-b.stopCh:
			flushAll()
			return
		}
	}
}

func (b *txGossipBatcher) sendBatchToRandomPeers(txs []*pb.AnyTx) bool {
	if b == nil || b.sm == nil || len(txs) == 0 {
		return false
	}
	targets, err := b.sm.getRandomGossipIPs(b.sm.txGossipFanout())
	if err != nil {
		logs.Debug("[TxGossipBatcher] skip batch: no gossip peers count=%d err=%v", len(txs), err)
		return false
	}
	data, ok := b.marshalBatch("", txs)
	if !ok {
		return false
	}
	sent := false
	now := time.Now()
	for _, target := range targets {
		if target == "" || b.targetBackpressured(target, now) {
			continue
		}
		b.sendBatchData(target, data, len(txs))
		sent = true
	}
	return sent
}

func (b *txGossipBatcher) sendBatch(target string, txs []*pb.AnyTx) {
	if b == nil || b.sm == nil || len(txs) == 0 || b.targetBackpressured(target, time.Now()) {
		return
	}
	data, ok := b.marshalBatch(target, txs)
	if !ok {
		return
	}
	b.sendBatchData(target, data, len(txs))
}

func (b *txGossipBatcher) marshalBatch(target string, txs []*pb.AnyTx) ([]byte, bool) {
	msg := &pb.BatchGetShortTxResponse{Transactions: txs}
	data, err := proto.Marshal(msg)
	if err != nil {
		logs.Debug("[TxGossipBatcher] marshal batch failed target=%s count=%d err=%v", target, len(txs), err)
		return nil, false
	}
	return data, true
}

func (b *txGossipBatcher) sendBatchData(target string, data []byte, count int) {
	if b == nil || b.sm == nil || target == "" || len(data) == 0 {
		return
	}
	b.sm.SendQueue.Enqueue(&SendTask{
		Target:      target,
		Message:     &txBatchMessage{requestData: data, count: count},
		RetryCount:  0,
		MaxRetries:  0,
		SendFunc:    doSendTxBatch,
		Priority:    PriorityData,
		NodeAddress: b.sm.address,
		OnFailure: func(err error) {
			b.recordTargetBackpressure(target, err, time.Now())
		},
	})
}

func (b *txGossipBatcher) recordTargetBackpressure(target string, err error, now time.Time) {
	if b == nil || target == "" || !isBackpressureSendError(err) {
		return
	}
	b.backpressureUntil.Store(target, now.Add(txGossipBackpressureCooldown))
}

func (b *txGossipBatcher) targetBackpressured(target string, now time.Time) bool {
	if b == nil || target == "" {
		return false
	}
	value, ok := b.backpressureUntil.Load(target)
	if !ok {
		return false
	}
	until, ok := value.(time.Time)
	return ok && now.Before(until)
}

func (sm *SenderManager) txGossipFanout() int {
	if sm == nil || sm.cfg == nil || sm.cfg.Sender.TxGossipFanout <= 0 {
		return 3
	}
	return sm.cfg.Sender.TxGossipFanout
}
