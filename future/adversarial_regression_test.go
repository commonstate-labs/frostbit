package future

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func TestCalcRequiredMargin_CeilsExactRawFraction(t *testing.T) {
	tests := []struct {
		name            string
		quantity, price *big.Int
		leverage        uint64
		want            *big.Int
	}{
		{
			name:     "sub leverage unit rounds up",
			quantity: new(big.Int).Set(PriceScale), price: big.NewInt(199), leverage: 100,
			want: big.NewInt(2),
		},
		{
			name:     "exact division does not add one",
			quantity: new(big.Int).Set(PriceScale), price: big.NewInt(200), leverage: 100,
			want: big.NewInt(2),
		},
		{
			name:     "smallest positive raw notional still needs margin",
			quantity: big.NewInt(1), price: new(big.Int).Set(PriceScale), leverage: 100,
			want: big.NewInt(1),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CalcRequiredMargin(tc.quantity, tc.price, tc.leverage)
			if got.Cmp(tc.want) != 0 {
				t.Fatalf("margin=%s want=%s", got, tc.want)
			}
		})
	}

	whole := CalcRequiredMargin(new(big.Int).Mul(big.NewInt(2), PriceScale), big.NewInt(199), 100)
	partA := CalcRequiredMargin(new(big.Int).Set(PriceScale), big.NewInt(199), 100)
	partB := CalcRequiredMargin(new(big.Int).Set(PriceScale), big.NewInt(199), 100)
	if whole.Cmp(big.NewInt(4)) != 0 || new(big.Int).Add(partA, partB).Cmp(whole) != 0 {
		t.Fatalf("split margin differs: whole=%s parts=%s+%s", whole, partA, partB)
	}
}

func TestPositionConservativeLiquidationPrice_DoesNotMissExactShortBoundary(t *testing.T) {
	pos := &Position{
		ID: "short-boundary", Owner: "alice", Pair: "BTC_USDT", Side: Short,
		EntryPrice: sc(50_000), Quantity: sc(1), Margin: sc(500), Status: StatusOpen,
	}
	mark := sc(50_249)
	const maintenanceRate = uint64(50)
	if !ShouldLiquidatePosition(pos, pos.Margin, mark, maintenanceRate) {
		t.Fatal("test setup: exact position must be liquidatable at the mark")
	}

	legacy := CalcLiquidationPrice(pos.Side, pos.EntryPrice, pos.Margin, pos.Quantity, maintenanceRate)
	if legacy.Cmp(mark) <= 0 {
		t.Fatalf("test setup: legacy approximation=%s should sit above risky mark=%s", legacy, mark)
	}
	got := positionConservativeLiquidationPrice(pos, pos.Margin, maintenanceRate)
	if got.Cmp(mark) > 0 {
		t.Fatalf("short index boundary=%s exceeds truly risky mark=%s", got, mark)
	}
}

func TestPositionConservativeLiquidationPrice_LongRoundsTowardCandidateSuperset(t *testing.T) {
	pos := &Position{
		ID: "long-boundary", Owner: "alice", Pair: "BTC_USDT", Side: Long,
		EntryPrice: sc(50_000), Quantity: sc(1), Margin: sc(500), Status: StatusOpen,
	}
	const maintenanceRate = uint64(50)
	boundary := positionConservativeLiquidationPrice(pos, pos.Margin, maintenanceRate)
	                                                                           
	                                       
	for delta := int64(-4); delta <= 4; delta++ {
		mark := new(big.Int).Add(boundary, big.NewInt(delta))
		if mark.Sign() > 0 && ShouldLiquidatePosition(pos, pos.Margin, mark, maintenanceRate) && mark.Cmp(boundary) > 0 {
			t.Fatalf("risky long mark=%s escaped boundary=%s", mark, boundary)
		}
	}
}

func assertLiquidationIndexCoversRiskyMark(t *testing.T, caseName string, pos *Position, margin *big.Int, rate uint64, boundary, mark *big.Int) {
	t.Helper()
	if mark.Sign() <= 0 || mark.Cmp(maximumLiquidationIndexPrice()) > 0 ||
		!ShouldLiquidatePosition(pos, margin, mark, rate) {
		return
	}
	if pos.Side == Long && mark.Cmp(boundary) > 0 {
		t.Fatalf("%s: risky Long mark=%s escaped upper index boundary=%s (q=%s n=%s carry=%s margin=%s rate=%d)",
			caseName, mark, boundary, pos.Quantity, pos.EntryNotionalNumerator, pos.ClosePnLRemainder, margin, rate)
	}
	if pos.Side == Short && mark.Cmp(boundary) < 0 {
		t.Fatalf("%s: risky Short mark=%s escaped lower index boundary=%s (q=%s n=%s carry=%s margin=%s rate=%d)",
			caseName, mark, boundary, pos.Quantity, pos.EntryNotionalNumerator, pos.ClosePnLRemainder, margin, rate)
	}
}

func TestPositionConservativeLiquidationPrice_CoversExactHealthOracle(t *testing.T) {
	maxUint64 := ^uint64(0)
	rates := []uint64{0, 50, 9999, 10000, maxUint64}
	quantityDeltas := []int64{-1, 0, 1}
	entryPrices := []int64{1, 7, 63}
	numeratorRemainders := []int{0, 1, 2}
	carries := []*big.Int{
		new(big.Int).Neg(new(big.Int).Sub(PriceScale, big.NewInt(1))),
		big.NewInt(-17), new(big.Int), big.NewInt(17),
		new(big.Int).Sub(PriceScale, big.NewInt(1)),
	}
	margins := []*big.Int{new(big.Int), big.NewInt(1), big.NewInt(7)}

	                                                                    
	                                                                             
	                                                                           
	caseNo := 0
	for _, side := range []Side{Long, Short} {
		for _, quantityDelta := range quantityDeltas {
			quantity := new(big.Int).Add(PriceScale, big.NewInt(quantityDelta))
			for _, entryPrice := range entryPrices {
				for _, remainderSelector := range numeratorRemainders {
					remainder := new(big.Int)
					switch remainderSelector {
					case 1:
						remainder.SetInt64(1)
					case 2:
						remainder.Sub(quantity, big.NewInt(1))
					}
					entryNumerator := new(big.Int).Mul(quantity, big.NewInt(entryPrice))
					entryNumerator.Add(entryNumerator, remainder)
					for _, carry := range carries {
						for _, margin := range margins {
							for _, rate := range rates {
								caseNo++
								pos := &Position{
									Side: side, EntryPrice: big.NewInt(entryPrice),
									EntryNotionalNumerator: new(big.Int).Set(entryNumerator),
									Quantity:               new(big.Int).Set(quantity),
									ClosePnLRemainder:      new(big.Int).Set(carry),
								}
								boundary := positionConservativeLiquidationPrice(pos, margin, rate)
								name := fmt.Sprintf("exhaustive-%d", caseNo)
								for rawMark := int64(1); rawMark <= 96; rawMark++ {
									assertLiquidationIndexCoversRiskyMark(t, name, pos, margin, rate, boundary, big.NewInt(rawMark))
								}
							}
						}
					}
				}
			}
		}
	}

	                                                                        
	                                                                             
	                                                                              
	seed := uint64(0x6a09e667f3bcc909)
	next := func() uint64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return seed
	}
	quantities := []*big.Int{
		big.NewInt(1), big.NewInt(3), new(big.Int).Sub(PriceScale, big.NewInt(1)),
		new(big.Int).Set(PriceScale), new(big.Int).Add(PriceScale, big.NewInt(1)),
		new(big.Int).Add(new(big.Int).Mul(PriceScale, big.NewInt(3)), big.NewInt(7)),
		new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(17)),
	}
	prices := []*big.Int{
		big.NewInt(1), new(big.Int).Sub(PriceScale, big.NewInt(1)), new(big.Int).Set(PriceScale),
		new(big.Int).Add(new(big.Int).Mul(PriceScale, big.NewInt(50_000)), big.NewInt(3)),
		new(big.Int).Sub(new(big.Int).Quo(maximumLiquidationIndexPrice(), big.NewInt(2)), big.NewInt(1)),
	}
	boundaryOffsets := []*big.Int{
		new(big.Int).Neg(new(big.Int).Add(PriceScale, big.NewInt(1))),
		new(big.Int).Neg(PriceScale), big.NewInt(-17), big.NewInt(-2), big.NewInt(-1),
		new(big.Int), big.NewInt(1), big.NewInt(2), big.NewInt(17),
		new(big.Int).Set(PriceScale), new(big.Int).Add(PriceScale, big.NewInt(1)),
	}
	maxPrice := maximumLiquidationIndexPrice()
	for i := 0; i < 2048; i++ {
		side := Long
		if next()&1 != 0 {
			side = Short
		}
		quantity := new(big.Int).Set(quantities[int(next()%uint64(len(quantities)))])
		entryPrice := new(big.Int).Set(prices[int(next()%uint64(len(prices)))])
		numeratorRemainder := new(big.Int).Mod(new(big.Int).SetUint64(next()), quantity)
		if quantity.Cmp(big.NewInt(1)) > 0 && numeratorRemainder.Sign() == 0 {
			numeratorRemainder.SetInt64(1)
		}
		entryNumerator := new(big.Int).Mul(entryPrice, quantity)
		entryNumerator.Add(entryNumerator, numeratorRemainder)
		carry := new(big.Int).Set(carries[int(next()%uint64(len(carries)))])
		notional := new(big.Int).Quo(entryNumerator, PriceScale)
		margin := new(big.Int)
		switch next() % 5 {
		case 1:
			margin.SetInt64(1)
		case 2:
			margin.Quo(notional, big.NewInt(100))
		case 3:
			margin.Quo(notional, big.NewInt(2))
		case 4:
			margin.Set(notional)
		}
		var rate uint64
		switch i % 8 {
		case 0, 1, 2, 3, 4:
			rate = rates[i%5]
		case 5:
			rate = next() % BPS
		case 6:
			rate = BPS + next()%(maxUint64-BPS)
		default:
			rate = next()
		}
		pos := &Position{
			Side: side, EntryPrice: entryPrice, EntryNotionalNumerator: entryNumerator,
			Quantity: quantity, ClosePnLRemainder: carry,
		}
		boundary := positionConservativeLiquidationPrice(pos, margin, rate)
		name := fmt.Sprintf("deterministic-%d", i)
		for _, offset := range boundaryOffsets {
			mark := new(big.Int).Add(boundary, offset)
			assertLiquidationIndexCoversRiskyMark(t, name, pos, margin, rate, boundary, mark)
		}
		assertLiquidationIndexCoversRiskyMark(t, name, pos, margin, rate, boundary, big.NewInt(1))
		assertLiquidationIndexCoversRiskyMark(t, name, pos, margin, rate, boundary, maxPrice)
		assertLiquidationIndexCoversRiskyMark(t, name, pos, margin, rate, boundary, entryPrice)

		wideMark := new(big.Int).SetUint64(next())
		wideMark.Lsh(wideMark, 64).Add(wideMark, new(big.Int).SetUint64(next()))
		wideMark.Mod(wideMark, maxPrice).Add(wideMark, big.NewInt(1))
		assertLiquidationIndexCoversRiskyMark(t, name, pos, margin, rate, boundary, wideMark)
	}
}

func TestProcessTrade_ReopenSameSideInSameHeightDoesNotReusePositionID(t *testing.T) {
	e, positions, _, _, _ := newTestEngine()
	const pair = "DUST_USDT"
	qty := big.NewInt(1)
	price := new(big.Int).Set(PriceScale)

	if err := e.ProcessTradeForPosition("alice", pair, Long, qty, price, big.NewInt(1), 77); err != nil {
		t.Fatal(err)
	}
	first, err := positions.GetPositionByOwnerAndPairSide("alice", pair, Long)
	if err != nil || first == nil {
		t.Fatalf("first position=%v err=%v", first, err)
	}
	firstID := first.ID

	if err := e.ProcessTradeForPosition("alice", pair, Short, qty, price, new(big.Int), 77); err != nil {
		t.Fatal(err)
	}
	if err := e.ProcessTradeForPosition("alice", pair, Long, qty, price, big.NewInt(1), 77); err != nil {
		t.Fatal(err)
	}
	reopened, err := positions.GetPositionByOwnerAndPairSide("alice", pair, Long)
	if err != nil || reopened == nil {
		t.Fatalf("reopened position=%v err=%v", reopened, err)
	}
	if reopened.ID == firstID || reopened.ID != firstID+"_2" {
		t.Fatalf("same-height reopen ID=%q, first=%q", reopened.ID, firstID)
	}
	closed, err := positions.GetPosition(firstID)
	if err != nil || closed == nil || closed.Status != StatusClosed {
		t.Fatalf("first position history was retargeted: pos=%+v err=%v", closed, err)
	}
}

func TestProcessTrade_SameSideZeroIncrementRequiresAggregateMargin(t *testing.T) {
	t.Run("allows zero increment with enough existing headroom", func(t *testing.T) {
		e, ps, _, _, _ := newTestEngine()
		pos := &Position{
			ID: "long", Owner: "alice", Pair: "BTC_USDT", Side: Long,
			EntryPrice: new(big.Int).Set(PriceScale), Quantity: big.NewInt(1), Margin: big.NewInt(2),
			Status: StatusOpen, LastCumulativeFunding: new(big.Int),
		}
		_ = ps.SetPosition(pos)
		if err := e.ProcessTradeForPosition("alice", pos.Pair, Long, big.NewInt(1), new(big.Int).Set(PriceScale), new(big.Int), 2); err != nil {
			t.Fatal(err)
		}
		back, _ := ps.GetPosition(pos.ID)
		if back.Quantity.Cmp(big.NewInt(2)) != 0 || back.Margin.Cmp(big.NewInt(2)) != 0 {
			t.Fatalf("position=%+v, want qty=2 margin=2", back)
		}
	})

	t.Run("rejects zero increment when aggregate becomes under-margined", func(t *testing.T) {
		e, ps, _, _, _ := newTestEngine()
		pos := &Position{
			ID: "long", Owner: "alice", Pair: "BTC_USDT", Side: Long,
			EntryPrice: big.NewInt(199), Quantity: new(big.Int).Set(PriceScale), Margin: big.NewInt(2),
			Status: StatusOpen, LastCumulativeFunding: new(big.Int),
		}
		_ = ps.SetPosition(pos)
		err := e.ProcessTradeForPosition("alice", pos.Pair, Long, new(big.Int).Set(PriceScale), big.NewInt(199), new(big.Int), 2)
		if err == nil || !strings.Contains(err.Error(), "aggregate margin") {
			t.Fatalf("error=%v, want aggregate margin rejection", err)
		}
		back, _ := ps.GetPosition(pos.ID)
		if back.Quantity.Cmp(PriceScale) != 0 || back.Margin.Cmp(big.NewInt(2)) != 0 {
			t.Fatalf("rejected fill mutated position: %+v", back)
		}
	})
}

func TestPreviewTradeClosePnL_ClipsToOppositePosition(t *testing.T) {
	e, ps, _, _, _ := newTestEngine()
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: "BTC_USDT", Side: Long,
		EntryPrice: sc(100), Quantity: sc(1), Margin: sc(10), Status: StatusOpen,
	})

	pnl, err := e.PreviewTradeClosePnL("alice", "BTC_USDT", Short, sc(2), sc(90))
	if err != nil {
		t.Fatal(err)
	}
	if pnl.Cmp(new(big.Int).Neg(sc(10))) != 0 {
		t.Fatalf("clipped pnl=%s want=%s", pnl, new(big.Int).Neg(sc(10)))
	}
	zero, err := e.PreviewTradeClosePnL("bob", "BTC_USDT", Short, sc(1), sc(90))
	if err != nil || zero.Sign() != 0 {
		t.Fatalf("missing position preview=%s err=%v, want zero", zero, err)
	}
}

func TestApplyPendingFunding_AutoLiquidationRunsADL(t *testing.T) {
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	const pair = "BTC_USDT"
	callbackCount := 0
	e.OnFundingLiquidated = func(gotPair string, liq *LiquidationResult, adl *ADLResult) {
		callbackCount++
		if gotPair != pair {
			t.Errorf("funding liquidation callback pair=%q want=%q", gotPair, pair)
		}
		if liq == nil || liq.PositionID != "long" {
			t.Errorf("funding liquidation callback result=%+v, want long", liq)
		}
		if adl == nil || len(adl.Reduced) != 1 || adl.Reduced[0].PositionID != "short" {
			t.Errorf("funding liquidation callback ADL=%+v, want one short reduction", adl)
		}
	}
	mp.prices[pair] = sc(90)
	long := &Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(100), Quantity: sc(1), Margin: sc(5), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	}
	short := &Position{
		ID: "short", Owner: "bob", Pair: pair, Side: Short,
		EntryPrice: sc(110), Quantity: sc(1), Margin: sc(20), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	}
	_ = ps.SetPosition(long)
	_ = ps.SetPosition(short)
	                                                                        
	                                                      
	_ = cf.SetCumulativeFunding(pair, big.NewInt(1000), new(big.Int))

	if err := e.ApplyPendingFundingForPair(pair); err != nil {
		t.Fatal(err)
	}
	if callbackCount != 1 {
		t.Fatalf("funding liquidation callback count=%d, want exactly 1", callbackCount)
	}
	longBack, _ := ps.GetPosition(long.ID)
	if longBack.Status != StatusLiquidated || longBack.Quantity.Sign() != 0 || longBack.Margin.Sign() != 0 {
		t.Fatalf("funding-killed position not terminal: %+v", longBack)
	}
	shortBack, _ := ps.GetPosition(short.ID)
	wantQty := new(big.Int).Quo(sc(1), big.NewInt(2))
	if shortBack.Status != StatusOpen || shortBack.Quantity.Cmp(wantQty) != 0 {
		t.Fatalf("ADL did not cover funding liquidation: %+v", shortBack)
	}
	bobBalance, _ := ba.GetAvailableBalance("bob", "USDT")
	if bobBalance.Cmp(sc(15)) != 0 {
		t.Fatalf("released ADL margin/profit=%s want=%s", bobBalance, sc(15))
	}
	if shortBack.RealizedPnL.Cmp(sc(5)) != 0 || shortBack.ADLWithheldPnL.Cmp(sc(5)) != 0 {
		t.Fatalf("ADL profit split paid/withheld=%s/%s, want %s/%s",
			shortBack.RealizedPnL, shortBack.ADLWithheldPnL, sc(5), sc(5))
	}
	shortfall, _ := ins.GetShortfall(pair)
	if shortfall.Sign() != 0 {
		t.Fatalf("fully covered liquidation recorded shortfall=%s", shortfall)
	}
}

func TestProcessTrade_BankruptReverseHandlesADLOrResidual(t *testing.T) {
	setup := func(withCandidate bool) (*Engine, *mockPositionStore, *mockInsuranceFund, *mockBalanceAccessor) {
		e, ps, ins, ba, mp, _ := newTestEngineFull()
		const pair = "BTC_USDT"
		mp.prices[pair] = sc(90)
		_ = ps.SetPosition(&Position{
			ID: "long", Owner: "alice", Pair: pair, Side: Long,
			EntryPrice: sc(100), Quantity: sc(1), Margin: sc(5), Status: StatusOpen,
			LastCumulativeFunding: new(big.Int),
		})
		if withCandidate {
			_ = ps.SetPosition(&Position{
				ID: "short", Owner: "bob", Pair: pair, Side: Short,
				EntryPrice: sc(110), Quantity: sc(1), Margin: sc(20), Status: StatusOpen,
				LastCumulativeFunding: new(big.Int),
			})
		}
		return e, ps, ins, ba
	}

	t.Run("executes ADL", func(t *testing.T) {
		e, ps, ins, _ := setup(true)
		if err := e.ProcessTradeForPosition("alice", "BTC_USDT", Short, sc(1), sc(90), new(big.Int), 2); err != nil {
			t.Fatal(err)
		}
		liquidated, _ := ps.GetPosition("long")
		candidate, _ := ps.GetPosition("short")
		wantCandidateQty := new(big.Int).Quo(new(big.Int).Mul(sc(1), big.NewInt(3)), big.NewInt(4))
		if liquidated.Status != StatusLiquidated || candidate.Quantity.Cmp(wantCandidateQty) != 0 {
			t.Fatalf("unexpected liquidation/adl state: liquidated=%+v candidate=%+v", liquidated, candidate)
		}
		shortfall, _ := ins.GetShortfall("BTC_USDT")
		if shortfall.Sign() != 0 {
			t.Fatalf("covered reverse liquidation left shortfall=%s", shortfall)
		}
	})

	t.Run("records residual without candidate", func(t *testing.T) {
		e, ps, ins, _ := setup(false)
		if err := e.ProcessTradeForPosition("alice", "BTC_USDT", Short, sc(1), sc(90), new(big.Int), 2); err != nil {
			t.Fatal(err)
		}
		liquidated, _ := ps.GetPosition("long")
		if liquidated.Status != StatusLiquidated {
			t.Fatalf("position status=%v want liquidated", liquidated.Status)
		}
		shortfall, _ := ins.GetShortfall("BTC_USDT")
		if shortfall.Cmp(sc(5)) != 0 {
			t.Fatalf("residual shortfall=%s want=%s", shortfall, sc(5))
		}
	})
}

func TestProcessTrade_FragmentBankruptcyDoesNotPanic(t *testing.T) {
	e, ps, _, _, _ := newTestEngine()
	const pair = "BTC_USDT"
	_ = ps.SetPosition(&Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: new(big.Int).Set(PriceScale), Quantity: big.NewInt(3), Margin: big.NewInt(1),
		Status: StatusOpen, LastCumulativeFunding: new(big.Int),
	})

	if err := e.ProcessTradeForPosition("alice", pair, Short, big.NewInt(2), new(big.Int).Quo(PriceScale, big.NewInt(2)), new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	back, _ := ps.GetPosition("long")
	if back.Status != StatusOpen || back.Quantity.Cmp(big.NewInt(1)) != 0 || back.Margin.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("fragment liquidation state=%+v, want open qty=1 margin=1", back)
	}
}

func setupFundingDeathTest(t *testing.T) (*Engine, *mockPositionStore, *mockInsuranceFund, *mockBalanceAccessor, *Position) {
	t.Helper()
	e, ps, ins, ba, mp, cf := newTestEngineFull()
	const pair = "BTC_USDT"
	mp.prices[pair] = sc(100)
	pos := &Position{
		ID: "long", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: sc(100), Quantity: sc(1), Margin: sc(5), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	}
	_ = ps.SetPosition(pos)
	_ = cf.SetCumulativeFunding(pair, big.NewInt(500), new(big.Int))
	return e, ps, ins, ba, pos
}

func TestFundingTerminalPositionCannotBeMutatedByCaller(t *testing.T) {
	t.Run("trade", func(t *testing.T) {
		e, ps, _, _, pos := setupFundingDeathTest(t)
		err := e.ProcessTradeForPosition(pos.Owner, pos.Pair, Long, sc(1), sc(100), sc(10), 2)
		if err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Fatalf("error=%v want terminal rejection", err)
		}
		back, _ := ps.GetPosition(pos.ID)
		if back.Status != StatusLiquidated || back.Quantity.Sign() != 0 || back.Margin.Sign() != 0 {
			t.Fatalf("dead position was modified: %+v", back)
		}
	})

	t.Run("add margin", func(t *testing.T) {
		e, ps, _, ba, pos := setupFundingDeathTest(t)
		ba.balances[balKey(pos.Owner, "USDT")] = sc(20)
		err := e.AddMargin(pos.ID, sc(10))
		if err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Fatalf("error=%v want terminal rejection", err)
		}
		back, _ := ps.GetPosition(pos.ID)
		balance, _ := ba.GetAvailableBalance(pos.Owner, "USDT")
		if back.Status != StatusLiquidated || back.Margin.Sign() != 0 || balance.Cmp(sc(20)) != 0 {
			t.Fatalf("add-margin mutated dead position/balance: pos=%+v balance=%s", back, balance)
		}
	})

	t.Run("remove margin", func(t *testing.T) {
		e, ps, _, ba, pos := setupFundingDeathTest(t)
		err := e.RemoveMargin(pos.ID, sc(1))
		if err == nil || !strings.Contains(err.Error(), "terminal") {
			t.Fatalf("error=%v want terminal rejection", err)
		}
		back, _ := ps.GetPosition(pos.ID)
		balance, _ := ba.GetAvailableBalance(pos.Owner, "USDT")
		if back.Status != StatusLiquidated || back.Margin.Sign() != 0 || balance.Sign() != 0 {
			t.Fatalf("remove-margin mutated dead position/balance: pos=%+v balance=%s", back, balance)
		}
	})
}

type failingADLPositionStore struct {
	*mockPositionStore
	failSet bool
}

func (s *failingADLPositionStore) SetPosition(pos *Position) error {
	if s.failSet {
		return fmt.Errorf("injected SetPosition failure")
	}
	return s.mockPositionStore.SetPosition(pos)
}

type failingADLBalance struct {
	*mockBalanceAccessor
	failAdd bool
}

func (b *failingADLBalance) AddBalance(owner, token string, amount *big.Int) error {
	if b.failAdd {
		return fmt.Errorf("injected AddBalance failure")
	}
	return b.mockBalanceAccessor.AddBalance(owner, token, amount)
}

type failingADLInsurance struct {
	*mockInsuranceFund
	failShortfall bool
}

func (i *failingADLInsurance) AddShortfall(pair string, amount *big.Int) error {
	if i.failShortfall {
		return fmt.Errorf("injected AddShortfall failure")
	}
	return i.mockInsuranceFund.AddShortfall(pair, amount)
}

func TestExecuteADL_WriteFailuresDoNotReportOrRetainCoverage(t *testing.T) {
	setup := func(entry int64) (*Engine, *mockPositionStore, *failingADLPositionStore, *failingADLBalance, *failingADLInsurance) {
		baseStore := newMockPositionStore()
		store := &failingADLPositionStore{mockPositionStore: baseStore}
		balance := &failingADLBalance{mockBalanceAccessor: newMockBalanceAccessor()}
		insurance := &failingADLInsurance{mockInsuranceFund: newMockInsuranceFund()}
		_ = insurance.SetBalance("BTC_USDT", sc(100))
		mark := newMockMarkPrice()
		mark.prices["BTC_USDT"] = sc(90)
		e := NewEngine(store, insurance, balance, mark, newMockCumulativeFunding(), nil)
		_ = baseStore.SetPosition(&Position{
			ID: "short", Owner: "bob", Pair: "BTC_USDT", Side: Short,
			EntryPrice: sc(entry), Quantity: sc(1), Margin: sc(10), Status: StatusOpen,
			RealizedPnL: new(big.Int), ClosedQty: new(big.Int), ExitNotional: new(big.Int),
		})
		return e, baseStore, store, balance, insurance
	}

	assertOriginal := func(t *testing.T, ps *mockPositionStore, balance *failingADLBalance, insurance *failingADLInsurance) {
		t.Helper()
		back, _ := ps.GetPosition("short")
		if back.Status != StatusOpen || back.Quantity.Cmp(sc(1)) != 0 || back.Margin.Cmp(sc(10)) != 0 {
			t.Fatalf("failed ADL retained position mutation: %+v", back)
		}
		bob, _ := balance.GetAvailableBalance("bob", "USDT")
		if bob.Sign() != 0 {
			t.Fatalf("failed ADL retained balance credit=%s", bob)
		}
		fund, _ := insurance.GetBalance("BTC_USDT")
		if fund.Cmp(sc(100)) != 0 {
			t.Fatalf("failed ADL retained insurance payout=%s, want %s", fund, sc(100))
		}
	}

	t.Run("AddBalance", func(t *testing.T) {
		e, ps, _, balance, insurance := setup(110)
		balance.failAdd = true
		result, err := e.ExecuteADL("BTC_USDT", Short, sc(5))
		if err == nil || result != nil {
			t.Fatalf("result=%+v err=%v, want nil/error", result, err)
		}
		assertOriginal(t, ps, balance, insurance)
	})

	t.Run("SetPosition", func(t *testing.T) {
		e, ps, store, balance, insurance := setup(110)
		store.failSet = true
		result, err := e.ExecuteADL("BTC_USDT", Short, sc(5))
		if err == nil || result != nil {
			t.Fatalf("result=%+v err=%v, want nil/error", result, err)
		}
		assertOriginal(t, ps, balance, insurance)
	})

	t.Run("AddShortfall rolls back earlier reductions", func(t *testing.T) {
		                                                                         
		                                                         
		e, ps, _, balance, insurance := setup(95)
		insurance.failShortfall = true
		result, err := e.ExecuteADL("BTC_USDT", Short, sc(10))
		if err == nil || result != nil {
			t.Fatalf("result=%+v err=%v, want nil/error", result, err)
		}
		assertOriginal(t, ps, balance, insurance)
	})
}

func TestProcessTrade_ExactEntryNotionalMakesSplitAndOrderInvariant(t *testing.T) {
	permutations := [][]int64{
		{1, 2, 3}, {1, 3, 2}, {2, 1, 3},
		{2, 3, 1}, {3, 1, 2}, {3, 2, 1},
	}
	for _, prices := range permutations {
		name := fmt.Sprintf("%d-%d-%d", prices[0], prices[1], prices[2])
		t.Run(name, func(t *testing.T) {
			e, ps, insurance, balance, _ := newTestEngine()
			const pair = "DUST_USDT"
			qty := new(big.Int).Set(PriceScale)

			for i, price := range prices {
				margin := new(big.Int)
				if i == 0 {
					margin.SetInt64(1)
				}
				if err := e.ProcessTradeForPosition("alice", pair, Long, qty, big.NewInt(price), margin, uint64(i+1)); err != nil {
					t.Fatalf("open fill %d @ %d: %v", i, price, err)
				}
			}

			pos, err := ps.GetPositionByOwnerAndPairSide("alice", pair, Long)
			if err != nil || pos == nil {
				t.Fatalf("load open position: pos=%v err=%v", pos, err)
			}
			wantNumerator := new(big.Int).Mul(big.NewInt(6), PriceScale)
			if pos.EntryPrice.Cmp(big.NewInt(2)) != 0 || pos.EntryNotionalNumerator.Cmp(wantNumerator) != 0 {
				t.Fatalf("entry price/numerator=%s/%s, want 2/%s", pos.EntryPrice, pos.EntryNotionalNumerator, wantNumerator)
			}
			positionID := pos.ID

			                                                                     
			                                                                  
			                                  
			for i := 0; i < 3; i++ {
				if err := e.ProcessTradeForPosition("alice", pair, Short, qty, big.NewInt(2), new(big.Int), uint64(10+i)); err != nil {
					t.Fatalf("close fragment %d: %v", i, err)
				}
			}

			closed, err := ps.GetPosition(positionID)
			if err != nil || closed == nil {
				t.Fatalf("load closed position: pos=%v err=%v", closed, err)
			}
			if closed.Status != StatusClosed || closed.RealizedPnL.Sign() != 0 || closed.EntryNotionalNumerator.Sign() != 0 {
				t.Fatalf("closed position=%+v, want closed with zero pnl/entry numerator", closed)
			}
			insuranceBalance, _ := insurance.GetBalance(pair)
			userBalance, _ := balance.GetAvailableBalance("alice", "USDT")
			if insuranceBalance.Sign() != 0 || userBalance.Cmp(big.NewInt(1)) != 0 {
				t.Fatalf("money drift: insurance=%s user=%s, want 0/1", insuranceBalance, userBalance)
			}
		})
	}
}

func TestProcessTrade_LegacyEntryNotionalFallbackIsDeterministic(t *testing.T) {
	e, ps, _, _, _ := newTestEngine()
	const pair = "LEGACY_USDT"
	legacy := &Position{
		ID: "legacy", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: big.NewInt(7), Quantity: big.NewInt(3), Margin: big.NewInt(100), Status: StatusOpen,
		LastCumulativeFunding: new(big.Int),
	}
	_ = ps.SetPosition(legacy)

	if err := e.ProcessTradeForPosition("alice", pair, Long, big.NewInt(1), big.NewInt(10), new(big.Int), 2); err != nil {
		t.Fatal(err)
	}
	if legacy.EntryNotionalNumerator.Cmp(big.NewInt(31)) != 0 || legacy.EntryPrice.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("legacy add produced entry=%s numerator=%s, want 7/31", legacy.EntryPrice, legacy.EntryNotionalNumerator)
	}

	if err := e.ProcessTradeForPosition("alice", pair, Short, big.NewInt(1), big.NewInt(7), new(big.Int), 3); err != nil {
		t.Fatal(err)
	}
	if legacy.Quantity.Cmp(big.NewInt(3)) != 0 || legacy.EntryNotionalNumerator.Cmp(big.NewInt(23)) != 0 || legacy.EntryPrice.Cmp(big.NewInt(7)) != 0 {
		t.Fatalf("legacy partial close produced qty/entry/numerator=%s/%s/%s, want 3/7/23",
			legacy.Quantity, legacy.EntryPrice, legacy.EntryNotionalNumerator)
	}
}

func TestEntryNotionalPortion_FragmentationIsAssociativeForBothSides(t *testing.T) {
	for _, side := range []Side{Long, Short} {
		for quantity := int64(3); quantity <= 8; quantity++ {
			for base := int64(1); base <= 3; base++ {
				for remainder := int64(0); remainder < quantity; remainder++ {
					total := base*quantity + remainder
					for first := int64(1); first < quantity; first++ {
						for second := int64(1); first+second < quantity; second++ {
							makePosition := func() *Position {
								return &Position{
									Side: side, EntryPrice: big.NewInt(total / quantity),
									EntryNotionalNumerator: big.NewInt(total), Quantity: big.NewInt(quantity),
								}
							}

							fragmented := makePosition()
							reduceEntryNotional(fragmented, big.NewInt(first))
							fragmented.Quantity.Sub(fragmented.Quantity, big.NewInt(first))
							reduceEntryNotional(fragmented, big.NewInt(second))
							fragmented.Quantity.Sub(fragmented.Quantity, big.NewInt(second))

							combined := makePosition()
							reduceEntryNotional(combined, big.NewInt(first+second))
							combined.Quantity.Sub(combined.Quantity, big.NewInt(first+second))

							if fragmented.EntryNotionalNumerator.Cmp(combined.EntryNotionalNumerator) != 0 ||
								fragmented.EntryPrice.Cmp(combined.EntryPrice) != 0 {
								t.Fatalf("side=%d total=%d qty=%d close=%d+%d fragmented=%s/%s combined=%s/%s",
									side, total, quantity, first, second,
									fragmented.EntryNotionalNumerator, fragmented.EntryPrice,
									combined.EntryNotionalNumerator, combined.EntryPrice)
							}
						}
					}
				}
			}
		}
	}
}

func TestMarginRelease_FragmentationUsesStableCumulativeBasis(t *testing.T) {
	for margin := int64(1); margin <= 8; margin++ {
		for quantity := int64(3); quantity <= 8; quantity++ {
			for first := int64(1); first < quantity; first++ {
				for second := int64(1); first+second < quantity; second++ {
					makePosition := func() *Position {
						return &Position{Margin: big.NewInt(margin), Quantity: big.NewInt(quantity)}
					}

					fragmented := makePosition()
					firstRelease := previewMarginRelease(fragmented, big.NewInt(first))
					advanceMarginBasis(fragmented, big.NewInt(first))
					fragmented.Margin.Sub(fragmented.Margin, firstRelease)
					fragmented.Quantity.Sub(fragmented.Quantity, big.NewInt(first))
					secondRelease := previewMarginRelease(fragmented, big.NewInt(second))
					fragmentedRelease := new(big.Int).Add(firstRelease, secondRelease)

					combined := makePosition()
					combinedRelease := previewMarginRelease(combined, big.NewInt(first+second))
					if fragmentedRelease.Cmp(combinedRelease) != 0 {
						t.Fatalf("margin=%d qty=%d close=%d+%d fragmented=%s combined=%s basis=%s/%s",
							margin, quantity, first, second, fragmentedRelease, combinedRelease,
							fragmented.MarginBasisAmount, fragmented.MarginBasisClosed)
					}
				}
			}
		}
	}
}

func TestReducePosition_FragmentedMarginReleaseMatchesOneShot(t *testing.T) {
	type result struct {
		balance, margin, quantity *big.Int
	}
	run := func(parts ...int64) result {
		e, ps, _, balance, _ := newTestEngine()
		const pair = "MARGIN_USDT"
		pos := &Position{
			ID: "margin", Owner: "alice", Pair: pair, Side: Long,
			EntryPrice:             new(big.Int).Set(PriceScale),
			EntryNotionalNumerator: new(big.Int).Mul(new(big.Int).Set(PriceScale), big.NewInt(4)),
			Quantity:               big.NewInt(4), Margin: big.NewInt(2), Status: StatusOpen,
			LastCumulativeFunding: new(big.Int),
		}
		if err := ps.SetPosition(pos); err != nil {
			t.Fatal(err)
		}
		for i, part := range parts {
			if err := e.ProcessTradeForPosition("alice", pair, Short, big.NewInt(part), new(big.Int).Set(PriceScale), new(big.Int), uint64(i+1)); err != nil {
				t.Fatalf("close part %d: %v", i, err)
			}
		}
		back, _ := ps.GetPosition(pos.ID)
		available, _ := balance.GetAvailableBalance("alice", "USDT")
		return result{available, new(big.Int).Set(back.Margin), new(big.Int).Set(back.Quantity)}
	}

	fragmented := run(1, 1)
	combined := run(2)
	if fragmented.balance.Cmp(big.NewInt(1)) != 0 || fragmented.margin.Cmp(big.NewInt(1)) != 0 || fragmented.quantity.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("fragmented balance/margin/qty=%s/%s/%s, want 1/1/2", fragmented.balance, fragmented.margin, fragmented.quantity)
	}
	if fragmented.balance.Cmp(combined.balance) != 0 || fragmented.margin.Cmp(combined.margin) != 0 || fragmented.quantity.Cmp(combined.quantity) != 0 {
		t.Fatalf("fragmented=%s/%s/%s combined=%s/%s/%s",
			fragmented.balance, fragmented.margin, fragmented.quantity,
			combined.balance, combined.margin, combined.quantity)
	}
}

func TestLiquidation_ExactEntryCostProducesShortfall(t *testing.T) {
	e, ps, _, _, _ := newTestEngine()
	const pair = "EXACT_USDT"
	price := new(big.Int).Set(PriceScale)
	quantity := new(big.Int).Mul(big.NewInt(2), PriceScale)
	entryNumerator := new(big.Int).Mul(price, quantity)
	entryNumerator.Add(entryNumerator, price)
	pos := &Position{
		ID: "exact_liq", Owner: "alice", Pair: pair, Side: Long,
		EntryPrice: price, EntryNotionalNumerator: entryNumerator,
		Quantity: quantity, Margin: big.NewInt(2), Status: StatusOpen,
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	healthMargin := big.NewInt(3)
	mark := new(big.Int).Sub(new(big.Int).Set(price), big.NewInt(1))
	if !ShouldLiquidatePosition(pos, healthMargin, mark, 0) {
		t.Fatal("exact health check missed the zero-equity boundary")
	}
	if ShouldLiquidate(healthMargin, pos.Side, pos.EntryPrice, mark, pos.Quantity, 0) {
		t.Fatal("test precondition failed: rounded legacy health check must remain above zero equity")
	}
	cfg, err := e.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.LiquidateAtPrice(pos, mark, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.NeedsADL || result.ADLShortfall.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("liquidation result=%+v, want exact ADL shortfall 1", result)
	}
}

func TestExecuteADL_ExactEntryCostDoesNotOverstateCoverage(t *testing.T) {
	e, ps, insurance, _, markStore := newTestEngine()
	const pair = "EXACT_USDT"
	price := new(big.Int).Set(PriceScale)
	quantity := new(big.Int).Mul(big.NewInt(2), PriceScale)
	entryNumerator := new(big.Int).Mul(price, quantity)
	entryNumerator.Add(entryNumerator, price)
	pos := &Position{
		ID: "exact_adl", Owner: "bob", Pair: pair, Side: Long,
		EntryPrice: price, EntryNotionalNumerator: entryNumerator,
		Quantity: quantity, Margin: big.NewInt(10), Status: StatusOpen,
	}
	if err := ps.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	markStore.prices[pair] = new(big.Int).Add(new(big.Int).Set(price), big.NewInt(1))
	_ = insurance.SetBalance(pair, big.NewInt(1))

	result, err := e.ExecuteADL(pair, Long, big.NewInt(2))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.TotalCovered.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("ADL result=%+v, want exact coverage 1", result)
	}
	shortfall, _ := insurance.GetShortfall(pair)
	if shortfall.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("residual shortfall=%s, want 1", shortfall)
	}
}

func TestBuildView_UsesExactEntryCostAndCloseCarry(t *testing.T) {
	price := new(big.Int).Set(PriceScale)
	quantity := new(big.Int).Mul(big.NewInt(2), PriceScale)
	entryNumerator := new(big.Int).Mul(price, quantity)
	entryNumerator.Add(entryNumerator, price)
	pos := &Position{
		Side: Long, EntryPrice: price, EntryNotionalNumerator: entryNumerator,
		Quantity: quantity, Margin: big.NewInt(2), Status: StatusOpen,
	}
	mark := new(big.Int).Sub(new(big.Int).Set(price), big.NewInt(1))
	cfg := &MarketConfig{
		Pair: "EXACT_USDT", MaintenanceMarginRateBps: DefaultMaintenanceMarginRateBps,
	}
	view := BuildView(pos, mark, new(big.Int), new(big.Int), new(big.Int), new(big.Int), cfg)
	if view.UnrealizedPnL.Cmp(big.NewInt(-3)) != 0 || view.Equity.Cmp(big.NewInt(-1)) != 0 {
		t.Fatalf("exact view pnl/equity=%s/%s, want -3/-1", view.UnrealizedPnL, view.Equity)
	}

	                                                                           
	                                                                               
	pos.ClosePnLRemainder = big.NewInt(1)
	view = BuildView(pos, mark, new(big.Int), new(big.Int), new(big.Int), new(big.Int), cfg)
	if view.UnrealizedPnL.Cmp(big.NewInt(-2)) != 0 || view.Equity.Sign() != 0 {
		t.Fatalf("carried view pnl/equity=%s/%s, want -2/0", view.UnrealizedPnL, view.Equity)
	}
}

func TestLiquidatePartial_FragmentationCarriesExactPnLDust(t *testing.T) {
	setup := func(t *testing.T) (*Engine, *mockPositionStore, *Position, *MarketConfig) {
		t.Helper()
		e, positions, _, _, _ := newTestEngine()
		const pair = "LIQ_DUST_USDT"
		pos := &Position{
			ID: "liq_dust", Owner: "alice", Pair: pair, Side: Long,
			EntryPrice: big.NewInt(260_000_000), EntryNotionalNumerator: big.NewInt(520_000_000),
			Quantity: big.NewInt(2), Margin: big.NewInt(1), Status: StatusOpen,
		}
		if err := positions.SetPosition(pos); err != nil {
			t.Fatal(err)
		}
		cfg, err := e.GetMarketConfig(pair)
		if err != nil {
			t.Fatal(err)
		}
		return e, positions, pos, cfg
	}
	closePrice := new(big.Int).Set(PriceScale)

	fullEngine, _, fullPos, fullCfg := setup(t)
	full, err := fullEngine.LiquidateAtPrice(fullPos, closePrice, fullCfg)
	if err != nil {
		t.Fatal(err)
	}
	if full.ADLShortfall.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("one-shot shortfall=%s, want 2", full.ADLShortfall)
	}

	splitEngine, splitStore, splitPos, splitCfg := setup(t)
	first, err := splitEngine.LiquidatePartial(splitPos, closePrice, big.NewInt(1), splitCfg)
	if err != nil {
		t.Fatal(err)
	}
	remaining, _ := splitStore.GetPosition(splitPos.ID)
	if remaining.ClosePnLRemainder.Cmp(big.NewInt(-60_000_000)) != 0 {
		t.Fatalf("first liquidation remainder=%s, want -60000000", remaining.ClosePnLRemainder)
	}
	second, err := splitEngine.LiquidatePartial(remaining, closePrice, big.NewInt(1), splitCfg)
	if err != nil {
		t.Fatal(err)
	}
	total := new(big.Int).Add(first.ADLShortfall, second.ADLShortfall)
	if total.Cmp(full.ADLShortfall) != 0 {
		t.Fatalf("fragmented shortfall=%s+%s=%s, one-shot=%s", first.ADLShortfall, second.ADLShortfall, total, full.ADLShortfall)
	}
}

func TestExecuteADL_FragmentationCarriesExactPnLDust(t *testing.T) {
	setup := func(t *testing.T) (*Engine, *mockPositionStore, *mockInsuranceFund, *mockMarkPrice) {
		t.Helper()
		e, positions, insurance, _, marks := newTestEngine()
		const pair = "ADL_DUST_USDT"
		pos := &Position{
			ID: "adl_dust", Owner: "bob", Pair: pair, Side: Short,
			EntryPrice: big.NewInt(260_000_000), EntryNotionalNumerator: big.NewInt(520_000_000),
			Quantity: big.NewInt(2), Margin: big.NewInt(2), Status: StatusOpen,
		}
		if err := positions.SetPosition(pos); err != nil {
			t.Fatal(err)
		}
		marks.prices[pair] = new(big.Int).Set(PriceScale)
		_ = insurance.SetBalance(pair, big.NewInt(3))
		return e, positions, insurance, marks
	}

	fullEngine, _, fullInsurance, _ := setup(t)
	full, err := fullEngine.ExecuteADL("ADL_DUST_USDT", Short, big.NewInt(3))
	if err != nil {
		t.Fatal(err)
	}
	if full == nil || full.TotalCovered.Cmp(big.NewInt(3)) != 0 {
		t.Fatalf("one-shot ADL=%+v, want coverage 3", full)
	}
	fullResidual, _ := fullInsurance.GetShortfall("ADL_DUST_USDT")
	if fullResidual.Sign() != 0 {
		t.Fatalf("one-shot residual=%s, want 0", fullResidual)
	}

	splitEngine, splitStore, splitInsurance, _ := setup(t)
	first, err := splitEngine.ExecuteADL("ADL_DUST_USDT", Short, big.NewInt(1))
	if err != nil {
		t.Fatal(err)
	}
	remaining, _ := splitStore.GetPosition("adl_dust")
	if remaining.ClosePnLRemainder.Cmp(big.NewInt(60_000_000)) != 0 {
		t.Fatalf("first ADL remainder=%s, want 60000000", remaining.ClosePnLRemainder)
	}
	second, err := splitEngine.ExecuteADL("ADL_DUST_USDT", Short, big.NewInt(2))
	if err != nil {
		t.Fatal(err)
	}
	total := new(big.Int).Add(first.TotalCovered, second.TotalCovered)
	if total.Cmp(full.TotalCovered) != 0 {
		t.Fatalf("fragmented ADL coverage=%s+%s=%s, one-shot=%s", first.TotalCovered, second.TotalCovered, total, full.TotalCovered)
	}
	residual, _ := splitInsurance.GetShortfall("ADL_DUST_USDT")
	if residual.Sign() != 0 {
		t.Fatalf("fragmented residual=%s, want 0", residual)
	}
}

func TestProcessTrade_ReverseRemainderMarginRoundsUp(t *testing.T) {
	e, positions, _, balance, _ := newTestEngine()
	const pair = "REVERSE_USDT"
	price := new(big.Int).Mul(big.NewInt(101), PriceScale)
	short := &Position{
		ID: "short", Owner: "alice", Pair: pair, Side: Short,
		EntryPrice: new(big.Int).Set(price), EntryNotionalNumerator: new(big.Int).Set(price),
		Quantity: big.NewInt(1), Margin: big.NewInt(10), Status: StatusOpen,
	}
	if err := positions.SetPosition(short); err != nil {
		t.Fatal(err)
	}

	                                                                         
	                                                                                    
	if err := e.ProcessTradeForPosition("alice", pair, Long, big.NewInt(2), price, big.NewInt(3), 7); err != nil {
		t.Fatal(err)
	}
	opened, err := positions.GetPositionByOwnerAndPairSide("alice", pair, Long)
	if err != nil || opened == nil {
		t.Fatalf("load reversed position: pos=%v err=%v", opened, err)
	}
	if opened.Quantity.Cmp(big.NewInt(1)) != 0 || opened.Margin.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("reversed qty/margin=%s/%s, want 1/2", opened.Quantity, opened.Margin)
	}
	available, _ := balance.GetAvailableBalance("alice", "USDT")
	if available.Cmp(big.NewInt(11)) != 0 {
		t.Fatalf("old margin plus reverse refund=%s, want 11", available)
	}
}
