package consensus

import (
	"dex/pb"
	"dex/types"
	"fmt"
	"sync/atomic"
	"time"
)

                                                                                       

                                             
type SyncTrigger string

const (
	                           
	SyncTriggerPeriodic SyncTrigger = "periodic"
	                                               
	SyncTriggerChit SyncTrigger = "chit"
	                        
	SyncTriggerManual SyncTrigger = "manual"
)

                                                          
type SyncStage string

const (
	                                                                  
	SyncStagePlan SyncStage = "plan"
	                                                        
	SyncStageCertChain SyncStage = "cert_chain"
	                                                      
	SyncStageStateSnapshot SyncStage = "state_snapshot"
	                                                                           
	SyncStageFastForward SyncStage = "fast_forward"
	                                                                           
	                                                                            
	SyncStageFrontier SyncStage = "frontier"
	                                                
	SyncStageBlockCatchUp SyncStage = "block_catch_up"
	                  
	SyncStageDone SyncStage = "done"
	                  
	SyncStageFailed SyncStage = "failed"
)

                                                                          
type SyncErrorKind string

const (
	                                                                                  
	SyncErrDeterministic SyncErrorKind = "deterministic"
	                                                                                       
	SyncErrNetwork SyncErrorKind = "network"
	                                                                                         
	SyncErrTransient SyncErrorKind = "transient"
)

                                                             
type SyncError struct {
	Stage   SyncStage
	Kind    SyncErrorKind
	Message string
}

func (e *SyncError) Error() string {
	return fmt.Sprintf("[%s][%s] %s", e.Stage, e.Kind, e.Message)
}

                                                                                                
type SyncSession struct {
	ID uint64

	Trigger SyncTrigger
	Stage   SyncStage

	LocalAcceptedHeight uint64
	PeerAcceptedHeight  uint64
	TargetHeight        uint64
	HeightDiff          uint64

	DeepLag         bool
	CertifiedHeight uint64
	FastForwarded   bool
	Light           bool

	FrontierAnchorHeight uint64
	FrontierAnchorID     string
	FrontierTargetHeight uint64
	FrontierTargetID     string
	frontierNonce        []byte
	frontierSupporters   map[types.NodeID]struct{}
	frontierTried        map[types.NodeID]struct{}
	serviceTried         map[types.NodeID]struct{}
	FinalizedBlocks      int

	                                                  
	CertStateRoot []byte
	CertBlockHash string
	certAnchor    *pb.Block

	StageRetries int                               

	StartedAt   time.Time
	LastUpdated time.Time
	FinishedAt  time.Time

	LastError string
}

                                                                            
                                                                               
                                                                             
func (s *SyncSession) pauseTargetHeight() uint64 {
	if s == nil || s.Light {
		return 0
	}
	switch s.Stage {
	case SyncStageStateSnapshot, SyncStageFastForward:
		if len(s.CertStateRoot) == 32 && s.CertBlockHash != "" {
			return s.CertifiedHeight
		}
	case SyncStageBlockCatchUp:
		if len(s.frontierNonce) == AcceptedFrontierNonceSize && len(s.frontierSupporters) > 0 &&
			s.FrontierTargetID != "" && s.FrontierTargetHeight > s.FrontierAnchorHeight {
			return s.FrontierTargetHeight
		}
	}
	return 0
}

                                                         
var syncSessionIDSeq uint64

                                                   
func nextSyncSessionID() uint64 {
	return atomic.AddUint64(&syncSessionIDSeq, 1)
}

                                                                    
func cloneSyncSession(s *SyncSession) *SyncSession {
	if s == nil {
		return nil
	}
	cp := *s
	                                                                            
	                                                                           
	cp.certAnchor = nil
	cp.frontierNonce = append([]byte(nil), s.frontierNonce...)
	cp.frontierSupporters = nil
	cp.frontierTried = nil
	cp.serviceTried = nil
	return &cp
}

                                                    
                                                                                                  
func (sm *SyncManager) startSyncSession(trigger SyncTrigger, peerAcceptedHeight, localAcceptedHeight uint64, light bool) *SyncSession {
	now := time.Now()
	session := &SyncSession{
		ID:                  nextSyncSessionID(),
		Trigger:             trigger,
		Stage:               SyncStagePlan,
		LocalAcceptedHeight: localAcceptedHeight,
		PeerAcceptedHeight:  peerAcceptedHeight,
		Light:               light,
		StartedAt:           now,
		LastUpdated:         now,
	}
	if peerAcceptedHeight > localAcceptedHeight {
		session.HeightDiff = peerAcceptedHeight - localAcceptedHeight
	}

	sm.Mu.Lock()
	if sm.activeSession != nil {
		activeID := sm.activeSession.ID
		activeStage := sm.activeSession.Stage
		sm.Mu.Unlock()
		sm.Logger.Info("[SyncPipeline] skipped trigger=%s peer=%d local=%d: active session=%d stage=%s",
			trigger, peerAcceptedHeight, localAcceptedHeight, activeID, activeStage)
		return nil
	}
	sm.activeSession = session
	sm.Syncing = false
	sm.Mu.Unlock()

	sm.Logger.Info("[SyncPipeline] session=%d trigger=%s stage=%s local=%d peer=%d diff=%d",
		session.ID, session.Trigger, session.Stage, localAcceptedHeight, peerAcceptedHeight, session.HeightDiff)
	return session
}

                                                                          
func (sm *SyncManager) setSyncSessionStage(session *SyncSession, stage SyncStage, detail string) bool {
	if session == nil {
		return false
	}
	sm.Mu.Lock()
	if sm.activeSession != session {
		sm.Mu.Unlock()
		return false
	}
	session.Stage = stage
	sm.Syncing = session.pauseTargetHeight() > 0
	session.StageRetries = 0
	session.LastUpdated = time.Now()
	sm.Mu.Unlock()

	if detail == "" {
		sm.Logger.Info("[SyncPipeline] session=%d stage=%s", session.ID, stage)
		return true
	}
	sm.Logger.Info("[SyncPipeline] session=%d stage=%s %s", session.ID, stage, detail)
	return true
}

func (sm *SyncManager) isActiveSyncSession(session *SyncSession, stages ...SyncStage) bool {
	if session == nil {
		return false
	}
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()
	if sm.activeSession != session {
		return false
	}
	if len(stages) == 0 {
		return true
	}
	for _, stage := range stages {
		if session.Stage == stage {
			return true
		}
	}
	return false
}

                                                            
func (sm *SyncManager) updateSyncSession(session *SyncSession, mutate func(*SyncSession)) {
	if session == nil || mutate == nil {
		return
	}
	sm.Mu.Lock()
	if sm.activeSession != session {
		sm.Mu.Unlock()
		return
	}
	mutate(session)
	session.LastUpdated = time.Now()
	sm.Mu.Unlock()
}

                                                                                       
func (sm *SyncManager) finishSyncSession(session *SyncSession, err error) {
	if session == nil {
		return
	}
	sm.Mu.Lock()
	if sm.activeSession != session {
		sm.Mu.Unlock()
		return
	}
	now := time.Now()
	session.LastUpdated = now
	session.FinishedAt = now
	if err != nil {
		session.Stage = SyncStageFailed
		session.LastError = err.Error()
	} else if session.Stage != SyncStageFailed {
		session.Stage = SyncStageDone
	}
	                                                            
	                                                                             
	                                                                          
	                                                                           
	                                                                      
	_, acceptedHeight := sm.store.GetLastAccepted()
	verifiedComplete := err == nil && session.Stage == SyncStageDone &&
		session.TargetHeight > session.LocalAcceptedHeight && acceptedHeight >= session.TargetHeight
	var verifiedPoint VerifiedSyncPoint
	if verifiedComplete {
		installed, exists := sm.store.GetFinalizedAtHeight(session.TargetHeight)
		verifiedComplete = exists && installed != nil && installed.ID != ""
		if verifiedComplete {
			verifiedPoint = VerifiedSyncPoint{Height: session.TargetHeight, BlockID: installed.ID}
		}
	}
	observer := sm.verifiedSyncObserver
	sm.lastSession = cloneSyncSession(session)
	sm.activeSession = nil
	if sm.frontierRound != nil && sm.frontierRound.sessionID == session.ID {
		sm.frontierRound = nil
	}
	for requestID, meta := range sm.SyncRequestMeta {
		if meta.FrontierSessionID == session.ID {
			delete(sm.SyncRequests, requestID)
			delete(sm.SyncRequestMeta, requestID)
			delete(sm.InFlightSyncRanges, meta.RangeKey)
		}
	}
	sm.Syncing = false
	sm.Mu.Unlock()
	if verifiedComplete {
		if observer != nil {
			if observeErr := observer(verifiedPoint); observeErr != nil {
				sm.Logger.Error("[SyncPipeline] persist verified target height=%d block=%s: %v", verifiedPoint.Height, verifiedPoint.BlockID, observeErr)
				                                                                   
				                                                                  
				                                                                   
				                                                                 
				                                          
				return
			}
		}
		sm.advanceVerifiedSyncHeight(verifiedPoint.Height)
		sm.verifiedSyncCompletions.Add(1)
	}

	if err != nil {
		sm.Logger.Warn("[SyncPipeline] session=%d stage=%s err=%v", session.ID, session.Stage, err)
		return
	}
	sm.Logger.Info("[SyncPipeline] session=%d stage=%s finished", session.ID, session.Stage)
}

                                                                                        
                                                                                                                 
func (sm *SyncManager) runSyncPipeline(trigger SyncTrigger, peerAcceptedHeight, localAcceptedHeight uint64) {
	sm.runSyncPipelineMode(trigger, peerAcceptedHeight, localAcceptedHeight, false)
}

func (sm *SyncManager) runLightFrontierPipeline(trigger SyncTrigger, peerAcceptedHeight, localAcceptedHeight uint64) {
	sm.runSyncPipelineMode(trigger, peerAcceptedHeight, localAcceptedHeight, true)
}

func (sm *SyncManager) runSyncPipelineMode(trigger SyncTrigger, peerAcceptedHeight, localAcceptedHeight uint64, light bool) {
	if peerAcceptedHeight <= localAcceptedHeight {
		return
	}

	session := sm.startSyncSession(trigger, peerAcceptedHeight, localAcceptedHeight, light)
	if session == nil {
		return
	}

	heightDiff := peerAcceptedHeight - localAcceptedHeight
	targetHeight := sm.frontierTarget(localAcceptedHeight, peerAcceptedHeight, light)
	deepLag := !light && heightDiff > sm.deepLagStateSyncThreshold()

	if !deepLag && atomic.LoadUint32(&sm.consecutiveStateRootFaults) >= 3 {
		sm.Logger.Warn("[SyncPipeline] forcing deep sync due to %d consecutive stateRoot mismatch failures (heightDiff=%d)",
			atomic.LoadUint32(&sm.consecutiveStateRootFaults), heightDiff)
		deepLag = true
		atomic.StoreUint32(&sm.consecutiveStateRootFaults, 0)
	}

	sm.updateSyncSession(session, func(s *SyncSession) {
		s.HeightDiff = heightDiff
		s.TargetHeight = targetHeight
		s.DeepLag = deepLag
	})

	if deepLag {
		                                                  
		sm.setSyncSessionStage(session, SyncStageCertChain,
			fmt.Sprintf("peer=%d local=%d", peerAcceptedHeight, localAcceptedHeight))
	} else {
		                                                                     
		if targetHeight < localAcceptedHeight+1 {
			sm.finishSyncSession(session, fmt.Errorf("invalid sync target: local=%d peer=%d target=%d",
				localAcceptedHeight, peerAcceptedHeight, targetHeight))
			return
		}
		sm.setSyncSessionStage(session, SyncStageFrontier,
			fmt.Sprintf("anchor=%d target=%d light=%t", localAcceptedHeight, targetHeight, light))
	}

	sm.runStateMachine(session)
}

                                                                     
const maxStageRetries = 2

                                                       
                                                                     
                                  
                                                                                                    
                                                                    
                                                                                             
func (sm *SyncManager) runStateMachine(session *SyncSession) {
	for {
		var serr *SyncError
		switch session.Stage {
		case SyncStageCertChain:
			serr = sm.execCertChainStage(session)
		case SyncStageStateSnapshot:
			serr = sm.execSnapshotStage(session)
		case SyncStageFastForward:
			serr = sm.execFastForwardStage(session)
		case SyncStageFrontier:
			serr = sm.execFrontierStage(session)
			if serr == nil {
				if sm.isActiveSyncSession(session, SyncStageFrontier) {
					return                                                        
				}
				continue
			}
		case SyncStageBlockCatchUp:
			serr = sm.execBlockCatchUpStage(session)
			if serr == nil {
				if sm.isActiveSyncSession(session, SyncStageBlockCatchUp) {
					return                                                 
				}
				continue
			}
		case SyncStageDone:
			sm.finishSyncSession(session, nil)
			return
		case SyncStageFailed:
			sm.finishSyncSession(session, fmt.Errorf("%s", session.LastError))
			return
		default:
			sm.finishSyncSession(session, fmt.Errorf("unknown stage: %s", session.Stage))
			return
		}

		if serr == nil {
			continue                                           
		}

		switch serr.Kind {
		case SyncErrDeterministic:
			sm.Logger.Warn("[SyncPipeline] session=%d deterministic failure: %s", session.ID, serr)
			sm.updateSyncSession(session, func(s *SyncSession) { s.LastError = serr.Error() })
			sm.setSyncSessionStage(session, SyncStageFailed, "")
			continue
		case SyncErrNetwork:
			retries := 0
			sm.updateSyncSession(session, func(s *SyncSession) {
				s.StageRetries++
				retries = s.StageRetries
			})
			if retries == 0 {
				sm.finishSyncSession(session, fmt.Errorf("session inactive while handling network error: %s", serr))
				return
			}
			if retries <= maxStageRetries {
				sm.Logger.Info("[SyncPipeline] session=%d network error at %s, retry %d/%d: %s",
					session.ID, serr.Stage, retries, maxStageRetries, serr.Message)
				continue                      
			}
			if session.Stage == SyncStageFrontier ||
				(session.Stage == SyncStageBlockCatchUp && session.FrontierTargetID != "") {
				                                                                
				                                                                     
				sm.finishSyncSession(session, serr)
				return
			}
			sm.Logger.Warn("[SyncPipeline] session=%d network retries exhausted at %s, fallback: %s",
				session.ID, serr.Stage, serr.Message)
			_, localAccepted := sm.store.GetLastAccepted()
			sm.enterFallbackBlockCatchUp(session, localAccepted, true)
			continue
		case SyncErrTransient:
			sm.Logger.Info("[SyncPipeline] session=%d transient error: %s", session.ID, serr)
			sm.finishSyncSession(session, nil)
			return
		default:
			sm.finishSyncSession(session, serr)
			return
		}
	}
}
