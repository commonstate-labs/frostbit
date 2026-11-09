package consensus

import (
	"bytes"
	"dex/interfaces"
	statedb "dex/stateDB"
	"dex/types"
	"errors"
	"fmt"
)

                                                                           
                                                                              
                               
var ErrInvalidCandidatePreStateRoot = errors.New("invalid candidate pre-state root")

                                                                            
                                                                          
type candidatePreStateValidator interface {
	ValidateCandidatePreStateRoot(block *types.Block) error
}

type candidateDiscarder interface {
	DiscardCandidate(height uint64, blockID string) bool
}

type candidateInvalidator interface {
	InvalidateCandidate(height uint64, blockID string)
}

func validateCandidatePreStateRoot(store interfaces.BlockStore, block *types.Block) error {
	validator, ok := store.(candidatePreStateValidator)
	if !ok {
		return nil
	}
	return validator.ValidateCandidatePreStateRoot(block)
}

func invalidateDeterministicCandidate(engine interfaces.ConsensusEngine, store interfaces.BlockStore, height uint64, blockID string) {
	if invalidator, ok := engine.(candidateInvalidator); ok {
		invalidator.InvalidateCandidate(height, blockID)
		return
	}
	if discarder, ok := store.(candidateDiscarder); ok {
		discarder.DiscardCandidate(height, blockID)
	}
}

                                                                               
                                                                              
                                              
func (s *RealBlockStore) ValidateCandidatePreStateRoot(block *types.Block) error {
	if block == nil {
		return fmt.Errorf("%w: nil block", ErrInvalidCandidatePreStateRoot)
	}

	                                                                           
	                                                                  
	s.mu.RLock()
	currentChild := s.lastAccepted != nil &&
		block.Header.Height == s.lastAcceptedHeight+1 &&
		block.Header.ParentID == s.lastAccepted.ID
	s.mu.RUnlock()
	if !currentChild {
		return nil
	}

	                                                                             
	                                                                            
	s.finalizeMu.Lock()
	defer s.finalizeMu.Unlock()
	return s.validateCandidatePreStateRootLocked(block)
}

                                                                            
                                                     
func (s *RealBlockStore) validateCandidatePreStateRootLocked(block *types.Block) error {
	if block == nil {
		return fmt.Errorf("%w: nil block", ErrInvalidCandidatePreStateRoot)
	}

	s.mu.RLock()
	acceptedHeight := s.lastAcceptedHeight
	acceptedID := ""
	if s.lastAccepted != nil {
		acceptedID = s.lastAccepted.ID
	}
	s.mu.RUnlock()

	if block.Header.Height != acceptedHeight+1 || block.Header.ParentID != acceptedID {
		return nil
	}

	headerRoot := []byte(block.Header.StateRoot)
	if len(headerRoot) != statedb.StateRootSize {
		return fmt.Errorf("%w: block=%s height=%d root length=%d want=%d",
			ErrInvalidCandidatePreStateRoot, block.ID, block.Header.Height, len(headerRoot), statedb.StateRootSize)
	}
	if s.dbManager == nil {
		return fmt.Errorf("candidate pre-state root unavailable: StateDB manager is nil")
	}

	localRoot, localFP := statedb.ReadFingerprintFromProvider(s.dbManager)
	if len(localRoot) != statedb.StateRootSize {
		return fmt.Errorf("candidate pre-state root unavailable: local root length=%d want=%d",
			len(localRoot), statedb.StateRootSize)
	}
	if bytes.Equal(localRoot, headerRoot) {
		return nil
	}

	headerFP := statedb.ParseFingerprintFromBytes(headerRoot)
	return fmt.Errorf("%w: block=%s height=%d local_fp=%016x header_fp=%016x",
		ErrInvalidCandidatePreStateRoot, block.ID, block.Header.Height, localFP, headerFP)
}

                                                                                
                                                                 
func (s *RealBlockStore) DiscardCandidate(height uint64, blockID string) bool {
	if blockID == "" {
		return false
	}

	s.mu.Lock()
	if finalized := s.finalizedBlocks[height]; finalized != nil && finalized.ID == blockID {
		s.mu.Unlock()
		return false
	}

	removed := false
	if block, ok := s.blockCache[blockID]; ok && block != nil && block.Header.Height == height {
		delete(s.blockCache, blockID)
		removed = true
	}

	blocks := s.heightIndex[height]
	if len(blocks) > 0 {
		kept := make([]*types.Block, 0, len(blocks))
		for _, block := range blocks {
			if block != nil && block.ID == blockID {
				removed = true
				continue
			}
			kept = append(kept, block)
		}
		if len(kept) == 0 {
			delete(s.heightIndex, height)
		} else {
			s.heightIndex[height] = kept
		}
	}
	if removed && height == s.maxHeight {
		for s.maxHeight > s.lastAcceptedHeight && len(s.heightIndex[s.maxHeight]) == 0 {
			s.maxHeight--
		}
	}
	s.mu.Unlock()

	if removed {
		RemoveCachedBlock(blockID)
	}
	return removed
}
