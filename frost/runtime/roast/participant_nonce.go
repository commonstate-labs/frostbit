                                           
                                                                                             

package roast

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"dex/frost/runtime/session"
	"dex/logs"
	"dex/pb"
)

                                         
func (p *Participant) HandleNonceRequest(env *FrostEnvelope) error {
	return p.HandleRoastNonceRequest(FromFrostEnvelope(env))
}

                                                                      
func (p *Participant) HandleRoastNonceRequest(env *Envelope) error {
	if env == nil {
		return ErrInvalidNonceRequest
	}
	logs.Debug("[Participant] received nonce request from %s for job %s", env.From, env.SessionID)

	committee, err := p.vaultProvider.VaultCommittee(env.Chain, env.VaultID, env.Epoch)
	if err != nil {
		return err
	}

	myIndex := -1
	for i, member := range committee {
		if member.ID == p.nodeID {
			myIndex = i
			break
		}
	}
	if myIndex < 0 {
		return ErrParticipantNotInCommittee
	}
	logs.Debug("[Participant] nonce request context node=%s job=%s chain=%s vault=%d epoch=%d sign_algo=%s coordinator=%s committee=%s my_index=%d",
		p.nodeID, env.SessionID, env.Chain, env.VaultID, env.Epoch, env.SignAlgo.String(), env.From, committeeSummaryForLog(committee), myIndex)

	if err := p.verifyCoordinator(env); err != nil {
		logs.Warn("[Participant] rejected nonce request from non-current coordinator: %v", err)
		return err
	}

	decodedMsgs, _, ok := decodeRoastRequestPayload(env.Payload)
	if !ok || len(decodedMsgs) == 0 {
		return errors.New("nonce request missing signing messages")
	}
	messages := decodedMsgs
	if err := p.validateSigningRequest(env, messages); err != nil {
		return err
	}
	myShare := p.GetShare(env.Chain, env.VaultID, env.Epoch)
	if myShare == nil {
		return fmt.Errorf("%w: chain=%s vault=%d epoch=%d", ErrLocalShareNotReady, env.Chain, env.VaultID, env.Epoch)
	}
	logs.Debug("[Participant] selected local share for nonce request node=%s job=%s chain=%s vault=%d epoch=%d share_fp=%s",
		p.nodeID, env.SessionID, env.Chain, env.VaultID, env.Epoch, shareFingerprintForLog(myShare))
	logs.Debug("[Participant] nonce request payload decoded node=%s job=%s task_count=%d first_msg=%s",
		p.nodeID, env.SessionID, len(messages), firstMessageForLog(messages))
	sess, err := p.getOrCreateSession(env.SessionID, env.Chain, env.VaultID, env.Epoch, env.SignAlgo, env.Round, myIndex, myShare, messages, env.Tweaks)
	if err != nil {
		if errors.Is(err, ErrStaleRoastRound) {
			logs.Debug("[Participant] ignore stale nonce request node=%s job=%s round=%d", p.nodeID, env.SessionID, env.Round)
			return nil
		}
		return err
	}

	return p.withLiveParticipantSession(env.SessionID, sess, func() error {
		numTasks := len(sess.Messages)
		if numTasks <= 0 {
			numTasks = 1
		}
		if err := p.generateNonces(sess, numTasks); err != nil {
			return err
		}
		if err := p.sendNonceCommitment(sess, env.From, env.Round); errors.Is(err, ErrStaleRoastRound) {
			logs.Debug("[Participant] nonce request became stale before response node=%s job=%s round=%d", p.nodeID, env.SessionID, env.Round)
			return nil
		} else {
			return err
		}
	})
}

                                                                
func (p *Participant) getOrCreateSession(jobID, chain string, vaultID uint32, epoch uint64, signAlgo pb.SignAlgo, round uint32, myIndex int, myShare []byte, messages, tweaks [][]byte) (*ParticipantSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if sess, exists := p.sessions[jobID]; exists {
		sess.mu.Lock()
		defer sess.mu.Unlock()

		if sess.Chain != chain || sess.VaultID != vaultID || sess.KeyEpoch != epoch || sess.SignAlgo != signAlgo || sess.MyIndex != myIndex || !bytes.Equal(sess.MyShare, myShare) {
			return nil, errors.New("nonce request context differs from existing participant session")
		}
		if len(sess.Messages) > 0 && !sameMessages(sess.Messages, messages) {
			return nil, errors.New("nonce request messages differ from existing participant session")
		}
		if len(sess.Tweaks) > 0 && !sameMessages(sess.Tweaks, tweaks) {
			return nil, errors.New("nonce request tweaks differ from existing participant session")
		}

		if round < sess.Round {
			return nil, fmt.Errorf("%w: current=%d got=%d", ErrStaleRoastRound, sess.Round, round)
		}
		if round == sess.Round {
			switch sess.State {
			case ParticipantStateInit:
				if len(sess.Messages) == 0 {
					sess.Messages = cloneMessages(messages)
				}
				if len(sess.Tweaks) == 0 {
					sess.Tweaks = cloneMessages(tweaks)
				}
			case ParticipantStateNonceGenerated, ParticipantStateShareGenerated, ParticipantStateComplete:
				                                                            
				                                                   
			case ParticipantStateFailed:
				return nil, session.ErrInvalidState
			}
		} else {
			hadNonceAttempt := sess.State != ParticipantStateInit || len(sess.HidingNonces) > 0 || len(sess.BindingNonces) > 0
			if hadNonceAttempt {
				maxRetries := DefaultSigningMaxRetries
				if p.config != nil {
					maxRetries = p.config.MaxRetries
				}
				if maxRetries > 0 && sess.RetryCount >= maxRetries {
					return nil, ErrParticipantRetryLimit
				}
				                                                            
				                                                               
				p.releaseParticipantSessionNoncesLocked(sess)
				sess.RetryCount++
			}
			resetParticipantSessionForRetryLocked(sess, myShare, messages, tweaks)
			sess.Round = round
		}

		logs.Debug("[Participant] reuse signing session node=%s job=%s chain=%s vault=%d epoch=%d round=%d my_index=%d state=%s message_count=%d share_fp=%s",
			p.nodeID, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, sess.Round, sess.MyIndex, sess.State.String(), len(sess.Messages), shareFingerprintForLog(sess.MyShare))
		return sess, nil
	}

	sess := &ParticipantSession{
		JobID:     jobID,
		Chain:     chain,
		VaultID:   vaultID,
		KeyEpoch:  epoch,
		SignAlgo:  signAlgo,
		Round:     round,
		MyIndex:   myIndex,
		MyShare:   cloneBytes(myShare),
		Messages:  cloneMessages(messages),
		Tweaks:    cloneMessages(tweaks),
		State:     ParticipantStateInit,
		CreatedAt: time.Now(),
	}

	p.sessions[jobID] = sess
	logs.Debug("[Participant] create signing session node=%s job=%s chain=%s vault=%d epoch=%d round=%d my_index=%d sign_algo=%s message_count=%d share_fp=%s",
		p.nodeID, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, sess.Round, sess.MyIndex, sess.SignAlgo.String(), len(sess.Messages), shareFingerprintForLog(sess.MyShare))
	return sess, nil
}

func (p *Participant) withLiveParticipantSession(jobID string, expected *ParticipantSession, fn func() error) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	current, exists := p.sessions[jobID]
	if !exists || current != expected {
		return ErrSessionNotFound
	}
	return fn()
}

                                                                               
                                                                     
func (p *Participant) releaseParticipantSessionNoncesLocked(sess *ParticipantSession) {
	for _, nonce := range sess.HidingNonces {
		_ = p.sessionStore.ReleaseNonce(nonce)
	}
	for _, nonce := range sess.BindingNonces {
		_ = p.sessionStore.ReleaseNonce(nonce)
	}
}

func resetParticipantSessionForRetryLocked(sess *ParticipantSession, myShare []byte, messages, tweaks [][]byte) {
	sess.MyShare = cloneBytes(myShare)
	sess.Messages = cloneMessages(messages)
	sess.Tweaks = cloneMessages(tweaks)
	sess.HidingNonces = nil
	sess.BindingNonces = nil
	sess.HidingPoints = nil
	sess.BindingPoints = nil
	sess.AggregatedNonces = nil
	sess.GeneratedShares = nil
	sess.State = ParticipantStateInit
	sess.CreatedAt = time.Now()
}

                                                                                                          
func (p *Participant) generateNonces(sess *ParticipantSession, numTasks int) error {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if sess.State != ParticipantStateInit {
		return nil             
	}

	roastExec, err := p.cryptoFactory.NewROASTExecutor(int32(sess.SignAlgo))
	if err != nil {
		return err
	}

	sess.HidingNonces = make([][]byte, numTasks)
	sess.BindingNonces = make([][]byte, numTasks)
	sess.HidingPoints = make([][]byte, numTasks)
	sess.BindingPoints = make([][]byte, numTasks)
	bindings := make([]session.NonceBinding, 0, numTasks*2)

	for i := 0; i < numTasks; i++ {
		hiding, binding, hidingPt, bindingPt, err := roastExec.GenerateNoncePair()
		if err != nil {
			return err
		}

		hidingBytes := make([]byte, 32)
		bindingBytes := make([]byte, 32)
		hiding.FillBytes(hidingBytes)
		binding.FillBytes(bindingBytes)

		sess.HidingNonces[i] = hidingBytes
		sess.BindingNonces[i] = bindingBytes
		sess.HidingPoints[i] = roastExec.SerializePoint(hidingPt)
		sess.BindingPoints[i] = roastExec.SerializePoint(bindingPt)

		                                                                                            
		msg := []byte(sess.JobID)
		if i < len(sess.Messages) && len(sess.Messages[i]) > 0 {
			msg = sess.Messages[i]
		}
		hidingContext := nonceBindingContext(sess.JobID, i, nonceRoleHiding, msg)
		bindings = append(bindings, session.NonceBinding{Nonce: hidingBytes, Message: hidingContext, KeyEpoch: sess.KeyEpoch})
		bindingContext := nonceBindingContext(sess.JobID, i, nonceRoleBinding, msg)
		bindings = append(bindings, session.NonceBinding{Nonce: bindingBytes, Message: bindingContext, KeyEpoch: sess.KeyEpoch})
	}

	if err := p.sessionStore.BindNonceBatch(bindings); err != nil {
		return fmt.Errorf("persist nonce commitment batch: %w", err)
	}

	sess.State = ParticipantStateNonceGenerated
	return nil
}

const nonceBindingDomain = "dex/frost/nonce-binding/v1"

const (
	nonceRoleHiding  = byte(1)
	nonceRoleBinding = byte(2)
)

                                                                               
                                                                              
                                                                      
func nonceBindingContext(jobID string, taskIndex int, nonceRole byte, message []byte) []byte {
	contextLen := len(nonceBindingDomain) + 4 + len(jobID) + 8 + 1 + 4 + len(message)
	out := make([]byte, contextLen)
	offset := copy(out, nonceBindingDomain)
	binary.BigEndian.PutUint32(out[offset:offset+4], uint32(len(jobID)))
	offset += 4
	offset += copy(out[offset:], jobID)
	binary.BigEndian.PutUint64(out[offset:offset+8], uint64(taskIndex))
	offset += 8
	out[offset] = nonceRole
	offset++
	binary.BigEndian.PutUint32(out[offset:offset+4], uint32(len(message)))
	offset += 4
	copy(out[offset:], message)
	return out
}

                                                            
func (p *Participant) sendNonceCommitment(sess *ParticipantSession, coordinator NodeID, round uint32) error {
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	if round < sess.Round {
		return fmt.Errorf("%w: current=%d got=%d", ErrStaleRoastRound, sess.Round, round)
	}
	if round > sess.Round {
		return fmt.Errorf("%w: current=%d got=%d", ErrFutureRoastRound, sess.Round, round)
	}

	msg := &Envelope{
		SessionID: sess.JobID,
		Kind:      "NonceCommit",
		From:      p.nodeID,
		Chain:     sess.Chain,
		VaultID:   sess.VaultID,
		SignAlgo:  sess.SignAlgo,
		Epoch:     sess.KeyEpoch,
		Round:     round,
		Payload:   p.serializeNonces(sess),
	}

	if p.messenger != nil {
		if err := p.messenger.Send(coordinator, toTypesRoastEnvelope(msg)); err != nil {
			return fmt.Errorf("%w: nonce commitment: %v", ErrRoastResponseSendFailed, err)
		}
	}
	return nil
}

                                                                            
func (p *Participant) serializeNonces(sess *ParticipantSession) []byte {
	var result []byte
	for i := range sess.HidingPoints {
		result = append(result, sess.HidingPoints[i]...)
		result = append(result, sess.BindingPoints[i]...)
	}
	return result
}
