package future

import "math/big"

                                                                     
                                                                                
  
                                                           
  
                                                 
  
                                                                               

                                                                   
var PriceScale = big.NewInt(100_000_000)

                                                        
                                                                                
                                                                                                   
                                                  
func MulDivFloor(a, b, c *big.Int) *big.Int {
	if c == nil || c.Sign() <= 0 {
		return new(big.Int)
	}
	prod := new(big.Int).Mul(a, b) // safe-math-ok: 比例分摊须先保留完整乘积再除正分母，此处无余额写入。
	return new(big.Int).Quo(prod, c)
}

                                                                                            
func MulDivCeil(a, b, c *big.Int) *big.Int {
	if a == nil || b == nil || c == nil || a.Sign() <= 0 || b.Sign() <= 0 || c.Sign() <= 0 {
		return new(big.Int)
	}
	numerator := new(big.Int).Mul(a, b) // safe-math-ok: 向上取整需使用完整乘积分子及 QuoRem 余数。
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, c, remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

                                                                                 
func CalcNotional(quantity, price *big.Int) *big.Int {
	prod := new(big.Int).Mul(quantity, price) // safe-math-ok: 名义额是数量乘价格的缩放计算，乘积随后除 PriceScale。
	return new(big.Int).Quo(prod, PriceScale)
}

                                                    
  
                                                      
  
                                                   
  
                                                                                           
                                                                        
func CalcRequiredMargin(quantity, price *big.Int, leverage uint64) *big.Int {
	if quantity == nil || price == nil || quantity.Sign() <= 0 || price.Sign() <= 0 || leverage == 0 {
		return new(big.Int)
	}
	numerator := new(big.Int).Mul(quantity, price) // safe-math-ok: 保证金分子先乘后除，保留向上取整所需精度。
	denominator := new(big.Int).Mul(PriceScale, new(big.Int).SetUint64(leverage)) // safe-math-ok: 正杠杆与价格精度构成除数，不是金额写入。
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}

                                                               
                                          
func CalcEffectiveLeverage(notional, margin *big.Int) uint64 {
	if margin == nil || margin.Sign() <= 0 {
		return 0
	}
	return new(big.Int).Div(notional, margin).Uint64()
}

                                                         
  
                                                                  
                                                                  
func CalcUnrealizedPnL(side Side, entryPrice, markPrice, quantity *big.Int) *big.Int {
	var diff *big.Int
	if side == Long {
		diff = new(big.Int).Sub(markPrice, entryPrice) // safe-math-ok: 多头价差可为负，需保留亏损符号。
	} else {
		diff = new(big.Int).Sub(entryPrice, markPrice) // safe-math-ok: 空头价差可为负，需保留亏损符号。
	}
	prod := new(big.Int).Mul(quantity, diff) // safe-math-ok: 有符号盈亏分子先乘后除 PriceScale。
	return new(big.Int).Quo(prod, PriceScale)
}

                                                      
func CalcEquity(margin, unrealizedPnL *big.Int) *big.Int {
	return new(big.Int).Add(margin, unrealizedPnL) // safe-math-ok: 风险判断的净权益可为负，此函数不直接入账。
}

                                                                            
func CalcMaintenanceMargin(notional *big.Int, mmRateBps uint64) *big.Int {
	mm := new(big.Int).Mul(notional, new(big.Int).SetUint64(mmRateBps)) // safe-math-ok: 维持保证金按费率分子先乘后除 BPS。
	return mm.Div(mm, big.NewInt(BPS))
}

                                        
                                      
func ShouldLiquidate(margin *big.Int, side Side, entryPrice, markPrice, quantity *big.Int, mmRateBps uint64) bool {
	pnl := CalcUnrealizedPnL(side, entryPrice, markPrice, quantity)
	equity := CalcEquity(margin, pnl)
	notional := CalcNotional(quantity, markPrice)
	mm := CalcMaintenanceMargin(notional, mmRateBps)
	return equity.Cmp(mm) <= 0
}

                                                      
  
                                                                               
                                                                               
  
                                                                       
                                                                        
func CalcLiquidationPrice(side Side, entryPrice, margin, quantity *big.Int, mmRateBps uint64) *big.Int {
	if quantity == nil || quantity.Sign() <= 0 {
		return new(big.Int)
	}
	                                                  
	marginPerUnit := new(big.Int).Mul(margin, PriceScale) // safe-math-ok: 清算价格换算分子随后除数量，不能先截断乘积。
	marginPerUnit.Div(marginPerUnit, quantity)
	                                          
	mmAdjust := new(big.Int).Mul(entryPrice, new(big.Int).SetUint64(mmRateBps)) // safe-math-ok: 风险价格调整先乘后除 BPS，不直接移动资产。
	mmAdjust.Div(mmAdjust, big.NewInt(BPS))

	result := new(big.Int)
	if side == Long {
		                                                
		result.Sub(entryPrice, marginPerUnit)
		result.Add(result, mmAdjust)
	} else {
		                                                
		result.Add(entryPrice, marginPerUnit)
		result.Sub(result, mmAdjust)
	}

	                        
	if result.Sign() < 0 {
		return new(big.Int)
	}
	return result
}

                                                                     
  
                                                                
                                                                
func CalcBankruptPrice(side Side, entryPrice, margin, quantity *big.Int) *big.Int {
	if quantity == nil || quantity.Sign() <= 0 {
		return new(big.Int)
	}
	marginPerUnit := new(big.Int).Mul(margin, PriceScale) // safe-math-ok: 破产价格换算分子随后除数量，结果负值在末尾夹零。
	marginPerUnit.Div(marginPerUnit, quantity)
	result := new(big.Int)
	if side == Long {
		result.Sub(entryPrice, marginPerUnit)
	} else {
		result.Add(entryPrice, marginPerUnit)
	}
	if result.Sign() < 0 {
		return new(big.Int)
	}
	return result
}

                                                     
  
                                                                             
func CalcWeightedEntryPrice(oldEntry, oldQty, tradePrice, tradeQty *big.Int) *big.Int {
	totalQty := new(big.Int).Add(oldQty, tradeQty) // safe-math-ok: 加权均价的数量分母只用于纯计算，非正值随后处理。
	if totalQty.Sign() <= 0 {
		return new(big.Int).Set(tradePrice)
	}
	oldVal := new(big.Int).Mul(oldEntry, oldQty) // safe-math-ok: 旧仓成本分子需保留乘积精度以计算加权均价。
	newVal := new(big.Int).Mul(tradePrice, tradeQty) // safe-math-ok: 新成交成本分子需与旧分子合并后再除总数量。
	totalVal := new(big.Int).Add(oldVal, newVal) // safe-math-ok: 加权均价汇总分子随后除 totalQty，此处不入账。
	return totalVal.Div(totalVal, totalQty)
}

                                
  
                                                                         
                                                                               
                                                   
func CalcADLScore(pnl, notional, margin *big.Int) *big.Int {
	if pnl.Sign() <= 0 || margin.Sign() <= 0 {
		return new(big.Int)
	}
	                                             
	num := new(big.Int).Mul(pnl, notional) // safe-math-ok: ADL 排名的比例分子仅用于分数比较，不是余额。
	denom := new(big.Int).Mul(margin, margin) // safe-math-ok: ADL 分数使用保证金平方作正分母，保留完整精度。
	if denom.Sign() <= 0 {
		return new(big.Int)
	}
	return new(big.Int).Div(num, denom)
}

                                                                                  
  
                                                  
                                                                            
                                                  
func CalcInsuranceDelta(side Side, bankruptPrice, closePrice, quantity *big.Int) *big.Int {
	var diff *big.Int
	if side == Long {
		diff = new(big.Int).Sub(closePrice, bankruptPrice) // safe-math-ok: 多头成交相对破产价的差值需保留缺口负号。
	} else {
		diff = new(big.Int).Sub(bankruptPrice, closePrice) // safe-math-ok: 空头成交相对破产价的差值需保留缺口负号。
	}
	prod := new(big.Int).Mul(diff, quantity) // safe-math-ok: 有符号保险价差分子随后除 PriceScale，不在此扣基金。
	return new(big.Int).Quo(prod, PriceScale)
}
