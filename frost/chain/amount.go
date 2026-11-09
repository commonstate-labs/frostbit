package chain

import (
	"errors"
	"math/big"
	"strings"
)

var (
	ErrInvalidWithdrawAmount = errors.New("invalid withdraw amount")
	maxUint256               = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
)

                                                                              
                                                                            
                                                                              
                                                                           
func ParseWithdrawAmount(raw string) (*big.Int, error) {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 78 {
		return nil, ErrInvalidWithdrawAmount
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil, ErrInvalidWithdrawAmount
		}
	}
	amount, ok := new(big.Int).SetString(s, 10)
	if !ok || amount.Sign() <= 0 || amount.Cmp(maxUint256) > 0 {
		return nil, ErrInvalidWithdrawAmount
	}
	return amount, nil
}

                                                                               
           
func CloneAmount(amount *big.Int) *big.Int {
	if amount == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(amount)
}
