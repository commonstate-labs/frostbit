package arbitrage

import (
	"dex/future"
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	statedb "dex/stateDB"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

func publicFutureOrder(
	id, owner string,
	nonce uint64,
	pair string,
	side pb.OrderSide,
	quantity, price string,
	leverage uint64,
) *pb.AnyTx {
	futureSide := future.Long
	if side == pb.OrderSide_SELL {
		futureSide = future.Short
	}
	return &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{
			TxId:        id,
			FromAddress: owner,
			Nonce:       nonce,
			Fee:         defaultTxFee,
			ChainId:     99,
			Status:      pb.Status_PENDING,
		},
		Pair:     pair,
		Side:     int32(futureSide),
		Quantity: quantity,
		Price:    price,
		Leverage: leverage,
	}}}
}

func futureLevel(
	pair, quote string,
	takerSide pb.OrderSide,
	price, orderID, owner, remaining string,
	leverage uint64,
) interfaces.ProposalFutureLevel {
	return interfaces.ProposalFutureLevel{
		Pair: pair, QuoteToken: quote, TakerSide: takerSide, Price: price,
		Makers: []interfaces.ProposalFutureMaker{{
			OrderID: orderID, Owner: owner, Remaining: remaining, Leverage: leverage,
		}},
	}
}

func futureExecution(
	position int,
	orderID, owner, pair, quote string,
	side pb.OrderSide,
	price, quantity, remaining string,
	leverage uint64,
	matches []interfaces.ProposalFutureMatch,
	levels ...interfaces.ProposalFutureLevel,
) interfaces.ProposalFutureExecution {
	return interfaces.ProposalFutureExecution{
		PublicPosition: position,
		OrderID:        orderID,
		Owner:          owner,
		Pair:           pair,
		QuoteToken:     quote,
		Side:           side,
		Price:          price,
		Quantity:       quantity,
		Remaining:      remaining,
		Leverage:       leverage,
		Matches:        matches,
		Levels:         levels,
	}
}

func futureMatch(orderID, quantity, price string) interfaces.ProposalFutureMatch {
	return interfaces.ProposalFutureMatch{MakerOrderID: orderID, Quantity: quantity, Price: price}
}

type futureCrossFixture struct {
	pair       string
	quote      string
	askID      string
	bidID      string
	askOwner   string
	bidOwner   string
	quantity   string
	askPrice   string
	bidPrice   string
	leverage   uint64
	startIndex int
}

                                                                         
                                                                             
                                                                              
                                                                        
type stateDBLikeMemoryState struct {
	state      *memoryState
	missingErr error
}

func (s *stateDBLikeMemoryState) Get(key string) ([]byte, error) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	data, exists := s.state.data[key]
	if !exists {
		return nil, s.missingErr
	}
	return append([]byte(nil), data...), nil
}

func newFutureCrossFixture(pair, quote, prefix string, startIndex int, askPrice, bidPrice int64) futureCrossFixture {
	return futureCrossFixture{
		pair: pair, quote: quote,
		askID: prefix + "-ask", bidID: prefix + "-bid",
		askOwner: prefix + "-alice", bidOwner: prefix + "-bob",
		quantity: future.PriceScale.String(),
		askPrice: scaledPrice(askPrice), bidPrice: scaledPrice(bidPrice),
		leverage: future.DefaultMaxLeverage, startIndex: startIndex,
	}
}

func (f futureCrossFixture) public() []*pb.AnyTx {
	return []*pb.AnyTx{
		publicFutureOrder(f.askID, f.askOwner, 0, f.pair, pb.OrderSide_SELL, f.quantity, f.askPrice, f.leverage),
		publicFutureOrder(f.bidID, f.bidOwner, 0, f.pair, pb.OrderSide_BUY, f.quantity, f.bidPrice, f.leverage),
	}
}

func (f futureCrossFixture) executions() []interfaces.ProposalFutureExecution {
	return []interfaces.ProposalFutureExecution{
		futureExecution(
			f.startIndex, f.askID, f.askOwner, f.pair, f.quote, pb.OrderSide_SELL,
			f.askPrice, f.quantity, f.quantity, f.leverage, nil,
			futureLevel(f.pair, f.quote, pb.OrderSide_BUY, f.askPrice, f.askID, f.askOwner, f.quantity, f.leverage),
		),
		futureExecution(
			f.startIndex+1, f.bidID, f.bidOwner, f.pair, f.quote, pb.OrderSide_BUY,
			f.bidPrice, f.quantity, "0", f.leverage,
			[]interfaces.ProposalFutureMatch{futureMatch(f.askID, f.quantity, f.askPrice)},
		),
	}
}

func futureCrossMarket(ctx interfaces.ProposalContext, available map[string]string, fixtures ...futureCrossFixture) (*interfaces.ProposalSpotMarket, []*pb.AnyTx) {
	market := proposalMarket(ctx)
	boundaryAvailable := cloneStringMap(available)
	if _, exists := boundaryAvailable[feeToken]; !exists {
		boundaryAvailable[feeToken] = "1000000"
	}
	market.FutureAvailable = cloneStringMap(boundaryAvailable)
	public := make([]*pb.AnyTx, 0, 2*len(fixtures))
	for _, fixture := range fixtures {
		public = append(public, fixture.public()...)
		executions := fixture.executions()
		for i := range executions {
			executions[i].TakerAvailable = cloneStringMap(boundaryAvailable)
		}
		market.FutureExecutions = append(market.FutureExecutions, executions...)
	}
	return market, public
}

func fundedFutureMachine(t *testing.T, quote string, quoteBalance string) (*Machine, *memoryState) {
	t.Helper()
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 7)
	seedBalance(t, state, testArbitrageAccount, feeToken, "1000000")
	seedBalance(t, state, testArbitrageAccount, quote, quoteBalance)
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	machine.state = &stateDBLikeMemoryState{state: state, missingErr: statedb.ErrNotFound}
	machine.allowUntracedCross = false
	return machine, state
}

func TestFutureCrossFinderDiscoversTwoPublicOrders(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "btc", 1, 100, 110)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-find", 2, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)

	f := finder{config: machine.config, state: machine.state, account: machine.account}
	got, err := f.findBestFutureCross(fromProposalContext(ctx), 7, public, fromProposalSpotMarket(market))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.legs) != 2 {
		t.Fatalf("future cross not found: %+v", got)
	}
	if !reflect.DeepEqual(got.insertAfterPublic, []int{1, 2}) {
		t.Fatalf("future insertion schedule=%v, want [1 2]", got.insertAfterPublic)
	}
	open, close := got.legs[0], got.legs[1]
	if open.market != plannedMarketFuture || close.market != plannedMarketFuture ||
		open.futureClose || !close.futureClose || open.futureGroup == "" || open.futureGroup != close.futureGroup {
		t.Fatalf("future open/close plan mismatch: open=%+v close=%+v", open, close)
	}
	if open.futurePair != fixture.pair || close.futurePair != fixture.pair ||
		open.leg.Side != pb.OrderSide_BUY || close.leg.Side != pb.OrderSide_SELL ||
		!reflect.DeepEqual(open.makerIDs, []string{fixture.askID}) || !reflect.DeepEqual(close.makerIDs, []string{fixture.bidID}) {
		t.Fatalf("future planned legs target wrong public orders: open=%+v close=%+v", open, close)
	}
}

func TestFutureCrossBuildsOpenAndReduceOnlyIOCTransactions(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "btc", 1, 100, 110)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-build", 2, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 2 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2}) {
		t.Fatalf("unexpected future batch: %+v", batch)
	}
	open, close := batch.Transactions[0].GetFutureOrderTx(), batch.Transactions[1].GetFutureOrderTx()
	if open == nil || close == nil {
		t.Fatalf("future plan materialized as non-future txs: open=%T close=%T", batch.Transactions[0].Content, batch.Transactions[1].Content)
	}
	wantPositionID := fmt.Sprintf("%s_%s_%d_%d", testArbitrageAccount, fixture.pair, future.Long, ctx.Height)
	if open.Pair != fixture.pair || open.Side != int32(future.Long) || open.Quantity != fixture.quantity ||
		open.Price != fixture.askPrice || open.Leverage != future.DefaultMaxLeverage || open.IsClose || open.IsMarket {
		t.Fatalf("bad future open tx: %+v", open)
	}
	if close.Pair != fixture.pair || close.Side != int32(future.Short) || close.Quantity != open.Quantity ||
		close.Price != fixture.bidPrice || close.Leverage != open.Leverage || !close.IsClose || close.IsMarket ||
		close.MaxSlippageBps != 0 || close.ClosePositionId != wantPositionID {
		t.Fatalf("bad reduce-only IOC close tx: %+v, want position %s", close, wantPositionID)
	}
	for i, tx := range batch.Transactions {
		base := tx.GetBase()
		if base.FromAddress != testArbitrageAccount || base.Nonce != 7+uint64(i) ||
			base.TargetHeight != ctx.Height || base.OnlyProposer != string(ctx.Proposer) || base.ChainId != 99 {
			t.Fatalf("future leg %d base mismatch: %+v", i, base)
		}
	}
}

func TestFutureCrossSelectsAllProfitablePairsThatFit(t *testing.T) {
	const quote = "USDT"
	btc := newFutureCrossFixture("BTC_USDT", quote, "btc", 1, 100, 110)
	eth := newFutureCrossFixture("ETH_USDT", quote, "eth", 3, 200, 240)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-multi-pair", 4, 4)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, btc, eth)

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 4 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2, 3, 4}) {
		t.Fatalf("multi-pair future batch=%+v", batch)
	}
	wantPairs := []string{btc.pair, btc.pair, eth.pair, eth.pair}
	for i, tx := range batch.Transactions {
		order := tx.GetFutureOrderTx()
		if order == nil || order.Pair != wantPairs[i] {
			t.Fatalf("future leg %d pair=%v, want %s", i, order, wantPairs[i])
		}
		if gotClose := order.IsClose; gotClose != (i%2 == 1) {
			t.Fatalf("future leg %d is_close=%t", i, gotClose)
		}
		if tx.GetBase().Nonce != 7+uint64(i) {
			t.Fatalf("future leg %d nonce=%d, want %d", i, tx.GetBase().Nonce, 7+uint64(i))
		}
	}
}

func TestFutureCrossSamePairSequentialGroupsUseDistinctPositionIDs(t *testing.T) {
	const quote = "USDT"
	first := newFutureCrossFixture("BTC_USDT", quote, "btc-first", 1, 100, 110)
	second := newFutureCrossFixture("BTC_USDT", quote, "btc-second", 3, 120, 140)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-same-pair-sequential", 4, 4)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, first, second)

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 4 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2, 3, 4}) {
		t.Fatalf("sequential same-pair future batch=%+v", batch)
	}
	wantIDs := []string{
		fmt.Sprintf("%s_%s_%d_%d", testArbitrageAccount, first.pair, future.Long, ctx.Height),
		fmt.Sprintf("%s_%s_%d_%d_2", testArbitrageAccount, first.pair, future.Long, ctx.Height),
	}
	for group, closeIndex := range []int{1, 3} {
		closeOrder := batch.Transactions[closeIndex].GetFutureOrderTx()
		if closeOrder == nil || !closeOrder.IsClose || closeOrder.ClosePositionId != wantIDs[group] {
			t.Fatalf("same-pair group %d close=%+v, want position %s", group, closeOrder, wantIDs[group])
		}
	}
}

func TestFutureCrossBuildsShortOpenThenBuyClose(t *testing.T) {
	const (
		pair  = "BTC_USDT"
		quote = "USDT"
	)
	quantity := future.PriceScale.String()
	highPrice, lowPrice := scaledPrice(110), scaledPrice(100)
	public := []*pb.AnyTx{
		publicFutureOrder("short-bid", "short-bob", 0, pair, pb.OrderSide_BUY, quantity, highPrice, future.DefaultMaxLeverage),
		publicFutureOrder("short-ask", "short-alice", 0, pair, pb.OrderSide_SELL, quantity, lowPrice, future.DefaultMaxLeverage),
	}
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-short", len(public), 2)
	market := proposalMarket(ctx)
	market.FutureAvailable = map[string]string{quote: "1000000000000"}
	market.FutureExecutions = []interfaces.ProposalFutureExecution{
		futureExecution(
			1, "short-bid", "short-bob", pair, quote, pb.OrderSide_BUY,
			highPrice, quantity, quantity, future.DefaultMaxLeverage, nil,
			futureLevel(pair, quote, pb.OrderSide_SELL, highPrice, "short-bid", "short-bob", quantity, future.DefaultMaxLeverage),
		),
		futureExecution(
			2, "short-ask", "short-alice", pair, quote, pb.OrderSide_SELL,
			lowPrice, quantity, "0", future.DefaultMaxLeverage,
			[]interfaces.ProposalFutureMatch{futureMatch("short-bid", quantity, highPrice)},
		),
	}
	for i := range market.FutureExecutions {
		market.FutureExecutions[i].TakerAvailable = map[string]string{quote: "1000000000000", feeToken: "1000000"}
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 2 {
		t.Fatalf("short future cross not prepared: %+v", batch)
	}
	open, close := batch.Transactions[0].GetFutureOrderTx(), batch.Transactions[1].GetFutureOrderTx()
	wantPositionID := fmt.Sprintf("%s_%s_%d_%d", testArbitrageAccount, pair, future.Short, ctx.Height)
	if open == nil || open.Side != int32(future.Short) || open.IsClose || open.Price != highPrice {
		t.Fatalf("bad short open: %+v", open)
	}
	if close == nil || close.Side != int32(future.Long) || !close.IsClose || close.Price != lowPrice || close.ClosePositionId != wantPositionID {
		t.Fatalf("bad short close: %+v, want position %s", close, wantPositionID)
	}
}

func TestFutureCrossSkipsPairWhenParentHasShortPosition(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "occupied-short", 1, 100, 110)
	machine, state := fundedFutureMachine(t, quote, "1000000000000")
	state.mu.Lock()
	state.data[keys.KeyFutureOwnerPairSidePosition(testArbitrageAccount, fixture.pair, int32(future.Short))] = []byte("existing-short-position")
	state.mu.Unlock()
	ctx := proposalContext("future-cross-parent-short", 2, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatalf("pair with an existing short position produced private orders: %+v", batch)
	}
}

func TestFuturePositionLookupHandlesMissingAndRealReadErrors(t *testing.T) {
	const pair = "BTC_USDT"
	for _, missingErr := range []error{statedb.ErrNotFound, statedb.ErrKVNotFound} {
		state := newMemoryState()
		finder := finder{
			state:   &stateDBLikeMemoryState{state: state, missingErr: missingErr},
			account: testArbitrageAccount,
		}
		occupied, err := finder.hasParentFuturePosition(pair)
		if err != nil || occupied {
			t.Fatalf("missing position err=%v occupied=%t, want nil/false", err, occupied)
		}

		state.mu.Lock()
		state.data[keys.KeyFutureOwnerPairSidePosition(testArbitrageAccount, pair, int32(future.Short))] = []byte("existing-short-position")
		state.mu.Unlock()
		occupied, err = finder.hasParentFuturePosition(pair)
		if err != nil || !occupied {
			t.Fatalf("short position after missing long err=%v occupied=%t, want nil/true", err, occupied)
		}
	}

	readFailure := errors.New("state read failed")
	finder := finder{
		state:   &stateDBLikeMemoryState{state: newMemoryState(), missingErr: readFailure},
		account: testArbitrageAccount,
	}
	if _, err := finder.hasParentFuturePosition(pair); !errors.Is(err, readFailure) {
		t.Fatalf("real state read error=%v, want wrapped %v", err, readFailure)
	}
}

func TestFutureCrossUsesLowerPublicLeverage(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "lower-leverage", 1, 100, 110)
	fixture.leverage = 20
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-lower-leverage", 2, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 2 {
		t.Fatalf("lower-leverage future cross not prepared: %+v", batch)
	}
	for i, tx := range batch.Transactions {
		order := tx.GetFutureOrderTx()
		if order == nil || order.Leverage != fixture.leverage {
			t.Fatalf("future leg %d leverage=%v, want %d", i, order, fixture.leverage)
		}
	}
}

func TestFutureCrossUsesHigherSuccessfulPublicLeverage(t *testing.T) {
	tests := []struct {
		name                string
		first, second, want uint64
	}{
		{name: "later order is higher", first: 2, second: 25, want: 25},
		{name: "earlier order is higher", first: 40, second: 5, want: 40},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const quote = "USDT"
			fixture := newFutureCrossFixture("BTC_USDT", quote, "mixed-leverage", 1, 100, 110)
			machine, _ := fundedFutureMachine(t, quote, "1000000000000")
			ctx := proposalContext("future-cross-higher-leverage-"+test.name, 2, 2)
			market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)
			public[0].GetFutureOrderTx().Leverage = test.first
			public[1].GetFutureOrderTx().Leverage = test.second
			market.FutureExecutions[0].Leverage = test.first
			market.FutureExecutions[1].Leverage = test.second
			market.FutureExecutions[0].Levels[0].Makers[0].Leverage = test.first

			batch, err := machine.Prepare(ctx, public, market)
			if err != nil {
				t.Fatal(err)
			}
			if batch == nil || len(batch.Transactions) != 2 {
				t.Fatalf("mixed-leverage future cross not prepared: %+v", batch)
			}
			for i, tx := range batch.Transactions {
				order := tx.GetFutureOrderTx()
				if order == nil || order.Leverage != test.want {
					t.Fatalf("future leg %d leverage=%v, want higher accepted leverage %d", i, order, test.want)
				}
			}
		})
	}
}

func TestFutureCrossOneSlotChoosesMaximumProfitPair(t *testing.T) {
	const quote = "USDT"
	low := newFutureCrossFixture("LOW_USDT", quote, "low", 1, 100, 105)
	high := newFutureCrossFixture("HIGH_USDT", quote, "high", 3, 100, 140)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-max-profit", 4, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, low, high)

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 2 {
		t.Fatalf("single-group future batch=%+v", batch)
	}
	for i, tx := range batch.Transactions {
		order := tx.GetFutureOrderTx()
		if order == nil || order.Pair != high.pair {
			t.Fatalf("leg %d selected %v, want higher-profit pair %s", i, order, high.pair)
		}
	}
}

func TestFutureCrossPortfolioChoosesMaximumProfitCombination(t *testing.T) {
	const quote = "USDT"
	low := newFutureCrossFixture("LOW_USDT", quote, "low-combo", 1, 100, 105)
	medium := newFutureCrossFixture("MEDIUM_USDT", quote, "medium-combo", 3, 100, 120)
	high := newFutureCrossFixture("HIGH_USDT", quote, "high-combo", 5, 100, 150)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-max-profit-combination", 6, 4)
	ctx.MaxTxs = 10
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, low, medium, high)

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 4 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{3, 4, 5, 6}) {
		t.Fatalf("maximum-profit future combination=%+v", batch)
	}
	wantPairs := []string{medium.pair, medium.pair, high.pair, high.pair}
	for i, tx := range batch.Transactions {
		order := tx.GetFutureOrderTx()
		if order == nil || order.Pair != wantPairs[i] {
			t.Fatalf("combination leg %d selected %v, want %s", i, order, wantPairs[i])
		}
	}
}

func TestFutureCrossUsesProjectedAvailableBalanceAndSkipsUnderfundedPair(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "btc", 1, 100, 110)
	                                                                          
	                                                                             
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-underfunded", 2, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "0"}, fixture)

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatalf("underfunded futures opportunity produced private orders: %+v", batch)
	}
}

func TestFutureCrossDoesNotSpendBalanceCreditedAfterOpenBoundary(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "late-credit", 1, 100, 110)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-late-credit", 3, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)
	public = append(public, publicTransfer("late-credit-after-cross", "funder", 0))
	for i := range market.FutureExecutions {
		market.FutureExecutions[i].TakerAvailable[quote] = "0"
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatalf("future open borrowed a later public-prefix credit: %+v", batch)
	}
}

func TestFutureCrossUsesBalanceAvailableBeforeLatePublicDebit(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "late-debit", 1, 100, 110)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-late-debit", 3, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "0"}, fixture)
	public = append(public, publicTransfer("late-debit-after-cross", testArbitrageAccount, 7))
	for i := range market.FutureExecutions {
		market.FutureExecutions[i].TakerAvailable[quote] = "1000000000000"
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 2 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2}) {
		t.Fatalf("future cross missed capital available at its earlier boundaries: %+v", batch)
	}
}

func TestFutureCrossRejectsWhenCapitalDisappearsBeforeCloseBoundary(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "mid-debit", 1, 100, 110)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-mid-debit", 2, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "0"}, fixture)
	market.FutureExecutions[0].TakerAvailable[quote] = "1000000000000"
	market.FutureExecutions[1].TakerAvailable[quote] = "0"

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatalf("future close used a balance timeline that is negative at its insertion boundary: %+v", batch)
	}
}

func TestSpotAndFutureCrossesShareOnePrivateSchedule(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "btc", 3, 100, 110)
	machine, state := fundedFutureMachine(t, quote, "1000000000000")
	seedBalance(t, state, testArbitrageAccount, "Q", "1000000000000")
	ctx := proposalContext("spot-future-cross", 4, 4)
	market, futurePublic := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)
	spotPublic := []*pb.AnyTx{
		publicSpotOrder("spot-ask", "spot-alice", 0, "A", "Q", pb.OrderSide_SELL, "100", scaledPrice(1)),
		publicSpotOrder("spot-bid", "spot-bob", 0, "A", "Q", pb.OrderSide_BUY, "100", scaledPrice(2)),
	}
	public := append(spotPublic, futurePublic...)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "spot-ask", "spot-alice", "A", "Q", pb.OrderSide_SELL, scaledPrice(1), "100", "100", nil,
			spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), "spot-ask", "spot-alice", "100")),
		spotExecution(2, "spot-bid", "spot-bob", "A", "Q", pb.OrderSide_BUY, scaledPrice(2), "100", "0",
			[]interfaces.ProposalSpotMatch{spotMatch("spot-ask", "100", scaledPrice(1))}),
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 4 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2, 3, 4}) {
		t.Fatalf("mixed spot/future batch=%+v", batch)
	}
	if batch.Transactions[0].GetOrderTx() == nil || batch.Transactions[1].GetOrderTx() == nil ||
		batch.Transactions[2].GetFutureOrderTx() == nil || batch.Transactions[3].GetFutureOrderTx() == nil {
		t.Fatalf("mixed private transaction kinds are wrong: %+v", batch.Transactions)
	}
	for i, tx := range batch.Transactions {
		if tx.GetBase().Nonce != 7+uint64(i) {
			t.Fatalf("mixed leg %d nonce=%d", i, tx.GetBase().Nonce)
		}
	}
}

func TestFutureCrossValidationRejectsActualZeroRealizedProfit(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "btc", 1, 100, 110)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-cross-actual-profit", 2, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)
	batch, err := machine.Prepare(ctx, public, market)
	if err != nil || batch == nil {
		t.Fatalf("prepare future cross: batch=%+v err=%v", batch, err)
	}

	actualGross := new(big.Int).Mul(future.PriceScale, big.NewInt(10)).String()
	openFee := futureTakerFee(future.PriceScale, new(big.Int).Mul(big.NewInt(100), future.PriceScale)).String()
	closeFee := futureTakerFee(future.PriceScale, new(big.Int).Mul(big.NewInt(110), future.PriceScale)).String()
	closePositionID := batch.Transactions[1].GetFutureOrderTx().ClosePositionId
	result := &interfaces.ProposalSimulationResult{Valid: true, Receipts: []interfaces.ProposalSimulationReceipt{
		{
			TxID: batch.Transactions[0].GetTxId(), Status: "SUCCEED", Fee: defaultTxFee,
			Events: []interfaces.ProposalSimulationEvent{
				{Kind: "ORDER_MATCHED", Payload: map[string]string{
					"maker_order_id": fixture.askID, "quantity": fixture.quantity, "price": fixture.askPrice,
				}},
				{Kind: "FEE_CHARGED", Payload: map[string]string{
					"owner": testArbitrageAccount, "role": "taker", "token": quote, "fee": openFee,
				}},
			},
		},
		{
			TxID: batch.Transactions[1].GetTxId(), Status: "SUCCEED", Fee: defaultTxFee,
			Events: []interfaces.ProposalSimulationEvent{
				{Kind: "ORDER_MATCHED", Payload: map[string]string{
					"maker_order_id": fixture.bidID, "quantity": fixture.quantity, "price": fixture.bidPrice,
				}},
				{Kind: "FEE_CHARGED", Payload: map[string]string{
					"owner": testArbitrageAccount, "role": "taker", "token": quote, "fee": closeFee,
				}},
				{Kind: "POSITION_CLOSED", Payload: map[string]string{
					"owner": testArbitrageAccount, "pair": fixture.pair, "close_position": closePositionID,
					"quantity": fixture.quantity, "unfilled_quantity": "0", "remaining_quantity": "0",
					"realized_pnl": actualGross, "pnl_shortfall": "0", "fully_closed": "true",
				}},
			},
		},
	}}
	if err := machine.ValidateCandidate(ctx, batch, result); err != nil {
		t.Fatalf("profitable fully closed future execution rejected: %v", err)
	}

	result.Receipts[1].Events[2].Payload["realized_pnl"] = "0"
	err = machine.ValidateCandidate(ctx, batch, result)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not profitable") {
		t.Fatalf("zero realized futures profit accepted: %v", err)
	}
}

func TestFutureCrossValidationUsesStrategySideForLimitChecks(t *testing.T) {
	const quote = "USDT"
	fixture := newFutureCrossFixture("BTC_USDT", quote, "future-limit-side", 1, 100, 110)
	machine, _ := fundedFutureMachine(t, quote, "1000000000000")
	ctx := proposalContext("future-limit-side", 2, 2)
	market, public := futureCrossMarket(ctx, map[string]string{quote: "1000000000000"}, fixture)
	batch, err := machine.Prepare(ctx, public, market)
	if err != nil || batch == nil {
		t.Fatalf("prepare future cross: batch=%+v err=%v", batch, err)
	}

	aboveLimit := new(big.Int)
	aboveLimit.SetString(fixture.askPrice, 10)
	aboveLimit.Add(aboveLimit, big.NewInt(1))
	result := &interfaces.ProposalSimulationResult{Valid: true, Receipts: []interfaces.ProposalSimulationReceipt{{
		TxID: batch.Transactions[0].GetTxId(), Status: "SUCCEED", Fee: defaultTxFee,
		Events: []interfaces.ProposalSimulationEvent{{Kind: "ORDER_MATCHED", Payload: map[string]string{
			"maker_order_id": fixture.askID, "quantity": fixture.quantity, "price": aboveLimit.String(),
		}}},
	}}}
	if err := machine.ValidateCandidate(ctx, batch, result); err == nil || !strings.Contains(err.Error(), "executed above limit") {
		t.Fatalf("future Long/BUY fill above its limit was not rejected: %v", err)
	}
}

func TestActualFutureTakerFeeAllowsZeroRoundedFeeWithoutEvent(t *testing.T) {
	receipt := ExecutionReceipt{
		TxID: "dust-future-fill",
		Matches: []ExecutionMatch{{
			MakerOrderID: "maker", Quantity: future.PriceScale.String(), Price: "1",
		}},
	}
	fee, err := actualFutureTakerFee(receipt, testArbitrageAccount, "USDT")
	if err != nil || fee.Sign() != 0 {
		t.Fatalf("zero-rounded fee: fee=%v err=%v", fee, err)
	}

	receipt.Matches[0].Price = "10000"
	if _, err := actualFutureTakerFee(receipt, testArbitrageAccount, "USDT"); err == nil {
		t.Fatal("fee-bearing fill without FEE_CHARGED event was accepted")
	}
}
