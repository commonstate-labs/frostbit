package consensus

import (
	"context"
	"dex/config"
	"dex/interfaces"
	"dex/logs"
	"dex/types"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrLiveProposalCacheCapacity = errors.New("live proposal cache capacity unavailable")

const (
	postFinalizationProposalDelay = 500 * time.Millisecond
	blockedProposalRetryDelay     = 100 * time.Millisecond
	defaultMaxCachedProposals     = 128
	defaultMaxCachedProposalBytes = 128 * 1024 * 1024
	proposalCacheFixedOverhead    = 512
)

type proposalCacheSlot struct {
	height   uint64
	proposer string
	window   int
}

                                                                          
                                                                           
                                                                             
                                                         
type DeferredLiveBlockValidator interface {
	PrevalidateDeferredLiveBlock(block *types.Block, acceptedHeight uint64) error
}

type ProposalManager struct {
	nodeID                 types.NodeID
	node                   *Node
	transport              interfaces.Transport
	store                  interfaces.BlockStore
	deferredValidator      DeferredLiveBlockValidator
	nodeConfig             *NodeConfig                                 
	windowConfig           config.WindowConfig                
	events                 interfaces.EventBus
	Logger                 logs.Logger
	proposedHeights        map[uint64]string
	proposalsInFlight      map[uint64]bool
	proposalWindow         int                                                            
	lastBlockTime          time.Time                                   
	cachedProposals        map[int][]*types.Block                                     
	cachedProposalIDs      map[string]struct{}                                                          
	cachedProposalSlots    map[proposalCacheSlot]string
	cachedProposalSizes    map[string]uint64
	cachedProposalBytes    uint64
	maxCachedProposals     int
	maxCachedProposalBytes uint64
	mu                     sync.Mutex
	proposer               interfaces.BlockProposer                            
	finalizedSignal        chan struct{}
	finalizedHeight        uint64
	hasFinalized           bool
	proposalDelay          time.Duration
	proposalNotBefore      time.Time
	startOnce              sync.Once
}

                                                                            
func NewProposalManager(id types.NodeID, transport interfaces.Transport, store interfaces.BlockStore, nodeConfig *NodeConfig, windowConfig config.WindowConfig, events interfaces.EventBus, logger logs.Logger) (*ProposalManager, error) {
	return NewProposalManagerWithProposer(id, transport, store, nodeConfig, events, NewDefaultBlockProposer(), windowConfig, logger)
}

                                                               
func NewProposalManagerWithProposer(nodeID types.NodeID, transport interfaces.Transport, store interfaces.BlockStore, nodeConfig *NodeConfig, events interfaces.EventBus, proposer interfaces.BlockProposer, windowConfig config.WindowConfig, logger logs.Logger) (*ProposalManager, error) {
	if store == nil {
		return nil, fmt.Errorf("proposal manager requires a block store")
	}
	deferredValidator, ok := store.(DeferredLiveBlockValidator)
	if !ok || deferredValidator == nil {
		return nil, fmt.Errorf("proposal manager block store %T does not implement DeferredLiveBlockValidator", store)
	}
	if err := validateWindowConfiguration(windowConfig); err != nil {
		return nil, fmt.Errorf("invalid proposal window configuration: %w", err)
	}
	windowConfig.Stages = append([]config.WindowStage(nil), windowConfig.Stages...)
	finalizedID, finalizedHeight := store.GetLastAccepted()
	return &ProposalManager{
		nodeID:                 nodeID,
		transport:              transport,
		store:                  store,
		deferredValidator:      deferredValidator,
		nodeConfig:             nodeConfig,                         
		windowConfig:           windowConfig,
		events:                 events,
		Logger:                 logger,
		proposedHeights:        make(map[uint64]string),
		proposalsInFlight:      make(map[uint64]bool),
		proposer:               proposer,
		lastBlockTime:          time.Now(),                            
		cachedProposals:        make(map[int][]*types.Block),
		cachedProposalIDs:      make(map[string]struct{}),
		cachedProposalSlots:    make(map[proposalCacheSlot]string),
		cachedProposalSizes:    make(map[string]uint64),
		maxCachedProposals:     defaultMaxCachedProposals,
		maxCachedProposalBytes: defaultMaxCachedProposalBytes,
		finalizedSignal:        make(chan struct{}, 1),
		finalizedHeight:        finalizedHeight,
		hasFinalized:           finalizedID != "",
		proposalDelay:          postFinalizationProposalDelay,
	}, nil
}

func (pm *ProposalManager) Start(ctx context.Context) {
	pm.startOnce.Do(func() {
		pm.start(ctx)
	})
}

func (pm *ProposalManager) start(ctx context.Context) {
	                              
	pm.events.Subscribe(types.EventBlockFinalized, pm.handleBlockFinalized)

	go func() {
		cleanupLogger := logs.SetThreadNodeContext(string(pm.nodeID))
		defer cleanupLogger()
		ticker := time.NewTicker(pm.nodeConfig.ProposalInterval)
		defer ticker.Stop()
		var finalizedTimer *time.Timer
		var finalizedTimerC <-chan time.Time
		armFinalizedTimer := func(delay time.Duration) {
			if delay < 0 {
				delay = 0
			}
			if finalizedTimer == nil {
				finalizedTimer = time.NewTimer(delay)
			} else {
				if !finalizedTimer.Stop() {
					select {
					case <-finalizedTimer.C:
					default:
					}
				}
				finalizedTimer.Reset(delay)
			}
			finalizedTimerC = finalizedTimer.C
		}
		defer func() {
			if finalizedTimer != nil {
				finalizedTimer.Stop()
			}
		}()

		for {
			select {
			case <-ticker.C:
				pm.proposeBlock()
			case <-pm.finalizedSignal:
				                                                              
				                                            
				ticker.Reset(pm.nodeConfig.ProposalInterval)
				pm.mu.Lock()
				delay := time.Until(pm.proposalNotBefore)
				pm.mu.Unlock()
				armFinalizedTimer(delay)
			case <-finalizedTimerC:
				finalizedTimerC = nil
				if pm.proposeBlock() {
					armFinalizedTimer(blockedProposalRetryDelay)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

                                                                         
func (pm *ProposalManager) recordFinalizedHeight(height uint64, observedAt time.Time) (time.Duration, bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.hasFinalized && height <= pm.finalizedHeight {
		return 0, false
	}

	delay := pm.proposalDelay
	if delay < 0 {
		delay = 0
	}
	pm.hasFinalized = true
	pm.finalizedHeight = height
	pm.lastBlockTime = observedAt
	pm.proposalNotBefore = observedAt.Add(delay)
	for proposedHeight := range pm.proposedHeights {
		if proposedHeight <= height {
			delete(pm.proposedHeights, proposedHeight)
		}
	}
	for proposedHeight := range pm.proposalsInFlight {
		if proposedHeight <= height {
			delete(pm.proposalsInFlight, proposedHeight)
		}
	}
	return delay, true
}

                                                                          
                                                                                
                                                                              
                                                                        
                 
func (pm *ProposalManager) candidateForParent(height uint64, parentID string) (*types.Block, bool) {
	for _, block := range pm.store.GetByHeight(height) {
		if block == nil ||
			block.Header.Height != height ||
			block.Header.ParentID != parentID {
			continue
		}
		return block, true
	}
	return nil, false
}

func (pm *ProposalManager) beginProposal(height uint64) bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.proposedHeights[height] != "" || pm.proposalsInFlight[height] {
		return false
	}
	pm.proposalsInFlight[height] = true
	return true
}

func (pm *ProposalManager) finishProposal(height uint64, blockID string, added bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.proposalsInFlight, height)
	if added && (!pm.hasFinalized || height > pm.finalizedHeight) {
		pm.proposedHeights[height] = blockID
	}
}

func (pm *ProposalManager) signalFinalizedHeight() {
	                                                                         
	                                                                            
	                                                                            
	                                                                         
	select {
	case pm.finalizedSignal <- struct{}{}:
	default:
	}
}

                                                   
func (pm *ProposalManager) handleBlockFinalized(event interfaces.Event) {
	var block *types.Block
	switch data := event.Data().(type) {
	case *types.BlockFinalizedData:
		if data != nil {
			block = data.Block
		}
	case *types.Block:
		block = data
	}
	if block == nil {
		return
	}

	delay, recorded := pm.recordFinalizedHeight(block.Header.Height, time.Now())
	if !recorded {
		return
	}
	pm.signalFinalizedHeight()
	logs.Debug("[ProposalManager] Block %s finalized, scheduled proposal check after %v",
		block.ID, delay)
}

                                                                        
                                      
func (pm *ProposalManager) proposeBlock() bool {
	if pm.node != nil && pm.node.paused.Load() == 1 {
		return true
	}
	if pm.node != nil && pm.node.SyncManager != nil && pm.node.SyncManager.ShouldPauseConsensusForCatchUp() {
		return true
	}

	lastAcceptedID, lastHeight := pm.store.GetLastAccepted()
	pm.mu.Lock()
	missedFinalization := lastAcceptedID != "" && (!pm.hasFinalized || lastHeight > pm.finalizedHeight)
	pm.mu.Unlock()
	if missedFinalization {
		                                                                   
		                                                                        
		                                                                    
		                                                                      
		if _, recorded := pm.recordFinalizedHeight(lastHeight, time.Now()); recorded {
			pm.signalFinalizedHeight()
			logs.Debug("[ProposalManager] Reconciled finalized height %d from local store", lastHeight)
		}
		return false
	}

	pm.mu.Lock()
	if time.Now().Before(pm.proposalNotBefore) {
		pm.mu.Unlock()
		return false
	}

	                     
	currentWindow := pm.calculateCurrentWindow()
	pm.proposalWindow = currentWindow
	lastBlockTime := pm.lastBlockTime
	proposer := pm.proposer

	pm.mu.Unlock()

	                                                   
	pm.processCachedProposals(currentWindow)

	targetHeight := lastHeight + 1

	                                                                                   
	                                                                        
	parentBlock, exists := pm.store.GetFinalizedAtHeight(lastHeight)
	if !exists {
		                                                   
		logs.Debug("[ProposalManager] Parent block at height %d not finalized yet, skipping proposal", lastHeight)
		return false
	}
	parentID := parentBlock.ID

	                                                                        
	                                                                         
	                                                                          
	                                                                   
	if candidate, exists := pm.candidateForParent(targetHeight, parentID); exists {
		logs.Debug("[ProposalManager] Existing candidate %s suppresses a new local proposal at height=%d window=%d parent=%s",
			candidate.ID, targetHeight, currentWindow, parentID)
		return false
	}

	                                                
	if !proposer.ShouldPropose(pm.nodeID, currentWindow, 0, int(lastHeight), int(targetHeight), lastBlockTime, parentID) {
		if currentWindow >= 2 {
			since := time.Since(lastBlockTime)
			if since > 10*time.Second {
				logs.Warn("[ProposalManager] No proposal at node=%s height=%d window=%d currentBlocks=%d sinceLastFinalized=%v parent=%s",
					pm.nodeID, targetHeight, currentWindow, 0, since.Round(time.Second), parentID)
			}
		}
		return false
	}

	                                                                     
	                                                                    
	                           
	if !pm.beginProposal(targetHeight) {
		return false
	}
	attemptFinished := false
	defer func() {
		if !attemptFinished {
			pm.finishProposal(targetHeight, "", false)
		}
	}()

	block, err := proposer.ProposeBlock(parentID, targetHeight, pm.nodeID, currentWindow)
	if err != nil {
		Logf("[Node %s] Failed to propose block: %v\n", pm.nodeID, err)
		return false
	}
	if block == nil {
		logs.Debug("[ProposalManager] no block proposed (pending txs=0?) at height=%d window=%d",
			targetHeight, currentWindow)
		return false
	}
	if acceptedID, acceptedHeight := pm.store.GetLastAccepted(); acceptedHeight != lastHeight || acceptedID != parentID {
		if acceptedID != "" {
			if _, recorded := pm.recordFinalizedHeight(acceptedHeight, time.Now()); recorded {
				pm.signalFinalizedHeight()
			}
		}
		logs.Debug("[ProposalManager] Discarding stale proposal %s: finalized tip advanced from %s/%d to %s/%d",
			block.ID, parentID, lastHeight, acceptedID, acceptedHeight)
		return false
	}

	                                                                    
	                                                                         
	                                            
	if candidate, exists := pm.candidateForParent(targetHeight, parentID); exists {
		logs.Debug("[ProposalManager] Discarding local proposal %s because candidate %s arrived during build at height=%d parent=%s",
			block.ID, candidate.ID, targetHeight, parentID)
		if candidate.ID != block.ID {
			RemoveCachedBlock(block.ID)
		}
		return false
	}

	isNew, err := pm.store.Add(block)
	if err != nil || !isNew {
		return false
	}
	pm.finishProposal(targetHeight, block.ID, true)
	attemptFinished = true

	if acceptedID, acceptedHeight := pm.store.GetLastAccepted(); acceptedHeight != lastHeight || acceptedID != parentID {
		if acceptedID != "" {
			if _, recorded := pm.recordFinalizedHeight(acceptedHeight, time.Now()); recorded {
				pm.signalFinalizedHeight()
			}
		}
		logs.Debug("[ProposalManager] Suppressing stale proposal %s broadcast: finalized tip is now %s/%d",
			block.ID, acceptedID, acceptedHeight)
		return false
	}

	                                                                          
	                                                                         
	                                                                      
	if first, exists := pm.candidateForParent(targetHeight, parentID); !exists || first.ID != block.ID {
		firstID := ""
		if first != nil {
			firstID = first.ID
		}
		if discarder, ok := pm.store.(candidateDiscarder); ok {
			discarder.DiscardCandidate(targetHeight, block.ID)
		}
		logs.Debug("[ProposalManager] Suppressing local proposal %s broadcast because candidate %s won admission at height=%d parent=%s",
			block.ID, firstID, targetHeight, parentID)
		return false
	}

	if pm.node != nil {
		pm.node.Stats.Mu.Lock()
		pm.node.Stats.BlocksProposed++
		pm.node.Stats.Mu.Unlock()
	}

	Logf("[Node %s] Proposing %s at height %d on parent %s (window %d)\n",
		pm.nodeID, block.ID, block.Header.Height, parentID, currentWindow)

	pm.events.PublishAsync(types.BaseEvent{
		EventType: types.EventNewBlock,
		EventData: block,
	})

	                                                         
	if pm.node != nil && pm.node.queryManager != nil {
		go func() {
			cleanupLogger := logs.SetThreadNodeContext(string(pm.nodeID))
			defer cleanupLogger()
			pm.node.queryManager.tryIssueQuery()
		}()
	}
	return false
}

                                                   
func (pm *ProposalManager) SetProposer(proposer interfaces.BlockProposer) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.proposer = proposer
}

                                                                    
                                       
func (pm *ProposalManager) calculateCurrentWindow() int {
	if !pm.windowConfig.Enabled || len(pm.windowConfig.Stages) == 0 {
		return 0                                                   
	}

	elapsed := time.Since(pm.lastBlockTime)
	var cumulativeDuration time.Duration

	                                                      
	for i, stage := range pm.windowConfig.Stages {
		if stage.Duration == 0 {
			                                             
			return i
		}
		cumulativeDuration += stage.Duration
		if elapsed < cumulativeDuration {
			return i
		}
	}

	                                                      
	return len(pm.windowConfig.Stages) - 1
}

                                                                            
                                                                                
                                                                               
                                
  
                                                                              
                                                                             
                                                                               
                                
func (pm *ProposalManager) admitLiveBlock(block *types.Block) (added bool, deferred bool, err error) {
	if block == nil || block.ID == "" {
		return false, false, fmt.Errorf("invalid live block")
	}
	if err := validateProposalWindow(pm.windowConfig, block.Header.Window); err != nil {
		return false, false, err
	}
	_, acceptedHeight := pm.store.GetLastAccepted()
	SetBlockCacheAcceptedHeight(acceptedHeight)
	if IsTooFarFutureHeight(block.Header.Height, acceptedHeight) {
		return false, false, fmt.Errorf("block height %d too far ahead of accepted %d (window %d)",
			block.Header.Height, acceptedHeight, MaxFutureBlockCacheWindow)
	}
	if block.Header.Height <= acceptedHeight {
		return false, false, nil
	}

	pm.mu.Lock()
	currentWindow := pm.calculateCurrentWindow()
	shouldDefer := block.Header.Height != acceptedHeight+1 || block.Header.Window > currentWindow
	pm.mu.Unlock()
	if !shouldDefer {
		added, err = pm.store.Add(block)
		return added, false, err
	}
	pm.mu.Lock()
	pm.pruneStaleCachedProposalsLocked(acceptedHeight)
	_, alreadyCached := pm.cachedProposalIDs[block.ID]
	if !alreadyCached {
		err = pm.checkProposalCacheLimitsLocked(block, acceptedHeight)
	}
	pm.mu.Unlock()
	if alreadyCached {
		return false, true, nil
	}
	if err != nil {
		return false, false, err
	}

	if err := pm.deferredValidator.PrevalidateDeferredLiveBlock(block, acceptedHeight); err != nil {
		return false, false, err
	}

	                                                                            
	                                                                          
	                                                  
	_, acceptedHeight = pm.store.GetLastAccepted()
	if IsTooFarFutureHeight(block.Header.Height, acceptedHeight) {
		return false, false, fmt.Errorf("block height %d too far ahead of accepted %d (window %d)",
			block.Header.Height, acceptedHeight, MaxFutureBlockCacheWindow)
	}
	if block.Header.Height <= acceptedHeight {
		return false, false, nil
	}
	pm.mu.Lock()
	pm.pruneStaleCachedProposalsLocked(acceptedHeight)
	currentWindow = pm.calculateCurrentWindow()
	shouldDefer = block.Header.Height != acceptedHeight+1 || block.Header.Window > currentWindow
	if shouldDefer {
		err = pm.cacheProposalLocked(block, acceptedHeight)
	}
	pm.mu.Unlock()
	if err != nil {
		return false, false, err
	}
	if shouldDefer {
		                                                                      
		                                                                   
		reason := "future_window"
		if block.Header.Height != acceptedHeight+1 {
			reason = "future_height"
		}
		logs.ConsensusTracef("event=live_block_deferred node=%q block=%q height=%d accepted=%d block_window=%d local_window=%d reason=%s",
			pm.nodeID, block.ID, block.Header.Height, acceptedHeight, block.Header.Window, currentWindow, reason)
		return false, true, nil
	}
	added, err = pm.store.Add(block)
	return added, false, err
}

                                                                                   
func (pm *ProposalManager) processCachedProposals(currentWindow int) {
	_, acceptedHeight := pm.store.GetLastAccepted()
	ready := make([]*types.Block, 0)

	pm.mu.Lock()
	pm.pruneStaleCachedProposalsLocked(acceptedHeight)
	for window, proposals := range pm.cachedProposals {
		remaining := proposals[:0]
		for _, block := range proposals {
			if block == nil || block.ID == "" || block.Header.Height <= acceptedHeight {
				if block != nil {
					pm.removeCachedProposalAccountingLocked(block)
					RemoveCachedBlock(block.ID)
				}
				continue
			}
			if block.Header.Height == acceptedHeight+1 && window <= currentWindow {
				ready = append(ready, block)
				pm.removeCachedProposalAccountingLocked(block)
				continue
			}
			remaining = append(remaining, block)
		}
		if len(remaining) == 0 {
			delete(pm.cachedProposals, window)
		} else {
			pm.cachedProposals[window] = remaining
		}
	}
	pm.mu.Unlock()

	for _, block := range ready {
		isNew, err := pm.store.Add(block)
		if err != nil {
			RemoveCachedBlock(block.ID)
			logs.Debug("[ProposalManager] Cached live proposal %s failed admission at window %d: %v",
				block.ID, block.Header.Window, err)
			continue
		}
		if !isNew {
			continue
		}
		logs.Info("[ProposalManager] Processed cached live proposal %s from window %d", block.ID, block.Header.Window)
		pm.events.PublishAsync(types.BaseEvent{
			EventType: types.EventNewBlock,
			EventData: block,
		})
	}
}

func cloneLiveProposal(block *types.Block) *types.Block {
	if block == nil {
		return nil
	}
	cloned := *block
	cloned.Header = block.Header
	cloned.Header.VRFProof = append([]byte(nil), block.Header.VRFProof...)
	cloned.Header.VRFOutput = append([]byte(nil), block.Header.VRFOutput...)
	cloned.Header.PublicKey = append([]byte(nil), block.Header.PublicKey...)
	cloned.Header.OfflineReports = append([]uint32(nil), block.Header.OfflineReports...)
	cloned.Header.SampledReports = append([]uint32(nil), block.Header.SampledReports...)
	cloned.Header.OnlineReports = append([]uint32(nil), block.Header.OnlineReports...)
	cloned.MinerSignature = append([]byte(nil), block.MinerSignature...)
	cloned.CanonicalPayload = append([]byte(nil), block.CanonicalPayload...)
	cloned.Transactions = append([]string(nil), block.Transactions...)
	cloned.Signatures = make([][]byte, len(block.Signatures))
	for i := range block.Signatures {
		cloned.Signatures[i] = append([]byte(nil), block.Signatures[i]...)
	}
	return &cloned
}

func proposalSlotFor(block *types.Block) proposalCacheSlot {
	return proposalCacheSlot{
		height:   block.Header.Height,
		proposer: block.Header.Proposer,
		window:   block.Header.Window,
	}
}

func addProposalCacheSize(total uint64, size int) uint64 {
	amount := uint64(size)
	if ^uint64(0)-total < amount {
		return ^uint64(0)
	}
	return total + amount
}

func proposalCacheSize(block *types.Block) uint64 {
	if block == nil {
		return 0
	}
	size := uint64(proposalCacheFixedOverhead)
	for _, value := range []string{
		block.ID,
		block.Header.ParentID,
		block.Header.StateRoot,
		block.Header.TxHash,
		block.Header.Proposer,
		block.Header.Miner,
	} {
		size = addProposalCacheSize(size, len(value))
	}
	for _, value := range [][]byte{
		block.Header.VRFProof,
		block.Header.VRFOutput,
		block.Header.PublicKey,
		block.MinerSignature,
		block.CanonicalPayload,
	} {
		size = addProposalCacheSize(size, len(value))
	}
	size = addProposalCacheSize(size, len(block.Header.OfflineReports)*4)
	size = addProposalCacheSize(size, len(block.Header.SampledReports)*4)
	size = addProposalCacheSize(size, len(block.Header.OnlineReports)*4)
	for _, txID := range block.Transactions {
		size = addProposalCacheSize(size, len(txID))
	}
	for _, signature := range block.Signatures {
		size = addProposalCacheSize(size, len(signature))
	}
	return size
}

func (pm *ProposalManager) removeCachedProposalAccountingLocked(block *types.Block) {
	if block == nil {
		return
	}
	delete(pm.cachedProposalIDs, block.ID)
	if size, exists := pm.cachedProposalSizes[block.ID]; exists {
		if size >= pm.cachedProposalBytes {
			pm.cachedProposalBytes = 0
		} else {
			pm.cachedProposalBytes -= size
		}
		delete(pm.cachedProposalSizes, block.ID)
	}
	slot := proposalSlotFor(block)
	if cachedID, exists := pm.cachedProposalSlots[slot]; exists && cachedID == block.ID {
		delete(pm.cachedProposalSlots, slot)
	}
}

func (pm *ProposalManager) pruneStaleCachedProposalsLocked(acceptedHeight uint64) {
	for window, proposals := range pm.cachedProposals {
		remaining := proposals[:0]
		for _, block := range proposals {
			if block == nil || block.ID == "" || block.Header.Height <= acceptedHeight {
				if block != nil {
					pm.removeCachedProposalAccountingLocked(block)
					RemoveCachedBlock(block.ID)
				}
				continue
			}
			remaining = append(remaining, block)
		}
		if len(remaining) == 0 {
			delete(pm.cachedProposals, window)
		} else {
			pm.cachedProposals[window] = remaining
		}
	}
}

func (pm *ProposalManager) cacheProposalLocked(block *types.Block, acceptedHeight uint64) error {
	if block == nil || block.ID == "" {
		return fmt.Errorf("invalid cached live proposal")
	}
	if pm.cachedProposalIDs == nil {
		pm.cachedProposalIDs = make(map[string]struct{})
	}
	if _, exists := pm.cachedProposalIDs[block.ID]; exists {
		return nil
	}
	if pm.cachedProposals == nil {
		pm.cachedProposals = make(map[int][]*types.Block)
	}
	if err := pm.checkProposalCacheLimitsLocked(block, acceptedHeight); err != nil {
		return err
	}
	slot := proposalSlotFor(block)
	size := proposalCacheSize(block)
	cloned := cloneLiveProposal(block)
	pm.cachedProposals[cloned.Header.Window] = append(pm.cachedProposals[cloned.Header.Window], cloned)
	pm.cachedProposalIDs[cloned.ID] = struct{}{}
	pm.cachedProposalSlots[slot] = cloned.ID
	pm.cachedProposalSizes[cloned.ID] = size
	pm.cachedProposalBytes += size
	logs.Debug("[ProposalManager] Cached live proposal %s height=%d window=%d",
		cloned.ID, cloned.Header.Height, cloned.Header.Window)
	return nil
}

func (pm *ProposalManager) farFutureCacheUsageLocked(acceptedHeight uint64) (count int, bytes uint64) {
	nextHeight := acceptedHeight
	if nextHeight < ^uint64(0) {
		nextHeight++
	}
	for _, proposals := range pm.cachedProposals {
		for _, block := range proposals {
			if block == nil || block.Header.Height <= nextHeight {
				continue
			}
			count++
			size, exists := pm.cachedProposalSizes[block.ID]
			if !exists {
				size = proposalCacheSize(block)
			}
			if ^uint64(0)-bytes < size {
				bytes = ^uint64(0)
			} else {
				bytes += size
			}
		}
	}
	return count, bytes
}

func (pm *ProposalManager) checkProposalCacheLimitsLocked(block *types.Block, acceptedHeight uint64) error {
	if block == nil || block.ID == "" {
		return fmt.Errorf("invalid cached live proposal")
	}
	if pm.cachedProposalSlots == nil {
		pm.cachedProposalSlots = make(map[proposalCacheSlot]string)
	}
	if pm.cachedProposalSizes == nil {
		pm.cachedProposalSizes = make(map[string]uint64)
	}
	if pm.maxCachedProposals <= 0 {
		pm.maxCachedProposals = defaultMaxCachedProposals
	}
	if pm.maxCachedProposalBytes == 0 {
		pm.maxCachedProposalBytes = defaultMaxCachedProposalBytes
	}
	if _, exists := pm.cachedProposalIDs[block.ID]; exists {
		return nil
	}
	slot := proposalSlotFor(block)
	if cachedID, exists := pm.cachedProposalSlots[slot]; exists {
		logs.Warn("[ProposalManager] Rejecting duplicate proposer slot height=%d proposer=%s window=%d cached=%s incoming=%s",
			slot.height, slot.proposer, slot.window, cachedID, block.ID)
		return fmt.Errorf("proposal slot already occupied at height %d proposer %s window %d", slot.height, slot.proposer, slot.window)
	}
	if len(pm.cachedProposalIDs) >= pm.maxCachedProposals {
		return fmt.Errorf("%w: count limit reached (%d)", ErrLiveProposalCacheCapacity, pm.maxCachedProposals)
	}
	size := proposalCacheSize(block)
	if size > pm.maxCachedProposalBytes || pm.cachedProposalBytes > pm.maxCachedProposalBytes-size {
		return fmt.Errorf("%w: byte limit reached (%d bytes)", ErrLiveProposalCacheCapacity, pm.maxCachedProposalBytes)
	}
	                                                                             
	                                                                         
	                                                                        
	nextHeight := acceptedHeight
	if nextHeight < ^uint64(0) {
		nextHeight++
	}
	if block.Header.Height > nextHeight {
		farCount, farBytes := pm.farFutureCacheUsageLocked(acceptedHeight)
		farCountLimit := pm.maxCachedProposals - pm.maxCachedProposals/2
		if farCount >= farCountLimit {
			return fmt.Errorf("%w: far-future count limit reached (%d); capacity reserved for height %d",
				ErrLiveProposalCacheCapacity,
				farCountLimit, nextHeight)
		}
		farByteLimit := pm.maxCachedProposalBytes - pm.maxCachedProposalBytes/2
		if size > farByteLimit || farBytes > farByteLimit-size {
			return fmt.Errorf("%w: far-future byte limit reached (%d bytes); capacity reserved for height %d",
				ErrLiveProposalCacheCapacity,
				farByteLimit, nextHeight)
		}
	}
	return nil
}

                                                                                   
func (pm *ProposalManager) UpdateLastBlockTime(t time.Time) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.lastBlockTime = t
	logs.Debug("[ProposalManager] Updated last block time to %v", t)
}

                                                                                  
func (pm *ProposalManager) ResetProposalTimer() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.lastBlockTime = time.Now()
	logs.Info("[ProposalManager] Proposal timer reset to current time")
}
