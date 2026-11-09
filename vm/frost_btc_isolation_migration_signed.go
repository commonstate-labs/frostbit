package vm

import (
	"bytes"
	"fmt"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func (h *FrostVaultTransitionSignedTxHandler) dryRunBTCIsolationMigrationSigned(req *pb.FrostVaultTransitionSignedTx, transitionKey string, transition *pb.VaultTransitionState, parent *pb.FrostVaultMigrationState, sv StateView) (ops []WriteOp, receipt *Receipt, resultErr error) {
	fail := func(err error) ([]WriteOp, *Receipt, error) {
		return transitionSignedFailure(req.Base.TxId, err.Error())
	}
	active, err := chain.BTCIsolationActive(sv.Get)
	if err != nil {
		return fail(err)
	}
	if !active {
		return fail(fmt.Errorf("BTC v3 isolation is not active"))
	}
	if req.MigrationId == parent.MigrationId {
		return fail(fmt.Errorf("BTC manifest requires independent child packages"))
	}
	raw, found, err := sv.Get(keys.KeyFrostBtcMigrationChildState(req.MigrationId))
	if err != nil {
		return fail(err)
	}
	var child pb.FrostVaultMigrationState
	if !found || proto.Unmarshal(raw, &child) != nil || child.MigrationId != req.MigrationId {
		return fail(fmt.Errorf("BTC migration child not found"))
	}
	if child.Status == migrationStatusSigned {
		hash, exists, err := sv.Get(keys.KeyFrostMigrationPackageHash(child.MigrationId))
		if err != nil {
			return fail(err)
		}
		if !exists || !bytes.Equal(hash, transitionSignedPackageHash(req)) {
			return fail(fmt.Errorf("BTC migration child already signed by another package"))
		}
		return nil, &Receipt{TxID: req.Base.TxId, Status: "SUCCEED"}, nil
	}
	if transition.Lifecycle != VaultLifecycleDraining || child.Status != migrationStatusIntentFinalized {
		return fail(fmt.Errorf("BTC migration is not accepting child signatures"))
	}
	if err := validateBTCIsolationManifestChild(parent, &child, req); err != nil {
		return fail(err)
	}
	childRef, found, err := sv.Get(keys.KeyFrostBtcMigrationChild(parent.MigrationId, child.BtcChildSeq))
	if err != nil {
		return fail(err)
	}
	if !found || string(childRef) != child.MigrationId {
		return fail(fmt.Errorf("BTC migration child manifest index mismatch"))
	}
	cfg, err := loadMigrationVaultConfig(sv, chain.ChainBTC)
	if err != nil {
		return fail(err)
	}
	network := networkChainIDFromView(sv)
	if network == 0 || cfg.DepositDexChainId != network || len(parent.BtcManifestHash) != 32 ||
		!bytes.Equal(chain.BTCMigrationManifestIntentHash(network, cfg.BtcNetwork, parent), parent.IntentHash) {
		return fail(fmt.Errorf("BTC sealed manifest commitment mismatch"))
	}
	childHash := chain.BTCMigrationChildIntentHash(network, parent.MigrationId, child.BtcChildSeq, child.BtcOriginId, child.TemplateHash)
	if !bytes.Equal(childHash, child.IntentHash) || chain.BTCMigrationChildID(parent.MigrationId, child.BtcChildSeq, childHash) != child.MigrationId {
		return fail(fmt.Errorf("BTC migration child commitment mismatch"))
	}
	if len(req.InputSigs) != 1 || len(req.GuardianSigs) != 2 {
		return fail(fmt.Errorf("BTC migration child requires input and manifest dual signatures"))
	}
	for _, item := range []struct{ key, sig []byte }{{parent.OldGroupPubkey, req.Signature}, {cfg.GuardianPubkey, req.GuardianSigs[1]}} {
		valid, err := verifySignatureImpl(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, item.key, parent.IntentHash, item.sig)
		if err != nil || !valid {
			return fail(fmt.Errorf("invalid BTC manifest dual authorization"))
		}
	}
	vaultKey := keys.KeyFrostVaultState(chain.ChainBTC, parent.VaultId)
	vaultRaw, exists, err := sv.Get(vaultKey)
	if err != nil {
		return fail(err)
	}
	var vault pb.FrostVaultState
	if !exists || proto.Unmarshal(vaultRaw, &vault) != nil || vault.KeyEpoch != parent.OldEpoch ||
		!bytes.Equal(vault.GroupPubkey, parent.OldGroupPubkey) || vault.SignAlgo != pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340 {
		return fail(fmt.Errorf("BTC old vault changed after manifest freeze"))
	}
	if parent.BtcSignedChildCount >= parent.BtcChildCount {
		return fail(fmt.Errorf("BTC migration completion counter overflow"))
	}
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil || receipt == nil || receipt.Status != "SUCCEED" {
			_ = sv.Revert(snap)
		}
	}()
	parents, txid, err := validateAndApplyBTCMigrationArtifact(sv, req, &child, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340)
	if err != nil {
		return fail(err)
	}
	if _, err := storeFrostArtifact(sv, artifact.Metadata{ArtifactID: txid, Kind: artifact.KindMigration,
		Chain: chain.ChainBTC, VaultID: child.VaultId, Epoch: child.NewEpoch, Height: req.Base.ExecutedHeight,
		SourceID: child.MigrationId, ParentIDs: parents}, req.RawTx); err != nil {
		return fail(err)
	}
	signedChild := proto.Clone(&child).(*pb.FrostVaultMigrationState)
	signedChild.Status, signedChild.SignedTxId = migrationStatusSigned, req.Base.TxId
	archive := &pb.FrostVaultMigrationArchive{State: signedChild, Signature: req.Signature,
		GuardianSigs: cloneByteSlices(req.GuardianSigs), InputSigs: cloneByteSlices(req.InputSigs), ScriptPubkeys: cloneByteSlices(req.ScriptPubkeys),
		Submitter: req.Base.FromAddress, SignedHeight: req.Base.ExecutedHeight}
	archiveData, err := proto.Marshal(archive)
	if err != nil {
		return fail(err)
	}
	sv.Set(keys.KeyFrostPermanentMigration(child.MigrationId), archiveData)
	sv.Set(keys.KeyArchiveFrostMigration(child.MigrationId), archiveData)
	sv.Set(keys.KeyFrostMigrationArtifact(child.MigrationId), []byte(txid))
	sv.Set(keys.KeyFrostMigrationPackageHash(child.MigrationId), transitionSignedPackageHash(req))
	compact := proto.Clone(signedChild).(*pb.FrostVaultMigrationState)
	compact.TemplateData = nil
	compactData, err := proto.Marshal(compact)
	if err != nil {
		return fail(err)
	}
	sv.Set(keys.KeyFrostBtcMigrationChildState(child.MigrationId), compactData)
	sv.Del(keys.KeyFrostBtcMigrationActiveChild(parent.MigrationId, child.BtcChildSeq))
	parent.BtcSignedChildCount++
	parentArchive := &pb.FrostVaultMigrationArchive{State: parent}
	if parent.BtcSignedChildCount == parent.BtcChildCount {
		parent.Status, parent.SignedTxId = migrationStatusSigned, req.Base.TxId
		parentArchive.Signature, parentArchive.GuardianSigs = req.Signature, [][]byte{append([]byte(nil), req.GuardianSigs[1]...)}
		parentArchive.Submitter, parentArchive.SignedHeight = req.Base.FromAddress, req.Base.ExecutedHeight
		if err := finishBTCIsolationVaultTransition(sv, transitionKey, transition, parent, &vault, req.Base.ExecutedHeight); err != nil {
			return fail(err)
		}
	}
	parentData, err := proto.Marshal(parent)
	if err != nil {
		return fail(err)
	}
	parentArchiveData, err := proto.Marshal(parentArchive)
	if err != nil {
		return fail(err)
	}
	sv.Set(keys.KeyFrostVaultMigration(chain.ChainBTC, parent.VaultId, parent.NewEpoch), parentData)
	sv.Set(keys.KeyFrostPermanentMigration(parent.MigrationId), parentArchiveData)
	if parent.Status == migrationStatusSigned {
		sv.Set(keys.KeyArchiveFrostMigration(parent.MigrationId), parentArchiveData)
	}
	return nil, &Receipt{TxID: req.Base.TxId, Status: "SUCCEED", BlockHeight: req.Base.ExecutedHeight}, nil
}

func finishBTCIsolationVaultTransition(sv StateView, transitionKey string, transition *pb.VaultTransitionState, parent *pb.FrostVaultMigrationState, vault *pb.FrostVaultState, height uint64) error {
	nextReview, err := nextVaultCommitteeReviewHeight(height)
	if err != nil {
		return err
	}
	vault.KeyEpoch, vault.GroupPubkey, vault.SignAlgo, vault.Status = parent.NewEpoch, append([]byte(nil), parent.NewGroupPubkey...), transition.SignAlgo, VaultStatusActive
	vault.ActiveSinceHeight, vault.NextCommitteeReviewHeight = height, nextReview
	vault.CommitteeMembers, vault.CommitteeThreshold = append([]string(nil), transition.NewCommitteeMembers...), transition.DkgThresholdT
	vault.PendingCommitteeDigest, vault.PendingCommitteeSinceHeight = nil, 0
	data, err := proto.Marshal(vault)
	if err != nil {
		return err
	}
	sv.Set(keys.KeyFrostVaultState(chain.ChainBTC, parent.VaultId), data)
	transition.Lifecycle, transition.MigrationStatus, transition.MigrationJobId = VaultLifecycleActive, migrationStatusSigned, parent.MigrationId
	transition.MigrationTemplateHash, transition.MigrationIntentHash = append([]byte(nil), parent.TemplateHash...), append([]byte(nil), parent.IntentHash...)
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		return err
	}
	if parent.NewEpoch > 0 {
		previousKey := keys.KeyFrostVaultTransition(chain.ChainBTC, parent.VaultId, parent.NewEpoch-1)
		data, exists, err := sv.Get(previousKey)
		if err != nil && !isStateViewNotFound(err) {
			return err
		}
		if exists {
			var previous pb.VaultTransitionState
			if proto.Unmarshal(data, &previous) != nil {
				return fmt.Errorf("invalid previous BTC transition")
			}
			if previous.Lifecycle != VaultLifecycleRetired {
				previous.Lifecycle = VaultLifecycleRetired
				if err := setTransitionStateWithIndex(sv, previousKey, &previous); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
