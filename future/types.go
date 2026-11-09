package future

import (
	"fmt"
	"math/big"
)

                                                                
func biAdd(a, b *big.Int) *big.Int {
	r := new(big.Int)
	if a != nil {
		r.Add(r, a)
	}
	if b != nil {
		r.Add(r, b)
	}
	return r
}

                                          
func biMax(a, cur *big.Int) *big.Int {
	if a == nil {
		return new(big.Int).Set(cur)
	}
	if cur != nil && cur.Cmp(a) > 0 {
		return new(big.Int).Set(cur)
	}
	return new(big.Int).Set(a)
}

                                                                        
                                                                      
                                                                   
                                                 
func positionEntryNotionalNumerator(pos *Position) *big.Int {
	if pos == nil {
		return new(big.Int)
	}
	if pos.EntryNotionalNumerator != nil && pos.EntryNotionalNumerator.Sign() > 0 {
		return new(big.Int).Set(pos.EntryNotionalNumerator)
	}
	if pos.EntryPrice == nil || pos.Quantity == nil || pos.EntryPrice.Sign() <= 0 || pos.Quantity.Sign() <= 0 {
		return new(big.Int)
	}
	return new(big.Int).Mul(pos.EntryPrice, pos.Quantity) // safe-math-ok: 重建价格乘数量的精确成本分子，不是 quote 余额。
}

                                                                             
                                                                             
                                                                               
                                                                                 
                                                                           
                                                                                 
                                                                             
func entryNotionalPortion(pos *Position, closeQty *big.Int) *big.Int {
	if pos == nil || closeQty == nil || closeQty.Sign() <= 0 {
		return new(big.Int)
	}
	if pos.Quantity == nil || pos.Quantity.Sign() <= 0 {
		if pos.EntryPrice == nil || pos.EntryPrice.Sign() <= 0 {
			return new(big.Int)
		}
		return new(big.Int).Mul(pos.EntryPrice, closeQty) // safe-math-ok: 旧仓成本缺失时重建本次关闭的精确成本分子。
	}
	total := positionEntryNotionalNumerator(pos)
	if closeQty.Cmp(pos.Quantity) >= 0 {
		return total
	}
	base, highCostUnits := new(big.Int), new(big.Int)
	base.QuoRem(total, pos.Quantity, highCostUnits)
	portion := new(big.Int).Mul(base, closeQty) // safe-math-ok: closeQty 小于总数量，成本分摊保留整数商乘积和尾差。
	if pos.Side == Long {
		if closeQty.Cmp(highCostUnits) < 0 {
			portion.Add(portion, closeQty)
		} else {
			portion.Add(portion, highCostUnits)
		}
	} else {
		lowCostUnits := new(big.Int).Sub(pos.Quantity, highCostUnits) // safe-math-ok: QuoRem 保证 highCostUnits 小于正总数量。
		if closeQty.Cmp(lowCostUnits) > 0 {
			portion.Add(portion, new(big.Int).Sub(closeQty, lowCostUnits)) // safe-math-ok: 分支已确认 closeQty 大于低成本单位数，补入高成本单位尾差。
		}
	}
	return portion
}

func positionPnLNumerator(pos *Position, quantity, exitPrice *big.Int) *big.Int {
	if pos == nil || quantity == nil || quantity.Sign() <= 0 || exitPrice == nil {
		return new(big.Int)
	}
	qty := new(big.Int).Set(quantity)
	if pos.Quantity != nil && pos.Quantity.Sign() > 0 && qty.Cmp(pos.Quantity) > 0 {
		qty.Set(pos.Quantity)
	}
	exitNotional := new(big.Int).Mul(exitPrice, qty) // safe-math-ok: 退出价格乘数量保留精确分子，供有符号盈亏相减。
	entryNotional := entryNotionalPortion(pos, qty)
	if pos.Side == Long {
		return new(big.Int).Sub(exitNotional, entryNotional) // safe-math-ok: 多头盈亏分子允许为负，不能按余额做无符号减法。
	}
	return new(big.Int).Sub(entryNotional, exitNotional) // safe-math-ok: 空头盈亏分子允许为负，不能按余额做无符号减法。
}

                                                                              
                                                       
                                                                           
func CalcPositionUnrealizedPnL(pos *Position, markPrice, quantity *big.Int) *big.Int {
	numerator := positionPnLNumerator(pos, quantity, markPrice)
	return new(big.Int).Quo(numerator, PriceScale)
}

                                                                             
                                                                                 
func ShouldLiquidatePosition(pos *Position, margin, markPrice *big.Int, mmRateBps uint64) bool {
	if pos == nil || pos.Quantity == nil || pos.Quantity.Sign() <= 0 || margin == nil || markPrice == nil {
		return false
	}
	pnlNumerator := positionPnLNumerator(pos, pos.Quantity, markPrice)
	if pos.ClosePnLRemainder != nil {
		pnlNumerator.Add(pnlNumerator, pos.ClosePnLRemainder)
	}
	pnl := new(big.Int).Quo(pnlNumerator, PriceScale)
	equity := CalcEquity(margin, pnl)
	maintenance := CalcMaintenanceMargin(CalcNotional(pos.Quantity, markPrice), mmRateBps)
	return equity.Cmp(maintenance) <= 0
}

                                                                          
                                                                            
                                                                              
                                                                               
                            
func liquidationIndexEntryPrice(pos *Position) *big.Int {
	if pos == nil || pos.Quantity == nil || pos.Quantity.Sign() <= 0 {
		return new(big.Int)
	}
	total := positionEntryNotionalNumerator(pos)
	if pos.ClosePnLRemainder != nil {
		if pos.Side == Long {
			total.Sub(total, pos.ClosePnLRemainder)
		} else {
			total.Add(total, pos.ClosePnLRemainder)
		}
	}
	if total.Sign() < 0 {
		total.SetInt64(0)
	}
	entry, remainder := new(big.Int), new(big.Int)
	entry.QuoRem(total, pos.Quantity, remainder)
	if pos.Side == Long && remainder.Sign() > 0 {
		entry.Add(entry, big.NewInt(1))
	}
	return entry
}

func maximumLiquidationIndexPrice() *big.Int {
	                                                                        
	                                                                            
	                                                                     
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(33), nil)
}

                                                                               
                                                                                 
  
                                                                              
                                                                                
                  
  
                               
                               
  
                                                                              
                                                                               
                                                                               
                                                                              
                                                                               
func positionConservativeLiquidationPrice(pos *Position, margin *big.Int, mmRateBps uint64) *big.Int {
	if pos == nil || pos.Quantity == nil || pos.Quantity.Sign() <= 0 {
		return new(big.Int)
	}
	if margin == nil {
		margin = new(big.Int)
	}

	effectiveEntry := positionEntryNotionalNumerator(pos)
	if pos.ClosePnLRemainder != nil {
		if pos.Side == Long {
			effectiveEntry.Sub(effectiveEntry, pos.ClosePnLRemainder)
		} else {
			effectiveEntry.Add(effectiveEntry, pos.ClosePnLRemainder)
		}
	}

	bps := new(big.Int).SetUint64(BPS)
	rate := new(big.Int).SetUint64(mmRateBps)
	marginNumerator := new(big.Int).Mul(margin, PriceScale) // safe-math-ok: 清算索引公式的缩放分子需保持精度，最终价格按上限夹紧。
	roundingGuard := new(big.Int).Sub(PriceScale, big.NewInt(1)) // safe-math-ok: 固定 PriceScale 大于一，计算向零截断的最大尾差。
	adjusted := new(big.Int).Set(effectiveEntry)

	var denominator *big.Int
	if pos.Side == Long {
		if mmRateBps >= BPS {
			return maximumLiquidationIndexPrice()
		}
		adjusted.Sub(adjusted, marginNumerator)
		adjusted.Add(adjusted, roundingGuard)
		if adjusted.Sign() <= 0 {
			return new(big.Int)
		}
		denominator = new(big.Int).Mul(pos.Quantity, new(big.Int).Sub(bps, rate)) // safe-math-ok: 分支保证 rate 小于 BPS，正分母保留数量缩放精度。
	} else if pos.Side == Short {
		adjusted.Add(adjusted, marginNumerator)
		adjusted.Sub(adjusted, roundingGuard)
		if adjusted.Sign() <= 0 {
			return big.NewInt(1)
		}
		denominator = new(big.Int).Mul(pos.Quantity, new(big.Int).Add(bps, rate)) // safe-math-ok: 空头清算索引的正分母为数量乘 BPS 与费率之和。
	} else {
		return new(big.Int)
	}

	numerator := new(big.Int).Mul(adjusted, bps) // safe-math-ok: 清算边界先乘后除并按方向取整，返回前夹紧最大索引价格。
	price, remainder := new(big.Int), new(big.Int)
	price.QuoRem(numerator, denominator, remainder)
	if pos.Side == Long && remainder.Sign() > 0 {
		price.Add(price, big.NewInt(1))
	}
	if pos.Side == Short && price.Sign() <= 0 {
		price.SetInt64(1)
	}
	maxPrice := maximumLiquidationIndexPrice()
	if price.Cmp(maxPrice) > 0 {
		return maxPrice
	}
	return price
}

                                                                               
                                                                            
                                
func reduceEntryNotional(pos *Position, closeQty *big.Int) {
	if pos == nil || pos.Quantity == nil || pos.Quantity.Sign() <= 0 || closeQty == nil || closeQty.Sign() <= 0 {
		return
	}
	total := positionEntryNotionalNumerator(pos)
	if closeQty.Cmp(pos.Quantity) >= 0 {
		pos.EntryNotionalNumerator = new(big.Int)
		return
	}
	remainingQty := new(big.Int).Sub(pos.Quantity, closeQty) // safe-math-ok: 全平分支已返回，此处 closeQty 严格小于仓位数量。
	remainingNotional := new(big.Int).Sub(total, entryNotionalPortion(pos, closeQty)) // safe-math-ok: 精确成本分摊不超过总成本，扣除后用于剩余成本基准。
	pos.EntryNotionalNumerator = remainingNotional
	pos.EntryPrice = new(big.Int).Quo(new(big.Int).Set(remainingNotional), remainingQty)
}

func requiredMarginForEntryNotional(entryNotional *big.Int, leverage uint64) *big.Int {
	if entryNotional == nil || entryNotional.Sign() <= 0 || leverage == 0 {
		return new(big.Int)
	}
	denominator := new(big.Int).Mul(PriceScale, new(big.Int).SetUint64(leverage)) // safe-math-ok: 已拒绝零杠杆，此正分母用于精确保证金向上取整。
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(entryNotional, denominator, remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

                                                                           
                                                                            
                                                               
func normalizedMarginBasis(pos *Position) (amount, closed, quantity *big.Int) {
	if pos == nil || pos.Margin == nil || pos.Quantity == nil || pos.Margin.Sign() < 0 || pos.Quantity.Sign() <= 0 {
		return new(big.Int), new(big.Int), new(big.Int)
	}
	if pos.MarginBasisAmount != nil && pos.MarginBasisAmount.Sign() >= 0 {
		basisAmount := new(big.Int).Set(pos.MarginBasisAmount)
		basisClosed := new(big.Int)
		if pos.MarginBasisClosed != nil && pos.MarginBasisClosed.Sign() >= 0 {
			basisClosed.Set(pos.MarginBasisClosed)
		}
		basisQuantity := new(big.Int).Add(pos.Quantity, basisClosed) // safe-math-ok: 重建累计释放的原始数量分母，不增加当前仓位数量。
		if basisQuantity.Sign() > 0 && basisClosed.Cmp(basisQuantity) < 0 {
			alreadyReleased := MulDivFloor(basisAmount, basisClosed, basisQuantity)
			expectedMargin := new(big.Int).Sub(basisAmount, alreadyReleased) // safe-math-ok: basisClosed 小于 basisQuantity，比例释放额不超过 basisAmount。
			if expectedMargin.Cmp(pos.Margin) == 0 {
				return basisAmount, basisClosed, basisQuantity
			}
		}
	}
	return new(big.Int).Set(pos.Margin), new(big.Int), new(big.Int).Set(pos.Quantity)
}

                                                                              
                                                                              
                                                                     
func previewMarginRelease(pos *Position, closeQty *big.Int) *big.Int {
	if pos == nil || pos.Margin == nil || pos.Quantity == nil || pos.Quantity.Sign() <= 0 || closeQty == nil || closeQty.Sign() <= 0 {
		return new(big.Int)
	}
	if closeQty.Cmp(pos.Quantity) >= 0 {
		return new(big.Int).Set(pos.Margin)
	}
	amount, closed, quantity := normalizedMarginBasis(pos)
	before := MulDivFloor(amount, closed, quantity)
	afterClosed := new(big.Int).Add(closed, closeQty) // safe-math-ok: closeQty 小于剩余数量，累计关闭量用于两次比例分配差值。
	after := MulDivFloor(amount, afterClosed, quantity)
	release := new(big.Int).Sub(after, before) // safe-math-ok: 两次累计分配差值随后检查非负并夹紧到当前保证金。
	if release.Sign() < 0 {
		return new(big.Int)
	}
	if release.Cmp(pos.Margin) > 0 {
		return new(big.Int).Set(pos.Margin)
	}
	return release
}

                                                                               
                                                                             
                  
func advanceMarginBasis(pos *Position, closeQty *big.Int) {
	if pos == nil || pos.Quantity == nil || pos.Quantity.Sign() <= 0 || closeQty == nil || closeQty.Sign() <= 0 {
		return
	}
	amount, closed, _ := normalizedMarginBasis(pos)
	qty := new(big.Int).Set(closeQty)
	if qty.Cmp(pos.Quantity) > 0 {
		qty.Set(pos.Quantity)
	}
	pos.MarginBasisAmount = amount
	pos.MarginBasisClosed = new(big.Int).Add(closed, qty) // safe-math-ok: qty 已夹紧到剩余仓位，仅推进累计分配数量基准。
}

func resetMarginBasis(pos *Position) {
	if pos == nil {
		return
	}
	pos.MarginBasisAmount = cloneBigInt(pos.Margin)
	pos.MarginBasisClosed = new(big.Int)
}

func clearMarginBasis(pos *Position) {
	if pos == nil {
		return
	}
	pos.MarginBasisAmount = new(big.Int)
	pos.MarginBasisClosed = new(big.Int)
}

                                                                                                                              
                                                                                                                              
func (e *Engine) accumulateClose(pos *Position, closeQty, closePrice, pnl *big.Int) {
	if pos == nil || closeQty == nil || closeQty.Sign() <= 0 {
		return
	}
	pos.RealizedPnL = biAdd(pos.RealizedPnL, pnl)
	pos.ClosedQty = biAdd(pos.ClosedQty, closeQty)
	                                                                           
	                                                                  
	                                                                         
	pos.ExitNotional = biAdd(pos.ExitNotional, CalcNotional(closeQty, closePrice))
	assertClosedInvariants(pos, closeQty)
}

                                                                      
                                                                            
                                                                             
                                          
  
                                                                             
                                             
  
                                                                 
                                            
  
                                                                              
                                                                            
                                                                            
func previewIncrementalClosePnL(pos *Position, closeQty, closePrice *big.Int) (*big.Int, *big.Int) {
	remainder := new(big.Int)
	if pos != nil && pos.ClosePnLRemainder != nil {
		remainder.Set(pos.ClosePnLRemainder)
	}
	if pos == nil || pos.EntryPrice == nil || closeQty == nil || closeQty.Sign() <= 0 || closePrice == nil {
		return new(big.Int), remainder
	}
	numerator := positionPnLNumerator(pos, closeQty, closePrice)
	numerator.Add(numerator, remainder)
	pnl := new(big.Int)
	nextRemainder := new(big.Int)
	pnl.QuoRem(numerator, PriceScale, nextRemainder)
	return pnl, nextRemainder
}

func incrementalClosePnL(pos *Position, closeQty, closePrice *big.Int) *big.Int {
	pnl, _ := previewIncrementalClosePnL(pos, closeQty, closePrice)
	return pnl
}

                                                                            
  
                                                                                        
                                                                                         
                                                                                                         
                                                                                       
                                              
                                                                                               
                                     
  
                                                                                                            
                                                                               
func assertClosedInvariants(pos *Position, closeQty *big.Int) {
	if pos == nil {
		return
	}
	if pos.ExitNotional != nil && pos.ExitNotional.Sign() < 0 {
		panic(fmt.Sprintf("[future] NEGATIVE EXIT_NOTIONAL pos=%s exitNotional=%s",
			pos.ID, pos.ExitNotional.String()))
	}
	if closeQty != nil && pos.Quantity != nil && closeQty.Cmp(pos.Quantity) > 0 {
		panic(fmt.Sprintf("[future] CLOSE_QTY > POSITION_QTY pos=%s owner=%s closeQty=%s posQty=%s (over-close?)",
			pos.ID, pos.Owner, closeQty.String(), pos.Quantity.String()))
	}
}

                                                                                 
  
                                                                     
                                                                                                          
                                                      
                                                                                                        
                                                                                  
  
                                                                                           
                                                                                                  
                                                                                                    
  
                                                                                                   
                                                          
                                                        
                                                                                  
                                                                                                    
  
                                                                                            
                                        
                                                      
                                                                           
                                                                          
  
                                              

type Side int

const (
	Long  Side = 1
	Short Side = 2
)

func (s Side) Opposite() Side {
	if s == Long {
		return Short
	}
	return Long
}

func (s Side) String() string {
	if s == Long {
		return "Long"
	}
	return "Short"
}

type PositionStatus int

const (
	StatusOpen               PositionStatus = 1
	StatusClosed             PositionStatus = 2
	StatusLiquidated         PositionStatus = 3
	StatusADLClosed          PositionStatus = 4                                              
	StatusInsuredDeleveraged PositionStatus = 5                                                    
)

type OrderStatus int

const (
	OrderOpen      OrderStatus = 1
	OrderFilled    OrderStatus = 2
	OrderCancelled OrderStatus = 3
)

                                           

                                                         
type Position struct {
	ID         string                                         
	Owner      string                     
	Pair       string                          
	Side       Side                    
	EntryPrice *big.Int                                           
	                                                                        
	                                                                                  
	EntryNotionalNumerator *big.Int
	Quantity               *big.Int                                                
	Margin                 *big.Int                                                                
	                                                                          
	                                                        
	MarginBasisAmount     *big.Int
	MarginBasisClosed     *big.Int
	Status                PositionStatus                                                               
	OpenHeight            uint64                              
	LastCumulativeFunding *big.Int                                                                                    
	                                                                                
	                                                                                 
	                                                                                                 
	                                                                                   
	FirstCumulativeFunding *big.Int
	                                                                               
	SettledFunding     *big.Int                            
	LastSettledFunding *big.Int                                                                           
	                                                                                                     
	RealizedPnL    *big.Int                                                       
	ADLWithheldPnL *big.Int                                               
	ClosedQty      *big.Int                         
	ExitNotional   *big.Int                                                                                                  
	                                                                      
	                                                                           
	                                                             
	ClosePnLRemainder *big.Int
	MaxQuantity       *big.Int                                             
	InitialMargin     *big.Int                                                        
	CloseHeight       uint64                                                    
}

                                  
  
                                                                                                                    
                                                                                                          
                                                                                               
                                                                                                              
                                                                                 
type MarketConfig struct {
	Pair                     string             
	BaseToken                string                                                                         
	MaxLeverage              uint64                               
	MaintenanceMarginRateBps uint64                                               
	QuoteToken               string                                         
}

const (
	DefaultMaxLeverage              uint64 = 100
	DefaultMaintenanceMarginRateBps uint64 = 50
	DefaultNoFillOrderTTLBlocks     uint64 = 1000
)

func (cfg *MarketConfig) MaxLeverageOrDefault() uint64 {
	if cfg == nil || cfg.MaxLeverage == 0 {
		return DefaultMaxLeverage
	}
	return cfg.MaxLeverage
}

func (cfg *MarketConfig) MaintenanceMarginRateOrDefault() uint64 {
	if cfg == nil || cfg.MaintenanceMarginRateBps == 0 {
		return DefaultMaintenanceMarginRateBps
	}
	return cfg.MaintenanceMarginRateBps
}

func (cfg *MarketConfig) ApplyDefaults() {
	if cfg == nil {
		return
	}
	if cfg.MaxLeverage == 0 {
		cfg.MaxLeverage = DefaultMaxLeverage
	}
	if cfg.MaintenanceMarginRateBps == 0 {
		cfg.MaintenanceMarginRateBps = DefaultMaintenanceMarginRateBps
	}
}

                                      
const BPS = 10000

                                                   

                                 
type LiquidationResult struct {
	PositionID     string
	Owner          string
	Side           Side
	Quantity       *big.Int                         
	LiqPrice       *big.Int                   
	BankruptPrice  *big.Int             
	ClosePrice     *big.Int                                    
	MarginLost     *big.Int                            
	InsuranceDelta *big.Int                                              
	NeedsADL       bool                                                                
	ADLShortfall   *big.Int                             
}

                             
type ADLCandidate struct {
	PositionID string
	Owner      string
	Score      *big.Int                                                                             
	Quantity   *big.Int                   
}

                             
type ADLResult struct {
	Reduced      []ADLReducedPosition
	TotalCovered *big.Int                         
}

                                      
type ADLReducedPosition struct {
	PositionID      string
	Owner           string
	ReducedQuantity *big.Int                
	SettlePrice     *big.Int                               
	GrossProfit     *big.Int                                                 
	WithheldProfit  *big.Int                               
	PaidProfit      *big.Int                                                          
}
