package handlers

import (
	"dex/consensus"
	"dex/types"
	"io"
	"net/http"

	"github.com/cespare/xxhash/v2"
)

type gossipPayloadCacheKey struct {
	size int
	hash uint64
}

                                                                          
func (hm *HandlerManager) HandleBlockGossip(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleBlockGossip0")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	_ = hm.handleBlockGossipBytes(w, bodyBytes, "")
}

                                                                          
                                                                               
                                                                         
func (hm *HandlerManager) handleBlockGossipBytes(w http.ResponseWriter, bodyBytes []byte, relayFrom types.NodeID) bool {

	_, blockBytes, err := types.PeekGossipPayload(bodyBytes)
	if err != nil {
		http.Error(w, "Failed to parse GossipPayload", http.StatusBadRequest)
		hm.Logger.Debug("[HandleBlockGossip] peek payload failed: %v", err)
		return false
	}
	payloadKey := gossipPayloadCacheKey{size: len(blockBytes), hash: xxhash.Sum64(blockBytes)}
	if hm.seenGossipPayloadCache != nil {
		if cached, ok := hm.seenGossipPayloadCache.Get(payloadKey); ok {
			accepted, _ := cached.(bool)
			if !accepted {
				http.Error(w, "previously rejected gossip payload", http.StatusBadRequest)
				return false
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return true
		}
	}

	payload, err := types.DecodeGossipPayload(bodyBytes)
	if err != nil {
		http.Error(w, "Failed to parse GossipPayload", http.StatusBadRequest)
		hm.Logger.Debug("[HandleBlockGossip] decode payload failed: %v", err)
		return false
	}
	if payload.Block == nil || payload.Block.Header == nil {
		http.Error(w, "Invalid block", http.StatusBadRequest)
		return false
	}
	if hm.consensusManager != nil {
		_, acceptedHeight := hm.consensusManager.GetLastAccepted()
		consensus.SetBlockCacheAcceptedHeight(acceptedHeight)
		if consensus.IsTooFarFutureHeight(payload.Block.Header.Height, acceptedHeight) {
			if hm.seenGossipPayloadCache != nil {
				hm.seenGossipPayloadCache.Add(payloadKey, false)
			}
			hm.Logger.Warn("[HandleBlockGossip] reject far future block %s height=%d accepted=%d window=%d",
				payload.Block.BlockHash, payload.Block.Header.Height, acceptedHeight, consensus.MaxFutureBlockCacheWindow)
			http.Error(w, "block too far ahead", http.StatusBadRequest)
			return false
		}
	}

	if hm.adapter == nil {
		http.Error(w, "adapter unavailable", http.StatusServiceUnavailable)
		return false
	}
	consBlock, err := hm.adapter.DBBlockToConsensusWithPayload(payload.Block)
	if err != nil {
		http.Error(w, "Failed to convert block", http.StatusBadRequest)
		return false
	}
	if hm.seenBlocksCache != nil && consBlock.ID != "" && hm.seenBlocksCache.Contains(consBlock.ID) {
		hm.Logger.Debug("[HandleBlockGossip] Block %s already seen, OK", consBlock.ID)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
		return true
	}

	hm.Stats.RecordAPICall("HandleBlockGossip1")

	msg := types.Message{
		RequestID: payload.RequestID,
		Type:      types.MsgGossip,
		From:      types.NodeID(consBlock.Header.Proposer),
		RelayFrom: relayFrom,
		Block:     consBlock,
		BlockID:   consBlock.ID,
		Height:    consBlock.Header.Height,
		ShortTxs:  payload.Block.ShortTxs,
	}

	blockAccepted := false
	if hm.consensusManager != nil {
		if err := hm.consensusManager.AddBlock(payload.Block); err != nil {
			hm.Logger.Debug("[HandleBlockGossip] AddBlock warn: %v", err)
		} else {
			blockAccepted = true
			if hm.seenGossipPayloadCache != nil {
				hm.seenGossipPayloadCache.Add(payloadKey, true)
			}
			if hm.seenBlocksCache != nil && consBlock.ID != "" {
				hm.seenBlocksCache.Add(consBlock.ID, true)
			}
			if rt, ok := hm.consensusManager.Transport.(*consensus.RealTransport); ok {
				if err := rt.EnqueueReceivedMessage(msg); err != nil {
					hm.Logger.Warn("[Handler] Failed to enqueue chits message: %v", err)
					http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
					return false
				}
			}
		}
	}

	canonicalBlock, canonical := consensus.GetSealedCachedBlock(payload.Block.BlockHash)
	if blockAccepted && canonical && len(canonicalBlock.Body) > 0 {
		for _, tx := range canonicalBlock.Body {
			if shouldImportBlockTxToPending(tx) {
				pendingTx := cloneBlockTxForPending(tx)
				if pendingTx == nil {
					continue
				}
				if err := hm.txPool.StoreAnyTx(pendingTx); err != nil {
					hm.Logger.Debug("[HandleBlockGossip] Failed to store tx %s: %v", tx.GetTxId(), err)
				}
			}
		}
	}

	hm.Logger.Debug("[HandleBlockGossip] Block %s at height %d proposer=%s",
		consBlock.ID, consBlock.Header.Height, consBlock.Header.Proposer)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
	return blockAccepted
}
