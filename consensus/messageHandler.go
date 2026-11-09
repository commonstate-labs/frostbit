package consensus

import (
	"dex/interfaces"
	"dex/logs"
	"dex/pb"
	"dex/stats"
	"dex/types"
	"errors"
	"fmt"
	"strings"
	"time"
)

                                               
                  
                                               

type MessageHandler struct {
	nodeID        types.NodeID
	node          *Node
	isByzantine   bool
	transport     interfaces.Transport
	store         interfaces.BlockStore
	engine        interfaces.ConsensusEngine
	gossipManager *GossipManager
	syncManager   *SyncManager

	events             interfaces.EventBus
	config             *ConsensusConfig
	Logger             logs.Logger
	queryManager       *QueryManager
	proposalManager    *ProposalManager                                     
	stats              *stats.Stats
	pendingBlockBuffer *PendingBlockBuffer                              
	signer             interfaces.NodeSigner                                  
	livenessTracker    interfaces.LivenessTracker
}

func NewMessageHandler(id types.NodeID, byzantine bool, transport interfaces.Transport, store interfaces.BlockStore, engine interfaces.ConsensusEngine, events interfaces.EventBus, config *ConsensusConfig, logger logs.Logger) *MessageHandler {
	return &MessageHandler{
		nodeID:      id,
		isByzantine: byzantine,
		transport:   transport,
		store:       store,
		engine:      engine,
		events:      events,
		config:      config,
		Logger:      logger,
		stats:       stats.NewStats(),
	}
}

func (h *MessageHandler) SetManagers(qm *QueryManager, gm *GossipManager, sm *SyncManager) {
	h.queryManager = qm
	h.gossipManager = gm
	h.syncManager = sm
}

func (h *MessageHandler) SetProposalManager(pm *ProposalManager) {
	h.proposalManager = pm
}

func (h *MessageHandler) admitLiveBlock(block *types.Block) (added bool, deferred bool, err error) {
	if h.proposalManager == nil {
		return false, false, fmt.Errorf("live block admission unavailable: proposal manager is not configured")
	}
	return h.proposalManager.admitLiveBlock(block)
}

func (h *MessageHandler) SetPendingBlockBuffer(buffer *PendingBlockBuffer) {
	h.pendingBlockBuffer = buffer
}

func (h *MessageHandler) SetLivenessTracker(tracker interfaces.LivenessTracker) {
	h.livenessTracker = tracker
}

func (h *MessageHandler) HandleMsg(msg types.Message) {
	h.stats.RecordAPICall(string(msg.Type))
	if h.isByzantine && (msg.Type == types.MsgPullQuery || msg.Type == types.MsgPushQuery) {
		if h.node != nil {
			h.node.Stats.Mu.Lock()
			h.node.Stats.QueriesReceived++
			h.node.Stats.Mu.Unlock()
		}
		return
	}
	switch msg.Type {
	case types.MsgPullQuery:
		h.handlePullQuery(msg)
	case types.MsgPushQuery:
		h.handlePushQuery(msg)
	case types.MsgChits:
		h.queryManager.HandleChit(msg)
	case types.MsgGet:
		h.handleGet(msg)
	case types.MsgPut:
		h.handlePut(msg)
	case types.MsgGossip:
		h.gossipManager.HandleGossip(msg)
	case types.MsgSyncRequest:
		h.syncManager.HandleSyncRequest(msg)
	case types.MsgSyncResponse:
		h.syncManager.HandleSyncResponse(msg)
	case types.MsgHeightResponse:
		h.syncManager.HandleHeightResponse(msg)

	}
}

func (h *MessageHandler) handlePullQuery(msg types.Message) {
	if h.node != nil {
		h.node.Stats.Mu.Lock()
		h.node.Stats.QueriesReceived++
		h.node.Stats.Mu.Unlock()
	}
	if h.livenessTracker == nil || !h.livenessTracker.IsActive(string(msg.From)) {
		logs.Debug("[PullQuery] requester %s is not Active; ignoring", msg.From)
		return
	}
	if msg.BlockID == "" {
		h.sendProbationProbeChit(types.NodeID(msg.From), msg.RequestID, msg.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
		return
	}

	                                                                            
	                                                                          
	                                                                          
	_, acceptedHeight := h.store.GetLastAccepted()
	if msg.Height <= acceptedHeight {
		h.sendChits(types.NodeID(msg.From), msg.RequestID, msg.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
		return
	}

	if _, exists := h.store.Get(msg.BlockID); !exists && h.queryManager != nil {
		h.queryManager.RequestBlock(msg.BlockID, msg.From)
	}
	                                                                                                  
	                                                                                                  
	h.sendChits(msg.From, msg.RequestID, msg.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
}

func (h *MessageHandler) handlePushQuery(msg types.Message) {
	if h.node != nil {
		h.node.Stats.Mu.Lock()
		h.node.Stats.QueriesReceived++
		h.node.Stats.Mu.Unlock()
	}
	if h.livenessTracker == nil || !h.livenessTracker.IsActive(string(msg.From)) {
		logs.Debug("[PushQuery] requester %s is not Active; ignoring", msg.From)
		return
	}

	if msg.Block == nil {
		return
	}
	if err := validatePushQueryProposerBinding(msg); err != nil {
		RemoveCachedBlock(msg.Block.ID)
		logs.Warn("[Node %s] Reject PushQuery block %s: %v", h.nodeID, msg.Block.ID, err)
		return
	}

	                                                                                                                                  
	_, acceptedHeight := h.store.GetLastAccepted()
	SetBlockCacheAcceptedHeight(acceptedHeight)
	if IsTooFarFutureHeight(msg.Block.Header.Height, acceptedHeight) {
		RemoveCachedBlock(msg.Block.ID)
		logs.Warn("[Node %s] Reject far future PushQuery block %s height=%d accepted=%d window=%d",
			h.nodeID, msg.Block.ID, msg.Block.Header.Height, acceptedHeight, MaxFutureBlockCacheWindow)
		h.sendChits(types.NodeID(msg.From), msg.RequestID, msg.Block.Header.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
		return
	}
	if msg.Block.Header.Height <= acceptedHeight {
		h.sendChits(types.NodeID(msg.From), msg.RequestID, msg.Block.Header.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
		return
	}

	isNew, deferred, err := h.admitLiveBlock(msg.Block)
	if deferred {
		logs.Debug("[Node %s] Deferred live PushQuery block %s height=%d window=%d until its local window opens",
			h.nodeID, msg.Block.ID, msg.Block.Header.Height, msg.Block.Header.Window)
		                                                                           
		                                                                          
		h.sendChits(types.NodeID(msg.From), msg.RequestID, msg.Block.Header.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
		return
	}
	if err != nil {
		                                                                                                        
		if strings.Contains(err.Error(), "block data incomplete") {
			shortTxs := msg.ShortTxs
			if len(shortTxs) == 0 {
				if cached, exists := GetCachedBlock(msg.Block.ID); exists && cached != nil {
					shortTxs = cached.ShortTxs
				}
			}
			if h.pendingBlockBuffer != nil && len(shortTxs) > 0 {
				                                          
				fromNode := types.NodeID(msg.From)
				requestID := msg.RequestID
				block := msg.Block

				                                                      
				h.pendingBlockBuffer.AddPendingBlockForConsensus(
					block,
					shortTxs,
					fromNode,
					requestID,
					func(resolvedBlock *types.Block) {
						                                                   
						if newAdded, _, addErr := h.admitLiveBlock(resolvedBlock); addErr == nil && newAdded {
							logs.Info("[Node %s] Block %s resolved and added to consensus",
								h.nodeID, resolvedBlock.ID)
							h.events.PublishAsync(types.BaseEvent{
								EventType: types.EventNewBlock,
								EventData: resolvedBlock,
							})
						}
						                                  
						h.sendChits(fromNode, requestID, resolvedBlock.Header.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
					},
				)
				logs.Debug("[Node %s] Block %s has incomplete data, queued for resolution",
					h.nodeID, msg.Block.ID)
				                                                                        
				return
			}
		}

		                                                               
		RemoveCachedBlock(msg.Block.ID)
		if strings.Contains(err.Error(), "parent block") && strings.Contains(err.Error(), "not found") {
			if h.queryManager != nil {
				h.queryManager.RequestBlock(msg.Block.Header.ParentID, types.NodeID(msg.From))
			}
		}

		                                                         
		h.sendChits(types.NodeID(msg.From), msg.RequestID, msg.Block.Header.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
		return
	}

	if isNew {
		logs.Debug("[Node %s] Received new block %s via PushQuery (window %d)",
			h.nodeID, msg.Block.ID, msg.Block.Header.Window)
		h.events.PublishAsync(types.BaseEvent{
			EventType: types.EventNewBlock,
			EventData: msg.Block,
		})
	}

	h.sendChits(types.NodeID(msg.From), msg.RequestID, msg.Block.Header.Height, msg.VRFSeed, msg.SeqID, msg.QueryNonce)
}

                                                                    
func computeChitSignature(signer interfaces.NodeSigner, context ChitDigestContext) []byte {
	if signer == nil {
		return nil
	}
	digest := ComputeChitDigest(context)
	sig, err := signer.Sign(digest)
	if err != nil {
		logs.Warn("[computeChitSignature] ECDSA sign failed: %v", err)
		return nil
	}
	return sig
}

                                                                             
                                                                          
                                                                       
type finalizingPreferenceReader interface {
	finalizingBlockID(height uint64) string
}

var _ finalizingPreferenceReader = (*SnowmanEngine)(nil)

func finalizingPreferenceAt(engine interfaces.ConsensusEngine, height uint64) string {
	reader, ok := engine.(finalizingPreferenceReader)
	if !ok {
		return ""
	}
	return reader.finalizingBlockID(height)
}

func (h *MessageHandler) sendChits(to types.NodeID, requestID uint32, queryHeight uint64, vrfSeed []byte, seqID uint32, queryNonce []byte) {
	var preferred string
	traceResult := "no_membership"
	if logs.ConsensusTraceEnabled() {
		started := time.Now()
		defer func() { traceBuiltChit(h.nodeID, to, requestID, queryHeight, preferred, traceResult, started) }()
	}
	if h.livenessTracker == nil || !h.livenessTracker.IsActive(string(h.nodeID)) {
		logs.Debug("[sendChits] Local address %s has no verified Active membership; abstaining", h.nodeID)
		return
	}
	accepted, acceptedHeight := h.store.GetLastAccepted()

	                                                           
	if queryHeight <= acceptedHeight {
		                                                                   
		                                                                 
		preferred = h.finalizedPreference(queryHeight)
	} else {
		                                                                           
		parent, ok := h.store.GetFinalizedAtHeight(queryHeight - 1)
		if !ok {
			                                                   
			logs.Debug("[sendChits] Parent at height %d not finalized, abstaining for height %d",
				queryHeight-1, queryHeight)
			preferred = ""          
		} else {
			finalizingPreference := finalizingPreferenceAt(h.engine, queryHeight)
			if finalizingPreference != "" {
				                                                                     
				                                                                    
				                                                                  
				                                                                    
				                                                             
				preferred = finalizingPreference
				block, exists := h.store.Get(finalizingPreference)
				if !exists || block == nil || block.ID != finalizingPreference || block.Header.Height != queryHeight || block.Header.ParentID != parent.ID {
					logs.Warn("[sendChits] Finalizing preference %s does not match height/parent, abstaining", finalizingPreference)
					preferred = ""
				}
			} else {
				                              
				preferred = h.engine.GetPreference(queryHeight)

				                                                               
				if preferred != "" {
					block, exists := h.store.Get(preferred)
					if !exists || block == nil || block.ID != preferred || block.Header.Height != queryHeight || block.Header.ParentID != parent.ID {
						logs.Debug("[sendChits] Preferred block %s has wrong height/parent, abstaining", preferred)
						preferred = ""
					} else if err := validateCandidatePreStateRoot(h.store, block); err != nil {
						logs.Warn("[sendChits] Preferred block %s failed pre-state root validation, abstaining: %v", preferred, err)
						if errors.Is(err, ErrInvalidCandidatePreStateRoot) {
							invalidateDeterministicCandidate(h.engine, h.store, queryHeight, block.ID)
						}
						preferred = ""
					}
				}

				                                                                     
				                                                                              
				                                                   
				                                                                              
				                                                                   

				                                                                 
				                                                                 
				if preferred == "" {
					preferred = h.engine.GetPreference(queryHeight)
					if preferred != "" {
						block, exists := h.store.Get(preferred)
						if !exists || block == nil || block.ID != preferred || block.Header.Height != queryHeight || block.Header.ParentID != parent.ID {
							preferred = ""
						} else if err := validateCandidatePreStateRoot(h.store, block); err != nil {
							if errors.Is(err, ErrInvalidCandidatePreStateRoot) {
								invalidateDeterministicCandidate(h.engine, h.store, queryHeight, block.ID)
							}
							preferred = ""
						}
					}
				}
			}
		}
	}

	                                                                             
	                                                                         
	accepted, acceptedHeight = h.store.GetLastAccepted()
	if queryHeight <= acceptedHeight {
		preferred = h.finalizedPreference(queryHeight)
	}
	context := ChitDigestContext{
		ChainID:        pb.GetDefaultChainID(),
		RequestID:      requestID,
		Requester:      string(to),
		Responder:      string(h.nodeID),
		PreferredBlock: preferred,
		QueryHeight:    queryHeight,
		AcceptedBlock:  accepted,
		AcceptedHeight: acceptedHeight,
		VRFSeed:        vrfSeed,
		SeqID:          seqID,
		QueryNonce:     queryNonce,
	}

	                                                             
	var chitSig []byte
	if len(vrfSeed) > 0 && len(queryNonce) == SnowmanQueryNonceSize {
		chitSig = computeChitSignature(h.signer, context)
	}

	logs.Debug("[sendChits] to=%s req=%d h=%d preferred=%v accepted=%v hasSig=%v",
		to, requestID, queryHeight, preferred, accepted, len(chitSig) > 0)

	traceResult = "submitted"
	if err := h.transport.Send(to, types.Message{
		Type: types.MsgChits, From: h.nodeID, RequestID: requestID,
		PreferredID:       preferred,
		PreferredIDHeight: queryHeight,
		AcceptedID:        accepted, AcceptedHeight: acceptedHeight,
		ChitSignature: chitSig,
		QueryNonce:    append([]byte(nil), queryNonce...),
	}); err != nil {
		traceResult = "send_rejected"
	}

	                                  
	if h.node != nil {
		h.node.Stats.Mu.Lock()
		h.node.Stats.ChitsResponded++
		h.node.Stats.Mu.Unlock()
	}
}

                                                                               
                                                                            
                                                                             
                  
func (h *MessageHandler) sendProbationProbeChit(to types.NodeID, requestID uint32, queryHeight uint64, vrfSeed []byte, seqID uint32, queryNonce []byte) {
	if h.livenessTracker == nil || !h.livenessTracker.IsActiveOrProbation(string(h.nodeID)) {
		return
	}
	accepted, acceptedHeight := h.store.GetLastAccepted()
	context := ChitDigestContext{
		ChainID:        pb.GetDefaultChainID(),
		RequestID:      requestID,
		Requester:      string(to),
		Responder:      string(h.nodeID),
		PreferredBlock: "",
		QueryHeight:    queryHeight,
		AcceptedBlock:  accepted,
		AcceptedHeight: acceptedHeight,
		VRFSeed:        vrfSeed,
		SeqID:          seqID,
		QueryNonce:     queryNonce,
	}
	var chitSig []byte
	if len(vrfSeed) > 0 && len(queryNonce) == SnowmanQueryNonceSize {
		chitSig = computeChitSignature(h.signer, context)
	}
	if len(chitSig) == 0 {
		return
	}
	if err := h.transport.Send(to, types.Message{
		Type:              types.MsgChits,
		From:              h.nodeID,
		RequestID:         requestID,
		PreferredID:       "",
		PreferredIDHeight: queryHeight,
		AcceptedID:        accepted,
		AcceptedHeight:    acceptedHeight,
		ChitSignature:     chitSig,
		QueryNonce:        append([]byte(nil), queryNonce...),
	}); err == nil && h.node != nil {
		h.node.Stats.Mu.Lock()
		h.node.Stats.ChitsResponded++
		h.node.Stats.Mu.Unlock()
	}
}

func (h *MessageHandler) finalizedPreference(height uint64) string {
	if block, ok := h.store.GetFinalizedAtHeight(height); ok && block != nil {
		return block.ID
	}
	if height == 0 {
		return "genesis"
	}
	logs.Warn("[sendChits] finalized block at height %d unavailable; abstaining", height)
	return ""
}

func (h *MessageHandler) handleGet(msg types.Message) {
	                                                         
	if block, exists := h.store.Get(msg.BlockID); exists {
		err := h.transport.Send(types.NodeID(msg.From), types.Message{
			Type:      types.MsgPut,
			From:      h.nodeID,
			RequestID: msg.RequestID,
			Block:     block,
			Height:    block.Header.Height,
		})
		if err != nil {
			return
		}
	}
}

func (h *MessageHandler) handlePut(msg types.Message) {
	if msg.Block == nil {
		return
	}
	_, acceptedHeight := h.store.GetLastAccepted()
	SetBlockCacheAcceptedHeight(acceptedHeight)
	if IsTooFarFutureHeight(msg.Block.Header.Height, acceptedHeight) {
		RemoveCachedBlock(msg.Block.ID)
		logs.Warn("[Node %s] Reject far future Put block %s height=%d accepted=%d window=%d",
			h.nodeID, msg.Block.ID, msg.Block.Header.Height, acceptedHeight, MaxFutureBlockCacheWindow)
		return
	}
	isNew, deferred, err := h.admitLiveBlock(msg.Block)
	if deferred {
		logs.Debug("[Node %s] Deferred live Put block %s height=%d window=%d until its local window opens",
			h.nodeID, msg.Block.ID, msg.Block.Header.Height, msg.Block.Header.Window)
		return
	}
	if err != nil {
		defer RemoveCachedBlock(msg.Block.ID)
		if strings.Contains(err.Error(), "block data incomplete") {
			shortTxs := msg.ShortTxs
			if len(shortTxs) == 0 {
				if cached, exists := GetCachedBlock(msg.Block.ID); exists && cached != nil {
					shortTxs = cached.ShortTxs
				}
			}

			if h.pendingBlockBuffer != nil && len(shortTxs) > 0 {
				fromNode := types.NodeID(msg.From)
				requestID := msg.RequestID
				block := msg.Block

				_ = h.pendingBlockBuffer.AddPendingBlockForConsensus(
					block,
					shortTxs,
					fromNode,
					requestID,
					func(resolvedBlock *types.Block) {
						if resolvedBlock == nil {
							return
						}
						if newAdded, _, addErr := h.admitLiveBlock(resolvedBlock); addErr == nil && newAdded {
							logs.Info("[Node %s] Block %s resolved and added via Put path",
								h.nodeID, resolvedBlock.ID)
							h.events.PublishAsync(types.BaseEvent{
								EventType: types.EventBlockReceived,
								EventData: resolvedBlock,
							})
						}
					},
				)
				return
			}

			if h.queryManager != nil && msg.From != "" {
				h.queryManager.RequestBlock(msg.Block.ID, types.NodeID(msg.From))
			}
		}
		                                                               
		if strings.Contains(err.Error(), "parent block") && strings.Contains(err.Error(), "not found") {
			if h.queryManager != nil {
				h.queryManager.RequestBlock(msg.Block.Header.ParentID, types.NodeID(msg.From))
			}
		}

		return
	}

	if isNew {
		logs.Debug("[Node %s] Received new block %s via Put from Node %s",
			h.nodeID, msg.Block.ID, msg.From)
		h.events.PublishAsync(types.BaseEvent{
			EventType: types.EventBlockReceived,
			EventData: msg.Block,
		})
	}
}
