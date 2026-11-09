package consensus

import (
	"dex/logs"
	"dex/pb"
	"dex/sender"
	"dex/txpool"
	"dex/types"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

                                              
                                                                                 
type PendingBlockBuffer struct {
	mu            sync.RWMutex
	pending       map[string]*pendingBlockEntry                    
	txPool        *txpool.TxPool
	adapter       *ConsensusAdapter
	senderManager *sender.SenderManager
	stopChan      chan struct{}
	logger        logs.Logger
}

type pendingBlockEntry struct {
	block         *types.Block                              
	shortTxs      []byte
	height        uint64
	blockID       string
	sources       []string                                                       
	retryCount    int
	nextRetry     time.Time
	maxRetries    int
	onSuccess     func(*pb.Block)                                        
	onSuccessType func(*types.Block)                                                             
	missingHashes [][]byte                                
	requestID     uint32                                                    
	fromNode      types.NodeID                                           
	proposer      string                                                                
}

const (
	initialRetryDelay = 200 * time.Millisecond
	maxRetryDelay     = 2 * time.Second
	defaultMaxRetries = 5
)

                                                       
func NewPendingBlockBuffer(
	txPool *txpool.TxPool,
	adapter *ConsensusAdapter,
	senderManager *sender.SenderManager,
	logger logs.Logger,
) *PendingBlockBuffer {
	return &PendingBlockBuffer{
		pending:       make(map[string]*pendingBlockEntry),
		txPool:        txPool,
		adapter:       adapter,
		senderManager: senderManager,
		stopChan:      make(chan struct{}),
		logger:        logger,
	}
}

                                                                                                
func (b *PendingBlockBuffer) GetPendingBlock(blockID string) (*types.Block, []byte) {
	if b == nil {
		return nil, nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	entry, ok := b.pending[blockID]
	if !ok {
		return nil, nil
	}
	return entry.block, entry.shortTxs
}

                           
func (b *PendingBlockBuffer) Start() {
	go b.retryLoop()
}

                       
func (b *PendingBlockBuffer) Stop() {
	close(b.stopChan)
}

                                        
func (b *PendingBlockBuffer) AddPendingBlock(
	shortTxs []byte,
	height uint64,
	blockID string,
	peerAddress string,
	missingHashes [][]byte,
	onSuccess func(*pb.Block),
) {
	b.mu.Lock()
	defer b.mu.Unlock()

	                                    
	if _, exists := b.pending[blockID]; exists {
		return
	}

	entry := &pendingBlockEntry{
		shortTxs:      shortTxs,
		height:        height,
		blockID:       blockID,
		sources:       []string{peerAddress},                                       
		retryCount:    0,
		nextRetry:     time.Now().Add(initialRetryDelay),
		maxRetries:    defaultMaxRetries,
		onSuccess:     onSuccess,
		missingHashes: missingHashes,
	}

	b.pending[blockID] = entry

	if b.logger != nil {
		b.logger.Verbose("[PendingBlockBuffer] Added block %s at height %d, missing %d txs",
			blockID, height, len(missingHashes))
	}

	                                 
	go b.fetchMissingTxs(entry)
}

                                                                            
                                                                    
func (b *PendingBlockBuffer) AddPendingBlockForConsensus(
	block *types.Block,
	shortTxs []byte,
	fromNode types.NodeID,
	requestID uint32,
	onSuccess func(*types.Block),
) error {
	if block == nil {
		return fmt.Errorf("block is nil")
	}
	if len(shortTxs) == 0 {
		return fmt.Errorf("shortTxs is empty for block %s", block.ID)
	}

	                                       
	_, missingHashes, err := b.adapter.ResolveShorHashesToTxsWithMissing(shortTxs)
	if err != nil {
		return err
	}

	                                                                  
	if len(missingHashes) == 0 {
		txs, _, _ := b.adapter.ResolveShorHashesToTxsWithMissing(shortTxs)
		                                                                          
		                                                                       
		                                       
		fullBlock := b.buildFullPbBlock(block, txs)
		raw, err := proto.Marshal(fullBlock)
		if err != nil {
			return fmt.Errorf("marshal reconstructed block %s: %w", block.ID, err)
		}
		block.CanonicalPayload = raw

		if b.logger != nil {
			b.logger.Info("[PendingBlockBuffer] Block %s already complete, injecting to consensus", block.ID)
		}

		               
		if onSuccess != nil {
			onSuccess(block)
		}
		return nil
	}

	                                  
	peerIP := b.getNodeIP(fromNode)
	proposerIP := ""
	if block.Header.Proposer != "" {
		                                         
		proposerIP, _ = b.senderManager.AddressToIP(block.Header.Proposer)
	}

	b.mu.Lock()
	if entry, exists := b.pending[block.ID]; exists {
		                                       
		newAddr := peerIP
		found := false
		for _, s := range entry.sources {
			if s == newAddr {
				found = true
				break
			}
		}
		if !found && newAddr != "" {
			entry.sources = append(entry.sources, newAddr)
		}
		if entry.proposer == "" {
			entry.proposer = proposerIP
		}
		b.mu.Unlock()
		return nil
	}

	entry := &pendingBlockEntry{
		block:         block,
		shortTxs:      shortTxs,
		height:        block.Header.Height,
		blockID:       block.ID,
		sources:       []string{peerIP},
		retryCount:    0,
		nextRetry:     time.Now().Add(initialRetryDelay),
		maxRetries:    defaultMaxRetries,
		onSuccessType: onSuccess,
		missingHashes: missingHashes,
		requestID:     requestID,
		fromNode:      fromNode,
		proposer:      proposerIP,
	}

	b.pending[block.ID] = entry
	b.mu.Unlock()

	if b.logger != nil {
		b.logger.Verbose("[PendingBlockBuffer] Added consensus block %s at height %d, missing %d txs",
			block.ID, block.Header.Height, len(missingHashes))
	}

	                                 
	go b.fetchMissingTxs(entry)

	return nil
}

                                                                                
func (b *PendingBlockBuffer) getNodeIP(nodeID types.NodeID) string {
	                                                             
	                                                        
	return string(nodeID)
}

                                            
func (b *PendingBlockBuffer) buildFullPbBlock(block *types.Block, txs []*pb.AnyTx) *pb.Block {
	miner := block.Header.Miner
	if miner == "" {
		miner = block.Header.Proposer
	}
	return &pb.Block{
		BlockHash:      block.ID,
		MinerSignature: append([]byte(nil), block.MinerSignature...),
		Header: &pb.BlockHeader{
			Height:         block.Header.Height,
			PrevBlockHash:  block.Header.ParentID,
			Timestamp:      block.Header.Timestamp,
			StateRoot:      []byte(block.Header.StateRoot),
			TxsHash:        block.Header.TxHash,
			Miner:          miner,
			Window:         int32(block.Header.Window),
			VrfProof:       block.Header.VRFProof,
			VrfOutput:      block.Header.VRFOutput,
			PublicKey:      block.Header.PublicKey,
			OfflineReports: append([]uint32(nil), block.Header.OfflineReports...),
			SampledReports: append([]uint32(nil), block.Header.SampledReports...),
			OnlineReports:  append([]uint32(nil), block.Header.OnlineReports...),
		},
		Body: txs,
	}
}

                                           
func (b *PendingBlockBuffer) fetchMissingTxs(entry *pendingBlockEntry) {
	if b.senderManager == nil || len(entry.missingHashes) == 0 {
		return
	}

	                            
	missingMap := make(map[string]bool)
	for _, hash := range entry.missingHashes {
		missingMap[hex.EncodeToString(hash)] = true
	}

	                                                                                               
	b.mu.RLock()
	targetPeer := ""
	if entry.proposer != "" {
		targetPeer = entry.proposer
	} else if len(entry.sources) > 0 {
		targetPeer = entry.sources[entry.retryCount%len(entry.sources)]
	}
	b.mu.RUnlock()

	if targetPeer == "" {
		return
	}

	if b.logger != nil {
		b.logger.Verbose("[PendingBlockBuffer] Fetching %d missing txs for block %s from %s (retry %d)",
			len(missingMap), entry.blockID, targetPeer, entry.retryCount)
	}

	                            
	b.senderManager.BatchGetTxs(targetPeer, missingMap, func(txs []*pb.AnyTx) {
		if b.logger != nil {
			b.logger.Verbose("[PendingBlockBuffer] Received %d txs for block %s from %s",
				len(txs), entry.blockID, targetPeer)
		}

		                
		for _, tx := range txs {
			if err := b.txPool.CacheAnyTx(tx); err != nil {
				if b.logger != nil {
					b.logger.Debug("[PendingBlockBuffer] Failed to store tx: %v", err)
				}
			}
		}

		               
		b.triggerRetry(entry.blockID)
	})
}

                                  
func (b *PendingBlockBuffer) triggerRetry(blockID string) {
	b.mu.Lock()
	if entry, exists := b.pending[blockID]; exists {
		entry.nextRetry = time.Now()                
	}
	b.mu.Unlock()
}

                         
func (b *PendingBlockBuffer) retryLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopChan:
			return
		case <-ticker.C:
			b.processRetries()
		}
	}
}

                                       
func (b *PendingBlockBuffer) processRetries() {
	b.mu.Lock()
	now := time.Now()
	var toProcess []*pendingBlockEntry
	var toRemove []string

	for blockID, entry := range b.pending {
		if now.After(entry.nextRetry) {
			if entry.retryCount >= entry.maxRetries {
				toRemove = append(toRemove, blockID)
				if b.logger != nil {
					b.logger.Warn("[PendingBlockBuffer] Block %s exceeded max retries, giving up", blockID)
				}
			} else {
				toProcess = append(toProcess, entry)
			}
		}
	}

	                  
	for _, blockID := range toRemove {
		delete(b.pending, blockID)
	}
	b.mu.Unlock()

	                     
	for _, entry := range toProcess {
		b.retryResolve(entry)
	}
}

                                  
func (b *PendingBlockBuffer) retryResolve(entry *pendingBlockEntry) {
	               
	txs, missingHashes, err := b.adapter.ResolveShorHashesToTxsWithMissing(entry.shortTxs)
	if err != nil {
		if b.logger != nil {
			b.logger.Debug("[PendingBlockBuffer] Resolve error for block %s: %v", entry.blockID, err)
		}
		b.updateRetry(entry, missingHashes)
		return
	}

	if len(missingHashes) > 0 {
		                                       
		if b.logger != nil {
			b.logger.Verbose("[PendingBlockBuffer] Block %s still missing %d txs (retry %d/%d)",
				entry.blockID, len(missingHashes), entry.retryCount+1, entry.maxRetries)
		}
		b.updateRetry(entry, missingHashes)

		                     
		entry.missingHashes = missingHashes
		go b.fetchMissingTxs(entry)
		return
	}

	               
	if b.logger != nil {
		b.logger.Info("[PendingBlockBuffer] Successfully resolved block %s with %d txs",
			entry.blockID, len(txs))
	}

	                     
	var fullPbBlock *pb.Block
	if entry.block != nil {
		                                                   
		fullPbBlock = b.buildFullPbBlock(entry.block, txs)
	} else {
		                  
		fullPbBlock = &pb.Block{
			BlockHash: entry.blockID,
			Header: &pb.BlockHeader{
				Height: entry.height,
			},
			Body:     txs,
			ShortTxs: entry.shortTxs,
		}
	}

	                                                                           
	                                                                         
	                                                                              
	if entry.block != nil {
		if raw, marshalErr := proto.Marshal(fullPbBlock); marshalErr == nil {
			entry.block.CanonicalPayload = raw
		} else {
			if b.logger != nil {
				b.logger.Warn("[PendingBlockBuffer] Failed to marshal resolved block %s: %v", entry.blockID, marshalErr)
			}
			b.updateRetry(entry, nil)
			return
		}
	}

	if b.logger != nil {
		b.logger.Info("[PendingBlockBuffer] ✅ Successfully resolved block %s (height %d) after fetching missing txs", entry.blockID, entry.height)
	}

	                        
	b.mu.Lock()
	delete(b.pending, entry.blockID)
	b.mu.Unlock()

	                                                    
	if entry.onSuccessType != nil && entry.block != nil {
		entry.onSuccessType(entry.block)
	} else if entry.onSuccess != nil {
		entry.onSuccess(fullPbBlock)
	}
}

                                 
func (b *PendingBlockBuffer) updateRetry(entry *pendingBlockEntry, missingHashes [][]byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry.retryCount++
	entry.missingHashes = missingHashes

	               
	delay := initialRetryDelay * time.Duration(1<<uint(entry.retryCount))
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	entry.nextRetry = time.Now().Add(delay)
}

                                            
func (b *PendingBlockBuffer) HasPending(blockID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, exists := b.pending[blockID]
	return exists
}

                                              
func (b *PendingBlockBuffer) GetPendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}
