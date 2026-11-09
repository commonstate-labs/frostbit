package future

import (
	"math/big"
	"math/rand"
	"testing"
)

                                                                                                  
  
                                                              
  
                                                                                              
                                                                                                
func TestMulDivFloor_ResidueInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const N = 10_000
	for i := 0; i < N; i++ {
		a := big.NewInt(rng.Int63())
		b := big.NewInt(rng.Int63())
		c := big.NewInt(rng.Int63n(1_000_000_000) + 1)          

		q := MulDivFloor(a, b, c)

		                 
		prod := new(big.Int).Mul(a, b)
		lhs := new(big.Int).Mul(q, c)
		r := new(big.Int).Sub(prod, lhs)

		if r.Sign() < 0 {
			t.Fatalf("iter %d: residue negative: a=%s b=%s c=%s q=%s r=%s", i, a, b, c, q, r)
		}
		if r.Cmp(c) >= 0 {
			t.Fatalf("iter %d: residue >= c: a=%s b=%s c=%s q=%s r=%s", i, a, b, c, q, r)
		}
	}
}

                                                                                                       
  
                                                                
  
            
  
                                                                 
                                                                 
                                                                              
  
                                                                          
                                                                                                           
func TestMarginPortion_NoDoubleCount(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const N = 5_000
	for i := 0; i < N; i++ {
		qty := big.NewInt(rng.Int63n(10_000_000_000) + 1)
		requiredMargin := big.NewInt(rng.Int63n(1_000_000_000) + 1)

		                                     
		nTrades := 3 + rng.Intn(4)
		remainQty := new(big.Int).Set(qty)
		sumPortion := new(big.Int)
		for j := 0; j < nTrades && remainQty.Sign() > 0; j++ {
			maxT := new(big.Int).Set(remainQty)
			if j < nTrades-1 {
				                               
				maxT.Quo(maxT, big.NewInt(2))
				if maxT.Sign() == 0 {
					maxT.SetInt64(1)
				}
			}
			tradeQty := big.NewInt(rng.Int63n(maxT.Int64()) + 1)
			portion := MulDivFloor(requiredMargin, tradeQty, qty)
			sumPortion.Add(sumPortion, portion)
			remainQty.Sub(remainQty, tradeQty)
		}
		unusedMargin := MulDivFloor(requiredMargin, remainQty, qty)

		                                                       
		residue := new(big.Int).Sub(requiredMargin, sumPortion)
		residue.Sub(residue, unusedMargin)

		if residue.Sign() < 0 {
			t.Fatalf("iter %d: residue NEGATIVE (over-allocation!): qty=%s req=%s sumPortion=%s unused=%s residue=%s",
				i, qty, requiredMargin, sumPortion, unusedMargin, residue)
		}
		                                                                                                    
		                                                       
		maxResidue := big.NewInt(int64(2 * (nTrades + 1)))
		if residue.Cmp(maxResidue) > 0 {
			t.Fatalf("iter %d: residue abnormally large: residue=%s maxExpected=%s", i, residue, maxResidue)
		}
	}
}
