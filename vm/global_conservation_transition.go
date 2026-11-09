package vm

import (
	"dex/keys"
	"fmt"
	"math/big"
	"strings"
)

                                                                            
                                                                             
type conservationAuditOpKind uint8

const (
	conservationAuditOther conservationAuditOpKind = iota
	conservationAuditToken
	conservationAuditBalance
	conservationAuditPosition
	conservationAuditInsurance
	conservationAuditChallenge
)

                                                                           
                                                                              
                                             
type conservationAuditOpPlan struct {
	kind          conservationAuditOpKind
	token         string
	needsOldValue bool
}

                                                                              
                                                                              
                                                                         
type conservationAuditTransitionInput struct {
	Key          string
	NewValue     []byte
	OldValue     []byte
	FutureQuotes map[string]string
	plan         conservationAuditOpPlan
}

                                                                          
                                                                               
                                                        
type conservationAuditContribution struct {
	token  string
	source string
	key    string
	delta  *big.Int
}

                                                                             
                                                                              
type conservationAuditTransitionResult struct {
	contributions        []conservationAuditContribution
	invalidBalanceRecord string
}

                                                                           
                                                                               
                         
func planConservationAuditOp(key string, futureQuotes map[string]string) conservationAuditOpPlan {
	switch {
	case strings.HasPrefix(key, keys.KeyTokenPrefix()) && key != keys.KeyTokenRegistry():
		token := strings.TrimPrefix(key, keys.KeyTokenPrefix())
		return conservationAuditOpPlan{
			kind:          conservationAuditToken,
			token:         token,
			needsOldValue: token != "",
		}
	case strings.Contains(key, "balance_"):
		return conservationAuditOpPlan{kind: conservationAuditBalance, needsOldValue: true}
	case strings.HasPrefix(key, keys.KeyFuturePositionPrefix()):
		return conservationAuditOpPlan{kind: conservationAuditPosition, needsOldValue: true}
	case strings.HasPrefix(key, keys.KeyFutureInsuranceFund("")):
		pair := strings.TrimPrefix(key, keys.KeyFutureInsuranceFund(""))
		quote := futureQuotes[pair]
		return conservationAuditOpPlan{
			kind:          conservationAuditInsurance,
			token:         quote,
			needsOldValue: pair != "" && !strings.HasPrefix(pair, "shortfall_") && quote != "",
		}
	case strings.HasPrefix(key, keys.KeyChallengeRecordPrefix()):
		return conservationAuditOpPlan{
			kind:          conservationAuditChallenge,
			token:         FeeToken,
			needsOldValue: true,
		}
	default:
		return conservationAuditOpPlan{kind: conservationAuditOther}
	}
}

                                                                       
  
                 
                                                                           
                                                                      
  
                  
                                
                                                                      
                                                                    
func evaluateConservationAuditTransition(input conservationAuditTransitionInput) conservationAuditTransitionResult {
	result := conservationAuditTransitionResult{}
	appendContribution := func(token, source string, delta *big.Int) {
		result.contributions = append(result.contributions, conservationAuditContribution{
			token:  token,
			source: source,
			key:    input.Key,
			delta:  delta,
		})
	}

	switch input.plan.kind {
	case conservationAuditToken:
		if input.plan.token == "" {
			return result
		}
		appendContribution(input.plan.token, "supplyAdj", new(big.Int).Neg(tokenSupplyDelta(input.NewValue, input.OldValue)))
	case conservationAuditBalance:
		newRecord, newErr := parseBalanceRecordForAudit(input.NewValue)
		oldRecord, oldErr := parseBalanceRecordForAudit(input.OldValue)
		                                                                        
		                                                                
		if newErr == nil && oldErr == nil && newRecord == nil && oldRecord == nil {
			return result
		}
		token, tokenErr := consistentBalanceRecordToken(newRecord, oldRecord)
		if newErr != nil || oldErr != nil || tokenErr != nil {
			err := newErr
			if err == nil {
				err = oldErr
			}
			if err == nil {
				err = tokenErr
			}
			result.invalidBalanceRecord = fmt.Sprintf("key=%s err=%v", shortenAuditKey(input.Key), err)
			return result
		}
		delta := balanceRecordTotalValue(newRecord)
		delta.Sub(delta, balanceRecordTotalValue(oldRecord))
		appendContribution(token, "balance", delta)
	case conservationAuditPosition:
		newToken, newDelta := positionDeltaWithMarketQuotes(input.NewValue, 1, input.FutureQuotes)
		appendContribution(newToken, "position", newDelta)
		oldToken, oldDelta := positionDeltaWithMarketQuotes(input.OldValue, -1, input.FutureQuotes)
		appendContribution(oldToken, "position", oldDelta)
	case conservationAuditInsurance:
		if !input.plan.needsOldValue {
			return result
		}
		delta := bytesBig(input.NewValue)
		delta.Sub(delta, bytesBig(input.OldValue))
		appendContribution(input.plan.token, "insurance", delta)
	case conservationAuditChallenge:
		delta := challengeEscrowDelta(input.NewValue, 1)
		delta.Add(delta, challengeEscrowDelta(input.OldValue, -1))
		appendContribution(input.plan.token, "challenge", delta)
	}
	return result
}
