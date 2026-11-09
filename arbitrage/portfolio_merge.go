package arbitrage

import (
	"math/big"
)

                                                                             
                                                                                
                                                                                
                                                                         
                                                                          
                         
func (m *Machine) combineCrossPlans(ctx Context, nonce uint64, plans ...*plan) (*plan, error) {
	components := make([]*plan, 0)
	score := new(big.Rat)
	for _, source := range plans {
		if source == nil {
			continue
		}
		exploded := explodeCrossPlan(source)
		if len(exploded) == 0 {
			return nil, nil
		}
		components = append(components, exploded...)
		if source.utility != nil {
			score.Add(score, source.utility)
		}
	}
	if len(components) < 2 || score.Sign() <= 0 {
		return nil, nil
	}
	privateCount := 0
	maxInsertion := 0
	for _, component := range components {
		privateCount += len(component.legs)
		for _, insertion := range component.insertAfterPublic {
			if insertion > maxInsertion {
				maxInsertion = insertion
			}
		}
	}
	if privateCount > ctx.PrivateTxLimit || privateCount > ctx.MaxTxs || maxInsertion > ctx.MaxTxs-privateCount {
		return nil, nil
	}

	required := make(map[string]*big.Int)
	addRequired := func(token string, amount *big.Int) {
		if token == "" || amount == nil || amount.Sign() <= 0 {
			return
		}
		if required[token] == nil {
			required[token] = new(big.Int)
		}
		required[token].Add(required[token], amount)
	}
	for _, component := range components {
		addRequired(component.startToken, component.startSpent)
		market := candidateMarket(component)
		                                                                         
		                                                                      
		if market != plannedMarketFuture || component.startToken != feeToken {
			addRequired(feeToken, new(big.Int).Mul(m.config.fee, big.NewInt(int64(len(component.legs)))))
		}
	}
	for token, needed := range required {
		available, err := readAvailableBalance(m.state, m.account, token)
		if err != nil {
			return nil, err
		}
		for _, source := range plans {
			if source == nil || source.futureStartBalances[token] == nil {
				continue
			}
			projected := source.futureStartBalances[token]
			if projected.Cmp(available) < 0 {
				available.Set(projected)
			}
		}
		if needed.Cmp(available) > 0 {
			return nil, nil
		}
	}
	combined := buildCrossPortfolio(nonce, components, score)
	if combined == nil {
		return nil, nil
	}
	if candidateMarket(combined) == plannedMarketSpot {
		                                                                          
		                                                                 
		for _, group := range combined.groups {
			if group.market == plannedMarketFuture {
				return combined, nil
			}
		}
	}
	return combined, nil
}

func explodeCrossPlan(source *plan) []*plan {
	if source == nil {
		return nil
	}
	if len(source.groups) == 0 {
		if len(source.legs) != 2 || len(source.insertAfterPublic) != 2 {
			return nil
		}
		return []*plan{clonePlan(source)}
	}
	components := make([]*plan, 0, len(source.groups))
	for _, group := range source.groups {
		if len(group.legIndices) != 2 {
			return nil
		}
		component := &plan{
			routeName: group.routeName, startToken: group.startToken, profitToken: group.profitToken,
			startSpent: cloneBig(group.startSpent), finalOutput: cloneBig(group.finalOutput), profit: cloneBig(group.profit),
			roiBPS: cloneBig(group.roiBPS), nonce: source.nonce, residualProfit: group.residualProfit,
			restoresBook: true, legs: make([]plannedLeg, 2), insertAfterPublic: make([]int, 2),
			futureStartBalances: make(map[string]*big.Int, len(source.futureStartBalances)),
		}
		for routeIndex, flatIndex := range group.legIndices {
			if flatIndex < 0 || flatIndex >= len(source.legs) || flatIndex >= len(source.insertAfterPublic) {
				return nil
			}
			component.legs[routeIndex] = clonePlannedLeg(source.legs[flatIndex])
			component.insertAfterPublic[routeIndex] = source.insertAfterPublic[flatIndex]
		}
		for token, balance := range source.futureStartBalances {
			component.futureStartBalances[token] = cloneBig(balance)
		}
		if component.startSpent == nil || component.finalOutput == nil || component.profit == nil || component.roiBPS == nil {
			return nil
		}
		component.utility = new(big.Rat).SetFrac(new(big.Int).Set(component.profit), maxBigOne(component.startSpent))
		components = append(components, component)
	}
	return components
}

func clonePlannedLeg(source plannedLeg) plannedLeg {
	cloned := source
	cloned.baseAmount = cloneBig(source.baseAmount)
	cloned.price = cloneBig(source.price)
	cloned.inputSpent = cloneBig(source.inputSpent)
	cloned.output = cloneBig(source.output)
	cloned.makerIDs = append([]string(nil), source.makerIDs...)
	return cloned
}

func cloneBig(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}
	return new(big.Int).Set(value)
}

func maxBigOne(value *big.Int) *big.Int {
	if value == nil || value.Sign() <= 0 {
		return big.NewInt(1)
	}
	return new(big.Int).Set(value)
}
