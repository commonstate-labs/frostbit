package vm

import (
	"dex/keys"
	"testing"
)

func TestRecordUserActivityWritesCanonicalWithoutKindIndex(t *testing.T) {
	sv := newMemStateView()
	recordUserActivity(sv, "alice", &UserActivityRecord{
		Kind: ActKindFundingSettle, Height: 12, TxID: "tx-12", Amount: "7", Token: "USDT",
	})

	canonicalKey := keys.KeyUserActivity("alice", 12, 0)
	canonical, exists, err := sv.Get(canonicalKey)
	if err != nil || !exists || len(canonical) == 0 {
		t.Fatalf("canonical exists=%t err=%v value=%q", exists, err, canonical)
	}
	indexKey := keys.KeyUserActivityKindIndex("alice", ActKindFundingSettle, 12, "tx-12", 0)
	if marker, exists, err := sv.Get(indexKey); err != nil || exists || len(marker) != 0 {
		t.Fatalf("unexpected kind marker exists=%t err=%v value=%q", exists, err, marker)
	}
}

func TestLedgerPostWritesCanonicalWithoutKindIndex(t *testing.T) {
	sv := newMemStateView()
	entry := LedgerEntry{
		Height: 21, TxID: "ledger-tx", Reason: "ledger_reason", Account: "bob",
		Token: "USDT", Amount: "5", From: BucketExternal, To: BucketAvailable,
	}
	if err := NewLedger(sv).Post(entry); err != nil {
		t.Fatalf("post ledger entry: %v", err)
	}

	canonicalKey := keys.KeyUserActivity("bob", 21, 0)
	canonical, exists, err := sv.Get(canonicalKey)
	if err != nil || !exists || len(canonical) == 0 {
		t.Fatalf("canonical exists=%t err=%v value=%q", exists, err, canonical)
	}
	indexKey := keys.KeyUserActivityKindIndex("bob", entry.Reason, 21, entry.TxID, 0)
	if marker, exists, err := sv.Get(indexKey); err != nil || exists || len(marker) != 0 {
		t.Fatalf("unexpected ledger kind marker exists=%t err=%v value=%q", exists, err, marker)
	}
}
