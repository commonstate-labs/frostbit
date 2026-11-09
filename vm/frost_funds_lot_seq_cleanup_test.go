package vm

import (
	"fmt"
	"testing"

	"dex/keys"
)

func TestCleanupFrostFundsLotSequencesForHeight(t *testing.T) {
	sv := NewMockStateView()
	current := keys.KeyFrostFundsLotSeq("eth", "usdc", 1, 100)
	other := keys.KeyFrostFundsLotSeq("eth", "usdc", 1, 101)
	sv.Set(current, []byte("2"))
	sv.Set(other, []byte("1"))
	trackFrostFundsLotSeqForBlockCleanup(sv, 100, current)
	trackFrostFundsLotSeqForBlockCleanup(sv, 101, other)

	if err := cleanupFrostFundsLotSequencesForHeight(sv, 100); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(current); exists {
		t.Fatal("current-height sequence counter was retained")
	}
	if raw, exists, _ := sv.Get(other); !exists || string(raw) != "1" {
		t.Fatalf("other-height sequence counter changed: exists=%t raw=%q", exists, string(raw))
	}
	if _, exists, _ := sv.Get(keys.KeyFrostFundsLotSeqCleanupMarker(100)); exists {
		t.Fatal("current-height cleanup marker was retained")
	}
}

func TestEnsureFrostFundsLotSeqCleanupMigrationRunsOnce(t *testing.T) {
	sv := NewMockStateView()
	legacyA := keys.KeyFrostFundsLotSeq("eth", "usdc", 1, 10)
	legacyB := keys.KeyFrostFundsLotSeq("trx", "usdt", 2, 20)
	lot := keys.KeyFrostFundsLotIndex("eth", "usdc", 1, 10, 1)
	sv.Set(legacyA, []byte("2"))
	sv.Set(legacyB, []byte("1"))
	sv.Set(lot, []byte("request-1"))

	if err := ensureFrostFundsLotSeqCleanupMigration(sv, 100); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{legacyA, legacyB} {
		if _, exists, _ := sv.Get(key); exists {
			t.Fatalf("legacy sequence counter %s was retained", key)
		}
	}
	if raw, exists, _ := sv.Get(lot); !exists || string(raw) != "request-1" {
		t.Fatalf("live funds lot changed: exists=%t raw=%q", exists, string(raw))
	}
	if raw, exists, _ := sv.Get(keys.KeyFrostFundsLotSeqCleanupBackfillDone()); !exists || string(raw) != "100" {
		t.Fatalf("migration marker missing: exists=%t raw=%q", exists, string(raw))
	}

	lateLegacy := keys.KeyFrostFundsLotSeq("eth", "usdc", 1, 30)
	sv.Set(lateLegacy, []byte("1"))
	if err := ensureFrostFundsLotSeqCleanupMigration(sv, 101); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(lateLegacy); !exists {
		t.Fatal("completed migration unexpectedly rescanned StateDB")
	}
}

func TestEnsureFrostFundsLotSeqCleanupMigrationIsBounded(t *testing.T) {
	sv := NewMockStateView()
	for i := 0; i < frostFundsLotSeqBackfillLimit+1; i++ {
		key := keys.KeyFrostFundsLotSeq("eth", fmt.Sprintf("asset-%04d", i), 1, uint64(i+1))
		sv.Set(key, []byte("1"))
	}

	if err := ensureFrostFundsLotSeqCleanupMigration(sv, 100); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostFundsLotSeqCleanupBackfillDone()); exists {
		t.Fatal("migration completed despite more than one bounded batch")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostFundsLotSeqCleanupBackfillCursor()); !exists {
		t.Fatal("bounded migration cursor was not persisted")
	}

	if err := ensureFrostFundsLotSeqCleanupMigration(sv, 101); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := sv.Get(keys.KeyFrostFundsLotSeqCleanupBackfillDone()); !exists {
		t.Fatal("migration did not complete on the second batch")
	}
	rows, err := sv.Scan(keys.KeyFrostFundsLotSeqPrefix())
	if err != nil {
		t.Fatal(err)
	}
	for key := range rows {
		if isFrostFundsLotSequenceKey(key) {
			t.Fatalf("legacy sequence counter survived bounded migration: %s", key)
		}
	}
}
