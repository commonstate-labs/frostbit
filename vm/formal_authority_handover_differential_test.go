package vm

import (
	"bytes"
	"dex/formal/itftrace"
	"dex/formal/refinement"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"
	"math/big"
	"os"
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

type formalAuthorityInvalidKind string

const (
	formalAuthorityValid            formalAuthorityInvalidKind = ""
	formalAuthorityWrongMessageHash formalAuthorityInvalidKind = "wrong-message-hash"
	formalAuthorityWrongTemplate    formalAuthorityInvalidKind = "wrong-template"
	formalAuthorityWrongSignature   formalAuthorityInvalidKind = "wrong-signature"
)

type formalAuthorityFixture struct {
	sv                 *MockStateView
	chain              string
	oldPrivateKey      *btcec.PrivateKey
	guardianPrivateKey *btcec.PrivateKey
	guardianPublicKey  []byte
	oldGroupKey        []byte
	newGroupKey        []byte
	transitionKey      string
	previousKey        string
	vaultKey           string
	migrationKey       string
	withdrawJobKey     string
	rechargeKey        string
}

func TestFormalAuthorityHandoverITFDifferential(t *testing.T) {
	cases := []struct {
		name        string
		env         string
		invalidKind formalAuthorityInvalidKind
	}{
		{name: "complete-btc", env: "DEX_FORMAL_ITF_AUTHORITY_COMPLETE"},
		{name: "btc-recharge-drain", env: "DEX_FORMAL_ITF_AUTHORITY_BTC_RECHARGE"},
		{name: "account-recharge-does-not-block", env: "DEX_FORMAL_ITF_AUTHORITY_ACCOUNT_RECHARGE"},
		{name: "old-withdraw-drain", env: "DEX_FORMAL_ITF_AUTHORITY_WITHDRAW"},
		{name: "wrong-message-hash-stutters", env: "DEX_FORMAL_ITF_AUTHORITY_INVALID", invalidKind: formalAuthorityWrongMessageHash},
		{name: "wrong-template-stutters", env: "DEX_FORMAL_ITF_AUTHORITY_INVALID", invalidKind: formalAuthorityWrongTemplate},
		{name: "wrong-signature-stutters", env: "DEX_FORMAL_ITF_AUTHORITY_INVALID", invalidKind: formalAuthorityWrongSignature},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("authority handover ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalAuthorityHandoverTrace(t, os.Getenv(tc.env), tc.invalidKind)
		})
	}
}

func runFormalAuthorityHandoverTrace(t *testing.T, path string, invalidKind formalAuthorityInvalidKind) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint authority handover ITF trace: %v", err)
	}
	if len(trace.States) < 2 {
		t.Fatalf("authority handover differential trace states=%d want>=2", len(trace.States))
	}

	initial, err := refinement.AuthorityHandoverFromITF(trace.States[0])
	if err != nil {
		t.Fatal(err)
	}
	fixture := newFormalAuthorityFixture(t, initial)
	assertFormalAuthorityStateRelation(t, fixture, trace.States[0], 0)

	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.AuthorityHandoverFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.AuthorityHandoverFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}
		applyFormalAuthorityModelStep(t, fixture, before, after, invalidKind)
		assertFormalAuthorityStateRelation(t, fixture, trace.States[i], i)
		assertFormalAuthorityRestartRelation(t, fixture, trace.States[i], i)
	}
}

func newFormalAuthorityFixture(t *testing.T, model refinement.AuthorityHandoverState) *formalAuthorityFixture {
	t.Helper()
	if model.Phase != "Draining" || model.CurrentEpoch != 1 || model.TargetEpoch != 2 {
		t.Fatalf("unsupported authority handover initial state: %+v", model)
	}

	fixture := &formalAuthorityFixture{sv: NewMockStateView()}
	var signAlgo pb.SignAlgo
	switch model.Chain {
	case "Bitcoin":
		fixture.chain = chainpkg.ChainBTC
		fixture.oldPrivateKey, fixture.oldGroupKey = migrationTestKey(t, 17)
		fixture.guardianPrivateKey, fixture.guardianPublicKey = migrationTestKey(t, 31)
		_, fixture.newGroupKey = migrationTestKey(t, 23)
		signAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
		putMigrationProto(t, fixture.sv, keys.KeyFrostVaultConfig(fixture.chain, 0), &pb.FrostVaultConfig{
			Chain: fixture.chain, VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte,
			AuthorityProtocol: 3, GuardianPubkey: fixture.guardianPublicKey,
		})
	case "AccountChain":
		fixture.chain = chainpkg.ChainETH
		group := curve.NewBN256Group()
		fixture.guardianPublicKey = group.SerializePoint(group.ScalarBaseMult(big.NewInt(29)))
		var err error
		fixture.oldGroupKey, err = evm.CanonicalBN128PublicKey(group.SerializePoint(group.ScalarBaseMult(big.NewInt(17))))
		if err != nil {
			t.Fatal(err)
		}
		fixture.newGroupKey, err = evm.CanonicalBN128PublicKey(group.SerializePoint(group.ScalarBaseMult(big.NewInt(23))))
		if err != nil {
			t.Fatal(err)
		}
		signAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
		putMigrationProto(t, fixture.sv, keys.KeyFrostVaultConfig(fixture.chain, 0), &pb.FrostVaultConfig{
			Chain: fixture.chain, SignAlgo: signAlgo, VaultCount: 1, EvmChainId: 1,
			VaultRefs:         []string{"0x00000000000000000000000000000000000000aa"},
			AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: fixture.guardianPublicKey,
		})
	default:
		t.Fatalf("unsupported model chain %q", model.Chain)
	}

	fixture.transitionKey = keys.KeyFrostVaultTransition(fixture.chain, 0, 2)
	fixture.previousKey = keys.KeyFrostVaultTransition(fixture.chain, 0, 1)
	fixture.vaultKey = keys.KeyFrostVaultState(fixture.chain, 0)
	fixture.migrationKey = keys.KeyFrostVaultMigration(fixture.chain, 0, 2)
	transition := &pb.VaultTransitionState{
		Chain: fixture.chain, VaultId: 0, EpochId: 2, SignAlgo: signAlgo,
		DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleDraining,
		OldCommitteeMembers: []string{"old-member"}, NewCommitteeMembers: []string{"new-member"},
		OldGroupPubkey: append([]byte(nil), fixture.oldGroupKey...),
		NewGroupPubkey: append([]byte(nil), fixture.newGroupKey...),
	}
	previous := &pb.VaultTransitionState{
		Chain: fixture.chain, VaultId: 0, EpochId: 1, SignAlgo: signAlgo,
		DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleActive,
		NewCommitteeMembers: []string{"old-member"},
		NewGroupPubkey:      append([]byte(nil), fixture.oldGroupKey...),
	}
	putMigrationProto(t, fixture.sv, fixture.transitionKey, transition)
	putMigrationProto(t, fixture.sv, fixture.previousKey, previous)
	fixture.sv.Set(keys.KeyFrostVaultTransitionActive(fixture.chain, 0, 2), []byte(fixture.transitionKey))
	putMigrationProto(t, fixture.sv, fixture.vaultKey, &pb.FrostVaultState{
		Chain: fixture.chain, VaultId: 0, KeyEpoch: 1,
		GroupPubkey: append([]byte(nil), fixture.oldGroupKey...), SignAlgo: signAlgo,
		Status: VaultStatusActive, CommitteeMembers: []string{"old-member"},
	})

	if model.OldWithdrawPending {
		fixture.withdrawJobKey = keys.KeyFrostWithdrawJob("formal-old-epoch-withdraw")
		putMigrationProto(t, fixture.sv, fixture.withdrawJobKey, &pb.FrostWithdrawJobState{
			JobId: "formal-old-epoch-withdraw", Chain: fixture.chain, VaultId: 0,
			KeyEpoch: 1, Status: frostWithdrawJobStatusClaimed,
		})
		fixture.sv.Set(keys.KeyFrostWithdrawActiveVaultJob(fixture.chain, 0), []byte("formal-old-epoch-withdraw"))
		fixture.sv.Set(keys.KeyFrostWithdrawActiveJob(fixture.chain, 0, 1, "formal-old-epoch-withdraw"), []byte("formal-old-epoch-withdraw"))
	}
	if model.OldRechargePending {
		fixture.rechargeKey = keys.KeyRechargeRequest("formal-old-epoch-recharge")
		putMigrationProto(t, fixture.sv, fixture.rechargeKey, &pb.RechargeRequest{
			RequestId: "formal-old-epoch-recharge", NativeChain: fixture.chain, VaultId: 0,
			Status: pb.RechargeRequestStatus_RECHARGE_PENDING,
		})
	}
	return fixture
}

func applyFormalAuthorityModelStep(
	t *testing.T,
	fixture *formalAuthorityFixture,
	before, after refinement.AuthorityHandoverState,
	invalidKind formalAuthorityInvalidKind,
) {
	t.Helper()
	switch {
	case before.OldWithdrawPending && !after.OldWithdrawPending:
		if fixture.withdrawJobKey == "" {
			t.Fatal("model resolved an old withdrawal without a production job")
		}
		fixture.sv.Del(fixture.withdrawJobKey)
		fixture.sv.Del(keys.KeyFrostWithdrawActiveVaultJob(fixture.chain, 0))
		fixture.sv.Del(keys.KeyFrostWithdrawActiveJob(fixture.chain, 0, 1, "formal-old-epoch-withdraw"))
	case before.OldRechargePending && !after.OldRechargePending:
		if fixture.rechargeKey == "" {
			t.Fatal("model resolved an old recharge without a production request")
		}
		fixture.sv.Del(fixture.rechargeKey)
	case before.Phase == "Draining" || before.Phase == "WaitingWithdraw" || before.Phase == "WaitingRecharge":
		if err := (&Executor{}).processFrostMigrationIntents(
			&pb.Block{Header: &pb.BlockHeader{Height: 50}}, fixture.sv,
		); err != nil {
			t.Fatalf("process production migration intent: %v", err)
		}
	case before.Phase == "IntentFinalized" && after.Phase == "ActiveNew":
		if invalidKind != formalAuthorityValid {
			t.Fatalf("invalid-package trace unexpectedly activated: kind=%s", invalidKind)
		}
		submitFormalAuthorityMigration(t, fixture, formalAuthorityValid)
	case reflect.DeepEqual(before, after) && before.Phase == "IntentFinalized":
		if invalidKind == formalAuthorityValid {
			t.Fatal("valid authority trace unexpectedly stuttered")
		}
		submitFormalAuthorityMigration(t, fixture, invalidKind)
	default:
		t.Fatalf("unsupported modeled authority handover step: before=%+v after=%+v", before, after)
	}
}

func submitFormalAuthorityMigration(t *testing.T, fixture *formalAuthorityFixture, invalidKind formalAuthorityInvalidKind) {
	t.Helper()
	if fixture.oldPrivateKey == nil {
		t.Fatal("signed authority differential currently requires the BTC fixture")
	}
	migration := mustLoadFormalMigration(t, fixture.sv, fixture.migrationKey)
	signature, err := schnorr.Sign(fixture.oldPrivateKey, migration.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	guardianSignature, err := schnorr.Sign(fixture.guardianPrivateKey, migration.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	req := &pb.FrostVaultTransitionSignedTx{
		Base:  &pb.BaseMessage{TxId: "formal-authority-signed", FromAddress: "old-member", ExecutedHeight: 60},
		Chain: fixture.chain, OldVaultId: 0, NewVaultId: 0, EpochId: 2,
		MigrationId: migration.MigrationId, MsgHash: append([]byte(nil), migration.IntentHash...),
		Signature: signature.Serialize(), TemplateHash: append([]byte(nil), migration.TemplateHash...),
		TemplateData: append([]byte(nil), migration.TemplateData...),
		GuardianSigs: [][]byte{guardianSignature.Serialize()},
	}
	switch invalidKind {
	case formalAuthorityWrongMessageHash:
		req.MsgHash[0] ^= 0xff
	case formalAuthorityWrongTemplate:
		req.TemplateHash[0] ^= 0xff
	case formalAuthorityWrongSignature:
		req.Signature[0] ^= 0xff
	}

	before := cloneFormalAuthorityData(fixture.sv.data)
	_, receipt, runErr := (&FrostVaultTransitionSignedTxHandler{}).DryRun(
		&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, fixture.sv,
	)
	if invalidKind == formalAuthorityValid {
		if runErr != nil || receipt == nil || receipt.Status != "SUCCEED" {
			t.Fatalf("valid production authority handover failed: receipt=%+v err=%v", receipt, runErr)
		}
		return
	}
	if runErr == nil || receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("invalid production authority handover was accepted: kind=%s receipt=%+v err=%v", invalidKind, receipt, runErr)
	}
	if !reflect.DeepEqual(before, fixture.sv.data) {
		t.Fatalf("rejected authority handover mutated production state: kind=%s", invalidKind)
	}
}

func assertFormalAuthorityStateRelation(t *testing.T, fixture *formalAuthorityFixture, model itftrace.State, step int) {
	t.Helper()
	expected, err := refinement.AuthorityHandoverFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := formalAuthorityStateFromGo(t, fixture)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("authority handover state relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
}

func assertFormalAuthorityRestartRelation(t *testing.T, fixture *formalAuthorityFixture, model itftrace.State, step int) {
	t.Helper()
	restarted := *fixture
	restarted.sv = NewMockStateView()
	restarted.sv.data = cloneFormalAuthorityData(fixture.sv.data)
	actual := formalAuthorityStateFromGo(t, &restarted)
	expected, err := refinement.AuthorityHandoverFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("authority handover restart relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
}

func formalAuthorityStateFromGo(t *testing.T, fixture *formalAuthorityFixture) refinement.AuthorityHandoverState {
	t.Helper()
	transition := mustLoadFormalTransition(t, fixture.sv, fixture.transitionKey)
	previous := mustLoadFormalTransition(t, fixture.sv, fixture.previousKey)
	vault := mustLoadFormalVault(t, fixture.sv, fixture.vaultKey)
	migration, migrationExists := loadFormalMigration(t, fixture.sv, fixture.migrationKey)
	archive := loadFormalMigrationArchive(t, fixture.sv, migration)

	phase := "DkgRunning"
	switch {
	case transition.DkgStatus == DKGStatusFailed:
		phase = "Failed"
	case transition.Lifecycle == VaultLifecycleActive && migrationExists && migration.Status == migrationStatusSigned:
		phase = "ActiveNew"
	case migrationExists && migration.Status == migrationStatusIntentFinalized:
		phase = "IntentFinalized"
	case transition.MigrationStatus == migrationStatusWaitingWithdraw:
		phase = "WaitingWithdraw"
	case transition.MigrationStatus == migrationStatusWaitingRecharge:
		phase = "WaitingRecharge"
	case transition.DkgStatus == DKGStatusKeyReady && transition.Lifecycle == VaultLifecycleDraining:
		phase = "Draining"
	}

	authority := "OldKey"
	if vault.KeyEpoch == transition.EpochId && bytes.Equal(vault.GroupPubkey, fixture.newGroupKey) {
		authority = "NewKey"
	}
	withdrawPending, err := hasClaimedOldEpochWithdraw(fixture.sv, transition)
	if err != nil {
		t.Fatal(err)
	}
	rechargeID, err := findPendingRechargeRequestForVault(fixture.sv, fixture.chain, transition.VaultId)
	if err != nil {
		t.Fatal(err)
	}
	intentBound := formalAuthorityIntentIsBound(t, fixture, transition, migration, archive)
	signatureBound := formalAuthoritySignatureIsBound(t, fixture, archive)

	chainTag := "AccountChain"
	if fixture.chain == chainpkg.ChainBTC {
		chainTag = "Bitcoin"
	}
	return refinement.AuthorityHandoverState{
		Chain: chainTag, Phase: phase, Authority: authority,
		CurrentEpoch: int64(vault.KeyEpoch), TargetEpoch: int64(transition.EpochId),
		OldWithdrawPending: withdrawPending, OldRechargePending: rechargeID != "",
		IntentBound: intentBound, SignatureBoundToIntent: signatureBound,
		OldTransitionRetired: previous.Lifecycle == VaultLifecycleRetired,
	}
}

func formalAuthorityIntentIsBound(
	t *testing.T,
	fixture *formalAuthorityFixture,
	transition *pb.VaultTransitionState,
	hot *pb.FrostVaultMigrationState,
	archive *pb.FrostVaultMigrationArchive,
) bool {
	t.Helper()
	intent := hot
	if archive != nil && archive.State != nil {
		intent = archive.State
	}
	if intent == nil || intent.MigrationId != chainpkg.MigrationID(fixture.chain, 0, 2) ||
		intent.Chain != fixture.chain || intent.VaultId != 0 || intent.OldEpoch != 1 || intent.NewEpoch != 2 ||
		!bytes.Equal(intent.OldGroupPubkey, fixture.oldGroupKey) || !bytes.Equal(intent.NewGroupPubkey, fixture.newGroupKey) ||
		transition.MigrationJobId != intent.MigrationId ||
		!bytes.Equal(transition.MigrationTemplateHash, intent.TemplateHash) ||
		!bytes.Equal(transition.MigrationIntentHash, intent.IntentHash) {
		return false
	}
	if fixture.chain == chainpkg.ChainBTC {
		expected := chainpkg.MigrationIntentHash(
			fixture.chain, 0, 1, 2, fixture.oldGroupKey, fixture.newGroupKey,
			intent.TemplateHash, intent.NoFunds,
		)
		return bytes.Equal(intent.IntentHash, expected)
	}
	cfgData, found, err := fixture.sv.Get(keys.KeyFrostVaultConfig(fixture.chain, 0))
	if err != nil || !found {
		return false
	}
	var cfg pb.FrostVaultConfig
	if proto.Unmarshal(cfgData, &cfg) != nil || len(cfg.VaultRefs) == 0 {
		return false
	}
	expected, _, err := evm.CanonicalMigrationTemplate(&cfg, intent)
	return err == nil && bytes.Equal(intent.TemplateHash, expected.TemplateHash) &&
		bytes.Equal(intent.TemplateData, expected.TemplateData) && len(expected.SigHashes) == 1 &&
		bytes.Equal(intent.IntentHash, expected.SigHashes[0])
}

func formalAuthoritySignatureIsBound(t *testing.T, fixture *formalAuthorityFixture, archive *pb.FrostVaultMigrationArchive) bool {
	t.Helper()
	if archive == nil || archive.State == nil || archive.State.Status != migrationStatusSigned || len(archive.Signature) == 0 {
		return false
	}
	valid, err := verifySignatureImpl(
		pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		fixture.oldGroupKey,
		archive.State.IntentHash,
		archive.Signature,
	)
	if err != nil || !valid || len(archive.GuardianSigs) != 1 {
		return false
	}
	valid, err = verifySignatureImpl(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, fixture.guardianPublicKey, archive.State.IntentHash, archive.GuardianSigs[0])
	return err == nil && valid
}

func mustLoadFormalTransition(t *testing.T, sv StateView, key string) *pb.VaultTransitionState {
	t.Helper()
	data, found, err := sv.Get(key)
	if err != nil || !found {
		t.Fatalf("load formal transition %s: found=%t err=%v", key, found, err)
	}
	var state pb.VaultTransitionState
	if proto.Unmarshal(data, &state) != nil {
		t.Fatalf("decode formal transition %s", key)
	}
	return &state
}

func mustLoadFormalVault(t *testing.T, sv StateView, key string) *pb.FrostVaultState {
	t.Helper()
	data, found, err := sv.Get(key)
	if err != nil || !found {
		t.Fatalf("load formal vault %s: found=%t err=%v", key, found, err)
	}
	var state pb.FrostVaultState
	if proto.Unmarshal(data, &state) != nil {
		t.Fatalf("decode formal vault %s", key)
	}
	return &state
}

func loadFormalMigration(t *testing.T, sv StateView, key string) (*pb.FrostVaultMigrationState, bool) {
	t.Helper()
	data, found, err := sv.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		return nil, false
	}
	var state pb.FrostVaultMigrationState
	if proto.Unmarshal(data, &state) != nil {
		t.Fatalf("decode formal migration %s", key)
	}
	return &state, true
}

func mustLoadFormalMigration(t *testing.T, sv StateView, key string) *pb.FrostVaultMigrationState {
	t.Helper()
	state, found := loadFormalMigration(t, sv, key)
	if !found {
		t.Fatalf("formal migration %s not found", key)
	}
	return state
}

func loadFormalMigrationArchive(t *testing.T, sv StateView, migration *pb.FrostVaultMigrationState) *pb.FrostVaultMigrationArchive {
	t.Helper()
	if migration == nil || migration.MigrationId == "" {
		return nil
	}
	data, found, err := sv.Get(keys.KeyArchiveFrostMigration(migration.MigrationId))
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		return nil
	}
	var archive pb.FrostVaultMigrationArchive
	if proto.Unmarshal(data, &archive) != nil {
		t.Fatal("decode formal migration archive")
	}
	return &archive
}

func cloneFormalAuthorityData(source map[string][]byte) map[string][]byte {
	clone := make(map[string][]byte, len(source))
	for key, value := range source {
		clone[key] = append([]byte(nil), value...)
	}
	return clone
}
