package vm_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/vm"

	"google.golang.org/protobuf/proto"
)

type adversarialRollbackHandler struct {
	markerKey string
}

func (h adversarialRollbackHandler) Kind() string { return "transfer" }

func (h adversarialRollbackHandler) DryRun(tx *pb.AnyTx, sv vm.StateView) ([]vm.WriteOp, *vm.Receipt, error) {
	sv.Set(h.markerKey, []byte("must-not-commit"))
	err := errors.New("adversarial business rejection")
	return nil, &vm.Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: err.Error()}, err
}

func (adversarialRollbackHandler) Apply(*pb.AnyTx) error { return vm.ErrNotImplemented }

                                     

                                     

                                                                      
                                                                               
                                                        
type unbalancedFinalHookLivenessTracker struct {
	key   string
	value []byte
}

func (m *unbalancedFinalHookLivenessTracker) ProcessBlock(sv iface.LivenessWriteView, _ uint64, _, _ []uint32) ([]uint32, []iface.LivenessSlashEvent) {
	sv.Set(m.key, m.value)
	return nil, nil
}

func (*unbalancedFinalHookLivenessTracker) RegisterMinerAtHeight(iface.LivenessWriteView, string, uint64, uint64) (uint32, error) {
	return 0, nil
}

func (*unbalancedFinalHookLivenessTracker) RemoveMiner(iface.LivenessWriteView, string, uint64) error {
	return nil
}

func (*unbalancedFinalHookLivenessTracker) UnregisterMiner(iface.LivenessWriteView, string) error {
	return nil
}

func (*unbalancedFinalHookLivenessTracker) UnregisterMinersBatch(iface.LivenessWriteView, []string) error {
	return nil
}

func (*unbalancedFinalHookLivenessTracker) IsActive(string) bool                    { return false }
func (*unbalancedFinalHookLivenessTracker) IsActiveOrProbation(string) bool         { return false }
func (*unbalancedFinalHookLivenessTracker) IsRegistered(string) bool                { return false }
func (*unbalancedFinalHookLivenessTracker) GetActiveCount() int                     { return 0 }
func (*unbalancedFinalHookLivenessTracker) GetAddressByIndex(uint32) (string, bool) { return "", false }
func (*unbalancedFinalHookLivenessTracker) GetActiveMiners() []string               { return nil }
func (*unbalancedFinalHookLivenessTracker) GetMinerState(string) int                { return 0 }
func (*unbalancedFinalHookLivenessTracker) GetMinerSnapshot(string) *iface.LivenessMinerSnapshot {
	return nil
}

func TestCommitFinalizedBlockAuditsCommitOnlyHookDiffBeforeStateSync(t *testing.T) {
	db := NewMockDB()
	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))

	hookBalance, err := proto.Marshal(&pb.TokenBalanceRecord{
		Token: "FB",
		Balance: &pb.TokenBalance{
			Balance: "1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	executor.LivenessTracker = &unbalancedFinalHookLivenessTracker{
		key:   keys.KeyBalance("hook_inflation", "FB"),
		value: hookBalance,
	}
	block := &pb.Block{
		BlockHash: "block_unbalanced_final_hook",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
	}

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = executor.CommitFinalizedBlock(block)
	}()
	if recovered == nil {
		t.Fatal("expected finalized-diff conservation audit to reject hook-only inflation")
	}
	if message, ok := recovered.(string); !ok || !strings.Contains(message, "UNBALANCED token=FB delta=1") {
		t.Fatalf("unexpected panic: %v", recovered)
	}

	db.mu.RLock()
	stateSyncCalls := db.stateSyncCalls
	db.mu.RUnlock()
	if stateSyncCalls != 0 {
		t.Fatalf("finalized state synced before audit rejection: calls=%d", stateSyncCalls)
	}
}

                                                          
func TestIdempotency(t *testing.T) {
	db := NewMockDB()

	                                                
	aliceAddr := "alice"
	accountKey := keys.KeyAccount(aliceAddr)

	account := &pb.Account{
		Address: aliceAddr,
	}
	accountData, _ := proto.Marshal(account)
	db.data[accountKey] = accountData

	                     
	bal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000000",
			MinerLockedBalance: "0",
		},
	}
	balData, _ := proto.Marshal(bal)
	db.data[keys.KeyBalance(aliceAddr, "FB")] = balData

	                  
	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}

	cache := vm.NewSpecExecLRU(100)
	executor := vm.NewExecutor(db, registry, cache)

	                     
	transferTx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					TxId:        "tx_idem_001",
					FromAddress: aliceAddr,
					Status:      pb.Status_PENDING,
					Fee:         "1000",
				},
				To:           "bob",
				TokenAddress: "FB",
				Amount:       "100",
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_idem_001",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
		Body: []*pb.AnyTx{transferTx},
	}

	                  
	err := executor.CommitFinalizedBlock(block)
	if err != nil {
		t.Fatal("First commit failed:", err)
	}

	                                                                                                  
	err = executor.CommitFinalizedBlock(block)
	if err != nil {
		t.Fatalf("Second commit should succeed (idempotent): %v", err)
	}

	t.Logf("✅ Idempotency check working: second commit returned nil (already committed)")
}

                                                                                   
                                                        
func TestCommitFinalizedBlockDoesNotMutateInputTxStatus(t *testing.T) {
	db := NewMockDB()

	aliceAddr := "alice_input_immutable"
	account := &pb.Account{Address: aliceAddr}
	accountData, _ := proto.Marshal(account)
	db.data[keys.KeyAccount(aliceAddr)] = accountData

	bal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000000",
			MinerLockedBalance: "0",
		},
	}
	balData, _ := proto.Marshal(bal)
	db.data[keys.KeyBalance(aliceAddr, "FB")] = balData

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	cache := vm.NewSpecExecLRU(100)
	executor := vm.NewExecutor(db, registry, cache)

	tx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					TxId:        "tx_input_immutable_001",
					FromAddress: aliceAddr,
					Status:      pb.Status_PENDING,
					Fee:         "1000",
				},
				To:           "bob_input_immutable",
				TokenAddress: "FB",
				Amount:       "10",
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_input_immutable_001",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
		Body: []*pb.AnyTx{tx},
	}

	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	if got := tx.GetBase().GetStatus(); got != pb.Status_PENDING {
		t.Fatalf("input tx status mutated, want %v got %v", pb.Status_PENDING, got)
	}
}

                                                                                
                                                                                 
func TestReplayTxInLaterBlockShouldNotReapply(t *testing.T) {
	db := NewMockDB()

	aliceAddr := "alice_replay"
	accountKey := keys.KeyAccount(aliceAddr)
	account := &pb.Account{Address: aliceAddr}
	accountData, _ := proto.Marshal(account)
	db.data[accountKey] = accountData

	initialBal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000000",
			MinerLockedBalance: "0",
		},
	}
	initialBalData, _ := proto.Marshal(initialBal)
	balKey := keys.KeyBalance(aliceAddr, "FB")
	db.data[balKey] = initialBalData

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}

	cache := vm.NewSpecExecLRU(100)
	executor := vm.NewExecutor(db, registry, cache)

	txID := "tx_replay_001"
	replayTx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					TxId:        txID,
					FromAddress: aliceAddr,
					Status:      pb.Status_PENDING,
					Fee:         "1000",
				},
				To:           "bob_replay",
				TokenAddress: "FB",
				Amount:       "100",
			},
		},
	}

	block1 := &pb.Block{
		BlockHash: "block_replay_001",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
		Body: []*pb.AnyTx{replayTx},
	}
	if err := executor.CommitFinalizedBlock(block1); err != nil {
		t.Fatalf("first commit failed: %v", err)
	}

	                                               
	block2 := &pb.Block{
		BlockHash: "block_replay_002",
		Header: &pb.BlockHeader{
			PrevBlockHash: block1.BlockHash,
			Height:        2,
		},
		Body: []*pb.AnyTx{replayTx},
	}
	if err := executor.CommitFinalizedBlock(block2); err != nil {
		t.Fatalf("replay block should remain valid: %v", err)
	}
	replayReceipt := executor.TakeCommittedReceipts(block2.BlockHash)[txID]
	if replayReceipt == nil || replayReceipt.Status != "FAILED" || replayReceipt.Fee != "0" {
		t.Fatalf("unexpected replay receipt: %+v", replayReceipt)
	}
	wantReplayErr := "invalid nonce: account alice_replay got=0 expected=1"
	if replayReceipt.Error != wantReplayErr {
		t.Fatalf("unexpected replay error, want %q got %q", wantReplayErr, replayReceipt.Error)
	}

	finalBalData, err := db.Get(balKey)
	if err != nil {
		t.Fatalf("failed to get final balance: %v", err)
	}
	var finalBal pb.TokenBalanceRecord
	if err := proto.Unmarshal(finalBalData, &finalBal); err != nil {
		t.Fatalf("failed to unmarshal final balance: %v", err)
	}

	                                                                                 
	if finalBal.Balance == nil || finalBal.Balance.Balance != "998900" {
		got := "<nil>"
		if finalBal.Balance != nil {
			got = finalBal.Balance.Balance
		}
		t.Fatalf("unexpected final balance, want 998900 got %s", got)
	}

	                                                                                       
	heightVal, err := db.Get(keys.KeyVMTxHeight(txID))
	if err != nil {
		t.Fatalf("failed to get tx height: %v", err)
	}
	if string(heightVal) != "1" {
		t.Fatalf("unexpected tx height, want 1 got %s", string(heightVal))
	}
	finalAccountData, err := db.Get(accountKey)
	if err != nil {
		t.Fatalf("failed to get final account: %v", err)
	}
	var finalAccount pb.Account
	if err := proto.Unmarshal(finalAccountData, &finalAccount); err != nil {
		t.Fatalf("failed to unmarshal final account: %v", err)
	}
	if finalAccount.Nonce != 1 {
		t.Fatalf("stale replay changed nonce, want 1 got %d", finalAccount.Nonce)
	}
}

func TestMatchedNonceFeeFailureConsumesNonce(t *testing.T) {
	db := NewMockDB()
	from, to := "alice_fee_nonce", "bob_fee_nonce"
	accountData, _ := proto.Marshal(&pb.Account{Address: from})
	db.data[keys.KeyAccount(from)] = accountData
	balData, _ := proto.Marshal(&pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "2000", MinerLockedBalance: "0"}})
	db.data[keys.KeyBalance(from, "FB")] = balData

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))
	transfer := func(txID string, nonce uint64, fee string) *pb.AnyTx {
		return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
			Base:         &pb.BaseMessage{TxId: txID, FromAddress: from, Nonce: nonce, Fee: fee, Status: pb.Status_PENDING},
			To:           to,
			TokenAddress: "FB",
			Amount:       "100",
		}}}
	}
	block := &pb.Block{
		BlockHash: "block_fee_nonce",
		Header:    &pb.BlockHeader{PrevBlockHash: "genesis", Height: 1},
		Body: []*pb.AnyTx{
			transfer("tx_fee_nonce_0", 0, "5000"),                                             
			transfer("tx_fee_nonce_1", 1, "1000"),                                        
		},
	}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	receipts := executor.TakeCommittedReceipts(block.BlockHash)
	if rc := receipts["tx_fee_nonce_0"]; rc == nil || rc.Status != "FAILED" || rc.Fee != "0" {
		t.Fatalf("unexpected fee-failed receipt: %+v", rc)
	}
	if rc := receipts["tx_fee_nonce_1"]; rc == nil || rc.Status != "SUCCEED" || rc.Fee != "1000" {
		t.Fatalf("unexpected next-nonce receipt: %+v", rc)
	}

	finalAccountData, err := db.Get(keys.KeyAccount(from))
	if err != nil {
		t.Fatalf("get account: %v", err)
	}
	var finalAccount pb.Account
	if err := proto.Unmarshal(finalAccountData, &finalAccount); err != nil {
		t.Fatalf("unmarshal account: %v", err)
	}
	if finalAccount.Nonce != 2 {
		t.Fatalf("unexpected nonce, want 2 got %d", finalAccount.Nonce)
	}
	finalBalData, err := db.Get(keys.KeyBalance(from, "FB"))
	if err != nil {
		t.Fatalf("get sender balance: %v", err)
	}
	var finalBal pb.TokenBalanceRecord
	if err := proto.Unmarshal(finalBalData, &finalBal); err != nil {
		t.Fatalf("unmarshal sender balance: %v", err)
	}
	if finalBal.Balance == nil || finalBal.Balance.Balance != "900" {
		t.Fatalf("unexpected sender balance: %+v", finalBal.Balance)
	}
}

func TestAppliedTxKVDoesNotSkipConsensusExecution(t *testing.T) {
	db := NewMockDB()
	aliceAddr := "alice_applied_kv"
	accountData, _ := proto.Marshal(&pb.Account{Address: aliceAddr})
	db.data[keys.KeyAccount(aliceAddr)] = accountData
	balData, _ := proto.Marshal(&pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "1000000", MinerLockedBalance: "0"}})
	balKey := keys.KeyBalance(aliceAddr, "FB")
	db.data[balKey] = balData

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))

	txID := "tx_applied_kv_must_not_skip"
	db.data[keys.KeyVMAppliedTx(txID)] = []byte("SUCCEED")
	db.data[keys.KeyVMTxHeight(txID)] = []byte("999")
	tx := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base:         &pb.BaseMessage{TxId: txID, FromAddress: aliceAddr, Status: pb.Status_PENDING, Fee: "1000"},
		To:           "bob_applied_kv",
		TokenAddress: "FB",
		Amount:       "100",
	}}}
	block := &pb.Block{BlockHash: "block_applied_kv", Header: &pb.BlockHeader{PrevBlockHash: "genesis", Height: 1}, Body: []*pb.AnyTx{tx}}
	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	finalBalData, err := db.Get(balKey)
	if err != nil {
		t.Fatalf("failed to get final balance: %v", err)
	}
	var finalBal pb.TokenBalanceRecord
	if err := proto.Unmarshal(finalBalData, &finalBal); err != nil {
		t.Fatalf("failed to unmarshal final balance: %v", err)
	}
	if finalBal.Balance == nil || finalBal.Balance.Balance != "998900" {
		t.Fatalf("local applied tx KV skipped consensus execution, balance=%v", finalBal.Balance)
	}
	readModelRaw, err := db.Get(keys.KeyBlockReadModel(block.BlockHash))
	if err != nil || len(readModelRaw) == 0 {
		t.Fatalf("finalized read-model sidecar missing: len=%d err=%v", len(readModelRaw), err)
	}
	var readModel pb.BlockReadModel
	if err := proto.Unmarshal(readModelRaw, &readModel); err != nil {
		t.Fatalf("decode finalized read-model sidecar: %v", err)
	}
	if err := pb.VerifyBlockReadModel(&readModel, block.Header.Height, block.BlockHash); err != nil {
		t.Fatalf("verify finalized read-model sidecar: %v", err)
	}
}

                                                                                  
                                                                      
func TestCommitFinalizedBlockBuildsAgainstLatestState(t *testing.T) {
	db := NewMockDB()

	aliceAddr := "alice_stale_cache"
	bobAddr := "bob_stale_cache"
	accountKey := keys.KeyAccount(aliceAddr)
	account := &pb.Account{Address: aliceAddr}
	accountData, _ := proto.Marshal(account)
	db.data[accountKey] = accountData
	bobAccountData, _ := proto.Marshal(&pb.Account{Address: bobAddr})
	db.data[keys.KeyAccount(bobAddr)] = bobAccountData

	initialBal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000000",
			MinerLockedBalance: "0",
		},
	}
	initialBalData, _ := proto.Marshal(initialBal)
	balKey := keys.KeyBalance(aliceAddr, "FB")
	db.data[balKey] = initialBalData
	bobBalData, _ := proto.Marshal(&pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000000",
			MinerLockedBalance: "0",
		},
	})
	db.data[keys.KeyBalance(bobAddr, "FB")] = bobBalData

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}

	cache := vm.NewSpecExecLRU(100)
	executor := vm.NewExecutor(db, registry, cache)

	tx1ID := "tx_stale_parent_001"
	tx2ID := "tx_stale_child_002"

	tx1 := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					TxId:        tx1ID,
					FromAddress: bobAddr,
					Status:      pb.Status_PENDING,
					Fee:         "1000",
				},
				To:           aliceAddr,
				TokenAddress: "FB",
				Amount:       "100",
			},
		},
	}
	tx2 := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					TxId:        tx2ID,
					FromAddress: aliceAddr,
					Status:      pb.Status_PENDING,
					Fee:         "1000",
				},
				To:           "bob_stale_child",
				TokenAddress: "FB",
				Amount:       "10",
			},
		},
	}

	block1 := &pb.Block{
		BlockHash: "block_stale_parent_001",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
		Body: []*pb.AnyTx{tx1},
	}
	block2 := &pb.Block{
		BlockHash: "block_stale_child_002",
		Header: &pb.BlockHeader{
			PrevBlockHash: block1.BlockHash,
			Height:        2,
		},
		Body: []*pb.AnyTx{tx2},
	}

	                                                                 
	preRes, err := executor.BuildSpecResult(block2)
	if err != nil {
		t.Fatalf("build child spec failed: %v", err)
	}
	if !preRes.Valid {
		t.Fatalf("pre-execute child invalid: %s", preRes.Reason)
	}

	                                                                           
	if err := executor.CommitFinalizedBlock(block1); err != nil {
		t.Fatalf("commit parent failed: %v", err)
	}
	if err := executor.CommitFinalizedBlock(block2); err != nil {
		t.Fatalf("commit child failed: %v", err)
	}

	finalBalData, err := db.Get(balKey)
	if err != nil {
		t.Fatalf("failed to get final balance: %v", err)
	}
	var finalBal pb.TokenBalanceRecord
	if err := proto.Unmarshal(finalBalData, &finalBal); err != nil {
		t.Fatalf("failed to unmarshal final balance: %v", err)
	}

	                                                
	if finalBal.Balance == nil || finalBal.Balance.Balance != "999090" {
		got := "<nil>"
		if finalBal.Balance != nil {
			got = finalBal.Balance.Balance
		}
		t.Fatalf("unexpected final balance, want 999090 got %s", got)
	}

	                                                    
	heightVal, err := db.Get(keys.KeyVMTxHeight(tx2ID))
	if err != nil {
		t.Fatalf("failed to get tx2 height: %v", err)
	}
	if string(heightVal) != "2" {
		t.Fatalf("unexpected tx2 height, want 2 got %s", string(heightVal))
	}
}

func TestCommitFinalizedBlockSyncsStateDB(t *testing.T) {
	db := NewMockDB()

	aliceAddr := "alice_state_sync"
	account := &pb.Account{Address: aliceAddr}
	accountData, _ := proto.Marshal(account)
	db.data[keys.KeyAccount(aliceAddr)] = accountData

	bal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000000",
			MinerLockedBalance: "0",
		},
	}
	balData, _ := proto.Marshal(bal)
	db.data[keys.KeyBalance(aliceAddr, "FB")] = balData

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))

	tx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					TxId:        "tx_state_sync_001",
					FromAddress: aliceAddr,
					Status:      pb.Status_PENDING,
					Fee:         "1000",
				},
				To:           "bob_state_sync",
				TokenAddress: "FB",
				Amount:       "10",
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_state_sync_001",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
		Body: []*pb.AnyTx{tx},
	}

	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	db.mu.RLock()
	calls := db.stateSyncCalls
	height := db.stateSyncHeight
	ops := db.stateSyncOps
	db.mu.RUnlock()

	if calls != 1 {
		t.Fatalf("expected finalized stateDB sync to be called once, got %d", calls)
	}
	if height != block.Header.Height {
		t.Fatalf("unexpected synced height, want %d got %d", block.Header.Height, height)
	}
	if ops == 0 {
		t.Fatalf("expected non-zero synced ops")
	}
}

func TestDuplicateTxIDInvalidatesWholeBlockWithoutPartialState(t *testing.T) {
	db := NewMockDB()
	from := "alice_duplicate_block"
	accountBytes, err := proto.Marshal(&pb.Account{Address: from})
	if err != nil {
		t.Fatal(err)
	}
	balanceBytes, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: from,
		Token:   vm.FeeToken,
		Balance: &pb.TokenBalance{Balance: "1000000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	db.data[keys.KeyAccount(from)] = accountBytes
	db.data[keys.KeyBalance(from, vm.FeeToken)] = balanceBytes

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))
	transfer := func(nonce uint64, to, amount string) *pb.AnyTx {
		return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
			Base: &pb.BaseMessage{
				TxId:        "duplicated_tx_id",
				FromAddress: from,
				Nonce:       nonce,
				Fee:         "1000",
				Status:      pb.Status_PENDING,
			},
			To:           to,
			TokenAddress: vm.FeeToken,
			Amount:       amount,
		}}}
	}
	block := &pb.Block{
		BlockHash: "block_duplicate_tx_id",
		Header:    &pb.BlockHeader{PrevBlockHash: "genesis", Height: 1},
		Body: []*pb.AnyTx{
			transfer(0, "bob_duplicate_block", "100"),
			transfer(1, "mallory_duplicate_block", "900000"),
		},
	}

	spec, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("build spec: %v", err)
	}
	if spec.Valid || !strings.Contains(spec.Reason, "duplicate tx_id") {
		t.Fatalf("duplicate tx id should invalidate block, valid=%v reason=%q", spec.Valid, spec.Reason)
	}
	if err := executor.CommitFinalizedBlock(block); err == nil || !strings.Contains(err.Error(), "duplicate tx_id") {
		t.Fatalf("finalized duplicate block should be rejected, got %v", err)
	}

	assertStoredBalance := func(address, want string) {
		t.Helper()
		data, err := db.Get(keys.KeyBalance(address, vm.FeeToken))
		if err != nil {
			t.Fatalf("get balance %s: %v", address, err)
		}
		if want == "" {
			if len(data) != 0 {
				t.Fatalf("unexpected balance created for %s", address)
			}
			return
		}
		var record pb.TokenBalanceRecord
		if err := proto.Unmarshal(data, &record); err != nil {
			t.Fatalf("unmarshal balance %s: %v", address, err)
		}
		if record.Balance == nil || record.Balance.Balance != want {
			t.Fatalf("balance %s=%+v, want %s", address, record.Balance, want)
		}
	}
	assertStoredBalance(from, "1000000")
	assertStoredBalance("bob_duplicate_block", "")
	assertStoredBalance("mallory_duplicate_block", "")

	storedAccount, err := db.Get(keys.KeyAccount(from))
	if err != nil {
		t.Fatal(err)
	}
	var account pb.Account
	if err := proto.Unmarshal(storedAccount, &account); err != nil {
		t.Fatal(err)
	}
	if account.Nonce != 0 {
		t.Fatalf("invalid block advanced nonce to %d", account.Nonce)
	}
	if applied, err := db.Get(keys.KeyVMAppliedTx("duplicated_tx_id")); err != nil || len(applied) != 0 {
		t.Fatalf("invalid block persisted applied-tx marker: value=%q err=%v", applied, err)
	}
}

func TestAccountNonceOverflowRejectsBlockWithoutFeeOrTransfer(t *testing.T) {
	db := NewMockDB()
	from := "alice_nonce_overflow"
	accountBytes, err := proto.Marshal(&pb.Account{Address: from, Nonce: math.MaxUint64})
	if err != nil {
		t.Fatal(err)
	}
	balanceBytes, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: from,
		Token:   vm.FeeToken,
		Balance: &pb.TokenBalance{Balance: "1000000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	db.data[keys.KeyAccount(from)] = accountBytes
	db.data[keys.KeyBalance(from, vm.FeeToken)] = balanceBytes

	registry := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(registry); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))
	tx := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{
			TxId:        "tx_nonce_overflow",
			FromAddress: from,
			Nonce:       math.MaxUint64,
			Fee:         "1000",
			Status:      pb.Status_PENDING,
		},
		To:           "bob_nonce_overflow",
		TokenAddress: vm.FeeToken,
		Amount:       "100",
	}}}
	block := &pb.Block{
		BlockHash: "block_nonce_overflow",
		Header:    &pb.BlockHeader{PrevBlockHash: "genesis", Height: 1},
		Body:      []*pb.AnyTx{tx},
	}

	spec, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatalf("build spec: %v", err)
	}
	if spec.Valid || !strings.Contains(spec.Reason, "nonce overflow") {
		t.Fatalf("overflowing nonce should invalidate block, valid=%v reason=%q", spec.Valid, spec.Reason)
	}
	if err := executor.CommitFinalizedBlock(block); err == nil || !strings.Contains(err.Error(), "nonce overflow") {
		t.Fatalf("finalized nonce-overflow block should be rejected, got %v", err)
	}

	storedAccount, err := db.Get(keys.KeyAccount(from))
	if err != nil {
		t.Fatal(err)
	}
	var account pb.Account
	if err := proto.Unmarshal(storedAccount, &account); err != nil {
		t.Fatal(err)
	}
	if account.Nonce != math.MaxUint64 {
		t.Fatalf("nonce wrapped or changed: got %d", account.Nonce)
	}
	storedBalance, err := db.Get(keys.KeyBalance(from, vm.FeeToken))
	if err != nil {
		t.Fatal(err)
	}
	var balance pb.TokenBalanceRecord
	if err := proto.Unmarshal(storedBalance, &balance); err != nil {
		t.Fatal(err)
	}
	if balance.Balance == nil || balance.Balance.Balance != "1000000" {
		t.Fatalf("invalid block charged sender: %+v", balance.Balance)
	}
	if recipient, err := db.Get(keys.KeyBalance("bob_nonce_overflow", vm.FeeToken)); err != nil || len(recipient) != 0 {
		t.Fatalf("invalid block credited recipient: value=%x err=%v", recipient, err)
	}
	if pool, err := db.Get(keys.KeyBalance(vm.RuntimeFeePoolAccount, vm.FeeToken)); err != nil || len(pool) != 0 {
		t.Fatalf("invalid block credited runtime fee pool: value=%x err=%v", pool, err)
	}
}

func TestBusinessFailureRollsBackDirectWritesButConsumesFeeAndNonce(t *testing.T) {
	db := NewMockDB()
	from := "alice_business_rollback"
	accountBytes, err := proto.Marshal(&pb.Account{Address: from})
	if err != nil {
		t.Fatal(err)
	}
	balanceBytes, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: from,
		Token:   vm.FeeToken,
		Balance: &pb.TokenBalance{Balance: "10000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	db.data[keys.KeyAccount(from)] = accountBytes
	db.data[keys.KeyBalance(from, vm.FeeToken)] = balanceBytes

	const markerKey = "adversarial_business_write"
	registry := vm.NewHandlerRegistry()
	if err := registry.Register(adversarialRollbackHandler{markerKey: markerKey}); err != nil {
		t.Fatal(err)
	}
	executor := vm.NewExecutor(db, registry, vm.NewSpecExecLRU(16))
	tx := &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{
			TxId:        "tx_business_rollback",
			FromAddress: from,
			Nonce:       0,
			Fee:         "1000",
			Status:      pb.Status_PENDING,
		},
	}}}
	block := &pb.Block{
		BlockHash: "block_business_rollback",
		Header:    &pb.BlockHeader{PrevBlockHash: "genesis", Height: 1},
		Body:      []*pb.AnyTx{tx},
	}

	if err := executor.CommitFinalizedBlock(block); err != nil {
		t.Fatalf("business rejection should not invalidate block: %v", err)
	}
	receipt := executor.TakeCommittedReceipts(block.BlockHash)[tx.GetTxId()]
	if receipt == nil || receipt.Status != "FAILED" || receipt.Fee != "1000" || receipt.Error != "adversarial business rejection" {
		t.Fatalf("unexpected business-failure receipt: %+v", receipt)
	}
	if marker, err := db.Get(markerKey); err != nil || len(marker) != 0 {
		t.Fatalf("failed handler write escaped rollback: value=%q err=%v", marker, err)
	}

	storedAccount, err := db.Get(keys.KeyAccount(from))
	if err != nil {
		t.Fatal(err)
	}
	var account pb.Account
	if err := proto.Unmarshal(storedAccount, &account); err != nil {
		t.Fatal(err)
	}
	if account.Nonce != 1 {
		t.Fatalf("matched business failure did not consume nonce: got %d", account.Nonce)
	}
	for address, want := range map[string]string{
		from:                     "9000",
		vm.RuntimeFeePoolAccount: "1000",
	} {
		stored, err := db.Get(keys.KeyBalance(address, vm.FeeToken))
		if err != nil {
			t.Fatalf("get %s balance: %v", address, err)
		}
		var record pb.TokenBalanceRecord
		if err := proto.Unmarshal(stored, &record); err != nil {
			t.Fatalf("unmarshal %s balance: %v", address, err)
		}
		if record.Balance == nil || record.Balance.Balance != want {
			t.Fatalf("balance %s=%+v, want %s", address, record.Balance, want)
		}
	}
	if applied, err := db.Get(keys.KeyVMAppliedTx(tx.GetTxId())); err != nil || string(applied) != "FAILED" {
		t.Fatalf("business failure persistence=%q err=%v", applied, err)
	}
}
