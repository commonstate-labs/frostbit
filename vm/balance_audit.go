package vm

import (
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"math/big"
	"sort"
	"strings"
)

                                                                    
                                                                                                 
                                                                     
                                                                                                             
                                                                                                                                
                                                                                                                       
                                                                           
                                                                                                                 
                                                                                             
func AuditBalanceDiffConservation(diff []WriteOp, height uint64, readFn ReadThroughFn) {
	type tokenDelta struct {
		netBalance, netFrozen, netMiner, netWitness, netLiquid, netLeverage, netChallenge, netSupply *big.Int
		affectedCnt                                                                                  int
	}
	deltas := make(map[string]*tokenDelta)
	ensure := func(token string) *tokenDelta {
		if d, ok := deltas[token]; ok {
			return d
		}
		d := &tokenDelta{
			netBalance: new(big.Int), netFrozen: new(big.Int), netMiner: new(big.Int),
			netWitness: new(big.Int), netLiquid: new(big.Int), netLeverage: new(big.Int),
			netChallenge: new(big.Int), netSupply: new(big.Int),
		}
		deltas[token] = d
		return d
	}

	for _, op := range diff {
		if strings.HasPrefix(op.Key, keys.KeyChallengeRecordPrefix()) {
			newStake := activeChallengeStake(op.Value)
			oldStake := activeChallengeStake(oldValueFromOp(op, readFn))
			d := ensure(FeeToken)
			d.netChallenge.Add(d.netChallenge, new(big.Int).Sub(newStake, oldStake)) // safe-math-ok: 审计累计的是有符号质押变化量，不是资金写入。
			continue
		}
		if strings.HasPrefix(op.Key, keys.KeyTokenPrefix()) && op.Key != keys.KeyTokenRegistry() {
			token := strings.TrimPrefix(op.Key, keys.KeyTokenPrefix())
			if token == "" {
				continue
			}
			var newToken, oldToken pb.Token
			if len(op.Value) > 0 {
				if err := unmarshalProtoCompat(op.Value, &newToken); err != nil {
					continue
				}
			}
			if oldData := oldValueFromOp(op, readFn); len(oldData) > 0 {
				_ = unmarshalProtoCompat(oldData, &oldToken)
			}
			addDelta(ensure(token).netSupply, newToken.TotalSupply, oldToken.TotalSupply)
			continue
		}
		if !strings.Contains(op.Key, "balance_") {
			continue
		}
		newRecord, newErr := parseBalanceRecordForAudit(op.Value)
		oldRecord, oldErr := parseBalanceRecordForAudit(oldValueFromOp(op, readFn))
		if newErr == nil && oldErr == nil && newRecord == nil && oldRecord == nil {
			continue
		}
		token, tokenErr := consistentBalanceRecordToken(newRecord, oldRecord)
		if newErr != nil || oldErr != nil || tokenErr != nil {
			logs.Error("[VM][Audit][BalanceDiff][Diag] height=%d invalid balance record key=%s newErr=%v oldErr=%v tokenErr=%v", height, op.Key, newErr, oldErr, tokenErr)
			continue
		}
		var newBal *pb.TokenBalance
		if newRecord != nil {
			newBal = newRecord.Balance
		}
		if newBal == nil {
			newBal = &pb.TokenBalance{}
		}
		var oldBal *pb.TokenBalance
		if oldRecord != nil {
			oldBal = oldRecord.Balance
		}
		if oldBal == nil {
			oldBal = &pb.TokenBalance{}
		}
		d := ensure(token)
		d.affectedCnt++
		addDelta(d.netBalance, newBal.Balance, oldBal.Balance)
		addDelta(d.netFrozen, newBal.OrderFrozenBalance, oldBal.OrderFrozenBalance)
		addDelta(d.netMiner, newBal.MinerLockedBalance, oldBal.MinerLockedBalance)
		addDelta(d.netWitness, newBal.WitnessLockedBalance, oldBal.WitnessLockedBalance)
		addDelta(d.netLiquid, newBal.LiquidLockedBalance, oldBal.LiquidLockedBalance)
		addDelta(d.netLeverage, newBal.LeverageLockedBalance, oldBal.LeverageLockedBalance)
	}

	tokens := make([]string, 0, len(deltas))
	for t := range deltas {
		tokens = append(tokens, t)
	}
	sort.Strings(tokens)

	strict := StrictAuditEnabled()
	for _, token := range tokens {
		d := deltas[token]
		total := new(big.Int).Add(d.netBalance, d.netFrozen) // safe-math-ok: 各余额桶有符号净变化相加用于诊断守恒。
		total.Add(total, d.netMiner)
		total.Add(total, d.netWitness)
		total.Add(total, d.netLiquid)
		total.Add(total, d.netLeverage)
		total.Add(total, d.netChallenge)
		total.Sub(total, d.netSupply)
		if total.Sign() == 0 {
			continue
		}
		if strict {
			logs.Debug("[VM][Audit][BalanceDiff][Diag] STRICT height=%d token=%s netTotal=%s (balance=%s frozen=%s miner=%s witness=%s challenge=%s supply=%s) accounts=%d — 局部余额桶诊断，硬失败以 GlobalConservationDiff 为准",
				height, token, total.String(),
				d.netBalance.String(), d.netFrozen.String(), d.netMiner.String(), d.netWitness.String(), d.netChallenge.String(), d.netSupply.String(),
				d.affectedCnt)
			logs.Debug("[VM][Audit][BalanceDiff][Buckets] height=%d token=%s liquid=%s leverage=%s", height, token, d.netLiquid.String(), d.netLeverage.String())
		} else {
			logs.Info("[VM][Audit][BalanceDiff] height=%d token=%s netTotal=%s (balance=%s frozen=%s miner=%s witness=%s challenge=%s supply=%s) accounts=%d",
				height, token, total.String(),
				d.netBalance.String(), d.netFrozen.String(), d.netMiner.String(), d.netWitness.String(), d.netChallenge.String(), d.netSupply.String(),
				d.affectedCnt)
			logs.Info("[VM][Audit][BalanceDiff][Buckets] height=%d token=%s liquid=%s leverage=%s", height, token, d.netLiquid.String(), d.netLeverage.String())
		}
	}
}

                                                            
func addDelta(acc *big.Int, newValStr, oldValStr string) {
	newV := parseBigOrZero(newValStr)
	oldV := parseBigOrZero(oldValStr)
	diff := new(big.Int).Sub(newV, oldV) // safe-math-ok: 审计差值允许为负，不能替换成无符号 SafeSub。
	acc.Add(acc, diff)
}

func parseBigOrZero(s string) *big.Int {
	if s == "" {
		return new(big.Int)
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return new(big.Int)
	}
	return v
}

                                                                                                       
                                                                                   
func extractTokenFromBalanceKey(key string) string {
	                              
	prefix := "v1_balance_"
	if !strings.HasPrefix(key, prefix) {
		prefix = "balance_"
		if !strings.HasPrefix(key, prefix) {
			return ""
		}
	}
	rest := key[len(prefix):]
	                                                                 
	idx := strings.LastIndex(rest, "_")
	if idx < 0 {
		return ""
	}
	return rest[idx+1:]
}
