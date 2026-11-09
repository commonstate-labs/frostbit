package consensus

import (
	"context"
	"dex/db"
	statedb "dex/stateDB"
	"dex/types"
	"fmt"
)

func (sm *SyncManager) performDistributedStateDBSync(targetHeight uint64, certStateRoot []byte, certBlockHash string) (bool, bool) {
	fetcher, ok := sm.transport.(stateSnapshotFetcher)
	if !ok {
		sm.logDeepSyncAbort("state_snapshot", "transport does not support checkpoint file sync, transport=%T", sm.transport)
		return false, false
	}
	realStore, ok := sm.store.(*RealBlockStore)
	if !ok || realStore == nil || realStore.dbManager == nil {
		sm.logDeepSyncAbort("state_snapshot", "invalid local store for checkpoint file sync (store=%T realStoreNil=%v dbManagerNil=%v)", sm.store, realStore == nil, realStore == nil || realStore.dbManager == nil)
		return false, false
	}

	peers := sm.selectStateSyncPeers(targetHeight, sm.stateSyncPeers())
	if len(peers) == 0 {
		sm.logDeepSyncAbort("state_snapshot", "no peers available for checkpoint file download (targetHeight=%d)", targetHeight)
		sm.Logger.Warn("[SyncPipeline] StateDB checkpoint sync: no peers available")
		return false, false
	}
	acceptedID, acceptedHeight := sm.store.GetLastAccepted()
	sm.Logger.Info(
		"[SyncPipeline][CheckpointSync] target=%d localAccepted=%d(%s) peers=%s certStateRootLen=%d",
		targetHeight, acceptedHeight, acceptedID, formatPeerHeightsForLog(sm, peers), len(certStateRoot),
	)
	if targetHeight <= acceptedHeight {
		sm.logDeepSyncAbort("state_snapshot", "checkpoint target %d is not ahead of local accepted %d; skip checkpoint file install", targetHeight, acceptedHeight)
		return false, false
	}

	shardResp, metaPeer, snapshotHeight, ok := sm.loadSnapshotShardMetadata(fetcher, peers, targetHeight)
	if !ok {
		sm.logDeepSyncAbort("state_snapshot", "loadSnapshotShardMetadata failed (targetHeight=%d peers=%d)", targetHeight, len(peers))
		return false, false
	}
	if err := validateSnapshotManifest(shardResp, snapshotHeight, certStateRoot); err != nil {
		sm.logDeepSyncAbort("state_snapshot_metadata", "manifest verification failed (snapshotHeight=%d metadataPeer=%s err=%v)", snapshotHeight, metaPeer, err)
		sm.Logger.Warn("[SyncPipeline] StateDB checkpoint sync: manifest from %s failed at height %d: %v", metaPeer, snapshotHeight, err)
		return false, false
	}
	installed, switchLocked := sm.installStateCheckpointFiles(fetcher, realStore, orderedStateSyncPeers(peers, metaPeer), snapshotHeight, certStateRoot, certBlockHash)
	if !installed {
		return false, false
	}
	localRoot, localFP := statedb.ReadFingerprintFromProvider(realStore.dbManager)
	_, certFP, matched := statedb.CompareFingerprintBytes(localRoot, certStateRoot)
	if !matched {
		err := fmt.Errorf("installed checkpoint fingerprint mismatch at height=%d local=%x cert=%x", snapshotHeight, localFP, certFP)
		sm.invokeSafetyFaultHandler(err)
		if switchLocked {
			sm.unlockSnapshotSwitch()
		}
		sm.logDeepSyncAbort("state_snapshot_install", "%v", err)
		return false, false
	}
	sm.Logger.Info("[SyncPipeline] StateDB checkpoint sync complete: height=%d fp=%x metadataPeer=%s", snapshotHeight, localFP, metaPeer)
	return true, switchLocked
}

func (sm *SyncManager) installStateCheckpointFiles(
	fetcher stateSnapshotFetcher,
	realStore *RealBlockStore,
	peers []types.NodeID,
	snapshotHeight uint64,
	certStateRoot []byte,
	certBlockHash string,
) (bool, bool) {
	_, acceptedHeight := sm.store.GetLastAccepted()
	if snapshotHeight <= acceptedHeight {
		sm.logDeepSyncAbort("state_snapshot_install", "checkpoint target %d is not ahead of local accepted %d before download; skip install", snapshotHeight, acceptedHeight)
		return false, false
	}

	var lastErr error
	for _, peer := range peers {
		checkpointDir, cleanup, err := fetcher.FetchStateCheckpointFiles(peer, snapshotHeight)
		if err != nil {
			lastErr = err
			sm.Logger.Warn("[SyncPipeline] StateDB checkpoint sync: download checkpoint files from %s failed: %v", peer, err)
			continue
		}
		switchLocked := sm.lockSnapshotSwitch()
		if !switchLocked {
			if cleanup != nil {
				cleanup()
			}
			lastErr = fmt.Errorf("snapshot switch locks unavailable")
			sm.logDeepSyncAbort("state_snapshot_install", "snapshot switch locks unavailable before installing height=%d", snapshotHeight)
			return false, false
		}
		_, acceptedHeight := sm.store.GetLastAccepted()
		if snapshotHeight <= acceptedHeight {
			if cleanup != nil {
				cleanup()
			}
			if switchLocked {
				sm.unlockSnapshotSwitch()
			}
			sm.logDeepSyncAbort("state_snapshot_install", "checkpoint target %d is not ahead of local accepted %d after download; skip install", snapshotHeight, acceptedHeight)
			return false, false
		}
		err = realStore.dbManager.InstallStateCheckpointDir(context.Background(), acceptedHeight, snapshotHeight, checkpointDir, certStateRoot, certBlockHash)
		if cleanup != nil {
			cleanup()
		}
		if err != nil {
			if db.IsStateCheckpointPostInstallError(err) {
				                                                               
				                                                                   
				                                                                     
				sm.Logger.Warn("[SyncPipeline] StateDB checkpoint installed from %s at height=%d with recoverable metadata error: %v", peer, snapshotHeight, err)
				return true, switchLocked
			}
			if switchLocked {
				sm.unlockSnapshotSwitch()
			}
			lastErr = err
			sm.Logger.Warn("[SyncPipeline] StateDB checkpoint sync: install checkpoint files from %s failed: %v", peer, err)
			continue
		}
		sm.Logger.Info("[SyncPipeline] StateDB checkpoint sync: installed checkpoint files from %s at height=%d", peer, snapshotHeight)
		return true, switchLocked
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all peers exhausted")
	}
	sm.logDeepSyncAbort("state_snapshot_install", "checkpoint file sync failed at height=%d peers=%d err=%v", snapshotHeight, len(peers), lastErr)
	return false, false
}

func orderedStateSyncPeers(peers []types.NodeID, first types.NodeID) []types.NodeID {
	out := make([]types.NodeID, 0, len(peers))
	seen := make(map[types.NodeID]struct{}, len(peers))
	if first != "" {
		out = append(out, first)
		seen[first] = struct{}{}
	}
	for _, peer := range peers {
		if _, ok := seen[peer]; ok {
			continue
		}
		out = append(out, peer)
	}
	return out
}

func (sm *SyncManager) loadSnapshotShardMetadata(
	fetcher stateSnapshotFetcher,
	peers []types.NodeID,
	targetHeight uint64,
) (*types.StateSnapshotShardsResponse, types.NodeID, uint64, bool) {
	var (
		shardResp *types.StateSnapshotShardsResponse
		metaPeer  types.NodeID
	)
	for _, peer := range peers {
		resp, err := fetcher.FetchStateSnapshotShards(peer, targetHeight)
		if err != nil {
			sm.Logger.Warn("[SyncPipeline] StateDB checkpoint sync: fetch manifest from %s failed: %v", peer, err)
			continue
		}
		if resp == nil {
			continue
		}
		shardResp = resp
		metaPeer = peer
		break
	}
	if shardResp == nil {
		sm.logDeepSyncAbort("state_snapshot_metadata", "all peers returned no checkpoint manifest (targetHeight=%d peers=%d)", targetHeight, len(peers))
		return nil, "", 0, false
	}
	snapshotHeight := shardResp.SnapshotHeight
	if snapshotHeight == 0 {
		snapshotHeight = targetHeight
	}
	if snapshotHeight != targetHeight {
		sm.logDeepSyncAbort("state_snapshot_metadata", "snapshot height mismatch (snapshot_metadata=%d cert_target=%d metadataPeer=%s)", snapshotHeight, targetHeight, metaPeer)
		return nil, "", 0, false
	}
	return shardResp, metaPeer, snapshotHeight, true
}
