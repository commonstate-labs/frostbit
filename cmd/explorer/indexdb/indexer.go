package indexdb

import (
	"dex/db"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"

	"google.golang.org/protobuf/proto"
)

type TxRecord struct {
	TxID           string `json:"tx_id"`
	TxType         string `json:"tx_type"`
	FromAddress    string `json:"from_address"`
	ToAddress      string `json:"to_address,omitempty"`
	Value          string `json:"value,omitempty"`
	Status         string `json:"status"`
	Fee            string `json:"fee,omitempty"`
	Nonce          uint64 `json:"nonce,omitempty"`
	Height         uint64 `json:"height"`
	TxIndex        int    `json:"tx_index"`
	FBBalanceAfter string `json:"fb_balance_after,omitempty"`
}

func (idb *IndexDB) SetNodeDB(nodeDB *db.Manager) {
	idb.mu.Lock()
	defer idb.mu.Unlock()
	idb.nodeDB = nodeDB
}

func (idb *IndexDB) IndexBlock(block *pb.Block) error {
	return idb.indexBlock(block, nil)
}

                                                                              
                                                                              
                                                                         
                                                                          
func (idb *IndexDB) IndexBlockWithReadModel(block *pb.Block, readModel *pb.BlockReadModel) error {
	if readModel == nil {
		return fmt.Errorf("cannot advance finalized Explorer index without a read model")
	}
	return idb.indexBlock(block, readModel)
}

func (idb *IndexDB) indexBlock(block *pb.Block, readModel *pb.BlockReadModel) error {
	if block == nil {
		if readModel != nil {
			return fmt.Errorf("cannot index read model without block")
		}
		return nil
	}
	if block.Header == nil {
		return fmt.Errorf("cannot index block without header")
	}
	height := block.Header.Height
	if readModel != nil {
		if err := validateBlockReadModel(block, readModel); err != nil {
			return err
		}
	}

	idb.writeMu.Lock()
	defer idb.writeMu.Unlock()

	                                                                             
	                                                                           
	                                                                             
	                                                 
	existingManifestRaw, err := idb.Get(KeyHeightManifest(height))
	if err != nil {
		return err
	}
	if existingManifestRaw != "" {
		existingHash, hashErr := idb.GetBlockHash(height)
		if hashErr != nil {
			return hashErr
		}
		if existingHash != "" && block.BlockHash != "" && existingHash != block.BlockHash {
			return fmt.Errorf("height %d is already indexed with a different block hash", height)
		}
		if readModel != nil {
			var existingManifest heightManifest
			if err := json.Unmarshal([]byte(existingManifestRaw), &existingManifest); err != nil {
				return fmt.Errorf("decode height %d manifest: %w", height, err)
			}
			digest := readModelDigestHex(readModel)
			if existingManifest.ReadModelSchemaVersion != readModel.SchemaVersion || existingManifest.ReadModelDigest != digest {
				return fmt.Errorf("height %d is already indexed with a different read model", height)
			}
		}
		return nil
	}
	                                                                          
	                                                                    
	existingHash, err := idb.GetBlockHash(height)
	if err != nil {
		return err
	}
	if existingHash != "" {
		if block.BlockHash != "" && existingHash != block.BlockHash {
			return fmt.Errorf("height %d is already indexed with a different block hash", height)
		}
		if readModel != nil {
			return fmt.Errorf("height %d has a legacy index without a read-model manifest", height)
		}
		return nil
	}
	batch := idb.NewBatch()
	defer batch.Close()
	txCapacity := len(block.Body)
	manifestKeyCapacity := txCapacity*6 + 1
	if readModel != nil {
		manifestKeyCapacity += len(readModel.Mutations)
	}
	addressIncrements := make(map[string]int, txCapacity)
	manifest := &heightManifest{
		Keys:          make([]string, 0, manifestKeyCapacity),
		TxIDs:         make([]string, 0, txCapacity),
		AddressDeltas: make(map[string]int, txCapacity),
		txIDSet:       make(map[string]struct{}, txCapacity),
	}

	for i, tx := range block.Body {
		if tx == nil {
			continue
		}
		if err := idb.indexTransaction(batch, addressIncrements, manifest, tx, height, i); err != nil {
			return fmt.Errorf("failed to index tx at height %d index %d: %w", height, i, err)
		}
	}
	if err := idb.flushAddressIncrements(batch, addressIncrements); err != nil {
		return err
	}
	if readModel != nil {
		manifest.ReadModelSchemaVersion = readModel.SchemaVersion
		manifest.ReadModelDigest = readModelDigestHex(readModel)
		if err := idb.applyReadModelMutations(batch, manifest, readModel); err != nil {
			return err
		}
		if err := idb.advanceReadModelCoverage(batch, readModel); err != nil {
			return err
		}
	}
	if block.BlockHash != "" {
		if err := batch.Set(KeyBlockHash(height), block.BlockHash); err != nil {
			return err
		}
		manifest.add(KeyBlockHash(height))
	}
	for address, delta := range addressIncrements {
		manifest.AddressDeltas[address] = delta
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := batch.Set(KeyHeightManifest(height), string(manifestRaw)); err != nil {
		return err
	}
	if err := batch.Set(KeySyncHeight, strconv.FormatUint(height, 10)); err != nil {
		return err
	}
	return batch.Commit()
}

func (idb *IndexDB) indexTransaction(batch *IndexBatch, addressIncrements map[string]int, manifest *heightManifest, tx *pb.AnyTx, height uint64, txIndex int) error {
	record := extractTxRecord(tx, height, txIndex)
	if record == nil || record.TxID == "" {
		return nil
	}
	if manifest.addTxID(record.TxID) {
		refKey := KeyTxDetailRef(record.TxID)
		refRaw, err := idb.Get(refKey)
		if err != nil {
			return err
		}
		refs := 0
		if refRaw != "" {
			refs, err = strconv.Atoi(refRaw)
			if err != nil || refs <= 0 {
				return fmt.Errorf("invalid transaction detail refcount for %s: %q", record.TxID, refRaw)
			}
		}
		if refs == 0 {
			                                                                   
			                                                                     
			                                                                     
			                                                           
			idb.mu.RLock()
			ndb := idb.nodeDB
			idb.mu.RUnlock()
			if ndb != nil {
				fbKey := fmt.Sprintf("v1_vmtx_fbbal_%s", record.TxID)
				if data, err := ndb.Get(fbKey); err == nil && data != nil && len(data) > 0 {
					record.FBBalanceAfter = string(data)
				}
			}
			data, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if err := batch.SetBytes(KeyTxDetail(record.TxID), data); err != nil {
				return err
			}
		} else {
			                                                                       
			                                                                         
			                                                                            
			detailRaw, err := idb.Get(KeyTxDetail(record.TxID))
			if err != nil {
				return err
			}
			if detailRaw == "" {
				return fmt.Errorf("transaction detail %s is missing for refcount %d", record.TxID, refs)
			}
		}
		if err := batch.Set(refKey, strconv.Itoa(refs+1)); err != nil {
			return err
		}
	}

	if record.FromAddress != "" {
		if err := idb.indexAddressTx(batch, addressIncrements, manifest, record.FromAddress, record.TxType, height, txIndex, record.TxID); err != nil {
			return err
		}
	}
	if record.ToAddress != "" && record.ToAddress != record.FromAddress {
		if err := idb.indexAddressTx(batch, addressIncrements, manifest, record.ToAddress, record.TxType, height, txIndex, record.TxID); err != nil {
			return err
		}
	}
	if err := batch.Set(KeyTxTypeTx(record.TxType, height, txIndex), record.TxID); err != nil {
		return err
	}
	manifest.add(KeyTxTypeTx(record.TxType, height, txIndex))
	if err := batch.Set(KeyBlockTx(height, txIndex), record.TxID); err != nil {
		return err
	}
	manifest.add(KeyBlockTx(height, txIndex))
	return nil
}

func (idb *IndexDB) indexAddressTx(batch *IndexBatch, addressIncrements map[string]int, manifest *heightManifest, address, txType string, height uint64, txIndex int, txID string) error {
	key := KeyAddressTx(address, height, txIndex)
	                                                                           
	                                                                          
	                                       
	if err := batch.Set(key, txID); err != nil {
		return err
	}
	manifest.add(key)
	if txType != "" {
		typeKey := KeyAddressTypeTx(address, txType, height, txIndex)
		if err := batch.Set(typeKey, txID); err != nil {
			return err
		}
		manifest.add(typeKey)
	}
	addressIncrements[address]++
	return nil
}

func (idb *IndexDB) flushAddressIncrements(batch *IndexBatch, increments map[string]int) error {
	addresses := make([]string, 0, len(increments))
	for address := range increments {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	for _, address := range addresses {
		delta := increments[address]
		key := KeyAddressCount(address)
		val, _ := idb.Get(key)
		count := 0
		if val != "" {
			count, _ = strconv.Atoi(val)
		}
		if err := batch.Set(key, strconv.Itoa(count+delta)); err != nil {
			return err
		}
	}
	return nil
}

func (idb *IndexDB) GetSyncHeight() (uint64, error) {
	val, err := idb.Get(KeySyncHeight)
	if err != nil {
		return 0, err
	}
	if val == "" {
		return 0, nil
	}
	return strconv.ParseUint(val, 10, 64)
}

func (idb *IndexDB) SetSyncHeight(height uint64) error {
	return idb.Set(KeySyncHeight, strconv.FormatUint(height, 10))
}

func (idb *IndexDB) GetBlockHash(height uint64) (string, error) {
	return idb.Get(KeyBlockHash(height))
}

func (idb *IndexDB) SetSyncNode(node string) error {
	return idb.Set(KeySyncNode, node)
}

func (idb *IndexDB) GetSyncNode() (string, error) {
	return idb.Get(KeySyncNode)
}

func (idb *IndexDB) GetAddressTxCount(address string) (int, error) {
	val, err := idb.Get(KeyAddressCount(address))
	if err != nil || val == "" {
		return 0, err
	}
	return strconv.Atoi(val)
}

func CollectBlockAddresses(block *pb.Block) map[string]struct{} {
	addressSet := make(map[string]struct{})
	if block == nil {
		return addressSet
	}
	for _, tx := range block.Body {
		if tx == nil {
			continue
		}
		if base := tx.GetBase(); base != nil && base.FromAddress != "" {
			addressSet[base.FromAddress] = struct{}{}
		}
		if to, _ := extractToAndValue(tx); to != "" {
			addressSet[to] = struct{}{}
		}
	}
	return addressSet
}

func (idb *IndexDB) SnapshotBalancesForHeight(addressSet map[string]struct{}, height uint64) {
	idb.mu.RLock()
	nodeDB := idb.nodeDB
	idb.mu.RUnlock()
	if nodeDB == nil || len(addressSet) == 0 {
		return
	}

	batch := idb.NewBatch()
	defer batch.Close()
	wrote := false
	addresses := make([]string, 0, len(addressSet))
	for addr := range addressSet {
		addresses = append(addresses, addr)
	}
	sort.Strings(addresses)
	for _, addr := range addresses {
		balJSON := idb.queryFBBalanceJSON(nodeDB, addr)
		if balJSON == "" {
			continue
		}
		if err := batch.Set(KeyBalanceSnapshot(addr, height), balJSON); err != nil {
			log.Printf("[indexdb] failed to save balance snapshot for %s at height %d: %v", addr, height, err)
			continue
		}
		wrote = true
	}
	if wrote {
		if err := batch.Commit(); err != nil {
			log.Printf("[indexdb] failed to commit balance snapshots at height %d: %v", height, err)
		}
	}
}

type BalanceSnapshot struct {
	Balance       string `json:"balance"`
	MinerLocked   string `json:"miner_locked,omitempty"`
	WitnessLocked string `json:"witness_locked,omitempty"`
	OrderFrozen   string `json:"order_frozen,omitempty"`
	LiquidLocked  string `json:"liquid_locked,omitempty"`
}

func (idb *IndexDB) queryFBBalanceJSON(nodeDB *db.Manager, addr string) string {
	data, err := nodeDB.Get(keys.KeyBalance(addr, "FB"))
	if err != nil || data == nil || len(data) == 0 {
		return ""
	}
	var record pb.TokenBalanceRecord
	if err := proto.Unmarshal(data, &record); err != nil || record.Balance == nil {
		return ""
	}
	snap := BalanceSnapshot{
		Balance:       record.Balance.Balance,
		MinerLocked:   record.Balance.MinerLockedBalance,
		WitnessLocked: record.Balance.WitnessLockedBalance,
		OrderFrozen:   record.Balance.OrderFrozenBalance,
		LiquidLocked:  record.Balance.LiquidLockedBalance,
	}
	jsonData, err := json.Marshal(snap)
	if err != nil {
		return ""
	}
	return string(jsonData)
}
