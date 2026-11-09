package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"math/big"

	"google.golang.org/protobuf/proto"
)

                                           
type MintTokenTxHandler struct{}

func (h *MintTokenTxHandler) Kind() string { return "mint_token" }

func (h *MintTokenTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	mintTx := tx.GetMintTokenTx()
	if mintTx == nil || mintTx.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid mint token tx"}, fmt.Errorf("invalid mint token tx")
	}

	amount, err := ParseBalance(mintTx.Amount)
	if err != nil || amount.Sign() <= 0 {
		return nil, &Receipt{TxID: mintTx.Base.TxId, Status: "FAILED", Error: "invalid amount"}, fmt.Errorf("invalid amount")
	}

	               
	tokenKey := keys.KeyToken(mintTx.TokenAddr)
	tokenData, exists, err := sv.Get(tokenKey)
	if err != nil || !exists {
		return nil, &Receipt{TxID: mintTx.Base.TxId, Status: "FAILED", Error: "token not found"}, fmt.Errorf("token not found: %s", mintTx.TokenAddr)
	}
	var token pb.Token
	if err := unmarshalProtoCompat(tokenData, &token); err != nil {
		return nil, &Receipt{TxID: mintTx.Base.TxId, Status: "FAILED", Error: "parse token failed"}, err
	}

	                            
	if token.Type == pb.TokenType_TOKEN_BRIDGED {
		return nil, &Receipt{TxID: mintTx.Base.TxId, Status: "FAILED", Error: "bridged token cannot be minted"}, fmt.Errorf("bridged token cannot be minted")
	}

	               
	if !token.CanMint {
		return nil, &Receipt{TxID: mintTx.Base.TxId, Status: "FAILED", Error: "token does not support minting"}, fmt.Errorf("token does not support minting")
	}
	if token.Owner != mintTx.Base.FromAddress {
		return nil, &Receipt{TxID: mintTx.Base.TxId, Status: "FAILED", Error: "only owner can mint"}, fmt.Errorf("only owner can mint")
	}

	                  
	to := mintTx.To
	if to == "" {
		to = token.Owner
	}

	                     
	supply, _ := new(big.Int).SetString(token.TotalSupply, 10)
	if supply == nil {
		supply = big.NewInt(0)
	}
	newSupply, err := SafeAdd(supply, amount)
	if err != nil {
		return nil, &Receipt{TxID: mintTx.Base.TxId, Status: "FAILED", Error: "total supply overflow"}, fmt.Errorf("total supply overflow: %w", err)
	}
	token.TotalSupply = newSupply.String()

	updatedTokenData, _ := proto.Marshal(&token)

	                        
	bal := GetBalance(sv, to, mintTx.TokenAddr)
	curBal, _ := ParseBalance(bal.Balance)
	newBal, err := SafeAdd(curBal, amount)
	if err != nil {
		return nil, &Receipt{TxID: mintTx.Base.TxId, Status: "FAILED", Error: "balance overflow"}, fmt.Errorf("balance overflow: %w", err)
	}
	bal.Balance = newBal.String()
	SetBalance(sv, to, mintTx.TokenAddr, bal)

	ws := []WriteOp{
		{Key: tokenKey, Value: updatedTokenData, Category: "token"},
		balanceWriteOp(sv, to, mintTx.TokenAddr),
	}
	return ws, &Receipt{TxID: mintTx.Base.TxId, Status: "SUCCEED", WriteCount: len(ws)}, nil
}

func (h *MintTokenTxHandler) Apply(tx *pb.AnyTx) error { return ErrNotImplemented }
