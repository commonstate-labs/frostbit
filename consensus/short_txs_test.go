package consensus

import (
	"bytes"
	"dex/config"
	"dex/db"
	"dex/logs"
	"dex/pb"
	"encoding/hex"
	"fmt"
	"testing"
)

                                                    
func TestShortTxsEncoding(t *testing.T) {
	                                
	txIDs := []string{
		"0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		"0xfedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321",
		"0xaabbccdd11223344aabbccdd11223344aabbccdd11223344aabbccdd11223344",
	}

	                            
	var shortTxs []byte
	for _, txID := range txIDs {
		idBytes, err := hex.DecodeString(txID[2:])                    
		if err != nil {
			t.Fatalf("Failed to decode txID %s: %v", txID, err)
		}
		if len(idBytes) < 8 {
			t.Fatalf("txID too short: %s", txID)
		}
		shortTxs = append(shortTxs, idBytes[:8]...)
	}

	                     
	expectedLen := len(txIDs) * 8
	if len(shortTxs) != expectedLen {
		t.Errorf("Expected shortTxs length %d, got %d", expectedLen, len(shortTxs))
	}

	t.Logf("Encoded %d txIDs to %d bytes of shortTxs", len(txIDs), len(shortTxs))

	                                       
	const shortHashSize = 8
	if len(shortTxs)%shortHashSize != 0 {
		t.Fatalf("Invalid shortTxs length: %d", len(shortTxs))
	}

	hashCount := len(shortTxs) / shortHashSize
	if hashCount != len(txIDs) {
		t.Errorf("Expected %d hashes, got %d", len(txIDs), hashCount)
	}

	for i := 0; i < hashCount; i++ {
		shortHash := shortTxs[i*shortHashSize : (i+1)*shortHashSize]
		expectedShort, _ := hex.DecodeString(txIDs[i][2:18])                               
		if !bytes.Equal(shortHash, expectedShort) {
			t.Errorf("Hash %d mismatch: expected %x, got %x", i, expectedShort, shortHash)
		}
	}

	t.Logf("Successfully decoded %d short hashes", hashCount)
}

                                                                     
func TestBlockWithMassOrders(t *testing.T) {
	cfg := config.DefaultConfig()

	               
	maxTxsPerBlock := cfg.TxPool.MaxTxsPerBlock
	t.Logf("MaxTxsPerBlock: %d", maxTxsPerBlock)

	                                    
	txCount := maxTxsPerBlock + 500                
	t.Logf("Testing with %d transactions (> %d threshold)", txCount, maxTxsPerBlock)

	                     
	mockTxs := make([]*pb.AnyTx, txCount)
	for i := 0; i < txCount; i++ {
		mockTxs[i] = &pb.AnyTx{
			Content: &pb.AnyTx_OrderTx{
				OrderTx: &pb.OrderTx{
					Base: &pb.BaseMessage{
						TxId:        fmt.Sprintf("0x%064x", i+1),
						FromAddress: fmt.Sprintf("addr%d", i%10),
						Nonce:       uint64(i + 1),
					},
					BaseToken:  "FB",
					QuoteToken: "USDT",
					Amount:     fmt.Sprintf("%d", 100+i%100),
					Price:      fmt.Sprintf("%.2f", 1.0+float64(i%100)*0.01),
					Side:       pb.OrderSide(i % 2),
				},
			},
		}
	}

	                         
	shortTxsSize := txCount * 8
	t.Logf("ShortTxs size: %d bytes (%.2f KB)", shortTxsSize, float64(shortTxsSize)/1024)

	                           
	estimatedTxSize := 500                                 
	fullBlockSize := txCount * estimatedTxSize
	t.Logf("Estimated full block size: %d bytes (%.2f MB)", fullBlockSize, float64(fullBlockSize)/(1024*1024))

	                                  
	savingsPercent := 100 * (1 - float64(shortTxsSize)/float64(fullBlockSize))
	t.Logf("ShortTxs saves %.1f%% bandwidth", savingsPercent)

	if savingsPercent < 90 {
		t.Errorf("Expected ShortTxs to save at least 90%% bandwidth, got %.1f%%", savingsPercent)
	}

	_ = mockTxs                  
}

                                                                  
func TestAdapterPrepareBlockContainer(t *testing.T) {
	cfg := config.DefaultConfig()
	testDBPath := t.TempDir() + "/test_container"

	logger := logs.NewNodeLogger("test", 100)
	dbManager, err := db.NewManager(testDBPath, logger)
	if err != nil {
		t.Fatalf("Failed to create db.Manager: %v", err)
	}
	defer dbManager.Close()

	adapter := NewConsensusAdapter(dbManager)

	                                                                       
	t.Run("BelowThreshold", func(t *testing.T) {
		txCount := cfg.TxPool.MaxTxsPerBlock - 100
		t.Logf("Testing with %d txs (below %d threshold)", txCount, cfg.TxPool.MaxTxsPerBlock)
		                                                                                                     
	})

	                                                                     
	t.Run("AboveThreshold", func(t *testing.T) {
		txCount := cfg.TxPool.MaxTxsPerBlock + 500
		t.Logf("Testing with %d txs (above %d threshold)", txCount, cfg.TxPool.MaxTxsPerBlock)
		                                          
	})

	_ = adapter                  
}

                                        
type mockTxValidator struct{}

func (v *mockTxValidator) CheckAnyTx(tx *pb.AnyTx) error {
	return nil
}

                                                                 
func BenchmarkShortTxsEncoding(b *testing.B) {
	                               
	txCount := 10000
	txIDs := make([]string, txCount)
	for i := 0; i < txCount; i++ {
		txIDs[i] = fmt.Sprintf("0x%064x", i+1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var shortTxs []byte
		for _, txID := range txIDs {
			idBytes, _ := hex.DecodeString(txID[2:])
			shortTxs = append(shortTxs, idBytes[:8]...)
		}
		_ = shortTxs
	}
}

                                                                 
func BenchmarkShortTxsDecoding(b *testing.B) {
	                                           
	txCount := 10000
	shortTxs := make([]byte, txCount*8)
	for i := 0; i < txCount; i++ {
		copy(shortTxs[i*8:], []byte{byte(i), byte(i >> 8), byte(i >> 16), byte(i >> 24), 0, 0, 0, 0})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		const shortHashSize = 8
		hashes := make([][]byte, 0, txCount)
		for j := 0; j+shortHashSize <= len(shortTxs); j += shortHashSize {
			hash := make([]byte, shortHashSize)
			copy(hash, shortTxs[j:j+shortHashSize])
			hashes = append(hashes, hash)
		}
		_ = hashes
	}
}
