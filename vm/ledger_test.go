package vm

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	"dex/keys"
	"dex/pb"
)

type failLedgerBalanceReadView struct {
	StateView
	key string
}

func (v failLedgerBalanceReadView) Get(key string) ([]byte, bool, error) {
	if key == v.key {
		return nil, false, errors.New("injected balance read failure")
	}
	return v.StateView.Get(key)
}

func TestLedgerMoveBalanceReadErrorsDoNotOverwriteExistingState(t *testing.T) {
	tests := []struct {
		name string
		from Bucket
		to   Bucket
	}{
		{name: "deduct available", from: BucketAvailable, to: BucketMargin},
		{name: "add available", from: BucketMargin, to: BucketAvailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newMemStateView()
			const account, token = "alice", "USDT"
			key := keys.KeyBalance(account, token)
			SetBalance(base, account, token, &pb.TokenBalance{Balance: "100"})
			before, exists, err := base.Get(key)
			if err != nil || !exists {
				t.Fatalf("seed balance: exists=%v err=%v", exists, err)
			}
			wrapped := failLedgerBalanceReadView{StateView: base, key: key}

			err = NewLedger(wrapped).Move(MoveRequest{
				Account: account, Token: token, Amount: big.NewInt(10),
				From: tt.from, To: tt.to, Reason: "strict_balance_read", Height: 1,
			})
			if err == nil {
				t.Fatal("ledger mutation accepted an injected balance read failure")
			}
			after, exists, getErr := base.Get(key)
			if getErr != nil || !exists || !bytes.Equal(after, before) {
				t.Fatalf("failed ledger read overwrote balance: exists=%v getErr=%v before=%x after=%x", exists, getErr, before, after)
			}
		})
	}
}

func TestLedgerMoveMalformedBalanceDoesNotOverwriteState(t *testing.T) {
	base := newMemStateView()
	const account, token = "alice", "USDT"
	key := keys.KeyBalance(account, token)
	malformed := []byte{0xff, 0xff}
	base.Set(key, malformed)

	err := NewLedger(base).Move(MoveRequest{
		Account: account, Token: token, Amount: big.NewInt(10),
		From: BucketMargin, To: BucketAvailable, Reason: "strict_balance_decode", Height: 1,
	})
	if err == nil {
		t.Fatal("ledger mutation accepted a malformed balance record")
	}
	after, exists, getErr := base.Get(key)
	if getErr != nil || !exists || !bytes.Equal(after, malformed) {
		t.Fatalf("malformed balance was overwritten: exists=%v getErr=%v after=%x", exists, getErr, after)
	}
}

                                                                                                   
                                       
func TestLedger_PostAndQuery_BasicFlow(t *testing.T) {
	sv := newMemStateView()
	l := NewLedger(sv)

	err := l.Post(LedgerEntry{
		Height:  10,
		TxID:    "tx1",
		Reason:  "future_open_margin",
		Account: "alice",
		Token:   "USDT",
		Amount:  "500",
		From:    BucketAvailable,
		To:      BucketMargin,
		Ref:     "pos1",
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	entries, err := l.Query(LedgerFilter{Account: "alice"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.From != BucketAvailable || e.To != BucketMargin {
		t.Fatalf("bucket wrong: from=%s to=%s", e.From, e.To)
	}
	if e.Amount != "500" || e.Reason != "future_open_margin" || e.Ref != "pos1" {
		t.Fatalf("entry fields wrong: %+v", e)
	}
}

                                                                                          
                                      
func TestLedger_Post_SameHeightSeqIncrement(t *testing.T) {
	sv := newMemStateView()
	l := NewLedger(sv)

	for i := 0; i < 3; i++ {
		if err := l.Post(LedgerEntry{
			Height: 5, Account: "bob", Token: "USDT",
			Amount: "100", Reason: "transfer_in",
			From: BucketExternal, To: BucketAvailable,
		}); err != nil {
			t.Fatalf("Post %d: %v", i, err)
		}
	}
	entries, _ := l.Query(LedgerFilter{Account: "bob"})
	if len(entries) != 3 {
		t.Fatalf("seq collision: got %d entries", len(entries))
	}
}

                                                                                     
func TestLedger_Post_Rejects(t *testing.T) {
	sv := newMemStateView()
	l := NewLedger(sv)

	bad := []LedgerEntry{
		{Height: 1, Token: "USDT", Amount: "100", Reason: "x", From: BucketAvailable, To: BucketMargin},               
		{Height: 1, Account: "a", Amount: "100", Reason: "x", From: BucketAvailable, To: BucketMargin},              
		{Height: 1, Account: "a", Token: "USDT", Amount: "0", Reason: "x", From: BucketAvailable, To: BucketMargin},
		{Height: 1, Account: "a", Token: "USDT", Amount: "-1", Reason: "x", From: BucketAvailable, To: BucketMargin},
		{Height: 1, Account: "a", Token: "USDT", Amount: "100", Reason: "x", From: BucketAvailable, To: BucketAvailable},
		{Height: 1, Account: "a", Token: "USDT", Amount: "100", Reason: ""},
	}
	for i, e := range bad {
		if err := l.Post(e); err == nil {
			t.Fatalf("case %d should reject but accepted: %+v", i, e)
		}
	}
}

                                                            
func TestLedger_Query_Filters(t *testing.T) {
	sv := newMemStateView()
	l := NewLedger(sv)

	mustPost := func(e LedgerEntry) {
		t.Helper()
		if err := l.Post(e); err != nil {
			t.Fatalf("post: %v", err)
		}
	}
	mustPost(LedgerEntry{Height: 1, TxID: "tx1", Reason: "A", Account: "a", Token: "U", Amount: "10", From: BucketAvailable, To: BucketMargin})
	mustPost(LedgerEntry{Height: 2, TxID: "tx2", Reason: "B", Account: "a", Token: "U", Amount: "20", From: BucketAvailable, To: BucketMargin})
	mustPost(LedgerEntry{Height: 3, TxID: "tx3", Reason: "A", Account: "a", Token: "U", Amount: "30", From: BucketAvailable, To: BucketMargin})

	got, _ := l.Query(LedgerFilter{Account: "a", Reason: "A"})
	if len(got) != 2 {
		t.Fatalf("reason filter: got %d", len(got))
	}
	got, _ = l.Query(LedgerFilter{Account: "a", TxID: "tx2"})
	if len(got) != 1 || got[0].Reason != "B" {
		t.Fatalf("txid filter: %+v", got)
	}
	got, _ = l.Query(LedgerFilter{Account: "a", MinHeight: 2, MaxHeight: 3})
	if len(got) != 2 {
		t.Fatalf("height filter: got %d", len(got))
	}
}
