                              
                                          
package vm

import (
	"bytes"
	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/core/frost"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                                       
type FrostWithdrawSignedTxHandler struct{}

func (h *FrostWithdrawSignedTxHandler) Kind() string {
	return "frost_withdraw_signed"
}

func (h *FrostWithdrawSignedTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	signedTx, ok := tx.GetContent().(*pb.AnyTx_FrostWithdrawSignedTx)
	if !ok {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "not a frost withdraw signed transaction"}, errors.New("not a frost withdraw signed transaction")
	}

	signed := signedTx.FrostWithdrawSignedTx
	if signed == nil || signed.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid frost withdraw signed transaction"}, errors.New("invalid frost withdraw signed transaction")
	}

	txID := signed.Base.TxId
	jobID := signed.JobId
	signedPackageBytes := signed.SignedPackageBytes
	withdrawIDs := signed.WithdrawIds
	submitHeight := signed.Base.ExecutedHeight
	submitter := signed.Base.FromAddress

	                     
	if jobID == "" {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing job_id"}, errors.New("missing job_id")
	}
	if len(signedPackageBytes) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "empty signed_package_bytes"}, errors.New("empty signed_package_bytes")
	}
	if len(withdrawIDs) == 0 {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "empty withdraw_ids"}, errors.New("empty withdraw_ids")
	}
	if chain.NormalizeChain(signed.Chain) == chain.ChainBTC {
		if job, found, err := readBTCIsolationJob(sv, jobID); err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		} else if found && job.BtcProtocolVersion == 3 {
			return h.dryRunBTCIsolationSigned(signed, job, sv)
		} else if found && job.Status == frostWithdrawJobStatusSigned {
			active, err := chain.BTCIsolationActive(sv.Get)
			if err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
			}
			if active {
				return replayBTCIsolationLegacySigned(sv, signed, job)
			}
		}
	}

	ops := make([]WriteOp, 0)

	                                                                                         
	firstWithdrawID := withdrawIDs[0]
	firstWithdrawKey := keys.KeyFrostWithdraw(firstWithdrawID)
	firstWithdrawData, exists, err := sv.Get(firstWithdrawKey)
	if err != nil || !exists {
		if signed.Chain == "" && (err == nil || isStateViewNotFound(err)) {
			if old, found, readErr := readBTCIsolationJob(sv, jobID); readErr == nil && found && old.Chain == chain.ChainBTC && old.BtcProtocolVersion == 0 && old.Status == frostWithdrawJobStatusSigned {
				if active, gateErr := chain.BTCIsolationActive(sv.Get); gateErr == nil && active {
					return replayBTCIsolationLegacySigned(sv, signed, old)
				}
			}
		}
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw not found: " + firstWithdrawID}, errors.New("withdraw not found: " + firstWithdrawID)
	}

	firstWithdraw, err := unmarshalWithdrawRequest(firstWithdrawData)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse withdraw"}, err
	}

	chainName := chain.NormalizeChain(firstWithdraw.Chain)
	asset := firstWithdraw.Asset
	if chainName != chain.ChainBTC && len(withdrawIDs) > chain.MaxWithdrawalsPerBatch {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "account withdraw batch exceeds 64 items"}, errors.New("account withdraw batch exceeds 64 items")
	}
	if chainName != chain.ChainBTC {
		return h.dryRunMixedAccountSigned(signed, sv)
	}
	jobState, hasJobState, err := readFrostWithdrawJobState(sv, jobID)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("failed to read withdraw job state: %v", err)}, err
	}

	                                                                                      
	                                                                                   
	                                                                             
	if txChain := strings.TrimSpace(signed.Chain); txChain != "" && chain.NormalizeChain(txChain) != chainName {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "tx chain mismatch with withdraw"}, errors.New("tx chain mismatch with withdraw")
	}
	if txAsset := strings.TrimSpace(signed.Asset); txAsset != "" && !strings.EqualFold(txAsset, asset) {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "tx asset mismatch with withdraw"}, errors.New("tx asset mismatch with withdraw")
	}

	                                                           
	if !hasJobState {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw claim not found"}, errors.New("withdraw claim not found")
	}
	if err := validateWithdrawJobStateForSigned(jobState, signed, chainName, asset, withdrawIDs); err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
	}

	var externalArtifact *artifact.Record
	var accountArtifactMeta *artifact.Metadata
	var accountArtifactRaw []byte

	                                    
	if chainName == chain.ChainBTC {
		vaultID := signed.VaultId
		inputSigs := signed.InputSigs
		if !bytes.Equal(signed.TemplateData, jobState.TemplateData) || !bytes.Equal(signed.TemplateHash, jobState.TemplateHash) {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "BTC signed template differs from claim"}, errors.New("BTC signed template differs from claim")
		}
		validated, err := validateBTCClaimedWithdrawTemplate(sv, jobID, jobState)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		template := validated.template
		if len(inputSigs) != len(template.Inputs) || len(inputSigs) == 0 {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "input_sigs count mismatch with template inputs"}, errors.New("input_sigs count mismatch")
		}
		if !signedPackageMatchesInputSigs(signedPackageBytes, inputSigs) {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "signed_package_bytes differ from input_sigs"}, errors.New("signed package/input signatures mismatch")
		}
		if !equalByteSlices(signed.ScriptPubkeys, validated.scriptPubKeys) {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "script_pubkeys differ from local UTXO state"}, errors.New("script_pubkeys differ from local UTXO state")
		}
		pubKey := validated.vault.GroupPubkey
		signAlgo := validated.vault.SignAlgo
		if signAlgo == pb.SignAlgo_SIGN_ALGO_UNSPECIFIED {
			vaultCfgKey := keys.KeyFrostVaultConfig(chainName, 0)
			if cfgData, exists, _ := sv.Get(vaultCfgKey); exists {
				var cfg pb.FrostVaultConfig
				if err := unmarshalProtoCompat(cfgData, &cfg); err == nil {
					signAlgo = cfg.SignAlgo
				}
			}
		}

		sighashes, err := template.ComputeTaprootSighash(validated.scriptPubKeys, btc.SighashDefault)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to compute sighash: " + err.Error()}, err
		}

		if len(signed.GuardianSigs) != len(inputSigs) {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "guardian signature count mismatch"}, errors.New("guardian signature count mismatch")
		}
		for i, sig := range inputSigs {
			if len(sig) != 64 {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid signature length for input"}, errors.New("invalid signature length")
			}

			validGuardian, guardianErr := verifySignature(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, validated.guardianPubkey, sighashes[i], signed.GuardianSigs[i])
			if guardianErr != nil || !validGuardian {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("guardian signature verification failed for input[%d]", i)}, errors.New("guardian signature verification failed")
			}
			verifyPubKey := pubKey
			if len(validated.utxos[i].Tweak) == 32 {
				tweakedPub, err := utils.ComputeTweakedPubkey(pubKey, validated.utxos[i].Tweak)
				if err != nil {
					return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid BTC UTXO tweak"}, err
				}
				verifyPubKey = tweakedPub
			}

			valid, err := verifySignature(signAlgo, verifyPubKey, sighashes[i], sig)
			if err != nil {
				detail := diagnoseSignatureMismatch(signAlgo, verifyPubKey, sighashes, i, sig)
				errDetail := fmt.Sprintf("%s; %s", err.Error(), detail)
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("signature verification failed for input[%d]: %s", i, errDetail)}, errors.New("signature verification failed")
			}
			if !valid {
				detail := diagnoseSignatureMismatch(signAlgo, verifyPubKey, sighashes, i, sig)
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: fmt.Sprintf("signature verification failed for input[%d]: %s", i, detail)}, errors.New("signature verification failed")
			}
		}
		rawTx, err := template.BuildSignedRawTx(inputSigs, signed.GuardianSigs)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to build BTC raw transaction"}, err
		}
		btcTxID, err := btcSignedTemplateTxID(template, inputSigs)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to derive BTC transaction ID"}, err
		}
		parents, err := frostBTCParentArtifactIDs(sv, vaultID, template.Inputs)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to resolve BTC transaction dependencies"}, err
		}
		externalArtifact, err = storeFrostArtifact(sv, artifact.Metadata{ArtifactID: btcTxID, Kind: artifact.KindWithdraw, Chain: chainName, VaultID: vaultID, Epoch: jobState.KeyEpoch, Height: submitHeight, SourceID: jobID, ParentIDs: parents}, rawTx)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to archive BTC transaction artifact"}, err
		}
		btcChangeAmount := uint64(0)
		created, changeAmount, err := ensureBTCChangeUTXO(sv, vaultID, template, inputSigs, submitHeight)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to record BTC change UTXO: " + err.Error()}, err
		}
		if created {
			btcChangeAmount = changeAmount
		}
		                                                                                      
		advanceBtcUtxoFIFOHead(sv, vaultID)
		if btcChangeAmount > 0 {
			if err := addVaultAvailableBalance(sv, chainName, asset, vaultID, new(big.Int).SetUint64(btcChangeAmount)); err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to update vault available balance"}, err
			}
		}
		for _, input := range template.Inputs {
			sv.Del(keys.KeyFrostBtcUtxo(vaultID, input.TxID, input.Vout))
			sv.Del(keys.KeyFrostBtcLockedUtxo(vaultID, input.TxID, input.Vout))
			sv.Del(keys.KeyFrostBtcUtxoArtifact(vaultID, input.TxID, input.Vout))
		}
	} else {
		                                                                      
		                                                                                                   
		vaultID := signed.VaultId
		if vaultID == 0 && firstWithdraw.VaultID != 0 {
			vaultID = firstWithdraw.VaultID
		}

		                                                                                        
		if isSupportedAccountSigningChain(chainName) {
			if len(signed.TemplateHash) == 0 {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "missing template_hash for account chain"}, errors.New("missing template_hash for account chain")
			}

			vaultStateKey := keys.KeyFrostVaultState(chainName, vaultID)
			vaultStateData, vaultExists, vaultErr := sv.Get(vaultStateKey)
			if vaultErr != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to read vault state: " + vaultErr.Error()}, vaultErr
			}
			if !vaultExists || len(vaultStateData) == 0 {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "vault state not found"}, errors.New("vault state not found")
			}

			vaultState := &pb.FrostVaultState{}
			if err := unmarshalProtoCompat(vaultStateData, vaultState); err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse vault state"}, err
			}
			if signed.KeyEpoch > 0 && vaultState.KeyEpoch > 0 && signed.KeyEpoch != vaultState.KeyEpoch {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "key_epoch mismatch with vault state"}, errors.New("key_epoch mismatch with vault state")
			}

			templateKeyEpoch := vaultState.KeyEpoch
			if signed.KeyEpoch > 0 {
				templateKeyEpoch = signed.KeyEpoch
			}
			canonicalTemplate, _, err := buildCanonicalAccountTemplate(sv, chainName, asset, vaultID, templateKeyEpoch, withdrawIDs)
			if err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to rebuild canonical template: " + err.Error()}, err
			}
			if !bytes.Equal(canonicalTemplate.TemplateHash, signed.TemplateHash) {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "template_hash mismatch"}, errors.New("template_hash mismatch")
			}
			if len(signedPackageBytes) != evm.SchnorrSigWithYSize {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "invalid evm signed_package_bytes length"}, errors.New("invalid evm signed_package_bytes length")
			}

			signAlgo := vaultState.SignAlgo
			if signAlgo == pb.SignAlgo_SIGN_ALGO_UNSPECIFIED {
				vaultCfgKey := keys.KeyFrostVaultConfig(chainName, 0)
				if cfgData, exists, _ := sv.Get(vaultCfgKey); exists {
					var cfg pb.FrostVaultConfig
					if err := unmarshalProtoCompat(cfgData, &cfg); err == nil {
						signAlgo = cfg.SignAlgo
					}
				}
			}

			signingMsg, err := evm.SigningHashFromTemplateData(canonicalTemplate.TemplateData)
			if err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to compute signing hash: " + err.Error()}, err
			}
			valid, err := verifySignature(signAlgo, vaultState.GroupPubkey, signingMsg, signedPackageBytes)
			if err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "signature verification failed: " + err.Error()}, errors.New("signature verification failed")
			}
			if !valid {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "signature verification failed"}, errors.New("signature verification failed")
			}
			                                                                        
			                                                                           
			packaged, packageErr := packageAccountWithdrawal(
				sv, chainName, vaultID, canonicalTemplate.TemplateData, signedPackageBytes, signed.GuardianSigs[0],
				jobState.IncludesHandover,
			)
			if packageErr != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to package account transaction artifact"}, packageErr
			}
			accountArtifactMeta = &artifact.Metadata{Kind: artifact.KindWithdraw, Chain: chainName, VaultID: vaultID, Epoch: templateKeyEpoch, Height: submitHeight, SourceID: jobID}
			accountArtifactRaw = packaged.RawTx
		}

		                        
		accountWithdraws := make([]*FrostWithdrawRequest, 0, len(withdrawIDs))
		for _, wid := range withdrawIDs {
			withdrawKey := keys.KeyFrostWithdraw(wid)
			withdrawData, exists, err := sv.Get(withdrawKey)
			if err != nil || !exists || len(withdrawData) == 0 {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw not found while validating reservation: " + wid}, errors.New("withdraw not found while validating reservation")
			}
			withdraw, err := unmarshalWithdrawRequest(withdrawData)
			if err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse withdraw while validating reservation"}, err
			}
			accountWithdraws = append(accountWithdraws, withdraw)
		}
		totalAmount, err := sumWithdrawAmounts(accountWithdraws)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}
		if err := validateAccountWithdrawReservation(sv, jobState, chainName, asset, vaultID, totalAmount); err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: err.Error()}, err
		}

		                                                              
		                                                                                                 
		                                                                                                       
		if totalAmount.Sign() > 0 {
			simSnap := sv.Snapshot()
			simConsumedAmount, simHasLots, err := h.consumeVaultFundsLots(sv, chainName, asset, vaultID, totalAmount)
			if err != nil {
				_ = sv.Revert(simSnap)
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to consume funds lots"}, err
			}
			if simHasLots && simConsumedAmount.Cmp(totalAmount) < 0 {
				_ = sv.Revert(simSnap)
				return nil, &Receipt{
					TxID:   txID,
					Status: "FAILED",
					Error:  fmt.Sprintf("insufficient funds lots: need %s, got %s", totalAmount.String(), simConsumedAmount.String()),
				}, errors.New("insufficient funds lots")
			}
			if err := sv.Revert(simSnap); err != nil {
				return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to revert simulation snapshot"}, err
			}
			if simHasLots {
				_, _, err := h.consumeVaultFundsLots(sv, chainName, asset, vaultID, totalAmount)
				if err != nil {
					return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to consume funds lots"}, err
				}
			}
		}

		                                                         
		head := GetFundsLotHead(sv, chainName, asset, vaultID)
		headKey := keys.KeyFrostFundsLotHead(chainName, asset, vaultID)
		headValue := strconv.FormatUint(head.Height, 10) + "|" + strconv.FormatUint(head.Seq, 10)
		ops = append(ops, WriteOp{
			Key:      headKey,
			Value:    []byte(headValue),
			Category: "frost_funds_lot_head",
		})

		                                  
		if err := subVaultReservedBalance(sv, chainName, asset, vaultID, totalAmount); err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to consume vault reservation"}, err
		}
	}

	                                                                                     
	var maxSeq uint64
	for _, wid := range withdrawIDs {
		withdrawKey := keys.KeyFrostWithdraw(wid)
		withdrawData, exists, err := sv.Get(withdrawKey)
		if err != nil || !exists {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw not found: " + wid}, errors.New("withdraw not found: " + wid)
		}

		withdraw, err := unmarshalWithdrawRequest(withdrawData)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to parse withdraw"}, err
		}

		                               
		if chain.NormalizeChain(withdraw.Chain) != chainName || withdraw.Asset != asset {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "inconsistent chain/asset"}, errors.New("inconsistent chain/asset")
		}

		if withdraw.Status != WithdrawStatusSigning || withdraw.JobID != jobID {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "withdraw not claimed by job: " + wid}, errors.New("withdraw not claimed by job: " + wid)
		}
		if withdraw.Seq > maxSeq {
			maxSeq = withdraw.Seq
		}

		                                                                                                                                   
		                                                                                     

		                         
		withdraw.Status = WithdrawStatusSigned
		withdraw.JobID = jobID
		withdraw.SignedTxID = txID

		updatedData, err := marshalWithdrawRequest(withdraw)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal withdraw"}, err
		}

		ops = append(ops, archiveWriteOp(keys.KeyArchiveFrostWithdraw(withdraw.WithdrawID), updatedData, "archive_frost_withdraw"))
		ops = append(ops, archiveWriteOp(keys.KeyFrostPermanentWithdraw(withdraw.WithdrawID), updatedData, "frost_permanent_withdraw"))
		ops = append(ops, finalizeFrostWithdrawQueryIndexes(withdraw)...)
		ops = append(ops, archiveWriteOp(keys.KeyArchiveFrostWithdrawDone(chainName, asset, withdraw.Seq, withdraw.WithdrawID), []byte(withdraw.WithdrawID), "archive_frost_withdraw_done"))
		ops = append(ops, deleteWriteOp(withdrawKey, "frost_withdraw_prune"))
		ops = append(ops, deleteWriteOp(keys.KeyFrostWithdrawFIFOIndex(chainName, asset, withdraw.Seq), "frost_withdraw_fifo_prune"))
	}

	jobState.Status = frostWithdrawJobStatusSigned
	jobState.SignedTxId = txID
	jobData, err := proto.Marshal(jobState)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal withdraw job state"}, err
	}
	ops = append(ops, archiveWriteOp(keys.KeyArchiveFrostWithdrawJob(jobID), jobData, "archive_frost_withdraw_job"))
	ops = append(ops, archiveWriteOp(keys.KeyFrostPermanentWithdrawJob(jobID), jobData, "frost_permanent_job"))
	ops = append(ops, deleteWriteOp(keys.KeyFrostWithdrawJob(jobID), "frost_withdraw_job_prune"))
	ops = append(ops, deleteWriteOp(keys.KeyFrostWithdrawActiveJob(chainName, jobState.VaultId, jobState.KeyEpoch, jobID), "frost_withdraw_active_prune"))
	if chainName == chain.ChainBTC {
		ops = append(ops, deleteWriteOp(keys.KeyFrostBtcWithdrawActiveChild(asset, jobID), "frost_btc_active_child_prune"))
	}

	                       
	pkg := &pb.FrostSignedPackage{
		JobId:              jobID,
		Idx:                0,
		SignedPackageBytes: signedPackageBytes,
		GuardianSigs:       signed.GuardianSigs,
		SubmitHeight:       submitHeight,
		Submitter:          submitter,
	}
	pkgData, err := proto.Marshal(pkg)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to marshal signed package"}, err
	}
	ops = append(ops, archiveWriteOp(keys.KeyArchiveFrostSignedPackage(jobID, 0), pkgData, "archive_frost_signed_pkg"))
	ops = append(ops, archiveWriteOp(keys.KeyFrostPermanentSignedPackage(jobID, 0), pkgData, "frost_permanent_package"))

	                                                                                    
	                                                            
	nextHead, cancelledCleanupOps, _, err := resolveCancelledWithdrawsFrom(sv, chainName, asset, maxSeq+1, nil)
	if err != nil {
		return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to advance withdraw FIFO: " + err.Error()}, err
	}
	ops = append(ops, cancelledCleanupOps...)
	headKey := keys.KeyFrostWithdrawFIFOHead(chainName, asset)
	ops = append(ops, WriteOp{
		Key:      headKey,
		Value:    []byte(strconv.FormatUint(nextHead, 10)),
		Category: "frost_withdraw_head",
	})

	                                                                         
	                                                                              
	                                                                             
	if accountArtifactMeta != nil {
		var err error
		externalArtifact, err = storeFrostArtifact(sv, *accountArtifactMeta, accountArtifactRaw)
		if err != nil {
			return nil, &Receipt{TxID: txID, Status: "FAILED", Error: "failed to archive account transaction artifact"}, err
		}
	}
	if externalArtifact != nil {
		for _, wid := range withdrawIDs {
			ops = append(ops, archiveWriteOp(keys.KeyFrostWithdrawArtifact(wid), []byte(externalArtifact.Metadata.ArtifactID), "archive_frost_withdraw_artifact"))
			ops = append(ops, archiveWriteOp(keys.KeyFrostPermanentWithdrawArtifact(wid), []byte(externalArtifact.Metadata.ArtifactID), "frost_permanent_withdrawref"))
		}
	}
	applyFrostWithdrawTransactionOps(sv, ops)

	return ops, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: len(ops)}, nil
}

func isSupportedAccountSigningChain(chainName string) bool {
	switch chain.NormalizeChain(chainName) {
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
		return true
	default:
		return false
	}
}

func sameOrderedWithdrawIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if strings.TrimSpace(left[i]) != strings.TrimSpace(right[i]) {
			return false
		}
	}
	return true
}

func (h *FrostWithdrawSignedTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}

func (h *FrostWithdrawSignedTxHandler) consumeVaultFundsLots(sv StateView, chainName, asset string, vaultID uint32, targetAmount *big.Int) (*big.Int, bool, error) {
	consumedAmount := big.NewInt(0)
	if targetAmount == nil || targetAmount.Sign() <= 0 {
		return consumedAmount, false, nil
	}
	hasLots := false

	for consumedAmount.Cmp(targetAmount) < 0 {
		beforeHead := GetFundsLotHead(sv, chainName, asset, vaultID)
		requestID, ok, err := getFundsLotAtHeadStrict(sv, chainName, asset, vaultID)
		if err != nil {
			return nil, hasLots, err
		}
		if !ok {
			break
		}
		hasLots = true

		rechargeKey := keys.KeyRechargeRequest(requestID)
		rechargeData, exists, err := sv.Get(rechargeKey)
		if err != nil {
			return nil, hasLots, err
		}
		if !exists || len(rechargeData) == 0 {
			afterHead, err := advanceFundsLotHeadStrict(sv, chainName, asset, vaultID)
			if err != nil {
				return nil, hasLots, err
			}
			if afterHead.Height == beforeHead.Height && afterHead.Seq == beforeHead.Seq {
				break
			}
			continue
		}

		var recharge pb.RechargeRequest
		if err := unmarshalProtoCompat(rechargeData, &recharge); err != nil {
			afterHead, err := advanceFundsLotHeadStrict(sv, chainName, asset, vaultID)
			if err != nil {
				return nil, hasLots, err
			}
			if afterHead.Height == beforeHead.Height && afterHead.Seq == beforeHead.Seq {
				break
			}
			continue
		}

		lotAmount, err := ParseBalance(recharge.Amount)
		if err != nil {
			afterHead, err := advanceFundsLotHeadStrict(sv, chainName, asset, vaultID)
			if err != nil {
				return nil, hasLots, err
			}
			if afterHead.Height == beforeHead.Height && afterHead.Seq == beforeHead.Seq {
				break
			}
			continue
		}
		lotAmount, _, err = fundsLotRemainingAmount(sv, chainName, asset, vaultID, beforeHead.Height, beforeHead.Seq, lotAmount)
		if err != nil {
			return nil, hasLots, err
		}

		needed := new(big.Int).Sub(targetAmount, consumedAmount) // safe-math-ok: 循环仅在 consumedAmount < targetAmount 时继续消费
		if lotAmount.Cmp(needed) <= 0 {
			nextConsumedAmount, err := SafeAdd(consumedAmount, lotAmount)
			if err != nil {
				return nil, hasLots, err
			}
			consumedAmount = nextConsumedAmount
			afterHead, err := advanceFundsLotHeadStrict(sv, chainName, asset, vaultID)
			if err != nil {
				return nil, hasLots, err
			}
			if afterHead.Height == beforeHead.Height && afterHead.Seq == beforeHead.Seq {
				break
			}
			continue
		}

		remaining := new(big.Int).Sub(lotAmount, needed) // safe-math-ok: 仅在 lotAmount > needed 的分支保存剩余充值 lot
		sv.Set(keys.KeyFrostFundsLotRemaining(chainName, asset, vaultID, beforeHead.Height, beforeHead.Seq), []byte(remaining.String()))
		consumedAmount = new(big.Int).Set(targetAmount)
		break
	}

	return consumedAmount, hasLots, nil
}

                                                                    
                                                                                
                                                                                                            
                                                                        
var verifySignatureFn = verifySignatureImpl

func verifySignature(signAlgo pb.SignAlgo, pubKey, msg, sig []byte) (bool, error) {
	return verifySignatureFn(signAlgo, pubKey, msg, sig)
}

func verifySignatureImpl(signAlgo pb.SignAlgo, pubKey, msg, sig []byte) (valid bool, err error) {
	if signAlgo == pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 {
		return evm.NewETHAdapter().VerifySignature(pubKey, msg, sig)
	}

	verifyPubKey := pubKey
	if signAlgo == pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
		xOnlyPubKey, err := normalizeBIP340PubKey(pubKey)
		if err != nil {
			return false, err
		}
		verifyPubKey = xOnlyPubKey
	}

	defer func() {
		if r := recover(); r != nil {
			valid = false
			err = fmt.Errorf("signature verification panic: %v", r)
		}
	}()

	return frost.Verify(signAlgo, verifyPubKey, msg, sig)
}

func normalizeBIP340PubKey(pubKey []byte) ([]byte, error) {
	return utils.NormalizeSecp256k1XOnlyPubKey(pubKey)
}

func diagnoseSignatureMismatch(signAlgo pb.SignAlgo, pubKey []byte, sighashes [][]byte, failedInput int, sig []byte) string {
	prefix := "invalid signature"
	if failedInput >= 0 && failedInput < len(sighashes) {
		prefix = fmt.Sprintf("invalid signature (sighash=%s, sig=%s, pubkey=%s)",
			shortHex(sighashes[failedInput], 8),
			shortHex(sig, 8),
			shortHex(pubKey, 8),
		)
	}

	if allZero(sig) {
		return prefix + ": signature bytes are all zero (placeholder/not signed)"
	}

	                                                                                        
	for idx, msg := range sighashes {
		if idx == failedInput {
			continue
		}
		ok, err := verifySignature(signAlgo, pubKey, msg, sig)
		if err == nil && ok {
			return fmt.Sprintf("%s: signature matches input[%d] instead (likely input order mismatch)", prefix, idx)
		}
	}

	                                                                                              
	if signAlgo == pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 && len(pubKey) == 33 && pubKey[0] == 0x03 {
		return prefix + ": vault group_pubkey is compressed odd-Y (0x03); check signer-side BIP340 x-only normalization"
	}

	return prefix
}

func shortHex(data []byte, n int) string {
	if len(data) == 0 {
		return ""
	}
	if n <= 0 || len(data) <= n {
		return hex.EncodeToString(data)
	}
	return hex.EncodeToString(data[:n])
}

func allZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return len(data) > 0
}

                                                                    
            
                                                    
                                                       
                                            
func (h *FrostWithdrawSignedTxHandler) validateFIFOOrder(sv StateView, chainName, asset string, withdrawIDs []string) error {
	currentHead := readUint64FromState(sv, keys.KeyFrostWithdrawFIFOHead(chainName, asset))
	if currentHead == 0 {
		currentHead = 1
	}
	resolvedHead, _, _, err := resolveCancelledWithdrawsFrom(sv, chainName, asset, currentHead, nil)
	if err != nil {
		return err
	}
	return h.validateFIFOOrderAtHead(sv, chainName, asset, withdrawIDs, resolvedHead)
}

func (h *FrostWithdrawSignedTxHandler) validateFIFOOrderAtHead(sv StateView, chainName, asset string, withdrawIDs []string, currentHead uint64) error {
	if len(withdrawIDs) == 0 {
		return errors.New("FIFO: empty withdraw_ids")
	}
	if currentHead == 0 {
		currentHead = 1
	}

	                                     
	withdrawKeys := make([]string, len(withdrawIDs))
	for i, wid := range withdrawIDs {
		withdrawKeys[i] = keys.KeyFrostWithdraw(wid)
	}
	values, err := getManyFrostState(sv, withdrawKeys)
	if err != nil {
		return err
	}
	withdraws := make([]*FrostWithdrawRequest, 0, len(withdrawIDs))
	for i, wid := range withdrawIDs {
		withdrawData := values[withdrawKeys[i]]
		if len(withdrawData) == 0 {
			return errors.New("FIFO: withdraw not found: " + wid)
		}

		withdraw, err := unmarshalWithdrawRequest(withdrawData)
		if err != nil {
			return errors.New("FIFO: failed to parse withdraw: " + wid)
		}

		                               
		if withdraw.Status != WithdrawStatusQueued {
			return errors.New("FIFO: withdraw not in QUEUED status: " + wid)
		}

		                                                    
		if chain.NormalizeChain(withdraw.Chain) != chainName || withdraw.Asset != asset {
			return errors.New("FIFO: inconsistent chain/asset in withdraw: " + wid)
		}

		withdraws = append(withdraws, withdraw)
	}

	                                                     
	if withdraws[0].Seq != currentHead {
		return errors.New("FIFO: first withdraw seq (" + strconv.FormatUint(withdraws[0].Seq, 10) +
			") != head (" + strconv.FormatUint(currentHead, 10) + ")")
	}

	                             
	for i := 1; i < len(withdraws); i++ {
		expectedSeq := currentHead + uint64(i)
		if withdraws[i].Seq != expectedSeq {
			return errors.New("FIFO: seq gap at position " + strconv.Itoa(i) +
				", expected " + strconv.FormatUint(expectedSeq, 10) +
				", got " + strconv.FormatUint(withdraws[i].Seq, 10))
		}
	}

	return nil
}

func getManyFrostState(sv StateView, stateKeys []string) (map[string][]byte, error) {
	if getter, ok := sv.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return getter.GetMany(stateKeys)
	}
	values := make(map[string][]byte, len(stateKeys))
	for _, key := range stateKeys {
		value, found, err := sv.Get(key)
		if err != nil {
			return nil, err
		}
		if found {
			values[key] = value
		}
	}
	return values, nil
}
