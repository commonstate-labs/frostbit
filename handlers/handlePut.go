package handlers

import (
	"dex/consensus"
	"dex/pb"
	"dex/types"
	"io"
	"net/http"
	"strings"
)

                                                                 
func (hm *HandlerManager) HandlePut(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandlePut")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var block pb.Block
	if err := pb.UnmarshalSanitize(bodyBytes, &block); err != nil {
		http.Error(w, "Failed to parse protobuf db.Block", http.StatusBadRequest)
		return
	}

	if block.BlockHash == "" || block.BlockHash == "genesis" || block.Header == nil {
		http.Error(w, "Invalid block hash", http.StatusBadRequest)
		return
	}

	if hm.consensusManager == nil || hm.adapter == nil {
		http.Error(w, "Consensus unavailable", http.StatusServiceUnavailable)
		return
	}
	_, acceptedHeight := hm.consensusManager.GetLastAccepted()
	consensus.SetBlockCacheAcceptedHeight(acceptedHeight)
	if consensus.IsTooFarFutureHeight(block.Header.Height, acceptedHeight) {
		hm.Logger.Warn("[HandlePut] reject far future block %s height=%d accepted=%d window=%d",
			block.BlockHash, block.Header.Height, acceptedHeight, consensus.MaxFutureBlockCacheWindow)
		http.Error(w, "block too far ahead", http.StatusBadRequest)
		return
	}
	consensusBlock, err := hm.adapter.DBBlockToConsensusWithPayload(&block)
	if err != nil {
		http.Error(w, "Failed to convert block", http.StatusBadRequest)
		return
	}

	from := types.NodeID(consensusBlock.Header.Proposer)
	if from == "" && block.Header != nil {
		from = types.NodeID(block.Header.Miner)
	}

	msg := types.Message{
		RequestID: 0,
		Type:      types.MsgPut,
		From:      from,
		Block:     consensusBlock,
		BlockID:   block.BlockHash,
		Height:    block.Header.Height,
		ShortTxs:  block.ShortTxs,
	}

	rt, ok := hm.consensusManager.Transport.(*consensus.RealTransport)
	if !ok {
		http.Error(w, "Consensus transport unavailable", http.StatusServiceUnavailable)
		return
	}
	                                                                         
	                                                                           
	                                                                             
	                                                                           
	blockAccepted := false
	if addErr := hm.consensusManager.AddBlock(&block); addErr != nil {
		deferToResolver := strings.Contains(addErr.Error(), "block data incomplete") ||
			(strings.Contains(addErr.Error(), "parent block") && strings.Contains(addErr.Error(), "not found"))
		if !deferToResolver {
			hm.Logger.Debug("[HandlePut] reject block %s: %v", block.BlockHash, addErr)
			http.Error(w, "Invalid block", http.StatusBadRequest)
			return
		}
	} else {
		blockAccepted = true
	}
	if err := rt.EnqueueReceivedMessage(msg); err != nil {
		consensus.RemoveCachedBlock(block.BlockHash)
		hm.Logger.Warn("[HandlePut] Failed to enqueue block message: %v", err)
		http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}

	canonicalBlock, canonical := consensus.GetSealedCachedBlock(block.BlockHash)
	if blockAccepted && canonical && len(canonicalBlock.Body) > 0 {
		imported := 0
		for _, tx := range canonicalBlock.Body {
			if shouldImportBlockTxToPending(tx) {
				pendingTx := cloneBlockTxForPending(tx)
				if pendingTx == nil {
					continue
				}
				if err := hm.txPool.StoreAnyTx(pendingTx); err != nil {
					hm.Logger.Debug("[HandlePut] Failed to store tx %s: %v", tx.GetTxId(), err)
				} else {
					imported++
				}
			}
		}
		hm.Logger.Debug("[HandlePut] Added %d/%d canonical transactions from block %s to pool", imported, len(canonicalBlock.Body), block.BlockHash)
	}

	hm.Logger.Info("[HandlePut] Accepted block payload %s at height %d (txs=%d)", block.BlockHash, block.Header.Height, len(block.Body))
	w.WriteHeader(http.StatusOK)
}
