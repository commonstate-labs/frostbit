package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                            
type IssueTokenTxHandler struct{}

func (h *IssueTokenTxHandler) Kind() string {
	return "issue_token"
}

func (h *IssueTokenTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	                        
	issueTokenTx, ok := tx.GetContent().(*pb.AnyTx_IssueTokenTx)
	if !ok {
		return nil, &Receipt{
			TxID:   tx.GetTxId(),
			Status: "FAILED",
			Error:  "not an issue token transaction",
		}, fmt.Errorf("not an issue token transaction")
	}

	issueTx := issueTokenTx.IssueTokenTx
	if issueTx == nil || issueTx.Base == nil {
		return nil, &Receipt{
			TxID:   tx.GetTxId(),
			Status: "FAILED",
			Error:  "invalid issue token transaction",
		}, fmt.Errorf("invalid issue token transaction")
	}

	                                       
	accountKey := keys.KeyAccount(issueTx.Base.FromAddress)
	accountData, exists, err := sv.Get(accountKey)
	if err != nil {
		return nil, &Receipt{
			TxID:   issueTx.Base.TxId,
			Status: "FAILED",
			Error:  "read account failed",
		}, err
	}

	if !exists {
		return nil, &Receipt{
			TxID:   issueTx.Base.TxId,
			Status: "FAILED",
			Error:  "account not found",
		}, fmt.Errorf("account not found: %s", issueTx.Base.FromAddress)
	}

	                     
	var account pb.Account
	if err := unmarshalProtoCompat(accountData, &account); err != nil {
		return nil, &Receipt{
			TxID:   issueTx.Base.TxId,
			Status: "FAILED",
			Error:  "failed to parse account data",
		}, err
	}

	                       
	totalSupply, err := ParseBalance(issueTx.TotalSupply)
	if err != nil {
		return nil, &Receipt{
			TxID:   issueTx.Base.TxId,
			Status: "FAILED",
			Error:  fmt.Sprintf("invalid total supply: %v", err),
		}, err
	}

	                                                         
	tokenAddress := issueTx.Base.TxId

	                             
	tokenKey := keys.KeyToken(tokenAddress)
	_, tokenExists, _ := sv.Get(tokenKey)
	if tokenExists {
		return nil, &Receipt{
			TxID:   issueTx.Base.TxId,
			Status: "FAILED",
			Error:  "token already exists",
		}, fmt.Errorf("token already exists: %s", tokenAddress)
	}

	                                                                                     
	                                                           
	token := &pb.Token{
		Address:      tokenAddress,
		Owner:        issueTx.Base.FromAddress,
		TotalSupply:  totalSupply.String(),
		CanMint:      issueTx.CanMint,
		CanFreeze:    issueTx.CanFreeze,
		CanBurn:      issueTx.CanBurn,
		Type:         pb.TokenType_TOKEN_NATIVE,
		CreateHeight: 0,                                                     
	}

	tokenData, err := proto.Marshal(token)
	if err != nil {
		return nil, &Receipt{
			TxID:   issueTx.Base.TxId,
			Status: "FAILED",
			Error:  "failed to marshal token",
		}, err
	}

	ws := make([]WriteOp, 0)

	                                                                          
	ws = append(ws, WriteOp{
		Key:      tokenKey,
		Value:    tokenData,
		Del:      false,
		Category: "token",
	})

	                                       
	issuerBal := GetBalance(sv, issueTx.Base.FromAddress, tokenAddress)
	issuerBal.Balance = totalSupply.String()
	SetBalance(sv, issueTx.Base.FromAddress, tokenAddress, issuerBal)

	                                               
	balanceOp := balanceWriteOp(sv, issueTx.Base.FromAddress, tokenAddress)

	ws = append(ws, balanceOp)

	                        
	rc := &Receipt{
		TxID:       issueTx.Base.TxId,
		Status:     "SUCCEED",
		WriteCount: len(ws),
	}

	return ws, rc, nil
}

func (h *IssueTokenTxHandler) Apply(tx *pb.AnyTx) error {
	return ErrNotImplemented
}
