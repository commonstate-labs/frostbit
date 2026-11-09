package servicepeer

                                                                           
                                                                    
type ExchangeRequest struct {
	Self Record `json:"self"`
}

                                                                             
                                                                  
type ExchangeResponse struct {
	Self  Record   `json:"self"`
	Peers []Record `json:"peers"`
}

                                                                           
                                                                              
                                                         
type ProbeRequest struct {
	Caller    Record    `json:"caller"`
	Challenge Challenge `json:"challenge"`
}

                                                                            
                                                                           
                                                                            
                                                                     
type ProbeResponse struct {
	Self            Record            `json:"self"`
	Response        ChallengeResponse `json:"response"`
	ProofBlockBytes []byte            `json:"proof_block_bytes,omitempty"`
}
