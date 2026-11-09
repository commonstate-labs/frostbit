package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"
)

                                       
type BlockRewardParams struct {
	InitialReward    *big.Int
	HalvingInterval  uint64                               
	DecayRate        decimal.Decimal
	MinReward        *big.Int
	BlocksPerYear    uint64
	OtherMinersRatio decimal.Decimal                               
}

                                              
var DefaultBlockRewardParams = BlockRewardParams{
	InitialReward:    big.NewInt(1000000000),            
	HalvingInterval:  31536000,                                                       
	DecayRate:        decimal.NewFromFloat(0.5),                       
	MinReward:        big.NewInt(100000),                       
	BlocksPerYear:    31536000,
	OtherMinersRatio: decimal.NewFromFloat(0.5),                                   
}

                                                         
func CalculateBlockReward(height uint64, params BlockRewardParams) *big.Int {
	if params.BlocksPerYear == 0 {
		params.BlocksPerYear = 31536000
	}

	years := height / params.BlocksPerYear
	if years == 0 {
		return params.InitialReward
	}

	                                               
	rewardDec := decimal.NewFromBigInt(params.InitialReward, 0)
	decayFactor := params.DecayRate.Pow(decimal.NewFromInt(int64(years)))

	finalRewardDec := rewardDec.Mul(decayFactor)
	finalReward := finalRewardDec.BigInt()

	if finalReward.Cmp(params.MinReward) < 0 {
		return params.MinReward
	}

	return finalReward
}

func SplitBlockReward(totalReward *big.Int, otherMinersCount int, ratio decimal.Decimal) (proposerReward, otherMinersTotal, perMiner *big.Int) {
	if totalReward == nil || totalReward.Sign() <= 0 {
		return new(big.Int), new(big.Int), nil
	}
	otherTarget := decimal.NewFromBigInt(totalReward, 0).Mul(ratio).BigInt()
	if otherTarget.Sign() < 0 {
		otherTarget = new(big.Int)
	}
	if otherTarget.Cmp(totalReward) > 0 {
		otherTarget = new(big.Int).Set(totalReward)
	}
	proposerReward = new(big.Int).Sub(totalReward, otherTarget) // safe-math-ok: otherTarget 已夹在 0..totalReward，奖励拆分不下溢
	otherMinersTotal = new(big.Int)
	if otherMinersCount <= 0 || otherTarget.Sign() == 0 {
		proposerReward.Add(proposerReward, otherTarget)
		return proposerReward, otherMinersTotal, nil
	}
	perMiner = new(big.Int).Div(otherTarget, big.NewInt(int64(otherMinersCount)))
	if perMiner.Sign() == 0 {
		proposerReward.Add(proposerReward, otherTarget)
		return proposerReward, otherMinersTotal, nil
	}
	otherMinersTotal.Mul(perMiner, big.NewInt(int64(otherMinersCount)))
	remainder := new(big.Int).Sub(otherTarget, otherMinersTotal) // safe-math-ok: 每人份额先向下取整，分配总额不超过 otherTarget
	proposerReward.Add(proposerReward, remainder)
	return proposerReward, otherMinersTotal, perMiner
}

                                                                                   
                                                                                         
                                                                      
func ensureAccountExists(sv StateView, addr string) error {
	k := keys.KeyAccount(addr)
	data, exists, err := sv.Get(k)
	if err != nil {
		return fmt.Errorf("read account %s: %w", addr, err)
	}
	var acct pb.Account
	if exists {
		if err := unmarshalProtoCompat(data, &acct); err != nil {
			return fmt.Errorf("decode account %s: %w", addr, err)
		}
	} else {
		acct = pb.Account{Address: addr}
	}
	d, err := MarshalProto(&acct)
	if err != nil {
		return fmt.Errorf("encode account %s: %w", addr, err)
	}
	if !exists {
		rent.MarkOwnerInitialized(sv, addr)
	}
	sv.Set(k, d)
	return nil
}

                                                                                          
                                                                             
func addBalance(sv StateView, addr, token string, amount *big.Int) error {
	bal, err := getBalanceStrict(sv, addr, token)
	if err != nil {
		return err
	}
	cur, err := ParseBalance(bal.Balance)
	if err != nil {
		return fmt.Errorf("parse balance %s/%s: %w", addr, token, err)
	}
	next, err := SafeAdd(cur, amount)
	if err != nil {
		return fmt.Errorf("add balance %s/%s: %w", addr, token, err)
	}
	bal.Balance = next.String()
	SetBalance(sv, addr, token, bal)
	return nil
}
