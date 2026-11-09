package vm

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/formal/itftrace"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/custodyevidence"
	"dex/keys"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

func formalBTCIsolationMigrationBuild(t *testing.T, count int) (StateView, *pb.FrostVaultMigrationState) {
	t.Helper()
	sv := seedBTCIsolationMigration(t, count)
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
		t.Fatal(err)
	}
	p := loadBTCIsolationMigrationParent(t, sv)
	if p.Status != migrationStatusIntentFinalized {
		t.Fatalf("small fixture did not seal manifest: %s", p.Status)
	}
	return sv, p
}

func formalBTCIsolationMigrationEpoch(t *testing.T, sv StateView) uint64 {
	t.Helper()
	raw, found, err := sv.Get(keys.KeyFrostVaultState("btc", 0))
	var vault pb.FrostVaultState
	if err != nil || !found || proto.Unmarshal(raw, &vault) != nil {
		t.Fatalf("vault missing: %v", err)
	}
	return vault.KeyEpoch
}

func formalBTCIsolationMigrationReject(t *testing.T, sv StateView, tx *pb.AnyTx) {
	t.Helper()
	before := formalBTCRows(t, sv)
	ops, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
	formalBTCIsolationRequireResult(t, ops, receipt, err, false)
	if !reflect.DeepEqual(before, formalBTCRows(t, sv)) {
		t.Fatal("rejected migration authorization retained ledger/key/artifact writes")
	}
}

func formalBTCIsolationNoFundsSigned(t *testing.T, p *pb.FrostVaultMigrationState) *pb.AnyTx {
	t.Helper()
	op, guardian := btcDualTestKeys()
	a, err := schnorr.Sign(op, p.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	b, err := schnorr.Sign(guardian, p.IntentHash)
	if err != nil {
		t.Fatal(err)
	}
	return &pb.AnyTx{Content: &pb.AnyTx_FrostVaultTransitionSignedTx{FrostVaultTransitionSignedTx: &pb.FrostVaultTransitionSignedTx{
		Base:  &pb.BaseMessage{TxId: "formal-empty-v3-handover", FromAddress: "old-member", ExecutedHeight: 70},
		Chain: "btc", OldVaultId: 0, NewVaultId: 0, EpochId: 2, MigrationId: p.MigrationId,
		MsgHash: p.IntentHash, TemplateHash: p.TemplateHash, Signature: a.Serialize(), GuardianSigs: [][]byte{b.Serialize()},
	}}}
}

func TestFormalBTCJobIsolationEmptyManifestNeedsBothAuthorities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(*pb.FrostVaultTransitionSignedTx)
		accept bool
	}{
		{"both", func(*pb.FrostVaultTransitionSignedTx) {}, true},
		{"operator-only", func(r *pb.FrostVaultTransitionSignedTx) { r.GuardianSigs = nil }, false},
		{"guardian-only", func(r *pb.FrostVaultTransitionSignedTx) { r.Signature = nil }, false},
		{"wrong-guardian", func(r *pb.FrostVaultTransitionSignedTx) { r.GuardianSigs[0][0] ^= 1 }, false},
		{"wrong-context", func(r *pb.FrostVaultTransitionSignedTx) { r.MsgHash = bytes.Repeat([]byte{9}, 32) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv, p := formalBTCIsolationMigrationBuild(t, 0)
			if !p.NoFunds || p.BtcChildCount != 0 || len(p.BtcManifestHash) != 32 {
				t.Fatal("empty manifest not canonical")
			}
			tx := formalBTCIsolationNoFundsSigned(t, p)
			tc.edit(tx.GetFrostVaultTransitionSignedTx())
			if !tc.accept {
				formalBTCIsolationMigrationReject(t, sv, tx)
				return
			}
			ops, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
			                                                                   
			                                                                       
			if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
				t.Fatalf("empty dual authorization: %v %+v", err, receipt)
			}
			applyWriteOpsToStateView(sv, ops)
			if formalBTCIsolationMigrationEpoch(t, sv) != 2 {
				t.Fatal("authorized empty manifest did not advance epoch")
			}
			if raw, found, err := sv.Get(keys.KeyFrostPermanentMigration(p.MigrationId)); err != nil || !found || len(raw) == 0 {
				t.Fatalf("permanent empty handover missing: %v", err)
			}
		})
	}
}

func TestFormalBTCJobIsolationManifestBindingsAndDualAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*pb.FrostVaultTransitionSignedTx)
	}{
		{"manifest-guardian", func(r *pb.FrostVaultTransitionSignedTx) { r.GuardianSigs[1][0] ^= 1 }},
		{"input-guardian", func(r *pb.FrostVaultTransitionSignedTx) { r.GuardianSigs[0][0] ^= 1 }},
		{"manifest-operator", func(r *pb.FrostVaultTransitionSignedTx) { r.Signature[0] ^= 1 }},
		{"manifest-digest", func(r *pb.FrostVaultTransitionSignedTx) { r.MsgHash = bytes.Repeat([]byte{7}, 32) }},
		{"child-template", func(r *pb.FrostVaultTransitionSignedTx) { r.TemplateHash = bytes.Repeat([]byte{7}, 32) }},
		{"child-epoch", func(r *pb.FrostVaultTransitionSignedTx) { r.EpochId++ }},
		{"cross-vault", func(r *pb.FrostVaultTransitionSignedTx) { r.NewVaultId++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv, p := formalBTCIsolationMigrationBuild(t, 2)
			tx := btcIsolationMigrationSignedForTest(t, sv, p, 1)
			tc.edit(tx.GetFrostVaultTransitionSignedTx())
			formalBTCIsolationMigrationReject(t, sv, tx)
		})
	}
}

func TestFormalBTCJobIsolationManifestITFCutpoints(t *testing.T) {
	traceRoot := os.Getenv("DEX_FORMAL_ITF_BTC_JOB_ISOLATION")
	if traceRoot == "" {
		t.Skip("run dedicated runner for model-generated manifest cuts")
	}
	files, err := filepath.Glob(filepath.Join(traceRoot, "partialManifestCannotActivateTest_*.itf.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("manifest trace missing: %v %v", files, err)
	}
	trace, err := itftrace.Load(files[0])
	if err != nil {
		t.Fatal(err)
	}
	sv, p := formalBTCIsolationMigrationBuild(t, 2)
	var wantAcc = chain.BTCMigrationManifestAccumulate(nil, nil)
	var totalIn, totalOut, totalFee uint64
	for seq := uint64(1); seq <= 2; seq++ {
		ref, found, _ := sv.Get(keys.KeyFrostBtcMigrationChild(p.MigrationId, seq))
		if !found {
			t.Fatal("manifest child missing")
		}
		raw, found, _ := sv.Get(keys.KeyFrostBtcMigrationChildState(string(ref)))
		var child pb.FrostVaultMigrationState
		if !found || proto.Unmarshal(raw, &child) != nil {
			t.Fatal("child state missing")
		}
		template, err := btc.FromJSON(child.TemplateData)
		if err != nil {
			t.Fatal(err)
		}
		if len(template.Inputs) != 1 || len(template.Outputs) != 1 || child.BtcChildSeq != seq || len(child.BtcOriginId) != 32 {
			t.Fatal("migration merged provenance or lost sequence")
		}
		i, _ := strconv.ParseUint(child.BtcTotalInputAmount, 10, 64)
		o, _ := strconv.ParseUint(child.BtcTotalOutputAmount, 10, 64)
		f, _ := strconv.ParseUint(child.BtcTotalFeeAmount, 10, 64)
		if i != o+f || i != template.Inputs[0].Amount || o != template.Outputs[0].Amount {
			t.Fatal("migration child money partition differs")
		}
		totalIn += i
		totalOut += o
		totalFee += f
		wantAcc = chain.BTCMigrationManifestAccumulate(wantAcc, child.IntentHash)
	}
	if !bytes.Equal(wantAcc, p.BtcManifestHash) || p.BtcChildCount != 2 || p.BtcTotalInputAmount != strconv.FormatUint(totalIn, 10) ||
		p.BtcTotalOutputAmount != strconv.FormatUint(totalOut, 10) || p.BtcTotalFeeAmount != strconv.FormatUint(totalFee, 10) {
		t.Fatal("sealed manifest commitment/count/sums differ")
	}
	signed := uint64(0)
	for edge, s := range trace.States[1:] {
		switch formalBTCIsolationInt(t, s, "stepId") {
		case 8:                                                                         
		case 9:
			seq := uint64(formalBTCIsolationInt(t, s, "lastRoot") + 1)
			tx := btcIsolationMigrationSignedForTest(t, sv, p, seq)
			ops, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
			formalBTCIsolationRequireResult(t, ops, receipt, err, true)
			signed++
			childID := tx.GetFrostVaultTransitionSignedTx().MigrationId
			if raw, found, err := sv.Get(keys.KeyFrostPermanentMigration(childID)); err != nil || !found || len(raw) == 0 {
				t.Fatalf("signed child permanent artifact missing: %v", err)
			}
			                                                                
			                                                                    
			                                             
		case 10:
			if !formalBTCIsolationBool(t, s, "lastAccepted") {
				bad := btcIsolationMigrationSignedForTest(t, sv, p, 2)
				bad.GetFrostVaultTransitionSignedTx().MigrationId = p.MigrationId
				formalBTCIsolationMigrationReject(t, sv, bad)
			}
		default:
			t.Fatalf("unexpected manifest cut %d", formalBTCIsolationInt(t, s, "stepId"))
		}
		wantEpoch := uint64(1)
		if signed == 2 {
			wantEpoch = 2
		}
		if got := formalBTCIsolationMigrationEpoch(t, sv); got != wantEpoch {
			t.Fatalf("edge %d premature/incomplete key switch: %d want %d", edge, got, wantEpoch)
		}
		gotParent := loadBTCIsolationMigrationParent(t, sv)
		if gotParent.BtcSignedChildCount != signed {
			t.Fatal("signed child counter differs")
		}
	}
}

func TestFormalBTCJobIsolationMigrationLateFailureRollback(t *testing.T) {
	sv, p := formalBTCIsolationMigrationBuild(t, 1)
	tx := btcIsolationMigrationSignedForTest(t, sv, p, 1)
	                                                                       
	                                                                             
	sv.Set(keys.KeyFrostVaultTransition("btc", 0, 1), []byte("corrupt previous transition"))
	before := formalBTCRows(t, sv)
	ops, receipt, err := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
	formalBTCIsolationRequireResult(t, ops, receipt, err, false)
	if err == nil || !strings.Contains(err.Error(), "previous BTC transition") {
		t.Fatalf("late failure not reached: %v", err)
	}
	if !reflect.DeepEqual(before, formalBTCRows(t, sv)) {
		t.Fatal("late migration failure changed funds, keys, manifest, child or artifacts")
	}
}

func TestFormalBTCJobIsolationSignedRejectsMutatedIntentAndReplay(t *testing.T) {
	for _, name := range []string{"guardian", "operator", "request-list", "epoch", "template", "origin", "replay-package"} {
		t.Run(name, func(t *testing.T) {
			h := formalBTCIsolationHarnessNew(t)
			h.request(t, 0)
			h.claim(t, 0, 0, 0, 2, true)
			tx := btcIsolationSignedForTest(t, h.fixtures[0], h.claims[0])
			req := tx.GetFrostWithdrawSignedTx()
			switch name {
			case "guardian":
				req.GuardianSigs[0][0] ^= 1
			case "operator":
				req.InputSigs[0][0] ^= 1
				req.SignedPackageBytes = bytes.Clone(req.InputSigs[0])
			case "request-list":
				req.WithdrawIds = []string{"different-parent"}
			case "epoch":
				req.KeyEpoch++
			case "template":
				req.TemplateHash = bytes.Repeat([]byte{9}, 32)
			case "origin":
				raw, _, _ := h.sv.Get(keys.KeyFrostBtcUtxo(0, h.roots[0].Txid, 0))
				var u pb.FrostUtxo
				if err := proto.Unmarshal(raw, &u); err != nil {
					t.Fatal(err)
				}
				u.OriginId = bytes.Repeat([]byte{9}, 32)
				h.sv.Set(keys.KeyFrostBtcUtxo(0, u.Txid, 0), mustMarshalProto(t, &u))
			case "replay-package":
				ops, r, e := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, h.sv)
				formalBTCIsolationRequireResult(t, ops, r, e, true)
				req.GuardianSigs[0][0] ^= 1
			}
			before := formalBTCRows(t, h.sv)
			ops, r, e := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, h.sv)
			formalBTCIsolationRequireResult(t, ops, r, e, false)
			if !reflect.DeepEqual(before, formalBTCRows(t, h.sv)) {
				t.Fatal("invalid Signed changed parent/input/artifact state")
			}
		})
	}
}

func TestFormalBTCJobIsolationBudgetsFailClosed(t *testing.T) {
	for _, name := range []string{"fee", "parts", "canonical-zero", "overflow"} {
		t.Run(name, func(t *testing.T) {
			f, c, p := seedBTCIsolationPrepareFixture(t)
			enableBTCIsolationForTest(t, f.sv)
			switch name {
			case "fee":
				p.BtcMaxTotalFee = "1"
			case "parts":
				p.BtcMaxParts = 1
				p.BtcNextPartSeq = 2
				c.BtcPartSeq = 2
			case "canonical-zero":
				p.BtcClaimedAmount = "00"
			case "overflow":
				p.BtcUnallocatedAmount = "18446744073709551615"
			}
			data, err := marshalWithdrawRequest(p)
			if err != nil {
				t.Fatal(err)
			}
			f.sv.Set(keys.KeyFrostWithdraw(p.WithdrawID), data)
			uRaw, _, _ := f.sv.Get(keys.KeyFrostBtcUtxo(f.vaultID, f.txID, 0))
			f.sv.Set(keys.KeyFrostBtcUtxoAmount(f.vaultID, f.template.Inputs[0].Amount, f.txID, 0), uRaw)
			if name == "parts" {
				fee := f.template.Inputs[0].Amount
				for _, out := range f.template.Outputs {
					fee -= out.Amount
				}
				c.JobId = chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: 51025, BTCNetwork: "mainnet", ParentID: p.WithdrawID,
					PartSeq: 2, VaultID: f.vaultID, KeyEpoch: f.keyEpoch, Gross: 10000, Fee: fee, OriginID: bytes.Repeat([]byte("o"), 32), TemplateHash: c.TemplateHash})
				c.Base.FromAddress, _ = chain.WithdrawCoordinator(c.JobId, f.keyEpoch, f.committee)
			}
			before := formalBTCRows(t, f.sv)
			ops, r, e := (&FrostWithdrawClaimTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: c}}, f.sv)
			formalBTCIsolationRequireResult(t, ops, r, e, false)
			if !reflect.DeepEqual(before, formalBTCRows(t, f.sv)) {
				t.Fatal("invalid budget/ledger changed state")
			}
		})
	}
}

func TestFormalBTCJobIsolationRejectsMergedInputsAndParents(t *testing.T) {
	for _, multiInput := range []bool{false, true} {
		t.Run(strconv.FormatBool(multiInput), func(t *testing.T) {
			h := formalBTCIsolationHarnessNew(t)
			h.request(t, 0)
			snap := h.sv.Snapshot()
			h.claim(t, 0, 0, 0, 2, true)
			claim := proto.Clone(h.claims[0]).(*pb.FrostWithdrawClaimTx)
			if err := h.sv.Revert(snap); err != nil {
				t.Fatal(err)
			}
			if !multiInput {
				claim.WithdrawIds = append(claim.WithdrawIds, "another-parent")
			} else {
				recipient, err := btc.AddressScript(h.to)
				if err != nil {
					t.Fatal(err)
				}
				net, fee, change, err := btc.ExpectedUserPaidWithdrawAmounts(1200000, []uint64{200000}, 2,
					[][]byte{recipient}, h.roots[0].ScriptPubkey, 25)
				if err != nil {
					t.Fatal(err)
				}
				inputs := []chain.UTXO{}
				for _, u := range h.roots {
					inputs = append(inputs, chain.UTXO{TxID: u.Txid, Vout: u.Vout, Amount: 600000, ScriptPubKey: u.ScriptPubkey})
				}
				template, err := btc.NewBTCTemplate(chain.WithdrawTemplateParams{Chain: "btc", Asset: h.asset, VaultID: 0, KeyEpoch: 1,
					WithdrawIDs: claim.WithdrawIds, Outputs: []chain.WithdrawOutput{{WithdrawID: h.parents[0], To: h.to, Amount: strconv.FormatUint(net[0], 10)}},
					Inputs: inputs, GuardianPubkey: btcDualTestGuardianPubkey(), BTCInputPubkeys: [][]byte{btcDualTestOperatorPubkey(), btcDualTestOperatorPubkey()},
					Fee: fee, ChangeAmount: change, ChangeAddress: btcChangeAddressForTest(t, h.roots[0].ScriptPubkey)})
				if err != nil {
					t.Fatal(err)
				}
				claim.TemplateData, err = template.ToJSON()
				if err != nil {
					t.Fatal(err)
				}
				claim.TemplateHash = template.TemplateHash()
				claim.JobId = chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: 51025, BTCNetwork: "mainnet", ParentID: h.parents[0], PartSeq: 1,
					VaultID: 0, KeyEpoch: 1, Gross: 200000, Fee: fee, OriginID: h.origins[0], TemplateHash: claim.TemplateHash})
				claim.Base.FromAddress, _ = chain.WithdrawCoordinator(claim.JobId, 1, []string{"miner-a", "miner-b", "miner-c"})
			}
			before := formalBTCRows(t, h.sv)
			ops, r, e := (&FrostWithdrawClaimTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: claim}}, h.sv)
			t.Logf("merged scope input=%t rejection=%v", multiInput, e)
			formalBTCIsolationRequireResult(t, ops, r, e, false)
			if !reflect.DeepEqual(before, formalBTCRows(t, h.sv)) {
				t.Fatal("merged child changed independent parent/root state")
			}
		})
	}
}

func TestFormalBTCJobIsolationInternalOutputsNeverRemintAfterPruning(t *testing.T) {
	for _, migration := range []bool{false, true} {
		t.Run(strconv.FormatBool(migration), func(t *testing.T) {
			var sv StateView
			var id string
			var vout uint32
			var amount uint64
			if migration {
				var parent *pb.FrostVaultMigrationState
				sv, parent = formalBTCIsolationMigrationBuild(t, 1)
				tx := btcIsolationMigrationSignedForTest(t, sv, parent, 1)
				ops, r, e := (&FrostVaultTransitionSignedTxHandler{}).DryRun(tx, sv)
				formalBTCIsolationRequireResult(t, ops, r, e, true)
				template, e := btc.FromJSON(tx.GetFrostVaultTransitionSignedTx().TemplateData)
				if e != nil {
					t.Fatal(e)
				}
				id, e = template.TxID()
				if e != nil {
					t.Fatal(e)
				}
				amount = template.Outputs[0].Amount
			} else {
				h := formalBTCIsolationHarnessNew(t)
				h.request(t, 0)
				h.claim(t, 0, 0, 0, 2, true)
				tx := btcIsolationSignedForTest(t, h.fixtures[0], h.claims[0])
				ops, r, e := (&FrostWithdrawSignedTxHandler{}).DryRun(tx, h.sv)
				formalBTCIsolationRequireResult(t, ops, r, e, true)
				sv = h.sv
				id, e = h.fixtures[0].template.TxID()
				if e != nil {
					t.Fatal(e)
				}
				vout = 1
				amount = h.fixtures[0].template.Outputs[1].Amount
			}
			for _, prune := range []bool{false, true} {
				if prune {
					for key := range formalBTCRows(t, sv) {
						if strings.Contains(key, "archive_") || strings.HasPrefix(key, keys.KeyFrostBtcUtxoPrefix(0)) {
							sv.Del(key)
						}
					}
				}
				classification, e := custodyevidence.InternalBTCOutputs(sv.Get, id)
				if e != nil || !classification.KnownArtifact || !reflect.DeepEqual(classification.InternalVouts, []uint32{vout}) {
					t.Fatalf("v3 permanent classification prune=%t: %+v %v", prune, classification, e)
				}
			}
			                                                                       
			                                                                     
			                                                                        
			creditView, _, req := requesterBondFixture(t, "formal-v3-internal-remint")
			for key, value := range formalBTCRows(t, sv) {
				if strings.HasPrefix(key, keys.KeyFrostPermanentArtifact("")) || strings.HasPrefix(key, keys.KeyFrostPermanentWithdrawJob("")) ||
					strings.HasPrefix(key, keys.KeyFrostPermanentMigration("")) || strings.HasPrefix(key, keys.KeyFrostArtifactMeta("")) ||
					strings.HasPrefix(key, strings.TrimSuffix(keys.KeyFrostBtcMigrationChildPrefix(""), "_")) || key == keys.KeyFrostVaultConfig("btc", 0) {
					creditView.Set(key, value)
				}
			}
			asset := testBridgeAsset(t, "btc", "")
			req.NativeChain, req.NativeContract, req.TokenAddress, req.VaultId = "btc", "", asset.ID, 0
			req.Status, req.BtcProtocolVersion = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD, 3
			req.NativeTxHash, req.NativeVout, req.Amount = id, vout, strconv.FormatUint(amount, 10)
			creditView.Set(keys.KeyToken(asset.ID), mustMarshalProto(t, &pb.Token{Address: asset.ID, TotalSupply: "0", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "btc"}))
			creditView.Set(keys.KeyRechargeRequest(req.RequestId), mustMarshalProto(t, req))
			if e := applyRechargeFinalized(creditView, req, 500, 1, []string{"formal-witness"}); e != nil {
				t.Fatal(e)
			}
			stored := requesterBondStored(t, creditView, req.RequestId)
			if stored.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED {
				t.Fatal("v3 internal output was credited after pruning")
			}
			if _, credited, _ := creditView.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
				t.Fatal("v3 internal output acquired credit marker")
			}
			if formalRewardSupply(t, creditView, req.TokenAddress) != "0" {
				t.Fatal("v3 internal output minted new supply")
			}
		})
	}
}
