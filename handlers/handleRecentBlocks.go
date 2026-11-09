package handlers

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"dex/keys"
	"dex/logs"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type recentBlockJSON struct {
	Height            uint64 `json:"height"`
	BlockHash         string `json:"block_hash"`
	TxsHash           string `json:"txs_hash,omitempty"`
	Miner             string `json:"miner,omitempty"`
	TxCount           int32  `json:"tx_count"`
	AccumulatedReward string `json:"accumulated_reward,omitempty"`
	BlockReward       string `json:"block_reward,omitempty"`
	Timestamp         int64  `json:"timestamp,omitempty"`
	Window            int32  `json:"window,omitempty"`
	StateRoot         string `json:"state_root,omitempty"`
}

type recentBlocksJSONResponse struct {
	Blocks []recentBlockJSON `json:"blocks"`
	Error  string            `json:"error,omitempty"`
}

func (hm *HandlerManager) finalizedObservedAt(height uint64) int64 {
	if hm == nil || hm.consensusManager == nil {
		return 0
	}
	chits := hm.consensusManager.GetFinalizationChits(height)
	if chits == nil {
		return 0
	}
	return chits.ObservedAt()
}

func (hm *HandlerManager) HandleGetRecentBlocks(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetRecentBlocks")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req pb.GetRecentBlocksRequest
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var jsonReq struct {
			Count int32 `json:"count"`
		}
		if len(strings.TrimSpace(string(bodyBytes))) > 0 {
			if err := json.Unmarshal(bodyBytes, &jsonReq); err != nil {
				http.Error(w, "Invalid GetRecentBlocksRequest json", http.StatusBadRequest)
				return
			}
		}
		req.Count = jsonReq.Count
	} else {
		if err := pb.UnmarshalSanitize(bodyBytes, &req); err != nil {
			http.Error(w, "Invalid GetRecentBlocksRequest proto", http.StatusBadRequest)
			return
		}
	}

	count := int(req.Count)
	if count <= 0 {
		count = 10
	}
	if count > 100 {
		count = 100
	}

	_, lastHeight := hm.consensusManager.GetLastAccepted()

	var summaries []*pb.BlockSummary
	jsonBlocks := make([]recentBlockJSON, 0, count)

	for i := 0; i < count; i++ {
		if lastHeight == 0 {
			break
		}
		var block *pb.Block
		finalizedAttempted := false
		if hm.consensusManager != nil {
			if blockID, ok := hm.consensusManager.GetFinalizedBlockID(lastHeight); ok {
				finalizedAttempted = true
				b, err := hm.dbManager.GetBlockByID(blockID)
				if err == nil && b != nil {
					block = b
				} else {
					logs.Warn("[HandleGetRecentBlocks] Finalized block %s at height %d not found in DB: %v",
						blockID, lastHeight, err)
				}
			}
		}

		if block == nil && !finalizedAttempted {
			if blocksAtHeight, err := hm.dbManager.GetBlocksByHeight(lastHeight); err == nil && len(blocksAtHeight) > 1 {
				logs.Warn("[HandleGetRecentBlocks] Multiple blocks at height %d (count=%d); returning first",
					lastHeight, len(blocksAtHeight))
			}
			b, err := hm.dbManager.GetBlock(lastHeight)
			if err == nil && b != nil {
				block = b
			}
		}

		if block == nil {
			                                                              
			                                                      
			if lastHeight == 1 {
				break
			}
			lastHeight--
			continue
		}

		blockReward := hm.blockRewardAmount(block.Header.Height, block.AccumulatedReward)
		observedAt := hm.finalizedObservedAt(block.Header.Height)

		                      
		summary := &pb.BlockSummary{
			Height:            block.Header.Height,
			BlockHash:         block.BlockHash,
			TxsHash:           block.Header.TxsHash,
			Miner:             keys.MinerAddress(block.Header.Miner),
			TxCount:           int32(len(block.Body)),
			AccumulatedReward: blockReward,
			Window:            block.Header.Window,
			StateRoot:         fmt.Sprintf("%x", block.Header.StateRoot),
		}
		summaries = append(summaries, summary)
		jsonBlocks = append(jsonBlocks, recentBlockJSON{
			Height:            summary.Height,
			BlockHash:         summary.BlockHash,
			TxsHash:           summary.TxsHash,
			Miner:             summary.Miner,
			TxCount:           summary.TxCount,
			AccumulatedReward: blockReward,
			BlockReward:       blockReward,
			Timestamp:         observedAt,
			Window:            summary.Window,
			StateRoot:         summary.StateRoot,
		})
		lastHeight--
	}

	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(recentBlocksJSONResponse{Blocks: jsonBlocks})
		return
	}

	resp := &pb.GetRecentBlocksResponse{
		Blocks: summaries,
	}

	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)

	gz := gzip.NewWriter(w)
	defer gz.Close()
	gz.Write(respBytes)
}
