package consensus

import (
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func canonicalTxIDForValidation(tx *pb.AnyTx) string {
	if tx == nil {
		return ""
	}
	cloned, ok := proto.Clone(tx).(*pb.AnyTx)
	if !ok || cloned == nil {
		return ""
	}
	return cloned.GenerateTxID()
}

func sanitizeWitnessRequestTxForCanonicalID(tx *pb.AnyTx) (*pb.AnyTx, bool) {
	                                                                    
	                                                                     
	                                                            
	return tx, false
}

func sanitizeBlockTxsForCanonicalValidation(block *pb.Block) (*pb.Block, bool) {
	return block, false
}
