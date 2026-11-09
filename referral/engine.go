package referral

import "math/big"

                            
type Engine struct {
	referrers ReferrerStore
	balance   BalanceAccessor
	insurance InsuranceFundDepositor
	config    Config
}

                               
func NewEngine(rs ReferrerStore, ba BalanceAccessor, ins InsuranceFundDepositor, cfg Config) *Engine {
	return &Engine{referrers: rs, balance: ba, insurance: ins, config: cfg}
}

                                                                                          
                                                                                  
func (e *Engine) CalcAndDistributeFee(trader, pair, quoteToken string, notional *big.Int) (*FeeResult, error) {
	return e.CalcAndDistributeFeeWithRole(trader, pair, quoteToken, notional, RoleUnspecified)
}

                                                                                     
                                                            
func (e *Engine) CalcAndDistributeFeeWithRole(trader, pair, quoteToken string, notional *big.Int, role Role) (*FeeResult, error) {
	if notional == nil || notional.Sign() <= 0 {
		return nil, nil
	}
	                                                               
	                                                                                                                           
	rate := e.config.TradingFeeRate
	switch role {
	case RoleTaker:
		if e.config.TakerFeeRate > 0 {
			rate = e.config.TakerFeeRate
		}
	case RoleMaker:
		if e.config.MakerFeeRate > 0 {
			rate = e.config.MakerFeeRate
		}
	}
	if rate == 0 {
		return nil, nil
	}

	                                  
	fullFee := new(big.Int).Mul(notional, big.NewInt(int64(rate)))
	fullFee.Div(fullFee, big.NewInt(BPS))
	if fullFee.Sign() <= 0 {
		return nil, nil
	}

	result := &FeeResult{Trader: trader, Role: role, FullFee: new(big.Int).Set(fullFee)}

	                  
	referrer, hasReferrer, _ := e.referrers.GetReferrer(trader)
	if hasReferrer && referrer != "" {
		result.Referrer = referrer

		                                                                        
		discountMul := BPS - e.config.RefereeDiscountRate
		actualFee := new(big.Int).Mul(fullFee, big.NewInt(int64(discountMul)))
		actualFee.Div(actualFee, big.NewInt(BPS))
		result.ActualFee = actualFee

		                                               
		rebate := new(big.Int).Mul(fullFee, big.NewInt(int64(e.config.ReferrerRebateRate)))
		rebate.Div(rebate, big.NewInt(BPS))
		result.ReferrerRebate = rebate

		                                    
		result.InsuranceAmount = new(big.Int).Sub(actualFee, rebate)
		if result.InsuranceAmount.Sign() < 0 {
			result.InsuranceAmount = new(big.Int)
		}
	} else {
		result.ActualFee = new(big.Int).Set(fullFee)
		result.ReferrerRebate = new(big.Int)
		result.InsuranceAmount = new(big.Int).Set(fullFee)
	}

	                          
	if err := e.balance.DeductBalance(trader, quoteToken, result.ActualFee); err != nil {
		return nil, err
	}

	                        
	if result.ReferrerRebate.Sign() > 0 && result.Referrer != "" {
		if err := e.balance.AddBalance(result.Referrer, quoteToken, result.ReferrerRebate); err != nil {
			return nil, err
		}
	}

	                     
	if result.InsuranceAmount.Sign() > 0 {
		if err := e.insurance.DepositInsuranceFund(pair, result.InsuranceAmount); err != nil {
			return nil, err
		}
	}

	return result, nil
}

                                                                                                       
const maxReferralChainDepth = 10

                                                                                                 
                                                                                                        
func (e *Engine) wouldFormCycle(user, referrer string) bool {
	cur := referrer
	for i := 0; i <= maxReferralChainDepth; i++ {
		if cur == "" {
			return false
		}
		if cur == user {
			return true
		}
		if i == maxReferralChainDepth {
			return true
		}
		next, exists, _ := e.referrers.GetReferrer(cur)
		if !exists {
			return false
		}
		cur = next
	}
	return false
}

                                  
func (e *Engine) BindReferrer(user, referrer string) error {
	if user == referrer {
		return ErrSelfReferral
	}
	if _, exists, _ := e.referrers.GetReferrer(user); exists {
		return ErrAlreadyBound
	}
	if e.wouldFormCycle(user, referrer) {
		return ErrCyclicReferral
	}
	return e.referrers.SetReferrer(user, referrer)
}
