                                                 
                                                                                                

package roast

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

                                                            
                                                              
func (p *Participant) verifyCoordinator(env *Envelope) error {
	committee, err := p.vaultProvider.VaultCommittee(env.Chain, env.VaultID, env.Epoch)
	if err != nil {
		return err
	}

	if len(committee) == 0 {
		return errors.New("empty committee")
	}

	coordIndex := p.computeCoordinatorIndex(env.SessionID, env.Epoch, committee, env.Chain)

	if coordIndex < 0 || coordIndex >= len(committee) {
		return errors.New("invalid coordinator index")
	}

	currentCoordID := NodeID(committee[coordIndex].ID)
	if env.From != currentCoordID {
		return fmt.Errorf("request from non-current coordinator: expected=%s, got=%s", currentCoordID, env.From)
	}

	return nil
}

                                                                                                 
func (p *Participant) computeCoordinatorIndex(jobID string, keyEpoch uint64, committee []SignerInfo, chain string) int {
	if len(committee) == 0 {
		return 0
	}

	seed := computeAggregatorSeed(jobID, keyEpoch)

	participants := make([]ParticipantInfo, len(committee))
	for i, member := range committee {
		participants[i] = ParticipantInfo{
			ID:    string(member.ID),
			Index: i,
		}
	}

	permuted := permuteParticipantList(participants, seed)

	                                                                                         
	startHeight := p.currentHeight
	blocksElapsed := p.currentHeight - startHeight
	if blocksElapsed < 0 {
		blocksElapsed = 0
	}

	                                               
	aggTimeoutBlocks := uint64(10)
	rotations := blocksElapsed / aggTimeoutBlocks
	aggIndex := int(rotations) % len(permuted)

	for i, member := range committee {
		if member.ID == NodeID(permuted[aggIndex].ID) {
			return i
		}
	}
	return 0
}

                                                    
type ParticipantInfo struct {
	ID    string
	Index int
}

                                                       
func permuteParticipantList(participants []ParticipantInfo, seed []byte) []ParticipantInfo {
	result := make([]ParticipantInfo, len(participants))
	copy(result, participants)

	for i := len(result) - 1; i > 0; i-- {
		indexSeed := sha256.Sum256(append(seed, byte(i)))
		j := int(indexSeed[0]) % (i + 1)
		result[i], result[j] = result[j], result[i]
	}

	return result
}
