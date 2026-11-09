package future

import "math/big"

                                     

                                                                       
const DefaultFundingInterval uint64 = 10

                                                               
const DefaultMaxFundingRateBps int64 = 75

                                     
  
                                                                                       
                                                                        
func CalcFundingRate(futurePrice, spotPrice *big.Int, maxRateBps int64) int64 {
	if spotPrice == nil || spotPrice.Sign() <= 0 {
		return 0
	}
	                                 
	diff := new(big.Int).Sub(futurePrice, spotPrice) // safe-math-ok: 价差需保留正负号以区分资金费付款方向。
	                                   
	                                                                                                     
	                                                                                               
	rateBig := new(big.Int).Mul(diff, big.NewInt(BPS)) // safe-math-ok: 有符号费率分子先乘后除，转 int64 前按 maxRate 夹紧。
	rateBig.Quo(rateBig, spotPrice)

	                                                                       
	                                                                    
	                                                                    
	maxRate := big.NewInt(maxRateBps)
	minRate := new(big.Int).Neg(maxRate)
	if rateBig.Cmp(maxRate) > 0 {
		return maxRateBps
	}
	if rateBig.Cmp(minRate) < 0 {
		return -maxRateBps
	}
	return rateBig.Int64()
}

                                                       
  
                                             
                                             
                                             
               
  
                                                                                                                                  
func CalcFundingPayment(side Side, notional *big.Int, fundingRateBps int64) *big.Int {
	payment := new(big.Int).Mul(notional, big.NewInt(fundingRateBps)) // safe-math-ok: 资金费率可为负，保留有符号分子再除 BPS。
	payment.Quo(payment, big.NewInt(BPS))                               
	if side == Long {
		payment.Neg(payment)                                          
	}
	                                                                       
	return payment
}

                                                      
                                                                             
                                                                                        
func UpdateCumulativeFunding(oldCumLong, oldCumShort *big.Int, fundingRateBps int64) (newCumLong, newCumShort *big.Int) {
	delta := big.NewInt(fundingRateBps)
	return new(big.Int).Add(oldCumLong, delta), new(big.Int).Add(oldCumShort, delta) // safe-math-ok: 累计费率是有符号计数器，不是账户余额。
}

                                                                                             
                                                                               
                                                                                           
                                               
                                                                                         
                                                                                                             
                                                                                                                   
  
                                                                                   
func CalcDisplayPendingFunding(side Side, quantity, markPrice, firstCum, prevCum, currentCum *big.Int) *big.Int {
	if currentCum == nil {
		return new(big.Int)
	}
	baseline := prevCum
	if baseline == nil {
		baseline = new(big.Int)
	}
	if firstCum != nil && firstCum.Cmp(baseline) > 0 {
		baseline = firstCum
	}
	return CalcPendingFunding(side, quantity, markPrice, baseline, currentCum)
}

                                                          
                                                                                      
func CalcPendingFunding(side Side, quantity, markPrice, lastCumFunding, currentCumFunding *big.Int) *big.Int {
	if lastCumFunding == nil || currentCumFunding == nil {
		return new(big.Int)
	}
	cumDiff := new(big.Int).Sub(currentCumFunding, lastCumFunding) // safe-math-ok: 有符号费率增量决定应收或应付。
	if cumDiff.Sign() == 0 {
		return new(big.Int)
	}
	notional := CalcNotional(quantity, markPrice)
	pending := new(big.Int).Mul(notional, cumDiff) // safe-math-ok: 有符号资金费分子需先乘后除，不在此写入余额。
	                                                                                       
	pending.Quo(pending, big.NewInt(BPS))
	if side == Long {
		pending.Neg(pending)                                        
	}
	return pending
}
