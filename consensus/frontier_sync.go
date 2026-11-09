package consensus

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"dex/pb"
	"dex/servicepeer"
	"dex/types"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	AcceptedFrontierStatusOK             = "ok"
	AcceptedFrontierStatusAnchorMismatch = "anchor_mismatch"
	AcceptedFrontierStatusBelowTarget    = "below_target"
	AcceptedFrontierStatusUnavailable    = "unavailable"
)

var (
	errLightFrontierNotReady = errors.New("light accepted frontier has no hard quorum")
	errLightSyncYield        = errors.New("light sync yielded to local Snowman candidate")
)

                                                                              
                                                                             
                                                                          
type AcceptedFrontierQuery struct {
	Requester     types.NodeID
	Nonce         []byte
	AnchorHeight  uint64
	AnchorBlockID string
	TargetHeight  uint64
}

                                                                          
                                                                            
                                                                              
            
type AcceptedFrontierVote struct {
	Peer types.NodeID
	                                                                           
	                                                                            
	                                                                   
	RequestDigest    []byte
	Status           string
	AcceptedHeight   uint64
	ObservedAnchorID string
	TargetHeight     uint64
	TargetBlockID    string
	Signature        []byte
}

                                                                       
                                               
type FrontierQueryTransport interface {
	SendAcceptedFrontierQuery(peer types.NodeID, query AcceptedFrontierQuery, callback func(AcceptedFrontierVote)) error
}

type frontierRound struct {
	sessionID uint64
	query     AcceptedFrontierQuery
	startedAt time.Time
	light     bool
	k         int
	alpha     int

	sampled    map[types.NodeID]struct{}
	dispatched map[types.NodeID]struct{}
	responses  map[types.NodeID]AcceptedFrontierVote
	supporters map[types.NodeID]struct{}
	targetID   string
}

func (sm *SyncManager) frontierPollTimeout() time.Duration {
	timeout := 2 * time.Second
	if sm != nil && sm.config != nil && sm.config.FrontierPollTimeout > 0 {
		timeout = sm.config.FrontierPollTimeout
	}
	return timeout
}

func (sm *SyncManager) frontierTarget(anchorHeight, heightHint uint64, light bool) uint64 {
	if heightHint <= anchorHeight {
		return anchorHeight
	}
	batchSize := uint64(50)
	if sm != nil && sm.config != nil && sm.config.BatchSize > 0 {
		batchSize = sm.config.BatchSize
	}
	if batchSize > MaxFutureBlockCacheWindow {
		batchSize = MaxFutureBlockCacheWindow
	}
	if light {
		batchSize = 1
	}
	                                                                          
	                                                                              
	                          
	if heightHint-anchorHeight <= batchSize {
		return heightHint
	}
	return anchorHeight + batchSize
}

                                                                            
                                                                            
                                                                             
                                                                            
                                                                              
func (sm *SyncManager) hasLocalSnowmanCandidate(anchorID string, targetHeight uint64) bool {
	if sm == nil || sm.store == nil || anchorID == "" || targetHeight == 0 {
		return false
	}
	for _, block := range sm.store.GetByHeight(targetHeight) {
		if block == nil || block.ID == "" {
			continue
		}
		if block.Header.Height == targetHeight && block.Header.ParentID == anchorID {
			err := validateCandidatePreStateRoot(sm.store, block)
			if err == nil {
				return true
			}
			if !errors.Is(err, ErrInvalidCandidatePreStateRoot) {
				return true
			}
		}
	}
	return false
}

                                                                             
                                                                         
                                                                            
func (sm *SyncManager) execFrontierStage(session *SyncSession) *SyncError {
	if session == nil || !sm.isActiveSyncSession(session, SyncStageFrontier) {
		return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrTransient, Message: "frontier session is no longer active"}
	}
	transport, ok := sm.transport.(FrontierQueryTransport)
	if !ok || transport == nil {
		return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrNetwork,
			Message: fmt.Sprintf("transport %T does not support accepted-frontier queries", sm.transport)}
	}
	anchorID, anchorHeight := sm.store.GetLastAccepted()
	frontierK, frontierAlpha := sm.frontierK, sm.frontierAlpha
	if sm.consensusParams != nil {
		params, _, ready := sm.consensusParams.ParamsForAnchor(anchorHeight)
		if !ready {
			return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrTransient,
				Message: fmt.Sprintf("consensus voter snapshot is not ready for finalized anchor %d", anchorHeight)}
		}
		frontierK = params.K
		frontierAlpha = params.AlphaConfidence
	}
	if frontierK <= 0 || frontierAlpha <= frontierK/2 || frontierAlpha > frontierK {
		return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrDeterministic,
			Message: fmt.Sprintf("invalid frontier K/Alpha (require K/2 < Alpha <= K): K=%d Alpha=%d", frontierK, frontierAlpha)}
	}

	heightHint := session.TargetHeight
	if heightHint <= anchorHeight {
		heightHint = session.PeerAcceptedHeight
	}
	targetHeight := sm.frontierTarget(anchorHeight, heightHint, session.Light)
	if targetHeight <= anchorHeight {
		sm.setSyncSessionStage(session, SyncStageDone, "frontier target already reached")
		return nil
	}
	if session.Light && sm.hasLocalSnowmanCandidate(anchorID, targetHeight) {
		sm.setSyncSessionStage(session, SyncStageDone,
			fmt.Sprintf("ordinary Snowman owns local candidate at height %d", targetHeight))
		return nil
	}

	                                                                           
	                                                                         
	                                                                          
	                              
	sampleCount := frontierK
	if known := len(sm.transport.GetAllPeers(sm.nodeID)); known > sampleCount {
		sampleCount = known
	}
	peers := sm.filterBannedPeers(sm.transport.SamplePeers(sm.nodeID, sampleCount))
	uniquePeers := make([]types.NodeID, 0, len(peers))
	seen := make(map[types.NodeID]struct{}, len(peers))
	for _, peer := range peers {
		if peer == "" || peer == sm.nodeID {
			continue
		}
		if sm.livenessTracker != nil && !sm.livenessTracker.IsActive(string(peer)) {
			continue
		}
		if _, exists := seen[peer]; exists {
			continue
		}
		seen[peer] = struct{}{}
		uniquePeers = append(uniquePeers, peer)
		if len(uniquePeers) == frontierK {
			break
		}
	}
	if len(uniquePeers) < frontierK {
		return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrNetwork,
			Message: fmt.Sprintf("only %d unique frontier peers available, need K=%d", len(uniquePeers), frontierK)}
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrDeterministic,
			Message: fmt.Sprintf("generate frontier nonce: %v", err)}
	}
	query := AcceptedFrontierQuery{
		Requester:     sm.nodeID,
		Nonce:         nonce,
		AnchorHeight:  anchorHeight,
		AnchorBlockID: anchorID,
		TargetHeight:  targetHeight,
	}
	round := &frontierRound{
		sessionID:  session.ID,
		query:      query,
		light:      session.Light,
		k:          frontierK,
		alpha:      frontierAlpha,
		sampled:    seen,
		responses:  make(map[types.NodeID]AcceptedFrontierVote, len(uniquePeers)),
		supporters: make(map[types.NodeID]struct{}),
	}

	sm.Mu.Lock()
	if sm.activeSession != session || session.Stage != SyncStageFrontier {
		sm.Mu.Unlock()
		return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrTransient, Message: "frontier session changed before dispatch"}
	}
	if sm.frontierRound != nil && sm.frontierRound.sessionID == session.ID {
		sm.Mu.Unlock()
		return nil
	}
	sm.frontierRound = round
	session.FrontierAnchorHeight = anchorHeight
	session.FrontierAnchorID = anchorID
	session.FrontierTargetHeight = targetHeight
	session.FrontierTargetID = ""
	session.frontierNonce = append(session.frontierNonce[:0], nonce...)
	session.frontierSupporters = nil
	session.frontierTried = nil
	session.serviceTried = nil
	session.LastUpdated = time.Now()
	sm.Mu.Unlock()

	dispatchedPeers := make(map[types.NodeID]struct{}, len(uniquePeers))
	var dispatchMu sync.Mutex
	dispatchReady := false
	dispatchCanceled := false
	pendingVotes := make([]AcceptedFrontierVote, 0)
	for _, peer := range uniquePeers {
		peer := peer
		if err := transport.SendAcceptedFrontierQuery(peer, query, func(vote AcceptedFrontierVote) {
			if vote.Peer == "" {
				vote.Peer = peer
			}
			dispatchMu.Lock()
			if !dispatchReady {
				if !dispatchCanceled {
					pendingVotes = append(pendingVotes, vote)
				}
				dispatchMu.Unlock()
				return
			}
			dispatchMu.Unlock()
			go sm.HandleAcceptedFrontierVote(vote)
		}); err != nil {
			sm.Logger.Debug("[SyncFrontier] query dispatch failed peer=%s anchor=%d target=%d: %v",
				peer, anchorHeight, targetHeight, err)
			continue
		}
		dispatchedPeers[peer] = struct{}{}
	}
	dispatched := len(dispatchedPeers)
	                                                                           
	                                                                            
	                                                                        
	if dispatched < round.alpha {
		dispatchMu.Lock()
		dispatchCanceled = true
		pendingVotes = nil
		dispatchMu.Unlock()
		sm.Mu.Lock()
		if sm.frontierRound == round {
			sm.frontierRound = nil
		}
		sm.Mu.Unlock()
		return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrNetwork,
			Message: fmt.Sprintf("dispatched %d frontier queries from fixed K=%d sample, need Alpha=%d", dispatched, round.k, round.alpha)}
	}
	sm.Mu.Lock()
	if sm.frontierRound != round || sm.activeSession != session || session.Stage != SyncStageFrontier {
		if sm.frontierRound == round {
			sm.frontierRound = nil
		}
		sm.Mu.Unlock()
		dispatchMu.Lock()
		dispatchCanceled = true
		pendingVotes = nil
		dispatchMu.Unlock()
		return &SyncError{Stage: SyncStageFrontier, Kind: SyncErrTransient, Message: "frontier session changed during dispatch"}
	}
	round.dispatched = dispatchedPeers
	                                                                     
	                                                                           
	round.startedAt = time.Now()
	session.LastUpdated = round.startedAt
	sm.Mu.Unlock()
	dispatchMu.Lock()
	dispatchReady = true
	readyVotes := append([]AcceptedFrontierVote(nil), pendingVotes...)
	pendingVotes = nil
	dispatchMu.Unlock()
	for _, vote := range readyVotes {
		vote := vote
		go sm.HandleAcceptedFrontierVote(vote)
	}
	sm.Logger.Info("[SyncFrontier] session=%d poll anchor=%d/%s target=%d K=%d Alpha=%d sampled=%d dispatched=%d dispatch_failed=%d light=%t",
		session.ID, anchorHeight, anchorID, targetHeight, round.k, round.alpha,
		len(uniquePeers), dispatched, len(uniquePeers)-dispatched, session.Light)
	return nil
}

                                                                          
                                                                   
                            
func (sm *SyncManager) HandleAcceptedFrontierVote(vote AcceptedFrontierVote) {
	if sm == nil || vote.Peer == "" {
		return
	}

	sm.Mu.RLock()
	round := sm.frontierRound
	if round == nil {
		sm.Mu.RUnlock()
		return
	}
	capturedRound := round
	query := round.query
	expectedRequestDigest := ComputeAcceptedFrontierRequestDigest(query)
	_, sampled := round.dispatched[vote.Peer]
	_, duplicate := round.responses[vote.Peer]
	sm.Mu.RUnlock()
	                                                                          
	                                                                          
	                                                                           
	if len(vote.RequestDigest) != len(expectedRequestDigest) ||
		subtle.ConstantTimeCompare(vote.RequestDigest, expectedRequestDigest) != 1 {
		return
	}
	                                                                           
	                                                                             
	                                                        
	roundExpired := !round.startedAt.IsZero() && time.Since(round.startedAt) > sm.frontierPollTimeout()
	if !sampled || duplicate || vote.TargetHeight != query.TargetHeight || roundExpired {
		return
	}
	if err := validateAcceptedFrontierVote(query, vote); err != nil {
		sm.recordPeerFailure(vote.Peer, fmt.Sprintf("invalid accepted-frontier vote: %v", err))
		return
	}
	digest := ComputeAcceptedFrontierDigest(query, vote)
	if !VerifyChitSignature(string(vote.Peer), digest, vote.Signature) {
		sm.recordPeerFailure(vote.Peer, "invalid accepted-frontier signature")
		return
	}

	var (
		quorumSession *SyncSession
		conflictErr   error
		retrySession  *SyncSession
		retryErr      error
		loweredFrom   uint64
		loweredTo     uint64
		noQuorum      string
	)
	sm.Mu.Lock()
	round = sm.frontierRound
	if round == nil || round != capturedRound || round.sessionID == 0 || !bytes.Equal(round.query.Nonce, query.Nonce) {
		sm.Mu.Unlock()
		return
	}
	                                                                          
	                                                                       
	                                             
	if !round.startedAt.IsZero() && time.Since(round.startedAt) > sm.frontierPollTimeout() {
		sm.Mu.Unlock()
		return
	}
	if _, sampled = round.dispatched[vote.Peer]; !sampled {
		sm.Mu.Unlock()
		return
	}
	if _, duplicate = round.responses[vote.Peer]; duplicate {
		sm.Mu.Unlock()
		return
	}
	transition := applyVerifiedFrontierVote(frontierVoteTransitionInput{
		query:        round.query,
		alpha:        round.alpha,
		roundStarted: !round.startedAt.IsZero(),
		dispatched:   round.dispatched,
		responses:    round.responses,
		vote:         vote,
	})
	if !transition.accepted {
		sm.Mu.Unlock()
		return
	}
	round.responses = transition.responses

	if transition.decision == frontierVoteConflict {
		quorumSession = sm.activeSession
		sm.frontierRound = nil
		conflictErr = fmt.Errorf("accepted frontier conflicts with local finalized anchor at height %d: local=%s mismatch_support=%d observed=%v",
			round.query.AnchorHeight, round.query.AnchorBlockID, transition.conflictTotal, transition.conflictReport)
	}
	if transition.decision == frontierVoteQuorum {
		quorumSession = sm.activeSession
		if quorumSession == nil || quorumSession.ID != round.sessionID || quorumSession.Stage != SyncStageFrontier {
			quorumSession = nil
		} else {
			supporters := make(map[types.NodeID]struct{}, len(transition.supporters))
			for _, peer := range transition.supporters {
				supporters[peer] = struct{}{}
			}
			round.supporters = supporters
			round.targetID = transition.targetID
			quorumSession.FrontierAnchorHeight = round.query.AnchorHeight
			quorumSession.FrontierAnchorID = round.query.AnchorBlockID
			quorumSession.FrontierTargetHeight = round.query.TargetHeight
			quorumSession.FrontierTargetID = transition.targetID
			trustedHeight, ok := frontierTrustedAcceptedHeight(round, round.alpha)
			if !ok || trustedHeight < round.query.TargetHeight {
				trustedHeight = round.query.TargetHeight
			}
			quorumSession.PeerAcceptedHeight = trustedHeight
			quorumSession.frontierSupporters = supporters
			quorumSession.frontierTried = make(map[types.NodeID]struct{})
			quorumSession.serviceTried = make(map[types.NodeID]struct{})
			quorumSession.LastUpdated = time.Now()
			sm.frontierRound = nil
		}
	}
	if conflictErr == nil && quorumSession == nil && transition.decision == frontierVoteNoQuorum {
		noQuorum = frontierNoQuorumSummary(round)
		retrySession, retryErr, loweredFrom, loweredTo = sm.closeFrontierRoundWithoutQuorumLocked(round, time.Now())
	}
	sm.Mu.Unlock()

	if conflictErr != nil {
		sm.Logger.Warn("[SyncFrontier] %v; local finalized state is retained", conflictErr)
		sm.invokeSafetyFaultHandler(conflictErr)
		if quorumSession != nil {
			sm.finishSyncSession(quorumSession, conflictErr)
		}
		return
	}
	if quorumSession == nil {
		if retrySession != nil {
			if errors.Is(retryErr, errLightFrontierNotReady) {
				sm.Logger.Info("[SyncFrontier] session=%d light poll found no hard quorum; wait for a later hint: %s",
					retrySession.ID, noQuorum)
				sm.finishSyncSession(retrySession, nil)
				return
			}
			sm.Logger.Warn("[SyncFrontier] session=%d poll completed without quorum: %s", retrySession.ID, noQuorum)
			if retryErr != nil {
				sm.finishSyncSession(retrySession, retryErr)
				return
			}
			if loweredTo > 0 {
				sm.Logger.Info("[SyncFrontier] session=%d lower target %d -> %d from Alpha-compatible accepted heights",
					retrySession.ID, loweredFrom, loweredTo)
			}
			go sm.runStateMachine(retrySession)
		}
		return
	}
	sm.Logger.Info("[SyncFrontier] session=%d quorum target=%d block=%s supporters=%d",
		quorumSession.ID, quorumSession.FrontierTargetHeight, quorumSession.FrontierTargetID, len(quorumSession.frontierSupporters))
	if sm.setSyncSessionStage(quorumSession, SyncStageBlockCatchUp,
		fmt.Sprintf("range=%d-%d target=%s", quorumSession.FrontierAnchorHeight+1,
			quorumSession.FrontierTargetHeight, quorumSession.FrontierTargetID)) {
		go sm.runStateMachine(quorumSession)
	}
}

func (sm *SyncManager) processAcceptedFrontierTimeout(now time.Time) {
	if sm == nil {
		return
	}
	var session *SyncSession
	var retryErr error
	var loweredFrom uint64
	var loweredTo uint64
	var noQuorum string
	sm.Mu.Lock()
	round := sm.frontierRound
	if round == nil || round.startedAt.IsZero() || now.Sub(round.startedAt) <= sm.frontierPollTimeout() {
		sm.Mu.Unlock()
		return
	}
	if sm.activeSession != nil && sm.activeSession.ID == round.sessionID && sm.activeSession.Stage == SyncStageFrontier {
		noQuorum = frontierNoQuorumSummary(round)
		session, retryErr, loweredFrom, loweredTo = sm.closeFrontierRoundWithoutQuorumLocked(round, now)
	}
	sm.Mu.Unlock()
	if session == nil {
		return
	}
	if errors.Is(retryErr, errLightFrontierNotReady) {
		sm.Logger.Info("[SyncFrontier] session=%d light poll timed out without hard quorum; wait for a later hint: %s",
			session.ID, noQuorum)
		sm.finishSyncSession(session, nil)
		return
	}
	sm.Logger.Warn("[SyncFrontier] session=%d poll timeout without quorum: %s", session.ID, noQuorum)
	if retryErr != nil {
		sm.finishSyncSession(session, retryErr)
		return
	}
	if loweredTo > 0 {
		sm.Logger.Info("[SyncFrontier] session=%d poll timeout lowered target %d -> %d from Alpha-compatible accepted heights",
			session.ID, loweredFrom, loweredTo)
	}
	sm.Logger.Warn("[SyncFrontier] session=%d poll timeout, retry=%d/%d", session.ID, session.StageRetries, maxStageRetries)
	go sm.runStateMachine(session)
}

                                                                            
                                                                     
                                                                         
                                                               
func frontierRoundResponsesComplete(round *frontierRound) bool {
	return round != nil && !round.startedAt.IsZero() && len(round.dispatched) > 0 && len(round.responses) >= len(round.dispatched)
}

type frontierResponseSummaryKey struct {
	status         string
	acceptedHeight uint64
	targetBlockID  string
}

                                                                             
                                                                            
                                                                         
                                                            
func frontierNoQuorumSummary(round *frontierRound) string {
	if round == nil {
		return "sampled=0 dispatched=0 responses=0 missing=0 dispatch_failed=0 votes=[]"
	}
	counts := make(map[frontierResponseSummaryKey]int, len(round.responses))
	for _, response := range round.responses {
		key := frontierResponseSummaryKey{
			status:         response.Status,
			acceptedHeight: response.AcceptedHeight,
			targetBlockID:  response.TargetBlockID,
		}
		counts[key]++
	}
	keys := make([]frontierResponseSummaryKey, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].status != keys[j].status {
			return keys[i].status < keys[j].status
		}
		if keys[i].acceptedHeight != keys[j].acceptedHeight {
			return keys[i].acceptedHeight < keys[j].acceptedHeight
		}
		return keys[i].targetBlockID < keys[j].targetBlockID
	})
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, fmt.Sprintf("{status=%q acceptedHeight=%d targetBlockID=%q count=%d}",
			key.status, key.acceptedHeight, key.targetBlockID, counts[key]))
	}
	dispatched := len(round.dispatched)
	responses := len(round.responses)
	missing := dispatched - responses
	if missing < 0 {
		missing = 0
	}
	dispatchFailed := len(round.sampled) - dispatched
	if dispatchFailed < 0 {
		dispatchFailed = 0
	}
	return fmt.Sprintf("sampled=%d dispatched=%d responses=%d missing=%d dispatch_failed=%d votes=[%s]",
		len(round.sampled), dispatched, responses, missing, dispatchFailed, strings.Join(entries, ","))
}

                                                                             
                                                                            
                                                                            
                                                                               
                                     
func (sm *SyncManager) closeFrontierRoundWithoutQuorumLocked(round *frontierRound, now time.Time) (*SyncSession, error, uint64, uint64) {
	if round == nil || sm.frontierRound != round || sm.activeSession == nil ||
		sm.activeSession.ID != round.sessionID || sm.activeSession.Stage != SyncStageFrontier {
		return nil, nil, 0, 0
	}
	session := sm.activeSession
	oldTarget := round.query.TargetHeight
	if session.Light {
		session.LastUpdated = now
		sm.frontierRound = nil
		return session, errLightFrontierNotReady, 0, 0
	}
	if lower, ok := frontierLowerTarget(round, round.alpha); ok {
		session.TargetHeight = lower
		session.PeerAcceptedHeight = lower
		session.StageRetries = 0
		session.LastUpdated = now
		sm.frontierRound = nil
		return session, nil, oldTarget, lower
	}
	session.StageRetries++
	session.LastUpdated = now
	sm.frontierRound = nil
	if session.StageRetries > maxStageRetries {
		return session, fmt.Errorf("accepted-frontier poll ended without quorum after %d attempts", session.StageRetries), 0, 0
	}
	return session, nil, 0, 0
}

func frontierLowerTarget(round *frontierRound, alpha int) (uint64, bool) {
	height, ok := frontierTrustedAcceptedHeight(round, alpha)
	if !ok || height <= round.query.AnchorHeight || height >= round.query.TargetHeight {
		return 0, false
	}
	return height, true
}

func frontierTrustedAcceptedHeight(round *frontierRound, alpha int) (uint64, bool) {
	if round == nil || alpha <= 0 {
		return 0, false
	}
	heights := make([]uint64, 0, len(round.responses))
	for _, response := range round.responses {
		if response.ObservedAnchorID != round.query.AnchorBlockID {
			continue
		}
		if response.Status != AcceptedFrontierStatusOK && response.Status != AcceptedFrontierStatusBelowTarget {
			continue
		}
		heights = append(heights, response.AcceptedHeight)
	}
	if len(heights) < alpha {
		return 0, false
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] > heights[j] })
	return heights[alpha-1], true
}

func (sm *SyncManager) invokeSafetyFaultHandler(err error) {
	if sm == nil || err == nil {
		return
	}
	sm.Mu.RLock()
	handler := sm.safetyFaultHandler
	sm.Mu.RUnlock()
	if handler != nil {
		handler(err)
	}
}

func (sm *SyncManager) nextFrontierSupporter(session *SyncSession) (types.NodeID, bool) {
	if sm == nil || session == nil {
		return "", false
	}
	sm.Mu.Lock()
	defer sm.Mu.Unlock()
	if sm.activeSession != session || session.Stage != SyncStageBlockCatchUp {
		return "", false
	}
	peers := make([]types.NodeID, 0, len(session.frontierSupporters))
	for peer := range session.frontierSupporters {
		if session.frontierTried != nil {
			if _, tried := session.frontierTried[peer]; tried {
				continue
			}
		}
		if sm.isPeerBanned(peer) {
			continue
		}
		peers = append(peers, peer)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })
	if len(peers) == 0 {
		return "", false
	}
	if session.frontierTried == nil {
		session.frontierTried = make(map[types.NodeID]struct{})
	}
	peer := peers[0]
	session.frontierTried[peer] = struct{}{}
	return peer, true
}

const serviceSyncProviderAttemptsPerRound = 2

                                                                           
                                                                                   
                                                                              
                                                                   
func (sm *SyncManager) nextServiceProvider(session *SyncSession, fromHeight, toHeight uint64) (types.NodeID, bool) {
	if sm == nil || session == nil || fromHeight > toHeight {
		return "", false
	}
	sm.Mu.RLock()
	if sm.activeSession != session || session.Stage != SyncStageBlockCatchUp {
		sm.Mu.RUnlock()
		return "", false
	}
	servicePeers := sm.servicePeers
	if len(session.serviceTried) >= serviceSyncProviderAttemptsPerRound {
		sm.Mu.RUnlock()
		return "", false
	}
	excluded := make(map[string]struct{}, len(session.serviceTried)+1)
	excluded[string(sm.nodeID)] = struct{}{}
	for peer := range session.serviceTried {
		excluded[string(peer)] = struct{}{}
	}
	sm.Mu.RUnlock()
	if servicePeers == nil {
		return "", false
	}

	peers, err := servicePeers.SampleVerified(servicepeer.SampleOptions{
		Limit:          serviceSyncProviderAttemptsPerRound,
		Archive:        &servicepeer.HeightRange{From: fromHeight, To: toHeight},
		ExcludeNodeIDs: excluded,
	})
	if err != nil {
		sm.Logger.Warn("[SyncFrontier] sample verified service providers for range=%d-%d failed: %v",
			fromHeight, toHeight, err)
		return "", false
	}
	for _, entry := range peers {
		peer := types.NodeID(entry.Record.NodeID)
		if peer == "" || peer == sm.nodeID || sm.isPeerBanned(peer) {
			continue
		}
		sm.Mu.Lock()
		if sm.activeSession != session || session.Stage != SyncStageBlockCatchUp ||
			session.FrontierAnchorHeight+1 != fromHeight || session.FrontierTargetHeight != toHeight {
			sm.Mu.Unlock()
			return "", false
		}
		if session.serviceTried == nil {
			session.serviceTried = make(map[types.NodeID]struct{})
		}
		if _, tried := session.serviceTried[peer]; tried {
			sm.Mu.Unlock()
			continue
		}
		session.serviceTried[peer] = struct{}{}
		sm.Mu.Unlock()
		return peer, true
	}
	return "", false
}

func (sm *SyncManager) markFrontierSupporterTried(session *SyncSession, peer types.NodeID) {
	if sm == nil || session == nil || peer == "" {
		return
	}
	sm.Mu.Lock()
	defer sm.Mu.Unlock()
	if sm.activeSession != session {
		return
	}
	if session.frontierTried == nil {
		session.frontierTried = make(map[types.NodeID]struct{})
	}
	session.frontierTried[peer] = struct{}{}
}

func (sm *SyncManager) handleFrontierSyncResponse(msg types.Message, meta syncRequestMeta) {
	session, err := sm.validateFrontierResponseContext(msg, meta)
	if err != nil {
		sm.Logger.Warn("[SyncFrontier] reject stale or unbound block response from %s: %v", msg.From, err)
		return
	}
	blocks, err := sm.validateFrontierBatch(msg, meta)
	if err != nil {
		_, acceptedHeight := sm.store.GetLastAccepted()
		if meta.ProviderRole == syncProviderService {
			sm.markServiceProviderFailure(meta, acceptedHeight)
		} else {
			sm.recordPeerFailure(types.NodeID(msg.From), fmt.Sprintf("invalid frontier block batch: %v", err))
		}
		sm.Logger.Warn("[SyncFrontier] invalid batch from %s session=%d: %v", msg.From, session.ID, err)
		go sm.runStateMachine(session)
		return
	}

	finalized, acceptedHeight, finalizeErr := sm.finalizeFrontierBatch(blocks, meta, session.Light)
	if errors.Is(finalizeErr, errLightSyncYield) {
		sm.Logger.Info("[SyncFrontier] session=%d ordinary Snowman candidate arrived before light-sync commit; abandon catch-up",
			session.ID)
		sm.finishSyncSession(session, nil)
		return
	}
	if finalized > 0 {
		atomic.StoreUint32(&sm.consecutiveStateRootFaults, 0)
		if meta.ProviderRole == syncProviderService {
			sm.markServiceProviderSuccess(meta, acceptedHeight)
		} else {
			sm.resetPeerFailure(types.NodeID(msg.From))
		}
		sm.updateSyncSession(session, func(s *SyncSession) { s.FinalizedBlocks += finalized })
	}
	if finalizeErr != nil {
		if bytes.Contains([]byte(finalizeErr.Error()), []byte("stateRoot mismatch")) {
			atomic.AddUint32(&sm.consecutiveStateRootFaults, 1)
		}
		sm.Logger.Warn("[SyncFrontier] session=%d finalized prefix=%d then stopped at accepted=%d: %v",
			session.ID, finalized, acceptedHeight, finalizeErr)
	}
	sm.continueAfterFrontierBatch(session, finalized, acceptedHeight, finalizeErr)
}

func (sm *SyncManager) validateFrontierResponseContext(msg types.Message, meta syncRequestMeta) (*SyncSession, error) {
	if types.NodeID(msg.From) != meta.Peer {
		return nil, fmt.Errorf("response peer %s does not match requested provider %s", msg.From, meta.Peer)
	}
	sm.Mu.RLock()
	session := sm.activeSession
	valid := session != nil && session.ID == meta.FrontierSessionID && session.Stage == SyncStageBlockCatchUp &&
		bytes.Equal(session.frontierNonce, meta.FrontierNonce) &&
		session.FrontierAnchorHeight == meta.FrontierAnchor && session.FrontierAnchorID == meta.FrontierAnchorID &&
		session.FrontierTargetHeight == meta.FrontierTarget && session.FrontierTargetID == meta.FrontierTargetID
	_, supporter := sessionSupporter(session, meta.Peer)
	providerAuthorized := false
	switch meta.ProviderRole {
	case syncProviderFrontierSupporter:
		providerAuthorized = supporter
	case syncProviderService:
		                                                                     
		                                                                             
		                                                      
		providerAuthorized = true
	}
	sm.Mu.RUnlock()
	if !valid || !providerAuthorized {
		return nil, fmt.Errorf("response is not bound to the active frontier round and authorized provider")
	}
	if msg.FromHeight != meta.FromHeight || msg.ToHeight != meta.ToHeight ||
		meta.FromHeight != meta.FrontierAnchor+1 || meta.ToHeight != meta.FrontierTarget {
		return nil, fmt.Errorf("response range %d-%d does not match frontier range %d-%d",
			msg.FromHeight, msg.ToHeight, meta.FrontierAnchor+1, meta.FrontierTarget)
	}
	return session, nil
}

func sessionSupporter(session *SyncSession, peer types.NodeID) (*SyncSession, bool) {
	if session == nil || session.frontierSupporters == nil {
		return session, false
	}
	_, ok := session.frontierSupporters[peer]
	return session, ok
}

func (sm *SyncManager) validateFrontierBatch(msg types.Message, meta syncRequestMeta) ([]*types.Block, error) {
	if meta.FrontierTarget <= meta.FrontierAnchor ||
		meta.FrontierTarget-meta.FrontierAnchor > AcceptedFrontierMaxSpan {
		return nil, fmt.Errorf("invalid frontier range %d-%d", meta.FrontierAnchor+1, meta.FrontierTarget)
	}
	expectedCount := meta.FrontierTarget - meta.FrontierAnchor
	if uint64(len(msg.Blocks)) != expectedCount {
		return nil, fmt.Errorf("incomplete block range: got=%d expected=%d", len(msg.Blocks), expectedCount)
	}
	blocks := append([]*types.Block(nil), msg.Blocks...)
	validatedPayloads := make([]*pb.Block, len(blocks))
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i] == nil {
			return false
		}
		if blocks[j] == nil {
			return true
		}
		return blocks[i].Header.Height < blocks[j].Header.Height
	})

	expectedParent := meta.FrontierAnchorID
	for i, block := range blocks {
		expectedHeight := meta.FrontierAnchor + 1 + uint64(i)
		if block == nil {
			return nil, fmt.Errorf("nil block at response index %d", i)
		}
		if block.Header.Height != expectedHeight {
			return nil, fmt.Errorf("height gap at index %d: got=%d expected=%d", i, block.Header.Height, expectedHeight)
		}
		if block.Header.ParentID != expectedParent {
			return nil, fmt.Errorf("parent mismatch at height %d: got=%s expected=%s",
				block.Header.Height, block.Header.ParentID, expectedParent)
		}
		fullBlock, err := sm.validateFrontierBlockPayload(msg, block)
		if err != nil {
			return nil, fmt.Errorf("block %s at height %d: %w", block.ID, block.Header.Height, err)
		}
		validatedPayloads[i] = fullBlock
		expectedParent = block.ID
	}
	if len(blocks) == 0 || blocks[len(blocks)-1].ID != meta.FrontierTargetID {
		got := ""
		if len(blocks) > 0 {
			got = blocks[len(blocks)-1].ID
		}
		return nil, fmt.Errorf("terminal block mismatch: got=%s expected=%s", got, meta.FrontierTargetID)
	}
	                                                                             
	                                                                             
	                                                
	for i, fullBlock := range validatedPayloads {
		if fullBlock == nil {
			continue
		}
		if !SealCachedBlock(fullBlock) {
			return nil, fmt.Errorf("block %s payload is outside the accepted cache window", blocks[i].ID)
		}
	}
	return blocks, nil
}

func (sm *SyncManager) validateFrontierBlockPayload(msg types.Message, block *types.Block) (*pb.Block, error) {
	var fullBlock *pb.Block
	if raw := msg.SyncBlocksRaw[block.ID]; len(raw) > 0 {
		var decoded pb.Block
		if err := pb.UnmarshalSanitize(raw, &decoded); err != nil {
			return nil, fmt.Errorf("decode full payload: %w", err)
		}
		fullBlock = &decoded
	} else if cached, ok := GetCachedBlock(block.ID); ok {
		fullBlock = cached
	}
	if fullBlock == nil {
		if _, realStore := sm.store.(*RealBlockStore); realStore {
			return nil, fmt.Errorf("missing full block payload")
		}
		return nil, nil
	}
	if sanitized, changed := sanitizeBlockTxsForCanonicalValidation(fullBlock); changed {
		fullBlock = sanitized
	}
	if fullBlock.BlockHash != block.ID {
		return nil, fmt.Errorf("payload ID=%s does not match candidate ID=%s", fullBlock.BlockHash, block.ID)
	}
	if fullBlock.Header == nil || fullBlock.Header.Height != block.Header.Height ||
		fullBlock.Header.PrevBlockHash != block.Header.ParentID {
		return nil, fmt.Errorf("payload header does not match consensus block")
	}
	if err := ValidateBlockCommitment(fullBlock); err != nil {
		return nil, fmt.Errorf("commitment validation failed: %w", err)
	}
	if realStore, ok := sm.store.(*RealBlockStore); ok {
		if err := realStore.lightweightValidateBlock(fullBlock); err != nil {
			return nil, fmt.Errorf("transaction validation failed: %w", err)
		}
	}
	return fullBlock, nil
}

                                                                             
                                                                               
                                                                                
                             
func (sm *SyncManager) finalizeFrontierBatch(
	blocks []*types.Block,
	meta syncRequestMeta,
	light bool,
) (int, uint64, error) {
	sm.frontierFinalizeMu.Lock()
	defer sm.frontierFinalizeMu.Unlock()

	acceptedID, acceptedHeight := sm.store.GetLastAccepted()
	if acceptedHeight < meta.FrontierAnchor || (acceptedHeight == meta.FrontierAnchor && acceptedID != meta.FrontierAnchorID) {
		return 0, acceptedHeight, fmt.Errorf("local finalized anchor changed: got=%d/%s expected=%d/%s",
			acceptedHeight, acceptedID, meta.FrontierAnchor, meta.FrontierAnchorID)
	}
	if acceptedHeight == meta.FrontierAnchor &&
		acceptedID == meta.FrontierAnchorID &&
		light &&
		sm.hasLocalSnowmanCandidate(meta.FrontierAnchorID, meta.FrontierTarget) {
		                                                                   
		                                                                    
		                                                                    
		                           
		currentID, currentHeight := sm.store.GetLastAccepted()
		if currentID == acceptedID && currentHeight == acceptedHeight {
			return 0, acceptedHeight, fmt.Errorf("%w at height %d", errLightSyncYield, meta.FrontierTarget)
		}
		acceptedID, acceptedHeight = currentID, currentHeight
	}
	finalized := 0
	for _, block := range blocks {
		if block.Header.Height <= acceptedHeight {
			already, ok := sm.store.GetFinalizedAtHeight(block.Header.Height)
			if !ok || already == nil || already.ID != block.ID {
				return finalized, acceptedHeight, fmt.Errorf("local finalized chain conflicts at height %d", block.Header.Height)
			}
			continue
		}
		if block.Header.Height != acceptedHeight+1 || block.Header.ParentID != acceptedID {
			return finalized, acceptedHeight, fmt.Errorf("local tip moved off verified chain before height %d", block.Header.Height)
		}
		if _, err := sm.store.Add(block); err != nil {
			return finalized, acceptedHeight, fmt.Errorf("add verified block %s: %w", block.ID, err)
		}
		if err := sm.store.SetFinalized(block.Header.Height, block.ID); err != nil {
			if !errors.Is(err, ErrAlreadyFinalized) {
				return finalized, acceptedHeight, fmt.Errorf("finalize verified block %s: %w", block.ID, err)
			}
		}
		newID, newHeight := sm.store.GetLastAccepted()
		if newHeight != block.Header.Height || newID != block.ID {
			return finalized, newHeight, fmt.Errorf("chain mismatch after frontier finalize: got=%d/%s expected=%d/%s",
				newHeight, newID, block.Header.Height, block.ID)
		}
		acceptedID, acceptedHeight = newID, newHeight
		finalized++
	}
	return finalized, acceptedHeight, nil
}

func (sm *SyncManager) continueAfterFrontierBatch(session *SyncSession, finalized int, acceptedHeight uint64, batchErr error) {
	if session == nil || !sm.isActiveSyncSession(session, SyncStageBlockCatchUp) {
		return
	}
	_, currentAccepted := sm.store.GetLastAccepted()
	if currentAccepted > acceptedHeight {
		acceptedHeight = currentAccepted
	}
	heightHint := session.PeerAcceptedHeight

	if batchErr != nil && finalized == 0 {
		                                                                     
		                                                                 
		go sm.runStateMachine(session)
		return
	}
	if heightHint > acceptedHeight {
		if sm.shouldRestartBlockCatchUpAsDeepSync(session, acceptedHeight, heightHint) {
			trigger := session.Trigger
			sm.Logger.Info("[SyncFrontier] session=%d became deeply behind again local=%d peer=%d diff=%d threshold=%d; restart stateDB-first sync",
				session.ID, acceptedHeight, heightHint, heightHint-acceptedHeight, sm.deepLagStateSyncThreshold())
			sm.finishSyncSession(session, batchErr)
			go sm.runSyncPipeline(trigger, heightHint, acceptedHeight)
			return
		}
		if session.Light {
			                                                                    
			                                                                      
			                                                   
			sm.finishSyncSession(session, batchErr)
			go sm.runSyncPipeline(session.Trigger, heightHint, acceptedHeight)
			return
		}
		sm.updateSyncSession(session, func(s *SyncSession) {
			s.PeerAcceptedHeight = heightHint
			s.TargetHeight = sm.frontierTarget(acceptedHeight, heightHint, false)
			s.FrontierTargetID = ""
			s.frontierSupporters = nil
			s.frontierTried = nil
			s.serviceTried = nil
		})
		if sm.setSyncSessionStage(session, SyncStageFrontier,
			fmt.Sprintf("next_anchor=%d next_hint=%d", acceptedHeight, heightHint)) {
			go sm.runStateMachine(session)
		}
		return
	}

	total := session.FinalizedBlocks
	if total > 0 && sm.events != nil {
		sm.events.PublishAsync(types.BaseEvent{EventType: types.EventSyncComplete, EventData: total})
	}
	sm.finishSyncSession(session, batchErr)
}

func (sm *SyncManager) shouldRestartBlockCatchUpAsDeepSync(session *SyncSession, localAccepted, maxPeer uint64) bool {
	if sm == nil || session == nil || session.Light || session.Stage != SyncStageBlockCatchUp || maxPeer <= localAccepted {
		return false
	}
	return maxPeer-localAccepted > sm.deepLagStateSyncThreshold()
}
