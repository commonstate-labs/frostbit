package future

import (
	"math/big"
	"testing"
)

func bi(v int64) *big.Int { return big.NewInt(v) }

                                                                     
                                                                                  
func sc(v int64) *big.Int { return new(big.Int).Mul(big.NewInt(v), PriceScale) }

func TestCalcNotional(t *testing.T) {
	                                                 
	got := CalcNotional(sc(2), sc(50000))
	if got.Cmp(sc(100000)) != 0 {
		t.Fatalf("CalcNotional = %s, want %s", got, sc(100000))
	}
}

func TestCalcRequiredMargin(t *testing.T) {
	                                       
	got := CalcRequiredMargin(sc(1), sc(50000), 100)
	if got.Cmp(sc(500)) != 0 {
		t.Fatalf("CalcRequiredMargin = %s, want %s", got, sc(500))
	}
}

func TestMaintenanceMathPreservesUint64Rate(t *testing.T) {
	rate := uint64(1) << 63
	notional := big.NewInt(25_000)
	wantMaintenance := new(big.Int).Mul(notional, new(big.Int).SetUint64(rate))
	wantMaintenance.Div(wantMaintenance, big.NewInt(BPS))
	if got := CalcMaintenanceMargin(notional, rate); got.Cmp(wantMaintenance) != 0 {
		t.Fatalf("maintenance at uint64 rate = %s, want %s", got, wantMaintenance)
	}

	entry := big.NewInt(50_000)
	wantAdjust := new(big.Int).Mul(entry, new(big.Int).SetUint64(rate))
	wantAdjust.Div(wantAdjust, big.NewInt(BPS))
	wantLiq := new(big.Int).Add(entry, wantAdjust)
	gotLiq := CalcLiquidationPrice(Long, entry, new(big.Int), new(big.Int).Set(PriceScale), rate)
	if gotLiq.Cmp(wantLiq) != 0 {
		t.Fatalf("long liquidation price at uint64 rate = %s, want %s", gotLiq, wantLiq)
	}
}

func TestCalcUnrealizedPnL(t *testing.T) {
	                                                                             
	pnl := CalcUnrealizedPnL(Long, sc(50000), sc(50500), sc(2))
	if pnl.Cmp(sc(1000)) != 0 {
		t.Fatalf("Long PnL = %s, want %s", pnl, sc(1000))
	}
	                                                                             
	pnl = CalcUnrealizedPnL(Short, sc(50000), sc(49500), sc(2))
	if pnl.Cmp(sc(1000)) != 0 {
		t.Fatalf("Short PnL = %s, want %s", pnl, sc(1000))
	}
	                                                                           
	pnl = CalcUnrealizedPnL(Short, sc(50000), sc(50500), sc(2))
	want := new(big.Int).Neg(sc(1000))
	if pnl.Cmp(want) != 0 {
		t.Fatalf("Short loss PnL = %s, want %s", pnl, want)
	}
}

func TestCalcLiquidationPrice_Long100x(t *testing.T) {
	                                                             
	                                                                     
	liq := CalcLiquidationPrice(Long, sc(50000), sc(500), sc(1), 50)
	if liq.Cmp(sc(49750)) != 0 {
		t.Fatalf("100x Long liq price = %s, want %s", liq, sc(49750))
	}
}

func TestCalcLiquidationPrice_Short100x(t *testing.T) {
	                                                
	                                                                     
	liq := CalcLiquidationPrice(Short, sc(50000), sc(500), sc(1), 50)
	if liq.Cmp(sc(50250)) != 0 {
		t.Fatalf("100x Short liq price = %s, want %s", liq, sc(50250))
	}
}

func TestCalcLiquidationPrice_Long20x(t *testing.T) {
	                                                       
	                                                                         
	liq := CalcLiquidationPrice(Long, sc(50000), sc(2500), sc(1), 250)
	if liq.Cmp(sc(48750)) != 0 {
		t.Fatalf("20x Long liq price = %s, want %s", liq, sc(48750))
	}
}

func TestCalcBankruptPrice(t *testing.T) {
	                                                                
	bp := CalcBankruptPrice(Long, sc(50000), sc(500), sc(1))
	if bp.Cmp(sc(49500)) != 0 {
		t.Fatalf("Long bankrupt price = %s, want %s", bp, sc(49500))
	}
	bp = CalcBankruptPrice(Short, sc(50000), sc(500), sc(1))
	if bp.Cmp(sc(50500)) != 0 {
		t.Fatalf("Short bankrupt price = %s, want %s", bp, sc(50500))
	}
}

func TestShouldLiquidate(t *testing.T) {
	                                                
	                                                                             
	if !ShouldLiquidate(sc(500), Long, sc(50000), sc(49740), sc(1), 50) {
		t.Fatal("should liquidate at 49740")
	}
	                                                         
	if ShouldLiquidate(sc(500), Long, sc(50000), sc(49800), sc(1), 50) {
		t.Fatal("should NOT liquidate at 49800")
	}
}

func TestCalcWeightedEntryPrice(t *testing.T) {
	                                                     
	                                                     
	wp := CalcWeightedEntryPrice(sc(50000), sc(1), sc(52000), sc(1))
	if wp.Cmp(sc(51000)) != 0 {
		t.Fatalf("weighted entry = %s, want %s", wp, sc(51000))
	}
}

func TestMarketConfigDefaults(t *testing.T) {
	cfg := &MarketConfig{}
	if got := cfg.MaxLeverageOrDefault(); got != DefaultMaxLeverage {
		t.Fatalf("default max leverage = %d, want %d", got, DefaultMaxLeverage)
	}
	if got := cfg.MaintenanceMarginRateOrDefault(); got != DefaultMaintenanceMarginRateBps {
		t.Fatalf("default mm rate = %d, want %d", got, DefaultMaintenanceMarginRateBps)
	}
	cfg = &MarketConfig{MaxLeverage: 50, MaintenanceMarginRateBps: 100}
	if got := cfg.MaxLeverageOrDefault(); got != 50 {
		t.Fatalf("custom max leverage = %d, want 50", got)
	}
	if got := cfg.MaintenanceMarginRateOrDefault(); got != 100 {
		t.Fatalf("custom mm rate = %d, want 100", got)
	}
}

func TestCalcADLScore(t *testing.T) {
	                                       
	                                                                                      
	score := CalcADLScore(sc(1000), sc(10000), sc(500))
	if score.Cmp(bi(40)) != 0 {
		t.Fatalf("ADL score = %s, want 40", score)
	}
	                             
	score = CalcADLScore(new(big.Int).Neg(sc(100)), sc(10000), sc(500))
	if score.Sign() != 0 {
		t.Fatal("negative PnL should have score 0")
	}
}

func TestCalcInsuranceDelta(t *testing.T) {
	                                                                             
	delta := CalcInsuranceDelta(Long, sc(49500), sc(49700), sc(1))
	if delta.Cmp(sc(200)) != 0 {
		t.Fatalf("insurance delta = %s, want %s", delta, sc(200))
	}
	                                                                  
	delta = CalcInsuranceDelta(Long, sc(49500), sc(49300), sc(1))
	want := new(big.Int).Neg(sc(200))
	if delta.Cmp(want) != 0 {
		t.Fatalf("insurance delta (breach) = %s, want %s", delta, want)
	}
}

func TestAddMarginChangesLiqPrice(t *testing.T) {
	                                                                
	liq1 := CalcLiquidationPrice(Long, sc(50000), sc(500), sc(1), 50)
	                                                                       
	liq2 := CalcLiquidationPrice(Long, sc(50000), sc(1000), sc(1), 50)
	if liq2.Cmp(sc(49250)) != 0 {
		t.Fatalf("after add margin liq = %s, want %s", liq2, sc(49250))
	}
	if liq2.Cmp(liq1) >= 0 {
		t.Fatal("adding margin should move liq price further down")
	}
}
