package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"

	"google.golang.org/protobuf/proto"
)

                                           
type BurnTokenTxHandler struct{}

func (h *BurnTokenTxHandler) Kind() string { return "burn_token" }

func (h *BurnTokenTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	burnTx := tx.GetBurnTokenTx()
	if burnTx == nil || burnTx.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid burn token tx"}, fmt.Errorf("invalid burn token tx")
	}

	amount, err := ParseBalance(burnTx.Amount)
	if err != nil || amount.Sign() <= 0 {
		return nil, &Receipt{TxID: burnTx.Base.TxId, Status: "FAILED", Error: "invalid amount"}, fmt.Errorf("invalid amount")
	}

	               
	tokenKey := keys.KeyToken(burnTx.TokenAddr)
	tokenData, exists, err := sv.Get(tokenKey)
	if err != nil || !exists {
		return nil, &Receipt{TxID: burnTx.Base.TxId, Status: "FAILED", Error: "token not found"}, fmt.Errorf("token not found: %s", burnTx.TokenAddr)
	}
	var token pb.Token
	if err := unmarshalProtoCompat(tokenData, &token); err != nil {
		return nil, &Receipt{TxID: burnTx.Base.TxId, Status: "FAILED", Error: "parse token failed"}, err
	}

	                            
	if token.Type == pb.TokenType_TOKEN_BRIDGED {
		return nil, &Receipt{TxID: burnTx.Base.TxId, Status: "FAILED", Error: "bridged token cannot be burned"}, fmt.Errorf("bridged token cannot be burned")
	}

	               
	if !token.CanBurn {
		return nil, &Receipt{TxID: burnTx.Base.TxId, Status: "FAILED", Error: "token does not support burning"}, fmt.Errorf("token does not support burning")
	}

	                                                      
	if IsFrozen(sv, burnTx.Base.FromAddress, burnTx.TokenAddr) {
		return nil, &Receipt{TxID: burnTx.Base.TxId, Status: "FAILED", Error: "account is frozen for this token"}, fmt.Errorf("account is frozen for token: %s", burnTx.TokenAddr)
	}

	                           
	bal := GetBalance(sv, burnTx.Base.FromAddress, burnTx.TokenAddr)
	curBal, _ := ParseBalance(bal.Balance)
	newBal, err := SafeSub(curBal, amount)
	if err != nil || newBal.Sign() < 0 {
		return nil, &Receipt{TxID: burnTx.Base.TxId, Status: "FAILED", Error: "insufficient balance"}, fmt.Errorf("insufficient balance to burn")
	}
	bal.Balance = newBal.String()
	SetBalance(sv, burnTx.Base.FromAddress, burnTx.TokenAddr, bal)

	                     
	supply, _ := new(big.Int).SetString(token.TotalSupply, 10)
	if supply == nil {
		supply = big.NewInt(0)
	}
	newSupply, err := SafeSub(supply, amount)
	if err != nil || newSupply.Sign() < 0 {
		return nil, &Receipt{TxID: burnTx.Base.TxId, Status: "FAILED", Error: "burn amount exceeds total supply"}, fmt.Errorf("burn amount exceeds total supply")
	}
	token.TotalSupply = newSupply.String()

	updatedTokenData, _ := proto.Marshal(&token)

	ws := []WriteOp{
		{Key: tokenKey, Value: updatedTokenData, Category: "token"},
		balanceWriteOp(sv, burnTx.Base.FromAddress, burnTx.TokenAddr),
	}
	return ws, &Receipt{TxID: burnTx.Base.TxId, Status: "SUCCEED", WriteCount: len(ws)}, nil
}

func (h *BurnTokenTxHandler) Apply(tx *pb.AnyTx) error { return ErrNotImplemented }
