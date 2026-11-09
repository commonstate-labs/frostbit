package vm

import (
	"bytes"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"testing"

	"dex/frost/artifact"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/frost/core/dkg"
	coreRoast "dex/frost/core/roast"
	"dex/keys"
	"dex/pb"
	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

                                                                           
                                                                              
                                                                                
func signAccountThresholdTest(t *testing.T, rawSecret *big.Int, message []byte) []byte {
	t.Helper()
	grp := curve.NewBN256Group()
	rawPub := grp.SerializePoint(grp.ScalarBaseMult(rawSecret))
	pub, err := evm.CanonicalBN128PublicKey(rawPub)
	if err != nil {
		t.Fatal(err)
	}
	negate := !bytes.Equal(pub, rawPub)
	ids := []int{1, 3}
	nonces := make([]coreRoast.SignerNonce, len(ids))
	shares := make(map[int]*big.Int)
	for i, id := range ids {
		share := new(big.Int).Add(rawSecret, new(big.Int).Mul(big.NewInt(19), big.NewInt(int64(id))))
		if negate {
			share.Neg(share)
		}
		share.Mod(share, grp.Order())
		shares[id] = share
		hiding, binding := dkg.RandomScalar(grp.Order()), dkg.RandomScalar(grp.Order())
		nonces[i] = coreRoast.SignerNonce{SignerID: id, HidingNonce: hiding, BindingNonce: binding, HidingPoint: grp.ScalarBaseMult(hiding), BindingPoint: grp.ScalarBaseMult(binding)}
	}
	R, _ := coreRoast.ComputeGroupCommitment(nonces, message, grp)
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(grp.SerializePoint(R))
	_, _ = h.Write(pub)
	_, _ = h.Write(message)
	challenge := new(big.Int).SetBytes(h.Sum(nil))
	challenge.Mod(challenge, grp.Order())
	lambdas := coreRoast.ComputeLagrangeCoefficientsForSet(ids, grp.Order())
	z := big.NewInt(0)
	for i, id := range ids {
		rho := coreRoast.ComputeBindingCoefficient(id, message, nonces, grp)
		part := coreRoast.ComputePartialSignature(id, nonces[i].HidingNonce, nonces[i].BindingNonce, shares[id], rho, lambdas[id], challenge, grp)
		z.Add(z, part)
	}
	z.Mod(z, grp.Order())
	signature := append(grp.SerializePoint(R), z.FillBytes(make([]byte, 32))...)
	if valid, err := verifySignatureImpl(chainpkg.SignAlgoETH, rawPub, message, signature); err != nil || !valid {
		t.Fatalf("genuine threshold signature rejected: valid=%v err=%v", valid, err)
	}
	return signature
}

func accountTestSecret(highY bool) *big.Int {
	grp := curve.NewBN256Group()
	half := new(big.Int).Rsh(grp.Modulus(), 1)
	for i := int64(1); i < 1000; i++ {
		k := big.NewInt(i)
		if (grp.ScalarBaseMult(k).Y.Cmp(half) > 0) == highY {
			return k
		}
	}
	panic("test key not found")
}

func loadAccountProtocolArtifact(t *testing.T, sv StateView, id string) *artifact.Record {
	t.Helper()
	data, found, err := sv.Get(keys.KeyArchiveFrostArtifact(id))
	if err != nil || !found {
		t.Fatalf("missing artifact %s: %v", id, err)
	}
	record, err := artifact.UnmarshalRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestAccountVaultClaimSignedMigrationRealThreshold(t *testing.T) {
	for _, name := range []string{"eth", "bnb", "base", "arb", "trx"} {
		for _, native := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/native_%v", name, native), func(t *testing.T) {
				sv := NewMockStateView()
				grp := curve.NewBN256Group()
				oldSecret, newSecret := accountTestSecret(true), accountTestSecret(false)
				oldPub, newPub := grp.SerializePoint(grp.ScalarBaseMult(oldSecret)), grp.SerializePoint(grp.ScalarBaseMult(newSecret))
				contract, to, token := "0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222", "0x3333333333333333333333333333333333333333"
				if name == "trx" {
					contract = "TSqQQDnd9KraaGdBDB1kkrPZbLCtyFiPG8"
					to = "TJRabPrwbZy45sbavfcjinPJC18kjpRTv8"
					token = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
				}
				if native {
					token = ""
				}
				asset := "test-asset"
				cfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountGuardianTestKey(t), Chain: name, SignAlgo: chainpkg.SignAlgoETH, VaultCount: 1, EvmChainId: evm.DefaultChainID(name), VaultRefs: []string{contract}}
				putMigrationProto(t, sv, keys.KeyFrostVaultConfig(name, 0), cfg)
				putMigrationProto(t, sv, keys.KeyFrostVaultState(name, 0), &pb.FrostVaultState{Chain: name, VaultId: 0, KeyEpoch: 1, SignAlgo: chainpkg.SignAlgoETH, Status: "ACTIVE", GroupPubkey: oldPub, CommitteeMembers: []string{"old-member"}})
				putMigrationProto(t, sv, keys.KeyToken(asset), &pb.Token{Address: asset, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: name, NativeContract: token})
				var previousDigest []byte
				runWithdraw := func(seq, epoch uint64, secret *big.Int) {
					id := fmt.Sprintf("%s-withdraw-%d", name, seq)
					putMigrationProto(t, sv, keys.KeyFrostWithdraw(id), &pb.FrostWithdrawState{
						WithdrawId: id, Seq: seq, LiveSeq: seq, Chain: name, Asset: asset, VaultId: 0,
						To: to, Amount: "1000", GrossAmount: "1000", FeeAmount: "1", NetAmount: "999", FeeVersion: 1,
						RequestHeight: 100, Status: "QUEUED",
					})
					sv.Set(keys.KeyFrostWithdrawLiveHead(name, 0), []byte(strconv.FormatUint(seq, 10)))
					sv.Set(keys.KeyFrostWithdrawLiveTail(name, 0), []byte(strconv.FormatUint(seq, 10)))
					sv.Set(keys.KeyFrostWithdrawLivePending(name, 0), []byte("1"))
					sv.Set(keys.KeyFrostWithdrawLiveIndex(name, 0, seq), []byte(id))
					sv.Set(keys.KeyFrostVaultAvailableBalance(name, asset, 0), []byte("0"))
					sv.Set(keys.KeyFrostVaultReservedBalance(name, asset, 0), []byte("1000"))
					tpl, first, err := buildCanonicalAccountTemplate(sv, name, "", 0, epoch, []string{id})
					if err != nil {
						t.Fatal(err)
					}
					if len(previousDigest) > 0 && bytes.Equal(previousDigest, tpl.SigHashes[0]) {
						t.Fatal("distinct same-amount withdrawals reused message")
					}
					previousDigest = append([]byte(nil), tpl.SigHashes[0]...)
					decoded, err := evm.FromJSON(tpl.TemplateData)
					if err != nil {
						t.Fatal(err)
					}
					job := chainpkg.ComputeWithdrawJobID(name, "", 0, first, tpl.TemplateHash, epoch)
					_, handoverConsumed, _ := sv.Get(keys.KeyFrostVaultHandoverConsumed(name, 0, epoch))
					includesHandover := epoch > 1 && !handoverConsumed
					member := "old-member"
					if epoch > 1 {
						member = "new-member"
					}
					claim := &pb.FrostWithdrawClaimTx{Base: &pb.BaseMessage{TxId: "claim-" + id, FromAddress: member, ExecutedHeight: 200 + seq}, JobId: job, Chain: name, Asset: "", VaultId: 0, KeyEpoch: epoch, WithdrawIds: []string{id}, TemplateHash: tpl.TemplateHash, TemplateData: tpl.TemplateData, BatchSeq: decoded.BatchSeq, IncludesHandover: includesHandover}
					if _, receipt, err := (&FrostWithdrawClaimTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: claim}}, sv); err != nil || receipt.Status != "SUCCEED" {
						t.Fatalf("claim: receipt=%+v err=%v", receipt, err)
					}
					sig := signAccountThresholdTest(t, secret, tpl.SigHashes[0])
					signed := &pb.FrostWithdrawSignedTx{Base: &pb.BaseMessage{TxId: "signed-" + id, FromAddress: member, ExecutedHeight: 210 + seq}, JobId: job, Chain: name, Asset: "", VaultId: 0, KeyEpoch: epoch, WithdrawIds: []string{id}, TemplateHash: tpl.TemplateHash, TemplateData: tpl.TemplateData, SignedPackageBytes: sig, GuardianSigs: accountGuardianTemplateSignature(t, tpl.TemplateData), BatchSeq: decoded.BatchSeq, IncludesHandover: includesHandover}
					bad := proto.Clone(signed).(*pb.FrostWithdrawSignedTx)
					bad.SignedPackageBytes[95] ^= 1
					if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: bad}}, sv); err == nil || receipt.Status != "FAILED" {
						t.Fatal("tampered signature accepted")
					}
					if _, found, _ := sv.Get(keys.KeyFrostWithdrawArtifact(id)); found {
						t.Fatal("failed signature published a withdrawal artifact lookup")
					}
					if signed.IncludesHandover {
						assertAccountWithdrawalStorageIndependent(t, sv, signed)
					}
					if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}, sv); err != nil || receipt.Status != "SUCCEED" {
						t.Fatalf("signed: receipt=%+v err=%v", receipt, err)
					}
					head, found, err := sv.Get(keys.KeyFrostWithdrawArtifact(id))
					if err != nil || !found {
						t.Fatalf("missing account artifact: %v", err)
					}
					record := loadAccountProtocolArtifact(t, sv, string(head))
					wantParents := 0
					if signed.IncludesHandover {
						wantParents = 1
					}
					if len(record.Metadata.ParentIDs) != wantParents {
						t.Fatalf("withdrawal parents=%d want=%d", len(record.Metadata.ParentIDs), wantParents)
					}
					if signed.IncludesHandover {
						if !bytes.Equal(record.RawTx[:4], evm.WithdrawBatchWithHandoversMethodID()) ||
							!bytes.Contains(record.RawTx, newPub) {
							t.Fatal("new-epoch withdrawal is missing its canonical handover proof")
						}
					} else if !bytes.Equal(record.RawTx[:4], decoded.MethodID) {
						t.Fatal("ordinary withdrawal used the wrong selector")
					}
					if raw, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance(name, asset, 0)); string(raw) != "0" {
						t.Fatal("signed amount remains reserved")
					}
				}
				runWithdraw(1, 1, oldSecret)
				runWithdraw(2, 1, oldSecret)
				tr := &pb.VaultTransitionState{Chain: name, VaultId: 0, EpochId: 2, SignAlgo: chainpkg.SignAlgoETH, DkgStatus: "KEY_READY", Lifecycle: "DRAINING", OldGroupPubkey: oldPub, NewGroupPubkey: newPub, OldCommitteeMembers: []string{"old-member"}, NewCommitteeMembers: []string{"new-member"}}
				intent, err := buildFrostMigrationIntent(sv, tr, 120)
				if err != nil {
					t.Fatal(err)
				}
				tr.MigrationJobId = intent.MigrationId
				tr.MigrationStatus = "INTENT_FINALIZED"
				tr.MigrationIntentHash = intent.IntentHash
				tr.MigrationTemplateHash = intent.TemplateHash
				putMigrationProto(t, sv, keys.KeyFrostVaultTransition(name, 0, 2), tr)
				putMigrationProto(t, sv, keys.KeyFrostVaultMigration(name, 0, 2), intent)
				sig := signAccountThresholdTest(t, oldSecret, intent.IntentHash)
				req := &pb.FrostVaultTransitionSignedTx{Base: &pb.BaseMessage{TxId: "migration-" + name, FromAddress: "old-member", ExecutedHeight: 125}, Chain: name, OldVaultId: 0, NewVaultId: 0, EpochId: 2, MigrationId: intent.MigrationId, TemplateHash: intent.TemplateHash, TemplateData: intent.TemplateData, MsgHash: intent.IntentHash, Signature: sig, GuardianSigs: [][]byte{signAccountThresholdTest(t, big.NewInt(29), intent.IntentHash)}}
				if _, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: req}}, sv); err != nil || receipt.Status != "SUCCEED" {
					t.Fatalf("migration real signature: %+v %v", receipt, err)
				}
				migrationHead, found, _ := sv.Get(keys.KeyFrostVaultLastMigrationArtifact(name, 0))
				if !found || len(migrationHead) == 0 {
					t.Fatal("account handover proof must persist its non-broadcastable parent artifact")
				}
				vaultData, _, _ := sv.Get(keys.KeyFrostVaultState(name, 0))
				var vault pb.FrostVaultState
				if err := proto.Unmarshal(vaultData, &vault); err != nil {
					t.Fatal(err)
				}
				if vault.KeyEpoch != 2 || !bytes.Equal(vault.GroupPubkey, newPub) {
					t.Fatal("raw DKG key/epoch not activated")
				}
				runWithdraw(3, 2, newSecret)
				runWithdraw(4, 2, newSecret)
				if head, _, _ := sv.Get(keys.KeyFrostVaultLastMigrationArtifact(name, 0)); !bytes.Equal(head, migrationHead) {
					t.Fatal("user withdrawal changed the maintenance head")
				}
			})
		}
	}
}

func assertAccountWithdrawalStorageIndependent(t *testing.T, original *MockStateView, signed *pb.FrostWithdrawSignedTx) {
	t.Helper()
	clone := func(stateOnly bool) *MockStateView {
		copy := NewMockStateView()
		for key, value := range original.data {
			if !stateOnly || keys.IsStatefulKey(key) {
				copy.Set(key, append([]byte(nil), value...))
			}
		}
		return copy
	}
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}
	var expectedState map[string][]byte
	for _, mode := range []string{"full", "pruned_archive", "state_only"} {
		sv := clone(mode == "state_only")
		if mode == "pruned_archive" {
			for key := range sv.data {
				if !keys.IsStatefulKey(key) && keys.StorageClassForKey(key) == keys.StorageArchive {
					sv.Del(key)
				}
			}
		}
		_, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, sv)
		if err != nil || receipt.Status != "SUCCEED" {
			t.Fatalf("%s execution: receipt=%+v err=%v", mode, receipt, err)
		}
		state := make(map[string][]byte)
		for key, value := range sv.data {
			if keys.IsStatefulKey(key) {
				state[key] = value
			}
		}
		if expectedState == nil {
			expectedState = state
		} else if !reflect.DeepEqual(state, expectedState) {
			t.Fatalf("%s changed consensus outcome for the same signed transaction", mode)
		}
	}
	for _, missing := range []bool{false, true} {
		sv := clone(false)
		key := keys.KeyFrostVaultHandoverProof(signed.Chain, signed.VaultId, signed.KeyEpoch)
		if missing {
			sv.Del(key)
		} else {
			raw, found, _ := sv.Get(key)
			if !found || len(raw) == 0 {
				t.Fatal("handover signature missing from consensus state")
			}
			sv.Set(key, make([]byte, len(raw)))
		}
		if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, sv); err == nil || receipt.Status != "FAILED" {
			t.Fatalf("missing=%v: invalid StateDB handover proof was accepted", missing)
		}
	}
}
