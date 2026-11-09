package vm

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestFrostWithdrawSignedDryRun_NonBTCInsufficientLots_NoMutation(t *testing.T) {
	chainName := "eth"
	asset := "USDT"
	vaultID := uint32(1)
	withdrawID := "wid_insufficient"

	base := map[string][]byte{}
	base[keys.KeyFrostWithdrawFIFOHead(chainName, asset)] = []byte("1")
	base[keys.KeyFrostFundsLotHead(chainName, asset, vaultID)] = []byte("10|1")
	base[keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, 10)] = []byte("2")
	                                                                             
	                                                                               
	base[keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID)] = []byte("100")

	withdraw := &pb.FrostWithdrawState{
		WithdrawId:   withdrawID,
		TxId:         "tx_req_insufficient",
		Seq:          1,
		Chain:        chainName,
		Asset:        asset,
		To:           "0x00000000000000000000000000000000000000bb",
		Amount:       "100",
		BroadcastFee: "1",
		Status:       string(WithdrawStatusQueued),
		VaultId:      vaultID,
	}
	base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, withdraw)

	base[keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 1)] = []byte("recharge_60")
	base[keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 2)] = []byte("recharge_20")
	base[keys.KeyRechargeRequest("recharge_60")] = mustMarshalProto(t, &pb.RechargeRequest{RequestId: "recharge_60", Amount: "60"})
	base[keys.KeyRechargeRequest("recharge_20")] = mustMarshalProto(t, &pb.RechargeRequest{RequestId: "recharge_20", Amount: "20"})

	tplHash, jobID := seedAccountChainClaimStub(t, base, chainName, asset, vaultID, []string{withdrawID})

	sv := newReadOnlyBaseStateView(base)
	handler := &FrostWithdrawSignedTxHandler{}
	signed := &pb.FrostWithdrawSignedTx{
		Base: &pb.BaseMessage{
			TxId:           "tx_signed_insufficient",
			FromAddress:    "coordinator",
			ExecutedHeight: 100,
		},
		JobId:              jobID,
		SignedPackageBytes: SignAccountClaimForTest(t, base, chainName, asset, vaultID, []string{withdrawID}),
		GuardianSigs:       AccountGuardianClaimForTest(t, base, chainName, asset, vaultID, []string{withdrawID}),
		WithdrawIds:        []string{withdrawID},
		VaultId:            vaultID,
		KeyEpoch:           1,
		Chain:              chainName,
		TemplateHash:       tplHash,
	}
	populateMixedSignedTxFromClaim(t, base, signed)
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}

	ops, receipt, err := handler.DryRun(tx, sv)
	if err == nil {
		t.Fatalf("expect insufficient lots error, got nil receipt=%+v", receipt)
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("expect FAILED receipt, got %+v", receipt)
	}
	if !strings.Contains(receipt.Error, "insufficient Vault funds lots") {
		t.Fatalf("unexpected receipt error: %s", receipt.Error)
	}
	if len(ops) != 0 {
		t.Fatalf("expect no write ops on insufficient lots, got %d", len(ops))
	}
	if len(sv.Diff()) != 0 {
		t.Fatalf("expect no state diff on insufficient lots, got %d", len(sv.Diff()))
	}

	headData, _, _ := sv.Get(keys.KeyFrostFundsLotHead(chainName, asset, vaultID))
	if string(headData) != "10|1" {
		t.Fatalf("funds lot head must stay unchanged, got %s", string(headData))
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID)); string(available) != "0" {
		t.Fatalf("failed Signed changed Claim-deducted available balance: %q", string(available))
	}
	if reserved, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)); string(reserved) != "100" {
		t.Fatalf("failed Signed consumed its reservation: %q", string(reserved))
	}
	withdrawData, _, _ := sv.Get(keys.KeyFrostWithdraw(withdrawID))
	var gotWithdraw pb.FrostWithdrawState
	if err := unmarshalProtoCompat(withdrawData, &gotWithdraw); err != nil {
		t.Fatalf("unmarshal withdraw failed: %v", err)
	}
	if gotWithdraw.Status != string(WithdrawStatusSigning) || gotWithdraw.JobId != jobID {
		t.Fatalf("withdraw claim state changed after failed signing: %+v", &gotWithdraw)
	}
}

func TestFrostWithdrawSignedDryRun_NonBTCTwoPhaseConsume_Succeed(t *testing.T) {
	chainName := "eth"
	asset := "USDT"
	vaultID := uint32(2)
	withdrawID := "wid_succeed"

	base := map[string][]byte{}
	base[keys.KeyFrostWithdrawFIFOHead(chainName, asset)] = []byte("1")
	base[keys.KeyFrostFundsLotHead(chainName, asset, vaultID)] = []byte("10|1")
	base[keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, 10)] = []byte("2")
	base[keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, 11)] = []byte("1")
	base[keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID)] = []byte("140")

	withdraw := &pb.FrostWithdrawState{
		WithdrawId:   withdrawID,
		TxId:         "tx_req_succeed",
		Seq:          1,
		Chain:        chainName,
		Asset:        asset,
		To:           "0x00000000000000000000000000000000000000bb",
		Amount:       "100",
		BroadcastFee: "1",
		Status:       string(WithdrawStatusQueued),
		VaultId:      vaultID,
	}
	base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, withdraw)

	base[keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 1)] = []byte("recharge_60")
	base[keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 2)] = []byte("recharge_50")
	base[keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 11, 1)] = []byte("recharge_30")
	base[keys.KeyRechargeRequest("recharge_60")] = mustMarshalProto(t, &pb.RechargeRequest{RequestId: "recharge_60", Amount: "60"})
	base[keys.KeyRechargeRequest("recharge_50")] = mustMarshalProto(t, &pb.RechargeRequest{RequestId: "recharge_50", Amount: "50"})
	base[keys.KeyRechargeRequest("recharge_30")] = mustMarshalProto(t, &pb.RechargeRequest{RequestId: "recharge_30", Amount: "30"})

	tplHash, jobID := seedAccountChainClaimStub(t, base, chainName, asset, vaultID, []string{withdrawID})

	sv := newReadOnlyBaseStateView(base)
	handler := &FrostWithdrawSignedTxHandler{}
	signed := &pb.FrostWithdrawSignedTx{
		Base: &pb.BaseMessage{
			TxId:           "tx_signed_succeed",
			FromAddress:    "coordinator",
			ExecutedHeight: 100,
		},
		JobId:              jobID,
		SignedPackageBytes: SignAccountClaimForTest(t, base, chainName, asset, vaultID, []string{withdrawID}),
		GuardianSigs:       AccountGuardianClaimForTest(t, base, chainName, asset, vaultID, []string{withdrawID}),
		WithdrawIds:        []string{withdrawID},
		VaultId:            vaultID,
		KeyEpoch:           1,
		Chain:              chainName,
		TemplateHash:       tplHash,
	}
	populateMixedSignedTxFromClaim(t, base, signed)
	tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed}}

	ops, receipt, err := handler.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("expected success, got err=%v receipt=%+v", err, receipt)
	}
	if receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if len(ops) != 0 || receipt.WriteCount == 0 {
		t.Fatal("direct StateView writes must be reported once and not returned again")
	}

	headData, _, _ := sv.Get(keys.KeyFrostFundsLotHead(chainName, asset, vaultID))
	if string(headData) != "10|2" {
		t.Fatalf("unexpected funds lot head after partial consume, got %s want 10|2", string(headData))
	}
	balanceData, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID))
	if string(balanceData) != "40" {
		t.Fatalf("Signed must not deduct available a second time, got %s want 40", string(balanceData))
	}
	if lot, exists, _ := sv.Get(keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 1)); exists || len(lot) > 0 {
		t.Fatalf("consumed first funds lot should be deleted: exists=%v data=%x", exists, lot)
	}
	if lot, exists, _ := sv.Get(keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 2)); !exists || string(lot) != "recharge_50" {
		t.Fatalf("partially consumed second funds lot should remain: exists=%v data=%q", exists, string(lot))
	}
	if remaining, exists, _ := sv.Get(keys.KeyFrostFundsLotRemaining(chainName, asset, vaultID, 10, 2)); !exists || string(remaining) != "10" {
		t.Fatalf("second funds lot should retain exact remainder 10: exists=%v data=%q", exists, string(remaining))
	}
	if reserved, _, _ := sv.Get(keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)); string(reserved) != "0" {
		t.Fatalf("Signed should consume the Claim reservation, got %q", string(reserved))
	}
	ledgerAvailable, foundLots, err := fundsLotAvailableBalance(sv, chainName, asset, vaultID)
	if err != nil || !foundLots || ledgerAvailable.String() != "40" {
		t.Fatalf("partial lot ledger mismatch: available=%v found=%v err=%v", ledgerAvailable, foundLots, err)
	}

	if hotWithdraw, exists, _ := sv.Get(keys.KeyFrostWithdraw(withdrawID)); exists || len(hotWithdraw) > 0 {
		t.Fatalf("hot withdraw should be pruned after signing")
	}
	withdrawData, _, _ := sv.Get(keys.KeyArchiveFrostWithdraw(withdrawID))
	var gotWithdraw pb.FrostWithdrawState
	if err := unmarshalProtoCompat(withdrawData, &gotWithdraw); err != nil {
		t.Fatalf("unmarshal withdraw failed: %v", err)
	}
	if gotWithdraw.Status != string(WithdrawStatusSigned) {
		t.Fatalf("withdraw status should be SIGNED, got %s", gotWithdraw.Status)
	}
	if gotWithdraw.JobId != jobID {
		t.Fatalf("unexpected withdraw job id: %s", gotWithdraw.JobId)
	}
}

func TestConsumeVaultFundsLotsContinuesFromPartialRemainder(t *testing.T) {
	const (
		chainName = "eth"
		asset     = "USDT"
	)
	vaultID := uint32(9)
	base := map[string][]byte{
		keys.KeyFrostFundsLotHead(chainName, asset, vaultID):         []byte("10|1"),
		keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, 10):      []byte("1"),
		keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 1): []byte("recharge_50"),
		keys.KeyRechargeRequest("recharge_50"):                       mustMarshalProto(t, &pb.RechargeRequest{RequestId: "recharge_50", Amount: "50"}),
	}
	sv := newReadOnlyBaseStateView(base)
	handler := &FrostWithdrawSignedTxHandler{}

	consumed, hasLots, err := handler.consumeVaultFundsLots(sv, chainName, asset, vaultID, big.NewInt(40))
	if err != nil || !hasLots || consumed.String() != "40" {
		t.Fatalf("first partial consumption failed: consumed=%v hasLots=%v err=%v", consumed, hasLots, err)
	}
	if remaining, exists, _ := sv.Get(keys.KeyFrostFundsLotRemaining(chainName, asset, vaultID, 10, 1)); !exists || string(remaining) != "10" {
		t.Fatalf("partial remainder = %q exists=%v, want 10", string(remaining), exists)
	}

	consumed, hasLots, err = handler.consumeVaultFundsLots(sv, chainName, asset, vaultID, big.NewInt(10))
	if err != nil || !hasLots || consumed.String() != "10" {
		t.Fatalf("remainder consumption failed: consumed=%v hasLots=%v err=%v", consumed, hasLots, err)
	}
	if lot, exists, _ := sv.Get(keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 1)); exists || len(lot) != 0 {
		t.Fatalf("fully consumed lot remains: exists=%v lot=%q", exists, string(lot))
	}
	if remaining, exists, _ := sv.Get(keys.KeyFrostFundsLotRemaining(chainName, asset, vaultID, 10, 1)); exists || len(remaining) != 0 {
		t.Fatalf("fully consumed remainder marker remains: exists=%v value=%q", exists, string(remaining))
	}
}

func TestFundsLotHeadFindsFirstLotAboveLegacyProbeWindow(t *testing.T) {
	const (
		chainName = "eth"
		asset     = "USDT"
		height    = uint64(50_000)
	)
	vaultID := uint32(10)
	base := map[string][]byte{
		keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, height):      []byte("1"),
		keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, height, 1): []byte("late-first-deposit"),
	}
	sv := newReadOnlyBaseStateView(base)

	requestID, ok, err := getFundsLotAtHeadStrict(sv, chainName, asset, vaultID)
	if err != nil || !ok || requestID != "late-first-deposit" {
		t.Fatalf("high first lot not found: request=%q ok=%v err=%v", requestID, ok, err)
	}
	head := GetFundsLotHead(sv, chainName, asset, vaultID)
	if head.Height != height || head.Seq != 1 {
		t.Fatalf("high first lot head=%d|%d, want %d|1", head.Height, head.Seq, height)
	}
}

func TestAdvanceFundsLotHeadCrossesArbitraryHeightGap(t *testing.T) {
	const (
		chainName = "eth"
		asset     = "USDT"
	)
	vaultID := uint32(11)
	base := map[string][]byte{
		keys.KeyFrostFundsLotHead(chainName, asset, vaultID):                []byte("10|1"),
		keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, 10):             []byte("1"),
		keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 1):        []byte("first"),
		keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, 1_000_000):      []byte("1"),
		keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 1_000_000, 1): []byte("after-large-gap"),
	}
	sv := newReadOnlyBaseStateView(base)

	head, err := advanceFundsLotHeadStrict(sv, chainName, asset, vaultID)
	if err != nil {
		t.Fatalf("advance across large gap: %v", err)
	}
	if head.Height != 1_000_000 || head.Seq != 1 {
		t.Fatalf("head after large gap=%d|%d, want 1000000|1", head.Height, head.Seq)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 1)); exists {
		t.Fatal("consumed lot index was not deleted")
	}
}

func TestEmptyFundsLotHeadDiscoversLaterDeposit(t *testing.T) {
	const (
		chainName = "eth"
		asset     = "USDT"
	)
	vaultID := uint32(12)
	base := map[string][]byte{
		keys.KeyFrostFundsLotHead(chainName, asset, vaultID):         []byte("10|1"),
		keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, 10):      []byte("1"),
		keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, 10, 1): []byte("only-lot"),
	}
	sv := newReadOnlyBaseStateView(base)

	head, err := advanceFundsLotHeadStrict(sv, chainName, asset, vaultID)
	if err != nil || head.Height != 0 || head.Seq != 0 {
		t.Fatalf("empty ledger head=%+v err=%v, want 0|0", head, err)
	}
	const laterHeight = uint64(2_000_000)
	sv.Set(keys.KeyFrostFundsLotSeq(chainName, asset, vaultID, laterHeight), []byte("1"))
	sv.Set(keys.KeyFrostFundsLotIndex(chainName, asset, vaultID, laterHeight, 1), []byte("later-deposit"))
	includeFundsLotInHead(sv, chainName, asset, vaultID, laterHeight, 1)

	requestID, ok, err := getFundsLotAtHeadStrict(sv, chainName, asset, vaultID)
	if err != nil || !ok || requestID != "later-deposit" {
		t.Fatalf("later deposit not found: request=%q ok=%v err=%v", requestID, ok, err)
	}
}

func TestFundsLotScanFailureDoesNotLookLikeAnEmptyLedger(t *testing.T) {
	wantErr := errors.New("ordered lot index unavailable")
	sv := NewStateView(
		func(string) ([]byte, error) { return nil, nil },
		func(string) (map[string][]byte, error) { return nil, wantErr },
	)
	if _, ok, err := getFundsLotAtHeadStrict(sv, "eth", "USDT", 13); ok || !errors.Is(err, wantErr) {
		t.Fatalf("scan failure = ok:%v err:%v, want fail-closed error", ok, err)
	}
}

func mustMarshalProto(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal proto failed: %v", err)
	}
	return data
}

func newReadOnlyBaseStateView(base map[string][]byte) StateView {
	readFn := func(key string) ([]byte, error) {
		v, ok := base[key]
		if !ok {
			return nil, nil
		}
		out := make([]byte, len(v))
		copy(out, v)
		return out, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for k, v := range base {
			if strings.HasPrefix(k, prefix) {
				c := make([]byte, len(v))
				copy(c, v)
				out[k] = c
			}
		}
		return out, nil
	}
	return NewStateView(readFn, scanFn)
}
