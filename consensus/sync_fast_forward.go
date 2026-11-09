package consensus

import (
	"dex/pb"
	statedb "dex/stateDB"
)

func (sm *SyncManager) fastForwardAfterStateSync(targetHeight uint64, certStateRoot []byte, certBlockHash string, anchorBlock *pb.Block) bool {
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil {
		sm.logDeepSyncAbort("fast_forward", "store does not support fast-forward, store=%T", sm.store)
		return false
	}
	if realStore.dbManager == nil {
		sm.logDeepSyncAbort("fast_forward", "dbManager unavailable")
		return false
	}
	_, currentAccepted := sm.store.GetLastAccepted()
	if targetHeight <= currentAccepted {
		sm.logDeepSyncAbort("fast_forward", "certified checkpoint %d is not ahead of local accepted %d; skip anchor install", targetHeight, currentAccepted)
		return false
	}

	certAnchorHash := certBlockHash
	certAnchorRoot := certStateRoot
	certAnchorFP := statedb.ParseFingerprintFromBytes(certAnchorRoot)
	if certAnchorHash == "" {
		dbHash, dbRoot, dbHasAnchor := sm.loadCertifiedAnchorForHeight(targetHeight)
		if !dbHasAnchor || dbHash == "" {
			sm.logDeepSyncAbort("fast_forward", "missing cert anchor hash at targetHeight=%d", targetHeight)
			sm.Logger.Warn("[SyncPipeline] FastForward: missing cert anchor hash at targetHeight=%d", targetHeight)
			return false
		}
		certAnchorHash = dbHash
		if len(certAnchorRoot) == 0 {
			certAnchorRoot = dbRoot
			certAnchorFP = statedb.ParseFingerprintFromBytes(certAnchorRoot)
		}
	}
	if statedb.IsZeroFingerprintBytes(certAnchorRoot) {
		sm.logDeepSyncAbort("fast_forward", "missing cert anchor fingerprint at targetHeight=%d", targetHeight)
		sm.Logger.Warn("[SyncPipeline] FastForward: missing cert anchor fingerprint at targetHeight=%d", targetHeight)
		return false
	}

	localRoot, localFP := statedb.ReadFingerprintFromProvider(realStore.dbManager)
	if statedb.IsZeroFingerprintBytes(localRoot) {
		sm.logDeepSyncAbort("fast_forward", "local state fingerprint is 0 at targetHeight=%d", targetHeight)
		sm.Logger.Warn("[SyncPipeline] FastForward: local fingerprint is 0 at targetHeight=%d", targetHeight)
		return false
	}
	_, _, matched := statedb.CompareFingerprintBytes(localRoot, certAnchorRoot)
	if !matched {
		sm.logDeepSyncAbort("fast_forward", "local fingerprint mismatch at targetHeight=%d (local=%x cert=%x)", targetHeight, localFP, certAnchorFP)
		sm.Logger.Warn("[SyncPipeline] FastForward: local fingerprint mismatch at targetHeight=%d (local=%x cert=%x)", targetHeight, localFP, certAnchorFP)
		return false
	}
	anchor, err := validateCertifiedAnchorBlock(anchorBlock, targetHeight, certAnchorHash)
	if err != nil {
		sm.logDeepSyncAbort("fast_forward", "certified anchor validation failed at targetHeight=%d hash=%s err=%v", targetHeight, certAnchorHash, err)
		sm.Logger.Warn("[SyncPipeline] FastForward: certified anchor validation failed: %v", err)
		return false
	}
	                                                                            
	                                                                        
	                                                                       
	if err := validateProposerVRF(realStore.dbManager, realStore.windowConfig, anchor, false); err != nil {
		sm.logDeepSyncAbort("fast_forward", "certified anchor VRF validation failed at targetHeight=%d hash=%s err=%v", targetHeight, certAnchorHash, err)
		sm.Logger.Warn("[SyncPipeline] FastForward: certified anchor VRF validation failed: %v", err)
		return false
	}
	if err := realStore.persistPreparedCertifiedAnchor(targetHeight, certAnchorHash, certAnchorRoot, anchorBlock); err != nil {
		sm.logDeepSyncAbort("fast_forward", "persist certified anchor failed at targetHeight=%d hash=%s err=%v", targetHeight, certAnchorHash, err)
		sm.Logger.Warn("[SyncPipeline] FastForward: persist certified anchor failed: %v", err)
		return false
	}

	if err := realStore.InstallCertifiedAnchor(targetHeight, certAnchorHash, anchorBlock); err != nil {
		sm.logDeepSyncAbort("fast_forward", "install certified anchor failed at targetHeight=%d hash=%s err=%v", targetHeight, certAnchorHash, err)
		sm.Logger.Warn("[SyncPipeline] FastForward: install certified anchor failed: %v", err)
		return false
	}
	sm.Logger.Info("[SyncPipeline] FastForward: installed certified anchor at height=%d block=%s fp=%x", targetHeight, certAnchorHash, localFP)
	return true
}
