package main

                                                                                   
                                                                                                        

import (
	"dex/cmd/explorer/indexdb"
	"dex/db"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                           

                                                                      
func localGetBlockByHeight(nodeDB *db.Manager, height uint64) (*pb.Block, error) {
	if nodeDB == nil {
		return nil, fmt.Errorf("node database not available")
	}
	return nodeDB.GetBlock(height)
}

                                                                       
func localGetBlockByID(nodeDB *db.Manager, blockID string) (*pb.Block, error) {
	if nodeDB == nil {
		return nil, fmt.Errorf("node database not available")
	}
	return nodeDB.GetBlockByID(blockID)
}

                                                                        
func localGetLatestHeight(nodeDB *db.Manager) (uint64, error) {
	if nodeDB == nil {
		return 0, fmt.Errorf("node database not available")
	}
	return nodeDB.GetLatestBlockHeight()
}

                                                                           
func localGetRecentBlocks(nodeDB *db.Manager, count int) ([]*pb.Block, error) {
	if nodeDB == nil {
		return nil, fmt.Errorf("node database not available")
	}
	if count <= 0 {
		count = 50
	}
	if count > 100 {
		count = 100
	}
	latestHeight, err := nodeDB.GetLatestBlockHeight()
	if err != nil {
		return nil, err
	}

	startHeight := uint64(0)
	if latestHeight > uint64(count) {
		startHeight = latestHeight - uint64(count) + 1
	}

	return nodeDB.GetBlocksByRange(startHeight, latestHeight)
}

func localBlockReward(nodeDB *db.Manager, height uint64, fallback string) string {
	if nodeDB == nil {
		return fallback
	}
	data, err := nodeDB.GetKV(keys.KeyBlockReward(height))
	if err != nil || len(data) == 0 {
		return fallback
	}
	var reward pb.BlockReward
	if err := proto.Unmarshal(data, &reward); err != nil || reward.BlockReward == "" {
		return fallback
	}
	return reward.BlockReward
}

func localFinalizedObservedAt(nodeDB *db.Manager, height uint64) int64 {
	if nodeDB == nil {
		return 0
	}
	data, err := nodeDB.Read(fmt.Sprintf("finalization_chits_%d", height))
	if err != nil || data == "" {
		return 0
	}
	var chits finalizationChitsInfo
	if err := json.Unmarshal([]byte(data), &chits); err != nil {
		return 0
	}
	return explorerChitsObservedAt(&chits)
}

                                                           

                                                         
func localGetAnyTxByID(nodeDB *db.Manager, txID string) (*pb.AnyTx, error) {
	if nodeDB == nil {
		return nil, fmt.Errorf("node database not available")
	}
	return nodeDB.GetAnyTxById(txID)
}

                                                               
func localGetTxReceipt(nodeDB *db.Manager, txID string) (*pb.Receipt, error) {
	if nodeDB == nil {
		return nil, fmt.Errorf("node database not available")
	}
	return nodeDB.GetTxReceipt(txID)
}

                                                           

                                                             
func localGetRecentTxRecords(nodeDB *db.Manager, txType string, limit int) ([]*txRecord, error) {
	if nodeDB == nil {
		return nil, fmt.Errorf("node database not available")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	latestHeight, err := nodeDB.GetLatestBlockHeight()
	if err != nil {
		return nil, err
	}
	filterAll := txType == "" || txType == "all"
	results := make([]*txRecord, 0, limit)
	const maxLocalRecentTxBlockScan = 64
	for height, scanned := latestHeight, 0; height > 0 && len(results) < limit && scanned < maxLocalRecentTxBlockScan; height, scanned = height-1, scanned+1 {
		block, err := nodeDB.GetBlock(height)
		if err != nil || block == nil {
			continue
		}
		for i := len(block.Body) - 1; i >= 0 && len(results) < limit; i-- {
			tx := block.Body[i]
			if tx == nil {
				continue
			}
			typ := indexdb.ExtractTxType(tx)
			if !filterAll && typ != txType {
				continue
			}
			results = append(results, recentTxRecordFromAnyTx(tx, typ, height, i))
		}
	}
	return results, nil
}

func recentTxRecordFromAnyTx(tx *pb.AnyTx, txType string, height uint64, txIndex int) *txRecord {
	rec := &txRecord{TxID: tx.GetTxId(), TxType: txType, Height: height, TxIndex: txIndex}
	if base := tx.GetBase(); base != nil {
		rec.FromAddress = base.FromAddress
		rec.Status = base.Status.String()
		rec.Fee = base.Fee
		rec.Nonce = base.Nonce
	}
	rec.ToAddress, rec.Value = indexdb.ExtractToAndValue(tx)
	return rec
}

func localGetAccount(nodeDB *db.Manager, address string) (*pb.Account, error) {
	if nodeDB == nil {
		return nil, fmt.Errorf("node database not available")
	}
	return nodeDB.GetAccount(address)
}

                                                                                    
func localGetAccountBalances(nodeDB *db.Manager, address string) map[string]*pb.TokenBalance {
	if nodeDB == nil {
		return nil
	}
	balances := make(map[string]*pb.TokenBalance)
	prefix := keys.KeyBalancePrefix(address)

	kvResults, err := nodeDB.ScanByPrefix(prefix, 100)
	if err != nil {
		log.Printf("[localGetAccountBalances] scan error for %s: %v", address, err)
		return balances
	}
	for key, val := range kvResults {
		token := localExtractTokenFromBalanceKey(key, address)
		if token == "" {
			continue
		}
		var record pb.TokenBalanceRecord
		if err := proto.Unmarshal([]byte(val), &record); err == nil && record.Balance != nil {
			balances[token] = record.Balance
		}
	}
	return balances
}

                                                                       
func localExtractTokenFromBalanceKey(key, address string) string {
	prefix := keys.KeyBalancePrefix(address)
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return key[len(prefix):]
}

                                                           

                                                            
func localGetToken(nodeDB *db.Manager, tokenAddress string) (*pb.Token, error) {
	if nodeDB == nil {
		return nil, fmt.Errorf("node database not available")
	}
	return nodeDB.GetToken(tokenAddress)
}
