                                 
                                 

package session

                                      
type SignSessionState int

const (
	                                    
	SignSessionStateInit SignSessionState = iota
	                                                       
	SignSessionStateCollectingNonces
	                                                      
	SignSessionStateCollectingShares
	                                           
	SignSessionStateAggregating
	                                        
	SignSessionStateComplete
	                                      
	SignSessionStateFailed
)

func (s SignSessionState) String() string {
	switch s {
	case SignSessionStateInit:
		return "INIT"
	case SignSessionStateCollectingNonces:
		return "COLLECTING_NONCES"
	case SignSessionStateCollectingShares:
		return "COLLECTING_SHARES"
	case SignSessionStateAggregating:
		return "AGGREGATING"
	case SignSessionStateComplete:
		return "COMPLETE"
	case SignSessionStateFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

                                        

                              
type Participant struct {
	ID      string                                              
	Index   int                                                                
	Address string                            
	IP      string                         
}

                                     

                                      
type SignSessionEvent int

const (
	                                                    
	SignSessionEventNonceReceived SignSessionEvent = iota
	                                                   
	SignSessionEventShareReceived
	                                 
	SignSessionEventTimeout
	                               
	SignSessionEventRetry
	                                                   
	SignSessionEventAggregatorSwitch
)
