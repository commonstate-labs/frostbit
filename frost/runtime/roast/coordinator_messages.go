                                              
                                                                            
                                           

package roast

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	roastsession "dex/frost/runtime/session"
	"dex/logs"
)

                                                                                 
func (c *Coordinator) AddNonce(jobID string, participantIndex int, hidingNonces, bindingNonces [][]byte) error {
	c.mu.RLock()
	sess, exists := c.sessions[jobID]
	c.mu.RUnlock()

	if !exists {
		return ErrSessionNotFound
	}
	return sess.AddNonce(participantIndex, hidingNonces, bindingNonces)
}

                              
func (c *Coordinator) AddShare(jobID string, participantIndex int, shares [][]byte) error {
	c.mu.RLock()
	sess, exists := c.sessions[jobID]
	c.mu.RUnlock()

	if !exists {
		return ErrSessionNotFound
	}
	return sess.AddShare(participantIndex, shares)
}

                                                                 
func (c *Coordinator) HandleNonceCommit(env *FrostEnvelope) error {
	return c.HandleRoastNonceCommit(FromFrostEnvelope(env))
}

                                                                         
func (c *Coordinator) HandleRoastNonceCommit(env *Envelope) error {
	if env == nil {
		return ErrInvalidRoastPayload
	}

	c.mu.RLock()
	sess, exists := c.sessions[env.SessionID]
	c.mu.RUnlock()

	if !exists {
		return ErrSessionNotFound
	}

	participantIndex, ok := participantIndexFor(sess, env.From)
	if !ok {
		return ErrParticipantNotFound
	}

	hiding, binding, err := splitNonceCommitPayload(env.Payload, env.SignAlgo)
	if err != nil {
		return err
	}

	if err := sess.AddNonceForRound(int(env.Round), participantIndex, hiding, binding); err != nil {
		var mismatch *roastsession.RoundMismatchError
		if errors.As(err, &mismatch) {
			logs.Debug("[Coordinator] ignore nonce commit for mismatched round job=%s from=%s got=%d current=%d",
				env.SessionID, env.From, mismatch.Got, mismatch.Expected)
			return nil
		}
		if errors.Is(err, roastsession.ErrInvalidState) {
			switch sess.GetState() {
			case roastsession.SignSessionStateInit:
				return fmt.Errorf("%w: %v", ErrRoastStateNotReady, err)
			case roastsession.SignSessionStateCollectingShares, roastsession.SignSessionStateAggregating, roastsession.SignSessionStateComplete, roastsession.SignSessionStateFailed:
				logs.Debug("[Coordinator] ignore stale nonce commit job=%s from=%s state=%s",
					env.SessionID, env.From, sess.GetState().String())
				return nil
			}
		}
		return err
	}

	return nil
}

                                                             
func (c *Coordinator) HandleSigShare(env *FrostEnvelope) error {
	return c.HandleRoastSigShare(FromFrostEnvelope(env))
}

                                                                     
func (c *Coordinator) HandleRoastSigShare(env *Envelope) error {
	if env == nil {
		return ErrInvalidRoastPayload
	}

	c.mu.RLock()
	sess, exists := c.sessions[env.SessionID]
	c.mu.RUnlock()

	if !exists {
		return ErrSessionNotFound
	}

	participantIndex, ok := participantIndexFor(sess, env.From)
	if !ok {
		return ErrParticipantNotFound
	}

	shares, err := splitSignatureShares(env.Payload)
	if err != nil {
		return err
	}
	shareHexList := make([]string, 0, len(shares))
	for _, share := range shares {
		shareHexList = append(shareHexList, hex.EncodeToString(share))
	}
	signerAddr := string(env.From)
	if participantIndex >= 0 && participantIndex < len(sess.Committee) {
		if sess.Committee[participantIndex].ID != "" {
			signerAddr = sess.Committee[participantIndex].ID
		}
	}
	c.logInfo("[Coordinator] received signature share job=%s chain=%s vault=%d epoch=%d signer=%s participant_index=%d shares=%s",
		env.SessionID, env.Chain, env.VaultID, env.Epoch, signerAddr, participantIndex+1, strings.Join(shareHexList, ","))

	if err := sess.AddShareForRound(int(env.Round), participantIndex, shares); err != nil {
		var mismatch *roastsession.RoundMismatchError
		if errors.As(err, &mismatch) {
			logs.Debug("[Coordinator] ignore signature share for mismatched round job=%s from=%s got=%d current=%d",
				env.SessionID, env.From, mismatch.Got, mismatch.Expected)
			return nil
		}
		if errors.Is(err, roastsession.ErrInvalidState) {
			switch sess.GetState() {
			case roastsession.SignSessionStateInit, roastsession.SignSessionStateCollectingNonces:
				return fmt.Errorf("%w: %v", ErrRoastStateNotReady, err)
			case roastsession.SignSessionStateAggregating, roastsession.SignSessionStateComplete, roastsession.SignSessionStateFailed:
				logs.Debug("[Coordinator] ignore late signature share job=%s from=%s state=%s",
					env.SessionID, env.From, sess.GetState().String())
				return nil
			}
		}
		return err
	}

	return nil
}

                          
func (c *Coordinator) GetSession(jobID string) *roastsession.Session {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessions[jobID]
}

                                        
func (c *Coordinator) GetFinalSignatures(jobID string) ([][]byte, error) {
	c.mu.RLock()
	sess, exists := c.sessions[jobID]
	c.mu.RUnlock()

	if !exists {
		return nil, ErrSessionNotFound
	}
	if sess.GetState() != roastsession.SignSessionStateComplete {
		return nil, errors.New("session not complete")
	}
	return sess.GetFinalSignatures(), nil
}

                            
func (c *Coordinator) CloseSession(jobID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, jobID)
}
