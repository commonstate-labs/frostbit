package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"
)

func TestTransitionActiveIndexKeepsKeyReadyUntilActivation(t *testing.T) {
	sv := NewMockStateView()
	transitionKey := keys.KeyFrostVaultTransition("btc", 1, 2)
	activeKey := keys.KeyFrostVaultTransitionActive("btc", 1, 2)
	transition := &pb.VaultTransitionState{
		Chain: "btc", VaultId: 1, EpochId: 2,
		DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleDraining,
	}

	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("persist KEY_READY+DRAINING: %v", err)
	}
	if value, exists, err := sv.Get(activeKey); err != nil || !exists || string(value) != transitionKey {
		t.Fatalf("KEY_READY+DRAINING lost active index: exists=%v value=%q err=%v", exists, value, err)
	}

	transition.Lifecycle = VaultLifecycleActive
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("persist KEY_READY+ACTIVE: %v", err)
	}
	if _, exists, err := sv.Get(activeKey); err != nil || exists {
		t.Fatalf("KEY_READY+ACTIVE retained active index: exists=%v err=%v", exists, err)
	}

	transition.Lifecycle = VaultLifecycleDraining
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("restore KEY_READY+DRAINING: %v", err)
	}
	transition.Lifecycle = VaultLifecycleRetired
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("persist RETIRED: %v", err)
	}
	if _, exists, err := sv.Get(activeKey); err != nil || exists {
		t.Fatalf("RETIRED transition retained active index: exists=%v err=%v", exists, err)
	}
}

func TestTransitionLifecycleUsesOldKeyToRequireHandover(t *testing.T) {
	cfg := &pb.FrostVaultConfig{ThresholdRatio: 0.67, DkgCommitWindowBlocks: 5, DkgSharingWindowBlocks: 5, DkgDisputeWindowBlocks: 5}
	firstEpoch, _ := buildAutoTransitionState("btc", 0, 1, 1, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, 1, nil, []string{"a", "b", "c", "d"}, nil, cfg)
	if firstEpoch.Lifecycle != VaultLifecycleActive {
		t.Fatalf("first epoch lifecycle=%s want=%s", firstEpoch.Lifecycle, VaultLifecycleActive)
	}
	rotation, _ := buildAutoTransitionState("btc", 0, 2, 1, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, 1, []string{"a", "b", "c", "d"}, []string{"b", "c", "d", "e"}, []byte("old-key"), cfg)
	if rotation.Lifecycle != VaultLifecycleDraining {
		t.Fatalf("rotation lifecycle=%s want=%s", rotation.Lifecycle, VaultLifecycleDraining)
	}
}

func TestHasPendingHandoverProtectsKeyReadyNewCommittee(t *testing.T) {
	sv := NewMockStateView()
	transitionKey := keys.KeyFrostVaultTransition("btc", 0, 2)
	transition := &pb.VaultTransitionState{
		Chain: "btc", VaultId: 0, EpochId: 2,
		DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleDraining,
		OldCommitteeMembers: []string{"old"}, NewCommitteeMembers: []string{"new"},
	}
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("seed transition: %v", err)
	}
	if !hasPendingHandover(sv, "old") || !hasPendingHandover(sv, "new") {
		t.Fatalf("KEY_READY handover did not protect both committees: old=%v new=%v", hasPendingHandover(sv, "old"), hasPendingHandover(sv, "new"))
	}

	transition.Lifecycle = VaultLifecycleActive
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("activate transition: %v", err)
	}
	if hasPendingHandover(sv, "old") || hasPendingHandover(sv, "new") {
		t.Fatal("activated transition still blocks committee exits")
	}
}
