package vm

import (
	"fmt"

	"dex/keys"
	"dex/pb"
	"dex/rent"

	"google.golang.org/protobuf/proto"
)

func zeroTokenBalance() *pb.TokenBalance {
	return &pb.TokenBalance{
		Balance:               "0",
		MinerLockedBalance:    "0",
		LiquidLockedBalance:   "0",
		WitnessLockedBalance:  "0",
		LeverageLockedBalance: "0",
		OrderFrozenBalance:    "0",
	}
}

                                               
                           
                                                          
                                               

                                               
                                                      
func GetBalance(sv StateView, addr, token string) *pb.TokenBalance {
	key := keys.KeyBalance(addr, token)
	data, exists, err := sv.Get(key)
	if err != nil || !exists || len(data) == 0 {
		return zeroTokenBalance()
	}

	var record pb.TokenBalanceRecord
	if err := unmarshalProtoCompat(data, &record); err != nil {
		return zeroTokenBalance()
	}

	if record.Balance == nil {
		return zeroTokenBalance()
	}

	return record.Balance
}

                                                                             
                                                                               
                                                                                
                                
func getBalanceStrict(sv StateView, addr, token string) (*pb.TokenBalance, error) {
	key := keys.KeyBalance(addr, token)
	data, exists, err := sv.Get(key)
	if err != nil {
		return nil, fmt.Errorf("read balance %s/%s: %w", addr, token, err)
	}
	if !exists {
		return zeroTokenBalance(), nil
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("decode balance %s/%s: empty record", addr, token)
	}

	var record pb.TokenBalanceRecord
	if err := unmarshalProtoCompat(data, &record); err != nil {
		return nil, fmt.Errorf("decode balance %s/%s: %w", addr, token, err)
	}
	if record.Balance == nil {
		return nil, fmt.Errorf("decode balance %s/%s: missing balance payload", addr, token)
	}
	return record.Balance, nil
}

                                               
func SetBalance(sv StateView, addr, token string, balance *pb.TokenBalance) {
	key := keys.KeyBalance(addr, token)
	if balance == nil || rent.TokenBalanceTotal(balance).Sign() == 0 {
		sv.Del(key)
		return
	}
	record := &pb.TokenBalanceRecord{
		Address: addr,
		Token:   token,
		Balance: balance,
	}
	data, _ := proto.Marshal(record)
	sv.Set(key, data)
	if token != rent.FeeToken && rent.IsRentManagedAccount(addr) {
		rent.MarkOwnerActivity(sv, addr, rent.OwnerMarkerNonFBBalance)
	}
}

func balanceWriteOp(sv StateView, addr, token string) WriteOp {
	key := keys.KeyBalance(addr, token)
	data, exists, _ := sv.Get(key)
	if !exists {
		return WriteOp{Key: key, Del: true, Category: "balance"}
	}
	return WriteOp{Key: key, Value: data, Category: "balance"}
}

                                                                      
                                             
func GetBalanceOrCreate(sv StateView, addr, token string) (*pb.TokenBalance, bool) {
	key := keys.KeyBalance(addr, token)
	data, exists, err := sv.Get(key)
	if err != nil || !exists || len(data) == 0 {
		return zeroTokenBalance(), true
	}

	var record pb.TokenBalanceRecord
	if err := unmarshalProtoCompat(data, &record); err != nil {
		return zeroTokenBalance(), true
	}

	if record.Balance == nil {
		return zeroTokenBalance(), true
	}

	return record.Balance, false
}

                                                           
                                    
func EnsureBalance(sv StateView, addr, token string) *pb.TokenBalance {
	bal, isNew := GetBalanceOrCreate(sv, addr, token)
	if isNew {
		SetBalance(sv, addr, token, bal)
	}
	return bal
}
