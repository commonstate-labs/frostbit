package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"dex/witness"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

const btcRechargeTestPubKeyHex = "03d194cd76d1ba9f139cca4af1277993c90e20ac5c817dcfe93a92810c5f9b03cb"

func seedBTCRechargeFinalizationTest(t *testing.T, sv StateView, requestID string, status pb.RechargeRequestStatus) *pb.RechargeRequest {
	t.Helper()
	receiver := "btc_recharge_receiver"
	vaultID := uint32(0)
	putTestVaultConfig(t, sv, "btc", 1)
	putTestVaultStateWithPubkeyHex(t, sv, "btc", vaultID, VaultStatusActive, btcRechargeTestPubKeyHex)
	scriptPubKey, err := utils.BuildTweakedDualCustodyTaprootScriptPubKey(mustDecodeHex(t, btcRechargeTestPubKeyHex), receiver, btcDualTestGuardianPubkey())
	if err != nil {
		t.Fatalf("build tweaked script pubkey failed: %v", err)
	}
	asset := testBridgeAsset(t, "btc", "")
	tokenData, err := proto.Marshal(&pb.Token{
		Address:     asset.ID,
		TotalSupply: "0",
		Owner:       "system",
		Type:        pb.TokenType_TOKEN_BRIDGED,
		NativeChain: asset.Chain,
	})
	if err != nil {
		t.Fatalf("marshal token failed: %v", err)
	}
	sv.Set(keys.KeyToken(asset.ID), tokenData)
	sv.Set(keys.KeyNativeTokenIndex(asset.Chain, asset.Contract), []byte(asset.ID))
	req := &pb.RechargeRequest{
		RequestId:       requestID,
		NativeChain:     "BTC",
		NativeTxHash:    strings.Repeat("a", 64),
		NativeVout:      2,
		NativeScript:    scriptPubKey,
		TokenAddress:    asset.ID,
		Amount:          "7000",
		ReceiverAddress: receiver,
		Status:          status,
		FinalizeHeight:  67,
		VaultId:         vaultID,
	}
	reqData, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal recharge request failed: %v", err)
	}
	sv.Set(keys.KeyRechargeRequest(requestID), reqData)
	_, nativeLockKey, err := rechargeNativeLockIdentity("btc", req.NativeTxHash, req.NativeVout)
	if err != nil {
		t.Fatalf("build BTC recharge native lock failed: %v", err)
	}
	sv.Set(nativeLockKey, []byte(requestID))
	return req
}

func TestRechargeNativeLockIdentityBTCIsCanonicalAndIncludesVout(t *testing.T) {
	txid := strings.Repeat("a", 64)
	canonical, key0, err := rechargeNativeLockIdentity("btc", txid, 0)
	if err != nil || canonical != txid {
		t.Fatalf("canonical BTC identity failed: canonical=%q err=%v", canonical, err)
	}
	_, key1, err := rechargeNativeLockIdentity("btc", txid, 1)
	if err != nil {
		t.Fatal(err)
	}
	if key0 == key1 {
		t.Fatal("different BTC vouts must have different recharge locks")
	}
	for _, invalid := range []string{strings.ToUpper(txid), txid + ":0", " " + txid, txid[:63], strings.Repeat("g", 64)} {
		if _, _, err := rechargeNativeLockIdentity("btc", invalid, 0); err == nil {
			t.Fatalf("invalid BTC txid accepted: %q", invalid)
		}
	}
	accountHash := "0xAbC"
	if _, _, err := rechargeNativeLockIdentity("eth", accountHash, 7); err == nil {
		t.Fatal("noncanonical account-chain transaction hash must be rejected")
	}
}

func TestWitnessRequestRejectsInvalidRechargeAmount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount string
	}{
		{name: "empty", amount: ""},
		{name: "zero", amount: "0"},
		{name: "negative", amount: "-1"},
		{name: "malformed", amount: "not-a-number"},
		{name: "fractional", amount: "1.5"},
		{name: "uint64_overflow", amount: "18446744073709551616"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &WitnessRequestTxHandler{}
			tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
				Base:            &pb.BaseMessage{TxId: "req_invalid_amount_" + tc.name},
				NativeChain:     "BTC",
				Amount:          tc.amount,
				ReceiverAddress: "receiver",
			}}}
			_, receipt, err := h.DryRun(tx, NewMockStateView())
			if err == nil || receipt == nil || receipt.Status != "FAILED" {
				t.Fatalf("invalid amount accepted: amount=%q receipt=%+v err=%v", tc.amount, receipt, err)
			}
		})
	}
}

func TestWitnessRequestRejectsBlankReceiver(t *testing.T) {
	for _, receiver := range []string{"", " \t\r\n"} {
		h := &WitnessRequestTxHandler{}
		tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
			Base:            &pb.BaseMessage{TxId: "req_blank_receiver"},
			NativeChain:     "BTC",
			Amount:          "1",
			ReceiverAddress: receiver,
		}}}
		_, receipt, err := h.DryRun(tx, NewMockStateView())
		if err == nil || receipt == nil || receipt.Status != "FAILED" || receipt.Error != "receiver address is required" {
			t.Fatalf("blank receiver %q accepted: receipt=%+v err=%v", receiver, receipt, err)
		}
	}
}

func TestApplyRechargeFinalizedBTCWritesUTXOAndCredited(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_finalized_writes_utxo", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	req.SelectedWitnesses = []string{"witness_1", "witness_2"}
	req.Votes = []*pb.WitnessVote{{
		RequestId:      req.RequestId,
		WitnessAddress: "witness_1",
		VoteType:       pb.WitnessVoteType_VOTE_PASS,
		Reason:         "full vote reason",
		TxId:           "vote_tx_1",
	}}
	reqData, _ := proto.Marshal(req)
	sv.Set(keys.KeyRechargeRequest(req.RequestId), reqData)
	voteData, _ := proto.Marshal(req.Votes[0])
	sv.Set(keys.KeyWitnessVote(req.RequestId, "witness_1"), voteData)

	if err := applyRechargeFinalized(sv, req, 70, 1); err != nil {
		t.Fatalf("applyRechargeFinalized failed: %v", err)
	}

	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !credited {
		t.Fatalf("expected credited marker")
	}
	utxoData, exists, _ := sv.Get(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout))
	if !exists {
		t.Fatalf("expected BTC UTXO")
	}
	var utxo pb.FrostUtxo
	if err := proto.Unmarshal(utxoData, &utxo); err != nil {
		t.Fatalf("unmarshal utxo failed: %v", err)
	}
	if utxo.Amount != "7000" || utxo.RequestId != req.RequestId {
		t.Fatalf("unexpected utxo: %+v", &utxo)
	}
	if utxo.FinalizeHeight != 70 {
		t.Fatalf("UTXO finalize height must be the executing block height: got=%d want=70", utxo.FinalizeHeight)
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("btc", req.TokenAddress, req.VaultId)); string(available) != "7000" {
		t.Fatalf("expected available=7000, got %q", string(available))
	}
	if credited, _, _ := sv.Get(keys.KeyFrostVaultFundsLedger("btc", req.TokenAddress, req.VaultId)); string(credited) != "7000" {
		t.Fatalf("expected credited funds ledger=7000, got %q", string(credited))
	}
	storedData, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(storedData, &stored); err != nil {
		t.Fatalf("unmarshal stored request failed: %v", err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED || stored.NativeChain != "btc" {
		t.Fatalf("unexpected stored request status/chain: status=%s chain=%s", stored.Status, stored.NativeChain)
	}
	if len(stored.NativeScript) != 0 || len(stored.SelectedWitnesses) != 0 || len(stored.Votes) != 0 {
		t.Fatalf("hot recharge request should be compacted, got script=%d selected=%d votes=%d", len(stored.NativeScript), len(stored.SelectedWitnesses), len(stored.Votes))
	}
	archivedData, archived, _ := sv.Get(keys.KeyArchiveRechargeRequest(req.RequestId))
	if !archived {
		t.Fatalf("expected archived recharge request")
	}
	var archivedReq pb.RechargeRequest
	if err := proto.Unmarshal(archivedData, &archivedReq); err != nil {
		t.Fatalf("unmarshal archived request failed: %v", err)
	}
	if len(archivedReq.NativeScript) == 0 || len(archivedReq.SelectedWitnesses) != 2 || len(archivedReq.Votes) != 1 {
		t.Fatalf("archived recharge request lost full details: script=%d selected=%d votes=%d", len(archivedReq.NativeScript), len(archivedReq.SelectedWitnesses), len(archivedReq.Votes))
	}
	if _, hotVote, _ := sv.Get(keys.KeyWitnessVote(req.RequestId, "witness_1")); hotVote {
		t.Fatalf("terminal witness vote should be removed from StateDB")
	}
	archivedVoteData, archivedVote, _ := sv.Get(keys.KeyArchiveWitnessVote(req.RequestId, "witness_1"))
	if !archivedVote {
		t.Fatalf("expected archived witness vote")
	}
	var archivedVoteMsg pb.WitnessVote
	if err := proto.Unmarshal(archivedVoteData, &archivedVoteMsg); err != nil {
		t.Fatalf("unmarshal archived vote failed: %v", err)
	}
	if archivedVoteMsg.Reason != "full vote reason" {
		t.Fatalf("archived vote lost reason: %q", archivedVoteMsg.Reason)
	}
	if err := applyRechargeFinalized(sv, req, 71, 1); err != nil {
		t.Fatalf("idempotent finalization failed: %v", err)
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != "7000" {
		t.Fatalf("repeated finalization changed user balance: %s", got)
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("btc", req.TokenAddress, req.VaultId)); string(available) != "7000" {
		t.Fatalf("repeated finalization changed vault available balance: %q", string(available))
	}
	if credited, _, _ := sv.Get(keys.KeyFrostVaultFundsLedger("btc", req.TokenAddress, req.VaultId)); string(credited) != "7000" {
		t.Fatalf("repeated finalization changed credited funds ledger: %q", string(credited))
	}
}

func TestApplyRechargeFinalizedBTCAllowsAssignedVaultDraining(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_draining_vault", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	putTestVaultStateWithPubkeyHex(t, sv, "btc", req.VaultId, VaultLifecycleDraining, btcRechargeTestPubKeyHex)

	if err := applyRechargeFinalized(sv, req, 70, 1); err != nil {
		t.Fatalf("finalize recharge assigned before vault started draining: %v", err)
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !credited {
		t.Fatal("existing BTC recharge was not credited after assigned vault entered DRAINING")
	}
}

func TestApplyRechargeFinalizedBTCRejectsInvalidScriptWithoutFinalizing(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_invalid_script", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	req.NativeScript = []byte{0x51}
	reqData, _ := proto.Marshal(req)
	sv.Set(keys.KeyRechargeRequest(req.RequestId), reqData)

	if err := applyRechargeFinalized(sv, req, 70, 1); err == nil {
		t.Fatal("expected invalid BTC script to fail finalization")
	}
	storedData, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(storedData, &stored); err != nil {
		t.Fatalf("unmarshal stored request failed: %v", err)
	}
	if stored.Status == pb.RechargeRequestStatus_RECHARGE_FINALIZED {
		t.Fatalf("invalid BTC recharge must not be finalized")
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		t.Fatalf("invalid BTC recharge must not be credited")
	}
}

func TestApplyRechargeFinalizedBTCRejectsEmptyReceiver(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_empty_receiver", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	req.ReceiverAddress = ""
	reqData, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), reqData)

	if err := applyRechargeFinalized(sv, req, 70, 1); err == nil {
		t.Fatal("expected empty receiver to fail finalization")
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		t.Fatal("empty receiver recharge must not be credited")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout)); exists {
		t.Fatal("empty receiver recharge must not create a BTC UTXO")
	}
}

func TestApplyRechargeFinalizedBTCRollsBackPartialEconomicWrites(t *testing.T) {
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_atomic_rollback", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	invalidBalance, err := proto.Marshal(&pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyBalance(req.ReceiverAddress, req.TokenAddress), invalidBalance)

	if err := applyRechargeFinalized(sv, req, 70, 1); err == nil {
		t.Fatal("expected invalid balance state to fail BTC finalization")
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		t.Fatal("failed finalization left credited marker")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout)); exists {
		t.Fatal("failed finalization left BTC UTXO")
	}
	if available, exists, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("btc", req.TokenAddress, req.VaultId)); exists || len(available) != 0 {
		t.Fatalf("failed finalization changed vault available balance: exists=%v value=%q", exists, string(available))
	}
	if credited, exists, _ := sv.Get(keys.KeyFrostVaultFundsLedger("btc", req.TokenAddress, req.VaultId)); exists || len(credited) != 0 {
		t.Fatalf("failed finalization changed credited funds ledger: exists=%v value=%q", exists, string(credited))
	}
	storedData, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(storedData, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD || len(stored.NativeScript) == 0 {
		t.Fatalf("failed finalization compacted request: status=%s script=%d", stored.Status, len(stored.NativeScript))
	}
}

func TestApplyRechargeFinalizedBTCRejectsOutpointOwnedByAnotherRequest(t *testing.T) {
	sv := NewMockStateView()
	first := seedBTCRechargeFinalizationTest(t, sv, "req_btc_outpoint_owner", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	if err := applyRechargeFinalized(sv, first, 70, 1); err != nil {
		t.Fatal(err)
	}

	second := proto.Clone(first).(*pb.RechargeRequest)
	second.RequestId = "req_btc_outpoint_duplicate"
	second.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	secondData, err := proto.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyRechargeRequest(second.RequestId), secondData)
	_, nativeLockKey, err := rechargeNativeLockIdentity("btc", second.NativeTxHash, second.NativeVout)
	if err != nil {
		t.Fatal(err)
	}
	                                                                            
	                                                                        
	sv.Set(nativeLockKey, []byte(second.RequestId))
	if err := applyRechargeFinalized(sv, second, 71, 1); err == nil {
		t.Fatal("expected duplicate BTC outpoint finalization to fail")
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(second.RequestId)); credited {
		t.Fatal("duplicate BTC outpoint must not be credited")
	}
	if got := GetBalance(sv, first.ReceiverAddress, first.TokenAddress).Balance; got != "7000" {
		t.Fatalf("duplicate outpoint changed user balance: %s", got)
	}
	tokenData, exists, _ := sv.Get(keys.KeyToken(first.TokenAddress))
	if !exists {
		t.Fatal("BTC token missing")
	}
	var token pb.Token
	if err := proto.Unmarshal(tokenData, &token); err != nil {
		t.Fatal(err)
	}
	if token.TotalSupply != "7000" {
		t.Fatalf("duplicate outpoint changed total supply: %s", token.TotalSupply)
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("btc", first.TokenAddress, first.VaultId)); string(available) != "7000" {
		t.Fatalf("duplicate outpoint changed vault available balance: %q", string(available))
	}
}

func TestApplyRechargeFinalizedBTCRejectsPartialSameRequestUTXO(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_partial_same_request_utxo", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	partial := &pb.FrostUtxo{
		Txid:      req.NativeTxHash,
		Vout:      req.NativeVout,
		Amount:    req.Amount,
		VaultId:   req.VaultId,
		RequestId: req.RequestId,
	}
	partialData, err := proto.Marshal(partial)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout), partialData)

	if err := applyRechargeFinalized(sv, req, 70, 1); err == nil {
		t.Fatal("expected partial same-request UTXO state to fail closed")
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		t.Fatal("partial UTXO state must not create credited marker")
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != "0" {
		t.Fatalf("partial UTXO state changed user balance: %s", got)
	}
}

func TestRechargeDueRejectsCreditedMarkerOnNonFinalizedRequest(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_marker_nonterminal", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	req.DeadlineHeight = 69
	reqData, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), reqData)
	dueKey := keys.KeyRechargeChallengeDue(70, req.RequestId)
	sv.Set(dueKey, []byte(req.RequestId))
	sv.Set(keys.KeyRechargeCredited(req.RequestId), []byte("1"))

	x := &Executor{}
	if err := x.applyRechargeChallengeFinalizations(sv, 70, 1); err == nil {
		t.Fatal("expected marker/nonterminal invariant violation")
	}
	if _, exists, _ := sv.Get(dueKey); !exists {
		t.Fatal("invariant failure must not silently delete the due index")
	}
	storedData, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(storedData, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		t.Fatalf("marker invariant failure changed request status: %s", stored.Status)
	}
}

func TestApplyWriteOpFinalizedBTCUsesAtomicFinalizer(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_writeop_atomic", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	finalized := proto.Clone(req).(*pb.RechargeRequest)
	finalized.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	finalized.FinalizeHeight = 80
	data, err := proto.Marshal(finalized)
	if err != nil {
		t.Fatal(err)
	}

	if err := applyWriteOpWithRechargeIndexes(sv, WriteOp{
		Key:      keys.KeyRechargeRequest(req.RequestId),
		Value:    data,
		Category: "witness_request",
	}, 1, 0, 80); err != nil {
		t.Fatalf("apply finalized write op: %v", err)
	}

	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !credited {
		t.Fatal("finalized write op must atomically credit the recharge")
	}
	if available, _, _ := sv.Get(keys.KeyFrostVaultAvailableBalance("btc", req.TokenAddress, req.VaultId)); string(available) != "7000" {
		t.Fatalf("expected available=7000 after finalization, got %q", string(available))
	}
	storedData, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(storedData, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED || len(stored.NativeScript) != 0 {
		t.Fatalf("expected credited compact terminal request, got status=%s script=%d", stored.Status, len(stored.NativeScript))
	}
}

func TestSetRechargeRequestRejectsUncreditedFinalizedBTC(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_guard", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	finalized := proto.Clone(req).(*pb.RechargeRequest)
	finalized.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED

	if err := setRechargeRequestWithIndexes(sv, finalized, 1); err == nil {
		t.Fatal("expected direct uncredited FINALIZED write to be rejected")
	}
	storedData, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(storedData, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD || len(stored.NativeScript) == 0 {
		t.Fatalf("guard changed the canonical request: status=%s script=%d", stored.Status, len(stored.NativeScript))
	}
}

func TestApplyArbitrationOutcomeFinalizesBTCAtomically(t *testing.T) {
	sv := NewMockStateView()
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_arbitration_atomic", pb.RechargeRequestStatus_RECHARGE_CHALLENGED)
	finalized := proto.Clone(req).(*pb.RechargeRequest)
	finalized.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	finalized.FinalizeHeight = 90

	if err := applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{
		Request: finalized,
	}, 1, 0, 90); err != nil {
		t.Fatalf("apply arbitration outcome: %v", err)
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !credited {
		t.Fatal("arbitration outcome compacted request before BTC credit")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout)); !exists {
		t.Fatal("arbitration outcome did not register BTC UTXO")
	}
}

func TestApplyArbitrationOutcomeRollsBackBTCWhenSettlementFails(t *testing.T) {
	sv := newStateDBNotFoundStateView(map[string][]byte{})
	req := seedBTCRechargeFinalizationTest(t, sv, "req_btc_arbitration_rollback", pb.RechargeRequestStatus_RECHARGE_CHALLENGED)
	finalized := proto.Clone(req).(*pb.RechargeRequest)
	finalized.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	finalized.FinalizeHeight = 91
	challenge := &pb.ChallengeRecord{
		ChallengeId: "challenge_btc_arbitration_rollback",
		RequestId:   req.RequestId,
		StakeAmount: "100",
		Status:      pb.ChallengeStatus_CHALLENGE_FINALIZED,
		Finalized:   true,
	}

	err := applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{
		Request:   finalized,
		Challenge: challenge,
	}, 1, 0, 91)
	if err == nil {
		t.Fatal("expected challenge settlement without FB token to fail")
	}
	if _, credited, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); credited {
		t.Fatal("failed arbitration settlement left BTC credited")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcUtxo(req.VaultId, req.NativeTxHash, req.NativeVout)); exists {
		t.Fatal("failed arbitration settlement left BTC UTXO")
	}
	storedData, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var stored pb.RechargeRequest
	if err := proto.Unmarshal(storedData, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED || len(stored.NativeScript) == 0 {
		t.Fatalf("failed arbitration settlement changed request: status=%s script=%d", stored.Status, len(stored.NativeScript))
	}
}

func TestSyncWitnessServiceFromCommittedDiffUsesArchivedParticipantsAfterWitnessWrites(t *testing.T) {
	svc := witness.NewService(witness.DefaultConfig())
	defer svc.Stop()

	const (
		address     = "witness_postcommit_order"
		requestID   = "request_postcommit_order"
		challengeID = "challenge_postcommit_order"
	)
	staleInfo := &pb.WitnessInfo{
		Address:      address,
		StakeAmount:  "1000",
		PendingTasks: []string{requestID, challengeID},
	}
	fullRequest := &pb.RechargeRequest{
		RequestId:         requestID,
		Status:            pb.RechargeRequestStatus_RECHARGE_FINALIZED,
		SelectedWitnesses: []string{address},
	}
	fullChallenge := &pb.ChallengeRecord{
		ChallengeId: challengeID,
		Status:      pb.ChallengeStatus_CHALLENGE_FINALIZED,
		Finalized:   true,
		Arbitrators: []string{address},
	}

	mustMarshal := func(message proto.Message) []byte {
		t.Helper()
		data, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	x := &Executor{WitnessService: svc}
	x.syncWitnessServiceFromCommittedDiff([]WriteOp{
		                                                                         
		                                                                    
		{Key: keys.KeyArchiveRechargeRequest(requestID), Value: mustMarshal(fullRequest)},
		{Key: keys.KeyArchiveChallengeRecord(challengeID), Value: mustMarshal(fullChallenge)},
		{Key: keys.KeyRechargeRequest(requestID), Value: mustMarshal(compactRechargeRequestForHotState(fullRequest))},
		{Key: keys.KeyChallengeRecord(challengeID), Value: mustMarshal(compactChallengeRecordForHotState(fullChallenge))},
		{Key: keys.KeyWitnessInfo(address), Value: mustMarshal(staleInfo)},
	})

	updated, err := svc.GetWitnessInfo(address)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.PendingTasks) != 0 {
		t.Fatalf("post-commit sync restored stale pending tasks: %v", updated.PendingTasks)
	}
	if _, err := svc.GetRequest(requestID); err == nil {
		t.Fatal("terminal request remained in the active service cache")
	}
}
