package protocol

                                                                                   
const MinWitnessRechargeFeeRaw int64 = 1_000_000

                                                                            
                                        
const RequesterBondRaw int64 = 1_000_000_000

const (
	RechargeEconomicsLegacy   uint32 = 0
	RechargeEconomicsLaunchV1 uint32 = 1
)

                                                                             
                                                                            
                                                           
func WitnessRechargeEconomicsActive(chainID uint64) bool { return chainID == 51025 }

                                                                             
                                                                          
                                                                            
const MinChallengeContributionRaw int64 = 1_000_000_000

                                                                              
const WitnessCaseBondRaw int64 = 1_000_000_000

const ArbitrationProtocolVersion uint32 = 1
const ArbitrationRoundBlocks uint64 = 10_000

                                                                          
                                           
const DefaultRechargeChallengePeriodBlocks uint64 = 300
