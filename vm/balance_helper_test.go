package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"
)

func TestSetBalanceDeletesZeroTotalBalance(t *testing.T) {
	sv := NewMockStateView()
	owner, token := "alice", "USDT"

	SetBalance(sv, owner, token, &pb.TokenBalance{Balance: "10"})
	if _, exists, _ := sv.Get(keys.KeyBalance(owner, token)); !exists {
		t.Fatal("positive balance key should exist")
	}

	SetBalance(sv, owner, token, &pb.TokenBalance{Balance: "0"})
	if _, exists, _ := sv.Get(keys.KeyBalance(owner, token)); exists {
		t.Fatal("zero total balance key should be deleted")
	}
	if got := GetBalance(sv, owner, token).Balance; got != "0" {
		t.Fatalf("missing balance should read as zero, got %s", got)
	}
}

func TestSetBalanceKeepsNonZeroLockedBucket(t *testing.T) {
	sv := NewMockStateView()
	owner, token := "alice", "USDT"

	SetBalance(sv, owner, token, &pb.TokenBalance{Balance: "0", OrderFrozenBalance: "4"})
	if _, exists, _ := sv.Get(keys.KeyBalance(owner, token)); !exists {
		t.Fatal("non-zero frozen bucket should keep balance key")
	}
}

func TestBalanceWriteOpDeletesMissingBalance(t *testing.T) {
	sv := NewMockStateView()
	owner, token := "alice", "USDT"

	SetBalance(sv, owner, token, &pb.TokenBalance{Balance: "1"})
	SetBalance(sv, owner, token, &pb.TokenBalance{Balance: "0"})

	op := balanceWriteOp(sv, owner, token)
	if !op.Del || op.Key != keys.KeyBalance(owner, token) {
		t.Fatalf("missing balance should produce delete op, got %+v", op)
	}
}
