package consensus

import (
	"context"
	"dex/interfaces"
	"dex/logs"
	"dex/pb"
	"dex/servicepeer"
	"dex/types"
	"dex/utils/peerrep"
	"sync"
	"sync/atomic"
	"time"
)

const serviceSyncProviderResponseTimeout = 40 * time.Second

                                               
                                                      
                                               

type SyncManager struct {
	nodeID             types.NodeID
	node               *Node
	transport          interfaces.Transport
	store              interfaces.BlockStore
	config             *SyncConfig
	events             interfaces.EventBus
	Logger             logs.Logger
	Mu                 sync.RWMutex
	frontierFinalizeMu sync.Mutex
	frontierK          int
	frontierAlpha      int
	consensusParams    *ConsensusParamsProvider
	livenessTracker    interfaces.LivenessTracker
	frontierRound      *frontierRound
	safetyFaultHandler func(error)
	servicePeers       *servicepeer.Store

	                     
	PeerHeights map[types.NodeID]uint64
	lastPoll    time.Time

	               

	                     
	SyncRequests       map[uint32]time.Time
	SyncRequestMeta    map[uint32]syncRequestMeta
	nextSyncID         uint32
	Syncing            bool                                                                     
	InFlightSyncRanges map[string]time.Time
	                                                                          
	                                                                           
	                                                                             
	verifiedSyncCompletions atomic.Uint64
	                                                                        
	                                                                       
	                                                                           
	verifiedSyncHeight atomic.Uint64
	                                                                          
	                                                                             
	                                       
	verifiedSyncObserver func(VerifiedSyncPoint) error

	               
	consecutiveStateRootFaults uint32                                                                     

	                                                                                        
	peerRep *peerrep.Tracker

	              
	chitPending          bool
	chitPendingHeight    uint64
	chitPendingFrom      types.NodeID
	chitPendingFirstSeen time.Time
	chitTimerArmed       bool
	lastChitTriggerAt    time.Time

	               
	activeSession *SyncSession
	lastSession   *SyncSession
}

                                                                           
                                                                            
                                                                            
func (sm *SyncManager) SetLivenessTracker(tracker interfaces.LivenessTracker) {
	if sm == nil {
		return
	}
	sm.Mu.Lock()
	sm.livenessTracker = tracker
	sm.Mu.Unlock()
}

                                                                                
                               
type VerifiedSyncPoint struct {
	Height  uint64
	BlockID string
}

                                                                               
                                                                             
                             
func (sm *SyncManager) SetVerifiedSyncObserver(observer func(VerifiedSyncPoint) error) {
	if sm == nil {
		return
	}
	sm.Mu.Lock()
	sm.verifiedSyncObserver = observer
	sm.Mu.Unlock()
}

func (sm *SyncManager) advanceVerifiedSyncHeight(height uint64) {
	for {
		current := sm.verifiedSyncHeight.Load()
		if height <= current || sm.verifiedSyncHeight.CompareAndSwap(current, height) {
			return
		}
	}
}

                                                                             
                                                                           
                                                                               
                              
func (sm *SyncManager) SetServicePeerStore(store *servicepeer.Store) {
	if sm == nil {
		return
	}
	sm.Mu.Lock()
	sm.servicePeers = store
	sm.Mu.Unlock()
}

func (sm *SyncManager) servicePeerStore() *servicepeer.Store {
	if sm == nil {
		return nil
	}
	sm.Mu.RLock()
	store := sm.servicePeers
	sm.Mu.RUnlock()
	return store
}

func (sm *SyncManager) markServiceProviderSuccess(meta syncRequestMeta, atHeight uint64) {
	if meta.ProviderRole != syncProviderService {
		return
	}
	if store := sm.servicePeerStore(); store != nil {
		                                                                           
		                                                                       
		                                                                      
		                                                                          
		if meta.FromHeight == 0 || store.MarkArchiveBatchSuccess(string(meta.Peer), meta.FromHeight, atHeight) != nil {
			_ = store.MarkDataSuccess(string(meta.Peer), atHeight)
		}
	}
}

func (sm *SyncManager) markServiceProviderFailure(meta syncRequestMeta, atHeight uint64) {
	if meta.ProviderRole != syncProviderService {
		return
	}
	if store := sm.servicePeerStore(); store != nil {
		_ = store.MarkDataFailure(string(meta.Peer), atHeight)
	}
}

                                                                            
                                                                                    
                                                                             
                                                             
func (sm *SyncManager) SetSafetyFaultHandler(handler func(error)) {
	if sm == nil {
		return
	}
	sm.Mu.Lock()
	sm.safetyFaultHandler = handler
	sm.Mu.Unlock()
}

type stateSnapshotFetcher interface {
	FetchStateSnapshotShards(peer types.NodeID, targetHeight uint64) (*types.StateSnapshotShardsResponse, error)
	FetchStateCheckpointFiles(peer types.NodeID, targetHeight uint64) (string, func(), error)
	FetchBlockStateRoot(peer types.NodeID, height uint64) ([]byte, error)
	FetchFinalizedBlock(peer types.NodeID, height uint64) (*pb.Block, error)
	FetchCertChain(peer types.NodeID, fromHeight, toHeight, interval uint64) ([][]byte, error)
}

func NewSyncManager(id types.NodeID, transport interfaces.Transport, store interfaces.BlockStore,
	config *SyncConfig, consensusConfig *ConsensusConfig, events interfaces.EventBus, logger logs.Logger) *SyncManager {
	frontierK, frontierAlpha := 0, 0
	if consensusConfig != nil {
		if consensusConfig.K > 0 {
			frontierK = consensusConfig.K
		}
		if consensusConfig.AlphaConfidence > 0 {
			frontierAlpha = consensusConfig.AlphaConfidence
		}
	}
	sm := &SyncManager{
		nodeID:             id,
		transport:          transport,
		store:              store,
		config:             config,
		events:             events,
		Logger:             logger,
		SyncRequests:       make(map[uint32]time.Time),
		SyncRequestMeta:    make(map[uint32]syncRequestMeta),
		PeerHeights:        make(map[types.NodeID]uint64),
		lastPoll:           time.Now(),
		InFlightSyncRanges: make(map[string]time.Time),
		frontierK:          frontierK,
		frontierAlpha:      frontierAlpha,
	}
	sm.peerRep = sm.newPeerBanTracker()
	return sm
}

                                                                                      
func (sm *SyncManager) Start(ctx context.Context) {
	go func() {
		cleanupLogger := logs.SetThreadNodeContext(string(sm.nodeID))
		defer cleanupLogger()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		var tickCount uint64
		for {
			select {
			case <-ticker.C:
				tickCount++
				sm.processTimeouts()

				if tickCount%5 == 0 {
					sm.pollPeerHeights()
				}

				                                                                         
				checkTicks := uint64(sm.config.CheckInterval / (200 * time.Millisecond))
				if checkTicks < 1 {
					checkTicks = 1
				}
				if tickCount%checkTicks == 0 {
					sm.tickCheckAndSync()
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

                                               
               
                                               

func (sm *SyncManager) HandleHeightQuery(msg types.Message) {
	_, height := sm.store.GetLastAccepted()
	if sm.nodeID == "20" {
		logs.Info("[Node %s] HandleHeightQuery from %s, height=%d", sm.nodeID, msg.From, height)
	}
	sm.transport.Send(types.NodeID(msg.From), types.Message{
		Type:          types.MsgHeightResponse,
		From:          sm.nodeID,
		Height:        height,
		CurrentHeight: sm.store.GetCurrentHeight(),
	})
}

func (sm *SyncManager) HandleHeightResponse(msg types.Message) {
	peer := types.NodeID(msg.From)
	height := msg.Height

	sm.Mu.Lock()
	sm.PeerHeights[peer] = height
	sm.Mu.Unlock()

	if height > 0 {
		go func() {
			cleanupLogger := logs.SetThreadNodeContext(string(sm.nodeID))
			defer cleanupLogger()
			sm.TriggerSyncFromChit(height, peer)
		}()
	}
}

                                               
                 
                                               

                                                                         
func (sm *SyncManager) pollPeerHeights() {
	sm.Mu.RLock()
	if sm.Syncing {
		sm.Mu.RUnlock()
		return
	}
	_, accepted := sm.store.GetLastAccepted()
	maxPeer := sm.getMaxPeerHeightLocked()
	sm.Mu.RUnlock()

	cooldown := 2 * time.Second
	if maxPeer > accepted+2 {
		cooldown = 500 * time.Millisecond
	}
	if time.Since(sm.lastPoll) < cooldown {
		return
	}
	sm.lastPoll = time.Now()

	peers := sm.transport.SamplePeers(sm.nodeID, 10)

	for _, peer := range peers {
		sm.transport.Send(peer, types.Message{Type: types.MsgHeightQuery, From: sm.nodeID})
	}
}

                                                                      
func (sm *SyncManager) processTimeouts() {
	sm.processAcceptedFrontierTimeout(time.Now())
	sm.Mu.Lock()
	if !sm.Syncing && sm.frontierRound == nil && len(sm.SyncRequests) == 0 && len(sm.InFlightSyncRanges) == 0 {
		sm.Mu.Unlock()
		return
	}

	now := time.Now()
	timeout := time.Second
	if sm.config != nil && sm.config.Timeout > 0 {
		timeout = sm.config.Timeout
	}
	var frontierSessionToRetry *SyncSession
	var timedOutMetas []syncRequestMeta
	for syncID, startTime := range sm.SyncRequests {
		meta, hasMeta := sm.SyncRequestMeta[syncID]
		requestTimeout := syncRequestResponseTimeout(timeout, meta, hasMeta)
		if now.Sub(startTime) <= requestTimeout {
			continue
		}
		delete(sm.SyncRequests, syncID)
		delete(sm.SyncRequestMeta, syncID)
		if hasMeta {
			delete(sm.InFlightSyncRanges, meta.RangeKey)
		}
		if hasMeta && meta.FrontierSessionID != 0 && sm.activeSession != nil &&
			sm.activeSession.ID == meta.FrontierSessionID && sm.activeSession.Stage == SyncStageBlockCatchUp {
			frontierSessionToRetry = sm.activeSession
			timedOutMetas = append(timedOutMetas, meta)
		}
	}

	rangeTimeout := timeout * 2
	if serviceRangeTimeout := serviceSyncProviderResponseTimeout * 2; serviceRangeTimeout > rangeTimeout {
		rangeTimeout = serviceRangeTimeout
	}
	for rangeKey, startTime := range sm.InFlightSyncRanges {
		if now.Sub(startTime) > rangeTimeout {
			delete(sm.InFlightSyncRanges, rangeKey)
		}
	}
	sm.Mu.Unlock()
	if frontierSessionToRetry != nil {
		_, acceptedHeight := sm.store.GetLastAccepted()
		for _, meta := range timedOutMetas {
			if meta.Peer == "" {
				continue
			}
			if meta.ProviderRole == syncProviderService {
				sm.markServiceProviderFailure(meta, acceptedHeight)
			} else {
				sm.recordPeerFailure(meta.Peer, "accepted-frontier block request timed out")
			}
		}
		sm.Logger.Warn("[SyncFrontier] session=%d block request timed out; trying another authorized provider",
			frontierSessionToRetry.ID)
		go sm.runStateMachine(frontierSessionToRetry)
	}
}

func syncRequestResponseTimeout(base time.Duration, meta syncRequestMeta, hasMeta bool) time.Duration {
	if hasMeta && meta.ProviderRole == syncProviderService && base < serviceSyncProviderResponseTimeout {
		return serviceSyncProviderResponseTimeout
	}
	return base
}

                                               
         
                                               

                                                                                   
func (sm *SyncManager) getMaxPeerHeightLocked() uint64 {
	var max uint64
	for _, h := range sm.PeerHeights {
		if h > max {
			max = h
		}
	}
	return max
}

func (sm *SyncManager) ShouldPauseConsensusForCatchUp() bool {
	if sm == nil || sm.store == nil {
		return false
	}

	localID, localAccepted := sm.store.GetLastAccepted()
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()
	return sm.shouldPauseConsensusLocked(localID, localAccepted)
}

                                                                            
                                                                              
func (sm *SyncManager) shouldPauseConsensusLocked(localID string, localAccepted uint64) bool {
	session := sm.activeSession
	if session.pauseTargetHeight() <= localAccepted {
		return false
	}
	if session.Stage == SyncStageBlockCatchUp {
		                                                                        
		                                                                       
		return session.FrontierAnchorHeight == localAccepted && session.FrontierAnchorID == localID
	}
	return true
}
