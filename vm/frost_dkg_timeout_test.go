package vm

import (
	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"fmt"
	"math/big"
	"sort"
	"testing"

	"google.golang.org/protobuf/proto"
)

type mutableDkgLivenessTracker struct {
	active  map[string]bool
	indices map[string]uint32
}

                                                                           
                                                                   
type initializedDkgDBManager struct{ iface.DBManager }

func (*mutableDkgLivenessTracker) ProcessBlock(iface.LivenessWriteView, uint64, []uint32, []uint32) ([]uint32, []iface.LivenessSlashEvent) {
	return nil, nil
}

func (m *mutableDkgLivenessTracker) RegisterMinerAtHeight(iface.LivenessWriteView, string, uint64, uint64) (uint32, error) {
	return 0, nil
}

func (m *mutableDkgLivenessTracker) RemoveMiner(_ iface.LivenessWriteView, address string, _ uint64) error {
	m.active[address] = false
	return nil
}

func (m *mutableDkgLivenessTracker) UnregisterMiner(_ iface.LivenessWriteView, address string) error {
	delete(m.active, address)
	return nil
}

func (m *mutableDkgLivenessTracker) UnregisterMinersBatch(_ iface.LivenessWriteView, addresses []string) error {
	for _, address := range addresses {
		delete(m.active, address)
	}
	return nil
}

func (m *mutableDkgLivenessTracker) IsActive(address string) bool {
	return m.active[address]
}

func (m *mutableDkgLivenessTracker) IsActiveOrProbation(address string) bool {
	return m.active[address]
}

func (m *mutableDkgLivenessTracker) IsRegistered(address string) bool {
	_, ok := m.active[address]
	return ok
}

func (m *mutableDkgLivenessTracker) GetActiveCount() int {
	return len(m.GetActiveMiners())
}

func (m *mutableDkgLivenessTracker) GetAddressByIndex(index uint32) (string, bool) {
	for address, candidate := range m.indices {
		if candidate == index {
			return address, true
		}
	}
	return "", false
}

func (m *mutableDkgLivenessTracker) GetActiveMiners() []string {
	miners := make([]string, 0, len(m.active))
	for address, active := range m.active {
		if active {
			miners = append(miners, address)
		}
	}
	sort.Strings(miners)
	return miners
}

func (*mutableDkgLivenessTracker) GetMinerState(string) int { return 0 }

func (m *mutableDkgLivenessTracker) GetMinerSnapshot(address string) *iface.LivenessMinerSnapshot {
	if !m.active[address] {
		return nil
	}
	return &iface.LivenessMinerSnapshot{
		Index:        m.indices[address],
		Address:      address,
		State:        1,
		MaturedStake: ^uint64(0),
	}
}

func (m *mutableDkgLivenessTracker) GetMinerIndex(address string) (uint32, bool) {
	index, ok := m.indices[address]
	return index, ok
}

func (*mutableDkgLivenessTracker) SetVaultMaturityBlocks(uint64) {}

func TestProcessFrostDkgTimeoutsRetriesExpiredSharing(t *testing.T) {
	sv := NewMockStateView()
	chain := "btc"
	vaultID := uint32(0)
	epochID := uint64(1)
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	transition := &pb.VaultTransitionState{
		Chain:               chain,
		VaultId:             vaultID,
		EpochId:             epochID,
		SignAlgo:            pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		TriggerHeight:       0,
		OldCommitteeMembers: []string{"a", "b", "c", "d"},
		NewCommitteeMembers: []string{"a", "b", "c", "d"},
		DkgStatus:           DKGStatusSharing,
		DkgSessionId:        "btc_0_1_1",
		DkgThresholdT:       3,
		DkgN:                4,
		DkgCommitDeadline:   4,
		DkgSharingDeadline:  9,
		DkgDisputeDeadline:  14,
		ValidationStatus:    "NOT_STARTED",
		Lifecycle:           VaultLifecycleActive,
		DkgAttempt:          1,
	}
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("seed transition: %v", err)
	}
	topData, err := MarshalProto(testRankedFrostSnapshot([]string{"a", "b", "c", "d", "e"}, 35))
	if err != nil {
		t.Fatalf("seed VaultEligible miners: %v", err)
	}
	sv.Set(keys.KeyFrostActiveMiners(), topData)
	sv.Set(keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, transition.DkgSessionId, "a"), []byte("commit"))
	sv.Set(keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, transition.DkgSessionId, "a", "b"), []byte("share"))
	complaintData, err := MarshalProto(&pb.FrostVaultDkgComplaint{
		Chain:      chain,
		VaultId:    vaultID,
		EpochId:    epochID,
		DealerId:   "a",
		ReceiverId: "b",
		Status:     ComplaintStatusPending,
	})
	if err != nil {
		t.Fatalf("seed complaint: %v", err)
	}
	sv.Set(keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, transition.DkgSessionId, "a", "b"), complaintData)
	sv.Set(keys.KeyFrostVaultDkgValidationAck(chain, vaultID, epochID, transition.DkgSessionId, "b"), []byte("ack"))
	seedDkgMinerForTimeout(t, sv, "a", "0")

	executor := &Executor{}
	executor.processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: 36}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	if got.DkgStatus != DKGStatusNotStarted {
		t.Fatalf("expected retry status %s, got %s", DKGStatusNotStarted, got.DkgStatus)
	}
	if got.DkgAttempt != 2 {
		t.Fatalf("expected attempt 2, got %d", got.DkgAttempt)
	}
	if got.DkgSessionId != "btc_0_1_2" {
		t.Fatalf("expected new session btc_0_1_2, got %s", got.DkgSessionId)
	}
	if got.TriggerHeight != 37 || got.DkgCommitDeadline != 41 || got.DkgSharingDeadline != 46 || got.DkgDisputeDeadline != 51 {
		t.Fatalf("unexpected retry deadlines: trigger=%d commit=%d sharing=%d dispute=%d",
			got.TriggerHeight, got.DkgCommitDeadline, got.DkgSharingDeadline, got.DkgDisputeDeadline)
	}
	activeKey := keys.KeyFrostVaultTransitionActive(chain, vaultID, epochID)
	activeValue, ok, _ := sv.Get(activeKey)
	if !ok || string(activeValue) != transitionKey {
		t.Fatalf("expected active index to point to transition, got ok=%v value=%q", ok, string(activeValue))
	}
	assertNoFrostDkgSessionTempState(t, sv, chain, vaultID, epochID, "btc_0_1_1")
}

func TestProcessFrostDkgTimeoutsMissingCommitmentStartsFreshAttempt(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusCommitting, []string{"a", "b", "c"}, 2)
	ts.NewGroupPubkey = []byte("stale-key")
	ts.ValidationMsgHash = []byte("stale-hash")
	ts.MigrationJobId = "stale-job"
	if err := setTransitionStateWithIndex(sv, transitionKey, ts); err != nil {
		t.Fatalf("update transition: %v", err)
	}
	for _, dealer := range []string{"a", "b"} {
		sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer), []byte("commit"))
	}
	seedMinerLockedBalance(t, sv, "c", "700")

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: ts.DkgCommitDeadline}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	assertFreshDkgRetry(t, got, []string{"a", "b"}, 2, "btc_0_1_2", 6)
	if len(got.NewGroupPubkey) != 0 || len(got.ValidationMsgHash) != 0 || got.MigrationJobId != "" {
		t.Fatalf("fresh attempt retained stale outputs: %+v", got)
	}
	if locked := GetBalance(sv, "c", "FB").MinerLockedBalance; locked != "700" {
		t.Fatalf("missing commitment must not slash stake, got %s", locked)
	}
	assertNoFrostDkgSessionTempState(t, sv, ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId)
}

func TestProcessFrostDkgTimeoutsNotStartedWithoutCommitsFails(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusNotStarted, []string{"a", "b", "c"}, 2)

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: ts.DkgCommitDeadline}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	if got.DkgStatus != DKGStatusFailed || got.Lifecycle != VaultLifecycleDraining || got.DkgN != 0 || len(got.NewCommitteeMembers) != 0 {
		t.Fatalf("expected deterministic draining failure, got status=%s lifecycle=%s n=%d committee=%v", got.DkgStatus, got.Lifecycle, got.DkgN, got.NewCommitteeMembers)
	}
	activeValue, active, _ := sv.Get(keys.KeyFrostVaultTransitionActive(ts.Chain, ts.VaultId, ts.EpochId))
	if !active || string(activeValue) != transitionKey {
		t.Fatalf("draining failure must retain exit-protection index, active=%v value=%q", active, activeValue)
	}
	if shouldAutoRetryFailedDkg(got) {
		t.Fatal("FAILED+DRAINING must not be auto-retried with the unchanged miner set")
	}
	if !hasPendingHandover(sv, "a") {
		t.Fatal("FAILED+DRAINING must retain old-committee exit protection")
	}
}

func TestProcessFrostAutoTransitionsRecoversPendingInitialDkg(t *testing.T) {
	sv := NewMockStateView()
	cfg, vault, ts, transitionKey := seedPendingInitialDkgRecovery(t, sv, 1)
	top := testRankedFrostSnapshot([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, 100)

	firstCommittee, err := pendingInitialDkgRecoveryCommittee(top, cfg, ts, vault.CommitteeMembers)
	if err != nil {
		t.Fatalf("build first recovery committee: %v", err)
	}
	secondCommittee, err := pendingInitialDkgRecoveryCommittee(top, cfg, ts, vault.CommitteeMembers)
	if err != nil {
		t.Fatalf("build second recovery committee: %v", err)
	}
	if !equalStringSlices(firstCommittee, secondCommittee) {
		t.Fatalf("recovery committee is not deterministic: first=%v second=%v", firstCommittee, secondCommittee)
	}
	if len(firstCommittee) < 3 {
		t.Fatalf("recovery committee too small for cumulative exclusion test: %v", firstCommittee)
	}
	failedReplacement := firstCommittee[2]
	laterAttempt := proto.Clone(ts).(*pb.VaultTransitionState)
	laterAttempt.DkgAttempt = 2
	laterAttempt.OldCommitteeMembers = append([]string(nil), firstCommittee...)
	laterAttempt.NewCommitteeMembers = append([]string(nil), firstCommittee[:2]...)
	laterAttempt.NewCommitteeMembers = append(laterAttempt.NewCommitteeMembers, firstCommittee[3:]...)
	laterCommittee, err := pendingInitialDkgRecoveryCommittee(top, cfg, laterAttempt, vault.CommitteeMembers)
	if err != nil {
		t.Fatalf("build later recovery committee: %v", err)
	}
	for _, excluded := range []string{"c", "d", failedReplacement} {
		if isInCommittee(excluded, laterCommittee) {
			t.Fatalf("member excluded by an earlier attempt was reselected: member=%s committee=%v", excluded, laterCommittee)
		}
	}

	oldSessionID := ts.DkgSessionId
	sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, oldSessionID, "a"), []byte("stale-commit"))
	executor := &Executor{DB: &initializedDkgDBManager{}}
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, top, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	if got.DkgStatus != DKGStatusNotStarted || got.Lifecycle != VaultLifecycleActive {
		t.Fatalf("pending initial DKG did not enter a fresh active attempt: status=%s lifecycle=%s", got.DkgStatus, got.Lifecycle)
	}
	if got.DkgAttempt != 2 || got.DkgSessionId != "eth_0_1_2" {
		t.Fatalf("unexpected fresh session: attempt=%d session=%s", got.DkgAttempt, got.DkgSessionId)
	}
	if got.TriggerHeight != 101 || got.DkgCommitDeadline != 120 || got.DkgSharingDeadline != 130 || got.DkgDisputeDeadline != 140 {
		t.Fatalf("unexpected recovery deadlines: trigger=%d commit=%d sharing=%d dispute=%d",
			got.TriggerHeight, got.DkgCommitDeadline, got.DkgSharingDeadline, got.DkgDisputeDeadline)
	}
	if got.DkgN != cfg.CommitteeSize || got.DkgThresholdT != ts.DkgThresholdT {
		t.Fatalf("unexpected recovered quorum: n=%d t=%d", got.DkgN, got.DkgThresholdT)
	}
	if len(got.NewCommitteeMembers) != int(cfg.CommitteeSize) || !equalStringSlices(got.OldCommitteeMembers, got.NewCommitteeMembers) {
		t.Fatalf("fresh attempt must record its complete baseline committee: old=%v new=%v", got.OldCommitteeMembers, got.NewCommitteeMembers)
	}
	if got.NewCommitteeMembers[0] != "a" || got.NewCommitteeMembers[1] != "b" {
		t.Fatalf("eligible survivors were not preserved: %v", got.NewCommitteeMembers)
	}
	if isInCommittee("c", got.NewCommitteeMembers) || isInCommittee("d", got.NewCommitteeMembers) {
		t.Fatalf("members removed from the failed attempt were immediately reselected: %v", got.NewCommitteeMembers)
	}
	if vault.Status != VaultStatusPending || len(vault.GroupPubkey) != 0 {
		t.Fatal("test fixture no longer represents a pending first-generation vault")
	}
	assertNoFrostDkgSessionTempState(t, sv, ts.Chain, ts.VaultId, ts.EpochId, oldSessionID)
	activeValue, active, _ := sv.Get(keys.KeyFrostVaultTransitionActive(ts.Chain, ts.VaultId, ts.EpochId))
	if !active || string(activeValue) != transitionKey {
		t.Fatalf("fresh initial attempt lost active transition index: active=%v value=%q", active, activeValue)
	}
}

func TestProcessFrostAutoTransitionsPendingInitialDkgRemainsFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		attempt uint32
		top     *pb.FrostActiveMiners
	}{
		{
			name:    "insufficient eligible miners",
			attempt: 1,
			top:     testRankedFrostSnapshot([]string{"a", "b", "c", "d"}, 100),
		},
		{
			name:    "restart budget exhausted",
			attempt: maxDkgRestartsPerEpoch + 1,
			top:     testRankedFrostSnapshot([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, 100),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sv := NewMockStateView()
			_, _, ts, transitionKey := seedPendingInitialDkgRecovery(t, sv, test.attempt)
			executor := &Executor{DB: &initializedDkgDBManager{}}
			executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, test.top, sv)

			got := mustLoadTransition(t, sv, transitionKey)
			if got.DkgStatus != DKGStatusFailed || got.Lifecycle != VaultLifecycleDraining {
				t.Fatalf("unsafe recovery from fail-closed state: status=%s lifecycle=%s", got.DkgStatus, got.Lifecycle)
			}
			if got.DkgAttempt != test.attempt || got.DkgSessionId != ts.DkgSessionId || !equalStringSlices(got.NewCommitteeMembers, ts.NewCommitteeMembers) {
				t.Fatalf("fail-closed transition changed: attempt=%d session=%s committee=%v", got.DkgAttempt, got.DkgSessionId, got.NewCommitteeMembers)
			}
		})
	}
}

func TestProcessFrostAutoTransitionsDoesNotRecoverActiveVaultDrainingFailure(t *testing.T) {
	sv := NewMockStateView()
	cfg, vault, _, _ := seedPendingInitialDkgRecovery(t, sv, 1)
	vault.Status = VaultStatusActive
	vault.GroupPubkey = []byte("old-group-key")
	vaultData, err := MarshalProto(vault)
	if err != nil {
		t.Fatalf("marshal active vault: %v", err)
	}
	sv.Set(keys.KeyFrostVaultState(vault.Chain, vault.VaultId), vaultData)

	rotation := &pb.VaultTransitionState{
		Chain: vault.Chain, VaultId: vault.VaultId, EpochId: vault.KeyEpoch + 1,
		SignAlgo: cfg.SignAlgo, DkgAttempt: 1, DkgSessionId: "eth_0_2_1",
		DkgStatus: DKGStatusFailed, ValidationStatus: "FAILED", Lifecycle: VaultLifecycleDraining,
		OldCommitteeMembers: append([]string(nil), vault.CommitteeMembers...),
		NewCommitteeMembers: []string{"a", "b"}, DkgN: 2, DkgThresholdT: 3,
		OldGroupPubkey: append([]byte(nil), vault.GroupPubkey...),
	}
	transitionKey := keys.KeyFrostVaultTransition(rotation.Chain, rotation.VaultId, rotation.EpochId)
	if err := setTransitionStateWithIndex(sv, transitionKey, rotation); err != nil {
		t.Fatalf("seed active-vault rotation: %v", err)
	}
	top := testRankedFrostSnapshot([]string{"a", "b", "c", "d", "e", "f", "g", "h"}, 100)
	executor := &Executor{DB: &initializedDkgDBManager{}}
	executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, top, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	if got.DkgStatus != DKGStatusFailed || got.Lifecycle != VaultLifecycleDraining || got.DkgAttempt != rotation.DkgAttempt || got.DkgSessionId != rotation.DkgSessionId {
		t.Fatalf("ACTIVE rotation semantics changed: status=%s lifecycle=%s attempt=%d session=%s",
			got.DkgStatus, got.Lifecycle, got.DkgAttempt, got.DkgSessionId)
	}
}

func TestProcessFrostDkgTimeoutsGraceRetryLimitDrains(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusResolving, []string{"a", "b", "c"}, 2)
	ts.DkgAttempt = maxDkgRestartsPerEpoch + 1                                              
	ts.DkgSessionId = "btc_0_1_4"
	if err := setTransitionStateWithIndex(sv, transitionKey, ts); err != nil {
		t.Fatalf("update exhausted transition: %v", err)
	}
	sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, "a"), []byte("stale"))

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{
		Height: ts.DkgDisputeDeadline + dkgTimeoutRetryGraceBlocks + 1,
	}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	if got.DkgStatus != DKGStatusFailed || got.Lifecycle != VaultLifecycleDraining || got.ValidationStatus != "FAILED" {
		t.Fatalf("exhausted DKG must drain: status=%s lifecycle=%s validation=%s", got.DkgStatus, got.Lifecycle, got.ValidationStatus)
	}
	if got.DkgAttempt != ts.DkgAttempt || got.DkgSessionId != ts.DkgSessionId {
		t.Fatalf("exhausted DKG unexpectedly restarted: attempt=%d session=%s", got.DkgAttempt, got.DkgSessionId)
	}
	activeValue, active, _ := sv.Get(keys.KeyFrostVaultTransitionActive(ts.Chain, ts.VaultId, ts.EpochId))
	if !active || string(activeValue) != transitionKey {
		t.Fatalf("draining transition lost handover index: active=%v value=%q", active, activeValue)
	}
	assertNoFrostDkgSessionTempState(t, sv, ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId)
}

func TestProcessFrostDkgTimeoutsCommitteeChangeHonorsRetryLimit(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusCommitting, []string{"a", "b", "c"}, 2)
	ts.DkgAttempt = maxDkgRestartsPerEpoch + 1
	ts.DkgSessionId = "btc_0_1_4"
	if err := setTransitionStateWithIndex(sv, transitionKey, ts); err != nil {
		t.Fatalf("update exhausted transition: %v", err)
	}
	for _, dealer := range []string{"a", "b"} {
		sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer), []byte("commit"))
	}

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: ts.DkgCommitDeadline}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	if got.DkgStatus != DKGStatusFailed || got.Lifecycle != VaultLifecycleDraining || got.DkgAttempt != ts.DkgAttempt {
		t.Fatalf("committee-change retry bypassed limit: status=%s lifecycle=%s attempt=%d", got.DkgStatus, got.Lifecycle, got.DkgAttempt)
	}
	if len(got.NewCommitteeMembers) != 2 || got.NewCommitteeMembers[0] != "a" || got.NewCommitteeMembers[1] != "b" {
		t.Fatalf("faulty member was not removed before draining: %v", got.NewCommitteeMembers)
	}
}

func TestProcessFrostDkgTimeoutsMissingCrossShareStartsFreshAttempt(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusSharing, []string{"a", "b", "c"}, 2)
	for _, dealer := range ts.NewCommitteeMembers {
		sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer), []byte("commit"))
		for _, receiver := range ts.NewCommitteeMembers {
			if dealer == receiver || (dealer == "c" && receiver == "a") {
				continue
			}
			sv.Set(keys.KeyFrostVaultDkgShare(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer, receiver), []byte("share"))
		}
	}
	seedMinerLockedBalance(t, sv, "c", "900")

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: ts.DkgSharingDeadline}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	assertFreshDkgRetry(t, got, []string{"a", "b"}, 2, "btc_0_1_2", ts.DkgSharingDeadline+1)
	if locked := GetBalance(sv, "c", "FB").MinerLockedBalance; locked != "900" {
		t.Fatalf("missing share must not slash stake, got %s", locked)
	}
}

func TestProcessFrostDkgTimeoutsMissingValidationAckRemovesUnreadyMember(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusResolving, []string{"a", "b", "c"}, 2)
	ts.ValidationStatus = "SIGNING"
	ts.ValidationMsgHash = []byte("common-validation-hash")
	if err := setTransitionStateWithIndex(sv, transitionKey, ts); err != nil {
		t.Fatalf("update transition: %v", err)
	}
	for _, member := range []string{"a", "b"} {
		sv.Set(keys.KeyFrostVaultDkgValidationAck(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, member), append([]byte(nil), ts.ValidationMsgHash...))
	}
	sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, "a"), []byte("old-transcript"))

	height := ts.DkgDisputeDeadline + dkgTimeoutRetryGraceBlocks + 1
	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: height}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	assertFreshDkgRetry(t, got, []string{"a", "b"}, 2, "btc_0_1_2", height+1)
	if got.ValidationStatus != "NOT_STARTED" || len(got.ValidationMsgHash) != 0 {
		t.Fatalf("fresh attempt retained validation output: status=%s hash=%x", got.ValidationStatus, got.ValidationMsgHash)
	}
	assertNoFrostDkgSessionTempState(t, sv, ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId)
}

func TestProcessFrostDkgTimeoutsSharingDeadlineRechecksCommitments(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusSharing, []string{"a", "b", "c"}, 2)
	for _, dealer := range []string{"a", "b"} {
		sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer), []byte("commit"))
	}
	for _, dealer := range ts.NewCommitteeMembers {
		for _, receiver := range ts.NewCommitteeMembers {
			if dealer == receiver {
				continue
			}
			sv.Set(keys.KeyFrostVaultDkgShare(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer, receiver), []byte("share"))
		}
	}

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: ts.DkgSharingDeadline}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	assertFreshDkgRetry(t, got, []string{"a", "b"}, 2, "btc_0_1_2", ts.DkgSharingDeadline+1)
}

func TestProcessFrostDkgTimeoutsRefundsPendingBondThenRetries(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusResolving, []string{"a", "b", "c"}, 2)
	const bond = "100000000"
	seedAvailableBalance(t, sv, DkgComplaintBondPoolAccount, bond)
	complaint := &pb.FrostVaultDkgComplaint{
		Chain: ts.Chain, VaultId: ts.VaultId, EpochId: ts.EpochId,
		DealerId: "c", ReceiverId: "a", Bond: bond, Status: ComplaintStatusPending,
	}
	complaintData, _ := proto.Marshal(complaint)
	sv.Set(keys.KeyFrostVaultDkgComplaint(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, "c", "a"), complaintData)
	seedDkgMinerForTimeout(t, sv, "c", "500000000")

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: ts.DkgDisputeDeadline}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	assertFreshDkgRetry(t, got, []string{"a", "b"}, 2, "btc_0_1_2", ts.DkgDisputeDeadline+1)
	if available := GetBalance(sv, "a", DkgComplaintBondToken).Balance; available != bond {
		t.Fatalf("pending complaint bond not refunded: got %s want %s", available, bond)
	}
	if pool := GetBalance(sv, DkgComplaintBondPoolAccount, DkgComplaintBondToken).Balance; pool != "0" {
		t.Fatalf("bond pool not debited: got %s", pool)
	}
	if locked := GetBalance(sv, "c", "FB").MinerLockedBalance; locked != "0" {
		t.Fatalf("no-reveal dealer stake not slashed: got %s", locked)
	}
	if slashPool := GetBalance(sv, DkgSlashPoolAccount, "FB").Balance; slashPool != "500000000" {
		t.Fatalf("slash pool balance=%s want=500000000", slashPool)
	}
	minerRaw, _, _ := sv.Get(keys.KeyAccount("c"))
	var miner pb.Account
	if err := proto.Unmarshal(minerRaw, &miner); err != nil || miner.IsMiner {
		t.Fatalf("timed-out dealer must lose miner flag, is_miner=%v err=%v", miner.IsMiner, err)
	}
	assertNoFrostDkgSessionTempState(t, sv, ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId)
}

func TestDkgTimeoutThenLivenessRemovesOnlySlashedMinerInSameBlock(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusResolving, []string{"a", "b", "c", "d"}, 2)
	const bond = "100000000"
	seedAvailableBalance(t, sv, DkgComplaintBondPoolAccount, bond)
	complaintData, err := proto.Marshal(&pb.FrostVaultDkgComplaint{
		Chain: ts.Chain, VaultId: ts.VaultId, EpochId: ts.EpochId,
		DealerId: "c", ReceiverId: "a", Bond: bond, Status: ComplaintStatusPending,
	})
	if err != nil {
		t.Fatalf("marshal complaint: %v", err)
	}
	sv.Set(keys.KeyFrostVaultDkgComplaint(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, "c", "a"), complaintData)

	for index, member := range []string{"a", "b", "c", "d"} {
		account := &pb.Account{
			Address: member, Index: uint64(index + 1), IsMiner: true,
			PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
				int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): {byte(index + 1)},
			}},
		}
		accountData, marshalErr := proto.Marshal(account)
		if marshalErr != nil {
			t.Fatalf("marshal miner %s: %v", member, marshalErr)
		}
		sv.Set(keys.KeyAccount(member), accountData)
	}
	minStake := new(big.Int).SetUint64(protocol.MinMinerStakeRaw)
	seedMinerLockedBalance(t, sv, "a", minStake.String())
	seedMinerLockedBalance(t, sv, "b", minStake.String())
	seedMinerLockedBalance(t, sv, "c", new(big.Int).Mul(new(big.Int).Set(minStake), big.NewInt(3)).String())
	seedMinerLockedBalance(t, sv, "d", new(big.Int).Mul(new(big.Int).Set(minStake), big.NewInt(2)).String())

	staleTop, _ := proto.Marshal(testGenesisFrostSnapshot([]string{"a", "b", "c", "d"}))
	sv.Set(keys.KeyFrostActiveMiners(), staleTop)
	sv.Set(keys.KeyTotalMinerStake(), []byte(new(big.Int).Mul(new(big.Int).Set(minStake), big.NewInt(7)).String()))
	tracker := &mutableDkgLivenessTracker{
		active:  map[string]bool{"a": true, "b": true, "c": true, "d": true},
		indices: map[string]uint32{"a": 1, "b": 2, "c": 3, "d": 4},
	}
	executor := &Executor{LivenessTracker: tracker}
	block := &pb.Block{Header: &pb.BlockHeader{Height: ts.DkgDisputeDeadline}}

	                                                                             
	executor.processFrostDkgTimeouts(block, sv)
	executor.processLivenessHooks(block, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	assertFreshDkgRetry(t, got, []string{"a", "b", "d"}, 2, "btc_0_1_2", ts.DkgDisputeDeadline+1)
	if tracker.IsActiveOrProbation("c") || !tracker.IsActiveOrProbation("d") {
		t.Fatalf("independent miner status is wrong: c=%v d=%v", tracker.IsActiveOrProbation("c"), tracker.IsActiveOrProbation("d"))
	}
	for _, member := range []string{"c"} {
		raw, exists, getErr := sv.Get(keys.KeyAccount(member))
		if getErr != nil || !exists {
			t.Fatalf("load account %s: exists=%v err=%v", member, exists, getErr)
		}
		var account pb.Account
		if unmarshalErr := proto.Unmarshal(raw, &account); unmarshalErr != nil || account.IsMiner {
			t.Fatalf("slashed miner %s was not demoted: is_miner=%v err=%v", member, account.IsMiner, unmarshalErr)
		}
	}
	dRaw, _, _ := sv.Get(keys.KeyAccount("d"))
	var dAccount pb.Account
	if err := proto.Unmarshal(dRaw, &dAccount); err != nil || !dAccount.IsMiner {
		t.Fatalf("independent miner d was incorrectly demoted: is_miner=%v err=%v", dAccount.IsMiner, err)
	}

	topRaw, exists, getErr := sv.Get(keys.KeyFrostActiveMiners())
	if getErr != nil || !exists {
		t.Fatalf("load active miner snapshot: exists=%v err=%v", exists, getErr)
	}
	var top pb.FrostActiveMiners
	if err := proto.Unmarshal(topRaw, &top); err != nil {
		t.Fatalf("unmarshal active miner snapshot: %v", err)
	}
	if len(top.Addresses) != 3 || top.Addresses[0] != "d" || top.Addresses[1] != "a" || top.Addresses[2] != "b" {
		t.Fatalf("active miner snapshot is wrong after independent slash: %v", top.Addresses)
	}
	totalStake, exists, getErr := sv.Get(keys.KeyTotalMinerStake())
	wantTotalStake := new(big.Int).Mul(new(big.Int).Set(minStake), big.NewInt(4)).String()
	if getErr != nil || !exists || string(totalStake) != wantTotalStake {
		t.Fatalf("total miner stake not refreshed: exists=%v value=%q err=%v", exists, totalStake, getErr)
	}
}

func TestProcessFrostDkgTimeoutsRefundFailureKeepsSessionPending(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusResolving, []string{"a", "b", "c"}, 2)
	complaintKey := keys.KeyFrostVaultDkgComplaint(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, "c", "a")
	complaintData, _ := proto.Marshal(&pb.FrostVaultDkgComplaint{
		Chain: ts.Chain, VaultId: ts.VaultId, EpochId: ts.EpochId,
		DealerId: "c", ReceiverId: "a", Bond: "100000000", Status: ComplaintStatusPending,
	})
	sv.Set(complaintKey, complaintData)
	seedDkgMinerForTimeout(t, sv, "c", "500000000")

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: ts.DkgDisputeDeadline}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	if got.DkgSessionId != ts.DkgSessionId || got.DkgAttempt != ts.DkgAttempt || !isInCommittee("c", got.NewCommitteeMembers) {
		t.Fatalf("refund failure must preserve attempt, got session=%s attempt=%d committee=%v", got.DkgSessionId, got.DkgAttempt, got.NewCommitteeMembers)
	}
	raw, exists, _ := sv.Get(complaintKey)
	if !exists {
		t.Fatal("pending complaint was cleaned despite refund failure")
	}
	var stored pb.FrostVaultDkgComplaint
	if err := proto.Unmarshal(raw, &stored); err != nil || stored.Status != ComplaintStatusPending {
		t.Fatalf("complaint must remain pending, status=%s err=%v", stored.Status, err)
	}
	if locked := GetBalance(sv, "c", "FB").MinerLockedBalance; locked != "500000000" {
		t.Fatalf("refund failure must not partially slash dealer, got %s", locked)
	}
	if slashPool := GetBalance(sv, DkgSlashPoolAccount, "FB").Balance; slashPool != "0" {
		t.Fatalf("refund failure mutated slash pool: %s", slashPool)
	}
}

func TestProcessFrostDkgTimeoutsSlashPreflightFailureKeepsSessionPending(t *testing.T) {
	sv := NewMockStateView()
	ts, transitionKey := seedDkgDeadlineTransition(t, sv, DKGStatusResolving, []string{"a", "b", "c"}, 2)
	complaintKey := keys.KeyFrostVaultDkgComplaint(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, "c", "a")
	complaintData, _ := proto.Marshal(&pb.FrostVaultDkgComplaint{
		Chain: ts.Chain, VaultId: ts.VaultId, EpochId: ts.EpochId,
		DealerId: "c", ReceiverId: "a", Bond: "100000000", Status: ComplaintStatusPending,
	})
	sv.Set(complaintKey, complaintData)
	seedAvailableBalance(t, sv, DkgComplaintBondPoolAccount, "100000000")
	seedAvailableBalance(t, sv, DkgSlashPoolAccount, MaxUint256.String())
	seedDkgMinerForTimeout(t, sv, "c", "1")

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: ts.DkgDisputeDeadline}}, sv)

	got := mustLoadTransition(t, sv, transitionKey)
	if got.DkgSessionId != ts.DkgSessionId || !isInCommittee("c", got.NewCommitteeMembers) {
		t.Fatalf("slash preflight failure changed attempt: session=%s committee=%v", got.DkgSessionId, got.NewCommitteeMembers)
	}
	raw, exists, _ := sv.Get(complaintKey)
	var stored pb.FrostVaultDkgComplaint
	if !exists || proto.Unmarshal(raw, &stored) != nil || stored.Status != ComplaintStatusPending {
		t.Fatalf("complaint must stay pending after slash preflight failure: exists=%v status=%s", exists, stored.Status)
	}
	if got := GetBalance(sv, "a", DkgComplaintBondToken).Balance; got != "0" {
		t.Fatalf("bond refunded before slash preflight succeeded: %s", got)
	}
	if got := GetBalance(sv, DkgComplaintBondPoolAccount, DkgComplaintBondToken).Balance; got != "100000000" {
		t.Fatalf("bond pool changed after slash preflight failure: %s", got)
	}
	if got := GetBalance(sv, "c", "FB").MinerLockedBalance; got != "1" {
		t.Fatalf("dealer stake changed after slash preflight failure: %s", got)
	}
}

func seedPendingInitialDkgRecovery(
	t *testing.T,
	sv StateView,
	attempt uint32,
) (*pb.FrostVaultConfig, *pb.FrostVaultState, *pb.VaultTransitionState, string) {
	t.Helper()
	cfg := &pb.FrostVaultConfig{
		Chain:                         "eth",
		SignAlgo:                      pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		VaultCount:                    1,
		CommitteeSize:                 4,
		ThresholdRatio:                0.75,
		VaultMaturityBlocks:           30,
		CommitteeTermBlocks:           protocol.VaultCommitteeTermBlocks,
		CommitteeScaleMinerMultiplier: 1,
		TransitionConfirmBlocks:       10,
		CommitteeMaxSize:              100,
		DkgCommitWindowBlocks:         20,
		DkgSharingWindowBlocks:        10,
		DkgDisputeWindowBlocks:        10,
		TransitionTriggerRatio:        0.2,
	}
	cfgData, err := MarshalProto(cfg)
	if err != nil {
		t.Fatalf("marshal vault config: %v", err)
	}
	sv.Set(keys.KeyFrostVaultConfig(cfg.Chain, 0), cfgData)

	vault := &pb.FrostVaultState{
		Chain:            cfg.Chain,
		VaultId:          0,
		KeyEpoch:         1,
		SignAlgo:         cfg.SignAlgo,
		Status:           VaultStatusPending,
		CommitteeMembers: []string{"a", "b", "c", "d"},
	}
	vaultData, err := MarshalProto(vault)
	if err != nil {
		t.Fatalf("marshal pending vault: %v", err)
	}
	sv.Set(keys.KeyFrostVaultState(vault.Chain, vault.VaultId), vaultData)

	ts := &pb.VaultTransitionState{
		Chain:               vault.Chain,
		VaultId:             vault.VaultId,
		EpochId:             vault.KeyEpoch,
		SignAlgo:            vault.SignAlgo,
		TriggerHeight:       1,
		OldCommitteeMembers: append([]string(nil), vault.CommitteeMembers...),
		NewCommitteeMembers: []string{"a", "b"},
		DkgStatus:           DKGStatusFailed,
		DkgSessionId:        fmt.Sprintf("eth_0_1_%d", attempt),
		DkgThresholdT:       3,
		DkgN:                2,
		DkgCommitDeadline:   5,
		DkgSharingDeadline:  10,
		DkgDisputeDeadline:  15,
		ValidationStatus:    "FAILED",
		Lifecycle:           VaultLifecycleDraining,
		DkgAttempt:          attempt,
	}
	transitionKey := keys.KeyFrostVaultTransition(ts.Chain, ts.VaultId, ts.EpochId)
	if err := setTransitionStateWithIndex(sv, transitionKey, ts); err != nil {
		t.Fatalf("seed pending initial DKG failure: %v", err)
	}
	return cfg, vault, ts, transitionKey
}

func seedDkgDeadlineTransition(t *testing.T, sv StateView, status string, committee []string, threshold uint32) (*pb.VaultTransitionState, string) {
	t.Helper()
	ts := &pb.VaultTransitionState{
		Chain: "btc", VaultId: 0, EpochId: 1,
		TriggerHeight: 1, DkgAttempt: 1, DkgSessionId: "btc_0_1_1",
		DkgStatus: status, ValidationStatus: "NOT_STARTED",
		OldCommitteeMembers: append([]string(nil), committee...),
		NewCommitteeMembers: append([]string(nil), committee...),
		DkgN:                uint32(len(committee)), DkgThresholdT: threshold,
		DkgCommitDeadline: 5, DkgSharingDeadline: 10, DkgDisputeDeadline: 15,
	}
	key := keys.KeyFrostVaultTransition(ts.Chain, ts.VaultId, ts.EpochId)
	if err := setTransitionStateWithIndex(sv, key, ts); err != nil {
		t.Fatalf("seed transition: %v", err)
	}
	return ts, key
}

func seedAvailableBalance(t *testing.T, sv StateView, account, amount string) {
	t.Helper()
	value, ok := new(big.Int).SetString(amount, 10)
	if !ok {
		t.Fatalf("invalid amount %q", amount)
	}
	bal := GetBalance(sv, account, DkgComplaintBondToken)
	bal.Balance = value.String()
	SetBalance(sv, account, DkgComplaintBondToken, bal)
}

func seedMinerLockedBalance(t *testing.T, sv StateView, account, amount string) {
	t.Helper()
	bal := GetBalance(sv, account, "FB")
	bal.MinerLockedBalance = amount
	SetBalance(sv, account, "FB", bal)
}

func seedDkgMinerForTimeout(t *testing.T, sv StateView, account, amount string) {
	t.Helper()
	accountData, err := proto.Marshal(&pb.Account{Address: account, IsMiner: true})
	if err != nil {
		t.Fatalf("marshal miner: %v", err)
	}
	sv.Set(keys.KeyAccount(account), accountData)
	seedMinerLockedBalance(t, sv, account, amount)
}

func assertFreshDkgRetry(t *testing.T, got *pb.VaultTransitionState, committee []string, attempt uint32, sessionID string, triggerHeight uint64) {
	t.Helper()
	if got.DkgStatus != DKGStatusNotStarted || got.DkgAttempt != attempt || got.DkgSessionId != sessionID || got.TriggerHeight != triggerHeight {
		t.Fatalf("unexpected fresh retry: status=%s attempt=%d session=%s trigger=%d", got.DkgStatus, got.DkgAttempt, got.DkgSessionId, got.TriggerHeight)
	}
	if got.DkgThresholdT != 2 || got.DkgN != uint32(len(committee)) {
		t.Fatalf("threshold changed or n stale: t=%d n=%d", got.DkgThresholdT, got.DkgN)
	}
	if len(got.NewCommitteeMembers) != len(committee) {
		t.Fatalf("committee=%v want=%v", got.NewCommitteeMembers, committee)
	}
	for i := range committee {
		if got.NewCommitteeMembers[i] != committee[i] {
			t.Fatalf("committee=%v want=%v", got.NewCommitteeMembers, committee)
		}
	}
}
