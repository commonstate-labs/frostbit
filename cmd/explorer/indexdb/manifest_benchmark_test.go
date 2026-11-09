package indexdb

import (
	"fmt"
	"testing"
)

func BenchmarkHeightManifestAddTxIDs5000(b *testing.B) {
	txIDs := make([]string, 5000)
	for i := range txIDs {
		txIDs[i] = fmt.Sprintf("tx-%064d", i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		manifest := heightManifest{}
		for _, txID := range txIDs {
			manifest.addTxID(txID)
		}
	}
}
