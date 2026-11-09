package arbitrage

import (
	"crypto/sha256"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
)

                                                                               
                                                                    
type Machine struct {
	config  normalizedConfig
	state   StateReader
	account string
	sign    SignFunc
	logger  logs.Logger
	                                                                          
	                                                                            
	                                                                   
	allowUntracedCross bool

	mu        sync.Mutex
	cacheKey  string
	evaluated bool
	plans     []*plan
	evalErr   error
	batches   []*CandidateBatch
	planByID  map[string]*plan
	batchByID map[string]*CandidateBatch
}

func NewMachine(cfg EngineConfig, state StateReader, account string, sign SignFunc, logger logs.Logger) (*Machine, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	account = strings.TrimSpace(account)
	if normalized.Enabled && state == nil {
		return nil, fmt.Errorf("enabled arbitrage machine requires a state reader")
	}
	if normalized.Enabled && account == "" {
		return nil, fmt.Errorf("enabled arbitrage machine requires a funding account")
	}
	if normalized.Enabled && sign == nil {
		return nil, fmt.Errorf("enabled arbitrage machine requires a signer")
	}
	return &Machine{config: normalized, state: state, account: account, sign: sign, logger: logger}, nil
}

                                                                             
                                                                              
func (m *Machine) HasPendingContext(Context) bool { return false }

                                                                             
                                                                               
                                                                           
func (m *Machine) PrepareContext(ctx Context, orderedPublic []*pb.AnyTx, market *MarketSnapshot) (*CandidateBatch, error) {
	candidates, err := m.PrepareCandidatesContext(ctx, orderedPublic, market)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	return cloneBatch(candidates[0]), nil
}

                                                                            
                                                                               
                                                     
func (m *Machine) PrepareCandidatesContext(ctx Context, orderedPublic []*pb.AnyTx, market *MarketSnapshot) ([]*CandidateBatch, error) {
	if m == nil || !m.config.Enabled {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensureEvaluatedLocked(ctx, orderedPublic, market); err != nil {
		return nil, err
	}
	if len(m.plans) == 0 {
		return nil, nil
	}
	if len(m.batches) > 0 {
		return cloneBatches(m.batches), nil
	}
	preparedBatches := make([]*CandidateBatch, 0, len(m.plans))
	planByID := make(map[string]*plan, len(m.plans))
	batchByID := make(map[string]*CandidateBatch, len(m.plans))
	for _, candidate := range m.plans {
		batch, err := m.buildBatchLocked(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if batch == nil {
			continue
		}
		if _, duplicate := planByID[batch.ID]; duplicate {
			return nil, fmt.Errorf("duplicate arbitrage candidate ID %s", batch.ID)
		}
		preparedBatches = append(preparedBatches, cloneBatch(batch))
		planByID[batch.ID] = clonePlan(candidate)
		batchByID[batch.ID] = cloneBatch(batch)
		if m.logger != nil {
			m.logger.Info(
				"[Arbitrage] prepared account=%s proposer=%s route=%s height=%d public_prefix=%d legs=%d start=%s spent=%s output=%s profit_token=%s profit=%s roi_bps=%s",
				m.account,
				ctx.Proposer,
				candidate.routeName,
				ctx.Height,
				ctx.PublicPrefixCount,
				len(candidate.legs),
				candidate.startToken,
				candidate.startSpent,
				candidate.finalOutput,
				planProfitToken(candidate),
				candidate.profit,
				candidate.roiBPS,
			)
		}
	}
	m.batches = preparedBatches
	m.planByID = planByID
	m.batchByID = batchByID
	return cloneBatches(m.batches), nil
}

                                                                            
                                                                               
                                                                     
func (m *Machine) ValidateExecution(batch *CandidateBatch, execution CandidateExecution) error {
	if batch == nil || len(batch.Txs) == 0 {
		return fmt.Errorf("empty arbitrage candidate")
	}
	if !execution.Valid {
		return fmt.Errorf("arbitrage candidate preview invalid: %s", execution.Reason)
	}

	m.mu.Lock()
	prepared := m.batchByID[batch.ID]
	planned := m.planByID[batch.ID]
	if prepared == nil || planned == nil {
		m.mu.Unlock()
		return fmt.Errorf("arbitrage candidate %s is not the current prepared plan", batch.ID)
	}
	planned = clonePlan(planned)
	prepared = cloneBatch(prepared)
	m.mu.Unlock()
	if err := validateClosedPlanTokens(planned); err != nil {
		return fmt.Errorf("arbitrage candidate %s has invalid token route: %w", batch.ID, err)
	}
	if len(planned.legs) != len(batch.Txs) {
		return fmt.Errorf("arbitrage candidate leg count changed: planned=%d got=%d", len(planned.legs), len(batch.Txs))
	}
	if !equalInsertions(batch.InsertAfterPublic, prepared.InsertAfterPublic) {
		return fmt.Errorf("arbitrage candidate insertion schedule changed")
	}
	if len(prepared.Txs) != len(batch.Txs) {
		return fmt.Errorf("arbitrage candidate transaction count changed")
	}
	byTxID := make(map[string]ExecutionReceipt, len(execution.Receipts))
	for _, receipt := range execution.Receipts {
		if receipt.TxID == "" {
			continue
		}
		if _, exists := byTxID[receipt.TxID]; exists {
			return fmt.Errorf("duplicate arbitrage preview receipt for %s", receipt.TxID)
		}
		byTxID[receipt.TxID] = receipt
	}

	actualInputs := make([]*big.Int, len(batch.Txs))
	actualOutputs := make([]*big.Int, len(batch.Txs))
	futureActuals := make([]futureLegActual, len(batch.Txs))
	for i, tx := range batch.Txs {
		if tx == nil || tx.GetBase() == nil {
			return fmt.Errorf("nil arbitrage transaction at leg %d", i)
		}
		wantLeg := planned.legs[i]
		var (
			orderSide pb.OrderSide
			amountRaw string
			priceRaw  string
		)
		switch wantLeg.market {
		case plannedMarketSpot:
			order := tx.GetOrderTx()
			if order == nil {
				return fmt.Errorf("non-spot arbitrage transaction at leg %d", i)
			}
			if order.BaseToken != wantLeg.leg.BaseToken || order.QuoteToken != wantLeg.leg.QuoteToken || order.Side != wantLeg.leg.Side {
				return fmt.Errorf("arbitrage transaction %s route fields changed", tx.GetTxId())
			}
			orderSide, amountRaw, priceRaw = order.Side, order.Amount, order.Price
		case plannedMarketFuture:
			order := tx.GetFutureOrderTx()
			if order == nil {
				return fmt.Errorf("non-future arbitrage transaction at leg %d", i)
			}
			strategySide, validSide := strategySideFromFutureRaw(order.Side)
			if order.Pair != wantLeg.futurePair || !validSide || strategySide != wantLeg.leg.Side ||
				order.Leverage != wantLeg.futureLeverage || order.IsClose != wantLeg.futureClose ||
				order.IsMarket || order.MaxSlippageBps != 0 {
				return fmt.Errorf("future arbitrage transaction %s route fields changed", tx.GetTxId())
			}
			if wantLeg.futureClose != (strings.TrimSpace(order.ClosePositionId) != "") {
				return fmt.Errorf("future arbitrage transaction %s has invalid close position binding", tx.GetTxId())
			}
			                                                                
			                                                            
			                                                                
			orderSide, amountRaw, priceRaw = strategySide, order.Quantity, order.Price
		default:
			return fmt.Errorf("arbitrage transaction %s has unknown market", tx.GetTxId())
		}
		if tx.GetBase().FromAddress != m.account {
			return fmt.Errorf("arbitrage transaction %s sender=%q, want funding account %q", tx.GetTxId(), tx.GetBase().FromAddress, m.account)
		}
		if tx.GetBase().Nonce != planned.nonce+uint64(i) {
			return fmt.Errorf("arbitrage transaction %s nonce=%d, want %d", tx.GetTxId(), tx.GetBase().Nonce, planned.nonce+uint64(i))
		}
		if !proto.Equal(tx, prepared.Txs[i]) {
			return fmt.Errorf("arbitrage candidate transaction %d changed after preparation", i)
		}
		receipt, exists := byTxID[tx.GetTxId()]
		if !exists {
			return fmt.Errorf("missing arbitrage preview receipt for %s: %s", tx.GetTxId(), executionLegDiagnostic(planned, prepared, tx, i, ExecutionReceipt{}))
		}
		status := strings.ToUpper(strings.TrimSpace(receipt.Status))
		if status != "SUCCEED" && status != "SUCCESS" {
			return fmt.Errorf("arbitrage transaction %s preview status=%s: %s", tx.GetTxId(), receipt.Status, executionLegDiagnostic(planned, prepared, tx, i, receipt))
		}

		allowedMakers := make(map[string]struct{}, len(wantLeg.makerIDs))
		for _, orderID := range wantLeg.makerIDs {
			allowedMakers[orderID] = struct{}{}
		}
		matchedBase := new(big.Int)
		matchedQuote := new(big.Int)
		limitPrice, err := parseUint256("arbitrage limit price", priceRaw)
		if err != nil {
			return fmt.Errorf("invalid arbitrage order price for %s: %w", tx.GetTxId(), err)
		}
		if limitPrice.Sign() <= 0 {
			return fmt.Errorf("non-positive arbitrage order price for %s", tx.GetTxId())
		}
		for _, match := range receipt.Matches {
			if _, allowed := allowedMakers[match.MakerOrderID]; !allowed {
				return fmt.Errorf("arbitrage transaction %s matched unprojected maker %s: %s", tx.GetTxId(), match.MakerOrderID, executionLegDiagnostic(planned, prepared, tx, i, receipt))
			}
			quantity, parseErr := parseUint256("matched quantity", match.Quantity)
			if parseErr != nil {
				return fmt.Errorf("invalid matched quantity for %s: %w", tx.GetTxId(), parseErr)
			}
			if quantity.Sign() <= 0 {
				return fmt.Errorf("non-positive matched quantity for %s", tx.GetTxId())
			}
			price, parseErr := parseUint256("matched price", match.Price)
			if parseErr != nil {
				return fmt.Errorf("invalid matched price for %s: %w", tx.GetTxId(), parseErr)
			}
			if price.Sign() <= 0 || !basePriceProductFits(quantity, price) {
				return fmt.Errorf("non-positive or overflowing matched price for %s", tx.GetTxId())
			}
			if orderSide == pb.OrderSide_BUY && price.Cmp(limitPrice) > 0 {
				return fmt.Errorf("arbitrage BUY %s executed above limit", tx.GetTxId())
			}
			if orderSide == pb.OrderSide_SELL && price.Cmp(limitPrice) < 0 {
				return fmt.Errorf("arbitrage SELL %s executed below limit", tx.GetTxId())
			}
			matchedBase.Add(matchedBase, quantity)
			matchedQuote.Add(matchedQuote, quoteFromBase(quantity, price))
			if matchedBase.Cmp(maxUint256) > 0 || matchedQuote.Cmp(maxUint256) > 0 {
				return fmt.Errorf("arbitrage matched totals overflow for %s", tx.GetTxId())
			}
		}
		wantAmount, err := parseUint256("order amount", amountRaw)
		if err != nil {
			return fmt.Errorf("invalid arbitrage order amount for %s: %w", tx.GetTxId(), err)
		}
		if wantAmount.Sign() <= 0 || matchedBase.Cmp(wantAmount) != 0 {
			return fmt.Errorf("arbitrage transaction %s partially filled: matched=%s want=%s: %s", tx.GetTxId(), matchedBase, amountRaw, executionLegDiagnostic(planned, prepared, tx, i, receipt))
		}
		if wantLeg.market == plannedMarketFuture {
			futureActuals[i] = futureLegActual{
				matchedQuantity: new(big.Int).Set(matchedBase),
				matchedNotional: new(big.Int).Set(matchedQuote),
				receipt:         receipt,
			}
			continue
		}

		inputSpent := matchedBase
		output := matchedQuote
		if orderSide == pb.OrderSide_BUY {
			inputSpent = matchedQuote
			output = matchedBase
		}
		if inputSpent.Sign() <= 0 || output.Sign() <= 0 {
			return fmt.Errorf("arbitrage transaction %s has zero economic execution", tx.GetTxId())
		}
		actualInputs[i] = new(big.Int).Set(inputSpent)
		actualOutputs[i] = new(big.Int).Set(output)
	}
	feeBalance, err := readAvailableBalance(m.state, m.account, feeToken)
	if err != nil {
		return err
	}
	totalFees := new(big.Int).Mul(m.config.fee, big.NewInt(int64(len(batch.Txs))))
	if feeBalance.Cmp(totalFees) < 0 {
		return fmt.Errorf("arbitrage fee balance %s below candidate fees %s", feeBalance, totalFees)
	}
	if err := m.validateActualPlanEconomics(planned, actualInputs, actualOutputs); err != nil {
		return err
	}
	return m.validateActualFutureEconomics(planned, batch, futureActuals)
}

func executionLegDiagnostic(planned *plan, prepared *CandidateBatch, tx *pb.AnyTx, legIndex int, receipt ExecutionReceipt) string {
	groupIndex, groupLegIndex := 0, legIndex
	route, totalLegs := "", 0
	if planned != nil {
		route = planned.routeName
		totalLegs = len(planned.legs)
		if len(planned.groups) > 0 {
			groupIndex, groupLegIndex = -1, -1
			for candidateGroupIndex, group := range planned.groups {
				for candidateGroupLegIndex, candidateLegIndex := range group.legIndices {
					if candidateLegIndex != legIndex {
						continue
					}
					groupIndex = candidateGroupIndex
					groupLegIndex = candidateGroupLegIndex
					route = group.routeName
					break
				}
				if groupIndex >= 0 {
					break
				}
			}
		}
	}

	market, pair, side := "unknown", "", "UNKNOWN"
	expectedMakers := []string(nil)
	if planned != nil && legIndex >= 0 && legIndex < len(planned.legs) {
		leg := planned.legs[legIndex]
		expectedMakers = leg.makerIDs
		side = leg.leg.Side.String()
		switch leg.market {
		case plannedMarketSpot:
			market = "spot"
			pair = leg.leg.BaseToken + "/" + leg.leg.QuoteToken
		case plannedMarketFuture:
			market = "future"
			pair = leg.futurePair
		}
	}

	insertion := -1
	if prepared != nil && legIndex >= 0 && legIndex < len(prepared.InsertAfterPublic) {
		insertion = prepared.InsertAfterPublic[legIndex]
	}
	nonce := uint64(0)
	if tx != nil && tx.GetBase() != nil {
		nonce = tx.GetBase().Nonce
	}
	actualMatches := make([]string, 0, len(receipt.Matches))
	for _, match := range receipt.Matches {
		actualMatches = append(actualMatches, fmt.Sprintf("%s:%s@%s", match.MakerOrderID, match.Quantity, match.Price))
	}
	return fmt.Sprintf(
		"leg=%d validated_prior_legs=%d total_legs=%d group=%d group_leg=%d route=%q market=%s pair=%q side=%s insertion_after_public=%d nonce=%d expected_makers=%q actual_matches=%q actual_match_count=%d receipt_error=%q",
		legIndex,
		legIndex,
		totalLegs,
		groupIndex,
		groupLegIndex,
		route,
		market,
		pair,
		side,
		insertion,
		nonce,
		compactDiagnosticValues(expectedMakers, 8),
		compactDiagnosticValues(actualMatches, 8),
		len(receipt.Matches),
		receipt.Error,
	)
}

func compactDiagnosticValues(values []string, limit int) string {
	if len(values) == 0 {
		return ""
	}
	if limit <= 0 || len(values) <= limit {
		return strings.Join(values, ",")
	}
	return fmt.Sprintf("%s,...(%d total)", strings.Join(values[:limit], ","), len(values))
}

func (m *Machine) validateActualPlanEconomics(planned *plan, inputs, outputs []*big.Int) error {
	if planned == nil || len(inputs) != len(planned.legs) || len(outputs) != len(planned.legs) {
		return fmt.Errorf("arbitrage execution has invalid route amounts")
	}
	if len(planned.groups) == 0 {
		if candidateMarket(planned) == plannedMarketFuture {
			return nil
		}
		indices := make([]int, len(planned.legs))
		for i := range indices {
			indices[i] = i
		}
		return m.validateActualClosedGroup(planProfitToken(planned), planned.residualProfit, indices, inputs, outputs)
	}
	for groupIndex, group := range planned.groups {
		if group.market == plannedMarketFuture {
			continue
		}
		profitToken := group.profitToken
		if profitToken == "" {
			profitToken = group.startToken
		}
		if err := m.validateActualClosedGroup(profitToken, group.residualProfit, group.legIndices, inputs, outputs); err != nil {
			return fmt.Errorf("arbitrage group %d (%s): %w", groupIndex, group.routeName, err)
		}
	}
	return nil
}

func (m *Machine) validateActualClosedGroup(profitToken string, residualProfit bool, legIndices []int, inputs, outputs []*big.Int) error {
	if len(legIndices) == 0 {
		return fmt.Errorf("empty leg index sequence")
	}
	firstIndex := legIndices[0]
	if firstIndex < 0 || firstIndex >= len(inputs) || firstIndex >= len(outputs) {
		return fmt.Errorf("invalid first leg index %d", firstIndex)
	}
	firstInput := inputs[firstIndex]
	previousOutput := outputs[firstIndex]
	if firstInput == nil || previousOutput == nil {
		return fmt.Errorf("missing execution amounts")
	}
	for routeIndex := 1; routeIndex < len(legIndices); routeIndex++ {
		legIndex := legIndices[routeIndex]
		if legIndex < 0 || legIndex >= len(inputs) || legIndex >= len(outputs) {
			return fmt.Errorf("invalid leg index %d at route position %d", legIndex, routeIndex)
		}
		if inputs[legIndex] == nil || outputs[legIndex] == nil {
			return fmt.Errorf("missing execution amounts at leg %d", legIndex)
		}
		if inputs[legIndex].Cmp(previousOutput) > 0 {
			return fmt.Errorf("leg %d consumed %s, exceeding prior output %s", legIndex, inputs[legIndex], previousOutput)
		}
		previousOutput = outputs[legIndex]
	}
	profit := new(big.Int)
	denominator := new(big.Int)
	if residualProfit {
		if len(legIndices) != 2 || previousOutput.Cmp(firstInput) != 0 {
			return fmt.Errorf("residual-profit route did not restore capital: input=%s output=%s", firstInput, previousOutput)
		}
		secondIndex := legIndices[1]
		profit.Sub(outputs[firstIndex], inputs[secondIndex])
		denominator.Set(inputs[secondIndex])
	} else {
		profit.Sub(new(big.Int).Set(previousOutput), firstInput)
		denominator.Set(firstInput)
	}
	fees := new(big.Int).Mul(m.config.fee, big.NewInt(int64(len(legIndices))))
	                                                                           
	                                                                              
	if profitToken == feeToken {
		profit.Sub(profit, fees)
		denominator.Add(denominator, fees)
	}
	if profit.Sign() <= 0 || denominator.Sign() <= 0 {
		return fmt.Errorf("actual route is not profitable: input=%s output=%s profit=%s", firstInput, previousOutput, profit)
	}
	roiBPS := new(big.Int).Mul(profit, big.NewInt(10_000))
	roiBPS.Quo(roiBPS, denominator)
	if roiBPS.Cmp(new(big.Int).SetUint64(uint64(m.config.MinProfitBPS))) < 0 {
		return fmt.Errorf("actual roi_bps=%s below minimum %d", roiBPS, m.config.MinProfitBPS)
	}
	return nil
}

func (m *Machine) ensureEvaluatedLocked(ctx Context, orderedPublic []*pb.AnyTx, market *MarketSnapshot) error {
	key := contextCacheKey(ctx)
	if m.evaluated && key == m.cacheKey {
		return m.evalErr
	}
	m.cacheKey = key
	m.evaluated = true
	m.plans = nil
	m.evalErr = nil
	m.batches = nil
	m.planByID = nil
	m.batchByID = nil

	if err := validateContext(ctx); err != nil {
		m.evalErr = err
		return err
	}
	if orderedPublic != nil && len(orderedPublic) != ctx.PublicPrefixCount {
		err := fmt.Errorf("arbitrage public prefix count mismatch: txs=%d context=%d", len(orderedPublic), ctx.PublicPrefixCount)
		m.evalErr = err
		return err
	}
	if market != nil && (market.PublicPrefixHash != ctx.PublicPrefixHash || market.PublicPrefixCount != ctx.PublicPrefixCount) {
		err := fmt.Errorf(
			"arbitrage market prefix mismatch: market=%s/%d context=%s/%d",
			market.PublicPrefixHash, market.PublicPrefixCount, ctx.PublicPrefixHash, ctx.PublicPrefixCount,
		)
		m.evalErr = err
		return err
	}
	if len(orderedPublic) == 0 && market == nil {
		return nil
	}
	nonce, err := readAccountNonce(m.state, m.account)
	if err != nil {
		m.evalErr = err
		return err
	}
	f := finder{config: m.config, state: m.state, account: m.account, allowUntracedCross: m.allowUntracedCross}
	cross, err := f.findBestCrossOrder(ctx, nonce, orderedPublic, market)
	if err != nil {
		m.evalErr = err
		return err
	}
	futureCross, err := f.findBestFutureCross(ctx, nonce, orderedPublic, market)
	if err != nil {
		m.evalErr = err
		return err
	}
	var dynamic *plan
	if market != nil {
		dynamic, err = f.findBest(ctx, nonce, market)
		if err != nil {
			m.evalErr = err
			return err
		}
		                                                                         
		                                                                          
		                                                    
		if dynamic != nil && ctx.PublicPrefixCount+len(dynamic.legs) > ctx.MaxTxs {
			dynamic = nil
		}
	}
	if cross != nil && futureCross != nil {
		combined, combineErr := m.combineCrossPlans(ctx, nonce, cross, futureCross)
		if combineErr != nil {
			m.evalErr = combineErr
			return combineErr
		}
		if combined != nil {
			m.plans = append(m.plans, combined)
		}
	}
	if cross != nil {
		m.plans = append(m.plans, cross)
	}
	if futureCross != nil {
		m.plans = append(m.plans, futureCross)
	}
	if dynamic != nil {
		m.plans = append(m.plans, dynamic)
	}
	sortCandidatePlans(m.plans)
	if len(m.plans) > 1 {
		m.plans = m.plans[:1]
	}
	return nil
}

func (m *Machine) buildBatchLocked(ctx Context, candidate *plan) (*CandidateBatch, error) {
	if candidate == nil || len(candidate.legs) == 0 {
		return nil, nil
	}
	if err := validateClosedPlanTokens(candidate); err != nil {
		return nil, fmt.Errorf("invalid arbitrage token route: %w", err)
	}
	if len(candidate.legs) > ctx.PrivateTxLimit {
		return nil, fmt.Errorf("arbitrage route needs %d private slots, proposal reserved %d", len(candidate.legs), ctx.PrivateTxLimit)
	}
	insertions := append([]int(nil), candidate.insertAfterPublic...)
	if len(insertions) == 0 {
		insertions = make([]int, len(candidate.legs))
		for i := range insertions {
			insertions[i] = ctx.PublicPrefixCount
		}
	}
	if len(insertions) != len(candidate.legs) {
		return nil, fmt.Errorf("arbitrage insertion count mismatch: legs=%d insertions=%d", len(candidate.legs), len(insertions))
	}
	maxRetainedPublic := ctx.MaxTxs - len(candidate.legs)
	for i, position := range insertions {
		if position < 0 || position > ctx.PublicPrefixCount || position > maxRetainedPublic || (i > 0 && position < insertions[i-1]) {
			return nil, fmt.Errorf("invalid arbitrage insertion position %d at leg %d", position, i)
		}
	}
	if candidate.nonce > math.MaxUint64-uint64(len(candidate.legs)) {
		return nil, fmt.Errorf("arbitrage nonce overflow")
	}
	chainID := m.config.ChainID
	if chainID == 0 {
		chainID = pb.GetDefaultChainID()
	}
	if chainID == 0 {
		return nil, fmt.Errorf("arbitrage chain ID is not configured")
	}

	txs := make([]*pb.AnyTx, 0, len(candidate.legs))
	futurePositionIDs := make(map[string]string)
	futureOrdinals := make(map[string]uint64)
	for i, planned := range candidate.legs {
		base := &pb.BaseMessage{
			FromAddress:  m.account,
			Fee:          m.config.TxFee,
			Status:       pb.Status_PENDING,
			Nonce:        candidate.nonce + uint64(i),
			ChainId:      chainID,
			TargetHeight: ctx.Height,
			OnlyProposer: ctx.Proposer,
		}
		var tx *pb.AnyTx
		switch planned.market {
		case plannedMarketSpot:
			tx = &pb.AnyTx{Content: &pb.AnyTx_OrderTx{OrderTx: &pb.OrderTx{
				Base: base, BaseToken: planned.leg.BaseToken, QuoteToken: planned.leg.QuoteToken,
				Op: pb.OrderOp_ADD, Amount: planned.baseAmount.String(), Price: planned.price.String(),
				Side: planned.leg.Side, IsMarket: false, MaxSlippageBps: 0,
			}}}
		case plannedMarketFuture:
			futureSide, validSide := futureSideFromStrategy(planned.leg.Side)
			if !validSide {
				return nil, fmt.Errorf("future arbitrage leg %d has invalid side", i)
			}
			positionID := ""
			if planned.futureClose {
				positionID = futurePositionIDs[planned.futureGroup]
				if positionID == "" {
					return nil, fmt.Errorf("future arbitrage close leg %d has no preceding open position", i)
				}
			} else {
				key := planned.futurePair + "\x00" + planned.leg.Side.String()
				futureOrdinals[key]++
				positionID = plannedFuturePositionID(m.account, planned.futurePair, planned.leg.Side, ctx.Height, futureOrdinals[key])
				if planned.futureGroup == "" {
					return nil, fmt.Errorf("future arbitrage open leg %d has no group binding", i)
				}
				if _, duplicate := futurePositionIDs[planned.futureGroup]; duplicate {
					return nil, fmt.Errorf("future arbitrage group %s opens more than one position", planned.futureGroup)
				}
				futurePositionIDs[planned.futureGroup] = positionID
				positionID = ""
			}
			tx = &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
				Base: base, Pair: planned.futurePair, Side: int32(futureSide),
				Quantity: planned.baseAmount.String(), Price: planned.price.String(),
				Leverage: planned.futureLeverage, IsClose: planned.futureClose,
				ClosePositionId: positionID, IsMarket: false, MaxSlippageBps: 0,
			}}}
		default:
			return nil, fmt.Errorf("arbitrage leg %d has unknown market", i)
		}
		txID, err := m.sign(tx)
		if err != nil {
			return nil, fmt.Errorf("sign arbitrage leg %d: %w", i, err)
		}
		if txID == "" || tx.GetTxId() != txID {
			return nil, fmt.Errorf("sign arbitrage leg %d returned inconsistent tx ID", i)
		}
		txs = append(txs, tx)
	}
	return &CandidateBatch{
		ID:                candidateBatchID(ctx, candidate, txs, insertions),
		Txs:               txs,
		InsertAfterPublic: insertions,
		PublicPrefixHash:  ctx.PublicPrefixHash,
		PublicPrefixCount: ctx.PublicPrefixCount,
	}, nil
}

func validateClosedPlanTokens(candidate *plan) error {
	if candidate == nil || len(candidate.legs) == 0 {
		return fmt.Errorf("empty route")
	}
	if len(candidate.groups) == 0 {
		if candidateMarket(candidate) == plannedMarketFuture {
			return validateFutureLegRange(candidate.legs, candidate.startToken)
		}
		for i, leg := range candidate.legs {
			if leg.market != plannedMarketSpot {
				return fmt.Errorf("spot route contains non-spot leg %d", i)
			}
		}
		return validateClosedLegRange(candidate.legs, candidate.startToken)
	}
	covered := make([]bool, len(candidate.legs))
	for groupIndex, group := range candidate.groups {
		if len(group.legIndices) == 0 {
			return fmt.Errorf("group %d has no leg indices", groupIndex)
		}
		groupLegs := make([]plannedLeg, len(group.legIndices))
		for routeIndex, legIndex := range group.legIndices {
			if legIndex < 0 || legIndex >= len(candidate.legs) {
				return fmt.Errorf("group %d has invalid leg index %d", groupIndex, legIndex)
			}
			if covered[legIndex] {
				return fmt.Errorf("group %d repeats portfolio leg %d", groupIndex, legIndex)
			}
			covered[legIndex] = true
			groupLegs[routeIndex] = candidate.legs[legIndex]
		}
		var err error
		if group.market == plannedMarketFuture {
			err = validateFutureLegRange(groupLegs, group.startToken)
		} else {
			for routeIndex, leg := range groupLegs {
				if leg.market != plannedMarketSpot {
					return fmt.Errorf("group %d spot route contains non-spot leg %d", groupIndex, routeIndex)
				}
			}
			err = validateClosedLegRange(groupLegs, group.startToken)
		}
		if err != nil {
			return fmt.Errorf("group %d: %w", groupIndex, err)
		}
	}
	for legIndex, isCovered := range covered {
		if !isCovered {
			return fmt.Errorf("portfolio leg %d is not covered by a group", legIndex)
		}
	}
	return nil
}

func validateClosedLegRange(legs []plannedLeg, startToken string) error {
	if len(legs) == 0 {
		return fmt.Errorf("empty route")
	}
	if input := legInputToken(legs[0].leg); input != startToken {
		return fmt.Errorf("first input token %q differs from start token %q", input, startToken)
	}
	for i := 1; i < len(legs); i++ {
		previousOutput := legOutputToken(legs[i-1].leg)
		currentInput := legInputToken(legs[i].leg)
		if previousOutput != currentInput {
			return fmt.Errorf("leg %d output token %q differs from leg %d input token %q", i-1, previousOutput, i, currentInput)
		}
	}
	if output := legOutputToken(legs[len(legs)-1].leg); output != startToken {
		return fmt.Errorf("final output token %q differs from start token %q", output, startToken)
	}
	return nil
}

func readAccountNonce(state StateReader, address string) (uint64, error) {
	if state == nil {
		return 0, fmt.Errorf("nil arbitrage state reader")
	}
	data, err := state.Get(keys.KeyAccount(address))
	if err != nil {
		return 0, fmt.Errorf("read arbitrage account %s: %w", address, err)
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("arbitrage account %s not found", address)
	}
	var account pb.Account
	if err := unmarshalProtoCompat(data, &account); err != nil {
		return 0, fmt.Errorf("decode arbitrage account %s: %w", address, err)
	}
	if account.Address != "" && account.Address != address {
		return 0, fmt.Errorf("arbitrage account key/address mismatch: key=%s value=%s", address, account.Address)
	}
	return account.Nonce, nil
}

func validateContext(ctx Context) error {
	if strings.TrimSpace(ctx.ParentID) == "" {
		return fmt.Errorf("arbitrage proposal parent is empty")
	}
	if ctx.Height == 0 {
		return fmt.Errorf("arbitrage proposal height is zero")
	}
	if strings.TrimSpace(ctx.Proposer) == "" {
		return fmt.Errorf("arbitrage proposer is empty")
	}
	if ctx.MaxTxs < 0 || ctx.PrivateTxLimit < 0 || ctx.PrivateTxLimit > ctx.MaxTxs {
		return fmt.Errorf("invalid arbitrage proposal limits: max=%d private=%d", ctx.MaxTxs, ctx.PrivateTxLimit)
	}
	if ctx.PublicPrefixCount < 0 || ctx.PublicPrefixCount > ctx.MaxTxs {
		return fmt.Errorf("invalid arbitrage public prefix count %d", ctx.PublicPrefixCount)
	}
	if ctx.PublicPrefixCount > 0 && strings.TrimSpace(ctx.PublicPrefixHash) == "" {
		return fmt.Errorf("arbitrage public prefix hash is empty")
	}
	return nil
}

func contextCacheKey(ctx Context) string {
	                                                                            
	                                                                             
	return fmt.Sprintf(
		"%s\x00%d\x00%s\x00%d\x00%d\x00%d\x00%s",
		ctx.ParentID, ctx.Height, ctx.Proposer, ctx.MaxTxs, ctx.PrivateTxLimit,
		ctx.PublicPrefixCount, ctx.PublicPrefixHash,
	)
}

func candidateBatchID(ctx Context, candidate *plan, txs []*pb.AnyTx, insertions []int) string {
	h := sha256.New()
	_, _ = h.Write([]byte(contextCacheKey(ctx)))
	_, _ = h.Write([]byte{0})
	if candidate != nil {
		_, _ = h.Write([]byte(candidate.routeName))
		for _, leg := range candidate.legs {
			for _, makerID := range leg.makerIDs {
				                                                                   
				                                                                 
				_, _ = h.Write([]byte{0})
				_, _ = h.Write([]byte(makerID))
			}
		}
	}
	for i, tx := range txs {
		_, _ = h.Write([]byte{0})
		if tx != nil {
			_, _ = h.Write([]byte(tx.GetTxId()))
		}
		if i < len(insertions) {
			_, _ = h.Write([]byte(fmt.Sprintf("\x00%d", insertions[i])))
		}
	}
	return "arb-" + hex.EncodeToString(h.Sum(nil))
}

func cloneBatch(batch *CandidateBatch) *CandidateBatch {
	if batch == nil {
		return nil
	}
	cloned := &CandidateBatch{
		ID:                batch.ID,
		Txs:               make([]*pb.AnyTx, len(batch.Txs)),
		InsertAfterPublic: append([]int(nil), batch.InsertAfterPublic...),
		PublicPrefixHash:  batch.PublicPrefixHash,
		PublicPrefixCount: batch.PublicPrefixCount,
	}
	for i, tx := range batch.Txs {
		if tx != nil {
			cloned.Txs[i] = proto.Clone(tx).(*pb.AnyTx)
		}
	}
	return cloned
}

func cloneBatches(batches []*CandidateBatch) []*CandidateBatch {
	if len(batches) == 0 {
		return nil
	}
	cloned := make([]*CandidateBatch, 0, len(batches))
	for _, batch := range batches {
		if batch != nil {
			cloned = append(cloned, cloneBatch(batch))
		}
	}
	return cloned
}

func equalInsertions(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
