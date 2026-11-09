package db

import (
	"context"
	statedb "dex/stateDB"
	"errors"
	"fmt"
	"io"
	"sort"
)

                                                                               
                                                                             
                                                                              
                         
type StateCheckpointPostInstallError struct {
	Height uint64
	Err    error
}

func (e *StateCheckpointPostInstallError) Error() string {
	return fmt.Sprintf("checkpoint %d installed but local metadata completion failed: %v", e.Height, e.Err)
}

func (e *StateCheckpointPostInstallError) Unwrap() error { return e.Err }

func IsStateCheckpointPostInstallError(err error) bool {
	var committed *StateCheckpointPostInstallError
	return errors.As(err, &committed)
}

                                                                       
func (manager *Manager) EnsureStateCheckpoint(height uint64) error {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return fmt.Errorf("stateDB is not initialized")
	}
	if height == 0 {
		return nil
	}
	return stateStore.FlushAndRotate(height)
}

                                                                               
                                                                                 
func (manager *Manager) RecreateStateCheckpoint(height uint64) error {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil
	}
	return stateStore.RecreateCheckpoint(height)
}

func (manager *Manager) LoadStateSnapshotShardMetadata(height uint64) ([]statedb.ShardInfo, []statedb.ShardFingerprint, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, nil, fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.LoadCheckpointShardMetadata(height)
}

func (manager *Manager) ListStateCheckpointFiles(height uint64) ([]statedb.CheckpointFile, int64, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, 0, fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.ListCheckpointFiles(height)
}

func (manager *Manager) WriteStateCheckpointChunk(height uint64, name string, offset, limit int64, w io.Writer) (int64, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return 0, fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.WriteCheckpointChunk(height, name, offset, limit, w)
}

func (manager *Manager) InstallStateCheckpointDir(ctx context.Context, previousHeight, height uint64, checkpointDir string, expectedStateRoot []byte, expectedBlockID string) error {
	if height <= previousHeight {
		return fmt.Errorf("checkpoint height must advance: previous=%d checkpoint=%d", previousHeight, height)
	}
	if height == ^uint64(0) {
		return fmt.Errorf("checkpoint height overflows query prune cursor: %d", height)
	}
	if expectedBlockID == "" {
		return fmt.Errorf("checkpoint block identity is required")
	}
	if err := manager.ForceFlush(); err != nil {
		return fmt.Errorf("ForceFlush before checkpoint directory install failed: %w", err)
	}
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return fmt.Errorf("stateDB is not initialized")
	}
	manager.referralIndexMu.Lock()
	defer manager.referralIndexMu.Unlock()
	manager.rechargeQueryIndexMu.Lock()
	defer manager.rechargeQueryIndexMu.Unlock()
	manager.frostWithdrawQueryIndexMu.Lock()
	defer manager.frostWithdrawQueryIndexMu.Unlock()
	if err := stateStore.InstallCheckpointDir(ctx, height, expectedBlockID, checkpointDir, expectedStateRoot); err != nil {
		return err
	}
	if err := manager.completeStateCheckpointInstallLocked(previousHeight, height); err != nil {
		return &StateCheckpointPostInstallError{Height: height, Err: err}
	}
	return nil
}

                                                                             
                                                                          
                                               
func (manager *Manager) CompleteStateCheckpointInstall(previousHeight, height uint64) error {
	manager.referralIndexMu.Lock()
	defer manager.referralIndexMu.Unlock()
	manager.rechargeQueryIndexMu.Lock()
	defer manager.rechargeQueryIndexMu.Unlock()
	manager.frostWithdrawQueryIndexMu.Lock()
	defer manager.frostWithdrawQueryIndexMu.Unlock()
	return manager.completeStateCheckpointInstallLocked(previousHeight, height)
}

func (manager *Manager) completeStateCheckpointInstallLocked(previousHeight, height uint64) (err error) {
	if height <= previousHeight {
		return fmt.Errorf("checkpoint height must advance: previous=%d checkpoint=%d", previousHeight, height)
	}
	defer func() {
		manager.referralIndexErr = err
		manager.rechargeQueryIndexErr = err
		manager.frostWithdrawQueryIndexErr = err
	}()
	                                                                           
	                                                                         
	                                                                         
	if err := manager.ResetBlockArchiveCoverage(height); err != nil {
		return fmt.Errorf("reset block archive coverage after checkpoint install: %w", err)
	}
	if err := manager.recordQueryPruneStateSync(previousHeight, height); err != nil {
		return err
	}
	if err := manager.rebuildReferralInviteeIndexLocked(); err != nil {
		return err
	}
	if err := manager.rebuildActiveRechargeQueryIndexLocked(); err != nil {
		return err
	}
	return manager.rebuildActiveFrostWithdrawQueryIndexLocked()
}

                                                                                                
func (manager *Manager) GetStateSnapshotFingerprints(height uint64) ([]statedb.ShardFingerprint, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.ComputeShardFingerprints(height)
}

                                                                                                  
func (manager *Manager) GetStateFingerprint() []byte {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil
	}
	return stateStore.GetLiveFingerprintBytes()
}

                                                                                                          
                                                                  
func (manager *Manager) DumpCertEvidence(height uint64) (string, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return "", fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.DumpCertEvidence(height)
}

                                                                                                                                 
func (manager *Manager) DumpMismatchEvidence(height uint64, blockHash string) (string, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return "", fmt.Errorf("stateDB is not initialized")
	}
	return stateStore.DumpMismatchEvidence(height, blockHash)
}

                                                                                  
func (manager *Manager) SetStateDBDriftAudit(enabled bool) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return
	}
	stateStore.SetDriftAudit(enabled)
}

                                                                                             
func (manager *Manager) GetLiveShardFingerprints() []statedb.ShardFingerprint {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil
	}
	fps := stateStore.GetLiveShardFingerprints()
	sort.Slice(fps, func(i, j int) bool { return fps[i].Shard < fps[j].Shard })
	return fps
}
