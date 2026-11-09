                 
package utils

import (
	"strconv"
	"testing"
	"time"
)

                                                                                                      
func generateTxs(n int) [][]byte {
	txs := make([][]byte, n)
	for i := 0; i < n; i++ {
		                                                         
		txs[i] = []byte("tx_" + strconv.Itoa(i) + "_data")
	}
	return txs
}

                                                                                     
func TestBuildBlockHash_100(t *testing.T) {
	txs := generateTxs(100)
	start := time.Now()
	hash := BuildBlockHash(txs)
	elapsed := time.Since(start)

	if len(hash) == 0 {
		t.Error("BuildBlockHash 返回空哈希")
	}

	t.Logf("BuildBlockHash with 100 transactions took %s", elapsed)
}

                                                                                       
func TestBuildBlockHash_1000(t *testing.T) {
	txs := generateTxs(1000)
	start := time.Now()
	hash := BuildBlockHash(txs)
	elapsed := time.Since(start)

	if len(hash) == 0 {
		t.Error("BuildBlockHash 返回空哈希")
	}

	t.Logf("BuildBlockHash with 1000 transactions took %s", elapsed)
}

                                                                                          
func BenchmarkBuildBlockHash_100(b *testing.B) {
	txs := generateTxs(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildBlockHash(txs)
	}
}

                                                                                            
func BenchmarkBuildBlockHash_1000(b *testing.B) {
	txs := generateTxs(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildBlockHash(txs)
	}
}
