package future

import "math/big"

                                      
                                                          
type PositionStore interface {
	                                                                          
	                                                                      
	AllocatePositionID(owner, pair string, side Side, height uint64) (string, error)
	GetPosition(positionID string) (*Position, error)
	SetPosition(pos *Position) error
	DeletePosition(positionID string) error
	                                                  
	GetPositionsByOwner(owner string) ([]*Position, error)
	                                                                              
	                                  
	GetPositionByOwnerAndPairSide(owner, pair string, side Side) (*Position, error)
	                                                                                          
	GetAllOpenPositions(pair string) ([]*Position, error)
}

                                          
type MarkPriceProvider interface {
	                                                                            
	                                                                      
	GetMarkPrice(pair string) (*big.Int, error)
	                                                                      
	                                                          
	                                                                                    
	                                                 
	GetSettleMarkPrice(pair string) (*big.Int, error)
}

                                        
type InsuranceFundStore interface {
	GetBalance(pair string) (*big.Int, error)
	SetBalance(pair string, amount *big.Int) error
	                                                                                              
	                                                                                                
	AddShortfall(pair string, amount *big.Int) error
	GetShortfall(pair string) (*big.Int, error)
}

                                                                          
type BalanceAccessor interface {
	GetAvailableBalance(owner, token string) (*big.Int, error)
	DeductBalance(owner, token string, amount *big.Int) error
	AddBalance(owner, token string, amount *big.Int) error
}

                                                  
type CumulativeFundingStore interface {
	                                                                                                       
	GetCumulativeFunding(pair string) (cumLong, cumShort *big.Int, err error)
	SetCumulativeFunding(pair string, cumLong, cumShort *big.Int) error
	                                                                                                
	                                                                            
	                                                                                      
	                                    
	GetPrevCumulativeFunding(pair string) (prevLong, prevShort *big.Int, err error)
	                                                                                       
	SetPrevCumulativeFunding(pair string, prevLong, prevShort *big.Int) error
}

                                         
                                                                     
type LiquidationIndex interface {
	                                               
	Upsert(positionID string, pair string, side Side, liqPrice *big.Int) error
	                                                           
	Remove(positionID string, pair string, side Side, oldLiqPrice *big.Int) error
	                                                                              
	FindLongsAtRisk(pair string, markPrice *big.Int) ([]string, error)
	                                                                               
	FindShortsAtRisk(pair string, markPrice *big.Int) ([]string, error)
}
