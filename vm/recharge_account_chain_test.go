package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/txverify"
	"dex/utils"
	"dex/witness"
	"fmt"
	"google.golang.org/protobuf/proto"
	"reflect"
	"strings"
	"testing"
)

func seedAccountDepositAudit(t *testing.T, chain string) (StateView, *WitnessRequestTxHandler) {
	t.Helper()
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	seedChallengeSettlementFBToken(t, sv, "100000000000")
	cfg := &pb.FrostVaultConfig{Chain: chain, VaultCount: 2, CommitteeSize: 4, ThresholdRatio: 0.75, DepositProtocol: protocol.DepositProtocolVault, DepositDexChainId: 1, VaultRefs: []string{"0x2222222222222222222222222222222222222222", "0x3333333333333333333333333333333333333333"}}
	data, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostVaultConfig(chain, 0), data)
	for id := uint32(0); id < 2; id++ {
		putTestVaultStateWithPubkeyHex(t, sv, chain, id, VaultStatusActive, btcRechargeTestPubKeyHex)
	}
	h := &WitnessRequestTxHandler{}
	h.SetWitnessService(witness.NewService(witness.DefaultConfig()))
	return sv, h
}

func TestGlobalRechargePendingMigrationChoosesEarliestLegacyRequest(t *testing.T) {
	sv, _ := seedAccountDepositAudit(t, "eth")
	hash := "0x" + strings.Repeat("ab", 32)
	requests := []*pb.RechargeRequest{
		{RequestId: "later", NativeChain: "eth", NativeTxHash: hash, DepositEventIndex: 2, DepositProtocol: protocol.DepositProtocolVault, CreateHeight: 12, Status: pb.RechargeRequestStatus_RECHARGE_VOTING},
		{RequestId: "earlier", NativeChain: "eth", NativeTxHash: hash, DepositEventIndex: 2, DepositProtocol: protocol.DepositProtocolVault, CreateHeight: 10, Status: pb.RechargeRequestStatus_RECHARGE_PENDING},
	}
	for _, request := range requests {
		raw, err := proto.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyRechargeRequest(request.RequestId), raw)
		legacyKey := fmt.Sprintf("%seth_%s_%010d_%s", keys.KeyRechargeEventPendingPrefix(), hash, request.DepositEventIndex, request.RequestId)
		sv.Set(legacyKey, []byte(request.RequestId))
	}
	if err := ensureGlobalRechargePendingIndex(sv); err != nil {
		t.Fatal(err)
	}
	global := keys.KeyRechargeEventPending("eth", hash, 2)
	if owner, ok, _ := sv.Get(global); !ok || string(owner) != "earlier" {
		t.Fatalf("global owner=%q exists=%t", owner, ok)
	}
	locks, err := sv.Scan(keys.KeyRechargeEventPendingPrefix())
	if err != nil || len(locks) != 1 {
		t.Fatalf("pending locks=%v err=%v", locks, err)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargePendingMigration()); !ok {
		t.Fatal("migration marker missing")
	}
	if err := ensureGlobalRechargePendingIndex(sv); err != nil {
		t.Fatal(err)
	}
}

func signedAccountDepositAudit(t *testing.T, identity int, chain string, index uint32, nonce uint64) *pb.AnyTx {
	t.Helper()
	user := utils.GenerateTestIdentity(identity)
	hash := strings.Repeat("ab", 32)
	if chain != "trx" {
		hash = "0x" + hash
	}
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{Base: &pb.BaseMessage{FromAddress: user.Address, ChainId: 1, Nonce: nonce, Fee: "1000", ExecutedHeight: 10}, NativeChain: chain, NativeTxHash: hash, Amount: "100", ReceiverAddress: user.Address, VaultId: 1, DepositProtocol: protocol.DepositProtocolVault, DepositEventIndex: index, RechargeFee: "1000000"}}}
	if _, err := txverify.SignWithPrivateKey(tx, user.PrivateKeyHex); err != nil {
		t.Fatal(err)
	}
	if err := txverify.Verify(tx, 1); err != nil {
		t.Fatal(err)
	}
	return tx
}

func admitAccountDepositAudit(t *testing.T, sv StateView, h *WitnessRequestTxHandler, tx *pb.AnyTx) *pb.RechargeRequest {
	t.Helper()
	if GetBalance(sv, tx.GetWitnessRequestTx().Base.FromAddress, FeeToken).Balance == "0" {
		SetBalance(sv, tx.GetWitnessRequestTx().Base.FromAddress, FeeToken, &pb.TokenBalance{Balance: "10000000000"})
	}
	ops, r, err := h.DryRun(tx, sv)
	if err != nil || r == nil || r.Status != "SUCCEED" {
		t.Fatalf("admit: %+v %v", r, err)
	}
	for _, op := range ops {
		if err := applyWriteOpWithRechargeIndexes(sv, op, 1, 0, 10); err != nil {
			t.Fatal(err)
		}
	}
	return loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(tx.GetTxId()))
}

func TestAccountDepositGlobalPendingLockAndAtomicEventConsumption(t *testing.T) {
	for _, chain := range []string{"eth", "bnb", "base", "arb", "trx"} {
		t.Run(chain, func(t *testing.T) {
			sv, h := seedAccountDepositAudit(t, chain)
			bad := admitAccountDepositAudit(t, sv, h, signedAccountDepositAudit(t, 8101, chain, 2, 1))
			if _, r, err := h.DryRun(signedAccountDepositAudit(t, 8102, chain, 2, 1), sv); err != nil || r == nil || r.Status != "FAILED" {
				t.Fatalf("second requester bypassed global pending lock: receipt=%+v err=%v", r, err)
			}
			                                                                          
			if err := applyRechargeRejected(sv, bad, 1); err != nil {
				t.Fatal(err)
			}
			good := admitAccountDepositAudit(t, sv, h, signedAccountDepositAudit(t, 8102, chain, 2, 1))
			if good.VaultId != 1 || good.DepositEventIndex != 2 || good.DepositProtocol != protocol.DepositProtocolVault {
				t.Fatalf("lost protocol fields: %+v", good)
			}
			if owner, ok, _ := sv.Get(accountChainRechargePendingKey(good)); !ok || string(owner) != good.RequestId {
				t.Fatal("corrected request did not acquire global pending lock")
			}
			if _, r, err := h.DryRun(signedAccountDepositAudit(t, 8103, chain, 2, 1), sv); err != nil || r == nil || r.Status != "FAILED" {
				t.Fatalf("third requester bypassed global pending lock: receipt=%+v err=%v", r, err)
			}
			if err := applyRechargeFinalized(sv, good, 100, 1); err != nil {
				t.Fatal(err)
			}
			if err := applyRechargeFinalized(sv, good, 101, 1); err != nil {
				t.Fatal(err)
			}
			if got := GetBalance(sv, good.ReceiverAddress, good.TokenAddress).Balance; got != "100" {
				t.Fatalf("honest balance=%s", got)
			}
			if owner, ok, _ := sv.Get(keys.KeyRechargeEvent(chain, good.NativeTxHash, 2)); !ok || string(owner) != good.RequestId {
				t.Fatal("event consumption overwritten")
			}
			if _, ok, _ := sv.Get(accountChainRechargePendingKey(good)); ok {
				t.Fatal("credited pending lock not removed")
			}
			if _, r, _ := h.DryRun(signedAccountDepositAudit(t, 8104, chain, 2, 1), sv); r == nil || r.Status != "FAILED" {
				t.Fatal("consumed event accepted")
			}
			                                                    
			next := admitAccountDepositAudit(t, sv, h, signedAccountDepositAudit(t, 8102, chain, 3, 2))
			if err := applyRechargeFinalized(sv, next, 102, 1); err != nil {
				t.Fatal(err)
			}
			if got := GetBalance(sv, good.ReceiverAddress, good.TokenAddress).Balance; got != "200" {
				t.Fatalf("distinct event not credited: %s", got)
			}
		})
	}
}

func TestAccountDepositRejectsInvalidAndNoncanonicalAdmission(t *testing.T) {
	for _, mutation := range []string{"disabled", "space", "uppercase", "receiver", "chain", "vault", "btcfields", "no_domain"} {
		t.Run(mutation, func(t *testing.T) {
			sv, h := seedAccountDepositAudit(t, "eth")
			tx := signedAccountDepositAudit(t, 8101, "eth", 0, 1)
			r := tx.GetWitnessRequestTx()
			switch mutation {
			case "disabled":
				r.DepositProtocol = 0
			case "space":
				r.NativeTxHash += " "
			case "uppercase":
				r.NativeTxHash = "0x" + strings.ToUpper(r.NativeTxHash[2:])
			case "receiver":
				r.ReceiverAddress = "not-an-account"
			case "chain":
				r.Base.ChainId = 2
			case "vault":
				r.VaultId = 2
			case "btcfields":
				r.NativeVout = 1
			case "no_domain":
				cfg, _ := loadFrostVaultConfig(sv, "eth")
				cfg.DepositDexChainId = 0
				data, _ := proto.Marshal(cfg)
				sv.Set(keys.KeyFrostVaultConfig("eth", 0), data)
			}
			if _, receipt, err := h.DryRun(tx, sv); receipt == nil || receipt.Status != "FAILED" || err == nil {
				t.Fatalf("accepted %s: %+v %v", mutation, receipt, err)
			}
		})
	}
}

func TestAccountDepositLegacyMigrationPreservesConflictingLocks(t *testing.T) {
	sv, h := seedAccountDepositAudit(t, "eth")
	tx := signedAccountDepositAudit(t, 8101, "eth", 0, 1)
	hash := tx.GetWitnessRequestTx().NativeTxHash
	k1, k2 := keys.KeyRechargeRequestByNativeTx("eth", hash+" "), keys.KeyRechargeRequestByNativeTx("eth", "0x"+strings.ToUpper(hash[2:]))
	sv.Set(k1, []byte("old-1"))
	sv.Set(k2, []byte("old-2"))
	if _, r, err := h.DryRun(tx, sv); err != nil || r == nil || r.Status != "FAILED" {
		t.Fatalf("legacy alias accepted: %+v %v", r, err)
	}
	if value, ok, _ := sv.Get(keys.KeyRechargeLegacyNative("eth", hash)); !ok || string(value) != "legacy-conflict" {
		t.Fatalf("quarantine=%q", value)
	}
	for _, key := range []string{k1, k2} {
		if _, ok, _ := sv.Get(key); !ok {
			t.Fatal("old liability lock deleted")
		}
	}
}

func TestAccountDepositFinalizationRollsBackOnBalanceFailure(t *testing.T) {
	sv, h := seedAccountDepositAudit(t, "eth")
	req := admitAccountDepositAudit(t, sv, h, signedAccountDepositAudit(t, 8101, "eth", 0, 1))
	data, err := proto.Marshal(&pb.TokenBalanceRecord{Address: req.ReceiverAddress, Token: req.TokenAddress, Balance: &pb.TokenBalance{Balance: "broken"}})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyBalance(req.ReceiverAddress, req.TokenAddress), data)
	before, err := sv.Scan("")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRechargeFinalized(sv, req, 100, 1); err == nil {
		t.Fatal("invalid balance should abort")
	}
	after, err := sv.Scan("")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed finalization changed state: %v", err)
	}
	for _, key := range []string{keys.KeyRechargeCredited(req.RequestId), keys.KeyRechargeEvent("eth", req.NativeTxHash, 0), keys.KeyFrostVaultAvailableBalance("eth", req.TokenAddress, req.VaultId)} {
		if _, ok, _ := sv.Get(key); ok {
			t.Fatalf("failed finalization left %s", key)
		}
	}
	if _, ok, _ := sv.Get(accountChainRechargePendingKey(req)); !ok {
		t.Fatal("failed finalization lost pending request")
	}
}

func TestLegacyAccountRechargeCannotFinalizeOrReopen(t *testing.T) {
	sv, _ := seedAccountDepositAudit(t, "eth")
	req := &pb.RechargeRequest{RequestId: "legacy", NativeChain: "eth", NativeTxHash: "0x" + strings.Repeat("ab", 32), ReceiverAddress: utils.GenerateTestIdentity(8101).Address, Amount: "100", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD}
	data, _ := proto.Marshal(req)
	sv.Set(keys.KeyRechargeRequest(req.RequestId), data)
	sv.Set(keys.KeyRechargeRequestByNativeTx("eth", req.NativeTxHash), []byte(req.RequestId))
	if err := applyRechargeFinalized(sv, req, 100, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); ok {
		t.Fatal("legacy credited")
	}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatal(err)
	}
	if got := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId)); got.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED {
		t.Fatal("legacy reopened")
	}
}
