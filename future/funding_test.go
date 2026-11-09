package future

import (
	"math/big"
	"testing"
)

func TestCalcEMA_Init(t *testing.T) {
	                                             
	result := CalcEMA(nil, big.NewInt(50000), 20)
	if result.Cmp(big.NewInt(50000)) != 0 {
		t.Fatalf("init EMA = %s, want 50000", result)
	}
}

func TestCalcEMA_Smoothing(t *testing.T) {
	                                           
	                        
	                                            
	                                                             
	result := CalcEMA(big.NewInt(50000), big.NewInt(51000), 20)
	if result.Cmp(big.NewInt(50095)) != 0 {
		t.Fatalf("EMA smoothing = %s, want 50095", result)
	}
}

func TestCalcEMA_Stable(t *testing.T) {
	                              
	result := CalcEMA(big.NewInt(50000), big.NewInt(50000), 20)
	if result.Cmp(big.NewInt(50000)) != 0 {
		t.Fatalf("stable EMA = %s, want 50000", result)
	}
}

func TestCalcFundingRate_Positive(t *testing.T) {
	                                                                 
	rate := CalcFundingRate(big.NewInt(51000), big.NewInt(50000), 75)
	                                                             
	if rate != 75 {
		t.Fatalf("rate = %d, want 75 (clamped)", rate)
	}
}

func TestCalcFundingRate_Negative(t *testing.T) {
	                                                                 
	rate := CalcFundingRate(big.NewInt(49500), big.NewInt(50000), 75)
	                                                               
	if rate != -75 {
		t.Fatalf("rate = %d, want -75 (clamped)", rate)
	}
}

func TestCalcFundingRate_Small(t *testing.T) {
	                           
	rate := CalcFundingRate(big.NewInt(50050), big.NewInt(50000), 75)
	                                            
	if rate != 10 {
		t.Fatalf("rate = %d, want 10", rate)
	}
}

func TestCalcFundingRate_HugePositivePremiumClampsBeforeInt64Conversion(t *testing.T) {
	                                                                      
	                                                                          
	spotPrice := big.NewInt(10_000)
	futurePrice := new(big.Int).Add(
		new(big.Int).Lsh(big.NewInt(1), 64),
		spotPrice,
	)

	rate := CalcFundingRate(futurePrice, spotPrice, 75)
	if rate != 75 {
		t.Fatalf("rate = %d, want clamp 75", rate)
	}
}

func TestCalcFundingPayment_LongPositive(t *testing.T) {
	                                            
	payment := CalcFundingPayment(Long, big.NewInt(100000), 10)
	                                                              
	if payment.Cmp(big.NewInt(-100)) != 0 {
		t.Fatalf("long payment = %s, want -100", payment)
	}
}

func TestCalcFundingPayment_ShortPositive(t *testing.T) {
	                                            
	payment := CalcFundingPayment(Short, big.NewInt(100000), 10)
	if payment.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("short payment = %s, want 100", payment)
	}
}

func TestCalcFundingPayment_LongNegative(t *testing.T) {
	                                            
	payment := CalcFundingPayment(Long, big.NewInt(100000), -10)
	                                                                 
	if payment.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("long negative payment = %s, want 100", payment)
	}
}

                                                                                                   
func TestCalcPendingFunding_ShortPositive(t *testing.T) {
	                                                         
	newCumLong, newCumShort := UpdateCumulativeFunding(new(big.Int), new(big.Int), 10)
	                                                        
	qty := new(big.Int).Set(PriceScale)
	mark := new(big.Int).Set(PriceScale)
	                                                                                                
	want := new(big.Int).Div(PriceScale, big.NewInt(1000))
	pLong := CalcPendingFunding(Long, qty, mark, new(big.Int), newCumLong)
	if pLong.Sign() >= 0 || new(big.Int).Neg(pLong).Cmp(want) != 0 {
		t.Fatalf("long pending = %s, want -%s", pLong, want)
	}
	pShort := CalcPendingFunding(Short, qty, mark, new(big.Int), newCumShort)
	if pShort.Cmp(want) != 0 {
		t.Fatalf("short pending = %s, want %s (收入)", pShort, want)
	}
}
