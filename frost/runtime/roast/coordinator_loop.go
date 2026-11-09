                                          
                                                                                 

package roast

import (
	"bytes"
	"context"
	"time"

	roastsession "dex/frost/runtime/session"
	"dex/logs"
	"dex/pb"
)

                                        
func (c *Coordinator) runCoordinatorLoop(ctx context.Context, sess *roastsession.Session) {
	c.logInfo("[Coordinator] starting coordinator loop job=%s chain=%s vault=%d epoch=%d state=%d messenger=%v",
		sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, sess.GetState(), c.messenger != nil)

	rotateTicker := time.NewTicker(1 * time.Second)
	defer rotateTicker.Stop()
	stateTicker := time.NewTicker(200 * time.Millisecond)
	defer stateTicker.Stop()

	var lastBroadcastState roastsession.SignSessionState = -1
	lastBroadcastRound := -1

	for {
		select {
		case <-ctx.Done():
			c.logInfo("[Coordinator] context cancelled for session %s", sess.JobID)
			return
		case <-rotateTicker.C:
			if !c.isCurrentCoordinator(sess) {
				c.logInfo("[Coordinator] no longer coordinator for session %s, stopping", sess.JobID)
				return
			}
			continue
		case <-stateTicker.C:
		}

		state := sess.GetState()
		round := sess.CurrentRound()

		switch state {
		case roastsession.SignSessionStateCollectingNonces:
			if lastBroadcastState != state || lastBroadcastRound != round {
				c.broadcastNonceRequest(sess)
				lastBroadcastState = state
				lastBroadcastRound = round
			}

			if c.waitForNonces(ctx, sess) {
				c.logInfo("[Coordinator] nonces collected, freezing nonce payload and broadcasting sign request job=%s", sess.JobID)
				c.freezeAndBroadcastSignRequest(sess)
				sess.SetState(roastsession.SignSessionStateCollectingShares)
				lastBroadcastState = roastsession.SignSessionStateCollectingShares
				lastBroadcastRound = round
			} else {
				c.logInfo("[Coordinator] nonce collection timeout job=%s retry=%d", sess.JobID, sess.RetryCount)
				if c.shouldRotateCoordinator(sess) {
					c.logInfo("[Coordinator] rotating coordinator for session %s due to nonce timeout", sess.JobID)
					return
				}
				if !c.retryWithNewSet(sess) {
					c.logInfo("[Coordinator] max retries exceeded, session %s FAILED at nonce collection", sess.JobID)
					sess.SetState(roastsession.SignSessionStateFailed)
					return
				}
			}

		case roastsession.SignSessionStateCollectingShares:
			if lastBroadcastState != state || lastBroadcastRound != round {
				c.broadcastSignRequest(sess)
				lastBroadcastState = state
				lastBroadcastRound = round
			}

			if c.waitForShares(ctx, sess) {
				c.logInfo("[Coordinator] shares collected, aggregating job=%s", sess.JobID)
				sess.SetState(roastsession.SignSessionStateAggregating)
			} else {
				c.logInfo("[Coordinator] share collection timeout job=%s retry=%d", sess.JobID, sess.RetryCount)
				if c.shouldRotateCoordinator(sess) {
					c.logInfo("[Coordinator] rotating coordinator for session %s due to share timeout", sess.JobID)
					return
				}
				if !c.retryWithNewSet(sess) {
					c.logInfo("[Coordinator] max retries exceeded, session %s FAILED at share collection", sess.JobID)
					sess.SetState(roastsession.SignSessionStateFailed)
					return
				}
			}

		case roastsession.SignSessionStateAggregating:
			signatures, err := c.aggregateSignatures(sess)
			if err != nil {
				c.logWarn("[Coordinator] aggregate failed job=%s: %v", sess.JobID, err)
				if c.shouldRotateCoordinator(sess) {
					c.logInfo("[Coordinator] rotating coordinator for session %s due to aggregate failure", sess.JobID)
					return
				}
				if !c.retryWithNewSet(sess) {
					c.logInfo("[Coordinator] max retries exceeded, session %s FAILED at aggregation", sess.JobID)
					sess.SetState(roastsession.SignSessionStateFailed)
					return
				}
			} else {
				allCompleted := true
				for i := 0; i < len(sess.Messages); i++ {
					if i >= len(signatures) || signatures[i] == nil {
						allCompleted = false
						break
					}
				}

				if allCompleted {
					logs.Info("[Coordinator] session %s completed (all tasks)", sess.JobID)
					return
				} else {
					logs.Info("[Coordinator] session %s partial completion, retrying remaining tasks", sess.JobID)
					if !c.retryWithNewSet(sess) {
						c.logInfo("[Coordinator] max retries exceeded, session %s FAILED at partial completion", sess.JobID)
						sess.SetState(roastsession.SignSessionStateFailed)
						return
					}
				}
			}

		case roastsession.SignSessionStateComplete, roastsession.SignSessionStateFailed:
			return
		}
	}
}

                                            
func (c *Coordinator) broadcastNonceRequest(sess *roastsession.Session) {
	if c.messenger == nil {
		return
	}
	selectedSet := sess.SelectedSetSnapshot()
	peers := make([]NodeID, 0, len(selectedSet))
	for _, idx := range selectedSet {
		if idx >= len(sess.Committee) {
			continue
		}
		peers = append(peers, NodeID(sess.Committee[idx].ID))
	}
	if len(peers) == 0 {
		return
	}

	msg := &Envelope{
		SessionID: sess.JobID,
		Kind:      "NonceRequest",
		From:      c.nodeID,
		Chain:     sess.Chain,
		VaultID:   sess.VaultID,
		SignAlgo:  pb.SignAlgo(sess.SignAlgo),
		Epoch:     sess.KeyEpoch,
		Round:     uint32(sess.CurrentRound()),
		Payload:   encodeRoastRequestPayload(sess.Messages, nil),
		Tweaks:    sess.Tweaks,
	}

	_ = c.messenger.Broadcast(peers, toTypesRoastEnvelope(msg))
}

                                   
func (c *Coordinator) waitForNonces(ctx context.Context, sess *roastsession.Session) bool {
	deadline := time.After(c.config.NonceCollectTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-ticker.C:
			if sess.HasEnoughNonces() {
				return true
			}
		}
	}
}

                                                                             
func (c *Coordinator) broadcastSignRequest(sess *roastsession.Session) {
	if c.messenger == nil {
		return
	}

	c.mu.RLock()
	payload := sess.CachedNoncePayload
	c.mu.RUnlock()
	if len(payload) == 0 {
		payload = c.aggregateNonces(sess)
	}

	selectedSet := sess.SelectedSetSnapshot()
	peers := make([]NodeID, 0, len(selectedSet))
	for _, idx := range selectedSet {
		if idx >= len(sess.Committee) {
			continue
		}
		peers = append(peers, NodeID(sess.Committee[idx].ID))
	}
	if len(peers) == 0 {
		return
	}

	msg := &Envelope{
		SessionID: sess.JobID,
		Kind:      "SignRequest",
		From:      c.nodeID,
		Chain:     sess.Chain,
		VaultID:   sess.VaultID,
		SignAlgo:  pb.SignAlgo(sess.SignAlgo),
		Epoch:     sess.KeyEpoch,
		Round:     uint32(sess.CurrentRound()),
		Payload:   payload,
		Tweaks:    sess.Tweaks,
	}

	_ = c.messenger.Broadcast(peers, toTypesRoastEnvelope(msg))
}

                                                                                    
func (c *Coordinator) freezeAndBroadcastSignRequest(sess *roastsession.Session) {
	payload := c.aggregateNonces(sess)
	c.mu.Lock()
	sess.CachedNoncePayload = payload
	c.mu.Unlock()
	c.broadcastSignRequest(sess)
}

                                                                                             
func (c *Coordinator) aggregateNonces(sess *roastsession.Session) []byte {
	var buf bytes.Buffer
	signerIDs := make([]int, 0)
	selectedSet := sess.SelectedSetSnapshot()
	for _, idx := range selectedSet {
		if nonce, ok := sess.GetNonce(idx); ok {
			signerIDs = append(signerIDs, idx+1)
			for i := range nonce.HidingNonces {
				buf.Write(nonce.HidingNonces[i])
				buf.Write(nonce.BindingNonces[i])
			}
		}
	}
	if buf.Len() == 0 || len(signerIDs) == 0 {
		return nil
	}
	needsHeader := false
	for pos, signerID := range signerIDs {
		if signerID != pos+1 {
			needsHeader = true
			break
		}
	}
	if !needsHeader {
		return buf.Bytes()
	}
	return encodeAggregatedNoncePayload(signerIDs, buf.Bytes())
}

                                         
func (c *Coordinator) waitForShares(ctx context.Context, sess *roastsession.Session) bool {
	deadline := time.After(c.config.ShareCollectTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			return false
		case <-ticker.C:
			if sess.HasEnoughShares() {
				return true
			}
		}
	}
}

                                                             
func (c *Coordinator) retryWithNewSet(sess *roastsession.Session) bool {
	ok := sess.ResetForRetry(c.config.MaxRetries)
	if ok {
		logs.Info("[Coordinator] session %s retry %d", sess.JobID, sess.RetryCount)
	}
	return ok
}
