package arbitrage

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

                                                                             
                                                                             
                                                                               
                                                    
type crossPortfolioSearchState struct {
	position     int
	balances     map[string]*big.Int
	pending      []pendingCrossLeg
	activePairs  map[publicSpotPair]int
	dirtyPairs   map[publicSpotPair]struct{}
	usedMakers   map[string]struct{}
	selected     []*plan
	score        big.Int
	maxInsertion int
}

                                                                             
                                                                          
                                                                                 
type pendingCrossLeg struct {
	insertion    int
	pair         publicSpotPair
	restoresPair bool
	outputToken  string
	output       *big.Int
}

func (f *finder) selectBeamCrossPortfolio(
	ctx Context,
	nonce uint64,
	opportunities []crossOpportunity,
	maxGroups int,
	feesPerGroup *big.Int,
	initialBalanceFor func(string) (*big.Int, error),
	profitNormalizationFor func(string) (*big.Int, error),
) (*plan, error) {
	if len(opportunities) == 0 || maxGroups <= 0 || feesPerGroup == nil ||
		initialBalanceFor == nil || profitNormalizationFor == nil {
		return nil, nil
	}
	sort.SliceStable(opportunities, func(i, j int) bool {
		if opportunities[i].start() != opportunities[j].start() {
			return opportunities[i].start() < opportunities[j].start()
		}
		if opportunities[i].end() != opportunities[j].end() {
			return opportunities[i].end() < opportunities[j].end()
		}
		left, right := opportunities[i].comparison, opportunities[j].comparison
		if left.first.leg.BaseToken != right.first.leg.BaseToken {
			return left.first.leg.BaseToken < right.first.leg.BaseToken
		}
		if left.first.leg.QuoteToken != right.first.leg.QuoteToken {
			return left.first.leg.QuoteToken < right.first.leg.QuoteToken
		}
		if left.first.orderID != right.first.orderID {
			return left.first.orderID < right.first.orderID
		}
		return left.second.orderID < right.second.orderID
	})
	if maxGroups > len(opportunities) {
		maxGroups = len(opportunities)
	}
	maxOpportunities := maxCrossPortfolioTransitions / crossPortfolioBeamWidth
	if maxOpportunities < 1 {
		maxOpportunities = 1
	}
	if len(opportunities) > maxOpportunities {
		                                                                    
		                                                                          
		sampled := make([]crossOpportunity, maxOpportunities)
		for i := range sampled {
			bucketStart := int(int64(i) * int64(len(opportunities)) / int64(maxOpportunities))
			bucketEnd := int(int64(i+1) * int64(len(opportunities)) / int64(maxOpportunities))
			sampled[i] = opportunities[bucketStart+(bucketEnd-bucketStart)/2]
		}
		opportunities = sampled
		if maxGroups > len(opportunities) {
			maxGroups = len(opportunities)
		}
	}

	initialBalances, err := loadCrossSearchBalances(opportunities, initialBalanceFor)
	if err != nil {
		return nil, err
	}
	initial := &crossPortfolioSearchState{
		balances:    initialBalances,
		activePairs: make(map[publicSpotPair]int),
		dirtyPairs:  make(map[publicSpotPair]struct{}),
		usedMakers:  make(map[string]struct{}),
	}

	                                                                        
	                                                                           
	                                                           
	optimistic := make([]big.Int, len(opportunities))
	for i := range opportunities {
		candidate, quoteErr := f.quoteCrossForSearchState(
			nonce, &opportunities[i], 1, feesPerGroup, initial, profitNormalizationFor,
		)
		if quoteErr != nil {
			return nil, quoteErr
		}
		if candidate != nil {
			optimistic[i].Set(planFixedUtility(candidate))
		}
	}
	suffixBest := make([]big.Int, len(opportunities)+1)
	for i := len(opportunities) - 1; i >= 0; i-- {
		suffixBest[i].Set(&suffixBest[i+1])
		if optimistic[i].Cmp(&suffixBest[i]) > 0 {
			suffixBest[i].Set(&optimistic[i])
		}
	}

	beam := []*crossPortfolioSearchState{initial}
	for opportunityIndex := range opportunities {
		opportunity := &opportunities[opportunityIndex]
		next := make([]*crossPortfolioSearchState, 0, len(beam)*2)
		for _, state := range beam {
			if state == nil || !state.advanceTo(opportunity.start()) {
				continue
			}
			                                                                       
			                                                            
			next = append(next, state)
			if len(state.selected) >= maxGroups || !state.canStart(opportunity, ctx) {
				continue
			}

			taken := state.clone()
			candidate, quoteErr := f.quoteCrossForSearchState(
				nonce, opportunity, len(taken.selected)+1, feesPerGroup, taken, profitNormalizationFor,
			)
			if quoteErr != nil {
				return nil, quoteErr
			}
			if candidate == nil || !taken.applyCandidate(candidate, opportunity, feesPerGroup) {
				continue
			}
			next = append(next, taken)
		}
		beam = pruneCrossPortfolioBeam(next, &suffixBest[opportunityIndex+1], maxGroups, len(opportunities)-opportunityIndex-1)
		if len(beam) == 0 {
			return nil, nil
		}
	}

	var best *crossPortfolioSearchState
	maxPosition := int(^uint(0) >> 1)
	for _, state := range beam {
		if state == nil || len(state.selected) == 0 || !state.advanceTo(maxPosition) {
			continue
		}
		if betterFinalCrossState(state, best) {
			best = state
		}
	}
	if best == nil {
		return nil, nil
	}
	selected := append([]*plan(nil), best.selected...)
	return buildCrossPortfolio(nonce, selected, portfolioUtility(selected)), nil
}

func loadCrossSearchBalances(opportunities []crossOpportunity, read func(string) (*big.Int, error)) (map[string]*big.Int, error) {
	tokenSet := map[string]struct{}{feeToken: {}}
	for i := range opportunities {
		first := opportunities[i].comparison.first.leg
		tokenSet[first.BaseToken] = struct{}{}
		tokenSet[first.QuoteToken] = struct{}{}
	}
	tokens := make([]string, 0, len(tokenSet))
	for token := range tokenSet {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	balances := make(map[string]*big.Int, len(tokens))
	for _, token := range tokens {
		balance, err := read(token)
		if err != nil {
			return nil, err
		}
		if balance == nil || balance.Sign() < 0 || balance.Cmp(maxUint256) > 0 {
			return nil, fmt.Errorf("invalid arbitrage search balance for %s", token)
		}
		balances[token] = new(big.Int).Set(balance)
	}
	return balances, nil
}

func (f *finder) quoteCrossForSearchState(
	nonce uint64,
	opportunity *crossOpportunity,
	ordinal int,
	feesPerGroup *big.Int,
	state *crossPortfolioSearchState,
	profitNormalizationFor func(string) (*big.Int, error),
) (*plan, error) {
	if opportunity == nil || state == nil || feesPerGroup == nil {
		return nil, nil
	}
	feeBalance := state.balance(feeToken)
	if feeBalance.Cmp(feesPerGroup) < 0 {
		return nil, nil
	}
	available := state.balance(opportunity.startToken)
	if opportunity.startToken == feeToken {
		available.Sub(available, feesPerGroup)
	}
	if available.Sign() <= 0 {
		return nil, nil
	}
	capital := new(big.Int).Mul(available, new(big.Int).SetUint64(uint64(f.config.capitalBPS)))
	capital.Quo(capital, bigTenK)
	if capital.Sign() <= 0 {
		return nil, nil
	}
	capitalFor := func(token string, _ int) (*big.Int, error) {
		if token != opportunity.startToken {
			return new(big.Int), nil
		}
		return new(big.Int).Set(capital), nil
	}
	return f.quoteCrossOpportunity(
		nonce, opportunity, ordinal, feesPerGroup, capitalFor, profitNormalizationFor,
	)
}

func (state *crossPortfolioSearchState) balance(token string) *big.Int {
	if state == nil || state.balances[token] == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(state.balances[token])
}

func (state *crossPortfolioSearchState) clone() *crossPortfolioSearchState {
	if state == nil {
		return nil
	}
	cloned := &crossPortfolioSearchState{
		position:     state.position,
		balances:     make(map[string]*big.Int, len(state.balances)),
		pending:      append([]pendingCrossLeg(nil), state.pending...),
		activePairs:  make(map[publicSpotPair]int, len(state.activePairs)),
		dirtyPairs:   make(map[publicSpotPair]struct{}, len(state.dirtyPairs)),
		usedMakers:   make(map[string]struct{}, len(state.usedMakers)),
		selected:     append([]*plan(nil), state.selected...),
		maxInsertion: state.maxInsertion,
	}
	cloned.score.Set(&state.score)
	for token, balance := range state.balances {
		cloned.balances[token] = new(big.Int).Set(balance)
	}
	for i := range cloned.pending {
		cloned.pending[i].output = new(big.Int).Set(state.pending[i].output)
	}
	for pair, count := range state.activePairs {
		cloned.activePairs[pair] = count
	}
	for pair := range state.dirtyPairs {
		cloned.dirtyPairs[pair] = struct{}{}
	}
	for maker := range state.usedMakers {
		cloned.usedMakers[maker] = struct{}{}
	}
	return cloned
}

func (state *crossPortfolioSearchState) advanceTo(position int) bool {
	if state == nil || position < state.position {
		return false
	}
	consumed := 0
	for consumed < len(state.pending) && state.pending[consumed].insertion <= position {
		pending := state.pending[consumed]
		if !state.credit(pending.outputToken, pending.output) {
			return false
		}
		if count := state.activePairs[pending.pair]; count <= 1 {
			delete(state.activePairs, pending.pair)
		} else {
			state.activePairs[pending.pair] = count - 1
		}
		if !pending.restoresPair {
			state.dirtyPairs[pending.pair] = struct{}{}
		}
		consumed++
	}
	if consumed > 0 {
		state.pending = append([]pendingCrossLeg(nil), state.pending[consumed:]...)
	}
	state.position = position
	return true
}

func (state *crossPortfolioSearchState) canStart(opportunity *crossOpportunity, ctx Context) bool {
	if state == nil || opportunity == nil {
		return false
	}
	pair := opportunityPair(opportunity)
	if _, dirty := state.dirtyPairs[pair]; dirty {
		return false
	}
	if state.activePairs[pair] > 0 {
		                                                                         
		                                                                           
		                                                                           
		return false
	}
	for _, maker := range []string{
		opportunity.comparison.first.orderID,
		opportunity.comparison.second.orderID,
	} {
		if _, used := state.usedMakers[maker]; used {
			return false
		}
	}
	groupCount := len(state.selected) + 1
	retainedPublic := ctx.MaxTxs - 2*groupCount
	maxInsertion := state.maxInsertion
	if opportunity.end() > maxInsertion {
		maxInsertion = opportunity.end()
	}
	return retainedPublic >= 0 && maxInsertion <= retainedPublic
}

func (state *crossPortfolioSearchState) applyCandidate(candidate *plan, opportunity *crossOpportunity, feesPerGroup *big.Int) bool {
	if state == nil || candidate == nil || opportunity == nil || feesPerGroup == nil ||
		len(candidate.legs) != 2 || len(candidate.insertAfterPublic) != 2 || candidate.utility == nil || candidate.utility.Sign() <= 0 {
		return false
	}
	first, second := candidate.legs[0], candidate.legs[1]
	if first.inputSpent == nil || first.output == nil || second.inputSpent == nil || second.output == nil ||
		first.inputSpent.Sign() <= 0 || first.output.Sign() <= 0 || second.inputSpent.Sign() <= 0 || second.output.Sign() <= 0 ||
		legOutputToken(first.leg) != legInputToken(second.leg) || first.output.Cmp(second.inputSpent) < 0 {
		return false
	}
	if !state.debit(feeToken, feesPerGroup) || !state.debit(legInputToken(first.leg), first.inputSpent) {
		return false
	}
	                                                                         
	                                                                              
	residual := new(big.Int).Sub(first.output, second.inputSpent)
	if residual.Sign() > 0 && !state.credit(legOutputToken(first.leg), residual) {
		return false
	}
	pair := opportunityPair(opportunity)
	state.activePairs[pair]++
	state.pending = append(state.pending, pendingCrossLeg{
		insertion:    candidate.insertAfterPublic[1],
		pair:         pair,
		restoresPair: candidate.restoresBook,
		outputToken:  legOutputToken(second.leg),
		output:       new(big.Int).Set(second.output),
	})
	sort.SliceStable(state.pending, func(i, j int) bool {
		if state.pending[i].insertion != state.pending[j].insertion {
			return state.pending[i].insertion < state.pending[j].insertion
		}
		if state.pending[i].pair.baseToken != state.pending[j].pair.baseToken {
			return state.pending[i].pair.baseToken < state.pending[j].pair.baseToken
		}
		return state.pending[i].pair.quoteToken < state.pending[j].pair.quoteToken
	})
	for _, leg := range candidate.legs {
		for _, maker := range leg.makerIDs {
			if maker == "" {
				return false
			}
			if _, used := state.usedMakers[maker]; used {
				return false
			}
			state.usedMakers[maker] = struct{}{}
		}
	}
	state.selected = append(state.selected, candidate)
	state.score.Add(&state.score, planFixedUtility(candidate))
	if candidate.insertAfterPublic[1] > state.maxInsertion {
		state.maxInsertion = candidate.insertAfterPublic[1]
	}
	return true
}

func (state *crossPortfolioSearchState) debit(token string, amount *big.Int) bool {
	if state == nil || amount == nil || amount.Sign() < 0 {
		return false
	}
	current := state.balances[token]
	if current == nil || current.Cmp(amount) < 0 {
		return false
	}
	state.balances[token] = new(big.Int).Sub(current, amount)
	return true
}

func (state *crossPortfolioSearchState) credit(token string, amount *big.Int) bool {
	if state == nil || amount == nil || amount.Sign() < 0 {
		return false
	}
	current := state.balances[token]
	if current == nil {
		current = new(big.Int)
	}
	next := new(big.Int).Add(current, amount)
	if next.Cmp(maxUint256) > 0 {
		return false
	}
	state.balances[token] = next
	return true
}

func opportunityPair(opportunity *crossOpportunity) publicSpotPair {
	if opportunity == nil {
		return publicSpotPair{}
	}
	return publicSpotPair{
		baseToken:  opportunity.comparison.first.leg.BaseToken,
		quoteToken: opportunity.comparison.first.leg.QuoteToken,
	}
}

func planFixedUtility(candidate *plan) *big.Int {
	if candidate == nil || candidate.utility == nil || candidate.utility.Sign() <= 0 {
		return new(big.Int)
	}
	scaled := new(big.Int).Lsh(new(big.Int).Set(candidate.utility.Num()), portfolioUtilityPrecision)
	return scaled.Quo(scaled, candidate.utility.Denom())
}

func pruneCrossPortfolioBeam(states []*crossPortfolioSearchState, suffixBest *big.Int, maxGroups, opportunitiesLeft int) []*crossPortfolioSearchState {
	if len(states) <= 1 {
		return states
	}
	rank := func(state *crossPortfolioSearchState) *big.Int {
		result := new(big.Int).Set(&state.score)
		remaining := maxGroups - len(state.selected)
		if remaining > opportunitiesLeft {
			remaining = opportunitiesLeft
		}
		if remaining > 0 && suffixBest != nil && suffixBest.Sign() > 0 {
			result.Add(result, new(big.Int).Mul(suffixBest, big.NewInt(int64(remaining))))
		}
		return result
	}
	sort.SliceStable(states, func(i, j int) bool {
		leftRank, rightRank := rank(states[i]), rank(states[j])
		if cmp := leftRank.Cmp(rightRank); cmp != 0 {
			return cmp > 0
		}
		if cmp := states[i].score.Cmp(&states[j].score); cmp != 0 {
			return cmp > 0
		}
		if len(states[i].selected) != len(states[j].selected) {
			return len(states[i].selected) < len(states[j].selected)
		}
		return crossSearchSelectionKey(states[i]) < crossSearchSelectionKey(states[j])
	})
	if len(states) > crossPortfolioBeamWidth {
		states = states[:crossPortfolioBeamWidth]
	}
	return states
}

func betterFinalCrossState(candidate, current *crossPortfolioSearchState) bool {
	if candidate == nil || len(candidate.selected) == 0 {
		return false
	}
	if current == nil {
		return true
	}
	if cmp := candidate.score.Cmp(&current.score); cmp != 0 {
		return cmp > 0
	}
	if len(candidate.selected) != len(current.selected) {
		return len(candidate.selected) < len(current.selected)
	}
	return crossSearchSelectionKey(candidate) < crossSearchSelectionKey(current)
}

func crossSearchSelectionKey(state *crossPortfolioSearchState) string {
	if state == nil || len(state.selected) == 0 {
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
