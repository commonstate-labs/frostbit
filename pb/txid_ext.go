package pb

import (
	"crypto/sha256"
	"encoding/hex"
	"sync/atomic"

	"google.golang.org/protobuf/proto"
)

                                                                                                            
                                                                                                              
                                                  
var defaultChainID atomic.Uint64

                                                                                                              
func SetDefaultChainID(id uint64) { defaultChainID.Store(id) }

                                                     
func GetDefaultChainID() uint64 { return defaultChainID.Load() }

func stripDerivedTxIDFields(tx *AnyTx) {
	if tx == nil {
		return
	}
	switch t := tx.GetContent().(type) {
	case *AnyTx_WitnessVoteTx:
		if t.WitnessVoteTx != nil && t.WitnessVoteTx.Vote != nil {
			t.WitnessVoteTx.Vote.TxId = ""
		}
	case *AnyTx_ArbitrationVoteTx:
		if t.ArbitrationVoteTx != nil && t.ArbitrationVoteTx.Vote != nil {
			t.ArbitrationVoteTx.Vote.TxId = ""
		}
	}
}

                                                                                                               
                                                                                                                                                                       
                                                                                                                                                                  
                                                                                                                                          
func (m *AnyTx) GenerateTxID() string {
	                                                                                            
	if base := m.GetBase(); base != nil && base.ChainId == 0 {
		if def := defaultChainID.Load(); def != 0 {
			base.ChainId = def
		}
	}

	                                                   
	cloned := proto.Clone(m).(*AnyTx)

	                                                            
	base := cloned.GetBase()
	if base != nil {
		base.TxId = ""                                            
		base.Signature = nil                                                                                                
		base.Status = Status_PENDING                             
		base.ExecutedHeight = 0                                    
		base.StatusText = ""                                                    
		                                                                                                                     
	}

	stripDerivedTxIDFields(cloned)

	                                                                                      
	opts := proto.MarshalOptions{Deterministic: true}
	data, err := opts.Marshal(cloned)
	if err != nil {
		                                          
		return "0x_marshal_err"
	}

	                   
	hash := sha256.Sum256(data)
	return "0x" + hex.EncodeToString(hash[:])
}
