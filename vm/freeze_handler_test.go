package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                    
                                                                                      
                                                                                                         
                                             
                                
                                                                       
                         

func seedFreezableToken(t *testing.T, sv *MockStateView, tokenAddr, owner string,
	canFreeze bool, tokenType pb.TokenType) {
	t.Helper()
	tok := &pb.Token{
		Address:   tokenAddr,
		Owner:     owner,
		CanFreeze: canFreeze,
		Type:      tokenType,
	}
	data, err := proto.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	sv.Set(keys.KeyToken(tokenAddr), data)
}

func buildFreezeTx(sender, tokenAddr, target string, freeze bool, txID string) *pb.AnyTx {
	return &pb.AnyTx{
		Content: &pb.AnyTx_FreezeTx{
			FreezeTx: &pb.FreezeTx{
				Base:       &pb.BaseMessage{TxId: txID, FromAddress: sender},
				TokenAddr:  tokenAddr,
				TargetAddr: target,
				Freeze:     freeze,
			},
		},
	}
}

                                                                                             
func TestFreezeHandler_FreezeThenUnfreeze_Symmetric(t *testing.T) {
	sv := NewMockStateView()
	owner, target, tok := "0xOWNER", "0xVICTIM", "TOKFOO"
	seedFreezableToken(t, sv, tok, owner, true, pb.TokenType_TOKEN_NATIVE)

	h := &FreezeTxHandler{}

	         
	ops, r, err := h.DryRun(buildFreezeTx(owner, tok, target, true, "tx1"), sv)
	if err != nil || r.Status != "SUCCEED" {
		t.Fatalf("freeze 失败：%+v err=%v", r, err)
	}
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
	if !IsFrozen(sv, target, tok) {
		t.Fatal("IsFrozen 应为 true")
	}

	           
	ops, r, err = h.DryRun(buildFreezeTx(owner, tok, target, false, "tx2"), sv)
	if err != nil || r.Status != "SUCCEED" {
		t.Fatalf("unfreeze 失败：%+v err=%v", r, err)
	}
	sawFreezeDel := false
	for _, op := range ops {
		if op.Key == keys.KeyFreeze(target, tok) && op.Del {
			sawFreezeDel = true
		}
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, op.Value)
		}
	}
	if !sawFreezeDel {
		t.Fatal("unfreeze 必须产出冻结 key 的 Del 操作（对称语义）")
	}
	if IsFrozen(sv, target, tok) {
		t.Fatal("unfreeze 后 IsFrozen 必须为 false")
	}
}

                                                                                
                                                                                              
                                                                                                                        
func TestIsFrozen_StrictValueMatch(t *testing.T) {
	sv := NewMockStateView()
	addr, tok := "0xA", "TOK"

	if IsFrozen(sv, addr, tok) {
		t.Fatal("空状态下必须不冻结")
	}
	sv.Set(keys.KeyFreeze(addr, tok), []byte("true"))
	if !IsFrozen(sv, addr, tok) {
		t.Fatal("value=\"true\" 必须冻结")
	}
	sv.Set(keys.KeyFreeze(addr, tok), []byte("1"))
	if IsFrozen(sv, addr, tok) {
		t.Fatal("value=\"1\" 不应被当作冻结")
	}
	sv.Set(keys.KeyFreeze(addr, tok), []byte(""))
	if IsFrozen(sv, addr, tok) {
		t.Fatal("空 value 不应被当作冻结")
	}
}

                                                                       
                                                                                         
func TestFreezeHandler_BridgedToken_Rejected(t *testing.T) {
	sv := NewMockStateView()
	owner, target, tok := "0xOWNER", "0xV", "BTC_WRAPPED"
	seedFreezableToken(t, sv, tok, owner, true, pb.TokenType_TOKEN_BRIDGED)

	h := &FreezeTxHandler{}
	_, r, err := h.DryRun(buildFreezeTx(owner, tok, target, true, "tx_bridged"), sv)
	if err == nil {
		t.Fatal("bridged token 必须拒绝冻结")
	}
	if r == nil || r.Status != "FAILED" || r.Error != "bridged token cannot be frozen" {
		t.Fatalf("expected bridged token rejection, got %+v", r)
	}
}

                                      
func TestFreezeHandler_CanFreezeFalse_Rejected(t *testing.T) {
	sv := NewMockStateView()
	owner, target, tok := "0xOWNER", "0xV", "NOFREEZE"
	seedFreezableToken(t, sv, tok, owner, false, pb.TokenType_TOKEN_NATIVE)

	h := &FreezeTxHandler{}
	_, r, err := h.DryRun(buildFreezeTx(owner, tok, target, true, "tx_nofreeze"), sv)
	if err == nil {
		t.Fatal("can_freeze=false 必须拒绝")
	}
	if r == nil || r.Status != "FAILED" || r.Error != "this token does not support freeze" {
		t.Fatalf("expected can_freeze rejection, got %+v", r)
	}
}

                                                     
                                           
func TestFreezeHandler_NonOwner_Rejected(t *testing.T) {
	sv := NewMockStateView()
	owner, target, tok := "0xOWNER", "0xV", "TOK"
	seedFreezableToken(t, sv, tok, owner, true, pb.TokenType_TOKEN_NATIVE)

	h := &FreezeTxHandler{}
	_, r, err := h.DryRun(buildFreezeTx("0xATTACKER", tok, target, true, "tx_att"), sv)
	if err == nil {
		t.Fatal("非 owner 必须拒绝")
	}
	if r == nil || r.Status != "FAILED" || r.Error != "only token owner can freeze/unfreeze" {
		t.Fatalf("expected owner rejection, got %+v", r)
	}
	                              
	if IsFrozen(sv, target, tok) {
		t.Fatal("冒充 owner 不应写入冻结标记")
	}
}

                              
func TestFreezeHandler_TokenNotFound_Rejected(t *testing.T) {
	sv := NewMockStateView()
	h := &FreezeTxHandler{}
	_, r, err := h.DryRun(buildFreezeTx("0xOWNER", "GHOST", "0xV", true, "tx_ghost"), sv)
	if err == nil {
		t.Fatal("未知 token 必须拒绝")
	}
	if r == nil || r.Status != "FAILED" || r.Error != "token not found" {
		t.Fatalf("expected token not found, got %+v", r)
	}
}

                                                             
func TestFreezeHandler_WrongContentType_Rejected(t *testing.T) {
	sv := NewMockStateView()
	h := &FreezeTxHandler{}
	                                               
	tx := &pb.AnyTx{Content: &pb.AnyTx_Transaction{
		Transaction: &pb.Transaction{Base: &pb.BaseMessage{TxId: "misrouted"}},
	}}
	_, r, err := h.DryRun(tx, sv)
	if err == nil {
		t.Fatal("错误的 content type 必须拒绝")
	}
	if r == nil || r.Status != "FAILED" || r.Error != "not a freeze transaction" {
		t.Fatalf("expected misroute rejection, got %+v", r)
	}
}

func TestFreezeHandler_WriteSetIncludesFreezeState(t *testing.T) {
	sv := NewMockStateView()
	owner, target, tok := "0xOWNER", "0xV", "TOK"
	seedFreezableToken(t, sv, tok, owner, true, pb.TokenType_TOKEN_NATIVE)

	h := &FreezeTxHandler{}
	ops, _, err := h.DryRun(buildFreezeTx(owner, tok, target, true, "tx_hist"), sv)
	if err != nil {
		t.Fatalf("freeze 失败：%v", err)
	}
	freezeKey := keys.KeyFreeze(target, tok)
	sawFreeze := false
	for _, op := range ops {
		if op.Key == freezeKey && !op.Del && string(op.Value) == "true" {
			sawFreeze = true
		}
	}
	if !sawFreeze {
		t.Fatal("缺少 freeze 标记写入（v1_freeze_... = \"true\"）")
	}
}
