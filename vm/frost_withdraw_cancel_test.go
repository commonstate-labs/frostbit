package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/txverify"
	"dex/utils"
	"strconv"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

const cancelTestAsset = "cancel-test-token"

func seedWithdrawCancelState(t *testing.T, base map[string][]byte, withdrawID, requester string, seq uint64, status WithdrawRequestStatus, jobID string) {
	t.Helper()
	if _, exists := base[keys.KeyToken(cancelTestAsset)]; !exists {
		base[keys.KeyToken(cancelTestAsset)] = mustMarshalProto(t, &pb.Token{
			Address: cancelTestAsset, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "eth", TotalSupply: "80",
		})
	}
	if requester != "" {
		balanceKey := keys.KeyBalance(requester, cancelTestAsset)
		if _, exists := base[balanceKey]; !exists {
			base[balanceKey] = mustMarshalProto(t, &pb.TokenBalanceRecord{
				Address: requester, Token: cancelTestAsset, Balance: &pb.TokenBalance{Balance: "0"},
			})
		}
	}
	base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, &pb.FrostWithdrawState{
		WithdrawId: withdrawID, TxId: "request-" + withdrawID, Seq: seq, Chain: "eth", Asset: cancelTestAsset,
		To: "0x1111111111111111111111111111111111111111", Amount: "10", GrossAmount: "10", FeeAmount: "1", NetAmount: "9", FeeVersion: 1, RequestHeight: 10,
		Status: string(status), JobId: jobID, Requester: requester, VaultId: 0, LiveSeq: seq, KeyEpoch: 1,
	})
	base[keys.KeyFrostWithdrawLiveIndex("eth", 0, seq)] = []byte(withdrawID)
	base[keys.KeyFrostWithdrawLiveTail("eth", 0)] = []byte(strconv.FormatUint(seq, 10))
	base[keys.KeyFrostWithdrawLivePending("eth", 0)] = []byte(strconv.FormatUint(seq, 10))
	if _, exists := base[keys.KeyFrostWithdrawLiveHead("eth", 0)]; !exists {
		base[keys.KeyFrostWithdrawLiveHead("eth", 0)] = []byte("1")
	}
	base[keys.KeyFrostVaultReservedBalance("eth", cancelTestAsset, 0)] = []byte(strconv.FormatUint(seq*10, 10))
	base[keys.KeyFrostVaultAvailableBalance("eth", cancelTestAsset, 0)] = []byte("0")
	base[keys.KeyFrostWithdrawTxRef("request-"+withdrawID)] = []byte(withdrawID)
}

func cancelTx(withdrawID, requester, txID string, height uint64) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{
		Base: &pb.BaseMessage{TxId: txID, FromAddress: requester, ChainId: 1, ExecutedHeight: height}, WithdrawId: withdrawID,
	}}}
}

func readCancelTestBalance(t *testing.T, sv StateView, requester string) string {
	t.Helper()
	balance, err := getBalanceStrict(sv, requester, cancelTestAsset)
	if err != nil {
		t.Fatalf("read balance: %v", err)
	}
	return balance.Balance
}

func readCancelTestSupply(t *testing.T, sv StateView) string {
	t.Helper()
	data, exists, err := sv.Get(keys.KeyToken(cancelTestAsset))
	if err != nil || !exists {
		t.Fatalf("read token: exists=%v err=%v", exists, err)
	}
	var token pb.Token
	if err := proto.Unmarshal(data, &token); err != nil {
		t.Fatalf("parse token: %v", err)
	}
	return token.TotalSupply
}

func TestFrostWithdrawCancelRefundsAndArchivesHead(t *testing.T) {
	base := map[string][]byte{
		keys.KeyFrostWithdrawFIFOHead("eth", cancelTestAsset): []byte("1"),
		keys.KeyFrostWithdrawFIFOSeq("eth", cancelTestAsset):  []byte("1"),
	}
	seedWithdrawCancelState(t, base, "withdraw-1", "alice", 1, WithdrawStatusQueued, "")
	sv := newMemoryStateView(base)

	ops, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancelTx("withdraw-1", "alice", "cancel-1", 20), sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(ops) == 0 {
		t.Fatalf("cancel failed: err=%v receipt=%+v ops=%d", err, receipt, len(ops))
	}
	if got := readCancelTestBalance(t, sv, "alice"); got != "10" {
		t.Fatalf("refunded balance=%s want 10", got)
	}
	if got := readCancelTestSupply(t, sv); got != "90" {
		t.Fatalf("refunded total supply=%s want 90", got)
	}
	if data, exists, _ := sv.Get(keys.KeyFrostWithdraw("withdraw-1")); !exists {
		t.Fatal("cancelled withdrawal query state missing")
	} else if state, _ := unmarshalWithdrawRequest(data); state.Status != WithdrawStatusCancelled {
		t.Fatalf("cancelled query status=%s", state.Status)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostWithdrawLiveIndex("eth", 0, 1)); exists {
		t.Fatal("cancelled head retained FIFO index")
	}
	if done, exists, _ := sv.Get(keys.KeyArchiveFrostWithdrawDone("eth", cancelTestAsset, 1, "withdraw-1")); !exists || string(done) != "withdraw-1" {
		t.Fatalf("cancelled head terminal index missing: exists=%v value=%q", exists, done)
	}
	archiveData, exists, err := sv.Get(keys.KeyArchiveFrostWithdraw("withdraw-1"))
	if err != nil || !exists {
		t.Fatalf("cancel archive missing: exists=%v err=%v", exists, err)
	}
	archived, err := unmarshalWithdrawRequest(archiveData)
	if err != nil {
		t.Fatalf("parse archived withdraw: %v", err)
	}
	if archived.Status != WithdrawStatusCancelled || archived.CancelTxID != "cancel-1" || archived.CancelHeight != 20 || archived.Requester != "alice" {
		t.Fatalf("unexpected archived cancel state: %+v", archived)
	}
	if head, _, _ := sv.Get(keys.KeyFrostWithdrawLiveHead("eth", 0)); string(head) != "2" {
		t.Fatalf("FIFO head=%q want 2", head)
	}
}

func TestFrostWithdrawCancelOutOfOrderLeavesNoFIFOBarrier(t *testing.T) {
	base := map[string][]byte{
		keys.KeyFrostWithdrawFIFOHead("eth", cancelTestAsset): []byte("1"),
		keys.KeyFrostWithdrawFIFOSeq("eth", cancelTestAsset):  []byte("2"),
	}
	seedWithdrawCancelState(t, base, "withdraw-1", "alice", 1, WithdrawStatusQueued, "")
	seedWithdrawCancelState(t, base, "withdraw-2", "alice", 2, WithdrawStatusQueued, "")
	sv := newMemoryStateView(base)

	if _, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancelTx("withdraw-2", "alice", "cancel-2", 20), sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("cancel non-head failed: err=%v receipt=%+v", err, receipt)
	}
	secondData, exists, _ := sv.Get(keys.KeyFrostWithdraw("withdraw-2"))
	if !exists {
		t.Fatal("non-head cancellation must remain hot until it reaches head")
	}
	second, _ := unmarshalWithdrawRequest(secondData)
	if second.Status != WithdrawStatusCancelled {
		t.Fatalf("non-head status=%s want CANCELLED", second.Status)
	}
	if head, _, _ := sv.Get(keys.KeyFrostWithdrawLiveHead("eth", 0)); string(head) != "1" {
		t.Fatalf("FIFO head moved past live request: %q", head)
	}

	if _, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancelTx("withdraw-1", "alice", "cancel-1", 21), sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("cancel head failed: err=%v receipt=%+v", err, receipt)
	}
	if head, _, _ := sv.Get(keys.KeyFrostWithdrawLiveHead("eth", 0)); string(head) != "3" {
		t.Fatalf("FIFO head=%q want 3 after crossing consecutive cancellations", head)
	}
	for _, id := range []string{"withdraw-1", "withdraw-2"} {
		if data, exists, _ := sv.Get(keys.KeyFrostWithdraw(id)); !exists {
			t.Fatalf("%s terminal query state missing", id)
		} else if state, _ := unmarshalWithdrawRequest(data); state.Status != WithdrawStatusCancelled {
			t.Fatalf("%s query state=%s", id, state.Status)
		}
		archiveData, exists, _ := sv.Get(keys.KeyArchiveFrostWithdraw(id))
		if !exists {
			t.Fatalf("%s was not archived", id)
		}
		archived, _ := unmarshalWithdrawRequest(archiveData)
		if archived.Status != WithdrawStatusCancelled {
			t.Fatalf("%s archive status=%s", id, archived.Status)
		}
	}
	if got := readCancelTestBalance(t, sv, "alice"); got != "20" {
		t.Fatalf("two refunds balance=%s want 20", got)
	}
	if got := readCancelTestSupply(t, sv); got != "100" {
		t.Fatalf("two refunds supply=%s want 100", got)
	}
}

func TestFrostWithdrawCancelRejectsWrongOwnerAndClaimedRequest(t *testing.T) {
	t.Run("wrong owner", func(t *testing.T) {
		base := map[string][]byte{
			keys.KeyFrostWithdrawFIFOHead("eth", cancelTestAsset): []byte("1"),
			keys.KeyFrostWithdrawFIFOSeq("eth", cancelTestAsset):  []byte("1"),
		}
		seedWithdrawCancelState(t, base, "withdraw-1", "alice", 1, WithdrawStatusQueued, "")
		sv := newMemoryStateView(base)
		_, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancelTx("withdraw-1", "mallory", "cancel-bad", 20), sv)
		if err == nil || receipt == nil || !strings.Contains(receipt.Error, "only the withdrawal requester") {
			t.Fatalf("wrong owner accepted: err=%v receipt=%+v", err, receipt)
		}
		if got := readCancelTestSupply(t, sv); got != "80" {
			t.Fatalf("failed cancellation changed supply: %s", got)
		}
	})

	t.Run("claim wins execution order", func(t *testing.T) {
		base := map[string][]byte{
			keys.KeyFrostWithdrawFIFOHead("eth", cancelTestAsset): []byte("1"),
			keys.KeyFrostWithdrawFIFOSeq("eth", cancelTestAsset):  []byte("1"),
		}
		seedWithdrawCancelState(t, base, "withdraw-1", "alice", 1, WithdrawStatusSigning, "job-1")
		sv := newMemoryStateView(base)
		_, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancelTx("withdraw-1", "alice", "cancel-late", 20), sv)
		if err == nil || receipt == nil || !strings.Contains(receipt.Error, "no longer cancellable") {
			t.Fatalf("SIGNING cancellation accepted: err=%v receipt=%+v", err, receipt)
		}
		if got := readCancelTestSupply(t, sv); got != "80" {
			t.Fatalf("late cancellation changed supply: %s", got)
		}
	})
}

func TestFrostWithdrawCancelRejectsBrokenFIFOEntryWithoutRefund(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutateBase func(map[string][]byte)
	}{
		{
			name: "missing index",
			mutateBase: func(base map[string][]byte) {
				delete(base, keys.KeyFrostWithdrawLiveIndex("eth", 0, 2))
			},
		},
		{
			name: "mismatched index",
			mutateBase: func(base map[string][]byte) {
				base[keys.KeyFrostWithdrawLiveIndex("eth", 0, 2)] = []byte("different-withdraw")
			},
		},
		{
			name: "sequence beyond tail",
			mutateBase: func(base map[string][]byte) {
				base[keys.KeyFrostWithdrawLiveTail("eth", 0)] = []byte("1")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := map[string][]byte{
				keys.KeyFrostWithdrawFIFOHead("eth", cancelTestAsset): []byte("1"),
				keys.KeyFrostWithdrawFIFOSeq("eth", cancelTestAsset):  []byte("2"),
			}
			seedWithdrawCancelState(t, base, "withdraw-1", "alice", 1, WithdrawStatusQueued, "")
			seedWithdrawCancelState(t, base, "withdraw-2", "alice", 2, WithdrawStatusQueued, "")
			tc.mutateBase(base)
			sv := newMemoryStateView(base)

			_, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancelTx("withdraw-2", "alice", "cancel-broken", 20), sv)
			if err == nil || receipt == nil || (!strings.Contains(receipt.Error, "FIFO") && !strings.Contains(receipt.Error, "live index")) {
				t.Fatalf("broken FIFO cancellation accepted: err=%v receipt=%+v", err, receipt)
			}
			if got := readCancelTestBalance(t, sv, "alice"); got != "0" {
				t.Fatalf("broken FIFO cancellation refunded balance=%s", got)
			}
			if got := readCancelTestSupply(t, sv); got != "80" {
				t.Fatalf("broken FIFO cancellation changed supply=%s", got)
			}
			data, exists, _ := sv.Get(keys.KeyFrostWithdraw("withdraw-2"))
			if !exists {
				t.Fatal("broken FIFO cancellation deleted withdraw")
			}
			state, _ := unmarshalWithdrawRequest(data)
			if state.Status != WithdrawStatusQueued {
				t.Fatalf("broken FIFO cancellation changed status=%s", state.Status)
			}
		})
	}
}

func TestFrostWithdrawCancelLegacyProof(t *testing.T) {
	identity := utils.GenerateTestIdentity(91)
	original := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
		Base:  &pb.BaseMessage{FromAddress: identity.Address, ChainId: 1, Nonce: 7, Fee: "100"},
		Chain: "eth", Asset: cancelTestAsset, To: "0x1111111111111111111111111111111111111111", Amount: "10",
	}}}
	requestTxID, err := txverify.SignWithPrivateKey(original, identity.PrivateKeyHex)
	if err != nil {
		t.Fatalf("sign legacy request: %v", err)
	}
	proof, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("marshal legacy proof: %v", err)
	}

	base := map[string][]byte{
		keys.KeyToken(cancelTestAsset): mustMarshalProto(t, &pb.Token{
			Address: cancelTestAsset, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "eth", TotalSupply: "90",
		}),
		keys.KeyBalance(identity.Address, cancelTestAsset): mustMarshalProto(t, &pb.TokenBalanceRecord{
			Address: identity.Address, Token: cancelTestAsset, Balance: &pb.TokenBalance{Balance: "0"},
		}),
		keys.KeyFrostWithdrawTxRef(requestTxID): []byte("legacy-withdraw"),
	}
	seedWithdrawCancelState(t, base, "legacy-withdraw", "", 1, WithdrawStatusQueued, "")
	var legacyState pb.FrostWithdrawState
	_ = proto.Unmarshal(base[keys.KeyFrostWithdraw("legacy-withdraw")], &legacyState)
	legacyState.TxId = requestTxID
	base[keys.KeyFrostWithdraw("legacy-withdraw")] = mustMarshalProto(t, &legacyState)
	sv := newMemoryStateView(base)
	cancel := cancelTx("legacy-withdraw", identity.Address, "cancel-legacy", 30)
	cancel.GetFrostWithdrawCancelTx().OriginalRequestTx = proof
	if _, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancel, sv); err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("valid legacy proof rejected: err=%v receipt=%+v", err, receipt)
	}
	archiveData, exists, _ := sv.Get(keys.KeyArchiveFrostWithdraw("legacy-withdraw"))
	if !exists {
		t.Fatal("legacy cancellation was not archived")
	}
	archived, _ := unmarshalWithdrawRequest(archiveData)
	if archived.Requester != identity.Address || archived.Status != WithdrawStatusCancelled {
		t.Fatalf("legacy requester/status not recovered: %+v", archived)
	}
}

func TestFrostWithdrawCancelLegacyProofRejectsTampering(t *testing.T) {
	identity := utils.GenerateTestIdentity(92)
	original := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
		Base: &pb.BaseMessage{FromAddress: identity.Address, ChainId: 1}, Chain: "eth", Asset: cancelTestAsset,
		To: "0x1111111111111111111111111111111111111111", Amount: "10",
	}}}
	requestTxID, err := txverify.SignWithPrivateKey(original, identity.PrivateKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	original.GetFrostWithdrawRequestTx().Amount = "11"
	proof, _ := proto.Marshal(original)
	base := map[string][]byte{keys.KeyFrostWithdrawTxRef(requestTxID): []byte("legacy-withdraw")}
	seedWithdrawCancelState(t, base, "legacy-withdraw", "", 1, WithdrawStatusQueued, "")
	var legacyState pb.FrostWithdrawState
	_ = proto.Unmarshal(base[keys.KeyFrostWithdraw("legacy-withdraw")], &legacyState)
	legacyState.TxId = requestTxID
	base[keys.KeyFrostWithdraw("legacy-withdraw")] = mustMarshalProto(t, &legacyState)
	sv := newMemoryStateView(base)
	cancel := cancelTx("legacy-withdraw", identity.Address, "cancel-legacy", 30)
	cancel.GetFrostWithdrawCancelTx().OriginalRequestTx = proof
	_, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancel, sv)
	if err == nil || receipt == nil || !strings.Contains(receipt.Error, "invalid original withdrawal signature") {
		t.Fatalf("tampered legacy proof accepted: err=%v receipt=%+v", err, receipt)
	}
}

func TestFrostWithdrawCancelLegacyBTCBelowCurrentMinimum(t *testing.T) {
	const asset = "legacy-btc-token"
	identity := utils.GenerateTestIdentity(93)
	receiver := btcAdmissionAddressForTest(t, &chaincfg.MainNetParams, 93)
	original := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
		Base: &pb.BaseMessage{FromAddress: identity.Address, ChainId: 1, Nonce: 3}, Chain: "btc", Asset: asset,
		To: receiver, Amount: "2500",                                               
	}}}
	requestTxID, err := txverify.SignWithPrivateKey(original, identity.PrivateKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := proto.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string][]byte{
		keys.KeyToken(asset): mustMarshalProto(t, &pb.Token{
			Address: asset, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: "btc", TotalSupply: "2500",
		}),
		keys.KeyFrostVaultConfig("btc", 0): mustMarshalProto(t, &pb.FrostVaultConfig{Chain: "btc", BtcNetwork: "mainnet"}),
		keys.KeyBalance(identity.Address, asset): mustMarshalProto(t, &pb.TokenBalanceRecord{
			Address: identity.Address, Token: asset, Balance: &pb.TokenBalance{Balance: "0"},
		}),
		keys.KeyFrostWithdrawFIFOHead("btc", asset):     []byte("1"),
		keys.KeyFrostWithdrawFIFOSeq("btc", asset):      []byte("1"),
		keys.KeyFrostWithdrawFIFOIndex("btc", asset, 1): []byte("legacy-btc-withdraw"),
		keys.KeyFrostWithdrawTxRef(requestTxID):         []byte("legacy-btc-withdraw"),
		keys.KeyFrostWithdraw("legacy-btc-withdraw"): mustMarshalProto(t, &pb.FrostWithdrawState{
			WithdrawId: "legacy-btc-withdraw", TxId: requestTxID, Seq: 1, Chain: "btc", Asset: asset,
			To: receiver, Amount: "2500", Status: "QUEUED",
		}),
	}
	sv := newMemoryStateView(base)
	cancel := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawCancelTx{FrostWithdrawCancelTx: &pb.FrostWithdrawCancelTx{
		Base:       &pb.BaseMessage{TxId: "cancel-legacy-btc", FromAddress: identity.Address, ChainId: 1, ExecutedHeight: 50},
		WithdrawId: "legacy-btc-withdraw", OriginalRequestTx: proof,
	}}}
	if _, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancel, sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("legacy BTC request below current minimum was not cancellable: err=%v receipt=%+v", err, receipt)
	}
	balance, err := getBalanceStrict(sv, identity.Address, asset)
	if err != nil || balance.Balance != "2500" {
		t.Fatalf("legacy BTC refund balance=%v err=%v", balance, err)
	}
}
