package consensus

import (
	"bytes"
	"dex/certchain"
	"dex/db"
	"dex/liveness"
	"dex/pb"
	statedb "dex/stateDB"
	"dex/types"
	"dex/vm"
	"fmt"
	"strings"
)

                                                                               
                                                                               
                                                                                 
func (sm *SyncManager) lockSnapshotSwitch() bool {
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil || realStore.GetExecutor() == nil {
		return false
	}
	realStore.finalizeMu.Lock()
	realStore.GetExecutor().LockForSnapshotSwitch()
	return true
}

func (sm *SyncManager) unlockSnapshotSwitch() {
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil || realStore.GetExecutor() == nil {
		return
	}
	realStore.GetExecutor().UnlockForSnapshotSwitch()
	realStore.finalizeMu.Unlock()
}

                                                                              
                                                                  
func (sm *SyncManager) reloadStatefulComponentsAfterSnapshot(snapshotHeight uint64) error {
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil {
		return nil
	}
	exec := realStore.GetExecutor()
	if exec == nil {
		return nil
	}
	exec.ResetHotStateCache()
	sm.Logger.Info("[SyncPipeline] HotStateCache reset after snapshot sync")
	                                                              
	if lt, ok := exec.LivenessTracker.(*liveness.Tracker); ok && lt != nil {
		lt.ReloadFromDB()
		sm.Logger.Info("[SyncPipeline] LivenessTracker reloaded after snapshot sync")
	}
	if exec.CertChainTracker != nil {
		exec.CertChainTracker.ReloadFromDB()
		sm.Logger.Info("[SyncPipeline] CertChainTracker reloaded after snapshot sync")
	}
	if dbMgr, ok := exec.DB.(*db.Manager); ok && dbMgr != nil {
		                                              
		dbMgr.InvalidateMinerCache()
		sm.Logger.Info("[SyncPipeline] MinerCache invalidated after snapshot sync")
		                                                                         
		                                                                                  
		lt, ok := exec.LivenessTracker.(*liveness.Tracker)
		if !ok || lt == nil {
			return fmt.Errorf("reconcile miner index after snapshot: unsupported liveness tracker %T", exec.LivenessTracker)
		}
		count, err := dbMgr.ReconcileMinerIndexFromLiveness(lt)
		if err != nil {
			return fmt.Errorf("reconcile miner index after snapshot: %w", err)
		}
		sm.Logger.Info("[SyncPipeline] Miner index reconciled after snapshot sync (miners=%d)", count)
	}
	                                                                                            
	if err := exec.LoadWitnessState(); err != nil {
		sm.Logger.Warn("[SyncPipeline] LoadWitnessState failed after snapshot sync: %v", err)
	} else {
		sm.Logger.Info("[SyncPipeline] WitnessService state reloaded after snapshot sync")
		                                                                                                  
		if svc := exec.WitnessService; svc != nil && snapshotHeight > 0 {
			svc.SetCurrentHeight(snapshotHeight)
			sm.Logger.Info("[SyncPipeline] WitnessService advanced to snapshot height %d after snapshot sync", snapshotHeight)
		}
	}
	return nil
}

func (sm *SyncManager) reloadStatefulTrackersAfterSnapshot(snapshotHeight uint64) error {
	if err := sm.reloadStatefulComponentsAfterSnapshot(snapshotHeight); err != nil {
		return err
	}
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil {
		return fmt.Errorf("snapshot Active-set verification requires RealBlockStore")
	}
	exec := realStore.GetExecutor()
	if exec == nil || exec.CertChainTracker == nil {
		return fmt.Errorf("snapshot Active-set verification requires VM and cert tracker")
	}
	tracker, ok := exec.CertChainTracker.(*certchain.Tracker)
	if !ok || tracker == nil {
		return fmt.Errorf("snapshot requires a certificate chain tracker")
	}
	if err := restoreCertTrackerAfterSnapshot(tracker, snapshotHeight); err != nil {
		return fmt.Errorf("restore checkpoint tracker: %w", err)
	}
	lt, ok := exec.LivenessTracker.(*liveness.Tracker)
	if !ok || lt == nil {
		return fmt.Errorf("snapshot Active-set verification requires liveness tracker")
	}
	if voterHeight, _ := lt.GetConsensusVoterSnapshot(); voterHeight != snapshotHeight {
		return fmt.Errorf("snapshot consensus voter height %d != checkpoint height %d", voterHeight, snapshotHeight)
	}
	dbMgr, ok := exec.DB.(*db.Manager)
	if !ok || dbMgr == nil {
		return fmt.Errorf("snapshot Active-set verification requires db manager")
	}
	raw, found := exec.CertChainTracker.GetCertificate(snapshotHeight)
	if !found || len(raw) == 0 {
		return fmt.Errorf("verified checkpoint certificate %d unavailable after snapshot", snapshotHeight)
	}
	cert, err := certchain.ParseCertificate(raw)
	if err != nil {
		return fmt.Errorf("parse checkpoint certificate %d after snapshot: %w", snapshotHeight, err)
	}
	if cert.Height != snapshotHeight {
		return fmt.Errorf("checkpoint certificate height %d != snapshot height %d", cert.Height, snapshotHeight)
	}
	active, err := vm.BuildCertificateActiveMiners(lt, vm.NewStateView(dbMgr.Get, dbMgr.Scan))
	if err != nil {
		return fmt.Errorf("derive snapshot Active set at height %d: %w", snapshotHeight, err)
	}
	root, err := certchain.ValidatorSetRootFromInterfaces(pb.GetDefaultChainID(), active)
	if err != nil {
		return fmt.Errorf("commit snapshot Active set at height %d: %w", snapshotHeight, err)
	}
	if len(cert.NextValidatorSetRoot) != len(root) || !bytes.Equal(cert.NextValidatorSetRoot, root) {
		return fmt.Errorf("snapshot Active-set root mismatch at height %d", snapshotHeight)
	}
	                                                                         
	                                                                          
	if err := dbMgr.ForceFlushHot(); err != nil {
		return fmt.Errorf("persist restored checkpoint tracker: %w", err)
	}
	sm.Logger.Info("[SyncPipeline] Snapshot Active set verified at height %d (miners=%d root=%x)", snapshotHeight, len(active), root[:8])
	return nil
}

func shortNodeID(id types.NodeID) string {
	s := string(id)
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func formatPeerHeightsForLog(sm *SyncManager, peers []types.NodeID) string {
	if len(peers) == 0 {
		return "-"
	}
	sm.Mu.RLock()
	defer sm.Mu.RUnlock()
	parts := make([]string, 0, len(peers))
	for _, peer := range peers {
		parts = append(parts, fmt.Sprintf("%s:%d", shortNodeID(peer), sm.PeerHeights[peer]))
	}
	return strings.Join(parts, ",")
}

func summarizePBBlockForLog(b *pb.Block) string {
	if b == nil || b.Header == nil {
		return "nil"
	}
	stateFP := statedb.ParseFingerprintFromBytes(b.Header.StateRoot)
	return fmt.Sprintf("h=%d hash=%s parent=%s stateFP=%016x", b.Header.Height, b.BlockHash, b.Header.PrevBlockHash, stateFP)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (sm *SyncManager) logCertRootPeerAnchors(fetcher stateSnapshotFetcher, peers []types.NodeID, snapshotHeight uint64, certStateRoot []byte, tag string) {
	if sm == nil || sm.Logger == nil || fetcher == nil || snapshotHeight == 0 || len(peers) == 0 {
		return
	}
	certFP := statedb.ParseFingerprintFromBytes(certStateRoot)
	verifyHeight := snapshotHeight + 1
	if certFP == 0 {
		sm.Logger.Warn(
			"[SyncPipeline][AnchorProbe] tag=%s snapshotHeight=%d verifyHeight=%d certFP=0 certLen=%d peers=%d",
			tag, snapshotHeight, verifyHeight, len(certStateRoot), len(peers),
		)
		return
	}

	sampled := minInt(len(peers), 4)
	matchAtH := 0
	matchAtH1 := 0
	for i := 0; i < sampled; i++ {
		peer := peers[i]
		rootAtH, errAtH := fetcher.FetchBlockStateRoot(peer, snapshotHeight)
		rootAtH1, errAtH1 := fetcher.FetchBlockStateRoot(peer, verifyHeight)
		fpAtH := statedb.ParseFingerprintFromBytes(rootAtH)
		fpAtH1 := statedb.ParseFingerprintFromBytes(rootAtH1)
		eqH := errAtH == nil && fpAtH != 0 && fpAtH == certFP
		eqH1 := errAtH1 == nil && fpAtH1 != 0 && fpAtH1 == certFP
		if eqH {
			matchAtH++
		}
		if eqH1 {
			matchAtH1++
		}
		sm.Logger.Info(
			"[SyncPipeline][AnchorProbe] tag=%s peer=%s cert@%d=%016x block[%d]=%016x errH=%v block[%d]=%016x errH1=%v eqH=%v eqH1=%v",
			tag, peer, snapshotHeight, certFP, snapshotHeight, fpAtH, errAtH, verifyHeight, fpAtH1, errAtH1, eqH, eqH1,
		)
	}
	sm.Logger.Info(
		"[SyncPipeline][AnchorProbe] tag=%s summary cert@%d=%016x sampled=%d/%d matchH=%d matchH1=%d",
		tag, snapshotHeight, certFP, sampled, len(peers), matchAtH, matchAtH1,
	)
	if matchAtH > 0 && matchAtH1 == 0 {
		sm.Logger.Warn(
			"[SyncPipeline][AnchorProbe] tag=%s cert root matches block[%d] but not block[%d] (possible H/H+1 semantic drift)",
			tag, snapshotHeight, verifyHeight,
		)
	}
}

                                               
                                                                       
                                               

                                                                    
                                                                                                
                                        
func (sm *SyncManager) deepLagStateSyncThreshold() uint64 {
	if sm == nil || sm.config == nil || sm.config.DeepLagStateSyncThreshold == 0 {
		return 100
	}
	return sm.config.DeepLagStateSyncThreshold
}

                                                                           
                                      
func (sm *SyncManager) stateSyncPeers() int {
	if sm == nil || sm.config == nil || sm.config.StateSyncPeers <= 0 {
		return 4
	}
	return sm.config.StateSyncPeers
}

                                                           
                                      
func (sm *SyncManager) stateSyncShardConcurrency() int {
	if sm == nil || sm.config == nil || sm.config.StateSyncShardConcurrency <= 0 {
		return 8
	}
	return sm.config.StateSyncShardConcurrency
}

                                                            
                                         
func (sm *SyncManager) stateSyncPageSize() int {
	if sm == nil || sm.config == nil || sm.config.StateSyncPageSize <= 0 {
		return 1000
	}
	return sm.config.StateSyncPageSize
}

                                                                                                
func (sm *SyncManager) logDeepSyncAbort(stage, format string, args ...interface{}) {
	if sm == nil || sm.Logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	sm.Logger.Warn("[SyncPipeline][DeepSyncAbort] stage=%s %s", stage, msg)
}

                                                                                      
func (sm *SyncManager) logDeepSyncTrace(stage, format string, args ...interface{}) {
	if sm == nil || sm.Logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	sm.Logger.Debug("[SyncPipeline][DeepSyncTrace] stage=%s %s", stage, msg)
}

                                               
                                                                                                                 
                                               

                                                                                              
                                                                                                            
func (sm *SyncManager) execCertChainStage(session *SyncSession) *SyncError {
	sm.initDeepLagSession(session, session.PeerAcceptedHeight, session.LocalAcceptedHeight)

	syncTarget, certStateRoot, certBlockHash, certErr := sm.downloadAndVerifyCertChain(session.PeerAcceptedHeight, session.LocalAcceptedHeight)
	if certErr != nil {
		return &SyncError{Stage: SyncStageCertChain, Kind: SyncErrDeterministic, Message: certErr.Error()}
	}

	certStateRootHex := ""
	if len(certStateRoot) > 0 {
		certStateRootHex = fmt.Sprintf("%016x", certStateRoot)
	}
	sm.Logger.Info("[SyncPipeline] session=%d cert_chain: syncTarget=%d certStateRoot=%s certBlockHash=%s",
		session.ID, syncTarget, certStateRootHex, certBlockHash)

	if syncTarget == 0 {
		sm.logDeepSyncAbort("cert_chain", "downloadAndVerifyCertChain failed (peer=%d local=%d)", session.PeerAcceptedHeight, session.LocalAcceptedHeight)
		return &SyncError{Stage: SyncStageCertChain, Kind: SyncErrNetwork,
			Message: fmt.Sprintf("cert chain unavailable (peer=%d local=%d)", session.PeerAcceptedHeight, session.LocalAcceptedHeight)}
	}

	_, currentAccepted := sm.store.GetLastAccepted()
	if syncTarget <= currentAccepted {
		sm.fallbackFromStaleCheckpoint(session, "cert_chain", syncTarget, currentAccepted)
		return nil
	}
	certAnchor, err := sm.fetchCertifiedAnchorBlock(syncTarget, certBlockHash)
	if err != nil {
		sm.logDeepSyncAbort("cert_anchor", "fetch certified anchor failed at height=%d block=%s err=%v", syncTarget, certBlockHash, err)
		return &SyncError{Stage: SyncStageCertChain, Kind: SyncErrNetwork,
			Message: fmt.Sprintf("certified anchor unavailable at height %d: %v", syncTarget, err)}
	}
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil {
		return &SyncError{Stage: SyncStageCertChain, Kind: SyncErrDeterministic,
			Message: fmt.Sprintf("certified anchor recovery requires RealBlockStore, got %T", sm.store)}
	}
	                                                                       
	                                                                             
	                                                                             
	                                                
	if err := realStore.prepareCertifiedAnchorRecovery(currentAccepted, syncTarget, certBlockHash, certStateRoot, certAnchor); err != nil {
		return &SyncError{Stage: SyncStageCertChain, Kind: SyncErrTransient,
			Message: fmt.Sprintf("persist certified anchor recovery record at height %d: %v", syncTarget, err)}
	}

	sm.updateSyncSession(session, func(s *SyncSession) {
		s.CertifiedHeight = syncTarget
		s.CertStateRoot = certStateRoot
		s.CertBlockHash = certBlockHash
		s.certAnchor = certAnchor
		if s.TargetHeight < syncTarget {
			s.TargetHeight = syncTarget
		}
	})
	sm.setSyncSessionStage(session, SyncStageStateSnapshot, fmt.Sprintf("certified=%d", syncTarget))
	return nil
}

                                                                              
                                                                                                     
func (sm *SyncManager) execSnapshotStage(session *SyncSession) *SyncError {
	_, currentAccepted := sm.store.GetLastAccepted()
	if session.CertifiedHeight <= currentAccepted {
		sm.fallbackFromStaleCheckpoint(session, "state_snapshot", session.CertifiedHeight, currentAccepted)
		return nil
	}

	snapshotSynced, switchLocked := sm.performDistributedStateDBSync(session.CertifiedHeight, session.CertStateRoot, session.CertBlockHash)
	if !snapshotSynced {
		_, currentAccepted := sm.store.GetLastAccepted()
		if session.CertifiedHeight <= currentAccepted {
			sm.fallbackFromStaleCheckpoint(session, "state_snapshot", session.CertifiedHeight, currentAccepted)
			return nil
		}
		return &SyncError{Stage: SyncStageStateSnapshot, Kind: SyncErrNetwork,
			Message: fmt.Sprintf("snapshot sync failed at height %d", session.CertifiedHeight)}
	}

	sm.Logger.Info("[SyncPipeline] session=%d snapshot synced at certified height %d", session.ID, session.CertifiedHeight)

	if !switchLocked {
		err := fmt.Errorf("StateDB checkpoint %d switched without snapshot locks", session.CertifiedHeight)
		sm.invokeSafetyFaultHandler(err)
		return &SyncError{Stage: SyncStageStateSnapshot, Kind: SyncErrDeterministic, Message: err.Error()}
	}
	                                                                            
	                                                                             
	                                                                          
	if !sm.fastForwardAfterStateSync(session.CertifiedHeight, session.CertStateRoot, session.CertBlockHash, session.certAnchor) {
		err := fmt.Errorf("complete certified snapshot anchor failed at height %d", session.CertifiedHeight)
		sm.invokeSafetyFaultHandler(err)
		sm.unlockSnapshotSwitch()
		return &SyncError{Stage: SyncStageStateSnapshot, Kind: SyncErrDeterministic, Message: err.Error()}
	}
	sm.updateSyncSession(session, func(s *SyncSession) { s.FastForwarded = true })
	if err := sm.reloadStatefulTrackersAfterSnapshot(session.CertifiedHeight); err != nil {
		sm.invokeSafetyFaultHandler(err)
		sm.unlockSnapshotSwitch()
		return &SyncError{Stage: SyncStageStateSnapshot, Kind: SyncErrDeterministic, Message: err.Error()}
	}
	if !sm.isActiveSyncSession(session, SyncStageStateSnapshot) {
		sm.unlockSnapshotSwitch()
		sm.Logger.Warn("[SyncPipeline] session=%d expired after consistent snapshot reload at height %d", session.ID, session.CertifiedHeight)
		return &SyncError{Stage: SyncStageStateSnapshot, Kind: SyncErrTransient,
			Message: fmt.Sprintf("session expired after consistent snapshot reload at height %d", session.CertifiedHeight)}
	}
	nextStageSet := false
	if session.PeerAcceptedHeight <= session.CertifiedHeight {
		nextStageSet = sm.setSyncSessionStage(session, SyncStageDone, "")
	} else {
		toHeight := sm.frontierTarget(session.CertifiedHeight, session.PeerAcceptedHeight, false)
		sm.updateSyncSession(session, func(s *SyncSession) { s.TargetHeight = toHeight })
		nextStageSet = sm.setSyncSessionStage(session, SyncStageFrontier,
			fmt.Sprintf("anchor=%d target=%d after_checkpoint=true", session.CertifiedHeight, toHeight))
	}
	sm.unlockSnapshotSwitch()
	if !nextStageSet {
		sm.Logger.Warn("[SyncPipeline] session=%d expired after consistent snapshot switch at height %d", session.ID, session.CertifiedHeight)
		return &SyncError{Stage: SyncStageStateSnapshot, Kind: SyncErrTransient,
			Message: fmt.Sprintf("session expired after consistent snapshot switch at height %d", session.CertifiedHeight)}
	}
	return nil
}

                                                                                
                                                                                                                        
                                                                                            
func (sm *SyncManager) execFastForwardStage(session *SyncSession) *SyncError {
	defer sm.unlockSnapshotSwitch()

	_, currentAccepted := sm.store.GetLastAccepted()
	if session.CertifiedHeight <= currentAccepted {
		sm.fallbackFromStaleCheckpoint(session, "fast_forward", session.CertifiedHeight, currentAccepted)
		return nil
	}

	if !sm.fastForwardAfterStateSync(session.CertifiedHeight, session.CertStateRoot, session.CertBlockHash, session.certAnchor) {
		                                                                            
		sm.invokeSafetyFaultHandler(fmt.Errorf("fast-forward failed after StateDB switch at height %d", session.CertifiedHeight))
		return &SyncError{Stage: SyncStageFastForward, Kind: SyncErrDeterministic,
			Message: fmt.Sprintf("fast-forward failed at height %d", session.CertifiedHeight)}
	}

	sm.updateSyncSession(session, func(s *SyncSession) { s.FastForwarded = true })

	if session.PeerAcceptedHeight <= session.CertifiedHeight {
		sm.Logger.Info("[SyncPipeline] session=%d fast-forward to height %d, done", session.ID, session.CertifiedHeight)
		sm.setSyncSessionStage(session, SyncStageDone, "")
		return nil
	}

	toHeight := sm.frontierTarget(session.CertifiedHeight, session.PeerAcceptedHeight, false)
	sm.updateSyncSession(session, func(s *SyncSession) { s.TargetHeight = toHeight })
	sm.setSyncSessionStage(session, SyncStageFrontier,
		fmt.Sprintf("anchor=%d target=%d after_checkpoint=true", session.CertifiedHeight, toHeight))
	return nil
}

                                                                                                     
                                                                                          
func (sm *SyncManager) execBlockCatchUpStage(session *SyncSession) *SyncError {
	localID, localAccepted := sm.store.GetLastAccepted()
	sm.Mu.RLock()
	if sm.activeSession != session || session.Stage != SyncStageBlockCatchUp {
		sm.Mu.RUnlock()
		return &SyncError{Stage: SyncStageBlockCatchUp, Kind: SyncErrTransient,
			Message: "frontier block catch-up session is no longer active"}
	}
	anchorHeight := session.FrontierAnchorHeight
	targetHeight := session.FrontierTargetHeight
	targetID := session.FrontierTargetID
	sm.Mu.RUnlock()
	if targetHeight <= anchorHeight || targetID == "" {
		return &SyncError{Stage: SyncStageBlockCatchUp, Kind: SyncErrDeterministic,
			Message: fmt.Sprintf("invalid accepted-frontier target anchor=%d target=%d id=%q", anchorHeight, targetHeight, targetID)}
	}

	                                                                           
	                                                                           
	                                                                         
	                                                                       
	if localAccepted >= targetHeight {
		finalized, ok := sm.store.GetFinalizedAtHeight(targetHeight)
		if !ok || finalized == nil || finalized.ID != targetID {
			err := fmt.Errorf("local finalized target conflicts with accepted frontier at height %d: local=%v sampled=%s",
				targetHeight, finalized, targetID)
			sm.invokeSafetyFaultHandler(err)
			return &SyncError{Stage: SyncStageBlockCatchUp, Kind: SyncErrDeterministic, Message: err.Error()}
		}
		sm.setSyncSessionStage(session, SyncStageDone,
			fmt.Sprintf("target already finalized locally at %d/%s", targetHeight, targetID))
		return nil
	}

	                                                                        
	                                                                        
	                                                       
	if localAccepted > anchorHeight {
		sm.updateSyncSession(session, func(s *SyncSession) {
			s.TargetHeight = targetHeight
			s.FrontierAnchorHeight = localAccepted
			s.FrontierAnchorID = localID
			s.FrontierTargetHeight = 0
			s.FrontierTargetID = ""
			s.frontierNonce = nil
			s.frontierSupporters = nil
			s.frontierTried = nil
			s.serviceTried = nil
		})
		sm.setSyncSessionStage(session, SyncStageFrontier,
			fmt.Sprintf("local tip advanced; resample anchor=%d/%s target_hint=%d", localAccepted, localID, targetHeight))
		return nil
	}
	if session.Light && sm.hasLocalSnowmanCandidate(localID, targetHeight) {
		sm.setSyncSessionStage(session, SyncStageDone,
			fmt.Sprintf("ordinary Snowman owns local candidate at height %d", targetHeight))
		return nil
	}

	fromHeight := localAccepted + 1
	toHeight := targetHeight
	if toHeight < fromHeight {
		return &SyncError{Stage: SyncStageBlockCatchUp, Kind: SyncErrDeterministic,
			Message: fmt.Sprintf("frontier target is behind local tip (target=%d local=%d)", toHeight, localAccepted)}
	}
	                                                                              
	                                                                               
	                                                                      
	for {
		peer, ok := sm.nextServiceProvider(session, fromHeight, toHeight)
		if !ok {
			break
		}
		if sm.requestSyncFromService(fromHeight, toHeight, peer) {
			return nil
		}
		sm.Logger.Debug("[SyncFrontier] service provider %s could not dispatch range=%d-%d; trying next provider",
			peer, fromHeight, toHeight)
	}

	                                                                           
	                                                            
	for {
		peer, ok := sm.nextFrontierSupporter(session)
		if !ok {
			return &SyncError{Stage: SyncStageBlockCatchUp, Kind: SyncErrNetwork,
				Message: fmt.Sprintf("no untried frontier supporter for range=%d-%d", fromHeight, toHeight)}
		}
		if sm.requestSyncFrom(fromHeight, toHeight, peer) {
			return nil
		}
		sm.markFrontierSupporterTried(session, peer)
		sm.Logger.Debug("[SyncFrontier] supporter %s could not dispatch range=%d-%d; trying next signer",
			peer, fromHeight, toHeight)
	}
}

                                                                                  
                                              
func (sm *SyncManager) enterFallbackBlockCatchUp(session *SyncSession, localAccepted uint64, fromCertFallback bool) {
	fromHeight, toHeight, ok := sm.deepLagCatchUpRange(localAccepted, session.PeerAcceptedHeight)
	if !ok {
		sm.logDeepSyncAbort("block_catch_up", "invalid catch-up range (peerAccepted=%d localAccepted=%d)", session.PeerAcceptedHeight, localAccepted)
		sm.updateSyncSession(session, func(s *SyncSession) { s.LastError = "invalid fallback catch-up range" })
		sm.setSyncSessionStage(session, SyncStageFailed, "")
		return
	}
	sm.updateSyncSession(session, func(s *SyncSession) { s.TargetHeight = toHeight })
	detail := fmt.Sprintf("range=%d-%d", fromHeight, toHeight)
	if fromCertFallback {
		detail = fmt.Sprintf("fallback %s", detail)
	}
	sm.setSyncSessionStage(session, SyncStageFrontier, detail)
}

func (sm *SyncManager) fallbackFromStaleCheckpoint(session *SyncSession, stage string, certifiedHeight, currentAccepted uint64) {
	sm.logDeepSyncAbort(stage, "certified checkpoint %d is not ahead of local accepted %d; skip snapshot install", certifiedHeight, currentAccepted)
	if session.PeerAcceptedHeight <= currentAccepted {
		sm.updateSyncSession(session, func(s *SyncSession) { s.TargetHeight = currentAccepted })
		sm.setSyncSessionStage(session, SyncStageDone, "")
		return
	}
	sm.enterFallbackBlockCatchUp(session, currentAccepted, true)
}

                                                                                                      
func (sm *SyncManager) initDeepLagSession(session *SyncSession, peerAcceptedHeight, localAccepted uint64) {
	sm.updateSyncSession(session, func(s *SyncSession) {
		s.LocalAcceptedHeight = localAccepted
		s.PeerAcceptedHeight = peerAcceptedHeight
		s.HeightDiff = peerAcceptedHeight - localAccepted
		s.DeepLag = true
	})
}

                                                                          
                                                                                               
func (sm *SyncManager) deepLagCatchUpRange(localAccepted, peerAcceptedHeight uint64) (uint64, uint64, bool) {
	if peerAcceptedHeight <= localAccepted {
		return 0, 0, false
	}
	fromHeight := localAccepted + 1
	toHeight := peerAcceptedHeight
	threshold := sm.deepLagStateSyncThreshold()
	if peerAcceptedHeight-localAccepted > threshold {
		                                                                     
		                                     
		toHeight = localAccepted + threshold
	}
	if toHeight < fromHeight {
		return 0, 0, false
	}
	return fromHeight, toHeight, true
}

                                                                                
                  
                                                         
