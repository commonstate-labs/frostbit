package vm_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	bridgeasset "dex/frost/asset"
	frostchain "dex/frost/chain"
	"dex/frost/chain/btc"
	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"dex/vm"

	btcec "github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const testBTCTaprootAddressExternal = "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0"

                                                                                               
type btcSignedSample struct {
	template      *btc.BTCTemplate
	templateData  []byte
	templateHash  []byte
	inputSigs     [][]byte
	guardianSigs  [][]byte
	scriptPubkeys [][]byte
}

const btcTestFeeRateSatsPerVByte = uint64(25)

func externalBTCGuardian() (*btcec.PrivateKey, []byte) {
	p, _ := btcec.PrivKeyFromBytes([]byte{31})
	return p, schnorr.SerializePubKey(p.PubKey())
}

func buildBTCVaultKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	pk, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key failed: %v", err)
	}
	return pk
}

func buildBTCVaultStateData(t *testing.T, privKey *btcec.PrivateKey, vaultID uint32) []byte {
	t.Helper()
	vs := &pb.FrostVaultState{
		VaultId:          vaultID,
		Chain:            "btc",
		KeyEpoch:         1,
		GroupPubkey:      privKey.PubKey().SerializeCompressed(),
		SignAlgo:         pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		Status:           "ACTIVE",
		CommitteeMembers: []string{"coordinator"},
	}
	data, err := proto.Marshal(vs)
	if err != nil {
		t.Fatalf("marshal vault state failed: %v", err)
	}
	return data
}

func buildBTCSignedSample(t *testing.T, privKey *btcec.PrivateKey, vaultID uint32, withdrawID string, inputs []btc.TxInput, grossAmount uint64) *btcSignedSample {
	t.Helper()

	totalInput := uint64(0)
	for _, input := range inputs {
		totalInput += input.Amount
	}
	recipientScript, err := btc.AddressScript(testBTCTaprootAddressExternal)
	if err != nil {
		t.Fatalf("build BTC recipient script: %v", err)
	}
	var changeAddress string
	var changeScript []byte
	if totalInput > grossAmount {
		_, guardian := externalBTCGuardian()
		addr, err := utils.DualCustodyTaprootAddress(schnorr.SerializePubKey(privKey.PubKey()), guardian, &chaincfg.MainNetParams)
		if err != nil {
			t.Fatalf("build BTC change address: %v", err)
		}
		changeAddress = addr
		changeScript, err = btc.AddressScript(changeAddress)
		if err != nil {
			t.Fatalf("build BTC change script: %v", err)
		}
	}
	netAmounts, _, change, err := btc.ExpectedUserPaidWithdrawAmounts(
		totalInput,
		[]uint64{grossAmount},
		len(inputs),
		[][]byte{recipientScript},
		changeScript,
		btcTestFeeRateSatsPerVByte,
	)
	if err != nil {
		t.Fatalf("compute BTC user-paid fee/change: %v", err)
	}
	outputs := []btc.TxOutput{{Address: testBTCTaprootAddressExternal, Amount: netAmounts[0]}}
	if change > 0 {
		outputs = append(outputs, btc.TxOutput{Address: changeAddress, Amount: change})
	}
	template := &btc.BTCTemplate{
		Version:     2,
		LockTime:    0,
		Inputs:      inputs,
		Outputs:     outputs,
		VaultID:     vaultID,
		KeyEpoch:    1,
		WithdrawIDs: []string{withdrawID},
	}
	guardianPrivate, guardianPublic := externalBTCGuardian()
	scriptPubKey, leaf, control, err := utils.BuildDualCustodyTaproot(privKey.PubKey().SerializeCompressed(), guardianPublic)
	if err != nil {
		t.Fatal(err)
	}
	for i := range template.Inputs {
		template.Inputs[i].SpendScript, template.Inputs[i].ControlBlock = leaf, control
	}
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatalf("template json failed: %v", err)
	}

	scriptPubkeys := make([][]byte, len(inputs))
	for i := range scriptPubkeys {
		scriptPubkeys[i] = append([]byte(nil), scriptPubKey...)
	}

	sighashes, err := template.ComputeTaprootSighash(scriptPubkeys, btc.SighashDefault)
	if err != nil {
		t.Fatalf("compute sighashes failed: %v", err)
	}

	inputSigs := make([][]byte, len(sighashes))
	guardianSigs := make([][]byte, len(sighashes))
	for i, h := range sighashes {
		sig, err := schnorr.Sign(privKey, h)
		if err != nil {
			t.Fatalf("sign input %d failed: %v", i, err)
		}
		inputSigs[i] = sig.Serialize()
		guardSig, err := schnorr.Sign(guardianPrivate, h)
		if err != nil {
			t.Fatal(err)
		}
		guardianSigs[i] = guardSig.Serialize()
	}

	return &btcSignedSample{
		template:      template,
		templateData:  templateData,
		templateHash:  template.TemplateHash(),
		inputSigs:     inputSigs,
		guardianSigs:  guardianSigs,
		scriptPubkeys: scriptPubkeys,
	}
}

func applyMockWriteOps(db *MockDB, ops []vm.WriteOp) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, op := range ops {
		if op.Del {
			delete(db.data, op.Key)
		} else {
			db.data[op.Key] = append([]byte(nil), op.Value...)
		}
	}
}

func seedMockBTCInputs(t *testing.T, db *MockDB, vaultID uint32, sample *btcSignedSample) {
	t.Helper()
	db.mu.Lock()
	defer db.mu.Unlock()
	for i, input := range sample.template.Inputs {
		utxo := &pb.FrostUtxo{Txid: input.TxID, Vout: input.Vout, Amount: strconv.FormatUint(input.Amount, 10), VaultId: vaultID, ScriptPubkey: sample.scriptPubkeys[i]}
		data, err := proto.Marshal(utxo)
		if err != nil {
			t.Fatalf("marshal BTC UTXO: %v", err)
		}
		seq := uint64(i + 1)
		db.data[keys.KeyFrostBtcUtxo(vaultID, input.TxID, input.Vout)] = data
		db.data[keys.KeyFrostBtcUtxoFIFOIndex(vaultID, seq)] = data
		db.data[keys.KeyFrostBtcSpendableUtxoIndex(vaultID, seq)] = data
		db.data[keys.KeyFrostBtcUtxoSeqRef(vaultID, input.TxID, input.Vout)] = []byte(strconv.FormatUint(seq, 10))
	}
	db.data[keys.KeyFrostBtcUtxoFIFOHead(vaultID)] = []byte("1")
	db.data[keys.KeyFrostBtcUtxoFIFOSeq(vaultID)] = []byte(strconv.Itoa(len(sample.template.Inputs)))
}

func submitMockBTCClaim(t *testing.T, db *MockDB, asset, withdrawID string, seq uint64, sample *btcSignedSample) (string, []vm.WriteOp) {
	t.Helper()
	_, guardianPublic := externalBTCGuardian()
	cfgData, err := proto.Marshal(&pb.FrostVaultConfig{
		Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte, GuardianPubkey: guardianPublic,
	})
	if err != nil {
		t.Fatalf("marshal BTC vault config: %v", err)
	}
	db.mu.Lock()
	db.data[keys.KeyFrostVaultConfig("btc", 0)] = cfgData
	db.mu.Unlock()
	jobID := frostchain.ComputeWithdrawJobID("btc", asset, sample.template.VaultID, seq, sample.templateHash, sample.template.KeyEpoch)
	claim := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base:  &pb.BaseMessage{TxId: "claim_" + jobID, FromAddress: "coordinator", ExecutedHeight: 100},
		JobId: jobID, WithdrawIds: []string{withdrawID}, VaultId: sample.template.VaultID, KeyEpoch: sample.template.KeyEpoch,
		TemplateHash: sample.templateHash, Chain: "btc", Asset: asset, TemplateData: sample.templateData,
	}}}
	sv := vm.NewStateView(db.Get, db.Scan)
	ops, receipt, err := (&vm.FrostWithdrawClaimTxHandler{}).DryRun(claim, sv)
	if err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("BTC claim failed: err=%v receipt=%+v", err, receipt)
	}
	applyMockWriteOps(db, ops)
	return jobID, ops
}

                                            

type MockDB struct {
	mu              sync.RWMutex
	data            map[string][]byte
	pending         []func()
	stateSyncCalls  int
	stateSyncHeight uint64
	stateSyncOps    int
	stateSyncForce  bool
}

                                    
type MockSession struct {
	db *MockDB
}

func (s *MockSession) Get(key string) ([]byte, error) {
	return s.db.Get(key)
}

func (s *MockSession) ApplyStateUpdate(height uint64, updates []interface{}) ([]byte, error) {
	for _, u := range updates {
		type writeOpInterface interface {
			GetKey() string
			GetValue() []byte
			IsDel() bool
		}
		if op, ok := u.(writeOpInterface); ok {
			s.db.mu.Lock()
			if op.IsDel() {
				delete(s.db.data, op.GetKey())
			} else {
				s.db.data[op.GetKey()] = op.GetValue()
			}
			s.db.mu.Unlock()
		}
	}
	return []byte("mock_root"), nil
}

func (s *MockSession) Commit() error   { return nil }
func (s *MockSession) Rollback() error { return nil }
func (s *MockSession) Close() error    { return nil }

func (db *MockDB) NewSession() (iface.DBSession, error) {
	return &MockSession{db: db}, nil
}

func (db *MockDB) CommitRoot(height uint64, root []byte) {
	                                     
}

func NewMockDB() *MockDB {
	return &MockDB{
		data:    make(map[string][]byte),
		pending: make([]func(), 0),
	}
}

func (db *MockDB) Get(key string) ([]byte, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	val, exists := db.data[key]
	if !exists {
		return nil, nil
	}
	return val, nil
}

func (db *MockDB) GetKV(key string) ([]byte, error) {
	return db.Get(key)
}

func (db *MockDB) EnqueueSet(key, value string) {
	db.pending = append(db.pending, func() {
		db.mu.Lock()
		defer db.mu.Unlock()
		db.data[key] = []byte(value)
	})
}

func (db *MockDB) EnqueueDel(key string) {
	db.pending = append(db.pending, func() {
		db.mu.Lock()
		defer db.mu.Unlock()
		delete(db.data, key)
	})
}

func (db *MockDB) ForceFlush() error {
	for _, op := range db.pending {
		op()
	}
	db.pending = db.pending[:0]
	return nil
}

func (db *MockDB) SyncStateUpdates(height uint64, updates []interface{}) (int, error) {
	return db.syncStateUpdates(height, updates, false)
}

func (db *MockDB) SyncFinalizedStateUpdates(height uint64, updates []interface{}, forceCheckpoint bool) (int, error) {
	return db.syncStateUpdates(height, updates, forceCheckpoint)
}

func (db *MockDB) syncStateUpdates(height uint64, updates []interface{}, forceCheckpoint bool) (int, error) {
	type writeOpInterface interface {
		GetKey() string
		GetValue() []byte
		IsDel() bool
	}

	count := 0
	for _, u := range updates {
		op, ok := u.(writeOpInterface)
		if !ok || op == nil || op.GetKey() == "" {
			continue
		}
		count++
	}

	db.mu.Lock()
	db.stateSyncCalls++
	db.stateSyncHeight = height
	db.stateSyncOps = count
	db.stateSyncForce = forceCheckpoint
	db.mu.Unlock()
	return count, nil
}

func (db *MockDB) Scan(prefix string) (map[string][]byte, error) {
	result := make(map[string][]byte)

	db.mu.RLock()
	defer db.mu.RUnlock()
	for k, v := range db.data {
		if strings.HasPrefix(k, prefix) {
			valCopy := make([]byte, len(v))
			copy(valCopy, v)
			result[k] = valCopy
		}
	}
	return result, nil
}
func (db *MockDB) ScanKVWithLimit(prefix string, limit int) (map[string][]byte, error) {
	result := make(map[string][]byte)
	db.mu.RLock()
	defer db.mu.RUnlock()
	count := 0
	for k, v := range db.data {
		if strings.HasPrefix(k, prefix) {
			if limit > 0 && count >= limit {
				break
			}
			valCopy := make([]byte, len(v))
			copy(valCopy, v)
			result[k] = valCopy
			count++
		}
	}
	return result, nil
}

func (db *MockDB) ScanKVWithLimitReverse(prefix string, limit int) (map[string][]byte, error) {
	return db.ScanKVWithLimit(prefix, limit)
}

func (db *MockDB) ScanOrdersByPairs(pairs []string) (map[string]map[string][]byte, error) {
	result := make(map[string]map[string][]byte)

	db.mu.RLock()
	defer db.mu.RUnlock()

	for _, pair := range pairs {
		prefix := keys.KeyOrderPriceIndexGeneralPrefix(pair, false)
		pairMap := make(map[string][]byte)

		for k, v := range db.data {
			if strings.HasPrefix(k, prefix) {
				valCopy := make([]byte, len(v))
				copy(valCopy, v)
				pairMap[k] = valCopy
			}
		}
		result[pair] = pairMap
	}

	return result, nil
}

                                                                       
func (db *MockDB) GetFrostVaultState(key string) (*pb.FrostVaultState, error) {
	data, err := db.Get(key)
	if err != nil || data == nil {
		return nil, err
	}
	var state pb.FrostVaultState
	if err := proto.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

                                     

func TestBasicExecution(t *testing.T) {
	                           
	db := NewMockDB()

	                                                
	aliceAccount := &pb.Account{Address: "alice"}
	aliceData, _ := proto.Marshal(aliceAccount)
	db.data[keys.KeyAccount("alice")] = aliceData

	                     
	aliceBal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000",
			MinerLockedBalance: "0",
		},
	}
	aliceBalData, _ := proto.Marshal(aliceBal)
	db.data[keys.KeyBalance("alice", "token123")] = aliceBalData

	                                                   
	for _, addr := range []string{"alice", "bob"} {
		fbBal := &pb.TokenBalanceRecord{
			Balance: &pb.TokenBalance{Balance: "100000", MinerLockedBalance: "0"},
		}
		fbData, _ := proto.Marshal(fbBal)
		db.data[keys.KeyBalance(addr, "FB")] = fbData
	}

	bobAccount := &pb.Account{Address: "bob"}
	bobData, _ := proto.Marshal(bobAccount)
	db.data[keys.KeyAccount("bob")] = bobData

	bobBal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "500",
			MinerLockedBalance: "0",
		},
	}
	bobBalData, _ := proto.Marshal(bobBal)
	db.data[keys.KeyBalance("bob", "token123")] = bobBalData

	                  
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
					TxId:        "tx_001",
					FromAddress: "alice",
					Status:      pb.Status_PENDING,
					Fee:         "1000",
				},
				To:           "bob",
				TokenAddress: "token123",
				Amount:       "100",
			},
		},
	}

	                       
	block := &pb.Block{
		BlockHash: "block_001",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
		Body: []*pb.AnyTx{transferTx},
	}

	                  
	result, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatal("BuildSpecResult failed:", err)
	}

	if !result.Valid {
		t.Fatal("Block should be valid")
	}

	if len(result.Receipts) != 1 {
		t.Fatal("Should have 1 receipt")
	}

	                                                                     
	aliceBalKey := keys.KeyBalance("alice", "token123")
	balVal, _ := db.Get(aliceBalKey)
	var checkBal pb.TokenBalanceRecord
	proto.Unmarshal(balVal, &checkBal)
	if checkBal.Balance.Balance != "1000" {
		t.Fatal("Database should not change during BuildSpecResult")
	}

	               
	err = executor.CommitFinalizedBlock(block)
	if err != nil {
		t.Fatal("Commit failed:", err)
	}

	                     
	status, _ := executor.GetTransactionStatus("tx_001")
	if status != "SUCCEED" {
		t.Fatal("Transaction should succeed")
	}

	                           
	committed, blockID := executor.IsBlockCommitted(1)
	if !committed || blockID != "block_001" {
		t.Fatal("Block should be committed")
	}
}

func TestCacheEffectiveness(t *testing.T) {
	db := NewMockDB()

	                                                
	aliceAccount := &pb.Account{Address: "alice"}
	aliceData, _ := proto.Marshal(aliceAccount)
	db.data[keys.KeyAccount("alice")] = aliceData

	aliceBal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "1000",
			MinerLockedBalance: "0",
		},
	}
	aliceBalData, _ := proto.Marshal(aliceBal)
	db.data[keys.KeyBalance("alice", "token123")] = aliceBalData

	bobAccount := &pb.Account{Address: "bob"}
	bobData, _ := proto.Marshal(bobAccount)
	db.data[keys.KeyAccount("bob")] = bobData

	bobBal := &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{
			Balance:            "0",
			MinerLockedBalance: "0",
		},
	}
	bobBalData, _ := proto.Marshal(bobBal)
	db.data[keys.KeyBalance("bob", "token123")] = bobBalData

	registry := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(registry)

	cache := vm.NewSpecExecLRU(10)
	executor := vm.NewExecutor(db, registry, cache)

	                       
	transferTx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					TxId:        "tx_cache_001",
					FromAddress: "alice",
					Status:      pb.Status_PENDING,
				},
				To:           "bob",
				TokenAddress: "token123",
				Amount:       "100",
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_cache_test",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
		Body: []*pb.AnyTx{transferTx},
	}

	                  
	result1, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatal(err)
	}

	                                             
	result2, err := executor.BuildSpecResult(block)
	if err != nil {
		t.Fatal(err)
	}

	                     
	if result1.BlockID != result2.BlockID {
		t.Fatal("Cache returned different result")
	}
}

func TestInvalidTransaction(t *testing.T) {
	db := NewMockDB()
	                                                

	registry := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(registry)

	cache := vm.NewSpecExecLRU(10)
	executor := vm.NewExecutor(db, registry, cache)

	                                            
	transferTx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					TxId:        "tx_invalid_001",
					FromAddress: "alice_addr",
					Status:      pb.Status_PENDING,
				},
				To:           "bob",
				TokenAddress: "token123",
				Amount:       "200",
			},
		},
	}

	block := &pb.Block{
		BlockHash: "block_invalid",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        2,
		},
		Body: []*pb.AnyTx{transferTx},
	}

	            
	result, _ := executor.BuildSpecResult(block)

	                                                                                     
	                                           
	assert.True(t, result.Valid, "Block structure should be valid despite invalid tx execution")
	require.Equal(t, 1, len(result.Receipts))
	assert.Equal(t, "FAILED", result.Receipts[0].Status)
}

func TestConcurrentExecution(t *testing.T) {
	db := NewMockDB()
	registry := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(registry)

	cache := vm.NewSpecExecLRU(100)
	executor := vm.NewExecutor(db, registry, cache)

	                                                
	for i := 0; i < 10; i++ {
		userAddr := fmt.Sprintf("user%d", i)
		account := &pb.Account{Address: userAddr}
		accountData, _ := proto.Marshal(account)
		db.data[keys.KeyAccount(userAddr)] = accountData

		bal := &pb.TokenBalanceRecord{
			Balance: &pb.TokenBalance{
				Balance:            "1000",
				MinerLockedBalance: "0",
			},
		}
		balData, _ := proto.Marshal(bal)
		db.data[keys.KeyBalance(userAddr, "token123")] = balData
	}

	                           
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(blockNum int) {
			defer wg.Done()

			                             
			txs := make([]*pb.AnyTx, 0)
			for j := 0; j < 5; j++ {
				from := fmt.Sprintf("user%d", j)
				to := fmt.Sprintf("user%d", (j+1)%10)

				tx := &pb.AnyTx{
					Content: &pb.AnyTx_Transaction{
						Transaction: &pb.Transaction{
							Base: &pb.BaseMessage{
								TxId:        fmt.Sprintf("tx_%d_%d", blockNum, j),
								FromAddress: from,
								Status:      pb.Status_PENDING,
							},
							To:           to,
							TokenAddress: "token123",
							Amount:       "10",
						},
					},
				}
				txs = append(txs, tx)
			}

			block := &pb.Block{
				BlockHash: fmt.Sprintf("block_%d", blockNum),
				Header: &pb.BlockHeader{
					PrevBlockHash: "genesis",
					Height:        uint64(blockNum + 1),
				},
				Body: txs,
			}

			            
			result, err := executor.BuildSpecResult(block)
			if err != nil {
				t.Errorf("Block %d execution error: %v", blockNum, err)
				return
			}

			if !result.Valid {
				t.Errorf("Block %d should be valid", blockNum)
			}
		}(i)
	}

	wg.Wait()
}

func TestStateViewSnapshot(t *testing.T) {
	                  
	readFn := func(key string) ([]byte, error) {
		if key == "test_key" {
			return []byte("initial_value"), nil
		}
		return nil, nil
	}

	scanFn := func(prefix string) (map[string][]byte, error) {
		return make(map[string][]byte), nil
	}

	sv := vm.NewStateView(readFn, scanFn)

	               
	val, exists, _ := sv.Get("test_key")
	if !exists || string(val) != "initial_value" {
		t.Fatal("Initial value incorrect")
	}

	               
	snap1 := sv.Snapshot()

	               
	sv.Set("test_key", []byte("modified_value"))
	sv.Set("new_key", []byte("new_value"))

	               
	val, exists, _ = sv.Get("test_key")
	if string(val) != "modified_value" {
		t.Fatal("Modified value incorrect")
	}

	                  
	err := sv.Revert(snap1)
	if err != nil {
		t.Fatal("Revert failed:", err)
	}

	               
	val, exists, _ = sv.Get("test_key")
	if !exists || string(val) != "initial_value" {
		t.Fatal("Reverted value incorrect")
	}

	val, exists, _ = sv.Get("new_key")
	if exists {
		t.Fatal("New key should not exist after revert")
	}
}

func TestLRUCache(t *testing.T) {
	cache := vm.NewSpecExecLRU(3)

	               
	for i := 0; i < 5; i++ {
		cache.Put(&vm.SpecResult{
			BlockID: fmt.Sprintf("block_%d", i),
			Height:  uint64(i),
			Valid:   true,
		})
	}

	                                 
	if _, ok := cache.Get("block_0"); ok {
		t.Fatal("block_0 should be evicted")
	}

	if _, ok := cache.Get("block_1"); ok {
		t.Fatal("block_1 should be evicted")
	}

	                              
	if _, ok := cache.Get("block_4"); !ok {
		t.Fatal("block_4 should be in cache")
	}

	                   
	cache.EvictBelow(4)

	if _, ok := cache.Get("block_2"); ok {
		t.Fatal("block_2 should be evicted")
	}

	if _, ok := cache.Get("block_3"); ok {
		t.Fatal("block_3 should be evicted")
	}

	if _, ok := cache.Get("block_4"); !ok {
		t.Fatal("block_4 should remain in cache")
	}
}

func BenchmarkBuildSpecResult(b *testing.B) {
	db := NewMockDB()
	registry := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(registry)
	cache := vm.NewSpecExecLRU(1000)
	executor := vm.NewExecutor(db, registry, cache)

	                                       
	for i := 0; i < 100; i++ {
		userAddr := fmt.Sprintf("user%d", i)
		account := &pb.Account{Address: userAddr}
		accountData, _ := proto.Marshal(account)
		db.data[keys.KeyAccount(userAddr)] = accountData

		bal := &pb.TokenBalanceRecord{
			Balance: &pb.TokenBalance{
				Balance:            "10000",
				MinerLockedBalance: "0",
			},
		}
		balData, _ := proto.Marshal(bal)
		db.data[keys.KeyBalance(userAddr, "token123")] = balData
	}

	                     
	txs := make([]*pb.AnyTx, 100)
	for i := 0; i < 100; i++ {
		txs[i] = &pb.AnyTx{
			Content: &pb.AnyTx_Transaction{
				Transaction: &pb.Transaction{
					Base: &pb.BaseMessage{
						TxId:        fmt.Sprintf("tx_%d", i),
						FromAddress: fmt.Sprintf("user%d", i%100),
						Status:      pb.Status_PENDING,
					},
					To:           fmt.Sprintf("user%d", (i+1)%100),
					TokenAddress: "token123",
					Amount:       "1",
				},
			},
		}
	}

	block := &pb.Block{
		BlockHash: "bench_block",
		Header: &pb.BlockHeader{
			PrevBlockHash: "genesis",
			Height:        1,
		},
		Body: txs,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		executor.BuildSpecResult(block)
	}
}

                                                                  
func TestFrostKind(t *testing.T) {
	                                
	frostWithdrawRequestTx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawRequestTx{
			FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
				Base: &pb.BaseMessage{
					TxId:        "test-frost-withdraw-request-1",
					FromAddress: "alice",
				},
				Chain:  "BTC",
				Asset:  "native",
				To:     "bc1qtest...",
				Amount: "100000",
			},
		},
	}

	kind, err := vm.DefaultKindFn(frostWithdrawRequestTx)
	assert.NoError(t, err)
	assert.Equal(t, "frost_withdraw_request", kind)

	                               
	frostWithdrawSignedTx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:        "test-frost-withdraw-signed-1",
					FromAddress: "runtime",
				},
				JobId:              "job-123",
				SignedPackageBytes: []byte("signed-package"),
			},
		},
	}

	kind, err = vm.DefaultKindFn(frostWithdrawSignedTx)
	assert.NoError(t, err)
	assert.Equal(t, "frost_withdraw_signed", kind)

	                        
	registry := vm.NewHandlerRegistry()
	err = vm.RegisterDefaultHandlers(registry)
	assert.NoError(t, err)

	                                 
	h1, ok := registry.Get("frost_withdraw_request")
	assert.True(t, ok)
	assert.Equal(t, "frost_withdraw_request", h1.Kind())

	h2, ok := registry.Get("frost_withdraw_signed")
	assert.True(t, ok)
	assert.Equal(t, "frost_withdraw_signed", h2.Kind())
}

                                                               
func TestFrostWithdrawRequest(t *testing.T) {
	                   
	store := make(map[string][]byte)
	readFn := func(key string) ([]byte, error) {
		if v, ok := store[key]; ok {
			return v, nil
		}
		return nil, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		return make(map[string][]byte), nil
	}

	                                                     
	asset, err := bridgeasset.Resolve("btc", "")
	require.NoError(t, err)
	tokenData, _ := proto.Marshal(&pb.Token{
		Address:     asset.ID,
		TotalSupply: "999999999",
		Owner:       "system",
		Type:        pb.TokenType_TOKEN_BRIDGED,
		NativeChain: asset.Chain,
	})
	store[keys.KeyToken(asset.ID)] = tokenData
	store[keys.KeyNativeTokenIndex(asset.Chain, asset.Contract)] = []byte(asset.ID)
	vaultConfigData, _ := proto.Marshal(&pb.FrostVaultConfig{
		Chain:              "btc",
		SignAlgo:           pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		VaultCount:         1,
		BtcNetwork:         "mainnet",
		BtcFeeSatsPerVbyte: btcTestFeeRateSatsPerVByte,
	})
	store[keys.KeyFrostVaultConfig("btc", 0)] = vaultConfigData
	withdrawKey := buildBTCVaultKey(t)
	withdrawAddress, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(withdrawKey.PubKey()), &chaincfg.MainNetParams)
	require.NoError(t, err)
	for _, addr := range []string{"alice", "bob"} {
		bal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "999999999"}}
		balData, _ := proto.Marshal(bal)
		store[keys.KeyBalance(addr, asset.ID)] = balData
	}

	handler := &vm.FrostWithdrawRequestTxHandler{}

	                                                         
	sv := vm.NewStateView(readFn, scanFn)
	tx1 := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawRequestTx{
			FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_001",
					FromAddress:    "alice",
					ExecutedHeight: 100,
				},
				Chain:  "BTC",
				Asset:  asset.ID,
				To:     withdrawAddress.EncodeAddress(),
				Amount: "100000",
			},
		},
	}

	ops1, receipt1, err1 := handler.DryRun(tx1, sv)
	assert.NoError(t, err1)
	assert.Equal(t, "SUCCEED", receipt1.Status)
	assert.True(t, len(ops1) >= 5)                                                  

	                        
	for _, op := range ops1 {
		if op.Del {
			delete(store, op.Key)
		} else {
			store[op.Key] = op.Value
		}
	}

	                                                          
	sv2 := vm.NewStateView(readFn, scanFn)
	tx2 := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawRequestTx{
			FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_002",
					FromAddress:    "bob",
					ExecutedHeight: 101,
				},
				Chain:  "BTC",
				Asset:  asset.ID,
				To:     withdrawAddress.EncodeAddress(),
				Amount: "200000",
			},
		},
	}

	ops2, receipt2, err2 := handler.DryRun(tx2, sv2)
	assert.NoError(t, err2)
	assert.Equal(t, "SUCCEED", receipt2.Status)
	assert.True(t, len(ops2) >= 5)

	                        
	for _, op := range ops2 {
		if op.Del {
			delete(store, op.Key)
		} else {
			store[op.Key] = op.Value
		}
	}

	                                                              
	seqKey := keys.KeyFrostWithdrawFIFOSeq("btc", asset.ID)
	seqData := store[seqKey]
	assert.Equal(t, "2", string(seqData))

	                                                   
	sv3 := vm.NewStateView(readFn, scanFn)
	ops3, receipt3, err3 := handler.DryRun(tx1, sv3)                 
	assert.NoError(t, err3)
	assert.Equal(t, "SUCCEED", receipt3.Status)
	assert.Equal(t, 0, receipt3.WriteCount)                      

	                  
	assert.Nil(t, ops3)
	seqData2 := store[seqKey]
	assert.Equal(t, "2", string(seqData2))
}

                                                             
func TestFrostWithdrawSigned(t *testing.T) {
	store := make(map[string][]byte)
	readFn := func(key string) ([]byte, error) {
		if v, ok := store[key]; ok {
			return v, nil
		}
		return nil, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		return make(map[string][]byte), nil
	}

	                                                                              
	asset, err := bridgeasset.Resolve("eth", "0x00000000000000000000000000000000000000cc")
	require.NoError(t, err)
	tokenData, _ := proto.Marshal(&pb.Token{
		Address:        asset.ID,
		TotalSupply:    "999999999",
		Owner:          "system",
		Type:           pb.TokenType_TOKEN_BRIDGED,
		NativeChain:    asset.Chain,
		NativeContract: asset.Contract,
	})
	store[keys.KeyToken(asset.ID)] = tokenData
	store[keys.KeyNativeTokenIndex(asset.Chain, asset.Contract)] = []byte(asset.ID)
	vaultConfigData, _ := proto.Marshal(&pb.FrostVaultConfig{
		Chain:      "eth",
		SignAlgo:   pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		VaultCount: 1,
		VaultRefs:  []string{"0x00000000000000000000000000000000000000aa"},
		EvmChainId: 1,
	})
	store[keys.KeyFrostVaultConfig("eth", 0)] = vaultConfigData
	vaultStateData, _ := proto.Marshal(&pb.FrostVaultState{
		Chain: "eth", VaultId: 0, KeyEpoch: 1, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		Status: "ACTIVE",
	})
	store[keys.KeyFrostVaultState("eth", 0)] = vaultStateData
	store[keys.KeyFrostVaultAvailableBalance("eth", asset.ID, 0)] = []byte("999999999")
	bal := &pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "999999999"}}
	balData, _ := proto.Marshal(bal)
	store[keys.KeyBalance("alice", asset.ID)] = balData

	requestHandler := &vm.FrostWithdrawRequestTxHandler{}
	signedHandler := &vm.FrostWithdrawSignedTxHandler{}

	                                   
	sv1 := vm.NewStateView(readFn, scanFn)
	tx1 := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawRequestTx{
			FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_req_001",
					FromAddress:    "alice",
					ExecutedHeight: 100,
				},
				Chain:  "ETH",
				Asset:  asset.ID,
				To:     "0xde709f2102306220921060314715629080e2fb77",
				Amount: "1000000",
			},
		},
	}

	ops1, _, _ := requestHandler.DryRun(tx1, sv1)
	for _, op := range ops1 {
		if op.Del {
			delete(store, op.Key)
		} else {
			store[op.Key] = op.Value
		}
	}

	                                            
	txRefKey := keys.KeyFrostWithdrawTxRef("tx_req_001")
	withdrawID := string(store[txRefKey])
	assert.NotEmpty(t, withdrawID)

	                                                                                            
	chainForSig := "eth"
	tplHash, jobID := vm.SeedAccountChainClaimStubForTest(t, store, chainForSig, asset.ID, 0, []string{withdrawID})
	var claimedJob pb.FrostWithdrawJobState
	require.NoError(t, proto.Unmarshal(store[keys.KeyFrostWithdrawJob(jobID)], &claimedJob))

	                     
	sv2 := vm.NewStateView(readFn, scanFn)
	signedTx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_signed_001",
					FromAddress:    "coordinator",
					ExecutedHeight: 110,
				},
				JobId:              jobID,
				SignedPackageBytes: vm.SignAccountClaimForTest(t, store, chainForSig, asset.ID, 0, []string{withdrawID}),
				GuardianSigs:       vm.AccountGuardianClaimForTest(t, store, chainForSig, asset.ID, 0, []string{withdrawID}),
				WithdrawIds:        []string{withdrawID},
				VaultId:            0,
				KeyEpoch:           1,
				Chain:              chainForSig,
				Asset:              "",
				TemplateHash:       tplHash,
				TemplateData:       claimedJob.TemplateData,
				BatchSeq:           claimedJob.BatchSeq,
			},
		},
	}

	ops2, receipt2, err2 := signedHandler.DryRun(signedTx, sv2)
	assert.NoError(t, err2)
	assert.Equal(t, "SUCCEED", receipt2.Status)
	assert.Empty(t, ops2)                                               
	ops2 = sv2.Diff()
	assert.True(t, len(ops2) >= 3)

	               
	for _, op := range ops2 {
		if op.Del {
			delete(store, op.Key)
		} else {
			store[op.Key] = op.Value
		}
	}

	                                      
	withdrawKey := keys.KeyFrostWithdraw(withdrawID)
	assert.Empty(t, store[withdrawKey])
	archivedWithdrawData := store[keys.KeyArchiveFrostWithdraw(withdrawID)]
	assert.NotEmpty(t, archivedWithdrawData)

	                                                                                  
	sv3 := vm.NewStateView(readFn, scanFn)
	signedTx2 := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "tx_signed_002",
					FromAddress:    "coordinator2",
					ExecutedHeight: 111,
				},
				JobId:              jobID,                 
				SignedPackageBytes: []byte("signed_tx_data_v2"),
				WithdrawIds:        []string{withdrawID},
				VaultId:            0,
				Chain:              chainForSig,
				Asset:              "",
				KeyEpoch:           1,
				TemplateHash:       claimedJob.TemplateHash,
				TemplateData:       claimedJob.TemplateData,
				BatchSeq:           claimedJob.BatchSeq,
			},
		},
	}

	signedTx2.GetFrostWithdrawSignedTx().SignedPackageBytes = signedTx.GetFrostWithdrawSignedTx().SignedPackageBytes
	signedTx2.GetFrostWithdrawSignedTx().GuardianSigs = signedTx.GetFrostWithdrawSignedTx().GuardianSigs
	ops3, receipt3, err3 := signedHandler.DryRun(signedTx2, sv3)
	assert.Error(t, err3)
	assert.Equal(t, "FAILED", receipt3.Status)
	assert.Contains(t, receipt3.Error, "withdraw not found")
	assert.Empty(t, ops3)

}

                                                 
func TestFrostFundsLedger(t *testing.T) {
	store := make(map[string][]byte)
	readFn := func(key string) ([]byte, error) {
		if v, ok := store[key]; ok {
			return v, nil
		}
		return nil, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for key, value := range store {
			if strings.HasPrefix(key, prefix) {
				out[key] = append([]byte(nil), value...)
			}
		}
		return out, nil
	}

	chain := "ETH"
	asset := "native"
	vaultID := uint32(0)

	                                  
	sv := vm.NewStateView(readFn, scanFn)
	head := vm.GetFundsLotHead(sv, chain, asset, vaultID)
	assert.Equal(t, uint64(0), head.Height)
	assert.Equal(t, uint64(0), head.Seq)

	                                                         
	                    
	seqKey1 := keys.KeyFrostFundsLotSeq(chain, asset, vaultID, 100)
	store[seqKey1] = []byte("2")             
	lotKey1 := keys.KeyFrostFundsLotIndex(chain, asset, vaultID, 100, 1)
	store[lotKey1] = []byte("request_001")
	lotKey2 := keys.KeyFrostFundsLotIndex(chain, asset, vaultID, 100, 2)
	store[lotKey2] = []byte("request_002")

	                    
	seqKey2 := keys.KeyFrostFundsLotSeq(chain, asset, vaultID, 102)
	store[seqKey2] = []byte("1")
	lotKey3 := keys.KeyFrostFundsLotIndex(chain, asset, vaultID, 102, 1)
	store[lotKey3] = []byte("request_003")

	                        
	vm.SetFundsLotHead(sv, &vm.FundsLotHead{
		Chain:   chain,
		Asset:   asset,
		VaultID: vaultID,
		Height:  100,
		Seq:     1,
	})
	                  
	for _, op := range sv.Diff() {
		if op.Del {
			delete(store, op.Key)
		} else {
			store[op.Key] = op.Value
		}
	}

	                         
	sv2 := vm.NewStateView(readFn, scanFn)
	requestID, ok := vm.GetFundsLotAtHead(sv2, chain, asset, vaultID)
	assert.True(t, ok)
	assert.Equal(t, "request_001", requestID)

	                      
	sv3 := vm.NewStateView(readFn, scanFn)
	consumedID, consumed := vm.ConsumeFundsLot(sv3, chain, asset, vaultID)
	assert.True(t, consumed)
	assert.Equal(t, "request_001", consumedID)

	                        
	for _, op := range sv3.Diff() {
		if op.Del {
			delete(store, op.Key)
		} else {
			store[op.Key] = op.Value
		}
	}

	                                       
	_, lot1StillHot := store[lotKey1]
	assert.False(t, lot1StillHot)

	sv4 := vm.NewStateView(readFn, scanFn)
	head2 := vm.GetFundsLotHead(sv4, chain, asset, vaultID)
	assert.Equal(t, uint64(100), head2.Height)
	assert.Equal(t, uint64(2), head2.Seq)

	                      
	requestID2, ok2 := vm.GetFundsLotAtHead(sv4, chain, asset, vaultID)
	assert.True(t, ok2)
	assert.Equal(t, "request_002", requestID2)

	                      
	sv5 := vm.NewStateView(readFn, scanFn)
	consumedID2, consumed2 := vm.ConsumeFundsLot(sv5, chain, asset, vaultID)
	assert.True(t, consumed2)
	assert.Equal(t, "request_002", consumedID2)

	                        
	for _, op := range sv5.Diff() {
		if op.Del {
			delete(store, op.Key)
		} else {
			store[op.Key] = op.Value
		}
	}

	                                       
	_, lot2StillHot := store[lotKey2]
	assert.False(t, lot2StillHot)

	sv6 := vm.NewStateView(readFn, scanFn)
	head3 := vm.GetFundsLotHead(sv6, chain, asset, vaultID)
	assert.Equal(t, uint64(102), head3.Height)
	assert.Equal(t, uint64(1), head3.Seq)

	                      
	requestID3, ok3 := vm.GetFundsLotAtHead(sv6, chain, asset, vaultID)
	assert.True(t, ok3)
	assert.Equal(t, "request_003", requestID3)
}

                                                      

                                                                
func createTestBTCTemplateData(inputs []struct {
	TxID   string
	Vout   uint32
	Amount uint64
}, outputAmount uint64, withdrawID string) []byte {
	                            
	template := map[string]interface{}{
		"version":   2,
		"lock_time": 0,
		"vault_id":  1,
		"key_epoch": 1,
		"inputs":    make([]map[string]interface{}, len(inputs)),
		"outputs": []map[string]interface{}{
			{"address": "bc1qtest", "amount": outputAmount},
		},
		"withdraw_ids": []string{withdrawID},
	}
	for i, in := range inputs {
		template["inputs"].([]map[string]interface{})[i] = map[string]interface{}{
			"txid":     in.TxID,
			"vout":     in.Vout,
			"amount":   in.Amount,
			"sequence": 0xffffffff,
		}
	}
	data, _ := json.Marshal(template)
	return data
}

                                                                      
func TestFrostWithdrawSignedBTC_UTXOLock(t *testing.T) {
	db := NewMockDB()

	readFn := func(key string) ([]byte, error) {
		return db.Get(key)
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		return db.Scan(prefix)
	}

	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	withdrawID := "test_withdraw_btc_001"
	chain := "btc"
	asset := "native"
	vaultID := uint32(1)

	withdrawKey := keys.KeyFrostWithdraw(withdrawID)
	withdrawState := &pb.FrostWithdrawState{
		WithdrawId:    withdrawID,
		TxId:          "orig_tx_001",
		Seq:           1,
		Chain:         chain,
		Asset:         asset,
		To:            testBTCTaprootAddressExternal,
		Amount:        "20000",
		RequestHeight: 100,
		Status:        "QUEUED",
	}
	withdrawData, _ := proto.Marshal(withdrawState)

	privKey := buildBTCVaultKey(t)
	vaultStateData := buildBTCVaultStateData(t, privKey, vaultID)

	db.mu.Lock()
	db.data[withdrawKey] = withdrawData
	db.data[keys.KeyFrostVaultAvailableBalance("btc", "native", vaultID)] = []byte("100000")
	db.data[keys.KeyFrostVaultState("btc", vaultID)] = vaultStateData
	db.mu.Unlock()

	inputs := []btc.TxInput{
		{TxID: strings.Repeat("a1", 32), Vout: 0, Amount: 50000, Sequence: btc.DefaultSequence},
		{TxID: strings.Repeat("a2", 32), Vout: 1, Amount: 30000, Sequence: btc.DefaultSequence},
	}
	sample := buildBTCSignedSample(t, privKey, vaultID, withdrawID, inputs, 20000)
	seedMockBTCInputs(t, db, vaultID, sample)
	jobID, claimOps := submitMockBTCClaim(t, db, asset, withdrawID, 1, sample)
	signedTx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "signed_tx_001",
					ExecutedHeight: 101,
					FromAddress:    "signer_001",
				},
				JobId:              jobID,
				SignedPackageBytes: bytes.Join(sample.inputSigs, nil),
				WithdrawIds:        []string{withdrawID},
				VaultId:            vaultID,
				KeyEpoch:           1,
				Chain:              chain,
				Asset:              asset,
				TemplateData:       sample.templateData,
				TemplateHash:       sample.templateHash,
				InputSigs:          sample.inputSigs,
				GuardianSigs:       sample.guardianSigs,
				ScriptPubkeys:      sample.scriptPubkeys,
			},
		},
	}

	sv := vm.NewStateView(readFn, scanFn)
	handler, _ := reg.Get("frost_withdraw_signed")
	ops, receipt, err := handler.DryRun(signedTx, sv)

	assert.NoError(t, err)
	assert.Equal(t, "SUCCEED", receipt.Status)
	assert.True(t, len(ops) > 0)

	hasUtxoLock := false
	for _, op := range claimOps {
		if strings.Contains(op.Key, "frost_btc_locked_utxo") {
			hasUtxoLock = true
			assert.Equal(t, jobID, string(op.Value))
		}
	}
	assert.True(t, hasUtxoLock, "should have UTXO lock ops")

	applyMockWriteOps(db, ops)

	                                                                                     
	withdrawID2 := "test_withdraw_btc_002"
	withdrawKey2 := keys.KeyFrostWithdraw(withdrawID2)
	withdrawState2 := &pb.FrostWithdrawState{
		WithdrawId:    withdrawID2,
		TxId:          "orig_tx_002",
		Seq:           2,
		Chain:         chain,
		Asset:         asset,
		To:            testBTCTaprootAddressExternal,
		Amount:        "20000",
		RequestHeight: 100,
		Status:        "QUEUED",
	}
	withdrawData2, _ := proto.Marshal(withdrawState2)
	db.mu.Lock()
	db.data[withdrawKey2] = withdrawData2
	db.data[keys.KeyFrostWithdrawFIFOHead(chain, asset)] = []byte("2")
	db.mu.Unlock()

	inputs2 := []btc.TxInput{
		{TxID: inputs[0].TxID, Vout: inputs[0].Vout, Amount: inputs[0].Amount, Sequence: btc.DefaultSequence},
	}
	sample2 := buildBTCSignedSample(t, privKey, vaultID, withdrawID2, inputs2, 20000)
	jobID2 := frostchain.ComputeWithdrawJobID(chain, asset, vaultID, 2, sample2.templateHash, 1)
	claimTx2 := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: &pb.FrostWithdrawClaimTx{
		Base:  &pb.BaseMessage{TxId: "claim_btc_002", FromAddress: "coordinator", ExecutedHeight: 102},
		JobId: jobID2, WithdrawIds: []string{withdrawID2}, VaultId: vaultID, KeyEpoch: 1,
		TemplateHash: sample2.templateHash, Chain: chain, Asset: asset, TemplateData: sample2.templateData,
	}}}

	sv2 := vm.NewStateView(readFn, scanFn)
	_, receipt2, err2 := (&vm.FrostWithdrawClaimTxHandler{}).DryRun(claimTx2, sv2)

	assert.Error(t, err2)
	assert.Equal(t, "FAILED", receipt2.Status)
	assert.Contains(t, receipt2.Error, "BTC input already locked")
}

                                                                                                           
func TestFrostWithdrawSignedBTC_SameJobResubmitRejected(t *testing.T) {
	db := NewMockDB()

	readFn := func(key string) ([]byte, error) {
		return db.Get(key)
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		return db.Scan(prefix)
	}

	reg := vm.NewHandlerRegistry()
	vm.RegisterDefaultHandlers(reg)

	withdrawID := "test_withdraw_btc_002"
	chain := "btc"
	asset := "native"
	jobID := "job_btc_same_001"
	vaultID := uint32(1)

	withdrawKey := keys.KeyFrostWithdraw(withdrawID)
	withdrawState := &pb.FrostWithdrawState{
		WithdrawId: withdrawID,
		Seq:        1,
		Chain:      chain,
		Asset:      asset,
		To:         testBTCTaprootAddressExternal,
		Amount:     "9000",
		Status:     "QUEUED",
		VaultId:    vaultID,
	}
	withdrawData, _ := proto.Marshal(withdrawState)

	privKey := buildBTCVaultKey(t)
	vaultStateData := buildBTCVaultStateData(t, privKey, vaultID)

	db.mu.Lock()
	db.data[withdrawKey] = withdrawData
	db.data[keys.KeyFrostVaultAvailableBalance("btc", "native", vaultID)] = []byte("100000")
	db.data[keys.KeyFrostVaultState("btc", vaultID)] = vaultStateData
	db.mu.Unlock()

	inputs := []btc.TxInput{{TxID: strings.Repeat("b0", 32), Vout: 0, Amount: 11080, Sequence: btc.DefaultSequence}}
	sample := buildBTCSignedSample(t, privKey, vaultID, withdrawID, inputs, 9000)
	seedMockBTCInputs(t, db, vaultID, sample)
	jobID, _ = submitMockBTCClaim(t, db, asset, withdrawID, 1, sample)

	signedTx := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "signed_tx_same_001",
					ExecutedHeight: 101,
					FromAddress:    "signer",
				},
				JobId:              jobID,
				SignedPackageBytes: bytes.Join(sample.inputSigs, nil),
				WithdrawIds:        []string{withdrawID},
				VaultId:            vaultID,
				KeyEpoch:           1,
				Chain:              chain,
				Asset:              asset,
				TemplateData:       sample.templateData,
				TemplateHash:       sample.templateHash,
				InputSigs:          sample.inputSigs,
				GuardianSigs:       sample.guardianSigs,
				ScriptPubkeys:      sample.scriptPubkeys,
			},
		},
	}

	sv1 := vm.NewStateView(readFn, scanFn)
	handler, _ := reg.Get("frost_withdraw_signed")
	ops1, receipt1, err1 := handler.DryRun(signedTx, sv1)

	assert.NoError(t, err1)
	assert.Equal(t, "SUCCEED", receipt1.Status)

	applyMockWriteOps(db, ops1)

	signedTx2 := &pb.AnyTx{
		Content: &pb.AnyTx_FrostWithdrawSignedTx{
			FrostWithdrawSignedTx: &pb.FrostWithdrawSignedTx{
				Base: &pb.BaseMessage{
					TxId:           "signed_tx_same_002",
					ExecutedHeight: 102,
					FromAddress:    "signer2",
				},
				JobId:              jobID,
				SignedPackageBytes: bytes.Join(sample.inputSigs, nil),
				WithdrawIds:        []string{withdrawID},
				VaultId:            vaultID,
				KeyEpoch:           1,
				Chain:              chain,
				Asset:              asset,
				TemplateData:       sample.templateData,
				TemplateHash:       sample.templateHash,
				InputSigs:          sample.inputSigs,
				GuardianSigs:       sample.guardianSigs,
				ScriptPubkeys:      sample.scriptPubkeys,
			},
		},
	}

	sv2 := vm.NewStateView(readFn, scanFn)
	ops2, receipt2, err2 := handler.DryRun(signedTx2, sv2)

	assert.Error(t, err2)
	assert.Equal(t, "FAILED", receipt2.Status)
	assert.Contains(t, receipt2.Error, "withdraw not found")
	assert.Empty(t, ops2)
}
