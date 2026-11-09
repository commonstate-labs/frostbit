package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

func seedAccountRechargeFinalizationTest(t *testing.T, sv StateView, requestID string, status pb.RechargeRequestStatus) *pb.RechargeRequest {
	t.Helper()
	asset := testBridgeAsset(t, "eth", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	putTestVaultConfig(t, sv, "eth", 8)
	req := &pb.RechargeRequest{
		RequestId:        requestID,
		NativeChain:      "eth",
		NativeTxHash:     accountRechargeTestHash(requestID),
		TokenAddress:     asset.ID,
		NativeContract:   asset.Contract,
		Amount:           "123",
		ReceiverAddress:  utils.GenerateTestIdentity(7103).Address,
		RequesterAddress: utils.GenerateTestIdentity(7104).Address,
		DepositProtocol:  protocol.DepositProtocolVault,
		Status:           status,
		FinalizeHeight:   50_000,
		VaultId:          7,
	}
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal recharge request: %v", err)
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), data)
	sv.Set(keys.KeyRechargeEventPending(req.NativeChain, req.NativeTxHash, req.DepositEventIndex), []byte(req.RequestId))
	return req
}

func TestApplyRechargeFinalizedAccountChainIncrementsVaultAvailableBalance(t *testing.T) {
	sv := NewMockStateView()
	asset := testBridgeAsset(t, "eth", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	req := seedAccountRechargeFinalizationTest(t, sv, "req_account_balance_increment", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	sv.Set(keys.KeyFrostVaultFundsLedger("eth", asset.ID, req.VaultId), []byte("77"))

	if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err != nil {
		t.Fatalf("applyRechargeFinalized: %v", err)
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("eth", asset.ID, req.VaultId)); string(available) != "123" {
		t.Fatalf("expected available=123, got %q", string(available))
	}
	if credited, _, _ := sv.Get(keys.KeyFrostVaultFundsLedger("eth", asset.ID, req.VaultId)); string(credited) != "200" {
		t.Fatalf("expected cumulative credited funds=200, got %q", string(credited))
	}
	if lot, ok, _ := sv.Get(keys.KeyFrostFundsLotIndex("eth", asset.ID, req.VaultId, req.FinalizeHeight, 1)); !ok || string(lot) != req.RequestId {
		t.Fatalf("expected funds lot index to point to request, ok=%v lot=%q", ok, string(lot))
	}
	if head := GetFundsLotHead(sv, "eth", asset.ID, req.VaultId); head.Height != req.FinalizeHeight || head.Seq != 1 {
		t.Fatalf("expected first high-height lot to initialize head, got %d|%d", head.Height, head.Seq)
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !credited {
		t.Fatalf("expected credited marker")
	}
	if err := applyRechargeFinalized(sv, req, req.FinalizeHeight+1, 2); err != nil {
		t.Fatalf("idempotent applyRechargeFinalized: %v", err)
	}
	if credited, _, _ := sv.Get(keys.KeyFrostVaultFundsLedger("eth", asset.ID, req.VaultId)); string(credited) != "200" {
		t.Fatalf("repeated finalization changed credited funds ledger: %q", string(credited))
	}
}

func TestApplyArbitrationOutcomeFinalizesAccountChainAtomically(t *testing.T) {
	sv := NewMockStateView()
	req := seedAccountRechargeFinalizationTest(t, sv, "req_account_arbitration_atomic", pb.RechargeRequestStatus_RECHARGE_CHALLENGED)
	finalized := proto.Clone(req).(*pb.RechargeRequest)
	finalized.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	finalized.FinalizeHeight = 50_010

	if err := applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{Request: finalized}, 1, 0, finalized.FinalizeHeight); err != nil {
		t.Fatalf("apply arbitration outcome: %v", err)
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("eth", req.TokenAddress, req.VaultId)); string(available) != req.Amount {
		t.Fatalf("account-chain arbitration available=%q want=%q", string(available), req.Amount)
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !credited {
		t.Fatal("account-chain arbitration must atomically credit recharge")
	}
	if lot, exists, _ := sv.Get(keys.KeyFrostFundsLotIndex("eth", req.TokenAddress, req.VaultId, finalized.FinalizeHeight, 1)); !exists || string(lot) != req.RequestId {
		t.Fatalf("account-chain arbitration lot missing: exists=%v lot=%q", exists, string(lot))
	}
}

func TestApplyWriteOpFinalizesAccountChainAtomically(t *testing.T) {
	sv := NewMockStateView()
	req := seedAccountRechargeFinalizationTest(t, sv, "req_account_writeop_atomic", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	finalized := proto.Clone(req).(*pb.RechargeRequest)
	finalized.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	finalized.FinalizeHeight = 50_020
	data, err := proto.Marshal(finalized)
	if err != nil {
		t.Fatal(err)
	}

	if err := applyWriteOpWithRechargeIndexes(sv, WriteOp{
		Key: keys.KeyRechargeRequest(req.RequestId), Value: data, Category: "witness_request",
	}, 1, 0, finalized.FinalizeHeight); err != nil {
		t.Fatalf("apply finalized write op: %v", err)
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("eth", req.TokenAddress, req.VaultId)); string(available) != req.Amount {
		t.Fatalf("account-chain write op available=%q want=%q", string(available), req.Amount)
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !credited {
		t.Fatal("account-chain finalized write op must atomically credit recharge")
	}
}

func TestSetRechargeRequestRejectsUncreditedFinalizedAccountChain(t *testing.T) {
	sv := NewMockStateView()
	req := seedAccountRechargeFinalizationTest(t, sv, "req_account_guard", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	finalized := proto.Clone(req).(*pb.RechargeRequest)
	finalized.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED

	if err := setRechargeRequestWithIndexes(sv, finalized, 1); err == nil {
		t.Fatal("expected direct uncredited account-chain FINALIZED write to be rejected")
	}
	storedData, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(storedData, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		t.Fatalf("guard changed canonical request status: %s", stored.Status)
	}
}

func TestUnregisteredETHUNIRechargeThenWithdraw(t *testing.T) {
	sv := NewMockStateView()
	seedChallengeSettlementFBToken(t, sv, "2000000000")
	SetBalance(sv, "requester", FeeToken, &pb.TokenBalance{Balance: "2000000000"})
	asset := testBridgeAsset(t, "eth", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	putTestVaultConfig(t, sv, "eth", 1)
	putTestVaultState(t, sv, "eth", 0, VaultStatusActive)

	handler := &WitnessRequestTxHandler{}
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:            &pb.BaseMessage{TxId: "req_eth_uni_first", FromAddress: "requester", ExecutedHeight: 10, ChainId: accountRechargeTestChainID},
		NativeChain:     "ETH",
		NativeTxHash:    accountRechargeTestHash("req_eth_uni_first"),
		NativeContract:  "0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984",
		Amount:          "1000000000000000000",
		ReceiverAddress: utils.GenerateTestIdentity(7105).Address,
		DepositProtocol: protocol.DepositProtocolVault,
		RechargeFee:     "1000000",
	}}}
	ops, receipt, err := handler.DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("first UNI witness request failed: receipt=%+v err=%v", receipt, err)
	}
	var request pb.RechargeRequest
	for _, op := range append(ops, sv.Diff()...) {
		sv.Set(op.Key, op.Value)
		if op.Key == keys.KeyRechargeRequest("req_eth_uni_first") {
			if err := proto.Unmarshal(op.Value, &request); err != nil {
				t.Fatal(err)
			}
		}
	}
	storedRaw, storedOK, storedErr := sv.Get(keys.KeyRechargeRequest(tx.GetWitnessRequestTx().Base.TxId))
	if storedErr != nil || !storedOK {
		t.Fatalf("request missing: %v", storedErr)
	}
	if err := proto.Unmarshal(storedRaw, &request); err != nil {
		t.Fatal(err)
	}
	if request.TokenAddress != asset.ID || request.NativeChain != asset.Chain || request.NativeContract != asset.Contract {
		t.Fatalf("request identity=%+v want=%+v", &request, asset)
	}
	if _, exists, _ := sv.Get(keys.KeyToken(asset.ID)); exists {
		t.Fatal("asset was registered before finalized credit")
	}
	if _, exists, _ := sv.Get(keys.KeyNativeTokenIndex(asset.Chain, asset.Contract)); exists {
		t.Fatal("native index was registered before finalized credit")
	}

	request.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	request.FinalizeHeight = 25
	rawRequest, _ := proto.Marshal(&request)
	sv.Set(keys.KeyRechargeRequest(request.RequestId), rawRequest)
	if err := applyRechargeFinalized(sv, &request, request.FinalizeHeight, 1); err != nil {
		t.Fatalf("finalize first UNI recharge: %v", err)
	}
	tokenData, exists, _ := sv.Get(keys.KeyToken(asset.ID))
	if !exists {
		t.Fatal("finalized recharge did not register UNI")
	}
	var token pb.Token
	if err := proto.Unmarshal(tokenData, &token); err != nil {
		t.Fatal(err)
	}
	if token.Address != asset.ID || token.NativeChain != asset.Chain || token.NativeContract != asset.Contract || token.TotalSupply != request.Amount {
		t.Fatalf("registered UNI token mismatch: %+v", &token)
	}
	if indexed, ok, _ := sv.Get(keys.KeyNativeTokenIndex(asset.Chain, asset.Contract)); !ok || string(indexed) != asset.ID {
		t.Fatalf("UNI native index=%q exists=%v", indexed, ok)
	}
	if got := GetBalance(sv, request.ReceiverAddress, asset.ID).Balance; got != request.Amount {
		t.Fatalf("UNI user balance=%s want=%s", got, request.Amount)
	}
	if got, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance(asset.Chain, asset.ID, request.VaultId)); string(got) != request.Amount {
		t.Fatalf("UNI vault available=%q want=%s", got, request.Amount)
	}

	vaultConfig := &pb.FrostVaultConfig{
		Chain: asset.Chain, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		VaultCount: 1, VaultRefs: []string{"0x00000000000000000000000000000000000000aa"}, EvmChainId: 1,
	}
	vaultConfigData, _ := proto.Marshal(vaultConfig)
	sv.Set(keys.KeyFrostVaultConfig(asset.Chain, 0), vaultConfigData)
	vaultStateData, _ := proto.Marshal(&pb.FrostVaultState{
		Chain: asset.Chain, VaultId: 0, KeyEpoch: 1,
		SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, Status: VaultStatusActive,
	})
	sv.Set(keys.KeyFrostVaultState(asset.Chain, 0), vaultStateData)
	withdrawTx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
		Base:  &pb.BaseMessage{TxId: "withdraw_eth_uni_first", FromAddress: request.ReceiverAddress, ExecutedHeight: 30},
		Asset: asset.ID, To: "0xde709f2102306220921060314715629080e2fb77", Amount: "1000",
	}}}
	withdrawOps, withdrawReceipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(withdrawTx, sv)
	if err != nil || withdrawReceipt == nil || withdrawReceipt.Status != "SUCCEED" {
		t.Fatalf("withdraw newly registered UNI failed: receipt=%+v err=%v", withdrawReceipt, err)
	}
	if len(withdrawOps) == 0 {
		t.Fatal("withdraw newly registered UNI produced no queue writes")
	}
	if seq, _, _ := sv.Get(keys.KeyFrostWithdrawLiveTail(asset.Chain, 0)); string(seq) != "1" {
		t.Fatalf("UNI withdraw live tail=%q want=1", seq)
	}
	if pending, _, _ := sv.Get(keys.KeyFrostWithdrawLivePending(asset.Chain, 0)); string(pending) != "1" {
		t.Fatalf("UNI withdraw pending=%q want=1", pending)
	}
}
