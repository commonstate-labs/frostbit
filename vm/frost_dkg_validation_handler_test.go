package vm

import (
	"bytes"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"fmt"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestFrostVaultDkgValidationSignedRejectsMismatchedGroupPubkey(t *testing.T) {
	sv := NewMockStateView()
	signers := newValidationSigners(t, 2)
	committee := signerAddresses(signers)

	chain := "btc"
	vaultID := uint32(7)
	epochID := uint64(3)
	signAlgo := pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340

	_, sessionID, expectedPubkey := seedDKGValidationState(t, sv, chain, vaultID, epochID, committee, signAlgo, 2)

	grp := curve.NewSecp256k1Group()
	wrongPubkey := grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(9)))
	if bytes.Equal(wrongPubkey, expectedPubkey) {
		t.Fatalf("wrong pubkey unexpectedly equals expected pubkey")
	}

	msgHash := computeDkgValidationMsgHash(chain, vaultID, epochID, signAlgo, wrongPubkey)
	tx := buildValidationTx(t, signers[0], chain, vaultID, epochID, sessionID, 30, wrongPubkey, msgHash)

	h := &FrostVaultDkgValidationSignedTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("DryRun returned error: %v", err)
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if receipt.Error != "group_pubkey mismatch with commitments" {
		t.Fatalf("unexpected receipt error: %s", receipt.Error)
	}
}

func TestFrostVaultDkgValidationSignedRequiresThresholdAcks(t *testing.T) {
	sv := NewMockStateView()
	signers := newValidationSigners(t, 2)
	committee := signerAddresses(signers)

	chain := "btc"
	vaultID := uint32(8)
	epochID := uint64(4)
	signAlgo := pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340

	transitionKey, sessionID, expectedPubkey := seedDKGValidationState(t, sv, chain, vaultID, epochID, committee, signAlgo, 2)
	msgHash := computeDkgValidationMsgHash(chain, vaultID, epochID, signAlgo, expectedPubkey)

	h := &FrostVaultDkgValidationSignedTxHandler{}

	tx1 := buildValidationTx(t, signers[0], chain, vaultID, epochID, sessionID, 30, expectedPubkey, msgHash)
	_, receipt1, err := h.DryRun(tx1, sv)
	if err != nil {
		t.Fatalf("first DryRun returned error: %v", err)
	}
	if receipt1 == nil || receipt1.Status != "SUCCEED" {
		t.Fatalf("expected first receipt SUCCEED, got %+v", receipt1)
	}

	transitionAfterFirst := mustLoadTransition(t, sv, transitionKey)
	if transitionAfterFirst.DkgStatus != DKGStatusSharing {
		t.Fatalf("expected dkg_status to remain SHARING after first ack, got %s", transitionAfterFirst.DkgStatus)
	}
	if transitionAfterFirst.ValidationStatus != "SIGNING" {
		t.Fatalf("expected validation_status SIGNING after first ack, got %s", transitionAfterFirst.ValidationStatus)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostVaultDkgValidationAck(chain, vaultID, epochID, sessionID, signers[0].Address)); !exists {
		t.Fatal("expected first validation ack to be stored")
	}
	shareData, err := proto.Marshal(&pb.FrostVaultDkgShare{Chain: chain, VaultId: vaultID, EpochId: epochID, DealerId: committee[0], ReceiverId: committee[1], Ciphertext: []byte("cipher")})
	if err != nil {
		t.Fatalf("marshal share: %v", err)
	}
	sv.Set(keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, committee[0], committee[1]), shareData)
	complaintData, err := proto.Marshal(&pb.FrostVaultDkgComplaint{Chain: chain, VaultId: vaultID, EpochId: epochID, DealerId: committee[0], ReceiverId: committee[1], Status: ComplaintStatusResolved})
	if err != nil {
		t.Fatalf("marshal complaint: %v", err)
	}
	sv.Set(keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, committee[0], committee[1]), complaintData)

	tx2 := buildValidationTx(t, signers[1], chain, vaultID, epochID, sessionID, 31, expectedPubkey, msgHash)
	_, receipt2, err := h.DryRun(tx2, sv)
	if err != nil {
		t.Fatalf("second DryRun returned error: %v", err)
	}
	if receipt2 == nil || receipt2.Status != "SUCCEED" {
		t.Fatalf("expected second receipt SUCCEED, got %+v", receipt2)
	}

	updatedTransition := mustLoadTransition(t, sv, transitionKey)
	if updatedTransition.DkgStatus != DKGStatusKeyReady {
		t.Fatalf("expected dkg_status KEY_READY, got %s", updatedTransition.DkgStatus)
	}
	if updatedTransition.ValidationStatus != "PASSED" {
		t.Fatalf("expected validation_status PASSED, got %s", updatedTransition.ValidationStatus)
	}
	if !bytes.Equal(updatedTransition.NewGroupPubkey, expectedPubkey) {
		t.Fatalf("transition new_group_pubkey mismatch")
	}

	vaultKey := keys.KeyFrostVaultState(chain, vaultID)
	vaultRaw, exists, _ := sv.Get(vaultKey)
	if !exists || len(vaultRaw) == 0 {
		t.Fatalf("vault state missing")
	}
	var vault pb.FrostVaultState
	if err := proto.Unmarshal(vaultRaw, &vault); err != nil {
		t.Fatalf("unmarshal vault state: %v", err)
	}
	if !bytes.Equal(vault.GroupPubkey, expectedPubkey) {
		t.Fatalf("vault group_pubkey mismatch")
	}
	assertNoFrostDkgSessionTempState(t, sv, chain, vaultID, epochID, sessionID)
}

func TestFrostVaultDkgValidationWaitsForEveryRetainedMember(t *testing.T) {
	sv := NewMockStateView()
	signers := newValidationSigners(t, 3)
	committee := signerAddresses(signers)

	const (
		chain   = "btc"
		vaultID = uint32(28)
		epochID = uint64(9)
	)
	signAlgo := pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
	transitionKey, sessionID, expectedPubkey := seedDKGValidationState(t, sv, chain, vaultID, epochID, committee, signAlgo, 2)
	msgHash := computeDkgValidationMsgHash(chain, vaultID, epochID, signAlgo, expectedPubkey)
	h := &FrostVaultDkgValidationSignedTxHandler{}

	for i := 0; i < 2; i++ {
		tx := buildValidationTx(t, signers[i], chain, vaultID, epochID, sessionID, uint64(30+i), expectedPubkey, msgHash)
		_, receipt, err := h.DryRun(tx, sv)
		if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
			t.Fatalf("validation ack %d failed: receipt=%+v err=%v", i+1, receipt, err)
		}
	}

	beforeAllReady := mustLoadTransition(t, sv, transitionKey)
	if beforeAllReady.DkgStatus == DKGStatusKeyReady {
		t.Fatal("t acknowledgements finalized an n-member DKG before every retained member was durable-ready")
	}
	if _, exists, err := sv.Get(keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, committee[0])); err != nil || !exists {
		t.Fatalf("transcript was cleaned before all members acknowledged: exists=%v err=%v", exists, err)
	}

	tx := buildValidationTx(t, signers[2], chain, vaultID, epochID, sessionID, 32, expectedPubkey, msgHash)
	_, receipt, err := h.DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("final validation ack failed: receipt=%+v err=%v", receipt, err)
	}
	if got := mustLoadTransition(t, sv, transitionKey); got.DkgStatus != DKGStatusKeyReady {
		t.Fatalf("all retained members acknowledged but status=%s", got.DkgStatus)
	}
	assertNoFrostDkgSessionTempState(t, sv, chain, vaultID, epochID, sessionID)
}

func TestFrostVaultDkgValidationKeepsActiveVaultKeyUntilTransition(t *testing.T) {
	sv := NewMockStateView()
	signers := newValidationSigners(t, 2)
	committee := signerAddresses(signers)
	const (
		chain   = "btc"
		vaultID = uint32(18)
		epochID = uint64(4)
	)
	signAlgo := pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
	transitionKey, sessionID, nextPubkey := seedDKGValidationState(t, sv, chain, vaultID, epochID, committee, signAlgo, 2)

	grp := curve.NewSecp256k1Group()
	oldPubkey := grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(7)))
	transition := mustLoadTransition(t, sv, transitionKey)
	transition.OldGroupPubkey = append([]byte(nil), oldPubkey...)
	transitionData, err := proto.Marshal(transition)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(transitionKey, transitionData)
	oldVault := &pb.FrostVaultState{
		VaultId:          vaultID,
		Chain:            chain,
		KeyEpoch:         epochID - 1,
		GroupPubkey:      append([]byte(nil), oldPubkey...),
		SignAlgo:         signAlgo,
		Status:           VaultStatusActive,
		CommitteeMembers: []string{"old-member"},
	}
	oldVaultData, err := proto.Marshal(oldVault)
	if err != nil {
		t.Fatal(err)
	}
	vaultKey := keys.KeyFrostVaultState(chain, vaultID)
	sv.Set(vaultKey, oldVaultData)

	msgHash := computeDkgValidationMsgHash(chain, vaultID, epochID, signAlgo, nextPubkey)
	h := &FrostVaultDkgValidationSignedTxHandler{}
	for i, signer := range signers {
		tx := buildValidationTx(t, signer, chain, vaultID, epochID, sessionID, uint64(30+i), nextPubkey, msgHash)
		_, receipt, err := h.DryRun(tx, sv)
		if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
			t.Fatalf("validation ack %d failed: receipt=%+v err=%v", i+1, receipt, err)
		}
	}

	readyTransition := mustLoadTransition(t, sv, transitionKey)
	if readyTransition.DkgStatus != DKGStatusKeyReady || !bytes.Equal(readyTransition.NewGroupPubkey, nextPubkey) {
		t.Fatalf("next key was not retained in transition: status=%s", readyTransition.DkgStatus)
	}
	vaultRaw, exists, err := sv.Get(vaultKey)
	if err != nil || !exists {
		t.Fatalf("load active vault: exists=%v err=%v", exists, err)
	}
	var unchanged pb.FrostVaultState
	if err := proto.Unmarshal(vaultRaw, &unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged.KeyEpoch != epochID-1 || unchanged.Status != VaultStatusActive || !bytes.Equal(unchanged.GroupPubkey, oldPubkey) {
		t.Fatalf("DKG validation activated the new key early: epoch=%d status=%s", unchanged.KeyEpoch, unchanged.Status)
	}
}

func TestFrostVaultDkgValidationSignedRejectsBeforeDisputeDeadline(t *testing.T) {
	sv := NewMockStateView()
	signers := newValidationSigners(t, 2)
	committee := signerAddresses(signers)

	chain := "btc"
	vaultID := uint32(9)
	epochID := uint64(5)
	signAlgo := pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340

	_, sessionID, expectedPubkey := seedDKGValidationState(t, sv, chain, vaultID, epochID, committee, signAlgo, 2)
	msgHash := computeDkgValidationMsgHash(chain, vaultID, epochID, signAlgo, expectedPubkey)
	tx := buildValidationTx(t, signers[0], chain, vaultID, epochID, sessionID, 20, expectedPubkey, msgHash)

	h := &FrostVaultDkgValidationSignedTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("DryRun returned error: %v", err)
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if receipt.Error != "dispute window not closed (height 20 <= deadline 20)" {
		t.Fatalf("unexpected receipt error: %s", receipt.Error)
	}
}

func TestFrostVaultDkgValidationSignedRejectsInvalidAckSignature(t *testing.T) {
	sv := NewMockStateView()
	signers := newValidationSigners(t, 3)
	committee := signerAddresses(signers[:2])

	chain := "btc"
	vaultID := uint32(10)
	epochID := uint64(6)
	signAlgo := pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340

	_, sessionID, expectedPubkey := seedDKGValidationState(t, sv, chain, vaultID, epochID, committee, signAlgo, 2)
	msgHash := computeDkgValidationMsgHash(chain, vaultID, epochID, signAlgo, expectedPubkey)
	tx := buildValidationTx(t, signers[0], chain, vaultID, epochID, sessionID, 30, expectedPubkey, msgHash)
	tx.GetFrostVaultDkgValidationSignedTx().Signature = mustSign(t, signers[2].Key, msgHash)

	h := &FrostVaultDkgValidationSignedTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("DryRun returned error: %v", err)
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if receipt.Error != "invalid validation signature" {
		t.Fatalf("unexpected receipt error: %s", receipt.Error)
	}
}

func TestFrostVaultDkgValidationSignedRejectsPendingComplaint(t *testing.T) {
	sv := NewMockStateView()
	signers := newValidationSigners(t, 2)
	committee := signerAddresses(signers)

	chain := "btc"
	vaultID := uint32(11)
	epochID := uint64(7)
	signAlgo := pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340

	_, sessionID, expectedPubkey := seedDKGValidationState(t, sv, chain, vaultID, epochID, committee, signAlgo, 2)
	msgHash := computeDkgValidationMsgHash(chain, vaultID, epochID, signAlgo, expectedPubkey)
	tx := buildValidationTx(t, signers[0], chain, vaultID, epochID, sessionID, 30, expectedPubkey, msgHash)

	complaint := &pb.FrostVaultDkgComplaint{
		Chain:      chain,
		VaultId:    vaultID,
		EpochId:    epochID,
		DealerId:   committee[0],
		ReceiverId: committee[1],
		Status:     ComplaintStatusPending,
	}
	complaintData, err := proto.Marshal(complaint)
	if err != nil {
		t.Fatalf("marshal complaint: %v", err)
	}
	sv.Set(keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, committee[0], committee[1]), complaintData)

	h := &FrostVaultDkgValidationSignedTxHandler{}
	_, receipt, err := h.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("DryRun returned error: %v", err)
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expected FAILED receipt, got %+v", receipt)
	}
	if receipt.Error != "pending complaints not resolved" {
		t.Fatalf("unexpected receipt error: %s", receipt.Error)
	}
}

type validationSigner struct {
	Address string
	Key     *utils.KeyManager
}

func newValidationSigners(t *testing.T, n int) []validationSigner {
	t.Helper()
	signers := make([]validationSigner, 0, n)
	for i := 0; i < n; i++ {
		km := utils.NewKeyManager()
		if err := km.InitKeyRandom(); err != nil {
			t.Fatalf("InitKeyRandom: %v", err)
		}
		signers = append(signers, validationSigner{
			Address: km.GetAddress(),
			Key:     km,
		})
	}
	return signers
}

func signerAddresses(signers []validationSigner) []string {
	out := make([]string, 0, len(signers))
	for _, signer := range signers {
		out = append(out, signer.Address)
	}
	return out
}

func buildValidationTx(t *testing.T, signer validationSigner, chain string, vaultID uint32, epochID uint64, sessionID string, height uint64, groupPubkey, msgHash []byte) *pb.AnyTx {
	t.Helper()
	return &pb.AnyTx{
		Content: &pb.AnyTx_FrostVaultDkgValidationSignedTx{
			FrostVaultDkgValidationSignedTx: &pb.FrostVaultDkgValidationSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_dkg_validation_" + signer.Address,
					FromAddress:    signer.Address,
					ExecutedHeight: height,
					PublicKeys: &pb.PublicKeys{
						Keys: map[int32][]byte{
							int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): signer.Key.PublicKeyBytes(),
						},
					},
				},
				Chain:          chain,
				VaultId:        vaultID,
				EpochId:        epochID,
				SessionId:      sessionID,
				MsgHash:        msgHash,
				NewGroupPubkey: groupPubkey,
				Signature:      mustSign(t, signer.Key, msgHash),
			},
		},
	}
}

func mustSign(t *testing.T, km *utils.KeyManager, digest []byte) []byte {
	t.Helper()
	sig, err := km.Sign(digest)
	if err != nil {
		t.Fatalf("sign digest: %v", err)
	}
	return sig
}

func mustLoadTransition(t *testing.T, sv *MockStateView, transitionKey string) *pb.VaultTransitionState {
	t.Helper()
	transitionRaw, exists, _ := sv.Get(transitionKey)
	if !exists || len(transitionRaw) == 0 {
		t.Fatalf("transition missing")
	}
	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(transitionRaw, &transition); err != nil {
		t.Fatalf("unmarshal transition: %v", err)
	}
	return &transition
}

func seedDKGValidationState(t *testing.T, sv *MockStateView, chain string, vaultID uint32, epochID uint64, committee []string, signAlgo pb.SignAlgo, threshold uint32) (string, string, []byte) {
	t.Helper()
	sessionID := fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, 1)

	grp := curve.NewSecp256k1Group()
	var expectedPoint curve.Point
	hasExpectedPoint := false
	commitmentPoints := make([][]byte, len(committee))
	for i := range committee {
		point := grp.ScalarBaseMult(big.NewInt(int64(i + 2)))
		commitmentPoints[i] = grp.SerializePoint(point)
		if !hasExpectedPoint {
			expectedPoint = point
			hasExpectedPoint = true
		} else {
			expectedPoint = grp.Add(expectedPoint, point)
		}
	}
	expectedPubkey := grp.SerializePoint(expectedPoint)

	transition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		SignAlgo:            signAlgo,
		DkgStatus:           DKGStatusSharing,
		DkgSessionId:        sessionID,
		DkgAttempt:          1,
		DkgSharingDeadline:  10,
		DkgDisputeDeadline:  20,
		DkgThresholdT:       threshold,
		NewCommitteeMembers: committee,
		OldGroupPubkey:      []byte{0x01},
	}
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transitionData, err := proto.Marshal(transition)
	if err != nil {
		t.Fatalf("marshal transition: %v", err)
	}
	sv.Set(transitionKey, transitionData)

	for i, member := range committee {
		commitment := &pb.FrostVaultDkgCommitment{
			Chain: chain, VaultId: vaultID, EpochId: epochID,
			MinerAddress: member, SignAlgo: signAlgo, AI0: commitmentPoints[i],
		}
		commitmentData, err := proto.Marshal(commitment)
		if err != nil {
			t.Fatalf("marshal commitment %d: %v", i, err)
		}
		sv.Set(keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, member), commitmentData)
	}

	return transitionKey, sessionID, expectedPubkey
}

func assertNoFrostDkgSessionTempState(t *testing.T, sv StateView, chain string, vaultID uint32, epochID uint64, sessionID string) {
	t.Helper()
	for _, item := range frostDkgSessionCleanupPrefixes(chain, vaultID, epochID, sessionID) {
		kvs, err := sv.Scan(item.prefix)
		if err != nil {
			t.Fatalf("scan %s: %v", item.prefix, err)
		}
		if len(kvs) != 0 {
			t.Fatalf("expected no DKG temp keys under %s, got %d", item.prefix, len(kvs))
		}
	}
}
