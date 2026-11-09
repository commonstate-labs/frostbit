                                               
                                                                                    

package roast

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"dex/frost/core/curve"
	"dex/frost/core/frost"
	roastsession "dex/frost/runtime/session"
	"dex/logs"
	"dex/pb"
)

                                                           
                                                                               
func (c *Coordinator) aggregateSignatures(sess *roastsession.Session) ([][]byte, error) {
	signatures, err := c.aggregateSessionSignatures(sess)
	if err != nil {
		return nil, err
	}

	allCompleted := true
	for i := 0; i < len(sess.Messages); i++ {
		if i >= len(signatures) || signatures[i] == nil {
			allCompleted = false
			break
		}
	}

	if allCompleted {
		sess.MarkCompleted(signatures)
	} else {
		                                                                                       
		                                                         
		sess.MarkCompleted(signatures)
		sess.SetState(roastsession.SignSessionStateCollectingShares)
	}

	return signatures, nil
}

func (c *Coordinator) aggregateSessionSignatures(sess *roastsession.Session) ([][]byte, error) {
	if sess == nil {
		return nil, ErrInvalidRoastPayload
	}
	if len(sess.Messages) == 0 {
		return nil, errors.New("no messages to sign")
	}

	roastExec, err := c.cryptoFactory.NewROASTExecutor(sess.SignAlgo)
	if err != nil {
		return nil, err
	}

	minSigners := sess.Threshold
	if minSigners <= 0 {
		minSigners = 1
	}

	selectedSet := sess.SelectedSetSnapshot()
	signatures := make([][]byte, len(sess.Messages))
	completedTasks := make(map[int]bool)

	for taskIdx, msg := range sess.Messages {
		                                                                                                                         
		currentRound := sess.CurrentRound()
		nonces := make([]NonceInput, 0, len(selectedSet))
		shares := make([]ShareInput, 0, len(selectedSet))
		for _, idx := range selectedSet {
			nonceData, nonceOK := sess.GetNonce(idx)
			shareData, shareOK := sess.GetShare(idx)
			if !nonceOK || !shareOK {
				continue
			}
			if nonceData.Round != currentRound || shareData.Round != currentRound {
				logs.Warn("[Coordinator] task %d: skipping stale round data from signer idx=%d (nonce_round=%d share_round=%d current_round=%d)",
					taskIdx, idx, nonceData.Round, shareData.Round, currentRound)
				continue
			}
			if taskIdx >= len(nonceData.HidingNonces) || taskIdx >= len(nonceData.BindingNonces) || taskIdx >= len(shareData.Shares) {
				continue
			}

			hidingBytes := nonceData.HidingNonces[taskIdx]
			bindingBytes := nonceData.BindingNonces[taskIdx]
			hidingPoint, err := decodeSerializedPoint(pb.SignAlgo(sess.SignAlgo), hidingBytes)
			if err != nil {
				logs.Warn("[Coordinator] task %d: invalid hiding nonce point from signer %d: %v", taskIdx, idx+1, err)
				continue
			}
			bindingPoint, err := decodeSerializedPoint(pb.SignAlgo(sess.SignAlgo), bindingBytes)
			if err != nil {
				logs.Warn("[Coordinator] task %d: invalid binding nonce point from signer %d: %v", taskIdx, idx+1, err)
				continue
			}

			shareValue := new(big.Int).SetBytes(shareData.Shares[taskIdx])
			nonces = append(nonces, NonceInput{
				SignerID:     idx + 1,
				HidingPoint:  hidingPoint,
				BindingPoint: bindingPoint,
			})
			shares = append(shares, ShareInput{
				SignerID: idx + 1,
				Share:    shareValue,
			})
		}

		if len(nonces) < minSigners {
			logs.Debug("[Coordinator] task %d: insufficient nonce+share pairs (%d < %d), continue collecting", taskIdx, len(nonces), minSigners)
			continue
		}

		R, err := roastExec.ComputeGroupCommitment(nonces, msg)
		if err != nil {
			logs.Warn("[Coordinator] task %d: failed to compute group commitment: %v", taskIdx, err)
			continue
		}

		c.logAggregateDiagnostics(sess, taskIdx, msg, R, nonces, shares, roastExec)

		sig, err := roastExec.AggregateSignatures(R, shares)
		if err != nil {
			logs.Warn("[Coordinator] task %d: failed to aggregate signatures: %v", taskIdx, err)
			continue
		}
		c.logZVerifyDiagnostics(sess, taskIdx, R, shares, roastExec)

		signerIDs := make([]int, 0, len(shares))
		for _, s := range shares {
			signerIDs = append(signerIDs, s.SignerID)
		}
		signerAddresses := participantAddressesBySignerIDs(sess, signerIDs)
		c.logInfo("[Coordinator] aggregate input job=%s chain=%s vault=%d epoch=%d task=%d signers=%s message=%s signature=%s",
			sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, taskIdx,
			strings.Join(signerAddresses, ","),
			hex.EncodeToString(msg),
			hex.EncodeToString(sig))
		valid, verifyErr, panicVal := c.verifyAggregatedSignature(sess, taskIdx, msg, sig)
		if panicVal != nil {
			c.logWarn("[Coordinator] task %d: aggregated signature panic during verify (job=%s signers=%s message=%s sig=%s panic=%v)",
				taskIdx, sess.JobID, strings.Join(signerAddresses, ","), hex.EncodeToString(msg), hex.EncodeToString(sig), panicVal)
			c.logWarn("[Coordinator] task %d: session dump during verify panic job=%s sess=%s",
				taskIdx, sess.JobID, formatSessionDump(sess))
			c.logVerifyPanicDiagnostics(sess, taskIdx, msg, sig, signerIDs)
			continue
		}
		if verifyErr != nil {
			c.logWarn("[Coordinator] task %d: aggregated signature verify error (job=%s signers=%s message=%s sig=%s err=%v)",
				taskIdx, sess.JobID, strings.Join(signerAddresses, ","), hex.EncodeToString(msg), hex.EncodeToString(sig), verifyErr)
			continue
		}
		if !valid {
			c.logWarn("[Coordinator] task %d: aggregated signature invalid (job=%s signers=%s message=%s sig=%s)",
				taskIdx, sess.JobID, strings.Join(signerAddresses, ","), hex.EncodeToString(msg), hex.EncodeToString(sig))
			continue
		}

		signatures[taskIdx] = sig
		completedTasks[taskIdx] = true
		c.logInfo("[Coordinator] task %d completed job=%s signers=%s signature=%s",
			taskIdx, sess.JobID, strings.Join(signerAddresses, ","), hex.EncodeToString(sig))
	}

	if len(completedTasks) == 0 {
		return nil, ErrInsufficientShares
	}

	logs.Info("[Coordinator] partial completion: %d/%d tasks completed", len(completedTasks), len(sess.Messages))
	return signatures, nil
}

                                                                               
func (c *Coordinator) logAggregateDiagnostics(sess *roastsession.Session, taskIdx int, msg []byte, R CurvePoint, nonces []NonceInput, shares []ShareInput, roastExec ROASTExecutor) {
	signerIDs := make([]int, 0, len(shares))
	for _, s := range shares {
		signerIDs = append(signerIDs, s.SignerID)
	}
	rParity := -1
	if R.Y != nil {
		rParity = int(R.Y.Bit(0))
	}
	coordGroupPubX := big.NewInt(0)
	taskTweak := taskTweakAt(sess.Tweaks, taskIdx)
	if c.vaultProvider != nil {
		if gpb, gpErr := c.vaultProvider.VaultGroupPubkey(sess.Chain, sess.VaultID, sess.KeyEpoch); gpErr == nil {
			if _, derivedGroupPubX, deriveErr := deriveSigningPubkeyForTask(pb.SignAlgo(sess.SignAlgo), gpb, taskTweak); deriveErr == nil {
				coordGroupPubX = derivedGroupPubX
			}
		}
	}
	diagE := roastExec.ComputeChallenge(R, coordGroupPubX, msg)
	diagLambdas := roastExec.ComputeLagrangeCoefficients(signerIDs)

	rxBytes := make([]byte, 32)
	if R.X != nil {
		R.X.FillBytes(rxBytes)
	}
	gpxBytes := make([]byte, 32)
	coordGroupPubX.FillBytes(gpxBytes)
	eBytes := make([]byte, 32)
	diagE.FillBytes(eBytes)
	c.logInfo("[Coordinator][sign-diag] job=%s task=%d R_x=%s R_y_parity=%d groupPubX=%s challenge=%s signer_ids=%v",
		sess.JobID, taskIdx,
		hex.EncodeToString(rxBytes), rParity,
		hex.EncodeToString(gpxBytes),
		hex.EncodeToString(eBytes),
		signerIDs)
	for _, s := range shares {
		sBytes := make([]byte, 32)
		s.Share.FillBytes(sBytes)
		lambdaHex := "nil"
		if diagLambdas[s.SignerID] != nil {
			lb := make([]byte, 32)
			diagLambdas[s.SignerID].FillBytes(lb)
			lambdaHex = hex.EncodeToString(lb)
		}
		                                                                                    
		logs.Debug("[Coordinator][sign-diag] job=%s task=%d signer=%d lambda=%s share_fp=%s",
			sess.JobID, taskIdx, s.SignerID, lambdaHex, dataFingerprintForLog(sBytes))
	}
}

                                                                  
func (c *Coordinator) logZVerifyDiagnostics(sess *roastsession.Session, taskIdx int, R CurvePoint, shares []ShareInput, roastExec ROASTExecutor) {
	if sess == nil || taskIdx < 0 || taskIdx >= len(sess.Messages) || roastExec == nil {
		return
	}
	if pb.SignAlgo(sess.SignAlgo) != pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			c.logWarn("[Coordinator][z-verify] skipped after diagnostic panic job=%s task=%d panic=%v", sess.JobID, taskIdx, r)
		}
	}()
	if R.X == nil || R.Y == nil {
		c.logWarn("[Coordinator][z-verify] skipped invalid R job=%s task=%d", sess.JobID, taskIdx)
		return
	}
	grp := curve.NewSecp256k1Group()
	zTotal := big.NewInt(0)
	for _, s := range shares {
		if s.Share == nil {
			c.logWarn("[Coordinator][z-verify] skipped nil share job=%s task=%d signer=%d", sess.JobID, taskIdx, s.SignerID)
			return
		}
		zTotal.Add(zTotal, s.Share)
	}
	zTotal.Mod(zTotal, grp.Order())
	zG := grp.ScalarBaseMult(zTotal)
	if zG.X == nil || zG.Y == nil {
		c.logWarn("[Coordinator][z-verify] skipped invalid zG job=%s task=%d", sess.JobID, taskIdx)
		return
	}

	Reven := curve.Point{X: new(big.Int).Set(R.X), Y: new(big.Int).Set(R.Y)}
	if Reven.Y.Bit(0) == 1 {
		fieldP, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F", 16)
		Reven.Y = new(big.Int).Sub(fieldP, Reven.Y)
	}

	taskTweak := taskTweakAt(sess.Tweaks, taskIdx)
	coordVerifyPubkey := []byte(nil)
	coordGroupPubX := big.NewInt(0)
	if c.vaultProvider != nil {
		if gpb, gpErr := c.vaultProvider.VaultGroupPubkey(sess.Chain, sess.VaultID, sess.KeyEpoch); gpErr == nil {
			if derivedPubkey, derivedGroupPubX, deriveErr := deriveSigningPubkeyForTask(pb.SignAlgo(sess.SignAlgo), gpb, taskTweak); deriveErr == nil {
				coordVerifyPubkey = derivedPubkey
				coordGroupPubX = derivedGroupPubX
			}
		}
	}
	msg := sess.Messages[taskIdx]
	diagE := roastExec.ComputeChallenge(R, coordGroupPubX, msg)

	gpb := coordVerifyPubkey
	if len(gpb) == 0 && c.vaultProvider != nil {
		gpb, _ = c.vaultProvider.VaultGroupPubkey(sess.Chain, sess.VaultID, sess.KeyEpoch)
	}
	var P curve.Point
	if len(gpb) == 33 {
		P = grp.DecompressPoint(gpb)
	} else if len(gpb) >= 32 {
		compressed := make([]byte, 33)
		compressed[0] = 0x02
		copy(compressed[1:], gpb[:32])
		P = grp.DecompressPoint(compressed)
	}
	if P.X == nil || P.Y == nil {
		c.logWarn("[Coordinator][z-verify] skipped invalid secp256k1 pubkey job=%s task=%d sign_algo=%s pubkey_len=%d pubkey=%s",
			sess.JobID, taskIdx, pb.SignAlgo(sess.SignAlgo).String(), len(gpb), hex.EncodeToString(gpb))
		return
	}
	eP := grp.ScalarMult(P, diagE)
	expected := grp.Add(Reven, eP)
	if eP.X == nil || eP.Y == nil || expected.X == nil || expected.Y == nil {
		c.logWarn("[Coordinator][z-verify] skipped invalid expected point job=%s task=%d", sess.JobID, taskIdx)
		return
	}
	match := zG.X.Cmp(expected.X) == 0 && zG.Y.Cmp(expected.Y) == 0
	c.logInfo("[Coordinator][z-verify] job=%s task=%d z_total=%s zG=(%s,%s) R_even=(%s,%s) eP=(%s,%s) expected=(%s,%s) match=%v",
		sess.JobID, taskIdx,
		hex.EncodeToString(bigIntFillBytes32(zTotal)),
		hex.EncodeToString(bigIntFillBytes32(zG.X)), hex.EncodeToString(bigIntFillBytes32(zG.Y)),
		hex.EncodeToString(bigIntFillBytes32(Reven.X)), hex.EncodeToString(bigIntFillBytes32(Reven.Y)),
		hex.EncodeToString(bigIntFillBytes32(eP.X)), hex.EncodeToString(bigIntFillBytes32(eP.Y)),
		hex.EncodeToString(bigIntFillBytes32(expected.X)), hex.EncodeToString(bigIntFillBytes32(expected.Y)),
		match)
	c.logInfo("[Coordinator][z-verify] job=%s task=%d R_orig=(%s,%s) R_y_parity=%d P=(%s,%s)",
		sess.JobID, taskIdx,
		hex.EncodeToString(bigIntFillBytes32(R.X)), hex.EncodeToString(bigIntFillBytes32(R.Y)), R.Y.Bit(0),
		hex.EncodeToString(bigIntFillBytes32(P.X)), hex.EncodeToString(bigIntFillBytes32(P.Y)))
}

func (c *Coordinator) verifyAggregatedSignature(sess *roastsession.Session, taskIdx int, msg, sig []byte) (valid bool, err error, panicVal any) {
	if sess == nil {
		return false, errors.New("nil session"), nil
	}
	if c.vaultProvider == nil {
		return true, nil, nil
	}

	signAlgo := pb.SignAlgo(sess.SignAlgo)
	groupPubkey, err := c.vaultProvider.VaultGroupPubkey(sess.Chain, sess.VaultID, sess.KeyEpoch)
	if err != nil {
		return false, fmt.Errorf("load group pubkey failed: %w", err), nil
	}
	verifyPubkey, _, err := deriveSigningPubkeyForTask(signAlgo, groupPubkey, taskTweakAt(sess.Tweaks, taskIdx))
	if err != nil {
		return false, err, nil
	}
	c.logInfo("[Coordinator] verify aggregated signature input job=%s chain=%s vault=%d epoch=%d sign_algo=%s group_pubkey=%s verify_pubkey=%s message=%s signature=%s",
		sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, signAlgo.String(),
		hex.EncodeToString(groupPubkey),
		hex.EncodeToString(verifyPubkey),
		hex.EncodeToString(msg),
		hex.EncodeToString(sig))

	defer func() {
		if r := recover(); r != nil {
			panicVal = r
		}
	}()
	valid, err = frost.Verify(signAlgo, verifyPubkey, msg, sig)
	c.logInfo("[Coordinator] verify aggregated signature result job=%s chain=%s vault=%d epoch=%d valid=%t err=%v",
		sess.JobID, sess.Chain, sess.VaultID, sess.KeyEpoch, valid, err)
	return valid, err, nil
}

func shortBytes(data []byte, n int) []byte {
	if len(data) <= n || n <= 0 {
		return data
	}
	return data[:n]
}
