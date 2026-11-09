package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
)

                                                                                           
                                                                                             

                                                                 
                                                                                                                
func IsFrozen(sv StateView, addr, tokenAddr string) bool {
	data, ok, _ := sv.Get(keys.KeyFreeze(addr, tokenAddr))
	return ok && string(data) == "true"
}

                                                    
type FreezeTxHandler struct{}

func (h *FreezeTxHandler) Kind() string {
	return "freeze"
}

func (h *FreezeTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	                    
	freezeTx, ok := tx.GetContent().(*pb.AnyTx_FreezeTx)
	if !ok {
		return nil, &Receipt{
			TxID:   tx.GetTxId(),
			Status: "FAILED",
			Error:  "not a freeze transaction",
		}, fmt.Errorf("not a freeze transaction")
	}

	freeze := freezeTx.FreezeTx
	if freeze == nil || freeze.Base == nil {
		return nil, &Receipt{
			TxID:   tx.GetTxId(),
			Status: "FAILED",
			Error:  "invalid freeze transaction",
		}, fmt.Errorf("invalid freeze transaction")
	}

	                             
	tokenKey := keys.KeyToken(freeze.TokenAddr)
	tokenData, tokenExists, err := sv.Get(tokenKey)
	if err != nil {
		return nil, &Receipt{
			TxID:   freeze.Base.TxId,
			Status: "FAILED",
			Error:  "read token failed",
		}, err
	}

	if !tokenExists {
		return nil, &Receipt{
			TxID:   freeze.Base.TxId,
			Status: "FAILED",
			Error:  "token not found",
		}, fmt.Errorf("token not found: %s", freeze.TokenAddr)
	}

	                    
	var token pb.Token
	if err := unmarshalProtoCompat(tokenData, &token); err != nil {
		return nil, &Receipt{
			TxID:   freeze.Base.TxId,
			Status: "FAILED",
			Error:  "failed to parse token data",
		}, err
	}

	                            
	if token.Type == pb.TokenType_TOKEN_BRIDGED {
		return nil, &Receipt{
			TxID:   freeze.Base.TxId,
			Status: "FAILED",
			Error:  "bridged token cannot be frozen",
		}, fmt.Errorf("bridged token cannot be frozen")
	}

	                                   
	if !token.CanFreeze {
		return nil, &Receipt{
			TxID:   freeze.Base.TxId,
			Status: "FAILED",
			Error:  "this token does not support freeze",
		}, fmt.Errorf("token %s does not support freeze", freeze.TokenAddr)
	}

	                                           
	if token.Owner != freeze.Base.FromAddress {
		return nil, &Receipt{
			TxID:   freeze.Base.TxId,
			Status: "FAILED",
			Error:  "only token owner can freeze/unfreeze",
		}, fmt.Errorf("only token owner can freeze/unfreeze: owner=%s, from=%s", token.Owner, freeze.Base.FromAddress)
	}

	                               
	                                                                             
	freezeKey := keys.KeyFreeze(freeze.TargetAddr, freeze.TokenAddr)

	ws := make([]WriteOp, 0)

	if freeze.Freeze {
		ws = append(ws, WriteOp{Key: freezeKey, Value: []byte("true"), Category: "freeze"})
	} else {
		ws = append(ws, WriteOp{Key: freezeKey, Del: true, Category: "freeze"})
	}

	return ws, &Receipt{TxID: freeze.Base.TxId, Status: "SUCCEED", WriteCount: len(ws)}, nil
}

func (h *FreezeTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}
