package arbitrage

import (
	"crypto/sha256"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/referral"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type futureCrossOpportunity struct {
	pair           string
	quoteToken     string
	cross          crossOpportunity
	startAvailable map[string]*big.Int
	endAvailable   map[string]*big.Int
}

type pendingFutureCredit struct {
	insertion int
	pair      string
	token     string
	amount    *big.Int
	available map[string]*big.Int
}

type futurePortfolioState struct {
	position int
	                                                                          
	                                                                          
	                                                                            
	adjustments  map[string]*big.Int
	pending      []pendingFutureCredit
	activePairs  map[string]int
	usedMakers   map[string]struct{}
	selected     []*plan
	maxInsertion int
	score        big.Int
}

                                                                         
                                                                           
                                                                            
                                                     
func (f *finder) findBestFutureCross(ctx Context, nonce uint64, orderedPublic []*pb.AnyTx, market *MarketSnapshot) (*plan, error) {
	if !f.config.Enabled || market == nil || ctx.PrivateTxLimit < 2 || len(orderedPublic) < 2 || len(market.FutureExecutions) < 2 {
		return nil, nil
	}
	ordersByPair, err := f.collectTracedFutureOrders(ctx, orderedPublic, market)
	if err != nil {
		return nil, err
	}
	if len(ordersByPair) == 0 {
		return nil, nil
	}
	pairs := make([]string, 0, len(ordersByPair))
	for pair := range ordersByPair {
		pairs = append(pairs, pair)
	}
	sort.Strings(pairs)

	remainingEvaluations := f.config.MaxCycleEvaluations
	cursors := make([]publicPairComparisonCursor, 0, len(pairs))
	cursorPairs := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if len(ordersByPair[pair]) < 2 {
			continue
		}
		if occupied, readErr := f.hasParentFuturePosition(pair); readErr != nil {
			return nil, readErr
		} else if occupied {
			                                                                   
			                                                           
			continue
		}
		cursors = append(cursors, publicPairComparisonCursor{orders: ordersByPair[pair]})
		cursorPairs = append(cursorPairs, pair)
	}
	opportunities := make([]futureCrossOpportunity, 0)
	for remainingEvaluations > 0 {
		advanced := false
		for i := range cursors {
			if remainingEvaluations == 0 {
				break
			}
			first, second, ok := cursors[i].next()
			if !ok {
				continue
			}
			advanced = true
			remainingEvaluations--
			cross, viable := buildCrossOpportunity(publicOrderComparison{first: first, second: second})
			if !viable {
				continue
			}
			pair := cursorPairs[i]
			identity, identityErr := f.futureMarketIdentity(pair)
			if identityErr != nil || identity.QuoteToken != first.leg.QuoteToken {
				continue
			}
			opportunities = append(opportunities, futureCrossOpportunity{
				pair: pair, quoteToken: identity.QuoteToken, cross: *cross,
				startAvailable: cloneFutureBalances(first.futureAvailable),
				endAvailable:   cloneFutureBalances(second.futureAvailable),
			})
		}
		if !advanced {
			break
		}
	}
	if len(opportunities) == 0 {
		return nil, nil
	}
	sort.SliceStable(opportunities, func(i, j int) bool {
		left, right := opportunities[i], opportunities[j]
		if left.cross.start() != right.cross.start() {
			return left.cross.start() < right.cross.start()
		}
		if left.cross.end() != right.cross.end() {
			return left.cross.end() < right.cross.end()
		}
		if left.pair != right.pair {
			return left.pair < right.pair
		}
		return left.cross.comparison.first.orderID < right.cross.comparison.first.orderID
	})
	return f.selectFuturePortfolio(ctx, nonce, opportunities, market.FutureAvailable)
}

func (f *finder) hasParentFuturePosition(pair string) (bool, error) {
	for _, side := range []future.Side{future.Long, future.Short} {
		data, err := f.state.Get(keys.KeyFutureOwnerPairSidePosition(f.account, pair, int32(side)))
		if err != nil {
			                                                              
			                                                                  
			                                                         
			if isMissingStateKey(err) {
				continue
			}
			return false, fmt.Errorf("read arbitrage future position %s side=%d: %w", pair, side, err)
		}
		if len(data) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (f *finder) collectTracedFutureOrders(ctx Context, orderedPublic []*pb.AnyTx, market *MarketSnapshot) (map[string][]publicLimitOrder, error) {
	byPair := make(map[string][]publicLimitOrder)
	lastPosition := 0
	for _, execution := range market.FutureExecutions {
		position := execution.PublicPosition
		if position <= lastPosition || position < 1 || position > len(orderedPublic) {
			return nil, fmt.Errorf("invalid future execution trace position %d after %d", position, lastPosition)
		}
		lastPosition = position
		tx := orderedPublic[position-1]
		if tx == nil || !pb.TxRequiresAccountNonce(tx) || tx.GetBase() == nil || tx.GetFutureOrderTx() == nil {
			return nil, fmt.Errorf("future execution trace position %d does not bind a nonce future order", position)
		}
		if err := pb.ValidateTxExecutionGuard(tx, ctx.Height, ctx.Proposer); err != nil {
			return nil, fmt.Errorf("future execution trace position %d guard mismatch: %w", position, err)
		}
		base, order := tx.GetBase(), tx.GetFutureOrderTx()
		side, validSide := strategySideFromFutureRaw(order.Side)
		owner := strings.TrimSpace(base.FromAddress)
		if order.IsClose || order.IsMarket || !validSide ||
			owner == "" || strings.TrimSpace(base.TxId) == "" || strings.TrimSpace(order.Pair) == "" || order.Leverage == 0 {
			return nil, fmt.Errorf("future execution trace position %d binds malformed limit order", position)
		}
		identity, identityErr := f.futureMarketIdentity(order.Pair)
		if identityErr != nil || execution.OrderID != base.TxId || execution.Owner != owner || execution.Pair != order.Pair ||
			execution.QuoteToken != identity.QuoteToken || execution.Side != side || execution.Leverage != order.Leverage {
			return nil, fmt.Errorf("future execution trace position %d does not match public order %s", position, base.TxId)
		}
		baseToken, quoteToken := identity.BaseToken, identity.QuoteToken
		quantity, quantityErr := parseUint256("traced future quantity", execution.Quantity)
		declaredQuantity, declaredQuantityErr := parseUint256("public future quantity", order.Quantity)
		price, priceErr := parseUint256("traced future price", execution.Price)
		declaredPrice, declaredPriceErr := parseUint256("public future price", order.Price)
		remaining, remainingErr := parseUint256("traced future remaining", execution.Remaining)
		if quantityErr != nil || declaredQuantityErr != nil || priceErr != nil || declaredPriceErr != nil || remainingErr != nil ||
			quantity.Sign() <= 0 || price.Sign() <= 0 || quantity.Cmp(declaredQuantity) != 0 || price.Cmp(declaredPrice) != 0 ||
			remaining.Cmp(quantity) > 0 || !basePriceProductFits(quantity, price) {
			return nil, fmt.Errorf("future execution trace %s has inconsistent quantity/price/remaining", base.TxId)
		}

		matches := make([]publicOrderMatch, 0, len(execution.Matches))
		totalMatched := new(big.Int)
		for _, match := range execution.Matches {
			matchedQty, qtyErr := parseUint256("traced future match quantity", match.Quantity)
			matchPrice, matchPriceErr := parseUint256("traced future match price", match.Price)
			if strings.TrimSpace(match.MakerOrderID) == "" || qtyErr != nil || matchPriceErr != nil ||
				matchedQty.Sign() <= 0 || matchPrice.Sign() <= 0 || !basePriceProductFits(matchedQty, matchPrice) {
				return nil, fmt.Errorf("future execution trace %s has malformed match", base.TxId)
			}
			totalMatched.Add(totalMatched, matchedQty)
			if totalMatched.Cmp(maxUint256) > 0 {
				return nil, fmt.Errorf("future execution trace %s match total overflows", base.TxId)
			}
			matches = append(matches, publicOrderMatch{makerOrderID: match.MakerOrderID, quantity: matchedQty, price: matchPrice})
		}
		if new(big.Int).Add(totalMatched, remaining).Cmp(quantity) != 0 {
			return nil, fmt.Errorf("future execution trace %s matched=%s remaining=%s quantity=%s", base.TxId, totalMatched, remaining, quantity)
		}

		postBest := make(map[pb.OrderSide]publicOrderBest, len(execution.Levels))
		for _, level := range execution.Levels {
			if level.Pair != order.Pair || level.QuoteToken != quoteToken ||
				(level.Side != pb.OrderSide_BUY && level.Side != pb.OrderSide_SELL) || len(level.Makers) != 1 {
				return nil, fmt.Errorf("future execution trace %s has malformed post level", base.TxId)
			}
			if _, duplicate := postBest[level.Side]; duplicate {
				return nil, fmt.Errorf("future execution trace %s repeats post level side %s", base.TxId, level.Side.String())
			}
			levelPrice, levelPriceErr := parseUint256("traced future post price", level.Price)
			makerRemaining, makerRemainingErr := parseUint256("traced future post maker remaining", level.Makers[0].Remaining)
			maker := level.Makers[0]
			if levelPriceErr != nil || makerRemainingErr != nil || levelPrice.Sign() <= 0 || makerRemaining.Sign() <= 0 ||
				strings.TrimSpace(maker.OrderID) == "" || strings.TrimSpace(maker.Owner) == "" ||
				!basePriceProductFits(makerRemaining, levelPrice) {
				return nil, fmt.Errorf("future execution trace %s has invalid post maker", base.TxId)
			}
			postBest[level.Side] = publicOrderBest{price: levelPrice, orderID: maker.OrderID, owner: maker.Owner, remaining: makerRemaining}
		}
		if owner == f.account {
			continue
		}
		futureAvailable, availableErr := parseFutureBoundaryAvailable(base.TxId, quoteToken, execution.TakerAvailable)
		if availableErr != nil {
			return nil, availableErr
		}
		byPair[order.Pair] = append(byPair[order.Pair], publicLimitOrder{
			insertAfter:     position,
			orderID:         base.TxId,
			owner:           owner,
			leg:             Leg{BaseToken: baseToken, QuoteToken: quoteToken, Side: side},
			amount:          quantity,
			price:           price,
			remaining:       remaining,
			matches:         matches,
			postBest:        postBest,
			traced:          true,
			futureLeverage:  order.Leverage,
			futureAvailable: futureAvailable,
		})
	}
	return byPair, nil
}

func parseFutureBoundaryAvailable(orderID, quoteToken string, raw map[string]string) (map[string]*big.Int, error) {
	tokens := []string{quoteToken}
	if quoteToken != feeToken {
		tokens = append(tokens, feeToken)
	}
	available := make(map[string]*big.Int, len(tokens))
	for _, token := range tokens {
		rawAmount, exists := raw[token]
		if !exists {
			return nil, fmt.Errorf("future execution trace %s is missing %s balance at its insertion boundary", orderID, token)
		}
		amount, err := parseUint256("traced future boundary balance", rawAmount)
		if err != nil {
			return nil, fmt.Errorf("future execution trace %s has invalid %s boundary balance: %w", orderID, token, err)
		}
		available[token] = amount
	}
	return available, nil
}

func cloneFutureBalances(source map[string]*big.Int) map[string]*big.Int {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]*big.Int, len(source))
	for token, amount := range source {
		if amount != nil {
			cloned[token] = new(big.Int).Set(amount)
		}
	}
	return cloned
}

func strategySideFromFutureRaw(raw int32) (pb.OrderSide, bool) {
	switch future.Side(raw) {
	case future.Long:
		return pb.OrderSide_BUY, true
	case future.Short:
		return pb.OrderSide_SELL, true
	default:
		return pb.OrderSide_BUY, false
	}
}

func futureSideFromStrategy(side pb.OrderSide) (future.Side, bool) {
	switch side {
	case pb.OrderSide_BUY:
		return future.Long, true
	case pb.OrderSide_SELL:
		return future.Short, true
	default:
		return 0, false
	}
}

func (f *finder) futureMarketIdentity(pair string) (protocol.MarketIdentity, error) {
	if f == nil || f.state == nil {
		return protocol.MarketIdentity{}, fmt.Errorf("future market state is unavailable")
	}
	data, err := f.state.Get(keys.KeyFutureMarketConfig(pair))
	if err == nil && len(data) > 0 {
		if identity, decodeErr := protocol.DecodeMarketIdentity(pair, data); decodeErr == nil {
			return identity, nil
		}
	}
	                                                                         
	                                                      
	if strings.Count(pair, "_") == 1 {
		baseToken, quoteToken, _ := strings.Cut(pair, "_")
		return protocol.NewMarketIdentity(baseToken, quoteToken)
	}
	if err != nil {
		return protocol.MarketIdentity{}, fmt.Errorf("read future market %s: %w", pair, err)
	}
	return protocol.MarketIdentity{}, fmt.Errorf("future market identity not registered for %s", pair)
}

func (f *finder) selectFuturePortfolio(ctx Context, nonce uint64, opportunities []futureCrossOpportunity, projected map[string]string) (*plan, error) {
	initial := make(map[string]*big.Int)
	load := func(token string) (*big.Int, error) {
		if value := initial[token]; value != nil {
			return new(big.Int).Set(value), nil
		}
		if raw, ok := projected[token]; ok {
			value, err := parseUint256("projected future available balance", raw)
			if err != nil {
				return nil, err
			}
			initial[token] = new(big.Int).Set(value)
			return value, nil
		}
		value, err := readAvailableBalance(f.state, f.account, token)
		if err != nil {
			return nil, err
		}
		initial[token] = new(big.Int).Set(value)
		return value, nil
	}
	for _, opportunity := range opportunities {
		if _, err := load(opportunity.quoteToken); err != nil {
			return nil, err
		}
	}
	if _, err := load(feeToken); err != nil {
		return nil, err
	}
	beam := []*futurePortfolioState{{
		adjustments: make(map[string]*big.Int), activePairs: make(map[string]int), usedMakers: make(map[string]struct{}),
	}}
	maxGroups := ctx.PrivateTxLimit / 2
	if capacityGroups := ctx.MaxTxs / 2; maxGroups > capacityGroups {
		maxGroups = capacityGroups
	}
	if maxGroups <= 0 {
		return nil, nil
	}
	feesPerGroup := new(big.Int).Mul(f.config.fee, big.NewInt(2))
	transitions := 0
	for _, opportunity := range opportunities {
		next := make([]*futurePortfolioState, 0, len(beam)*2)
		for _, current := range beam {
			skipped := current.clone()
			if skipped.advanceTo(opportunity.cross.start()) {
				next = append(next, skipped)
			}
			if transitions >= maxCrossPortfolioTransitions || len(current.selected) >= maxGroups {
				continue
			}
			chosen := current.clone()
			if !chosen.advanceTo(opportunity.cross.start()) || !chosen.canStart(&opportunity, ctx) {
				continue
			}
			available, ok := chosen.balanceAt(opportunity.startAvailable, opportunity.quoteToken)
			if !ok {
				continue
			}
			candidate, quoteErr := f.quoteFutureOpportunity(nonce, &opportunity, available, available, feesPerGroup)
			transitions++
			if quoteErr != nil {
				return nil, quoteErr
			}
			if candidate != nil && chosen.apply(candidate, &opportunity, feesPerGroup) {
				next = append(next, chosen)
			}
		}
		beam = pruneFutureBeam(next, maxGroups)
		if len(beam) == 0 {
			return nil, nil
		}
	}
	var best *futurePortfolioState
	for _, state := range beam {
		if !state.advanceTo(ctx.PublicPrefixCount) || len(state.pending) != 0 || len(state.selected) == 0 || !state.fitsFinalBalances(initial) {
			continue
		}
		if best == nil || state.score.Cmp(&best.score) > 0 ||
			(state.score.Cmp(&best.score) == 0 && futureSelectionKey(state) < futureSelectionKey(best)) {
			best = state
		}
	}
	if best == nil {
		return nil, nil
	}
	return buildCrossPortfolio(nonce, best.selected, portfolioUtility(best.selected)), nil
}

func (f *finder) quoteFutureOpportunity(nonce uint64, opportunity *futureCrossOpportunity, available, normalization, runtimeFees *big.Int) (*plan, error) {
	if opportunity == nil || available == nil || normalization == nil || runtimeFees == nil || available.Sign() <= 0 {
		return nil, nil
	}
	first, second := opportunity.cross.comparison.first, opportunity.cross.comparison.second
	if first.leg.Side == second.leg.Side || first.price == nil || second.price == nil {
		return nil, nil
	}
	openSide := oppositeSpotSide(first.leg.Side)
	closeSide := oppositeSpotSide(openSide)
	positionSide := future.Long
	if openSide == pb.OrderSide_SELL {
		positionSide = future.Short
	}
	grossAtOne := future.CalcUnrealizedPnL(positionSide, first.price, second.price, big.NewInt(1))
	if grossAtOne.Sign() < 0 {
		return nil, nil
	}
	capacity := opportunity.cross.firstCapacity
	if capacity == nil || capacity.Sign() <= 0 {
		return nil, nil
	}
	budget := new(big.Int).Mul(available, new(big.Int).SetUint64(uint64(f.config.capitalBPS)))
	budget.Quo(budget, bigTenK)
	if budget.Sign() <= 0 {
		return nil, nil
	}
	leverage := first.futureLeverage
	if second.futureLeverage > leverage {
		leverage = second.futureLeverage
	}
	if leverage == 0 {
		return nil, nil
	}
	capitalFor := func(quantity *big.Int) *big.Int {
		margin := future.CalcRequiredMargin(quantity, first.price, leverage)
		fees := new(big.Int).Add(futureTakerFee(quantity, first.price), futureTakerFee(quantity, second.price))
		capital := new(big.Int).Add(margin, fees)
		if opportunity.quoteToken == feeToken {
			capital.Add(capital, runtimeFees)
		}
		return capital
	}
	low, high := new(big.Int), new(big.Int).Set(capacity)
	for low.Cmp(high) < 0 {
		mid := new(big.Int).Add(low, high)
		mid.Add(mid, bigOne)
		mid.Rsh(mid, 1)
		if capitalFor(mid).Cmp(budget) <= 0 {
			low.Set(mid)
		} else {
			high.Sub(mid, bigOne)
		}
	}
	quantity := low
	if quantity.Sign() <= 0 || !basePriceProductFits(quantity, first.price) || !basePriceProductFits(quantity, second.price) {
		return nil, nil
	}
	capital := capitalFor(quantity)
	if capital.Sign() <= 0 || capital.Cmp(available) > 0 {
		return nil, nil
	}
	gross := future.CalcUnrealizedPnL(positionSide, first.price, second.price, quantity)
	tradingFees := new(big.Int).Add(futureTakerFee(quantity, first.price), futureTakerFee(quantity, second.price))
	profit := new(big.Int).Sub(gross, tradingFees)
	if opportunity.quoteToken == feeToken {
		profit.Sub(profit, runtimeFees)
	}
	if profit.Sign() <= 0 {
		return nil, nil
	}
	roiBPS := new(big.Int).Mul(new(big.Int).Set(profit), bigTenK)
	roiBPS.Quo(roiBPS, capital)
	if roiBPS.Cmp(new(big.Int).SetUint64(uint64(f.config.MinProfitBPS))) < 0 {
		return nil, nil
	}
	name := futureCrossRouteName(opportunity.pair, first.orderID, second.orderID)
	finalOutput := new(big.Int).Add(new(big.Int).Set(capital), profit)
	plan := &plan{
		routeName: name, startToken: opportunity.quoteToken, profitToken: opportunity.quoteToken,
		startSpent: new(big.Int).Set(capital), finalOutput: finalOutput, profit: profit, roiBPS: roiBPS,
		nonce: nonce, restoresBook: true,
		insertAfterPublic:   []int{first.insertAfter, second.insertAfter},
		futureStartBalances: cloneFutureBalances(opportunity.startAvailable),
	}
	plan.legs = []plannedLeg{
		{
			leg:        Leg{BaseToken: first.leg.BaseToken, QuoteToken: opportunity.quoteToken, Side: openSide},
			baseAmount: new(big.Int).Set(quantity), price: new(big.Int).Set(first.price),
			inputSpent: new(big.Int).Set(capital), output: new(big.Int).Set(quantity), makerIDs: []string{first.orderID},
			market: plannedMarketFuture, futurePair: opportunity.pair, futureLeverage: leverage, futureGroup: name,
		},
		{
			leg:        Leg{BaseToken: second.leg.BaseToken, QuoteToken: opportunity.quoteToken, Side: closeSide},
			baseAmount: new(big.Int).Set(quantity), price: new(big.Int).Set(second.price),
			inputSpent: new(big.Int).Set(quantity), output: new(big.Int).Set(finalOutput), makerIDs: []string{second.orderID},
			market: plannedMarketFuture, futurePair: opportunity.pair, futureLeverage: leverage, futureClose: true, futureGroup: name,
		},
	}
	profitNormalization := new(big.Int).Mul(
		new(big.Int).Set(normalization),
		new(big.Int).SetUint64(uint64(f.config.capitalBPS)),
	)
	profitNormalization.Quo(profitNormalization, bigTenK)
	if profitNormalization.Sign() <= 0 {
		profitNormalization = bigOne
	}
	plan.utility = new(big.Rat).SetFrac(new(big.Int).Set(profit), new(big.Int).Set(profitNormalization))
	return plan, nil
}

func futureTakerFee(quantity, price *big.Int) *big.Int {
	notional := future.CalcNotional(quantity, price)
	fee := new(big.Int).Mul(notional, new(big.Int).SetUint64(referral.DefaultConfig.TakerFeeRate))
	return fee.Quo(fee, big.NewInt(referral.BPS))
}

func futureCrossRouteName(pair, firstOrderID, secondOrderID string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("future-cross"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(pair))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(firstOrderID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(secondOrderID))
	return "future-cross-" + hex.EncodeToString(h.Sum(nil)[:12])
}

func (state *futurePortfolioState) clone() *futurePortfolioState {
	cloned := &futurePortfolioState{
		position: state.position, adjustments: make(map[string]*big.Int, len(state.adjustments)),
		pending: append([]pendingFutureCredit(nil), state.pending...), activePairs: make(map[string]int, len(state.activePairs)),
		usedMakers: make(map[string]struct{}, len(state.usedMakers)), selected: append([]*plan(nil), state.selected...),
		maxInsertion: state.maxInsertion,
	}
	cloned.score.Set(&state.score)
	for token, adjustment := range state.adjustments {
		cloned.adjustments[token] = new(big.Int).Set(adjustment)
	}
	for i := range cloned.pending {
		cloned.pending[i].amount = new(big.Int).Set(state.pending[i].amount)
		cloned.pending[i].available = cloneFutureBalances(state.pending[i].available)
	}
	for pair, count := range state.activePairs {
		cloned.activePairs[pair] = count
	}
	for maker := range state.usedMakers {
		cloned.usedMakers[maker] = struct{}{}
	}
	return cloned
}

func (state *futurePortfolioState) balanceAt(publicAvailable map[string]*big.Int, token string) (*big.Int, bool) {
	if state == nil || publicAvailable[token] == nil {
		return nil, false
	}
	balance := new(big.Int).Set(publicAvailable[token])
	if adjustment := state.adjustments[token]; adjustment != nil {
		balance.Add(balance, adjustment)
	}
	if balance.Sign() < 0 || balance.Cmp(maxUint256) > 0 {
		return nil, false
	}
	return balance, true
}

func (state *futurePortfolioState) fitsBoundary(publicAvailable map[string]*big.Int) bool {
	if state == nil || len(publicAvailable) == 0 {
		return false
	}
	for token := range publicAvailable {
		if _, ok := state.balanceAt(publicAvailable, token); !ok {
			return false
		}
	}
	return true
}

func (state *futurePortfolioState) fitsFinalBalances(publicAvailable map[string]*big.Int) bool {
	if state == nil {
		return false
	}
	for token := range state.adjustments {
		if _, ok := state.balanceAt(publicAvailable, token); !ok {
			return false
		}
	}
	return true
}

func (state *futurePortfolioState) addAdjustment(token string, delta *big.Int) {
	if state.adjustments[token] == nil {
		state.adjustments[token] = new(big.Int)
	}
	state.adjustments[token].Add(state.adjustments[token], delta)
}

func (state *futurePortfolioState) advanceTo(position int) bool {
	if state == nil || position < state.position {
		return false
	}
	consumed := 0
	for consumed < len(state.pending) && state.pending[consumed].insertion <= position {
		pending := state.pending[consumed]
		                                                                      
		                                                                      
		if !state.fitsBoundary(pending.available) {
			return false
		}
		state.addAdjustment(pending.token, pending.amount)
		if !state.fitsBoundary(pending.available) {
			return false
		}
		if state.activePairs[pending.pair] <= 1 {
			delete(state.activePairs, pending.pair)
		} else {
			state.activePairs[pending.pair]--
		}
		consumed++
	}
	if consumed > 0 {
		state.pending = append([]pendingFutureCredit(nil), state.pending[consumed:]...)
	}
	state.position = position
	return true
}

func (state *futurePortfolioState) canStart(opportunity *futureCrossOpportunity, ctx Context) bool {
	if state == nil || opportunity == nil || state.activePairs[opportunity.pair] > 0 {
		return false
	}
	for _, maker := range []string{opportunity.cross.comparison.first.orderID, opportunity.cross.comparison.second.orderID} {
		if _, used := state.usedMakers[maker]; used {
			return false
		}
	}
	groups := len(state.selected) + 1
	retainedPublic := ctx.MaxTxs - 2*groups
	maxInsertion := state.maxInsertion
	if opportunity.cross.end() > maxInsertion {
		maxInsertion = opportunity.cross.end()
	}
	return retainedPublic >= 0 && maxInsertion <= retainedPublic
}

func (state *futurePortfolioState) apply(candidate *plan, opportunity *futureCrossOpportunity, runtimeFees *big.Int) bool {
	if state == nil || candidate == nil || opportunity == nil || len(candidate.legs) != 2 || candidate.startSpent == nil || candidate.finalOutput == nil {
		return false
	}
	quote := opportunity.quoteToken
	if !state.fitsBoundary(opportunity.startAvailable) {
		return false
	}
	current, ok := state.balanceAt(opportunity.startAvailable, quote)
	if !ok || current.Cmp(candidate.startSpent) < 0 {
		return false
	}
	state.addAdjustment(quote, new(big.Int).Neg(candidate.startSpent))
	if quote != feeToken {
		feeBalance, feeOK := state.balanceAt(opportunity.startAvailable, feeToken)
		if !feeOK || feeBalance.Cmp(runtimeFees) < 0 {
			state.addAdjustment(quote, candidate.startSpent)
			return false
		}
		state.addAdjustment(feeToken, new(big.Int).Neg(runtimeFees))
	}
	if !state.fitsBoundary(opportunity.startAvailable) {
		state.addAdjustment(quote, candidate.startSpent)
		if quote != feeToken {
			state.addAdjustment(feeToken, runtimeFees)
		}
		return false
	}
	state.activePairs[opportunity.pair]++
	state.pending = append(state.pending, pendingFutureCredit{
		insertion: opportunity.cross.end(), pair: opportunity.pair, token: quote,
		amount: new(big.Int).Set(candidate.finalOutput), available: cloneFutureBalances(opportunity.endAvailable),
	})
	sort.SliceStable(state.pending, func(i, j int) bool {
		if state.pending[i].insertion != state.pending[j].insertion {
			return state.pending[i].insertion < state.pending[j].insertion
		}
		return state.pending[i].pair < state.pending[j].pair
	})
	for _, maker := range []string{opportunity.cross.comparison.first.orderID, opportunity.cross.comparison.second.orderID} {
		state.usedMakers[maker] = struct{}{}
	}
	state.selected = append(state.selected, candidate)
	state.score.Add(&state.score, planFixedUtility(candidate))
	if opportunity.cross.end() > state.maxInsertion {
		state.maxInsertion = opportunity.cross.end()
	}
	return true
}

func pruneFutureBeam(states []*futurePortfolioState, maxGroups int) []*futurePortfolioState {
	if len(states) <= 1 {
		return states
	}
	sort.SliceStable(states, func(i, j int) bool {
		if cmp := states[i].score.Cmp(&states[j].score); cmp != 0 {
			return cmp > 0
		}
		if len(states[i].selected) != len(states[j].selected) {
			return len(states[i].selected) < len(states[j].selected)
		}
		return futureSelectionKey(states[i]) < futureSelectionKey(states[j])
	})
	limit := crossPortfolioBeamWidth
	if maxGroups > limit {
		limit = maxGroups
	}
	if limit > 256 {
		limit = 256
	}
	if len(states) > limit {
		states = states[:limit]
	}
	return states
}

func futureSelectionKey(state *futurePortfolioState) string {
	if state == nil {
		return ""
	}
	names := make([]string, 0, len(state.selected))
	for _, candidate := range state.selected {
		if candidate != nil {
			names = append(names, candidate.routeName)
		}
	}
	return strings.Join(names, "\x00")
}
