package workers

import (
	"context"
	"dex/frost/protocol"
	"dex/keys"
	"dex/pb"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
)

func (w *TransitionWorker) verifyAndComplain(ctx context.Context, session *DKGSession) error {
	w.Logger.Debug("[TransitionWorker] verifyAndComplain session=%s", session.SessionID)

	dkgExec, err := w.cryptoFactory.NewDKGExecutor(int32(session.SignAlgo))
	if err != nil {
		return err
	}

	commitments, err := w.loadDKGCommitments(session)
	if err != nil {
		return err
	}

	if w.localIdentity == nil {
		return fmt.Errorf("local identity not configured for share decryption")
	}

	for _, dealerID := range session.Committee {
		if dealerID == w.localAddress {
			continue             
		}

		                                                                          
		                                                                        
		                                          
		shareKey := keys.KeyFrostVaultDkgShare(
			session.Chain, session.VaultID, session.EpochID, session.SessionID,
			dealerID, w.localAddress,
		)
		shareData, exists, err := w.stateReader.Get(shareKey)
		if err != nil {
			return fmt.Errorf("load dkg share from dealer %s: %w", dealerID, err)
		}
		if !exists || len(shareData) == 0 {
			w.Logger.Warn("[TransitionWorker] session %s has no on-chain share from dealer %s; waiting for deterministic VM timeout handling",
				session.SessionID, dealerID)
			continue
		}

		var share pb.FrostVaultDkgShare
		if err := proto.Unmarshal(shareData, &share); err != nil {
			                                                                        
			                                                                     
			                                                     
			w.Logger.Error("[DKG] verifyAndComplain: invalid on-chain share record from %s: %v", dealerID, err)
			continue
		}
		if share.DealerId != dealerID || share.ReceiverId != w.localAddress {
			w.Logger.Error("[DKG] verifyAndComplain: share key/payload mismatch: expected dealer=%s receiver=%s, got dealer=%s receiver=%s",
				dealerID, w.localAddress, share.DealerId, share.ReceiverId)
			continue
		}

		commitment, hasCommitment := commitments[dealerID]
		if !hasCommitment || commitment == nil {
			                                                                       
			                                                                     
			w.Logger.Warn("[DKG] verifyAndComplain: missing commitment for dealer=%s", dealerID)
			continue
		}

		invalidShare := false
		plain, err := w.localIdentity.DecryptECIES(share.Ciphertext)
		if err != nil {
			w.Logger.Warn("[DKG] verifyAndComplain: decrypt share from %s failed: %v", share.DealerId, err)
			invalidShare = true
		} else {
			if ok := dkgExec.VerifyShare(plain, commitment.CommitmentPoints, 0, session.MyIndex); !ok {
				w.Logger.Warn("[DKG] verifyAndComplain: share verification failed for dealer=%s", dealerID)
				invalidShare = true
			}
		}

		if !invalidShare {
			continue
		}

		w.Logger.Warn("[TransitionWorker] session %s has an invalid on-chain share from dealer %s, submitting complaint...", session.SessionID, dealerID)
		tx := &pb.FrostVaultDkgComplaintTx{
			Base:       w.newBaseMessage(),
			Chain:      session.Chain,
			VaultId:    session.VaultID,
			EpochId:    session.EpochID,
			DealerId:   dealerID,
			ReceiverId: w.localAddress,
			Bond:       protocol.MinDkgComplaintBond,
			SessionId:  session.SessionID,
		}
		w.signAndSetTxID(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgComplaintTx{FrostVaultDkgComplaintTx: tx}})

		if err := w.txSubmitter.SubmitDkgComplaintTx(ctx, tx); err != nil {
			w.Logger.Error("[TransitionWorker] submit complaint for dealer %s failed: %v", dealerID, err)
		}
	}

	return nil
}

                                                                                                   
func (w *TransitionWorker) monitorPendingComplaints(ctx context.Context, session *DKGSession) {
	if w.localShareStore == nil {
		return
	}
	if !w.dkgRevealResponseStillNeeded(session) {
		return
	}
	w.triggerPendingComplaintScan(ctx, session)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !w.dkgRevealResponseStillNeeded(session) {
				return
			}
			w.triggerPendingComplaintScan(ctx, session)
		}
	}
}

                                                                               
                                                                                
                                                                                
func (w *TransitionWorker) triggerPendingComplaintScan(ctx context.Context, session *DKGSession) {
	if w == nil || session == nil || w.localShareStore == nil {
		return
	}
	sessionID := session.SessionID
	w.mu.Lock()
	if w.complaintScans == nil {
		w.complaintScans = make(map[string]bool)
	}
	if w.complaintPending == nil {
		w.complaintPending = make(map[string]bool)
	}
	if w.complaintScans[sessionID] {
		w.complaintPending[sessionID] = true
		w.mu.Unlock()
		return
	}
	w.complaintScans[sessionID] = true
	w.mu.Unlock()

	go func() {
		for {
			if w.dkgRevealResponseStillNeeded(session) {
				if err := w.respondToPendingComplaints(ctx, session); err != nil && w.Logger != nil {
					w.Logger.Warn("[TransitionWorker] pending complaint scan failed for session %s: %v", sessionID, err)
				}
			}

			w.mu.Lock()
			if w.complaintPending[sessionID] {
				delete(w.complaintPending, sessionID)
				w.mu.Unlock()
				continue
			}
			delete(w.complaintScans, sessionID)
			delete(w.complaintPending, sessionID)
			w.mu.Unlock()
			return
		}
	}()
}

func (w *TransitionWorker) dkgRevealResponseStillNeeded(session *DKGSession) bool {
	if session == nil {
		return false
	}
	if session.DisputeDeadline > 0 && w.GetCurrentHeight() > session.DisputeDeadline {
		return false
	}
	data, exists, err := w.stateReader.Get(keys.KeyFrostVaultTransition(session.Chain, session.VaultID, session.EpochID))
	if err != nil || !exists || len(data) == 0 {
		                                                                        
		                                                                  
		return true
	}
	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(data, &transition); err != nil {
		return true
	}
	if transition.DkgSessionId != session.SessionID {
		return false
	}
	return transition.DkgStatus != "FAILED" && transition.DkgStatus != "KEY_READY"
}

func (w *TransitionWorker) respondToPendingComplaints(ctx context.Context, session *DKGSession) error {
	                                                                                                               
	prefix := keys.KeyFrostVaultDkgComplaintPrefix(session.Chain, session.VaultID, session.EpochID, session.SessionID) + w.localAddress + "_"
	return w.stateReader.Scan(prefix, func(k string, v []byte) bool {
		var c pb.FrostVaultDkgComplaint
		if err := proto.Unmarshal(v, &c); err != nil {
			w.Logger.Warn("[TransitionWorker] skip malformed DKG complaint key=%s: %v", k, err)
			return true
		}
		if c.Status != "PENDING" {
			return true
		}

		w.Logger.Info("[TransitionWorker] Found PENDING complaint from %s against us for session %s. Attempting to Reveal.", c.ReceiverId, session.SessionID)

		receiverIndex := -1
		for i, member := range session.Committee {
			if member == c.ReceiverId {
				receiverIndex = i + 1
				break
			}
		}
		if receiverIndex == -1 {
			w.Logger.Warn("[TransitionWorker] ReceiverId %s not found in committee", c.ReceiverId)
			return true
		}

		share, encRand, err := w.localShareStore.LoadDkgRevealMaterial(session.Chain, session.VaultID, session.EpochID, session.SessionID, receiverIndex)
		if err != nil {
			w.Logger.Error("[TransitionWorker] Failed to load reveal material for receiver %s: %v", c.ReceiverId, err)
			return true
		}
		if len(share) == 0 || len(encRand) == 0 {
			w.Logger.Warn("[TransitionWorker] reveal material unavailable for receiver %s session %s; refusing empty RevealTx", c.ReceiverId, session.SessionID)
			return true
		}

		tx := &pb.FrostVaultDkgRevealTx{
			Base:       w.newBaseMessage(),
			Chain:      session.Chain,
			VaultId:    session.VaultID,
			EpochId:    session.EpochID,
			DealerId:   w.localAddress,
			ReceiverId: c.ReceiverId,
			Share:      share,
			EncRand:    encRand,
			SessionId:  session.SessionID,
		}
		w.signAndSetTxID(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgRevealTx{FrostVaultDkgRevealTx: tx}})

		if err := w.txSubmitter.SubmitDkgRevealTx(ctx, tx); err != nil {
			w.Logger.Error("[TransitionWorker] SubmitDkgRevealTx failed: %v", err)
		} else {
			w.Logger.Info("[TransitionWorker] Successfully submitted RevealTx for receiver %s", c.ReceiverId)
		}
		return true
	})
}
