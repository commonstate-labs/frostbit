package consensus

import (
	"context"
	"dex/interfaces"
	"dex/logs"
	"dex/types"
	"errors"
	"fmt"
	"sync"
	"time"
)

                                               
               
                                               

type SnowmanEngine struct {
	mu sync.RWMutex
	                                                                    
	                                                               
	                                              
	processVotesMu sync.Mutex
	nodeID         types.NodeID
	store          interfaces.BlockStore
	config         *ConsensusConfig
	events         interfaces.EventBus
	snowballs      map[uint64]*Snowball
	activeQueries  map[string]*QueryContext
	finalizing     map[uint64]string
	pollOrder      map[uint64]*pollOrderState
	                                                                           
	                                                                 
	                                                                     
	outstandingQueries   map[uint64]int
	outstandingQueryKeys map[string]*QueryContext
	Logger               logs.Logger
}

type pollOrderState struct {
	nextSeq uint32
	pending map[uint32]*QueryContext
}

type settledQuery struct {
	key    string
	reason string
	trace  *QueryContext
}

type QueryContext struct {
	queryKey       string
	blockID        string
	voters         map[types.NodeID]string                              
	signatures     map[types.NodeID][]byte                                                  
	poll           *PollAccumulator[types.NodeID]
	startTime      time.Time
	height         uint64
	seqID          uint32
	vrfSeed        []byte
	params         ConsensusPollParams
	terminalReason string
	timeoutTimer   *time.Timer
	outstanding    bool
	trace          *pollTrace                                                              
}

func NewSnowmanEngine(nodeID types.NodeID, store interfaces.BlockStore, config *ConsensusConfig, events interfaces.EventBus, logger logs.Logger) interfaces.ConsensusEngine {
	return &SnowmanEngine{
		nodeID:               nodeID,
		store:                store,
		config:               config,
		events:               events,
		snowballs:            make(map[uint64]*Snowball),
		activeQueries:        make(map[string]*QueryContext),
		finalizing:           make(map[uint64]string),
		pollOrder:            make(map[uint64]*pollOrderState),
		outstandingQueries:   make(map[uint64]int),
		outstandingQueryKeys: make(map[string]*QueryContext),
		Logger:               logger,
	}
}

func (e *SnowmanEngine) Start(ctx context.Context) error {
	                                                                         
	                                     
	e.processVotesMu.Lock()
	genesisSB := NewSnowball(e.events)
	genesisSB.AddCandidate("genesis")
	if !genesisSB.Finalize("genesis") {
		e.processVotesMu.Unlock()
		return fmt.Errorf("failed to initialize finalized genesis Snowball")
	}
	e.mu.Lock()
	e.snowballs[0] = genesisSB
	e.mu.Unlock()
	e.processVotesMu.Unlock()

	                     
	go func() {
		cleanupLogger := logs.SetThreadLogger(e.Logger)
		defer cleanupLogger()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				e.checkTimeouts()
			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (e *SnowmanEngine) RegisterQuery(
	nodeID types.NodeID,
	requestID uint32,
	blockID string,
	height uint64,
	seqID uint32,
	vrfSeed []byte,
	sampleWeights map[types.NodeID]int,
) string {
	return e.RegisterQueryWithParams(
		nodeID,
		requestID,
		blockID,
		height,
		seqID,
		vrfSeed,
		sampleWeights,
		pollParamsFromConfig(*e.config),
	)
}

                                                                             
                                                                             
                                                                            
                           
func (e *SnowmanEngine) RegisterQueryWithParams(
	nodeID types.NodeID,
	requestID uint32,
	blockID string,
	height uint64,
	seqID uint32,
	vrfSeed []byte,
	sampleWeights map[types.NodeID]int,
	params ConsensusPollParams,
) string {
	if blockID == "" || e.finalizedBlockID(height) != "" || e.finalizingBlockID(height) != "" {
		return ""
	}
	if params.K < 1 || params.AlphaPreference < 1 ||
		params.AlphaPreference > params.AlphaConfidence ||
		params.AlphaConfidence > params.K || params.Beta < 1 {
		return ""
	}

	e.processVotesMu.Lock()
	defer e.processVotesMu.Unlock()
	if e.finalizedBlockID(height) != "" || e.finalizingBlockID(height) != "" {
		return ""
	}
	_, candidates, _ := e.ensureCandidatesLocked(height)
	candidateSet := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateSet[candidate] = struct{}{}
	}
	if _, ok := candidateSet[blockID]; !ok {
		return ""
	}
	if order := e.pollOrder[height]; order != nil {
		if seqID < order.nextSeq || order.pending[seqID] != nil {
			return ""
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finalizing[height] != "" {
		return ""
	}
	networkActive := 0
	for _, query := range e.activeQueries {
		if query.height == height {
			if query.seqID == seqID {
				return ""
			}
			networkActive++
		}
	}
	if networkActive >= maxPerHeightPollPipeline ||
		e.outstandingQueries[height] >= maxPerHeightPollOutstanding {
		return ""
	}

	queryKey := fmt.Sprintf("%s-%d", nodeID, requestID)
	if _, exists := e.outstandingQueryKeys[queryKey]; exists {
		return ""
	}
	for peer := range sampleWeights {
		if peer == "" || peer == nodeID {
			return ""
		}
	}
	accumulator, err := NewPollAccumulator(
		params.K,
		params.AlphaPreference,
		params.AlphaConfidence,
		sampleWeights,
		candidateSet,
	)
	if err != nil {
		return ""
	}
	ctx := &QueryContext{
		queryKey:    queryKey,
		blockID:     blockID,
		voters:      make(map[types.NodeID]string),
		signatures:  make(map[types.NodeID][]byte),
		poll:        accumulator,
		startTime:   time.Now(),
		height:      height,
		seqID:       seqID,
		vrfSeed:     vrfSeed,
		params:      params,
		outstanding: true,
	}
	if logs.ConsensusTraceEnabled() {
		ctx.trace = newPollTrace()
	}
	e.activeQueries[queryKey] = ctx
	e.outstandingQueries[height]++
	e.outstandingQueryKeys[queryKey] = ctx
	if timeout := e.config.QueryTimeout; timeout > 0 {
		ctx.timeoutTimer = time.AfterFunc(timeout, func() {
			e.expireQuery(queryKey, ctx)
		})
	}

	return queryKey
}

                                                               
func (e *SnowmanEngine) SubmitChit(nodeID types.NodeID, queryKey string, preferredID string, chitSignature []byte) {
	arrivedAt := time.Now()
	var completedCtx *QueryContext
	shouldProcess := false
	e.mu.Lock()
	ctx, exists := e.activeQueries[queryKey]
	if !exists {
		e.mu.Unlock()
		return
	}
	result := ctx.poll.Record(nodeID, preferredID)
	if !result.Accepted {
		e.mu.Unlock()
		return
	}
	ctx.voters[nodeID] = preferredID
	if ctx.trace != nil {
		ctx.trace.recordReply(nodeID, arrivedAt, len(chitSignature) > 0)
	}
	if len(chitSignature) > 0 {
		ctx.signatures[nodeID] = chitSignature
	}
	if result.Completed {
		if ctx.trace != nil {
			ctx.trace.networkDoneAt = time.Now()
		}
		delete(e.activeQueries, queryKey)
		if ctx.timeoutTimer != nil {
			ctx.timeoutTimer.Stop()
		}
		completedCtx = ctx
		shouldProcess = true
	}
	e.mu.Unlock()
	if !shouldProcess {
		return
	}
	emitPollNetworkTrace(completedCtx)
	                                                                             
	                                                                         
	e.publishSettledQueries(e.enqueueCompletedQuery(completedCtx))
}

func (e *SnowmanEngine) enqueueCompletedQuery(ctx *QueryContext) []settledQuery {
	e.processVotesMu.Lock()
	defer e.processVotesMu.Unlock()
	if finalized, ok := e.store.GetFinalizedAtHeight(ctx.height); ok && finalized != nil {
		return e.settleFinalizedHeightLocked(ctx)
	}

	state := e.pollOrder[ctx.height]
	if state == nil {
		state = &pollOrderState{pending: make(map[uint32]*QueryContext)}
		e.pollOrder[ctx.height] = state
	}
	if ctx.seqID < state.nextSeq {
		e.settleOutstandingQuery(ctx)
		return []settledQuery{{key: ctx.queryKey, reason: "stale"}}
	}
	if state.pending[ctx.seqID] != nil {
		                                                                 
		                                                                     
		e.settleOutstandingQuery(ctx)
		return []settledQuery{{key: ctx.queryKey, reason: "stale"}}
	}
	state.pending[ctx.seqID] = ctx

	settled := make([]settledQuery, 0, len(state.pending))
	for {
		next := state.pending[state.nextSeq]
		if next == nil {
			break
		}
		delete(state.pending, state.nextSeq)
		state.nextSeq++
		if next.trace != nil {
			next.trace.settleStartedAt = time.Now()
		}
		reason := e.processVotesOrdered(next)
		if next.trace != nil {
			next.trace.settledAt = time.Now()
		}
		e.settleOutstandingQuery(next)
		if next.terminalReason != "" {
			reason = next.terminalReason
		}
		settled = append(settled, settledQuery{key: next.queryKey, reason: reason, trace: next})
		if e.finalizedBlockID(ctx.height) != "" || e.IsFinalizing(ctx.height) {
			for seq, pending := range state.pending {
				e.settleOutstandingQuery(pending)
				settled = append(settled, settledQuery{key: pending.queryKey, reason: "finalizing"})
				delete(state.pending, seq)
			}
			delete(e.pollOrder, ctx.height)
			                                                                    
			                                                                 
			                                          
			e.mu.Lock()
			canceledKeys := e.cancelActiveQueriesAtHeightMuLocked(ctx.height)
			e.mu.Unlock()
			for _, key := range canceledKeys {
				settled = append(settled, settledQuery{key: key, reason: "finalizing"})
			}
			break
		}
	}
	return settled
}

                                                                              
                                                                               
                                       
func (e *SnowmanEngine) settleFinalizedHeightLocked(completed *QueryContext) []settledQuery {
	settled := make([]settledQuery, 0, maxPerHeightPollOutstanding)
	e.settleOutstandingQuery(completed)
	settled = append(settled, settledQuery{key: completed.queryKey, reason: "finalizing"})

	if state := e.pollOrder[completed.height]; state != nil {
		for seq, pending := range state.pending {
			e.settleOutstandingQuery(pending)
			settled = append(settled, settledQuery{key: pending.queryKey, reason: "finalizing"})
			delete(state.pending, seq)
		}
		delete(e.pollOrder, completed.height)
	}

	e.mu.Lock()
	canceledKeys := e.cancelActiveQueriesAtHeightMuLocked(completed.height)
	e.mu.Unlock()
	for _, key := range canceledKeys {
		settled = append(settled, settledQuery{key: key, reason: "finalizing"})
	}
	return settled
}

func (e *SnowmanEngine) settleOutstandingQuery(ctx *QueryContext) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.settleOutstandingQueryMuLocked(ctx)
}

                                                                             
                                          
func (e *SnowmanEngine) settleOutstandingQueryMuLocked(ctx *QueryContext) {
	if ctx == nil || !ctx.outstanding {
		return
	}
	ctx.outstanding = false
	if reserved := e.outstandingQueryKeys[ctx.queryKey]; reserved == ctx {
		delete(e.outstandingQueryKeys, ctx.queryKey)
	}
	remaining := e.outstandingQueries[ctx.height] - 1
	if remaining <= 0 {
		delete(e.outstandingQueries, ctx.height)
		return
	}
	e.outstandingQueries[ctx.height] = remaining
}

func (e *SnowmanEngine) publishSettledQueries(settled []settledQuery) {
	for _, result := range settled {
		emitPollSettledTrace(result.trace, result.reason)
		e.events.PublishAsync(types.BaseEvent{
			EventType: types.EventQueryComplete,
			EventData: QueryCompleteData{Reason: result.reason, QueryKeys: []string{result.key}},
		})
	}
}

func (e *SnowmanEngine) processVotes(ctx *QueryContext) string {
	e.processVotesMu.Lock()
	defer e.processVotesMu.Unlock()
	return e.processVotesOrdered(ctx)
}

                                                                             
                                                                      
func (e *SnowmanEngine) processVotesOrdered(ctx *QueryContext) string {
	if e.finalizedBlockID(ctx.height) != "" {
		return "finalizing"
	}
	params := ctx.params
	if params.K < 1 || params.AlphaPreference < 1 ||
		params.AlphaPreference > params.AlphaConfidence ||
		params.AlphaConfidence > params.K || params.Beta < 1 {
		                                                                        
		                                                                          
		params = pollParamsFromConfig(*e.config)
	}
	sb, candidates, missingReason := e.ensureCandidatesLocked(ctx.height)
	if len(candidates) == 0 {
		logs.Debug("[Engine] No valid candidates for height %d", ctx.height)
		if missingReason != "" {
			return missingReason
		}
		return "candidates_missing"
	}

	candidateSet := make(map[string]bool, len(candidates))
	for _, id := range candidates {
		candidateSet[id] = true
	}
	outcome := ctx.poll.Outcome()
	filteredVotes := make(map[string]int, len(outcome.Votes))
	droppedVotes := 0
	for id, count := range outcome.Votes {
		if candidateSet[id] {
			filteredVotes[id] = count
		} else {
			droppedVotes += count
		}
	}
	if droppedVotes > 0 {
		logs.Debug("[Engine] Dropped %d vote(s) for non-candidate blocks at height %d (query=%s)",
			droppedVotes, ctx.height, ctx.queryKey)
	}

	voteDetails := make([]types.FinalizationChit, 0, len(ctx.voters))
	for nodeID, preferredID := range ctx.voters {
		sampleWeight, _ := ctx.poll.SampleWeight(nodeID)
		voteDetails = append(voteDetails, types.FinalizationChit{
			NodeID:       string(nodeID),
			PreferredID:  preferredID,
			Timestamp:    ctx.startTime.UnixMilli(),
			Signature:    ctx.signatures[nodeID],                       
			SampleWeight: sampleWeight,
		})
	}

	if e.IsFinalizing(ctx.height) {
		return "finalizing"
	}
	if ctx.trace != nil {
		ctx.trace.confidenceBefore = sb.GetConfidence()
		ctx.trace.preferenceBefore = sb.GetPreference()
		ctx.trace.confidencePreferenceBefore = sb.GetConfidencePreference()
		for _, weight := range filteredVotes {
			ctx.trace.maxValidVoteWeight = max(ctx.trace.maxValidVoteWeight, weight)
		}
		ctx.trace.hard = ctx.trace.maxValidVoteWeight >= params.AlphaConfidence
	}
	sb.RecordVoteWithDetails(
		candidates,
		filteredVotes,
		params.AlphaPreference,
		params.AlphaConfidence,
		ctx.seqID,
		ctx.vrfSeed,
		voteDetails,
	)
	if ctx.trace != nil {
		ctx.trace.confidenceAfter = sb.GetConfidence()
		ctx.trace.preferenceAfter = sb.GetPreference()
		ctx.trace.confidencePreferenceAfter = sb.GetConfidencePreference()
	}

	if finalPreference, ready := sb.FinalizationPreference(params.Beta); ready {
		reserved, canceledKeys := e.reserveFinalization(ctx.height, finalPreference)
		if !reserved {
			return "finalizing"
		}
		chits := e.collectFinalizationChits(ctx.height, finalPreference)
		e.publishFinalizingCancellations(canceledKeys)
		defer e.releaseFinalization(ctx.height, finalPreference)
		e.finalizeBlock(ctx.height, finalPreference, chits)
		return "success"
	}
	return "success"
}

func (e *SnowmanEngine) reserveFinalization(height uint64, blockID string) (bool, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if existing := e.finalizing[height]; existing != "" {
		return false, nil
	}
	e.finalizing[height] = blockID
	return true, e.cancelActiveQueriesAtHeightMuLocked(height)
}

                                                                           
                                                                             
                                                        
func (e *SnowmanEngine) cancelActiveQueriesAtHeightMuLocked(height uint64) []string {
	canceledKeys := make([]string, 0)
	for queryKey, query := range e.activeQueries {
		if query.height == height {
			delete(e.activeQueries, queryKey)
			if query.timeoutTimer != nil {
				query.timeoutTimer.Stop()
			}
			e.settleOutstandingQueryMuLocked(query)
			canceledKeys = append(canceledKeys, queryKey)
		}
	}
	return canceledKeys
}

func (e *SnowmanEngine) releaseFinalization(height uint64, blockID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if existing := e.finalizing[height]; existing == blockID {
		delete(e.finalizing, height)
	}
}

func (e *SnowmanEngine) IsFinalizing(height uint64) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.finalizing[height] != ""
}

func (e *SnowmanEngine) publishFinalizingCancellations(queryKeys []string) {
	if len(queryKeys) == 0 {
		return
	}
	event := types.BaseEvent{
		EventType: types.EventQueryComplete,
		EventData: QueryCompleteData{Reason: "finalizing", QueryKeys: queryKeys},
	}
	                                                                           
	                                                                         
	                                                                      
	if publisher, ok := e.events.(interface{ Publish(interfaces.Event) }); ok {
		publisher.Publish(event)
		return
	}
	e.events.PublishAsync(event)
}

func (e *SnowmanEngine) getOrCreateSnowball(height uint64) *Snowball {
	e.mu.Lock()
	defer e.mu.Unlock()
	sb, exists := e.snowballs[height]
	if !exists {
		sb = NewSnowball(e.events)
		e.snowballs[height] = sb
	}
	return sb
}

func (e *SnowmanEngine) getSnowball(height uint64) *Snowball {
	e.mu.RLock()
	sb := e.snowballs[height]
	e.mu.RUnlock()
	return sb
}

func (e *SnowmanEngine) finalizedBlockID(height uint64) string {
	block, ok := e.store.GetFinalizedAtHeight(height)
	if !ok || block == nil {
		return ""
	}
	return block.ID
}

func (e *SnowmanEngine) finalizingBlockID(height uint64) string {
	e.mu.RLock()
	blockID := e.finalizing[height]
	e.mu.RUnlock()
	return blockID
}

                                                                         
                                                        
func (e *SnowmanEngine) ensureCandidatesLocked(height uint64) (*Snowball, []string, string) {
	var parent *types.Block
	if height > 0 {
		var ok bool
		parent, ok = e.store.GetFinalizedAtHeight(height - 1)
		if !ok || parent == nil {
			logs.Debug("[Engine] Parent block at height %d not finalized, cannot initialize height %d",
				height-1, height)
			return e.getSnowball(height), nil, "parent_missing"
		}
	}

	blocks := e.store.GetByHeight(height)
	candidates := make([]string, 0, len(blocks))
	seen := make(map[string]struct{}, len(blocks))
	for _, block := range blocks {
		if block == nil || block.ID == "" {
			continue
		}
		if _, duplicate := seen[block.ID]; duplicate {
			continue
		}
		if height > 0 && block.Header.ParentID != parent.ID {
			logs.Debug("[Engine] Block %s rejected from candidates: parent mismatch (expected %s, got %s)",
				block.ID, parent.ID, block.Header.ParentID)
			continue
		}
		if err := validateCandidatePreStateRoot(e.store, block); err != nil {
			if errors.Is(err, ErrInvalidCandidatePreStateRoot) {
				e.invalidateCandidateLocked(height, block.ID)
			}
			logs.Warn("[Engine] Block %s rejected from candidates before consensus: %v", block.ID, err)
			continue
		}
		seen[block.ID] = struct{}{}
		candidates = append(candidates, block.ID)
	}
	if len(candidates) == 0 {
		return e.getSnowball(height), nil, "candidates_missing"
	}

	sb := e.getOrCreateSnowball(height)
	sb.AddCandidates(candidates)
	return sb, candidates, ""
}

func (e *SnowmanEngine) collectFinalizationChits(height uint64, blockID string) *types.FinalizationChits {
	sb := e.getSnowball(height)
	if sb == nil {
		return nil
	}

	successHistory := sb.GetSuccessHistory()

	                                    
	rounds := make([]types.RoundChits, 0, len(successHistory))
	allChits := make([]types.FinalizationChit, 0)
	totalVotes := 0
	for _, sr := range successHistory {
		rc := types.RoundChits{
			Round:     sr.Round,
			Timestamp: sr.Timestamp,
			Votes:     sr.Votes,
		}
		rounds = append(rounds, rc)
		allChits = append(allChits, sr.Votes...)
		for _, chit := range sr.Votes {
			totalVotes += chit.EffectiveSampleWeight()
		}
	}

	return &types.FinalizationChits{
		BlockID:     blockID,
		Height:      height,
		TotalRounds: len(rounds),
		Rounds:      rounds,
		TotalVotes:  totalVotes,
		Chits:       allChits,
		                                                                            
		                                                                                       
		                                                                                         
		                                                            
		FinalizedAt: time.Now().UnixMilli(),
	}
}

func (e *SnowmanEngine) finalizeBlock(height uint64, blockID string, chits *types.FinalizationChits) {
	if _, exists := e.store.Get(blockID); !exists {
		logs.Warn("[Engine] Finalize skipped: block %s not found at height %d", blockID, height)
		return
	}
	if err := e.store.SetFinalized(height, blockID); err != nil {
		if errors.Is(err, ErrAlreadyFinalized) {
			logs.Debug("[Engine] Finalization already recorded for block %s at height %d; refreshing observability metadata", blockID, height)
		} else if errors.Is(err, ErrInvalidCandidatePreStateRoot) {
			logs.Warn("[Engine] Discarding block %s at height %d after deterministic pre-state root failure: %v", blockID, height, err)
			e.invalidateCandidateLocked(height, blockID)
			return
		} else {
			logs.Warn("[Engine] Finalize failed: block %s at height %d: %v", blockID, height, err)
			return
		}
	}

	                                                                                 
	if realStore, ok := e.store.(*RealBlockStore); ok {
		if chits != nil {
			realStore.SetFinalizationChits(height, chits)
		}
	}

	sb := e.getSnowball(height)
	if sb != nil {
		if !sb.Finalize(blockID) {
			panic(fmt.Sprintf("consensus invariant violated: committed block %s at height %d does not match Snowball finalization preference", blockID, height))
		}
		sb.DropFinalizedEvidence()
	}

	if block, exists := e.store.Get(blockID); exists {
		Logf("[Engine] 🎉 Finalized block %s at height %d\n", blockID, height)
		                               
		e.events.PublishAsync(types.BaseEvent{
			EventType: types.EventBlockFinalized,
			EventData: &types.BlockFinalizedData{
				Block: block,
				Chits: chits,
			},
		})
	}

}

type QueryCompleteData struct {
	Reason    string                                          
	QueryKeys []string                                  
}

func (e *SnowmanEngine) checkTimeouts() {
	now := time.Now()
	type expiredQuery struct {
		key string
		ctx *QueryContext
	}
	expired := make([]expiredQuery, 0)
	e.mu.RLock()
	for k, ctx := range e.activeQueries {
		if now.Sub(ctx.startTime) >= e.config.QueryTimeout {
			expired = append(expired, expiredQuery{key: k, ctx: ctx})
		}
	}
	e.mu.RUnlock()
	expiredCount := 0
	for _, query := range expired {
		if e.expireQuery(query.key, query.ctx) {
			expiredCount++
		}
	}
	if expiredCount > 0 {
		logs.Debug("[Engine] Query timeout: %d expired. Still processed available votes before deletion.", expiredCount)
	}
}

                                                                                
                                                                                
                              
func (e *SnowmanEngine) expireQuery(queryKey string, expected *QueryContext) bool {
	e.mu.Lock()
	ctx, exists := e.activeQueries[queryKey]
	if !exists || (expected != nil && ctx != expected) {
		e.mu.Unlock()
		return false
	}
	delete(e.activeQueries, queryKey)
	if ctx.timeoutTimer != nil {
		ctx.timeoutTimer.Stop()
	}
	ctx.poll.Close()
	ctx.terminalReason = "timeout"
	if ctx.trace != nil {
		ctx.trace.networkDoneAt = time.Now()
	}
	e.mu.Unlock()
	emitPollNetworkTrace(ctx)

	                                                                             
	                 
	e.publishSettledQueries(e.enqueueCompletedQuery(ctx))
	return true
}
func (e *SnowmanEngine) GetActiveQueryCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.activeQueries)
}

                                                                            
                                                                           
                                                                            
func (e *SnowmanEngine) GetQueryPipelineCounts(height uint64) (networkActive int, totalOutstanding int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, query := range e.activeQueries {
		if query.height == height {
			networkActive++
		}
	}
	return networkActive, e.outstandingQueries[height]
}

func (e *SnowmanEngine) IsQueryActive(queryKey string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, exists := e.activeQueries[queryKey]
	return exists
}

func (e *SnowmanEngine) GetPreference(height uint64) string {
	if blockID := e.finalizedBlockID(height); blockID != "" {
		return blockID
	}
	if blockID := e.finalizingBlockID(height); blockID != "" {
		return blockID
	}

	e.processVotesMu.Lock()
	defer e.processVotesMu.Unlock()
	if blockID := e.finalizedBlockID(height); blockID != "" {
		return blockID
	}
	if blockID := e.finalizingBlockID(height); blockID != "" {
		return blockID
	}
	sb, candidates, _ := e.ensureCandidatesLocked(height)
	if sb == nil || len(candidates) == 0 {
		return ""
	}
	return sb.GetPreference()
}

                                                                             
                                                                    
func (e *SnowmanEngine) InvalidateCandidate(height uint64, blockID string) {
	e.processVotesMu.Lock()
	defer e.processVotesMu.Unlock()
	e.invalidateCandidateLocked(height, blockID)
}

                                                                            
                                          
func (e *SnowmanEngine) invalidateCandidateLocked(height uint64, blockID string) {
	if discarder, ok := e.store.(candidateDiscarder); ok {
		discarder.DiscardCandidate(height, blockID)
	}

	sb := e.getSnowball(height)
	if sb != nil {
		sb.InvalidateCandidate(blockID)
	}
}

                                                
type HeightState struct {
	Height     uint64
	Preference string
	Confidence int
	Finalized  bool
	LastVotes  map[string]int
}

                                                   
func (e *SnowmanEngine) GetHeightState(height uint64) *HeightState {
	e.mu.RLock()
	sb := e.snowballs[height]
	e.mu.RUnlock()
	if sb == nil {
		return nil
	}

	return &HeightState{
		Height:     height,
		Preference: sb.GetPreference(),
		Confidence: sb.GetConfidence(),
		Finalized:  sb.IsFinalized(),
		LastVotes:  sb.GetLastVotes(),
	}
}

                                                                       
func (e *SnowmanEngine) GetPendingHeightsState() []*HeightState {
	e.mu.RLock()
	snowballs := make(map[uint64]*Snowball, len(e.snowballs))
	for height, sb := range e.snowballs {
		snowballs[height] = sb
	}
	e.mu.RUnlock()

	result := make([]*HeightState, 0, len(snowballs))
	for height, sb := range snowballs {
		if !sb.IsFinalized() {
			result = append(result, &HeightState{
				Height:     height,
				Preference: sb.GetPreference(),
				Confidence: sb.GetConfidence(),
				Finalized:  false,
				LastVotes:  sb.GetLastVotes(),
			})
		}
	}
	return result
}
