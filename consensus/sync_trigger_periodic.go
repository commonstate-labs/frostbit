package consensus

                                                                              
                                                                          
                                                                               
                                                                  
func (sm *SyncManager) tickCheckAndSync() {
	if sm == nil || sm.store == nil || sm.config == nil {
		return
	}
	_, localAccepted := sm.store.GetLastAccepted()
	sm.Mu.RLock()
	if sm.Syncing || sm.activeSession != nil {
		sm.Mu.RUnlock()
		return
	}
	peerHeight, hasHint := sm.confirmedDeepHeightLocked(localAccepted)
	if !hasHint {
		peerHeight, hasHint = sm.highestBoundedHintLocked(localAccepted)
	}
	sm.Mu.RUnlock()
	if hasHint && peerHeight > localAccepted && peerHeight-localAccepted > sm.config.BehindThreshold {
		sm.Logger.Debug("[Sync] Detected lag of %d blocks, starting accepted-frontier verification",
			peerHeight-localAccepted)
		sm.runSyncPipeline(SyncTriggerPeriodic, peerHeight, localAccepted)
	}
}
