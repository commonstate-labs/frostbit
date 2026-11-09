package future

import (
	"math/big"
	"testing"
)

func putRealizedPnLBatchLong(t *testing.T, ps *mockPositionStore, id, owner, pair string, qty, margin *big.Int) {
	t.Helper()
	entryPrice := sc(100)
	pos := &Position{
		ID:                     id,
		Owner:                  owner,
		Pair:                   pair,
		Side:                   Long,
		EntryPrice:             entryPrice,
		EntryNotionalNumerator: new(big.Int).Mul(entryPrice, qty),
		Quantity:               new(big.Int).Set(qty),
		Margin:                 new(big.Int).Set(margin),
		MarginBasisAmount:      new(big.Int).Set(margin),
		MarginBasisClosed:      new(big.Int),
		Status:                 StatusOpen,
		LastCumulativeFunding:  new(big.Int),
		FirstCumulativeFunding: new(big.Int),
		SettledFunding:         new(big.Int),
		LastSettledFunding:     new(big.Int),
		RealizedPnL:            new(big.Int),
		ClosedQty:              new(big.Int),
		ExitNotional:           new(big.Int),
		ClosePnLRemainder:      new(big.Int),
		MaxQuantity:            new(big.Int).Set(qty),
		InitialMargin:          new(big.Int).Set(margin),
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatalf("seed position %s: %v", id, err)
	}
}

func TestRealizedPnLBatch_NetsProfitAndLossAcrossFills(t *testing.T) {
	e, ps, ins, ba, _, _ := newTestEngineFull()
	pair := "BTC_USDT"
	putRealizedPnLBatchLong(t, ps, "alice-long", "alice", pair, sc(2), sc(200))

	if err := e.BeginRealizedPnLBatch(); err != nil {
		t.Fatal(err)
	}
	if err := e.ProcessTradeForPosition("alice", pair, Short, sc(1), sc(198), new(big.Int), 10); err != nil {
		t.Fatalf("first profitable fill: %v", err)
	}
	if err := e.ProcessTradeForPosition("alice", pair, Short, sc(1), sc(2), new(big.Int), 10); err != nil {
		t.Fatalf("second losing fill: %v", err)
	}
	result, err := e.CommitRealizedPnLBatch()
	if err != nil {
		t.Fatalf("commit realized PnL batch: %v", err)
	}

	closed, err := ps.GetPosition("alice-long")
	if err != nil || closed == nil {
		t.Fatalf("closed position: pos=%+v err=%v", closed, err)
	}
	balance, _ := ba.GetAvailableBalance("alice", "USDT")
	insurance, _ := ins.GetBalance(pair)
	shortfall, _ := ins.GetShortfall(pair)
	if closed.Status != StatusClosed || closed.RealizedPnL.Sign() != 0 {
		t.Fatalf("closed status/realized = %v/%s, want CLOSED/0", closed.Status, closed.RealizedPnL)
	}
	if balance.Cmp(sc(200)) != 0 {
		t.Fatalf("returned balance = %s, want %s", balance, sc(200))
	}
	if insurance.Sign() != 0 || shortfall.Sign() != 0 || result.ShortfallForPair(pair).Sign() != 0 {
		t.Fatalf("insurance/shortfall/result = %s/%s/%s, want 0/0/0",
			insurance, shortfall, result.ShortfallForPair(pair))
	}
}

func TestRealizedPnLBatch_InsufficientFundsPaysClaimsFIFOAndTracksActualPnL(t *testing.T) {
	e, ps, ins, ba, _, _ := newTestEngineFull()
	pair := "BTC_USDT"
	putRealizedPnLBatchLong(t, ps, "alice-long", "alice", pair, sc(1), sc(50))
	putRealizedPnLBatchLong(t, ps, "bob-long", "bob", pair, sc(1), sc(50))
	putRealizedPnLBatchLong(t, ps, "carol-long", "carol", pair, sc(1), sc(50))

	if err := e.BeginRealizedPnLBatch(); err != nil {
		t.Fatal(err)
	}
	                                                                        
	if err := e.ProcessTradeForPosition("alice", pair, Short, sc(1), sc(110), new(big.Int), 20); err != nil {
		t.Fatalf("alice profitable fill: %v", err)
	}
	if err := e.ProcessTradeForPosition("bob", pair, Short, sc(1), sc(110), new(big.Int), 20); err != nil {
		t.Fatalf("bob profitable fill: %v", err)
	}
	                                                                         
	if err := e.ProcessTradeForPosition("carol", pair, Short, sc(1), sc(88), new(big.Int), 20); err != nil {
		t.Fatalf("carol losing fill: %v", err)
	}
	result, err := e.CommitRealizedPnLBatch()
	if err != nil {
		t.Fatalf("commit realized PnL batch: %v", err)
	}

	alice, _ := ps.GetPosition("alice-long")
	bob, _ := ps.GetPosition("bob-long")
	carol, _ := ps.GetPosition("carol-long")
	if alice.RealizedPnL.Cmp(sc(10)) != 0 || bob.RealizedPnL.Cmp(sc(2)) != 0 || carol.RealizedPnL.Cmp(sc(-12)) != 0 {
		t.Fatalf("actual realized PnL alice/bob/carol = %s/%s/%s, want %s/%s/%s",
			alice.RealizedPnL, bob.RealizedPnL, carol.RealizedPnL, sc(10), sc(2), sc(-12))
	}
	aliceBalance, _ := ba.GetAvailableBalance("alice", "USDT")
	bobBalance, _ := ba.GetAvailableBalance("bob", "USDT")
	carolBalance, _ := ba.GetAvailableBalance("carol", "USDT")
	if aliceBalance.Cmp(sc(60)) != 0 || bobBalance.Cmp(sc(52)) != 0 || carolBalance.Cmp(sc(38)) != 0 {
		t.Fatalf("returned balances alice/bob/carol = %s/%s/%s, want %s/%s/%s",
			aliceBalance, bobBalance, carolBalance, sc(60), sc(52), sc(38))
	}
	insurance, _ := ins.GetBalance(pair)
	shortfall, _ := ins.GetShortfall(pair)
	if insurance.Sign() != 0 || shortfall.Cmp(sc(8)) != 0 || result.ShortfallForPair(pair).Cmp(sc(8)) != 0 {
		t.Fatalf("insurance/shortfall/result = %s/%s/%s, want 0/%s/%s",
			insurance, shortfall, result.ShortfallForPair(pair), sc(8), sc(8))
	}
}

func TestRealizedPnLBatch_SelfTradeReincreaseRefreshesFinalPosition(t *testing.T) {
	e, ps, _, _, _, _ := newTestEngineFull()
	pair := "BTC_USDT"
	putRealizedPnLBatchLong(t, ps, "alice-long", "alice", pair, sc(2), sc(20))

	if err := e.BeginRealizedPnLBatch(); err != nil {
		t.Fatal(err)
	}
	                                                                             
	                                                                             
	                                                                        
	                                                 
	if err := e.ProcessTradeForPosition("alice", pair, Short, sc(1), sc(100), new(big.Int), 30); err != nil {
		t.Fatalf("self-trade close leg: %v", err)
	}
	if err := e.ProcessTradeForPosition("alice", pair, Long, sc(1), sc(100), sc(10), 30); err != nil {
		t.Fatalf("self-trade increase leg: %v", err)
	}
	if _, err := e.CommitRealizedPnLBatch(); err != nil {
		t.Fatalf("commit realized PnL batch: %v", err)
	}

	pos, err := ps.GetPosition("alice-long")
	if err != nil || pos == nil {
		t.Fatalf("load final self-trade position: pos=%+v err=%v", pos, err)
	}
	if pos.Status != StatusOpen || pos.Quantity.Cmp(sc(2)) != 0 || pos.Margin.Cmp(sc(20)) != 0 {
		t.Fatalf("stale batch clone overwrote self-trade increase: status=%v qty=%s margin=%s, want OPEN/%s/%s",
			pos.Status, pos.Quantity, pos.Margin, sc(2), sc(20))
	}
}

func TestRealizedPnLBatch_ADLRefreshesPreviouslyTrackedPosition(t *testing.T) {
	e, ps, ins, _, mp, _ := newTestEngineFull()
	pair := "BTC_USDT"
	entryPrice := sc(100)
	qty := sc(2)
	margin := sc(20)
	pos := &Position{
		ID:                     "bob-short",
		Owner:                  "bob",
		Pair:                   pair,
		Side:                   Short,
		EntryPrice:             entryPrice,
		EntryNotionalNumerator: new(big.Int).Mul(entryPrice, qty),
		Quantity:               new(big.Int).Set(qty),
		Margin:                 new(big.Int).Set(margin),
		MarginBasisAmount:      new(big.Int).Set(margin),
		MarginBasisClosed:      new(big.Int),
		Status:                 StatusOpen,
		LastCumulativeFunding:  new(big.Int),
		FirstCumulativeFunding: new(big.Int),
		SettledFunding:         new(big.Int),
		LastSettledFunding:     new(big.Int),
		RealizedPnL:            new(big.Int),
		ClosedQty:              new(big.Int),
		ExitNotional:           new(big.Int),
		ClosePnLRemainder:      new(big.Int),
		MaxQuantity:            new(big.Int).Set(qty),
		InitialMargin:          new(big.Int).Set(margin),
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	mp.prices[pair] = sc(90)
	_ = ins.SetBalance(pair, sc(2))

	if err := e.BeginRealizedPnLBatch(); err != nil {
		t.Fatal(err)
	}
	                                                                          
	                                                                             
	                                    
	if err := e.ProcessTradeForPosition("bob", pair, Long, sc(1), sc(110), new(big.Int), 40); err != nil {
		t.Fatalf("track partial close: %v", err)
	}
	                                                                    
	                                                             
	if _, err := e.ExecuteADL(pair, Short, sc(10)); err != nil {
		t.Fatalf("execute ADL: %v", err)
	}
	if _, err := e.CommitRealizedPnLBatch(); err != nil {
		t.Fatalf("commit realized PnL batch: %v", err)
	}

	final, err := ps.GetPosition("bob-short")
	if err != nil || final == nil {
		t.Fatalf("load final ADL position: pos=%+v err=%v", final, err)
	}
	if final.Status != StatusADLClosed || final.Quantity.Sign() != 0 || final.Margin.Sign() != 0 {
		t.Fatalf("batch commit resurrected ADL candidate: %+v", final)
	}
	if final.RealizedPnL.Cmp(sc(-8)) != 0 || final.ADLWithheldPnL.Cmp(sc(8)) != 0 {
		t.Fatalf("ADL lost prior PnL or its split: net=%s withheld=%s, want %s/%s",
			final.RealizedPnL, final.ADLWithheldPnL, sc(-8), sc(8))
	}
}
