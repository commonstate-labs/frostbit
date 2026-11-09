package indexdb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	                  
	KeySyncHeight      = "sync_height"                                    
	KeySyncNode        = "sync_node"                                
	KeyHistoryMetadata = "history_metadata"                                        
	KeyRetentionCursor = "retention_cursor"                                          

	                                                                            
	                                                                          
	KeyReadModelBaseHeight    = "read_model_base_height"
	KeyReadModelTipHeight     = "read_model_tip_height"
	KeyReadModelSchemaVersion = "read_model_schema_version"
)

                                                                         
                                                                                
                                                                                
func KeyReadModelOwner(rawKey string) string {
	digest := sha256.Sum256([]byte(rawKey))
	return "read_model_owner_" + hex.EncodeToString(digest[:])
}

                                                                              
                                                                               
func KeyHeightManifest(height uint64) string {
	return fmt.Sprintf("height_manifest_%020d", height)
}

                                  
                                                              
                                                      
func KeyAddressTx(address string, height uint64, txIndex int) string {
	                                                                  
	invertedHeight := ^height                               
	return fmt.Sprintf("addr_tx_%s_%020d_%04d", address, invertedHeight, txIndex)
}

                                              
func KeyAddressTxPrefix(address string) string {
	return fmt.Sprintf("addr_tx_%s_", address)
}

                                                                                     
                                                            
func ParseAddressTxKey(key, address string) (height uint64, txIndex int, ok bool) {
	prefix := KeyAddressTxPrefix(address)
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, false
	}

	rest := strings.TrimPrefix(key, prefix)
	parts := strings.Split(rest, "_")
	if len(parts) != 2 {
		return 0, 0, false
	}

	invertedHeight, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	txIndex, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}

	return ^invertedHeight, txIndex, true
}

func KeyAddressTypeTx(address, txType string, height uint64, txIndex int) string {
	return fmt.Sprintf("addr_type_tx_%s_%s_%020d_%020d", address, txType, ^height, ^uint64(txIndex))
}

func KeyAddressTypeTxPrefix(address, txType string) string {
	return fmt.Sprintf("addr_type_tx_%s_%s_", address, txType)
}

func ParseAddressTypeTxKey(key, address, txType string) (height uint64, txIndex int, ok bool) {
	prefix := KeyAddressTypeTxPrefix(address, txType)
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "_")
	if len(parts) != 2 {
		return 0, 0, false
	}
	invertedHeight, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	invertedTxIndex, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ^invertedHeight, int(^invertedTxIndex), true
}

                                                                             
                                       
func KeyBlockTx(height uint64, txIndex int) string {
	return fmt.Sprintf("block_tx_%020d_%04d", height, txIndex)
}

                                            
func KeyBlockTxPrefix(height uint64) string {
	return fmt.Sprintf("block_tx_%020d_", height)
}

                                                                                                   
                                                                  
func KeyTxTypeTx(txType string, height uint64, txIndex int) string {
	return fmt.Sprintf("tx_type_tx_%s_%020d_%020d", txType, ^height, ^uint64(txIndex))
}

func KeyTxTypeTxPrefix(txType string) string {
	return fmt.Sprintf("tx_type_tx_%s_", txType)
}

func ParseTxTypeTxKey(key, txType string) (height uint64, txIndex int, ok bool) {
	prefix := KeyTxTypeTxPrefix(txType)
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "_")
	if len(parts) != 2 {
		return 0, 0, false
	}
	invertedHeight, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	invertedTxIndex, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ^invertedHeight, int(^invertedTxIndex), true
}

func KeyBlockHash(height uint64) string {
	return fmt.Sprintf("block_hash_%020d", height)
}

                           
                            
func ParseBlockTxKey(key string) (height uint64, txIndex int, ok bool) {
	const prefix = "block_tx_"
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "_")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	idx, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return h, idx, true
}

func KeyTxDetail(txID string) string {
	return fmt.Sprintf("tx_detail_%s", txID)
}

                                                                            
                                                                          
                                                                              
                                         
func KeyTxDetailRef(txID string) string {
	return fmt.Sprintf("tx_detail_ref_%s", txID)
}

                                     
                               
func KeyAddressCount(address string) string {
	return fmt.Sprintf("addr_count_%s", address)
}

                                    
                                                    
                                                                        
func KeyBalanceSnapshot(address string, height uint64) string {
	invertedHeight := ^height
	return fmt.Sprintf("bal_snap_%s_%020d", address, invertedHeight)
}

                                              
func KeyBalanceSnapshotPrefix(address string) string {
	return fmt.Sprintf("bal_snap_%s_", address)
}
