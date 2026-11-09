package workers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"dex/frost/security"
	"dex/keys"
	"dex/pb"
	"dex/txverify"
	"fmt"
	"math/big"
	"strings"

	"google.golang.org/protobuf/proto"
)

func (w *TransitionWorker) submitCommitment(ctx context.Context, session *DKGSession) error {
	w.Logger.Debug("[TransitionWorker] submitCommitment session=%s", session.SessionID)

	                             
	dkgExec, err := w.cryptoFactory.NewDKGExecutor(int32(session.SignAlgo))
	if err != nil {
		return fmt.Errorf("create dkg executor: %w", err)
	}

	                                                                                       
	                                               
	poly, err := dkgExec.GeneratePolynomial(session.Threshold)
	if err != nil {
		return fmt.Errorf("generate polynomial: %w", err)
	}
	session.Polynomial = poly

	                                                         
	commitmentPoints := dkgExec.ComputeCommitments(poly)

	                                                            
	ai0 := commitmentPoints[0]

	                                                                                     
	session.LocalShares = make(map[int][]byte)
	session.EncRands = make(map[int][]byte)
	for idx := 1; idx <= len(session.Committee); idx++ {
		                           
		shareBytes := dkgExec.EvaluateShare(poly, idx)
		session.LocalShares[idx] = shareBytes

		                                                                                             
		encRand := make([]byte, 32)
		if _, err := rand.Read(encRand); err != nil {
			return fmt.Errorf("read rand: %w", err)
		}
		session.EncRands[idx] = encRand

		if w.localShareStore != nil {
			if err := w.localShareStore.SaveDkgRevealMaterial(session.Chain, session.VaultID, session.EpochID, session.SessionID, idx, shareBytes, encRand); err != nil {
				return fmt.Errorf("persist dkg reveal material for session %s index %d: %w", session.SessionID, idx, err)
			}
		}
	}

	tx := &pb.FrostVaultDkgCommitTx{
		Base:             w.newBaseMessage(),
		Chain:            session.Chain,
		VaultId:          session.VaultID,
		EpochId:          session.EpochID,
		SignAlgo:         session.SignAlgo,
		CommitmentPoints: commitmentPoints,
		AI0:              ai0,
		SessionId:        session.SessionID,
	}

	w.signAndSetTxID(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgCommitTx{FrostVaultDkgCommitTx: tx}})

	if err := w.txSubmitter.SubmitDkgCommitTx(ctx, tx); err != nil {
		return fmt.Errorf("submit commitment: %w", err)
	}

	session.Phase = "SHARING"
	return nil
}

                                    
func (w *TransitionWorker) submitShares(ctx context.Context, session *DKGSession) error {
	w.Logger.Debug("[TransitionWorker] submitShares session=%s", session.SessionID)

	                                                                  
	for idx, receiverID := range session.Committee {
		receiverIndex := idx + 1                 
		if receiverID == w.localAddress {
			                                                                   
			                                                                    
			              
			continue
		}

		receiverPubKey, err := w.getReceiverPubKey(session, receiverID)
		if err != nil {
			w.Logger.Warn("[TransitionWorker] cannot get pubkey for %s: %v", receiverID, err)
			continue
		}

		                                                                
		shareBytes := session.LocalShares[receiverIndex]
		encRand := session.EncRands[receiverIndex]
		w.Logger.Debug("[TransitionWorker] dkg share mapping session=%s chain=%s vault=%d epoch=%d dealer=%s dealer_index=%d receiver=%s receiver_index=%d share_fp=%s rand_fp=%s",
			session.SessionID, session.Chain, session.VaultID, session.EpochID, w.localAddress, session.MyIndex, receiverID, receiverIndex, shareFingerprintForLog(shareBytes), shareFingerprintForLog(encRand))

		               
		ciphertext, err := security.ECIESEncrypt(receiverPubKey, shareBytes, encRand)
		if err != nil {
			return fmt.Errorf("encrypt share for %s: %w", receiverID, err)
		}

		tx := &pb.FrostVaultDkgShareTx{
			Base:       w.newBaseMessage(),
			Chain:      session.Chain,
			VaultId:    session.VaultID,
			EpochId:    session.EpochID,
			DealerId:   w.localAddress,
			ReceiverId: receiverID,
			Ciphertext: ciphertext,
			SessionId:  session.SessionID,
		}

		w.signAndSetTxID(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgShareTx{FrostVaultDkgShareTx: tx}})

		if err := w.txSubmitter.SubmitDkgShareTx(ctx, tx); err != nil {
			return fmt.Errorf("submit share to %s: %w", receiverID, err)
		}
	}

	session.Phase = "RESOLVING"
	return nil
}

func (w *TransitionWorker) getReceiverPubKey(session *DKGSession, receiverID string) ([]byte, error) {
	if w.pubKeyProvider != nil {
		pub, err := w.pubKeyProvider.GetMinerSigningPubKey(receiverID, session.SignAlgo)
		if err == nil && len(pub) > 0 {
			out := make([]byte, len(pub))
			copy(out, pub)
			return out, nil
		}
	}
	if session != nil && session.CommitteePubKeys != nil {
		if pub, ok := session.CommitteePubKeys[receiverID]; ok && len(pub) > 0 {
			out := make([]byte, len(pub))
			copy(out, pub)
			return out, nil
		}
	}
	return nil, fmt.Errorf("pubkey not found")
}

                                                               
func (w *TransitionWorker) generateKey(ctx context.Context, session *DKGSession) error {
	w.Logger.Debug("[TransitionWorker] generateKey session=%s", session.SessionID)

	                             
	dkgExec, err := w.cryptoFactory.NewDKGExecutor(int32(session.SignAlgo))
	if err != nil {
		return fmt.Errorf("create dkg executor: %w", err)
	}

	                                                                                                                                  
	myShareBytes := dkgExec.EvaluateShare(session.Polynomial, session.MyIndex)
	w.Logger.Debug("[TransitionWorker] dkg local polynomial share session=%s chain=%s vault=%d epoch=%d node=%s my_index=%d share_fp=%s",
		session.SessionID, session.Chain, session.VaultID, session.EpochID, w.localAddress, session.MyIndex, shareFingerprintForLog(myShareBytes))

	                                                                                                
	decryptedShares, err := w.collectAndVerifyShares(ctx, dkgExec, session)
	if err != nil {
		return fmt.Errorf("collect shares: %w", err)
	}
	for _, dealerID := range session.Committee {
		if dealerID == w.localAddress {
			continue
		}
		shareBytes := decryptedShares[dealerID]
		w.Logger.Debug("[TransitionWorker] dkg decrypted share session=%s chain=%s vault=%d epoch=%d dealer=%s receiver=%s receiver_index=%d share_fp=%s",
			session.SessionID, session.Chain, session.VaultID, session.EpochID, dealerID, w.localAddress, session.MyIndex, shareFingerprintForLog(shareBytes))
	}

	                                                  
	allShares := make([][]byte, 0, 1+len(decryptedShares))
	allShares = append(allShares, myShareBytes)
	for _, s := range decryptedShares {
		allShares = append(allShares, s)
	}
	for idx, s := range allShares {
		w.Logger.Debug("[TransitionWorker] dkg aggregate INPUT session=%s chain=%s vault=%d epoch=%d node=%s input_index=%d share_fp=%s",
			session.SessionID, session.Chain, session.VaultID, session.EpochID, w.localAddress, idx, shareFingerprintForLog(s))
	}
	aggShare := dkgExec.AggregateShares(allShares)
	w.Logger.Debug("[TransitionWorker] dkg aggregate share input summary session=%s chain=%s vault=%d epoch=%d input_count=%d committee=%s",
		session.SessionID, session.Chain, session.VaultID, session.EpochID, len(allShares), committeeMembersForLog(session.Committee))

	session.LocalShare = new(big.Int).SetBytes(aggShare)
	session.LocalShareBytes = aggShare
	w.Logger.Debug("[TransitionWorker] dkg aggregate share result session=%s chain=%s vault=%d epoch=%d node=%s my_index=%d agg_share_fp=%s",
		session.SessionID, session.Chain, session.VaultID, session.EpochID, w.localAddress, session.MyIndex, shareFingerprintForLog(aggShare))
	if w.localShareStore != nil {
		shareCopy := make([]byte, len(aggShare))
		copy(shareCopy, aggShare)
		if err := w.localShareStore.SavePendingLocalShare(session.Chain, session.VaultID, session.EpochID, session.SessionID, shareCopy); err != nil {
			return fmt.Errorf("persist pending local share for %s/%d/%d session=%s: %w",
				session.Chain, session.VaultID, session.EpochID, session.SessionID, err)
		}
		w.Logger.Debug("[TransitionWorker] persisted pending aggregated local share session=%s chain=%s vault=%d epoch=%d key=%s share_fp=%s",
			session.SessionID, session.Chain, session.VaultID, session.EpochID,
			keys.KeyFrostPendingLocalShare(session.Chain, session.VaultID, session.EpochID, session.SessionID), shareFingerprintForLog(shareCopy))
	}

	                                                                                
	groupPubkey, err := w.aggregateGroupPubkey(ctx, session)
	if err != nil {
		return fmt.Errorf("aggregate group pubkey: %w", err)
	}
	session.GroupPubkey = groupPubkey
	w.Logger.Debug("[TransitionWorker] dkg group pubkey ready session=%s chain=%s vault=%d epoch=%d group_pubkey_fp=%s",
		session.SessionID, session.Chain, session.VaultID, session.EpochID, shareFingerprintForLog(groupPubkey))

	session.Phase = "KEY_READY"
	                                                                                                                         
	w.Logger.Debug("[TransitionWorker] generateKey completed session=%s local_share_fp=%s group_pubkey_fp=%s",
		session.SessionID, shareFingerprintForLog(session.LocalShareBytes), shareFingerprintForLog(session.GroupPubkey))
	return nil
}

                                                                                                                                            
func (w *TransitionWorker) collectAndVerifyShares(ctx context.Context, dkgExec DKGExecutor, session *DKGSession) (map[string][]byte, error) {
	result := make(map[string][]byte)                     

	commitments, err := w.loadDKGCommitments(session)
	if err != nil {
		return nil, err
	}

	                                                                       
	sharePrefix := keys.KeyFrostVaultDkgSharePrefix(session.Chain, session.VaultID, session.EpochID, session.SessionID)
	w.Logger.Debug("[TransitionWorker] collect shares context session=%s chain=%s vault=%d epoch=%d receiver=%s receiver_index=%d committee=%s share_prefix=%s",
		session.SessionID, session.Chain, session.VaultID, session.EpochID, w.localAddress, session.MyIndex, committeeMembersForLog(session.Committee), sharePrefix)

	                                                                         
	if w.localIdentity == nil {
		return nil, fmt.Errorf("local identity not configured for share decryption")
	}

	err = w.stateReader.Scan(sharePrefix, func(k string, v []byte) bool {
		                                                 
		var share pb.FrostVaultDkgShare
		if err := proto.Unmarshal(v, &share); err != nil {
			w.Logger.Warn("[DKG] skip invalid share record: %v", err)
			return true
		}
		                                                    
		if share.ReceiverId != w.localAddress {
			return true
		}
		                                                                                                               
		if share.DealerId == w.localAddress {
			return true
		}
		w.Logger.Debug("[TransitionWorker] collect share candidate session=%s chain=%s vault=%d epoch=%d dealer=%s receiver=%s key=%s ciphertext_len=%d",
			session.SessionID, session.Chain, session.VaultID, session.EpochID, share.DealerId, share.ReceiverId, k, len(share.Ciphertext))

		                     
		plain, err := w.localIdentity.DecryptECIES(share.Ciphertext)
		if err != nil {
			w.Logger.Warn("[DKG] decrypt share from %s failed: %v", share.DealerId, err)
			return true
		}

		commitment, exists := commitments[share.DealerId]
		if !exists || commitment == nil {
			w.Logger.Warn("[DKG] missing commitment for dealer=%s", share.DealerId)
			return true
		}

		                                             
		if ok := dkgExec.VerifyShare(plain, commitment.CommitmentPoints, 0, session.MyIndex); !ok {
			w.Logger.Warn("[DKG] share verification failed for dealer=%s", share.DealerId)
			return true
		}

		         
		if prev, exists := result[share.DealerId]; exists {
			w.Logger.Warn("[TransitionWorker] duplicate share from dealer session=%s chain=%s vault=%d epoch=%d dealer=%s prev_fp=%s new_fp=%s",
				session.SessionID, session.Chain, session.VaultID, session.EpochID, share.DealerId, shareFingerprintForLog(prev), shareFingerprintForLog(plain))
		}
		result[share.DealerId] = plain
		w.Logger.Info("[TransitionWorker] verified decrypted share session=%s chain=%s vault=%d epoch=%d dealer=%s receiver=%s receiver_index=%d share_fp=%s",
			session.SessionID, session.Chain, session.VaultID, session.EpochID, share.DealerId, w.localAddress, session.MyIndex, shareFingerprintForLog(plain))
		return true
	})
	if err != nil {
		return nil, err
	}

	missing := make([]string, 0)
	for _, dealerID := range session.Committee {
		if dealerID == w.localAddress {
			continue
		}
		if _, ok := result[dealerID]; !ok {
			missing = append(missing, dealerID)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing valid shares from dealers: %v", missing)
	}
	w.Logger.Debug("[TransitionWorker] collect shares completed session=%s chain=%s vault=%d epoch=%d received=%d expected=%d",
		session.SessionID, session.Chain, session.VaultID, session.EpochID, len(result), len(session.Committee)-1)

	return result, nil
}

func shareFingerprintForLog(data []byte) string {
	if len(data) == 0 {
		return "len=0"
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("len=%d,sha256_8=%x", len(data), sum[:8])
}

func committeeMembersForLog(committee []string) string {
	if len(committee) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(committee))
	for idx, member := range committee {
		parts = append(parts, fmt.Sprintf("%d:%s", idx+1, member))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (w *TransitionWorker) loadDKGCommitments(session *DKGSession) (map[string]*pb.FrostVaultDkgCommitment, error) {
	commitPrefix := keys.KeyFrostVaultDkgCommitPrefix(session.Chain, session.VaultID, session.EpochID, session.SessionID)
	commitments := make(map[string]*pb.FrostVaultDkgCommitment, len(session.Committee))

	err := w.stateReader.Scan(commitPrefix, func(k string, v []byte) bool {
		dealerID := strings.TrimPrefix(k, commitPrefix)
		if dealerID == "" {
			return true
		}
		var commit pb.FrostVaultDkgCommitment
		if err := proto.Unmarshal(v, &commit); err != nil {
			w.Logger.Warn("[DKG] skip invalid commitment record for dealer=%s: %v", dealerID, err)
			return true
		}
		commitments[dealerID] = &commit
		return true
	})
	if err != nil {
		return nil, err
	}
	return commitments, nil
}

                                                                                          
func (w *TransitionWorker) aggregateGroupPubkey(ctx context.Context, session *DKGSession) ([]byte, error) {
	dkgExec, err := w.cryptoFactory.NewDKGExecutor(int32(session.SignAlgo))
	if err != nil {
		return nil, err
	}
	commitments, err := w.loadDKGCommitments(session)
	if err != nil {
		return nil, err
	}
	w.Logger.Debug("[TransitionWorker] aggregate group pubkey context session=%s chain=%s vault=%d epoch=%d committee=%s commitment_count=%d",
		session.SessionID, session.Chain, session.VaultID, session.EpochID, committeeMembersForLog(session.Committee), len(commitments))
	ai0Points := make([][]byte, 0, len(session.Committee))
	missing := make([]string, 0)
	for _, dealerID := range session.Committee {
		commit, ok := commitments[dealerID]
		if !ok || commit == nil || len(commit.AI0) == 0 {
			missing = append(missing, dealerID)
			continue
		}
		ai0Points = append(ai0Points, commit.AI0)
		w.Logger.Debug("[TransitionWorker] aggregate group pubkey contributor session=%s dealer=%s ai0_fp=%s",
			session.SessionID, dealerID, shareFingerprintForLog(commit.AI0))
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing DKG commitments (AI0) from dealers: %v", missing)
	}
	if len(ai0Points) == 0 {
		return nil, fmt.Errorf("no AI0 commitments found")
	}
	groupPubkey := dkgExec.ComputeGroupPubkey(ai0Points)
	w.Logger.Debug("[TransitionWorker] aggregate group pubkey result session=%s group_pubkey_fp=%s", session.SessionID, shareFingerprintForLog(groupPubkey))
	return groupPubkey, nil
}

                                      
func (w *TransitionWorker) submitValidation(ctx context.Context, session *DKGSession) error {
	w.Logger.Debug("[TransitionWorker] submitValidation session=%s", session.SessionID)

	                                                                                 
	msgHash := computeValidationMsgHash(session.Chain, session.VaultID, session.EpochID, session.SignAlgo, session.GroupPubkey)

	                             
	dkgExec, err := w.cryptoFactory.NewDKGExecutor(int32(session.SignAlgo))
	if err != nil {
		return fmt.Errorf("create dkg executor: %w", err)
	}

	                                                       
	signature, err := dkgExec.SchnorrSign(session.LocalShare, msgHash)
	if err != nil {
		return fmt.Errorf("schnorr sign: %w", err)
	}

	tx := &pb.FrostVaultDkgValidationSignedTx{
		Base:           w.newBaseMessage(),
		Chain:          session.Chain,
		VaultId:        session.VaultID,
		EpochId:        session.EpochID,
		MsgHash:        msgHash,
		NewGroupPubkey: session.GroupPubkey,
		Signature:      signature,
		SessionId:      session.SessionID,
	}

	if w.localIdentity == nil {
		return fmt.Errorf("local identity not configured for validation ack")
	}
	ackSignature, err := w.localIdentity.Sign(msgHash)
	if err != nil {
		return fmt.Errorf("sign validation ack: %w", err)
	}
	tx.Signature = ackSignature

	w.signAndSetTxID(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultDkgValidationSignedTx{FrostVaultDkgValidationSignedTx: tx}})

	if err := w.txSubmitter.SubmitDkgValidationSignedTx(ctx, tx); err != nil {
		return fmt.Errorf("submit validation: %w", err)
	}

	return nil
}

                                                                                                             
func computeValidationMsgHash(chain string, vaultID uint32, epochID uint64, signAlgo pb.SignAlgo, groupPubkey []byte) []byte {
	h := sha256.New()
	h.Write([]byte("frost_vault_dkg_validation"))
	h.Write([]byte(chain))
	h.Write([]byte{byte(vaultID >> 24), byte(vaultID >> 16), byte(vaultID >> 8), byte(vaultID)})
	h.Write([]byte{byte(epochID >> 56), byte(epochID >> 48), byte(epochID >> 40), byte(epochID >> 32),
		byte(epochID >> 24), byte(epochID >> 16), byte(epochID >> 8), byte(epochID)})
	h.Write([]byte{byte(signAlgo)})
	h.Write(groupPubkey)
	return h.Sum(nil)
}

func (w *TransitionWorker) newBaseMessage() *pb.BaseMessage {
	                                                                     
	var currentNonce uint64
	accountKey := fmt.Sprintf("v1_account_%s", w.localAddress)
	if data, exists, err := w.stateReader.Get(accountKey); err == nil && exists {
		var acc pb.Account
		if err := proto.Unmarshal(data, &acc); err == nil {
			currentNonce = acc.Nonce
		}
	}

	                                                                                                                                  
	w.mu.Lock()
	defer w.mu.Unlock()

	nonce := currentNonce
	if w.issuedNonce && nonce <= w.lastIssuedNonce {
		nonce = w.lastIssuedNonce + 1
	}
	w.lastIssuedNonce = nonce
	w.issuedNonce = true

	if w.Logger != nil {
		w.Logger.Debug("[TransitionWorker] newBaseMessage: sender=%s, base_nonce=%d, final_nonce=%d",
			w.localAddress, currentNonce, nonce)
	}

	base := &pb.BaseMessage{
		FromAddress: w.localAddress,
		Nonce:       nonce,
		Status:      pb.Status_PENDING,
		Fee:         "1000",
	}

	return base
}

                                                                                       
func (w *TransitionWorker) signAndSetTxID(anyTx *pb.AnyTx) {
	if _, err := txverify.SignWithSigner(anyTx, w.localIdentity); err != nil {
		if w.Logger != nil {
			w.Logger.Warn("[TransitionWorker] sign tx failed: %v", err)
		}
	}
}

                                          
