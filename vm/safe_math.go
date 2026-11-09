package vm

import (
	"errors"
	"math/big"
)

                                                       
                                                             

var (
	                                 
	ErrOverflow = errors.New("arithmetic overflow")
	                                                       
	ErrUnderflow = errors.New("arithmetic underflow")
	                                          
	ErrInvalidBalance = errors.New("invalid balance format")
	                                          
	ErrBalanceTooLong = errors.New("balance string too long")
	                                        
	ErrNegativeBalance = errors.New("negative balance not allowed")
)

                                                                                     
const MaxBalanceStringLen = 78

const (
	minSpotOrderNotionalRaw = int64(1)
)

                                                    
                                    
var MaxUint256 = func() *big.Int {
	max := new(big.Int)
	max.Exp(big.NewInt(2), big.NewInt(256), nil)
	max.Sub(max, big.NewInt(1))
	return max
}()

func minSpotOrderNotional() *big.Int { return big.NewInt(minSpotOrderNotionalRaw) }

                               
                          
                                                     
func SafeAdd(a, b *big.Int) (*big.Int, error) {
	if a == nil {
		a = big.NewInt(0)
	}
	if b == nil {
		b = big.NewInt(0)
	}

	                        
	if a.Sign() < 0 || b.Sign() < 0 {
		return nil, errors.New("negative value not allowed")
	}

	result := new(big.Int).Add(a, b)

	                              
	if result.Cmp(MaxUint256) > 0 {
		return nil, ErrOverflow
	}

	return result, nil
}

                               
                          
                                     
func SafeSub(a, b *big.Int) (*big.Int, error) {
	if a == nil {
		a = big.NewInt(0)
	}
	if b == nil {
		b = big.NewInt(0)
	}

	                        
	if a.Sign() < 0 || b.Sign() < 0 {
		return nil, errors.New("negative value not allowed")
	}

	                        
	if a.Cmp(b) < 0 {
		return nil, ErrUnderflow
	}

	result := new(big.Int).Sub(a, b)
	return result, nil
}

                                                        
                                                                   
                                                                                                          
var PriceScale = big.NewInt(100_000_000)        

                                                                                                                              
func QuoteFromBasePrice(base, price *big.Int) (*big.Int, error) {
	prod, err := SafeMul(base, price)
	if err != nil {
		return nil, err
	}
	return new(big.Int).Quo(prod, PriceScale), nil
}

                               
                          
                                                     
func SafeMul(a, b *big.Int) (*big.Int, error) {
	if a == nil {
		a = big.NewInt(0)
	}
	if b == nil {
		b = big.NewInt(0)
	}

	                        
	if a.Sign() < 0 || b.Sign() < 0 {
		return nil, errors.New("negative value not allowed")
	}

	result := new(big.Int).Mul(a, b)
	if result.Cmp(MaxUint256) > 0 {
		return nil, ErrOverflow
	}
	return result, nil
}

                                           
            
                                                    
                                          
               
                          
func ParseBalance(s string) (*big.Int, error) {
	                       
	if s == "" {
		return big.NewInt(0), nil
	}

	               
	if len(s) > MaxBalanceStringLen {
		return nil, ErrBalanceTooLong
	}

	                                                                     
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil, ErrInvalidBalance
		}
	}

	                    
	balance, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, ErrInvalidBalance
	}

	                                                                  
	if balance.Sign() < 0 {
		return nil, ErrNegativeBalance
	}

	                           
	if balance.Cmp(MaxUint256) > 0 {
		return nil, ErrOverflow
	}

	return balance, nil
}
