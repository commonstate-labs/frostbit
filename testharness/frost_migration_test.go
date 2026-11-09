package testharness

import (
	"bytes"
	"context"
	"dex/config"
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/frost/runtime/workers"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/chaincfg"
	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

                                                          

                                                                                                                
                                                                                          
type fakeChainAdapterFactory struct{}

func (f *fakeChainAdapterFactory) Adapter(chainName string) (workers.ChainAdapter, error) {
	switch chainName {
	case "btc":
		return btc.NewBTCAdapter("regtest"), nil
	case "eth":
		return evm.NewETHAdapter(), nil
	case "bnb":
		return evm.NewBNBAdapter(), nil
	default:
		return evm.NewETHAdapter(), nil
	}
}

func (f *fakeChainAdapterFactory) RegisterAdapter(workers.ChainAdapter) {}

func newTestTransitionWorker(h *Harness) *workers.TransitionWorker {
	return workers.NewTransitionWorker(
		h.StateReader(),
		h.TxSubmitter(),
		nil,                  
		nil,                 
		nil,                 
		nil,                  
		&fakeChainAdapterFactory{},
		nil,                  
		nil,                   
		nil,                                              
		h.Committee[0],
		logs.NewNodeLogger("test", 0),
	)
}

                                                              

var testGroupPubkey = func() []byte {
	b, _ := hex.DecodeString("0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	return b
}()

var testGroupPubkeyXOnly = func() []byte {
	x, _ := utils.NormalizeSecp256k1XOnlyPubKey(testGroupPubkey)
	return x
}()

var testTaprootScriptPubKey = func() []byte {
	spk, _, _, _ := utils.BuildDualCustodyTaproot(testGroupPubkey, harnessGuardianPubkey("btc"))
	return spk
}()

func testBN128GroupPubkey(secret int64) []byte {
	grp := curve.NewBN256Group()
	return grp.SerializePoint(grp.ScalarBaseMult(big.NewInt(secret)))
}

func seedFinalizedMigrationIntent(t *testing.T, h *Harness, chainName string, vaultID uint32, oldEpoch, newEpoch uint64, oldPubkey, newPubkey []byte, result *chainpkg.TemplateResult, noFunds bool) {
	t.Helper()
	migrationID := chainpkg.MigrationID(chainName, vaultID, newEpoch)
	templateHash := chainpkg.EmptyMigrationTemplateHash()
	var templateData []byte
	if result != nil {
		templateHash = append([]byte(nil), result.TemplateHash...)
		templateData = append([]byte(nil), result.TemplateData...)
	}
	intentHash := chainpkg.MigrationIntentHash(chainName, vaultID, oldEpoch, newEpoch, oldPubkey, newPubkey, templateHash, noFunds)
	if chainName != "btc" {
		intentHash = result.SigHashes[0]
	}
	intent := &pb.FrostVaultMigrationState{
		MigrationId: migrationID, Chain: chainName, VaultId: vaultID,
		OldEpoch: oldEpoch, NewEpoch: newEpoch,
		OldGroupPubkey: append([]byte(nil), oldPubkey...), NewGroupPubkey: append([]byte(nil), newPubkey...),
		TemplateHash: templateHash, TemplateData: templateData, IntentHash: intentHash,
		Status: "INTENT_FINALIZED", IntentHeight: 10, NoFunds: noFunds,
	}
	if chainName != "btc" {
		intent.AuthorityProtocol, intent.FrozenNextBatchSeq = 3, "1"
	}
	hot := proto.Clone(intent).(*pb.FrostVaultMigrationState)
	hot.TemplateData = nil
	archive := &pb.FrostVaultMigrationArchive{State: intent}
	threshold, err := config.ComputeVaultCommitteeThreshold(len(h.Committee))
	if err != nil {
		t.Fatalf("migration fixture committee: %v", err)
	}
	transition := &pb.VaultTransitionState{
		Chain: chainName, VaultId: vaultID, EpochId: newEpoch,
		SignAlgo:            pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		OldCommitteeMembers: append([]string(nil), h.Committee...), NewCommitteeMembers: append([]string(nil), h.Committee...),
		DkgStatus: "KEY_READY", DkgN: uint32(len(h.Committee)), DkgThresholdT: uint32(threshold), Lifecycle: "DRAINING",
		OldGroupPubkey: append([]byte(nil), oldPubkey...), NewGroupPubkey: append([]byte(nil), newPubkey...),
		MigrationJobId: migrationID, MigrationStatus: "INTENT_FINALIZED",
		MigrationTemplateHash: append([]byte(nil), templateHash...), MigrationIntentHash: append([]byte(nil), intentHash...),
	}
	if chainName == "btc" {
		transition.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
	}
	vault := &pb.FrostVaultState{
		Chain: chainName, VaultId: vaultID, Status: "ACTIVE", KeyEpoch: oldEpoch,
		GroupPubkey: append([]byte(nil), oldPubkey...), CommitteeMembers: append([]string(nil), h.Committee...),
	}
	for key, msg := range map[string]proto.Message{
		keys.KeyFrostVaultMigration(chainName, vaultID, newEpoch):  intent,
		keys.KeyArchiveFrostMigration(migrationID):                 archive,
		keys.KeyFrostVaultTransition(chainName, vaultID, newEpoch): transition,
		keys.KeyFrostVaultState(chainName, vaultID):                vault,
	} {
		data, err := proto.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal migration fixture %s: %v", key, err)
		}
		h.RawSet(key, data)
	}
}

                                                                          

func TestBTCMigration_UTXORestore(t *testing.T) {
	h := New(t, 4)
	tw := newTestTransitionWorker(h)
	h.SeedVaultConfig("btc", 0, 1)

	                    
	h.SeedBtcUtxo(0, "aabb", 0, 50000, testTaprootScriptPubKey, 10)
	h.SeedBtcUtxo(0, "ccdd", 1, 30000, testTaprootScriptPubKey, 20)

	canonical, template, err := btc.BuildMigrationTemplate("btc", 0, 1, 2, []chainpkg.UTXO{
		{TxID: "aabb", Vout: 0, Amount: 50000, ScriptPubKey: testTaprootScriptPubKey, ConfirmHeight: 10},
		{TxID: "ccdd", Vout: 1, Amount: 30000, ScriptPubKey: testTaprootScriptPubKey, ConfirmHeight: 20},
	}, testGroupPubkey, "regtest", 25, harnessGuardianPubkey("btc"), [][]byte{testGroupPubkey, testGroupPubkey})
	inputCount := 0
	if template != nil {
		inputCount = len(template.Inputs)
	}
	if err != nil || inputCount != 2 {
		t.Fatalf("build consensus BTC migration intent: inputs=%d err=%v", inputCount, err)
	}
	seedFinalizedMigrationIntent(t, h, "btc", 0, 1, 2, testGroupPubkey, testGroupPubkey, canonical, false)

	err = tw.PlanMigrationJobs(context.Background(), "btc", 0, 2)
	if err == nil || !strings.Contains(err.Error(), "vault provider and local identity are required") {
		t.Fatalf("worker did not fail closed without a frozen committee provider: %v", err)
	}
	t.Log("BTC migration intent contains the real UTXO set; coordination fails closed without a frozen committee provider")
}

func TestBTCMigration_TaprootAddressDerivation(t *testing.T) {
	addr, err := utils.DualCustodyTaprootAddress(testGroupPubkey, harnessGuardianPubkey("btc"), &chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatalf("TaprootAddressFromPubKey failed: %v", err)
	}
	if addr == "" {
		t.Fatal("derived address is empty")
	}
	t.Logf("derived taproot address: %s", addr)

	                                               
	addr2, _ := utils.DualCustodyTaprootAddress(testGroupPubkey, harnessGuardianPubkey("btc"), &chaincfg.RegressionNetParams)
	if addr != addr2 {
		t.Fatalf("not deterministic: %s vs %s", addr, addr2)
	}

	                                              
	addr3, _ := utils.DualCustodyTaprootAddress(testGroupPubkeyXOnly, harnessGuardianPubkey("btc"), &chaincfg.RegressionNetParams)
	if addr != addr3 {
		t.Fatalf("compressed vs x-only mismatch: %s vs %s", addr, addr3)
	}
}

                                                                          

func TestEVMMigration_MethodIDAndABI(t *testing.T) {
	newPubkey := testBN128GroupPubkey(7)
	contractAddr := "0x00000000000000000000000000000000000000aa"
	result, template, err := evm.BuildMigrationTemplateV2(evm.MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 771, ContractAddr: contractAddr, VaultID: 0, OldEpoch: 1, NewEpoch: 2, NewGroupPubkey: newPubkey, GuardianPubkey: harnessGuardianPubkey("eth"), FrozenNextBatchSeq: "1"})
	if err != nil {
		t.Fatalf("build EVM migration: %v", err)
	}

	                                                                            
	                                                                        
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte("DEX_VAULT_AUTHORITY_HANDOVER_V3"))
	expectedMethodID := h.Sum(nil)[:4]
	if !bytes.Equal(template.MethodID, expectedMethodID) {
		t.Fatalf("methodID mismatch: got=%x want=%x", template.MethodID, expectedMethodID)
	}
	payload := template.Payload

	if len(payload) != 160 {
		t.Fatalf("payload length: want 160 got %d", len(payload))
	}
	if !bytes.Equal(payload[:64], newPubkey) {
		t.Fatal("pubkey not at correct offset")
	}
	if binary.BigEndian.Uint64(payload[88:96]) != 2 {
		t.Fatalf("epochID mismatch")
	}
	if len(result.SigHashes) != 1 || len(result.SigHashes[0]) != 32 {
		t.Fatalf("missing domain-bound EVM signing hash")
	}

	t.Logf("methodID: %s", hex.EncodeToString(expectedMethodID))
	t.Logf("payload (%d bytes): %s", len(payload), hex.EncodeToString(payload))
}

func TestEVMMigration_ContractAddrFromVaultConfig(t *testing.T) {
	h := New(t, 4)
	tw := newTestTransitionWorker(h)

	contractAddr := "0x00000000000000000000000000000000000000aa"
	h.SeedVaultConfigWithRefs("eth", 1, []string{contractAddr})
	oldPubkey := testBN128GroupPubkey(5)
	newPubkey := testBN128GroupPubkey(7)
	canonical, _, err := evm.BuildMigrationTemplateV2(evm.MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 771, ContractAddr: contractAddr, VaultID: 0, OldEpoch: 1, NewEpoch: 2, NewGroupPubkey: newPubkey, GuardianPubkey: harnessGuardianPubkey("eth"), FrozenNextBatchSeq: "1"})
	if err != nil {
		t.Fatalf("build consensus EVM intent: %v", err)
	}
	seedFinalizedMigrationIntent(t, h, "eth", 0, 1, 2, oldPubkey, newPubkey, canonical, false)

	err = tw.PlanMigrationJobs(context.Background(), "eth", 0, 2)
	if err == nil || !strings.Contains(err.Error(), "vault provider and local identity are required") {
		t.Fatalf("worker did not fail closed without a frozen committee provider: %v", err)
	}
	t.Log("EVM migration template is bound to VaultConfig; coordination fails closed without a frozen committee provider")
}

func TestEVMMigration_MissingContractAddr(t *testing.T) {
	_, _, err := evm.BuildMigrationTemplate("eth", 1, "", 0, 1, 2, bytes.Repeat([]byte{2}, 64))
	if err == nil {
		t.Fatal("expected error for missing contract address")
	}
	t.Logf("correctly rejected: %v", err)
}

                                                                                         

func TestSingleVault_SeedVerification(t *testing.T) {
	h := New(t, 4)

	                                                   
	h.SeedVaultState("eth", 0, "ACTIVE", 1)
	h.SeedVaultAvailableBalance("eth", "ETH", 0, 5000)
	h.SeedVaultConfigWithRefs("eth", 1, []string{
		"0x00000000000000000000000000000000000000aa",
	})

	                                    
	withdrawID := "withdraw_composite_1"
	h.SeedQueuedWithdraw("eth", "ETH", withdrawID, "0xreceiveraddr", "4000", 1)

	                     
	h.AssertWithdrawStatus(withdrawID, "QUEUED")

	raw, ok := h.RawGet("v1_frost_vault_cfg_eth_0")
	if !ok {
		t.Fatal("vault config not found")
	}
	var cfg pb.FrostVaultConfig
	if err := proto.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal vault config: %v", err)
	}
	if cfg.VaultCount != 1 || len(cfg.VaultRefs) != 1 {
		t.Fatalf("vault config: count=%d refs=%d", cfg.VaultCount, len(cfg.VaultRefs))
	}

	bal0, _ := h.RawGet("v1_frost_vault_available_eth_ETH_0")
	if string(bal0) != "5000" {
		t.Fatalf("balance seed error: vault0=%s", bal0)
	}

	t.Log("single-Vault seed state verified: one vault with sufficient balance and one queued withdraw")
}
