package vm

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"dex/frost/artifact"
	bridgeasset "dex/frost/asset"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func (h *FrostVaultTransitionSignedTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	content, ok := tx.GetContent().(*pb.AnyTx_FrostVaultTransitionSignedTx)
	if !ok || content.FrostVaultTransitionSignedTx == nil || content.FrostVaultTransitionSignedTx.Base == nil {
		return transitionSignedFailure(tx.GetTxId(), "invalid transition signed tx")
	}
	req := content.FrostVaultTransitionSignedTx
	txID := req.Base.TxId
	if txID == "" {
		txID = tx.GetTxId()
	}
	chainName := chainpkg.NormalizeChain(req.Chain)
	if !chainpkg.IsValidChain(chainName) {
		return transitionSignedFailure(txID, "invalid transition chain")
	}
	if req.OldVaultId != req.NewVaultId {
		return transitionSignedFailure(txID, "cross-vault transition is unsupported")
	}
	if req.EpochId == 0 || strings.TrimSpace(req.MigrationId) == "" {
		return transitionSignedFailure(txID, "missing migration identity")
	}

	transitionKey := keys.KeyFrostVaultTransition(chainName, req.OldVaultId, req.EpochId)
	transitionData, exists, err := sv.Get(transitionKey)
	if err != nil || !exists || len(transitionData) == 0 {
		return transitionSignedFailure(txID, "transition not found")
	}
	var transition pb.VaultTransitionState
	if err := unmarshalProtoCompat(transitionData, &transition); err != nil {
		return transitionSignedFailure(txID, "invalid transition state")
	}
	if transition.DkgStatus != DKGStatusKeyReady || transition.VaultId != req.OldVaultId || transition.EpochId != req.EpochId {
		return transitionSignedFailure(txID, "transition is not ready")
	}
	if !stringMember(transition.OldCommitteeMembers, req.Base.FromAddress) {
		return transitionSignedFailure(txID, "submitter is not an old vault member")
	}

	migrationKey := keys.KeyFrostVaultMigration(chainName, req.OldVaultId, req.EpochId)
	migrationData, migrationExists, err := sv.Get(migrationKey)
	if err != nil || !migrationExists || len(migrationData) == 0 {
		return transitionSignedFailure(txID, "finalized migration intent not found")
	}
	var migration pb.FrostVaultMigrationState
	if err := unmarshalProtoCompat(migrationData, &migration); err != nil {
		return transitionSignedFailure(txID, "invalid migration state")
	}
	if migration.BtcProtocolVersion == 3 {
		active, err := chainpkg.BTCIsolationActive(sv.Get)
		if err != nil || !active {
			return transitionSignedFailure(txID, "BTC v3 isolation is not active")
		}
		if migration.NoFunds {
			cfg, err := loadMigrationVaultConfig(sv, chainpkg.ChainBTC)
			if err != nil || networkChainIDFromView(sv) == 0 || cfg.DepositDexChainId != networkChainIDFromView(sv) ||
				migration.BtcChildCount != 0 || migration.Status != migrationStatusIntentFinalized && migration.Status != migrationStatusSigned ||
				len(migration.BtcManifestHash) != 32 || !bytes.Equal(migration.IntentHash, chainpkg.BTCMigrationManifestIntentHash(networkChainIDFromView(sv), cfg.BtcNetwork, &migration)) {
				return transitionSignedFailure(txID, "invalid empty BTC manifest commitment")
			}
		}
	}
	if migration.BtcProtocolVersion == 3 && !migration.NoFunds {
		return h.dryRunBTCIsolationMigrationSigned(req, transitionKey, &transition, &migration, sv)
	}
	if migration.Status == migrationStatusSigned {
		                                                                        
		                                                                      
		                                            
		storedPackageHash, found, _ := sv.Get(keys.KeyFrostMigrationPackageHash(migration.MigrationId))
		if found && bytes.Equal(storedPackageHash, transitionSignedPackageHash(req)) {
			return nil, &Receipt{TxID: txID, Status: "SUCCEED", WriteCount: 0}, nil
		}
		return transitionSignedFailure(txID, "migration is already signed by a different package")
	}
	if migration.Status != migrationStatusIntentFinalized || transition.Lifecycle != VaultLifecycleDraining {
		return transitionSignedFailure(txID, "migration is not accepting signatures")
	}
	intent := proto.Clone(&migration).(*pb.FrostVaultMigrationState)
	if err := validateTransitionSignedEnvelope(req, &migration, intent); err != nil {
		return transitionSignedFailure(txID, err.Error())
	}

	vaultKey := keys.KeyFrostVaultState(chainName, req.OldVaultId)
	vaultData, vaultExists, err := sv.Get(vaultKey)
	if err != nil || !vaultExists || len(vaultData) == 0 {
		return transitionSignedFailure(txID, "old vault not found")
	}
	var vault pb.FrostVaultState
	if err := unmarshalProtoCompat(vaultData, &vault); err != nil {
		return transitionSignedFailure(txID, "invalid old vault state")
	}
	if vault.KeyEpoch != intent.OldEpoch || !frostVaultGroupPubkeysEqual(chainName, vault.GroupPubkey, intent.OldGroupPubkey) {
		return transitionSignedFailure(txID, "old vault epoch/key changed after intent")
	}
	nextReview, termErr := nextVaultCommitteeReviewHeight(req.Base.ExecutedHeight)
	if termErr != nil {
		return transitionSignedFailure(txID, termErr.Error())
	}
	signAlgo := transition.SignAlgo
	if signAlgo == pb.SignAlgo_SIGN_ALGO_UNSPECIFIED {
		signAlgo = vault.SignAlgo
	}
	var valid bool
	var verifyErr error
	if isSupportedAccountSigningChain(chainName) {
		if signAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128 {
			return transitionSignedFailure(txID, "invalid account migration signature algorithm")
		}
		valid, verifyErr = evm.NewEVMAdapter(chainName, 0).VerifySignature(intent.OldGroupPubkey, intent.IntentHash, req.Signature)
	} else {
		valid, verifyErr = verifySignatureImpl(signAlgo, intent.OldGroupPubkey, intent.IntentHash, req.Signature)
	}
	if verifyErr != nil || !valid {
		return transitionSignedFailure(txID, "invalid migration authorization signature")
	}

	cfg, err := loadMigrationVaultConfig(sv, chainName)
	if err != nil {
		return transitionSignedFailure(txID, "guardian configuration unavailable")
	}
	wantGuardianCount := 1
	if chainName == chainpkg.ChainBTC {
		wantGuardianCount += len(req.InputSigs)
	}
	if len(req.GuardianSigs) != wantGuardianCount {
		return transitionSignedFailure(txID, "missing migration guardian authorization")
	}
	guardianSignature := req.GuardianSigs[len(req.GuardianSigs)-1]
	if isSupportedAccountSigningChain(chainName) {
		valid, verifyErr = evm.NewEVMAdapter(chainName, 0).VerifySignature(cfg.GuardianPubkey, intent.IntentHash, guardianSignature)
	} else {
		valid, verifyErr = verifySignatureImpl(signAlgo, cfg.GuardianPubkey, intent.IntentHash, guardianSignature)
	}
	if verifyErr != nil || !valid {
		return transitionSignedFailure(txID, "invalid guardian migration authorization")
	}
	var parentArtifactIDs []string
	artifactID := ""
	switch chainName {
	case chainpkg.ChainBTC:
		parentArtifactIDs, artifactID, err = validateAndApplyBTCMigrationArtifact(sv, req, intent, signAlgo)
		if err != nil {
			return transitionSignedFailure(txID, err.Error())
		}
	case chainpkg.ChainETH, chainpkg.ChainBNB, chainpkg.ChainBASE, chainpkg.ChainARB, chainpkg.ChainTRX:
		if err := validateEVMMigration(sv, req, intent); err != nil {
			return transitionSignedFailure(txID, err.Error())
		}
	default:
		return transitionSignedFailure(txID, "unsupported migration chain")
	}
	if chainName == chainpkg.ChainBTC {
		externalArtifact, err := storeFrostArtifact(sv, artifact.Metadata{ArtifactID: artifactID, Kind: artifact.KindMigration, Chain: chainName, VaultID: intent.VaultId, Epoch: intent.NewEpoch, Height: req.Base.ExecutedHeight, SourceID: intent.MigrationId, ParentIDs: parentArtifactIDs}, req.RawTx)
		if err != nil {
			return transitionSignedFailure(txID, "archive migration transaction artifact: "+err.Error())
		}
		sv.Set(keys.KeyFrostMigrationArtifact(intent.MigrationId), []byte(externalArtifact.Metadata.ArtifactID))
	}
	sv.Set(keys.KeyFrostMigrationPackageHash(intent.MigrationId), transitionSignedPackageHash(req))

	vault.KeyEpoch = intent.NewEpoch
	vault.GroupPubkey = append([]byte(nil), intent.NewGroupPubkey...)
	vault.SignAlgo = transition.SignAlgo
	vault.Status = VaultStatusActive
	vault.ActiveSinceHeight = req.Base.ExecutedHeight
	vault.NextCommitteeReviewHeight = nextReview
	vault.CommitteeMembers = append([]string(nil), transition.NewCommitteeMembers...)
	vault.CommitteeThreshold = transition.DkgThresholdT
	vault.PendingCommitteeDigest = nil
	vault.PendingCommitteeSinceHeight = 0
	updatedVault, err := proto.Marshal(&vault)
	if err != nil {
		return transitionSignedFailure(txID, "marshal updated vault failed")
	}
	sv.Set(vaultKey, updatedVault)

	migration.Status = migrationStatusSigned
	migration.SignedTxId = txID
	migration.TemplateData = nil
	updatedMigration, err := proto.Marshal(&migration)
	if err != nil {
		return transitionSignedFailure(txID, "marshal migration marker failed")
	}
	sv.Set(migrationKey, updatedMigration)
	if isSupportedAccountSigningChain(chainName) {
		sv.Set(keys.KeyFrostVaultHandoverProof(chainName, intent.VaultId, intent.NewEpoch), append(append([]byte(nil), req.Signature...), guardianSignature...))
	}

	signedIntent := proto.Clone(intent).(*pb.FrostVaultMigrationState)
	signedIntent.Status = migrationStatusSigned
	signedIntent.SignedTxId = txID
	archive := &pb.FrostVaultMigrationArchive{State: signedIntent}
	archive.GuardianSigs = cloneByteSlices(req.GuardianSigs)
	archive.InputSigs = cloneByteSlices(req.InputSigs)
	archive.ScriptPubkeys = cloneByteSlices(req.ScriptPubkeys)
	                                                                           
	                                                                           
	archive.RawTx = nil
	archive.Signature = append([]byte(nil), req.Signature...)
	                                                                           
	                                                                          
	archive.DependencyRawTxs = nil
	archive.Submitter = req.Base.FromAddress
	archive.SignedHeight = req.Base.ExecutedHeight
	updatedArchive, err := proto.Marshal(archive)
	if err != nil {
		return transitionSignedFailure(txID, "marshal migration archive failed")
	}
	if isSupportedAccountSigningChain(chainName) {
		externalArtifact, artifactErr := storeFrostArtifact(sv, artifact.Metadata{
			Kind: artifact.KindMigration, Chain: chainName, VaultID: intent.VaultId,
			Epoch: intent.NewEpoch, Height: req.Base.ExecutedHeight, SourceID: intent.MigrationId,
		}, updatedArchive)
		if artifactErr != nil {
			return transitionSignedFailure(txID, "archive account handover artifact: "+artifactErr.Error())
		}
		sv.Set(keys.KeyFrostMigrationArtifact(intent.MigrationId), []byte(externalArtifact.Metadata.ArtifactID))
	}
	sv.Set(keys.KeyArchiveFrostMigration(intent.MigrationId), updatedArchive)
	sv.Set(keys.KeyFrostPermanentMigration(intent.MigrationId), updatedArchive)

	transition.Lifecycle = VaultLifecycleActive
	transition.MigrationStatus = migrationStatusSigned
	transition.MigrationJobId = intent.MigrationId
	transition.MigrationTemplateHash = append([]byte(nil), intent.TemplateHash...)
	transition.MigrationIntentHash = append([]byte(nil), intent.IntentHash...)
	if err := setTransitionStateWithIndex(sv, transitionKey, &transition); err != nil {
		return transitionSignedFailure(txID, "update transition failed")
	}
	if req.EpochId > 0 {
		prevKey := keys.KeyFrostVaultTransition(chainName, req.OldVaultId, req.EpochId-1)
		if previousData, found, _ := sv.Get(prevKey); found && len(previousData) > 0 {
			var previous pb.VaultTransitionState
			if unmarshalProtoCompat(previousData, &previous) == nil && previous.Lifecycle != VaultLifecycleRetired {
				previous.Lifecycle = VaultLifecycleRetired
				if err := setTransitionStateWithIndex(sv, prevKey, &previous); err != nil {
					return transitionSignedFailure(txID, "retire previous transition failed")
				}
			}
		}
	}
	return nil, &Receipt{TxID: txID, Status: "SUCCEED"}, nil
}

func transitionSignedFailure(txID, message string) ([]WriteOp, *Receipt, error) {
	return nil, &Receipt{TxID: txID, Status: "FAILED", Error: message}, errors.New(message)
}

func transitionSignedPackageHash(req *pb.FrostVaultTransitionSignedTx) []byte {
	if req == nil {
		return nil
	}
	canonical := proto.Clone(req).(*pb.FrostVaultTransitionSignedTx)
	canonical.Base = nil
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(canonical)
	if err != nil {
		return nil
	}
	digest := sha256.Sum256(raw)
	return digest[:]
}

func stringMember(members []string, address string) bool {
	if strings.TrimSpace(address) == "" {
		return false
	}
	for _, member := range members {
		if member == address {
			return true
		}
	}
	return false
}

func validateTransitionSignedEnvelope(req *pb.FrostVaultTransitionSignedTx, hot, intent *pb.FrostVaultMigrationState) error {
	if req == nil || hot == nil || intent == nil {
		return fmt.Errorf("missing migration intent")
	}
	if req.MigrationId != hot.MigrationId || hot.MigrationId != intent.MigrationId ||
		chainpkg.NormalizeChain(req.Chain) != intent.Chain || req.OldVaultId != intent.VaultId ||
		req.NewVaultId != intent.VaultId || req.EpochId != intent.NewEpoch {
		return fmt.Errorf("migration identity mismatch")
	}
	if len(req.Signature) == 0 || !bytes.Equal(req.MsgHash, intent.IntentHash) ||
		!bytes.Equal(req.TemplateHash, intent.TemplateHash) || !bytes.Equal(req.TemplateData, intent.TemplateData) {
		return fmt.Errorf("submitted package differs from finalized migration intent")
	}
	return nil
}

func validateAndApplyBTCMigrationArtifact(sv StateView, req *pb.FrostVaultTransitionSignedTx, intent *pb.FrostVaultMigrationState, signAlgo pb.SignAlgo) ([]string, string, error) {
	if intent.NoFunds {
		if _, err := validateAndApplyBTCMigration(sv, req, intent, signAlgo); err != nil {
			return nil, "", err
		}
		return nil, "", nil
	}
	template, err := btc.FromJSON(intent.TemplateData)
	if err != nil {
		return nil, "", err
	}
	parents, err := frostBTCParentArtifactIDs(sv, intent.VaultId, template.Inputs)
	if err != nil {
		return nil, "", err
	}
	if _, err := validateAndApplyBTCMigration(sv, req, intent, signAlgo); err != nil {
		return nil, "", err
	}
	txid, err := btcSignedTemplateTxID(template, req.InputSigs)
	if err != nil {
		return nil, "", err
	}
	for _, input := range template.Inputs {
		sv.Del(keys.KeyFrostBtcUtxo(intent.VaultId, input.TxID, input.Vout))
		sv.Del(keys.KeyFrostBtcLockedUtxo(intent.VaultId, input.TxID, input.Vout))
		sv.Del(keys.KeyFrostBtcUtxoArtifact(intent.VaultId, input.TxID, input.Vout))
	}
	sv.Set(keys.KeyFrostBtcUtxoArtifact(intent.VaultId, txid, 0), []byte(txid))
	return parents, txid, nil
}

func validateAndApplyBTCMigration(sv StateView, req *pb.FrostVaultTransitionSignedTx, intent *pb.FrostVaultMigrationState, signAlgo pb.SignAlgo) ([][]byte, error) {
	if intent.NoFunds {
		if len(intent.TemplateData) != 0 || len(req.InputSigs) != 0 || len(req.ScriptPubkeys) != 0 || len(req.RawTx) != 0 {
			return nil, fmt.Errorf("empty BTC vault migration must not carry a sweep transaction")
		}
		return nil, nil
	}
	template, err := btc.FromJSON(intent.TemplateData)
	if err != nil || template.VaultID != intent.VaultId || template.KeyEpoch != intent.OldEpoch || len(template.Inputs) == 0 || len(template.Outputs) != 1 {
		return nil, fmt.Errorf("invalid BTC migration template")
	}
	cfg, err := loadMigrationVaultConfig(sv, intent.Chain)
	if err != nil {
		return nil, err
	}
	var utxos []chainpkg.UTXO
	if intent.BtcProtocolVersion == 3 {
		if len(template.Inputs) != 1 || intent.ParentMigrationId == "" || len(intent.BtcOriginId) != 32 {
			return nil, fmt.Errorf("invalid BTC migration child")
		}
		input := template.Inputs[0]
		inputKey := keys.KeyFrostBtcUtxo(intent.VaultId, input.TxID, input.Vout)
		lockKey := keys.KeyFrostBtcLockedUtxo(intent.VaultId, input.TxID, input.Vout)
		state, err := readBTCStateMany(sv, []string{inputKey, lockKey})
		if err != nil {
			return nil, err
		}
		var utxo pb.FrostUtxo
		if proto.Unmarshal(state[inputKey], &utxo) != nil || utxo.Txid != input.TxID || utxo.Vout != input.Vout || utxo.VaultId != intent.VaultId ||
			string(state[lockKey]) != intent.MigrationId || !bytes.Equal(utxo.OriginId, intent.BtcOriginId) {
			return nil, fmt.Errorf("BTC migration child input/lock/origin mismatch")
		}
		amount, err := parseBTCIsolationAmount("migration input", utxo.Amount)
		if err != nil || amount != input.Amount {
			return nil, fmt.Errorf("BTC migration child input amount mismatch")
		}
		utxos = []chainpkg.UTXO{{TxID: utxo.Txid, Vout: utxo.Vout, Amount: amount, ScriptPubKey: utxo.ScriptPubkey, ConfirmHeight: utxo.FinalizeHeight}}
	} else {
		utxos, err = unlockedOldKeyBTCUTXOs(sv, intent.VaultId, intent.OldGroupPubkey, intent.NewGroupPubkey)
		if err != nil {
			return nil, err
		}
	}
	inputPubkeys, err := btcMigrationInputPubkeys(sv, intent.VaultId, intent.OldGroupPubkey, utxos)
	if err != nil {
		return nil, err
	}
	canonical, canonicalTemplate, err := btc.BuildMigrationTemplate(intent.Chain, intent.VaultId, intent.OldEpoch, intent.NewEpoch, utxos, intent.NewGroupPubkey, cfg.BtcNetwork, cfg.BtcFeeSatsPerVbyte, cfg.GuardianPubkey, inputPubkeys)
	if err != nil || !bytes.Equal(canonical.TemplateHash, intent.TemplateHash) || !bytes.Equal(canonical.TemplateData, intent.TemplateData) || !bytes.Equal(canonicalTemplate.Serialize(), template.Serialize()) {
		return nil, fmt.Errorf("BTC migration template no longer matches consensus state")
	}
	if len(req.InputSigs) != len(template.Inputs) || len(req.ScriptPubkeys) != len(template.Inputs) || len(req.GuardianSigs) != len(template.Inputs)+1 {
		return nil, fmt.Errorf("BTC migration input signature/script count mismatch")
	}
	localUTXOs := make([]*pb.FrostUtxo, len(template.Inputs))
	localScripts := make([][]byte, len(template.Inputs))
	inputKeys := make([]string, len(template.Inputs))
	for i, input := range template.Inputs {
		inputKeys[i] = keys.KeyFrostBtcUtxo(intent.VaultId, input.TxID, input.Vout)
	}
	values, err := readBTCStateMany(sv, inputKeys)
	if err != nil {
		return nil, err
	}
	var totalInput uint64
	for i, input := range template.Inputs {
		data := values[inputKeys[i]]
		if len(data) == 0 {
			return nil, fmt.Errorf("BTC migration input disappeared")
		}
		var utxo pb.FrostUtxo
		if err := unmarshalProtoCompat(data, &utxo); err != nil {
			return nil, fmt.Errorf("invalid BTC migration input state")
		}
		amount, err := strconv.ParseUint(strings.TrimSpace(utxo.Amount), 10, 64)
		if err != nil || amount != input.Amount || math.MaxUint64-totalInput < amount {
			return nil, fmt.Errorf("BTC migration input amount mismatch")
		}
		totalInput += amount
		localUTXOs[i] = &utxo
		localScripts[i] = append([]byte(nil), utxo.ScriptPubkey...)
	}
	if !equalByteSlices(req.ScriptPubkeys, localScripts) {
		return nil, fmt.Errorf("BTC migration scripts differ from finalized UTXOs")
	}
	sighashes, err := template.ComputeTaprootSighash(localScripts, btc.SighashDefault)
	if err != nil {
		return nil, fmt.Errorf("compute BTC migration sighashes: %w", err)
	}
	for i, signature := range req.InputSigs {
		if len(signature) != 64 {
			return nil, fmt.Errorf("invalid BTC migration input signature length")
		}
		verifyKey := intent.OldGroupPubkey
		if len(localUTXOs[i].Tweak) == 32 {
			verifyKey, err = utils.ComputeTweakedPubkey(intent.OldGroupPubkey, localUTXOs[i].Tweak)
			if err != nil {
				return nil, fmt.Errorf("invalid BTC migration tweak")
			}
		}
		valid, err := verifySignature(signAlgo, verifyKey, sighashes[i], signature)
		if err != nil || !valid {
			return nil, fmt.Errorf("invalid BTC migration signature for input %d", i)
		}
		guardianValid, guardianErr := verifySignature(signAlgo, cfg.GuardianPubkey, sighashes[i], req.GuardianSigs[i])
		if guardianErr != nil || !guardianValid {
			return nil, fmt.Errorf("invalid BTC guardian migration signature %d", i)
		}
	}
	expectedRaw, err := template.BuildSignedRawTx(req.InputSigs, req.GuardianSigs[:len(req.InputSigs)])
	if err != nil || !bytes.Equal(expectedRaw, req.RawTx) {
		return nil, fmt.Errorf("BTC raw transaction differs from signed template")
	}
	                                                                           
	                                                                   
	var dependencies [][]byte
	outputAmount := template.Outputs[0].Amount
	if totalInput <= outputAmount {
		return nil, fmt.Errorf("invalid BTC migration fee")
	}
	                                                                    
	                                                                     
	                                                                         
	                                                                     
	btcAsset, err := bridgeasset.Resolve(intent.Chain, "")
	if err != nil {
		return nil, fmt.Errorf("resolve BTC migration asset: %w", err)
	}
	available, err := getVaultAvailableBalance(sv, intent.Chain, btcAsset.ID, intent.VaultId)
	if err != nil || available.Cmp(new(big.Int).SetUint64(totalInput)) < 0 {
		return nil, fmt.Errorf("BTC available balance cannot cover migration inputs")
	}
	fee := new(big.Int).SetUint64(totalInput - outputAmount)
	remaining, err := SafeSub(available, fee)
	if err != nil {
		return nil, fmt.Errorf("BTC migration fee exceeds available balance")
	}
	txid, err := btcSignedTemplateTxID(template, req.InputSigs)
	if err != nil {
		return nil, err
	}
	outputScript, err := btc.AddressScriptForNetwork(template.Outputs[0].Address, cfg.BtcNetwork)
	if err != nil {
		return nil, err
	}
	newUTXO := &pb.FrostUtxo{Txid: txid, Vout: 0, Amount: strconv.FormatUint(outputAmount, 10), VaultId: intent.VaultId, FinalizeHeight: req.Base.ExecutedHeight, RequestId: "migration:" + intent.MigrationId, ScriptPubkey: outputScript}
	if intent.BtcProtocolVersion == 3 {
		newUTXO.OriginId, newUTXO.OriginLegacy = append([]byte(nil), localUTXOs[0].OriginId...), localUTXOs[0].OriginLegacy
	} else if active, err := chainpkg.BTCIsolationActive(sv.Get); err != nil {
		return nil, err
	} else if active {
		if len(localUTXOs) == 1 {
			if err := ensureBTCUTXOOrigin(sv, localUTXOs[0], true); err != nil {
				return nil, err
			}
			newUTXO.OriginId, newUTXO.OriginLegacy = append([]byte(nil), localUTXOs[0].OriginId...), localUTXOs[0].OriginLegacy
		} else if err := ensureBTCUTXOOrigin(sv, newUTXO, true); err != nil {
			return nil, err
		}
	}
	newKey := keys.KeyFrostBtcUtxo(intent.VaultId, txid, 0)
	if existing, found, err := sv.Get(newKey); err != nil || found && len(existing) > 0 {
		return nil, fmt.Errorf("BTC migration output already exists")
	}
	newData, err := proto.Marshal(newUTXO)
	if err != nil {
		return nil, err
	}
	seqKey := keys.KeyFrostBtcUtxoFIFOSeq(intent.VaultId)
	seq := readUint64FromState(sv, seqKey)
	if seq == math.MaxUint64 {
		return nil, fmt.Errorf("BTC UTXO sequence overflow")
	}
	seq++

	for _, input := range template.Inputs {
		sv.Set(keys.KeyFrostBtcLockedUtxo(intent.VaultId, input.TxID, input.Vout), []byte(intent.MigrationId))
		delBTCSpendableUTXOIndex(sv, intent.VaultId, input.TxID, input.Vout)
		sv.Del(keys.KeyFrostBtcUtxoAmount(intent.VaultId, input.Amount, input.TxID, input.Vout))
	}
	sv.Set(newKey, newData)
	sv.Set(keys.KeyFrostBtcUtxoFIFOIndex(intent.VaultId, seq), newData)
	if err := setBTCSpendableUTXOIndex(sv, intent.VaultId, seq, newUTXO, newData); err != nil {
		return nil, err
	}
	sv.Set(seqKey, []byte(strconv.FormatUint(seq, 10)))
	if _, found, _ := sv.Get(keys.KeyFrostBtcUtxoFIFOHead(intent.VaultId)); !found {
		sv.Set(keys.KeyFrostBtcUtxoFIFOHead(intent.VaultId), []byte("1"))
	}
	setVaultAvailableBalance(sv, intent.Chain, btcAsset.ID, intent.VaultId, remaining)
	if intent.BtcProtocolVersion != 3 {
		advanceBtcUtxoFIFOHead(sv, intent.VaultId)
	}
	return dependencies, nil
}

func validateEVMMigration(sv StateView, req *pb.FrostVaultTransitionSignedTx, intent *pb.FrostVaultMigrationState) error {
	if intent.NoFunds || len(req.InputSigs) != 0 || len(req.ScriptPubkeys) != 0 || len(req.RawTx) != 0 || len(req.Signature) != evm.SchnorrSigWithYSize {
		return fmt.Errorf("invalid EVM migration package")
	}
	cfg, err := loadMigrationVaultConfig(sv, intent.Chain)
	if err != nil {
		return err
	}
	if int(intent.VaultId) >= len(cfg.VaultRefs) {
		return fmt.Errorf("EVM vault contract not configured")
	}
	if intent.AuthorityProtocol == 3 {
		raw, found, e := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq(intent.Chain, intent.VaultId))
		if e != nil {
			return e
		}
		current := "1"
		if found {
			current = string(raw)
		}
		if current != intent.FrozenNextBatchSeq {
			return fmt.Errorf("account cutoff changed after freeze")
		}
	}
	canonical, _, err := evm.CanonicalMigrationTemplate(cfg, intent)
	if err != nil || !bytes.Equal(canonical.TemplateHash, intent.TemplateHash) || !bytes.Equal(canonical.TemplateData, intent.TemplateData) || len(canonical.SigHashes) != 1 || !bytes.Equal(canonical.SigHashes[0], intent.IntentHash) {
		return fmt.Errorf("EVM migration template differs from consensus state")
	}
	return nil
}

func loadMigrationVaultConfig(sv StateView, chainName string) (*pb.FrostVaultConfig, error) {
	data, found, err := sv.Get(keys.KeyFrostVaultConfig(chainpkg.NormalizeChain(chainName), 0))
	if err != nil || !found || len(data) == 0 {
		return nil, fmt.Errorf("vault config not found")
	}
	var cfg pb.FrostVaultConfig
	if err := unmarshalProtoCompat(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func cloneByteSlices(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}
