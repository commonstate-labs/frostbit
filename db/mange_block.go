package db

import (
	"dex/config"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"strconv"
)

                                                    
  
                                                                     
                                                                         
                                                                     
  
                                                                
func (mgr *Manager) SaveBlock(block *pb.Block) error {
	mgr.Logger.Debug("Saving new block_%d with ID %s", block.Header.Height, block.BlockHash)

	                                                   
	blockKey := keys.KeyBlockData(block.BlockHash)
	data, err := ProtoMarshal(block)
	if err != nil {
		return err
	}
	mgr.EnqueueSet(blockKey, string(data))

	                                            
	heightKey := keys.KeyHeightBlocks(block.Header.Height)
	existingIDs, _ := mgr.Read(heightKey)

	var blockIDs []string
	if existingIDs != "" {
		                          
		json.Unmarshal([]byte(existingIDs), &blockIDs)
	}

	                                  
	found := false
	for _, id := range blockIDs {
		if id == block.BlockHash {
			found = true
			break
		}
	}
	if !found {
		blockIDs = append(blockIDs, block.BlockHash)
	}

	idsData, _ := json.Marshal(blockIDs)
	mgr.EnqueueSet(heightKey, string(idsData))

	                       
	idKey := keys.KeyBlockIDToHeight(block.BlockHash)
	mgr.EnqueueSet(idKey, strconv.FormatUint(block.Header.Height, 10))

	                              
	mgr.EnqueueSet(keys.KeyLatestHeight(), strconv.FormatUint(block.Header.Height, 10))

	                        
	                        
	mgr.cachedBlocksMu.Lock()
	cacheFound := false
	for i, v := range mgr.cachedBlocks {
		if v != nil && v.BlockHash == block.BlockHash {
			mgr.cachedBlocks[i] = block
			cacheFound = true
			break
		}
	}
	if !cacheFound {
		cfg := mgr.cfg
		if cfg == nil {
			cfg = config.DefaultConfig()
		}
		if len(mgr.cachedBlocks) >= cfg.Database.BlockCacheSize {
			mgr.cachedBlocks = mgr.cachedBlocks[1:]
		}
		mgr.cachedBlocks = append(mgr.cachedBlocks, block)
	}
	mgr.cachedBlocksMu.Unlock()

	return nil
}

                                                                           
func (mgr *Manager) GetBlock(height uint64) (*pb.Block, error) {
	                                                               
	blocks, err := mgr.GetBlocksByHeight(height)
	if err != nil || len(blocks) == 0 {
		return nil, fmt.Errorf("no blocks at height %d", height)
	}
	return blocks[0], nil
}

func (mgr *Manager) GetBlocksByHeight(height uint64) ([]*pb.Block, error) {
	                                         
	heightKey := keys.KeyHeightBlocks(height)
	idsStr, err := mgr.Read(heightKey)
	if err != nil {
		return nil, err
	}

	var blockIDs []string
	if err := json.Unmarshal([]byte(idsStr), &blockIDs); err != nil {
		return nil, err
	}

	                        
	var blocks []*pb.Block
	for _, id := range blockIDs {
		block, err := mgr.GetBlockByID(id)
		if err == nil && block != nil {
			blocks = append(blocks, block)
		}
	}

	return blocks, nil
}

                                                         
func (mgr *Manager) GetBlockByID(blockID string) (*pb.Block, error) {
	                     
	mgr.cachedBlocksMu.RLock()
	for _, b := range mgr.cachedBlocks {
		if b != nil && b.BlockHash == blockID {
			mgr.cachedBlocksMu.RUnlock()
			return b, nil
		}
	}
	mgr.cachedBlocksMu.RUnlock()

	                                          
	blockKey := keys.KeyBlockData(blockID)
	val, err := mgr.Read(blockKey)
	if err != nil {
		return nil, fmt.Errorf("block %s not found", blockID)
	}

	block := &pb.Block{}

	if err := ProtoUnmarshal([]byte(val), block); err != nil {
		return nil, err
	}

	return block, nil
}

                                                        
func (mgr *Manager) getBlockByIDFallback(blockID string) (*pb.Block, error) {
	                     
	latestHeight, err := mgr.GetLatestBlockHeight()
	if err != nil {
		return nil, fmt.Errorf("failed to get latest height: %v", err)
	}

	                                                                                       
	for h := latestHeight; h > 0; h-- {
		block, err := mgr.GetBlock(h)
		if err != nil {
			continue                            
		}
		if block.BlockHash == blockID {
			                                                            
			idKey := keys.KeyBlockIDToHeight(blockID)
			mgr.EnqueueSet(idKey, strconv.FormatUint(h, 10))
			return block, nil
		}
	}

	                     
	genesis, err := mgr.GetBlock(0)
	if err == nil && genesis.BlockHash == blockID {
		return genesis, nil
	}

	return nil, fmt.Errorf("block with ID %s not found", blockID)
}

                                                                                         
func (mgr *Manager) GetLatestBlockHeight() (uint64, error) {
	latestKey := keys.KeyLatestHeight()
	val, err := mgr.Read(latestKey)
	if err != nil {
		return 0, err
	}
	height, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		return 0, err
	}
	return height, nil
}

                                                                                        
func (mgr *Manager) GetCurrentHeight() uint64 {
	height, err := mgr.GetLatestBlockHeight()
	if err != nil {
		return 0
	}
	return height
}

                                             
func (mgr *Manager) GetBlocksByRange(fromHeight, toHeight uint64) ([]*pb.Block, error) {
	if fromHeight > toHeight {
		return nil, fmt.Errorf("invalid range: from %d to %d", fromHeight, toHeight)
	}

	blocks := make([]*pb.Block, 0, toHeight-fromHeight+1)
	for h := fromHeight; h <= toHeight; h++ {
		block, err := mgr.GetBlock(h)
		if err != nil {
			                           
			mgr.Logger.Debug("[GetBlocksByRange] Skip height %d: %v", h, err)
			continue
		}
		blocks = append(blocks, block)
	}

	return blocks, nil
}

                                                  
func (mgr *Manager) BlockExists(blockID string) bool {
	                     
	mgr.cachedBlocksMu.RLock()
	for _, b := range mgr.cachedBlocks {
		if b != nil && b.BlockHash == blockID {
			mgr.cachedBlocksMu.RUnlock()
			return true
		}
	}
	mgr.cachedBlocksMu.RUnlock()

	                                   
	idKey := keys.KeyBlockIDToHeight(blockID)
	_, err := mgr.Read(idKey)
	return err == nil
}
