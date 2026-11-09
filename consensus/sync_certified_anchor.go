package consensus

import (
	"dex/pb"
	statedb "dex/stateDB"
	"dex/types"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                              
                                                                              
                                
func validateCertifiedAnchorBlock(anchor *pb.Block, height uint64, blockID string) (*types.Block, error) {
	if height == 0 {
		return nil, fmt.Errorf("invalid certified anchor height: 0")
	}
	if blockID == "" {
		return nil, fmt.Errorf("empty certified anchor block id")
	}
	if anchor == nil || anchor.Header == nil {
		return nil, fmt.Errorf("nil certified anchor block/header")
	}
	if !isCompleteBlockPayload(anchor) {
		return nil, fmt.Errorf("certified anchor block payload is incomplete")
	}
	if anchor.Header.Height != height {
		return nil, fmt.Errorf("certified anchor height mismatch: cert=%d block=%d", height, anchor.Header.Height)
	}
	if anchor.BlockHash != blockID {
		return nil, fmt.Errorf("certified anchor block id mismatch: cert=%s block=%s", blockID, anchor.BlockHash)
	}
	if anchor.Header.PrevBlockHash == "" {
		return nil, fmt.Errorf("certified anchor is missing parent block id")
	}
	if len(anchor.Header.StateRoot) != statedb.StateRootSize {
		return nil, fmt.Errorf("certified anchor pre-state root length mismatch: got=%d want=%d", len(anchor.Header.StateRoot), statedb.StateRootSize)
	}
	if anchor.Header.Window < 0 {
		return nil, fmt.Errorf("certified anchor has invalid proposal window %d", anchor.Header.Window)
	}
	if len(anchor.Header.VrfProof) == 0 || len(anchor.Header.VrfOutput) == 0 {
		return nil, fmt.Errorf("certified anchor is missing VRF proof or output")
	}
	if len(anchor.Header.PublicKey) == 0 {
		return nil, fmt.Errorf("certified anchor is missing proposer public key")
	}
	if err := ValidateBlockCommitment(anchor); err != nil {
		return nil, fmt.Errorf("certified anchor commitment validation failed: %w", err)
	}
	converted, err := (&ConsensusAdapter{}).DBBlockToConsensus(anchor)
	if err != nil {
		return nil, fmt.Errorf("convert certified anchor: %w", err)
	}
	if err := validateHeaderMinerBinding(converted.Header); err != nil {
		return nil, fmt.Errorf("certified anchor proposer binding failed: %w", err)
	}
	return converted, nil
}

                                                                            
                                                                                
                                                                      
func (sm *SyncManager) fetchCertifiedAnchorBlock(height uint64, blockID string) (*pb.Block, error) {
	fetcher, ok := sm.transport.(stateSnapshotFetcher)
	if !ok {
		return nil, fmt.Errorf("transport does not support finalized block fetch: %T", sm.transport)
	}
	if height == 0 || blockID == "" {
		return nil, fmt.Errorf("incomplete certified anchor identity: height=%d block=%q", height, blockID)
	}

	peers := sm.selectStateSyncPeers(height, sm.stateSyncPeers())
	if len(peers) == 0 {
		return nil, fmt.Errorf("no peers available for certified anchor height %d", height)
	}

	var lastErr error
	for _, peer := range peers {
		anchor, err := fetcher.FetchFinalizedBlock(peer, height)
		if err == nil {
			if sanitized, changed := sanitizeBlockTxsForCanonicalValidation(anchor); changed {
				anchor = sanitized
			}
			_, err = validateCertifiedAnchorBlock(anchor, height, blockID)
		}
		if err == nil {
			if realStore, ok := sm.store.(*RealBlockStore); ok && realStore != nil {
				err = realStore.lightweightValidateBlock(anchor)
			}
		}
		if err != nil {
			lastErr = err
			if sm.Logger != nil {
				sm.Logger.Warn("[SyncPipeline] certified anchor fetch from %s failed at height %d: %v", peer, height, err)
			}
			continue
		}
		cloned, ok := proto.Clone(anchor).(*pb.Block)
		if !ok || cloned == nil {
			lastErr = fmt.Errorf("clone certified anchor returned %T", cloned)
			continue
		}
		return cloned, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all peers returned no certified anchor")
	}
	return nil, fmt.Errorf("fetch certified anchor height %d block %s: %w", height, blockID, lastErr)
}
