package consensus

import (
	"dex/blocktrace"
	"dex/config"
	"dex/db"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/txpool"
	"dex/txverify"
	"dex/types"
	"dex/vm"
	"dex/witness"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
)

var ErrAlreadyFinalized = errors.New("already finalized")
var emptyTxsHash = txpool.ComputeTxsHash(nil)
var finalizedVMCommitLimiter = &vmCommitLimiter{}

type vmCommitLimiter struct {
	mu    sync.Mutex
	sem   chan struct{}
	limit int
}

func configureFinalizedVMCommitLimiter(limit int) {
	if limit <= 0 {
		limit = config.DefaultFinalizedVMCommitConcurrency()
	}
	finalizedVMCommitLimiter.mu.Lock()
	defer finalizedVMCommitLimiter.mu.Unlock()
	if finalizedVMCommitLimiter.sem != nil && finalizedVMCommitLimiter.limit == limit {
		return
	}
	finalizedVMCommitLimiter.sem = make(chan struct{}, limit)
	finalizedVMCommitLimiter.limit = limit
}

func acquireFinalizedVMCommitSlot() (func(), time.Duration) {
	finalizedVMCommitLimiter.mu.Lock()
	sem := finalizedVMCommitLimiter.sem
	if sem == nil {
		limit := config.DefaultFinalizedVMCommitConcurrency()
		sem = make(chan struct{}, limit)
		finalizedVMCommitLimiter.sem = sem
		finalizedVMCommitLimiter.limit = limit
	}
	finalizedVMCommitLimiter.mu.Unlock()

	start := time.Now()
	sem <- struct{}{}
	return func() { <-sem }, time.Since(start)
}

                                                            
type RealBlockStore struct {
	mu         sync.RWMutex
	finalizeMu sync.Mutex                                                                                
	dbManager  *db.Manager
	pool       *txpool.TxPool
	adapter    *ConsensusAdapter
	vmExecutor *vm.Executor                                                              
	events     interfaces.EventBus                
	               
	blockCache         map[string]*types.Block
	heightIndex        map[uint64][]*types.Block
	finalizedBlocks    map[uint64]*types.Block
	lastAccepted       *types.Block
	lastAcceptedHeight uint64
	maxHeight          uint64
	                                                                           
	                                                                       
	                                                                     
	orphanBlocks map[string]*types.Block
	orphanOrder  []string
	orphanBytes  int
	orphanLimit  int

	nodeID       types.NodeID
	windowConfig config.WindowConfig

	                                          
	finalizationChits map[uint64]*types.FinalizationChits

	                                                                                    
	forceFlushMu            sync.Mutex
	forceFlushEveryN        int
	forceFlushMaxInterval   time.Duration
	finalizedSinceLastFlush uint64
	lastForceFlushAt        time.Time
	timingMu                sync.RWMutex
	lastBlockTiming         BlockTimingSnapshot
	diskPruneRunning        atomic.Bool
	recoveryErr             error
}

                              
func NewRealBlockStore(nodeID types.NodeID, dbManager *db.Manager, pool *txpool.TxPool, cfg *config.Config) interfaces.BlockStore {
	if cfg != nil {
		configureFinalizedVMCommitLimiter(cfg.Database.FinalizedVMCommitConcurrency)
	} else {
		configureFinalizedVMCommitLimiter(0)
	}

	                        
	var witnessCfg *witness.Config
	if cfg != nil {
		witnessCfg = &witness.Config{
			ConsensusThreshold:           cfg.Witness.ConsensusThreshold,
			AbstainThreshold:             cfg.Witness.AbstainThreshold,
			VotingPeriodBlocks:           cfg.Witness.VotingPeriodBlocks,
			VoteGracePeriodBlocks:        cfg.Witness.VoteGracePeriodBlocks,
			ChallengePeriodBlocks:        cfg.Witness.ChallengePeriodBlocks,
			ArbitrationPeriodBlocks:      cfg.Witness.ArbitrationPeriodBlocks,
			UnstakeLockBlocks:            cfg.Witness.UnstakeLockBlocks,
			RetryIntervalBlocks:          cfg.Witness.RetryIntervalBlocks,
			MaxShelveWaitBlocks:          cfg.Witness.MaxShelveWaitBlocks,
			MinStakeAmount:               cfg.Witness.MinStakeAmount,
			ChallengeStakeAmount:         cfg.Witness.ChallengeStakeAmount,
			ChallengeStakeRatioBps:       cfg.Witness.ChallengeStakeRatioBps,
			ChallengeArbitrationBaseCost: cfg.Witness.ChallengeArbitrationBaseCost,
			MaxChallengeStakeAmount:      cfg.Witness.MaxChallengeStakeAmount,
			InitialWitnessCount:          cfg.Witness.InitialWitnessCount,
			MinSelectedWitnessCount:      cfg.Witness.MinSelectedWitnessCount,
			ExpandMultiplier:             cfg.Witness.ExpandMultiplier,
			OrdinaryCommitteeSizes:       append([]uint32(nil), cfg.Witness.OrdinaryCommitteeSizes...),
			OrdinaryThresholdBps:         cfg.Witness.OrdinaryThresholdBps,
			SelectionBeaconDepth:         cfg.Witness.SelectionBeaconDepth,
			BeaconMixBlocks:              cfg.Witness.BeaconMixBlocks,
			SlashRatio:                   cfg.Witness.SlashRatio,
			ChallengerReward:             cfg.Witness.ChallengerReward,
			SilenceSlashRatio:            cfg.Witness.SilenceSlashRatio,
		}
	}

	                           
	witnessSvc := witness.NewService(witnessCfg)
	_ = witnessSvc.Start()

	                         
	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry, cfg, witnessSvc); err != nil {
		logs.Error("Failed to register VM handlers: %v", err)
		                                            
	}
	                                                                   
	                                                                                 
	                                                                                   
	cache := vm.NewSpecExecLRU(32)

	vmExecutor := vm.NewExecutorWithWitnessService(dbManager, registry, cache, witnessSvc)
	if cfg != nil {
		vmExecutor.SetDumpCertEvidence(cfg.Diagnostics.DumpCertEvidence)
		                                                                                                           
		                                                                                    
		dbManager.SetStateDBDriftAudit(cfg.Diagnostics.DumpCertEvidence)
	}

	                                                                                              
	                                                                                         
	                                                                                                          
	if err := vmExecutor.LoadWitnessState(); err != nil {
		logs.Warn("[RealBlockStore] failed to load witness state: %v", err)
	}
	windowConfig := config.DefaultConfig().Window
	if cfg != nil {
		windowConfig = cfg.Window
	}
	windowConfig.Stages = append([]config.WindowStage(nil), windowConfig.Stages...)

	store := &RealBlockStore{
		dbManager:             dbManager,
		pool:                  pool,
		vmExecutor:            vmExecutor,
		blockCache:            make(map[string]*types.Block),
		heightIndex:           make(map[uint64][]*types.Block),
		finalizedBlocks:       make(map[uint64]*types.Block),
		orphanBlocks:          make(map[string]*types.Block),
		orphanLimit:           defaultMaxPendingParentOrphanBytes,
		maxHeight:             0,
		adapter:               NewConsensusAdapter(dbManager),
		nodeID:                nodeID,
		windowConfig:          windowConfig,
		finalizationChits:     make(map[uint64]*types.FinalizationChits),
		forceFlushEveryN:      1,
		forceFlushMaxInterval: 0,
		lastForceFlushAt:      time.Now(),
	}

	if cfg != nil {
		store.forceFlushEveryN = cfg.Database.FinalizationForceFlushEveryN
		store.forceFlushMaxInterval = cfg.Database.FinalizationForceFlushInterval
	}

	logs.Info(
		"[RealBlockStore] Finalization flush policy: everyN=%d, interval=%v",
		store.forceFlushEveryN,
		store.forceFlushMaxInterval,
	)

	                        
	genesis := &types.Block{
		ID: "genesis",
		Header: types.BlockHeader{
			Height:   0,
			ParentID: "",
			Proposer: "-1",
		},
	}

	store.blockCache[genesis.ID] = genesis
	store.heightIndex[0] = []*types.Block{genesis}
	store.lastAccepted = genesis
	store.lastAcceptedHeight = 0
	store.finalizedBlocks[0] = genesis

	                                 
	store.recoveryErr = store.loadFromDB()
	if store.recoveryErr != nil {
		logs.Error("[RealBlockStore] finalized-state recovery failed: %v", store.recoveryErr)
	}
	SetBlockCacheAcceptedHeight(store.lastAcceptedHeight)

	return store
}

                                                            
func (s *RealBlockStore) SetEventBus(events interfaces.EventBus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = events
}

                      
func isCompleteBlockPayload(block *pb.Block) bool {
	if block == nil || block.Header == nil {
		return false
	}
	if len(block.Body) > 0 {
		return true
	}
	if len(block.ShortTxs) > 0 {
		return false
	}
	return block.Header.TxsHash == emptyTxsHash
}

func normalizeReceiptStatus(status string) (pb.Status, bool) {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCEED", "SUCCESS":
		return pb.Status_SUCCEED, true
	case "FAILED", "FAIL":
		return pb.Status_FAILED, true
	default:
		return pb.Status_PENDING, false
	}
}

                                                                                        
                                                                                            
func (s *RealBlockStore) materializeFinalizedBlock(block *pb.Block, receipts map[string]*vm.Receipt) *pb.Block {
	if block == nil {
		return nil
	}
	cloned, ok := proto.Clone(block).(*pb.Block)
	if !ok || cloned == nil || cloned.Header == nil || len(cloned.Body) == 0 {
		return block
	}

	finalizedHeight := cloned.Header.Height
	for _, tx := range cloned.Body {
		if tx == nil {
			continue
		}
		base := tx.GetBase()
		if base == nil {
			continue
		}
		txID := tx.GetTxId()
		if txID == "" {
			continue
		}

		status := ""
		blockHeight := uint64(0)
		if receipt := receipts[txID]; receipt != nil {
			status = receipt.Status
			blockHeight = receipt.BlockHeight
		} else {
			receipt, err := s.dbManager.GetTxReceipt(txID)
			if err != nil || receipt == nil {
				continue
			}
			status = receipt.Status
			blockHeight = receipt.BlockHeight
		}
		                                                         
		if blockHeight != finalizedHeight {
			continue
		}
		if normalized, ok := normalizeReceiptStatus(status); ok {
			base.Status = normalized
			base.ExecutedHeight = blockHeight
		}
	}

	return cloned
}

func (s *RealBlockStore) Add(block *types.Block) (bool, error) {
	if block == nil {
		return false, fmt.Errorf("invalid block")
	}
	exactPayloadBytes := append([]byte(nil), block.CanonicalPayload...)
	exactPayload := len(exactPayloadBytes) > 0
	admissionBlockID := block.ID
	discardFallbackPayload := func() {
		                                                                        
		                                                                       
		                                                                    
		if !exactPayload {
			RemoveCachedBlock(admissionBlockID)
		}
	}
	                                                      
	s.mu.Lock()
	if IsTooFarFutureHeight(block.Header.Height, s.lastAcceptedHeight) {
		acceptedHeight := s.lastAcceptedHeight
		s.mu.Unlock()
		discardFallbackPayload()
		return false, fmt.Errorf("block height %d too far ahead of accepted %d (window %d)", block.Header.Height, acceptedHeight, MaxFutureBlockCacheWindow)
	}
	if _, exists := s.blockCache[block.ID]; exists {
		s.mu.Unlock()
		return false, nil
	}
	s.mu.Unlock()

	                                                                  
	                                                                                    
	var pbBlock *pb.Block
	hasFullData := false
	if exactPayload {
		var decoded pb.Block
		if err := pb.UnmarshalSanitize(block.CanonicalPayload, &decoded); err != nil {
			return false, fmt.Errorf("invalid canonical block payload: %w", err)
		}
		pbBlock = &decoded
		hasFullData = true
	} else {
		pbBlock, hasFullData = GetCachedBlock(block.ID)
	}
	if !exactPayload && (!hasFullData || !isCompleteBlockPayload(pbBlock)) && s.dbManager != nil {
		if dbBlock, err := s.dbManager.GetBlockByID(block.ID); err == nil && isCompleteBlockPayload(dbBlock) {
			pbBlock = dbBlock
			hasFullData = true
			CacheBlock(dbBlock)
		}
	}
	if !hasFullData || !isCompleteBlockPayload(pbBlock) {
		                                                
		logs.Debug("[RealBlockStore] Block %s rejected: no full data available (awaiting transaction body)", block.ID)
		return false, fmt.Errorf("block data incomplete: awaiting transaction body")
	}
	if sanitized, ok := sanitizeBlockTxsForCanonicalValidation(pbBlock); ok {
		pbBlock = sanitized
	}
	if pbBlock.BlockHash != block.ID {
		discardFallbackPayload()
		return false, fmt.Errorf("block payload ID mismatch: candidate=%s payload=%s", block.ID, pbBlock.BlockHash)
	}
	if err := ValidateBlockCommitment(pbBlock); err != nil {
		discardFallbackPayload()
		return false, fmt.Errorf("block commitment validation failed: %w", err)
	}
	canonicalBlock := s.convertDBBlockToTypes(pbBlock)
	if canonicalBlock == nil {
		discardFallbackPayload()
		return false, fmt.Errorf("failed to derive canonical consensus block")
	}
	if exactPayload {
		canonicalBlock.CanonicalPayload = exactPayloadBytes
	}
	block = canonicalBlock

	                                                                                    
	if err := s.lightweightValidateBlock(pbBlock); err != nil {
		discardFallbackPayload()
		logs.Warn("[RealBlockStore] Block %s failed lightweight validation: %v", block.ID, err)
		return false, fmt.Errorf("lightweight validation failed: %w", err)
	}

	                                                                                    
	                                                                                   
	                                                                                       
	for {
		s.mu.RLock()
		validatedHeight := s.lastAcceptedHeight
		validatedID := ""
		if s.lastAccepted != nil {
			validatedID = s.lastAccepted.ID
		}
		s.mu.RUnlock()

		if err := s.ValidateCandidatePreStateRoot(block); err != nil {
			discardFallbackPayload()
			logs.Warn("[RealBlockStore] Block %s failed pre-state root admission: %v", block.ID, err)
			return false, err
		}

		s.mu.Lock()
		currentID := ""
		if s.lastAccepted != nil {
			currentID = s.lastAccepted.ID
		}
		if validatedHeight == s.lastAcceptedHeight && validatedID == currentID {
			break
		}
		s.mu.Unlock()
	}

	                                                                     
	if IsTooFarFutureHeight(block.Header.Height, s.lastAcceptedHeight) {
		acceptedHeight := s.lastAcceptedHeight
		s.mu.Unlock()
		discardFallbackPayload()
		return false, fmt.Errorf("block height %d too far ahead of accepted %d (window %d)", block.Header.Height, acceptedHeight, MaxFutureBlockCacheWindow)
	}
	                                                                          
	if _, exists := s.blockCache[block.ID]; exists {
		s.mu.Unlock()
		return false, nil
	}
	                                                                    
	if err := s.validateBlock(block); err != nil {
		if isMissingParentBlockError(err) {
			if len(block.CanonicalPayload) == 0 {
				if raw, marshalErr := proto.Marshal(pbBlock); marshalErr == nil {
					block.CanonicalPayload = raw
				}
			}
			s.queueOrphanLocked(block)
		}
		s.mu.Unlock()
		discardFallbackPayload()
		return false, err
	}
	if !SealCachedBlock(pbBlock) {
		s.mu.Unlock()
		discardFallbackPayload()
		return false, fmt.Errorf("failed to seal validated block payload")
	}
	                                                                            
	                                                                        
	block.CanonicalPayload = nil
	s.removeOrphanLocked(block.ID)
	s.compactOrphanOrderLocked()
	s.blockCache[block.ID] = block
	s.heightIndex[block.Header.Height] = append(s.heightIndex[block.Header.Height], block)
	if block.Header.Height > s.maxHeight {
		s.maxHeight = block.Header.Height
	}
	s.mu.Unlock()

	logs.Debug("[RealBlockStore] Block %s passed commitment and lightweight validation", block.ID)

	                                                       
	                                                                                           
	                                                                                                 
	                                                                                        

	logs.Debug("[RealBlockStore] Added block %s at height %d", block.ID, block.Header.Height)

	return true, nil
}

                                                                               
                                                                                
                                                                               
                                                                               
func (s *RealBlockStore) PrevalidateDeferredLiveBlock(block *types.Block, acceptedHeight uint64) error {
	if block == nil || block.ID == "" {
		return fmt.Errorf("invalid live block")
	}

	var pbBlock *pb.Block
	if len(block.CanonicalPayload) > 0 {
		var decoded pb.Block
		if err := pb.UnmarshalSanitize(block.CanonicalPayload, &decoded); err != nil {
			return fmt.Errorf("invalid canonical block payload: %w", err)
		}
		pbBlock = &decoded
	} else if cached, ok := GetCachedBlock(block.ID); ok {
		pbBlock = cached
	}
	if !isCompleteBlockPayload(pbBlock) {
		return fmt.Errorf("block data incomplete: awaiting transaction body")
	}
	if sanitized, ok := sanitizeBlockTxsForCanonicalValidation(pbBlock); ok {
		pbBlock = sanitized
	}
	if pbBlock.BlockHash != block.ID {
		return fmt.Errorf("block payload ID mismatch: candidate=%s payload=%s", block.ID, pbBlock.BlockHash)
	}
	if err := ValidateBlockCommitment(pbBlock); err != nil {
		return fmt.Errorf("block commitment validation failed: %w", err)
	}
	if block.Header.ComputeIDForChain(pb.GetDefaultChainID()) != block.ID {
		return fmt.Errorf("live consensus header does not match block ID %s", block.ID)
	}
	if err := s.lightweightValidateBlock(pbBlock); err != nil {
		return fmt.Errorf("lightweight validation failed: %w", err)
	}

	canonicalBlock, err := (&ConsensusAdapter{}).DBBlockToConsensus(pbBlock)
	if err != nil {
		return fmt.Errorf("decode deferred live block: %w", err)
	}
	requireEligibility := canonicalBlock.Header.Height == acceptedHeight+1
	if err := validateProposerVRF(s.dbManager, s.windowConfig, canonicalBlock, requireEligibility); err != nil {
		return fmt.Errorf("VRF validation failed: %w", err)
	}
	return nil
}

const (
	maxPendingParentOrphans            = 256
	defaultMaxPendingParentOrphanBytes = 8 * pb.MaxTotalSize
)

func isMissingParentBlockError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "parent block") && strings.Contains(err.Error(), "not found")
}

                                                                              
                                                                         
func (s *RealBlockStore) queueOrphanLocked(block *types.Block) {
	if block == nil || block.ID == "" || len(block.CanonicalPayload) == 0 {
		return
	}
	if s.orphanBlocks == nil {
		s.orphanBlocks = make(map[string]*types.Block)
	}
	payloadBytes := len(block.CanonicalPayload)
	byteLimit := s.orphanLimit
	if byteLimit <= 0 {
		byteLimit = defaultMaxPendingParentOrphanBytes
	}
	if payloadBytes > byteLimit {
		return
	}

	                                                                     
	                                                                  
	if _, exists := s.orphanBlocks[block.ID]; exists {
		s.removeOrphanLocked(block.ID)
	}
	for (len(s.orphanBlocks) >= maxPendingParentOrphans || s.orphanBytes > byteLimit-payloadBytes) && len(s.orphanOrder) > 0 {
		oldest := s.orphanOrder[0]
		s.orphanOrder = s.orphanOrder[1:]
		s.removeOrphanLocked(oldest)
	}
	if len(s.orphanBlocks) >= maxPendingParentOrphans || s.orphanBytes > byteLimit-payloadBytes {
		return
	}
	s.orphanBlocks[block.ID] = block
	s.orphanBytes += payloadBytes
	if !s.orphanOrderContainsLocked(block.ID) {
		s.orphanOrder = append(s.orphanOrder, block.ID)
	}
}

func (s *RealBlockStore) removeOrphanLocked(blockID string) {
	block, exists := s.orphanBlocks[blockID]
	if !exists {
		return
	}
	payloadBytes := 0
	if block != nil {
		payloadBytes = len(block.CanonicalPayload)
	}
	if payloadBytes >= s.orphanBytes {
		s.orphanBytes = 0
	} else {
		s.orphanBytes -= payloadBytes
	}
	delete(s.orphanBlocks, blockID)
}

func (s *RealBlockStore) orphanOrderContainsLocked(blockID string) bool {
	for _, queuedID := range s.orphanOrder {
		if queuedID == blockID {
			return true
		}
	}
	return false
}

func (s *RealBlockStore) compactOrphanOrderLocked() {
	if len(s.orphanOrder) == 0 {
		return
	}
	kept := s.orphanOrder[:0]
	for _, blockID := range s.orphanOrder {
		if _, exists := s.orphanBlocks[blockID]; exists {
			kept = append(kept, blockID)
		}
	}
	s.orphanOrder = kept
}

func (s *RealBlockStore) takeOrphansAfterFinalized(parentID string, finalizedHeight uint64) []*types.Block {
	s.mu.Lock()
	ready := make([]*types.Block, 0)
	for blockID, block := range s.orphanBlocks {
		if block == nil || block.Header.Height <= finalizedHeight {
			s.removeOrphanLocked(blockID)
			continue
		}
		if block.Header.ParentID == parentID && block.Header.Height == finalizedHeight+1 {
			ready = append(ready, block)
			s.removeOrphanLocked(blockID)
		}
	}
	s.compactOrphanOrderLocked()
	s.mu.Unlock()
	sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
	return ready
}

func (s *RealBlockStore) retryOrphansAfterFinalized(parentID string, finalizedHeight uint64) {
	for _, child := range s.takeOrphansAfterFinalized(parentID, finalizedHeight) {
		added, err := s.Add(child)
		if err != nil || !added {
			continue
		}
		logs.Debug("[RealBlockStore] Admitted reordered child block %s after parent %s finalized", child.ID, parentID)
		if s.events != nil {
			s.events.PublishAsync(types.BaseEvent{EventType: types.EventNewBlock, EventData: child})
		}
	}
}

func (s *RealBlockStore) scheduleOrphanRetry(parentID string, finalizedHeight uint64) {
	s.mu.RLock()
	hasOrphans := len(s.orphanBlocks) > 0
	s.mu.RUnlock()
	if hasOrphans {
		go s.retryOrphansAfterFinalized(parentID, finalizedHeight)
	}
}

                   
func (s *RealBlockStore) Get(id string) (*types.Block, bool) {
	s.mu.RLock()
	                           
	if block, exists := s.blockCache[id]; exists {
		defer s.mu.RUnlock()
		return block, true
	}
	s.mu.RUnlock()

	                             
	dbBlock, err := s.dbManager.GetBlockByID(id)
	if err != nil || dbBlock == nil {
		logs.Debug("[RealBlockStore] Block %s not found: %v", id, err)
		return nil, false
	}

	                       
	block := s.convertDBBlockToTypes(dbBlock)
	if block != nil {
		                                    
		s.mu.Lock()
		s.blockCache[id] = block
		s.mu.Unlock()
		return block, true
	}

	return nil, false
}

                                                
func (s *RealBlockStore) GetByHeight(height uint64) []*types.Block {
	s.mu.RLock()
	                     
	if blocks, exists := s.heightIndex[height]; exists {
		result := make([]*types.Block, len(blocks))
		copy(result, blocks)
		s.mu.RUnlock()
		return result
	}

	                                                                                            
	if height > s.maxHeight {
		s.mu.RUnlock()
		return []*types.Block{}
	}
	s.mu.RUnlock()

	                     
	dbBlock, err := s.dbManager.GetBlock(height)
	if err != nil || dbBlock == nil {
		return []*types.Block{}
	}

	                       
	block := s.convertDBBlockToTypes(dbBlock)
	if block != nil {
		s.mu.Lock()
		s.blockCache[block.ID] = block
		s.heightIndex[height] = []*types.Block{block}
		s.mu.Unlock()
		return []*types.Block{block}
	}

	return []*types.Block{}
}

                                              
func (s *RealBlockStore) GetLastAccepted() (string, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.lastAccepted != nil {
		return s.lastAccepted.ID, s.lastAcceptedHeight
	}
	                                                                            
	                                                                             
	                                                                       
	return "genesis", 0
}

                                                            
func (s *RealBlockStore) GetFinalizedAtHeight(height uint64) (*types.Block, bool) {
	s.mu.RLock()
	                  
	if block, exists := s.finalizedBlocks[height]; exists {
		s.mu.RUnlock()
		return block, true
	}
	acceptedHeight := s.lastAcceptedHeight
	s.mu.RUnlock()
	if height > acceptedHeight {
		return nil, false
	}

	                                                                            
	                                                                      
	dbBlock, err := s.loadFinalizedBlockFromDB(height)
	if err != nil || dbBlock == nil {
		return nil, false
	}

	block := s.convertDBBlockToTypes(dbBlock)
	if block != nil {
		s.mu.Lock()
		                                                                         
		                                                                       
		if height > s.lastAcceptedHeight {
			s.mu.Unlock()
			return nil, false
		}
		s.finalizedBlocks[height] = block
		s.mu.Unlock()
		return block, true
	}

	return nil, false
}

                                                                                       
func (s *RealBlockStore) GetBlocksFromHeight(from, to uint64) []*types.Block {
	if from > to {
		return nil
	}
	s.mu.RLock()
	acceptedHeight := s.lastAcceptedHeight
	s.mu.RUnlock()
	if from > acceptedHeight {
		return []*types.Block{}
	}
	if to > acceptedHeight {
		to = acceptedHeight
	}
	blocks := make([]*types.Block, 0)
	for h := from; ; h++ {
		if block, exists := s.GetFinalizedAtHeight(h); exists && block != nil {
			blocks = append(blocks, block)
		}
		if h == to {
			break
		}
	}
	return blocks
}

                                            
func (s *RealBlockStore) GetCurrentHeight() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	                                       
	if s.maxHeight > 0 {
		return s.maxHeight
	}

	                     
	height, _ := s.dbManager.GetLatestBlockHeight()
	return height
}

                                                   
                                                                                                         
                                                                              
                                                                       
                                                                      
func (s *RealBlockStore) InstallCertifiedAnchor(height uint64, blockID string, anchorBlock *pb.Block) error {
	cloned, ok := proto.Clone(anchorBlock).(*pb.Block)
	if !ok || cloned == nil {
		return fmt.Errorf("clone certified anchor returned %T", cloned)
	}
	if sanitized, changed := sanitizeBlockTxsForCanonicalValidation(cloned); changed {
		cloned = sanitized
	}
	anchor, err := validateCertifiedAnchorBlock(cloned, height, blockID)
	if err != nil {
		return err
	}
	if err := s.lightweightValidateBlock(cloned); err != nil {
		return fmt.Errorf("certified anchor transaction validation failed: %w", err)
	}

	s.mu.Lock()
	if finalized, exists := s.finalizedBlocks[height]; exists && finalized != nil && finalized.ID != blockID {
		s.mu.Unlock()
		return fmt.Errorf("height %d already finalized by %s", height, finalized.ID)
	}
	if s.lastAccepted != nil && height < s.lastAcceptedHeight {
		acceptedHeight := s.lastAcceptedHeight
		s.mu.Unlock()
		return fmt.Errorf("certified anchor height %d is behind accepted height %d", height, acceptedHeight)
	}
	if s.lastAccepted != nil && height == s.lastAcceptedHeight && s.lastAccepted.ID != blockID {
		acceptedID := s.lastAccepted.ID
		s.mu.Unlock()
		return fmt.Errorf("certified anchor conflicts with accepted block %s at height %d", acceptedID, height)
	}

	s.blockCache[blockID] = anchor
	s.heightIndex[height] = []*types.Block{anchor}
	s.finalizedBlocks[height] = anchor
	s.lastAccepted = anchor
	s.lastAcceptedHeight = height
	if height > s.maxHeight {
		s.maxHeight = height
	}
	s.mu.Unlock()

	                                                                        
	                                                                        
	SetBlockCacheAcceptedHeight(height)
	if !SealCachedBlock(cloned) {
		logs.Warn("[RealBlockStore] Certified anchor payload was not cached: height=%d block=%s", height, blockID)
	}
	if s.pool != nil {
		if expired := s.pool.RemoveExpiredExecutionGuardTxs(height); expired > 0 {
			logs.Debug("[RealBlockStore] Removed %d execution-guard txs expired by certified anchor height %d", expired, height)
		}
	}
	s.scheduleOrphanRetry(blockID, height)

	logs.Info("[RealBlockStore] Installed certified anchor: height=%d block=%s", height, blockID)
	return nil
}

func (s *RealBlockStore) SetFinalized(height uint64, blockID string) (err error) {
	                                                                                          
	traceStart := time.Now()
	finalizeLockAt := time.Now()
	s.finalizeMu.Lock()
	durFinalizeLock := time.Since(finalizeLockAt)
	blocktrace.Record(height, blockID, "RealBlockStore.finalizeMuWait", durFinalizeLock)
	var txCount int
	var durPrepare time.Duration
	var durUpdateFinalizedState time.Duration
	var durCleanup time.Duration
	defer func() {
		blocktrace.Flush(height, blockID, "RealBlockStore.SetFinalized", time.Since(traceStart), err, blocktrace.F("txs", txCount))
	}()
	defer s.finalizeMu.Unlock()

	                                                      
	prepareAt := time.Now()
	s.mu.Lock()

	                                                                              
	if finalized, ok := s.finalizedBlocks[height]; ok && finalized != nil {
		if finalized.ID == blockID {
			s.mu.Unlock()
			s.ensureFinalizationObserved(height, blockID)
			logs.Debug("[RealBlockStore] Skip duplicate finalization: height=%d block=%s", height, blockID)
			return ErrAlreadyFinalized
		}
		s.mu.Unlock()
		err := fmt.Errorf("height %d already finalized by %s", height, finalized.ID)
		logs.Error("[RealBlockStore] Cannot finalize block %s at height %d: %v", blockID, height, err)
		return err
	}

	block, exists := s.blockCache[blockID]
	if !exists {
		                             
		if dbBlock, err := s.dbManager.GetBlockByID(blockID); err == nil && dbBlock != nil {
			block = s.convertDBBlockToTypes(dbBlock)
			s.blockCache[blockID] = block
		}
	}

	if block == nil {
		s.mu.Unlock()
		err := fmt.Errorf("block not found")
		logs.Error("[RealBlockStore] Cannot finalize block %s: %v", blockID, err)
		return err
	}

	                                             
	                                                                                              
	if height > 0 {
		parentBlock, parentExists := s.finalizedBlocks[height-1]
		if !parentExists {
			s.mu.Unlock()
			err := fmt.Errorf("parent at height %d not finalized", height-1)
			logs.Error("[RealBlockStore] Cannot finalize block %s at height %d: %v", blockID, height, err)
			return err
		}
		if block.Header.ParentID != parentBlock.ID {
			s.mu.Unlock()
			err := fmt.Errorf("parent mismatch (expected %s, got %s)", parentBlock.ID, block.Header.ParentID)
			logs.Error("[RealBlockStore] Cannot finalize block %s at height %d: %v", blockID, height, err)
			return err
		}
	}

	                                                      
	events := s.events
	s.mu.Unlock()
	if block.Header.Height != height {
		return fmt.Errorf("finalized block height mismatch: requested=%d block=%d", height, block.Header.Height)
	}
	if err := s.validateCandidatePreStateRootLocked(block); err != nil {
		logs.Warn("[RealBlockStore] Block %s failed pre-state root finalization gate: %v", block.ID, err)
		if errors.Is(err, ErrInvalidCandidatePreStateRoot) {
			s.DiscardCandidate(height, block.ID)
		}
		return err
	}
	if height > 0 {
		                                                                      
		                                                                        
		                                                                     
		if validateErr := validateProposerVRF(s.dbManager, s.windowConfig, block, true); validateErr != nil {
			return fmt.Errorf("finalized proposer VRF validation failed: %w", validateErr)
		}
	}
	durPrepare = time.Since(prepareAt)
	blocktrace.Record(height, blockID, "RealBlockStore.prepare", durPrepare)

	                                                                        
	                                                                                                              
	commitStart := time.Now()
	var (
		durLoadPB        time.Duration
		durVMCommit      time.Duration
		durVMCommitWait  time.Duration
		durPoolRemove    time.Duration
		durMaterialize   time.Duration
		durSaveBlock     time.Duration
		durFinalizeFlush time.Duration
		cacheHitPB       bool
		loadedPBFromDB   bool
	)

	                                                   
	loadPBAt := time.Now()
	pbBlock, exists := GetCachedBlock(block.ID)
	cacheHitPB = exists && isCompleteBlockPayload(pbBlock)
	if (!exists || !isCompleteBlockPayload(pbBlock)) && s.dbManager != nil {
		if dbBlock, err := s.dbManager.GetBlockByID(blockID); err == nil && isCompleteBlockPayload(dbBlock) {
			pbBlock = dbBlock
			exists = true
			loadedPBFromDB = true
			CacheBlock(dbBlock)
		}
	}
	durLoadPB += time.Since(loadPBAt)
	blocktrace.Record(height, blockID, "RealBlockStore.loadPB", durLoadPB, blocktrace.F("cacheHit", cacheHitPB), blocktrace.F("loadedFromDB", loadedPBFromDB))

	if exists && isCompleteBlockPayload(pbBlock) {
		if sanitized, ok := sanitizeBlockTxsForCanonicalValidation(pbBlock); ok {
			pbBlock = sanitized
		}
		if pbBlock.BlockHash != block.ID {
			return fmt.Errorf("finalized block payload ID mismatch: candidate=%s payload=%s", block.ID, pbBlock.BlockHash)
		}
		if err := ValidateBlockCommitment(pbBlock); err != nil {
			return fmt.Errorf("finalized block commitment validation failed: %w", err)
		}
		if err := s.lightweightValidateBlock(pbBlock); err != nil {
			return fmt.Errorf("finalized block transaction validation failed: %w", err)
		}
		releaseCommitSlot, waitCommitSlot := acquireFinalizedVMCommitSlot()
		durVMCommitWait += waitCommitSlot
		blocktrace.Record(height, blockID, "RealBlockStore.vmCommitSlotWait", durVMCommitWait)
		if waitCommitSlot > 500*time.Millisecond {
			logs.Info("[RealBlockStore] VM commit waiting for global slot: block=%s height=%d wait=%v", block.ID, height, waitCommitSlot)
		}
		vmCommitAt := time.Now()
		commitErr := func() error {
			defer releaseCommitSlot()
			return s.vmExecutor.CommitFinalizedBlock(pbBlock)
		}()
		durVMCommit += time.Since(vmCommitAt)
		blocktrace.Record(height, blockID, "RealBlockStore.vmCommit", durVMCommit)
		if commitErr != nil {
			logs.Error("[RealBlockStore] VM CommitFinalizedBlock failed for block %s: %v", block.ID, commitErr)
			return fmt.Errorf("vm commit failed: %w", commitErr)
		}
		txCount = len(pbBlock.Body)
		logs.Info("[RealBlockStore] VM committed finalized block %s with %d txs at height %d",
			block.ID, txCount, height)

		                                       
		                                                                                             
		                                                                        
		poolRemoveAt := time.Now()
		appliedTxIDs := make([]string, 0, len(pbBlock.Body))
		for _, tx := range pbBlock.Body {
			if base := tx.GetBase(); base != nil {
				appliedTxIDs = append(appliedTxIDs, base.TxId)
			}
		}
		s.pool.RemoveAppliedAnyTxs(appliedTxIDs)
		durPoolRemove += time.Since(poolRemoveAt)
		blocktrace.Record(height, blockID, "RealBlockStore.poolRemove", durPoolRemove, blocktrace.F("txs", len(appliedTxIDs)))

		                                                                                       
		                                                                                                          
		materializeAt := time.Now()
		commitReceipts := s.vmExecutor.TakeCommittedReceipts(pbBlock.BlockHash)
		finalizedPBBlock := s.materializeFinalizedBlock(pbBlock, commitReceipts)
		durMaterialize += time.Since(materializeAt)
		blocktrace.Record(height, blockID, "RealBlockStore.materializeFinalizedBlock", durMaterialize, blocktrace.F("receipts", len(commitReceipts)))

		saveBlockAt := time.Now()
		if err := s.dbManager.SaveBlock(finalizedPBBlock); err != nil {
			logs.Error("[RealBlockStore] Failed to save finalized block %s: %v", block.ID, err)
			return fmt.Errorf("save finalized block failed: %w", err)
		}
		                                                                         
		                                                                         
		                                              
		s.dbManager.ObserveBlockArchiveHeight(height)
		durSaveBlock += time.Since(saveBlockAt)
		blocktrace.Record(height, blockID, "RealBlockStore.saveBlock", durSaveBlock)
		ReplaceCachedBlock(finalizedPBBlock)
		                                                                                                 
		s.dbManager.ObserveFrostArchiveHeight(height)
		finalizeFlushAt := time.Now()
		s.maybeForceFlushAfterFinalize(height, blockID)
		durFinalizeFlush += time.Since(finalizeFlushAt)
		blocktrace.Record(height, blockID, "RealBlockStore.finalizeFlush", durFinalizeFlush)
	} else {
		                                                                                                     
		if _, err := s.dbManager.GetBlockByID(blockID); err != nil {
			                                                                   
			                                                                  
			forceFlushAt := time.Now()
			if flushErr := s.dbManager.ForceFlushTrace(height, blockID); flushErr != nil {
				logs.Warn("[RealBlockStore] ForceFlush before retry failed: %v", flushErr)
			}
			blocktrace.Record(height, blockID, "RealBlockStore.fallbackForceFlush", time.Since(forceFlushAt))
			if _, retryErr := s.dbManager.GetBlockByID(blockID); retryErr != nil {
				logs.Error("[RealBlockStore] Cannot finalize block %s at height %d: missing cached pb.Block and not persisted (after flush): %v",
					blockID, height, retryErr)
				return fmt.Errorf("no cached pb.Block and block not persisted: %w", retryErr)
			}
			logs.Info("[RealBlockStore] Block %s found after ForceFlush (was in async write queue)", blockID)
		}
		if block.Header.Height > 0 {
			logs.Debug("[RealBlockStore] No cached pb.Block for %s; finalized using already-persisted block", block.ID)
		}
	}
	                                                                           
	                                                                       
	s.dbManager.ObserveFrostArchiveHeight(height)
	s.dbManager.ObserveBlockArchiveHeight(height)

	if duration := time.Since(commitStart); duration > 200*time.Millisecond {
		logs.Info(
			"[RealBlockStore] SLOW Finalization: block=%s, txs=%d, duration=%v, loadPB=%v, vmCommitWait=%v, vmCommit=%v, poolRemove=%v, materialize=%v, saveBlock=%v, finalizeFlush=%v, cacheHit=%t, loadedFromDB=%t",
			block.ID, txCount, duration, durLoadPB, durVMCommitWait, durVMCommit, durPoolRemove, durMaterialize, durSaveBlock, durFinalizeFlush, cacheHitPB, loadedPBFromDB,
		)
	}

	                                                               
	stateUpdateAt := time.Now()
	s.mu.Lock()
	s.finalizedBlocks[height] = block
	s.lastAccepted = block
	s.lastAcceptedHeight = height

	                              
	newBlocks := make([]*types.Block, 0, 1)
	for _, b := range s.heightIndex[height] {
		if b.ID == blockID {
			newBlocks = append(newBlocks, b)
		} else {
			delete(s.blockCache, b.ID)
		}
	}
	s.heightIndex[height] = newBlocks
	s.mu.Unlock()
	if cleaned := SetBlockCacheAcceptedHeight(height); cleaned > 0 {
		logs.Debug("[RealBlockStore] Cleaned %d future blocks above accepted window at height %d", cleaned, height)
	}
	s.ensureFinalizationObserved(height, blockID)
	if s.pool != nil {
		if expired := s.pool.RemoveExpiredExecutionGuardTxs(height); expired > 0 {
			logs.Debug("[RealBlockStore] Removed %d execution-guard txs expired at finalized height %d", expired, height)
		}
	}
	s.scheduleOrphanRetry(blockID, height)
	durUpdateFinalizedState = time.Since(stateUpdateAt)
	blocktrace.Record(height, blockID, "RealBlockStore.updateFinalizedState", durUpdateFinalizedState)

	logs.Info("[RealBlockStore] Finalized block %s at height %d", blockID, height)

	                                                               
	                                     
	cleanupAt := time.Now()
	const keepRecentHeights = 20
	if height > keepRecentHeights {
		cleanupHeight := height - keepRecentHeights

		                               
		cleanedCount := CleanupBlockCacheBelowHeight(cleanupHeight)
		if cleanedCount > 0 {
			logs.Debug("[RealBlockStore] Cleaned %d blocks from global cache below height %d", cleanedCount, cleanupHeight)
		}

		                                       
		s.mu.Lock()
		s.cleanupOldData(cleanupHeight)
		s.mu.Unlock()

		                   
		if s.vmExecutor != nil {
			s.vmExecutor.CleanupCache(cleanupHeight)
		}
	}
	durCleanup = time.Since(cleanupAt)
	blocktrace.Record(height, blockID, "RealBlockStore.cleanup", durCleanup)

	diskPruneAt := time.Now()
	diskPruneScheduled := s.triggerDiskPrune(height, blockID)
	blocktrace.Record(height, blockID, "RealBlockStore.diskPrune", time.Since(diskPruneAt), blocktrace.F("async", true), blocktrace.F("scheduled", diskPruneScheduled))

	var vmBuildSpec time.Duration
	var vmApplyAndFlush time.Duration
	var vmErr string
	if s.vmExecutor != nil {
		if timing, ok := s.vmExecutor.GetCommitTiming(blockID); ok {
			vmBuildSpec = timing.BuildSpec
			vmApplyAndFlush = timing.ApplyAndFlush
			vmErr = timing.Error
		}
	}
	total := time.Since(traceStart)
	s.rememberBlockTiming(BlockTimingSnapshot{
		Height:                 height,
		BlockID:                blockID,
		TxCount:                txCount,
		RecordedAtUnixMs:       time.Now().UnixMilli(),
		TotalMs:                durationMs(total),
		ConsensusMs:            finalizationOverheadMs(total, durVMCommit),
		PrepareMs:              durationMs(durPrepare),
		LoadBlockMs:            durationMs(durLoadPB),
		VMCommitWaitMs:         durationMs(durVMCommitWait),
		VMCommitMs:             durationMs(durVMCommit),
		VMBuildSpecMs:          durationMs(vmBuildSpec),
		VMApplyAndFlushMs:      durationMs(vmApplyAndFlush),
		PoolRemoveMs:           durationMs(durPoolRemove),
		MaterializeBlockMs:     durationMs(durMaterialize),
		SaveBlockMs:            durationMs(durSaveBlock),
		FinalizeFlushMs:        durationMs(durFinalizeFlush),
		UpdateFinalizedStateMs: durationMs(durUpdateFinalizedState),
		CleanupMs:              durationMs(durCleanup),
		CacheHit:               cacheHitPB,
		LoadedFromDB:           loadedPBFromDB,
		Error:                  vmErr,
		Steps:                  blocktrace.Snapshot(height, blockID),
	})

	                                                                                                                         
	if height > 0 && height%50 == 0 {
		go func(h uint64) {
			logs.Info("[RealBlockStore] Starting async compaction for order indexes at height %d...", h)
			start := time.Now()
			if err := s.dbManager.CompactOrderIndexes(); err != nil {
				logs.Warn("[RealBlockStore] Async compaction for order indexes failed at height %d: %v", h, err)
			} else {
				logs.Info("[RealBlockStore] Async compaction for order indexes finished at height %d, cost: %v", h, time.Since(start))
			}

			logs.Info("[RealBlockStore] Starting async compaction for state orderstates at height %d...", h)
			start = time.Now()
			if err := s.dbManager.CompactStateOrderStates(); err != nil {
				logs.Warn("[RealBlockStore] Async compaction for state orderstates failed at height %d: %v", h, err)
			} else {
				logs.Info("[RealBlockStore] Async compaction for state orderstates finished at height %d, cost: %v", h, time.Since(start))
			}
		}(height)
	}

	                                          
	if events != nil {
		events.PublishAsync(types.BaseEvent{
			EventType: types.EventBlockFinalized,
			EventData: block,
		})
	}
	return nil
}

                     

func (s *RealBlockStore) triggerDiskPrune(height uint64, blockID string) bool {
	if s == nil || s.dbManager == nil {
		return false
	}
	if !s.diskPruneRunning.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		start := time.Now()
		defer s.diskPruneRunning.Store(false)
		diskUsage, err := s.dbManager.DiskUsage()
		if err != nil {
			logs.Warn("[RealBlockStore] async disk pressure sampling failed at height %d: %v", height, err)
			return
		}
		prune, err := s.dbManager.PruneBlockDataWithDiskUsage(height, diskUsage)
		if err != nil {
			logs.Warn("[RealBlockStore] async disk pressure block prune failed at height %d: %v", height, err)
		} else if prune.PrunedHeights > 0 {
			logs.Info("[RealBlockStore] async pruned old block archive data: triggerHeight=%d block=%s free=%.2f%% scanned=%d pruned=%d next=%d deletedKeys=%d blocks=%d txraw=%d duration=%v",
				height, blockID, prune.DiskFreePct, prune.ScannedHeights, prune.PrunedHeights, prune.NextCursor, prune.DeletedKeys, prune.DeletedBlocks, prune.DeletedTxRaw, time.Since(start))
		}
		queryStart := time.Now()
		queryPrune, err := s.dbManager.PruneQueryDataWithDiskUsage(height, diskUsage)
		if err != nil {
			logs.Warn("[RealBlockStore] async disk pressure query prune failed at height %d: %v", height, err)
			return
		}
		if queryPrune.PrunedHeights > 0 || queryPrune.ArchiveAligned {
			logs.Info("[RealBlockStore] async pruned old query history data: triggerHeight=%d block=%s free=%.2f%% maxHeight=%d start=%d next=%d scannedHeights=%d prunedHeights=%d skippedHeights=%d archiveAligned=%t scannedKeys=%d deletedKeys=%d families=%s compacted=%v duration=%v",
				height, blockID, queryPrune.DiskFreePct, queryPrune.MaxPruneHeight, queryPrune.StartCursor, queryPrune.NextCursor, queryPrune.ScannedHeights, queryPrune.PrunedHeights, queryPrune.SkippedHeights, queryPrune.ArchiveAligned, queryPrune.ScannedKeys, queryPrune.DeletedKeys, queryPrune.FamilySummary(), queryPrune.CompactedFamilies, time.Since(queryStart))
		}
		planningPrune, planningErr := s.dbManager.PruneFrostPlanningLogsWithDiskUsage(height, diskUsage)
		if planningErr != nil {
			logs.Warn("[RealBlockStore] async FROST planning-log prune failed at height %d: %v", height, planningErr)
		} else if planningPrune.DeletedRows > 0 {
			logs.Info("[RealBlockStore] async pruned terminal FROST planning logs: triggerHeight=%d free=%.2f%% scanned=%d deleted=%d malformed=%d wrapped=%t compacted=%t",
				height, planningPrune.DiskFreePct, planningPrune.ScannedRows, planningPrune.DeletedRows, planningPrune.MalformedRows, planningPrune.Wrapped, planningPrune.Compacted)
		}
		artifactPrune, err := s.dbManager.PruneFrostArtifactsWithDiskUsage(height, diskUsage)
		if err != nil {
			logs.Warn("[RealBlockStore] async FROST artifact prune failed at height %d: %v", height, err)
			return
		}
		if artifactPrune.DeletedRecords > 0 {
			logs.Info("[RealBlockStore] async pruned FROST artifacts: triggerHeight=%d free=%.2f%% scannedHeights=%d records=%d next=%d", height, artifactPrune.DiskFreePct, artifactPrune.ScannedHeights, artifactPrune.DeletedRecords, artifactPrune.NextCursor)
		}
	}()
	return true
}

func (s *RealBlockStore) validateBlock(block *types.Block) error {
	if block == nil || block.ID == "" {
		return fmt.Errorf("invalid block")
	}
	if block.Header.Height == 0 && block.ID != "genesis" {
		return fmt.Errorf("invalid genesis block")
	}
	if block.Header.Height > 0 && block.Header.ParentID == "" {
		return fmt.Errorf("non-genesis block must have parent")
	}
	if reportsTag, ok := extractBoundLivenessReportsTag(block.ID); ok {
		expected := types.LivenessReportsTag(block.Header.SampledReports, block.Header.OfflineReports, block.Header.OnlineReports)
		if reportsTag != expected {
			return fmt.Errorf("liveness reports tag mismatch: id=%s header=%s", reportsTag, expected)
		}
	}

	                                                                            
	                                                                          
	                                                                          
	                                         
	if block.Header.Height > 0 {
		if err := s.validateVRF(block); err != nil {
			return fmt.Errorf("VRF validation failed: %w", err)
		}
	}

	                                                      
	                                                                               
	if block.Header.Height > 0 {
		parent, exists := s.blockCache[block.Header.ParentID]
		if !exists {
			                           
			if dbBlock, err := s.dbManager.GetBlockByID(block.Header.ParentID); err == nil && dbBlock != nil {
				parent = s.convertDBBlockToTypes(dbBlock)
				if parent != nil {
					s.blockCache[block.Header.ParentID] = parent
					exists = true
				}
			}
		}

		if !exists {
			logs.Warn("[RealBlockStore] Block %s rejected: parent %s not found locally", block.ID, block.Header.ParentID)
			return fmt.Errorf("parent block %s not found", block.Header.ParentID)
		}

		                              
		if parent.Header.Height != block.Header.Height-1 {
			logs.Warn("[RealBlockStore] Block %s rejected: parent height mismatch (expected %d, got %d)",
				block.ID, block.Header.Height-1, parent.Header.Height)
			return fmt.Errorf("parent height mismatch: expected %d, got %d", block.Header.Height-1, parent.Header.Height)
		}
	}

	return nil
}

func extractBoundLivenessReportsTag(blockID string) (string, bool) {
	wIdx := strings.LastIndex(blockID, "-w")
	if wIdx < 0 {
		return "", false
	}
	afterWindow := blockID[wIdx+2:]
	firstDash := strings.Index(afterWindow, "-")
	if firstDash < 0 || firstDash+1 >= len(afterWindow) {
		return "", false
	}
	parts := strings.Split(afterWindow[firstDash+1:], "-")
	if len(parts) < 3 {
		return "", false
	}
	tag := parts[len(parts)-1]
	return tag, tag != ""
}

                           
func (s *RealBlockStore) validateVRF(block *types.Block) error {
	if block == nil {
		return fmt.Errorf("missing block")
	}
	requireEligibility := s.lastAccepted != nil &&
		block.Header.Height == s.lastAcceptedHeight+1 &&
		block.Header.ParentID == s.lastAccepted.ID
	if err := validateProposerVRF(s.dbManager, s.windowConfig, block, requireEligibility); err != nil {
		logs.Warn("[RealBlockStore] VRF/proposer validation failed for block %s: %v", block.ID, err)
		return err
	}

	logs.Debug("[RealBlockStore] VRF/proposer validation passed for block %s", block.ID)
	return nil
}

                                                                      
                                                                                         
                                                                                  
  
                                                                                                            
                                                                                                                 
                                                                                                                         
func (s *RealBlockStore) lightweightValidateBlock(block *pb.Block) error {
	if block == nil || block.Header == nil {
		return fmt.Errorf("nil block or header")
	}

	chainID := pb.GetDefaultChainID()
	seenTxIDs := make(map[string]struct{}, len(block.Body))
	for i, tx := range block.Body {
		if tx == nil {
			continue
		}

		                                    
		kind, err := vm.DefaultKindFn(tx)
		if err != nil {
			return fmt.Errorf("tx %d has invalid structure: %v", i, err)
		}
		_ = kind

		                                                                       
		                                                                         
		                                                                    
		if err := pb.ValidateTxExecutionGuard(tx, block.Header.Height, keys.MinerAddress(block.Header.Miner)); err != nil {
			return fmt.Errorf("tx %d execution guard rejected: %v", i, err)
		}

		                             
		if base := tx.GetBase(); base != nil && base.TxId != "" {
			if _, exists := seenTxIDs[base.TxId]; exists {
				return fmt.Errorf("duplicate tx_id in block: %s", base.TxId)
			}
			seenTxIDs[base.TxId] = struct{}{}
		}

		                                                                                                      
		if chainID != 0 {
			if err := txverify.Verify(tx, chainID); err != nil {
				return fmt.Errorf("tx %d rejected: %v", i, err)
			}
		}
	}
	if err := validateBlockProposerSignature(block); err != nil {
		return err
	}

	return nil
}

func (s *RealBlockStore) saveBlockToDB(block *types.Block) {
	                                    
	if cachedBlock, exists := GetCachedBlock(block.ID); exists {
		                              
		if err := s.dbManager.SaveBlock(cachedBlock); err != nil {
			logs.Error("[RealBlockStore] Failed to save block to DB: %v", err)
		} else {
			logs.Debug("[RealBlockStore] Saved block %s to DB", block.ID)
		}
	} else {
		miner := block.Header.Miner
		if miner == "" {
			miner = fmt.Sprintf(keys.KeyNode()+"%s", block.Header.Proposer)
		}
		                                 
		dbBlock := &pb.Block{
			BlockHash: block.ID,
			Header: &pb.BlockHeader{
				Height:        block.Header.Height,
				PrevBlockHash: block.Header.ParentID,
				Miner:         miner,
				Window:        int32(block.Header.Window),
				VrfProof:      block.Header.VRFProof,
				VrfOutput:     block.Header.VRFOutput,
				PublicKey:     block.Header.PublicKey,
			},
		}
		if err := s.dbManager.SaveBlock(dbBlock); err != nil {
			logs.Error("[RealBlockStore] Failed to save simple block to DB: %v", err)
		} else {
			logs.Debug("[RealBlockStore] Saved simple block %s to DB", block.ID)
		}
	}
}

                                                                            
                                                                  
func (s *RealBlockStore) RecoveryError() error {
	if s == nil {
		return fmt.Errorf("real block store is nil")
	}
	return s.recoveryErr
}

func (s *RealBlockStore) loadFinalizedBlockFromDB(height uint64) (*pb.Block, error) {
	stateID, stateIDExists, err := s.dbManager.FinalizedStateBlockID(height)
	if err != nil {
		return nil, fmt.Errorf("read StateDB block identity at height %d: %w", height, err)
	}
	runtimeID := ""
	if raw, readErr := s.dbManager.GetKV(keys.KeyVMCommitHeight(height)); readErr == nil {
		runtimeID = string(raw)
	}
	if stateIDExists && runtimeID != "" && stateID != runtimeID {
		return nil, fmt.Errorf("finalized block identity conflict at height %d: state=%s runtime=%s", height, stateID, runtimeID)
	}
	blockID := stateID
	if blockID == "" {
		blockID = runtimeID
	}
	if blockID != "" {
		block, loadErr := s.dbManager.GetBlockByID(blockID)
		if loadErr != nil {
			return nil, fmt.Errorf("load finalized block %s at height %d: %w", blockID, height, loadErr)
		}
		if block == nil {
			return nil, fmt.Errorf("finalized block %s at height %d is nil", blockID, height)
		}
		if block.Header == nil || block.Header.Height != height {
			return nil, fmt.Errorf("finalized block %s has wrong height at %d", blockID, height)
		}
		return block, nil
	}
	                                                                            
	                                         
	return s.dbManager.GetBlock(height)
}

func (s *RealBlockStore) loadFromDB() error {
	                                                                             
	heightFromLatest := uint64(0)
	hasLatestKey := false
	if h, err := s.dbManager.GetLatestBlockHeight(); err == nil {
		heightFromLatest = h
		hasLatestKey = true
	}

	                                                                   
	heightFromScan, hasScannedHeight := s.findMaxPersistedHeightKey()

	                                                                         
	effectiveTip := uint64(0)
	hasPersistedTip := false
	if hasLatestKey {
		effectiveTip = heightFromLatest
		hasPersistedTip = true
	}
	if hasScannedHeight && (!hasPersistedTip || heightFromScan > effectiveTip) {
		if hasLatestKey && heightFromScan != heightFromLatest {
			logs.Warn("[RealBlockStore] latest height key=%d, but scanned max persisted height=%d, using scanned tip",
				heightFromLatest, heightFromScan)
		}
		effectiveTip = heightFromScan
		hasPersistedTip = true
	}

	finalizedStateHeight, hasFinalizedStateHeight, err := s.dbManager.GetFinalizedStateHeight()
	if err != nil {
		return fmt.Errorf("read finalized StateDB height: %w", err)
	}
	                                                                     
	                                                                           
	                                                                          
	                                                                             
	if hasFinalizedStateHeight && (!hasPersistedTip || finalizedStateHeight > effectiveTip) {
		recovered, recoverErr := s.recoverPreparedCertifiedAnchor(finalizedStateHeight)
		if recoverErr != nil {
			return fmt.Errorf("recover certified snapshot anchor at height %d: %w", finalizedStateHeight, recoverErr)
		}
		if recovered {
			effectiveTip = finalizedStateHeight
			hasPersistedTip = true
			logs.Info("[RealBlockStore] Recovered certified snapshot anchor at height %d", finalizedStateHeight)
		}
	}
	if !hasPersistedTip {
		if hasFinalizedStateHeight {
			return fmt.Errorf("StateDB finalized height %d has no persisted block", finalizedStateHeight)
		}
		                                                         
		if s.lastAccepted != nil {
			s.saveBlockToDB(s.lastAccepted)
		}
		logs.Info("[RealBlockStore] No persisted tip found, initialized with genesis")
		return nil
	}
	recoveryTip := effectiveTip
	if hasFinalizedStateHeight {
		if finalizedStateHeight > effectiveTip {
			return fmt.Errorf("StateDB finalized height %d is ahead of persisted block height %d", finalizedStateHeight, effectiveTip)
		}
		recoveryTip = finalizedStateHeight
		if effectiveTip > recoveryTip {
			logs.Warn("[RealBlockStore] ignoring persisted candidate blocks above finalized StateDB height: finalized=%d persistedMax=%d",
				recoveryTip, effectiveTip)
		}
	}

	                                                                             
	                                                                          
	startHeight := uint64(0)
	if recoveryTip > 100 {
		startHeight = recoveryTip - 100
	}

	var loadedTip *types.Block
	var loadedTipHeight uint64

	for h := startHeight; h <= recoveryTip; h++ {
		dbBlock, loadErr := s.loadFinalizedBlockFromDB(h)
		if h == recoveryTip && (loadErr != nil || dbBlock == nil) && hasFinalizedStateHeight {
			recovered, recoverErr := s.recoverPreparedCertifiedAnchor(finalizedStateHeight)
			if recoverErr != nil {
				return fmt.Errorf("recover missing certified snapshot anchor at height %d: %w", finalizedStateHeight, recoverErr)
			}
			if recovered {
				dbBlock, loadErr = s.loadFinalizedBlockFromDB(h)
			}
		}
		if loadErr == nil && dbBlock != nil {
			block := s.convertDBBlockToTypes(dbBlock)
			if block == nil {
				continue
			}
			s.blockCache[block.ID] = block
			s.heightIndex[h] = []*types.Block{block}
			s.finalizedBlocks[h] = block                                     

			if loadedTip == nil || h > loadedTipHeight {
				loadedTip = block
				loadedTipHeight = h
			}
		} else if h == recoveryTip {
			if loadErr != nil {
				return fmt.Errorf("load finalized recovery tip at height %d: %w", h, loadErr)
			}
			return fmt.Errorf("finalized recovery tip at height %d is nil", h)
		}
	}

	if loadedTip != nil {
		s.lastAccepted = loadedTip
		s.lastAcceptedHeight = loadedTipHeight
		s.maxHeight = loadedTipHeight

		logs.Info("[RealBlockStore] Loaded finalized blocks from DB, tip height: %d (latest key: %d, scanned max: %d)",
			loadedTipHeight, heightFromLatest, heightFromScan)
		return nil
	}

	                                                                                   
	s.maxHeight = 0
	s.lastAcceptedHeight = 0
	if genesis, exists := s.blockCache["genesis"]; exists {
		s.lastAccepted = genesis
		s.finalizedBlocks[0] = genesis
	}
	if _, err := s.dbManager.GetBlock(0); err != nil && s.lastAccepted != nil {
		s.saveBlockToDB(s.lastAccepted)
	}
	logs.Warn("[RealBlockStore] persisted tip metadata exists but no blocks loadable (latest=%d, scanned=%d), fallback to genesis",
		heightFromLatest, heightFromScan)
	return nil
}

func (s *RealBlockStore) findMaxPersistedHeightKey() (uint64, bool) {
	if s.dbManager == nil {
		return 0, false
	}

	const (
		prefix = "v1_height_"
		suffix = "_blocks"
	)

	records, err := s.dbManager.Scan(prefix)
	if err != nil {
		return 0, false
	}

	var (
		maxHeight uint64
		found     bool
	)

	for key := range records {
		if !strings.HasSuffix(key, suffix) {
			continue
		}

		raw := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
		h, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			continue
		}

		if !found || h > maxHeight {
			maxHeight = h
			found = true
		}
	}

	return maxHeight, found
}

func (s *RealBlockStore) convertDBBlockToTypes(dbBlock *pb.Block) *types.Block {
	if s.adapter == nil {
		s.adapter = NewConsensusAdapter(s.dbManager)
	}
	block, err := s.adapter.DBBlockToConsensus(dbBlock)
	if err != nil {
		logs.Error("[RealBlockStore] Failed to convert block: %v", err)
		return nil
	}
	return block
}

func (s *RealBlockStore) maybeForceFlushAfterFinalize(height uint64, blockID string) {
	if s.dbManager == nil {
		return
	}

	s.forceFlushMu.Lock()
	defer s.forceFlushMu.Unlock()

	s.finalizedSinceLastFlush++

	                                                                        
	if s.forceFlushEveryN <= 0 && s.forceFlushMaxInterval <= 0 {
		if err := s.dbManager.ForceFlushTrace(height, blockID); err != nil {
			logs.Error("[RealBlockStore] ForceFlush failed (fallback): %v", err)
			return
		}
		if err := s.dbManager.CommitBlockArchiveCoverage(); err != nil {
			logs.Error("[RealBlockStore] commit block archive coverage failed: %v", err)
		}
		if err := s.dbManager.CommitFrostArchiveCoverage(); err != nil {
			logs.Error("[RealBlockStore] commit FROST archive coverage failed: %v", err)
		}
		s.finalizedSinceLastFlush = 0
		s.lastForceFlushAt = time.Now()
		return
	}

	countTrigger := s.forceFlushEveryN > 0 && s.finalizedSinceLastFlush >= uint64(s.forceFlushEveryN)
	intervalTrigger := s.forceFlushMaxInterval > 0 && time.Since(s.lastForceFlushAt) >= s.forceFlushMaxInterval
	if !countTrigger && !intervalTrigger {
		return
	}

	pending := s.finalizedSinceLastFlush
	if err := s.dbManager.ForceFlushTrace(height, blockID); err != nil {
		logs.Error("[RealBlockStore] ForceFlush failed: pending=%d err=%v", pending, err)
		return
	}
	if err := s.dbManager.CommitBlockArchiveCoverage(); err != nil {
		logs.Error("[RealBlockStore] commit block archive coverage failed: %v", err)
	}
	if err := s.dbManager.CommitFrostArchiveCoverage(); err != nil {
		logs.Error("[RealBlockStore] commit FROST archive coverage failed: %v", err)
	}

	s.finalizedSinceLastFlush = 0
	s.lastForceFlushAt = time.Now()
}

                                                                                          
func (s *RealBlockStore) cleanupOldData(belowHeight uint64) {
	cleanedBlocks := 0
	cleanedHeights := 0
	cleanedFinalized := 0

	                                    
	for blockID, block := range s.blockCache {
		if block.Header.Height < belowHeight {
			delete(s.blockCache, blockID)
			cleanedBlocks++
		}
	}

	                                     
	for height := range s.heightIndex {
		if height < belowHeight {
			delete(s.heightIndex, height)
			cleanedHeights++
		}
	}

	                                         
	for height := range s.finalizedBlocks {
		if height < belowHeight {
			delete(s.finalizedBlocks, height)
			cleanedFinalized++
		}
	}

	                                           
	for height := range s.finalizationChits {
		if height < belowHeight {
			delete(s.finalizationChits, height)
		}
	}

	if cleanedBlocks > 0 || cleanedHeights > 0 || cleanedFinalized > 0 {
		logs.Debug("[RealBlockStore] Cleaned old data below height %d: blocks=%d, heights=%d, finalized=%d",
			belowHeight, cleanedBlocks, cleanedHeights, cleanedFinalized)
	}
}

func (s *RealBlockStore) GetWitnessService() *witness.Service {
	if s.vmExecutor == nil {
		return nil
	}
	return s.vmExecutor.GetWitnessService()
}

                                        
func (s *RealBlockStore) GetExecutor() *vm.Executor {
	return s.vmExecutor
}

func (s *RealBlockStore) rememberBlockTiming(t BlockTimingSnapshot) {
	if s == nil || t.Height == 0 {
		return
	}
	s.timingMu.Lock()
	s.lastBlockTiming = t
	s.timingMu.Unlock()
}

func (s *RealBlockStore) GetLastBlockTiming() (BlockTimingSnapshot, bool) {
	if s == nil {
		return BlockTimingSnapshot{}, false
	}
	s.timingMu.RLock()
	defer s.timingMu.RUnlock()
	if s.lastBlockTiming.Height == 0 {
		return BlockTimingSnapshot{}, false
	}
	return s.lastBlockTiming, true
}

                                                                                  
func (s *RealBlockStore) GetPendingBlocksCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]bool)
	for height := s.lastAcceptedHeight + 1; height <= s.maxHeight; height++ {
		if blocks, exists := s.heightIndex[height]; exists {
			for _, b := range blocks {
				seen[b.ID] = true
			}
		}
	}
	return len(seen)
}

                                                              
func (s *RealBlockStore) GetPendingBlocks() []*types.Block {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*types.Block, 0)
	seen := make(map[string]bool)
	for height := s.lastAcceptedHeight + 1; height <= s.maxHeight; height++ {
		if blocks, exists := s.heightIndex[height]; exists {
			for _, b := range blocks {
				if !seen[b.ID] {
					result = append(result, b)
					seen[b.ID] = true
				}
			}
		}
	}
	return result
}

func (s *RealBlockStore) ensureFinalizationObserved(height uint64, blockID string) {
	now := time.Now().UnixMilli()
	s.mu.Lock()
	chits := s.finalizationChits[height]
	if chits == nil {
		chits = &types.FinalizationChits{BlockID: blockID, Height: height, FinalizedAt: now}
		s.finalizationChits[height] = chits
	} else {
		chits.EnsureObservedAt(now)
	}
	s.mu.Unlock()
	s.persistFinalizationChits(height, chits)
}

func (s *RealBlockStore) persistFinalizationChits(height uint64, chits *types.FinalizationChits) {
	if s.dbManager == nil || chits == nil {
		return
	}
	key := fmt.Sprintf("finalization_chits_%d", height)
	data, err := json.Marshal(chits)
	if err != nil {
		logs.Warn("[RealBlockStore] Failed to marshal finalization chits for height %d: %v", height, err)
		return
	}
	s.dbManager.EnqueueSet(key, string(data))
}

                                                                  
func (s *RealBlockStore) SetFinalizationChits(height uint64, chits *types.FinalizationChits) {
	if chits == nil {
		return
	}
	chits.EnsureObservedAt(time.Now().UnixMilli())
	s.mu.Lock()
	s.finalizationChits[height] = chits
	s.mu.Unlock()

	                                                                              
	s.persistFinalizationChits(height, chits)
}

                                                                  
func (s *RealBlockStore) GetFinalizationChits(height uint64) (*types.FinalizationChits, bool) {
	s.mu.RLock()
	if chits, exists := s.finalizationChits[height]; exists {
		s.mu.RUnlock()
		return chits, true
	}
	s.mu.RUnlock()

	                           
	key := fmt.Sprintf("finalization_chits_%d", height)
	data, err := s.dbManager.Read(key)
	if err != nil || data == "" {
		return nil, false
	}

	var chits types.FinalizationChits
	if err := json.Unmarshal([]byte(data), &chits); err != nil {
		logs.Warn("[RealBlockStore] Failed to unmarshal finalization chits for height %d: %v", height, err)
		return nil, false
	}

	                  
	chits.EnsureObservedAt(0)
	s.mu.Lock()
	s.finalizationChits[height] = &chits
	s.mu.Unlock()

	return &chits, true
}
