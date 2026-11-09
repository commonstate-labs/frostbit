package arbitrage

import (
	"dex/pb"
	"math/big"
)

type legQuote struct {
	baseAmount *big.Int
	inputSpent *big.Int
	output     *big.Int
	overflow   bool
}

func quoteFromBase(base, price *big.Int) *big.Int {
	if base == nil || price == nil || base.Sign() <= 0 || price.Sign() <= 0 {
		return new(big.Int)
	}
	return new(big.Int).Quo(new(big.Int).Mul(base, price), priceScale)
}

func quoteLeg(leg Leg, level *topLevel, input *big.Int) legQuote {
	if level == nil || level.price == nil || len(level.orders) == 0 || input == nil || input.Sign() <= 0 {
		return zeroLegQuote()
	}
	if leg.Side == pb.OrderSide_BUY {
		return quoteBuy(level, input)
	}
	return quoteSell(level, input)
}

func quoteSell(level *topLevel, input *big.Int) legQuote {
	if input.Cmp(maxBaseForPrice(level.price)) > 0 {
		return legQuote{overflow: true}
	}
	remaining := new(big.Int).Set(input)
	output := new(big.Int)
	for _, order := range level.orders {
		if remaining.Sign() <= 0 {
			break
		}
		tradeBase := minBig(remaining, order.remain)
		tradeQuote := quoteFromBase(tradeBase, level.price)
		if tradeQuote.Sign() == 0 {
			                                                                       
			continue
		}
		remaining.Sub(remaining, tradeBase)
		output.Add(output, tradeQuote)
	}
	if remaining.Sign() > 0 {
		consumed := new(big.Int).Sub(input, remaining)
		return legQuote{
			baseAmount: consumed,
			inputSpent: new(big.Int).Set(consumed),
			output:     output,
		}
	}
	return legQuote{
		baseAmount: new(big.Int).Set(input),
		inputSpent: new(big.Int).Set(input),
		output:     output,
	}
}

func quoteBuy(level *topLevel, inputBudget *big.Int) legQuote {
	baseOut := new(big.Int)
	one := big.NewInt(1)

	                                                                      
	                                                                            
	                                                                         
	                                                      
	numerator := new(big.Int).Mul(new(big.Int).Add(inputBudget, one), priceScale)
	numerator.Sub(numerator, one)
	remainingTarget := numerator.Quo(numerator, level.price)
	if safeMax := maxBaseForPrice(level.price); remainingTarget.Cmp(safeMax) > 0 {
		remainingTarget.Set(safeMax)
	}
	if remainingTarget.Sign() <= 0 {
		return zeroLegQuote()
	}

	for _, order := range level.orders {
		if remainingTarget.Sign() <= 0 {
			break
		}
		tradeBase := minBig(order.remain, remainingTarget)
		tradeQuote := quoteFromBase(tradeBase, level.price)
		if tradeQuote.Sign() == 0 {
			                                                                 
			                                                                   
			                                                  
			break
		}
		baseOut.Add(baseOut, tradeBase)
		remainingTarget.Sub(remainingTarget, tradeBase)
	}
	freezeAmount := quoteFromBase(baseOut, level.price)
	if freezeAmount.Sign() <= 0 || freezeAmount.Cmp(inputBudget) > 0 {
		return zeroLegQuote()
	}
	return legQuote{
		baseAmount: baseOut,
		inputSpent: freezeAmount,
		output:     new(big.Int).Set(baseOut),
	}
}

func zeroLegQuote() legQuote {
	return legQuote{
		baseAmount: new(big.Int),
		inputSpent: new(big.Int),
		output:     new(big.Int),
	}
}

func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) <= 0 {
		return new(big.Int).Set(a)
	}
	return new(big.Int).Set(b)
}

func maxBaseForPrice(price *big.Int) *big.Int {
	if price == nil || price.Sign() <= 0 {
		return new(big.Int)
	}
	return new(big.Int).Quo(new(big.Int).Set(maxUint256), price)
}

func basePriceProductFits(base, price *big.Int) bool {
	if base == nil || price == nil || base.Sign() < 0 || price.Sign() < 0 {
		return false
	}
	return new(big.Int).Mul(base, price).Cmp(maxUint256) <= 0
}
