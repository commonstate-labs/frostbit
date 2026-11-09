                                          
                                                                                                              

package roast

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"dex/frost/core/curve"
	"dex/frost/runtime/session"
	"dex/logs"
	"dex/pb"
)

                                       
func (p *Participant) HandleSignRequest(env *FrostEnvelope) error {
	return p.HandleRoastSignRequest(FromFrostEnvelope(env))
}

                                                                    
func (p *Participant) HandleRoastSignRequest(env *Envelope) error {
	if env == nil {
		return ErrInvalidSignRequest
	}
	logs.Debug("[Participant] received sign request from %s for job %s", env.From, env.SessionID)

	if err := p.verifyCoordinator(env); err != nil {
		logs.Warn("[Participant] rejected sign request from non-current coordinator: %v", err)
		return err
	}

	                                                                   
	                                                                         
	                                                                          
	p.mu.RLock()
	defer p.mu.RUnlock()
	sess, exists := p.sessions[env.SessionID]
	if !exists {
		return ErrSessionNotFound
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()

	if env.Chain != sess.Chain || env.VaultID != sess.VaultID || env.Epoch != sess.KeyEpoch || env.SignAlgo != sess.SignAlgo {
		return errors.New("sign request context differs from nonce request")
	}
	if env.Round < sess.Round {
		logs.Debug("[Participant] ignore stale sign request node=%s job=%s got_round=%d current_round=%d",
			p.nodeID, env.SessionID, env.Round, sess.Round)
		return nil
	}
	if env.Round > sess.Round {
		return fmt.Errorf("%w: current=%d got=%d", ErrFutureRoastRound, sess.Round, env.Round)
	}
	decodedMsgs, decodedNonces, hasStructuredPayload := decodeRoastRequestPayload(env.Payload)
	if hasStructuredPayload && len(decodedMsgs) > 0 && !sameMessages(sess.Messages, decodedMsgs) {
		return errors.New("sign request messages differ from nonce request")
	}
	requestNonces := env.Payload
	if hasStructuredPayload {
		requestNonces = decodedNonces
	}

	if sess.State == ParticipantStateShareGenerated || sess.State == ParticipantStateComplete {
		if !bytes.Equal(sess.AggregatedNonces, requestNonces) {
			return errors.New("sign request aggregate nonces differ from generated share context")
		}
		if len(sess.GeneratedShares) == 0 {
			logs.Debug("[Participant] duplicate sign request ignored without cached shares node=%s job=%s state=%s",
				p.nodeID, env.SessionID, sess.State.String())
			return nil
		}
		logs.Debug("[Participant] duplicate sign request, resend cached shares node=%s job=%s state=%s",
			p.nodeID, env.SessionID, sess.State.String())
		return p.sendSignatureShares(sess, env.From, cloneMessages(sess.GeneratedShares))
	}

	if sess.State == ParticipantStateInit {
		return fmt.Errorf("%w: %v", ErrRoastStateNotReady, session.ErrInvalidState)
	}
	if sess.State != ParticipantStateNonceGenerated {
		return session.ErrInvalidState
	}

	                                                                                     
	if hasStructuredPayload {
		sess.AggregatedNonces = requestNonces
	} else {
		sess.AggregatedNonces = env.Payload
	}

	shares, err := p.computeSignatureShares(sess)
	if err != nil {
		sess.State = ParticipantStateFailed
		return err
	}

	sess.GeneratedShares = cloneMessages(shares)
	sess.State = ParticipantStateShareGenerated

	return p.sendSignatureShares(sess, env.From, shares)
}

                                                                                  
func (p *Participant) computeSignatureShares(sess *ParticipantSession) ([][]byte, error) {
	if len(sess.Messages) == 0 {
		return nil, errors.New("missing signing messages in participant session")
	}
	if len(sess.HidingNonces) != len(sess.Messages) {
		return nil, fmt.Errorf("nonce/message task count mismatch: nonces=%d messages=%d", len(sess.HidingNonces), len(sess.Messages))
	}

	numTasks := len(sess.HidingNonces)
	shares := make([][]byte, numTasks)
	logs.Debug("[Participant] compute signature shares start node=%s job=%s chain=%s vault=%d epoch=%d my_index=%d task_count=%d local_share_fp=%s",
		p.nodeID, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, sess.MyIndex, numTasks, shareFingerprintForLog(sess.MyShare))
	if p.vaultProvider == nil {
		return nil, errors.New("vault provider is required for signing")
	}
	if signingCommittee, err := p.vaultProvider.VaultCommittee(sess.Chain, sess.VaultID, sess.KeyEpoch); err != nil {
		logs.Warn("[Participant] failed to load signing committee snapshot node=%s job=%s chain=%s vault=%d epoch=%d err=%v",
			p.nodeID, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, err)
	} else {
		logs.Debug("[Participant] signing committee snapshot node=%s job=%s chain=%s vault=%d epoch=%d committee=%s my_index=%d",
			p.nodeID, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, committeeSummaryForLog(signingCommittee), sess.MyIndex)
	}

	roastExec, err := p.cryptoFactory.NewROASTExecutor(int32(sess.SignAlgo))
	if err != nil {
		return nil, err
	}

	pubBytes, err := p.vaultProvider.VaultGroupPubkey(sess.Chain, sess.VaultID, sess.KeyEpoch)
	if err != nil {
		return nil, fmt.Errorf("failed to load group pubkey for signing chain=%s vault=%d epoch=%d: %w", sess.Chain, sess.VaultID, sess.KeyEpoch, err)
	}
	if len(pubBytes) < 32 {
		return nil, fmt.Errorf("invalid group pubkey length for signing: %d", len(pubBytes))
	}
	groupPubBytes := pubBytes
	logs.Info("[Participant] signing context miner=%s job=%s chain=%s vault=%d epoch=%d sign_algo=%s tasks=%d aggregated_pubkey=%s",
		p.nodeID, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, sess.SignAlgo.String(), numTasks, hex.EncodeToString(groupPubBytes))

	allNonces := parseAggregatedNonces(sess.AggregatedNonces, numTasks, sess.SignAlgo)

	for i := 0; i < numTasks; i++ {
		msg := sess.Messages[i]
		if len(msg) == 0 {
			return nil, fmt.Errorf("empty signing message for task %d", i)
		}
		logs.Info("[Participant] signing input miner=%s job=%s chain=%s vault=%d epoch=%d task=%d message=%s",
			p.nodeID, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, i, hex.EncodeToString(msg))

		                                                                       
		                                                              
		hidingNonceBytes := sess.HidingNonces[i]
		bindingNonceBytes := sess.BindingNonces[i]
		hidingContext := nonceBindingContext(sess.JobID, i, nonceRoleHiding, msg)
		if !p.sessionStore.ValidateNonceForMessage(hidingNonceBytes, hidingContext) {
			return nil, fmt.Errorf("hiding nonce for task %d is not bound to signing context (possible replay attack)", i)
		}
		bindingContext := nonceBindingContext(sess.JobID, i, nonceRoleBinding, msg)
		if !p.sessionStore.ValidateNonceForMessage(bindingNonceBytes, bindingContext) {
			return nil, fmt.Errorf("binding nonce for task %d is not bound to signing context (possible replay attack)", i)
		}

		hidingNonce := new(big.Int).SetBytes(hidingNonceBytes)
		bindingNonce := new(big.Int).SetBytes(bindingNonceBytes)

		hidingPt := roastExec.ScalarBaseMult(hidingNonce)
		bindingPt := roastExec.ScalarBaseMult(bindingNonce)

		taskNonces := allNonces[i]
		if len(taskNonces) == 0 {
			taskNonces = []NonceInput{{
				SignerID:     sess.MyIndex + 1,
				HidingNonce:  hidingNonce,
				BindingNonce: bindingNonce,
				HidingPoint:  hidingPt,
				BindingPoint: bindingPt,
			}}
		}

		signerIDs := make([]int, len(taskNonces))
		for j, n := range taskNonces {
			signerIDs[j] = n.SignerID
		}
		selfSignerID, found := findSignerIDByNoncePoints(taskNonces, hidingPt, bindingPt)
		if !found {
			return nil, fmt.Errorf("self nonce missing from aggregated nonces for task %d (job=%s my_index=%d)",
				i, sess.JobID, sess.MyIndex+1)
		}
		if selfSignerID != sess.MyIndex+1 {
			logs.Warn("[Participant] signer id remapped by nonce match job=%s task=%d my_index=%d resolved_signer_id=%d",
				sess.JobID, i, sess.MyIndex+1, selfSignerID)
		}

		rho := roastExec.ComputeBindingCoefficient(selfSignerID, msg, taskNonces)
		lambdas := roastExec.ComputeLagrangeCoefficients(signerIDs)
		lambda := lambdas[selfSignerID]
		if lambda == nil {
			lambda = big.NewInt(1)
		}
		logs.Info("[Participant] signer-share mapping node=%s job=%s task=%d my_index=%d self_signer_id=%d signer_ids=%v lambda_self=%s lambdas=%s",
			p.nodeID, sess.JobID, i, sess.MyIndex+1, selfSignerID, signerIDs, bigIntHexForLog(lambda), lagrangeMapForLog(signerIDs, lambdas))

		R, err := roastExec.ComputeGroupCommitment(taskNonces, msg)
		if err != nil {
			return nil, err
		}

		taskTweak := taskTweakAt(sess.Tweaks, i)
		taskShare, shareNegated, tweakApplied, err := deriveTaskSecretShare(sess.SignAlgo, groupPubBytes, sess.MyShare, taskTweak)
		if err != nil {
			return nil, fmt.Errorf("failed to derive task secret share for task %d: %w", i, err)
		}
		if shareNegated {
			logs.Info("[Participant] normalized secret share for signing pubkey chain=%s vault=%d epoch=%d sign_algo=%s task=%d",
				sess.Chain, sess.VaultID, sess.KeyEpoch, sess.SignAlgo.String(), i)
		}
		if tweakApplied {
			logs.Info("[Participant] applied tweak compensation chain=%s vault=%d epoch=%d task=%d tweak=%s",
				sess.Chain, sess.VaultID, sess.KeyEpoch, i, hex.EncodeToString(taskTweak))
		}
		_, groupPubX, err := deriveSigningPubkeyForTask(sess.SignAlgo, groupPubBytes, taskTweak)
		if err != nil {
			return nil, fmt.Errorf("failed to derive task signing pubkey for task %d: %w", i, err)
		}

		e := roastExec.ComputeChallenge(R, groupPubX, msg)
		rParity := -1
		if R.Y != nil {
			rParity = int(R.Y.Bit(0))
		}
		logs.Info("[Participant][sign-diag] node=%s job=%s task=%d signer_id=%d R_x=%s R_y_parity=%d groupPubX=%s challenge=%s rho=%s",
			p.nodeID, sess.JobID, i, selfSignerID,
			hex.EncodeToString(bigIntFillBytes32(R.X)), rParity,
			hex.EncodeToString(bigIntFillBytes32(groupPubX)),
			hex.EncodeToString(bigIntFillBytes32(e)),
			hex.EncodeToString(bigIntFillBytes32(rho)))

		adjHidingNonce, adjBindingNonce := adjustNoncePairForBIP340(sess.SignAlgo, R, hidingNonce, bindingNonce)
		nonceNegated := adjHidingNonce.Cmp(hidingNonce) != 0
		logs.Debug("[Participant][sign-diag] node=%s job=%s task=%d signer_id=%d nonce_negated=%v share_negated=%v share_fp=%s nonce_count=%d",
			p.nodeID, sess.JobID, i, selfSignerID, nonceNegated, shareNegated,
			shareFingerprintForLog(bigIntFillBytes32(taskShare)), len(taskNonces))
		z := roastExec.ComputePartialSignature(PartialSignParams{
			SignerID:     selfSignerID,
			HidingNonce:  adjHidingNonce,
			BindingNonce: adjBindingNonce,
			SecretShare:  taskShare,
			Rho:          rho,
			Lambda:       lambda,
			Challenge:    e,
		})

		p.logPartialSignDiagnostics(sess, i, selfSignerID, adjHidingNonce, adjBindingNonce, taskShare, rho, lambda, e, z, roastExec)

		shareBytes := make([]byte, 32)
		z.FillBytes(shareBytes)
		shares[i] = shareBytes
		                                                                                   
		logs.Debug("[Participant] signature share generated miner=%s job=%s chain=%s vault=%d epoch=%d task=%d signer_id=%d share_fp=%s",
			p.nodeID, sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, i, selfSignerID, shareFingerprintForLog(shareBytes))
	}

	return shares, nil
}

                                                                                                                   
func (p *Participant) logPartialSignDiagnostics(sess *ParticipantSession, taskIdx, selfSignerID int, adjH, adjB, taskShare, rho, lambda, e, z *big.Int, roastExec ROASTExecutor) {
	order := curve.NewSecp256k1Group().Order()

	noncePart := new(big.Int).Mul(rho, adjB)
	noncePart.Add(noncePart, adjH)
	noncePart.Mod(noncePart, order)

	keyPart := new(big.Int).Mul(lambda, e)
	keyPart.Mul(keyPart, taskShare)
	keyPart.Mod(keyPart, order)

	zCheck := new(big.Int).Add(noncePart, keyPart)
	zCheck.Mod(zCheck, order)
	zOK := zCheck.Cmp(z) == 0

	adjHP := roastExec.ScalarBaseMult(adjH)
	adjBP := roastExec.ScalarBaseMult(adjB)
	rhoB := curve.NewSecp256k1Group().ScalarMultBytes(curve.Point(adjBP), rho.Bytes())
	nonceContribPt := curve.NewSecp256k1Group().Add(curve.Point(adjHP), rhoB)
	keyContribPt := roastExec.ScalarBaseMult(keyPart)
	zG := roastExec.ScalarBaseMult(z)
	expectedPt := curve.NewSecp256k1Group().Add(nonceContribPt, curve.Point(keyContribPt))
	ptMatch := zG.X.Cmp(expectedPt.X) == 0 && zG.Y.Cmp(expectedPt.Y) == 0

	pubSharePt := roastExec.ScalarBaseMult(taskShare)
	pubShareCompressed := curve.NewSecp256k1Group().SerializePoint(curve.Point(pubSharePt))
	weightedPubShare := curve.NewSecp256k1Group().ScalarMultBytes(curve.Point(pubSharePt), lambda.Bytes())
	weightedCompressed := curve.NewSecp256k1Group().SerializePoint(weightedPubShare)

	logs.Info("[Participant][z-verify] node=%s job=%s task=%d signer_id=%d z_check_match=%v pt_match=%v nonce_contrib=(%s) key_contrib=(%s) pubshare_x=%s",
		p.nodeID, sess.JobID, taskIdx, selfSignerID, zOK, ptMatch,
		hex.EncodeToString(bigIntFillBytes32(nonceContribPt.X)),
		hex.EncodeToString(bigIntFillBytes32(curve.Point(keyContribPt).X)),
		hex.EncodeToString(bigIntFillBytes32(curve.Point(pubSharePt).X)))
	logs.Info("[Participant][shamir-check] node=%s job=%s task=%d signer_id=%d lambda=%s pubshare=%s weighted_pubshare=%s",
		p.nodeID, sess.JobID, taskIdx, selfSignerID,
		hex.EncodeToString(bigIntFillBytes32(lambda)),
		hex.EncodeToString(pubShareCompressed),
		hex.EncodeToString(weightedCompressed))
}

func normalizeSecretShareForBIP340(signAlgo pb.SignAlgo, groupPubBytes []byte, share *big.Int) *big.Int {
	if share == nil {
		return nil
	}
	if signAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
		return share
	}
	if len(groupPubBytes) != 33 || groupPubBytes[0] != 0x03 {
		return share
	}

	order := curve.NewSecp256k1Group().Order()
	normalized := new(big.Int).Sub(order, share)
	normalized.Mod(normalized, order)
	return normalized
}

func adjustNoncePairForBIP340(signAlgo pb.SignAlgo, groupCommit CurvePoint, hidingNonce, bindingNonce *big.Int) (*big.Int, *big.Int) {
	if hidingNonce == nil || bindingNonce == nil {
		return hidingNonce, bindingNonce
	}
	if signAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
		return hidingNonce, bindingNonce
	}
	if groupCommit.Y == nil || groupCommit.Y.Bit(0) == 0 {
		return hidingNonce, bindingNonce
	}

	order := curve.NewSecp256k1Group().Order()
	adjHiding := new(big.Int).Sub(order, hidingNonce)
	adjHiding.Mod(adjHiding, order)
	adjBinding := new(big.Int).Sub(order, bindingNonce)
	adjBinding.Mod(adjBinding, order)
	return adjHiding, adjBinding
}

func findSignerIDByNoncePoints(taskNonces []NonceInput, hidingPoint, bindingPoint CurvePoint) (int, bool) {
	for _, n := range taskNonces {
		if sameCurvePoint(n.HidingPoint, hidingPoint) && sameCurvePoint(n.BindingPoint, bindingPoint) {
			return n.SignerID, true
		}
	}
	return 0, false
}

func sameCurvePoint(a, b CurvePoint) bool {
	if a.X == nil || a.Y == nil || b.X == nil || b.Y == nil {
		return false
	}
	return a.X.Cmp(b.X) == 0 && a.Y.Cmp(b.Y) == 0
}

                                                                         
func parseAggregatedNonces(data []byte, numTasks int, signAlgo pb.SignAlgo) [][]NonceInput {
	result := make([][]NonceInput, numTasks)
	for i := range result {
		result[i] = []NonceInput{}
	}

	if len(data) == 0 || numTasks <= 0 {
		return result
	}

	signerIDs, noncePayload, hasHeader := decodeAggregatedNoncePayload(data)
	if !hasHeader {
		noncePayload = data
	}

	pointSize := getPointSize(signAlgo)
	nonceSize := 2 * pointSize
	if nonceSize <= 0 || len(noncePayload)%nonceSize != 0 {
		return result
	}

	var signerCount int
	if hasHeader {
		signerCount = len(signerIDs)
		if signerCount == 0 {
			return result
		}
		expectedLen := signerCount * nonceSize * numTasks
		if len(noncePayload) != expectedLen {
			logs.Warn("[Participant] invalid aggregated nonce payload length: got=%d want=%d signers=%d tasks=%d",
				len(noncePayload), expectedLen, signerCount, numTasks)
			return result
		}
	} else {
		totalPerSigner := nonceSize * numTasks
		if totalPerSigner <= 0 || len(noncePayload)%totalPerSigner != 0 {
			logs.Warn("[Participant] legacy aggregated nonce payload length mismatch: got=%d per_signer=%d tasks=%d",
				len(noncePayload), totalPerSigner, numTasks)
			return result
		}
		signerCount = len(noncePayload) / totalPerSigner
		if signerCount == 0 {
			return result
		}
		signerIDs = make([]int, signerCount)
		for i := 0; i < signerCount; i++ {
			signerIDs[i] = i + 1
		}
	}

	offset := 0
	                                                                        
	for signerPos := 0; signerPos < signerCount && offset < len(noncePayload); signerPos++ {
		signerID := signerIDs[signerPos]
		if signerID <= 0 {
			continue
		}
		for taskIdx := 0; taskIdx < numTasks && offset+nonceSize <= len(noncePayload); taskIdx++ {
			hiding := noncePayload[offset : offset+pointSize]
			binding := noncePayload[offset+pointSize : offset+nonceSize]
			offset += nonceSize

			hidingPoint, err := decodeSerializedPoint(signAlgo, hiding)
			if err != nil {
				logs.Warn("[Participant] failed to decode hiding nonce point (signer=%d task=%d): %v", signerID, taskIdx, err)
				continue
			}
			bindingPoint, err := decodeSerializedPoint(signAlgo, binding)
			if err != nil {
				logs.Warn("[Participant] failed to decode binding nonce point (signer=%d task=%d): %v", signerID, taskIdx, err)
				continue
			}

			result[taskIdx] = append(result[taskIdx], NonceInput{
				SignerID:     signerID,
				HidingPoint:  hidingPoint,
				BindingPoint: bindingPoint,
			})
		}
	}

	return result
}

                                                                
func (p *Participant) sendSignatureShares(sess *ParticipantSession, coordinator NodeID, shares [][]byte) error {
	var payload []byte
	for _, share := range shares {
		payload = append(payload, share...)
	}

	msg := &Envelope{
		SessionID: sess.JobID,
		Kind:      "SigShare",
		From:      p.nodeID,
		Chain:     sess.Chain,
		VaultID:   sess.VaultID,
		SignAlgo:  sess.SignAlgo,
		Epoch:     sess.KeyEpoch,
		Round:     sess.Round,
		Payload:   payload,
	}

	if p.messenger != nil {
		if err := p.messenger.Send(coordinator, toTypesRoastEnvelope(msg)); err != nil {
			return fmt.Errorf("%w: signature shares: %v", ErrRoastResponseSendFailed, err)
		}
	}
	return nil
}
