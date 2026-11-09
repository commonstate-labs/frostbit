package future

import (
	"math/big"
	"testing"
)

                                                                                               
                                                                                                            
                                                                                                 
func TestPositionView_IdempotentSameBlock(t *testing.T) {
	e, ps, _, _, mp := newTestEngine()
	                        
	if err := e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(50000*1e8), bi(500*1e8), 100); err != nil {
		t.Fatalf("open position: %v", err)
	}
	                                                                                       
	                                                                              
	mp.prices["BTC_USDT"] = bi(50000 * 1e8)

	                                                
	cf := e.funding.(*mockCumulativeFunding)
	cf.data["BTC_USDT"] = [2]*big.Int{big.NewInt(30), big.NewInt(0)}                       

	var owner = "alice"
	list, err := e.ViewPositionsByOwner(owner)
	if err != nil || len(list) != 1 {
		t.Fatalf("view 1: err=%v list=%v", err, list)
	}
	v1 := list[0]
	list2, _ := e.ViewPositionsByOwner(owner)
	v2 := list2[0]

	if v1.PendingFunding.Cmp(v2.PendingFunding) != 0 {
		t.Fatalf("pending funding mismatch: %s vs %s", v1.PendingFunding, v2.PendingFunding)
	}
	if v1.EffectiveMargin.Cmp(v2.EffectiveMargin) != 0 {
		t.Fatalf("effective margin mismatch: %s vs %s", v1.EffectiveMargin, v2.EffectiveMargin)
	}
	if v1.CurrentLiqPrice.Cmp(v2.CurrentLiqPrice) != 0 {
		t.Fatalf("liq price mismatch: %s vs %s", v1.CurrentLiqPrice, v2.CurrentLiqPrice)
	}

	                                                                                              
	stored, _ := ps.GetPosition(v1.ID)
	if stored.Margin.Cmp(bi(500*1e8)) != 0 {
		t.Fatalf("view mutated stored margin: got %s want 500e8", stored.Margin)
	}
	if stored.LastCumulativeFunding.Sign() != 0 {
		t.Fatalf("view mutated lastCumulativeFunding: got %s want 0", stored.LastCumulativeFunding)
	}
}

                                                                                 
                                                                                          
func TestPositionView_PendingFundingSignAndMagnitude(t *testing.T) {
	e, _, _, _, mp := newTestEngine()
	if err := e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(50000*1e8), bi(500*1e8), 100); err != nil {
		t.Fatalf("open position: %v", err)
	}
	mp.prices["BTC_USDT"] = bi(50000 * 1e8)

	                                                                                   
	cf := e.funding.(*mockCumulativeFunding)
	cf.data["BTC_USDT"] = [2]*big.Int{big.NewInt(50), big.NewInt(0)}

	list, _ := e.ViewPositionsByOwner("alice")
	v := list[0]
	if v.PendingFunding.Sign() >= 0 {
		t.Fatalf("long should owe funding when cum advances; got %s", v.PendingFunding)
	}
	if v.DisplayFunding.Cmp(v.PendingFunding) != 0 {
		t.Fatalf("display funding should match pending before settlement sweep: display=%s pending=%s", v.DisplayFunding, v.PendingFunding)
	}
	                                                                             
	expected := new(big.Int).Add(v.Margin, v.PendingFunding)
	if expected.Sign() < 0 {
		expected = new(big.Int)
	}
	if v.EffectiveMargin.Cmp(expected) != 0 {
		t.Fatalf("effective margin mismatch: view=%s expected=%s", v.EffectiveMargin, expected)
	}
}

func TestPositionView_DisplayFundingDoesNotDoubleChargeSettledFunding(t *testing.T) {
	pos := &Position{
		ID: "pos1", Owner: "alice", Pair: "BTC_USDT", Side: Long,
		EntryPrice:             sc(50000),
		Quantity:               sc(1),
		Margin:                 sc(450),
		Status:                 StatusOpen,
		LastCumulativeFunding:  big.NewInt(10),
		FirstCumulativeFunding: new(big.Int),
		SettledFunding:         sc(-50),
		LastSettledFunding:     sc(-50),
	}
	view := BuildView(pos, sc(50000), big.NewInt(10), new(big.Int), new(big.Int), new(big.Int), &MarketConfig{MaintenanceMarginRateBps: 50})
	if view.PendingFunding.Sign() != 0 {
		t.Fatalf("actual pending should be zero after settlement sweep, got %s", view.PendingFunding)
	}
	if view.DisplayFunding.Cmp(sc(-50)) != 0 {
		t.Fatalf("display funding should keep last settlement window visible, got %s", view.DisplayFunding)
	}
	if view.EffectiveMargin.Cmp(pos.Margin) != 0 {
		t.Fatalf("effective margin must not double charge display funding: got %s want %s", view.EffectiveMargin, pos.Margin)
	}
}

                                                                                                         
func TestBuildViewWithSettleMark_UsesLiveMarkForRiskAndSettleMarkForFunding(t *testing.T) {
	pos := &Position{
		ID: "pos1", Owner: "alice", Pair: "BTC_USDT", Side: Long,
		EntryPrice:             sc(100),
		Quantity:               sc(1),
		Margin:                 sc(10),
		Status:                 StatusOpen,
		LastCumulativeFunding:  new(big.Int),
		FirstCumulativeFunding: new(big.Int),
	}
	view := BuildViewWithSettleMark(pos, sc(80), sc(100), big.NewInt(100), new(big.Int), new(big.Int), new(big.Int), &MarketConfig{MaintenanceMarginRateBps: 50})
	if view.PendingFunding.Cmp(sc(-1)) != 0 {
		t.Fatalf("pending funding should use settle mark: got %s want %s", view.PendingFunding, sc(-1))
	}
	if view.UnrealizedPnL.Cmp(sc(-20)) != 0 {
		t.Fatalf("unrealized pnl should use live mark: got %s want %s", view.UnrealizedPnL, sc(-20))
	}
	if view.MarkPrice.Cmp(sc(80)) != 0 {
		t.Fatalf("view mark should be live mark: got %s", view.MarkPrice)
	}
	if view.Health.Cmp(new(big.Rat).SetInt64(1)) >= 0 {
		t.Fatalf("health should reflect live loss and be below liquidation threshold, got %s", view.Health.FloatString(6))
	}
}

func TestPositionView_NoMarkPrice(t *testing.T) {
	e, _, _, _, _ := newTestEngine()
	if err := e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(50000*1e8), bi(500*1e8), 100); err != nil {
		t.Fatalf("open position: %v", err)
	}
	                         

	list, err := e.ViewPositionsByOwner("alice")
	if err != nil || len(list) != 1 {
		t.Fatalf("view: err=%v list=%v", err, list)
	}
	v := list[0]
	if v.Owner != "alice" || v.Side != Long {
		t.Fatalf("base fields wrong: owner=%s side=%v", v.Owner, v.Side)
	}
	if v.MarkPrice != nil {
		t.Fatalf("mark price should be nil when unavailable; got %s", v.MarkPrice)
	}
	if v.UnrealizedPnL.Sign() != 0 {
		t.Fatalf("pnl should be zero without mark price; got %s", v.UnrealizedPnL)
	}
	if v.EffectiveMargin.Cmp(v.Margin) != 0 {
		t.Fatalf("effective margin should equal margin when no mark; got %s vs %s", v.EffectiveMargin, v.Margin)
	}
}

                                                                                    
                                                                                                            
func TestBuildView_PureFunctionEquivalence(t *testing.T) {
	e, _, _, _, mp := newTestEngine()
	if err := e.ProcessTradeForPosition("alice", "BTC_USDT", Long, bi(1), bi(50000*1e8), bi(500*1e8), 100); err != nil {
		t.Fatalf("open position: %v", err)
	}
	mp.prices["BTC_USDT"] = bi(48000 * 1e8)
	cf := e.funding.(*mockCumulativeFunding)
	cf.data["BTC_USDT"] = [2]*big.Int{big.NewInt(10), big.NewInt(-5)}

	                
	list, _ := e.ViewPositionsByOwner("alice")
	engView := list[0]

	                                             
	pos, _ := e.positions.GetPosition(engView.ID)
	cumL, cumS, _ := e.funding.GetCumulativeFunding(pos.Pair)
	prevL, prevS, _ := e.funding.GetPrevCumulativeFunding(pos.Pair)
	cfg, _ := e.GetMarketConfig(pos.Pair)
	pureView := BuildView(pos, mp.prices[pos.Pair], cumL, cumS, prevL, prevS, cfg)

	if engView.PendingFunding.Cmp(pureView.PendingFunding) != 0 {
		t.Fatalf("pending funding: eng=%s pure=%s", engView.PendingFunding, pureView.PendingFunding)
	}
	if engView.UnrealizedPnL.Cmp(pureView.UnrealizedPnL) != 0 {
		t.Fatalf("unrealized pnl: eng=%s pure=%s", engView.UnrealizedPnL, pureView.UnrealizedPnL)
	}
	if engView.CurrentLiqPrice.Cmp(pureView.CurrentLiqPrice) != 0 {
		t.Fatalf("liq price: eng=%s pure=%s", engView.CurrentLiqPrice, pureView.CurrentLiqPrice)
	}
}
