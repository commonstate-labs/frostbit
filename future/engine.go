package future

import (
	"fmt"
	"math/big"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	parallelFundingPreviewMinPositions = 512
	parallelFundingPreviewMaxWork      = 8
)

                                  
                                                               
type Engine struct {
	positions PositionStore
	insurance InsuranceFundStore
	balance   BalanceAccessor
	markPrice MarkPriceProvider
	funding   CumulativeFundingStore
	liqIndex  LiquidationIndex
	                                                                  
	                                                                        
	                                                                               
	configCache              map[string]*MarketConfig
	marketConfigLoader       func(pair string) (*MarketConfig, error)
	pendingFundingPayerCache map[string]bool

	                                                                                         
	                                                                 
	                                                                        
	OnFundingSettled func(owner, token, pair string, delta *big.Int)

	                                                                                                 
	                                                                                                      
	                                                                                                
	OnFundingLiquidated func(pair string, liquidation *LiquidationResult, adl *ADLResult)

	                                                                                     
	                                                                                                              
	currentHeight uint64

	                                                                         
	                                                                           
	                                                                             
	                                                                           
	realizedPnLBatch *realizedPnLBatch
}

type realizedPnLClaim struct {
	pair       string
	owner      string
	token      string
	positionID string
	amount     *big.Int
}

type realizedPnLBatch struct {
	losses               map[string]*big.Int
	pairOrder            []string
	insuranceInflows     map[string]*big.Int
	insuranceInflowOrder []string
	claims               []realizedPnLClaim
	actualPnLByPosition  map[string]*big.Int
	shortfallByPosition  map[string]*big.Int
	positions            map[string]*Position
	positionOrder        []string
}

                                                                                
                                                                          
                                                                               
                                      
type RealizedPnLBatchResult struct {
	ProfitClaimsByPair      map[string]*big.Int
	LossContributionsByPair map[string]*big.Int
	InsuranceInflowsByPair  map[string]*big.Int
	ShortfallByPair         map[string]*big.Int
	ActualPnLByPosition     map[string]*big.Int
	ShortfallByPosition     map[string]*big.Int
}

func (r *RealizedPnLBatchResult) ProfitClaimsForPair(pair string) *big.Int {
	if r == nil || r.ProfitClaimsByPair == nil || r.ProfitClaimsByPair[pair] == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(r.ProfitClaimsByPair[pair])
}

func (r *RealizedPnLBatchResult) ShortfallForPair(pair string) *big.Int {
	if r == nil || r.ShortfallByPair == nil || r.ShortfallByPair[pair] == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(r.ShortfallByPair[pair])
}

func (r *RealizedPnLBatchResult) LossContributionsForPair(pair string) *big.Int {
	if r == nil || r.LossContributionsByPair == nil || r.LossContributionsByPair[pair] == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(r.LossContributionsByPair[pair])
}

func (r *RealizedPnLBatchResult) InsuranceInflowsForPair(pair string) *big.Int {
	if r == nil || r.InsuranceInflowsByPair == nil || r.InsuranceInflowsByPair[pair] == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(r.InsuranceInflowsByPair[pair])
}

                                                                              
                                                                               
                                                       
func (r *RealizedPnLBatchResult) ActualPnLForPosition(positionID string) *big.Int {
	if r == nil || r.ActualPnLByPosition == nil || r.ActualPnLByPosition[positionID] == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(r.ActualPnLByPosition[positionID])
}

func (r *RealizedPnLBatchResult) ShortfallForPosition(positionID string) *big.Int {
	if r == nil || r.ShortfallByPosition == nil || r.ShortfallByPosition[positionID] == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(r.ShortfallByPosition[positionID])
}

                                                                         
                                                                             
                                                                        
func (e *Engine) BeginRealizedPnLBatch() error {
	if e.realizedPnLBatch != nil {
		return fmt.Errorf("realized PnL batch already active")
	}
	e.realizedPnLBatch = &realizedPnLBatch{
		losses:              make(map[string]*big.Int),
		insuranceInflows:    make(map[string]*big.Int),
		actualPnLByPosition: make(map[string]*big.Int),
		shortfallByPosition: make(map[string]*big.Int),
		positions:           make(map[string]*Position),
	}
	return nil
}

                                                                               
                                                                           
func (e *Engine) AbortRealizedPnLBatch() { e.realizedPnLBatch = nil }

func (e *Engine) trackRealizedPnLBatchPosition(pos *Position) {
	batch := e.realizedPnLBatch
	if batch == nil || pos == nil {
		return
	}
	if _, exists := batch.positions[pos.ID]; !exists {
		batch.positionOrder = append(batch.positionOrder, pos.ID)
	}
	batch.positions[pos.ID] = clonePositionForUpdate(pos)
}

func (e *Engine) trackRealizedPnLBatchInsuranceInflow(pair string, amount *big.Int) {
	batch := e.realizedPnLBatch
	if batch == nil || pair == "" || amount == nil || amount.Sign() <= 0 {
		return
	}
	if _, exists := batch.insuranceInflows[pair]; !exists {
		batch.insuranceInflows[pair] = new(big.Int)
		batch.insuranceInflowOrder = append(batch.insuranceInflowOrder, pair)
	}
	batch.insuranceInflows[pair].Add(batch.insuranceInflows[pair], amount)
}

                                                                            
                                                                               
                                                                            
                                                                              
                                        
func (e *Engine) setPosition(pos *Position) error {
	if err := e.positions.SetPosition(pos); err != nil {
		return err
	}
	e.trackRealizedPnLBatchPosition(pos)
	return nil
}

                                                                      
                                                                              
                                                                               
                                            
func (e *Engine) CommitRealizedPnLBatch() (*RealizedPnLBatchResult, error) {
	batch := e.realizedPnLBatch
	if batch == nil {
		return nil, fmt.Errorf("no active realized PnL batch")
	}
	                                                                          
	e.realizedPnLBatch = nil
	result := &RealizedPnLBatchResult{
		ProfitClaimsByPair:      make(map[string]*big.Int),
		LossContributionsByPair: make(map[string]*big.Int),
		InsuranceInflowsByPair:  make(map[string]*big.Int),
		ShortfallByPair:         make(map[string]*big.Int),
		ActualPnLByPosition:     make(map[string]*big.Int),
		ShortfallByPosition:     make(map[string]*big.Int),
	}
	for _, pair := range batch.insuranceInflowOrder {
		if amount := batch.insuranceInflows[pair]; amount != nil && amount.Sign() > 0 {
			result.InsuranceInflowsByPair[pair] = new(big.Int).Set(amount)
		}
	}

	for _, pair := range batch.pairOrder {
		amount := batch.losses[pair]
		if amount == nil || amount.Sign() <= 0 {
			continue
		}
		result.LossContributionsByPair[pair] = new(big.Int).Set(amount)
		if err := e.DepositInsuranceFund(pair, amount); err != nil {
			return nil, fmt.Errorf("deposit batched realized loss for %s: %w", pair, err)
		}
	}

	for _, claim := range batch.claims {
		if result.ProfitClaimsByPair[claim.pair] == nil {
			result.ProfitClaimsByPair[claim.pair] = new(big.Int)
		}
		result.ProfitClaimsByPair[claim.pair].Add(result.ProfitClaimsByPair[claim.pair], claim.amount)
		consumed, shortfall, err := e.WithdrawInsuranceFund(claim.pair, claim.amount)
		if err != nil {
			return nil, fmt.Errorf("pay batched realized profit for %s: %w", claim.positionID, err)
		}
		if consumed != nil && consumed.Sign() > 0 {
			if err := e.balance.AddBalance(claim.owner, claim.token, consumed); err != nil {
				return nil, fmt.Errorf("credit batched realized profit for %s: %w", claim.positionID, err)
			}
			pos := batch.positions[claim.positionID]
			if pos == nil {
				return nil, fmt.Errorf("missing final position clone for realized PnL claim %s", claim.positionID)
			}
			pos.RealizedPnL = biAdd(pos.RealizedPnL, consumed)
			if batch.actualPnLByPosition[claim.positionID] == nil {
				batch.actualPnLByPosition[claim.positionID] = new(big.Int)
			}
			batch.actualPnLByPosition[claim.positionID].Add(batch.actualPnLByPosition[claim.positionID], consumed)
		}
		if shortfall != nil && shortfall.Sign() > 0 {
			if err := e.insurance.AddShortfall(claim.pair, shortfall); err != nil {
				return nil, fmt.Errorf("record batched realized PnL shortfall for %s: %w", claim.pair, err)
			}
			if result.ShortfallByPair[claim.pair] == nil {
				result.ShortfallByPair[claim.pair] = new(big.Int)
			}
			result.ShortfallByPair[claim.pair].Add(result.ShortfallByPair[claim.pair], shortfall)
			if batch.shortfallByPosition[claim.positionID] == nil {
				batch.shortfallByPosition[claim.positionID] = new(big.Int)
			}
			batch.shortfallByPosition[claim.positionID].Add(batch.shortfallByPosition[claim.positionID], shortfall)
		}
	}
	for positionID, amount := range batch.actualPnLByPosition {
		result.ActualPnLByPosition[positionID] = new(big.Int).Set(amount)
	}
	for positionID, amount := range batch.shortfallByPosition {
		result.ShortfallByPosition[positionID] = new(big.Int).Set(amount)
	}

	for _, positionID := range batch.positionOrder {
		pos := batch.positions[positionID]
		if pos == nil {
			continue
		}
		if err := e.setPosition(pos); err != nil {
			return nil, fmt.Errorf("persist batched realized PnL for %s: %w", positionID, err)
		}
	}
	return result, nil
}

                                                                          
func (e *Engine) SetCurrentHeight(h uint64) { e.currentHeight = h }

                               
func NewEngine(ps PositionStore, ins InsuranceFundStore, ba BalanceAccessor, mp MarkPriceProvider, cf CumulativeFundingStore, li LiquidationIndex) *Engine {
	return &Engine{
		positions:                ps,
		insurance:                ins,
		balance:                  ba,
		markPrice:                mp,
		funding:                  cf,
		liqIndex:                 li,
		configCache:              make(map[string]*MarketConfig),
		pendingFundingPayerCache: make(map[string]bool),
	}
}

                                                                                                             
                                                                                                  
func (e *Engine) RegisterMarket(cfg *MarketConfig) {
	if cfg == nil {
		return
	}
	cfg.ApplyDefaults()
	e.configCache[cfg.Pair] = cfg
}

                                                                           
                                                                             
                                 
func (e *Engine) SetMarketConfigLoader(loader func(pair string) (*MarketConfig, error)) {
	e.marketConfigLoader = loader
}

                                                                                  
func (e *Engine) GetMarketConfig(pair string) (*MarketConfig, error) {
	if cfg, ok := e.configCache[pair]; ok {
		cfg.ApplyDefaults()
		return cfg, nil
	}
	if e.marketConfigLoader != nil {
		cfg, err := e.marketConfigLoader(pair)
		if err != nil {
			return nil, err
		}
		if cfg == nil || cfg.Pair != pair || cfg.BaseToken == "" || cfg.QuoteToken == "" {
			return nil, fmt.Errorf("invalid explicit market config for %s", pair)
		}
		cfg.ApplyDefaults()
		e.configCache[pair] = cfg
		return cfg, nil
	}
	quote := pair
	if idx := strings.LastIndex(pair, "_"); idx >= 0 {
		quote = pair[idx+1:]
	}
	cfg := &MarketConfig{
		Pair:                     pair,
		MaxLeverage:              DefaultMaxLeverage,
		MaintenanceMarginRateBps: DefaultMaintenanceMarginRateBps,
		QuoteToken:               quote,
	}
	e.configCache[pair] = cfg
	return cfg, nil
}

                                                      
                                            
  
            
                                                                                        
                                                                           
                                          
  
                                                                                           
                                                                                                         
                                                                                         
                                                                   
func (e *Engine) PreviewPendingFunding(pos *Position) *big.Int {
	pending, err := e.PreviewPendingFundingStrict(pos)
	if err != nil {
		return new(big.Int)
	}
	return pending
}

                                                                            
                                                                            
                                                                   
func (e *Engine) PreviewPendingFundingStrict(pos *Position) (*big.Int, error) {
	if e.funding == nil || pos == nil || pos.Status != StatusOpen {
		return new(big.Int), nil
	}
	cumLong, cumShort, err := e.funding.GetCumulativeFunding(pos.Pair)
	if err != nil {
		return nil, fmt.Errorf("get cumulative funding for %s: %w", pos.Pair, err)
	}
	currentCum := cumLong
	if pos.Side == Short {
		currentCum = cumShort
	}
	lastCum := pos.LastCumulativeFunding
	if lastCum == nil {
		lastCum = new(big.Int)
	}
	if currentCum.Cmp(lastCum) == 0 {
		return new(big.Int), nil
	}
	                                                                                                    
	markPrice, err := e.markPrice.GetSettleMarkPrice(pos.Pair)
	if err != nil {
		return nil, fmt.Errorf("get settle mark price for %s: %w", pos.Pair, err)
	}
	return CalcPendingFunding(pos.Side, pos.Quantity, markPrice, lastCum, currentCum), nil
}

func (e *Engine) hasPendingFundingPayerStrict(pair string) (bool, error) {
	if e.positions == nil {
		return false, nil
	}
	if e.pendingFundingPayerCache != nil {
		if cached, ok := e.pendingFundingPayerCache[pair]; ok {
			return cached, nil
		}
	}
	positions, err := e.positions.GetAllOpenPositions(pair)
	if err != nil {
		return false, fmt.Errorf("scan funding payers for %s: %w", pair, err)
	}
	hasPayer := false
	for _, p := range positions {
		if p == nil || p.Status != StatusOpen {
			continue
		}
		pending, err := e.PreviewPendingFundingStrict(p)
		if err != nil {
			return false, fmt.Errorf("preview funding payer %s: %w", p.ID, err)
		}
		if pending.Sign() < 0 {
			hasPayer = true
			break
		}
	}
	if e.pendingFundingPayerCache != nil {
		e.pendingFundingPayerCache[pair] = hasPayer
	}
	return hasPayer, nil
}

                                                                       
                                                                                                        
func (e *Engine) ApplyPendingFunding(pos *Position, cfg *MarketConfig) error {
	if e.funding == nil || pos == nil || pos.Status != StatusOpen {
		return nil
	}
	allowPositivePayout := false
	pending, err := e.PreviewPendingFundingStrict(pos)
	if err != nil {
		return err
	}
	if pending.Sign() > 0 {
		allowPositivePayout, err = e.hasPendingFundingPayerStrict(pos.Pair)
		if err != nil {
			return err
		}
	}
	return e.applyPendingFunding(pos, cfg, allowPositivePayout, true)
}

func (e *Engine) applyPendingFunding(pos *Position, cfg *MarketConfig, allowPositivePayout, autoLiquidate bool) error {
	if e.funding == nil || pos == nil || pos.Status != StatusOpen {
		return nil
	}
	cumLong, cumShort, err := e.funding.GetCumulativeFunding(pos.Pair)
	if err != nil {
		return fmt.Errorf("get cumulative funding for %s: %w", pos.Pair, err)
	}
	currentCum := cumLong
	if pos.Side == Short {
		currentCum = cumShort
	}
	lastCum := pos.LastCumulativeFunding
	if lastCum == nil {
		lastCum = new(big.Int)
	}
	                                                                          
	                                                                           
	                                                                           
	if currentCum.Cmp(lastCum) == 0 {
		return nil
	}

	                                                                                                 
	                                                                                                 
	                                                                                         
	settleMark, err := e.markPrice.GetSettleMarkPrice(pos.Pair)
	if err != nil {
		return fmt.Errorf("get settle mark price for %s: %w", pos.Pair, err)
	}
	pending := CalcPendingFunding(pos.Side, pos.Quantity, settleMark, lastCum, currentCum)

	                                                                             
	                                                                               
	                                                                          
	                                                                           
	                                                                         
	                                                                               
	                                                            
	work := clonePositionForUpdate(pos)
	applied := new(big.Int)
	if pending.Sign() > 0 {
		if allowPositivePayout {
			consumed, shortfall, err := e.WithdrawInsuranceFund(pos.Pair, pending)
			if err != nil {
				return fmt.Errorf("funding insurance payout for %s: %w", pos.Pair, err)
			}
			if consumed != nil && consumed.Sign() > 0 {
				if err := e.balance.AddBalance(pos.Owner, cfg.QuoteToken, consumed); err != nil {
					                                                                    
					if rerr := e.DepositInsuranceFund(pos.Pair, consumed); rerr != nil {
						return fmt.Errorf("credit funding payout: %w (restore insurance: %v)", err, rerr)
					}
					return fmt.Errorf("credit funding payout: %w", err)
				}
				applied.Set(consumed)
			}
			if shortfall != nil && shortfall.Sign() > 0 {
				if err := e.insurance.AddShortfall(pos.Pair, shortfall); err != nil {
					return fmt.Errorf("record funding payout shortfall for %s: %w", pos.Pair, err)
				}
			}
		}
	} else if pending.Sign() < 0 {
		cost := new(big.Int).Neg(pending)
		collected := new(big.Int)
		if work.Margin.Cmp(cost) >= 0 {
			work.Margin = new(big.Int).Sub(work.Margin, cost) // safe-math-ok: 仅保证金足够覆盖 cost 的比较分支执行扣款
			collected.Set(cost)
		} else {
			excess := new(big.Int).Sub(cost, work.Margin) // safe-math-ok: 仅 cost > work.Margin 分支计算剩余欠款，实际扣款另受可用余额限制
			collected.Set(work.Margin)
			work.Margin = new(big.Int)

			bal, err := e.balance.GetAvailableBalance(pos.Owner, cfg.QuoteToken)
			if err != nil {
				return fmt.Errorf("get funding payer balance: %w", err)
			}
			if bal == nil {
				bal = new(big.Int)
			}
			actualDeduct := new(big.Int).Set(excess)
			if bal.Cmp(actualDeduct) < 0 {
				actualDeduct = new(big.Int).Set(bal)
			}
			if actualDeduct.Sign() > 0 {
				if err := e.balance.DeductBalance(pos.Owner, cfg.QuoteToken, actualDeduct); err != nil {
					return fmt.Errorf("deduct funding payer balance: %w", err)
				}
				collected.Add(collected, actualDeduct)
			}
		}
		if collected.Sign() > 0 {
			if err := e.DepositInsuranceFund(pos.Pair, collected); err != nil {
				return fmt.Errorf("deposit collected funding for %s: %w", pos.Pair, err)
			}
			applied.Neg(collected)
		}
	}
	if work.Margin.Cmp(pos.Margin) != 0 {
		resetMarginBasis(work)
	}
	work.LastCumulativeFunding = new(big.Int).Set(currentCum)
	work.SettledFunding = biAdd(work.SettledFunding, applied)
	                                                                            
	                                         
	work.LastSettledFunding = new(big.Int).Set(applied)
	if err := e.setPosition(work); err != nil {
		return fmt.Errorf("persist funding settlement for %s: %w", work.ID, err)
	}
	*pos = *work

	if e.OnFundingSettled != nil && applied.Sign() != 0 {
		e.OnFundingSettled(work.Owner, cfg.QuoteToken, work.Pair, new(big.Int).Set(applied))
	}

	                                                                                                            
	                                                                                                          
	                                                                       
	if autoLiquidate && work.Status == StatusOpen && work.Margin.Sign() <= 0 {
		liveMark, err := e.markPrice.GetMarkPrice(work.Pair)
		if err != nil {
			return fmt.Errorf("get live mark for funding liquidation: %w", err)
		}
		liqResult, err := e.processLiquidation(work, liveMark, cfg)
		if err != nil {
			return fmt.Errorf("funding liquidation for %s: %w", work.ID, err)
		}
		*pos = *work
		adlResult, err := e.handleLiquidationResult(work.Pair, liqResult)
		if err != nil {
			return fmt.Errorf("handle funding liquidation result for %s: %w", work.ID, err)
		}
		if e.OnFundingLiquidated != nil {
			e.OnFundingLiquidated(work.Pair, liqResult, adlResult)
		}
	}
	return nil
}

func (e *Engine) ApplyPendingFundingForPair(pair string) error {
	if e.positions == nil {
		return nil
	}
	cfg, err := e.GetMarketConfig(pair)
	if err != nil {
		return err
	}
	positions, err := e.positions.GetAllOpenPositions(pair)
	if err != nil {
		return err
	}
	pendingByID, hasPayer, err := e.previewPendingFundingByPosition(positions)
	if err != nil {
		return err
	}
	sort.Slice(positions, func(i, j int) bool {
		pi, pj := pendingByID[positions[i].ID], pendingByID[positions[j].ID]
		if pi != nil && pj != nil && pi.Sign() != pj.Sign() {
			return pi.Sign() < pj.Sign()
		}
		return positions[i].ID < positions[j].ID
	})
	                                                                          
	                                                                       
	                                                                        
	                                                                           
	                                                                              
	for _, listed := range positions {
		if listed == nil {
			continue
		}
		pos, err := e.positions.GetPosition(listed.ID)
		if err != nil {
			return err
		}
		if pos == nil || pos.Status != StatusOpen {
			continue
		}
		if err := e.applyPendingFunding(pos, cfg, hasPayer, false); err != nil {
			return err
		}
	}

	var markPrice *big.Int
	if e.markPrice != nil {
		markPrice, err = e.markPrice.GetMarkPrice(pair)
		if err != nil {
			return fmt.Errorf("get live mark after funding settlement for %s: %w", pair, err)
		}
	}
	                                                                            
	                                                                          
	                                                                         
	               
	for _, listed := range positions {
		if listed == nil {
			continue
		}
		fresh, err := e.positions.GetPosition(listed.ID)
		if err != nil {
			return err
		}
		if fresh == nil || fresh.Status != StatusOpen {
			continue
		}
		if markPrice != nil && ShouldLiquidatePosition(fresh, fresh.Margin, markPrice, cfg.MaintenanceMarginRateOrDefault()) {
			liqResult, err := e.processLiquidation(fresh, markPrice, cfg)
			if err != nil {
				return err
			}
			adlResult, err := e.handleLiquidationResult(pair, liqResult)
			if err != nil {
				return err
			}
			if e.OnFundingLiquidated != nil {
				e.OnFundingLiquidated(pair, liqResult, adlResult)
			}
			continue
		}
		e.updateLiqIndex(fresh, cfg)
	}
	return nil
}

func (e *Engine) previewPendingFundingByPosition(positions []*Position) (map[string]*big.Int, bool, error) {
	workers := futureParallelWorkers(len(positions), parallelFundingPreviewMinPositions)
	if workers <= 1 {
		pendingByID := make(map[string]*big.Int, len(positions))
		hasPayer := false
		for _, pos := range positions {
			if pos == nil || pos.Status != StatusOpen {
				continue
			}
			pending, err := e.PreviewPendingFundingStrict(pos)
			if err != nil {
				return nil, false, fmt.Errorf("preview pending funding for %s: %w", pos.ID, err)
			}
			pendingByID[pos.ID] = pending
			if pending.Sign() < 0 {
				hasPayer = true
			}
		}
		return pendingByID, hasPayer, nil
	}

	type partResult struct {
		pendingByID map[string]*big.Int
		hasPayer    bool
		err         error
	}
	chunk := (len(positions) + workers - 1) / workers
	parts := make([]partResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		start := i * chunk
		if start >= len(positions) {
			parts = parts[:i]
			break
		}
		end := start + chunk
		if end > len(positions) {
			end = len(positions)
		}
		wg.Add(1)
		go func(idx, start, end int) {
			defer wg.Done()
			pendingByID := make(map[string]*big.Int, end-start)
			hasPayer := false
			for _, pos := range positions[start:end] {
				if pos == nil || pos.Status != StatusOpen {
					continue
				}
				pending, err := e.PreviewPendingFundingStrict(pos)
				if err != nil {
					parts[idx] = partResult{err: fmt.Errorf("preview pending funding for %s: %w", pos.ID, err)}
					return
				}
				pendingByID[pos.ID] = pending
				if pending.Sign() < 0 {
					hasPayer = true
				}
			}
			parts[idx] = partResult{pendingByID: pendingByID, hasPayer: hasPayer}
		}(i, start, end)
	}
	wg.Wait()

	total := 0
	hasPayer := false
	for _, part := range parts {
		if part.err != nil {
			return nil, false, part.err
		}
		total += len(part.pendingByID)
		hasPayer = hasPayer || part.hasPayer
	}
	pendingByID := make(map[string]*big.Int, total)
	for _, part := range parts {
		ids := make([]string, 0, len(part.pendingByID))
		for id := range part.pendingByID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			pendingByID[id] = part.pendingByID[id]
		}
	}
	return pendingByID, hasPayer, nil
}

func futureParallelWorkers(items, minItems int) int {
	if items < minItems {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > parallelFundingPreviewMaxWork {
		workers = parallelFundingPreviewMaxWork
	}
	if workers < 2 || items < workers*2 {
		return 1
	}
	return workers
}

func cloneBigInt(v *big.Int) *big.Int {
	if v == nil {
		return nil
	}
	return new(big.Int).Set(v)
}

                                                                              
                                                                           
                                                                      
func clonePositionForUpdate(pos *Position) *Position {
	if pos == nil {
		return nil
	}
	cloned := *pos
	cloned.EntryPrice = cloneBigInt(pos.EntryPrice)
	cloned.EntryNotionalNumerator = cloneBigInt(pos.EntryNotionalNumerator)
	cloned.Quantity = cloneBigInt(pos.Quantity)
	cloned.Margin = cloneBigInt(pos.Margin)
	cloned.MarginBasisAmount = cloneBigInt(pos.MarginBasisAmount)
	cloned.MarginBasisClosed = cloneBigInt(pos.MarginBasisClosed)
	cloned.LastCumulativeFunding = cloneBigInt(pos.LastCumulativeFunding)
	cloned.FirstCumulativeFunding = cloneBigInt(pos.FirstCumulativeFunding)
	cloned.SettledFunding = cloneBigInt(pos.SettledFunding)
	cloned.LastSettledFunding = cloneBigInt(pos.LastSettledFunding)
	cloned.RealizedPnL = cloneBigInt(pos.RealizedPnL)
	cloned.ADLWithheldPnL = cloneBigInt(pos.ADLWithheldPnL)
	cloned.ClosedQty = cloneBigInt(pos.ClosedQty)
	cloned.ExitNotional = cloneBigInt(pos.ExitNotional)
	cloned.ClosePnLRemainder = cloneBigInt(pos.ClosePnLRemainder)
	cloned.MaxQuantity = cloneBigInt(pos.MaxQuantity)
	cloned.InitialMargin = cloneBigInt(pos.InitialMargin)
	return &cloned
}

                                                                      
                                                                      
                                                                   
func (e *Engine) handleLiquidationResult(pair string, result *LiquidationResult) (*ADLResult, error) {
	if result == nil || !result.NeedsADL || result.ADLShortfall == nil || result.ADLShortfall.Sign() <= 0 {
		return nil, nil
	}
	adlResult, err := e.ExecuteADL(pair, result.Side.Opposite(), result.ADLShortfall)
	if err != nil {
		return nil, err
	}
	return adlResult, nil
}

                                                                               
                                                                             
                                                                              
  
                                                                     
                                                                  
func (e *Engine) PreviewTradeClosePnL(owner, pair string, side Side, qty, price *big.Int) (*big.Int, error) {
	if qty == nil || qty.Sign() <= 0 {
		return nil, fmt.Errorf("preview close PnL: quantity must be positive")
	}
	if price == nil || price.Sign() <= 0 {
		return nil, fmt.Errorf("preview close PnL: price must be positive")
	}
	pos, err := e.positions.GetPositionByOwnerAndPairSide(owner, pair, side.Opposite())
	if err != nil {
		return nil, err
	}
	if pos == nil || pos.Status != StatusOpen || pos.Quantity == nil || pos.Quantity.Sign() <= 0 {
		return new(big.Int), nil
	}
	closeQty := new(big.Int).Set(qty)
	if closeQty.Cmp(pos.Quantity) > 0 {
		closeQty.Set(pos.Quantity)
	}
	return incrementalClosePnL(pos, closeQty, price), nil
}

func (e *Engine) ProcessTradeForPosition(
	owner, pair string,
	side Side,
	tradeQty, tradePrice *big.Int,
	frozenMargin *big.Int,
	height uint64,
) error {
	cfg, err := e.GetMarketConfig(pair)
	if err != nil {
		return err
	}
	if frozenMargin == nil {
		frozenMargin = new(big.Int)
	}

	                        
	existing, err := e.positions.GetPositionByOwnerAndPairSide(owner, pair, side)
	if err != nil {
		return err
	}

	if existing != nil && existing.Status == StatusOpen {
		if frozenMargin != nil && frozenMargin.Sign() < 0 {
			return fmt.Errorf("refuse to increase position with negative margin: owner=%s pair=%s side=%d", owner, pair, side)
		}
		if err := e.ApplyPendingFunding(existing, cfg); err != nil {
			return err
		}
		if existing.Status != StatusOpen {
			return fmt.Errorf("cannot increase position %s: pending funding made it terminal", existing.ID)
		}
		marginIncrement := new(big.Int)
		if frozenMargin != nil {
			marginIncrement.Set(frozenMargin)
		}
		newQuantity := new(big.Int).Add(existing.Quantity, tradeQty) // safe-math-ok: 数量聚合用于重新计算名义价值与最低保证金，不是资金增发
		newEntryNotional := positionEntryNotionalNumerator(existing)
		newEntryNotional.Add(newEntryNotional, new(big.Int).Mul(tradePrice, tradeQty)) // safe-math-ok: 保留完整累计入场名义价值分子，随后用于均价与保证金公式
		newEntry := new(big.Int).Quo(new(big.Int).Set(newEntryNotional), newQuantity)
		newMargin := new(big.Int).Add(existing.Margin, marginIncrement) // safe-math-ok: 追加已冻结的成交保证金，保留仓位现有任意精度累计域
		requiredMargin := requiredMarginForEntryNotional(newEntryNotional, cfg.MaxLeverageOrDefault())
		if newMargin.Cmp(requiredMargin) < 0 {
			return fmt.Errorf("refuse to increase position: aggregate margin %s below required %s", newMargin, requiredMargin)
		}
		existing.EntryPrice = newEntry
		existing.EntryNotionalNumerator = newEntryNotional
		existing.Quantity = newQuantity
		existing.Margin = newMargin
		resetMarginBasis(existing)
		existing.InitialMargin = biAdd(existing.InitialMargin, marginIncrement)
		existing.MaxQuantity = biMax(existing.MaxQuantity, existing.Quantity)
		if err := e.setPosition(existing); err != nil {
			return err
		}
		e.updateLiqIndex(existing, cfg)
		return nil
	}

	                                                 
	opposite, err := e.positions.GetPositionByOwnerAndPairSide(owner, pair, side.Opposite())
	if err != nil {
		return err
	}

	if opposite != nil && opposite.Status == StatusOpen {
		if err := e.ApplyPendingFunding(opposite, cfg); err != nil {
			return err
		}
		if opposite.Status != StatusOpen {
			return fmt.Errorf("cannot close position %s: pending funding made it terminal", opposite.ID)
		}
		                                                                                     
		                                                                                  
		                                                                                       
		                                                                                         
		                                                                                            
		reduceQty := new(big.Int).Set(tradeQty)
		if reduceQty.Cmp(opposite.Quantity) > 0 {
			reduceQty.Set(opposite.Quantity)
		}
		segmentPnL := incrementalClosePnL(opposite, reduceQty, tradePrice)
		releasedMargin := previewMarginRelease(opposite, reduceQty)
		if new(big.Int).Add(releasedMargin, segmentPnL).Sign() < 0 { // safe-math-ok: 可为负的平仓权益用于判定是否必须走清算路径
			return e.handleBankruptReverse(opposite, tradeQty, tradePrice, frozenMargin, cfg, height, owner, pair, side)
		}
		return e.reducePosition(opposite, tradeQty, tradePrice, frozenMargin, cfg, height, owner, pair, side)
	}

	                                                           
	                                                                                                 
	                                                                                     
	if frozenMargin == nil || frozenMargin.Sign() <= 0 {
		return fmt.Errorf("refuse to open new position with non-positive margin: owner=%s pair=%s side=%d", owner, pair, side)
	}
	minimumMargin := CalcRequiredMargin(tradeQty, tradePrice, cfg.MaxLeverageOrDefault())
	if frozenMargin.Cmp(minimumMargin) < 0 {
		return fmt.Errorf("refuse to open new position: margin %s below required %s", frozenMargin, minimumMargin)
	}
	lastCum := new(big.Int)
	if e.funding != nil {
		cumL, cumS, err := e.funding.GetCumulativeFunding(pair)
		if err != nil {
			return fmt.Errorf("snapshot cumulative funding for new position %s/%s: %w", owner, pair, err)
		}
		if side == Long {
			lastCum = cumL
		} else {
			lastCum = cumS
		}
	}
	positionID, err := e.positions.AllocatePositionID(owner, pair, side, height)
	if err != nil {
		return fmt.Errorf("allocate future position ID: %w", err)
	}
	pos := &Position{
		ID:    positionID,
		Owner: owner, Pair: pair, Side: side,
		EntryPrice:             new(big.Int).Set(tradePrice),
		EntryNotionalNumerator: new(big.Int).Mul(tradePrice, tradeQty), // safe-math-ok: 保留价格乘数量的精确分子供均价及分段平仓计算
		Quantity:               new(big.Int).Set(tradeQty),
		Margin:                 new(big.Int).Set(frozenMargin),
		MarginBasisAmount:      new(big.Int).Set(frozenMargin),
		MarginBasisClosed:      new(big.Int),
		Status:                 StatusOpen, OpenHeight: height,
		LastCumulativeFunding:  lastCum,
		FirstCumulativeFunding: new(big.Int).Set(lastCum),                               
		SettledFunding:         new(big.Int),
		LastSettledFunding:     new(big.Int),
		RealizedPnL:            new(big.Int),
		ClosedQty:              new(big.Int),
		ExitNotional:           new(big.Int),
		ClosePnLRemainder:      new(big.Int),
		MaxQuantity:            new(big.Int).Set(tradeQty),
		InitialMargin:          new(big.Int).Set(frozenMargin),
	}
	if err := e.setPosition(pos); err != nil {
		return err
	}
	e.updateLiqIndex(pos, cfg)
	return nil
}

                                                                                                   
                                                                                                         
                                                                                                           
  
               
                                                                                             
                                                                                                
                                                                                                         
                                                                                             
                      
func (e *Engine) handleBankruptReverse(
	opposite *Position,
	tradeQty, tradePrice, frozenMargin *big.Int,
	cfg *MarketConfig,
	height uint64,
	owner, pair string,
	side Side,
) error {
	reduceQty := new(big.Int).Set(tradeQty)
	if reduceQty.Cmp(opposite.Quantity) > 0 {
		reduceQty.Set(opposite.Quantity)
	}
	remainQty := new(big.Int).Sub(tradeQty, reduceQty) // safe-math-ok: reduceQty 已取 tradeQty 与旧仓数量较小值
	remainMargin := new(big.Int)
	if remainQty.Sign() > 0 {
		remainMargin = MulDivCeil(frozenMargin, remainQty, tradeQty)
		minimumMargin := CalcRequiredMargin(remainQty, tradePrice, cfg.MaxLeverageOrDefault())
		if remainMargin.Sign() > 0 && remainMargin.Cmp(minimumMargin) < 0 {
			return fmt.Errorf("refuse bankrupt reverse: margin %s below required %s", remainMargin, minimumMargin)
		}
	}
	liqResult, err := e.LiquidatePartial(opposite, tradePrice, reduceQty, cfg)
	if err != nil {
		return err
	}
	if _, err := e.handleLiquidationResult(pair, liqResult); err != nil {
		return fmt.Errorf("handle bankrupt reverse liquidation for %s: %w", opposite.ID, err)
	}

	if remainQty.Sign() <= 0 {
		if frozenMargin != nil && frozenMargin.Sign() > 0 {
			if err := e.balance.AddBalance(owner, cfg.QuoteToken, frozenMargin); err != nil {
				return fmt.Errorf("refund unused reverse margin: %w", err)
			}
		}
		return nil
	}

	                                                                      
	                                                                       
	refund := new(big.Int).Sub(frozenMargin, remainMargin) // safe-math-ok: remainMargin 按剩余数量比例分摊，正退款另经 BalanceAccessor 校验
	if refund.Sign() > 0 {
		if err := e.balance.AddBalance(owner, cfg.QuoteToken, refund); err != nil {
			return fmt.Errorf("refund reverse margin: %w", err)
		}
	}
	if remainMargin.Sign() <= 0 {
		return nil
	}
	return e.ProcessTradeForPosition(owner, pair, side, remainQty, tradePrice, remainMargin, height)
}

func (e *Engine) settleRealizedPnL(pos *Position, token string, pnl *big.Int) (*big.Int, error) {
	if pnl == nil || pnl.Sign() == 0 {
		return new(big.Int), nil
	}
	if pos == nil || pos.Pair == "" {
		return nil, fmt.Errorf("settle realized PnL: missing position pair")
	}
	pair := pos.Pair
	if batch := e.realizedPnLBatch; batch != nil {
		if pnl.Sign() < 0 {
			if _, exists := batch.losses[pair]; !exists {
				batch.losses[pair] = new(big.Int)
				batch.pairOrder = append(batch.pairOrder, pair)
			}
			batch.losses[pair].Add(batch.losses[pair], new(big.Int).Neg(pnl)) // safe-math-ok: 块内已实现负盈亏的临时累计，实际结算另经保险与余额适配器
			if batch.actualPnLByPosition[pos.ID] == nil {
				batch.actualPnLByPosition[pos.ID] = new(big.Int)
			}
			batch.actualPnLByPosition[pos.ID].Add(batch.actualPnLByPosition[pos.ID], pnl)
			return new(big.Int).Set(pnl), nil
		}
		if pos.ID == "" || pos.Owner == "" {
			return nil, fmt.Errorf("settle batched realized PnL: incomplete position identity")
		}
		batch.claims = append(batch.claims, realizedPnLClaim{
			pair:       pair,
			owner:      pos.Owner,
			token:      token,
			positionID: pos.ID,
			amount:     new(big.Int).Set(pnl),
		})
		return new(big.Int), nil
	}
	if pnl.Sign() < 0 {
		if err := e.DepositInsuranceFund(pair, new(big.Int).Neg(pnl)); err != nil {
			return nil, fmt.Errorf("deposit realized loss for %s: %w", pair, err)
		}
		return new(big.Int).Set(pnl), nil
	}
	consumed, shortfall, err := e.WithdrawInsuranceFund(pair, pnl)
	if err != nil {
		return nil, fmt.Errorf("withdraw realized profit for %s: %w", pair, err)
	}
	if shortfall != nil && shortfall.Sign() > 0 {
		if err := e.insurance.AddShortfall(pair, shortfall); err != nil {
			return nil, fmt.Errorf("record realized PnL shortfall for %s: %w", pair, err)
		}
	}
	if consumed == nil {
		return new(big.Int), nil
	}
	return consumed, nil
}

func validateSolventClose(pos *Position, closeQty, tradePrice, pnl, releasedMargin *big.Int) error {
	if pos == nil || closeQty == nil || closeQty.Sign() <= 0 {
		return nil
	}
	if new(big.Int).Add(releasedMargin, pnl).Sign() >= 0 { // safe-math-ok: 有符号平仓权益比较，负值明确拒绝普通平仓
		return nil
	}
	return fmt.Errorf("bankrupt close must use liquidation path: pos=%s owner=%s pair=%s side=%d entry=%s tradePrice=%s closeQty=%s releasedMargin=%s pnl=%s",
		pos.ID, pos.Owner, pos.Pair, pos.Side, pos.EntryPrice, tradePrice, closeQty, releasedMargin, pnl)
}

                                            
func (e *Engine) reducePosition(
	opposite *Position,
	tradeQty, tradePrice, frozenMargin *big.Int,
	cfg *MarketConfig,
	height uint64,
	owner, pair string,
	side Side,
) error {
	cmp := tradeQty.Cmp(opposite.Quantity)

	if cmp <= 0 {
		                        
		pnl, nextRemainder := previewIncrementalClosePnL(opposite, tradeQty, tradePrice)

		                                                                          
		                                                 
		releasedMargin := previewMarginRelease(opposite, tradeQty)

		                                                                                              
		                                                                                              
		                                                                                                 
		                                                    
		if err := validateSolventClose(opposite, tradeQty, tradePrice, pnl, releasedMargin); err != nil {
			return err
		}
		actualPnL, err := e.settleRealizedPnL(opposite, cfg.QuoteToken, pnl)
		if err != nil {
			return err
		}
		                                        
		totalReturn := new(big.Int).Add(releasedMargin, actualPnL) // safe-math-ok: 允许负的已实现盈亏；仅正返还额经 BalanceAccessor 入账
		if totalReturn.Sign() > 0 {
			if err := e.balance.AddBalance(owner, cfg.QuoteToken, totalReturn); err != nil {
				return fmt.Errorf("return close margin and PnL: %w", err)
			}
		}
		                                                   
		if frozenMargin.Sign() > 0 {
			if err := e.balance.AddBalance(owner, cfg.QuoteToken, frozenMargin); err != nil {
				return fmt.Errorf("refund unused close margin: %w", err)
			}
		}

		e.accumulateClose(opposite, tradeQty, tradePrice, actualPnL)
		reduceEntryNotional(opposite, tradeQty)
		advanceMarginBasis(opposite, tradeQty)

		if cmp == 0 {
			               
			e.removeLiqIndex(opposite)
			opposite.Status = StatusClosed
			opposite.Quantity = new(big.Int)
			opposite.Margin = new(big.Int)
			clearMarginBasis(opposite)
			opposite.ClosePnLRemainder = new(big.Int)
			opposite.CloseHeight = height
		} else {
			               
			opposite.Quantity = new(big.Int).Sub(opposite.Quantity, tradeQty) // safe-math-ok: 仅 tradeQty < opposite.Quantity 的部分平仓分支执行
			opposite.Margin = new(big.Int).Sub(opposite.Margin, releasedMargin) // safe-math-ok: 释放量由 previewMarginRelease 限制在已有保证金内
			opposite.ClosePnLRemainder = nextRemainder
		}
		if err := e.setPosition(opposite); err != nil {
			return err
		}
		if cmp < 0 {
			e.updateLiqIndex(opposite, cfg)
		}
		return nil
	}

	                                                                  
	oldQty := new(big.Int).Set(opposite.Quantity)                                           
	remainQty := new(big.Int).Sub(tradeQty, oldQty) // safe-math-ok: 仅 tradeQty > opposite.Quantity 的反向开仓分支执行
	                                                                                           
	                                                               
	remainMargin := MulDivCeil(frozenMargin, remainQty, tradeQty)
	minimumMargin := CalcRequiredMargin(remainQty, tradePrice, cfg.MaxLeverageOrDefault())
	if remainMargin.Sign() > 0 && remainMargin.Cmp(minimumMargin) < 0 {
		return fmt.Errorf("refuse reverse position: margin %s below required %s", remainMargin, minimumMargin)
	}
	                                                                          
	                                                                        
	lastCum := new(big.Int)
	if remainMargin.Sign() > 0 && e.funding != nil {
		cumL, cumS, err := e.funding.GetCumulativeFunding(pair)
		if err != nil {
			return fmt.Errorf("snapshot cumulative funding for reverse position %s/%s: %w", owner, pair, err)
		}
		if side == Long {
			lastCum = cumL
		} else {
			lastCum = cumS
		}
	}
	var newPositionID string
	if remainMargin.Sign() > 0 {
		var err error
		newPositionID, err = e.positions.AllocatePositionID(owner, pair, side, height)
		if err != nil {
			return fmt.Errorf("allocate reverse future position ID: %w", err)
		}
	}
	pnl, _ := previewIncrementalClosePnL(opposite, oldQty, tradePrice)
	                                                                                                  
	if err := validateSolventClose(opposite, oldQty, tradePrice, pnl, opposite.Margin); err != nil {
		return err
	}
	actualPnL, err := e.settleRealizedPnL(opposite, cfg.QuoteToken, pnl)
	if err != nil {
		return err
	}
	totalReturn := new(big.Int).Add(opposite.Margin, actualPnL) // safe-math-ok: 有符号平仓盈亏与保证金合计，仅正值经 BalanceAccessor 入账
	if totalReturn.Sign() > 0 {
		if err := e.balance.AddBalance(owner, cfg.QuoteToken, totalReturn); err != nil {
			return fmt.Errorf("return reverse close margin and PnL: %w", err)
		}
	}

	e.accumulateClose(opposite, oldQty, tradePrice, actualPnL)
	reduceEntryNotional(opposite, oldQty)
	e.removeLiqIndex(opposite)
	opposite.Status = StatusClosed
	opposite.Quantity = new(big.Int)
	opposite.Margin = new(big.Int)
	clearMarginBasis(opposite)
	opposite.ClosePnLRemainder = new(big.Int)
	opposite.CloseHeight = height
	if err := e.setPosition(opposite); err != nil {
		return err
	}

	                                                             
	                                                                                          
	                                                                                                          
	                                                                                           
	refund := new(big.Int).Sub(frozenMargin, remainMargin) // safe-math-ok: remainMargin 按剩余数量比例分摊，正退款另经 BalanceAccessor 校验
	if refund.Sign() > 0 {
		if err := e.balance.AddBalance(owner, cfg.QuoteToken, refund); err != nil {
			return fmt.Errorf("refund reverse order margin: %w", err)
		}
	}

	                                                                                                
	                                                                                           
	if remainMargin.Sign() <= 0 {
		return nil
	}

	                                                                                                    
	newPos := &Position{
		ID:                     newPositionID,
		Owner:                  owner,
		Pair:                   pair,
		Side:                   side,
		EntryPrice:             new(big.Int).Set(tradePrice),
		EntryNotionalNumerator: new(big.Int).Mul(tradePrice, remainQty), // safe-math-ok: 反向新仓的精确名义价值分子，不是余额增发
		Quantity:               remainQty,
		Margin:                 remainMargin,
		MarginBasisAmount:      new(big.Int).Set(remainMargin),
		MarginBasisClosed:      new(big.Int),
		Status:                 StatusOpen,
		OpenHeight:             height,
		LastCumulativeFunding:  lastCum,
		FirstCumulativeFunding: new(big.Int).Set(lastCum),
		SettledFunding:         new(big.Int),
		LastSettledFunding:     new(big.Int),
		RealizedPnL:            new(big.Int),
		ClosedQty:              new(big.Int),
		ExitNotional:           new(big.Int),
		ClosePnLRemainder:      new(big.Int),
		MaxQuantity:            new(big.Int).Set(remainQty),
		InitialMargin:          new(big.Int).Set(remainMargin),
	}
	if err := e.setPosition(newPos); err != nil {
		return err
	}
	e.updateLiqIndex(newPos, cfg)
	return nil
}

                            
func (e *Engine) AddMargin(positionID string, amount *big.Int) error {
	pos, err := e.positions.GetPosition(positionID)
	if err != nil {
		return err
	}
	if pos == nil || pos.Status != StatusOpen {
		return fmt.Errorf("position not found or not open: %s", positionID)
	}
	cfg, err := e.GetMarketConfig(pos.Pair)
	if err != nil {
		return err
	}
	if err := e.ApplyPendingFunding(pos, cfg); err != nil {
		return err
	}
	if pos.Status != StatusOpen {
		return fmt.Errorf("cannot add margin to position %s: pending funding made it terminal", positionID)
	}
	if err := e.balance.DeductBalance(pos.Owner, cfg.QuoteToken, amount); err != nil {
		return fmt.Errorf("insufficient balance to add margin: %w", err)
	}
	pos.Margin = new(big.Int).Add(pos.Margin, amount) // safe-math-ok: 前一步 DeductBalance 已成功扣款，保留仓位现有累计域
	resetMarginBasis(pos)
	if err := e.setPosition(pos); err != nil {
		return err
	}
	e.updateLiqIndex(pos, cfg)
	return nil
}

                               
func (e *Engine) RemoveMargin(positionID string, amount *big.Int) error {
	pos, err := e.positions.GetPosition(positionID)
	if err != nil {
		return err
	}
	if pos == nil || pos.Status != StatusOpen {
		return fmt.Errorf("position not found or not open: %s", positionID)
	}
	cfg, err := e.GetMarketConfig(pos.Pair)
	if err != nil {
		return err
	}
	if err := e.ApplyPendingFunding(pos, cfg); err != nil {
		return err
	}
	if pos.Status != StatusOpen {
		return fmt.Errorf("cannot remove margin from position %s: pending funding made it terminal", positionID)
	}

	newMargin := new(big.Int).Sub(pos.Margin, amount) // safe-math-ok: 下一步拒绝非正保证金并复验清算健康度后才写回
	if newMargin.Sign() <= 0 {
		return fmt.Errorf("cannot remove margin: would result in non-positive margin")
	}

	markPrice, err := e.markPrice.GetMarkPrice(pos.Pair)
	if err != nil {
		return err
	}
	if ShouldLiquidatePosition(pos, newMargin, markPrice, cfg.MaintenanceMarginRateOrDefault()) {
		return fmt.Errorf("cannot remove margin: would trigger liquidation")
	}

	pos.Margin = newMargin
	resetMarginBasis(pos)
	if err := e.setPosition(pos); err != nil {
		return err
	}
	e.updateLiqIndex(pos, cfg)
	return e.balance.AddBalance(pos.Owner, cfg.QuoteToken, amount)
}

                                                                         
func (e *Engine) CheckAndProcessLiquidations(pair string) ([]LiquidationResult, []ADLResult, error) {
	cfg, err := e.GetMarketConfig(pair)
	if err != nil {
		return nil, nil, err
	}
	markPrice, err := e.markPrice.GetMarkPrice(pair)
	if err != nil {
		return nil, nil, err
	}

	                                                                         
	                                                                          
	                                                                  
	atRiskIDs, err := e.FindAtRiskPositions(pair, markPrice)
	if err != nil {
		return nil, nil, fmt.Errorf("collect liquidation candidates for %s: %w", pair, err)
	}

	var liqResults []LiquidationResult
	var adlResults []ADLResult

	for _, posID := range atRiskIDs {
		pos, err := e.positions.GetPosition(posID)
		if err != nil {
			return liqResults, adlResults, fmt.Errorf("read liquidation candidate %s: %w", posID, err)
		}
		if pos == nil || pos.Status != StatusOpen {
			continue
		}

		                                                                              
		                                                                                   
		pending, err := e.PreviewPendingFundingStrict(pos)
		if err != nil {
			return liqResults, adlResults, fmt.Errorf("preview liquidation funding for %s: %w", posID, err)
		}
		projectedMargin := new(big.Int).Add(pos.Margin, pending) // safe-math-ok: 只读清算预览的资金费可为负，随后将负值归零
		if projectedMargin.Sign() < 0 {
			projectedMargin = new(big.Int)                                          
		}

		if !ShouldLiquidatePosition(pos, projectedMargin, markPrice, cfg.MaintenanceMarginRateOrDefault()) {
			                                                                                  
			e.updateLiqIndex(pos, cfg)
			continue
		}

		                                                                                        
		                                                                                 
		                                                                                   
		if err := e.ApplyPendingFunding(pos, cfg); err != nil {
			return liqResults, adlResults, err
		}
		if pos.Status != StatusOpen {
			continue
		}

		liqResult, err := e.processLiquidation(pos, markPrice, cfg)
		if err != nil {
			return liqResults, adlResults, err
		}
		liqResults = append(liqResults, *liqResult)

		adlResult, err := e.handleLiquidationResult(pair, liqResult)
		if err != nil {
			return liqResults, adlResults, err
		}
		if adlResult != nil {
			adlResults = append(adlResults, *adlResult)
		}
	}

	return liqResults, adlResults, nil
}

                                                                                      
                                                                                                  
                                                                               
                                                                                                        
func (e *Engine) LiquidateAtPrice(pos *Position, closePrice *big.Int, cfg *MarketConfig) (*LiquidationResult, error) {
	return e.processLiquidation(pos, closePrice, cfg)
}

                                                                              
                                                                                                        
func (e *Engine) RefreshLiquidationIndex(pos *Position, cfg *MarketConfig) {
	e.updateLiqIndex(pos, cfg)
}

                                                                                                      
  
                                                                                                         
            
                                                                                                  
                                                                                                   
                                                                   
                                                                                                  
  
                                                                                                                
                                                                                                                           
func (e *Engine) LiquidatePartial(pos *Position, closePrice, qty *big.Int, cfg *MarketConfig) (*LiquidationResult, error) {
	if qty == nil || qty.Sign() <= 0 {
		return nil, fmt.Errorf("LiquidatePartial: qty must be positive")
	}
	if qty.Cmp(pos.Quantity) >= 0 {
		return e.processLiquidation(pos, closePrice, cfg)
	}

	bankruptPrice := positionConservativeLiquidationPrice(pos, pos.Margin, 0)
	liqPrice := positionConservativeLiquidationPrice(pos, pos.Margin, cfg.MaintenanceMarginRateOrDefault())

	portionMargin := previewMarginRelease(pos, qty)
	segmentPnL, nextRemainder := previewIncrementalClosePnL(pos, qty, closePrice)
	segmentEquity := new(big.Int).Add(portionMargin, segmentPnL) // safe-math-ok: 有符号部分清算权益用于记录穿仓风险，不直接扣保险余额

	result := &LiquidationResult{
		PositionID:     pos.ID,
		Owner:          pos.Owner,
		Side:           pos.Side,
		Quantity:       new(big.Int).Set(qty),
		LiqPrice:       liqPrice,
		BankruptPrice:  bankruptPrice,
		ClosePrice:     new(big.Int).Set(closePrice),
		MarginLost:     new(big.Int).Set(portionMargin),
		InsuranceDelta: new(big.Int).Set(portionMargin),
	}

	if err := e.DepositInsuranceFund(pos.Pair, portionMargin); err != nil {
		return nil, err
	}
	if segmentEquity.Sign() < 0 {
		result.NeedsADL = true
		result.ADLShortfall = new(big.Int).Neg(segmentEquity)
	}

	                                                                                                                                   
	lossForBook := new(big.Int).Neg(portionMargin)
	e.accumulateClose(pos, qty, closePrice, lossForBook)
	reduceEntryNotional(pos, qty)
	advanceMarginBasis(pos, qty)

	pos.Quantity = new(big.Int).Sub(pos.Quantity, qty) // safe-math-ok: 只在 qty < pos.Quantity 的部分清算分支执行
	pos.Margin = new(big.Int).Sub(pos.Margin, portionMargin) // safe-math-ok: previewMarginRelease 将核销保证金限制在原保证金内

	                                                                                                                                    
	if pos.Margin.Sign() <= 0 {
		                                                                                                                                     
		if pos.Quantity.Sign() > 0 {
			e.accumulateClose(pos, pos.Quantity, closePrice, new(big.Int))
			reduceEntryNotional(pos, pos.Quantity)
		}
		e.removeLiqIndex(pos)
		pos.Status = StatusLiquidated
		pos.Quantity = new(big.Int)
		pos.Margin = new(big.Int)
		clearMarginBasis(pos)
		pos.ClosePnLRemainder = new(big.Int)
		pos.CloseHeight = e.currentHeight
	} else {
		pos.ClosePnLRemainder = nextRemainder
		e.updateLiqIndex(pos, cfg)
	}

	if err := e.setPosition(pos); err != nil {
		return nil, err
	}
	return result, nil
}

                                                                                       
                                                                                               
                                                                                                   
func (e *Engine) FindAtRiskPositions(pair string, markPrice *big.Int) ([]string, error) {
	if e.liqIndex == nil {
		positions, err := e.positions.GetAllOpenPositions(pair)
		if err != nil {
			return nil, fmt.Errorf("scan open positions: %w", err)
		}
		out := make([]string, 0, len(positions))
		for _, p := range positions {
			out = append(out, p.ID)
		}
		return out, nil
	}
	longs, err := e.liqIndex.FindLongsAtRisk(pair, markPrice)
	if err != nil {
		return nil, fmt.Errorf("scan long liquidation index: %w", err)
	}
	shorts, err := e.liqIndex.FindShortsAtRisk(pair, markPrice)
	if err != nil {
		return nil, fmt.Errorf("scan short liquidation index: %w", err)
	}
	return append(longs, shorts...), nil
}

                                                 
func (e *Engine) processLiquidation(pos *Position, markPrice *big.Int, cfg *MarketConfig) (*LiquidationResult, error) {
	bankruptPrice := positionConservativeLiquidationPrice(pos, pos.Margin, 0)
	liqPrice := positionConservativeLiquidationPrice(pos, pos.Margin, cfg.MaintenanceMarginRateOrDefault())

	                                                                                                                
	closePnL, _ := previewIncrementalClosePnL(pos, pos.Quantity, markPrice)
	closeEquity := new(big.Int).Add(pos.Margin, closePnL) // safe-math-ok: 有符号整仓清算权益用于决定 ADL 缺口，不直接生成资金

	result := &LiquidationResult{
		PositionID:     pos.ID,
		Owner:          pos.Owner,
		Side:           pos.Side,
		Quantity:       new(big.Int).Set(pos.Quantity),
		LiqPrice:       liqPrice,
		BankruptPrice:  bankruptPrice,
		ClosePrice:     new(big.Int).Set(markPrice),
		MarginLost:     new(big.Int).Set(pos.Margin),
		InsuranceDelta: new(big.Int).Set(pos.Margin),
	}

	if err := e.DepositInsuranceFund(pos.Pair, pos.Margin); err != nil {
		return nil, err
	}
	if closeEquity.Sign() < 0 {
		result.NeedsADL = true
		result.ADLShortfall = new(big.Int).Neg(closeEquity)
	}

	                                                                                         
	lossForBook := new(big.Int).Neg(pos.Margin)
	e.accumulateClose(pos, pos.Quantity, markPrice, lossForBook)
	reduceEntryNotional(pos, pos.Quantity)

	                        
	e.removeLiqIndex(pos)
	pos.Status = StatusLiquidated
	pos.Quantity = new(big.Int)
	pos.Margin = new(big.Int)
	clearMarginBasis(pos)
	pos.ClosePnLRemainder = new(big.Int)
	pos.CloseHeight = e.currentHeight
	if err := e.setPosition(pos); err != nil {
		return nil, err
	}

	return result, nil
}

                                             
func (e *Engine) ValidateOpenOrder(pair string, quantity, price *big.Int, leverage uint64) error {
	cfg, err := e.GetMarketConfig(pair)
	if err != nil {
		return err
	}
	if leverage == 0 {
		return fmt.Errorf("leverage must be positive")
	}
	notional := CalcNotional(quantity, price)
	if notional.Sign() <= 0 {
		return fmt.Errorf("notional must be positive")
	}
	requiredMargin := CalcRequiredMargin(quantity, price, leverage)
	if requiredMargin.Sign() <= 0 {
		return fmt.Errorf("required margin must be positive")
	}
	if leverage > cfg.MaxLeverageOrDefault() {
		return fmt.Errorf("leverage %d exceeds max %d", leverage, cfg.MaxLeverageOrDefault())
	}
	return nil
}

                                                                                                                                           
func (e *Engine) SettleFundingRate(pair string, fundingRateBps int64) error {
	if fundingRateBps == 0 || e.funding == nil {
		return nil
	}
	cumLong, cumShort, err := e.funding.GetCumulativeFunding(pair)
	if err != nil {
		return err
	}
	newCumLong, newCumShort := UpdateCumulativeFunding(cumLong, cumShort, fundingRateBps)
	if err := e.funding.SetCumulativeFunding(pair, newCumLong, newCumShort); err != nil {
		return err
	}
	if e.pendingFundingPayerCache != nil {
		delete(e.pendingFundingPayerCache, pair)
	}
	return nil
}

                                                      
func (e *Engine) updateLiqIndex(pos *Position, cfg *MarketConfig) {
	if e.liqIndex == nil || pos.Status != StatusOpen {
		return
	}
	liqPrice := positionConservativeLiquidationPrice(pos, pos.Margin, cfg.MaintenanceMarginRateOrDefault())
	if liqPrice.Sign() <= 0 {
		                                                                       
		                                                                      
		                                                                         
		                                                                    
		e.removeLiqIndex(pos)
		return
	}
	_ = e.liqIndex.Upsert(pos.ID, pos.Pair, pos.Side, liqPrice)
}

                                          
func (e *Engine) removeLiqIndex(pos *Position) {
	if e.liqIndex == nil {
		return
	}
	                                                   
	_ = e.liqIndex.Remove(pos.ID, pos.Pair, pos.Side, nil)
}
