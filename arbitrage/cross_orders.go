package arbitrage

import (
	"crypto/sha256"
	"dex/pb"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

                                                                             
                                                                             
                                                                              
                                                
type publicLimitOrder struct {
	insertAfter int
	orderID     string
	owner       string
	leg         Leg
	amount      *big.Int
	price       *big.Int
	remaining   *big.Int
	matches     []publicOrderMatch
	postBest    map[pb.OrderSide]publicOrderBest
	traced      bool
	                                                                           
	                                                                             
	                                                       
	futureLeverage uint64
	                                                                         
	                                                                      
	futureAvailable map[string]*big.Int
}

type publicOrderMatch struct {
	makerOrderID string
	quantity     *big.Int
	price        *big.Int
}

type publicOrderBest struct {
	price     *big.Int
	orderID   string
	owner     string
	remaining *big.Int
}

type publicSpotPair struct {
	baseToken  string
	quoteToken string
}

type publicPairComparisonCursor struct {
	orders []publicLimitOrder
	gap    int
	i      int
}

func (cursor *publicPairComparisonCursor) next() (publicLimitOrder, publicLimitOrder, bool) {
	if cursor == nil || len(cursor.orders) < 2 {
		return publicLimitOrder{}, publicLimitOrder{}, false
	}
	if cursor.gap == 0 {
		cursor.gap = 1
	}
	                                                                          
	                                                                           
	                                                                
	for cursor.gap < len(cursor.orders) {
		if cursor.i+cursor.gap < len(cursor.orders) {
			first, second := cursor.orders[cursor.i], cursor.orders[cursor.i+cursor.gap]
			cursor.i++
			return first, second, true
		}
		cursor.gap++
		cursor.i = 0
	}
	return publicLimitOrder{}, publicLimitOrder{}, false
}

type publicOrderComparison struct {
	first  publicLimitOrder
	second publicLimitOrder
}

type crossOpportunity struct {
	comparison       publicOrderComparison
	startToken       string
	profitToken      string
	firstCapacity    *big.Int
	secondRemainder  *big.Int
	baselineRestored bool
	residualProfit   bool
}

func (op *crossOpportunity) start() int { return op.comparison.first.insertAfter }
func (op *crossOpportunity) end() int   { return op.comparison.second.insertAfter }

                                                                              
                           
                                                                               
                                                                             
                                                                               
  
                                                                           
                                                                              
                                                                              
func (f *finder) findBestCrossOrder(ctx Context, nonce uint64, orderedPublic []*pb.AnyTx, market *MarketSnapshot) (*plan, error) {
	if !f.config.Enabled || ctx.PrivateTxLimit < 2 || len(orderedPublic) < 2 {
		return nil, nil
	}
	orders, err := f.collectReadyLimitOrders(ctx, orderedPublic, market)
	if err != nil {
		return nil, err
	}
	if len(orders) < 2 {
		return nil, nil
	}

	balanceCache := make(map[string]*big.Int)
	readBalance := func(token string) (*big.Int, error) {
		if cached, ok := balanceCache[token]; ok {
			return new(big.Int).Set(cached), nil
		}
		balance, readErr := readAvailableBalance(f.state, f.account, token)
		if readErr != nil {
			return nil, readErr
		}
		balanceCache[token] = new(big.Int).Set(balance)
		return balance, nil
	}
	feesPerGroup := new(big.Int).Mul(f.config.fee, big.NewInt(2))
	feeBalance, err := readBalance(feeToken)
	if err != nil {
		return nil, err
	}
	if feeBalance.Cmp(feesPerGroup) < 0 {
		return nil, nil
	}
	byPair := make(map[publicSpotPair][]publicLimitOrder)
	for _, order := range orders {
		pair := publicSpotPair{baseToken: order.leg.BaseToken, quoteToken: order.leg.QuoteToken}
		byPair[pair] = append(byPair[pair], order)
	}
	pairs := make([]publicSpotPair, 0, len(byPair))
	for pair := range byPair {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].baseToken != pairs[j].baseToken {
			return pairs[i].baseToken < pairs[j].baseToken
		}
		return pairs[i].quoteToken < pairs[j].quoteToken
	})
	maxGroups := ctx.PrivateTxLimit / 2
	feeGroups := new(big.Int).Quo(new(big.Int).Set(feeBalance), feesPerGroup)
	if feeGroups.IsInt64() && feeGroups.Int64() < int64(maxGroups) {
		maxGroups = int(feeGroups.Int64())
	}
	if maxGroups <= 0 {
		return nil, nil
	}
	fundedTokens := make(map[string]bool)
	for _, pair := range pairs {
		for _, token := range []string{pair.baseToken, pair.quoteToken} {
			if _, known := fundedTokens[token]; known {
				continue
			}
			balance, balanceErr := readBalance(token)
			if balanceErr != nil {
				return nil, balanceErr
			}
			if token == feeToken {
				balance.Sub(balance, feesPerGroup)
			}
			fundedTokens[token] = balance.Sign() > 0
		}
	}
	maxGroups = maxPotentialCrossGroups(ctx, byPair, pairs, fundedTokens, maxGroups)
	if maxGroups <= 0 {
		return nil, nil
	}
	                                                                       
	                                                                     
	                                                                           
	comparisons := make([]publicOrderComparison, 0, f.config.MaxCycleEvaluations)
	remainingEvaluations := f.config.MaxCycleEvaluations
	cursors := make([]publicPairComparisonCursor, 0, len(pairs))
	for _, pair := range pairs {
		cursors = append(cursors, publicPairComparisonCursor{orders: byPair[pair]})
	}
	for remainingEvaluations > 0 {
		advanced := false
		for cursorIndex := range cursors {
			if remainingEvaluations == 0 {
				break
			}
			first, second, ok := cursors[cursorIndex].next()
			if !ok {
				continue
			}
			advanced = true
			remainingEvaluations--
			comparisons = append(comparisons, publicOrderComparison{first: first, second: second})
		}
		if !advanced {
			break
		}
	}
	if len(comparisons) == 0 {
		return nil, nil
	}
	strategyCapitalCache := make(map[string]*big.Int)
	strategyCapitalFor := func(token string) (*big.Int, error) {
		if cached, ok := strategyCapitalCache[token]; ok {
			return new(big.Int).Set(cached), nil
		}
		available, readErr := readBalance(token)
		if readErr != nil {
			return nil, readErr
		}
		available.Mul(available, new(big.Int).SetUint64(uint64(f.config.capitalBPS)))
		available.Quo(available, big.NewInt(10_000))
		strategyCapitalCache[token] = new(big.Int).Set(available)
		return available, nil
	}
	profitNormalizationFor := func(token string) (*big.Int, error) {
		normalization, readErr := strategyCapitalFor(token)
		if readErr != nil {
			return nil, readErr
		}
		if normalization.Sign() <= 0 {
			                                                                 
			                                                                     
			                                                                    
			return big.NewInt(1), nil
		}
		return normalization, nil
	}
	opportunities := make([]crossOpportunity, 0, len(comparisons))
	for _, comparison := range comparisons {
		opportunity, viable := buildCrossOpportunity(comparison)
		if !viable {
			continue
		}
		strategyCapital, capitalErr := strategyCapitalFor(opportunity.startToken)
		if capitalErr != nil {
			return nil, capitalErr
		}
		if strategyCapital.Sign() <= 0 {
			continue
		}
		opportunities = append(opportunities, *opportunity)
	}
	if len(opportunities) == 0 {
		return nil, nil
	}
	return f.selectDynamicCrossPortfolio(
		ctx,
		nonce,
		opportunities,
		maxGroups,
		feesPerGroup,
		readBalance,
		profitNormalizationFor,
	)
}

                                                                          
                                                                               
                                                                                
               
func (f *finder) quoteSellThenBuy(
	nonce uint64,
	opportunity *crossOpportunity,
	totalFees *big.Int,
	capitalFor func(string) (*big.Int, error),
) (*plan, error) {
	if opportunity == nil {
		return nil, nil
	}
	ask, bid := opportunity.comparison.first, opportunity.comparison.second
	if ask.leg.Side != pb.OrderSide_SELL || bid.leg.Side != pb.OrderSide_BUY || bid.price.Cmp(ask.price) <= 0 {
		return nil, nil
	}
	if ask.leg.BaseToken != bid.leg.BaseToken || ask.leg.QuoteToken != bid.leg.QuoteToken {
		return nil, nil
	}
	                                                                        
	                                                                          
	                                                                           
	                                                           
	if !opportunity.baselineRestored && ask.amount.Cmp(bid.amount) > 0 {
		return nil, nil
	}
	capital, err := capitalFor(ask.leg.QuoteToken)
	if err != nil {
		return nil, err
	}
	baseAmount := maxBaseForQuoteBudget(capital, ask.price)
	capacity := opportunity.firstCapacity
	if capacity == nil {
		capacity = ask.amount
	}
	if baseAmount.Cmp(capacity) > 0 {
		baseAmount.Set(capacity)
	}
	if baseAmount.Sign() <= 0 || !basePriceProductFits(baseAmount, ask.price) || !basePriceProductFits(baseAmount, bid.price) {
		return nil, nil
	}
	askCost := quoteFromBase(baseAmount, ask.price)
	bidOutput := quoteFromBase(baseAmount, bid.price)
	if askCost.Sign() <= 0 || askCost.Cmp(capital) > 0 || bidOutput.Cmp(askCost) <= 0 {
		return nil, nil
	}

	candidate := crossPlan(
		nonce,
		crossRouteName("sell-buy", ask.orderID, bid.orderID),
		ask.leg.QuoteToken,
		askCost,
		bidOutput,
		totalFees,
		[]plannedLeg{
			{
				leg:        Leg{BaseToken: ask.leg.BaseToken, QuoteToken: ask.leg.QuoteToken, Side: pb.OrderSide_BUY},
				baseAmount: new(big.Int).Set(baseAmount),
				price:      new(big.Int).Set(ask.price),
				inputSpent: new(big.Int).Set(askCost),
				output:     new(big.Int).Set(baseAmount),
				makerIDs:   []string{ask.orderID},
			},
			{
				leg:        Leg{BaseToken: bid.leg.BaseToken, QuoteToken: bid.leg.QuoteToken, Side: pb.OrderSide_SELL},
				baseAmount: new(big.Int).Set(baseAmount),
				price:      new(big.Int).Set(bid.price),
				inputSpent: new(big.Int).Set(baseAmount),
				output:     new(big.Int).Set(bidOutput),
				makerIDs:   []string{bid.orderID},
			},
		},
		[]int{ask.insertAfter, bid.insertAfter},
	)
	if candidate != nil {
		candidate.restoresBook = true
	}
	if !meetsMinimumProfit(candidate, f.config.MinProfitBPS) {
		return nil, nil
	}
	return candidate, nil
}

                                                                             
                                                                             
                                                                           
func (f *finder) quoteBuyThenSell(
	nonce uint64,
	opportunity *crossOpportunity,
	totalFees *big.Int,
	capitalFor func(string) (*big.Int, error),
) (*plan, error) {
	if opportunity == nil {
		return nil, nil
	}
	bid, ask := opportunity.comparison.first, opportunity.comparison.second
	if bid.leg.Side != pb.OrderSide_BUY || ask.leg.Side != pb.OrderSide_SELL || bid.price.Cmp(ask.price) <= 0 {
		return nil, nil
	}
	if bid.leg.BaseToken != ask.leg.BaseToken || bid.leg.QuoteToken != ask.leg.QuoteToken {
		return nil, nil
	}
	if !opportunity.baselineRestored && ask.amount.Cmp(bid.amount) <= 0 {
		return nil, nil
	}
	capital, err := capitalFor(bid.leg.BaseToken)
	if err != nil {
		return nil, err
	}
	capacity := opportunity.firstCapacity
	if capacity == nil {
		capacity = bid.amount
	}
	maxSell := minBig(capital, capacity)
	if safeMax := maxBaseForPrice(bid.price); maxSell.Cmp(safeMax) > 0 {
		maxSell.Set(safeMax)
	}
	if maxSell.Sign() <= 0 {
		return nil, nil
	}
	if opportunity.baselineRestored {
		return f.quoteTracedReverseSettlement(nonce, opportunity, maxSell, totalFees), nil
	}

	remainingDelta := new(big.Int).Sub(ask.amount, bid.amount)
	sizing := bestReverseSizing(maxSell, bid.price, ask.price, remainingDelta)
	if sizing == nil {
		return nil, nil
	}

	candidate := crossPlan(
		nonce,
		crossRouteName("buy-sell", bid.orderID, ask.orderID),
		bid.leg.BaseToken,
		sizing.baseSold,
		sizing.buyAmount,
		totalFees,
		[]plannedLeg{
			{
				leg:        Leg{BaseToken: bid.leg.BaseToken, QuoteToken: bid.leg.QuoteToken, Side: pb.OrderSide_SELL},
				baseAmount: new(big.Int).Set(sizing.baseSold),
				price:      new(big.Int).Set(bid.price),
				inputSpent: new(big.Int).Set(sizing.baseSold),
				output:     new(big.Int).Set(sizing.bidOutput),
				makerIDs:   []string{bid.orderID},
			},
			{
				leg:        Leg{BaseToken: ask.leg.BaseToken, QuoteToken: ask.leg.QuoteToken, Side: pb.OrderSide_BUY},
				baseAmount: new(big.Int).Set(sizing.buyAmount),
				price:      new(big.Int).Set(ask.price),
				inputSpent: new(big.Int).Set(sizing.askCost),
				output:     new(big.Int).Set(sizing.buyAmount),
				makerIDs:   []string{ask.orderID},
			},
		},
		[]int{bid.insertAfter, ask.insertAfter},
	)
	if !meetsMinimumProfit(candidate, f.config.MinProfitBPS) {
		return nil, nil
	}
	return candidate, nil
}

                                                                                
                                                                            
                                                                                
                                                                           
                                                                         
func (f *finder) quoteTracedReverseSettlement(
	nonce uint64,
	opportunity *crossOpportunity,
	baseSold, totalFees *big.Int,
) *plan {
	if opportunity == nil || baseSold == nil || baseSold.Sign() <= 0 || totalFees == nil {
		return nil
	}
	bid, ask := opportunity.comparison.first, opportunity.comparison.second
	bidOutput := quoteFromBase(baseSold, bid.price)
	restoreCost := quoteFromBase(baseSold, ask.price)
	if bidOutput.Sign() <= 0 || restoreCost.Sign() <= 0 || restoreCost.Cmp(bidOutput) >= 0 {
		return nil
	}
	restoring := crossResidualPlan(
		nonce,
		crossRouteName("buy-sell-restoring", bid.orderID, ask.orderID),
		bid.leg.BaseToken,
		bid.leg.QuoteToken,
		baseSold,
		bidOutput,
		restoreCost,
		totalFees,
		[]plannedLeg{
			{
				leg:        Leg{BaseToken: bid.leg.BaseToken, QuoteToken: bid.leg.QuoteToken, Side: pb.OrderSide_SELL},
				baseAmount: new(big.Int).Set(baseSold),
				price:      new(big.Int).Set(bid.price),
				inputSpent: new(big.Int).Set(baseSold),
				output:     new(big.Int).Set(bidOutput),
				makerIDs:   []string{bid.orderID},
			},
			{
				leg:        Leg{BaseToken: ask.leg.BaseToken, QuoteToken: ask.leg.QuoteToken, Side: pb.OrderSide_BUY},
				baseAmount: new(big.Int).Set(baseSold),
				price:      new(big.Int).Set(ask.price),
				inputSpent: new(big.Int).Set(restoreCost),
				output:     new(big.Int).Set(baseSold),
				makerIDs:   []string{ask.orderID},
			},
		},
		[]int{bid.insertAfter, ask.insertAfter},
	)
	if !meetsMinimumProfit(restoring, f.config.MinProfitBPS) {
		restoring = nil
	}

	reinvesting := f.quoteTracedReverseReinvestment(nonce, opportunity, baseSold, bidOutput, totalFees)
	if !meetsMinimumProfit(reinvesting, f.config.MinProfitBPS) {
		reinvesting = nil
	}
	if reinvesting == nil {
		return restoring
	}

	                                                                           
	                                                                          
	                                                                          
	                                                           
	grossBaseProfit := new(big.Int).Sub(reinvesting.finalOutput, reinvesting.startSpent)
	reinvestValue := quoteFromBase(grossBaseProfit, ask.price)
	quoteDust := new(big.Int).Sub(bidOutput, reinvesting.legs[1].inputSpent)
	if quoteDust.Sign() > 0 {
		reinvestValue.Add(reinvestValue, quoteDust)
	}
	if reinvestValue.Sign() <= 0 {
		return restoring
	}
	restoreValue := new(big.Int).Sub(bidOutput, restoreCost)
	if restoring == nil || reinvestValue.Cmp(restoreValue) >= 0 {
		return reinvesting
	}
	return restoring
}

func (f *finder) quoteTracedReverseReinvestment(
	nonce uint64,
	opportunity *crossOpportunity,
	baseSold, bidOutput, totalFees *big.Int,
) *plan {
	if opportunity == nil || opportunity.secondRemainder == nil || opportunity.secondRemainder.Sign() <= 0 {
		return nil
	}
	bid, ask := opportunity.comparison.first, opportunity.comparison.second
	availableAsk := new(big.Int).Add(new(big.Int).Set(baseSold), opportunity.secondRemainder)
	if availableAsk.Cmp(maxUint256) > 0 {
		availableAsk.Set(maxUint256)
	}
	buyAmount := maxBaseForQuoteBudget(bidOutput, ask.price)
	if buyAmount.Cmp(availableAsk) > 0 {
		buyAmount.Set(availableAsk)
	}
	if buyAmount.Cmp(baseSold) <= 0 || !basePriceProductFits(buyAmount, ask.price) {
		return nil
	}
	buyCost := quoteFromBase(buyAmount, ask.price)
	if buyCost.Sign() <= 0 || buyCost.Cmp(bidOutput) > 0 {
		return nil
	}
	return crossPlan(
		nonce,
		crossRouteName("buy-sell-reinvest", bid.orderID, ask.orderID),
		bid.leg.BaseToken,
		baseSold,
		buyAmount,
		totalFees,
		[]plannedLeg{
			{
				leg:        Leg{BaseToken: bid.leg.BaseToken, QuoteToken: bid.leg.QuoteToken, Side: pb.OrderSide_SELL},
				baseAmount: new(big.Int).Set(baseSold),
				price:      new(big.Int).Set(bid.price),
				inputSpent: new(big.Int).Set(baseSold),
				output:     new(big.Int).Set(bidOutput),
				makerIDs:   []string{bid.orderID},
			},
			{
				leg:        Leg{BaseToken: ask.leg.BaseToken, QuoteToken: ask.leg.QuoteToken, Side: pb.OrderSide_BUY},
				baseAmount: new(big.Int).Set(buyAmount),
				price:      new(big.Int).Set(ask.price),
				inputSpent: new(big.Int).Set(buyCost),
				output:     new(big.Int).Set(buyAmount),
				makerIDs:   []string{ask.orderID},
			},
		},
		[]int{bid.insertAfter, ask.insertAfter},
	)
}

type reverseSizing struct {
	baseSold  *big.Int
	buyAmount *big.Int
	bidOutput *big.Int
	askCost   *big.Int
}

                                                                            
                                                                               
                                                                               
                                                                              
                                                                             
                                                     
func bestReverseSizing(maxSell, bidPrice, askPrice, remainingDelta *big.Int) *reverseSizing {
	if maxSell == nil || maxSell.Sign() <= 0 {
		return nil
	}
	var best *reverseSizing
	evaluate := func(q *big.Int) {
		if q == nil || q.Sign() <= 0 || q.Cmp(maxSell) > 0 {
			return
		}
		bidOutput := quoteFromBase(q, bidPrice)
		if bidOutput.Sign() <= 0 {
			return
		}
		buyAmount := maxBaseForQuoteBudget(bidOutput, askPrice)
		askRemainder := new(big.Int).Add(remainingDelta, q)
		if buyAmount.Cmp(askRemainder) > 0 {
			buyAmount.Set(askRemainder)
		}
		if buyAmount.Cmp(q) <= 0 || !basePriceProductFits(buyAmount, askPrice) {
			return
		}
		askCost := quoteFromBase(buyAmount, askPrice)
		if askCost.Sign() <= 0 || askCost.Cmp(bidOutput) > 0 {
			return
		}
		candidate := &reverseSizing{
			baseSold:  new(big.Int).Set(q),
			buyAmount: new(big.Int).Set(buyAmount),
			bidOutput: new(big.Int).Set(bidOutput),
			askCost:   new(big.Int).Set(askCost),
		}
		if betterReverseSizing(candidate, best) {
			best = candidate
		}
	}
	one := big.NewInt(1)
	two := big.NewInt(2)
	mid := new(big.Int).Rsh(new(big.Int).Set(maxSell), 1)
	points := []*big.Int{
		one,
		new(big.Int).Sub(new(big.Int).Set(maxSell), two),
		new(big.Int).Sub(new(big.Int).Set(maxSell), one),
		new(big.Int).Set(maxSell),
		new(big.Int).Sub(new(big.Int).Set(mid), one),
		new(big.Int).Set(mid),
		new(big.Int).Add(new(big.Int).Set(mid), one),
	}
	seen := make(map[string]struct{}, len(points))
	for _, q := range points {
		key := q.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		evaluate(q)
	}
	return best
}

func betterReverseSizing(candidate, current *reverseSizing) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	candidateProfit := new(big.Int).Sub(candidate.buyAmount, candidate.baseSold)
	currentProfit := new(big.Int).Sub(current.buyAmount, current.baseSold)
	if cmp := candidateProfit.Cmp(currentProfit); cmp != 0 {
		return cmp > 0
	}
	candidateQuoteRemainder := new(big.Int).Sub(candidate.bidOutput, candidate.askCost)
	currentQuoteRemainder := new(big.Int).Sub(current.bidOutput, current.askCost)
	if cmp := candidateQuoteRemainder.Cmp(currentQuoteRemainder); cmp != 0 {
		return cmp > 0
	}
	return candidate.baseSold.Cmp(current.baseSold) > 0
}

func crossPlan(nonce uint64, name, startToken string, spent, output, totalFees *big.Int, legs []plannedLeg, insertions []int) *plan {
	if spent == nil || output == nil || spent.Sign() <= 0 || output.Cmp(spent) <= 0 {
		return nil
	}
	profit := new(big.Int).Sub(output, spent)
	denominator := new(big.Int).Set(spent)
	if startToken == feeToken {
		profit.Sub(profit, totalFees)
		denominator.Add(denominator, totalFees)
	}
	if profit.Sign() <= 0 || denominator.Sign() <= 0 {
		return nil
	}
	roiBPS := new(big.Int).Mul(new(big.Int).Set(profit), big.NewInt(10_000))
	roiBPS.Quo(roiBPS, denominator)
	return &plan{
		routeName:         name,
		startToken:        startToken,
		profitToken:       startToken,
		startSpent:        new(big.Int).Set(spent),
		finalOutput:       new(big.Int).Set(output),
		profit:            profit,
		roiBPS:            roiBPS,
		nonce:             nonce,
		legs:              legs,
		insertAfterPublic: append([]int(nil), insertions...),
	}
}

                                                                            
                                                                              
                                                                    
func crossResidualPlan(
	nonce uint64,
	name, startToken, profitToken string,
	baseCapital, sellOutput, buyCost, totalFees *big.Int,
	legs []plannedLeg,
	insertions []int,
) *plan {
	if baseCapital == nil || sellOutput == nil || buyCost == nil ||
		baseCapital.Sign() <= 0 || sellOutput.Cmp(buyCost) <= 0 {
		return nil
	}
	profit := new(big.Int).Sub(sellOutput, buyCost)
	denominator := new(big.Int).Set(buyCost)
	if profitToken == feeToken {
		profit.Sub(profit, totalFees)
		denominator.Add(denominator, totalFees)
	}
	if profit.Sign() <= 0 || denominator.Sign() <= 0 {
		return nil
	}
	roiBPS := new(big.Int).Mul(new(big.Int).Set(profit), bigTenK)
	roiBPS.Quo(roiBPS, denominator)
	return &plan{
		routeName:         name,
		startToken:        startToken,
		profitToken:       profitToken,
		startSpent:        new(big.Int).Set(baseCapital),
		finalOutput:       new(big.Int).Set(baseCapital),
		profit:            profit,
		roiBPS:            roiBPS,
		nonce:             nonce,
		legs:              legs,
		residualProfit:    true,
		restoresBook:      true,
		insertAfterPublic: append([]int(nil), insertions...),
	}
}

func meetsMinimumProfit(candidate *plan, minProfitBPS uint32) bool {
	return candidate != nil && candidate.roiBPS.Cmp(new(big.Int).SetUint64(uint64(minProfitBPS))) >= 0
}

func classifyCrossComparison(comparison publicOrderComparison) (string, bool) {
	first, second := comparison.first, comparison.second
	if first.leg.Side == second.leg.Side ||
		first.leg.BaseToken != second.leg.BaseToken ||
		first.leg.QuoteToken != second.leg.QuoteToken {
		return "", false
	}
	if first.leg.Side == pb.OrderSide_SELL {
		if second.leg.Side != pb.OrderSide_BUY || second.price.Cmp(first.price) <= 0 || first.amount.Cmp(second.amount) > 0 {
			return "", false
		}
		return first.leg.QuoteToken, true
	}
	if first.leg.Side == pb.OrderSide_BUY {
		if second.leg.Side != pb.OrderSide_SELL || first.price.Cmp(second.price) <= 0 || second.amount.Cmp(first.amount) <= 0 {
			return "", false
		}
		return first.leg.BaseToken, true
	}
	return "", false
}

func buildCrossOpportunity(comparison publicOrderComparison) (*crossOpportunity, bool) {
	first, second := comparison.first, comparison.second
	if !first.traced && !second.traced {
		startToken, ok := classifyCrossComparison(comparison)
		if !ok {
			return nil, false
		}
		return &crossOpportunity{
			comparison:    comparison,
			startToken:    startToken,
			profitToken:   startToken,
			firstCapacity: new(big.Int).Set(first.amount),
		}, true
	}
	if !first.traced || !second.traced || first.remaining == nil || second.remaining == nil ||
		first.leg.Side == second.leg.Side || first.leg.BaseToken != second.leg.BaseToken ||
		first.leg.QuoteToken != second.leg.QuoteToken || first.remaining.Sign() <= 0 {
		return nil, false
	}
	if first.leg.Side == pb.OrderSide_SELL {
		if second.leg.Side != pb.OrderSide_BUY || second.price.Cmp(first.price) <= 0 {
			return nil, false
		}
	} else if first.leg.Side == pb.OrderSide_BUY {
		if second.leg.Side != pb.OrderSide_SELL || first.price.Cmp(second.price) <= 0 {
			return nil, false
		}
	} else {
		return nil, false
	}

	firstTakerSide := oppositeSpotSide(first.leg.Side)
	firstBest, ok := first.postBest[firstTakerSide]
	if !ok || firstBest.orderID != first.orderID || firstBest.owner != first.owner || firstBest.price.Cmp(first.price) != 0 ||
		firstBest.remaining.Cmp(first.remaining) != 0 {
		return nil, false
	}

	matchedFirst := new(big.Int)
	seenFirst := false
	for _, match := range second.matches {
		if match.makerOrderID == first.orderID {
			seenFirst = true
			if match.price.Cmp(first.price) != 0 {
				return nil, false
			}
			matchedFirst.Add(matchedFirst, match.quantity)
			continue
		}
		                                                                       
		                                                                     
		                                              
		if seenFirst {
			return nil, false
		}
	}
	if !seenFirst || matchedFirst.Cmp(first.remaining) != 0 {
		return nil, false
	}

	secondTakerSide := oppositeSpotSide(second.leg.Side)
	secondBest, hasSecondBest := second.postBest[secondTakerSide]
	if second.remaining.Sign() > 0 {
		if !hasSecondBest || secondBest.orderID != second.orderID || secondBest.owner != second.owner || secondBest.price.Cmp(second.price) != 0 ||
			secondBest.remaining.Cmp(second.remaining) != 0 {
			return nil, false
		}
	} else if hasSecondBest {
		                                                                        
		                                                                         
		                                                                       
		                                                                       
		if second.leg.Side == pb.OrderSide_BUY && secondBest.price.Cmp(second.price) >= 0 {
			return nil, false
		}
		if second.leg.Side == pb.OrderSide_SELL && secondBest.price.Cmp(second.price) <= 0 {
			return nil, false
		}
	}

	opportunity := &crossOpportunity{
		comparison:       comparison,
		firstCapacity:    new(big.Int).Set(first.remaining),
		secondRemainder:  new(big.Int).Set(second.remaining),
		baselineRestored: true,
	}
	if first.leg.Side == pb.OrderSide_SELL {
		opportunity.startToken = first.leg.QuoteToken
		opportunity.profitToken = first.leg.QuoteToken
	} else {
		opportunity.startToken = first.leg.BaseToken
		opportunity.profitToken = first.leg.QuoteToken
		opportunity.residualProfit = true
	}
	return opportunity, true
}

func oppositeSpotSide(side pb.OrderSide) pb.OrderSide {
	if side == pb.OrderSide_BUY {
		return pb.OrderSide_SELL
	}
	return pb.OrderSide_BUY
}

func (f *finder) quoteCrossOpportunity(
	nonce uint64,
	opportunity *crossOpportunity,
	ordinal int,
	feesPerGroup *big.Int,
	capitalAtOrdinal func(string, int) (*big.Int, error),
	profitNormalizationFor func(string) (*big.Int, error),
) (*plan, error) {
	if opportunity == nil || ordinal <= 0 {
		return nil, nil
	}
	capital, err := capitalAtOrdinal(opportunity.startToken, ordinal)
	if err != nil || capital.Sign() <= 0 {
		return nil, err
	}
	capitalFor := func(token string) (*big.Int, error) {
		if token != opportunity.startToken {
			return nil, nil
		}
		return new(big.Int).Set(capital), nil
	}
	comparison := opportunity.comparison
	var candidate *plan
	if comparison.first.leg.Side == pb.OrderSide_SELL {
		candidate, err = f.quoteSellThenBuy(nonce, opportunity, feesPerGroup, capitalFor)
	} else {
		candidate, err = f.quoteBuyThenSell(nonce, opportunity, feesPerGroup, capitalFor)
	}
	if err != nil || candidate == nil {
		return candidate, err
	}
	profitNormalization, err := profitNormalizationFor(planProfitToken(candidate))
	if err != nil || profitNormalization.Sign() <= 0 {
		return nil, err
	}
	                                                                          
	                                                                        
	                                                                            
	candidate.utility = new(big.Rat).SetFrac(new(big.Int).Set(candidate.profit), profitNormalization)
	return candidate, nil
}

func sortCandidatePlans(plans []*plan) {
	sort.SliceStable(plans, func(i, j int) bool {
		return betterPlan(plans[i], plans[j])
	})
}

func maxPotentialCrossGroups(ctx Context, byPair map[publicSpotPair][]publicLimitOrder, pairs []publicSpotPair, fundedTokens map[string]bool, limit int) int {
	if limit <= 0 {
		return 0
	}
	potential := 0
	for _, pair := range pairs {
		sells, buys := 0, 0
		for _, order := range byPair[pair] {
			if order.leg.Side == pb.OrderSide_SELL {
				sells++
			} else {
				buys++
			}
		}
		if !fundedTokens[pair.baseToken] && !fundedTokens[pair.quoteToken] {
			continue
		}
		if sells < buys {
			potential += sells
		} else {
			potential += buys
		}
		if potential >= limit {
			potential = limit
			break
		}
	}
	if bodyBound := ctx.MaxTxs / 4; potential > bodyBound {
		potential = bodyBound
	}
	return potential
}

                                                                       
                                                                              
                                                                                
                                                                         
func (f *finder) selectDynamicCrossPortfolio(
	ctx Context,
	nonce uint64,
	opportunities []crossOpportunity,
	maxGroups int,
	feesPerGroup *big.Int,
	initialBalanceFor func(string) (*big.Int, error),
	profitNormalizationFor func(string) (*big.Int, error),
) (*plan, error) {
	return f.selectBeamCrossPortfolio(
		ctx, nonce, opportunities, maxGroups, feesPerGroup, initialBalanceFor, profitNormalizationFor,
	)
}

func portfolioUtility(selected []*plan) *big.Rat {
	score := new(big.Rat)
	for _, candidate := range selected {
		if candidate != nil && candidate.utility != nil {
			score.Add(score, candidate.utility)
		}
	}
	return score
}

type crossScoreScratch struct {
	q              big.Int
	tmp            big.Int
	bidOutput      big.Int
	buyAmount      big.Int
	askRemainder   big.Int
	askCost        big.Int
	profit         big.Int
	denominator    big.Int
	left           big.Int
	right          big.Int
	point          big.Int
	mid            big.Int
	quoteRemainder big.Int
	bestProfit     big.Int
	bestQuote      big.Int
	bestQ          big.Int
}

func newCrossScoreScratch() *crossScoreScratch { return &crossScoreScratch{} }

func maxBaseForQuoteBudgetInto(dst, budget, price, scratch *big.Int) {
	if budget == nil || price == nil || budget.Sign() <= 0 || price.Sign() <= 0 {
		dst.SetInt64(0)
		return
	}
	scratch.Add(budget, bigOne)
	scratch.Mul(scratch, priceScale)
	scratch.Sub(scratch, bigOne)
	dst.Quo(scratch, price)
}

func quoteFromBaseInto(dst, base, price *big.Int) {
	dst.Mul(base, price)
	dst.Quo(dst, priceScale)
}

                                                                             
                                                                              
                                                             
func (f *finder) scoreCrossOpportunity(
	opportunity *crossOpportunity,
	capital, profitNormalization, feesPerGroup *big.Int,
	score *big.Int,
	s *crossScoreScratch,
) bool {
	if opportunity == nil || capital == nil || profitNormalization == nil || feesPerGroup == nil ||
		capital.Sign() <= 0 || profitNormalization.Sign() <= 0 || s == nil {
		return false
	}
	first, second := opportunity.comparison.first, opportunity.comparison.second
	if first.leg.Side == pb.OrderSide_SELL {
		maxBaseForQuoteBudgetInto(&s.q, capital, first.price, &s.tmp)
		capacity := opportunity.firstCapacity
		if capacity == nil {
			capacity = first.amount
		}
		if s.q.Cmp(capacity) > 0 {
			s.q.Set(capacity)
		}
		if s.q.Sign() <= 0 {
			return false
		}
		quoteFromBaseInto(&s.askCost, &s.q, first.price)
		quoteFromBaseInto(&s.bidOutput, &s.q, second.price)
		if s.askCost.Sign() <= 0 || s.askCost.Cmp(capital) > 0 || s.bidOutput.Cmp(&s.askCost) <= 0 {
			return false
		}
		s.profit.Sub(&s.bidOutput, &s.askCost)
		s.denominator.Set(&s.askCost)
	} else if opportunity.residualProfit {
		capacity := opportunity.firstCapacity
		if capacity == nil {
			capacity = first.amount
		}
		s.q.Set(capital)
		if s.q.Cmp(capacity) > 0 {
			s.q.Set(capacity)
		}
		s.tmp.Quo(maxUint256, first.price)
		if s.q.Cmp(&s.tmp) > 0 {
			s.q.Set(&s.tmp)
		}
		if s.q.Sign() <= 0 {
			return false
		}
		quoteFromBaseInto(&s.bidOutput, &s.q, first.price)
		quoteFromBaseInto(&s.askCost, &s.q, second.price)
		if s.askCost.Sign() <= 0 || s.bidOutput.Cmp(&s.askCost) <= 0 {
			return false
		}
		s.profit.Sub(&s.bidOutput, &s.askCost)
		s.denominator.Set(&s.askCost)
	} else {
		s.q.Set(capital)
		if s.q.Cmp(first.amount) > 0 {
			s.q.Set(first.amount)
		}
		if s.q.Sign() <= 0 {
			return false
		}
		remainingDelta := s.tmp.Sub(second.amount, first.amount)
		best := false
		evaluate := func(q *big.Int) {
			if q.Sign() <= 0 || q.Cmp(&s.q) > 0 {
				return
			}
			quoteFromBaseInto(&s.bidOutput, q, first.price)
			if s.bidOutput.Sign() <= 0 {
				return
			}
			maxBaseForQuoteBudgetInto(&s.buyAmount, &s.bidOutput, second.price, &s.left)
			s.askRemainder.Add(remainingDelta, q)
			if s.buyAmount.Cmp(&s.askRemainder) > 0 {
				s.buyAmount.Set(&s.askRemainder)
			}
			if s.buyAmount.Cmp(q) <= 0 {
				return
			}
			quoteFromBaseInto(&s.askCost, &s.buyAmount, second.price)
			if s.askCost.Sign() <= 0 || s.askCost.Cmp(&s.bidOutput) > 0 {
				return
			}
			s.profit.Sub(&s.buyAmount, q)
			s.quoteRemainder.Sub(&s.bidOutput, &s.askCost)
			if !best || s.profit.Cmp(&s.bestProfit) > 0 ||
				(s.profit.Cmp(&s.bestProfit) == 0 && (s.quoteRemainder.Cmp(&s.bestQuote) > 0 ||
					(s.quoteRemainder.Cmp(&s.bestQuote) == 0 && q.Cmp(&s.bestQ) > 0))) {
				best = true
				s.bestProfit.Set(&s.profit)
				s.bestQuote.Set(&s.quoteRemainder)
				s.bestQ.Set(q)
			}
		}
		s.mid.Rsh(&s.q, 1)
		s.point.Set(bigOne)
		evaluate(&s.point)
		s.point.Sub(&s.q, bigTwo)
		evaluate(&s.point)
		s.point.Sub(&s.q, bigOne)
		evaluate(&s.point)
		s.point.Set(&s.q)
		evaluate(&s.point)
		s.point.Sub(&s.mid, bigOne)
		evaluate(&s.point)
		s.point.Set(&s.mid)
		evaluate(&s.point)
		s.point.Add(&s.mid, bigOne)
		evaluate(&s.point)
		if !best {
			return false
		}
		s.profit.Set(&s.bestProfit)
		s.denominator.Set(&s.bestQ)
	}
	if crossOpportunityProfitToken(opportunity) == feeToken {
		s.profit.Sub(&s.profit, feesPerGroup)
		s.denominator.Add(&s.denominator, feesPerGroup)
	}
	if s.profit.Sign() <= 0 || s.denominator.Sign() <= 0 {
		return false
	}
	s.left.Mul(&s.profit, bigTenK)
	s.right.SetUint64(uint64(f.config.MinProfitBPS))
	s.right.Mul(&s.denominator, &s.right)
	if s.left.Cmp(&s.right) < 0 {
		return false
	}
	score.Lsh(&s.profit, portfolioUtilityPrecision)
	score.Quo(score, profitNormalization)
	return score.Sign() > 0
}

func crossOpportunityProfitToken(opportunity *crossOpportunity) string {
	if opportunity == nil {
		return ""
	}
	if opportunity.profitToken != "" {
		return opportunity.profitToken
	}
	return opportunity.startToken
}

func buildCrossPortfolio(nonce uint64, selected []*plan, score *big.Rat) *plan {
	if len(selected) == 0 || score == nil || score.Sign() <= 0 {
		return nil
	}
	firstMakerID := func(candidate *plan) string {
		if candidate == nil || len(candidate.legs) == 0 || len(candidate.legs[0].makerIDs) == 0 {
			return ""
		}
		return candidate.legs[0].makerIDs[0]
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].insertAfterPublic[0] != selected[j].insertAfterPublic[0] {
			return selected[i].insertAfterPublic[0] < selected[j].insertAfterPublic[0]
		}
		if selected[i].insertAfterPublic[1] != selected[j].insertAfterPublic[1] {
			return selected[i].insertAfterPublic[1] < selected[j].insertAfterPublic[1]
		}
		if selected[i].legs[0].leg.BaseToken != selected[j].legs[0].leg.BaseToken {
			return selected[i].legs[0].leg.BaseToken < selected[j].legs[0].leg.BaseToken
		}
		if selected[i].legs[0].leg.QuoteToken != selected[j].legs[0].leg.QuoteToken {
			return selected[i].legs[0].leg.QuoteToken < selected[j].legs[0].leg.QuoteToken
		}
		leftFirst, rightFirst := firstMakerID(selected[i]), firstMakerID(selected[j])
		if leftFirst != rightFirst {
			return leftFirst < rightFirst
		}
		return selected[i].routeName < selected[j].routeName
	})
	portfolio := &plan{
		nonce:               nonce,
		legs:                make([]plannedLeg, 0, 2*len(selected)),
		groups:              make([]plannedGroup, len(selected)),
		insertAfterPublic:   make([]int, 0, 2*len(selected)),
		utility:             new(big.Rat).Set(score),
		futureStartBalances: make(map[string]*big.Int),
	}
	type portfolioLegEvent struct {
		groupIndex int
		legIndex   int
		insertion  int
	}
	candidates := make([]*plan, len(selected))
	events := make([]portfolioLegEvent, 0, 2*len(selected))
	h := sha256.New()
	var commonToken string
	allSameToken := true
	var commonProfitToken string
	allSameProfitToken := true
	for groupIndex, source := range selected {
		candidate := clonePlan(source)
		if candidate == nil || len(candidate.legs) != 2 || len(candidate.insertAfterPublic) != 2 {
			return nil
		}
		candidates[groupIndex] = candidate
		if groupIndex == 0 {
			commonToken = candidate.startToken
			commonProfitToken = planProfitToken(candidate)
		} else if candidate.startToken != commonToken {
			allSameToken = false
		}
		if planProfitToken(candidate) != commonProfitToken {
			allSameProfitToken = false
		}
		_, _ = h.Write([]byte(candidate.routeName))
		_, _ = h.Write([]byte{0})
		portfolio.groups[groupIndex] = plannedGroup{
			routeName:      candidate.routeName,
			startToken:     candidate.startToken,
			profitToken:    candidate.profitToken,
			startSpent:     new(big.Int).Set(candidate.startSpent),
			finalOutput:    new(big.Int).Set(candidate.finalOutput),
			profit:         new(big.Int).Set(candidate.profit),
			roiBPS:         new(big.Int).Set(candidate.roiBPS),
			residualProfit: candidate.residualProfit,
			market:         candidateMarket(candidate),
			legIndices:     []int{-1, -1},
		}
		for token, balance := range candidate.futureStartBalances {
			if balance == nil {
				continue
			}
			if existing := portfolio.futureStartBalances[token]; existing == nil || balance.Cmp(existing) < 0 {
				portfolio.futureStartBalances[token] = new(big.Int).Set(balance)
			}
		}
		for legIndex, insertion := range candidate.insertAfterPublic {
			events = append(events, portfolioLegEvent{groupIndex: groupIndex, legIndex: legIndex, insertion: insertion})
		}
	}
	                                                                             
	                                                                               
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].insertion != events[j].insertion {
			return events[i].insertion < events[j].insertion
		}
		leftClosing, rightClosing := events[i].legIndex > 0, events[j].legIndex > 0
		if leftClosing != rightClosing {
			return leftClosing
		}
		if events[i].groupIndex != events[j].groupIndex {
			return events[i].groupIndex < events[j].groupIndex
		}
		return events[i].legIndex < events[j].legIndex
	})
	for _, event := range events {
		candidate := candidates[event.groupIndex]
		flatIndex := len(portfolio.legs)
		portfolio.legs = append(portfolio.legs, candidate.legs[event.legIndex])
		portfolio.insertAfterPublic = append(portfolio.insertAfterPublic, event.insertion)
		portfolio.groups[event.groupIndex].legIndices[event.legIndex] = flatIndex
	}
	for _, group := range portfolio.groups {
		for _, legIndex := range group.legIndices {
			if legIndex < 0 {
				return nil
			}
		}
	}
	portfolio.routeName = "cross-portfolio-" + hex.EncodeToString(h.Sum(nil)[:12])
	portfolio.roiBPS = ratFloor(new(big.Rat).Mul(new(big.Rat).Set(score), new(big.Rat).SetInt64(10_000)))
	if allSameToken && !portfolioHasResidualProfit(portfolio.groups) {
		portfolio.startToken = commonToken
		portfolio.profitToken = commonProfitToken
		portfolio.startSpent = new(big.Int)
		portfolio.finalOutput = new(big.Int)
		portfolio.profit = new(big.Int)
		for _, group := range portfolio.groups {
			portfolio.startSpent.Add(portfolio.startSpent, group.startSpent)
			portfolio.finalOutput.Add(portfolio.finalOutput, group.finalOutput)
			portfolio.profit.Add(portfolio.profit, group.profit)
		}
	} else {
		                                                                  
		                                                                         
		portfolio.startToken = multiTokenPortfolio
		portfolio.startSpent = big.NewInt(1_000_000_000_000)
		if allSameProfitToken {
			portfolio.profitToken = commonProfitToken
			portfolio.profit = new(big.Int)
			for _, group := range portfolio.groups {
				portfolio.profit.Add(portfolio.profit, group.profit)
			}
		} else {
			portfolio.profitToken = multiTokenPortfolio
			portfolio.profit = ratFloor(new(big.Rat).Mul(new(big.Rat).Set(score), new(big.Rat).SetInt(portfolio.startSpent)))
		}
		portfolio.finalOutput = new(big.Int).Add(new(big.Int).Set(portfolio.startSpent), portfolio.profit)
	}
	return portfolio
}

func candidateMarket(candidate *plan) plannedMarket {
	if candidate == nil || len(candidate.legs) == 0 {
		return plannedMarketSpot
	}
	market := candidate.legs[0].market
	for _, leg := range candidate.legs[1:] {
		if leg.market != market {
			return plannedMarketSpot
		}
	}
	return market
}

func portfolioHasResidualProfit(groups []plannedGroup) bool {
	for _, group := range groups {
		if group.residualProfit {
			return true
		}
	}
	return false
}

func ratFloor(value *big.Rat) *big.Int {
	if value == nil {
		return new(big.Int)
	}
	return new(big.Int).Quo(new(big.Int).Set(value.Num()), value.Denom())
}

func maxBaseForQuoteBudget(quoteBudget, price *big.Int) *big.Int {
	if quoteBudget == nil || price == nil || quoteBudget.Sign() <= 0 || price.Sign() <= 0 {
		return new(big.Int)
	}
	                                        
	                                        
	numerator := new(big.Int).Add(quoteBudget, big.NewInt(1))
	numerator.Mul(numerator, priceScale)
	numerator.Sub(numerator, big.NewInt(1))
	base := numerator.Quo(numerator, price)
	if safeMax := maxBaseForPrice(price); base.Cmp(safeMax) > 0 {
		base.Set(safeMax)
	}
	return base
}

func crossRouteName(direction, firstOrderID, secondOrderID string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(direction))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(firstOrderID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(secondOrderID))
	return "cross-" + hex.EncodeToString(h.Sum(nil)[:12])
}

func (f *finder) collectReadyLimitOrders(ctx Context, orderedPublic []*pb.AnyTx, market *MarketSnapshot) ([]publicLimitOrder, error) {
	if market != nil {
		return f.collectTracedLimitOrders(ctx, orderedPublic, market)
	}
	if !f.allowUntracedCross {
		return nil, nil
	}
	return f.collectLegacyLimitOrders(ctx, orderedPublic), nil
}

func (f *finder) collectLegacyLimitOrders(ctx Context, orderedPublic []*pb.AnyTx) []publicLimitOrder {
	orders := make([]publicLimitOrder, 0)
	for index, tx := range orderedPublic {
		if tx == nil || !pb.TxRequiresAccountNonce(tx) {
			continue
		}
		base := tx.GetBase()
		if base == nil || strings.TrimSpace(base.FromAddress) == "" {
			continue
		}
		if err := pb.ValidateTxExecutionGuard(tx, ctx.Height, ctx.Proposer); err != nil {
			continue
		}
		owner := base.FromAddress
		order := tx.GetOrderTx()
		if order == nil || order.Op != pb.OrderOp_ADD || order.IsMarket || owner == f.account || strings.TrimSpace(base.TxId) == "" {
			continue
		}
		if strings.TrimSpace(order.BaseToken) == "" || strings.TrimSpace(order.QuoteToken) == "" || order.BaseToken == order.QuoteToken {
			continue
		}
		if order.Side != pb.OrderSide_BUY && order.Side != pb.OrderSide_SELL {
			continue
		}
		amount, amountErr := parseUint256("public order amount", order.Amount)
		price, priceErr := parseUint256("public order price", order.Price)
		if amountErr != nil || priceErr != nil || amount.Sign() <= 0 || price.Sign() <= 0 || !basePriceProductFits(amount, price) {
			continue
		}
		orders = append(orders, publicLimitOrder{
			insertAfter: index + 1,
			orderID:     base.TxId,
			owner:       owner,
			leg:         Leg{BaseToken: order.BaseToken, QuoteToken: order.QuoteToken, Side: order.Side},
			amount:      amount,
			price:       price,
		})
	}
	return orders
}

func (f *finder) collectTracedLimitOrders(ctx Context, orderedPublic []*pb.AnyTx, market *MarketSnapshot) ([]publicLimitOrder, error) {
	if market == nil {
		return nil, nil
	}

	orders := make([]publicLimitOrder, 0, len(market.Executions))
	lastPosition := 0
	for _, execution := range market.Executions {
		position := execution.PublicPosition
		if position <= lastPosition || position < 1 || position > len(orderedPublic) {
			return nil, fmt.Errorf("invalid spot execution trace position %d after %d", position, lastPosition)
		}
		lastPosition = position
		tx := orderedPublic[position-1]
		if tx == nil || !pb.TxRequiresAccountNonce(tx) || tx.GetBase() == nil || tx.GetOrderTx() == nil {
			return nil, fmt.Errorf("spot execution trace position %d does not bind a nonce spot order", position)
		}
		if err := pb.ValidateTxExecutionGuard(tx, ctx.Height, ctx.Proposer); err != nil {
			return nil, fmt.Errorf("spot execution trace position %d guard mismatch: %w", position, err)
		}
		base, order := tx.GetBase(), tx.GetOrderTx()
		if order.Op != pb.OrderOp_ADD || order.IsMarket ||
			(order.Side != pb.OrderSide_BUY && order.Side != pb.OrderSide_SELL) {
			return nil, fmt.Errorf("spot execution trace position %d does not bind a limit ADD", position)
		}
		owner := strings.TrimSpace(base.FromAddress)
		if owner == "" || strings.TrimSpace(base.TxId) == "" || order.BaseToken == "" ||
			order.QuoteToken == "" || order.BaseToken == order.QuoteToken {
			return nil, fmt.Errorf("spot execution trace position %d binds malformed order identity", position)
		}
		if execution.OrderID != base.TxId || execution.Owner != owner ||
			execution.Leg.BaseToken != order.BaseToken || execution.Leg.QuoteToken != order.QuoteToken ||
			execution.Leg.Side != order.Side {
			return nil, fmt.Errorf("spot execution trace position %d does not match public order %s", position, base.TxId)
		}
		amount, amountErr := parseUint256("traced public order amount", execution.Amount)
		declaredAmount, declaredAmountErr := parseUint256("public order amount", order.Amount)
		price, priceErr := parseUint256("traced public order price", execution.Price)
		declaredPrice, declaredPriceErr := parseUint256("public order price", order.Price)
		remaining, remainingErr := parseUint256("traced public order remaining", execution.Remaining)
		if amountErr != nil || declaredAmountErr != nil || priceErr != nil || declaredPriceErr != nil || remainingErr != nil ||
			amount.Sign() <= 0 || price.Sign() <= 0 || amount.Cmp(declaredAmount) != 0 || price.Cmp(declaredPrice) != 0 ||
			remaining.Cmp(amount) > 0 || !basePriceProductFits(amount, price) {
			return nil, fmt.Errorf("spot execution trace %s has inconsistent amount/price/remaining", base.TxId)
		}

		matches := make([]publicOrderMatch, 0, len(execution.Matches))
		totalMatched := new(big.Int)
		for _, match := range execution.Matches {
			quantity, quantityErr := parseUint256("traced public match quantity", match.Quantity)
			matchPrice, matchPriceErr := parseUint256("traced public match price", match.Price)
			if strings.TrimSpace(match.MakerOrderID) == "" || quantityErr != nil || matchPriceErr != nil ||
				quantity.Sign() <= 0 || matchPrice.Sign() <= 0 || !basePriceProductFits(quantity, matchPrice) {
				return nil, fmt.Errorf("spot execution trace %s has malformed match", base.TxId)
			}
			totalMatched.Add(totalMatched, quantity)
			if totalMatched.Cmp(maxUint256) > 0 {
				return nil, fmt.Errorf("spot execution trace %s match total overflows", base.TxId)
			}
			matches = append(matches, publicOrderMatch{
				makerOrderID: match.MakerOrderID,
				quantity:     quantity,
				price:        matchPrice,
			})
		}
		if new(big.Int).Add(totalMatched, remaining).Cmp(amount) != 0 {
			return nil, fmt.Errorf("spot execution trace %s matched=%s remaining=%s amount=%s", base.TxId, totalMatched, remaining, amount)
		}

		postBest := make(map[pb.OrderSide]publicOrderBest, len(execution.Levels))
		for _, level := range execution.Levels {
			if level.Leg.BaseToken != order.BaseToken || level.Leg.QuoteToken != order.QuoteToken ||
				(level.Leg.Side != pb.OrderSide_BUY && level.Leg.Side != pb.OrderSide_SELL) || len(level.Makers) != 1 {
				return nil, fmt.Errorf("spot execution trace %s has malformed post level", base.TxId)
			}
			if _, duplicate := postBest[level.Leg.Side]; duplicate {
				return nil, fmt.Errorf("spot execution trace %s repeats post level side %s", base.TxId, level.Leg.Side.String())
			}
			levelPrice, levelPriceErr := parseUint256("traced post level price", level.Price)
			makerRemaining, makerRemainingErr := parseUint256("traced post maker remaining", level.Makers[0].Remaining)
			maker := level.Makers[0]
			if levelPriceErr != nil || makerRemainingErr != nil || levelPrice.Sign() <= 0 || makerRemaining.Sign() <= 0 ||
				strings.TrimSpace(maker.OrderID) == "" || strings.TrimSpace(maker.Owner) == "" ||
				!basePriceProductFits(makerRemaining, levelPrice) {
				return nil, fmt.Errorf("spot execution trace %s has invalid post maker", base.TxId)
			}
			postBest[level.Leg.Side] = publicOrderBest{
				price: levelPrice, orderID: maker.OrderID, owner: maker.Owner, remaining: makerRemaining,
			}
		}

		                                                                        
		                                                                      
		                                                                   
		                                                              
		if owner == f.account {
			continue
		}
		orders = append(orders, publicLimitOrder{
			insertAfter: position,
			orderID:     base.TxId,
			owner:       owner,
			leg:         Leg{BaseToken: order.BaseToken, QuoteToken: order.QuoteToken, Side: order.Side},
			amount:      amount,
			price:       price,
			remaining:   remaining,
			matches:     matches,
			postBest:    postBest,
			traced:      true,
		})
	}
	return orders, nil
}
