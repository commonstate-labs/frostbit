package types

import "strconv"

type NodeID string

func (id NodeID) Last2Mod100() int {
	s := string(id)
	if s == "" {
		return 0
	}
	last2 := s
	if len(s) > 2 {
		last2 = s[len(s)-2:]                            
	}

	n, err := strconv.Atoi(last2)
	if err != nil {
		return 0
	}
	return n % 100
}

               
type MessageType string

const (
	MsgPullQuery      = "MsgPullQuery"
	MsgPushQuery      = "MsgPushQuery"
	MsgChits          = "MsgChits"
	MsgGet            = "MsgGet"                      
	MsgPut            = "MsgPut"                      
	MsgGossip         = "MsgGossip"
	MsgSyncRequest    = "MsgSyncRequest"
	MsgSyncResponse   = "MsgSyncResponse"
	MsgHeightQuery    = "MsgHeightQuery"
	MsgHeightResponse = "MsgHeightResponse"
	MsgFrost          = "MsgFrost"                      
)

                     
type Message struct {
	Type MessageType
	From NodeID
	                                                                           
	                                                                           
	                                                            
	RelayFrom NodeID
	RequestID uint32
	BlockID   string
	Block     *Block
	Height    uint64
	ShortTxs  []byte                                                                    
	                               
	VRFSeed []byte                                                              
	SeqID   uint32                   
	                                                                            
	QueryNonce []byte
	            
	PreferredID       string
	PreferredIDHeight uint64
	AcceptedID        string
	AcceptedHeight    uint64
	ChitSignature     []byte                                                                                    
	           
	FromHeight    uint64
	ToHeight      uint64
	Blocks        []*Block
	SyncID        uint32
	SyncBlocksRaw map[string][]byte                                      
	                                                                         
	                                                                            
	ServiceSync bool
	                                                                         
	                                                                          
	                                                                             
	                                                          
	SyncFailed bool
	SyncError  string
	                   
	CurrentHeight uint64

	            
	FrostPayload []byte                                 

	                                                                           
	                                                                          
	                                                                     
	PreparedGossip []byte
}
