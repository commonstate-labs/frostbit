package vm_test

import (
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/txverify"
	"dex/utils"
	"dex/vm"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestAccountDepositFailedTransactionsPreserveBlockLegacyMigration(t *testing.T) {
	db := NewMockDB()
	put := func(key string, value proto.Message) {
		t.Helper()
		data, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		db.data[key] = data
	}
	user := utils.GenerateTestIdentity(8301)
	put(keys.KeyAccount(user.Address), &pb.Account{Address: user.Address})
	put(keys.KeyBalance(user.Address, "FB"), &pb.TokenBalanceRecord{
		Address: user.Address, Token: "FB", Balance: &pb.TokenBalance{Balance: "10000000000"},
	})
	put(keys.KeyFrostVaultConfig("eth", 0), &pb.FrostVaultConfig{
		Chain: "eth", VaultCount: 1, DepositProtocol: protocol.DepositProtocolVault, DepositDexChainId: 1,
		VaultRefs: []string{"0x2222222222222222222222222222222222222222"},
	})
	                                                                                
	                                                                                 
	put(keys.KeyFrostVaultState("eth", 0), &pb.FrostVaultState{Chain: "eth", VaultId: 0, Status: "PENDING"})
	legacyHash := "0x" + strings.Repeat("ab", 32)
	legacyKey := keys.KeyRechargeRequestByNativeTx("eth", legacyHash+" ")
	db.data[legacyKey] = []byte("old-deposit")

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutorWithWitness(db, registry, nil, nil)
	defer executor.GetWitnessService().Stop()
	legacyScans := 0
	scan := executor.ScanFn
	executor.ScanFn = func(prefix string) (map[string][]byte, error) {
		if prefix == keys.KeyRechargeNativePrefix() {
			legacyScans++
		}
		return scan(prefix)
	}
	block := &pb.Block{BlockHash: "deposit-failed-migration", Header: &pb.BlockHeader{Height: 1, PrevBlockHash: "genesis"}}
	for nonce := uint64(0); nonce < 2; nonce++ {
		tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
			Base:        &pb.BaseMessage{FromAddress: user.Address, ChainId: 1, Nonce: nonce, Fee: "1000"},
			NativeChain: "eth", NativeTxHash: "0x" + strings.Repeat("cd", 32), Amount: "100",
			ReceiverAddress: user.Address, DepositProtocol: protocol.DepositProtocolVault, DepositEventIndex: uint32(nonce), RechargeFee: "1000000",
		}}}
		if _, err := txverify.SignWithPrivateKey(tx, user.PrivateKeyHex); err != nil {
			t.Fatal(err)
		}
		if err := txverify.Verify(tx, 1); err != nil {
			t.Fatal(err)
		}
		block.Body = append(block.Body, tx)
	}
	result, err := executor.BuildSpecResult(block)
	if err != nil || result == nil || !result.Valid {
		t.Fatalf("BuildSpecResult: result=%+v err=%v", result, err)
	}
	if len(result.Receipts) != len(block.Body) {
		t.Fatalf("receipt count=%d", len(result.Receipts))
	}
	for _, receipt := range result.Receipts {
		if receipt.Status != "FAILED" || receipt.Error != "deposit vault is not active" || receipt.Fee != "1000" {
			t.Fatalf("request did not reach intended business rejection: %+v", receipt)
		}
	}
	writes := make(map[string]vm.WriteOp)
	for _, op := range result.Diff {
		writes[op.Key] = op
	}
	for key, want := range map[string]string{
		keys.KeyRechargeLegacyMigration():               "1",
		keys.KeyRechargeLegacyNative("eth", legacyHash): "old-deposit",
	} {
		op, exists := writes[key]
		if !exists || op.Del || string(op.Value) != want {
			t.Fatalf("failed transaction rolled back migration %s: exists=%v op=%+v", key, exists, op)
		}
	}
	if op, exists := writes[legacyKey]; exists && op.Del {
		t.Fatal("migration deleted the original legacy liability")
	}
	if legacyScans != 1 {
		t.Fatalf("legacy locks scanned %d times for two rejected transactions; want one block-level scan", legacyScans)
	}
	for _, tx := range block.Body {
		if _, exists := writes[keys.KeyRechargeRequest(tx.GetTxId())]; exists {
			t.Fatal("rejected transaction left a ghost request through the service event queue")
		}
		if _, err := executor.GetWitnessService().GetRequest(tx.GetTxId()); err == nil {
			t.Fatal("rejected transaction left a ghost request in service memory")
		}
	}
}
