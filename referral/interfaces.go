package referral

import "math/big"

                                   
type ReferrerStore interface {
	GetReferrer(user string) (referrer string, exists bool, err error)
	SetReferrer(user, referrer string) error
}

                               
type BalanceAccessor interface {
	DeductBalance(owner, token string, amount *big.Int) error
	AddBalance(owner, token string, amount *big.Int) error
}

                                            
type InsuranceFundDepositor interface {
	DepositInsuranceFund(pair string, amount *big.Int) error
}
