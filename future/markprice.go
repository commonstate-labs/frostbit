package future

import "math/big"

                                              

                                          
const DefaultEMAWindow = 20

                                                                 
  
                                              
               
  
                                            
  
                         
                                                                      
  
                                                                             
func CalcEMA(oldEMA, lastPrice *big.Int, window int) *big.Int {
	if oldEMA == nil || oldEMA.Sign() <= 0 {
		return new(big.Int).Set(lastPrice)
	}
	if window <= 0 {
		window = DefaultEMAWindow
	}

	alphaBps := int64(2 * BPS / (window + 1))
	oneMinusAlpha := int64(BPS) - alphaBps

	                                                                 
	term1 := new(big.Int).Mul(big.NewInt(alphaBps), lastPrice) // safe-math-ok: EMA 加权分子保留 BPS 精度，最终统一除 BPS。
	term2 := new(big.Int).Mul(big.NewInt(oneMinusAlpha), oldEMA) // safe-math-ok: EMA 旧值加权分子先乘后除，避免逐项截断。
	sum := new(big.Int).Add(term1, term2) // safe-math-ok: 合并两个 EMA 分子后才除 BPS，此处不是金额入账。
	return sum.Div(sum, big.NewInt(BPS))
}

