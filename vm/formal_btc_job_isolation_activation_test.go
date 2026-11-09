package vm

import (
	"bytes"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func TestFormalBTCJobIsolationVersionReceiptAndBudgetBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, version, fee string
		receipt            bool
		parts              uint32
		accepted           bool
	}{
		{"v1", "1", "30000", false, 3, false},
		{"v2", "2", "30000", false, 3, false},
		{"missing-receipt", "3", "30000", false, 3, false},
		{"missing-fee", "3", "", true, 3, false},
		{"missing-parts", "3", "30000", true, 0, false},
		{"excess-parts", "3", "30000", true, 4097, false},
		{"both-explicit", "3", "30000", true, 3, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := formalBTCIsolationHarnessNew(t)
			h.sv.Set(keys.KeyProtocolVersion(), []byte(tc.version))
			if !tc.receipt {
				h.sv.Del(keys.KeyProtocolUpgradeReceipt(3))
			}
			tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
				Base:  &pb.BaseMessage{TxId: "formal-boundary", FromAddress: h.owners[0], ExecutedHeight: 101},
				Chain: "btc", Asset: h.asset, To: h.to, Amount: "600000", BtcProtocolVersion: 3, BtcMaxTotalFee: tc.fee, BtcMaxParts: tc.parts,
			}}}
			before := formalBTCRows(t, h.sv)
			ops, r, e := (&FrostWithdrawRequestTxHandler{}).DryRun(tx, h.sv)
			formalBTCIsolationRequireResult(t, ops, r, e, tc.accepted)
			if !tc.accepted && !reflect.DeepEqual(before, formalBTCRows(t, h.sv)) {
				t.Fatal("rejected v3 admission changed balance/supply/state")
			}
		})
	}
}

func formalBTCIsolationRechargeAdmission(t *testing.T, amount uint64, active bool) (StateView, *pb.AnyTx) {
	t.Helper()
	sv := newMemoryStateView(map[string][]byte{})
	seed := seedBTCRechargeFinalizationTest(t, sv, "formal-admit-btc", pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION)
	sv.Del(keys.KeyRechargeRequest(seed.RequestId))
	_, lock, e := rechargeNativeLockIdentity("btc", seed.NativeTxHash, seed.NativeVout)
	if e != nil {
		t.Fatal(e)
	}
	sv.Del(lock)
	sv.(interface{ BindNetworkChainID(uint64) }).BindNetworkChainID(51025)
	sv.Set(keys.KeyProtocolVersion(), []byte("2"))
	if active {
		enableBTCIsolationForTest(t, sv)
	}
	raw, _, _ := sv.Get(keys.KeyFrostVaultConfig("btc", 0))
	var cfg pb.FrostVaultConfig
	if e := proto.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	cfg.DepositDexChainId = 51025
	sv.Set(keys.KeyFrostVaultConfig("btc", 0), mustMarshalProto(t, &cfg))
	const payer = "formal-btc-recharge-payer"
	SetBalance(sv, payer, FeeToken, &pb.TokenBalance{Balance: "3000000000"})
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:        &pb.BaseMessage{TxId: seed.RequestId, FromAddress: payer, ChainId: 51025, ExecutedHeight: 50},
		NativeChain: "btc", NativeTxHash: seed.NativeTxHash, NativeVout: seed.NativeVout, NativeScript: seed.NativeScript,
		ReceiverAddress: seed.ReceiverAddress, Amount: strconv.FormatUint(amount, 10), RechargeFee: "1000000",
	}}}
	return sv, tx
}

func TestFormalBTCJobIsolationRechargeAdmissionAndGrandfatherBoundary(t *testing.T) {
	for _, tc := range []struct {
		amount   uint64
		active   bool
		accepted bool
	}{
		{99999, false, true}, {99999, true, false}, {100000, true, true}, {100001, true, true},
	} {
		t.Run(fmt.Sprintf("amount-%d-active-%t", tc.amount, tc.active), func(t *testing.T) {
			sv, tx := formalBTCIsolationRechargeAdmission(t, tc.amount, tc.active)
			before := formalBTCRows(t, sv)
			ops, r, e := (&WitnessRequestTxHandler{}).DryRun(tx, sv)
			formalBTCIsolationRequireResult(t, ops, r, e, tc.accepted)
			if !tc.accepted {
				if !strings.Contains(r.Error, "minimum") {
					t.Fatalf("not rejected at amount policy: %+v", r)
				}
				if !reflect.DeepEqual(before, formalBTCRows(t, sv)) {
					t.Fatal("small BTC admission consumed fee/bond/native lock")
				}
				return
			}
			req := requesterBondStored(t, sv, tx.GetTxId())
			wantPolicy := uint32(0)
			if tc.active {
				wantPolicy = 3
			}
			if req.BtcProtocolVersion != wantPolicy {
				t.Fatal("admission policy was not frozen")
			}
			                                                                       
			                                                                      
			if !tc.active {
				enableBTCIsolationForTest(t, sv)
			}
			req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
			req.OriginalPositions = []*pb.WitnessVote{{WitnessAddress: utils.GenerateTestIdentity(6102).Address, VoteType: pb.WitnessVoteType_VOTE_PASS}}
			sv.Set(keys.KeyRechargeRequest(req.RequestId), mustMarshalProto(t, req))
			if e := applyRechargeFinalized(sv, req, 100, 1); e != nil {
				t.Fatal(e)
			}
			stored := requesterBondStored(t, sv, req.RequestId)
			if stored.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED {
				t.Fatalf("admitted evidence was retroactively rejected: %+v", stored)
			}
			if stored.BtcProtocolVersion != wantPolicy {
				t.Fatal("terminal hot marker lost the admission policy")
			}
			raw, found, e := sv.Get(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout))
			var u pb.FrostUtxo
			if e != nil || !found || proto.Unmarshal(raw, &u) != nil || u.Amount != strconv.FormatUint(tc.amount, 10) || len(u.OriginId) != 32 || u.OriginLegacy == tc.active {
				t.Fatalf("origin/policy projection: %+v %v", &u, e)
			}
			if !bytes.Equal(u.ScriptPubkey, req.NativeScript) {
				t.Fatal("registered outpoint script changed")
			}
		})
	}
}

func TestFormalBTCJobIsolationLegacyClaimCutpoint(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-claim-%t", existing), func(t *testing.T) {
			f := newBTCWithdrawValidationFixture(t)
			tx := f.claimTx(t, f.template)
			if existing {
				ops, r, e := (&FrostWithdrawClaimTxHandler{}).DryRun(tx, f.sv)
				if e != nil || r.Status != "SUCCEED" {
					t.Fatalf("pre-v3 Claim: %v %+v", e, r)
				}
				applyWriteOpsToStateView(f.sv, ops)
			}
			enableBTCIsolationForTest(t, f.sv)
			before := formalBTCRows(t, f.sv)
			ops, r, e := (&FrostWithdrawClaimTxHandler{}).DryRun(tx, f.sv)
			formalBTCIsolationRequireResult(t, ops, r, e, existing)
			if !reflect.DeepEqual(before, formalBTCRows(t, f.sv)) {
				t.Fatal("legacy boundary replay/reservation changed state")
			}
			if existing {
				mutant := proto.Clone(tx).(*pb.AnyTx)
				mutant.GetFrostWithdrawClaimTx().WithdrawIds = []string{"different-parent"}
				ops, r, e = (&FrostWithdrawClaimTxHandler{}).DryRun(mutant, f.sv)
				formalBTCIsolationRequireResult(t, ops, r, e, false)
			}
		})
	}
}

func TestFormalBTCJobIsolationNoActivationFromBinaryVersionAlone(t *testing.T) {
	h := formalBTCIsolationHarnessNew(t)
	h.sv.Del(keys.KeyProtocolVersion())
	h.sv.Del(keys.KeyProtocolUpgradeReceipt(3))
	active, e := chain.BTCIsolationActive(h.sv.Get)
	if e != nil || active {
		t.Fatalf("binary v%d silently activated: %t %v", protocol.CurrentVersion, active, e)
	}
	before := formalBTCRows(t, h.sv)
	for _, height := range []uint64{1, 42600, 1000000000} {
		if e := (&Executor{networkChainID: 51025}).applyProtocolMigrations(h.sv, height); e != nil {
			t.Fatal(e)
		}
	}
	if !reflect.DeepEqual(before, formalBTCRows(t, h.sv)) {
		t.Fatal("unscheduled real network received v3 activation")
	}
}

func TestFormalBTCJobIsolationIntentHashBindsEveryEconomicField(t *testing.T) {
	base := chain.BTCWithdrawPartIntent{Network: 51025, BTCNetwork: "mainnet", ParentID: "parent-a", PartSeq: 1,
		VaultID: 0, KeyEpoch: 1, Gross: 200000, Fee: 4000, OriginID: bytes.Repeat([]byte{1}, 32), TemplateHash: bytes.Repeat([]byte{2}, 32)}
	want := chain.BTCWithdrawPartJobID(base)
	for _, tc := range []struct {
		name string
		edit func(*chain.BTCWithdrawPartIntent)
	}{
		{"network", func(v *chain.BTCWithdrawPartIntent) { v.Network++ }}, {"btc-network", func(v *chain.BTCWithdrawPartIntent) { v.BTCNetwork = "testnet" }},
		{"parent", func(v *chain.BTCWithdrawPartIntent) { v.ParentID = "parent-b" }}, {"part", func(v *chain.BTCWithdrawPartIntent) { v.PartSeq++ }},
		{"vault", func(v *chain.BTCWithdrawPartIntent) { v.VaultID++ }}, {"epoch", func(v *chain.BTCWithdrawPartIntent) { v.KeyEpoch++ }},
		{"gross", func(v *chain.BTCWithdrawPartIntent) { v.Gross++ }}, {"fee", func(v *chain.BTCWithdrawPartIntent) { v.Fee++ }},
		{"origin", func(v *chain.BTCWithdrawPartIntent) { v.OriginID = bytes.Repeat([]byte{3}, 32) }}, {"template", func(v *chain.BTCWithdrawPartIntent) { v.TemplateHash = bytes.Repeat([]byte{4}, 32) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			tc.edit(&v)
			if chain.BTCWithdrawPartJobID(v) == want {
				t.Fatal("economic intent field is not bound")
			}
		})
	}
	manifest := &pb.FrostVaultMigrationState{MigrationId: "migration-a", VaultId: 0, OldEpoch: 1, NewEpoch: 2,
		OldGroupPubkey: bytes.Repeat([]byte{1}, 32), NewGroupPubkey: bytes.Repeat([]byte{2}, 32), BtcManifestHash: bytes.Repeat([]byte{3}, 32),
		BtcChildCount: 2, BtcTotalInputAmount: "200000", BtcTotalOutputAmount: "192000", BtcTotalFeeAmount: "8000", BtcProtocolVersion: 3, AuthorityProtocol: 3}
	old := chain.BTCMigrationManifestIntentHash(51025, "mainnet", manifest)
	for _, edit := range []func(*pb.FrostVaultMigrationState){func(v *pb.FrostVaultMigrationState) { v.BtcChildCount++ }, func(v *pb.FrostVaultMigrationState) { v.BtcTotalFeeAmount = "8001" }, func(v *pb.FrostVaultMigrationState) { v.BtcManifestHash = bytes.Repeat([]byte{4}, 32) }} {
		v := proto.Clone(manifest).(*pb.FrostVaultMigrationState)
		edit(v)
		if bytes.Equal(chain.BTCMigrationManifestIntentHash(51025, "mainnet", v), old) {
			t.Fatal("manifest working-set/count/fee is not bound")
		}
	}
}
