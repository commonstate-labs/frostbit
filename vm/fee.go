package vm

import (
	"dex/protocol"
	"fmt"
	"math/big"
)

                                              

const FeeToken = "FB"
const RuntimeFeePoolAccount = "__runtime_fee_pool__"

                                                                                                     
var MinTxFee = big.NewInt(protocol.MinRuntimeFeeRaw)

                                           
                                                                         
                                                                                                         
                                                      
                                                                    
func DeductFee(sv StateView, fromAddr, feeStr string) (WriteOp, string, error) {
	fee, err := parseBalanceStrict("tx fee", feeStr)
	if err != nil || fee.Cmp(MinTxFee) < 0 {
		return WriteOp{}, "", fmt.Errorf("fee must be >= %s, got %s", MinTxFee.String(), feeStr)
	}

	fbBal := GetBalance(sv, fromAddr, FeeToken)
	balance, err := parseBalanceStrict("sender FB balance", fbBal.Balance)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("invalid sender FB balance: %w", err)
	}
	if balance.Cmp(fee) < 0 {
		return WriteOp{}, "", fmt.Errorf("insufficient FB for fee: has %s, need %s", balance.String(), fee.String())
	}

	newBal, err := SafeSub(balance, fee)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("fee deduction underflow: %w", err)
	}
	fbBal.Balance = newBal.String()
	SetBalance(sv, fromAddr, FeeToken, fbBal)
	feePool := GetBalance(sv, RuntimeFeePoolAccount, FeeToken)
	poolBal, err := parseBalanceStrict("runtime fee pool FB balance", feePool.Balance)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("invalid runtime fee pool FB balance: %w", err)
	}
	poolBal, err = SafeAdd(poolBal, fee)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("fee pool overflow: %w", err)
	}
	feePool.Balance = poolBal.String()
	SetBalance(sv, RuntimeFeePoolAccount, FeeToken, feePool)

	                                      
	return balanceWriteOp(sv, fromAddr, FeeToken), fee.String(), nil
}

func RefundFee(sv StateView, fromAddr, feeStr string) (WriteOp, string, error) {
	fee, err := parseBalanceStrict("tx fee refund", feeStr)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("invalid tx fee refund: %w", err)
	}
	if fee.Sign() == 0 {
		return WriteOp{}, "0", nil
	}

	fbBal := GetBalance(sv, fromAddr, FeeToken)
	balance, err := parseBalanceStrict("sender FB balance", fbBal.Balance)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("invalid sender FB balance: %w", err)
	}
	newBal, err := SafeAdd(balance, fee)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("fee refund overflow: %w", err)
	}
	feePool := GetBalance(sv, RuntimeFeePoolAccount, FeeToken)
	poolBal, err := parseBalanceStrict("runtime fee pool FB balance", feePool.Balance)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("invalid runtime fee pool FB balance: %w", err)
	}
	if poolBal.Cmp(fee) < 0 {
		return WriteOp{}, "", fmt.Errorf("insufficient runtime fee pool for refund: has %s, need %s", poolBal.String(), fee.String())
	}
	poolBal, err = SafeSub(poolBal, fee)
	if err != nil {
		return WriteOp{}, "", fmt.Errorf("fee pool refund underflow: %w", err)
	}
	fbBal.Balance = newBal.String()
	SetBalance(sv, fromAddr, FeeToken, fbBal)
	feePool.Balance = poolBal.String()
	SetBalance(sv, RuntimeFeePoolAccount, FeeToken, feePool)

	return balanceWriteOp(sv, fromAddr, FeeToken), fee.String(), nil
}

func SpendRuntimeFeePool(sv StateView, amount *big.Int) error {
	if amount == nil || amount.Sign() == 0 {
		return nil
	}
	feePool := GetBalance(sv, RuntimeFeePoolAccount, FeeToken)
	poolBal, err := parseBalanceStrict("runtime fee pool FB balance", feePool.Balance)
	if err != nil {
		return fmt.Errorf("invalid runtime fee pool FB balance: %w", err)
	}
	if poolBal.Cmp(amount) < 0 {
		return fmt.Errorf("insufficient runtime fee pool: has %s, need %s", poolBal.String(), amount.String())
	}
	poolBal, err = SafeSub(poolBal, amount)
	if err != nil {
		return fmt.Errorf("fee pool spend underflow: %w", err)
	}
	feePool.Balance = poolBal.String()
	SetBalance(sv, RuntimeFeePoolAccount, FeeToken, feePool)
	return nil
}
