                                            
                                                                   
package adapters

import (
	"dex/db"
	"dex/frost/runtime"
	"dex/keys"
	"dex/pb"
	"fmt"
	"strconv"

	"google.golang.org/protobuf/proto"
)

                                                     
type TopKReader interface {
	                                   
	GetTopK() ([]byte, error)
	                                      
	GetCurrentHeight() uint64
}

                                                              
type DBManagerAdapter struct {
	dbManager *db.Manager
}

                                                    
func NewDBManagerAdapter(dbManager *db.Manager) *DBManagerAdapter {
	return &DBManagerAdapter{dbManager: dbManager}
}

                                   
func (a *DBManagerAdapter) GetTopK() ([]byte, error) {
	val, err := a.dbManager.Read(keys.KeyFrostActiveMiners())
	if err != nil {
		return nil, err
	}
	return []byte(val), nil
}

                                                   
func (a *DBManagerAdapter) GetCurrentHeight() uint64 {
	val, err := a.dbManager.Read(keys.KeyLatestHeight())
	if err != nil || val == "" {
		return 0
	}
	h, _ := strconv.ParseUint(val, 10, 64)
	return h
}

                                                                      
type ConsensusSignerProvider struct {
	reader    TopKReader
	dbManager *db.Manager                             
}

                                                                  
func NewConsensusSignerProvider(reader TopKReader) *ConsensusSignerProvider {
	return &ConsensusSignerProvider{reader: reader}
}

                                                                                                                
func NewConsensusSignerProviderWithDB(reader TopKReader, dbManager *db.Manager) *ConsensusSignerProvider {
	return &ConsensusSignerProvider{reader: reader, dbManager: dbManager}
}

                                                  
  
                                                                                               
                                                                                                 
                                              
                                                                                    
func (p *ConsensusSignerProvider) TopK(height uint64) ([]runtime.SignerInfo, error) {
	if p.reader == nil {
		return nil, fmt.Errorf("reader not available")
	}

	data, err := p.reader.GetTopK()
	if err != nil {
		return nil, fmt.Errorf("get topK: %w", err)
	}

	var topK pb.FrostActiveMiners
	if err := proto.Unmarshal(data, &topK); err != nil {
		return nil, fmt.Errorf("unmarshal topK: %w", err)
	}

	result := make([]runtime.SignerInfo, 0, len(topK.Indices))
	for i, idx := range topK.Indices {
		if i >= len(topK.Addresses) {
			break
		}
		var pubKey []byte
		if i < len(topK.PublicKeys) {
			pubKey = topK.PublicKeys[i]
		}

		result = append(result, runtime.SignerInfo{
			ID:        runtime.NodeID(topK.Addresses[i]),
			Index:     uint32(idx),
			PublicKey: pubKey,
			Weight:    1,                                                                                           
		})
	}

	return result, nil
}

                                                             
                                                                                      
func (p *ConsensusSignerProvider) CurrentEpoch(height uint64) uint64 {
	if p.dbManager != nil {
		                                                       
		val, err := p.dbManager.Read(keys.KeyFrostVaultState("btc", 0))
		if err == nil && val != "" {
			var state pb.FrostVaultState
			if err := proto.Unmarshal([]byte(val), &state); err == nil && state.KeyEpoch > 0 {
				return state.KeyEpoch
			}
		}
	}
	                                                              
	if p.reader != nil {
		currentHeight := p.reader.GetCurrentHeight()
		if currentHeight > 0 {
			return 1                                                    
		}
	}
	return 1
}

                                                                      
var _ runtime.SignerSetProvider = (*ConsensusSignerProvider)(nil)
