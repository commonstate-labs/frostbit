package vm

import (
	"bytes"
	"dex/formal/itftrace"
	"dex/formal/refinement"
	bridgeasset "dex/frost/asset"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"
	"math/big"
	"os"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
)

const (
	formalRotationAmount        = int64(10)
	formalRotationRequestHeight = uint64(100)
)

type formalVaultRotationFixture struct {
	sv            *MockStateView
	chain         string
	asset         bridgeasset.Identity
	oldSecret     *big.Int
	newSecret     *big.Int
	oldGroupKey   []byte
	newGroupKey   []byte
	transitionKey string
	vaultKey      string
	migrationKey  string
	withdrawID    string
	jobID         string
	user          string
	destination   string
	outcome       string
}

func TestAccountVaultWithdrawalRejectedDuringRotationThenUsesNewAuthority(t *testing.T) {
	fixture := newFormalVaultRotationFixture(t)
	formalRotationRejectWithdraw(t, fixture)
	formalRotationFreezeIntent(t, fixture)
	formalRotationActivate(t, fixture)
	formalRotationQueueWithdraw(t, fixture)
	formalRotationClaimNewEpoch(t, fixture)
	formalRotationRejectOldSignature(t, fixture)
	formalRotationAcceptNewSignature(t, fixture)

	got := formalVaultRotationStateFromGo(t, fixture)
	want := refinement.VaultRotationOperationsState{
		Phase: "ActiveNew", Authority: "NewKey", CurrentEpoch: 2,
		Withdraw: "SignedNew", VaultAvailable: 0, VaultReserved: 0,
		AuthorizedOutflow: formalRotationAmount, ArtifactPublished: true,
		Outcome: "NewSignatureAccepted",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rotation business flow mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestFormalVaultRotationOperationsITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{name: "queue-before-intent", env: "DEX_FORMAL_ITF_ROTATION_QUEUE_FIRST"},
		{name: "queue-after-intent", env: "DEX_FORMAL_ITF_ROTATION_INTENT_FIRST"},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("Vault rotation operation ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalVaultRotationTrace(t, os.Getenv(tc.env))
		})
	}
}

func TestFormalVaultRotationRejectsOldEpochClaimWhileDraining(t *testing.T) {
	fixture := newFormalVaultRotationFixture(t)
	transition := mustLoadFormalTransition(t, fixture.sv, fixture.transitionKey)

	                                                                           
	                                                                          
	                                                                         
	transition.Lifecycle = VaultLifecycleActive
	putMigrationProto(t, fixture.sv, fixture.transitionKey, transition)
	formalRotationQueueWithdraw(t, fixture)
	transition.Lifecycle = VaultLifecycleDraining
	putMigrationProto(t, fixture.sv, fixture.transitionKey, transition)

	before := cloneFormalAuthorityData(fixture.sv.data)
	claim := formalRotationClaim(t, fixture, 1, []string{"old-member"})
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claim, fixture.sv); err == nil || receipt == nil || receipt.Status != "FAILED" || receipt.Error != "vault is draining; new old-key withdraw claims are paused" {
		t.Fatalf("old-epoch claim was not rejected while Vault drains: receipt=%+v err=%v", receipt, err)
	}
	if !reflect.DeepEqual(before, fixture.sv.data) {
		t.Fatal("rejected old-epoch claim mutated withdrawal or Vault state")
	}
}

func runFormalVaultRotationTrace(t *testing.T, path string) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint Vault rotation ITF trace: %v", err)
	}
	if len(trace.States) < 2 {
		t.Fatalf("Vault rotation differential trace states=%d want>=2", len(trace.States))
	}
	fixture := newFormalVaultRotationFixture(t)
	assertFormalVaultRotationStateRelation(t, fixture, trace.States[0], 0)
	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.VaultRotationOperationsFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.VaultRotationOperationsFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}
		applyFormalVaultRotationStep(t, fixture, before, after)
		assertFormalVaultRotationStateRelation(t, fixture, trace.States[i], i)
		assertFormalVaultRotationRestartRelation(t, fixture, trace.States[i], i)
	}
}

func newFormalVaultRotationFixture(t *testing.T) *formalVaultRotationFixture {
	t.Helper()
	fixture := &formalVaultRotationFixture{
		sv: NewMockStateView(), chain: chainpkg.ChainETH,
		oldSecret: accountTestSecret(true), newSecret: accountTestSecret(false),
		user:        "formal-rotation-user",
		destination: "0x2222222222222222222222222222222222222222",
		outcome:     "None",
	}
	fixture.asset = testBridgeAsset(t, fixture.chain, "0x3333333333333333333333333333333333333333")
	group := curve.NewBN256Group()
	fixture.oldGroupKey = group.SerializePoint(group.ScalarBaseMult(fixture.oldSecret))
	fixture.newGroupKey = group.SerializePoint(group.ScalarBaseMult(fixture.newSecret))
	fixture.transitionKey = keys.KeyFrostVaultTransition(fixture.chain, 0, 2)
	fixture.vaultKey = keys.KeyFrostVaultState(fixture.chain, 0)
	fixture.migrationKey = keys.KeyFrostVaultMigration(fixture.chain, 0, 2)

	putMigrationProto(t, fixture.sv, keys.KeyFrostVaultConfig(fixture.chain, 0), &pb.FrostVaultConfig{
		Chain: fixture.chain, SignAlgo: chainpkg.SignAlgoETH, VaultCount: 1,
		EvmChainId:        evm.DefaultChainID(fixture.chain),
		VaultRefs:         []string{"0x1111111111111111111111111111111111111111"},
		AuthorityProtocol: 3, DepositDexChainId: 51025,
		GuardianPubkey: group.SerializePoint(group.ScalarBaseMult(big.NewInt(29))),
	})
	putMigrationProto(t, fixture.sv, fixture.vaultKey, &pb.FrostVaultState{
		Chain: fixture.chain, VaultId: 0, KeyEpoch: 1,
		SignAlgo: chainpkg.SignAlgoETH, Status: VaultStatusActive,
		GroupPubkey: fixture.oldGroupKey, CommitteeMembers: []string{"old-member"},
	})
	putMigrationProto(t, fixture.sv, keys.KeyFrostVaultTransition(fixture.chain, 0, 1), &pb.VaultTransitionState{
		Chain: fixture.chain, VaultId: 0, EpochId: 1,
		SignAlgo: chainpkg.SignAlgoETH, DkgStatus: DKGStatusKeyReady,
		Lifecycle: VaultLifecycleActive, NewGroupPubkey: fixture.oldGroupKey,
		NewCommitteeMembers: []string{"old-member"},
	})
	putMigrationProto(t, fixture.sv, fixture.transitionKey, &pb.VaultTransitionState{
		Chain: fixture.chain, VaultId: 0, EpochId: 2,
		SignAlgo: chainpkg.SignAlgoETH, DkgStatus: DKGStatusKeyReady,
		Lifecycle:      VaultLifecycleDraining,
		OldGroupPubkey: fixture.oldGroupKey, NewGroupPubkey: fixture.newGroupKey,
		OldCommitteeMembers: []string{"old-member"}, NewCommitteeMembers: []string{"new-member"},
	})
	fixture.sv.Set(keys.KeyFrostVaultTransitionActive(fixture.chain, 0, 2), []byte(fixture.transitionKey))
	putMigrationProto(t, fixture.sv, keys.KeyToken(fixture.asset.ID), &pb.Token{
		Address: fixture.asset.ID, TotalSupply: "10", Type: pb.TokenType_TOKEN_BRIDGED,
		NativeChain: fixture.asset.Chain, NativeContract: fixture.asset.Contract,
	})
	fixture.sv.Set(keys.KeyNativeTokenIndex(fixture.asset.Chain, fixture.asset.Contract), []byte(fixture.asset.ID))
	putMigrationProto(t, fixture.sv, keys.KeyBalance(fixture.user, fixture.asset.ID), &pb.TokenBalanceRecord{
		Address: fixture.user, Token: fixture.asset.ID,
		Balance: &pb.TokenBalance{Balance: "10"},
	})
	fixture.sv.Set(keys.KeyFrostVaultAvailableBalance(fixture.chain, fixture.asset.ID, 0), []byte("10"))
	return fixture
}

func applyFormalVaultRotationStep(t *testing.T, fixture *formalVaultRotationFixture, before, after refinement.VaultRotationOperationsState) {
	t.Helper()
	switch {
	case formalVaultRotationCoreEqual(before, after) && after.Outcome == "RequestRejected":
		formalRotationRejectWithdraw(t, fixture)
	case before.Withdraw == "NoRequest" && after.Withdraw == "Queued":
		formalRotationQueueWithdraw(t, fixture)
	case before.Phase == "Draining" && after.Phase == "IntentFinalized":
		formalRotationFreezeIntent(t, fixture)
	case before.Phase == "IntentFinalized" && after.Phase == "ActiveNew":
		formalRotationActivate(t, fixture)
	case before.Withdraw == "Queued" && after.Withdraw == "SigningNew":
		formalRotationClaimNewEpoch(t, fixture)
	case before.Withdraw == "SigningNew" && after.Withdraw == "SignedNew":
		formalRotationAcceptNewSignature(t, fixture)
	case formalVaultRotationCoreEqual(before, after) && after.Outcome == "OldSignatureRejected":
		formalRotationRejectOldSignature(t, fixture)
	default:
		t.Fatalf("unsupported modeled Vault rotation step: before=%+v after=%+v", before, after)
	}
	fixture.outcome = after.Outcome
}

func formalVaultRotationCoreEqual(left, right refinement.VaultRotationOperationsState) bool {
	left.Outcome = ""
	right.Outcome = ""
	return reflect.DeepEqual(left, right)
}

func formalRotationQueueWithdraw(t *testing.T, fixture *formalVaultRotationFixture) {
	t.Helper()
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
		Base:  &pb.BaseMessage{TxId: "formal-rotation-request", FromAddress: fixture.user, ExecutedHeight: formalRotationRequestHeight},
		Chain: fixture.chain, Asset: fixture.asset.ID, To: fixture.destination, Amount: "10",
	}}}
	if _, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(tx, fixture.sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("queue withdrawal during rotation: receipt=%+v err=%v", receipt, err)
	}
	fixture.withdrawID = generateAccountWithdrawID(fixture.chain, 0, 1, formalRotationRequestHeight)
	fixture.outcome = "RequestQueued"
}

func formalRotationRejectWithdraw(t *testing.T, fixture *formalVaultRotationFixture) {
	t.Helper()
	before := cloneFormalAuthorityData(fixture.sv.data)
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
		Base:  &pb.BaseMessage{TxId: "formal-rotation-rejected-request", FromAddress: fixture.user, ExecutedHeight: 99},
		Chain: fixture.chain, Asset: fixture.asset.ID, To: fixture.destination, Amount: "10",
	}}}
	if _, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(tx, fixture.sv); err == nil || receipt == nil || receipt.Status != "FAILED" || receipt.Error != "no ACTIVE Vault available for "+fixture.chain {
		t.Fatalf("withdraw request was not rejected during rotation: receipt=%+v err=%v", receipt, err)
	}
	if !reflect.DeepEqual(before, fixture.sv.data) {
		t.Fatal("rejected withdrawal request mutated Vault or withdrawal state")
	}
	fixture.outcome = "RequestRejected"
}

func formalRotationFreezeIntent(t *testing.T, fixture *formalVaultRotationFixture) {
	t.Helper()
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 110}}, fixture.sv); err != nil {
		t.Fatalf("freeze account-chain migration intent: %v", err)
	}
	if _, found, _ := fixture.sv.Get(fixture.migrationKey); !found {
		t.Fatal("rotation intent was not persisted")
	}
	fixture.outcome = "IntentFrozen"
}

func formalRotationActivate(t *testing.T, fixture *formalVaultRotationFixture) {
	t.Helper()
	migration := mustLoadFormalMigration(t, fixture.sv, fixture.migrationKey)
	signature := signAccountThresholdTest(t, fixture.oldSecret, migration.IntentHash)
	req := &pb.FrostVaultTransitionSignedTx{
		Base:  &pb.BaseMessage{TxId: "formal-rotation-migration", FromAddress: "old-member", ExecutedHeight: 120},
		Chain: fixture.chain, OldVaultId: 0, NewVaultId: 0, EpochId: 2,
		MigrationId: migration.MigrationId, MsgHash: migration.IntentHash,
		Signature: signature, TemplateHash: migration.TemplateHash,
		TemplateData: migration.TemplateData,
		GuardianSigs: [][]byte{signAccountThresholdTest(t, big.NewInt(29), migration.IntentHash)},
	}
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(
		&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, fixture.sv,
	); err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("activate new Vault authority: receipt=%+v err=%v", receipt, err)
	}
	fixture.outcome = "AuthorityActivated"
}

func formalRotationClaim(t *testing.T, fixture *formalVaultRotationFixture, epoch uint64, members []string) *pb.AnyTx {
	t.Helper()
	template, firstSeq, err := buildCanonicalAccountTemplate(fixture.sv, fixture.chain, "", 0, epoch, []string{fixture.withdrawID})
	if err != nil {
		t.Fatalf("build epoch-%d withdrawal template: %v", epoch, err)
	}
	jobID := chainpkg.ComputeWithdrawJobID(fixture.chain, "", 0, firstSeq, template.TemplateHash, epoch)
	coordinator, err := chainpkg.WithdrawCoordinator(jobID, epoch, members)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := evm.FromJSON(template.TemplateData)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base: &pb.BaseMessage{
			TxId:           "formal-rotation-claim",
			FromAddress:    coordinator,
			ExecutedHeight: formalRotationRequestHeight + chainpkg.AccountWithdrawBatchTimeoutBlocks,
		},
		JobId: jobID, Chain: fixture.chain, Asset: "",
		VaultId: 0, KeyEpoch: epoch, WithdrawIds: []string{fixture.withdrawID},
		TemplateHash: template.TemplateHash, TemplateData: template.TemplateData,
		BatchSeq: decoded.BatchSeq, IncludesHandover: epoch > 1,
	}}}
}

func formalRotationClaimNewEpoch(t *testing.T, fixture *formalVaultRotationFixture) {
	t.Helper()
	claim := formalRotationClaim(t, fixture, 2, []string{"new-member"})
	if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(claim, fixture.sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("claim queued withdrawal with new epoch: receipt=%+v err=%v", receipt, err)
	}
	fixture.jobID = claim.GetFrostWithdrawClaimTx().JobId
	fixture.outcome = "NewClaimAccepted"
}

func formalRotationSignedTx(t *testing.T, fixture *formalVaultRotationFixture, secret *big.Int, txID string) *pb.AnyTx {
	t.Helper()
	jobData, found, err := fixture.sv.Get(keys.KeyFrostWithdrawJob(fixture.jobID))
	if err != nil || !found {
		t.Fatalf("load active rotation withdrawal job: found=%t err=%v", found, err)
	}
	var job pb.FrostWithdrawJobState
	if err := proto.Unmarshal(jobData, &job); err != nil {
		t.Fatal(err)
	}
	template, _, err := buildCanonicalAccountTemplate(fixture.sv, fixture.chain, "", 0, 2, []string{fixture.withdrawID})
	if err != nil {
		t.Fatal(err)
	}
	signature := signAccountThresholdTest(t, secret, template.SigHashes[0])
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
		Base: &pb.BaseMessage{
			TxId:           txID,
			FromAddress:    "new-member",
			ExecutedHeight: formalRotationRequestHeight + chainpkg.AccountWithdrawBatchTimeoutBlocks + 1,
		},
		JobId: fixture.jobID, Chain: fixture.chain, Asset: "",
		VaultId: 0, KeyEpoch: 2, WithdrawIds: []string{fixture.withdrawID},
		TemplateHash: job.TemplateHash, TemplateData: job.TemplateData,
		SignedPackageBytes: signature, BatchSeq: job.BatchSeq, IncludesHandover: job.IncludesHandover,
		GuardianSigs: [][]byte{signAccountThresholdTest(t, big.NewInt(29), template.SigHashes[0])},
	}}}
}

func formalRotationRejectOldSignature(t *testing.T, fixture *formalVaultRotationFixture) {
	t.Helper()
	before := cloneFormalAuthorityData(fixture.sv.data)
	tx := formalRotationSignedTx(t, fixture, fixture.oldSecret, "formal-rotation-old-signature")
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, fixture.sv); err == nil || receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("old authority signature was not rejected for epoch-2 job: receipt=%+v err=%v", receipt, err)
	}
	if !reflect.DeepEqual(before, fixture.sv.data) {
		t.Fatal("rejected old authority signature mutated reservation or artifact state")
	}
	fixture.outcome = "OldSignatureRejected"
}

func formalRotationAcceptNewSignature(t *testing.T, fixture *formalVaultRotationFixture) {
	t.Helper()
	tx := formalRotationSignedTx(t, fixture, fixture.newSecret, "formal-rotation-new-signature")
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, fixture.sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("new authority signature rejected for epoch-2 job: receipt=%+v err=%v", receipt, err)
	}
	fixture.outcome = "NewSignatureAccepted"
}

func assertFormalVaultRotationStateRelation(t *testing.T, fixture *formalVaultRotationFixture, model itftrace.State, step int) {
	t.Helper()
	expected, err := refinement.VaultRotationOperationsFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := formalVaultRotationStateFromGo(t, fixture)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Vault rotation state relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v", step, model.Action, actual, expected)
	}
}

func assertFormalVaultRotationRestartRelation(t *testing.T, fixture *formalVaultRotationFixture, model itftrace.State, step int) {
	t.Helper()
	restarted := *fixture
	restarted.sv = NewMockStateView()
	restarted.sv.data = cloneFormalAuthorityData(fixture.sv.data)
	expected, err := refinement.VaultRotationOperationsFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	if actual := formalVaultRotationStateFromGo(t, &restarted); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("Vault rotation restart relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v", step, model.Action, actual, expected)
	}
}

func formalVaultRotationStateFromGo(t *testing.T, fixture *formalVaultRotationFixture) refinement.VaultRotationOperationsState {
	t.Helper()
	transition := mustLoadFormalTransition(t, fixture.sv, fixture.transitionKey)
	vault := mustLoadFormalVault(t, fixture.sv, fixture.vaultKey)
	phase := "Draining"
	if transition.Lifecycle == VaultLifecycleActive && transition.MigrationStatus == migrationStatusSigned {
		phase = "ActiveNew"
	} else if _, found := loadFormalMigration(t, fixture.sv, fixture.migrationKey); found {
		phase = "IntentFinalized"
	}
	authority := "OldKey"
	if vault.KeyEpoch == 2 && bytes.Equal(vault.GroupPubkey, fixture.newGroupKey) {
		authority = "NewKey"
	}

	withdrawPhase := "NoRequest"
	if fixture.withdrawID != "" {
		withdrawData, found, err := fixture.sv.Get(keys.KeyFrostWithdraw(fixture.withdrawID))
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			withdrawData, found, err = fixture.sv.Get(keys.KeyArchiveFrostWithdraw(fixture.withdrawID))
			if err != nil {
				t.Fatal(err)
			}
		}
		if !found {
			t.Fatal("rotation withdrawal disappeared from hot and archive state")
		}
		withdraw, err := unmarshalWithdrawRequest(withdrawData)
		if err != nil {
			t.Fatal(err)
		}
		switch withdraw.Status {
		case WithdrawStatusQueued:
			withdrawPhase = "Queued"
		case WithdrawStatusSigning:
			job := formalRotationLoadJob(t, fixture.sv, withdraw.JobID)
			if job.KeyEpoch == 2 {
				withdrawPhase = "SigningNew"
			} else {
				withdrawPhase = "SigningOld"
			}
		case WithdrawStatusSigned:
			job := formalRotationLoadJob(t, fixture.sv, withdraw.JobID)
			if job.KeyEpoch == 2 {
				withdrawPhase = "SignedNew"
			} else {
				withdrawPhase = "SignedOld"
			}
		default:
			withdrawPhase = string(withdraw.Status)
		}
	}

	available := formalRotationReadAmount(t, fixture.sv, keys.KeyFrostVaultAvailableBalance(fixture.chain, fixture.asset.ID, 0))
	reserved := formalRotationReadAmount(t, fixture.sv, keys.KeyFrostVaultReservedBalance(fixture.chain, fixture.asset.ID, 0))
	artifactPublished := false
	if fixture.withdrawID != "" {
		_, artifactPublished, _ = fixture.sv.Get(keys.KeyFrostWithdrawArtifact(fixture.withdrawID))
	}
	authorizedOutflow := int64(0)
	if artifactPublished {
		authorizedOutflow = formalRotationAmount
	}
	return refinement.VaultRotationOperationsState{
		Phase: phase, Authority: authority, CurrentEpoch: int64(vault.KeyEpoch),
		Withdraw: withdrawPhase, VaultAvailable: available, VaultReserved: reserved,
		AuthorizedOutflow: authorizedOutflow, ArtifactPublished: artifactPublished,
		Outcome: fixture.outcome,
	}
}

func formalRotationLoadJob(t *testing.T, sv StateView, jobID string) *pb.FrostWithdrawJobState {
	t.Helper()
	data, found, err := sv.Get(keys.KeyFrostWithdrawJob(jobID))
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		data, found, err = sv.Get(keys.KeyArchiveFrostWithdrawJob(jobID))
		if err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatalf("rotation withdraw job %q not found", jobID)
	}
	var job pb.FrostWithdrawJobState
	if err := proto.Unmarshal(data, &job); err != nil {
		t.Fatal(err)
	}
	return &job
}

func formalRotationReadAmount(t *testing.T, sv StateView, key string) int64 {
	t.Helper()
	data, found, err := sv.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if !found || len(data) == 0 {
		return 0
	}
	value, ok := new(big.Int).SetString(string(data), 10)
	if !ok || !value.IsInt64() {
		t.Fatalf("invalid rotation amount at %s: %q", key, string(data))
	}
	return value.Int64()
}
