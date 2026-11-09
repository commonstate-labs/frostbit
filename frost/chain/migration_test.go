package chain

import (
	"bytes"
	"testing"
)

func TestMigrationIntentHashBindsEveryAuthorityField(t *testing.T) {
	base := MigrationIntentHash("btc", 1, 2, 3, []byte{1}, []byte{2}, []byte{3}, false)
	cases := [][]byte{
		MigrationIntentHash("eth", 1, 2, 3, []byte{1}, []byte{2}, []byte{3}, false),
		MigrationIntentHash("btc", 2, 2, 3, []byte{1}, []byte{2}, []byte{3}, false),
		MigrationIntentHash("btc", 1, 1, 3, []byte{1}, []byte{2}, []byte{3}, false),
		MigrationIntentHash("btc", 1, 2, 4, []byte{1}, []byte{2}, []byte{3}, false),
		MigrationIntentHash("btc", 1, 2, 3, []byte{9}, []byte{2}, []byte{3}, false),
		MigrationIntentHash("btc", 1, 2, 3, []byte{1}, []byte{9}, []byte{3}, false),
		MigrationIntentHash("btc", 1, 2, 3, []byte{1}, []byte{2}, []byte{9}, false),
		MigrationIntentHash("btc", 1, 2, 3, []byte{1}, []byte{2}, []byte{3}, true),
	}
	for i, candidate := range cases {
		if bytes.Equal(base, candidate) {
			t.Fatalf("field mutation %d did not change migration intent hash", i)
		}
	}
}
