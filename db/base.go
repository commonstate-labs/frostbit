package db

import (
	"dex/keys"
	"dex/pb"
	"fmt"
)

func NewZeroTokenBalance() *pb.TokenBalance {
	return &pb.TokenBalance{
		Balance:               "0",
		MinerLockedBalance:    "0",
		LiquidLockedBalance:   "0",
		WitnessLockedBalance:  "0",
		LeverageLockedBalance: "0",
	}
}

                                              
                                                                                 
                                    
  
                  
                                                     
                                                               
                                                    
func (mgr *Manager) SaveTxRaw(anyTx *pb.AnyTx) error {
	txID := anyTx.GetTxId()
	if txID == "" {
		return fmt.Errorf("SaveTxRaw: empty txID not allowed")
	}
	                        
	data, err := ProtoMarshal(anyTx)
	if err != nil {
		return err
	}
	                                                  
	rawKey := keys.KeyTxRaw(txID)
	mgr.EnqueueSet(rawKey, string(data))
	return nil
}

                              
func (mgr *Manager) GetTxRaw(txID string) (*pb.AnyTx, error) {
	rawKey := keys.KeyTxRaw(txID)
	data, err := mgr.Read(rawKey)
	if err != nil {
		return nil, err
	}
	if data == "" {
		return nil, fmt.Errorf("tx raw not found: %s", txID)
	}
	anyTx := &pb.AnyTx{}
	if err := ProtoUnmarshal([]byte(data), anyTx); err != nil {
		return nil, err
	}
	return anyTx, nil
}

