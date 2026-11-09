package consensus

import (
	"dex/types"
	"time"
)

                                               
                                                         
                                               

func (sm *SyncManager) chitSoftGap() uint64 {
	if sm == nil || sm.config == nil || sm.config.ChitSoftGap == 0 {
		return 1
	}
	return sm.config.ChitSoftGap
}

func (sm *SyncManager) chitHardGap() uint64 {
	if sm == nil || sm.config == nil || sm.config.ChitHardGap == 0 {
		return 3
	}
	soft := sm.chitSoftGap()
	if sm.config.ChitHardGap < soft {
		return soft
	}
	return sm.config.ChitHardGap
}

func (sm *SyncManager) chitGracePeriod() time.Duration {
	if sm == nil || sm.config == nil || sm.config.ChitGracePeriod <= 0 {
		return time.Second
	}
	return sm.config.ChitGracePeriod
}

func (sm *SyncManager) chitCooldown() time.Duration {
	if sm == nil || sm.config == nil || sm.config.ChitCooldown < 0 {
		return 0
	}
	return sm.config.ChitCooldown
}

func (sm *SyncManager) deepLagMinAdditionalPeers() int {
	if sm == nil || sm.config == nil || sm.config.DeepLagMinAdditionalPeers < 0 {
		return 0
	}
	return sm.config.DeepLagMinAdditionalPeers
}

func (sm *SyncManager) resetStaleSyncStateLocked() bool {
	                                                                                                
	                                                                                            
	if sm.activeSession != nil {
		sm.Logger.Debug("[SyncManager] Chit trigger: keep active session %d(stage=%s), skip stale reset", sm.activeSession.ID, sm.activeSession.Stage)
		return true
	}
	if len(sm.SyncRequests) > 0 {
		return true
	}
	if !sm.Syncing {
		return false
	}
	sm.Logger.Debug("[SyncManager] Chit trigger: resetting stale sync state (Requests=0)")
	sm.Syncing = false
	return false
}

func (sm *SyncManager) triggerCooldownRemainingLocked(now time.Time) time.Duration {
	cooldown := sm.chitCooldown()
	if cooldown <= 0 || sm.lastChitTriggerAt.IsZero() {
		return 0
	}
	elapsed := now.Sub(sm.lastChitTriggerAt)
	if elapsed >= cooldown {
		return 0
	}
	return cooldown - elapsed
}

func (sm *SyncManager) markPendingChitLocked(height uint64, from types.NodeID) {
	now := time.Now()
	if !sm.chitPending {
		sm.chitPending = true
		sm.chitPendingHeight = height
		sm.chitPendingFrom = from
		sm.chitPendingFirstSeen = now
		return
	}
	                                                                         
	                                                                         
	                                                                            
	_, localAccepted := sm.store.GetLastAccepted()
	pendingDeep := sm.chitPendingHeight > localAccepted &&
		sm.chitPendingHeight-localAccepted > sm.deepLagStateSyncThreshold()
	newDeep := height > localAccepted && height-localAccepted > sm.deepLagStateSyncThreshold()
	if pendingDeep != newDeep {
		if newDeep {
			return
		}
		sm.chitPendingHeight = height
		sm.chitPendingFrom = from
		sm.chitPendingFirstSeen = now
		return
	}
	if pendingDeep && newDeep {
		                                                                      
		                                                                      
		                                              
		if height < sm.chitPendingHeight {
			sm.chitPendingHeight = height
			sm.chitPendingFrom = from
			sm.chitPendingFirstSeen = now
		}
		return
	}
	if height > sm.chitPendingHeight {
		sm.chitPendingHeight = height
		sm.chitPendingFrom = from
		sm.chitPendingFirstSeen = now
	}
}

func (sm *SyncManager) clearPendingChitLocked() {
	sm.chitPending = false
	sm.chitPendingHeight = 0
	sm.chitPendingFrom = ""
	sm.chitPendingFirstSeen = time.Time{}
}

func (sm *SyncManager) scheduleChitEvaluationLocked(delay time.Duration) {
	if !sm.chitPending {
		return
	}
	if delay <= 0 {
		delay = 50 * time.Millisecond
	}
	if sm.chitTimerArmed {
		return
	}
	sm.chitTimerArmed = true
	go func(wait time.Duration) {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		<-timer.C
		sm.evaluatePendingChitTrigger()
	}(delay)
}

func (sm *SyncManager) evaluatePendingChitTrigger() {
	sm.Mu.Lock()
	sm.chitTimerArmed = false
	if !sm.chitPending {
		sm.Mu.Unlock()
		return
	}

	targetHeight := sm.chitPendingHeight
	from := sm.chitPendingFrom
	firstSeen := sm.chitPendingFirstSeen
	_, localAccepted := sm.store.GetLastAccepted()
	if targetHeight <= localAccepted {
		sm.clearPendingChitLocked()
		sm.Mu.Unlock()
		return
	}

	if sm.resetStaleSyncStateLocked() {
		sm.scheduleChitEvaluationLocked(200 * time.Millisecond)
		sm.Mu.Unlock()
		return
	}

	now := time.Now()
	if remain := sm.triggerCooldownRemainingLocked(now); remain > 0 {
		sm.scheduleChitEvaluationLocked(remain)
		sm.Mu.Unlock()
		return
	}

	grace := sm.chitGracePeriod()
	if elapsed := now.Sub(firstSeen); elapsed < grace {
		sm.scheduleChitEvaluationLocked(grace - elapsed)
		sm.Mu.Unlock()
		return
	}

	heightDiff := targetHeight - localAccepted
	if heightDiff > sm.deepLagStateSyncThreshold() {
		confirmedHeight, confirmed := sm.confirmedDeepHeightLocked(localAccepted)
		if !confirmed {
			recheck := grace / 2
			if recheck <= 0 {
				recheck = 200 * time.Millisecond
			}
			sm.scheduleChitEvaluationLocked(recheck)
			sm.Mu.Unlock()
			return
		}
		targetHeight = confirmedHeight
		heightDiff = targetHeight - localAccepted
	}

	sm.clearPendingChitLocked()
	sm.lastChitTriggerAt = now
	sm.Mu.Unlock()

	if heightDiff == 1 {
		sm.Logger.Debug("[SyncManager] TriggerSyncFromChit: delayed light frontier catch-up peer=%s peerHeight=%d localAccepted=%d",
			from, targetHeight, localAccepted)
		sm.runLightFrontierPipeline(SyncTriggerChit, targetHeight, localAccepted)
		return
	}
	if heightDiff >= sm.chitHardGap() && heightDiff > sm.deepLagStateSyncThreshold() {
		sm.Logger.Info("[SyncManager] TriggerSyncFromChit: delayed deep lag peer=%s peerHeight=%d localAccepted=%d diff=%d threshold=%d, use stateDB-first catch-up",
			from, targetHeight, localAccepted, heightDiff, sm.deepLagStateSyncThreshold())
		sm.runSyncPipeline(SyncTriggerChit, targetHeight, localAccepted)
		return
	}

	sm.Logger.Debug("[SyncManager] TriggerSyncFromChit: delayed trigger peer=%s peerHeight=%d localAccepted=%d diff=%d",
		from, targetHeight, localAccepted, heightDiff)
	sm.performTriggeredSync(targetHeight, localAccepted, heightDiff)
}

                                                                                                
func (sm *SyncManager) TriggerSyncFromChit(peerAcceptedHeight uint64, from types.NodeID) {
	sm.Mu.Lock()

	if peerAcceptedHeight > sm.PeerHeights[from] {
		sm.PeerHeights[from] = peerAcceptedHeight
	}

	_, localAccepted := sm.store.GetLastAccepted()
	if peerAcceptedHeight <= localAccepted {
		sm.Mu.Unlock()
		return
	}

	heightDiff := peerAcceptedHeight - localAccepted
	now := time.Now()

	if heightDiff == 1 {
		if sm.Syncing || sm.activeSession != nil {
			sm.Mu.Unlock()
			return
		}
		sm.markPendingChitLocked(peerAcceptedHeight, from)
		grace := sm.chitGracePeriod()
		if elapsed := now.Sub(sm.chitPendingFirstSeen); elapsed >= grace {
			sm.scheduleChitEvaluationLocked(10 * time.Millisecond)
		} else {
			sm.scheduleChitEvaluationLocked(grace - elapsed)
		}
		sm.Mu.Unlock()
		return
	}

	                                                                                         
	if heightDiff >= sm.chitHardGap() {
		if heightDiff > sm.deepLagStateSyncThreshold() {
			confirmedHeight, confirmed := sm.confirmedDeepHeightLocked(localAccepted)
			if !confirmed {
				sm.markPendingChitLocked(peerAcceptedHeight, from)
				sm.scheduleChitEvaluationLocked(200 * time.Millisecond)
				sm.Mu.Unlock()
				return
			}
			peerAcceptedHeight = confirmedHeight
			heightDiff = peerAcceptedHeight - localAccepted
		}
		if sm.resetStaleSyncStateLocked() {
			sm.markPendingChitLocked(peerAcceptedHeight, from)
			sm.scheduleChitEvaluationLocked(200 * time.Millisecond)
			sm.Mu.Unlock()
			return
		}
		if remain := sm.triggerCooldownRemainingLocked(now); remain > 0 {
			sm.markPendingChitLocked(peerAcceptedHeight, from)
			sm.scheduleChitEvaluationLocked(remain)
			sm.Mu.Unlock()
			return
		}

		sm.clearPendingChitLocked()
		sm.lastChitTriggerAt = now
		sm.Mu.Unlock()

		if heightDiff > sm.deepLagStateSyncThreshold() {
			sm.Logger.Info("[SyncManager] TriggerSyncFromChit: deep lag detected peer=%s peerHeight=%d localAccepted=%d diff=%d threshold=%d, use stateDB-first catch-up",
				from, peerAcceptedHeight, localAccepted, heightDiff, sm.deepLagStateSyncThreshold())
			sm.runSyncPipeline(SyncTriggerChit, peerAcceptedHeight, localAccepted)
			return
		}

		sm.Logger.Debug("[SyncManager] TriggerSyncFromChit: hard trigger peer=%s peerHeight=%d localAccepted=%d diff=%d",
			from, peerAcceptedHeight, localAccepted, heightDiff)
		sm.performTriggeredSync(peerAcceptedHeight, localAccepted, heightDiff)
		return
	}

	                                                                         
	                                                                         
	if heightDiff < sm.chitSoftGap() {
		sm.Mu.Unlock()
		return
	}

	sm.markPendingChitLocked(peerAcceptedHeight, from)
	grace := sm.chitGracePeriod()
	if elapsed := now.Sub(sm.chitPendingFirstSeen); elapsed >= grace {
		sm.scheduleChitEvaluationLocked(10 * time.Millisecond)
	} else {
		sm.scheduleChitEvaluationLocked(grace - elapsed)
	}
	sm.Mu.Unlock()
}

                                                      
func (sm *SyncManager) performTriggeredSync(peerAcceptedHeight, localAccepted, heightDiff uint64) {
	                           
	_ = heightDiff
	sm.runSyncPipeline(SyncTriggerChit, peerAcceptedHeight, localAccepted)
}
