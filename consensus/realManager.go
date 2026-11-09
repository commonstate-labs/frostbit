package consensus

import (
	"context"
	"dex/config"
	"dex/db"
	"dex/interfaces"
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"
	"dex/sender"
	"dex/servicepeer"
	"dex/txpool"
	"dex/types"
	"dex/utils"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

                                                         
type ConsensusNodeManager struct {
	mu             sync.RWMutex
	Node           *Node
	engine         interfaces.ConsensusEngine
	store          interfaces.BlockStore
	Transport      interfaces.Transport
	messageHandler *MessageHandler
	queryManager   *QueryManager
	dbManager      *db.Manager
	senderManager  *sender.SenderManager
	adapter        *ConsensusAdapter
	txPool         *txpool.TxPool
	Logger         logs.Logger
	paused         atomic.Int32                       
	pausePending   atomic.Int32                                                      
	started        atomic.Bool                               
	emergency      *lifecycle.EmergencyStop
}

                                                                       
                                                                              
                                                          
func (m *ConsensusNodeManager) SetServicePeerStore(store *servicepeer.Store) {
	if m == nil {
		return
	}
	m.mu.RLock()
	node := m.Node
	transport := m.Transport
	m.mu.RUnlock()
	if rt, ok := transport.(*RealTransport); ok {
		rt.SetServicePeerStore(store)
	}
	if node != nil && node.SyncManager != nil {
		node.SyncManager.SetServicePeerStore(store)
	}
}

                                                                                      
                                                           
func (m *ConsensusNodeManager) SetEmergencyStop(e *lifecycle.EmergencyStop) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.emergency = e
	m.mu.Unlock()
	if m.Node != nil && m.Node.proposalManager != nil {
		if bp, ok := m.Node.proposalManager.proposer.(*RealBlockProposer); ok {
			bp.SetEmergencyStop(e)
		}
	}
	if m.Node != nil && m.Node.SyncManager != nil {
		m.Node.SyncManager.SetSafetyFaultHandler(func(err error) {
			if err == nil {
				return
			}
			reason := fmt.Sprintf("accepted-frontier safety fault: %v", err)
			e.Stop(reason)
			m.applyPause(true)
			m.Logger.Error("[ConsensusManager] %s", reason)
		})
	}
}

func InitConsensusManager(
	nodeID types.NodeID,
	dbManager *db.Manager,
	config *Config,
	senderMgr *sender.SenderManager,
	txPool *txpool.TxPool,
	logger logs.Logger,
	globalCfg *config.Config,
) (*ConsensusNodeManager, error) {
	return InitConsensusManagerWithSimulation(nodeID, dbManager, config, senderMgr, txPool, logger, 0.0, 0, 0, globalCfg)
}

                                                                                                           
                                                                                   
func InitConsensusManagerWithPacketLoss(
	nodeID types.NodeID,
	dbManager *db.Manager,
	config *Config,
	senderMgr *sender.SenderManager,
	txPool *txpool.TxPool,
	logger logs.Logger,
	packetLossRate float64,
	globalCfg *config.Config,
) (*ConsensusNodeManager, error) {
	return InitConsensusManagerWithSimulation(nodeID, dbManager, config, senderMgr, txPool, logger, packetLossRate, 0, 0, globalCfg)
}

                                                                                                         
                                                                                   
                                                                      
func InitConsensusManagerWithSimulation(
	nodeID types.NodeID,
	dbManager *db.Manager,
	runtimeConfig *Config,
	senderMgr *sender.SenderManager,
	txPool *txpool.TxPool,
	logger logs.Logger,
	packetLossRate float64,
	minLatency, maxLatency time.Duration,
	globalCfg *config.Config,
) (*ConsensusNodeManager, error) {
	if runtimeConfig == nil {
		runtimeConfig = DefaultConfig()
	}
	storeWindowConfig := DefaultConfig().Window
	if globalCfg != nil {
		storeWindowConfig = globalCfg.Window
	}
	if err := validateRuntimeWindowCompatibility(runtimeConfig.Window, storeWindowConfig); err != nil {
		return nil, err
	}

	                                     
	transport := NewRealTransportWithSimulation(nodeID, dbManager, senderMgr, context.Background(), packetLossRate, minLatency, maxLatency)

	                                                      
	realStore := NewRealBlockStore(nodeID, dbManager, txPool, globalCfg)
	if rs, ok := realStore.(*RealBlockStore); ok {
		if err := rs.RecoveryError(); err != nil {
			return nil, fmt.Errorf("recover finalized block store: %w", err)
		}
	}
	if txPool != nil {
		_, recoveredHeight := realStore.GetLastAccepted()
		txPool.ReconcileFinalizedHeight(recoveredHeight)
	}
	node, err := NewNode(nodeID, transport, realStore, false, runtimeConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize consensus node: %w", err)
	}

	                                  
	if rs, ok := realStore.(*RealBlockStore); ok {
		rs.SetEventBus(node.events)
	}

	                                         
	node.engine = NewSnowmanEngine(nodeID, realStore, &runtimeConfig.Consensus, node.events, logger)
	node.messageHandler.engine = node.engine
	node.queryManager.engine = node.engine

	                                                       
	proposer := NewRealBlockProposerWithConfig(dbManager, txPool, nil, "", globalCfg)
	node.proposalManager.SetProposer(proposer)

	                                                                           
	adapter := NewConsensusAdapterWithTxPool(dbManager, txPool)

	                                                                        
	pendingBlockBuffer := NewPendingBlockBuffer(txPool, adapter, senderMgr, logger)
	pendingBlockBuffer.Start()
	node.messageHandler.SetPendingBlockBuffer(pendingBlockBuffer)

	                              
	consensusManager := &ConsensusNodeManager{
		Node:           node,
		engine:         node.engine,
		store:          node.store,
		Transport:      transport,
		messageHandler: node.messageHandler,
		queryManager:   node.queryManager,
		dbManager:      dbManager,
		senderManager:  senderMgr,
		txPool:         txPool,
		adapter:        adapter,
		Logger:         logger,
	}

	if packetLossRate > 0 || maxLatency > 0 {
		logger.Info("[ConsensusManager] Initialized with NodeID %s, PacketLossRate=%.2f%%, Latency=%v~%v",
			nodeID, packetLossRate*100, minLatency, maxLatency)
	} else {
		logger.Info("[ConsensusManager] Initialized with NodeID %s", nodeID)
	}

	                                                                                                              
	                                                                                              
	node.events.Subscribe(types.EventBlockFinalized, func(interfaces.Event) {
		if consensusManager.pausePending.Load() == 1 {
			logs.Info("[ConsensusManager] In-flight block finalized, applying deferred pause")
			consensusManager.applyPause(true)
		}
	})

	return consensusManager, nil
}

                                                             
func (m *ConsensusNodeManager) SetLivenessTracker(lt interfaces.LivenessTracker) {
	if m.Node != nil && m.Node.consensusParams != nil {
		m.Node.consensusParams.SetLivenessTracker(lt)
	}
	if m.Node != nil && m.Node.proposalManager != nil {
		if rp, ok := m.Node.proposalManager.proposer.(*RealBlockProposer); ok {
			rp.SetLivenessTracker(lt)
		}
	}
	if m.queryManager != nil {
		m.queryManager.SetLivenessTracker(lt)
	}
	if m.messageHandler != nil {
		m.messageHandler.SetLivenessTracker(lt)
	}
	if m.Node != nil && m.Node.SyncManager != nil {
		m.Node.SyncManager.SetLivenessTracker(lt)
	}
}

                                                                                     
func (m *ConsensusNodeManager) SetLivenessReporter(reporter interfaces.LivenessLocalReporter) {
	if m.queryManager != nil {
		m.queryManager.SetLivenessReporter(reporter)
	}
	if m.Node != nil && m.Node.proposalManager != nil {
		if rp, ok := m.Node.proposalManager.proposer.(*RealBlockProposer); ok {
			rp.SetLivenessReporter(reporter)
		}
	}
}

                                                                             
                                                               
func (m *ConsensusNodeManager) SetPrivateTxProvider(provider interfaces.PrivateTxProvider) {
	if m == nil || m.Node == nil || m.Node.proposalManager == nil {
		return
	}
	if rp, ok := m.Node.proposalManager.proposer.(*RealBlockProposer); ok {
		rp.SetPrivateTxProvider(provider)
	}
}

                                                                              
                                                                      
func (m *ConsensusNodeManager) SetProposalPreflight(preflight interfaces.ProposalPreflight) {
	if m == nil || m.Node == nil || m.Node.proposalManager == nil {
		return
	}
	if rp, ok := m.Node.proposalManager.proposer.(*RealBlockProposer); ok {
		rp.SetProposalPreflight(preflight)
	}
}

func (m *ConsensusNodeManager) SetLocalIdentity(keyMgr *utils.KeyManager, address string) {
	if m == nil {
		return
	}
	if keyMgr != nil && address != "" {
		RegisterSignerPublicKey(types.NodeID(address), keyMgr)
	}
	if m.Node != nil && m.Node.messageHandler != nil {
		m.Node.messageHandler.signer = keyMgr
	}
	if m.Node != nil && m.Node.proposalManager != nil {
		if rp, ok := m.Node.proposalManager.proposer.(*RealBlockProposer); ok {
			rp.ConfigureLocalKey(keyMgr, address)
		}
	}
	if rt, ok := m.Transport.(*RealTransport); ok {
		rt.SetAddress(address)
		rt.SetSigner(keyMgr)
	}
}

func (m *ConsensusNodeManager) Start() {
	if m.Node != nil {
		m.Node.Start()
		m.started.Store(true)
		m.Logger.Info("[ConsensusManager] Started consensus engine")
	}
}
func (m *ConsensusNodeManager) GetActiveQueryCount() int {
	if m.engine != nil {
		return m.engine.GetActiveQueryCount()
	}
	return 0
}

                           
func (m *ConsensusNodeManager) AddBlock(block *pb.Block) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if block == nil || block.Header == nil {
		return fmt.Errorf("invalid block")
	}
	if m.Node == nil || m.Node.proposalManager == nil {
		return fmt.Errorf("live block admission unavailable: proposal manager is not configured")
	}

	_, acceptedHeight := m.store.GetLastAccepted()
	SetBlockCacheAcceptedHeight(acceptedHeight)
	if IsTooFarFutureHeight(block.Header.Height, acceptedHeight) {
		return fmt.Errorf("block height %d too far ahead of accepted %d (window %d)", block.Header.Height, acceptedHeight, MaxFutureBlockCacheWindow)
	}

	                       
	typesBlock, err := m.adapter.DBBlockToConsensusWithPayload(block)
	if err != nil {
		return err
	}

	                                                                                 
	                                                                          
	added, deferred, err := m.Node.proposalManager.admitLiveBlock(typesBlock)
	if err != nil {
		return err
	}
	if deferred {
		logs.Debug("[ConsensusManager] Deferred live block %s at height %d window %d",
			block.BlockHash, block.Header.Height, block.Header.Window)
		return nil
	}

	if added {
		                        
		m.Node.events.PublishAsync(types.BaseEvent{
			EventType: types.EventNewBlock,
			EventData: typesBlock,
		})

		logs.Debug("[ConsensusManager] Added new block %s at height %d",
			block.BlockHash, block.Header.Height)
	}

	return nil
}

                                                  
func (m *ConsensusNodeManager) GetPreference(height uint64) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.engine.GetPreference(height)
}

                                              
func (m *ConsensusNodeManager) GetLastAccepted() (string, uint64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store.GetLastAccepted()
}

func (m *ConsensusNodeManager) GetFinalizedBlockID(height uint64) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.store == nil {
		return "", false
	}
	if block, ok := m.store.GetFinalizedAtHeight(height); ok && block != nil {
		return block.ID, true
	}
	return "", false
}

                                                                           
                                                                                
func (m *ConsensusNodeManager) SetVerifiedSyncObserver(observer func(VerifiedSyncPoint) error) {
	if m == nil || m.Node == nil || m.Node.SyncManager == nil {
		return
	}
	m.Node.SyncManager.SetVerifiedSyncObserver(observer)
}

                                       
func (m *ConsensusNodeManager) HasBlock(blockId string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.store.Get(blockId)
	return exists
}

                          
func (m *ConsensusNodeManager) StartQuery() {
	m.queryManager.tryIssueQuery()
}

                           
func (m *ConsensusNodeManager) GetStats() *NodeStats {
	return m.Node.Stats
}

                                      
func (m *ConsensusNodeManager) GetCurrentHeight() uint64 {
	return m.store.GetCurrentHeight()
}

                                                 
func (m *ConsensusNodeManager) GetPendingBlocksCount() int {
	return m.store.GetPendingBlocksCount()
}

                                            
func (m *ConsensusNodeManager) GetPendingBlocks() []*types.Block {
	return m.store.GetPendingBlocks()
}

                                                      
func (m *ConsensusNodeManager) GetEventBus() interfaces.EventBus {
	if m.Node != nil {
		return m.Node.events
	}
	return nil
}

                                   
func (m *ConsensusNodeManager) GetBlockStore() interfaces.BlockStore {
	return m.store
}

func (m *ConsensusNodeManager) GetLastBlockTiming() (BlockTimingSnapshot, bool) {
	if m == nil {
		return BlockTimingSnapshot{}, false
	}
	if realStore, ok := m.store.(*RealBlockStore); ok {
		return realStore.GetLastBlockTiming()
	}
	return BlockTimingSnapshot{}, false
}

                                                                  
func (m *ConsensusNodeManager) GetFinalizationChits(height uint64) *types.FinalizationChits {
	if realStore, ok := m.store.(*RealBlockStore); ok {
		chits, exists := realStore.GetFinalizationChits(height)
		if exists {
			return chits
		}
	}
	return nil
}

                                         
func (m *ConsensusNodeManager) IsReady() bool {
	                              
	if m.Node == nil || m.engine == nil {
		return false
	}

	                                       
	peers := m.Transport.SamplePeers(m.Node.ID, 1)
	if len(peers) == 0 {
		return false
	}

	return true
}

                                           
func (m *ConsensusNodeManager) ResetProposalTimer() {
	if m.Node != nil {
		m.Node.ResetProposalTimer()
	}
}

                        
func (m *ConsensusNodeManager) Stop() {
	if m.Node != nil {
		m.Node.cancel()                      
		m.Node.Stop()
	}
	logs.Info("[ConsensusManager] Stopped")
}

                                                                                                                 
                                                                                                                              
                                                                                          
func (m *ConsensusNodeManager) SetPaused(paused bool) {
	if !paused {
		m.pausePending.Store(0)
		m.applyPause(false)
		return
	}
	if m.paused.Load() == 1 {
		return
	}
	if se, ok := m.engine.(*SnowmanEngine); ok && len(se.GetPendingHeightsState()) > 0 {
		if m.pausePending.CompareAndSwap(0, 1) {
			m.Logger.Info("[ConsensusManager] Pause deferred: waiting for in-flight block to finalize")
			go func() {
				time.Sleep(10 * time.Second)
				if m.pausePending.Load() == 1 {
					m.Logger.Warn("[ConsensusManager] Deferred pause timeout, forcing pause")
					m.applyPause(true)
				}
			}()
		}
		return
	}
	m.applyPause(true)
}

                                                                                                          
func (m *ConsensusNodeManager) applyPause(paused bool) {
	var v int32
	if paused {
		v = 1
	}
	m.pausePending.Store(0)
	m.paused.Store(v)
	if m.Node != nil {
		m.Node.paused.Store(v)
	}
	                                                         
	if rt, ok := m.Transport.(*RealTransport); ok {
		rt.paused.Store(v)
	}
	                                                                        
	if !paused && m.Node != nil && !m.started.Load() {
		m.Logger.Info("[ConsensusManager] Node was never started, performing full Start() on resume")
		m.Node.Start()
		m.started.Store(true)
		                                                                
	}
	                                 
	if !paused && m.Node != nil && m.Node.SyncManager != nil {
		go func() {
			sm := m.Node.SyncManager
			for i := 0; i < 5; i++ {
				sm.pollPeerHeights()
				time.Sleep(2 * time.Second)
				sm.tickCheckAndSync()
				sm.Mu.RLock()
				syncing := sm.Syncing
				sm.Mu.RUnlock()
				if syncing {
					break
				}
				time.Sleep(1 * time.Second)
			}
		}()
	}
}

                                                                                                           
func (m *ConsensusNodeManager) IsPaused() bool {
	if m.paused.Load() == 1 {
		return true
	}
	return m.emergency.IsStopped()
}

                                                                               
func (m *ConsensusNodeManager) IsEmergencyStopped() bool { return m.emergency.IsStopped() }

                                                                           
func (m *ConsensusNodeManager) EmergencyStop() *lifecycle.EmergencyStop { return m.emergency }

                                                                 
func (m *ConsensusNodeManager) GetPendingHeightsState() []*HeightState {
	if se, ok := m.engine.(*SnowmanEngine); ok {
		return se.GetPendingHeightsState()
	}
	return nil
}

                                                       
func (m *ConsensusNodeManager) GetPendingBlockBuffer() *PendingBlockBuffer {
	if m.messageHandler != nil {
		return m.messageHandler.pendingBlockBuffer
	}
	return nil
}

                          
type SyncStatus struct {
	IsSyncing               bool                              
	SyncTargetHeight        uint64                                                          
	VerifiedSyncCompletions uint64                                                              
	VerifiedSyncHeight      uint64                                                                             
}

                                                                                 
func (m *ConsensusNodeManager) IsSyncing() bool {
	if m == nil || m.Node == nil || m.Node.SyncManager == nil || m.Node.SyncManager.store == nil {
		return false
	}
	sm := m.Node.SyncManager
	localID, localHeight := sm.store.GetLastAccepted()
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()
	return sm.shouldPauseConsensusLocked(localID, localHeight)
}

                                         
func (m *ConsensusNodeManager) GetSyncStatus() SyncStatus {
	if m == nil || m.Node == nil || m.Node.SyncManager == nil || m.Node.SyncManager.store == nil {
		return SyncStatus{}
	}

	sm := m.Node.SyncManager
	localID, localHeight := sm.store.GetLastAccepted()
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()

	                                       
	var maxPeerHeight uint64
	for _, h := range sm.PeerHeights {
		if h > maxPeerHeight {
			maxPeerHeight = h
		}
	}

	return SyncStatus{
		IsSyncing:               sm.shouldPauseConsensusLocked(localID, localHeight),
		SyncTargetHeight:        maxPeerHeight,
		VerifiedSyncCompletions: sm.verifiedSyncCompletions.Load(),
		VerifiedSyncHeight:      sm.verifiedSyncHeight.Load(),
	}
}

                                                                                                            
func (m *ConsensusNodeManager) SyncPressureState() interfaces.SyncPressureState {
	if m == nil || m.Node == nil || m.Node.SyncManager == nil {
		return interfaces.SyncPressureState{}
	}

	sm := m.Node.SyncManager
	if sm.store == nil {
		return interfaces.SyncPressureState{}
	}
	localID, localHeight := sm.store.GetLastAccepted()
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()
	targetHeight := localHeight
	for _, h := range sm.PeerHeights {
		if h > targetHeight {
			targetHeight = h
		}
	}
	lag := uint64(0)
	if targetHeight > localHeight {
		lag = targetHeight - localHeight
	}

	pausing := sm.shouldPauseConsensusLocked(localID, localHeight)
	state := interfaces.SyncPressureState{
		Syncing:      pausing,
		LocalHeight:  localHeight,
		TargetHeight: targetHeight,
		Lag:          lag,
	}
	if pausing {
		state.CatchUp = true
		state.Reason = "syncing"
	}
	return state
}
