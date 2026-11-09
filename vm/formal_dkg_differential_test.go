package vm

import (
	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/keys"
	"dex/pb"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
)

type formalDkgGhostState struct {
	commits              map[string]struct{}
	shares               map[string]struct{}
	unresolvedComplaints map[string]struct{}
	acks                 map[string]struct{}
	tempSession          string
	abstractAttempt      int64
}

func TestFormalDkgAckITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_DKG")
	if path == "" {
		t.Skip("DEX_FORMAL_ITF_DKG is set by formal/run-go-differential.ps1")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint ITF trace: %v", err)
	}
	if len(trace.States) != 4 {
		t.Fatalf("DKG differential trace states=%d want=4", len(trace.States))
	}

	sv := NewMockStateView()
	signers := newValidationSigners(t, 3)
	committee := signerAddresses(signers)
	const (
		chain   = "btc"
		vaultID = uint32(128)
		epochID = uint64(2)
	)
	transitionKey, sessionID, groupPubkey := seedDKGValidationState(
		t, sv, chain, vaultID, epochID, committee,
		pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, 2,
	)
	seedFormalDkgCompleteShares(t, sv, chain, vaultID, epochID, sessionID, committee)
	msgHash := computeDkgValidationMsgHash(
		chain, vaultID, epochID,
		pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		groupPubkey,
	)
	memberSigner := map[string]int{"Alice": 0, "Bob": 1, "Carol": 2}
	memberTags := map[string]string{
		committee[0]: "Alice",
		committee[1]: "Bob",
		committee[2]: "Carol",
	}
	ghost := formalDkgGhostState{
		commits:              map[string]struct{}{"Alice": {}, "Bob": {}, "Carol": {}},
		shares:               map[string]struct{}{"Alice": {}, "Bob": {}, "Carol": {}},
		unresolvedComplaints: map[string]struct{}{},
		acks:                 map[string]struct{}{},
		tempSession:          "Session1",
	}
	handler := &FrostVaultDkgValidationSignedTxHandler{}
	assertFormalDkgStateRelation(
		t, trace.States[0], sv, transitionKey,
		chain, vaultID, epochID, sessionID, committee, memberTags, ghost,
	)

	for i := 1; i < len(trace.States); i++ {
		persistedBefore := cloneFormalDkgMockStateView(sv)
		before := mustFormalSet(t, trace.States[i-1], "validationAcks")
		after := mustFormalSet(t, trace.States[i], "validationAcks")
		added := oneAddedFormalMember(t, before, after)
		signerIndex, ok := memberSigner[added]
		if !ok {
			t.Fatalf("unknown model member %q", added)
		}
		modelStatus := mustFormalTag(t, trace.States[i], "status")
		pureBefore := mustLoadTransition(t, sv, transitionKey)
		pureStep, err := advanceDkgValidationAck(dkgValidationAckTransitionInput{
			transition:        pureBefore,
			ackCount:          len(before),
			validationMsgHash: msgHash,
			derivedGroupKey:   groupPubkey,
		})
		if err != nil {
			t.Fatalf("pure production transition rejected model ACK %s: %v", added, err)
		}
		expectedStatus := DKGStatusSharing
		if modelStatus == "KeyReady" {
			expectedStatus = DKGStatusKeyReady
		}
		if pureStep.transition.DkgStatus != expectedStatus {
			t.Fatalf("after %s: model status=%s pure production status=%s",
				added, modelStatus, pureStep.transition.DkgStatus)
		}

		tx := buildValidationTx(
			t, signers[signerIndex], chain, vaultID, epochID, sessionID,
			uint64(40+i), groupPubkey, msgHash,
		)
		ops, receipt, err := handler.DryRun(tx, sv)
		if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
			t.Fatalf("production DKG ack %s failed: receipt=%+v err=%v", added, receipt, err)
		}

		                                                                          
		                                                                    
		                                                        
		failedRestart := cloneFormalDkgMockStateView(persistedBefore)
		assertFormalDkgStateRelation(
			t, trace.States[i-1], failedRestart, transitionKey,
			chain, vaultID, epochID, sessionID, committee, memberTags, ghost,
		)

		production := mustLoadTransition(t, sv, transitionKey)
		if production.DkgStatus != expectedStatus {
			t.Fatalf("after %s: model status=%s production status=%s",
				added, modelStatus, production.DkgStatus)
		}
		ghost.acks[added] = struct{}{}
		restarted := cloneFormalDkgMockStateView(persistedBefore)
		applyFormalDkgOps(restarted, ops)
		assertFormalDkgStateRelation(
			t, trace.States[i], restarted, transitionKey,
			chain, vaultID, epochID, sessionID, committee, memberTags, ghost,
		)
		sv = restarted

		if modelStatus != "KeyReady" {
			ackCount, err := countDkgValidationAcks(sv, chain, vaultID, epochID, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if ackCount != len(after) {
				t.Fatalf("after %s: model acks=%d production acks=%d", added, len(after), ackCount)
			}
		}
	}

	                                                                       
	                                                           
	assertNoFrostDkgSessionTempState(t, sv, chain, vaultID, epochID, sessionID)
	if _, exists, err := sv.Get(keys.KeyFrostVaultDkgValidationAck(chain, vaultID, epochID, sessionID, committee[0])); err != nil || exists {
		t.Fatalf("production retained temporary ACK after KEY_READY: exists=%v err=%v", exists, err)
	}
}

func TestFormalDkgComplaintITFDifferential(t *testing.T) {
	cases := []struct {
		name              string
		env               string
		productionAttempt uint32
		abstractAttempt   int64
	}{
		{name: "retry-with-fresh-session", env: "DEX_FORMAL_ITF_DKG_COMPLAINT_RETRY", productionAttempt: 1},
		{name: "retry-limit-fails-closed", env: "DEX_FORMAL_ITF_DKG_COMPLAINT_FAIL", productionAttempt: maxDkgRestartsPerEpoch + 1, abstractAttempt: 2},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("DKG complaint ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalDkgComplaintTrace(t, os.Getenv(tc.env), tc.productionAttempt, tc.abstractAttempt)
		})
	}
}

func runFormalDkgComplaintTrace(t *testing.T, path string, productionAttempt uint32, abstractAttempt int64) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint DKG complaint trace: %v", err)
	}
	if len(trace.States) != 2 {
		t.Fatalf("DKG complaint trace states=%d want=2", len(trace.States))
	}

	seed := NewMockStateView()
	const (
		chain   = "btc"
		vaultID = uint32(129)
		epochID = uint64(2)
		height  = uint64(30)
	)
	committee := []string{"formal-alice", "formal-bob", "formal-carol"}
	memberTags := map[string]string{
		committee[0]: "Alice",
		committee[1]: "Bob",
		committee[2]: "Carol",
	}
	transitionKey, oldSessionID := seedFormalDkgComplaintAttempt(
		t, seed, chain, vaultID, epochID, productionAttempt, height, committee,
	)
	ghost := formalDkgGhostState{
		commits:              map[string]struct{}{"Alice": {}, "Bob": {}, "Carol": {}},
		shares:               map[string]struct{}{"Alice": {}},
		unresolvedComplaints: map[string]struct{}{"Alice": {}},
		acks:                 map[string]struct{}{},
		tempSession:          formalDkgSessionTag(uint32(mustFormalInt(t, trace.States[0], "attempt"))),
		abstractAttempt:      abstractAttempt,
	}
	assertFormalDkgStateRelation(
		t, trace.States[0], seed, transitionKey,
		chain, vaultID, epochID, oldSessionID, committee, memberTags, ghost,
	)

	                                                                          
	                                                                          
	working := newMemoryStateView(cloneFormalDkgData(seed.data))
	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: height}}, working)
	diff := working.Diff()
	if len(diff) == 0 {
		t.Fatal("complaint adjudication produced no persistence write set")
	}
	failedRestart := cloneFormalDkgMockStateView(seed)
	assertFormalDkgStateRelation(
		t, trace.States[0], failedRestart, transitionKey,
		chain, vaultID, epochID, oldSessionID, committee, memberTags, ghost,
	)

	persisted := cloneFormalDkgMockStateView(seed)
	applyFormalDkgOps(persisted, diff)
	ghost.commits = map[string]struct{}{}
	ghost.shares = map[string]struct{}{}
	ghost.unresolvedComplaints = map[string]struct{}{}
	ghost.acks = map[string]struct{}{}
	ghost.tempSession = "Session2"
	updated := mustLoadTransition(t, persisted, transitionKey)
	assertFormalDkgStateRelation(
		t, trace.States[1], persisted, transitionKey,
		chain, vaultID, epochID, updated.DkgSessionId, committee, memberTags, ghost,
	)
	assertNoFrostDkgSessionTempState(t, persisted, chain, vaultID, epochID, oldSessionID)

	                                                                       
	                                                                 
	restarted := newMemoryStateView(cloneFormalDkgData(persisted.data))
	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: height}}, restarted)
	if restartDiff := restarted.Diff(); len(restartDiff) != 0 {
		t.Fatalf("restart replay mutated completed DKG branch: %d ops", len(restartDiff))
	}
	assertFormalDkgStateRelation(
		t, trace.States[1], restarted, transitionKey,
		chain, vaultID, epochID, updated.DkgSessionId, committee, memberTags, ghost,
	)
}

func TestFormalDkgClosureITFDifferential(t *testing.T) {
	type closureCase struct {
		name    string
		env     string
		vaultID uint32
		status  string
		height  uint64
		seed    func(t *testing.T, sv StateView, ts *pb.VaultTransitionState, committee []string)
	}
	cases := []closureCase{
		{
			name: "missing-commit", env: "DEX_FORMAL_ITF_DKG_MISSING_COMMIT", vaultID: 131,
			status: DKGStatusCommitting, height: 10,
			seed: func(t *testing.T, sv StateView, ts *pb.VaultTransitionState, committee []string) {
				for _, dealer := range committee[:2] {
					sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer), []byte("formal-commit"))
				}
			},
		},
		{
			name: "missing-share", env: "DEX_FORMAL_ITF_DKG_MISSING_SHARE", vaultID: 132,
			status: DKGStatusSharing, height: 20,
			seed: func(t *testing.T, sv StateView, ts *pb.VaultTransitionState, committee []string) {
				for _, dealer := range committee {
					sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer), []byte("formal-commit"))
				}
				for _, dealer := range committee[:2] {
					seedFormalDkgDealerShares(t, sv, ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer, committee)
				}
			},
		},
		{
			name: "missing-durable-ack", env: "DEX_FORMAL_ITF_DKG_MISSING_ACK", vaultID: 133,
			status: DKGStatusSharing, height: 36,
			seed: func(t *testing.T, sv StateView, ts *pb.VaultTransitionState, committee []string) {
				for _, dealer := range committee {
					sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer), []byte("formal-commit"))
				}
				seedFormalDkgCompleteShares(t, sv, ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, committee)
				ts.ValidationMsgHash = []byte("formal-validation-hash")
				for _, member := range committee[:2] {
					sv.Set(keys.KeyFrostVaultDkgValidationAck(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, member), append([]byte(nil), ts.ValidationMsgHash...))
				}
			},
		},
		{
			name: "insufficient-committee", env: "DEX_FORMAL_ITF_DKG_INSUFFICIENT", vaultID: 134,
			status: DKGStatusResolving, height: 30,
			seed: func(t *testing.T, sv StateView, ts *pb.VaultTransitionState, committee []string) {
				for _, dealer := range committee {
					sv.Set(keys.KeyFrostVaultDkgCommit(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer), []byte("formal-commit"))
				}
				for _, dealer := range committee[:2] {
					seedFormalDkgDealerShares(t, sv, ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer, committee)
					complaint := &pb.FrostVaultDkgComplaint{
						Chain: ts.Chain, VaultId: ts.VaultId, EpochId: ts.EpochId,
						DealerId: dealer, ReceiverId: committee[2], Status: ComplaintStatusDisqualified,
					}
					data, err := proto.Marshal(complaint)
					if err != nil {
						t.Fatalf("marshal formal insufficient-committee complaint: %v", err)
					}
					sv.Set(keys.KeyFrostVaultDkgComplaint(ts.Chain, ts.VaultId, ts.EpochId, ts.DkgSessionId, dealer, committee[2]), data)
				}
			},
		},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("DKG closure ITF paths are set by formal/run-go-differential.ps1")
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace, err := itftrace.Load(os.Getenv(tc.env))
			if err != nil {
				t.Fatalf("load Quint DKG closure trace: %v", err)
			}
			if len(trace.States) != 2 {
				t.Fatalf("DKG closure trace states=%d want=2", len(trace.States))
			}

			seed := NewMockStateView()
			committee := []string{"formal-alice", "formal-bob", "formal-carol"}
			memberTags := map[string]string{committee[0]: "Alice", committee[1]: "Bob", committee[2]: "Carol"}
			transition := &pb.VaultTransitionState{
				Chain: "btc", VaultId: tc.vaultID, EpochId: 2,
				SignAlgo:      pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
				TriggerHeight: 1, DkgCommitDeadline: 10, DkgSharingDeadline: 20, DkgDisputeDeadline: 30,
				DkgStatus: tc.status, Lifecycle: VaultLifecycleActive,
				DkgSessionId: fmt.Sprintf("btc_%d_2_1", tc.vaultID), DkgAttempt: 1,
				DkgThresholdT: 2, DkgN: uint32(len(committee)),
				OldCommitteeMembers: append([]string(nil), committee...),
				NewCommitteeMembers: append([]string(nil), committee...),
			}
			tc.seed(t, seed, transition, committee)
			transitionKey := keys.KeyFrostVaultTransition(transition.Chain, transition.VaultId, transition.EpochId)
			if err := setTransitionStateWithIndex(seed, transitionKey, transition); err != nil {
				t.Fatalf("seed formal DKG closure transition: %v", err)
			}

			preGhost := formalDkgGhostFromModel(t, trace.States[0])
			assertFormalDkgStateRelation(
				t, trace.States[0], seed, transitionKey,
				transition.Chain, transition.VaultId, transition.EpochId, transition.DkgSessionId,
				committee, memberTags, preGhost,
			)

			working := newMemoryStateView(cloneFormalDkgData(seed.data))
			(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: tc.height}}, working)
			diff := working.Diff()
			if len(diff) == 0 {
				t.Fatal("DKG closure produced no persistence write set")
			}

			failedRestart := cloneFormalDkgMockStateView(seed)
			assertFormalDkgStateRelation(
				t, trace.States[0], failedRestart, transitionKey,
				transition.Chain, transition.VaultId, transition.EpochId, transition.DkgSessionId,
				committee, memberTags, preGhost,
			)

			persisted := cloneFormalDkgMockStateView(seed)
			applyFormalDkgOps(persisted, diff)
			updated := mustLoadTransition(t, persisted, transitionKey)
			postGhost := formalDkgGhostFromModel(t, trace.States[1])
			assertFormalDkgStateRelation(
				t, trace.States[1], persisted, transitionKey,
				transition.Chain, transition.VaultId, transition.EpochId, updated.DkgSessionId,
				committee, memberTags, postGhost,
			)
			assertNoFrostDkgSessionTempState(t, persisted, transition.Chain, transition.VaultId, transition.EpochId, transition.DkgSessionId)

			restarted := newMemoryStateView(cloneFormalDkgData(persisted.data))
			(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: tc.height}}, restarted)
			if restartDiff := restarted.Diff(); len(restartDiff) != 0 {
				t.Fatalf("restart replay mutated completed DKG closure: %d ops", len(restartDiff))
			}
		})
	}
}

func TestFormalDkgRejectedMessageITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_DKG_REJECTED")
	if path == "" {
		t.Skip("DKG rejected-message ITF path is set by formal/run-go-differential.ps1")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint DKG rejected-message trace: %v", err)
	}
	if len(trace.States) != 2 {
		t.Fatalf("rejected-message model trace states=%d want=2", len(trace.States))
	}
	beforeModel, err := refinement.DkgFromITF(trace.States[0])
	if err != nil {
		t.Fatal(err)
	}
	afterModel, err := refinement.DkgFromITF(trace.States[1])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeModel, afterModel) {
		t.Fatal("rejected-message model trace must stutter")
	}

	const (
		chain   = "btc"
		vaultID = uint32(135)
		epochID = uint64(2)
	)
	signers := newValidationSigners(t, 3)
	committee := signerAddresses(signers)
	memberTags := map[string]string{committee[0]: "Alice", committee[1]: "Bob", committee[2]: "Carol"}
	sv := NewMockStateView()
	transitionKey, sessionID, groupPubkey := seedDKGValidationState(
		t, sv, chain, vaultID, epochID, committee,
		pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, 2,
	)
	seedFormalDkgCompleteShares(t, sv, chain, vaultID, epochID, sessionID, committee)
	msgHash := computeDkgValidationMsgHash(chain, vaultID, epochID, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, groupPubkey)
	ackKey := keys.KeyFrostVaultDkgValidationAck(chain, vaultID, epochID, sessionID, committee[0])
	sv.Set(ackKey, append([]byte(nil), msgHash...))
	ghost := formalDkgGhostFromModel(t, trace.States[0])
	assertFormalDkgStateRelation(t, trace.States[0], sv, transitionKey, chain, vaultID, epochID, sessionID, committee, memberTags, ghost)

	handler := &FrostVaultDkgValidationSignedTxHandler{}
	duplicate := buildValidationTx(t, signers[0], chain, vaultID, epochID, sessionID, 40, groupPubkey, msgHash)
	before := cloneFormalDkgData(sv.data)
	ops, receipt, err := handler.DryRun(duplicate, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(ops) != 0 || !reflect.DeepEqual(before, sv.data) {
		t.Fatalf("duplicate ACK did not stutter: receipt=%+v ops=%d err=%v", receipt, len(ops), err)
	}

	sv.Set(ackKey, []byte("conflicting-ack"))
	before = cloneFormalDkgData(sv.data)
	conflict := buildValidationTx(t, signers[0], chain, vaultID, epochID, sessionID, 41, groupPubkey, msgHash)
	ops, receipt, err = handler.DryRun(conflict, sv)
	if err != nil || receipt == nil || receipt.Status != "FAILED" || receipt.Error != "validation ack conflict" || len(ops) != 0 || !reflect.DeepEqual(before, sv.data) {
		t.Fatalf("conflicting ACK did not fail closed: receipt=%+v ops=%d err=%v", receipt, len(ops), err)
	}

	                                                                         
	                                                                          
	                          
	malformedKey := keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, committee[0], committee[1])
	sv.Set(malformedKey, []byte{0xff, 0x00, 0x80})
	before = cloneFormalDkgData(sv.data)
	malformedBase := cloneFormalDkgData(sv.data)
	working := newMemoryStateView(malformedBase)
	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: 30}}, working)
	if diff := working.Diff(); len(diff) != 0 {
		t.Fatalf("malformed complaint produced partial DKG diff: %d ops", len(diff))
	}
	if !reflect.DeepEqual(before, malformedBase) {
		t.Fatal("malformed complaint changed durable pre-state")
	}
}

func TestFormalDkgCleanupScanFailurePreservesAttempt(t *testing.T) {
	seed := NewMockStateView()
	const (
		chain   = "btc"
		vaultID = uint32(130)
		epochID = uint64(2)
		height  = uint64(30)
	)
	committee := []string{"formal-alice", "formal-bob", "formal-carol"}
	transitionKey, sessionID := seedFormalDkgComplaintAttempt(t, seed, chain, vaultID, epochID, 1, height, committee)
	transitionBefore := append([]byte(nil), seed.data[transitionKey]...)
	tempCountsBefore := make(map[string]int)
	for _, item := range frostDkgSessionCleanupPrefixes(chain, vaultID, epochID, sessionID) {
		rows, err := seed.Scan(item.prefix)
		if err != nil {
			t.Fatalf("scan seeded %s state: %v", item.category, err)
		}
		tempCountsBefore[item.prefix] = len(rows)
	}
	working := newMemoryStateView(cloneFormalDkgData(seed.data))
	failing := &formalDkgScanFailureStateView{
		StateView:  working,
		failPrefix: keys.KeyFrostVaultDkgSharePrefix(chain, vaultID, epochID, sessionID),
	}

	(&Executor{}).processFrostDkgTimeouts(&pb.Block{Header: &pb.BlockHeader{Height: height}}, failing)
	if diff := working.Diff(); len(diff) != 0 {
		t.Fatalf("cleanup scan failure produced partial persistence diff: %d ops", len(diff))
	}
	transitionAfter, exists, err := working.Get(transitionKey)
	if err != nil || !exists || !reflect.DeepEqual(transitionAfter, transitionBefore) {
		t.Fatalf("cleanup scan failure changed transition: exists=%t err=%v", exists, err)
	}
	for _, item := range frostDkgSessionCleanupPrefixes(chain, vaultID, epochID, sessionID) {
		rows, scanErr := working.Scan(item.prefix)
		if scanErr != nil || len(rows) != tempCountsBefore[item.prefix] {
			t.Fatalf("cleanup scan failure changed %s state: rows=%d want=%d err=%v",
				item.category, len(rows), tempCountsBefore[item.prefix], scanErr)
		}
	}
}

type formalDkgScanFailureStateView struct {
	StateView
	failPrefix string
}

func (v *formalDkgScanFailureStateView) Scan(prefix string) (map[string][]byte, error) {
	if prefix == v.failPrefix {
		return nil, errors.New("injected DKG cleanup scan failure")
	}
	return v.StateView.Scan(prefix)
}

func seedFormalDkgComplaintAttempt(
	t *testing.T,
	sv StateView,
	chain string,
	vaultID uint32,
	epochID uint64,
	attempt uint32,
	disputeDeadline uint64,
	committee []string,
) (string, string) {
	t.Helper()
	sessionID := fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, attempt)
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		SignAlgo:      pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		TriggerHeight: 1, DkgCommitDeadline: 10, DkgSharingDeadline: 20, DkgDisputeDeadline: disputeDeadline,
		DkgStatus: DKGStatusResolving, Lifecycle: VaultLifecycleDraining,
		DkgSessionId: sessionID, DkgAttempt: attempt, DkgThresholdT: 2, DkgN: uint32(len(committee)),
		NewCommitteeMembers: append([]string(nil), committee...), OldGroupPubkey: []byte{1},
	}
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epochID)
	if err := setTransitionStateWithIndex(sv, transitionKey, transition); err != nil {
		t.Fatalf("seed formal complaint transition: %v", err)
	}
	for _, member := range committee {
		sv.Set(keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, member), []byte("formal-commit"))
	}
	seedFormalDkgDealerShares(t, sv, chain, vaultID, epochID, sessionID, committee[0], committee)
	complaint := &pb.FrostVaultDkgComplaint{
		Chain: chain, VaultId: vaultID, EpochId: epochID,
		DealerId: committee[0], ReceiverId: committee[1], Status: ComplaintStatusDisqualified,
	}
	data, err := proto.Marshal(complaint)
	if err != nil {
		t.Fatalf("marshal formal DKG complaint: %v", err)
	}
	sv.Set(keys.KeyFrostVaultDkgComplaint(chain, vaultID, epochID, sessionID, committee[0], committee[1]), data)
	return transitionKey, sessionID
}

func seedFormalDkgCompleteShares(t *testing.T, sv StateView, chain string, vaultID uint32, epochID uint64, sessionID string, committee []string) {
	t.Helper()
	for _, dealer := range committee {
		seedFormalDkgDealerShares(t, sv, chain, vaultID, epochID, sessionID, dealer, committee)
	}
}

func seedFormalDkgDealerShares(t *testing.T, sv StateView, chain string, vaultID uint32, epochID uint64, sessionID, dealer string, committee []string) {
	t.Helper()
	for _, receiver := range committee {
		if dealer == receiver {
			continue
		}
		share := &pb.FrostVaultDkgShare{
			Chain: chain, VaultId: vaultID, EpochId: epochID,
			DealerId: dealer, ReceiverId: receiver, Ciphertext: []byte("formal-share"),
		}
		data, err := proto.Marshal(share)
		if err != nil {
			t.Fatalf("marshal formal DKG share %s->%s: %v", dealer, receiver, err)
		}
		sv.Set(keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, dealer, receiver), data)
	}
}

func cloneFormalDkgMockStateView(src *MockStateView) *MockStateView {
	dst := NewMockStateView()
	dst.data = cloneFormalDkgData(src.data)
	dst.revision = src.revision
	return dst
}

func cloneFormalDkgData(src map[string][]byte) map[string][]byte {
	dst := make(map[string][]byte, len(src))
	for key, value := range src {
		dst[key] = append([]byte(nil), value...)
	}
	return dst
}

func formalDkgGhostFromModel(t *testing.T, state itftrace.State) formalDkgGhostState {
	t.Helper()
	return formalDkgGhostState{
		commits:              mustFormalSet(t, state, "commits"),
		shares:               mustFormalSet(t, state, "shares"),
		unresolvedComplaints: mustFormalSet(t, state, "unresolvedComplaints"),
		acks:                 mustFormalSet(t, state, "validationAcks"),
		tempSession:          mustFormalTag(t, state, "tempSession"),
		abstractAttempt:      mustFormalInt(t, state, "attempt"),
	}
}

func applyFormalDkgOps(sv StateView, ops []WriteOp) {
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, append([]byte(nil), op.Value...))
		}
	}
}

func assertFormalDkgStateRelation(
	t *testing.T,
	model itftrace.State,
	sv StateView,
	transitionKey, chain string,
	vaultID uint32,
	epochID uint64,
	sessionID string,
	committee []string,
	memberTags map[string]string,
	ghost formalDkgGhostState,
) {
	t.Helper()
	want, err := refinement.DkgFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	got, err := formalDkgStateFromProduction(
		sv, transitionKey, chain, vaultID, epochID, sessionID,
		committee, memberTags, ghost,
	)
	if err != nil {
		t.Fatalf("project production DKG state: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DKG state relation mismatch\nmodel:      %+v\nproduction: %+v", want, got)
	}
}

func formalDkgStateFromProduction(
	sv StateView,
	transitionKey, chain string,
	vaultID uint32,
	epochID uint64,
	sessionID string,
	committee []string,
	memberTags map[string]string,
	ghost formalDkgGhostState,
) (refinement.DkgState, error) {
	raw, exists, err := sv.Get(transitionKey)
	if err != nil {
		return refinement.DkgState{}, err
	}
	if !exists {
		return refinement.DkgState{}, fmt.Errorf("transition %s is missing", transitionKey)
	}
	var transition pb.VaultTransitionState
	if err := proto.Unmarshal(raw, &transition); err != nil {
		return refinement.DkgState{}, err
	}
	expectedSessionID := fmt.Sprintf("%s_%d_%d_%d", chain, vaultID, epochID, transition.DkgAttempt)
	if transition.DkgSessionId != expectedSessionID || transition.DkgSessionId != sessionID {
		return refinement.DkgState{}, fmt.Errorf("unexpected session %q want %q", transition.DkgSessionId, expectedSessionID)
	}

	status, err := formalDkgStatusTag(transition.DkgStatus)
	if err != nil {
		return refinement.DkgState{}, err
	}
	lifecycle, err := formalDkgLifecycleTag(&transition)
	if err != nil {
		return refinement.DkgState{}, err
	}
	eligible := make(map[string]struct{}, len(transition.NewCommitteeMembers))
	for _, member := range transition.NewCommitteeMembers {
		tag, ok := memberTags[member]
		if !ok {
			return refinement.DkgState{}, fmt.Errorf("unknown production committee member %q", member)
		}
		eligible[tag] = struct{}{}
	}

	tempStatePresent, err := formalDkgTempStatePresent(sv, chain, vaultID, epochID, sessionID)
	if err != nil {
		return refinement.DkgState{}, err
	}
	if err := verifyFormalDkgPhysicalEvidence(
		sv, chain, vaultID, epochID, sessionID, committee, memberTags, ghost, tempStatePresent,
	); err != nil {
		return refinement.DkgState{}, err
	}

	abstractAttempt := int64(transition.DkgAttempt)
	if ghost.abstractAttempt > 0 {
		abstractAttempt = ghost.abstractAttempt
	}
	return refinement.DkgState{
		Epoch:                int64(transition.EpochId),
		Attempt:              abstractAttempt,
		Session:              formalDkgSessionTag(uint32(abstractAttempt)),
		Status:               status,
		Lifecycle:            lifecycle,
		Eligible:             refinement.SortedStrings(eligible),
		Commits:              refinement.SortedStrings(ghost.commits),
		Shares:               refinement.SortedStrings(ghost.shares),
		UnresolvedComplaints: refinement.SortedStrings(ghost.unresolvedComplaints),
		ValidationAcks:       refinement.SortedStrings(ghost.acks),
		TempSession:          ghost.tempSession,
		TempStatePresent:     tempStatePresent,
		KeyMaterialReady:     transition.DkgStatus == DKGStatusKeyReady && len(transition.NewGroupPubkey) > 0,
	}, nil
}

func formalDkgStatusTag(status string) (string, error) {
	switch status {
	case DKGStatusNotStarted:
		return "NotStarted", nil
	case DKGStatusCommitting:
		return "Committing", nil
	case DKGStatusSharing:
		return "Sharing", nil
	case DKGStatusResolving:
		return "Resolving", nil
	case DKGStatusKeyReady:
		return "KeyReady", nil
	case DKGStatusFailed:
		return "Failed", nil
	default:
		return "", fmt.Errorf("unknown production DKG status %q", status)
	}
}

func formalDkgLifecycleTag(transition *pb.VaultTransitionState) (string, error) {
	if transition.DkgStatus == DKGStatusKeyReady || transition.DkgStatus == DKGStatusFailed {
		if transition.Lifecycle != VaultLifecycleDraining {
			return "", fmt.Errorf("terminal DKG status %s has lifecycle %q", transition.DkgStatus, transition.Lifecycle)
		}
		return "Draining", nil
	}
	if transition.Lifecycle == VaultLifecycleRetired {
		return "", fmt.Errorf("running DKG attempt is retired")
	}
	return "AttemptRunning", nil
}

func formalDkgSessionTag(attempt uint32) string {
	return fmt.Sprintf("Session%d", attempt)
}

func formalDkgTempStatePresent(sv StateView, chain string, vaultID uint32, epochID uint64, sessionID string) (bool, error) {
	for _, item := range frostDkgSessionCleanupPrefixes(chain, vaultID, epochID, sessionID) {
		values, err := sv.Scan(item.prefix)
		if err != nil {
			return false, err
		}
		if len(values) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func verifyFormalDkgPhysicalEvidence(
	sv StateView,
	chain string,
	vaultID uint32,
	epochID uint64,
	sessionID string,
	committee []string,
	memberTags map[string]string,
	ghost formalDkgGhostState,
	tempStatePresent bool,
) error {
	if !tempStatePresent {
		return nil
	}
	for _, dealer := range committee {
		dealerTag := memberTags[dealer]
		if _, expected := ghost.commits[dealerTag]; expected {
			if _, exists, err := sv.Get(keys.KeyFrostVaultDkgCommit(chain, vaultID, epochID, sessionID, dealer)); err != nil || !exists {
				return fmt.Errorf("commit evidence for %s missing: exists=%t err=%v", dealerTag, exists, err)
			}
		}
		if _, expected := ghost.shares[dealerTag]; expected {
			for _, receiver := range committee {
				if dealer == receiver {
					continue
				}
				if _, exists, err := sv.Get(keys.KeyFrostVaultDkgShare(chain, vaultID, epochID, sessionID, dealer, receiver)); err != nil || !exists {
					return fmt.Errorf("share evidence for %s->%s missing: exists=%t err=%v", dealerTag, memberTags[receiver], exists, err)
				}
			}
		}
		_, expectedAck := ghost.acks[dealerTag]
		_, ackExists, err := sv.Get(keys.KeyFrostVaultDkgValidationAck(chain, vaultID, epochID, sessionID, dealer))
		if err != nil || ackExists != expectedAck {
			return fmt.Errorf("ACK evidence for %s: exists=%t want=%t err=%v", dealerTag, ackExists, expectedAck, err)
		}
	}
	physicalComplaints, err := formalDkgPhysicalComplaintTags(sv, chain, vaultID, epochID, sessionID, memberTags)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(physicalComplaints, refinement.SortedStrings(ghost.unresolvedComplaints)) {
		return fmt.Errorf("complaint evidence=%v want=%v", physicalComplaints, refinement.SortedStrings(ghost.unresolvedComplaints))
	}
	return nil
}

func formalDkgPhysicalComplaintTags(
	sv StateView,
	chain string,
	vaultID uint32,
	epochID uint64,
	sessionID string,
	memberTags map[string]string,
) ([]string, error) {
	rows, err := sv.Scan(keys.KeyFrostVaultDkgComplaintPrefix(chain, vaultID, epochID, sessionID))
	if err != nil {
		return nil, err
	}
	tags := make(map[string]struct{})
	for key, raw := range rows {
		var complaint pb.FrostVaultDkgComplaint
		if err := proto.Unmarshal(raw, &complaint); err != nil {
			return nil, fmt.Errorf("parse complaint %s: %w", key, err)
		}
		member := complaint.DealerId
		if complaint.Status == ComplaintStatusResolved {
			member = complaint.ReceiverId
		}
		tag, ok := memberTags[member]
		if !ok {
			return nil, fmt.Errorf("unknown complaint member %q", member)
		}
		tags[tag] = struct{}{}
	}
	return refinement.SortedStrings(tags), nil
}

func mustFormalSet(t *testing.T, state itftrace.State, field string) map[string]struct{} {
	t.Helper()
	value, err := state.SetTags(field)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustFormalTag(t *testing.T, state itftrace.State, field string) string {
	t.Helper()
	value, err := state.Tag(field)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func oneAddedFormalMember(t *testing.T, before, after map[string]struct{}) string {
	t.Helper()
	var added string
	for member := range after {
		if _, exists := before[member]; exists {
			continue
		}
		if added != "" {
			t.Fatalf("model transition added multiple members: %s and %s", added, member)
		}
		added = member
	}
	if added == "" || len(after) != len(before)+1 {
		t.Fatalf("model transition did not add exactly one ACK: before=%v after=%v", before, after)
	}
	return added
}
