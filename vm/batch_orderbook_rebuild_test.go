package vm_test

import (
	dbpkg "dex/db"
	"fmt"
	"math/big"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/vm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

                                                                            
func TestBatchOrderBookRebuild_SinglePair(t *testing.T) {
	db := NewMockDB()

	                     
	createTestAccount(t, db, "test_user", "BTC", "10.0")
	createTestAccount(t, db, "test_user", "USDT", "100000.0")
	createTestAccount(t, db, "test_user", "FB", "1000000")

	executor := createTestExecutor(db)

	                                                             
	pair := "BTC_USDT"
	orders := createTestOrders(t, db, pair, 3)

	                                          
	block := &pb.Block{
		BlockHash: "block1",
		Header: &pb.BlockHeader{
			PrevBlockHash: "",
			Height:        1,
		},
		Body: []*pb.AnyTx{
			createOrderAnyTx("new_order_1", "BTC", "USDT", "50000", "1.0", 0),
		},
	}

	                  
	result, err := executor.BuildSpecResult(block)

	         
	require.NoError(t, err)
	assert.NotNil(t, result)

	               
	if !result.Valid {
		t.Logf("Block invalid, reason: %s", result.Reason)
	}
	t.Logf("Receipts count: %d", len(result.Receipts))
	for i, r := range result.Receipts {
		t.Logf("Receipt %d: TxID=%s, Status=%s, Error=%s", i, r.TxID, r.Status, r.Error)
	}

	assert.True(t, result.Valid, "Block should be valid, reason: %s", result.Reason)
	assert.Equal(t, 1, len(result.Receipts))

	                                                                              
	if len(result.Receipts) > 0 {
		receipt := result.Receipts[0]
		assert.Equal(t, "SUCCEED", receipt.Status)
	}

	t.Logf("Successfully rebuilt order book for pair %s with %d existing orders", pair, len(orders))
}

                                                                               
func TestBatchOrderBookRebuild_MultiplePairs(t *testing.T) {
	db := NewMockDB()

	                     
	createTestAccount(t, db, "test_user", "BTC", "100.0")
	createTestAccount(t, db, "test_user", "ETH", "1000.0")
	createTestAccount(t, db, "test_user", "TRX", "10000.0")
	createTestAccount(t, db, "test_user", "USDT", "1000000.0")
	createTestAccount(t, db, "test_user", "FB", "10000000")

	executor := createTestExecutor(db)

	                                               
	pairs := []string{"BTC_USDT", "ETH_USDT", "TRX_USDT"}
	ordersPerPair := 5

	totalOrders := 0
	for _, pair := range pairs {
		orders := createTestOrders(t, db, pair, ordersPerPair)
		totalOrders += len(orders)
		t.Logf("Created %d orders for pair %s", len(orders), pair)
	}

	                                                   
	block := &pb.Block{
		BlockHash: "block1",
		Header: &pb.BlockHeader{
			PrevBlockHash: "",
			Height:        1,
		},
		Body: []*pb.AnyTx{
			createOrderAnyTx("new_btc_1", "BTC", "USDT", "50000", "1", 0),
			createOrderAnyTx("new_eth_1", "ETH", "USDT", "3000", "2", 1),
			createOrderAnyTx("new_trx_1", "TRX", "USDT", "100", "10", 2),
		},
	}

	                  
	result, err := executor.BuildSpecResult(block)

	         
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Valid)
	assert.Equal(t, 3, len(result.Receipts))

	                                    
	for i, receipt := range result.Receipts {
		assert.Equal(t, "SUCCEED", receipt.Status, "Receipt %d should succeed", i)
	}

	t.Logf("Successfully rebuilt %d order books with total %d existing orders", len(pairs), totalOrders)
}

                                                                          
func TestBatchOrderBookRebuild_Performance(t *testing.T) {
	db := NewMockDB()

	                     
	createTestAccount(t, db, "test_user", "BTC", "1000.0")
	createTestAccount(t, db, "test_user", "ETH", "10000.0")
	createTestAccount(t, db, "test_user", "TRX", "100000.0")
	createTestAccount(t, db, "test_user", "DOGE", "1000000.0")
	createTestAccount(t, db, "test_user", "ADA", "1000000.0")
	createTestAccount(t, db, "test_user", "USDT", "10000000.0")
	createTestAccount(t, db, "test_user", "FB", "100000000")

	executor := createTestExecutor(db)

	                           
	pairs := []string{"BTC_USDT", "ETH_USDT", "TRX_USDT", "DOGE_USDT", "ADA_USDT"}
	ordersPerPair := 20

	totalOrders := 0
	for _, pair := range pairs {
		orders := createTestOrders(t, db, pair, ordersPerPair)
		totalOrders += len(orders)
	}

	                                    
	block := &pb.Block{
		BlockHash: "block1",
		Header: &pb.BlockHeader{
			PrevBlockHash: "",
			Height:        1,
		},
		Body: make([]*pb.AnyTx, 0, 50),
	}

	                                           
	orderID := 0
	for _, pair := range pairs {
		tokens := splitPair(pair)
		for i := 0; i < 10; i++ {
			orderID++
			block.Body = append(block.Body,
				createOrderAnyTx(
					fmt.Sprintf("new_order_%d", orderID),
					tokens[0],
					tokens[1],
					"50000",
					"1",
					uint64(orderID-1),
				),
			)
		}
	}

	                           
	result, err := executor.BuildSpecResult(block)

	         
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Valid)
	assert.Equal(t, 50, len(result.Receipts))

	successCount := 0
	for _, receipt := range result.Receipts {
		if receipt.Status == "SUCCEED" {
			successCount++
		}
	}

	t.Logf("Performance test: %d pairs, %d existing orders, %d new orders, %d succeeded",
		len(pairs), totalOrders, len(block.Body), successCount)
}

                                                                       
func TestBatchOrderBookRebuild_EmptyOrderBook(t *testing.T) {
	db := NewMockDB()

	                     
	createTestAccount(t, db, "test_user", "BTC", "10.0")
	createTestAccount(t, db, "test_user", "USDT", "100000.0")
	createTestAccount(t, db, "test_user", "FB", "1000000")

	executor := createTestExecutor(db)

	                                              
	block := &pb.Block{
		BlockHash: "block1",
		Header: &pb.BlockHeader{
			PrevBlockHash: "",
			Height:        1,
		},
		Body: []*pb.AnyTx{
			createOrderAnyTx("new_order_1", "BTC", "USDT", "50000", "1.0", 0),
		},
	}

	                  
	result, err := executor.BuildSpecResult(block)

	         
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Valid)
	assert.Equal(t, 1, len(result.Receipts))
	assert.Equal(t, "SUCCEED", result.Receipts[0].Status)

	t.Log("Successfully handled empty order book scenario")
}

                                                                          
func TestBatchOrderBookRebuild_Matching(t *testing.T) {
	db := NewMockDB()

	                     
	createTestAccount(t, db, "seller", "BTC", "10.0")
	createTestAccount(t, db, "seller", "FB", "1000000")
	createTestAccount(t, db, "test_user", "USDT", "100000.0")
	createTestAccount(t, db, "test_user", "FB", "1000000")

	executor := createTestExecutor(db)

	pair := "BTC_USDT"

	                               
	sellOrder := &pb.OrderTx{
		Base: &pb.BaseMessage{
			TxId:        "sell_order_1",
			FromAddress: "seller",
		},
		BaseToken:  "BTC",
		QuoteToken: "USDT",
		Op:         pb.OrderOp_ADD,
		Side:       pb.OrderSide_SELL,
		Price:      "50000",
		Amount:     "1",
	}
	saveOrderToDB(t, db, sellOrder, pair)

	                              
	block := &pb.Block{
		BlockHash: "block1",
		Header: &pb.BlockHeader{
			PrevBlockHash: "",
			Height:        1,
		},
		Body: []*pb.AnyTx{
			createBuyOrderAnyTx("buy_order_1", "BTC", "USDT", "50000", "1", 0),
		},
	}

	                  
	result, err := executor.BuildSpecResult(block)

	         
	require.NoError(t, err)
	assert.NotNil(t, result)
	if !result.Valid {
		t.Logf("Block not valid, reason: %s", result.Reason)
	}
	require.True(t, result.Valid, "Block should be valid")
	require.Equal(t, 1, len(result.Receipts), "Should have 1 receipt")

	receipt := result.Receipts[0]
	if receipt.Status != "SUCCEED" {
		t.Logf("Receipt failed: %s", receipt.Error)
	}
	assert.Equal(t, "SUCCEED", receipt.Status)

	                                             
	assert.NotEmpty(t, result.Diff)

	t.Logf("Matching test: generated %d write operations", len(result.Diff))
}

                                                                               
func TestBatchOrderBookRebuild_MixedOperations(t *testing.T) {
	db := NewMockDB()

	                     
	createTestAccount(t, db, "test_user", "BTC", "10.0")
	createTestAccount(t, db, "test_user", "USDT", "100000.0")
	createTestAccount(t, db, "test_user", "FB", "1000000")
	createTestAccount(t, db, "user1", "BTC", "10.0")
	createTestAccount(t, db, "user1", "FB", "1000000")

	executor := createTestExecutor(db)

	pair := "BTC_USDT"
	createTestOrders(t, db, pair, 3)

	                                              
	block := &pb.Block{
		BlockHash: "block1",
		Header: &pb.BlockHeader{
			PrevBlockHash: "",
			Height:        1,
		},
		Body: []*pb.AnyTx{
			createOrderAnyTx("new_order_1", "BTC", "USDT", "50000", "1.0", 0),
			                                              
			{
				Content: &pb.AnyTx_OrderTx{
					OrderTx: &pb.OrderTx{
						Base: &pb.BaseMessage{
							TxId:        "remove_order_1",
							FromAddress: "user1",
							Fee:         "1000",
						},
						Op: pb.OrderOp_REMOVE,
					},
				},
			},
		},
	}

	                  
	result, err := executor.BuildSpecResult(block)

	         
	require.NoError(t, err)
	assert.NotNil(t, result)

	t.Logf("Mixed operations test: %d receipts generated", len(result.Receipts))
}

                                     

                                                 
func createTestExecutor(db *MockDB) *vm.Executor {
	reg := vm.NewHandlerRegistry()
	if err := vm.RegisterDefaultHandlers(reg); err != nil {
		panic(err)
	}

	cache := vm.NewSpecExecLRU(1024)
	return vm.NewExecutor(db, reg, cache)
}

                                                               
func createTestAccount(t *testing.T, db *MockDB, address, tokenAddress, balance string) {
	balance = normalizeTestAmount(balance)
	accountKey := keys.KeyAccount(address)

	                           
	db.mu.RLock()
	existingData, exists := db.data[accountKey]
	db.mu.RUnlock()

	var account *pb.Account
	if exists {
		                                             
		account = &pb.Account{}
		if err := proto.Unmarshal(existingData, account); err != nil {
			account = &pb.Account{
				Address: address,
			}
		}
	} else {
		                                    
		account = &pb.Account{
			Address: address,
		}
	}

	                                             
	accountData, err := proto.Marshal(account)
	if err != nil {
		if t != nil {
			require.NoError(t, err)
		} else {
			panic(err)
		}
	}

	db.mu.Lock()
	db.data[accountKey] = accountData
	db.mu.Unlock()

	                                       
	bal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            balance,
			MinerLockedBalance: "0",
		},
	}
	balData, err := proto.Marshal(bal)
	if err != nil {
		if t != nil {
			require.NoError(t, err)
		} else {
			panic(err)
		}
	}
	balKey := keys.KeyBalance(address, tokenAddress)
	db.mu.Lock()
	db.data[balKey] = balData
	tokenData, tokenErr := proto.Marshal(&pb.Token{Address: tokenAddress})
	if tokenErr != nil {
		db.mu.Unlock()
		if t != nil {
			require.NoError(t, tokenErr)
		} else {
			panic(tokenErr)
		}
		return
	}
	db.data[keys.KeyToken(tokenAddress)] = tokenData
	db.mu.Unlock()
}

                                                               
func createTestOrders(t *testing.T, db *MockDB, pair string, count int) []*pb.OrderTx {
	orders := make([]*pb.OrderTx, count)
	tokens := splitPair(pair)

	for i := 0; i < count; i++ {
		order := &pb.OrderTx{
			Base: &pb.BaseMessage{
				TxId:        fmt.Sprintf("order_%s_%d", pair, i),
				FromAddress: fmt.Sprintf("user_%d", i),
			},
			BaseToken:  tokens[0],
			QuoteToken: tokens[1],
			Op:         pb.OrderOp_ADD,
			Side:       pb.OrderSide_SELL,                      
			Price:      fmt.Sprintf("%d", 50000+i*100),
			Amount:     "1.0",
		}
		saveOrderToDB(t, db, order, pair)
		orders[i] = order
	}

	return orders
}

                                                                                                    
func saveOrderToDB(t *testing.T, db *MockDB, order *pb.OrderTx, pair string) {
	order.Price = normalizeTestPrice(order.Price)
	order.Amount = normalizeTestAmount(order.Amount)
	                                               
	orderState := &pb.OrderState{
		OrderId:     order.Base.TxId,
		FilledBase:  "0",
		FilledQuote: "0",
		IsFilled:    false,
		Status:      pb.OrderStateStatus_ORDER_OPEN,
		BaseToken:   order.BaseToken,
		QuoteToken:  order.QuoteToken,
		Amount:      order.Amount,
		Price:       order.Price,
		Side:        order.Side,
		Owner:       order.Base.FromAddress,
	}
	stateData, err := proto.Marshal(orderState)
	if err != nil {
		if t != nil {
			require.NoError(t, err)
		} else {
			panic(err)
		}
	}
	stateKey := keys.KeyOrderState(order.Base.TxId)
	db.mu.Lock()
	db.data[stateKey] = stateData
	db.mu.Unlock()

	                     
	priceKey67, err := dbpkg.PriceToKey128(order.Price)
	if err != nil {
		if t != nil {
			require.NoError(t, err)
		} else {
			panic(err)
		}
	}

	indexKey := keys.KeyOrderPriceIndex(pair, order.Side, false, priceKey67, order.Base.TxId)
	indexData, _ := proto.Marshal(&pb.OrderPriceIndex{Ok: true})
	db.mu.Lock()
	db.data[indexKey] = indexData
	db.mu.Unlock()

	                                                  
	if order.Side == pb.OrderSide_SELL {
		balKey := keys.KeyBalance(order.Base.FromAddress, order.BaseToken)
		db.mu.RLock()
		balData, exists := db.data[balKey]
		db.mu.RUnlock()
		if exists {
			var rec pb.TokenBalanceRecord
			if proto.Unmarshal(balData, &rec) == nil && rec.Balance != nil {
				currentBal := new(big.Int)
				currentBal.SetString(rec.Balance.Balance, 10)
				orderAmt := new(big.Int)
				orderAmt.SetString(order.Amount, 10)
				currentFrz := new(big.Int)
				if rec.Balance.OrderFrozenBalance != "" {
					currentFrz.SetString(rec.Balance.OrderFrozenBalance, 10)
				}
				rec.Balance.Balance = new(big.Int).Sub(currentBal, orderAmt).String()
				rec.Balance.OrderFrozenBalance = new(big.Int).Add(currentFrz, orderAmt).String()
				if newData, err := proto.Marshal(&rec); err == nil {
					db.mu.Lock()
					db.data[balKey] = newData
					db.mu.Unlock()
				}
			}
		}
	}
}

                                                           
func createOrderAnyTx(txID, baseToken, quoteToken, price, amount string, nonce uint64) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_OrderTx{
			OrderTx: &pb.OrderTx{
				Base: &pb.BaseMessage{
					TxId:        txID,
					FromAddress: "test_user",
					Fee:         "1000",
					Nonce:       nonce,
				},
				BaseToken:  baseToken,
				QuoteToken: quoteToken,
				Op:         pb.OrderOp_ADD,
				Side:       pb.OrderSide_SELL,                   
				Price:      normalizeTestPrice(price),
				Amount:     normalizeTestAmount(amount),
			},
		},
	}
}

                                         
func createBuyOrderAnyTx(txID, baseToken, quoteToken, price, amount string, nonce uint64) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_OrderTx{
			OrderTx: &pb.OrderTx{
				Base: &pb.BaseMessage{
					TxId:        txID,
					FromAddress: "test_user",
					Fee:         "1000",
					Nonce:       nonce,
				},
				BaseToken:  baseToken,
				QuoteToken: quoteToken,
				Op:         pb.OrderOp_ADD,
				Side:       pb.OrderSide_BUY,          
				Price:      normalizeTestPrice(price),
				Amount:     normalizeTestAmount(amount),
			},
		},
	}
}

                                              
func splitPair(pair string) []string {
	                            
	tokens := make([]string, 2)
	for i := 0; i < len(pair); i++ {
		if pair[i] == '_' {
			tokens[0] = pair[:i]
			tokens[1] = pair[i+1:]
			break
		}
	}
	return tokens
}

                                                             
func generatePriceKey(price string) (string, error) {
	                                                                    
	                                                                        
	priceInt := 0
	fmt.Sscanf(price, "%d", &priceInt)
	return fmt.Sprintf("%067d", priceInt*1000000000000000000), nil
}

                                                              
func normalizeTestPrice(price string) string {
	v, ok := new(big.Int).SetString(price, 10)
	if !ok {
		return price
	}
	if v.Cmp(big.NewInt(100_000_000)) < 0 {
		v.Mul(v, big.NewInt(100_000_000))
	}
	return v.String()
}

func normalizeTestAmount(amount string) string {
	if v, ok := new(big.Int).SetString(amount, 10); ok {
		return v.String()
	}
	r, ok := new(big.Rat).SetString(amount)
	if !ok || r.Sign() < 0 || r.Denom().Cmp(big.NewInt(1)) != 0 {
		return amount
	}
	return r.Num().String()
}

func TestScanOrdersByPairs(t *testing.T) {
	db := NewMockDB()

	                                                           
	pairs := []string{"BTC_USDT", "ETH_USDT", "SOL_USDT"}
	for _, pair := range pairs {
		createTestOrders(t, db, pair, 5)
	}

	                     
	result, err := db.ScanOrdersByPairs(pairs)

	         
	require.NoError(t, err)
	assert.Equal(t, 3, len(result))

	for _, pair := range pairs {
		pairOrders := result[pair]
		assert.Equal(t, 5, len(pairOrders), "Pair %s should have 5 orders", pair)
		t.Logf("Pair %s: found %d orders", pair, len(pairOrders))
	}
}

                                                             
func TestBatchRebuild_DBScanOptimization(t *testing.T) {
	db := NewMockDB()

	                     
	pairs := []string{"BTC_USDT", "ETH_USDT", "SOL_USDT", "DOGE_USDT", "ADA_USDT"}
	ordersPerPair := 50

	for _, pair := range pairs {
		createTestOrders(t, db, pair, ordersPerPair)
	}

	                     
	result, err := db.ScanOrdersByPairs(pairs)
	require.NoError(t, err)

	totalOrders := 0
	for pair, orders := range result {
		totalOrders += len(orders)
		t.Logf("Pair %s: %d orders", pair, len(orders))
	}

	expectedTotal := len(pairs) * ordersPerPair
	assert.Equal(t, expectedTotal, totalOrders, "Total orders should match")

	t.Logf("DB Scan Optimization: scanned %d pairs with %d total orders in single operation",
		len(pairs), totalOrders)
}

                                                              
func TestBatchRebuild_Correctness(t *testing.T) {
	db := NewMockDB()

	                     
	createTestAccount(t, db, "test_user", "BTC", "10.0")
	createTestAccount(t, db, "test_user", "USDT", "100000.0")

	executor := createTestExecutor(db)

	pair := "BTC_USDT"

	                              
	prices := []string{"49000", "49500", "50000", "50500", "51000"}
	for i, price := range prices {
		order := &pb.OrderTx{
			Base: &pb.BaseMessage{
				TxId:        fmt.Sprintf("order_%d", i),
				FromAddress: fmt.Sprintf("user_%d", i),
			},
			BaseToken:  "BTC",
			QuoteToken: "USDT",
			Op:         pb.OrderOp_ADD,
			Side:       pb.OrderSide_SELL,
			Price:      price,
			Amount:     "1.0",
		}
		saveOrderToDB(t, db, order, pair)
	}

	                                          
	block := &pb.Block{
		BlockHash: "block1",
		Header: &pb.BlockHeader{
			PrevBlockHash: "",
			Height:        1,
		},
		Body: []*pb.AnyTx{
			createOrderAnyTx("new_order", "BTC", "USDT", "50000", "1", 0),
		},
	}

	         
	result, err := executor.BuildSpecResult(block)

	         
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Valid)

	t.Logf("Correctness test: rebuilt order book with %d price levels", len(prices))
}

                                                
func TestBatchRebuild_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		setupDB     func(*MockDB)
		block       *pb.Block
		expectValid bool
	}{
		{
			name: "no existing orders",
			setupDB: func(db *MockDB) {
				                                       
				createTestAccount(nil, db, "test_user", "BTC", "10.0")
				createTestAccount(nil, db, "test_user", "USDT", "100000.0")
			},
			block: &pb.Block{
				BlockHash: "block1",
				Header: &pb.BlockHeader{
					Height: 1,
				},
				Body: []*pb.AnyTx{
					createOrderAnyTx("order1", "BTC", "USDT", "50000", "1", 0),
				},
			},
			expectValid: true,
		},
		{
			name: "empty block",
			setupDB: func(db *MockDB) {
				                                    
				for i := 0; i < 5; i++ {
					order := &pb.OrderTx{
						Base: &pb.BaseMessage{
							TxId:        fmt.Sprintf("order_%d", i),
							FromAddress: fmt.Sprintf("user_%d", i),
						},
						BaseToken:  "BTC",
						QuoteToken: "USDT",
						Op:         pb.OrderOp_ADD,
						Side:       pb.OrderSide_SELL,
						Price:      fmt.Sprintf("%d", 50000+i*100),
						Amount:     "1.0",
					}
					saveOrderToDB(nil, db, order, "BTC_USDT")
				}
			},
			block: &pb.Block{
				BlockHash: "block1",
				Header: &pb.BlockHeader{
					Height: 1,
				},
				Body: []*pb.AnyTx{},
			},
			expectValid: true,
		},
		{
			name: "only non-ADD operations",
			setupDB: func(db *MockDB) {
				                              
				createTestAccount(nil, db, "user_0", "BTC", "10.0")
				for i := 0; i < 5; i++ {
					order := &pb.OrderTx{
						Base: &pb.BaseMessage{
							TxId:        fmt.Sprintf("order_%d", i),
							FromAddress: "user_0",                                    
						},
						BaseToken:  "BTC",
						QuoteToken: "USDT",
						Op:         pb.OrderOp_ADD,
						Side:       pb.OrderSide_SELL,
						Price:      fmt.Sprintf("%d", 50000+i*100),
						Amount:     "1.0",
					}
					saveOrderToDB(nil, db, order, "BTC_USDT")
				}
			},
			block: &pb.Block{
				BlockHash: "block1",
				Header: &pb.BlockHeader{
					Height: 1,
				},
				Body: []*pb.AnyTx{
					{
						Content: &pb.AnyTx_OrderTx{
							OrderTx: &pb.OrderTx{
								Base: &pb.BaseMessage{
									TxId:        "remove1",
									FromAddress: "user_0",                            
								},
								Op:         pb.OrderOp_REMOVE,
								OpTargetId: "order_0",                               
								BaseToken:  "BTC",
								QuoteToken: "USDT",
							},
						},
					},
				},
			},
			expectValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := NewMockDB()
			if tt.setupDB != nil {
				tt.setupDB(db)
			}

			executor := createTestExecutor(db)
			result, err := executor.BuildSpecResult(tt.block)

			require.NoError(t, err)
			assert.NotNil(t, result)

			if !result.Valid && tt.expectValid {
				t.Logf("Expected valid but got invalid, reason: %s", result.Reason)
			}
			assert.Equal(t, tt.expectValid, result.Valid, "Reason: %s", result.Reason)

			t.Logf("Edge case '%s': valid=%v, receipts=%d",
				tt.name, result.Valid, len(result.Receipts))
		})
	}
}
