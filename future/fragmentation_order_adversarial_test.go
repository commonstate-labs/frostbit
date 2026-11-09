package future

import (
	"fmt"
	"math/big"
	"testing"
)

type fragmentedCloseOutcome struct {
	status            PositionStatus
	quantity          *big.Int
	margin            *big.Int
	realizedPnL       *big.Int
	closedQty         *big.Int
	exitNotional      *big.Int
	closePnLRemainder *big.Int
	available         *big.Int
	insurance         *big.Int
	shortfall         *big.Int
}

func runFragmentedCloseOutcome(t *testing.T, side Side, profitable bool, fragments int) fragmentedCloseOutcome {
	t.Helper()

	e, positions, insurance, balance, _, _ := newTestEngineFull()
	const pair = "FRAGMENT_STRESS_USDT"
	quantity := big.NewInt(1025)
	entryPrice := new(big.Int).Mul(big.NewInt(2), PriceScale)
	closePrice := new(big.Int)
	if side == Long {
		if profitable {
			closePrice.Add(entryPrice, PriceScale)
		} else {
			closePrice.Sub(entryPrice, PriceScale)
		}
	} else {
		if profitable {
			closePrice.Sub(entryPrice, PriceScale)
		} else {
			closePrice.Add(entryPrice, PriceScale)
		}
	}
	margin := big.NewInt(2051)
	pos := &Position{
		ID:                     "fragmented-position",
		Owner:                  "alice",
		Pair:                   pair,
		Side:                   side,
		EntryPrice:             new(big.Int).Set(entryPrice),
		EntryNotionalNumerator: new(big.Int).Mul(entryPrice, quantity),
		Quantity:               new(big.Int).Set(quantity),
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
		MaxQuantity:            new(big.Int).Set(quantity),
		InitialMargin:          new(big.Int).Set(margin),
	}
	if err := positions.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	if profitable {
		if err := insurance.SetBalance(pair, big.NewInt(2000)); err != nil {
			t.Fatal(err)
		}
	}

	remaining := new(big.Int).Set(quantity)
	for i := 0; i < fragments; i++ {
		partsLeft := int64(fragments - i)
		part := new(big.Int).Quo(remaining, big.NewInt(partsLeft))
		if part.Sign() <= 0 {
			t.Fatalf("fragment %d/%d became zero with remaining quantity %s", i, fragments, remaining)
		}
		if err := e.ProcessTradeForPosition(
			"alice", pair, side.Opposite(), part, closePrice, new(big.Int), uint64(i+1),
		); err != nil {
			t.Fatalf("close fragment %d/%d qty=%s: %v", i+1, fragments, part, err)
		}
		remaining.Sub(remaining, part)
	}
	if remaining.Sign() != 0 {
		t.Fatalf("fragment schedule left quantity %s", remaining)
	}

	closed, err := positions.GetPosition(pos.ID)
	if err != nil || closed == nil {
		t.Fatalf("load closed position: pos=%+v err=%v", closed, err)
	}
	available, _ := balance.GetAvailableBalance("alice", "USDT")
	insuranceBalance, _ := insurance.GetBalance(pair)
	shortfall, _ := insurance.GetShortfall(pair)
	return fragmentedCloseOutcome{
		status:            closed.Status,
		quantity:          new(big.Int).Set(closed.Quantity),
		margin:            new(big.Int).Set(closed.Margin),
		realizedPnL:       new(big.Int).Set(closed.RealizedPnL),
		closedQty:         new(big.Int).Set(closed.ClosedQty),
		exitNotional:      new(big.Int).Set(closed.ExitNotional),
		closePnLRemainder: new(big.Int).Set(closed.ClosePnLRemainder),
		available:         available,
		insurance:         insuranceBalance,
		shortfall:         shortfall,
	}
}

func TestReducePosition_ThousandTinyFragmentsMatchOneShot(t *testing.T) {
	for _, side := range []Side{Long, Short} {
		for _, profitable := range []bool{false, true} {
			name := fmt.Sprintf("side_%d/profitable_%t", side, profitable)
			t.Run(name, func(t *testing.T) {
				oneShot := runFragmentedCloseOutcome(t, side, profitable, 1)
				fragmented := runFragmentedCloseOutcome(t, side, profitable, 1025)

				if oneShot.status != StatusClosed || fragmented.status != StatusClosed ||
					oneShot.quantity.Sign() != 0 || fragmented.quantity.Sign() != 0 ||
					oneShot.margin.Sign() != 0 || fragmented.margin.Sign() != 0 ||
					oneShot.closePnLRemainder.Sign() != 0 || fragmented.closePnLRemainder.Sign() != 0 {
					t.Fatalf("terminal state differs: one-shot=%+v fragmented=%+v", oneShot, fragmented)
				}
				for field, values := range map[string][2]*big.Int{
					"realized pnl":  {oneShot.realizedPnL, fragmented.realizedPnL},
					"closed qty":    {oneShot.closedQty, fragmented.closedQty},
					"exit notional": {oneShot.exitNotional, fragmented.exitNotional},
					"available":     {oneShot.available, fragmented.available},
					"insurance":     {oneShot.insurance, fragmented.insurance},
					"shortfall":     {oneShot.shortfall, fragmented.shortfall},
				} {
					if values[0].Cmp(values[1]) != 0 {
						t.Fatalf("%s depends on fragmentation: one-shot=%s fragmented=%s", field, values[0], values[1])
					}
				}
				wantPnL := big.NewInt(1025)
				if !profitable {
					wantPnL.Neg(wantPnL)
				}
				if fragmented.realizedPnL.Cmp(wantPnL) != 0 {
					t.Fatalf("realized pnl=%s, want %s", fragmented.realizedPnL, wantPnL)
				}
			})
		}
	}
}

func TestReducePosition_PnLCarryCrossesPriceScaleBoundaryForBothSides(t *testing.T) {
	for _, side := range []Side{Long, Short} {
		for _, sign := range []int64{-1, 1} {
			name := fmt.Sprintf("side_%d/sign_%+d", side, sign)
			t.Run(name, func(t *testing.T) {
				e, positions, insurance, balance, _, _ := newTestEngineFull()
				const pair = "PNL_BOUNDARY_USDT"
				entryPrice := new(big.Int).Mul(big.NewInt(2), PriceScale)
				closePrice := new(big.Int).Set(entryPrice)
				if side == Long {
					closePrice.Add(closePrice, big.NewInt(sign))
				} else {
					closePrice.Sub(closePrice, big.NewInt(sign))
				}
				carry := new(big.Int).Sub(PriceScale, big.NewInt(1))
				carry.Mul(carry, big.NewInt(sign))
				pos := &Position{
					ID: "boundary-position", Owner: "alice", Pair: pair, Side: side,
					EntryPrice: entryPrice, EntryNotionalNumerator: new(big.Int).Mul(entryPrice, big.NewInt(2)),
					Quantity: big.NewInt(2), Margin: big.NewInt(4),
					MarginBasisAmount: big.NewInt(4), MarginBasisClosed: new(big.Int),
					Status: StatusOpen, LastCumulativeFunding: new(big.Int), FirstCumulativeFunding: new(big.Int),
					SettledFunding: new(big.Int), LastSettledFunding: new(big.Int), RealizedPnL: new(big.Int),
					ClosedQty: new(big.Int), ExitNotional: new(big.Int), ClosePnLRemainder: carry,
					MaxQuantity: big.NewInt(2), InitialMargin: big.NewInt(4),
				}
				if err := positions.SetPosition(pos); err != nil {
					t.Fatal(err)
				}
				if sign > 0 {
					if err := insurance.SetBalance(pair, big.NewInt(5)); err != nil {
						t.Fatal(err)
					}
				}

				beforeBoundary := clonePositionForUpdate(pos)
				beforeBoundary.ClosePnLRemainder = new(big.Int).Sub(new(big.Int).Set(carry), big.NewInt(sign))
				beforePnL, beforeRemainder := previewIncrementalClosePnL(beforeBoundary, big.NewInt(1), closePrice)
				if beforePnL.Sign() != 0 || new(big.Int).Abs(beforeRemainder).Cmp(new(big.Int).Sub(PriceScale, big.NewInt(1))) != 0 {
					t.Fatalf("pre-boundary result pnl/remainder=%s/%s, want 0/%s", beforePnL, beforeRemainder, new(big.Int).Mul(big.NewInt(sign), new(big.Int).Sub(PriceScale, big.NewInt(1))))
				}

				if err := e.ProcessTradeForPosition("alice", pair, side.Opposite(), big.NewInt(1), closePrice, new(big.Int), 1); err != nil {
					t.Fatal(err)
				}
				back, _ := positions.GetPosition(pos.ID)
				if back.RealizedPnL.Cmp(big.NewInt(sign)) != 0 || back.ClosePnLRemainder.Sign() != 0 ||
					back.Quantity.Cmp(big.NewInt(1)) != 0 || back.Margin.Cmp(big.NewInt(2)) != 0 {
					t.Fatalf("boundary close state pnl/remainder/qty/margin=%s/%s/%s/%s, want %d/0/1/2",
						back.RealizedPnL, back.ClosePnLRemainder, back.Quantity, back.Margin, sign)
				}
				available, _ := balance.GetAvailableBalance("alice", "USDT")
				insuranceBalance, _ := insurance.GetBalance(pair)
				if sign > 0 {
					if available.Cmp(big.NewInt(3)) != 0 || insuranceBalance.Cmp(big.NewInt(4)) != 0 {
						t.Fatalf("positive boundary funds available/insurance=%s/%s, want 3/4", available, insuranceBalance)
					}
				} else if available.Cmp(big.NewInt(1)) != 0 || insuranceBalance.Cmp(big.NewInt(1)) != 0 {
					t.Fatalf("negative boundary funds available/insurance=%s/%s, want 1/1", available, insuranceBalance)
				}
			})
		}
	}
}

type profitLossOrderOutcome struct {
	realizedPnL  *big.Int
	closedQty    *big.Int
	exitNotional *big.Int
	available    *big.Int
	insurance    *big.Int
	shortfall    *big.Int
	claims       *big.Int
	losses       *big.Int
}

func runProfitLossOrder(t *testing.T, side Side, profitFirst bool) profitLossOrderOutcome {
	t.Helper()

	e, positions, insurance, balance, _, _ := newTestEngineFull()
	const pair = "ORDER_INVARIANT_USDT"
	entryPrice := new(big.Int).Mul(big.NewInt(2), PriceScale)
	profitPrice := new(big.Int)
	lossPrice := new(big.Int)
	if side == Long {
		profitPrice.Add(entryPrice, new(big.Int).Add(PriceScale, big.NewInt(1)))
		lossPrice.Sub(entryPrice, new(big.Int).Add(PriceScale, big.NewInt(1)))
	} else {
		profitPrice.Sub(entryPrice, new(big.Int).Add(PriceScale, big.NewInt(1)))
		lossPrice.Add(entryPrice, new(big.Int).Add(PriceScale, big.NewInt(1)))
	}
	pos := &Position{
		ID: "order-position", Owner: "alice", Pair: pair, Side: side,
		EntryPrice: entryPrice, EntryNotionalNumerator: new(big.Int).Mul(entryPrice, big.NewInt(2)),
		Quantity: big.NewInt(2), Margin: big.NewInt(4),
		MarginBasisAmount: big.NewInt(4), MarginBasisClosed: new(big.Int),
		Status: StatusOpen, LastCumulativeFunding: new(big.Int), FirstCumulativeFunding: new(big.Int),
		SettledFunding: new(big.Int), LastSettledFunding: new(big.Int), RealizedPnL: new(big.Int),
		ClosedQty: new(big.Int), ExitNotional: new(big.Int), ClosePnLRemainder: new(big.Int),
		MaxQuantity: big.NewInt(2), InitialMargin: big.NewInt(4),
	}
	if err := positions.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	if err := e.BeginRealizedPnLBatch(); err != nil {
		t.Fatal(err)
	}
	prices := []*big.Int{profitPrice, lossPrice}
	if !profitFirst {
		prices[0], prices[1] = prices[1], prices[0]
	}
	for i, price := range prices {
		if err := e.ProcessTradeForPosition("alice", pair, side.Opposite(), big.NewInt(1), price, new(big.Int), uint64(i+1)); err != nil {
			e.AbortRealizedPnLBatch()
			t.Fatalf("close leg %d at %s: %v", i, price, err)
		}
	}
	result, err := e.CommitRealizedPnLBatch()
	if err != nil {
		t.Fatal(err)
	}
	closed, _ := positions.GetPosition(pos.ID)
	if closed.Status != StatusClosed || closed.Quantity.Sign() != 0 || closed.Margin.Sign() != 0 || closed.ClosePnLRemainder.Sign() != 0 {
		t.Fatalf("unexpected terminal position: %+v", closed)
	}
	available, _ := balance.GetAvailableBalance("alice", "USDT")
	insuranceBalance, _ := insurance.GetBalance(pair)
	shortfall, _ := insurance.GetShortfall(pair)
	return profitLossOrderOutcome{
		realizedPnL: new(big.Int).Set(closed.RealizedPnL), closedQty: new(big.Int).Set(closed.ClosedQty),
		exitNotional: new(big.Int).Set(closed.ExitNotional), available: available,
		insurance: insuranceBalance, shortfall: shortfall,
		claims: result.ProfitClaimsForPair(pair), losses: result.LossContributionsForPair(pair),
	}
}

func TestRealizedPnLBatch_ProfitLossOrderInvariantForBothSides(t *testing.T) {
	for _, side := range []Side{Long, Short} {
		t.Run(fmt.Sprintf("side_%d", side), func(t *testing.T) {
			profitFirst := runProfitLossOrder(t, side, true)
			lossFirst := runProfitLossOrder(t, side, false)
			for field, values := range map[string][2]*big.Int{
				"realized pnl":  {profitFirst.realizedPnL, lossFirst.realizedPnL},
				"closed qty":    {profitFirst.closedQty, lossFirst.closedQty},
				"exit notional": {profitFirst.exitNotional, lossFirst.exitNotional},
				"available":     {profitFirst.available, lossFirst.available},
				"insurance":     {profitFirst.insurance, lossFirst.insurance},
				"shortfall":     {profitFirst.shortfall, lossFirst.shortfall},
				"profit claims": {profitFirst.claims, lossFirst.claims},
				"losses":        {profitFirst.losses, lossFirst.losses},
			} {
				if values[0].Cmp(values[1]) != 0 {
					t.Fatalf("%s depends on profit/loss order: profit-first=%s loss-first=%s", field, values[0], values[1])
				}
			}
			if profitFirst.realizedPnL.Sign() != 0 || profitFirst.available.Cmp(big.NewInt(4)) != 0 ||
				profitFirst.insurance.Sign() != 0 || profitFirst.shortfall.Sign() != 0 ||
				profitFirst.claims.Cmp(big.NewInt(1)) != 0 || profitFirst.losses.Cmp(big.NewInt(1)) != 0 {
				t.Fatalf("unexpected balanced batch result: %+v", profitFirst)
			}
		})
	}
}
