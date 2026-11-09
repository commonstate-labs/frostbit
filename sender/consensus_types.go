package sender

import (
	"dex/pb"
	"dex/types"
)

                                            

                                       
type chitsMessage struct {
	requestData []byte
}

                                        
type blockMessage struct {
	requestData []byte
}

                                              
type heightQueryMessage struct {
	requestData []byte
	onSuccess   func(*pb.HeightResponse)
}

                                                                             
type acceptedFrontierMessage struct {
	requestData []byte
	onSuccess   func(*pb.AcceptedFrontierResponse) error
}

                                        
type syncRequestMessage struct {
	fromHeight           uint64
	toHeight             uint64
	onSuccessWithBundles func(*types.SyncBlocksResponse)
}

                                      
type pushQueryMsg struct {
	requestData []byte
}

                                      
type pullQueryMsg struct {
	requestData []byte
}

                                               
type pullBlockMessage struct {
	requestData []byte
	onSuccess   func(*pb.Block)
}

                                              
type pullBatchTxMessage struct {
	requestData []byte
	onSuccess   func([]*pb.AnyTx)
}
