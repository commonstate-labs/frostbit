                                          
                                               
                                                                     
package vm

import "dex/pb"

                                                                  
func unmarshalFrostVaultTransition(data []byte) (*pb.VaultTransitionState, error) {
	state := &pb.VaultTransitionState{}
	if err := unmarshalProtoCompat(data, state); err != nil {
		return nil, err
	}
	return state, nil
}

                                                        
func unmarshalFrostVaultState(data []byte) (*pb.FrostVaultState, error) {
	state := &pb.FrostVaultState{}
	if err := unmarshalProtoCompat(data, state); err != nil {
		return nil, err
	}
	return state, nil
}
