package vm

import (
	"dex/logs"
	"dex/pb"
	"fmt"
	"math/big"
	"strings"
)

                                                                                
                                                                                       
                                                                                   
func SanityCheckBalanceDiff(diff []WriteOp, height uint64) {
	for _, op := range diff {
		if op.Del || !strings.Contains(op.Key, "balance_") {
			continue
		}
		if len(op.Value) == 0 {
			continue
		}

		var record pb.TokenBalanceRecord
		if err := unmarshalProtoCompat(op.Value, &record); err != nil {
			continue                               
		}
		bal := record.Balance
		if bal == nil {
			continue
		}

		checks := []struct {
			name string
			val  string
		}{
			{"Balance", bal.Balance},
			{"OrderFrozenBalance", bal.OrderFrozenBalance},
			{"MinerLockedBalance", bal.MinerLockedBalance},
			{"WitnessLockedBalance", bal.WitnessLockedBalance},
			{"LiquidLockedBalance", bal.LiquidLockedBalance},
			{"LeverageLockedBalance", bal.LeverageLockedBalance},
		}

		for _, c := range checks {
			if c.val == "" || c.val == "0" {
				continue
			}
			v, ok := new(big.Int).SetString(c.val, 10)
			if !ok {
				logs.Warn("[VM][SanityCheck] height=%d key=%s field=%s unparseable value=%q", height, op.Key, c.name, c.val)
				continue
			}
			if v.Sign() < 0 {
				panic(fmt.Sprintf("[VM][SanityCheck] NEGATIVE BALANCE DETECTED! height=%d key=%s field=%s value=%s", height, op.Key, c.name, c.val))
			}
		}
	}
}
