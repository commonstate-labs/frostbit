package txpool

import (
	"context"
	"dex/config"
	"dex/db"
	"dex/interfaces"
	"dex/keys"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"
	"dex/stats"
	"dex/utils"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	lru "github.com/hashicorp/golang-lru"
	"google.golang.org/protobuf/proto"
)

var (
	ErrSyncCatchUpBackpressure = errors.New("sync catch-up backpressure")
	ErrExecutionGuardExpired   = errors.New("execution guard target height has finalized")
)

                            
type TxPool struct {
	dbManager *db.Manager

	         
	mu                     sync.RWMutex
	Logger                 logs.Logger
	pendingAnyTxCache      *lru.Cache
	shortPendingAnyTxCache *lru.Cache
	cacheTx                *lru.Cache
	shortTxCache           *lru.Cache
	appliedTxCache         *lru.Cache                                           
	appliedBloom           *appliedTxBloomIndex
	                                                                        
	                                                                            
	executionGuardByHeight map[uint64]map[string]struct{}
	executionGuardByTxID   map[string]uint64
	pendingAdmissions      map[string]*pendingAdmission
	pendingAccounts        map[string]*pendingAccount
	pendingBytes           int64
	localProtocolPending   int
	maintenanceCursor      int
	queuedBytes            atomic.Int64

	                     
	Queue     *txPoolQueue
	validator TxValidator

	         
	address  string
	stopChan chan struct{}
	wg       sync.WaitGroup

	                                                                                    
	pendingSaveQueue   chan *pb.AnyTx
	pendingSaveWorkers int
	                                                                             
	                                                                          
	pendingPersistenceLocks [256]sync.Mutex
	cfg                     *config.Config

	                                                                   
	emergency       *lifecycle.EmergencyStop
	syncPressure    interfaces.SyncPressureReader
	finalizedHeight atomic.Uint64
}

                                                                                                    
func (tp *TxPool) SetEmergencyStop(e *lifecycle.EmergencyStop) {
	if tp == nil {
		return
	}
	tp.emergency = e
}

func (tp *TxPool) SetSyncPressureReader(reader interfaces.SyncPressureReader) {
	if tp == nil {
		return
	}
	tp.syncPressure = reader
}

                                                            
func NewTxPool(dbManager *db.Manager, validator TxValidator, address string, logger logs.Logger) (*TxPool, error) {
	return NewTxPoolWithConfig(dbManager, validator, address, logger, nil)
}

                                                                    
func NewTxPoolWithConfig(dbManager *db.Manager, validator TxValidator, address string, logger logs.Logger, cfg *config.Config) (*TxPool, error) {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	pendingAnyTxCache, err := lru.New(cfg.TxPool.PendingTxCacheSize)
	if err != nil {
		return nil, err
	}
	shortPendingAnyTxCache, _ := lru.New(cfg.TxPool.ShortPendingTxCacheSize)
	cacheTx, _ := lru.New(cfg.TxPool.CacheTxSize)
	shortTxCache, _ := lru.New(cfg.TxPool.ShortTxCacheSize)
	appliedTxCache, _ := lru.New(50000)                      
	appliedBloom := newAppliedTxBloomIndex(cfg.TxPool.AppliedBloomEstimatedItems, cfg.TxPool.AppliedBloomFalsePositiveRate)

	pendingSaveQueueSize := cfg.TxPool.MessageQueueSize
	if pendingSaveQueueSize <= 0 {
		pendingSaveQueueSize = 10000
	}
	pendingSaveWorkers := 4

	tp := &TxPool{
		dbManager:              dbManager,
		address:                address,
		Logger:                 logger,
		pendingAnyTxCache:      pendingAnyTxCache,
		shortPendingAnyTxCache: shortPendingAnyTxCache,
		cacheTx:                cacheTx,
		shortTxCache:           shortTxCache,
		appliedTxCache:         appliedTxCache,
		appliedBloom:           appliedBloom,
		executionGuardByHeight: make(map[uint64]map[string]struct{}),
		executionGuardByTxID:   make(map[string]uint64),
		pendingAdmissions:      make(map[string]*pendingAdmission),
		pendingAccounts:        make(map[string]*pendingAccount),
		validator:              validator,
		stopChan:               make(chan struct{}),
		pendingSaveQueue:       make(chan *pb.AnyTx, pendingSaveQueueSize),
		pendingSaveWorkers:     pendingSaveWorkers,
		cfg:                    cfg,
	}
	                     
	tp.Queue = newTxPoolQueue(tp, validator)

	                             
	tp.loadFromDB()

	return tp, nil
}

                        
func (tp *TxPool) Start() error {
	                           
	tp.wg.Add(1)
	go tp.Queue.runLoop()
	for i := 0; i < tp.pendingSaveWorkers; i++ {
		tp.wg.Add(1)
		go tp.runPendingSaveWorker(i)
	}
	tp.startAppliedBloomRebuild()
	tp.wg.Add(1)
	go tp.runPendingMaintenance()

	logs.Info("[TxPool] Started")
	return nil
}

                       
func (tp *TxPool) Stop() error {
	close(tp.stopChan)
	tp.wg.Wait()
	logs.Info("[TxPool] Stopped")
	return nil
}

                                          
func (tp *TxPool) SubmitTx(anyTx *pb.AnyTx, fromIP string, onAdded OnTxAddedCallback) error {
	_, err := tp.submitTx(anyTx, fromIP, onAdded, nil)
	return err
}

                                                                                  
                                                                                 
                                                                                 
                          
func (tp *TxPool) SubmitTxAndWait(ctx context.Context, anyTx *pb.AnyTx, fromIP string, onAdded OnTxAddedCallback) error {
	result := make(chan error, 1)
	queued, err := tp.submitTx(anyTx, fromIP, onAdded, result)
	if err != nil || !queued {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-tp.stopChan:
		return fmt.Errorf("txpool is stopping")
	}
}

func (tp *TxPool) submitTx(anyTx *pb.AnyTx, fromIP string, onAdded OnTxAddedCallback, result chan<- error) (bool, error) {
	if anyTx == nil {
		return false, fmt.Errorf("nil transaction")
	}
	if tp == nil || tp.Queue == nil {
		return false, fmt.Errorf("txpool is not initialized")
	}
	if tp.isExecutionGuardExpired(anyTx) {
		return false, ErrExecutionGuardExpired
	}
	if tp.emergency.IsStopped() {
		return false, lifecycle.ErrEmergencyStop
	}
	txID := anyTx.GetTxId()
	                                                                   
	                                                                          
	if txID != "" && tp.HasTransaction(txID) {
		return false, nil
	}
	                                                                             
	                                                                               
	                                                            
	if base := anyTx.GetBase(); base != nil && base.FromAddress == tp.address && tp.address != "" && !pb.TxRequiresAccountNonce(anyTx) {
		added, err := tp.storeAnyTx(anyTx)
		if err == nil && added && onAdded != nil {
			onAdded(txID)
		}
		return false, err
	}
	if tp.syncPressure != nil {
		pressure := tp.syncPressure.SyncPressureState()
		if pressure.CatchUp && pb.TxRequiresAccountNonce(anyTx) {
			return false, fmt.Errorf("%w: reason=%s local=%d target=%d lag=%d",
				ErrSyncCatchUpBackpressure, pressure.Reason, pressure.LocalHeight, pressure.TargetHeight, pressure.Lag)
		}
	}

	maxPending := tp.MaxPendingLimit()
	pending := tp.PendingLen()
	if maxPending > 0 && pending >= maxPending {
		return false, fmt.Errorf("txpool pending is full (%d/%d)", pending, maxPending)
	}

	size := int64(proto.Size(anyTx))
	queueLimit := tp.cfg.TxPool.MaxPendingBytes / 4
	for {
		used := tp.queuedBytes.Load()
		if size > queueLimit-used {
			return false, fmt.Errorf("%w: transaction queue byte budget full", ErrPendingAdmission)
		}
		if tp.queuedBytes.CompareAndSwap(used, used+size) {
			break
		}
	}
	msg := &txPoolMessage{
		Type:    msgAddTx,
		Bytes:   size,
		AnyTx:   anyTx,
		IP:      fromIP,
		OnAdded: onAdded,
		Result:  result,
	}

	select {
	case tp.Queue.MsgChan <- msg:
		return true, nil
	default:
		tp.queuedBytes.Add(-size)
		return false, fmt.Errorf("txpool queue is full (%d/%d)", len(tp.Queue.MsgChan), cap(tp.Queue.MsgChan))
	}
}

func (tp *TxPool) pendingPersistenceLock(txID string) *sync.Mutex {
	var hash uint32 = 2166136261
	for i := 0; i < len(txID); i++ {
		hash ^= uint32(txID[i])
		hash *= 16777619
	}
	return &tp.pendingPersistenceLocks[hash%uint32(len(tp.pendingPersistenceLocks))]
}

                        
func (tp *TxPool) RemoveTx(txID string) error {
	if txID == "" {
		return fmt.Errorf("empty txID")
	}

	pLock := tp.pendingPersistenceLock(txID)
	pLock.Lock()
	defer pLock.Unlock()

	tp.mu.Lock()

	tp.removePendingLocked(txID)
	tp.mu.Unlock()

	                    
	return tp.dbManager.DeletePendingAnyTx(txID)
}

                                    
func (tp *TxPool) HasTransaction(txID string) bool {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	_, exists := tp.pendingAnyTxCache.Get(txID)
	return exists
}

func (tp *TxPool) GetTransactionById(txID string) *pb.AnyTx {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	if v, ok := tp.pendingAnyTxCache.Get(txID); ok {
		if tx, ok := v.(*pb.AnyTx); ok {
			return tx
		}
	}
	return nil
}

func (tp *TxPool) GetPendingTxs() []*pb.AnyTx {
	return tp.GetPendingTxsLimit(0)
}

func (tp *TxPool) GetPendingTxsLimit(limit int) []*pb.AnyTx {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	type pendingEntry struct {
		key      string
		priority int
		tx       *pb.AnyTx
	}

	rawKeys := tp.pendingAnyTxCache.Keys()
	entries := make([]pendingEntry, 0, len(rawKeys))
	for _, k := range rawKeys {
		keyStr, ok := k.(string)
		if !ok {
			continue
		}
		if value, exists := tp.pendingAnyTxCache.Get(keyStr); exists {
			if anyTx, ok := value.(*pb.AnyTx); ok {
				base := anyTx.GetBase()
				if base != nil {
					entries = append(entries, pendingEntry{
						key:      keyStr,
						priority: pendingAnyTxPriority(anyTx),
						tx:       anyTx,
					})
				}
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].priority != entries[j].priority {
			return entries[i].priority < entries[j].priority
		}
		return entries[i].key < entries[j].key
	})

	result := make([]*pb.AnyTx, 0, len(entries))
	for _, entry := range entries {
		if limit > 0 && len(result) >= limit {
			break
		}
		result = append(result, entry.tx)
	}
	return result
}

func pendingAnyTxPriority(anyTx *pb.AnyTx) int {
	if anyTx == nil {
		return 100
	}
	switch anyTx.GetContent().(type) {
	case *pb.AnyTx_FrostVaultDkgCommitTx,
		*pb.AnyTx_FrostVaultDkgShareTx,
		*pb.AnyTx_FrostVaultDkgComplaintTx,
		*pb.AnyTx_FrostVaultDkgRevealTx,
		*pb.AnyTx_FrostVaultDkgValidationSignedTx,
		*pb.AnyTx_FrostVaultTransitionSignedTx,
		*pb.AnyTx_CertSignTx:
		return 0
	default:
		return 100
	}
}

               
func (tp *TxPool) isTxApplied(txID string) bool {
	if txID == "" {
		return false
	}
	                    
	if tp.appliedTxCache != nil {
		if _, ok := tp.appliedTxCache.Get(txID); ok {
			return true
		}
	}
	if ready, might := tp.appliedBloom.mightContain(txID); ready && !might {
		return false
	}
	if tp.dbManager == nil {
		return false
	}
	status, err := tp.dbManager.Get(keys.KeyVMAppliedTx(txID))
	if err == nil && len(status) > 0 {
		tp.markTxAppliedInMemory(txID)
		return true
	}
	return false
}

func (tp *TxPool) markTxAppliedInMemory(txID string) {
	if txID == "" {
		return
	}
	if tp.appliedTxCache != nil {
		tp.appliedTxCache.Add(txID, struct{}{})
	}
	tp.appliedBloom.add(txID)
}

func (tp *TxPool) cacheAnyTxUnlocked(txID string, anyTx *pb.AnyTx, pending bool) {
	tp.cacheTx.Add(txID, anyTx)
	if pending {
		tp.pendingAnyTxCache.Add(txID, anyTx)
		tp.trackExecutionGuardUnlocked(txID, anyTx)
	}
	if len(txID) >= 18 {
		shortID := txID[2:18]
		tp.shortTxCache.Add(shortID, txID)
		if pending {
			tp.shortPendingAnyTxCache.Add(shortID, txID)
		}
	}
}

                                                                                    
func (tp *TxPool) CacheAnyTx(anyTx *pb.AnyTx) error {
	if anyTx == nil {
		return fmt.Errorf("nil transaction")
	}
	txID := anyTx.GetTxId()
	if txID == "" {
		return fmt.Errorf("txID is empty")
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.cacheAnyTxUnlocked(txID, anyTx, false)
	return nil
}

                                                                                           
func (tp *TxPool) insertPending(anyTx *pb.AnyTx) (bool, error) {
	if anyTx == nil || anyTx.GetBase() == nil || anyTx.GetTxId() == "" {
		return false, fmt.Errorf("invalid transaction")
	}
	if tp.isExecutionGuardExpired(anyTx) {
		return false, ErrExecutionGuardExpired
	}
	txID := anyTx.GetTxId()
	if tp.HasTransaction(txID) {
		return false, nil
	}
	if tp.validator == nil {
		return false, fmt.Errorf("transaction validator unavailable")
	}
	if err := tp.validator.CheckAnyTx(anyTx); err != nil {
		return false, err
	}
	if tp.isTxApplied(txID) {
		return false, tp.CacheAnyTx(anyTx)
	}
	state, err := tp.readAdmissionState(anyTx.GetBase().FromAddress)
	if err != nil {
		return false, err
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.isExecutionGuardExpired(anyTx) {
		return false, ErrExecutionGuardExpired
	}
	if _, exists := tp.pendingAnyTxCache.Peek(txID); exists {
		return false, nil
	}
	if tp.isTxApplied(txID) {
		return false, nil
	}
	entry, err := tp.admitPendingLocked(anyTx, state, time.Now())
	if err != nil {
		return false, err
	}
	tp.cacheAnyTxUnlocked(txID, anyTx, true)
	tp.trackAdmissionLocked(txID, entry)
	return true, nil
}

func (tp *TxPool) storeAnyTx(anyTx *pb.AnyTx) (bool, error) {
	added, err := tp.insertPending(anyTx)
	if err != nil || !added {
		return added, err
	}
	if err := tp.enqueuePendingSave(anyTx); err != nil {
		return false, err
	}
	return true, nil
}

func (tp *TxPool) enqueuePendingSave(anyTx *pb.AnyTx) error {
	if anyTx == nil {
		return fmt.Errorf("nil transaction")
	}
	select {
	case tp.pendingSaveQueue <- anyTx:
		return nil
	case <-tp.stopChan:
		return fmt.Errorf("txpool is stopping")
	}
}

func (tp *TxPool) runPendingSaveWorker(workerID int) {
	defer tp.wg.Done()
	for {
		select {
		case <-tp.stopChan:
			                                                                     
			for {
				select {
				case anyTx := <-tp.pendingSaveQueue:
					tp.persistPendingAnyTx(anyTx, workerID)
				default:
					return
				}
			}
		case anyTx := <-tp.pendingSaveQueue:
			tp.persistPendingAnyTx(anyTx, workerID)
		}
	}
}

func (tp *TxPool) persistPendingAnyTx(anyTx *pb.AnyTx, workerID int) {
	if anyTx == nil {
		return
	}
	txID := anyTx.GetTxId()
	if err := tp.savePendingAnyTxIfPresent(anyTx); err != nil {
		tp.Logger.Debug("[TxPool] pending save worker=%d failed for tx %s: %v", workerID, txID, err)
	}
}

                                                                         
                                                                           
                                                                              
                                                 
func (tp *TxPool) savePendingAnyTxIfPresent(anyTx *pb.AnyTx) error {
	if tp == nil || anyTx == nil {
		return nil
	}
	txID := anyTx.GetTxId()
	if txID == "" {
		return fmt.Errorf("empty txID")
	}
	if tp.isExecutionGuardExpired(anyTx) {
		return nil
	}
	pLock := tp.pendingPersistenceLock(txID)
	pLock.Lock()
	defer pLock.Unlock()

	tp.mu.RLock()
	if tp.isExecutionGuardExpired(anyTx) {
		tp.mu.RUnlock()
		return nil
	}
	if _, exists := tp.pendingAnyTxCache.Peek(txID); !exists {
		tp.mu.RUnlock()
		return nil
	}
	tp.mu.RUnlock()
	return tp.dbManager.SavePendingAnyTx(anyTx)
}

                                                            
func (p *TxPool) loadFromDB() {
	pending, err := p.dbManager.LoadPendingAnyTx()
	if err != nil {
		logs.Verbose("[TxPool] Failed to load pending: %v", err)
		return
	}
	                                                                                                  
	sort.SliceStable(pending, func(i, j int) bool {
		a, b := pending[i].GetBase(), pending[j].GetBase()
		if a == nil || b == nil {
			return a == nil && b != nil
		}
		if a.FromAddress != b.FromAddress {
			return a.FromAddress < b.FromAddress
		}
		if a.Nonce != b.Nonce {
			return a.Nonce < b.Nonce
		}
		return a.TxId < b.TxId
	})
	for _, tx := range pending {
		added, err := p.insertPending(tx)
		if err != nil || !added {
			_ = p.dbManager.DeletePendingAnyTx(tx.GetTxId())
		}
	}
}

func (p *TxPool) StoreAnyTx(anyTx *pb.AnyTx) error {
	added, err := p.insertPending(anyTx)
	if err != nil || !added {
		return err
	}
	return p.savePendingAnyTxIfPresent(anyTx)
}

func (p *TxPool) RemoveAnyTx(txID string) {
	if p == nil || txID == "" {
		return
	}
	pLock := p.pendingPersistenceLock(txID)
	pLock.Lock()
	defer pLock.Unlock()

	p.mu.Lock()
	p.removePendingLocked(txID)
	p.mu.Unlock()
	                     
	_ = p.dbManager.DeletePendingAnyTx(txID)
}

                                                                        
                                                                           
                                                                         
func (p *TxPool) RemoveExpiredExecutionGuardTxs(finalizedHeight uint64) int {
	if p == nil {
		return 0
	}
	p.recordFinalizedHeight(finalizedHeight)
	expired := make([]string, 0)
	p.mu.RLock()
	for targetHeight, txIDs := range p.executionGuardByHeight {
		if targetHeight > finalizedHeight {
			continue
		}
		for txID := range txIDs {
			expired = append(expired, txID)
		}
	}
	p.mu.RUnlock()
	sort.Strings(expired)
	for _, txID := range expired {
		pLock := p.pendingPersistenceLock(txID)
		pLock.Lock()
		p.mu.Lock()
		targetHeight, tracked := p.executionGuardByTxID[txID]
		if !tracked || targetHeight > finalizedHeight {
			p.mu.Unlock()
			pLock.Unlock()
			continue
		}
		p.removePendingLocked(txID)
		p.mu.Unlock()
		_ = p.dbManager.DeletePendingAnyTx(txID)
		pLock.Unlock()
	}
	return len(expired)
}

                                                                            
                                                                               
                                                                         
func (p *TxPool) ReconcileFinalizedHeight(height uint64) int {
	if p == nil {
		return 0
	}
	p.finalizedHeight.Store(height)
	return p.RemoveExpiredExecutionGuardTxs(height)
}

func (p *TxPool) recordFinalizedHeight(height uint64) {
	if p == nil {
		return
	}
	for {
		current := p.finalizedHeight.Load()
		if height <= current || p.finalizedHeight.CompareAndSwap(current, height) {
			return
		}
	}
}

func (p *TxPool) isExecutionGuardExpired(tx *pb.AnyTx) bool {
	if p == nil || tx == nil || !pb.TxRequiresAccountNonce(tx) {
		return false
	}
	base := tx.GetBase()
	if base == nil || base.TargetHeight == 0 {
		return false
	}
	return base.TargetHeight <= p.finalizedHeight.Load()
}

                                                                              
func (p *TxPool) trackExecutionGuardUnlocked(txID string, tx *pb.AnyTx) {
	if p == nil || txID == "" || tx == nil || !pb.TxRequiresAccountNonce(tx) {
		return
	}
	base := tx.GetBase()
	if base == nil || base.TargetHeight == 0 {
		return
	}
	if p.executionGuardByHeight == nil {
		p.executionGuardByHeight = make(map[uint64]map[string]struct{})
	}
	if p.executionGuardByTxID == nil {
		p.executionGuardByTxID = make(map[string]uint64)
	}
	if previous, exists := p.executionGuardByTxID[txID]; exists {
		if previous == base.TargetHeight {
			return
		}
		if ids := p.executionGuardByHeight[previous]; ids != nil {
			delete(ids, txID)
			if len(ids) == 0 {
				delete(p.executionGuardByHeight, previous)
			}
		}
	}
	ids := p.executionGuardByHeight[base.TargetHeight]
	if ids == nil {
		ids = make(map[string]struct{})
		p.executionGuardByHeight[base.TargetHeight] = ids
	}
	ids[txID] = struct{}{}
	p.executionGuardByTxID[txID] = base.TargetHeight
}

func (p *TxPool) untrackExecutionGuardUnlocked(txID string) {
	if p == nil || txID == "" {
		return
	}
	targetHeight, exists := p.executionGuardByTxID[txID]
	if !exists {
		return
	}
	delete(p.executionGuardByTxID, txID)
	if ids := p.executionGuardByHeight[targetHeight]; ids != nil {
		delete(ids, txID)
		if len(ids) == 0 {
			delete(p.executionGuardByHeight, targetHeight)
		}
	}
}

func (p *TxPool) RemoveAppliedAnyTxs(txIDs []string) {
	if len(txIDs) == 0 {
		return
	}
	unique := make([]string, 0, len(txIDs))
	seen := make(map[string]struct{}, len(txIDs))
	for _, txID := range txIDs {
		if txID == "" {
			continue
		}
		if _, ok := seen[txID]; ok {
			continue
		}
		seen[txID] = struct{}{}
		unique = append(unique, txID)
		p.markTxAppliedInMemory(txID)
	}
	if len(unique) == 0 {
		return
	}

	for _, txID := range unique {
		pLock := p.pendingPersistenceLock(txID)
		pLock.Lock()
		p.mu.Lock()
		p.removePendingLocked(txID)
		p.mu.Unlock()
		if p.dbManager != nil {
			_ = p.dbManager.DeletePendingAnyTx(txID)
		}
		pLock.Unlock()
	}
}

                               
func (p *TxPool) GetPendingAnyTx() []*pb.AnyTx {

	p.mu.RLock()
	defer p.mu.RUnlock()

	var result []*pb.AnyTx
	                                                
	rawKeys := p.pendingAnyTxCache.Keys()
	var keys []string
	for _, k := range rawKeys {
		if keyStr, ok := k.(string); ok {
			keys = append(keys, keyStr)
		}
	}

	                 
	sort.Strings(keys)

	                                           
	for _, k := range keys {
		if value, exists := p.pendingAnyTxCache.Get(k); exists {
			if anyTx, ok := value.(*pb.AnyTx); ok {
				result = append(result, anyTx)
			}
		}
	}
	return result
}

                                                                                
func (p *TxPool) GetPending65500Tx() []*pb.AnyTx {
	p.mu.RLock()
	defer p.mu.RUnlock()

	cfg := p.cfg
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	var result []*pb.AnyTx
	keys := p.pendingAnyTxCache.Keys()
	maxCount := cfg.TxPool.MaxPendingTxs
	count := 0

	for _, k := range keys {
		if count >= maxCount {
			break
		}
		keyStr, ok := k.(string)
		if !ok {
			continue
		}
		if value, exists := p.pendingAnyTxCache.Get(keyStr); exists {
			if anyTx, ok := value.(*pb.AnyTx); ok {
				result = append(result, anyTx)
				count++
			}
		}
	}
	return result
}

func (p *TxPool) ConcatFirst8Bytes(txs []*pb.AnyTx) []byte {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		logs.Info("[ConcatFirst8Bytes] elapsed=%s", elapsed)
	}()
	var result []byte
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		txID := tx.GetTxId()
		if txID == "" {
			continue
		}
		var idBytes []byte
		                                     
		if len(txID) > 2 && txID[0] == '0' && (txID[1] == 'x' || txID[1] == 'X') {
			if decoded, err := hex.DecodeString(txID[2:]); err == nil && len(decoded) > 0 {
				idBytes = decoded
			}
		}
		                                                                                             
		if len(idBytes) == 0 {
			idBytes = utils.Sha256Hash([]byte(txID))
		}
		                                                                   
		if len(idBytes) < 3 {
			continue
		}
		                   
		endIndex := 8
		if len(idBytes) < endIndex {
			endIndex = len(idBytes)
		}
		                                                    
		result = append(result, idBytes[:endIndex]...)
	}
	return result
}

                                                                                             
  
                              
  
                                                                              
func ExtractAnyTxId(a *pb.AnyTx) string {
	return a.GetTxId()
}

                                                                                           
                                                                                 
                             
func ComputeTxsHash(txs []*pb.AnyTx) string {
	                                          
	accumulatedHash := utils.Sha256Hash([]byte{})

	for _, tx := range txs {
		txID := tx.GetTxId()
		if txID == "" {
			continue
		}
		                                           
		combined := append(accumulatedHash, []byte(txID)...)
		accumulatedHash = utils.Sha256Hash(combined)
	}

	return fmt.Sprintf("%x", accumulatedHash)
}
func (p *TxPool) GetTxsByShortHashes(shortHashes [][]byte, isSync bool) []*pb.AnyTx {
	                     
	                
	                               
	                                                         
	     
	p.mu.RLock()
	defer p.mu.RUnlock()

	                                                                            

	var results []*pb.AnyTx
	seen := make(map[string]struct{}, len(shortHashes))

	for _, shortHash := range shortHashes {
		shortHex := hex.EncodeToString(shortHash)
		                                                   
		var v interface{}
		var exists bool
		if isSync {                                    
			v, exists = p.shortTxCache.Get(shortHex)
		} else {
			v, exists = p.shortPendingAnyTxCache.Get(shortHex)
		}
		if !exists {
			                                                                     
			continue
		}
		txID, ok := v.(string)
		if !ok {
			                                                                        
			continue
		}
		                                                           

		if _, added := seen[txID]; added {
			                                                     
			continue
		}

		txData, found := p.cacheTx.Get(txID)
		if !found {
			                                                        
			continue
		}
		anyTx, ok := txData.(*pb.AnyTx)
		if !ok {
			                                                                                      
			continue
		}

		                                                  
		results = append(results, anyTx)
		seen[txID] = struct{}{}
	}

	                                                                      
	return results
}

                                                                     
                                                                    
                                                                          
func (p *TxPool) AnalyzeProposalTxs(proposalTxs []byte) (missing map[string]bool, existTx map[string]bool) {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		logs.Info("[AnalyzeProposalTxs] elapsed=%s", elapsed)
	}()
	missing = make(map[string]bool)
	existTx = make(map[string]bool)
	const shortHashSize = 8
	for i := 0; i+shortHashSize <= len(proposalTxs); i += shortHashSize {
		shortHash := proposalTxs[i : i+shortHashSize]
		shortHashHex := hex.EncodeToString(shortHash)
		                                           
		matchedTxs := p.GetTxsByShortHashes([][]byte{shortHash}, false)
		if len(matchedTxs) == 0 {
			                                 
			missing[shortHashHex] = true
		} else {
			                                                                                       
			for _, tx := range matchedTxs {
				existTx[tx.GetTxId()] = true
			}
		}
	}
	return missing, existTx
}

                                                   
func (tp *TxPool) GetChannelStats() []stats.ChannelStat {
	if tp.Queue != nil {
		return []stats.ChannelStat{
			stats.NewChannelStat("MsgChan", "TxPool", len(tp.Queue.MsgChan), cap(tp.Queue.MsgChan)),
		}
	}
	return nil
}

                                                  
func (tp *TxPool) PendingLen() int {
	if tp.pendingAnyTxCache == nil {
		return 0
	}
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	return tp.pendingAnyTxCache.Len()
}

func (tp *TxPool) MaxPendingLimit() int {
	if tp == nil || tp.cfg == nil {
		return 0
	}
	limit := tp.cfg.TxPool.MaxPendingTxs
	cacheLimit := tp.cfg.TxPool.PendingTxCacheSize
	if cacheLimit > 0 && (limit <= 0 || cacheLimit < limit) {
		limit = cacheLimit
	}
	return limit
}

func (tp *TxPool) QueueDepth() (int, int) {
	if tp == nil || tp.Queue == nil {
		return 0, 0
	}
	return len(tp.Queue.MsgChan), cap(tp.Queue.MsgChan)
}
