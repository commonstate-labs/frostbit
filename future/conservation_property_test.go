package future

import (
	"math/big"
	"math/rand"
	"testing"
)

                                                                                       
                                                                                                                                
  
                                                                                                  
                                                         
  
                     
                                                                                          
                                                                                                                 
                                                                                                  
                                                                                              
  
                           
                                                      
                                                      
                                                                   
                                                                          
func TestConservation_OpenReduceFunding(t *testing.T) {
	rng := rand.New(rand.NewSource(123))

	const (
		numRounds    = 200
		numTraders   = 5
		initialPerUser = int64(1_000_000_000_000)                
		entryPrice   = int64(50_000 * 100_000_000)
	)

	                        
	e, posStore, insStore, balStore, mp := newTestEngine()
	                                                
	mp.prices["BTC_USDT"] = big.NewInt(entryPrice)

	               
	traders := make([]string, 0, numTraders)
	totalInitial := new(big.Int)
	for i := 0; i < numTraders; i++ {
		addr := string(rune('a' + i))
		traders = append(traders, addr)
		balStore.balances[addr+":USDT"] = big.NewInt(initialPerUser)
		totalInitial.Add(totalInitial, big.NewInt(initialPerUser))
	}

	                                                                     
	computeTotal := func() *big.Int {
		total := new(big.Int)
		for _, tr := range traders {
			if b, ok := balStore.balances[tr+":USDT"]; ok {
				total.Add(total, b)
			}
		}
		                                                                                  
		for _, p := range posStore.data {
			if p.Margin != nil {
				total.Add(total, p.Margin)
			}
		}
		if b, err := insStore.GetBalance("BTC_USDT"); err == nil {
			total.Add(total, b)
		}
		return total
	}

	                                     
	for round := 0; round < numRounds; round++ {
		                                
		newMark := big.NewInt(entryPrice)

		trader := traders[rng.Intn(numTraders)]
		op := rng.Intn(3)                                         

		switch op {
		case 0, 1:
			side := Long
			if op == 1 {
				side = Short
			}
			qty := big.NewInt(rng.Int63n(100_000_000) + 1)            
			leverage := uint64(rng.Intn(10) + 1)                     
			notional := CalcNotional(qty, newMark)
			margin := new(big.Int).Div(notional, big.NewInt(int64(leverage)))
			if margin.Sign() <= 0 {
				continue
			}
			                           
			bal := balStore.balances[trader+":USDT"]
			if bal == nil || bal.Cmp(margin) < 0 {
				continue
			}
			                            
			balStore.balances[trader+":USDT"] = new(big.Int).Sub(bal, margin)
			if err := e.ProcessTradeForPosition(trader, "BTC_USDT", side, qty, newMark, margin, uint64(round+1)); err != nil {
				                              
				balStore.balances[trader+":USDT"] = bal
			}

		case 2:
			                                                              
			for _, p := range posStore.data {
				if p.Status == StatusOpen && p.Owner == trader && p.Quantity.Sign() > 0 {
					                                 
					reduceQty := new(big.Int).Mul(p.Quantity, big.NewInt(int64(rng.Intn(50)+50)))
					reduceQty.Div(reduceQty, big.NewInt(100))
					if reduceQty.Sign() <= 0 {
						continue
					}
					_ = e.ProcessTradeForPosition(p.Owner, p.Pair, p.Side.Opposite(), reduceQty, newMark, new(big.Int), uint64(round+1))
					break
				}
			}
		}
	}

	                                                                                                                      
	                                                                             
	final := computeTotal()
	                                                                                 
	maxDrift := big.NewInt(int64(numRounds * numTraders * 10))
	diff := new(big.Int).Sub(final, totalInitial)
	absDiff := new(big.Int).Abs(diff)
	if absDiff.Cmp(maxDrift) > 0 {
		t.Fatalf("conservation violated: initial=%s final=%s diff=%s (max allowed drift=%s)",
			totalInitial, final, diff, maxDrift)
	}
	t.Logf("final=%s, drift=%s within bound=%s", final, diff, maxDrift)
}
