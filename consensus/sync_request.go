package consensus

import (
	"dex/servicepeer"
	"dex/types"
	"fmt"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
)

type syncProviderRole uint8

const (
	                                                                           
	                                                        
	syncProviderFrontierSupporter syncProviderRole = iota
	syncProviderService
)

                                                                             
                                                         
type syncRequestMeta struct {
	Peer         types.NodeID
	ProviderRole syncProviderRole
	FromHeight   uint64
	ToHeight     uint64
	RangeKey     string

	FrontierSessionID uint64
	FrontierNonce     []byte
	FrontierAnchor    uint64
	FrontierAnchorID  string
	FrontierTarget    uint64
	FrontierTargetID  string
}

func syncRangeKey(fromHeight, toHeight uint64) string {
	return fmt.Sprintf("%d-%d", fromHeight, toHeight)
}

                                                                               
                                     
func (sm *SyncManager) requestSyncFrom(fromHeight, toHeight uint64, peer types.NodeID) bool {
	return sm.requestSyncFromProvider(fromHeight, toHeight, peer, syncProviderFrontierSupporter)
}

func (sm *SyncManager) requestSyncFromService(fromHeight, toHeight uint64, peer types.NodeID) bool {
	return sm.requestSyncFromProvider(fromHeight, toHeight, peer, syncProviderService)
}

                                                                          
                                                                              
                                                                       
func (sm *SyncManager) requestSyncFromProvider(fromHeight, toHeight uint64, peer types.NodeID, role syncProviderRole) bool {
	if sm == nil || sm.transport == nil || peer == "" || sm.isPeerBanned(peer) {
		return false
	}
	if role == syncProviderService && !sm.verifiedServiceProviderCovers(peer, fromHeight, toHeight) {
		return false
	}
	if role != syncProviderFrontierSupporter && role != syncProviderService {
		return false
	}
	rangeKey := syncRangeKey(fromHeight, toHeight)
	now := time.Now()

	sm.Mu.Lock()
	session := sm.activeSession
	if session == nil || session.Stage != SyncStageBlockCatchUp ||
		fromHeight != session.FrontierAnchorHeight+1 || toHeight != session.FrontierTargetHeight {
		sm.Mu.Unlock()
		return false
	}
	if role == syncProviderFrontierSupporter {
		if _, supporter := session.frontierSupporters[peer]; !supporter {
			sm.Mu.Unlock()
			return false
		}
	}
	if started, exists := sm.InFlightSyncRanges[rangeKey]; exists {
		timeout := time.Second
		if sm.config != nil && sm.config.Timeout > 0 {
			timeout = sm.config.Timeout
		}
		if now.Sub(started) < timeout {
			sm.Mu.Unlock()
			return false
		}
		delete(sm.InFlightSyncRanges, rangeKey)
	}

	syncID := atomic.AddUint32(&sm.nextSyncID, 1)
	if sm.SyncRequests == nil {
		sm.SyncRequests = make(map[uint32]time.Time)
	}
	if sm.SyncRequestMeta == nil {
		sm.SyncRequestMeta = make(map[uint32]syncRequestMeta)
	}
	if sm.InFlightSyncRanges == nil {
		sm.InFlightSyncRanges = make(map[string]time.Time)
	}
	meta := syncRequestMeta{
		Peer:              peer,
		ProviderRole:      role,
		FromHeight:        fromHeight,
		ToHeight:          toHeight,
		RangeKey:          rangeKey,
		FrontierSessionID: session.ID,
		FrontierNonce:     append([]byte(nil), session.frontierNonce...),
		FrontierAnchor:    session.FrontierAnchorHeight,
		FrontierAnchorID:  session.FrontierAnchorID,
		FrontierTarget:    session.FrontierTargetHeight,
		FrontierTargetID:  session.FrontierTargetID,
	}
	sm.SyncRequests[syncID] = now
	sm.SyncRequestMeta[syncID] = meta
	sm.InFlightSyncRanges[rangeKey] = now
	sm.Mu.Unlock()

	Logf("[Node %s] Requesting frontier sync from Node %s for heights %d-%d\n",
		sm.nodeID, peer, fromHeight, toHeight)
	if err := sm.transport.Send(peer, types.Message{
		Type:        types.MsgSyncRequest,
		From:        sm.nodeID,
		SyncID:      syncID,
		FromHeight:  fromHeight,
		ToHeight:    toHeight,
		ServiceSync: role == syncProviderService,
	}); err != nil {
		sm.Mu.Lock()
		ownedRequest := false
		if registered, exists := sm.SyncRequestMeta[syncID]; exists && registered.Peer == meta.Peer && registered.RangeKey == meta.RangeKey {
			delete(sm.SyncRequests, syncID)
			delete(sm.SyncRequestMeta, syncID)
			ownedRequest = true
		}
		                                                                        
		                                                                      
		if admittedAt, exists := sm.InFlightSyncRanges[rangeKey]; exists && admittedAt.Equal(now) {
			delete(sm.InFlightSyncRanges, rangeKey)
		}
		sm.Mu.Unlock()
		sm.Logger.Warn("[SyncFrontier] request send to %s for heights %d-%d failed: %v",
			peer, fromHeight, toHeight, err)
		if role == syncProviderService && ownedRequest {
			_, acceptedHeight := sm.store.GetLastAccepted()
			sm.markServiceProviderFailure(meta, acceptedHeight)
		}
		return false
	}
	return true
}

func (sm *SyncManager) verifiedServiceProviderCovers(peer types.NodeID, fromHeight, toHeight uint64) bool {
	store := sm.servicePeerStore()
	if store == nil {
		return false
	}
	_, ok := store.CanProvideArchiveRange(string(peer), servicepeer.HeightRange{From: fromHeight, To: toHeight})
	return ok
}

                                                                               
                                                                               
                                                                           
                 
func (sm *SyncManager) HandleSyncRequest(msg types.Message) {
	if sm == nil || sm.store == nil || sm.transport == nil {
		return
	}
	if msg.ToHeight < msg.FromHeight || msg.ToHeight-msg.FromHeight >= AcceptedFrontierMaxSpan {
		sm.Logger.Warn("[SyncFrontier] reject invalid block request from %s range=%d-%d max_span=%d",
			msg.From, msg.FromHeight, msg.ToHeight, AcceptedFrontierMaxSpan)
		return
	}
	_, acceptedHeight := sm.store.GetLastAccepted()
	toHeight := msg.ToHeight
	if toHeight > acceptedHeight {
		toHeight = acceptedHeight
	}
	capacity := 0
	if msg.FromHeight <= toHeight {
		capacity = int(toHeight - msg.FromHeight + 1)
	}
	blocks := make([]*types.Block, 0, capacity)
	if msg.FromHeight <= toHeight {
		for height := msg.FromHeight; height <= toHeight; height++ {
			block, ok := sm.store.GetFinalizedAtHeight(height)
			if !ok || block == nil {
				break
			}
			blocks = append(blocks, block)
			                                                              
			                                                                   
			                                                  
			if height == toHeight {
				break
			}
		}
	}
	response := types.Message{
		Type:          types.MsgSyncResponse,
		From:          sm.nodeID,
		SyncID:        msg.SyncID,
		Blocks:        blocks,
		FromHeight:    msg.FromHeight,
		ToHeight:      msg.ToHeight,
		SyncBlocksRaw: make(map[string][]byte),
	}
	realStore, _ := sm.store.(*RealBlockStore)
	for _, block := range blocks {
		if block == nil {
			continue
		}
		fullBlock, exists := GetCachedBlock(block.ID)
		if (!exists || fullBlock == nil) && realStore != nil {
			if dbBlock, err := realStore.dbManager.GetBlockByID(block.ID); err == nil {
				fullBlock = dbBlock
			}
		}
		if fullBlock == nil {
			continue
		}
		if sanitized, changed := sanitizeBlockTxsForCanonicalValidation(fullBlock); changed {
			fullBlock = sanitized
		}
		if data, err := proto.Marshal(fullBlock); err == nil {
			response.SyncBlocksRaw[block.ID] = data
		}
	}
	if err := sm.transport.Send(types.NodeID(msg.From), response); err != nil {
		sm.Logger.Warn("[SyncFrontier] send block response to %s for heights %d-%d failed: %v",
			msg.From, msg.FromHeight, msg.ToHeight, err)
	}
}

                                                                         
                                                               
func (sm *SyncManager) HandleSyncResponse(msg types.Message) {
	meta, ok := sm.ackSyncRequest(msg)
	if !ok {
		return
	}
	if meta.FrontierSessionID == 0 {
		sm.Logger.Warn("[SyncFrontier] discard unbound sync response id=%d from %s", msg.SyncID, msg.From)
		return
	}
	if msg.SyncFailed {
		if meta.ProviderRole != syncProviderService {
			sm.Logger.Warn("[SyncFrontier] discard local failure notification for non-service provider %s id=%d", msg.From, msg.SyncID)
			return
		}
		session, err := sm.validateFrontierResponseContext(msg, meta)
		if err != nil {
			sm.Logger.Warn("[SyncFrontier] discard stale service failure from %s id=%d: %v", msg.From, msg.SyncID, err)
			return
		}
		_, acceptedHeight := sm.store.GetLastAccepted()
		sm.markServiceProviderFailure(meta, acceptedHeight)
		sm.Logger.Warn("[SyncFrontier] service provider %s failed range=%d-%d: %s; trying another authorized provider",
			msg.From, msg.FromHeight, msg.ToHeight, msg.SyncError)
		go sm.runStateMachine(session)
		return
	}
	sm.handleFrontierSyncResponse(msg, meta)
}

                                                                             
                                                                               
                                                         
func (sm *SyncManager) ackSyncRequest(msg types.Message) (syncRequestMeta, bool) {
	sm.Mu.Lock()
	defer sm.Mu.Unlock()
	if _, ok := sm.SyncRequests[msg.SyncID]; !ok {
		return syncRequestMeta{}, false
	}
	meta, ok := sm.SyncRequestMeta[msg.SyncID]
	if !ok || meta.FrontierSessionID == 0 || types.NodeID(msg.From) != meta.Peer ||
		msg.FromHeight != meta.FromHeight || msg.ToHeight != meta.ToHeight {
		return syncRequestMeta{}, false
	}
	delete(sm.SyncRequests, msg.SyncID)
	delete(sm.SyncRequestMeta, msg.SyncID)
	delete(sm.InFlightSyncRanges, meta.RangeKey)
	return meta, true
}

                                                                             
                                                                    
func (sm *SyncManager) verifyParentLink(block *types.Block, peer types.NodeID) bool {
	if block == nil {
		return true
	}
	lastAcceptedID, lastAcceptedHeight := sm.store.GetLastAccepted()
	if block.Header.Height == lastAcceptedHeight+1 {
		if block.Header.ParentID != lastAcceptedID {
			sm.Logger.Warn("[SyncManager] Reject block %s from %s: parentID=%s, expected lastAccepted=%s at height %d",
				block.ID, peer, block.Header.ParentID, lastAcceptedID, block.Header.Height)
			sm.recordPeerFailure(peer, fmt.Sprintf("parentID %s != lastAccepted %s at height %d",
				block.Header.ParentID, lastAcceptedID, block.Header.Height))
			return false
		}
		return true
	}
	if block.Header.Height > lastAcceptedHeight+1 {
		if parent, exists := sm.store.Get(block.Header.ParentID); exists && parent != nil &&
			parent.Header.Height+1 != block.Header.Height {
			sm.Logger.Warn("[SyncManager] Reject block %s from %s: parent height %d+1 != block height %d",
				block.ID, peer, parent.Header.Height, block.Header.Height)
			sm.recordPeerFailure(peer, fmt.Sprintf("parent height mismatch for block %s", block.ID))
			return false
		}
	}
	return true
}

                                                      
func (sm *SyncManager) getMaxPeerHeight() uint64 {
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()
	var maxHeight uint64
	for _, height := range sm.PeerHeights {
		if height > maxHeight {
			maxHeight = height
		}
	}
	return maxHeight
}
