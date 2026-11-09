package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/referral"
	"dex/utils"
	"fmt"
	"math/big"

	"google.golang.org/protobuf/proto"
)

                                            

                                                                 
type svReferrerStore struct{ sv StateView }

func (s *svReferrerStore) GetReferrer(user string) (string, bool, error) {
	data, exists, err := s.sv.Get(keys.KeyReferrer(user))
	if err != nil || !exists || len(data) == 0 {
		return "", false, err
	}
	return string(data), true, nil
}

func (s *svReferrerStore) SetReferrer(user, referrer string) error {
	s.sv.Set(keys.KeyReferrer(user), []byte(referrer))
	s.sv.Set(keys.KeyReferralInvitee(referrer, user), []byte{1})
	return nil
}

                                                                                   
type svInsuranceFundDepositor struct{ sv StateView }

func (s *svInsuranceFundDepositor) DepositInsuranceFund(pair string, amount *big.Int) error {
	ins := &svInsuranceFund{sv: s.sv}
	bal, _ := ins.GetBalance(pair)
	if bal == nil {
		bal = new(big.Int)
	}
	newBal, err := SafeAdd(bal, amount)
	if err != nil {
		return fmt.Errorf("insurance fund add overflow pair=%s: %w", pair, err)
	}
	return ins.SetBalance(pair, newBal)
}

                                                                    
                                                                                       
                                                                                                 
                                                                                                                 
func newReferralEngine(sv StateView, height uint64) *referral.Engine {
	return referral.NewEngine(
		&svReferrerStore{sv: sv},
		&svBalanceAccessor{sv: sv, height: height},
		&svInsuranceFundDepositor{sv: sv},
		referral.DefaultConfig,
	)
}

                                                                                   
func writeTradingFeeRecord(sv StateView, fr *referral.FeeResult, pair string, notional *big.Int, height uint64, txID string, seq int) {
	rec := &pb.TradingFeeRecord{
		Trader: fr.Trader, Pair: pair, Notional: notional.String(),
		FullFee: fr.FullFee.String(), ActualFee: fr.ActualFee.String(),
		Referrer: fr.Referrer, ReferrerRebate: fr.ReferrerRebate.String(),
		InsuranceAmount: fr.InsuranceAmount.String(),
		Height:          height, TxId: txID,
	}
	if data, err := proto.Marshal(rec); err == nil {
		sv.Set(keys.KeyTradingFeeRecord(fr.Trader, height, txID, seq), data)
		if fr.Referrer != "" && fr.ReferrerRebate != nil && fr.ReferrerRebate.Sign() > 0 {
			sv.Set(keys.KeyReferralRebateRecord(fr.Referrer, height, txID, seq), data)
		}
	}
}

                                             

type SetReferrerTxHandler struct{}

func (h *SetReferrerTxHandler) Kind() string            { return "set_referrer" }
func (h *SetReferrerTxHandler) Apply(_ *pb.AnyTx) error { return ErrNotImplemented }

func (h *SetReferrerTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	stx := tx.GetSetReferrerTx()
	if stx == nil {
		return nil, nil, fmt.Errorf("not a SetReferrerTx")
	}
	base := stx.Base
	from := base.FromAddress
	referrerAddr := stx.ReferrerAddress

	if !utils.IsDexAddress(referrerAddr) {
		err := fmt.Errorf("invalid referrer address")
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: err.Error()}, err
	}

	eng := newReferralEngine(sv, base.ExecutedHeight)
	if err := eng.BindReferrer(from, referrerAddr); err != nil {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: err.Error()}, err
	}

	receipt := &Receipt{
		TxID: base.TxId, Status: "SUCCEED", BlockHeight: base.ExecutedHeight,
		Logs: []string{fmt.Sprintf("set_referrer: %s → %s", from, referrerAddr)},
	}
	return nil, receipt, nil
}
