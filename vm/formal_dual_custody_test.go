package vm

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"testing"

	"dex/formal/itftrace"
	"dex/frost/chain"
	"dex/keys"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

func formalDualNoFundsFixture(t *testing.T, noise int) (StateView, *pb.FrostVaultTransitionSignedTx) {
	t.Helper()
	seed := NewMockStateView()
	op, opPub := migrationTestKey(t, 23)
	guardian, guardPub := migrationTestKey(t, 31)
	_, newPub := migrationTestKey(t, 43)
	intent := &pb.FrostVaultMigrationState{MigrationId: chain.MigrationID("btc", 0, 2), Chain: "btc", VaultId: 0,
		OldEpoch: 1, NewEpoch: 2, OldGroupPubkey: opPub, NewGroupPubkey: newPub,
		TemplateHash: chain.EmptyMigrationTemplateHash(), NoFunds: true, Status: migrationStatusIntentFinalized, IntentHeight: 40}
	intent.IntentHash = chain.MigrationIntentHash("btc", 0, 1, 2, opPub, newPub, intent.TemplateHash, true)
	seedBTCMigrationIntent(t, seed, opPub, newPub, intent)
	putMigrationProto(t, seed, keys.KeyFrostVaultConfig("btc", 0), &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte,
		AuthorityProtocol: 3, GuardianPubkey: guardPub})
	for i := 0; i < noise; i++ {
		seed.data[fmt.Sprintf("formal-unrelated/%08d", i)] = []byte("base")
	}
	sv := newOverlayOrderedTestStateView(seed.data)
	                                                                          
	                                                       
	for i := 0; i < noise; i += 2 {
		sv.Del(fmt.Sprintf("formal-unrelated/%08d", i))
	}
	opSig, err := schnorr.Sign(op, intent.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	guardSig, err := schnorr.Sign(guardian, intent.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	return sv, &pb.FrostVaultTransitionSignedTx{Base: &pb.BaseMessage{TxId: "formal-dual-no-funds", FromAddress: "old-member", ExecutedHeight: 60},
		Chain: "btc", OldVaultId: 0, NewVaultId: 0, EpochId: 2, MigrationId: intent.MigrationId, MsgHash: intent.IntentHash,
		Signature: opSig.Serialize(), GuardianSigs: [][]byte{guardSig.Serialize()}, TemplateHash: intent.TemplateHash}
}

func TestFormalDualCustodyNoFundsStateNeutral(t *testing.T) {
	for _, tc := range []struct {
		name     string
		edit     func(*pb.FrostVaultTransitionSignedTx)
		accepted bool
	}{
		{"both", func(*pb.FrostVaultTransitionSignedTx) {}, true},
		{"operator-only", func(tx *pb.FrostVaultTransitionSignedTx) { tx.GuardianSigs = nil }, false},
		{"guardian-only", func(tx *pb.FrostVaultTransitionSignedTx) { tx.Signature = nil }, false},
		{"wrong-guardian", func(tx *pb.FrostVaultTransitionSignedTx) { tx.GuardianSigs[0][0] ^= 1 }, false},
		{"extra-guardian", func(tx *pb.FrostVaultTransitionSignedTx) {
			tx.GuardianSigs = append(tx.GuardianSigs, append([]byte(nil), tx.GuardianSigs[0]...))
		}, false},
		{"different-intent", func(tx *pb.FrostVaultTransitionSignedTx) { tx.MsgHash[0] ^= 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv, req := formalDualNoFundsFixture(t, 65)
			tc.edit(req)
			before := sv.Diff()
			ops, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, sv)
			got := err == nil && receipt != nil && receipt.Status == "SUCCEED"
			if got != tc.accepted {
				t.Fatalf("production accepted=%v model=%v receipt=%+v err=%v", got, tc.accepted, receipt, err)
			}
			if !got {
				if len(ops) != 0 || !reflect.DeepEqual(before, sv.Diff()) {
					t.Fatal("rejected no-funds authorization changed real StateView overlay")
				}
				return
			}
			raw, found, err := sv.Get(keys.KeyFrostPermanentMigration(req.MigrationId))
			if err != nil || !found {
				t.Fatalf("permanent migration missing: %v", err)
			}
			var archive pb.FrostVaultMigrationArchive
			if err := proto.Unmarshal(raw, &archive); err != nil {
				t.Fatal(err)
			}
			if len(archive.GuardianSigs) != 1 || !bytes.Equal(archive.GuardianSigs[0], req.GuardianSigs[0]) {
				t.Fatal("permanent migration lost the guardian authorization")
			}
		})
	}
}

func TestFormalDualCustodyBTCSignedFailureIsStateNeutral(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*pb.FrostWithdrawSignedTx)
	}{
		{"operator-only", func(tx *pb.FrostWithdrawSignedTx) { tx.GuardianSigs = nil }},
		{"guardian-only", func(tx *pb.FrostWithdrawSignedTx) { tx.InputSigs = nil; tx.SignedPackageBytes = nil }},
		{"wrong-guardian", func(tx *pb.FrostWithdrawSignedTx) { tx.GuardianSigs[0][0] ^= 1 }},
		{"extra-guardian", func(tx *pb.FrostWithdrawSignedTx) { tx.GuardianSigs = append(tx.GuardianSigs, tx.GuardianSigs[0]) }},
		{"wrong-job", func(tx *pb.FrostWithdrawSignedTx) { tx.JobId += "-other" }},
		{"wrong-epoch", func(tx *pb.FrostWithdrawSignedTx) { tx.KeyEpoch++ }},
		{"wrong-chain", func(tx *pb.FrostWithdrawSignedTx) { tx.Chain = "eth" }},
		{"wrong-vault", func(tx *pb.FrostWithdrawSignedTx) { tx.VaultId++ }},
		{"wrong-template-hash", func(tx *pb.FrostWithdrawSignedTx) { tx.TemplateHash[0] ^= 1 }},
		{"wrong-template-data", func(tx *pb.FrostWithdrawSignedTx) { tx.TemplateData = []byte(`{}`) }},
		{"wrong-input-script", func(tx *pb.FrostWithdrawSignedTx) { tx.ScriptPubkeys[0][0] ^= 1 }},
		{"wrong-request", func(tx *pb.FrostWithdrawSignedTx) { tx.WithdrawIds[0] += "-other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv, tx := btcDualSignedFixture(t)
			tc.edit(tx)
			before := sv.Diff()
			ops, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: tx}}, sv)
			if err == nil || receipt == nil || receipt.Status != "FAILED" {
				t.Fatalf("invalid Signed accepted: err=%v receipt=%+v", err, receipt)
			}
			if len(ops) != 0 || !reflect.DeepEqual(before, sv.Diff()) {
				t.Fatal("invalid Signed changed FIFO, reservation, request, job or artifact StateView diff")
			}
		})
	}
}

func TestFormalDualCustodyAccountSignedFailureIsStateNeutral(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*pb.FrostWithdrawSignedTx)
	}{
		{"operator-only", func(tx *pb.FrostWithdrawSignedTx) { tx.GuardianSigs = nil }},
		{"guardian-only", func(tx *pb.FrostWithdrawSignedTx) { tx.SignedPackageBytes = nil }},
		{"wrong-guardian", func(tx *pb.FrostWithdrawSignedTx) { tx.GuardianSigs[0][0] ^= 1 }},
		{"extra-guardian", func(tx *pb.FrostWithdrawSignedTx) { tx.GuardianSigs = append(tx.GuardianSigs, tx.GuardianSigs[0]) }},
		{"wrong-job", func(tx *pb.FrostWithdrawSignedTx) { tx.JobId += "-other" }},
		{"wrong-epoch", func(tx *pb.FrostWithdrawSignedTx) { tx.KeyEpoch++ }},
		{"wrong-chain", func(tx *pb.FrostWithdrawSignedTx) { tx.Chain = "bnb" }},
		{"wrong-sequence", func(tx *pb.FrostWithdrawSignedTx) { tx.BatchSeq = "2" }},
		{"wrong-contract", func(tx *pb.FrostWithdrawSignedTx) {
			tx.TemplateData = bytes.ReplaceAll(tx.TemplateData, []byte("0x1111111111111111111111111111111111111111"), []byte("0x4444444444444444444444444444444444444444"))
		}},
		{"wrong-template-hash", func(tx *pb.FrostWithdrawSignedTx) { tx.TemplateHash[0] ^= 1 }},
		{"wrong-request", func(tx *pb.FrostWithdrawSignedTx) { tx.WithdrawIds[0] += "-other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFormalVaultRotationFixture(t)
			formalRotationFreezeIntent(t, fixture)
			formalRotationActivate(t, fixture)
			formalRotationQueueWithdraw(t, fixture)
			formalRotationClaimNewEpoch(t, fixture)
			tx := formalRotationSignedTx(t, fixture, fixture.newSecret, "formal-dual-invalid-account").GetFrostWithdrawSignedTx()
			tc.edit(tx)
			sv := newOverlayOrderedTestStateView(fixture.sv.data)
			before := sv.Diff()
			ops, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: tx}}, sv)
			if err == nil || receipt == nil || receipt.Status != "FAILED" {
				t.Fatalf("invalid account Signed accepted: err=%v receipt=%+v", err, receipt)
			}
			if len(ops) != 0 || !reflect.DeepEqual(before, sv.Diff()) {
				t.Fatal("invalid account Signed changed real StateView FIFO/reservation/sequence/artifact diff")
			}
		})
	}
}

func TestFormalDualCustodyNoFundsITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_DUAL_CUSTODY")
	if path == "" {
		t.Skip("formal/formal_dual_custody.ps1 supplies the Quint trace")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, state := range trace.States[1:] {
		kind, err := state.Int("kind")
		if err != nil {
			t.Fatal(err)
		}
		if kind != 2 {
			continue
		}
		guard, err := state.Bool("guardian")
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := state.Bool("accepted")
		if err != nil {
			t.Fatal(err)
		}
		sv, req := formalDualNoFundsFixture(t, 33)
		if !guard {
			req.GuardianSigs = nil
		}
		before := sv.Diff()
		_, receipt, runErr := (&FrostVaultTransitionSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, sv)
		got := runErr == nil && receipt != nil && receipt.Status == "SUCCEED"
		if got != accepted {
			t.Fatalf("NoFunds production accepted=%v Quint=%v: %v %+v", got, accepted, runErr, receipt)
		}
		if !got && !reflect.DeepEqual(before, sv.Diff()) {
			t.Fatal("rejected ITF cut changed StateView")
		}
		checked++
	}
	if checked != 2 {
		t.Fatalf("compared no-funds cuts=%d want=2", checked)
	}
}

func TestFormalDualCustodyAccountHandoverITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_DUAL_CUSTODY")
	if path == "" {
		t.Skip("formal/formal_dual_custody.ps1 supplies the Quint trace")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, state := range trace.States[1:] {
		kind, err := state.Int("kind")
		if err != nil {
			t.Fatal(err)
		}
		if kind != 3 {
			continue
		}
		pending, err := state.Bool("pending")
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := state.Bool("accepted")
		if err != nil {
			t.Fatal(err)
		}
		sv, _ := formalMultigenerationVault(t, "eth")
		formalMultigenerationPrepare(t, sv, "eth", 2)
		configBefore, _, _ := sv.Get(keys.KeyFrostVaultConfig("eth", 0))
		if pending {
			jobID := "unpaid-old-authority-job"
			putMigrationProto(t, sv, keys.KeyFrostWithdrawJob(jobID), &pb.FrostWithdrawJobState{JobId: jobID, Chain: "eth", VaultId: 0, KeyEpoch: 1, Status: frostWithdrawJobStatusClaimed})
			sv.Set(keys.KeyFrostWithdrawActiveVaultJob("eth", 0), []byte(jobID))
		}
		if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, sv); err != nil {
			t.Fatal(err)
		}
		_, found, err := sv.Get(keys.KeyFrostVaultMigration("eth", 0, 2))
		if err != nil || found != accepted {
			t.Fatalf("model accepted=%v production frozen=%v err=%v", accepted, found, err)
		}
		if accepted {
			formalMultigenerationFinalize(t, sv, "eth", 2)
		}
		vault := mustLoadFormalVault(t, sv, keys.KeyFrostVaultState("eth", 0))
		expectedEpoch := uint64(1)
		if accepted {
			expectedEpoch = 2
		}
		if vault.KeyEpoch != expectedEpoch {
			t.Fatalf("epoch=%d expected=%d", vault.KeyEpoch, expectedEpoch)
		}
		configAfter, _, _ := sv.Get(keys.KeyFrostVaultConfig("eth", 0))
		if !bytes.Equal(configBefore, configAfter) {
			t.Fatal("handover changed fixed guardian configuration")
		}
		checked++
	}
	if checked != 2 {
		t.Fatalf("checked account cuts=%d expected2", checked)
	}
}

func TestFormalDualCustodyCheckpointProofNeedsBothRoles(t *testing.T) {
	sv, _ := formalMultigenerationVault(t, "eth")
	formalMultigenerationPrepare(t, sv, "eth", 2)
	formalMultigenerationFinalize(t, sv, "eth", 2)
	key := keys.KeyFrostVaultHandoverProof("eth", 0, 2)
	proof, found, err := sv.Get(key)
	if err != nil || !found || len(proof) != 192 {
		t.Fatalf("dual proof length=%d err=%v", len(proof), err)
	}
	for _, offset := range []int{0, 96} {
		copyView := NewMockStateView()
		copyView.data = cloneFormalAuthorityData(sv.data)
		mutated := append([]byte(nil), proof...)
		mutated[offset] ^= 1
		copyView.Set(key, mutated)
		formalMultigenerationAssertGate(t, copyView, "eth", 2, false)
	}
}
