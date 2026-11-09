package arbitrage

import (
	"dex/keys"
	"dex/pb"
	statedb "dex/stateDB"
	"errors"
	"fmt"
	"math/big"
)

const feeToken = "FB"

type finder struct {
	config             normalizedConfig
	state              StateReader
	account            string
	allowUntracedCross bool
}

func (f *finder) findBest(ctx Context, nonce uint64, market *MarketSnapshot) (*plan, error) {
	if !f.config.Enabled || market == nil || ctx.PrivateTxLimit < 2 {
		return nil, nil
	}
	routes, err := discoverClosedRoutes(
		market,
		ctx.PrivateTxLimit,
		f.config.MaxCycleEvaluations,
		f.account,
	)
	if err != nil || len(routes) == 0 {
		return nil, err
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

	feeBalance, err := readBalance(feeToken)
	if err != nil {
		return nil, err
	}
	var best *plan
	for _, route := range routes {
		if len(route.legs) > ctx.PrivateTxLimit {
			continue
		}
		totalFees := new(big.Int).Mul(f.config.fee, big.NewInt(int64(len(route.legs))))
		if feeBalance.Cmp(totalFees) < 0 {
			continue
		}
		available, readErr := readBalance(route.startToken)
		if readErr != nil {
			return nil, readErr
		}
		                                                                       
		                                                                         
		                                                                        
		                                                     
		if route.startToken == feeToken {
			available.Sub(available, totalFees)
		}
		if available.Sign() <= 0 {
			continue
		}
		capital := new(big.Int).Mul(available, new(big.Int).SetUint64(uint64(f.config.capitalBPS)))
		capital.Quo(capital, big.NewInt(10_000))
		if capital.Sign() <= 0 {
			continue
		}

		candidate := quoteClosedRoute(route, capital, totalFees, nonce)
		if candidate == nil || candidate.roiBPS.Cmp(new(big.Int).SetUint64(uint64(f.config.MinProfitBPS))) < 0 {
			continue
		}
		candidate.utility = new(big.Rat).SetFrac(new(big.Int).Set(candidate.profit), new(big.Int).Set(capital))
		if betterPlan(candidate, best) {
			best = candidate
		}
	}
	return clonePlan(best), nil
}

func quoteClosedRoute(route discoveredRoute, capital, totalFees *big.Int, nonce uint64) *plan {
	if len(route.levels) != len(route.legs) || capital == nil || capital.Sign() <= 0 {
		return nil
	}
	                                                                           
	                                                   
	low := new(big.Int)
	high := new(big.Int).Set(capital)
	one := big.NewInt(1)
	for low.Cmp(high) < 0 {
		mid := new(big.Int).Add(low, high)
		mid.Add(mid, one)
		mid.Rsh(mid, 1)
		if routeOverflows(route.legs, route.levels, mid) {
			high.Sub(mid, one)
		} else {
			low.Set(mid)
		}
	}
	if low.Sign() <= 0 {
		return nil
	}

	input := new(big.Int).Set(low)
	planned := make([]plannedLeg, 0, len(route.legs))
	var firstSpent *big.Int
	for i, leg := range route.legs {
		quoted := quoteLeg(leg, route.levels[i], input)
		if quoted.overflow || quoted.baseAmount.Sign() <= 0 || quoted.inputSpent.Sign() <= 0 || quoted.output.Sign() <= 0 {
			return nil
		}
		if i == 0 {
			firstSpent = new(big.Int).Set(quoted.inputSpent)
		}
		makerIDs := make([]string, 0, len(route.levels[i].orders))
		for _, maker := range route.levels[i].orders {
			makerIDs = append(makerIDs, maker.orderID)
		}
		planned = append(planned, plannedLeg{
			leg:        leg,
			baseAmount: new(big.Int).Set(quoted.baseAmount),
			price:      new(big.Int).Set(route.levels[i].price),
			inputSpent: new(big.Int).Set(quoted.inputSpent),
			output:     new(big.Int).Set(quoted.output),
			makerIDs:   makerIDs,
		})
		input.Set(quoted.output)
	}
	if firstSpent == nil || firstSpent.Sign() <= 0 {
		return nil
	}
	profit := new(big.Int).Sub(input, firstSpent)
	denominator := new(big.Int).Set(firstSpent)
	if route.startToken == feeToken {
		profit.Sub(profit, totalFees)
		denominator.Add(denominator, totalFees)
	}
	if profit.Sign() <= 0 || denominator.Sign() <= 0 {
		return nil
	}
	roiBPS := new(big.Int).Mul(profit, big.NewInt(10_000))
	roiBPS.Quo(roiBPS, denominator)
	return &plan{
		routeName:   route.name,
		startToken:  route.startToken,
		startSpent:  firstSpent,
		finalOutput: new(big.Int).Set(input),
		profit:      profit,
		roiBPS:      roiBPS,
		nonce:       nonce,
		legs:        planned,
	}
}

func routeOverflows(legs []Leg, levels []*topLevel, start *big.Int) bool {
	input := new(big.Int).Set(start)
	for i, leg := range legs {
		if leg.Side == pb.OrderSide_SELL && input.Cmp(levelBaseDepth(levels[i])) > 0 {
			return true
		}
		quoted := quoteLeg(leg, levels[i], input)
		input.Set(quoted.output)
	}
	return false
}

func levelBaseDepth(level *topLevel) *big.Int {
	depth := new(big.Int)
	if level == nil {
		return depth
	}
	for _, order := range level.orders {
		depth.Add(depth, order.remain)
	}
	if level.price != nil {
		if safeMax := maxBaseForPrice(level.price); depth.Cmp(safeMax) > 0 {
			depth.Set(safeMax)
		}
	}
	return depth
}

func betterPlan(candidate, current *plan) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	                                                                            
	                                                                          
	                                                                            
	                   
	if planProfitToken(candidate) == planProfitToken(current) && planProfitToken(candidate) != multiTokenPortfolio {
		if cmp := candidate.profit.Cmp(current.profit); cmp != 0 {
			return cmp > 0
		}
	}
	if candidate.utility != nil && current.utility != nil {
		if cmp := candidate.utility.Cmp(current.utility); cmp != 0 {
			return cmp > 0
		}
	}
	if cmp := candidate.roiBPS.Cmp(current.roiBPS); cmp != 0 {
		return cmp > 0
	}
	return candidate.routeName < current.routeName
}

func readAvailableBalance(state StateReader, address, token string) (*big.Int, error) {
	if state == nil {
		return nil, fmt.Errorf("nil arbitrage state reader")
	}
	data, err := state.Get(keys.KeyBalance(address, token))
	if err != nil {
		if isMissingStateKey(err) {
			return new(big.Int), nil
		}
		return nil, fmt.Errorf("read arbitrage balance %s/%s: %w", address, token, err)
	}
	if len(data) == 0 {
		return new(big.Int), nil
	}
	var record pb.TokenBalanceRecord
	if err := unmarshalProtoCompat(data, &record); err != nil {
		return nil, fmt.Errorf("decode arbitrage balance %s/%s: %w", address, token, err)
	}
	if record.Balance == nil {
		return nil, fmt.Errorf("decode arbitrage balance %s/%s: missing balance payload", address, token)
	}
	available, err := parseUint256("available balance", record.Balance.Balance)
	if err != nil {
		return nil, fmt.Errorf("decode arbitrage balance %s/%s: %w", address, token, err)
	}
	return available, nil
}

func isMissingStateKey(err error) bool {
	return errors.Is(err, statedb.ErrNotFound) || errors.Is(err, statedb.ErrKVNotFound)
}

func clonePlan(source *plan) *plan {
	if source == nil {
		return nil
	}
	cloned := &plan{
		routeName:           source.routeName,
		startToken:          source.startToken,
		profitToken:         source.profitToken,
		nonce:               source.nonce,
		legs:                make([]plannedLeg, len(source.legs)),
		groups:              make([]plannedGroup, len(source.groups)),
		insertAfterPublic:   append([]int(nil), source.insertAfterPublic...),
		residualProfit:      source.residualProfit,
		restoresBook:        source.restoresBook,
		futureStartBalances: make(map[string]*big.Int, len(source.futureStartBalances)),
	}
	cloned.startSpent = new(big.Int).Set(source.startSpent)
	cloned.finalOutput = new(big.Int).Set(source.finalOutput)
	cloned.profit = new(big.Int).Set(source.profit)
	cloned.roiBPS = new(big.Int).Set(source.roiBPS)
	if source.utility != nil {
		cloned.utility = new(big.Rat).Set(source.utility)
	}
	for i, leg := range source.legs {
		cloned.legs[i] = leg
		cloned.legs[i].baseAmount = new(big.Int).Set(leg.baseAmount)
		cloned.legs[i].price = new(big.Int).Set(leg.price)
		cloned.legs[i].inputSpent = new(big.Int).Set(leg.inputSpent)
		cloned.legs[i].output = new(big.Int).Set(leg.output)
		cloned.legs[i].makerIDs = append([]string(nil), leg.makerIDs...)
	}
	for i, group := range source.groups {
		cloned.groups[i] = group
		cloned.groups[i].startSpent = new(big.Int).Set(group.startSpent)
		cloned.groups[i].finalOutput = new(big.Int).Set(group.finalOutput)
		cloned.groups[i].profit = new(big.Int).Set(group.profit)
		cloned.groups[i].roiBPS = new(big.Int).Set(group.roiBPS)
		cloned.groups[i].legIndices = append([]int(nil), group.legIndices...)
	}
	for token, balance := range source.futureStartBalances {
		if balance != nil {
			cloned.futureStartBalances[token] = new(big.Int).Set(balance)
		}
	}
	return cloned
}

func planProfitToken(candidate *plan) string {
	if candidate == nil {
		return ""
	}
	if candidate.profitToken != "" {
		return candidate.profitToken
	}
	return candidate.startToken
}
