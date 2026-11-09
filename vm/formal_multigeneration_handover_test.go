package vm

import (
	"bytes"
	"dex/formal/itftrace"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"testing"

	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"
	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

                                                                          
                                                                               
func formalMultigenerationVault(t *testing.T, name string) (*MockStateView, string) {
	t.Helper()
	sv := NewMockStateView()
	contract := "0x1111111111111111111111111111111111111111"
	if name == "trx" {
		contract = "TSqQQDnd9KraaGdBDB1kkrPZbLCtyFiPG8"
	}
	group := curve.NewBN256Group()
	putMigrationProto(t, sv, keys.KeyFrostVaultConfig(name, 0), &pb.FrostVaultConfig{
		Chain: name, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		VaultCount: 1, EvmChainId: evm.DefaultChainID(name), VaultRefs: []string{contract}, AuthorityProtocol: 3, DepositDexChainId: 51025,
		GuardianPubkey: group.SerializePoint(group.ScalarBaseMult(big.NewInt(29))),
	})
	putMigrationProto(t, sv, keys.KeyFrostVaultState(name, 0), &pb.FrostVaultState{
		Chain: name, VaultId: 0, KeyEpoch: 1, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		Status: VaultStatusActive, GroupPubkey: group.SerializePoint(group.ScalarBaseMult(big.NewInt(17))),
		CommitteeMembers: []string{"member"}, CommitteeThreshold: 1,
	})
	putMigrationProto(t, sv, keys.KeyFrostVaultTransition(name, 0, 1), &pb.VaultTransitionState{
		Chain: name, VaultId: 0, EpochId: 1, DkgStatus: DKGStatusKeyReady,
		Lifecycle: VaultLifecycleActive, NewCommitteeMembers: []string{"member"},
		NewGroupPubkey: group.SerializePoint(group.ScalarBaseMult(big.NewInt(17))),
	})
	return sv, contract
}

func formalMultigenerationPrepare(t *testing.T, sv *MockStateView, name string, epoch uint64) *pb.VaultTransitionState {
	t.Helper()
	group := curve.NewBN256Group()
	transition := &pb.VaultTransitionState{
		Chain: name, VaultId: 0, EpochId: epoch, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		DkgStatus: DKGStatusKeyReady, Lifecycle: VaultLifecycleDraining,
		OldCommitteeMembers: []string{"member"}, NewCommitteeMembers: []string{"member"},
		OldGroupPubkey: group.SerializePoint(group.ScalarBaseMult(big.NewInt(int64(17 + epoch - 2)))),
		NewGroupPubkey: group.SerializePoint(group.ScalarBaseMult(big.NewInt(int64(17 + epoch - 1)))),
		DkgN:           1, DkgThresholdT: 1,
	}
	key := keys.KeyFrostVaultTransition(name, 0, epoch)
	putMigrationProto(t, sv, key, transition)
	sv.Set(keys.KeyFrostVaultTransitionActive(name, 0, epoch), []byte(key))
	return transition
}

func formalMultigenerationFinalize(t *testing.T, sv *MockStateView, name string, epoch uint64) *pb.FrostVaultTransitionSignedTx {
	t.Helper()
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 100 + epoch}}, sv); err != nil {
		t.Fatal(err)
	}
	intent := mustLoadFormalMigration(t, sv, keys.KeyFrostVaultMigration(name, 0, epoch))
	if intent.AuthorityProtocol != 3 || intent.FrozenNextBatchSeq != "1" {
		t.Fatalf("intent is not an empty-epoch V3 cut: protocol=%d cutoff=%q", intent.AuthorityProtocol, intent.FrozenNextBatchSeq)
	}
	signature := signAccountThresholdTest(t, big.NewInt(int64(17+epoch-2)), intent.IntentHash)
	req := &pb.FrostVaultTransitionSignedTx{
		Base:  &pb.BaseMessage{TxId: fmt.Sprintf("formal-v2-%s-%d", name, epoch), FromAddress: "member", ExecutedHeight: 110 + epoch},
		Chain: name, OldVaultId: 0, NewVaultId: 0, EpochId: epoch, MigrationId: intent.MigrationId,
		MsgHash: append([]byte(nil), intent.IntentHash...), TemplateHash: append([]byte(nil), intent.TemplateHash...),
		TemplateData: append([]byte(nil), intent.TemplateData...), Signature: signature,
		GuardianSigs: [][]byte{signAccountThresholdTest(t, big.NewInt(29), intent.IntentHash)},
	}
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("epoch %d finalize: receipt=%+v err=%v", epoch, receipt, err)
	}
	before := cloneFormalAuthorityData(sv.data)
	if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" || receipt.WriteCount != 0 {
		t.Fatalf("exact handover replay: receipt=%+v err=%v", receipt, err)
	}
	if !reflect.DeepEqual(before, sv.data) {
		t.Fatal("exact handover replay changed state")
	}
	return req
}

func TestFormalMultigenerationContinuousEmptyEpochs(t *testing.T) {
	for _, name := range []string{"eth", "bnb", "base", "arb", "trx"} {
		t.Run(name, func(t *testing.T) {
			sv, _ := formalMultigenerationVault(t, name)
			for epoch := uint64(2); epoch <= 4; epoch++ {
				formalMultigenerationPrepare(t, sv, name, epoch)
				formalMultigenerationFinalize(t, sv, name, epoch)
				vault := mustLoadFormalVault(t, sv, keys.KeyFrostVaultState(name, 0))
				if vault.KeyEpoch != epoch {
					t.Fatalf("epoch=%d want %d", vault.KeyEpoch, epoch)
				}
				marker := mustLoadFormalMigration(t, sv, keys.KeyFrostVaultMigration(name, 0, epoch))
				if marker.FrozenNextBatchSeq != "1" || marker.AuthorityProtocol != 3 || marker.Status != migrationStatusSigned {
					t.Fatalf("permanent marker lost cutoff/protocol/signature state: %+v", marker)
				}
				proof, found, err := sv.Get(keys.KeyFrostVaultHandoverProof(name, 0, epoch))
				if err != nil || !found || len(proof) == 0 {
					t.Fatalf("permanent old-key proof missing: %v", err)
				}
				if raw, _, _ := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq(name, 0)); len(raw) != 0 && string(raw) != "1" {
					t.Fatal("handover consumed a withdrawal sequence")
				}
				if _, found, _ := sv.Get(keys.KeyFrostVaultHandoverConsumed(name, 0, epoch)); found {
					t.Fatal("empty epoch unexpectedly has a withdrawal handover marker")
				}
				formalMultigenerationAssertGate(t, sv, name, epoch, true)
				                                                                
				                                                                 
				restarted := NewMockStateView()
				for key, value := range sv.data {
					if keys.IsStatefulKey(key) {
						restarted.Set(key, bytes.Clone(value))
					}
				}
				formalMultigenerationAssertGate(t, restarted, name, epoch, true)
				sv = restarted
			}
		})
	}
}

func TestFormalMultigenerationFreezeDrainsClaimedJob(t *testing.T) {
	sv, _ := formalMultigenerationVault(t, "eth")
	formalMultigenerationPrepare(t, sv, "eth", 2)
	jobID := "formal-v2-inflight"
	putMigrationProto(t, sv, keys.KeyFrostWithdrawJob(jobID), &pb.FrostWithdrawJobState{
		JobId: jobID, Chain: "eth", VaultId: 0, KeyEpoch: 1, Status: frostWithdrawJobStatusClaimed,
	})
	sv.Set(keys.KeyFrostWithdrawActiveVaultJob("eth", 0), []byte(jobID))
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 100}}, sv); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := sv.Get(keys.KeyFrostVaultMigration("eth", 0, 2)); found {
		t.Fatal("sealed handover while old threshold signature could still be in flight")
	}
	transition := mustLoadFormalTransition(t, sv, keys.KeyFrostVaultTransition("eth", 0, 2))
	if transition.MigrationStatus != migrationStatusWaitingWithdraw {
		t.Fatalf("inflight state=%s", transition.MigrationStatus)
	}
	                                                                        
	                                                                           
	sv.Del(keys.KeyFrostWithdrawActiveVaultJob("eth", 0))
	sv.Set(keys.KeyFrostVaultDEXNextBatchSeq("eth", 0), []byte("2"))
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 101}}, sv); err != nil {
		t.Fatal(err)
	}
	intent := mustLoadFormalMigration(t, sv, keys.KeyFrostVaultMigration("eth", 0, 2))
	if intent.FrozenNextBatchSeq != "2" {
		t.Fatalf("cutoff=%q want completed SIGNED seq+1", intent.FrozenNextBatchSeq)
	}
	before := proto.Clone(intent).(*pb.FrostVaultMigrationState)
	sv.Set(keys.KeyFrostVaultDEXNextBatchSeq("eth", 0), []byte(strconv.Itoa(3)))
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 102}}, sv); err != nil {
		t.Fatal(err)
	}
	after := mustLoadFormalMigration(t, sv, keys.KeyFrostVaultMigration("eth", 0, 2))
	if !proto.Equal(before, after) {
		t.Fatal("finalized cutoff was overwritten by later mutable sequence")
	}
}

type formalMultigenerationReadCounter struct {
	StateView
	gets, scans int
}

func (s *formalMultigenerationReadCounter) Get(key string) ([]byte, bool, error) {
	s.gets++
	return s.StateView.Get(key)
}
func (s *formalMultigenerationReadCounter) Scan(prefix string) (map[string][]byte, error) {
	s.scans++
	return s.StateView.Scan(prefix)
}

func formalMultigenerationAssertGate(t *testing.T, sv StateView, name string, epoch uint64, expected bool) {
	t.Helper()
	raw, found, err := sv.Get(keys.KeyFrostVaultConfig(name, 0))
	if err != nil || !found {
		t.Fatal("missing config for real auto-DKG gate")
	}
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	counted := &formalMultigenerationReadCounter{StateView: sv}
	ready, err := accountEpochAuthorityReady(counted, name, 0, epoch, &cfg)
	if ready != expected || (expected && err != nil) {
		t.Fatalf("real auto-DKG gate: ready=%v want=%v err=%v", ready, expected, err)
	}
	if counted.scans != 0 || counted.gets > 8 {
		t.Fatalf("epoch-local gate must be constant exact reads: Get=%d Scan=%d", counted.gets, counted.scans)
	}
}

func TestFormalMultigenerationGateRejectsMissingOrChangedProof(t *testing.T) {
	sv, _ := formalMultigenerationVault(t, "eth")
	formalMultigenerationPrepare(t, sv, "eth", 2)
	formalMultigenerationFinalize(t, sv, "eth", 2)
	formalMultigenerationAssertGate(t, sv, "eth", 2, true)
	key := keys.KeyFrostVaultHandoverProof("eth", 0, 2)
	original, _, _ := sv.Get(key)
	for _, proof := range [][]byte{nil, make([]byte, len(original))} {
		copied := NewMockStateView()
		copied.data = cloneFormalAuthorityData(sv.data)
		if proof == nil {
			copied.Del(key)
		} else {
			copied.Set(key, proof)
		}
		formalMultigenerationAssertGate(t, copied, "eth", 2, false)
	}
	corrupted := NewMockStateView()
	corrupted.data = cloneFormalAuthorityData(sv.data)
	markerKey := keys.KeyFrostVaultMigration("eth", 0, 2)
	marker := mustLoadFormalMigration(t, corrupted, markerKey)
	marker.FrozenNextBatchSeq = "2"
	putMigrationProto(t, corrupted, markerKey, marker)
	formalMultigenerationAssertGate(t, corrupted, "eth", 2, false)
}

                                                                            
                                                                             
func TestFormalMultigenerationITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_MULTIGENERATION_HANDOVER")
	if path == "" {
		t.Skip("formal_multigeneration_handover.ps1 supplies the Quint trace")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.States) != 7 {
		t.Fatalf("trace has %d states, want exact six-edge two-epoch trace", len(trace.States))
	}
	sv, _ := formalMultigenerationVault(t, "eth")
	previousEpoch := int64(1)
	for step, model := range trace.States {
		epoch, err := model.Int("dexEpoch")
		if err != nil {
			t.Fatal(err)
		}
		nextSeq, err := model.Int("dexNextSeq")
		if err != nil {
			t.Fatal(err)
		}
		if epoch != previousEpoch {
			if epoch != previousEpoch+1 {
				t.Fatal("model skipped an epoch")
			}
			formalMultigenerationPrepare(t, sv, "eth", uint64(epoch))
			formalMultigenerationFinalize(t, sv, "eth", uint64(epoch))
		}
		vault := mustLoadFormalVault(t, sv, keys.KeyFrostVaultState("eth", 0))
		if vault.KeyEpoch != uint64(epoch) {
			t.Fatalf("step%d epoch: production=%d model=%d", step, vault.KeyEpoch, epoch)
		}
		actualSeq := "1"
		if raw, found, err := sv.Get(keys.KeyFrostVaultDEXNextBatchSeq("eth", 0)); err != nil {
			t.Fatal(err)
		} else if found {
			actualSeq = string(raw)
		}
		if actualSeq != strconv.FormatInt(nextSeq, 10) {
			t.Fatalf("step%d seq: production=%s model=%d", step, actualSeq, nextSeq)
		}
		raw, found := model.Values["cutoffs"]
		if !found {
			t.Fatal("model omitted cutoff list")
		}
		var cutoffs []struct {
			BigInt string `json:"#bigint"`
		}
		if err := json.Unmarshal(raw, &cutoffs); err != nil || len(cutoffs) != 3 {
			t.Fatalf("malformed model cutoffs: %s %v", raw, err)
		}
		for i, cutoff := range cutoffs {
			target := uint64(i + 2)
			marker, found := loadFormalMigration(t, sv, keys.KeyFrostVaultMigration("eth", 0, target))
			if cutoff.BigInt == "0" {
				if found {
					t.Fatalf("step%d model has no cert%d but production does", step, target)
				}
				continue
			}
			if !found || marker.Status != migrationStatusSigned || marker.FrozenNextBatchSeq != cutoff.BigInt {
				t.Fatalf("step%d certificate%d cutoff differs from model: %+v want %s", step, target, marker, cutoff.BigInt)
			}
			if target == uint64(epoch) {
				formalMultigenerationAssertGate(t, sv, "eth", target, true)
			}
		}
		restarted := NewMockStateView()
		for key, value := range sv.data {
			if keys.IsStatefulKey(key) {
				restarted.Set(key, bytes.Clone(value))
			}
		}
		if epoch > 1 {
			formalMultigenerationAssertGate(t, restarted, "eth", uint64(epoch), true)
		}
		sv = restarted
		previousEpoch = epoch
	}
}

func TestFormalMultigenerationGateRejectsLegacyCompressedProof(t *testing.T) {
	sv, _ := formalMultigenerationVault(t, "eth")
	formalMultigenerationPrepare(t, sv, "eth", 2)
	formalMultigenerationFinalize(t, sv, "eth", 2)
	marker := mustLoadFormalMigration(t, sv, keys.KeyFrostVaultMigration("eth", 0, 2))
	group := curve.NewBN256Group()
	secret := big.NewInt(17)
	rawPub := group.SerializePoint(group.ScalarBaseMult(secret))
	pub, err := evm.CanonicalBN128PublicKey(rawPub)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawPub, pub) {
		secret.Sub(group.Order(), secret)
	}
	                                                                               
	r := group.SerializePoint(group.ScalarBaseMult(big.NewInt(1)))
	hash := sha3.NewLegacyKeccak256()
	hash.Write(r)
	hash.Write(pub)
	hash.Write(marker.IntentHash)
	challenge := new(big.Int).SetBytes(hash.Sum(nil))
	challenge.Mod(challenge, group.Order())
	scalar := new(big.Int).Mul(challenge, secret)
	scalar.Add(scalar, big.NewInt(1))
	scalar.Mod(scalar, group.Order())
	compressed := append(bytes.Clone(r[:32]), scalar.FillBytes(make([]byte, 32))...)
	valid, err := evm.NewETHAdapter().VerifySignature(marker.OldGroupPubkey, marker.IntentHash, compressed)
	if err != nil || !valid {
		t.Fatalf("legacy compact-signature fixture invalid: %v %v", valid, err)
	}
	sv.Set(keys.KeyFrostVaultHandoverProof("eth", 0, 2), compressed)
	formalMultigenerationAssertGate(t, sv, "eth", 2, false)
}
