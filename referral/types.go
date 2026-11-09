package referral

import "math/big"

                                      
const BPS = 10000

                                  
  
                                                                                                  
                                                                                                      
                                                              
type Config struct {
	TradingFeeRate      uint64                                                                                   
	TakerFeeRate        uint64                                                                          
	MakerFeeRate        uint64                                                                   
	ReferrerRebateRate  uint64                                                    
	RefereeDiscountRate uint64                                                       
}

                             
  
                                                                                                            
                                                                     
var DefaultConfig = Config{
	TradingFeeRate:      5,                        
	TakerFeeRate:        5,                  
	MakerFeeRate:        2,                                            
	ReferrerRebateRate:  2000,       
	RefereeDiscountRate: 1000,       
}

                                                                       
type Role int

const (
	RoleUnspecified Role = iota                                           
	RoleTaker                               
	RoleMaker                               
)

                                                 
type FeeResult struct {
	Trader          string                     
	Role            Role                                            
	FullFee         *big.Int                               
	ActualFee       *big.Int                                                                     
	ReferrerRebate  *big.Int                   
	InsuranceAmount *big.Int                      
	Referrer        string                                                
}
