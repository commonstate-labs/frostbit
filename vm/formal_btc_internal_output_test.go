package vm

import (
	"bytes"
	"dex/formal/itftrace"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"dex/frost/artifact"
	"dex/frost/chain/btc"
	"dex/frost/custodyevidence"
	"dex/keys"
	"dex/pb"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
)

func TestFormalCustodyBTCActualSignedPersistsClassification(t *testing.T) {
	sv, signed := btcDualSignedFixture(t)
	if _, receipt, err := (&FrostWithdrawSignedTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}, sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("real BTC dual-Signed: %v %+v", err, receipt)
	}
	template, err := btc.FromJSON(signed.TemplateData)
	if err != nil {
		t.Fatal(err)
	}
	id, err := template.TxID()
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{uint32(len(template.Outputs) - 1)}
	before, err := custodyevidence.InternalBTCOutputs(sv.Get, id)
	if err != nil || !before.KnownArtifact || !reflect.DeepEqual(before.InternalVouts, want) {
		t.Fatalf("actual Signed classification: %+v %v", before, err)
	}
	                                                                              
	                                                                            
	if _, live, err := sv.Get(keys.KeyFrostBtcUtxo(signed.VaultId, id, want[0])); err != nil || !live {
		t.Fatalf("real Signed did not create live change to spend: %v", err)
	}
	rows := formalBTCRows(t, sv)
	removedArchives := 0
	for k := range rows {
		if strings.Contains(k, "archive_") || strings.Contains(k, "btc_utxo") || k == keys.KeyFrostWithdrawJob(signed.JobId) {
			if strings.Contains(k, "archive_") {
				removedArchives++
			}
			delete(rows, k)
		}
	}
	if removedArchives == 0 {
		t.Fatal("fixture did not actually remove cold archives")
	}
	restored := newMemoryStateView(rows)
	after, err := custodyevidence.InternalBTCOutputs(restored.Get, id)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("spent/pruned change lost classification: %+v %v", after, err)
	}
}

                                                                             
                                                                            
                                                                              
func formalCustodyBTCOutputFixture(t *testing.T, migration bool, vout uint32) (StateView, *pb.RechargeRequest, *btc.BTCTemplate, string) {
	t.Helper()
	sv := newMemoryStateView(map[string][]byte{})
	req := seedBTCRechargeFinalizationTest(t, sv, fmt.Sprintf("formal-btc-%t-%d", migration, vout), pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	address, err := btcutil.NewAddressTaproot(req.NativeScript[2:], &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	template := &btc.BTCTemplate{Version: 2, VaultID: 0, KeyEpoch: 1,
		Inputs:      []btc.TxInput{{TxID: strings.Repeat("b", 64), Amount: 100000, Sequence: btc.DefaultSequence}},
		Outputs:     []btc.TxOutput{{Address: address.EncodeAddress(), Amount: 10000}, {Address: address.EncodeAddress(), Amount: 89000}},
		WithdrawIDs: []string{strings.Repeat("c", 64)}}
	if migration {
		template.Outputs = template.Outputs[:1]
		template.WithdrawIDs = nil
	}
	btcDualDecorateTemplate(t, template)
	raw, err := template.BuildSignedRawTx([][]byte{make([]byte, 64)}, [][]byte{make([]byte, 64)})
	if err != nil {
		t.Fatal(err)
	}
	id, err := template.TxID()
	if err != nil {
		t.Fatal(err)
	}
	data, err := template.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	kind, epoch, source := artifact.KindWithdraw, uint64(1), "formal-btc-permanent-job"
	if migration {
		kind, epoch, source = artifact.KindMigration, 2, "formal-btc-permanent-sweep"
	}
	if _, err := storeFrostArtifact(sv, artifact.Metadata{ArtifactID: id, Kind: kind, Chain: "btc", VaultID: 0, Epoch: epoch, Height: 1, SourceID: source}, raw); err != nil {
		t.Fatal(err)
	}
	if migration {
		sv.Set(keys.KeyFrostPermanentMigration(source), mustMarshalProto(t, &pb.FrostVaultMigrationArchive{
			RawTx: raw, InputSigs: [][]byte{make([]byte, 64)}, GuardianSigs: [][]byte{make([]byte, 64), make([]byte, 64)},
			State: &pb.FrostVaultMigrationState{MigrationId: source, Chain: "btc", VaultId: 0, OldEpoch: 1, NewEpoch: 2, Status: "SIGNED", TemplateData: data, TemplateHash: template.TemplateHash()},
		}))
	} else {
		sv.Set(keys.KeyFrostPermanentWithdrawJob(source), mustMarshalProto(t, &pb.FrostWithdrawJobState{JobId: source, Chain: "btc", VaultId: 0, KeyEpoch: 1, Status: "SIGNED", TemplateData: data, TemplateHash: template.TemplateHash(), WithdrawIds: template.WithdrawIDs}))
	}
	_, oldLock, err := rechargeNativeLockIdentity("btc", req.NativeTxHash, req.NativeVout)
	if err != nil {
		t.Fatal(err)
	}
	sv.Del(oldLock)
	req.NativeChain, req.NativeTxHash, req.NativeVout = "btc", id, vout
	req.Amount = fmt.Sprint(template.Outputs[vout].Amount)
	sv.Set(keys.KeyRechargeRequest(req.RequestId), mustMarshalProto(t, req))
	_, lock, err := rechargeNativeLockIdentity("btc", id, vout)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(lock, []byte(req.RequestId))
	return sv, req, template, source
}

func TestFormalCustodyBTCInternalVersusPaymentDifferential(t *testing.T) {
	for _, tc := range []struct {
		name      string
		migration bool
		vout      uint32
		internal  bool
	}{
		{"withdraw-change", false, 1, true}, {"migration-sweep", true, 0, true}, {"ordinary-signed-payment", false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv, req, _, _ := formalCustodyBTCOutputFixture(t, tc.migration, tc.vout)
			classified, err := custodyevidence.InternalBTCOutputs(sv.Get, req.NativeTxHash)
			if err != nil || !classified.KnownArtifact || slices.Contains(classified.InternalVouts, req.NativeVout) != tc.internal {
				t.Fatalf("classification %+v %v", classified, err)
			}
			if tc.internal {
				before := formalBTCRows(t, sv)
				tx := &pb.WitnessRequestTx{Base: &pb.BaseMessage{TxId: "formal-btc-rejected", FromAddress: req.ReceiverAddress}, NativeChain: "btc", NativeTxHash: req.NativeTxHash, NativeVout: req.NativeVout, NativeScript: req.NativeScript, ReceiverAddress: req.ReceiverAddress, Amount: req.Amount}
				_, receipt, err := (&WitnessRequestTxHandler{}).DryRun(&pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: tx}}, sv)
				if receipt == nil || receipt.Status != "FAILED" || !strings.Contains(receipt.Error, "custody") {
					t.Fatalf("internal admission guard missing: %v %+v", err, receipt)
				}
				if !reflect.DeepEqual(before, formalBTCRows(t, sv)) {
					t.Fatal("internal admission changed state")
				}
			}
			                                                                        
			                                                                    
			if err := applyRechargeFinalized(sv, req, 251, 1); err != nil {
				t.Fatal(err)
			}
			stored := requesterBondStored(t, sv, req.RequestId)
			wantSupply := "0"
			wantStatus := pb.RechargeRequestStatus_RECHARGE_REJECTED
			if !tc.internal {
				wantSupply = req.Amount
				wantStatus = pb.RechargeRequestStatus_RECHARGE_FINALIZED
			}
			if stored.Status != wantStatus || formalRewardSupply(t, sv, req.TokenAddress) != wantSupply || GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance != wantSupply {
				t.Fatalf("wrong credit boundary: status=%s supply=%s balance=%s", stored.Status, formalRewardSupply(t, sv, req.TokenAddress), GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance)
			}
			if _, found, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); found == tc.internal {
				t.Fatal("credited marker disagrees with internal classification")
			}
		})
	}
}

func TestFormalCustodyBTCPermanentCorruptionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		migration bool
		mutate    func(*testing.T, StateView, *pb.RechargeRequest, *btc.BTCTemplate, string)
	}{
		{"missing-meta", false, func(t *testing.T, sv StateView, r *pb.RechargeRequest, _ *btc.BTCTemplate, _ string) {
			sv.Del(keys.KeyFrostArtifactMeta(r.NativeTxHash))
		}},
		{"missing-raw", false, func(t *testing.T, sv StateView, r *pb.RechargeRequest, _ *btc.BTCTemplate, _ string) {
			sv.Del(keys.KeyFrostPermanentArtifact(r.NativeTxHash))
		}},
		{"missing-original-template", false, func(t *testing.T, sv StateView, _ *pb.RechargeRequest, _ *btc.BTCTemplate, s string) {
			sv.Del(keys.KeyFrostPermanentWithdrawJob(s))
		}},
		{"withdraw-epoch", false, func(t *testing.T, sv StateView, _ *pb.RechargeRequest, b *btc.BTCTemplate, s string) {
			var j pb.FrostWithdrawJobState
			raw, _, _ := sv.Get(keys.KeyFrostPermanentWithdrawJob(s))
			if err := unmarshalProtoCompat(raw, &j); err != nil {
				t.Fatal(err)
			}
			j.KeyEpoch++
			sv.Set(keys.KeyFrostPermanentWithdrawJob(s), mustMarshalProto(t, &j))
		}},
		{"sweep-new-epoch", true, func(t *testing.T, sv StateView, _ *pb.RechargeRequest, _ *btc.BTCTemplate, s string) {
			var a pb.FrostVaultMigrationArchive
			raw, _, _ := sv.Get(keys.KeyFrostPermanentMigration(s))
			if err := unmarshalProtoCompat(raw, &a); err != nil {
				t.Fatal(err)
			}
			a.State.NewEpoch++
			sv.Set(keys.KeyFrostPermanentMigration(s), mustMarshalProto(t, &a))
		}},
		{"sweep-old-epoch", true, func(t *testing.T, sv StateView, _ *pb.RechargeRequest, _ *btc.BTCTemplate, s string) {
			var a pb.FrostVaultMigrationArchive
			raw, _, _ := sv.Get(keys.KeyFrostPermanentMigration(s))
			if err := unmarshalProtoCompat(raw, &a); err != nil {
				t.Fatal(err)
			}
			a.State.OldEpoch++
			sv.Set(keys.KeyFrostPermanentMigration(s), mustMarshalProto(t, &a))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vout := uint32(1)
			if tc.migration {
				vout = 0
			}
			sv, r, b, s := formalCustodyBTCOutputFixture(t, tc.migration, vout)
			tc.mutate(t, sv, r, b, s)
			if _, err := custodyevidence.InternalBTCOutputs(sv.Get, r.NativeTxHash); err == nil {
				t.Fatal("partial/corrupt permanent evidence treated as external deposit")
			}
			before := formalBTCRows(t, sv)
			if err := applyRechargeFinalized(sv, r, 251, 1); err == nil {
				t.Fatal("unreadable permanent evidence finalized")
			}
			if !reflect.DeepEqual(before, formalBTCRows(t, sv)) {
				t.Fatal("corrupt permanent evidence left partial state")
			}
		})
	}
	sv, r, _, _ := formalCustodyBTCOutputFixture(t, false, 1)
	sv.Del(keys.KeyFrostArtifactMeta(r.NativeTxHash))
	sv.Del(keys.KeyFrostPermanentArtifact(r.NativeTxHash))
	unknown, err := custodyevidence.InternalBTCOutputs(sv.Get, r.NativeTxHash)
	if err != nil || unknown.KnownArtifact || len(unknown.InternalVouts) != 0 {
		t.Fatalf("all absent external tx should be unknown %+v %v", unknown, err)
	}
}

func formalBTCRows(t *testing.T, sv StateView) map[string][]byte {
	t.Helper()
	rows, err := sv.Scan("")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range rows {
		rows[k] = bytes.Clone(v)
	}
	return rows
}

                                                                            
                                                                              
                                                                             
func TestFormalCustodyBTCITFDifferential(t *testing.T) {
	traceRoot := os.Getenv("DEX_FORMAL_ITF_BTC_INTERNAL")
	if traceRoot == "" {
		t.Skip("run formal/formal_btc_internal_output.ps1 to generate model traces")
	}
	for _, tc := range []struct {
		name       string
		kind       int64
		operations []string
	}{
		{"spentChangeCannotRechargeTest", 2, []string{"init", "select", "spend", "credit"}},
		{"spentMigrationCannotRechargeTest", 3, []string{"init", "select", "spend", "credit"}},
		{"registeredPayoutStillDepositsTest", 1, []string{"init", "select", "credit"}},
		{"spentExternalCannotDoubleCreditTest", 0, []string{"init", "select", "credit", "spend", "restart", "credit"}},
		{"corruptArtifactFailsClosedTest", 1, []string{"init", "select", "credit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, err := filepath.Glob(filepath.Join(traceRoot, tc.name+"_*.itf.json"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("expected exactly one retained trace: paths=%v err=%v", paths, err)
			}
			trace, err := itftrace.Load(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(trace.States) != len(tc.operations) {
				t.Fatalf("trace edges=%d expected=%d", len(trace.States), len(tc.operations))
			}
			var sv StateView
			var req *pb.RechargeRequest
			var accepted bool
			for i, expected := range trace.States {
				switch tc.operations[i] {
				case "init":
				case "select":
					vout := uint32(0)
					if tc.kind == 2 {
						vout = 1
					}
					sv, req, _, _ = formalCustodyBTCOutputFixture(t, tc.kind == 3, vout)
					integrity, err := expected.Bool("integrity")
					if err != nil {
						t.Fatal(err)
					}
					if !integrity {
						sv.Del(keys.KeyFrostPermanentArtifact(req.NativeTxHash))
					}
					if tc.kind == 0 {
						sv.Del(keys.KeyFrostArtifactMeta(req.NativeTxHash))
						sv.Del(keys.KeyFrostPermanentArtifact(req.NativeTxHash))
					}
					live, err := expected.Bool("liveUTXO")
					if err != nil {
						t.Fatal(err)
					}
					if live {
						sv.Set(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout), []byte("model live inventory cut"))
					}
				case "spend":
					sv.Del(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout))
					accepted = false
				case "restart":
					sv = newMemoryStateView(formalBTCRows(t, sv))
				case "credit":
					old := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance
					err := applyRechargeFinalized(sv, req, 251, 1)
					integrity, decodeErr := expected.Bool("integrity")
					if decodeErr != nil {
						t.Fatal(decodeErr)
					}
					if err != nil && integrity {
						t.Fatal(err)
					}
					if err == nil && !integrity {
						t.Fatal("corrupt permanent evidence did not fail closed")
					}
					accepted = GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance != old
				default:
					t.Fatal("unknown model operation")
				}
				wantAccepted, err := expected.Bool("accepted")
				if err != nil {
					t.Fatal(err)
				}
				if accepted != wantAccepted {
					t.Fatalf("edge %d %s accepted=%v model=%v", i, tc.operations[i], accepted, wantAccepted)
				}
				if sv == nil {
					continue
				}
				kind, err := expected.Int("kind")
				if err != nil || kind != tc.kind {
					t.Fatalf("bad trace kind %d %v", kind, err)
				}
				expectedCredits, err := expected.Int("credits")
				if err != nil {
					t.Fatal(err)
				}
				_, credited, err := sv.Get(keys.KeyRechargeCredited(req.RequestId))
				if err != nil {
					t.Fatal(err)
				}
				if credited != (expectedCredits == 1) {
					t.Fatalf("edge %d durable credited=%v model count=%d", i, credited, expectedCredits)
				}
				amount, err := strconv.ParseInt(req.Amount, 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"balance", "supply"} {
					units, err := expected.Int(field)
					if err != nil {
						t.Fatal(err)
					}
					actual := formalRewardSupply(t, sv, req.TokenAddress)
					if field == "balance" {
						actual = GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance
					}
					if actual != fmt.Sprint(units/10*amount) {
						t.Fatalf("edge %d %s=%s expected scaled units=%d", i, field, actual, units)
					}
				}
				expectedLive, err := expected.Bool("liveUTXO")
				if err != nil {
					t.Fatal(err)
				}
				_, live, err := sv.Get(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout))
				if err != nil {
					t.Fatal(err)
				}
				if live != expectedLive {
					t.Fatalf("edge %d liveUTXO=%v model=%v", i, live, expectedLive)
				}
			}
		})
	}
}
