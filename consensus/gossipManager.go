package consensus

import (
	"context"
	"dex/interfaces"
	"dex/logs"
	"dex/types"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"time"
)

                                               
                  
                                               

type GossipManager struct {
	nodeID          types.NodeID
	node            *Node
	transport       interfaces.Transport
	store           interfaces.BlockStore
	config          *GossipConfig
	events          interfaces.EventBus
	Logger          logs.Logger
	queryManager    *QueryManager
	proposalManager *ProposalManager
	seenBlocks      map[string]bool
	relayedBlocks   map[string]bool
	mu              sync.RWMutex
}

func NewGossipManager(id types.NodeID, transport interfaces.Transport, store interfaces.BlockStore, config *GossipConfig, events interfaces.EventBus, logger logs.Logger) *GossipManager {
	gm := &GossipManager{
		nodeID:        id,
		transport:     transport,
		store:         store,
		config:        config,
		events:        events,
		Logger:        logger,
		seenBlocks:    make(map[string]bool),
		relayedBlocks: make(map[string]bool),
	}

	events.Subscribe(types.EventNewBlock, func(e interfaces.Event) {
		if block, ok := e.Data().(*types.Block); ok {
			gm.gossipBlock(block)
		}
	})

	return gm
}

func (gm *GossipManager) SetQueryManager(qm *QueryManager) {
	gm.queryManager = qm
}

func (gm *GossipManager) SetProposalManager(pm *ProposalManager) {
	gm.proposalManager = pm
}

func (gm *GossipManager) Start(ctx context.Context) {
	go func() {
		cleanupLogger := logs.SetThreadNodeContext(string(gm.nodeID))
		defer cleanupLogger()
		ticker := time.NewTicker(gm.config.Interval)
		cleanupTicker := time.NewTicker(60 * time.Second)                                          
		defer ticker.Stop()
		defer cleanupTicker.Stop()

		for {
			select {
			case <-ticker.C:
				gm.gossipNewBlocks()
			case <-cleanupTicker.C:
				gm.cleanupSeenBlocks()
			case <-ctx.Done():
				return
			}
		}
	}()
}

                                                                 
func (gm *GossipManager) cleanupSeenBlocks() {
	_, currentHeight := gm.store.GetLastAccepted()
	if currentHeight < 100 {
		return
	}

	gm.mu.Lock()
	defer gm.mu.Unlock()

	                                                                                          
	                                                            
	if len(gm.seenBlocks) > 1000 || len(gm.relayedBlocks) > 1000 {
		gm.seenBlocks = make(map[string]bool)
		gm.relayedBlocks = make(map[string]bool)
		logs.Debug("[GossipManager] Cleaned up gossip deduplication caches (one was > 1000 entries)")
	}
}

func (gm *GossipManager) gossipNewBlocks() {
	_, currentHeight := gm.store.GetLastAccepted()
	curMax := gm.store.GetCurrentHeight()
	                                                                                     
	if curMax <= currentHeight {
		return
	}

	blocks := make([]*types.Block, 0, 4)                                   
	blocks = append(blocks, gm.store.GetByHeight(currentHeight+1)...)
	if curMax >= currentHeight+2 {                           
		blocks = append(blocks, gm.store.GetByHeight(currentHeight+2)...)
	}

	for _, block := range blocks {
		if block.ID == "genesis" {
			continue
		}
		gm.mu.RLock()
		relayed := gm.relayedBlocks[block.ID]
		gm.mu.RUnlock()
		if !relayed {
			gm.gossipBlock(block)
		}
	}
}

func (gm *GossipManager) gossipBlock(block *types.Block) {
	gm.mu.Lock()
	if gm.relayedBlocks[block.ID] {                                  
		gm.mu.Unlock()
		return
	}
	gm.relayedBlocks[block.ID] = true
	gm.seenBlocks[block.ID] = true                                                          
	gm.mu.Unlock()

	msg := types.Message{
		Type:    types.MsgGossip,
		From:    gm.nodeID,
		Block:   block,
		BlockID: block.ID,
		Height:  block.Header.Height,
	}

	                                                                          
	                                                                           
	                                                  
	var peers []types.NodeID
	if rt, ok := gm.transport.(*RealTransport); ok {
		serviceBudget := gm.config.Fanout / 2
		if gm.config.Fanout > 0 && serviceBudget == 0 {
			serviceBudget = 1
		}
		serviceSent := rt.BroadcastServiceGossip(msg, serviceBudget)
		minerBudget := gm.config.Fanout - serviceSent
		if gm.config.Fanout > 0 && minerBudget < 1 {
			minerBudget = 1
		}
		peers = rt.SampleGossipPeers(gm.nodeID, minerBudget)
	} else {
		peers = gm.transport.SamplePeers(gm.nodeID, gm.config.Fanout)
	}

	gm.transport.Broadcast(msg, peers)
}

func (gm *GossipManager) HandleGossip(msg types.Message) {
	if msg.Block == nil {
		return
	}

	if gm.node != nil {
		gm.node.Stats.Mu.Lock()
		gm.node.Stats.GossipsReceived++
		gm.node.Stats.Mu.Unlock()
	}

	gm.mu.RLock()
	alreadySeen := gm.seenBlocks[msg.Block.ID]
	gm.mu.RUnlock()

	if alreadySeen {
		return
	}
	_, acceptedHeight := gm.store.GetLastAccepted()
	SetBlockCacheAcceptedHeight(acceptedHeight)
	if IsTooFarFutureHeight(msg.Block.Header.Height, acceptedHeight) {
		RemoveCachedBlock(msg.Block.ID)
		logs.Warn("[Node %s] Reject far future gossip block %s height=%d accepted=%d window=%d",
			gm.nodeID, msg.Block.ID, msg.Block.Header.Height, acceptedHeight, MaxFutureBlockCacheWindow)
		return
	}

	if gm.proposalManager == nil {
		logs.Error("[Node %s] Rejecting live gossip block %s: proposal manager is not configured", gm.nodeID, msg.Block.ID)
		return
	}
	isNew, deferred, err := gm.proposalManager.admitLiveBlock(msg.Block)
	if err != nil {
		RemoveCachedBlock(msg.Block.ID)
		if !errors.Is(err, ErrLiveProposalCacheCapacity) {
			gm.mu.Lock()
			gm.seenBlocks[msg.Block.ID] = true
			gm.mu.Unlock()
		}
		                                                               
		if strings.Contains(err.Error(), "parent block") && strings.Contains(err.Error(), "not found") {
			if gm.queryManager != nil {
				gm.queryManager.RequestBlock(msg.Block.Header.ParentID, types.NodeID(msg.From))
			}
		}
		return
	}
	                                                                        
	                                                                      
	gm.mu.Lock()
	gm.seenBlocks[msg.Block.ID] = true
	gm.mu.Unlock()
	if deferred {
		logs.Debug("[Node %s] Deferred live gossip block %s height=%d window=%d until its local window opens",
			gm.nodeID, msg.Block.ID, msg.Block.Header.Height, msg.Block.Header.Window)
		return
	}

	if isNew {
		relay := msg.RelayFrom
		if relay == "" {
			relay = msg.From
		}
		logs.Debug("[Node %s] Received new block %s via gossip relay=%s proposer=%s",
			gm.nodeID, msg.Block.ID, relay, msg.From)

		gm.events.PublishAsync(types.BaseEvent{
			EventType: types.EventBlockReceived,
			EventData: msg.Block,
		})

		go func() {
			cleanupLogger := logs.SetThreadNodeContext(string(gm.nodeID))
			defer cleanupLogger()
			time.Sleep(time.Duration(50+rand.Intn(100)) * time.Millisecond)
			gm.gossipBlock(msg.Block)
		}()
	}
}
