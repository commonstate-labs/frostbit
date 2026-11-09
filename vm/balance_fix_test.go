package vm_test

import (
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/vm"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

                                                                                                                         
func TestOrderBalance_TakerAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	dbMgr, err := db.NewManager(tmpDir, logs.NewNodeLogger("test", 0))
	require.NoError(t, err)
	defer dbMgr.Close()
	dbMgr.InitWriteQueue(10, 50*time.Millisecond)

	registry := vm.NewHandlerRegistry()
	require.NoError(t, vm.RegisterDefaultHandlers(registry))
	executor := vm.NewExecutor(dbMgr, registry, nil)

	aliceAddr := "alice"
	bobAddr := "bob"

	                                                                                                                 
	createE2ETestAccount(t, dbMgr, aliceAddr, map[string]string{"BTC": "1000000000", "USDT": "0", "FB": "100000"})            
	createE2ETestAccount(t, dbMgr, bobAddr, map[string]string{"BTC": "0", "USDT": "10000000000000", "FB": "100000"})               
	require.NoError(t, dbMgr.ForceFlush())

	                                            
	block1 := &pb.Block{
		BlockHash: "b1",
		Header:    &pb.BlockHeader{Height: 1, StateRoot: dbMgr.GetStateFingerprint()},
		Body:      []*pb.AnyTx{createSellOrder("s1", aliceAddr, "BTC", "USDT", "5000000000000", "200000000")},
	}
	require.NoError(t, executor.CommitFinalizedBlock(block1))
	require.NoError(t, dbMgr.ForceFlush())

	                                                                                  
	                                                                                        
	buyOrder := createBuyOrder("b1", bobAddr, "BTC", "USDT", "5000000000000", "100000000")
	block2 := &pb.Block{
		BlockHash: "b2",
		Header:    &pb.BlockHeader{Height: 2, PrevBlockHash: "b1", StateRoot: dbMgr.GetStateFingerprint()},
		Body:      []*pb.AnyTx{buyOrder},
	}
	require.NoError(t, executor.CommitFinalizedBlock(block2))
	require.NoError(t, dbMgr.ForceFlush())

	bobUSDT := getE2EBalance(t, dbMgr, bobAddr, "USDT")
	bobBTC := getE2EBalance(t, dbMgr, bobAddr, "BTC")
	assert.Equal(t, "5000000000000", bobUSDT.Balance)              
	assert.Equal(t, "0", bobUSDT.OrderFrozenBalance)
	assert.Equal(t, "100000000", bobBTC.Balance)         
}

                                                                           
func TestOrderBalance_BuyRefund(t *testing.T) {
	tmpDir := t.TempDir()
	dbMgr, err := db.NewManager(tmpDir, logs.NewNodeLogger("test", 0))
	require.NoError(t, err)
	defer dbMgr.Close()
	dbMgr.InitWriteQueue(10, 50*time.Millisecond)

	registry := vm.NewHandlerRegistry()
	require.NoError(t, vm.RegisterDefaultHandlers(registry))
	executor := vm.NewExecutor(dbMgr, registry, nil)

	aliceAddr := "alice"
	bobAddr := "bob"

	                                                                                          
	createE2ETestAccount(t, dbMgr, aliceAddr, map[string]string{"BTC": "1000000000", "USDT": "0", "FB": "100000"})            
	createE2ETestAccount(t, dbMgr, bobAddr, map[string]string{"BTC": "0", "USDT": "10000000000000", "FB": "100000"})               
	require.NoError(t, dbMgr.ForceFlush())

	block1 := &pb.Block{
		BlockHash: "b1",
		Header:    &pb.BlockHeader{Height: 1, StateRoot: dbMgr.GetStateFingerprint()},
		Body:      []*pb.AnyTx{createSellOrder("s1", aliceAddr, "BTC", "USDT", "4000000000000", "100000000")},
	}
	require.NoError(t, executor.CommitFinalizedBlock(block1))
	require.NoError(t, dbMgr.ForceFlush())

	                                                                                               
	                                                                               
	buyOrder := createBuyOrder("b1", bobAddr, "BTC", "USDT", "6000000000000", "100000000")
	block2 := &pb.Block{
		BlockHash: "b2",
		Header:    &pb.BlockHeader{Height: 2, PrevBlockHash: "b1", StateRoot: dbMgr.GetStateFingerprint()},
		Body:      []*pb.AnyTx{buyOrder},
	}
	require.NoError(t, executor.CommitFinalizedBlock(block2))
	require.NoError(t, dbMgr.ForceFlush())

	bobUSDT := getE2EBalance(t, dbMgr, bobAddr, "USDT")
	bobBTC := getE2EBalance(t, dbMgr, bobAddr, "BTC")
	t.Logf("Bob USDT Balance: %s, Frozen: %s", bobUSDT.Balance, bobUSDT.OrderFrozenBalance)
	assert.Equal(t, "6000000000000", bobUSDT.Balance, "Bob should get 20000 USDT refund for price improvement")
	assert.Equal(t, "0", bobUSDT.OrderFrozenBalance)
	assert.Equal(t, "100000000", bobBTC.Balance)
}

                                                                    
func TestOrderBalance_RemoveOrder(t *testing.T) {
	tmpDir := t.TempDir()
	dbMgr, err := db.NewManager(tmpDir, logs.NewNodeLogger("test", 0))
	require.NoError(t, err)
	defer dbMgr.Close()
	dbMgr.InitWriteQueue(10, 50*time.Millisecond)

	registry := vm.NewHandlerRegistry()
	require.NoError(t, vm.RegisterDefaultHandlers(registry))
	executor := vm.NewExecutor(dbMgr, registry, nil)

	aliceAddr := "alice"
	createE2ETestAccount(t, dbMgr, aliceAddr, map[string]string{"BTC": "0", "USDT": "10000000000000", "FB": "100000"})               
	require.NoError(t, dbMgr.ForceFlush())

	                                                                                   
	buyOrder := createBuyOrder("b1", aliceAddr, "BTC", "USDT", "6000000000000", "100000000")
	block1 := &pb.Block{
		BlockHash: "b1",
		Header:    &pb.BlockHeader{Height: 1, StateRoot: dbMgr.GetStateFingerprint()},
		Body:      []*pb.AnyTx{buyOrder},
	}
	require.NoError(t, executor.CommitFinalizedBlock(block1))
	require.NoError(t, dbMgr.ForceFlush())

	                     
	removeTx := &pb.AnyTx{
		Content: &pb.AnyTx_OrderTx{
			OrderTx: &pb.OrderTx{
				Base: &pb.BaseMessage{
					FromAddress: aliceAddr,
					TxId:        "remove_b1",
					Fee:         "1000",
					Nonce:       1,
				},
				Op:         pb.OrderOp_REMOVE,
				OpTargetId: "b1",
			},
		},
	}
	block2 := &pb.Block{
		BlockHash: "b2",
		Header:    &pb.BlockHeader{Height: 2, PrevBlockHash: "b1", StateRoot: dbMgr.GetStateFingerprint()},
		Body:      []*pb.AnyTx{removeTx},
	}
	require.NoError(t, executor.CommitFinalizedBlock(block2))
	require.NoError(t, dbMgr.ForceFlush())

	aliceUSDT := getE2EBalance(t, dbMgr, aliceAddr, "USDT")
	assert.Equal(t, "10000000000000", aliceUSDT.Balance)               
	assert.Equal(t, "0", aliceUSDT.OrderFrozenBalance)
}

func TestOrderBalance_RemoveOrderRejectsAlreadyCancelled(t *testing.T) {
	tmpDir := t.TempDir()
	dbMgr, err := db.NewManager(tmpDir, logs.NewNodeLogger("test", 0))
	require.NoError(t, err)
	defer dbMgr.Close()
	dbMgr.InitWriteQueue(10, 50*time.Millisecond)

	registry := vm.NewHandlerRegistry()
	require.NoError(t, vm.RegisterDefaultHandlers(registry))
	executor := vm.NewExecutor(dbMgr, registry, nil)

	aliceAddr := "alice"
	createE2ETestAccount(t, dbMgr, aliceAddr, map[string]string{"BTC": "0", "USDT": "20000000000000", "FB": "100000"})
	require.NoError(t, dbMgr.ForceFlush())

	commit := func(hash, prev string, height uint64, tx *pb.AnyTx) {
		t.Helper()
		require.NoError(t, executor.CommitFinalizedBlock(&pb.Block{
			BlockHash: hash,
			Header:    &pb.BlockHeader{Height: height, PrevBlockHash: prev, StateRoot: dbMgr.GetStateFingerprint()},
			Body:      []*pb.AnyTx{tx},
		}))
		require.NoError(t, dbMgr.ForceFlush())
	}
	removeOrder := func(txID, targetID string, nonce uint64) *pb.AnyTx {
		return &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
			Base:       &pb.BaseMessage{FromAddress: aliceAddr, TxId: txID, Fee: "1000", Nonce: nonce},
			Op:         pb.OrderOp_REMOVE,
			OpTargetId: targetID,
		}}}
	}

	commit("b1", "", 1, createBuyOrder("b1_order", aliceAddr, "BTC", "USDT", "6000000000000", "100000000"))
	commit("b2", "b1", 2, removeOrder("remove_b1_once", "b1_order", 1))
	b2Order := createBuyOrder("b2_order", aliceAddr, "BTC", "USDT", "6000000000000", "100000000")
	b2Order.GetBase().Nonce = 2
	commit("b3", "b2", 3, b2Order)

	replayRemove := removeOrder("remove_b1_twice", "b1_order", 3)
	spec, err := executor.BuildSpecResult(&pb.Block{
		BlockHash: "b4",
		Header:    &pb.BlockHeader{Height: 4, PrevBlockHash: "b3", StateRoot: dbMgr.GetStateFingerprint()},
		Body:      []*pb.AnyTx{replayRemove},
	})
	require.NoError(t, err)
	require.True(t, spec.Valid)
	require.Len(t, spec.Receipts, 1)
	require.Equal(t, "FAILED", spec.Receipts[0].Status)

	commit("b4", "b3", 4, replayRemove)
	aliceUSDT := getE2EBalance(t, dbMgr, aliceAddr, "USDT")
	assert.Equal(t, "14000000000000", aliceUSDT.Balance)
	assert.Equal(t, "6000000000000", aliceUSDT.OrderFrozenBalance)
}

func createE2ETestAccount(t *testing.T, dbMgr *db.Manager, address string, balances map[string]string) {
	account := &pb.Account{Address: address}
	accountData, err := proto.Marshal(account)
	require.NoError(t, err)
	dbMgr.EnqueueSet(keys.KeyAccount(address), string(accountData))

	for token, amount := range balances {
		tokenData, err := proto.Marshal(&pb.Token{Address: token})
		require.NoError(t, err)
		dbMgr.EnqueueSet(keys.KeyToken(token), string(tokenData))

		bal := &pb.TokenBalanceRecord{
			Address: address,
			Token:   token,
			Balance: &pb.TokenBalance{
				Balance:            amount,
				OrderFrozenBalance: "0",
				MinerLockedBalance: "0",
			},
		}
		balData, err := proto.Marshal(bal)
		require.NoError(t, err)
		dbMgr.EnqueueSet(keys.KeyBalance(address, token), string(balData))
	}
}

func createSellOrder(txID, fromAddr, baseToken, quoteToken, price, amount string) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_OrderTx{
			OrderTx: &pb.OrderTx{
				Base: &pb.BaseMessage{
					TxId:        txID,
					FromAddress: fromAddr,
					Fee:         "1000",
					Nonce:       0,
				},
				BaseToken:  baseToken,
				QuoteToken: quoteToken,
				Op:         pb.OrderOp_ADD,
				Side:       pb.OrderSide_SELL,
				Price:      price,
				Amount:     amount,
			},
		},
	}
}

func createBuyOrder(txID, fromAddr, baseToken, quoteToken, price, amount string) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_OrderTx{
			OrderTx: &pb.OrderTx{
				Base: &pb.BaseMessage{
					TxId:        txID,
					FromAddress: fromAddr,
					Fee:         "1000",
					Nonce:       0,
				},
				BaseToken:  baseToken,
				QuoteToken: quoteToken,
				Op:         pb.OrderOp_ADD,
				Side:       pb.OrderSide_BUY,
				Price:      price,
				Amount:     amount,
			},
		},
	}
}

func getE2EBalance(t *testing.T, dbMgr *db.Manager, address, token string) *pb.TokenBalance {
	raw, err := dbMgr.Get(keys.KeyBalance(address, token))
	require.NoError(t, err)
	require.NotNil(t, raw, "balance should exist for %s %s", address, token)

	var rec pb.TokenBalanceRecord
	require.NoError(t, proto.Unmarshal(raw, &rec))
	require.NotNil(t, rec.Balance, "balance payload should not be nil")
	return rec.Balance
}
