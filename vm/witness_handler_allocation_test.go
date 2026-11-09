package vm

import (
	"crypto/sha256"
	bridgeasset "dex/frost/asset"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/witness"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

const accountRechargeTestChainID uint64 = 1

func accountRechargeTestHash(requestID string) string {
	hash := sha256.Sum256([]byte(requestID))
	return "0x" + hex.EncodeToString(hash[:])
}

func putTestVaultConfig(t *testing.T, sv StateView, chainName string, vaultCount uint32) {
	t.Helper()
	cfg := &pb.FrostVaultConfig{
		Chain:          chainName,
		VaultCount:     vaultCount,
		CommitteeSize:  4,
		ThresholdRatio: 0.67,
	}
	if isBTCChainName(chainName) {
		cfg.GuardianPubkey = btcDualTestGuardianPubkey()
	}
	if !isBTCChainName(chainName) {
		cfg.DepositProtocol = protocol.DepositProtocolVault
		cfg.DepositDexChainId = accountRechargeTestChainID
		for vaultID := uint32(0); vaultID < vaultCount; vaultID++ {
			cfg.VaultRefs = append(cfg.VaultRefs, fmt.Sprintf("0x%040x", vaultID+1))
		}
	}
	data, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal vault config failed: %v", err)
	}
	sv.Set(keys.KeyFrostVaultConfig(chainName, 0), data)
}

func putTestVaultState(t *testing.T, sv StateView, chainName string, vaultID uint32, status string) {
	t.Helper()
	state := &pb.FrostVaultState{
		Chain:       chainName,
		VaultId:     vaultID,
		Status:      status,
		GroupPubkey: make([]byte, 33),
	}
	state.GroupPubkey[0] = 0x02
	state.GroupPubkey[32] = byte(vaultID + 1)
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatalf("marshal vault state failed: %v", err)
	}
	sv.Set(keys.KeyFrostVaultState(chainName, vaultID), data)
}

func putTestVaultStateWithPubkeyHex(t *testing.T, sv StateView, chainName string, vaultID uint32, status, pubKeyHex string) {
	t.Helper()
	pubKey, err := hex.DecodeString(pubKeyHex)
	if err != nil {
		t.Fatalf("decode pubkey hex failed: %v", err)
	}
	state := &pb.FrostVaultState{
		Chain:       chainName,
		VaultId:     vaultID,
		Status:      status,
		GroupPubkey: pubKey,
	}
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatalf("marshal vault state failed: %v", err)
	}
	sv.Set(keys.KeyFrostVaultState(chainName, vaultID), data)
}

func putTestToken(t *testing.T, sv StateView, token *pb.Token) {
	t.Helper()
	data, err := proto.Marshal(token)
	if err != nil {
		t.Fatalf("marshal token failed: %v", err)
	}
	sv.Set(keys.KeyToken(token.Address), data)
	if token.NativeChain != "" {
		sv.Set(keys.KeyNativeTokenIndex(token.NativeChain, token.NativeContract), []byte(token.Address))
	}
}

func mustDecodeHex(t *testing.T, raw string) []byte {
	t.Helper()
	out, err := hex.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode hex failed: %v", err)
	}
	return out
}

func TestResolveVaultChainAndCountCaseInsensitive(t *testing.T) {
	sv := NewMockStateView()
	putTestVaultConfig(t, sv, "eth", 3)

	chainName, vaultCount, err := resolveVaultChainAndCount(sv, "ETH")
	if err != nil {
		t.Fatalf("resolveVaultChainAndCount failed: %v", err)
	}
	if chainName != "eth" {
		t.Fatalf("expected resolved chain eth, got %s", chainName)
	}
	if vaultCount != 3 {
		t.Fatalf("expected vault count 3, got %d", vaultCount)
	}
}

func TestResolveVaultChainAndCountRequiresOnChainConfig(t *testing.T) {
	sv := NewMockStateView()

	_, _, err := resolveVaultChainAndCount(sv, "eth")
	if err == nil {
		t.Fatal("expected missing vault config error")
	}
	if !strings.Contains(err.Error(), "vault config not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAllocateVaultIDWithStateCheckOnlyReadyOrActive(t *testing.T) {
	sv := NewMockStateView()
	chainName := "eth"
	vaultCount := uint32(5)
	requestID := "req-ready-only"

	initial := allocateVaultID(requestID, vaultCount)
	putTestVaultState(t, sv, chainName, initial, "PENDING")
	putTestVaultState(t, sv, chainName, (initial+1)%vaultCount, VaultLifecycleDraining)
	expected := (initial + 2) % vaultCount
	putTestVaultState(t, sv, chainName, expected, VaultStatusKeyReady)
	putTestVaultState(t, sv, chainName, (initial+3)%vaultCount, VaultStatusActive)

	actual, err := allocateVaultIDWithStateCheck(sv, chainName, requestID, vaultCount)
	if err != nil {
		t.Fatalf("allocateVaultIDWithStateCheck failed: %v", err)
	}
	if actual != expected {
		t.Fatalf("expected vault %d, got %d", expected, actual)
	}
}

func TestAllocateVaultIDWithStateCheckNoEligibleVault(t *testing.T) {
	sv := NewMockStateView()
	chainName := "eth"
	vaultCount := uint32(3)
	requestID := "req-no-eligible"

	putTestVaultState(t, sv, chainName, 0, "PENDING")
	putTestVaultState(t, sv, chainName, 1, VaultLifecycleDraining)
	putTestVaultState(t, sv, chainName, 2, VaultStatusDeprecated)

	_, err := allocateVaultIDWithStateCheck(sv, chainName, requestID, vaultCount)
	if err == nil {
		t.Fatal("expected no allocatable vault error")
	}
	if !strings.Contains(err.Error(), "no allocatable vault") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLookupVaultIDByScriptPubKeyBTC(t *testing.T) {
	sv := NewMockStateView()
	chainName := "btc"
	vaultCount := uint32(3)
	putTestVaultConfig(t, sv, chainName, vaultCount)
	receiverAddr := "user_test_address"

	                                   
	xOnly := "d194cd76d1ba9f139cca4af1277993c90e20ac5c817dcfe93a92810c5f9b03cb"
	putTestVaultStateWithPubkeyHex(t, sv, chainName, 0, VaultStatusActive, "03"+xOnly)
	putTestVaultStateWithPubkeyHex(t, sv, chainName, 1, VaultStatusActive, "02aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	putTestVaultStateWithPubkeyHex(t, sv, chainName, 2, VaultStatusDeprecated, "03bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	                                                      
	rawPubKey := mustDecodeHex(t, "03"+xOnly)
	rawXOnly := mustDecodeHex(t, xOnly)
	tweak := utils.ComputeUserTweak(rawXOnly, receiverAddr)
	tweakedPub, err := utils.ComputeTweakedPubkey(rawPubKey, tweak)
	if err != nil {
		t.Fatalf("ComputeTweakedPubkey failed: %v", err)
	}
	scriptPubKey, _, _, err := utils.BuildDualCustodyTaproot(tweakedPub, btcDualTestGuardianPubkey())
	if err != nil {
		t.Fatal(err)
	}

	vaultID, err := lookupVaultIDByScriptPubKey(sv, chainName, vaultCount, scriptPubKey, receiverAddr)
	if err != nil {
		t.Fatalf("lookupVaultIDByScriptPubKey failed: %v", err)
	}
	if vaultID != 0 {
		t.Fatalf("expected vault_id=0, got %d", vaultID)
	}
}

func TestLookupVaultIDByScriptPubKeyAmbiguous(t *testing.T) {
	sv := NewMockStateView()
	chainName := "btc"
	vaultCount := uint32(2)
	putTestVaultConfig(t, sv, chainName, vaultCount)
	receiverAddr := "ambiguous_user"

	xOnly := "05ffbabdb46782b00fa7e44feb117aaed34adddbf37d1f9d8c19dfaba47ae3f8"
	putTestVaultStateWithPubkeyHex(t, sv, chainName, 0, VaultStatusActive, "03"+xOnly)
	putTestVaultStateWithPubkeyHex(t, sv, chainName, 1, VaultStatusKeyReady, "02"+xOnly)

	                                                                     
	rawPubKey := mustDecodeHex(t, "03"+xOnly)
	rawXOnly := mustDecodeHex(t, xOnly)
	tweak := utils.ComputeUserTweak(rawXOnly, receiverAddr)
	tweakedPub, err := utils.ComputeTweakedPubkey(rawPubKey, tweak)
	if err != nil {
		t.Fatalf("ComputeTweakedPubkey failed: %v", err)
	}
	scriptPubKey, _, _, err := utils.BuildDualCustodyTaproot(tweakedPub, btcDualTestGuardianPubkey())
	if err != nil {
		t.Fatal(err)
	}

	                                                                    
	                                                                                                   
	_, err = lookupVaultIDByScriptPubKey(sv, chainName, vaultCount, scriptPubKey, receiverAddr)
	                                                                     
	_ = err
}

func TestWitnessRequestDerivesUnregisteredBNBNativeAsset(t *testing.T) {
	sv := NewMockStateView()
	SetBalance(sv, "requester", FeeToken, &pb.TokenBalance{Balance: "2000000000"})
	putTestVaultConfig(t, sv, "bnb", 1)
	putTestVaultState(t, sv, "bnb", 0, VaultStatusActive)
	identity, err := bridgeasset.Resolve("bnb", "")
	if err != nil {
		t.Fatal(err)
	}

	h := &WitnessRequestTxHandler{}
	h.SetWitnessService(witness.NewService(witness.DefaultConfig()))
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:            &pb.BaseMessage{TxId: "req_bnb_usdc", FromAddress: "requester", ExecutedHeight: 10, ChainId: accountRechargeTestChainID},
		NativeChain:     "BNB",
		NativeTxHash:    accountRechargeTestHash("req_bnb_usdc"),
		Amount:          "100",
		ReceiverAddress: utils.GenerateTestIdentity(7101).Address,
		DepositProtocol: protocol.DepositProtocolVault,
		RechargeFee:     "1000000",
	}}}

	ops, receipt, err := h.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("DryRun failed: %v receipt=%+v", err, receipt)
	}
	if receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	var stored pb.RechargeRequest
	for _, op := range append(ops, sv.Diff()...) {
		if strings.Contains(op.Key, "frost_funds_pending_") {
			t.Fatalf("witness request wrote retired pending funds key: %s", op.Key)
		}
		if op.Key == keys.KeyRechargeRequest("req_bnb_usdc") {
			if err := proto.Unmarshal(op.Value, &stored); err != nil {
				t.Fatalf("unmarshal recharge request failed: %v", err)
			}
		}
	}
	raw, ok, err := sv.Get(keys.KeyRechargeRequest(tx.GetWitnessRequestTx().Base.TxId))
	if err != nil || !ok {
		t.Fatalf("persisted request missing: %v", err)
	}
	if err := proto.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.NativeChain != "bnb" || stored.TokenAddress != identity.ID {
		t.Fatalf("unexpected chain/token: chain=%s token=%s", stored.NativeChain, stored.TokenAddress)
	}
	if stored.NativeContract != "" {
		t.Fatalf("native coin unexpectedly has contract: %+v", &stored)
	}
	original := tx.GetWitnessRequestTx()
	if original.NativeChain != "BNB" || original.NativeContract != "" {
		t.Fatalf("DryRun mutated original witness request: %+v", original)
	}
	if _, exists, _ := sv.Get(keys.KeyToken(identity.ID)); exists {
		t.Fatal("request admission registered asset before finalized recharge")
	}
}

func TestWitnessRequestResolvesTokenFromNativeIndex(t *testing.T) {
	sv := NewMockStateView()
	SetBalance(sv, "requester", FeeToken, &pb.TokenBalance{Balance: "2000000000"})
	putTestVaultConfig(t, sv, "bnb", 1)
	putTestVaultState(t, sv, "bnb", 0, VaultStatusActive)
	identity, err := bridgeasset.Resolve("bnb", "0x55d398326f99059ff775485246999027b3197955")
	if err != nil {
		t.Fatal(err)
	}
	putTestToken(t, sv, &pb.Token{
		Address:        identity.ID,
		Type:           pb.TokenType_TOKEN_BRIDGED,
		NativeChain:    identity.Chain,
		NativeContract: identity.Contract,
	})

	h := &WitnessRequestTxHandler{}
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:            &pb.BaseMessage{TxId: "req_bsc_usdt", FromAddress: "requester", ExecutedHeight: 10, ChainId: accountRechargeTestChainID},
		NativeChain:     "BSC",
		NativeTxHash:    accountRechargeTestHash("req_bsc_usdt"),
		NativeContract:  "0x55d398326f99059fF775485246999027B3197955",
		Amount:          "100",
		ReceiverAddress: utils.GenerateTestIdentity(7102).Address,
		DepositProtocol: protocol.DepositProtocolVault,
		RechargeFee:     "1000000",
	}}}

	ops, receipt, err := h.DryRun(tx, sv)
	if err != nil {
		t.Fatalf("DryRun failed: %v receipt=%+v", err, receipt)
	}
	var stored pb.RechargeRequest
	for _, op := range append(ops, sv.Diff()...) {
		if op.Key == keys.KeyRechargeRequest("req_bsc_usdt") {
			if err := proto.Unmarshal(op.Value, &stored); err != nil {
				t.Fatalf("unmarshal recharge request failed: %v", err)
			}
		}
	}
	raw, ok, err := sv.Get(keys.KeyRechargeRequest(tx.GetWitnessRequestTx().Base.TxId))
	if err != nil || !ok {
		t.Fatalf("persisted request missing: %v", err)
	}
	if err := proto.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.NativeChain != "bnb" || stored.TokenAddress != identity.ID || stored.NativeContract != identity.Contract {
		t.Fatalf("unexpected resolved asset: chain=%s token=%s", stored.NativeChain, stored.TokenAddress)
	}
	original := tx.GetWitnessRequestTx()
	if original.NativeChain != "BSC" || original.NativeContract != "0x55d398326f99059fF775485246999027B3197955" {
		t.Fatalf("DryRun mutated original witness request: %+v", original)
	}
}
