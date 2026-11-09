package arbitrage

import (
	"dex/interfaces"
	"dex/pb"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestPublicPairComparisonCursorVisitsAdjacentOrdersFirst(t *testing.T) {
	orders := []publicLimitOrder{
		{orderID: "a"}, {orderID: "b"}, {orderID: "c"}, {orderID: "d"},
	}
	cursor := &publicPairComparisonCursor{orders: orders}
	want := [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"a", "c"}}
	for i, pair := range want {
		first, second, ok := cursor.next()
		if !ok || first.orderID != pair[0] || second.orderID != pair[1] {
			t.Fatalf("comparison %d=%s/%s ok=%v, want %s/%s", i, first.orderID, second.orderID, ok, pair[0], pair[1])
		}
	}
}

func TestBetterPlanPrefersAbsoluteProfitWithinOneToken(t *testing.T) {
	higherProfit := &plan{
		startToken: "Q",
		profit:     big.NewInt(10),
		roiBPS:     big.NewInt(100),
		utility:    big.NewRat(1, 100),
		routeName:  "higher-profit",
	}
	higherRatio := &plan{
		startToken: "Q",
		profit:     big.NewInt(9),
		roiBPS:     big.NewInt(1_000),
		utility:    big.NewRat(9, 10),
		routeName:  "higher-ratio",
	}
	if !betterPlan(higherProfit, higherRatio) || betterPlan(higherRatio, higherProfit) {
		t.Fatal("same-token plan ranking preferred normalized utility over absolute native profit")
	}
}

func TestCrossOpportunityFixedPointScoreMatchesFullQuote(t *testing.T) {
	type scoreFixture struct {
		name                      string
		firstSide                 pb.OrderSide
		firstAmount, secondAmount string
		firstPrice, secondPrice   string
		startToken, wallet        string
		ordinal                   int
		capitalBPS, minProfitBPS  uint32
	}

	fixtures := []scoreFixture{
		{name: "sell-buy/non-FB/full", firstSide: pb.OrderSide_SELL, firstAmount: "10", secondAmount: "10", firstPrice: scaledPrice(1), secondPrice: scaledPrice(2), startToken: "Q", wallet: "100", ordinal: 1, capitalBPS: 10_000},
		{name: "sell-buy/non-FB/partial-rounding", firstSide: pb.OrderSide_SELL, firstAmount: "17", secondAmount: "17", firstPrice: "33333333", secondPrice: "66666667", startToken: "Q", wallet: "2", ordinal: 7, capitalBPS: 10_000},
		{name: "sell-buy/non-FB/capital-bps-rounding", firstSide: pb.OrderSide_SELL, firstAmount: "100", secondAmount: "100", firstPrice: scaledPrice(1), secondPrice: scaledPrice(2), startToken: "Q", wallet: "100", ordinal: 9, capitalBPS: 3_333},
		{name: "sell-buy/FB/ordinal-one", firstSide: pb.OrderSide_SELL, firstAmount: "10000", secondAmount: "10000", firstPrice: scaledPrice(1), secondPrice: scaledPrice(2), startToken: feeToken, wallet: "12000", ordinal: 1, capitalBPS: 10_000},
		{name: "sell-buy/FB/ordinal-five-net-zero", firstSide: pb.OrderSide_SELL, firstAmount: "10000", secondAmount: "10000", firstPrice: scaledPrice(1), secondPrice: scaledPrice(2), startToken: feeToken, wallet: "12000", ordinal: 5, capitalBPS: 10_000},
		{name: "sell-buy/non-FB/exact-min-profit", firstSide: pb.OrderSide_SELL, firstAmount: "100", secondAmount: "100", firstPrice: scaledPrice(1), secondPrice: "101000000", startToken: "Q", wallet: "100", ordinal: 2, capitalBPS: 10_000, minProfitBPS: 100},
		{name: "sell-buy/non-FB/below-min-profit", firstSide: pb.OrderSide_SELL, firstAmount: "100", secondAmount: "100", firstPrice: scaledPrice(1), secondPrice: "101000000", startToken: "Q", wallet: "100", ordinal: 2, capitalBPS: 10_000, minProfitBPS: 101},
		{name: "sell-buy/non-FB/zero-rounded-cost", firstSide: pb.OrderSide_SELL, firstAmount: "1", secondAmount: "1", firstPrice: "1", secondPrice: "2", startToken: "Q", wallet: "100", ordinal: 1, capitalBPS: 10_000},
		{name: "buy-sell/non-FB/full", firstSide: pb.OrderSide_BUY, firstAmount: "10", secondAmount: "30", firstPrice: scaledPrice(2), secondPrice: scaledPrice(1), startToken: "A", wallet: "5", ordinal: 1, capitalBPS: 10_000},
		{name: "buy-sell/non-FB/non-monotone-rounding", firstSide: pb.OrderSide_BUY, firstAmount: "9", secondAmount: "19", firstPrice: "20000000", secondPrice: "10000000", startToken: "A", wallet: "9", ordinal: 4, capitalBPS: 10_000},
		{name: "buy-sell/non-FB/neighbour-rounding", firstSide: pb.OrderSide_BUY, firstAmount: "3", secondAmount: "4", firstPrice: "150000000", secondPrice: "125000000", startToken: "A", wallet: "3", ordinal: 6, capitalBPS: 10_000},
		{name: "buy-sell/FB/ordinal-three", firstSide: pb.OrderSide_BUY, firstAmount: "10000", secondAmount: "30000", firstPrice: scaledPrice(2), secondPrice: scaledPrice(1), startToken: feeToken, wallet: "10000", ordinal: 3, capitalBPS: 10_000},
		{name: "buy-sell/FB/ordinal-four-net-zero", firstSide: pb.OrderSide_BUY, firstAmount: "10000", secondAmount: "30000", firstPrice: scaledPrice(2), secondPrice: scaledPrice(1), startToken: feeToken, wallet: "10000", ordinal: 4, capitalBPS: 10_000},
	}

	parse := func(t *testing.T, field, value string) *big.Int {
		t.Helper()
		parsed, ok := new(big.Int).SetString(value, 10)
		if !ok {
			t.Fatalf("parse %s=%q", field, value)
		}
		return parsed
	}
	scratch := newCrossScoreScratch()
	runFixture := func(t *testing.T, fixture scoreFixture) {
		t.Helper()
		cfg := DefaultConfig()
		cfg.Enabled = true
		cfg.CapitalBPS = fixture.capitalBPS
		cfg.MinProfitBPS = fixture.minProfitBPS
		normalized, err := normalizeConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		finder := &finder{config: normalized}

		baseToken, quoteToken := fixture.startToken, "QUOTE"
		secondSide := pb.OrderSide_SELL
		if fixture.firstSide == pb.OrderSide_SELL {
			baseToken, quoteToken = "BASE", fixture.startToken
			secondSide = pb.OrderSide_BUY
		}
		comparison := publicOrderComparison{
			first: publicLimitOrder{
				insertAfter: 1,
				orderID:     "first",
				leg:         Leg{BaseToken: baseToken, QuoteToken: quoteToken, Side: fixture.firstSide},
				amount:      parse(t, "first amount", fixture.firstAmount),
				price:       parse(t, "first price", fixture.firstPrice),
			},
			second: publicLimitOrder{
				insertAfter: 2,
				orderID:     "second",
				leg:         Leg{BaseToken: baseToken, QuoteToken: quoteToken, Side: secondSide},
				amount:      parse(t, "second amount", fixture.secondAmount),
				price:       parse(t, "second price", fixture.secondPrice),
			},
		}
		classifiedToken, viable := classifyCrossComparison(comparison)
		if !viable || classifiedToken != fixture.startToken {
			t.Fatalf("fixture is not a viable %s opportunity: token=%q viable=%v", fixture.name, classifiedToken, viable)
		}
		opportunity := &crossOpportunity{comparison: comparison, startToken: classifiedToken}
		feesPerGroup := new(big.Int).Mul(new(big.Int).Set(normalized.fee), big.NewInt(2))
		wallet := parse(t, "wallet", fixture.wallet)
		strategyCapital := new(big.Int).Mul(new(big.Int).Set(wallet), new(big.Int).SetUint64(uint64(normalized.capitalBPS)))
		strategyCapital.Quo(strategyCapital, bigTenK)
		available := new(big.Int).Set(wallet)
		if fixture.startToken == feeToken {
			available.Sub(available, new(big.Int).Mul(new(big.Int).Set(feesPerGroup), big.NewInt(int64(fixture.ordinal))))
		}
		capital := new(big.Int)
		if available.Sign() > 0 {
			capital.Mul(available, new(big.Int).SetUint64(uint64(normalized.capitalBPS)))
			capital.Quo(capital, bigTenK)
		}

		capitalAtOrdinal := func(token string, ordinal int) (*big.Int, error) {
			if token != fixture.startToken || ordinal != fixture.ordinal {
				return nil, fmt.Errorf("unexpected capital request %s/%d, want %s/%d", token, ordinal, fixture.startToken, fixture.ordinal)
			}
			return new(big.Int).Set(capital), nil
		}
		strategyCapitalFor := func(token string) (*big.Int, error) {
			if token != fixture.startToken {
				return nil, fmt.Errorf("unexpected strategy-capital token %s, want %s", token, fixture.startToken)
			}
			return new(big.Int).Set(strategyCapital), nil
		}

		var score big.Int
		valid := finder.scoreCrossOpportunity(opportunity, capital, strategyCapital, feesPerGroup, &score, scratch)
		quoted, err := finder.quoteCrossOpportunity(7, opportunity, fixture.ordinal, feesPerGroup, capitalAtOrdinal, strategyCapitalFor)
		if err != nil {
			t.Fatal(err)
		}
		if valid != (quoted != nil) {
			t.Fatalf("score validity=%v, full quote exists=%v (capital=%s strategy=%s score=%s)", valid, quoted != nil, capital, strategyCapital, &score)
		}
		if quoted == nil {
			return
		}
		if !meetsMinimumProfit(quoted, fixture.minProfitBPS) {
			t.Fatalf("full quote returned below min profitability: roi=%s min=%d", quoted.roiBPS, fixture.minProfitBPS)
		}
		expectedScore := new(big.Int).Lsh(new(big.Int).Set(quoted.profit), portfolioUtilityPrecision)
		expectedScore.Quo(expectedScore, strategyCapital)
		if score.Cmp(expectedScore) != 0 {
			t.Fatalf("fixed-point score=%s, want (profit=%s << %d)/strategyCapital=%s => %s", &score, quoted.profit, portfolioUtilityPrecision, strategyCapital, expectedScore)
		}
		wantUtility := new(big.Rat).SetFrac(new(big.Int).Set(quoted.profit), strategyCapital)
		if quoted.utility == nil || quoted.utility.Cmp(wantUtility) != 0 {
			t.Fatalf("quoted utility=%v, want profit/strategyCapital=%s", quoted.utility, wantUtility)
		}
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) { runFixture(t, fixture) })
	}

	rng := rand.New(rand.NewSource(20260823))
	bpsValues := []uint32{3_333, 7_500, 10_000}
	minProfitValues := []uint32{0, 1, 25, 100, 1_000, 10_000}
	for i := 0; i < 256; i++ {
		fixture := scoreFixture{
			name:         fmt.Sprintf("random-%03d", i),
			ordinal:      1 + rng.Intn(7),
			capitalBPS:   bpsValues[rng.Intn(len(bpsValues))],
			minProfitBPS: minProfitValues[rng.Intn(len(minProfitValues))],
		}
		if i%4 == 0 {
			fixture.startToken = feeToken
			fixture.wallet = new(big.Int).SetInt64(int64(fixture.ordinal*2_000 + rng.Intn(12_001))).String()
		} else {
			fixture.startToken = "Q"
			fixture.wallet = new(big.Int).SetInt64(int64(1 + rng.Intn(12_000))).String()
		}
		askPrice := int64(1 + rng.Int63n(3*priceScaleRaw))
		spread := int64(1 + rng.Int63n(2*priceScaleRaw))
		if i%2 == 0 {
			fixture.firstSide = pb.OrderSide_SELL
			amount := int64(1 + rng.Intn(10_000))
			fixture.firstAmount = big.NewInt(amount).String()
			fixture.secondAmount = big.NewInt(amount + int64(rng.Intn(1_000))).String()
			fixture.firstPrice = big.NewInt(askPrice).String()
			fixture.secondPrice = big.NewInt(askPrice + spread).String()
		} else {
			fixture.firstSide = pb.OrderSide_BUY
			amount := int64(1 + rng.Intn(10_000))
			fixture.firstAmount = big.NewInt(amount).String()
			fixture.secondAmount = big.NewInt(amount + 1 + int64(rng.Intn(10_000))).String()
			fixture.firstPrice = big.NewInt(askPrice + spread).String()
			fixture.secondPrice = big.NewInt(askPrice).String()
		}
		t.Run(fixture.name, func(t *testing.T) { runFixture(t, fixture) })
	}
}

func publicSpotOrder(id, owner string, nonce uint64, baseToken, quoteToken string, side pb.OrderSide, amount, price string) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
		Base: &pb.BaseMessage{
			TxId:        id,
			FromAddress: owner,
			Nonce:       nonce,
			Fee:         defaultTxFee,
			ChainId:     99,
			Status:      pb.Status_PENDING,
		},
		BaseToken:  baseToken,
		QuoteToken: quoteToken,
		Op:         pb.OrderOp_ADD,
		Amount:     amount,
		Price:      price,
		Side:       side,
	}}}
}

func publicTransfer(id, owner string, nonce uint64) *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{
			TxId:        id,
			FromAddress: owner,
			Nonce:       nonce,
			Fee:         defaultTxFee,
			ChainId:     99,
			Status:      pb.Status_PENDING,
		},
		To:           "receiver",
		TokenAddress: "OTHER",
		Amount:       "1",
	}}}
}

func sequentialCrossOrders(count int, quoteToken string) []*pb.AnyTx {
	public := make([]*pb.AnyTx, 0, count*2)
	for i := 0; i < count; i++ {
		baseToken := fmt.Sprintf("ASSET-%02d", i)
		public = append(public,
			publicSpotOrder(fmt.Sprintf("ask-%02d", i), fmt.Sprintf("asker-%02d", i), 0, baseToken, quoteToken, pb.OrderSide_SELL, "10", scaledPrice(1)),
			publicSpotOrder(fmt.Sprintf("bid-%02d", i), fmt.Sprintf("bidder-%02d", i), 0, baseToken, quoteToken, pb.OrderSide_BUY, "10", scaledPrice(2)),
		)
	}
	return public
}

func validateCrossExecution(t *testing.T, machine *Machine, ctx interfaces.ProposalContext, batch *interfaces.PrivateTxBatch, makers, quantities, prices []string) {
	t.Helper()
	if len(makers) != len(batch.Transactions) || len(quantities) != len(batch.Transactions) || len(prices) != len(batch.Transactions) {
		t.Fatal("invalid test execution fixture")
	}
	result := &interfaces.ProposalSimulationResult{Valid: true}
	for i, tx := range batch.Transactions {
		result.Receipts = append(result.Receipts, interfaces.ProposalSimulationReceipt{
			TxID: tx.GetTxId(), Status: "SUCCEED",
			Events: []interfaces.ProposalSimulationEvent{{Kind: "ORDER_MATCHED", Payload: map[string]string{
				"maker_order_id": makers[i],
				"quantity":       quantities[i],
				"price":          prices[i],
			}}},
		})
	}
	if err := machine.ValidateCandidate(ctx, batch, result); err != nil {
		t.Fatalf("cross-order execution rejected: %v", err)
	}
}

func TestCrossOrderSellThenBuyInterleavesWithoutReorderingPublicPrefix(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 7)
	seedBalance(t, state, testArbitrageAccount, "FB", "1000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	askPrice := scaledPrice(1000)
	bidPrice := scaledPrice(2000)
	public := []*pb.AnyTx{
		publicTransfer("before", "carol", 0),
		publicSpotOrder("public-ask", "alice", 0, "A", "FB", pb.OrderSide_SELL, "10", askPrice),
		publicTransfer("between", "alice", 1),
		publicSpotOrder("public-bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "20", bidPrice),
	}
	ctx := proposalContext("cross-forward", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 2 {
		t.Fatalf("forward cross was not prepared: %+v", batch)
	}
	if !reflect.DeepEqual(batch.InsertAfterPublic, []int{2, 4}) {
		t.Fatalf("insertion schedule=%v, want [2 4]", batch.InsertAfterPublic)
	}
	wants := []struct {
		side   pb.OrderSide
		amount string
		price  string
	}{
		{pb.OrderSide_BUY, "10", askPrice},
		{pb.OrderSide_SELL, "10", bidPrice},
	}
	for i, want := range wants {
		order := batch.Transactions[i].GetOrderTx()
		base := batch.Transactions[i].GetBase()
		if order.Side != want.side || order.Amount != want.amount || order.Price != want.price || order.IsMarket || order.Op != pb.OrderOp_ADD {
			t.Fatalf("private leg %d=%+v, want=%+v", i, order, want)
		}
		if base.Nonce != 7+uint64(i) || base.TargetHeight != ctx.Height || base.OnlyProposer != string(ctx.Proposer) {
			t.Fatalf("private leg %d guard/nonce mismatch: %+v", i, base)
		}
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"public-ask", "public-bid"},
		[]string{"10", "10"},
		[]string{askPrice, bidPrice},
	)
}

func TestCrossOrderBuyThenSellBuysBackMoreBase(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 3)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000")
	seedBalance(t, state, testArbitrageAccount, "A", "100")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	bidPrice := scaledPrice(2)
	askPrice := scaledPrice(1)
	public := []*pb.AnyTx{
		publicSpotOrder("public-bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", bidPrice),
		publicSpotOrder("public-ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "30", askPrice),
	}
	ctx := proposalContext("cross-reverse", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2}) {
		t.Fatalf("reverse cross was not prepared: %+v", batch)
	}
	first, second := batch.Transactions[0].GetOrderTx(), batch.Transactions[1].GetOrderTx()
	if first.Side != pb.OrderSide_SELL || first.Amount != "10" || first.Price != bidPrice {
		t.Fatalf("first reverse leg=%+v", first)
	}
	if second.Side != pb.OrderSide_BUY || second.Amount != "20" || second.Price != askPrice {
		t.Fatalf("second reverse leg must buy back more base: %+v", second)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"public-bid", "public-ask"},
		[]string{"10", "20"},
		[]string{bidPrice, askPrice},
	)
}

func TestCrossOrderBuyThenSellShrinksToBaseCapital(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000")
	seedBalance(t, state, testArbitrageAccount, "A", "5")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	bidPrice, askPrice := scaledPrice(2), scaledPrice(1)
	public := []*pb.AnyTx{
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", bidPrice),
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "30", askPrice),
	}
	ctx := proposalContext("cross-reverse-capital", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("base-capital-bounded reverse cross was not prepared")
	}
	first, second := batch.Transactions[0].GetOrderTx(), batch.Transactions[1].GetOrderTx()
	if first.Side != pb.OrderSide_SELL || first.Amount != "5" || second.Side != pb.OrderSide_BUY || second.Amount != "10" {
		t.Fatalf("reverse cross did not shrink to capital and buy back more: first=%+v second=%+v", first, second)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"bid", "ask"},
		[]string{"5", "10"},
		[]string{bidPrice, askPrice},
	)
}

func TestCrossOrderBuyThenSellShrinksToLaterAskRemainder(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000")
	seedBalance(t, state, testArbitrageAccount, "A", "5")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	bidPrice, askPrice := scaledPrice(2), scaledPrice(1)
	public := []*pb.AnyTx{
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", bidPrice),
		                                                                   
		                                                                    
		                                                             
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "12", askPrice),
	}
	ctx := proposalContext("cross-reverse-remainder", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("ask-remainder-bounded reverse cross was not prepared")
	}
	first, second := batch.Transactions[0].GetOrderTx(), batch.Transactions[1].GetOrderTx()
	if first.Amount != "5" || second.Amount != "7" {
		t.Fatalf("reverse sizing ignored capital or later ask remainder: first=%+v second=%+v", first, second)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"bid", "ask"},
		[]string{"5", "7"},
		[]string{bidPrice, askPrice},
	)
}

func TestCrossOrderBuyThenSellHandlesNonMonotoneRoundingCounterexample(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000")
	seedBalance(t, state, testArbitrageAccount, "A", "9")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	                                                                      
	                                                                         
	                                                                           
	                                                     
	bidPrice, askPrice := "20000000", "10000000"
	public := []*pb.AnyTx{
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "9", bidPrice),
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "19", askPrice),
	}
	ctx := proposalContext("cross-reverse-rounding", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("integer-rounding reverse cross was missed")
	}
	first, second := batch.Transactions[0].GetOrderTx(), batch.Transactions[1].GetOrderTx()
	if first.Amount != "9" || second.Amount != "19" {
		t.Fatalf("rounding counterexample sized incorrectly: first=%+v second=%+v", first, second)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"bid", "ask"},
		[]string{"9", "19"},
		[]string{bidPrice, askPrice},
	)
}

func TestCrossOrderRequiresGuardedExternalLimitMakers(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "1000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	wrongHeight := publicSpotOrder("guarded-ask", "eve", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1))
	wrongHeight.GetBase().TargetHeight = 999
	self := publicSpotOrder("self-ask", testArbitrageAccount, 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1))
	market := publicSpotOrder("market-ask", "mallory", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1))
	market.GetOrderTx().IsMarket = true
	ready := publicSpotOrder("ready-ask", "alice", 5, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000))
	bid := publicSpotOrder("public-bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "10", scaledPrice(2000))
	public := []*pb.AnyTx{wrongHeight, self, market, ready, bid}
	ctx := proposalContext("cross-filters", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || !reflect.DeepEqual(batch.InsertAfterPublic, []int{4, 5}) {
		t.Fatalf("planner selected a guarded/self/market maker: %+v", batch)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"ready-ask", "public-bid"},
		[]string{"10", "10"},
		[]string{scaledPrice(1000), scaledPrice(2000)},
	)
}

func TestCrossOrderPairIdentityCannotCollideOnEmbeddedNUL(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000")
	seedBalance(t, state, testArbitrageAccount, "\x00B", "100000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	                                                                          
	                                                                         
	                                                                  
	public := []*pb.AnyTx{
		publicSpotOrder("nul-ask", "alice", 0, "A", "\x00B", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicSpotOrder("nul-bid", "bob", 0, "A\x00", "B", pb.OrderSide_BUY, "10", scaledPrice(2)),
	}
	ctx := proposalContext("cross-nul-pair", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatalf("distinct NUL-containing token pairs were joined: %+v", batch)
	}
}

func TestClosedPlanValidationRejectsTokenDiscontinuity(t *testing.T) {
	candidate := &plan{
		startToken: "Q",
		legs: []plannedLeg{
			{leg: Leg{BaseToken: "A", QuoteToken: "Q", Side: pb.OrderSide_BUY}},
			{leg: Leg{BaseToken: "A\x00", QuoteToken: "Q", Side: pb.OrderSide_SELL}},
		},
	}
	if err := validateClosedPlanTokens(candidate); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("discontinuous token route accepted: %v", err)
	}
}

func TestCrossOrderSellThenBuyShrinksToIndependentCapital(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	                                                                
	                                                                           
	                                         
	seedBalance(t, state, testArbitrageAccount, "FB", "7000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	askPrice, bidPrice := scaledPrice(1000), scaledPrice(2000)
	public := []*pb.AnyTx{
		publicSpotOrder("ask", "alice", 0, "A", "FB", pb.OrderSide_SELL, "10", askPrice),
		publicSpotOrder("bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "10", bidPrice),
	}
	ctx := proposalContext("cross-capital-shrink", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Transactions[0].GetOrderTx().Amount != "5" || batch.Transactions[1].GetOrderTx().Amount != "5" {
		t.Fatalf("forward cross did not shrink safely to capital: %+v", batch)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"ask", "bid"},
		[]string{"5", "5"},
		[]string{askPrice, bidPrice},
	)
}

func TestCrossOrderChoosesLargestNetProfitInSameToken(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))

	public := []*pb.AnyTx{
		                                            
		publicSpotOrder("large-ask", "alice", 0, "A", "FB", pb.OrderSide_SELL, "100", scaledPrice(100)),
		publicSpotOrder("large-bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "100", scaledPrice(200)),
		                                                                              
		publicSpotOrder("roi-ask", "carol", 0, "B", "FB", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicSpotOrder("roi-bid", "dave", 0, "B", "FB", pb.OrderSide_BUY, "10", scaledPrice(1000)),
	}
	ctx := proposalContext("cross-best-net", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2}) {
		t.Fatalf("planner did not maximize comparable net profit: %+v", batch)
	}
	if got := batch.Transactions[0].GetOrderTx().BaseToken; got != "A" {
		t.Fatalf("selected base=%s, want A", got)
	}
}

func TestCrossOrderCandidateBindsInsertionScheduleAndExactMaker(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "1000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("ask", "alice", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000)),
		publicSpotOrder("bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "10", scaledPrice(2000)),
	}
	ctx := proposalContext("cross-binding", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil || batch == nil {
		t.Fatalf("prepare: batch=%+v err=%v", batch, err)
	}
	batch.InsertAfterPublic[0] = 0
	if err := machine.ValidateCandidate(ctx, batch, &interfaces.ProposalSimulationResult{Valid: true}); err == nil || !strings.Contains(err.Error(), "insertion schedule") {
		t.Fatalf("mutated insertion schedule accepted: %v", err)
	}

	retry, err := machine.Prepare(ctx, public, nil)
	if err != nil || retry == nil || !reflect.DeepEqual(retry.InsertAfterPublic, []int{1, 2}) {
		t.Fatalf("cached batch insertion schedule was mutated: batch=%+v err=%v", retry, err)
	}
	result := &interfaces.ProposalSimulationResult{Valid: true}
	for i, tx := range retry.Transactions {
		maker := []string{"ask", "bid"}[i]
		if i == 1 {
			maker = "different-public-bid"
		}
		result.Receipts = append(result.Receipts, interfaces.ProposalSimulationReceipt{
			TxID: tx.GetTxId(), Status: "SUCCEED",
			Events: []interfaces.ProposalSimulationEvent{{Kind: "ORDER_MATCHED", Payload: map[string]string{
				"maker_order_id": maker,
				"quantity":       "10",
				"price":          []string{scaledPrice(1000), scaledPrice(2000)}[i],
			}}},
		})
	}
	if err := machine.ValidateCandidate(ctx, retry, result); err == nil || !strings.Contains(err.Error(), "unprojected maker") {
		t.Fatalf("different public maker accepted: %v", err)
	}
}

func TestMachineChoosesSingleBestCrossOrDynamicCandidate(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 9)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000000")
	signCount := 0
	machine := enabledMachine(t, state, DefaultConfig(), &signCount)

	public := []*pb.AnyTx{
		                                              
		publicSpotOrder("cross-ask", "alice", 0, "A", "FB", pb.OrderSide_SELL, "100", scaledPrice(100)),
		publicSpotOrder("cross-bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "100", scaledPrice(200)),
	}
	ctx := proposalContext("multi-candidate", len(public), 2)
	                                                                         
	                                                                   
	market := proposalMarket(ctx,
		spotLevel("X", "FB", pb.OrderSide_BUY, scaledPrice(1), "dynamic-ask", "carol", "10"),
		spotLevel("X", "FB", pb.OrderSide_SELL, scaledPrice(1000), "dynamic-bid", "dave", "10"),
	)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "cross-ask", "alice", "A", "FB", pb.OrderSide_SELL, scaledPrice(100), "100", "100", nil,
			spotLevel("A", "FB", pb.OrderSide_BUY, scaledPrice(100), "cross-ask", "alice", "100")),
		spotExecution(2, "cross-bid", "bob", "A", "FB", pb.OrderSide_BUY, scaledPrice(200), "100", "0",
			[]interfaces.ProposalSpotMatch{spotMatch("cross-ask", "100", scaledPrice(100))}),
	}
	candidates, err := machine.PrepareCandidates(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count=%d, want one global winner: %+v", len(candidates), candidates)
	}
	if signCount != 2 {
		t.Fatalf("sign count=%d, want one signed two-leg winner", signCount)
	}
	if !reflect.DeepEqual(candidates[0].InsertAfterPublic, []int{1, 2}) || candidates[0].Transactions[0].GetOrderTx().BaseToken != "A" {
		t.Fatalf("highest-net cross candidate is not first: %+v", candidates[0])
	}
	for _, candidate := range candidates {
		for i, tx := range candidate.Transactions {
			if tx.GetBase().Nonce != 9+uint64(i) {
				t.Fatalf("candidate %s leg %d nonce=%d, want %d", candidate.ID, i, tx.GetBase().Nonce, 9+uint64(i))
			}
		}
	}

	validateCrossExecution(t, machine, ctx, candidates[0],
		[]string{"cross-ask", "cross-bid"},
		[]string{"100", "100"},
		[]string{scaledPrice(100), scaledPrice(200)},
	)

	                                                                           
	candidates[0].InsertAfterPublic[0] = 0
	retry, err := machine.PrepareCandidates(ctx, public, market)
	if err != nil || len(retry) != 1 || signCount != 2 {
		t.Fatalf("cached candidates changed or were re-signed: count=%d signs=%d err=%v", len(retry), signCount, err)
	}
	if !reflect.DeepEqual(retry[0].InsertAfterPublic, []int{1, 2}) || retry[0].Transactions[0].GetOrderTx().Amount != "100" {
		t.Fatalf("caller mutation poisoned cached candidates: %+v", retry)
	}
}

func TestTracedCrossUsesActualRemainingInsteadOfDeclaredAmount(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "Q", "1000")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "100", scaledPrice(1)),
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(2)),
	}
	ctx := proposalContext("traced-remaining", len(public), 2)
	market := proposalMarket(ctx)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "ask", "alice", "A", "Q", pb.OrderSide_SELL, scaledPrice(1), "100", "4",
			[]interfaces.ProposalSpotMatch{spotMatch("older-ask", "96", scaledPrice(1))},
			spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), "ask", "alice", "4")),
		spotExecution(2, "bid", "bob", "A", "Q", pb.OrderSide_BUY, scaledPrice(2), "10", "6",
			[]interfaces.ProposalSpotMatch{spotMatch("ask", "4", scaledPrice(1))},
			spotLevel("A", "Q", pb.OrderSide_SELL, scaledPrice(2), "bid", "bob", "6")),
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 2 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2}) {
		t.Fatalf("traced batch=%+v", batch)
	}
	for i, tx := range batch.Transactions {
		if tx.GetOrderTx().Amount != "4" {
			t.Fatalf("private leg %d amount=%s, want actual first remainder 4", i, tx.GetOrderTx().Amount)
		}
	}
}

func TestNonNilMarketRequiresAuthoritativeExecutionTrace(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "Q", "1000")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	machine.allowUntracedCross = false
	public := []*pb.AnyTx{
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(2)),
	}
	ctx := proposalContext("trace-required", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil || batch != nil {
		t.Fatalf("production machine used raw public declarations without VM market: batch=%+v err=%v", batch, err)
	}
	machine = enabledMachine(t, state, DefaultConfig(), new(int))
	machine.allowUntracedCross = false
	batch, err = machine.Prepare(ctx, public, proposalMarket(ctx))
	if err != nil || batch != nil {
		t.Fatalf("raw public declarations produced candidate with empty VM trace: batch=%+v err=%v", batch, err)
	}
}

func TestTracedCrossRejectsNonFrontMakerAndIncompleteLaterConsumption(t *testing.T) {
	tests := []struct {
		name       string
		firstMaker string
		laterMaker string
	}{
		{name: "first is not front maker", firstMaker: "older-front", laterMaker: "ask"},
		{name: "later does not consume first", firstMaker: "ask", laterMaker: "different-ask"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newMemoryState()
			seedAccount(t, state, testArbitrageAccount, 0)
			seedBalance(t, state, testArbitrageAccount, "Q", "1000")
			seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
			machine := enabledMachine(t, state, DefaultConfig(), new(int))
			public := []*pb.AnyTx{
				publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
				publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(2)),
			}
			ctx := proposalContext("unsafe-trace-"+test.name, len(public), 2)
			market := proposalMarket(ctx)
			market.Executions = []interfaces.ProposalSpotExecution{
				spotExecution(1, "ask", "alice", "A", "Q", pb.OrderSide_SELL, scaledPrice(1), "10", "10", nil,
					spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), test.firstMaker, "alice", "10")),
				spotExecution(2, "bid", "bob", "A", "Q", pb.OrderSide_BUY, scaledPrice(2), "10", "0",
					[]interfaces.ProposalSpotMatch{spotMatch(test.laterMaker, "10", scaledPrice(1))}),
			}
			batch, err := machine.Prepare(ctx, public, market)
			if err != nil || batch != nil {
				t.Fatalf("unsafe traced cross accepted: batch=%+v err=%v", batch, err)
			}
		})
	}
}

func TestTracedReverseCrossRestoresBaseAndEarnsQuoteResidual(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 3)
	seedBalance(t, state, testArbitrageAccount, "A", "10")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(2)),
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
	}
	ctx := proposalContext("restoring-reverse", len(public), 2)
	market := proposalMarket(ctx)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "bid", "bob", "A", "Q", pb.OrderSide_BUY, scaledPrice(2), "10", "10", nil,
			spotLevel("A", "Q", pb.OrderSide_SELL, scaledPrice(2), "bid", "bob", "10")),
		spotExecution(2, "ask", "alice", "A", "Q", pb.OrderSide_SELL, scaledPrice(1), "10", "0",
			[]interfaces.ProposalSpotMatch{spotMatch("bid", "10", scaledPrice(2))}),
	}
	batch, err := machine.Prepare(ctx, public, market)
	if err != nil || batch == nil || len(batch.Transactions) != 2 {
		t.Fatalf("reverse restoring batch=%+v err=%v", batch, err)
	}
	if batch.Transactions[0].GetOrderTx().Side != pb.OrderSide_SELL ||
		batch.Transactions[1].GetOrderTx().Side != pb.OrderSide_BUY ||
		batch.Transactions[0].GetOrderTx().Amount != "10" || batch.Transactions[1].GetOrderTx().Amount != "10" {
		t.Fatalf("reverse restoring legs=%+v", batch.Transactions)
	}
	planned := machine.planByID[batch.ID]
	if planned == nil || len(planned.groups) != 1 || planned.groups[0].profitToken != "Q" || !planned.groups[0].residualProfit {
		t.Fatalf("reverse without extra ask depth did not retain quote profit: %+v", planned)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"bid", "ask"},
		[]string{"10", "10"},
		[]string{scaledPrice(2), scaledPrice(1)},
	)
}

func TestTracedReverseCrossAutomaticallyReinvestsQuoteProfitIntoBase(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 3)
	seedBalance(t, state, testArbitrageAccount, "A", "10")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	machine.allowUntracedCross = false
	public := []*pb.AnyTx{
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(2)),
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "20", scaledPrice(1)),
	}
	ctx := proposalContext("reverse-auto-base-profit", len(public), 2)
	market := proposalMarket(ctx)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "bid", "bob", "A", "Q", pb.OrderSide_BUY, scaledPrice(2), "10", "10", nil,
			spotLevel("A", "Q", pb.OrderSide_SELL, scaledPrice(2), "bid", "bob", "10")),
		spotExecution(2, "ask", "alice", "A", "Q", pb.OrderSide_SELL, scaledPrice(1), "20", "10",
			[]interfaces.ProposalSpotMatch{spotMatch("bid", "10", scaledPrice(2))},
			spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), "ask", "alice", "10")),
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil || batch == nil || len(batch.Transactions) != 2 {
		t.Fatalf("reverse reinvestment batch=%+v err=%v", batch, err)
	}
	if sold, bought := batch.Transactions[0].GetOrderTx().Amount, batch.Transactions[1].GetOrderTx().Amount; sold != "10" || bought != "20" {
		t.Fatalf("automatic settlement sold/bought=%s/%s, want 10/20 with ten A profit", sold, bought)
	}
	planned := machine.planByID[batch.ID]
	if planned == nil || len(planned.groups) != 1 || planned.groups[0].profitToken != "A" || planned.groups[0].residualProfit {
		t.Fatalf("automatic settlement did not retain base profit: %+v", planned)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"bid", "ask"},
		[]string{"10", "20"},
		[]string{scaledPrice(2), scaledPrice(1)},
	)
}

func TestTracedReverseCrossAutomaticallyKeepsHigherQuoteValue(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "A", "2")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	machine.allowUntracedCross = false
	public := []*pb.AnyTx{
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "2", "150000000"),
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "4", "75000000"),
	}
	ctx := proposalContext("reverse-auto-quote-profit", len(public), 2)
	market := proposalMarket(ctx)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "bid", "bob", "A", "Q", pb.OrderSide_BUY, "150000000", "2", "2", nil,
			spotLevel("A", "Q", pb.OrderSide_SELL, "150000000", "bid", "bob", "2")),
		spotExecution(2, "ask", "alice", "A", "Q", pb.OrderSide_SELL, "75000000", "4", "2",
			[]interfaces.ProposalSpotMatch{spotMatch("bid", "2", "150000000")},
			spotLevel("A", "Q", pb.OrderSide_BUY, "75000000", "ask", "alice", "2")),
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil || batch == nil {
		t.Fatalf("reverse quote-value batch=%+v err=%v", batch, err)
	}
	if sold, bought := batch.Transactions[0].GetOrderTx().Amount, batch.Transactions[1].GetOrderTx().Amount; sold != "2" || bought != "2" {
		t.Fatalf("automatic quote settlement sold/bought=%s/%s, want 2/2", sold, bought)
	}
	planned := machine.planByID[batch.ID]
	if planned == nil || len(planned.groups) != 1 || planned.groups[0].profitToken != "Q" || !planned.groups[0].residualProfit {
		t.Fatalf("higher quote-equivalent settlement was not selected: %+v", planned)
	}
}

func TestBaseReinvestmentMarksPairDirtyAfterClosingLeg(t *testing.T) {
	opportunity := &crossOpportunity{comparison: publicOrderComparison{
		first:  publicLimitOrder{insertAfter: 1, orderID: "bid", leg: Leg{BaseToken: "A", QuoteToken: "Q", Side: pb.OrderSide_BUY}},
		second: publicLimitOrder{insertAfter: 2, orderID: "ask", leg: Leg{BaseToken: "A", QuoteToken: "Q", Side: pb.OrderSide_SELL}},
	}}
	candidate := crossPlan(
		0, "base-reinvestment", "A", big.NewInt(10), big.NewInt(20), big.NewInt(2000),
		[]plannedLeg{
			{leg: Leg{BaseToken: "A", QuoteToken: "Q", Side: pb.OrderSide_SELL}, baseAmount: big.NewInt(10), price: big.NewInt(2), inputSpent: big.NewInt(10), output: big.NewInt(20), makerIDs: []string{"bid"}},
			{leg: Leg{BaseToken: "A", QuoteToken: "Q", Side: pb.OrderSide_BUY}, baseAmount: big.NewInt(20), price: big.NewInt(1), inputSpent: big.NewInt(20), output: big.NewInt(20), makerIDs: []string{"ask"}},
		},
		[]int{1, 2},
	)
	candidate.utility = big.NewRat(1, 1)
	state := &crossPortfolioSearchState{
		balances:    map[string]*big.Int{"A": big.NewInt(10), "Q": new(big.Int), feeToken: big.NewInt(2000)},
		activePairs: make(map[publicSpotPair]int),
		dirtyPairs:  make(map[publicSpotPair]struct{}),
		usedMakers:  make(map[string]struct{}),
	}
	if !state.applyCandidate(candidate, opportunity, big.NewInt(2000)) || !state.advanceTo(2) {
		t.Fatal("failed to apply and close base-reinvestment search state")
	}
	later := &crossOpportunity{comparison: publicOrderComparison{
		first:  publicLimitOrder{insertAfter: 3, orderID: "later-ask", leg: Leg{BaseToken: "A", QuoteToken: "Q", Side: pb.OrderSide_SELL}},
		second: publicLimitOrder{insertAfter: 4, orderID: "later-bid", leg: Leg{BaseToken: "A", QuoteToken: "Q", Side: pb.OrderSide_BUY}},
	}}
	if state.canStart(later, Context{MaxTxs: 8}) {
		t.Fatal("base reinvestment allowed a later opportunity to reuse the changed orderbook trace")
	}
}

func TestTracedCrossAllowsCounterpartyWithAdditionalPrefixTransaction(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "Q", "1000")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicTransfer("alice-later-transfer", "alice", 1),
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(2)),
	}
	ctx := proposalContext("traced-repeated-public-sender", len(public), 2)
	market := proposalMarket(ctx)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "ask", "alice", "A", "Q", pb.OrderSide_SELL, scaledPrice(1), "10", "10", nil,
			spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), "ask", "alice", "10")),
		spotExecution(3, "bid", "bob", "A", "Q", pb.OrderSide_BUY, scaledPrice(2), "10", "0",
			[]interfaces.ProposalSpotMatch{spotMatch("ask", "10", scaledPrice(1))}),
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil || batch == nil {
		t.Fatalf("traced cross rejected because a counterparty sent another public transaction: batch=%+v err=%v", batch, err)
	}
	if got := batch.InsertAfterPublic; !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("traced repeated-sender insertion schedule=%v, want [1 3]", got)
	}
}

func TestTracedPortfolioCombinesIndependentRestoringIntervals(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 5)
	seedBalance(t, state, testArbitrageAccount, "Q", "1000")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("a-ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicSpotOrder("a-bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(2)),
		publicSpotOrder("b-ask", "carol", 0, "B", "Q", pb.OrderSide_SELL, "20", scaledPrice(1)),
		publicSpotOrder("b-bid", "dave", 0, "B", "Q", pb.OrderSide_BUY, "20", scaledPrice(3)),
	}
	ctx := proposalContext("traced-portfolio", len(public), 4)
	market := proposalMarket(ctx)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "a-ask", "alice", "A", "Q", pb.OrderSide_SELL, scaledPrice(1), "10", "10", nil,
			spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), "a-ask", "alice", "10")),
		spotExecution(2, "a-bid", "bob", "A", "Q", pb.OrderSide_BUY, scaledPrice(2), "10", "0",
			[]interfaces.ProposalSpotMatch{spotMatch("a-ask", "10", scaledPrice(1))}),
		spotExecution(3, "b-ask", "carol", "B", "Q", pb.OrderSide_SELL, scaledPrice(1), "20", "20", nil,
			spotLevel("B", "Q", pb.OrderSide_BUY, scaledPrice(1), "b-ask", "carol", "20")),
		spotExecution(4, "b-bid", "dave", "B", "Q", pb.OrderSide_BUY, scaledPrice(3), "20", "0",
			[]interfaces.ProposalSpotMatch{spotMatch("b-ask", "20", scaledPrice(1))}),
	}
	batch, err := machine.Prepare(ctx, public, market)
	if err != nil || batch == nil || len(batch.Transactions) != 4 {
		t.Fatalf("traced portfolio=%+v err=%v", batch, err)
	}
	if !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2, 3, 4}) {
		t.Fatalf("traced portfolio insertions=%v", batch.InsertAfterPublic)
	}
	for i, tx := range batch.Transactions {
		if tx.GetBase().Nonce != 5+uint64(i) {
			t.Fatalf("leg %d nonce=%d", i, tx.GetBase().Nonce)
		}
	}
}

func TestTracedPortfolioOverlapsIndependentBooksWithinLiveBalance(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 7)
	                                                                            
	                                                                          
	seedBalance(t, state, testArbitrageAccount, "Q", "15")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	machine.allowUntracedCross = false
	public := []*pb.AnyTx{
		publicSpotOrder("a-ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicSpotOrder("b-ask", "carol", 0, "B", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicSpotOrder("b-bid", "dave", 0, "B", "Q", pb.OrderSide_BUY, "10", scaledPrice(4)),
		publicSpotOrder("a-bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(3)),
	}
	ctx := proposalContext("traced-overlap-balance", len(public), 4)
	market := proposalMarket(ctx)
	market.Executions = []interfaces.ProposalSpotExecution{
		spotExecution(1, "a-ask", "alice", "A", "Q", pb.OrderSide_SELL, scaledPrice(1), "10", "10", nil,
			spotLevel("A", "Q", pb.OrderSide_BUY, scaledPrice(1), "a-ask", "alice", "10")),
		spotExecution(2, "b-ask", "carol", "B", "Q", pb.OrderSide_SELL, scaledPrice(1), "10", "10", nil,
			spotLevel("B", "Q", pb.OrderSide_BUY, scaledPrice(1), "b-ask", "carol", "10")),
		spotExecution(3, "b-bid", "dave", "B", "Q", pb.OrderSide_BUY, scaledPrice(4), "10", "0",
			[]interfaces.ProposalSpotMatch{spotMatch("b-ask", "10", scaledPrice(1))}),
		spotExecution(4, "a-bid", "bob", "A", "Q", pb.OrderSide_BUY, scaledPrice(3), "10", "0",
			[]interfaces.ProposalSpotMatch{spotMatch("a-ask", "10", scaledPrice(1))}),
	}

	batch, err := machine.Prepare(ctx, public, market)
	if err != nil || batch == nil {
		t.Fatalf("overlapping traced portfolio=%+v err=%v", batch, err)
	}
	if got := batch.InsertAfterPublic; !reflect.DeepEqual(got, []int{1, 2, 3, 4}) {
		t.Fatalf("overlapping insertion schedule=%v, want [1 2 3 4]", got)
	}
	wantBase := []string{"A", "B", "B", "A"}
	wantAmount := []string{"10", "5", "5", "10"}
	for i, tx := range batch.Transactions {
		if tx.GetOrderTx().BaseToken != wantBase[i] || tx.GetOrderTx().Amount != wantAmount[i] {
			t.Fatalf("leg %d base/amount=%s/%s, want %s/%s", i, tx.GetOrderTx().BaseToken, tx.GetOrderTx().Amount, wantBase[i], wantAmount[i])
		}
		if tx.GetBase().Nonce != 7+uint64(i) {
			t.Fatalf("leg %d nonce=%d, want %d", i, tx.GetBase().Nonce, 7+uint64(i))
		}
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"a-ask", "b-ask", "b-bid", "a-bid"},
		[]string{"10", "5", "5", "10"},
		[]string{scaledPrice(1), scaledPrice(1), scaledPrice(4), scaledPrice(3)},
	)
}

func TestCrossPortfolioDoesNotOverlapOneOrderbookOrReuseMaker(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "Q", "100")
	seedBalance(t, state, testArbitrageAccount, feeToken, "10000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("ask-1", "alice", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicSpotOrder("ask-2", "bob", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
		publicSpotOrder("shared-bid", "carol", 0, "A", "Q", pb.OrderSide_BUY, "20", scaledPrice(3)),
	}
	ctx := proposalContext("same-book-conflict", len(public), 4)

	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil || batch == nil {
		t.Fatalf("same-book portfolio=%+v err=%v", batch, err)
	}
	if len(batch.Transactions) != 2 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 3}) {
		t.Fatalf("same-book overlap reused a maker: %+v", batch)
	}
}

func TestCrossPortfolioCombinesTwoSameTokenIntervalsWithSequentialNonces(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 21)
	seedBalance(t, state, testArbitrageAccount, "FB", "10000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("a-ask", "alice", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000)),
		publicSpotOrder("a-bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "10", scaledPrice(2000)),
		publicSpotOrder("b-ask", "carol", 0, "B", "FB", pb.OrderSide_SELL, "20", scaledPrice(1000)),
		publicSpotOrder("b-bid", "dave", 0, "B", "FB", pb.OrderSide_BUY, "20", scaledPrice(3000)),
	}
	ctx := proposalContext("same-token-portfolio", len(public), 4)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || len(batch.Transactions) != 4 || !reflect.DeepEqual(batch.InsertAfterPublic, []int{1, 2, 3, 4}) {
		t.Fatalf("same-token portfolio=%+v, want four interleaved legs", batch)
	}
	for i, tx := range batch.Transactions {
		if tx.GetBase().Nonce != 21+uint64(i) {
			t.Fatalf("leg %d nonce=%d, want %d", i, tx.GetBase().Nonce, 21+uint64(i))
		}
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"a-ask", "a-bid", "b-ask", "b-bid"},
		[]string{"10", "10", "20", "20"},
		[]string{scaledPrice(1000), scaledPrice(2000), scaledPrice(1000), scaledPrice(3000)},
	)

	mutated := clonePrivateTxBatch(batch)
	mutated.Transactions[0].GetBase().Nonce++
	if err := machine.ValidateCandidate(ctx, mutated, &interfaces.ProposalSimulationResult{Valid: true}); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("portfolio nonce mutation accepted: %v", err)
	}
}

func TestCrossPortfolioCombinesMixedNativeProfitTokens(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000")
	seedBalance(t, state, testArbitrageAccount, "Q", "1000")
	seedBalance(t, state, testArbitrageAccount, "R", "1000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("a-ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "10", scaledPrice(10)),
		publicSpotOrder("a-bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "10", scaledPrice(20)),
		publicSpotOrder("b-ask", "carol", 0, "B", "R", pb.OrderSide_SELL, "10", scaledPrice(20)),
		publicSpotOrder("b-bid", "dave", 0, "B", "R", pb.OrderSide_BUY, "10", scaledPrice(40)),
	}
	ctx := proposalContext("mixed-token-portfolio", len(public), 4)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil || batch == nil || len(batch.Transactions) != 4 {
		t.Fatalf("mixed-token portfolio=%+v err=%v", batch, err)
	}
	validateCrossExecution(t, machine, ctx, batch,
		[]string{"a-ask", "a-bid", "b-ask", "b-bid"},
		[]string{"10", "10", "10", "10"},
		[]string{scaledPrice(10), scaledPrice(20), scaledPrice(20), scaledPrice(40)},
	)
}

func TestCrossPortfolioSelectsMoreThanEightCompatibleGroups(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 30)
	seedBalance(t, state, testArbitrageAccount, "FB", "1000000")
	seedBalance(t, state, testArbitrageAccount, "Q", "1000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := sequentialCrossOrders(9, "Q")
	ctx := proposalContext("more-than-eight-groups", len(public), 18)
	ctx.MaxTxs = 36

	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("nine-group portfolio was not prepared")
	}
	if len(batch.Transactions) != 18 {
		t.Fatalf("portfolio private tx count=%d, want all 18 legs from nine compatible groups: %+v", len(batch.Transactions), batch)
	}
	for i, tx := range batch.Transactions {
		if tx.GetBase().Nonce != 30+uint64(i) {
			t.Fatalf("leg %d nonce=%d, want %d", i, tx.GetBase().Nonce, 30+uint64(i))
		}
		if batch.InsertAfterPublic[i] != i+1 {
			t.Fatalf("leg %d insertion=%d, want %d", i, batch.InsertAfterPublic[i], i+1)
		}
	}
}

func TestCrossPortfolioGroupCountIsBoundedByFinalBlockCapacity(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "1000000")
	seedBalance(t, state, testArbitrageAccount, "Q", "1000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := sequentialCrossOrders(6, "Q")
	ctx := proposalContext("block-capacity-groups", len(public), 20)
	                                                                        
	                                                                         
	                                                                      
	ctx.MaxTxs = 20

	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("capacity-bounded portfolio was not prepared")
	}
	if len(batch.Transactions) != 10 {
		t.Fatalf("capacity-bounded private tx count=%d, want ten legs from five groups: %+v", len(batch.Transactions), batch)
	}
	if got := batch.InsertAfterPublic[len(batch.InsertAfterPublic)-1]; got != 10 {
		t.Fatalf("last selected public maker=%d, want 10 before the trimmed tail", got)
	}
}

func TestCrossPortfolioGroupCountIsBoundedByFeeBalance(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	                                                                       
	                                                                           
	seedBalance(t, state, testArbitrageAccount, "FB", "6000")
	seedBalance(t, state, testArbitrageAccount, "Q", "1000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := sequentialCrossOrders(6, "Q")
	ctx := proposalContext("fee-capacity-groups", len(public), 12)
	ctx.MaxTxs = 24

	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil {
		t.Fatal("fee-bounded portfolio was not prepared")
	}
	if len(batch.Transactions) != 6 {
		t.Fatalf("fee-bounded private tx count=%d, want six legs from three groups: %+v", len(batch.Transactions), batch)
	}
}

func TestCrossPortfolioMaximizesProfitWithIndependentOverlap(t *testing.T) {
	tests := []struct {
		name           string
		highBidPrice   int64
		wantBase       string
		wantInsertions []int
	}{
		{
			name:           "high_interval_combines_with_independent_early_interval",
			highBidPrice:   10,
			wantBase:       "LOW-1",
			wantInsertions: []int{1, 2, 3, 5},
		},
		{
			name:           "group_limit_keeps_the_best_overlapping_pair",
			highBidPrice:   8,
			wantBase:       "LOW-1",
			wantInsertions: []int{1, 2, 3, 5},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newMemoryState()
			seedAccount(t, state, testArbitrageAccount, 0)
			seedBalance(t, state, testArbitrageAccount, "FB", "1000000")
			seedBalance(t, state, testArbitrageAccount, "Q", "1000000")
			machine := enabledMachine(t, state, DefaultConfig(), new(int))
			public := []*pb.AnyTx{
				publicSpotOrder("low-1-ask", "alice", 0, "LOW-1", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
				publicSpotOrder("high-ask", "bob", 0, "HIGH", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
				publicSpotOrder("low-1-bid", "carol", 0, "LOW-1", "Q", pb.OrderSide_BUY, "10", scaledPrice(5)),
				publicSpotOrder("low-2-ask", "dave", 0, "LOW-2", "Q", pb.OrderSide_SELL, "10", scaledPrice(1)),
				publicSpotOrder("high-bid", "erin", 0, "HIGH", "Q", pb.OrderSide_BUY, "10", scaledPrice(test.highBidPrice)),
				publicSpotOrder("low-2-bid", "frank", 0, "LOW-2", "Q", pb.OrderSide_BUY, "10", scaledPrice(5)),
			}
			ctx := proposalContext("profit-not-cardinality-"+test.name, len(public), 4)
			ctx.MaxTxs = 10

			batch, err := machine.Prepare(ctx, public, nil)
			if err != nil {
				t.Fatal(err)
			}
			if batch == nil {
				t.Fatal("profit-maximizing portfolio was not prepared")
			}
			if !reflect.DeepEqual(batch.InsertAfterPublic, test.wantInsertions) {
				t.Fatalf("profit-maximizing schedule=%v, want %v: %+v", batch.InsertAfterPublic, test.wantInsertions, batch)
			}
			if got := batch.Transactions[0].GetOrderTx().BaseToken; got != test.wantBase {
				t.Fatalf("first selected base=%s, want %s", got, test.wantBase)
			}
		})
	}
}

func TestCrossPortfolioFeeReserveUsesFundedOpportunityBound(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	                                                                            
	                                                                    
	seedBalance(t, state, testArbitrageAccount, "FB", "5000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("ask", "alice", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000)),
		publicSpotOrder("bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "10", scaledPrice(2000)),
	}
	ctx := proposalContext("exact-fee-reserve", len(public), 4)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil || batch == nil {
		t.Fatalf("fee-sized portfolio=%+v err=%v", batch, err)
	}
	if got := batch.Transactions[0].GetOrderTx().Amount; got != "3" {
		t.Fatalf("first amount=%s, want 3 after exactly two reserved fees", got)
	}
}

func TestCrossPortfolioRejectsMakerBeyondCapacityCutoff(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "1000000")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		publicTransfer("f1", "x1", 0), publicTransfer("f2", "x2", 0),
		publicTransfer("f3", "x3", 0), publicTransfer("f4", "x4", 0),
		publicTransfer("f5", "x5", 0), publicTransfer("f6", "x6", 0),
		publicSpotOrder("late-ask", "alice", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(10)),
		publicSpotOrder("late-bid", "bob", 0, "A", "FB", pb.OrderSide_BUY, "10", scaledPrice(20)),
	}
	ctx := proposalContext("late-cutoff", len(public), 4)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil {
		t.Fatal(err)
	}
	if batch != nil {
		t.Fatalf("candidate references public maker that private legs would evict: %+v", batch)
	}
}

func TestReverseSizingChecksIntegerRoundingNeighbour(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "100000")
	seedBalance(t, state, testArbitrageAccount, "A", "3")
	machine := enabledMachine(t, state, DefaultConfig(), new(int))
	public := []*pb.AnyTx{
		                                                          
		                                                          
		publicSpotOrder("bid", "bob", 0, "A", "Q", pb.OrderSide_BUY, "3", "150000000"),
		publicSpotOrder("ask", "alice", 0, "A", "Q", pb.OrderSide_SELL, "4", "125000000"),
	}
	ctx := proposalContext("reverse-rounding-neighbour", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil || batch == nil {
		t.Fatalf("rounding portfolio=%+v err=%v", batch, err)
	}
	if sold, bought := batch.Transactions[0].GetOrderTx().Amount, batch.Transactions[1].GetOrderTx().Amount; sold != "2" || bought != "3" {
		t.Fatalf("reverse sizing sold/bought=%s/%s, want 2/3", sold, bought)
	}
}

func TestCrossComparisonBudgetRoundRobinsAcrossPairs(t *testing.T) {
	state := newMemoryState()
	seedAccount(t, state, testArbitrageAccount, 0)
	seedBalance(t, state, testArbitrageAccount, "FB", "1000000")
	cfg := DefaultConfig()
	cfg.MaxCycleEvaluations = 2
	machine := enabledMachine(t, state, cfg, new(int))
	public := []*pb.AnyTx{
		publicSpotOrder("a1", "a1", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000)),
		publicSpotOrder("a2", "a2", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000)),
		publicSpotOrder("a3", "a3", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000)),
		publicSpotOrder("a4", "a4", 0, "A", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000)),
		publicSpotOrder("z-ask", "alice", 0, "Z", "FB", pb.OrderSide_SELL, "10", scaledPrice(1000)),
		publicSpotOrder("z-bid", "bob", 0, "Z", "FB", pb.OrderSide_BUY, "10", scaledPrice(2000)),
	}
	ctx := proposalContext("round-robin-pairs", len(public), 2)
	batch, err := machine.Prepare(ctx, public, nil)
	if err != nil || batch == nil {
		t.Fatalf("later funded pair starved by first pair: batch=%+v err=%v", batch, err)
	}
	if got := batch.Transactions[0].GetOrderTx().BaseToken; got != "Z" {
		t.Fatalf("selected base=%s, want Z", got)
	}
}
