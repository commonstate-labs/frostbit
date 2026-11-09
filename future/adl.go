package future

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

                                                                                        
                                                                                                         
const ADLMaxProfitWithholdBps int64 = 8000

                                                                                   
                                                                                                 
func RankADLCandidates(positions []*Position, markPrice *big.Int, targetSide Side) []ADLCandidate {
	var candidates []ADLCandidate
	for _, pos := range positions {
		if pos.Status != StatusOpen || pos.Side != targetSide {
			continue
		}
		pnl := adlAvailablePnL(pos, markPrice, pos.Quantity)
		if pnl.Sign() <= 0 {
			continue
		}
		notional := CalcNotional(pos.Quantity, markPrice)
		candidates = append(candidates, ADLCandidate{
			PositionID: pos.ID,
			Owner:      pos.Owner,
			Score:      CalcADLScore(pnl, notional, pos.Margin),
			Quantity:   new(big.Int).Set(pos.Quantity),
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		cmp := candidates[i].Score.Cmp(candidates[j].Score)
		if cmp != 0 {
			return cmp > 0
		}
		return candidates[i].PositionID < candidates[j].PositionID
	})
	return candidates
}

                                                                                              
                                                                                    
func (e *Engine) ExecuteADL(pair string, targetSide Side, shortfall *big.Int) (*ADLResult, error) {
	if shortfall == nil || shortfall.Sign() <= 0 {
		return nil, nil
	}
	if e.insurance == nil {
		return nil, fmt.Errorf("execute ADL for %s: insurance store is nil", pair)
	}
	positions, err := e.positions.GetAllOpenPositions(pair)
	if err != nil {
		return nil, err
	}
	hasTarget := false
	for _, pos := range positions {
		if pos != nil && pos.Status == StatusOpen && pos.Side == targetSide && pos.Quantity != nil && pos.Quantity.Sign() > 0 {
			hasTarget = true
			break
		}
	}
	if !hasTarget {
		if err := e.recordADLShortfall(pair, shortfall); err != nil {
			return nil, err
		}
		return nil, nil
	}
	markPrice, err := e.markPrice.GetMarkPrice(pair)
	if err != nil {
		return nil, err
	}
	cfg, err := e.GetMarketConfig(pair)
	if err != nil {
		return nil, err
	}

	candidates := RankADLCandidates(positions, markPrice, targetSide)
	if len(candidates) == 0 {
		if err := e.recordADLShortfall(pair, shortfall); err != nil {
			return nil, err
		}
		return nil, nil
	}

	insuranceBalance, err := e.insurance.GetBalance(pair)
	if err != nil {
		return nil, fmt.Errorf("load ADL insurance balance for %s: %w", pair, err)
	}
	if insuranceBalance == nil || insuranceBalance.Sign() < 0 {
		insuranceBalance = new(big.Int)
	}

	                                                                             
	                                                                         
	                                                                     
	remaining := new(big.Int).Set(shortfall)
	insuranceBudget := new(big.Int).Set(insuranceBalance)
	plans := make([]adlReductionPlan, 0, len(candidates))
	for _, cand := range candidates {
		if remaining.Sign() <= 0 {
			break
		}
		pos, err := e.positions.GetPosition(cand.PositionID)
		if err != nil {
			return nil, fmt.Errorf("load ADL candidate %s: %w", cand.PositionID, err)
		}
		if pos == nil || pos.Status != StatusOpen {
			continue
		}
		reduceQty := adlQuantityWithinResources(pos, markPrice, remaining, insuranceBudget)
		if reduceQty.Sign() <= 0 {
			continue
		}

		pnl, _ := previewIncrementalClosePnL(pos, reduceQty, markPrice)
		withheldProfit, paidProfit := adlProfitSplit(pnl)
		if pnl.Sign() <= 0 {
			continue
		}
		plans = append(plans, adlReductionPlan{
			candidate:      cand,
			before:         clonePositionForUpdate(pos),
			reduceQty:      reduceQty,
			grossProfit:    pnl,
			withheldProfit: withheldProfit,
			paidProfit:     paidProfit,
		})
		remaining.Sub(remaining, pnl)
		insuranceBudget.Sub(insuranceBudget, paidProfit)
	}

	                                                                       
	                                                                           
	for i := range plans {
		if insuranceBudget.Sign() <= 0 {
			break
		}
		extraPaid := new(big.Int).Set(plans[i].withheldProfit)
		if extraPaid.Cmp(insuranceBudget) > 0 {
			extraPaid.Set(insuranceBudget)
		}
		plans[i].paidProfit.Add(plans[i].paidProfit, extraPaid)
		plans[i].withheldProfit.Sub(plans[i].withheldProfit, extraPaid)
		insuranceBudget.Sub(insuranceBudget, extraPaid)
	}

	result := &ADLResult{TotalCovered: new(big.Int)}
	var applied []adlAppliedChange
	for _, plan := range plans {
		pos := plan.before
		reduceQty := plan.reduceQty
		pnl := plan.grossProfit
		withheldProfit := plan.withheldProfit
		paidProfit := plan.paidProfit
		_, nextRemainder := previewIncrementalClosePnL(pos, reduceQty, markPrice)
		releasedMargin := previewMarginRelease(pos, reduceQty)

		before := clonePositionForUpdate(pos)
		next := clonePositionForUpdate(pos)
		if paidProfit.Sign() > 0 {
			consumed, unpaid, err := e.WithdrawInsuranceFund(pair, paidProfit)
			if err != nil {
				return nil, e.rollbackADL(applied, cfg, fmt.Errorf("pay retained ADL profit for %s: %w", next.ID, err))
			}
			if unpaid.Sign() > 0 || consumed.Cmp(paidProfit) != 0 {
				cause := fmt.Errorf("pay retained ADL profit for %s: insurance changed during settlement", next.ID)
				if rerr := e.restoreADLInsurance(pair, consumed); rerr != nil {
					cause = errors.Join(cause, rerr)
				}
				return nil, e.rollbackADL(applied, cfg, cause)
			}
		}
		balanceCredit := new(big.Int).Add(releasedMargin, paidProfit) // safe-math-ok: 利润先从保险池实际扣款，入账仍经 BalanceAccessor 校验
		if balanceCredit.Sign() > 0 {
			if err := e.balance.AddBalance(next.Owner, cfg.QuoteToken, balanceCredit); err != nil {
				cause := fmt.Errorf("credit ADL margin/profit for %s: %w", next.ID, err)
				if rerr := e.restoreADLInsurance(pair, paidProfit); rerr != nil {
					cause = errors.Join(cause, rerr)
				}
				return nil, e.rollbackADL(applied, cfg, cause)
			}
		}

		e.accumulateClose(next, reduceQty, markPrice, paidProfit)
		next.ADLWithheldPnL = biAdd(next.ADLWithheldPnL, withheldProfit)
		reduceEntryNotional(next, reduceQty)
		advanceMarginBasis(next, reduceQty)

		if reduceQty.Cmp(next.Quantity) >= 0 {
			next.Status = StatusInsuredDeleveraged
			if withheldProfit.Sign() > 0 {
				next.Status = StatusADLClosed
			}
			next.Quantity = new(big.Int)
			next.Margin = new(big.Int)
			clearMarginBasis(next)
			next.ClosePnLRemainder = new(big.Int)
			next.CloseHeight = e.currentHeight
		} else {
			next.Quantity = new(big.Int).Sub(next.Quantity, reduceQty) // safe-math-ok: 仅 reduceQty < next.Quantity 分支执行部分减仓
			next.Margin = new(big.Int).Sub(next.Margin, releasedMargin) // safe-math-ok: previewMarginRelease 将释放量限制在已有保证金内
			next.ClosePnLRemainder = nextRemainder
		}
		if err := e.setPosition(next); err != nil {
			cause := fmt.Errorf("persist ADL candidate %s: %w", next.ID, err)
			if balanceCredit.Sign() > 0 {
				if rerr := e.balance.DeductBalance(next.Owner, cfg.QuoteToken, balanceCredit); rerr != nil {
					cause = errors.Join(cause, fmt.Errorf("revert ADL balance credit for %s: %w", next.ID, rerr))
				}
			}
			if rerr := e.restoreADLInsurance(pair, paidProfit); rerr != nil {
				cause = errors.Join(cause, rerr)
			}
			return nil, e.rollbackADL(applied, cfg, cause)
		}
		if next.Status != StatusOpen {
			e.removeLiqIndex(next)
		} else {
			e.updateLiqIndex(next, cfg)
		}
		applied = append(applied, adlAppliedChange{
			before: before, balanceCredit: new(big.Int).Set(balanceCredit), insurancePaid: new(big.Int).Set(paidProfit),
		})

		result.TotalCovered.Add(result.TotalCovered, pnl)
		result.Reduced = append(result.Reduced, ADLReducedPosition{
			PositionID:      plan.candidate.PositionID,
			Owner:           plan.candidate.Owner,
			ReducedQuantity: reduceQty,
			SettlePrice:     new(big.Int).Set(markPrice),
			GrossProfit:     new(big.Int).Set(pnl),
			WithheldProfit:  new(big.Int).Set(withheldProfit),
			PaidProfit:      new(big.Int).Set(paidProfit),
		})
	}

	if remaining.Sign() > 0 {
		if err := e.recordADLShortfall(pair, remaining); err != nil {
			return nil, e.rollbackADL(applied, cfg, err)
		}
	}
	return result, nil
}

type adlReductionPlan struct {
	candidate      ADLCandidate
	before         *Position
	reduceQty      *big.Int
	grossProfit    *big.Int
	withheldProfit *big.Int
	paidProfit     *big.Int
}

type adlAppliedChange struct {
	before        *Position
	balanceCredit *big.Int
	insurancePaid *big.Int
}

                                                                            
                                                                          
                                                                           
                                                                  
func (e *Engine) rollbackADL(applied []adlAppliedChange, cfg *MarketConfig, cause error) error {
	err := cause
	for i := len(applied) - 1; i >= 0; i-- {
		change := applied[i]
		if change.before == nil {
			continue
		}
		if rerr := e.positions.SetPosition(clonePositionForUpdate(change.before)); rerr != nil {
			err = errors.Join(err, fmt.Errorf("restore ADL position %s: %w", change.before.ID, rerr))
		} else {
			e.updateLiqIndex(change.before, cfg)
		}
		if change.balanceCredit != nil && change.balanceCredit.Sign() > 0 {
			if rerr := e.balance.DeductBalance(change.before.Owner, cfg.QuoteToken, change.balanceCredit); rerr != nil {
				err = errors.Join(err, fmt.Errorf("restore ADL balance for %s: %w", change.before.ID, rerr))
			}
		}
		if rerr := e.restoreADLInsurance(change.before.Pair, change.insurancePaid); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}
	return err
}

func (e *Engine) restoreADLInsurance(pair string, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return nil
	}
	current, err := e.insurance.GetBalance(pair)
	if err != nil {
		return fmt.Errorf("restore ADL insurance for %s: %w", pair, err)
	}
	if current == nil {
		current = new(big.Int)
	}
	if err := e.insurance.SetBalance(pair, new(big.Int).Add(current, amount)); err != nil { // safe-math-ok: 补偿返还先前实际扣除的保险额，保持原资金池精度
		return fmt.Errorf("restore ADL insurance for %s: %w", pair, err)
	}
	return nil
}

func adlAvailablePnL(pos *Position, markPrice, quantity *big.Int) *big.Int {
	pnl, _ := previewIncrementalClosePnL(pos, quantity, markPrice)
	return pnl
}

func adlProfitSplit(pnl *big.Int) (withheld, paid *big.Int) {
	withheld, paid = new(big.Int), new(big.Int)
	if pnl == nil || pnl.Sign() <= 0 {
		return withheld, paid
	}
	withheld.Mul(pnl, big.NewInt(ADLMaxProfitWithholdBps))
	withheld.Quo(withheld, big.NewInt(int64(BPS)))
	paid.Sub(pnl, withheld)
	return withheld, paid
}

                                                                          
                                                                             
                                                                           
func adlQuantityWithinResources(pos *Position, markPrice, remaining, insurance *big.Int) *big.Int {
	if pos == nil || pos.Quantity == nil || pos.Quantity.Sign() <= 0 || remaining == nil || remaining.Sign() <= 0 {
		return new(big.Int)
	}
	if insurance == nil || insurance.Sign() < 0 {
		insurance = new(big.Int)
	}

	                                                                              
	                                                                           
	                                                          
	low := big.NewInt(1)
	high := new(big.Int).Set(pos.Quantity)
	one := big.NewInt(1)
	maxFeasible := new(big.Int)
	for low.Cmp(high) <= 0 {
		mid := new(big.Int).Add(low, high) // safe-math-ok: 二分数量搜索的中点分子，随后右移除以二
		mid.Rsh(mid, 1)
		pnl := adlAvailablePnL(pos, markPrice, mid)
		if pnl.Sign() <= 0 {
			low.Add(mid, one)
			continue
		}
		_, paid := adlProfitSplit(pnl)
		if pnl.Cmp(remaining) <= 0 && paid.Cmp(insurance) <= 0 {
			maxFeasible.Set(mid)
			low.Add(mid, one)
		} else {
			high.Sub(mid, one)
		}
	}
	if maxFeasible.Sign() <= 0 {
		return new(big.Int)
	}
	return maxFeasible
}

func (e *Engine) recordADLShortfall(pair string, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return nil
	}
	if e.insurance == nil {
		return fmt.Errorf("record ADL shortfall for %s: insurance store is nil", pair)
	}
	if err := e.insurance.AddShortfall(pair, amount); err != nil {
		return fmt.Errorf("record ADL shortfall for %s: %w", pair, err)
	}
	return nil
}
