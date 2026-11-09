package vm

import (
	"dex/keys"
	"dex/logs"
	"encoding/json"
	"fmt"
)

                                       
  
                                                                                                              
                                                                                            
                                       
                                             
  
                                                                                                        
                                                                                      

                                      
const (
	ActKindBlockRewardProposer = "BLOCK_REWARD_PROPOSER"                                     
	ActKindBlockRewardActive   = "BLOCK_REWARD_ACTIVE"                                        
	ActKindSlashPenalty        = "SLASH_PENALTY"                                        
	ActKindSlashDividend       = "SLASH_DIVIDEND"                                
	ActKindKicked              = "KICKED"                                     
	ActKindMinerExitUnlock     = "MINER_EXIT_UNLOCK"                       
	ActKindLiquidation         = "LIQUIDATION"                             
	ActKindADL                 = "ADL"                                                       
	ActKindInsuredDeleverage   = "INSURED_DELEVERAGE"                                                    
	ActKindFundingSettle       = "FUNDING_SETTLE"                          
	ActKindDKGDisqualified     = "DKG_DISQUALIFIED"                            
	ActKindTransitionJoin      = "TRANSITION_JOIN"                                  
	ActKindRechargeFinalized   = "RECHARGE_FINALIZED"                         
	ActKindRechargeRejected    = "RECHARGE_REJECTED"                          
	ActKindFeeRefund           = "FEE_REFUND"                                                          
	ActKindRentConversion      = "RENT_CONVERSION"                                              
	ActKindRentSweep           = "RENT_SWEEP"                                                   
)

                                                                  
type UserActivityRecord struct {
	Kind   string `json:"kind"`
	Height uint64 `json:"height"`
	TxID   string `json:"tx_id,omitempty"`
	Amount string `json:"amount,omitempty"`                                                               
	Token  string `json:"token,omitempty"`
	Ref    string `json:"ref,omitempty"`                                                                   
	Note   string `json:"note,omitempty"`                
}

type userActivitySeqAllocator interface {
	NextUserActivitySeq(prefix string) (uint32, error)
}

func nextUserActivitySeq(sv StateView, addr string, height uint64) (uint32, error) {
	prefix := keys.KeyUserActivityHeightPrefix(addr, height)
	if allocator, ok := sv.(userActivitySeqAllocator); ok {
		return allocator.NextUserActivitySeq(prefix)
	}
	existing, err := sv.Scan(prefix)
	if err != nil {
		return 0, fmt.Errorf("scan user activity seq failed: %w", err)
	}
	return uint32(len(existing)), nil
}

                                                                               
                                                                               
                                   
func writeUserActivity(sv StateView, addr string, height uint64, seq uint32, data []byte) {
	sv.Set(keys.KeyUserActivity(addr, height, seq), data)
}

                                                                                                          
func recordUserActivity(sv StateView, addr string, rec *UserActivityRecord) {
	if addr == "" || rec == nil {
		return
	}
	data, err := json.Marshal(rec)
	if err != nil {
		logs.Warn("[UserAct] marshal failed: %v", err)
		return
	}
	seq, err := nextUserActivitySeq(sv, addr, rec.Height)
	if err != nil {
		logs.Warn("[UserAct] allocate seq failed: %v", err)
		return
	}
	writeUserActivity(sv, addr, rec.Height, seq, data)
}
