package runtime

import (
	"dex/frost/runtime/roast"
	"dex/frost/runtime/types"
	"dex/pb"
)

                                                                                                            
                                                                                                  
type (
	NodeID              = types.NodeID
	SignerInfo          = types.SignerInfo
	ChainStateReader    = types.ChainStateReader
	StateReader         = types.StateReader
	TxSubmitter         = types.TxSubmitter
	LogReporter         = types.LogReporter
	LocalShareStore     = types.LocalShareStore
	DkgLocalSessionRef  = types.DkgLocalSessionRef
	MinerPubKeyProvider = types.MinerPubKeyProvider
)

                                                                                      
type FinalityNotifier interface {
	SubscribeBlockFinalized(fn func(height uint64))
}

                                 
type FrostEnvelope struct {
	SessionID string
	Kind      string                                                                      
	From      NodeID
	Chain     string             
	VaultID   uint32                
	SignAlgo  int32                                                         
	Epoch     uint64             
	Round     uint32
	Payload   []byte                                                                  
	Sig       []byte                                      
	Tweaks    [][]byte
}

                                               
type P2P interface {
	Send(to NodeID, msg *FrostEnvelope) error
	Broadcast(peers []NodeID, msg *FrostEnvelope) error
	SamplePeers(n int, role string) []NodeID
}

                                                                         
type SignerSetProvider interface {
	TopK(height uint64) ([]SignerInfo, error)                                          
	CurrentEpoch(height uint64) uint64
}

                                                                           
type VaultCommitteeProvider = types.VaultCommitteeProvider

                                                            
type RoastEnvelope = types.RoastEnvelope

                                                                   
type RoastMessenger = types.RoastMessenger

                                                                              
                                                                         
                         
                                
  
                  
                             
                                                              

                                                                                                
func PBEnvelopeFromRoast(msg *RoastEnvelope) (*pb.FrostEnvelope, error) {
	if msg == nil {
		return nil, nil
	}
	                                                                            
	return roast.PBEnvelopeFromRoast(&roast.Envelope{
		SessionID: msg.SessionID,
		Kind:      msg.Kind,
		From:      roast.NodeID(msg.From),
		Chain:     msg.Chain,
		VaultID:   msg.VaultID,
		SignAlgo:  pb.SignAlgo(msg.SignAlgo),
		Epoch:     msg.Epoch,
		Round:     msg.Round,
		Payload:   msg.Payload,
		Tweaks:    msg.Tweaks,
	})
}
