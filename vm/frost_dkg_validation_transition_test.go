package vm

import (
	"bytes"
	"dex/pb"
	"dex/protocol"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestDkgValidationAckTransitionWaitsForEveryRetainedMember(t *testing.T) {
	transition := formalDkgValidationTransition()
	original := proto.Clone(transition).(*pb.VaultTransitionState)
	msgHash := []byte("validation-hash")
	groupKey := []byte("next-group-key")

	result, err := advanceDkgValidationAck(dkgValidationAckTransitionInput{
		transition:        transition,
		ackCount:          1,
		validationMsgHash: msgHash,
		derivedGroupKey:   groupKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.requiredAcks != 3 || result.keyReady {
		t.Fatalf("required=%d keyReady=%t, want 3/false", result.requiredAcks, result.keyReady)
	}
	if result.transition.DkgStatus != DKGStatusSharing || result.transition.ValidationStatus != "SIGNING" {
		t.Fatalf("partial transition=%+v", result.transition)
	}
	if !bytes.Equal(result.transition.ValidationMsgHash, msgHash) {
		t.Fatal("validation hash was not recorded")
	}
	if !proto.Equal(transition, original) {
		t.Fatal("pure ACK transition mutated its input")
	}
}

func TestDkgValidationAckTransitionBecomesReadyAtNNotThreshold(t *testing.T) {
	transition := formalDkgValidationTransition()
	msgHash := []byte("validation-hash")
	groupKey := []byte("next-group-key")

	result, err := advanceDkgValidationAck(dkgValidationAckTransitionInput{
		transition:        transition,
		ackCount:          2,
		validationMsgHash: msgHash,
		derivedGroupKey:   groupKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.keyReady || result.transition.DkgStatus != DKGStatusKeyReady ||
		result.transition.ValidationStatus != "PASSED" {
		t.Fatalf("ready transition=%+v", result.transition)
	}
	if result.transition.DkgThresholdT != 2 {
		t.Fatalf("signing threshold changed to %d", result.transition.DkgThresholdT)
	}
	if !bytes.Equal(result.transition.NewGroupPubkey, groupKey) || result.transition.Lifecycle != VaultLifecycleActive {
		t.Fatalf("key/lifecycle mismatch: key=%q lifecycle=%s", result.transition.NewGroupPubkey, result.transition.Lifecycle)
	}

	                                                                           
	msgHash[0] ^= 0xff
	groupKey[0] ^= 0xff
	if bytes.Equal(result.transition.ValidationMsgHash, msgHash) || bytes.Equal(result.transition.NewGroupPubkey, groupKey) {
		t.Fatal("transition result aliases caller-owned byte slices")
	}
}

func TestDkgValidationAckTransitionRejectsInvalidThreshold(t *testing.T) {
	for _, threshold := range []uint32{0, 4} {
		transition := formalDkgValidationTransition()
		transition.DkgThresholdT = threshold
		if _, err := advanceDkgValidationAck(dkgValidationAckTransitionInput{transition: transition}); err == nil {
			t.Fatalf("threshold %d was accepted", threshold)
		}
	}
}

func TestDkgVaultActivationTransitionActivatesFirstEpoch(t *testing.T) {
	transition := formalDkgValidationTransition()
	transition.DkgStatus = DKGStatusKeyReady
	transition.NewGroupPubkey = []byte("first-key")
	transition.SignAlgo = pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1

	result, err := applyDkgVaultActivation(dkgVaultActivationTransitionInput{
		chain:      "eth",
		vaultID:    9,
		epochID:    1,
		transition: transition,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.writeVault || result.activationDeferred || result.vault.Status != VaultStatusActive ||
		result.vault.KeyEpoch != 1 || !bytes.Equal(result.vault.GroupPubkey, transition.NewGroupPubkey) {
		t.Fatalf("first epoch activation=%+v", result)
	}
	if !proto.Equal(result.vault, &pb.FrostVaultState{
		VaultId:                   9,
		Chain:                     "eth",
		KeyEpoch:                  1,
		GroupPubkey:               []byte("first-key"),
		SignAlgo:                  pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1,
		Status:                    VaultStatusActive,
		CommitteeMembers:          []string{"alice", "bob", "carol"},
		CommitteeThreshold:        transition.DkgThresholdT,
		NextCommitteeReviewHeight: protocol.VaultCommitteeTermBlocks,
	}) {
		t.Fatalf("unexpected vault projection: %+v", result.vault)
	}
}

func TestDkgVaultActivationTransitionDefersRotation(t *testing.T) {
	transition := formalDkgValidationTransition()
	transition.DkgStatus = DKGStatusKeyReady
	transition.OldGroupPubkey = []byte("old-key")
	transition.NewGroupPubkey = []byte("new-key")
	existing := &pb.FrostVaultState{
		VaultId:          9,
		Chain:            "eth",
		KeyEpoch:         3,
		GroupPubkey:      []byte("old-key"),
		Status:           VaultStatusActive,
		CommitteeMembers: []string{"old-member"},
	}
	original := proto.Clone(existing).(*pb.FrostVaultState)

	result, err := applyDkgVaultActivation(dkgVaultActivationTransitionInput{
		chain:      "eth",
		vaultID:    9,
		epochID:    4,
		transition: transition,
		vault:      existing,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.writeVault || !result.activationDeferred || !proto.Equal(result.vault, original) {
		t.Fatalf("rotation projection=%+v", result)
	}
	if !proto.Equal(existing, original) {
		t.Fatal("pure vault transition mutated the active vault")
	}

	existing.GroupPubkey = []byte("other-key")
	if _, err := applyDkgVaultActivation(dkgVaultActivationTransitionInput{
		chain: "eth", vaultID: 9, epochID: 4, transition: transition, vault: existing,
	}); err == nil {
		t.Fatal("conflicting active vault key was accepted")
	}
}

func formalDkgValidationTransition() *pb.VaultTransitionState {
	return &pb.VaultTransitionState{
		Chain:               "eth",
		VaultId:             9,
		EpochId:             1,
		DkgStatus:           DKGStatusSharing,
		DkgThresholdT:       2,
		NewCommitteeMembers: []string{"alice", "bob", "carol"},
	}
}
