package handlers

import (
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                         
                                                                             
                                                     
func shouldImportBlockTxToPending(tx *pb.AnyTx) bool {
	if tx == nil {
		return false
	}
	if !pb.TxRequiresAccountNonce(tx) {
		return true
	}
	base := tx.GetBase()
	return base == nil || (base.TargetHeight == 0 && base.OnlyProposer == "")
}

                                                                           
                                                                             
                                                                          
func cloneBlockTxForPending(tx *pb.AnyTx) *pb.AnyTx {
	if tx == nil {
		return nil
	}
	cloned, _ := proto.Clone(tx).(*pb.AnyTx)
	return cloned
}
