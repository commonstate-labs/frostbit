package vm

import "math/big"

                                                                                                     
                              
                                                                               
                                                                                                             
                                       
                                            
  
                                                                                                        
                                      

                                                        
func MulDivFloor(a, b, c *big.Int) *big.Int {
	if c == nil || c.Sign() <= 0 {
		return new(big.Int)
	}
	prod := new(big.Int).Mul(a, b) // safe-math-ok: 比例分摊的完整分子先乘后除，不能先按余额上限截断。
	return new(big.Int).Quo(prod, c)                                             
}

                                                                                                       
                                              
func MulDivCeil(a, b, c *big.Int) *big.Int {
	if c == nil || c.Sign() <= 0 {
		return new(big.Int)
	}
	prod := new(big.Int).Mul(a, b) // safe-math-ok: 向上取整需保留完整乘积及余数，再由调用方处理金额边界。
	if prod.Sign() == 0 {
		return new(big.Int)
	}
	q, r := new(big.Int).QuoRem(prod, c, new(big.Int))
	if r.Sign() != 0 && prod.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

                                                              
                                                                                               
                            
func MulDivResidue(a, b, c *big.Int) (quot, rem *big.Int) {
	if c == nil || c.Sign() <= 0 {
		return new(big.Int), new(big.Int)
	}
	prod := new(big.Int).Mul(a, b) // safe-math-ok: 商与残渣必须由同一完整分子计算，尚未写入余额。
	quot = new(big.Int)
	rem = new(big.Int)
	quot.QuoRem(prod, c, rem)
	return quot, rem
}
