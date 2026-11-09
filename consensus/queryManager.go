package consensus

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"dex/interfaces"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"encoding/binary"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

                                               
                  
                                               

type QueryManager struct {
	nodeID               types.NodeID
	node                 *Node
	transport            interfaces.Transport
	store                interfaces.BlockStore
	engine               interfaces.ConsensusEngine
	config               *ConsensusConfig
	consensusParams      *ConsensusParamsProvider
	events               interfaces.EventBus
	Logger               logs.Logger
	activePolls          sync.Map
	nextReqID            uint32
	mu                   sync.Mutex
	issueMu              sync.Mutex
	missingBlockRequests map[string]time.Time                                                                   
	issueWake            chan struct{}
	startOnce            sync.Once
	syncManager          *SyncManager                                                    
	                      
	seqID        uint32                                        
	lastSeed     []byte                                                         
	seqHeight    uint64
	seqHeightSet bool
	                                                             
	controlBackoffUntil time.Time
	pushedBlockHeight   uint64
	pushedHeightSet     bool
	pushedBlocks        map[string]struct{}
	                                                                                                  
	livenessReporter interfaces.LivenessReportSink
	livenessTracker  interfaces.LivenessTracker

	                                                                                             
	                                                                                         
	                                                                                            
	                                                                          
	                                                                                                 
	                                                                                    
	pollStatsMu sync.Mutex
	pollStats   map[types.NodeID]*peerPollRing

	                                                                   
	                                                                          
	                                                                      
	probeSessions      sync.Map                                       
	probeMu            sync.Mutex
	lastProbeHeight    uint64
	lastProbeHeightSet bool
}

const (
	maxMissingBlockRequests   = 256
	missingBlockRetryInterval = 5 * time.Second
	                                                                           
	                                                                        
	maxPerHeightPollPipeline = 4
	                                                                           
	                                                                          
	                                                                       
	maxPerHeightPollOutstanding = 8
	                                                                                                     
	pollStatsMinSamples = 5
	                                                                                                 
	pollStatsOfflineRatio = 0.5
	                                                                              
	                                                                               
	pollStatsWindow = 40
	                                                                         
	                                                                       
	                                                                            
	                                                  
	probationProbeMinReporters = 3
	probationProbeGroupHeights = 10
)

                                                                                             
                                                            
type peerPollRing struct {
	ring  [pollStatsWindow]bool                                
	head  int
	count int
	miss  int
}

func (r *peerPollRing) push(missed bool) {
	if r.count == pollStatsWindow {
		if r.ring[r.head] {
			r.miss--
		}
	} else {
		r.count++
	}
	r.ring[r.head] = missed
	if missed {
		r.miss++
	}
	r.head = (r.head + 1) % pollStatsWindow
}

type Poll struct {
	requestID      uint32
	blockID        string
	queryKey       string
	startTime      time.Time
	height         uint64
	vrfSeed        []byte                                                                           
	seqID          uint32                                                                     
	queryNonce     []byte                                                                  
	sampledPeers   []types.NodeID                         
	sampledSet     map[types.NodeID]struct{}
	sampleWeights  map[types.NodeID]int                                            
	respondedPeers sync.Map                                        
	                                                                               
	realRespondedPeers sync.Map
}

type probationProbeSession struct {
	requestID  uint32
	height     uint64
	vrfSeed    []byte
	seqID      uint32
	queryNonce []byte
	targets    map[types.NodeID]struct{}
	responded  sync.Map
	failed     sync.Map
}

type queryFinalizationState interface {
	IsFinalizing(height uint64) bool
}

type queryBroadcastTransport interface {
	BroadcastQuery(types.Message, []types.NodeID, func(types.NodeID, error))
}

type queryPipelineState interface {
	GetQueryPipelineCounts(height uint64) (networkActive int, totalOutstanding int)
}

type queryParamsRegistrar interface {
	RegisterQueryWithParams(
		nodeID types.NodeID,
		requestID uint32,
		blockID string,
		height uint64,
		seqID uint32,
		vrfSeed []byte,
		sampleWeights map[types.NodeID]int,
		params ConsensusPollParams,
	) string
}

                                                                                          
func computeVRFSeed(parentID string, height uint64, window int, nodeID types.NodeID) []byte {
	h := sha256.New()
	h.Write([]byte(parentID))
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, height)
	h.Write(buf)
	binary.BigEndian.PutUint32(buf[:4], uint32(window))
	h.Write(buf[:4])
	h.Write([]byte(nodeID))
	return h.Sum(nil)
}

func computePollSampleSeed(vrfSeed []byte, seqID uint32, queryNonce []byte) []byte {
	h := sha256.New()
	h.Write(vrfSeed)
	var seq [4]byte
	binary.BigEndian.PutUint32(seq[:], seqID)
	h.Write(seq[:])
	h.Write(queryNonce)
	return h.Sum(nil)
}

func (qm *QueryManager) issueQuery() {
	qm.issueMu.Lock()
	defer qm.issueMu.Unlock()
	qm.issueQuerySerial()
}

                                                                          
                                                                           
                                      
func (qm *QueryManager) issueQuerySerial() {
	_, currentHeight := qm.store.GetLastAccepted()
	nextHeight := currentHeight + 1
	pollParams := pollParamsFromConfig(*qm.config)
	if qm.consensusParams != nil {
		var ok bool
		pollParams, _, ok = qm.consensusParams.ParamsForAnchor(currentHeight)
		if !ok {
			                                                                        
			                                                                       
			                                      
			return
		}
	}

	blocks := qm.store.GetByHeight(nextHeight)
	if len(blocks) == 0 {
		return
	}

	                       
	blockID := qm.engine.GetPreference(nextHeight)
	if blockID != "" {
		if _, exists := qm.store.Get(blockID); !exists {
			logs.Warn("[QueryManager] Preferred block %s missing at height %d; waiting for consensus state to recover",
				blockID, nextHeight)
			return
		}
	}
	if blockID == "" {
		                                                                      
		                                                        
		return
	}

	block, exists := qm.store.Get(blockID)
	if !exists {
		logs.Warn("[QueryManager] Block %s not found for query at height %d (pending?), retrying after backoff", blockID, nextHeight)
		                                                                               
		time.AfterFunc(500*time.Millisecond, func() {
			qm.tryIssueQuery()
		})
		return
	}
	                              
	parentID := block.Header.ParentID
	window := block.Header.Window
	vrfSeed := computeVRFSeed(parentID, nextHeight, window, qm.nodeID)

	                                                                            
	                                                                            
	                                                                        
	                                              
	if !qm.seqHeightSet || qm.seqHeight != nextHeight {
		qm.seqID = 0
		qm.seqHeight = nextHeight
		qm.seqHeightSet = true
		qm.pushedBlockHeight = nextHeight
		qm.pushedHeightSet = true
		qm.pushedBlocks = make(map[string]struct{})
	}
	if !bytes.Equal(vrfSeed, qm.lastSeed) {
		qm.lastSeed = vrfSeed
	}

	requestID, err := secureRandUint32()
	if err != nil {
		logs.Warn("[QueryManager] failed to generate secure request ID: %v", err)
		return
	}
	queryNonce := make([]byte, SnowmanQueryNonceSize)
	if _, err := cryptorand.Read(queryNonce); err != nil {
		logs.Warn("[QueryManager] failed to generate secure query nonce: %v", err)
		return
	}
	querySeqID := qm.seqID

	allPeers := append([]types.NodeID(nil), qm.transport.GetAllPeers(qm.nodeID)...)
	sort.Slice(allPeers, func(i, j int) bool {
		return allPeers[i] < allPeers[j]
	})
	uniquePeers := make([]types.NodeID, 0, len(allPeers))
	seenPeers := make(map[types.NodeID]struct{}, len(allPeers))
	for _, peer := range allPeers {
		if peer == "" || peer == qm.nodeID {
			continue
		}
		if _, duplicate := seenPeers[peer]; duplicate {
			continue
		}
		seenPeers[peer] = struct{}{}
		if qm.livenessTracker == nil || qm.livenessTracker.IsActive(string(peer)) {
			uniquePeers = append(uniquePeers, peer)
			continue
		}
	}
	qm.issueProbationProbe(block.Header.Height, vrfSeed, querySeqID, pollParams)
	slotCount := pollParams.K
	weightedSample := BuildReplacementSample(
		uniquePeers,
		slotCount,
		computePollSampleSeed(vrfSeed, querySeqID, queryNonce),
	)
	if len(weightedSample) == 0 {
		return
	}
	peers := make([]types.NodeID, 0, len(weightedSample))
	sampleWeights := make(map[types.NodeID]int, len(weightedSample))
	for _, sample := range weightedSample {
		peers = append(peers, sample.Value)
		sampleWeights[sample.Value] = sample.Weight
	}

	queryKey := ""
	if registrar, ok := qm.engine.(queryParamsRegistrar); ok {
		queryKey = registrar.RegisterQueryWithParams(
			qm.nodeID,
			requestID,
			blockID,
			block.Header.Height,
			querySeqID,
			vrfSeed,
			sampleWeights,
			pollParams,
		)
	} else {
		queryKey = qm.engine.RegisterQuery(
			qm.nodeID,
			requestID,
			blockID,
			block.Header.Height,
			querySeqID,
			vrfSeed,
			sampleWeights,
		)
	}
	if queryKey == "" {
		                                                                      
		                                                                 
		return
	}

	                                                                 
	                                                                
	                                                                           
	                                
	qm.mu.Lock()
	defer qm.mu.Unlock()
	if state, ok := qm.engine.(queryFinalizationState); ok && state.IsFinalizing(nextHeight) {
		return
	}
	if finalized, ok := qm.store.GetFinalizedAtHeight(nextHeight); ok && finalized != nil {
		return
	}
	if state, ok := qm.engine.(interface{ IsQueryActive(string) bool }); ok && !state.IsQueryActive(queryKey) {
		return
	}

	sampledSet := make(map[types.NodeID]struct{}, len(peers))
	for _, p := range peers {
		sampledSet[p] = struct{}{}
	}
	poll := &Poll{
		requestID:     requestID,
		blockID:       blockID,
		queryKey:      queryKey,
		startTime:     time.Now(),
		height:        block.Header.Height,
		vrfSeed:       vrfSeed,
		seqID:         querySeqID,
		queryNonce:    append([]byte(nil), queryNonce...),
		sampledPeers:  peers,
		sampledSet:    sampledSet,
		sampleWeights: sampleWeights,
	}
	qm.activePolls.Store(requestID, poll)
	                                                                                                                
	                                                                                      
	qm.seqID++

	                                             
	isProposer := (block.Header.Proposer == string(qm.nodeID))

	sendFullBlock := false
	if isProposer {
		if !qm.pushedHeightSet || qm.pushedBlockHeight != nextHeight {
			qm.pushedBlockHeight = nextHeight
			qm.pushedHeightSet = true
			qm.pushedBlocks = make(map[string]struct{})
		}
		if _, alreadyPushed := qm.pushedBlocks[blockID]; !alreadyPushed {
			qm.pushedBlocks[blockID] = struct{}{}
			sendFullBlock = true
		}
	}

	var msg types.Message
	if sendFullBlock {
		                                                         
		msg = types.Message{
			Type:       types.MsgPushQuery,
			From:       qm.nodeID,
			RequestID:  requestID,
			BlockID:    blockID,
			Block:      block,                            
			Height:     block.Header.Height,
			VRFSeed:    vrfSeed,
			SeqID:      querySeqID,
			QueryNonce: append([]byte(nil), queryNonce...),
		}
		logs.Debug("[Node %s] Sending PushQuery for block %s (I'm the proposer, seqID=%d)",
			qm.nodeID, blockID, querySeqID)
	} else {
		                                                     
		msg = types.Message{
			Type:       types.MsgPullQuery,
			From:       qm.nodeID,
			RequestID:  requestID,
			BlockID:    blockID,
			Height:     block.Header.Height,
			VRFSeed:    vrfSeed,
			SeqID:      querySeqID,
			QueryNonce: append([]byte(nil), queryNonce...),
		}
		logs.Debug("[Node %s] Sending PullQuery for block %s (seqID=%d)",
			qm.nodeID, blockID, querySeqID)
	}

	if transport, ok := qm.transport.(queryBroadcastTransport); ok {
		transport.BroadcastQuery(msg, peers, func(peer types.NodeID, err error) {
			qm.handleQuerySendFailure(requestID, peer, err)
		})
	} else {
		qm.transport.Broadcast(msg, peers)
	}
	if qm.node != nil {
		qm.node.Stats.Mu.Lock()
		qm.node.Stats.QueriesSent++
		qm.node.Stats.QueriesPerHeight[block.Header.Height]++
		qm.node.Stats.Mu.Unlock()
	}
}

                                                                                
                                                                         
                                                                               
func (qm *QueryManager) handleQuerySendFailure(requestID uint32, peer types.NodeID, err error) {
	value, exists := qm.activePolls.Load(requestID)
	if !exists {
		return
	}
	poll, ok := value.(*Poll)
	if !ok || poll == nil {
		return
	}
	if _, sampled := poll.sampledSet[peer]; !sampled {
		return
	}
	if _, alreadySettled := poll.respondedPeers.LoadOrStore(peer, struct{}{}); alreadySettled {
		return
	}
	logs.Debug("[QueryManager] Query send failed request=%d peer=%s; settling sampled slots as abstentions: %v",
		requestID, peer, err)
	qm.engine.SubmitChit(peer, poll.queryKey, "", nil)
}

func (qm *QueryManager) issueProbationProbe(height uint64, vrfSeed []byte, seqID uint32, pollParams ConsensusPollParams) {
	if qm.livenessReporter == nil || qm.livenessTracker == nil {
		return
	}
	                                                                          
	                                                                         
	                                                                   
	if !qm.claimProbationProbeHeight(height) {
		return
	}

	activeReporters, probationPeers := qm.probeMembershipSnapshot()
	if len(activeReporters) == 0 || len(probationPeers) == 0 {
		return
	}
	reporterCount := probationProbeReporterCount(pollParams, len(activeReporters))
	selected := make([]types.NodeID, 0)
	for _, target := range probationPeers {
		group := buildProbationProbeReporterGroup(activeReporters, target, height, reporterCount)
		if containsNodeID(group, qm.nodeID) {
			selected = append(selected, target)
		}
	}
	if len(selected) == 0 {
		return
	}
	requestID, err := secureRandUint32()
	if err != nil {
		return
	}
	if _, collision := qm.activePolls.Load(requestID); collision {
		return
	}
	if _, collision := qm.probeSessions.Load(requestID); collision {
		return
	}
	nonce := make([]byte, SnowmanQueryNonceSize)
	if _, err := cryptorand.Read(nonce); err != nil {
		return
	}
	targets := make(map[types.NodeID]struct{}, len(selected))
	for _, target := range selected {
		targets[target] = struct{}{}
	}
	session := &probationProbeSession{
		requestID:  requestID,
		height:     height,
		vrfSeed:    append([]byte(nil), vrfSeed...),
		seqID:      seqID,
		queryNonce: append([]byte(nil), nonce...),
		targets:    targets,
	}
	qm.probeSessions.Store(requestID, session)

	msg := types.Message{
		Type:       types.MsgPullQuery,
		From:       qm.nodeID,
		RequestID:  requestID,
		BlockID:    "",                                                          
		Height:     height,
		VRFSeed:    append([]byte(nil), vrfSeed...),
		SeqID:      seqID,
		QueryNonce: append([]byte(nil), nonce...),
	}
	for peer := range targets {
		if err := qm.transport.Send(peer, msg); err != nil {
			session.failed.Store(peer, struct{}{})
		}
	}
	timeout := qm.config.QueryTimeout
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	time.AfterFunc(timeout, func() { qm.finishProbationProbe(session) })
}

func (qm *QueryManager) claimProbationProbeHeight(height uint64) bool {
	if qm == nil {
		return false
	}
	qm.probeMu.Lock()
	defer qm.probeMu.Unlock()
	if qm.lastProbeHeightSet && height <= qm.lastProbeHeight {
		return false
	}
	qm.lastProbeHeight = height
	qm.lastProbeHeightSet = true
	return true
}

func (qm *QueryManager) probeMembershipSnapshot() (active, probation []types.NodeID) {
	if qm == nil || qm.livenessTracker == nil {
		return nil, nil
	}
	for _, address := range qm.livenessTracker.GetActiveMiners() {
		if address == "" {
			continue
		}
		switch {
		case qm.livenessTracker.IsActive(address):
			active = append(active, types.NodeID(address))
		case qm.livenessTracker.GetMinerState(address) == 0:
			probation = append(probation, types.NodeID(address))
		}
	}
	return canonicalNodeIDs(active), canonicalNodeIDs(probation)
}

func canonicalNodeIDs(nodes []types.NodeID) []types.NodeID {
	if len(nodes) == 0 {
		return nil
	}
	sorted := append([]types.NodeID(nil), nodes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	result := sorted[:0]
	for _, node := range sorted {
		if node == "" || (len(result) > 0 && result[len(result)-1] == node) {
			continue
		}
		result = append(result, node)
	}
	return result
}

                                                                              
                                                                              
                                                                          
                                                                             
                                                                            
                                                                              
                                                                            
func probationProbeReporterCount(params ConsensusPollParams, activeCount int) int {
	if activeCount <= 0 {
		return 0
	}
	count := params.K * params.Beta
	if count < probationProbeMinReporters {
		count = probationProbeMinReporters
	}
	if count > activeCount {
		count = activeCount
	}
	return count
}

func buildProbationProbeReporterGroup(active []types.NodeID, target types.NodeID, height uint64, reporterCount int) []types.NodeID {
	active = canonicalNodeIDs(active)
	if len(active) == 0 {
		return nil
	}
	filtered := active[:0]
	for _, reporter := range active {
		if reporter != target {
			filtered = append(filtered, reporter)
		}
	}
	active = filtered
	if len(active) == 0 {
		return nil
	}
	count := reporterCount
	if count < probationProbeMinReporters {
		count = probationProbeMinReporters
	}
	if count > len(active) {
		count = len(active)
	}
	digest := sha256.Sum256(append([]byte("dex/probation-probe/reporter/v1\x00"), []byte(target)...))
	n := uint64(len(active))
	base := binary.BigEndian.Uint64(digest[:8]) % n
	round := uint64(0)
	if height > 0 {
		round = (height - 1) / probationProbeGroupHeights
	}
	                                                                     
	                                                                           
	                                                                            
	                                                            
	start := (base + (round%n)*uint64(count)) % n
	result := make([]types.NodeID, count)
	for i := 0; i < count; i++ {
		result[i] = active[(start+uint64(i))%n]
	}
	return result
}

func containsNodeID(nodes []types.NodeID, target types.NodeID) bool {
	for _, node := range nodes {
		if node == target {
			return true
		}
	}
	return false
}

func (qm *QueryManager) finishProbationProbe(session *probationProbeSession) {
	if session == nil || !qm.probeSessions.CompareAndDelete(session.requestID, session) {
		return
	}
	sampled := make([]string, 0, len(session.targets))
	toReportOffline := make([]string, 0)
	for peer := range session.targets {
		if _, failed := session.failed.Load(peer); failed {
			continue
		}
		sampled = append(sampled, string(peer))
		_, responded := session.responded.Load(peer)
		                                                                         
		                                                                            
		                                                                            
		                                                                         
		                                                                           
		                                                                          
		if !responded {
			toReportOffline = append(toReportOffline, string(peer))
		}
	}
	sort.Strings(sampled)
	sort.Strings(toReportOffline)
	qm.reportLivenessObservations(sampled, toReportOffline)
}

func (qm *QueryManager) reportLivenessObservations(sampled, offline []string) {
	if qm == nil || qm.livenessReporter == nil || (len(sampled) == 0 && len(offline) == 0) {
		return
	}
	if atomic, ok := qm.livenessReporter.(interfaces.LivenessAtomicReportSink); ok {
		atomic.ReportLiveness(sampled, offline)
		return
	}
	if len(sampled) > 0 {
		qm.livenessReporter.ReportSampled(sampled)
	}
	if len(offline) > 0 {
		qm.livenessReporter.ReportOffline(offline)
	}
}

func (qm *QueryManager) reportLivenessOnline(peer types.NodeID) {
	if qm == nil || qm.livenessReporter == nil {
		return
	}
	if sink, ok := qm.livenessReporter.(interfaces.LivenessOnlineReportSink); ok {
		addresses := []string{string(peer)}
		sink.ReportLivenessWithOnline(addresses, nil, addresses)
	}
}

func NewQueryManager(id types.NodeID, transport interfaces.Transport, store interfaces.BlockStore, engine interfaces.ConsensusEngine, config *ConsensusConfig, events interfaces.EventBus, logger logs.Logger) *QueryManager {
	qm := &QueryManager{
		nodeID:       id,
		transport:    transport,
		store:        store,
		engine:       engine,
		config:       config,
		events:       events,
		Logger:       logger,
		issueWake:    make(chan struct{}, 1),
		pollStats:    make(map[types.NodeID]*peerPollRing),
		pushedBlocks: make(map[string]struct{}),
	}

	events.Subscribe(types.EventQueryComplete, func(e interfaces.Event) {
		data, ok := e.Data().(QueryCompleteData)
		if !ok {
			if ptr, okPtr := e.Data().(*QueryCompleteData); okPtr && ptr != nil {
				data = *ptr
				ok = true
			}
		}
		if !ok {
			logs.Debug("[QueryManager] QueryComplete event has unexpected data type: %T", e.Data())
			return
		}

		if data.Reason == "finalizing" {
			                                                                 
			                                                                  
			qm.discardPollsByQueryKeys(data.QueryKeys)
		} else {
			qm.cleanupPollsByQueryKeys(data.QueryKeys, data.Reason)
		}

		if (data.Reason == "timeout" || data.Reason == "parent_missing") && len(data.QueryKeys) > 0 {
			                                                                                       
			                                         
			backoff := 300*time.Millisecond + time.Duration(rand.Int63n(400))*time.Millisecond
			logs.Debug("[QueryManager] Query %s, will retry after %v (count=%d)", data.Reason, backoff, len(data.QueryKeys))
			time.AfterFunc(backoff, func() {
				qm.tryIssueQuery()
			})
		}
	})

	events.Subscribe(types.EventSyncComplete, func(e interfaces.Event) {
		                                                                    
		qm.tryIssueQuery()
	})

	events.Subscribe(types.EventBlockReceived, func(e interfaces.Event) {
		                                             
		if qm.engine.GetActiveQueryCount() == 0 {
			qm.tryIssueQuery()
		}
	})

	return qm
}

                                                                        
func (qm *QueryManager) SetSyncManager(sm *SyncManager) {
	qm.syncManager = sm
}

                                                     
func (qm *QueryManager) SetLivenessReporter(reporter interfaces.LivenessReportSink) {
	qm.livenessReporter = reporter
}

                                                                         
                                                                           
func (qm *QueryManager) SetLivenessTracker(tracker interfaces.LivenessTracker) {
	qm.livenessTracker = tracker
}

                                                                             
                                                                          
func (qm *QueryManager) tryIssueQuery() {
	select {
	case qm.issueWake <- struct{}{}:
	default:
	}
}

                                                          
func (qm *QueryManager) issueQueryIfReady() {
	qm.issueMu.Lock()
	defer qm.issueMu.Unlock()
	if qm.livenessTracker == nil || !qm.livenessTracker.IsActive(string(qm.nodeID)) {
		return
	}

	                                          
	if qm.syncManager != nil {
		if qm.syncManager.ShouldPauseConsensusForCatchUp() {
			return
		}
	}

	qm.mu.Lock()

	_, currentHeight := qm.store.GetLastAccepted()
	nextHeight := currentHeight + 1
	if state, ok := qm.engine.(queryFinalizationState); ok && state.IsFinalizing(nextHeight) {
		qm.mu.Unlock()
		return
	}

	blocks := qm.store.GetByHeight(nextHeight)
	if len(blocks) == 0 {
		qm.mu.Unlock()
		return
	}

	if qm.engine.GetActiveQueryCount() >= qm.config.MaxConcurrentQueries {
		qm.mu.Unlock()
		return
	}
	if state, ok := qm.engine.(queryPipelineState); ok {
		networkActive, totalOutstanding := state.GetQueryPipelineCounts(nextHeight)
		if networkActive >= maxPerHeightPollPipeline ||
			totalOutstanding >= maxPerHeightPollOutstanding {
			qm.mu.Unlock()
			return
		}
	} else if qm.activePollCountAtHeight(nextHeight) >= maxPerHeightPollPipeline {
		                                                                   
		                                                       
		qm.mu.Unlock()
		return
	}

	                                                                               
	if qm.shouldBackoffForSenderPressureLocked() {
		qm.mu.Unlock()
		return
	}
	qm.mu.Unlock()

	qm.issueQuerySerial()
}

func (qm *QueryManager) activePollCountAtHeight(height uint64) int {
	count := 0
	qm.activePolls.Range(func(_, value interface{}) bool {
		poll, ok := value.(*Poll)
		if ok && poll.height == height {
			count++
		}
		return count < maxPerHeightPollPipeline
	})
	return count
}

                                                                                          
func (qm *QueryManager) shouldBackoffForSenderPressureLocked() bool {
	now := time.Now()
	controlLen, controlCap, immediateLen, immediateCap, ok := qm.senderQueueState()
	if !ok {
		return false
	}

	if controlCap <= 0 {
		controlCap = 64
	}
	controlUsage := float64(controlLen) / float64(controlCap)
	if controlUsage >= 0.5 {
		pause := 50 * time.Millisecond
		if controlUsage >= 0.75 {
			pause = 150 * time.Millisecond
		}
		if controlUsage >= 0.9 {
			pause = 300 * time.Millisecond
		}
		until := now.Add(pause)
		if until.After(qm.controlBackoffUntil) {
			qm.controlBackoffUntil = until
		}
	}
	if immediateCap <= 0 {
		immediateCap = 1024
	}
	immediateUsage := float64(immediateLen) / float64(immediateCap)
	if immediateUsage >= 0.25 {
		pause := 50 * time.Millisecond
		if immediateUsage >= 0.5 {
			pause = 150 * time.Millisecond
		}
		if immediateUsage >= 0.75 {
			pause = 300 * time.Millisecond
		}
		until := now.Add(pause)
		if until.After(qm.controlBackoffUntil) {
			qm.controlBackoffUntil = until
		}
	}

	return now.Before(qm.controlBackoffUntil)
}

func (qm *QueryManager) senderQueueState() (controlLen, controlCap, immediateLen, immediateCap int, ok bool) {
	rt, isReal := qm.transport.(*RealTransport)
	if !isReal || rt == nil || rt.senderManager == nil || rt.senderManager.SendQueue == nil {
		return 0, 0, 0, 0, false
	}

	sq := rt.senderManager.SendQueue
	controlLen = sq.ControlQueueLen()
	controlCap = 64
	immediateLen = sq.ImmediateQueueLen()
	immediateCap = 1024
	for _, ch := range sq.GetChannelStats() {
		switch ch.Name {
		case "controlChan":
			controlCap = ch.Cap
		case "immediateChan":
			immediateCap = ch.Cap
		}
	}
	return controlLen, controlCap, immediateLen, immediateCap, true
}

func secureRandUint32() (uint32, error) {
	var b [4]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

func parseProposerFromBlockID(blockID string) (types.NodeID, bool) {
	                                                              
	if !strings.HasPrefix(blockID, "block-") {
		return "", false
	}
	rest := strings.TrimPrefix(blockID, "block-")
	heightSep := strings.Index(rest, "-")
	if heightSep <= 0 || heightSep+1 >= len(rest) {
		return "", false
	}
	rest = rest[heightSep+1:]                               
	proposerSep := strings.Index(rest, "-")
	if proposerSep <= 0 {
		return "", false
	}
	proposer := rest[:proposerSep]
	return types.NodeID(proposer), proposer != ""
}

func parseHeightFromBlockID(blockID string) (uint64, bool) {
	                                             
	if !strings.HasPrefix(blockID, "block-") {
		return 0, false
	}
	rest := strings.TrimPrefix(blockID, "block-")
	sep := strings.Index(rest, "-")
	if sep <= 0 {
		return 0, false
	}
	h, err := strconv.ParseUint(rest[:sep], 10, 64)
	return h, err == nil
}

                                                                               
func (qm *QueryManager) RequestBlock(blockID string, from types.NodeID) {
	if blockID == "" || from == "" {
		return
	}
	if _, exists := qm.store.Get(blockID); exists {
		return
	}
	                                                                                     
	if qm.syncManager != nil {
		_, localAccepted := qm.store.GetLastAccepted()
		if blockHeight, ok := parseHeightFromBlockID(blockID); ok && blockHeight > localAccepted {
			if diff := blockHeight - localAccepted; diff > qm.syncManager.deepLagStateSyncThreshold() {
				qm.syncManager.TriggerSyncFromChit(blockHeight, from)
				return
			}
		}
	}

	if qm.reserveMissingBlockRequest(blockID, time.Now()) {
		logs.Warn("[QueryManager] Requesting missing block %s from %s", blockID, from)
		if err := qm.transport.Send(from, types.Message{
			Type:      types.MsgGet,
			From:      qm.nodeID,
			RequestID: 0,                                   
			BlockID:   blockID,
		}); err != nil {
			logs.Warn("[QueryManager] Failed to request missing block %s from %s: %v",
				blockID, from, err)
		}
	}
}

                                                                                           
                                                                                              
func (qm *QueryManager) reserveMissingBlockRequest(blockID string, now time.Time) bool {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	if qm.missingBlockRequests == nil {
		qm.missingBlockRequests = make(map[string]time.Time)
	}
	if last, exists := qm.missingBlockRequests[blockID]; exists {
		if now.Sub(last) < missingBlockRetryInterval {
			return false
		}
	} else if len(qm.missingBlockRequests) >= maxMissingBlockRequests {
		for id, last := range qm.missingBlockRequests {
			if now.Sub(last) >= missingBlockRetryInterval {
				delete(qm.missingBlockRequests, id)
			}
		}
		if len(qm.missingBlockRequests) >= maxMissingBlockRequests {
			return false
		}
	}
	qm.missingBlockRequests[blockID] = now
	return true
}

func (qm *QueryManager) HandleChit(msg types.Message) {
	traceOutcome := "inactive_poll"
	var tracePoll *Poll
	if logs.ConsensusTraceEnabled() {
		started := time.Now()
		defer func() { qm.traceReceivedChit(msg, tracePoll, traceOutcome, started) }()
	}
	if qm.handleProbationProbeChit(msg) {
		traceOutcome = "probation_probe"
		return
	}
	verifiedHeightHint := false
	if poll, ok := qm.activePolls.Load(msg.RequestID); ok {
		p := poll.(*Poll)
		tracePoll = p
		from := types.NodeID(msg.From)
		if qm.livenessTracker != nil && !qm.livenessTracker.IsActive(string(from)) {
			traceOutcome = "inactive_responder"
			return
		}

		if msg.RequestID != p.requestID {
			traceOutcome = "request_mismatch"
			logs.Debug("[QueryManager] Reject chit from %s: request ID %d does not match active poll %d",
				from, msg.RequestID, p.requestID)
			return
		}
		if len(msg.QueryNonce) != SnowmanQueryNonceSize || !bytes.Equal(msg.QueryNonce, p.queryNonce) {
			traceOutcome = "nonce_mismatch"
			logs.Debug("[QueryManager] Reject chit from %s: query nonce mismatch (req=%d height=%d)",
				from, p.requestID, p.height)
			return
		}
		if msg.PreferredIDHeight != p.height {
			traceOutcome = "height_mismatch"
			logs.Debug("[QueryManager] Reject chit from %s: query height %d does not match active poll %d (req=%d)",
				from, msg.PreferredIDHeight, p.height, p.requestID)
			return
		}

		                                                                           
		if _, sampled := p.sampledSet[from]; !sampled {
			traceOutcome = "unsampled"
			logs.Debug("[QueryManager] Reject chit from %s: not in sampled set (req=%d height=%d)",
				from, p.requestID, p.height)
			return
		}
		                                                                          
		accepted := msg.ChitSignature
		if len(msg.ChitSignature) == 0 {
			traceOutcome = "unsigned"
			logs.Debug("[QueryManager] Reject chit from %s: missing ECDSA signature (req=%d height=%d preferred=%s)",
				from, p.requestID, p.height, msg.PreferredID)
			return
		}
		if _, hasPub := ResolveNodePublicKey(string(from)); !hasPub {
			traceOutcome = "missing_public_key"
			logs.Warn("[QueryManager] Reject chit from %s: missing public key (req=%d height=%d preferred=%s)",
				from, p.requestID, p.height, msg.PreferredID)
			return
		}
		digest := ComputeChitDigest(ChitDigestContext{
			ChainID:        pb.GetDefaultChainID(),
			RequestID:      p.requestID,
			Requester:      string(qm.nodeID),
			Responder:      string(from),
			PreferredBlock: msg.PreferredID,
			QueryHeight:    p.height,
			AcceptedBlock:  msg.AcceptedID,
			AcceptedHeight: msg.AcceptedHeight,
			VRFSeed:        p.vrfSeed,
			SeqID:          p.seqID,
			QueryNonce:     p.queryNonce,
		})
		if !VerifyChitSignature(string(from), digest, msg.ChitSignature) {
			traceOutcome = "invalid_signature"
			logs.Warn("[QueryManager] Reject chit from %s: ECDSA verify failed (req=%d height=%d preferred=%s seq=%d)",
				from, p.requestID, p.height, msg.PreferredID, p.seqID)
			return
		}
		if current, active := qm.activePolls.Load(msg.RequestID); !active || current != p {
			traceOutcome = "expired_during_verification"
			return
		}
		                                                                       
		                                                                        
		if _, duplicate := p.realRespondedPeers.LoadOrStore(from, struct{}{}); !duplicate {
			qm.reportLivenessOnline(from)
		}
		if _, duplicate := p.respondedPeers.LoadOrStore(from, struct{}{}); duplicate {
			traceOutcome = "duplicate"
			logs.Debug("[QueryManager] Reject duplicate chit from %s (req=%d height=%d)",
				from, p.requestID, p.height)
			return
		}
		verifiedHeightHint = true
		traceOutcome = "verified"
		if msg.PreferredID != "" {
			if _, exists := qm.store.Get(msg.PreferredID); !exists {
				qm.RequestBlock(msg.PreferredID, from)
			}
		}

		qm.engine.SubmitChit(from, p.queryKey, msg.PreferredID, accepted)
	}

	                                                                                                           
	if verifiedHeightHint && qm.syncManager != nil && msg.AcceptedHeight > 0 {
		_, localAccepted := qm.store.GetLastAccepted()
		if msg.AcceptedHeight > localAccepted {
			qm.syncManager.TriggerSyncFromChit(msg.AcceptedHeight, types.NodeID(msg.From))
		}
	}
}

func (qm *QueryManager) handleProbationProbeChit(msg types.Message) bool {
	value, exists := qm.probeSessions.Load(msg.RequestID)
	if !exists {
		return false
	}
	session, ok := value.(*probationProbeSession)
	if !ok || session == nil {
		return true
	}
	from := types.NodeID(msg.From)
	if _, targeted := session.targets[from]; !targeted {
		return false
	}
	if msg.RequestID != session.requestID || msg.PreferredIDHeight != session.height || msg.PreferredID != "" ||
		len(msg.QueryNonce) != SnowmanQueryNonceSize || !bytes.Equal(msg.QueryNonce, session.queryNonce) || len(msg.ChitSignature) == 0 {
		return true
	}
	if _, hasPub := ResolveNodePublicKey(string(from)); !hasPub {
		return true
	}
	digest := ComputeChitDigest(ChitDigestContext{
		ChainID:        pb.GetDefaultChainID(),
		RequestID:      session.requestID,
		Requester:      string(qm.nodeID),
		Responder:      string(from),
		PreferredBlock: "",
		QueryHeight:    session.height,
		AcceptedBlock:  msg.AcceptedID,
		AcceptedHeight: msg.AcceptedHeight,
		VRFSeed:        session.vrfSeed,
		SeqID:          session.seqID,
		QueryNonce:     session.queryNonce,
	})
	if !VerifyChitSignature(string(from), digest, msg.ChitSignature) {
		return true
	}
	if current, active := qm.probeSessions.Load(msg.RequestID); !active || current != session {
		return true
	}
	if _, duplicate := session.responded.LoadOrStore(from, struct{}{}); !duplicate {
		qm.reportLivenessOnline(from)
	}
	                                                                             
	                                                                              
	return true
}

func (qm *QueryManager) discardPollsByQueryKeys(keys []string) {
	if len(keys) == 0 {
		return
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		keySet[key] = struct{}{}
	}

	                                                                           
	                                                                            
	                                      
	qm.mu.Lock()
	defer qm.mu.Unlock()
	qm.activePolls.Range(func(k, v interface{}) bool {
		poll, ok := v.(*Poll)
		if ok {
			if _, discard := keySet[poll.queryKey]; discard {
				qm.activePolls.Delete(k)
			}
		}
		return true
	})
}

func (qm *QueryManager) cleanupPollsByQueryKeys(keys []string, reason string) {
	if len(keys) == 0 {
		return
	}
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[k] = struct{}{}
	}

	                                                                                       
	                                                                
	type pollSnapshot struct {
		sampled   []types.NodeID
		responded map[types.NodeID]struct{}
	}
	var polls []pollSnapshot
	removed := 0
	qm.activePolls.Range(func(k, v interface{}) bool {
		p := v.(*Poll)
		if _, ok := keySet[p.queryKey]; ok {
			resp := make(map[types.NodeID]struct{})
			p.respondedPeers.Range(func(key, _ interface{}) bool {
				if nid, ok := key.(types.NodeID); ok {
					resp[nid] = struct{}{}
				}
				return true
			})
			polls = append(polls, pollSnapshot{
				sampled:   p.sampledPeers,
				responded: resp,
			})
			qm.activePolls.Delete(k)
			removed++
		}
		return true
	})

	if len(polls) == 0 {
		return
	}

	                                 
	  
	                                        
	                                                                                
	                                                                                   
	                                                                  
	  
	                                        
	                                                                                        
	                                                                                                  
	                                                    
	                                                                                   
	                                                             
	  
	                                                                                     
	                                                                               
	validPolls := 0
	skippedPolls := 0

	                                                                                              
	                                                                 
	var sampledAddrs []string
	sampledSet := make(map[types.NodeID]struct{})
	for _, p := range polls {
		if len(p.responded) == 0 {
			skippedPolls++
			continue
		}
		validPolls++
		for _, peer := range p.sampled {
			if _, dup := sampledSet[peer]; dup {
				continue
			}
			sampledSet[peer] = struct{}{}
			sampledAddrs = append(sampledAddrs, string(peer))
		}
	}
	                                                       
	var toReport []string
	qm.pollStatsMu.Lock()
	for _, p := range polls {
		if len(p.responded) == 0 {
			continue
		}
		for _, peer := range p.sampled {
			st := qm.pollStats[peer]
			if st == nil {
				st = &peerPollRing{}
				qm.pollStats[peer] = st
			}
			_, responded := p.responded[peer]
			st.push(!responded)
		}
	}
	for peer, st := range qm.pollStats {
		if st.count < pollStatsMinSamples {
			continue
		}
		if float64(st.miss)/float64(st.count) > pollStatsOfflineRatio {
			toReport = append(toReport, string(peer))
			delete(qm.pollStats, peer)
		}
	}
	qm.pollStatsMu.Unlock()

	qm.reportLivenessObservations(sampledAddrs, toReport)
	if len(toReport) > 0 && qm.livenessReporter != nil {
		logs.Info("[QueryManager] Reported %d offline miner(s) after cleanup (reason=%s): %v",
			len(toReport), reason, toReport)
	} else if removed > 0 {
		logs.Debug("[QueryManager] Cleaned %d poll(s) (reason=%s, valid=%d, skipped=%d, sampled=%d)",
			removed, reason, validPolls, skippedPolls, len(sampledAddrs))
	}
}

func (qm *QueryManager) Start(ctx context.Context) {
	qm.startOnce.Do(func() {
		go qm.runQueryScheduler(ctx)
	})
}

func (qm *QueryManager) runQueryScheduler(ctx context.Context) {
	interval := qm.config.PollInterval
	if interval <= 0 {
		logs.Error("[QueryManager] invalid PollInterval %v; scheduler not started", interval)
		return
	}

	cleanupLogger := logs.SetThreadNodeContext(string(qm.nodeID))
	defer cleanupLogger()
	timer := time.NewTimer(0)
	defer timer.Stop()
	var nextAttempt time.Time

	for {
		select {
		case <-timer.C:
		case <-qm.issueWake:
		case <-ctx.Done():
			return
		}

		now := time.Now()
		if !nextAttempt.IsZero() && now.Before(nextAttempt) {
			resetQueryTimer(timer, time.Until(nextAttempt))
			continue
		}

		                                                                   
		                                             
		attemptStarted := now
		qm.issueQueryIfReady()
		nextAttempt = attemptStarted.Add(interval)
		resetQueryTimer(timer, time.Until(nextAttempt))
	}
}

func resetQueryTimer(timer *time.Timer, delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}
