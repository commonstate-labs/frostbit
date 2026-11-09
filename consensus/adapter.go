package consensus

import (
	"dex/config"
	"dex/db"
	"dex/keys"
	"dex/pb"
	"dex/txpool"
	"dex/types"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

                                                      
type ConsensusAdapter struct {
	dbManager       *db.Manager
	txPool          *txpool.TxPool                                                 
	contactedNodes  map[string]bool                            
	contactedHeight uint64                                        
}

func NewConsensusAdapter(dbMgr *db.Manager) *ConsensusAdapter {
	return &ConsensusAdapter{
		dbManager:      dbMgr,
		contactedNodes: make(map[string]bool),
	}
}

                                                                                          
func NewConsensusAdapterWithTxPool(dbMgr *db.Manager, pool *txpool.TxPool) *ConsensusAdapter {
	return &ConsensusAdapter{
		dbManager:      dbMgr,
		txPool:         pool,
		contactedNodes: make(map[string]bool),
	}
}

                                                  
func (a *ConsensusAdapter) SetTxPool(pool *txpool.TxPool) {
	a.txPool = pool
}

                                            
func (a *ConsensusAdapter) RecordContact(nodeID string, height uint64) {
	                                    
	if height != a.contactedHeight {
		a.contactedNodes = make(map[string]bool)
		a.contactedHeight = height
	}
	a.contactedNodes[nodeID] = true
}

                                                   
func (a *ConsensusAdapter) GetContactedNodes() []string {
	nodes := make([]string, 0, len(a.contactedNodes))
	for node := range a.contactedNodes {
		nodes = append(nodes, node)
	}
	return nodes
}

                                               
                                    
                                               

                                                          
func (a *ConsensusAdapter) DBBlockToConsensus(dbBlock *pb.Block) (*types.Block, error) {
	return a.dbBlockToConsensus(dbBlock, false)
}

                                                                             
                                                                                
                                                                         
                                        
func (a *ConsensusAdapter) DBBlockToConsensusWithPayload(dbBlock *pb.Block) (*types.Block, error) {
	return a.dbBlockToConsensus(dbBlock, true)
}

func (a *ConsensusAdapter) dbBlockToConsensus(dbBlock *pb.Block, attachPayload bool) (*types.Block, error) {
	if dbBlock == nil {
		return nil, fmt.Errorf("nil db block")
	}

	                                        
	header := dbBlock.Header
	if header == nil {
		                                                     
		return nil, fmt.Errorf("block header is nil, old format blocks are no longer supported")
	}
	var canonicalPayload []byte
	if attachPayload {
		var err error
		canonicalPayload, err = proto.Marshal(dbBlock)
		if err != nil {
			return nil, fmt.Errorf("marshal canonical block payload: %w", err)
		}
	}

	                                                                                              
	                                                                                                                
	proposer := header.Miner
	if prefix := keys.KeyNode(); strings.HasPrefix(proposer, prefix) {
		proposer = strings.TrimPrefix(proposer, prefix)
	}

	return &types.Block{
		ID:               dbBlock.BlockHash,
		MinerSignature:   append([]byte(nil), dbBlock.MinerSignature...),
		CanonicalPayload: canonicalPayload,
		Header: types.BlockHeader{
			Height:         header.Height,
			ParentID:       header.PrevBlockHash,
			Timestamp:      header.Timestamp,
			StateRoot:      string(header.StateRoot),
			TxHash:         header.TxsHash,
			Proposer:       proposer,
			Miner:          header.Miner,
			Window:         int(header.Window),
			VRFProof:       header.VrfProof,
			VRFOutput:      header.VrfOutput,
			PublicKey:      header.PublicKey,
			OfflineReports: append([]uint32(nil), header.OfflineReports...),
			SampledReports: append([]uint32(nil), header.SampledReports...),
			OnlineReports:  append([]uint32(nil), header.OnlineReports...),
		},
		                                                                     
	}, nil
}

                                                             
func (a *ConsensusAdapter) ConsensusBlockToDB(block *types.Block, txs []*pb.AnyTx) *pb.Block {
	if block == nil {
		return nil
	}

	useProvidedTxs := txs != nil
	body := txs
	var shortTxs []byte
	minerSignature := append([]byte(nil), block.MinerSignature...)
	                                                                
	                                                                                           
	                                                                                            
	                                                                                
	                                                                                           
	offlineReports := append([]uint32(nil), block.Header.OfflineReports...)
	sampledReports := append([]uint32(nil), block.Header.SampledReports...)
	onlineReports := append([]uint32(nil), block.Header.OnlineReports...)

	                                                                           
	                                                                         
	                                                                             
	                              
	cachedBlock, cached := GetSealedCachedBlock(block.ID)
	if !cached && len(block.CanonicalPayload) == 0 {
		cachedBlock, cached = GetCachedBlock(block.ID)
	}
	if cached && cachedBlock != nil {
		if !useProvidedTxs {
			body = cachedBlock.Body
		}
		shortTxs = append([]byte(nil), cachedBlock.ShortTxs...)
		minerSignature = append([]byte(nil), cachedBlock.MinerSignature...)
		if cachedBlock.Header != nil {
			if len(offlineReports) == 0 && len(cachedBlock.Header.OfflineReports) > 0 {
				offlineReports = append([]uint32(nil), cachedBlock.Header.OfflineReports...)
			}
			if len(sampledReports) == 0 && len(cachedBlock.Header.SampledReports) > 0 {
				sampledReports = append([]uint32(nil), cachedBlock.Header.SampledReports...)
			}
			if len(onlineReports) == 0 && len(cachedBlock.Header.OnlineReports) > 0 {
				onlineReports = append([]uint32(nil), cachedBlock.Header.OnlineReports...)
			}
		}
	}
	                                                                          
	                                                                           
	                                                               
	if !cached && !useProvidedTxs && len(block.CanonicalPayload) > 0 {
		var attached pb.Block
		if err := pb.UnmarshalSanitize(block.CanonicalPayload, &attached); err == nil && attached.BlockHash == block.ID {
			body = attached.Body
			if len(shortTxs) == 0 {
				shortTxs = append([]byte(nil), attached.ShortTxs...)
			}
			if len(minerSignature) == 0 {
				minerSignature = append([]byte(nil), attached.MinerSignature...)
			}
		}
	}

	if len(shortTxs) == 0 && len(body) > 0 && a.txPool != nil {
		shortTxs = a.txPool.ConcatFirst8Bytes(body)
	}
	miner := block.Header.Miner
	if miner == "" {
		miner = block.Header.Proposer
	}

	return &pb.Block{
		BlockHash: block.ID,
		Header: &pb.BlockHeader{
			Height:         block.Header.Height,
			PrevBlockHash:  block.Header.ParentID,
			Timestamp:      block.Header.Timestamp,
			StateRoot:      []byte(block.Header.StateRoot),
			TxsHash:        block.Header.TxHash,
			Miner:          miner,
			Window:         int32(block.Header.Window),
			VrfProof:       block.Header.VRFProof,
			VrfOutput:      block.Header.VRFOutput,
			PublicKey:      block.Header.PublicKey,
			OfflineReports: offlineReports,
			SampledReports: sampledReports,
			OnlineReports:  onlineReports,
		},
		Body:           body,
		ShortTxs:       shortTxs,
		MinerSignature: minerSignature,
	}
}

                                               
                 
                                               

                                    
func (a *ConsensusAdapter) ConsensusMessageToPushQuery(msg types.Message, address string) (*pb.PushQuery, error) {
	container, isBlock, err := a.prepareContainer(msg)
	if err != nil {
		return nil, err
	}

	return &pb.PushQuery{
		Address:          address,
		ContainerIsBlock: isBlock,
		Container:        container,
		RequestedHeight:  msg.Height,
		BlockId:          msg.BlockID,
		RequestId:        msg.RequestID,
		                                                 
		VrfSeed: msg.VRFSeed,
		SeqId:   msg.SeqID,
		Nonce:   append([]byte(nil), msg.QueryNonce...),
	}, nil
}

                                                                
func (a *ConsensusAdapter) PushQueryToConsensusMessage(pq *pb.PushQuery, from types.NodeID) (types.Message, error) {
	msg := types.Message{
		Type:      types.MsgPushQuery,
		From:      from,
		BlockID:   pq.BlockId,
		Height:    pq.RequestedHeight,
		RequestID: pq.RequestId,
		                                                                      
		VRFSeed:    pq.VrfSeed,
		SeqID:      pq.SeqId,
		QueryNonce: append([]byte(nil), pq.Nonce...),
	}

	if pq.ContainerIsBlock {
		var block pb.Block
		if err := pb.UnmarshalSanitize(pq.Container, &block); err != nil {
			return msg, err
		}
		consensusBlock, err := a.DBBlockToConsensusWithPayload(&block)
		if err != nil {
			return msg, err
		}
		msg.Block = consensusBlock
		                                                 
		msg.ShortTxs = block.ShortTxs
	}

	return msg, nil
}

                            
func (a *ConsensusAdapter) ChitsToConsensusMessage(chits *pb.Chits, from types.NodeID) types.Message {
	return types.Message{
		Type:              types.MsgChits,
		From:              from,
		RequestID:         chits.RequestId,
		PreferredID:       chits.PreferredBlock,
		PreferredIDHeight: chits.PreferredBlockAtHeight,
		AcceptedID:        chits.AcceptedBlock,
		AcceptedHeight:    chits.AcceptedHeight,
		                                                                      
		ChitSignature: chits.ChitSignature,
		QueryNonce:    append([]byte(nil), chits.QueryNonce...),
	}
}

                                  
func (a *ConsensusAdapter) ConsensusMessageToChits(msg types.Message) *pb.Chits {
	return &pb.Chits{
		RequestId:              msg.RequestID,
		PreferredBlock:         msg.PreferredID,
		AcceptedBlock:          msg.AcceptedID,
		PreferredBlockAtHeight: msg.PreferredIDHeight,
		AcceptedHeight:         msg.AcceptedHeight,
		Bitmap:                 a.generateBitmap(),
		Address:                string(msg.From),                          
		ChitSignature:          msg.ChitSignature,                                                  
		QueryNonce:             append([]byte(nil), msg.QueryNonce...),
	}
}

                                               
                     
                                               

                                                 
func (a *ConsensusAdapter) PrepareBlockContainer(blockID string, height uint64) ([]byte, bool, error) {
	                     
	cfg := config.DefaultConfig()
	if cachedBlock, exists := GetCachedBlock(blockID); exists {
		if len(cachedBlock.Body) < cfg.TxPool.MaxTxsPerBlock {
			data, err := proto.Marshal(cachedBlock)
			return data, true, err
		}
		return cachedBlock.ShortTxs, false, nil
	}

	                     
	block, err := a.dbManager.GetBlock(height)
	if err != nil {
		return nil, false, err
	}

	if block.BlockHash != blockID {
		return nil, false, fmt.Errorf("block ID mismatch")
	}

	if len(block.Body) < cfg.TxPool.MaxTxsPerBlock {
		data, err := proto.Marshal(block)
		return data, true, err
	}

	return block.ShortTxs, false, nil
}

                                                          
func (a *ConsensusAdapter) ProcessReceivedContainer(container []byte, isBlock bool, height uint64, blockID string) (*pb.Block, error) {
	if isBlock {
		var block pb.Block
		if err := proto.Unmarshal(container, &block); err != nil {
			return nil, err
		}
		return &block, nil
	}

	                        
	txs, err := a.resolveShorHashesToTxs(container)
	if err != nil {
		return nil, err
	}

	return &pb.Block{
		BlockHash: blockID,
		Header: &pb.BlockHeader{
			Height: height,
		},
		Body:     txs,
		ShortTxs: container,
	}, nil
}

                                               
               
                                               

func (a *ConsensusAdapter) parseMinerToNodeID(miner string) types.NodeID {
	var nodeID int
	fmt.Sscanf(miner, keys.KeyNode()+"%d", &nodeID)
	return types.NodeID(strconv.Itoa(nodeID))
}

func (a *ConsensusAdapter) parseWindowFromBlockHash(blockHash string) int {
	                                                          
	var height, node, window int
	fmt.Sscanf(blockHash, "block-%d-%d-w%d", &height, &node, &window)
	return window
}

func (a *ConsensusAdapter) extractTxsHashFromData(data string) string {
	                                
	var txCount int
	var txsHash string
	fmt.Sscanf(data, "TxCount: %d, TxsHash: %s", &txCount, &txsHash)
	return txsHash
}

func (a *ConsensusAdapter) generateBitmap() []byte {
	                                             
	                                                                                      
	bitmap := make([]byte, 128)

	                                          
	                                                
	for nodeID := range a.contactedNodes {
		                                    
		hash := 0
		for _, c := range nodeID {
			hash = (hash*31 + int(c)) & 0x3FF                          
		}
		byteIdx := hash / 8
		bitIdx := uint(hash % 8)
		if byteIdx < len(bitmap) {
			bitmap[byteIdx] |= 1 << bitIdx
		}
	}

	return bitmap
}

func (a *ConsensusAdapter) prepareContainer(msg types.Message) ([]byte, bool, error) {
	                              
	if msg.Block != nil {
		dbBlock := a.ConsensusBlockToDB(msg.Block, nil)
		if dbBlock == nil {
			return nil, false, fmt.Errorf("prepare pushquery container failed: nil db block for %s", msg.Block.ID)
		}
		if dbBlock.Header == nil {
			return nil, false, fmt.Errorf("prepare pushquery container failed: nil header for %s", msg.Block.ID)
		}
		data, err := proto.Marshal(dbBlock)
		if err != nil {
			return nil, false, fmt.Errorf("prepare pushquery container marshal failed for %s: %w", msg.Block.ID, err)
		}
		return data, true, nil
	}
	return nil, false, nil
}

func (a *ConsensusAdapter) resolveShorHashesToTxs(shortHashes []byte) ([]*pb.AnyTx, error) {
	txs, missing, err := a.ResolveShorHashesToTxsWithMissing(shortHashes)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return txs, fmt.Errorf("missing transactions: expected %d, got %d (missing %d)",
			len(shortHashes)/8, len(txs), len(missing))
	}
	return txs, nil
}

                                                                                                       
func (a *ConsensusAdapter) ResolveShorHashesToTxsWithMissing(shortHashes []byte) (
	txs []*pb.AnyTx,
	missingHashes [][]byte,
	err error,
) {
	                                
	if a.txPool == nil {
		return nil, nil, fmt.Errorf("TxPool not set, cannot resolve short hashes")
	}

	                                                      
	const shortHashSize = 8
	if len(shortHashes)%shortHashSize != 0 {
		return nil, nil, fmt.Errorf("invalid shortHashes length: %d (must be multiple of %d)", len(shortHashes), shortHashSize)
	}

	hashCount := len(shortHashes) / shortHashSize
	allHashes := make([][]byte, 0, hashCount)
	for i := 0; i+shortHashSize <= len(shortHashes); i += shortHashSize {
		hash := make([]byte, shortHashSize)
		copy(hash, shortHashes[i:i+shortHashSize])
		allHashes = append(allHashes, hash)
	}

	                       
	txs = a.txPool.GetTxsByShortHashes(allHashes, true)

	                                                
	if len(txs) != len(allHashes) {
		                                          
		foundSet := make(map[string]bool)
		for _, tx := range txs {
			txID := tx.GetTxId()
			if len(txID) >= 18 {
				foundSet[txID[2:18]] = true                           
			}
		}

		                           
		for _, hash := range allHashes {
			shortHex := fmt.Sprintf("%x", hash)
			if !foundSet[shortHex] {
				missingHashes = append(missingHashes, hash)
			}
		}
	}

	return txs, missingHashes, nil
}
