package vm

import (
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/rent"

	"google.golang.org/protobuf/proto"
)

type keyedReadErrorStateView struct {
	StateView
	key string
}

func (s keyedReadErrorStateView) Get(key string) ([]byte, bool, error) {
	if key == s.key {
		return nil, false, errors.New("injected state read failure")
	}
	return s.StateView.Get(key)
}

                                      
                                                       
                                                                                      
                                                        
                                                              
                                                                     
                                              
  
                                                                                                       

func seedTransferAccount(t *testing.T, sv *MockStateView, addr string) {
	t.Helper()
	acc := &pb.Account{Address: addr}
	data, _ := proto.Marshal(acc)
	sv.Set(keys.KeyAccount(addr), data)
}

func seedTransferBalance(t *testing.T, sv *MockStateView, addr, token, balance string) {
	t.Helper()
	rec := &pb.TokenBalanceRecord{
		Address: addr, Token: token,
		Balance: &pb.TokenBalance{Balance: balance},
	}
	data, _ := proto.Marshal(rec)
	sv.Set(keys.KeyBalance(addr, token), data)
}

func buildTransferTx(from, to, token, amount, txID string) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base:         &pb.BaseMessage{TxId: txID, FromAddress: from},
				To:           to,
				TokenAddress: token,
				Amount:       amount,
			},
		},
	}
}

func seedTransferRentState(t *testing.T, sv *MockStateView, state rent.RentState) {
	t.Helper()
	data, err := json.Marshal(&state)
	if err != nil {
		t.Fatalf("marshal rent state: %v", err)
	}
	sv.Set(keys.KeyRentState(state.Owner), data)
}

                                                                   
func TestTransferHandler_Happy_BalancesMove(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "0xA", "0xB", "TOK"
	seedTransferAccount(t, sv, from)
	seedTransferAccount(t, sv, to)
	seedTransferBalance(t, sv, from, tok, "1000")
	seedTransferBalance(t, sv, to, tok, "200")

	h := &TransferTxHandler{}
	ops, r, err := h.DryRun(buildTransferTx(from, to, tok, "300", "tx1"), sv)
	if err != nil || r.Status != "SUCCEED" {
		t.Fatalf("transfer 失败：%+v err=%v", r, err)
	}

	                            
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}

	if got := GetBalance(sv, from, tok).Balance; got != "700" {
		t.Fatalf("from 余额应为 700，实得 %s", got)
	}
	if got := GetBalance(sv, to, tok).Balance; got != "500" {
		t.Fatalf("to 余额应为 500，实得 %s", got)
	}
}

                                                                      
                                                                          
func TestTransferHandler_FrozenSender_Rejected(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "0xA", "0xB", "TOK"
	seedTransferAccount(t, sv, from)
	seedTransferBalance(t, sv, from, tok, "1000")
	sv.Set(keys.KeyFreeze(from, tok), []byte("true"))

	h := &TransferTxHandler{}
	_, r, err := h.DryRun(buildTransferTx(from, to, tok, "1", "tx_frozen"), sv)
	if err == nil {
		t.Fatal("冻结账户转账必须拒绝")
	}
	if r == nil || r.Status != "FAILED" ||
		r.Error != "sender account is frozen for this token" {
		t.Fatalf("expected frozen sender rejection, got %+v", r)
	}
	                     
	if got := GetBalance(sv, from, tok).Balance; got != "1000" {
		t.Fatalf("余额应保持 1000，实得 %s", got)
	}
}

                                                                    
func TestTransferHandler_InsufficientBalance_Rejected(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "0xA", "0xB", "TOK"
	seedTransferAccount(t, sv, from)
	seedTransferBalance(t, sv, from, tok, "50")

	h := &TransferTxHandler{}
	_, r, err := h.DryRun(buildTransferTx(from, to, tok, "100", "tx_poor"), sv)
	if err == nil {
		t.Fatal("余额不足必须拒绝")
	}
	if r == nil || r.Status != "FAILED" {
		t.Fatalf("expected FAILED, got %+v", r)
	}
	if got := GetBalance(sv, from, tok).Balance; got != "50" {
		t.Fatalf("拒绝后余额应保持 50，实得 %s", got)
	}
}

                                                      
func TestTransferHandler_SenderNotFound_Rejected(t *testing.T) {
	sv := NewMockStateView()
	h := &TransferTxHandler{}
	_, r, err := h.DryRun(buildTransferTx("0xGHOST", "0xB", "TOK", "1", "tx_ghost"), sv)
	if err == nil {
		t.Fatal("sender 账户不存在必须拒绝")
	}
	if r == nil || r.Status != "FAILED" || r.Error != "sender account not found" {
		t.Fatalf("expected sender not found, got %+v", r)
	}
}

func TestTransferHandler_ReceiverAccountReadFailureDoesNotWriteInit(t *testing.T) {
	base := newMemStateView()
	from := "fbc1qreaderrorsender0000000000000000000000"
	to := "fbc1qreaderrorreceiver00000000000000000000"
	accountData, err := MarshalProto(&pb.Account{Address: from})
	if err != nil {
		t.Fatal(err)
	}
	base.Set(keys.KeyAccount(from), accountData)
	SetBalance(base, from, rent.FeeToken, &pb.TokenBalance{Balance: "1000"})
	sv := keyedReadErrorStateView{StateView: base, key: keys.KeyAccount(to)}

	_, receipt, err := (&TransferTxHandler{}).DryRun(
		buildTransferTx(from, to, rent.FeeToken, "1", "tx_receiver_read_error"), sv,
	)
	if err == nil {
		t.Fatal("receiver account read failure must reject the transfer")
	}
	if receipt != nil {
		t.Fatalf("storage failure must not be recorded as a business receipt: %+v", receipt)
	}
	if _, exists, getErr := base.Get(keys.KeyRentOwnerMarker(to, rent.OwnerMarkerInit)); getErr != nil {
		t.Fatal(getErr)
	} else if exists {
		t.Fatal("receiver read failure wrote a complete owner marker")
	}
	if got := GetBalance(base, from, rent.FeeToken).Balance; got != "1000" {
		t.Fatalf("sender balance changed on rejected transfer: %s", got)
	}
}

                                                    
                                                                                              
                                                                      
func TestTransferHandler_InvalidAmount_Rejected(t *testing.T) {
	cases := []struct {
		name   string
		amount string
	}{
		{"zero", "0"},
		{"zero_decimal_truncated", "0.5"},                                     
		{"negative", "-1"},
		{"empty", ""},
		{"gibberish", "abc"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			sv := NewMockStateView()
			from, to, tok := "0xA", "0xB", "TOK"
			seedTransferAccount(t, sv, from)
			seedTransferBalance(t, sv, from, tok, "1000")

			h := &TransferTxHandler{}
			_, r, err := h.DryRun(buildTransferTx(from, to, tok, c.amount, "tx_"+c.name), sv)
			if err == nil {
				t.Fatalf("金额=%q 必须拒绝", c.amount)
			}
			if r == nil || r.Status != "FAILED" || r.Error != "invalid transfer amount" {
				t.Fatalf("expected invalid transfer amount, got %+v", r)
			}
			if got := GetBalance(sv, from, tok).Balance; got != "1000" {
				t.Fatalf("拒绝后 from 余额应保持 1000，实得 %s", got)
			}
		})
	}
}

                                                                                           
                                                                                              
                                                                                                      
func TestTransferHandler_SelfTransfer_BalanceInvariant(t *testing.T) {
	sv := NewMockStateView()
	addr, tok := "0xSELF", "TOK"
	seedTransferAccount(t, sv, addr)
	seedTransferBalance(t, sv, addr, tok, "1000")

	h := &TransferTxHandler{}
	ops, r, err := h.DryRun(buildTransferTx(addr, addr, tok, "300", "tx_self"), sv)
	if err != nil || r.Status != "SUCCEED" {
		t.Fatalf("自转应允许，实得 %+v err=%v", r, err)
	}
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
	if got := GetBalance(sv, addr, tok).Balance; got != "1000" {
		t.Fatalf("自转后余额必须不变，实得 %s", got)
	}
}

                                                                                         
                                                                                    
func TestTransferHandler_ReceiverAutoCreated(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "0xA", "0xNEW", "TOK"
	seedTransferAccount(t, sv, from)
	seedTransferBalance(t, sv, from, tok, "100")
	                           

	h := &TransferTxHandler{}
	ops, r, err := h.DryRun(buildTransferTx(from, to, tok, "40", "tx_create_to"), sv)
	if err != nil || r.Status != "SUCCEED" {
		t.Fatalf("接收方不存在时应自动创建，实得 %+v err=%v", r, err)
	}
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
	                                                       
	accData, exists, _ := sv.Get(keys.KeyAccount(to))
	if !exists {
		t.Fatal("to 账户应被自动创建")
	}
	var acc pb.Account
	if err := proto.Unmarshal(accData, &acc); err != nil {
		t.Fatalf("parse to acc: %v", err)
	}
	if acc.Address != to {
		t.Fatalf("to 账户 Address 字段应 = %s，实得 %s", to, acc.Address)
	}
	if got := GetBalance(sv, to, tok).Balance; got != "40" {
		t.Fatalf("to 余额应为 40，实得 %s", got)
	}
}

                                                                                                               
                                                                                        
func TestTransferHandler_RentActivationForNewFBCReceiver(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "fbc1qfrom", "fbc1qnew", "USDT"
	seedTransferAccount(t, sv, from)
	seedTransferBalance(t, sv, from, tok, "100")
	seedTransferBalance(t, sv, from, rent.FeeToken, "200000000")

	h := &TransferTxHandler{}
	ops, r, err := h.DryRun(buildTransferTx(from, to, tok, "40", "tx_rent_activate"), sv)
	if err != nil || r.Status != "SUCCEED" {
		t.Fatalf("rent activation transfer failed: %+v err=%v", r, err)
	}
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
	if got := GetBalance(sv, from, rent.FeeToken).Balance; got != "199000000" {
		t.Fatalf("from FB should pay activation fee, got %s", got)
	}
	if got := GetBalance(sv, to, rent.FeeToken).Balance; got != rent.DefaultActivationFeeRaw {
		t.Fatalf("to FB activation balance mismatch: %s", got)
	}
	if got := GetBalance(sv, to, tok).Balance; got != "40" {
		t.Fatalf("to token balance mismatch: %s", got)
	}
}

func TestTransferHandler_RentActivationInsufficientFBRejected(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "fbc1qfrom", "fbc1qnew", "USDT"
	seedTransferAccount(t, sv, from)
	seedTransferBalance(t, sv, from, tok, "100")
	seedTransferBalance(t, sv, from, rent.FeeToken, "1")

	h := &TransferTxHandler{}
	_, r, err := h.DryRun(buildTransferTx(from, to, tok, "40", "tx_rent_activate_poor"), sv)
	if err == nil || r.Status != "FAILED" {
		t.Fatalf("expected activation fee failure, receipt=%+v err=%v", r, err)
	}
	if got := GetBalance(sv, to, tok).Balance; got != "0" {
		t.Fatalf("failed activation must not write receiver token balance, got %s", got)
	}
}

func TestTransferHandler_SweepingReceiverRejectedBeforeBalanceWrites(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "fbc1qsender", "fbc1qsweeping", "USDT"
	seedTransferAccount(t, sv, from)
	seedTransferAccount(t, sv, to)
	seedTransferBalance(t, sv, from, tok, "1000")
	seedTransferBalance(t, sv, to, tok, "25")
	seedTransferBalance(t, sv, from, rent.FeeToken, "200000000")
	seedTransferBalance(t, sv, to, rent.FeeToken, "7")
	seedTransferRentState(t, sv, rent.RentState{
		Version:           rent.RentStateVersion,
		Owner:             to,
		LastAccruedHeight: 10,
		UnpaidFB:          "100",
		Mode:              rent.RentModeSweeping,
		ExpireHeight:      10,
		NominalFB:         "7",
		Work: &rent.RentWork{
			NextActionHeight: 11,
			Phase:            rent.RentWorkSweepBalances,
		},
	})

	ops, receipt, err := (&TransferTxHandler{}).DryRun(
		buildTransferTx(from, to, tok, "100", "tx_sweeping_receiver"), sv,
	)
	if err == nil {
		t.Fatal("sweeping receiver must reject an ordinary transfer")
	}
	if len(ops) != 0 {
		t.Fatalf("rejected transfer must not return business writes, got %d", len(ops))
	}
	if receipt == nil || receipt.Status != "FAILED" || receipt.Error != "receiver account is being swept for unpaid rent" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if got := GetBalance(sv, from, tok).Balance; got != "1000" {
		t.Fatalf("sender token balance changed before rejection: %s", got)
	}
	if got := GetBalance(sv, to, tok).Balance; got != "25" {
		t.Fatalf("receiver token balance changed before rejection: %s", got)
	}
	if got := GetBalance(sv, from, rent.FeeToken).Balance; got != "200000000" {
		t.Fatalf("sender FB activation balance changed before rejection: %s", got)
	}
	if got := GetBalance(sv, to, rent.FeeToken).Balance; got != "7" {
		t.Fatalf("receiver FB balance changed before rejection: %s", got)
	}
}

func TestTransferHandler_NeedsFundingReceiverCanReceiveFB(t *testing.T) {
	sv := NewMockStateView()
	from, to := "fbc1qsender", "fbc1qneedsfunding"
	seedTransferAccount(t, sv, from)
	seedTransferAccount(t, sv, to)
	seedTransferBalance(t, sv, from, rent.FeeToken, "1000")
	seedTransferBalance(t, sv, to, rent.FeeToken, "0")
	seedTransferRentState(t, sv, rent.RentState{
		Version:           rent.RentStateVersion,
		Owner:             to,
		LastAccruedHeight: 20,
		UnpaidFB:          "500",
		Mode:              rent.RentModeNeedsFunding,
		ExpireHeight:      20,
		NominalFB:         "0",
	})

	_, receipt, err := (&TransferTxHandler{}).DryRun(
		buildTransferTx(from, to, rent.FeeToken, "600", "tx_fund_receiver"), sv,
	)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("needs-funding receiver must remain creditable: receipt=%+v err=%v", receipt, err)
	}
	if got := GetBalance(sv, from, rent.FeeToken).Balance; got != "400" {
		t.Fatalf("sender FB balance mismatch: %s", got)
	}
	if got := GetBalance(sv, to, rent.FeeToken).Balance; got != "600" {
		t.Fatalf("receiver FB balance mismatch: %s", got)
	}
}

func TestTransferHandler_DeferredNeedsFundingReceiverCanReceiveNonFB(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "fbc1qsender", "fbc1qdeferred", "USDT"
	seedTransferAccount(t, sv, from)
	seedTransferAccount(t, sv, to)
	seedTransferBalance(t, sv, from, tok, "1000")
	seedTransferBalance(t, sv, from, rent.FeeToken, "200000000")
	seedTransferRentState(t, sv, rent.RentState{
		Version:           rent.RentStateVersion,
		Owner:             to,
		LastAccruedHeight: 30,
		UnpaidFB:          "500",
		Mode:              rent.RentModeNeedsFunding,
		ExpireHeight:      30,
		NominalFB:         "0",
		Work: &rent.RentWork{
			NextActionHeight: 31,
			Phase:            rent.RentWorkFunding,
			Cursor:           "page-2",
		},
	})

	_, receipt, err := (&TransferTxHandler{}).DryRun(
		buildTransferTx(from, to, tok, "100", "tx_deferred_receiver"), sv,
	)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("deferred needs-funding receiver must remain creditable: receipt=%+v err=%v", receipt, err)
	}
	if got := GetBalance(sv, from, tok).Balance; got != "900" {
		t.Fatalf("sender token balance mismatch: %s", got)
	}
	if got := GetBalance(sv, to, tok).Balance; got != "100" {
		t.Fatalf("receiver token balance mismatch: %s", got)
	}
	if got := GetBalance(sv, from, rent.FeeToken).Balance; got != "199000000" {
		t.Fatalf("sender activation FB mismatch: %s", got)
	}
	if got := GetBalance(sv, to, rent.FeeToken).Balance; got != rent.DefaultActivationFeeRaw {
		t.Fatalf("receiver activation FB mismatch: %s", got)
	}
}

func TestTransferHandler_ReceiverOverflow_Rejected(t *testing.T) {
	sv := NewMockStateView()
	from, to, tok := "0xA", "0xFULL", "TOK"
	seedTransferAccount(t, sv, from)
	seedTransferAccount(t, sv, to)

	maxMinus1 := new(big.Int).Sub(MaxUint256, big.NewInt(1))
	seedTransferBalance(t, sv, from, tok, "1000")
	seedTransferBalance(t, sv, to, tok, maxMinus1.String())

	h := &TransferTxHandler{}
	                                                                           
	_, r, err := h.DryRun(buildTransferTx(from, to, tok, "2", "tx_over"), sv)
	if err == nil {
		t.Fatal("接收方溢出必须拒绝")
	}
	if r == nil || r.Status != "FAILED" || r.Error != "balance overflow" {
		t.Fatalf("expected balance overflow, got %+v", r)
	}
}

                         
func TestTransferHandler_WrongContentType_Rejected(t *testing.T) {
	sv := NewMockStateView()
	h := &TransferTxHandler{}
	tx := &pb.AnyTx{Content: &pb.AnyTx_FreezeTx{
		FreezeTx: &pb.FreezeTx{Base: &pb.BaseMessage{TxId: "mis"}},
	}}
	_, r, err := h.DryRun(tx, sv)
	if err == nil {
		t.Fatal("错误 content type 必须拒绝")
	}
	if r == nil || r.Status != "FAILED" || r.Error != "not a transfer transaction" {
		t.Fatalf("expected misroute rejection, got %+v", r)
	}
}
