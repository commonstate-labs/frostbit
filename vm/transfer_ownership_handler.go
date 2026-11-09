package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                                      
type TransferOwnershipTxHandler struct{}

func (h *TransferOwnershipTxHandler) Kind() string { return "transfer_ownership" }

func (h *TransferOwnershipTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	toTx := tx.GetTransferOwnershipTx()
	if toTx == nil || toTx.Base == nil {
		return nil, &Receipt{TxID: tx.GetTxId(), Status: "FAILED", Error: "invalid transfer ownership tx"}, fmt.Errorf("invalid transfer ownership tx")
	}

	if toTx.NewOwner == "" {
		return nil, &Receipt{TxID: toTx.Base.TxId, Status: "FAILED", Error: "new_owner is required"}, fmt.Errorf("new_owner is required")
	}

	               
	tokenKey := keys.KeyToken(toTx.TokenAddr)
	tokenData, exists, err := sv.Get(tokenKey)
	if err != nil || !exists {
		return nil, &Receipt{TxID: toTx.Base.TxId, Status: "FAILED", Error: "token not found"}, fmt.Errorf("token not found: %s", toTx.TokenAddr)
	}
	var token pb.Token
	if err := unmarshalProtoCompat(tokenData, &token); err != nil {
		return nil, &Receipt{TxID: toTx.Base.TxId, Status: "FAILED", Error: "parse token failed"}, err
	}

	                                        
	if token.Owner != toTx.Base.FromAddress {
		return nil, &Receipt{TxID: toTx.Base.TxId, Status: "FAILED", Error: "only owner can transfer ownership"}, fmt.Errorf("only owner can transfer ownership")
	}

	                                                                            
	if token.Owner == "0x0" {
		return nil, &Receipt{TxID: toTx.Base.TxId, Status: "FAILED", Error: "ownership already renounced"}, fmt.Errorf("ownership already renounced")
	}

	               
	token.Owner = toTx.NewOwner
	updatedTokenData, _ := proto.Marshal(&token)

	ws := []WriteOp{
		{Key: tokenKey, Value: updatedTokenData, Category: "token"},
	}
	return ws, &Receipt{TxID: toTx.Base.TxId, Status: "SUCCEED", WriteCount: len(ws)}, nil
}

func (h *TransferOwnershipTxHandler) Apply(tx *pb.AnyTx) error { return ErrNotImplemented }
