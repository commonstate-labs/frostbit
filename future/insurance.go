package future

import (
	"fmt"
	"math/big"
)

                                                                              
func (e *Engine) DepositInsuranceFund(pair string, amount *big.Int) error {
	if amount == nil || amount.Sign() <= 0 {
		return nil
	}
	current, err := e.insurance.GetBalance(pair)
	if err != nil {
		return fmt.Errorf("get insurance balance: %w", err)
	}
	if current == nil {
		current = new(big.Int)
	}
	newBalance := new(big.Int).Add(current, amount) // safe-math-ok: 聚合实际收取的损失或资金费，保留保险池现有任意精度域
	if err := e.insurance.SetBalance(pair, newBalance); err != nil {
		return err
	}
	e.trackRealizedPnLBatchInsuranceInflow(pair, amount)
	return nil
}

                                           
                                                                         
func (e *Engine) WithdrawInsuranceFund(pair string, amount *big.Int) (consumed, shortfall *big.Int, err error) {
	if amount == nil || amount.Sign() <= 0 {
		return new(big.Int), new(big.Int), nil
	}
	current, err := e.insurance.GetBalance(pair)
	if err != nil {
		return nil, nil, fmt.Errorf("get insurance balance: %w", err)
	}
	if current == nil {
		current = new(big.Int)
	}

	if current.Cmp(amount) >= 0 {
		                     
		newBalance := new(big.Int).Sub(current, amount) // safe-math-ok: 仅 current >= amount 分支执行，保险余额不下溢
		if err := e.insurance.SetBalance(pair, newBalance); err != nil {
			return nil, nil, err
		}
		return new(big.Int).Set(amount), new(big.Int), nil
	}

	                                    
	short := new(big.Int).Sub(amount, current) // safe-math-ok: 仅 amount > current 分支计算缺口，实际付款限于 current
	if err := e.insurance.SetBalance(pair, new(big.Int)); err != nil {
		return nil, nil, err
	}
	return new(big.Int).Set(current), short, nil
}
