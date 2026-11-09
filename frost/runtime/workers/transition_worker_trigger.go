package workers

import (
	"context"
	"dex/frost/assignment"
	"dex/keys"
	"dex/pb"
	"fmt"

	"google.golang.org/protobuf/proto"
)

func (w *TransitionWorker) loadChainVaultConfig(chain string) (*pb.FrostVaultConfig, error) {
	vaultCfgData, exists, err := w.stateReader.Get(keys.KeyFrostVaultConfig(chain, 0))
	if err != nil {
		return nil, fmt.Errorf("get vault config: %w", err)
	}
	if !exists || len(vaultCfgData) == 0 {
		return nil, fmt.Errorf("vault config not found")
	}

	var vaultCfg pb.FrostVaultConfig
	if err := proto.Unmarshal(vaultCfgData, &vaultCfg); err != nil {
		return nil, fmt.Errorf("unmarshal vault config: %w", err)
	}
	return &vaultCfg, nil
}

func (w *TransitionWorker) loadVaultState(chain string, vaultID uint32) (*pb.FrostVaultState, error) {
	data, exists, err := w.stateReader.Get(keys.KeyFrostVaultState(chain, vaultID))
	if err != nil {
		return nil, fmt.Errorf("get vault state: %w", err)
	}
	if !exists || len(data) == 0 {
		return nil, fmt.Errorf("vault state not found")
	}

	var state pb.FrostVaultState
	if err := proto.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("unmarshal vault state: %w", err)
	}
	return &state, nil
}

func buildIdealVaultCommittee(chain string, epochID uint64, vaultID uint32, vaultCfg *pb.FrostVaultConfig, currentSigners []SignerInfo) ([]SignerInfo, error) {
	if vaultCfg == nil {
		return nil, fmt.Errorf("vault config is nil")
	}
	if len(currentSigners) == 0 {
		return nil, nil
	}

	sortedIndices := make([]uint64, 0, len(currentSigners))
	signersByIndex := make(map[uint64]SignerInfo, len(currentSigners))
	for _, signer := range currentSigners {
		idx := uint64(signer.Index)
		sortedIndices = append(sortedIndices, idx)
		signerCopy := signer
		if len(signer.PublicKey) > 0 {
			signerCopy.PublicKey = append([]byte(nil), signer.PublicKey...)
		}
		signersByIndex[idx] = signerCopy
	}

	sortedIndices = assignment.SortByBitIndex(sortedIndices)
	targetIndices := assignment.GetVaultCommittee(
		sortedIndices,
		epochID,
		chain,
		vaultID,
		int(vaultCfg.VaultCount),
		int(vaultCfg.CommitteeSize),
	)

	result := make([]SignerInfo, 0, len(targetIndices))
	for _, idx := range targetIndices {
		signer, ok := signersByIndex[idx]
		if !ok {
			return nil, fmt.Errorf("signer index %d missing from top set", idx)
		}
		result = append(result, signer)
	}
	return result, nil
}

                                                                             
                                                                               
func (w *TransitionWorker) StartPendingSessions(ctx context.Context) error {
	w.mu.RLock()
	em := w.emergency
	w.mu.RUnlock()
	if em.IsStopped() {
		return nil
	}
	localRecovery := w.recoverDkgLocalSessions()
	prefix := keys.KeyFrostVaultTransitionActivePrefix()

	count := 0
	err := w.stateReader.Scan(prefix, func(k string, v []byte) bool {
		count++
		transitionKey := string(v)
		if transitionKey == "" {
			w.Logger.Debug("[TransitionWorker] skip active transition index with empty value: %s", k)
			return true
		}

		transitionData, exists, err := w.stateReader.Get(transitionKey)
		if err != nil {
			w.Logger.Warn("[TransitionWorker] failed to load transition by index %s -> %s: %v", k, transitionKey, err)
			return true
		}
		if !exists || len(transitionData) == 0 {
			w.Logger.Debug("[TransitionWorker] transition missing for index %s -> %s", k, transitionKey)
			return true
		}

		var state pb.VaultTransitionState
		if err := proto.Unmarshal(transitionData, &state); err != nil {
			w.Logger.Error("[TransitionWorker] Failed to unmarshal transition state for key %s: %v", transitionKey, err)
			return true
		}
		if state.DkgStatus == "FAILED" {
			return true                                                 
		}
		                                                      
		if state.DkgStatus == "KEY_READY" {
			migrationKey := fmt.Sprintf("%s_%d_%d", state.Chain, state.VaultId, state.EpochId)
			if state.MigrationStatus != "INTENT_FINALIZED" {
				return true
			}
			if !committeeContains(state.OldCommitteeMembers, w.localAddress) {
				return true
			}
			localMarkerPresent := false
			if w.localShareStore != nil && !localRecovery.listOK {
				w.Logger.Warn("[TransitionWorker] skip KEY_READY migration planning because local DKG recovery scan failed: chain=%s vault=%d epoch=%d session=%s",
					state.Chain, state.VaultId, state.EpochId, state.DkgSessionId)
				return true
			}
			if localRecovery.blocked[migrationKey] {
				w.Logger.Warn("[TransitionWorker] skip migration planning until local KEY_READY share promotion succeeds: chain=%s vault=%d epoch=%d session=%s",
					state.Chain, state.VaultId, state.EpochId, state.DkgSessionId)
				return true
			}
			if w.localShareStore != nil {
				hasMarker, err := w.localShareStore.HasDkgLocalSession(state.Chain, state.VaultId, state.EpochId, state.DkgSessionId)
				if err != nil {
					w.Logger.Warn("[TransitionWorker] skip migration planning after local DKG marker read failure: chain=%s vault=%d epoch=%d session=%s err=%v",
						state.Chain, state.VaultId, state.EpochId, state.DkgSessionId, err)
					return true
				}
				localMarkerPresent = hasMarker
				if hasMarker && !localRecovery.promoted[migrationKey] {
					w.Logger.Warn("[TransitionWorker] skip migration planning for unverified local DKG marker: chain=%s vault=%d epoch=%d session=%s",
						state.Chain, state.VaultId, state.EpochId, state.DkgSessionId)
					return true
				}
			}
			if err := w.planMigrationOnce(ctx, &state); err != nil {
				w.Logger.Warn("[TransitionWorker] migration planning failed: chain=%s vault=%d epoch=%d: %v",
					state.Chain, state.VaultId, state.EpochId, err)
				return true
			}
			if localMarkerPresent && state.DkgSessionId != "" {
				if err := w.localShareStore.DeleteDkgSessionArtifacts(state.Chain, state.VaultId, state.EpochId, state.DkgSessionId); err != nil {
					w.Logger.Warn("[TransitionWorker] cleanup completed DKG local artifacts failed: chain=%s vault=%d epoch=%d session=%s err=%v",
						state.Chain, state.VaultId, state.EpochId, state.DkgSessionId, err)
				}
			}
			return true
		}
		if err := w.StartSession(ctx, state.Chain, state.VaultId, state.EpochId, state.SignAlgo); err != nil {
			w.Logger.Warn("[TransitionWorker] start session failed: chain=%s vault=%d epoch=%d: %v",
				state.Chain, state.VaultId, state.EpochId, err)
		}
		return true
	})
	if count > 0 {
		w.Logger.Debug("[TransitionWorker] StartPendingSessions scanned %d transition keys", count)
	}
	return err
}

                                                                              
                                                                           
                                                                             
type dkgLocalRecoveryResult struct {
	listOK   bool
	promoted map[string]bool
	blocked  map[string]bool
}

func (w *TransitionWorker) recoverDkgLocalSessions() dkgLocalRecoveryResult {
	result := dkgLocalRecoveryResult{
		listOK:   true,
		promoted: make(map[string]bool),
		blocked:  make(map[string]bool),
	}
	if w.localShareStore == nil {
		return result
	}

	refs, err := w.localShareStore.ListDkgLocalSessions()
	if err != nil {
		w.Logger.Error("[TransitionWorker] list local DKG recovery sessions failed: %v", err)
		result.listOK = false
		return result
	}
	for _, ref := range refs {
		transitionKey := keys.KeyFrostVaultTransition(ref.Chain, ref.VaultID, ref.Epoch)
		data, exists, err := w.stateReader.Get(transitionKey)
		if err != nil {
			w.Logger.Warn("[TransitionWorker] retain local DKG artifacts after transition read failure: chain=%s vault=%d epoch=%d session=%s err=%v",
				ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID, err)
			continue
		}
		if !exists || len(data) == 0 {
			                                                                     
			                                                                      
			w.Logger.Debug("[TransitionWorker] retain local DKG artifacts while transition is unavailable: chain=%s vault=%d epoch=%d session=%s",
				ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID)
			continue
		}

		var transition pb.VaultTransitionState
		if err := proto.Unmarshal(data, &transition); err != nil {
			w.Logger.Warn("[TransitionWorker] retain local DKG artifacts after transition decode failure: chain=%s vault=%d epoch=%d session=%s err=%v",
				ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID, err)
			continue
		}

		if transition.DkgSessionId == ref.SessionID && transition.DkgStatus == "KEY_READY" {
			migrationKey := fmt.Sprintf("%s_%d_%d", ref.Chain, ref.VaultID, ref.Epoch)
			if err := w.localShareStore.PromotePendingLocalShare(ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID); err != nil {
				result.blocked[migrationKey] = true
				w.Logger.Error("[TransitionWorker] promote finalized DKG local share failed: chain=%s vault=%d epoch=%d session=%s err=%v",
					ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID, err)
				continue
			}
			result.promoted[migrationKey] = true
			w.Logger.Info("[TransitionWorker] promoted finalized DKG local share: chain=%s vault=%d epoch=%d session=%s",
				ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID)
			if !transitionNeedsMigration(&transition) {
				if err := w.localShareStore.DeleteDkgSessionArtifacts(ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID); err != nil {
					w.Logger.Warn("[TransitionWorker] cleanup directly activated DKG artifacts failed: chain=%s vault=%d epoch=%d session=%s err=%v",
						ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID, err)
				}
			}
			continue
		}

		if transition.DkgSessionId != ref.SessionID || transition.DkgStatus == "FAILED" {
			if err := w.localShareStore.DeleteDkgSessionArtifacts(ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID); err != nil {
				w.Logger.Warn("[TransitionWorker] delete superseded DKG local artifacts failed: chain=%s vault=%d epoch=%d session=%s err=%v",
					ref.Chain, ref.VaultID, ref.Epoch, ref.SessionID, err)
			}
		}
	}
	return result
}

func transitionNeedsMigration(transition *pb.VaultTransitionState) bool {
	if transition == nil || len(transition.OldGroupPubkey) == 0 {
		return false
	}
	switch transition.Lifecycle {
	case "ACTIVE", "MIGRATED", "COMPLETED", "RETIRED":
		return false
	default:
		return true
	}
}

func committeeContains(committee []string, member string) bool {
	for _, candidate := range committee {
		if candidate == member {
			return true
		}
	}
	return false
}

func (w *TransitionWorker) planMigrationOnce(ctx context.Context, transition *pb.VaultTransitionState) error {
	if transition == nil {
		return fmt.Errorf("transition is nil")
	}
	migrationKey := fmt.Sprintf("%s_%d_%d", transition.Chain, transition.VaultId, transition.EpochId)
	currentHeight := w.GetCurrentHeight()
	w.mu.Lock()
	if w.plannedMigrations == nil {
		w.plannedMigrations = make(map[string]bool)
	}
	if w.migrationLastAttempt == nil {
		w.migrationLastAttempt = make(map[string]uint64)
	}
	if w.plannedMigrations[migrationKey] {
		w.mu.Unlock()
		return nil
	}
	if lastHeight := w.migrationLastAttempt[migrationKey]; currentHeight > 0 && lastHeight > 0 && currentHeight >= lastHeight && currentHeight-lastHeight < 3 {
		w.mu.Unlock()
		return nil
	}
	w.plannedMigrations[migrationKey] = true
	if currentHeight > 0 {
		w.migrationLastAttempt[migrationKey] = currentHeight
	}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.plannedMigrations, migrationKey)
		w.mu.Unlock()
	}()
	return w.PlanMigrationJobs(ctx, transition.Chain, transition.VaultId, transition.EpochId)
}
