                                             
                                                                        

package roast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	roastsession "dex/frost/runtime/session"
	"dex/logs"
	"dex/pb"
)

                                        
type StartSessionParams struct {
	JobID        string
	maxInFlight  int                        
	localAddress string
	Logger       logs.Logger
	VaultID      uint32
	Chain        string
	KeyEpoch     uint64
	SignAlgo     pb.SignAlgo
	Messages     [][]byte                         
	Threshold    int
	Tweaks       [][]byte                              
}

                                        
func (c *Coordinator) StartSession(ctx context.Context, params *StartSessionParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.sessions[params.JobID]; exists {
		return ErrSessionAlreadyExists
	}

	committeeInfo, err := c.vaultProvider.VaultCommittee(params.Chain, params.VaultID, params.KeyEpoch)
	if err != nil {
		return err
	}
	if len(committeeInfo) < params.Threshold {
		return ErrInsufficientParticipants
	}

	c.logInfo("[Coordinator] start signing session job=%s chain=%s vault=%d epoch=%d threshold=%d sign_algo=%s tasks=%d",
		params.JobID, params.Chain, params.VaultID, params.KeyEpoch, params.Threshold, params.SignAlgo.String(), len(params.Messages))
	c.logInfo("[Coordinator] vault voter committee job=%s chain=%s vault=%d epoch=%d size=%d members=%s",
		params.JobID, params.Chain, params.VaultID, params.KeyEpoch, len(committeeInfo), formatSignerInfoList(committeeInfo))
	for i, msg := range params.Messages {
		c.logInfo("[Coordinator] signing message job=%s chain=%s vault=%d task=%d payload=%s",
			params.JobID, params.Chain, params.VaultID, i, hex.EncodeToString(msg))
	}
	if groupPubkey, pubErr := c.vaultProvider.VaultGroupPubkey(params.Chain, params.VaultID, params.KeyEpoch); pubErr != nil {
		c.logWarn("[Coordinator] failed to load aggregated public key for job=%s chain=%s vault=%d epoch=%d: %v",
			params.JobID, params.Chain, params.VaultID, params.KeyEpoch, pubErr)
	} else {
		c.logInfo("[Coordinator] aggregated public key job=%s chain=%s vault=%d epoch=%d pubkey=%s",
			params.JobID, params.Chain, params.VaultID, params.KeyEpoch, hex.EncodeToString(groupPubkey))
	}

	myIndex := -1
	committee := make([]roastsession.Participant, len(committeeInfo))
	for i, member := range committeeInfo {
		committee[i] = roastsession.Participant{
			ID:    string(member.ID),
			Index: i,
		}
		if member.ID == c.nodeID {
			myIndex = i
		}
	}
	if myIndex < 0 {
		return ErrParticipantNotFound
	}

	sess := roastsession.NewSession(roastsession.SessionParams{
		JobID:       params.JobID,
		VaultID:     params.VaultID,
		Chain:       params.Chain,
		KeyEpoch:    params.KeyEpoch,
		SignAlgo:    int32(params.SignAlgo),
		Messages:    params.Messages,
		Committee:   committee,
		Threshold:   params.Threshold,
		MyIndex:     myIndex,
		StartHeight: c.currentHeight,
		Tweaks:      params.Tweaks,
	})

	if !c.isCurrentCoordinator(sess) {
		return ErrCoordinatorNotLeader
	}
	if err := sess.Start(); err != nil {
		return err
	}
	c.sessions[params.JobID] = sess
	go c.runCoordinatorLoop(ctx, sess)

	return nil
}

                                                               
func (c *Coordinator) isCurrentCoordinator(sess *roastsession.Session) bool {
	if len(sess.Committee) == 0 {
		return false
	}
	coordIndex := c.computeCoordinatorIndex(sess)
	return sess.MyIndex == coordIndex
}

                                                      
                                                                  
                                                                                                                         
func (c *Coordinator) computeCoordinatorIndex(sess *roastsession.Session) int {
	if len(sess.Committee) == 0 {
		return 0
	}
	seed := computeAggregatorSeed(sess.JobID, sess.KeyEpoch)
	permuted := permuteCommittee(sess.Committee, seed)

	blocksElapsed := c.currentHeight - sess.StartHeight
	rotations := blocksElapsed / c.config.AggregatorRotateBlocks
	aggIndex := int(rotations) % len(permuted)

	for i, member := range sess.Committee {
		if member.ID == permuted[aggIndex].ID {
			return i
		}
	}
	return 0
}

                                                                              
func (c *Coordinator) shouldRotateCoordinator(sess *roastsession.Session) bool {
	blocksElapsed := c.currentHeight - sess.StartHeight
	if blocksElapsed >= c.config.AggregatorRotateBlocks {
		newCoordIndex := c.computeCoordinatorIndex(sess)
		return sess.MyIndex != newCoordIndex
	}
	return false
}

                                                    
func computeAggregatorSeed(jobID string, keyEpoch uint64) []byte {
	data := jobID + "|" + string(rune(keyEpoch)) + "|frost_agg"
	hash := sha256.Sum256([]byte(data))
	return hash[:]
}

                                                                                   
func permuteCommittee(committee []roastsession.Participant, seed []byte) []roastsession.Participant {
	result := make([]roastsession.Participant, len(committee))
	copy(result, committee)
	for i := len(result) - 1; i > 0; i-- {
		indexSeed := sha256.Sum256(append(seed, byte(i)))
		j := int(indexSeed[0]) % (i + 1)
		result[i], result[j] = result[j], result[i]
	}
	return result
}
