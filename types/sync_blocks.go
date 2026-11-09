package types

                                                             
type SyncBlocksRequest struct {
	FromHeight uint64 `json:"from_height"`
	ToHeight   uint64 `json:"to_height"`
}

                                               
                                        
type SyncBlockBundle struct {
	Height     uint64 `json:"height"`
	BlockID    string `json:"block_id"`
	BlockBytes []byte `json:"block_bytes"`
}

                                                                            
type SyncBlocksResponse struct {
	Blocks []SyncBlockBundle `json:"blocks,omitempty"`
	Error  string            `json:"error,omitempty"`
}
