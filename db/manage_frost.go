                     
                                             
                                                                                                
                                                                                                    

package db

import (
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                      
func (mgr *Manager) GetFrostVaultState(key string) (*pb.FrostVaultState, error) {
	val, err := mgr.Read(key)
	if err != nil {
		return nil, err
	}
	var state pb.FrostVaultState
	if err := proto.Unmarshal([]byte(val), &state); err != nil {
		return nil, err
	}
	return &state, nil
}
